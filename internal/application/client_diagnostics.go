package application

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"deciscope-core-api/internal/domain"
)

// ClientDiagnosticsSink は受理済み診断イベントの出力先。標準ログ出力と
// sessionId単位のJSONLファイルがそれぞれ実装する。書き込み失敗は
// ClientDiagnosticsService が握りつぶさずログに出すが、リクエストは失敗させない
// (診断機能の失敗が会議画面へ波及しないことを優先する)。
type ClientDiagnosticsSink interface {
	WriteClientDiagnosticEvent(event domain.ClientDiagnosticEvent) error
}

// ClientDiagnosticsSinkErrorReporter は sink 書き込み失敗の通知先。
type ClientDiagnosticsSinkErrorReporter func(sink string, err error)

var ErrClientDiagnosticsBatchEmpty = errors.New("client diagnostics batch has no events")

// ClientDiagnosticsLimits はログ量とペイロードサイズの制限値。
type ClientDiagnosticsLimits struct {
	// MaxEventsPerRequest は1リクエストで受け付けるイベント数の上限。
	MaxEventsPerRequest int
	// MaxDetailsBytes は details をJSON化したときの上限バイト数。
	MaxDetailsBytes int
	// MaxStringBytes は1つの文字列値の上限バイト数。
	MaxStringBytes int
	// MaxIdentifierBytes は sessionId / tabId などの識別子の上限バイト数。
	MaxIdentifierBytes int
	// MaxRouteBytes は route の上限バイト数。
	MaxRouteBytes int
	// MaxDetailsDepth は details のネスト深さの上限。
	MaxDetailsDepth int
	// MaxDetailsKeys は details の1オブジェクトあたりのキー数上限。
	MaxDetailsKeys int
	// MaxDetailsArrayItems は details の1配列あたりの要素数上限。
	MaxDetailsArrayItems int
	// ThrottleWindow は同一内容の高頻度イベントを抑制する時間窓。
	ThrottleWindow time.Duration
	// ThrottleEntries は抑制判定テーブルの最大保持数。
	ThrottleEntries int
}

func DefaultClientDiagnosticsLimits() ClientDiagnosticsLimits {
	return ClientDiagnosticsLimits{
		MaxEventsPerRequest:  100,
		MaxDetailsBytes:      32 * 1024,
		MaxStringBytes:       4096,
		MaxIdentifierBytes:   128,
		MaxRouteBytes:        512,
		MaxDetailsDepth:      6,
		MaxDetailsKeys:       80,
		MaxDetailsArrayItems: 200,
		ThrottleWindow:       time.Second,
		ThrottleEntries:      4096,
	}
}

func (l ClientDiagnosticsLimits) withDefaults() ClientDiagnosticsLimits {
	defaults := DefaultClientDiagnosticsLimits()
	if l.MaxEventsPerRequest <= 0 {
		l.MaxEventsPerRequest = defaults.MaxEventsPerRequest
	}
	if l.MaxDetailsBytes <= 0 {
		l.MaxDetailsBytes = defaults.MaxDetailsBytes
	}
	if l.MaxStringBytes <= 0 {
		l.MaxStringBytes = defaults.MaxStringBytes
	}
	if l.MaxIdentifierBytes <= 0 {
		l.MaxIdentifierBytes = defaults.MaxIdentifierBytes
	}
	if l.MaxRouteBytes <= 0 {
		l.MaxRouteBytes = defaults.MaxRouteBytes
	}
	if l.MaxDetailsDepth <= 0 {
		l.MaxDetailsDepth = defaults.MaxDetailsDepth
	}
	if l.MaxDetailsKeys <= 0 {
		l.MaxDetailsKeys = defaults.MaxDetailsKeys
	}
	if l.MaxDetailsArrayItems <= 0 {
		l.MaxDetailsArrayItems = defaults.MaxDetailsArrayItems
	}
	if l.ThrottleWindow < 0 {
		l.ThrottleWindow = defaults.ThrottleWindow
	}
	if l.ThrottleEntries <= 0 {
		l.ThrottleEntries = defaults.ThrottleEntries
	}
	return l
}

// ClientDiagnosticEventInput はHTTP層でデコードした生イベント。
type ClientDiagnosticEventInput struct {
	Timestamp            string
	Event                string
	SessionID            string
	WorkspaceID          string
	TabID                string
	Route                string
	FrontendBuildVersion string
	TreeVersion          *int64
	AnalysisVersion      *int64
	UpdatedAt            string
	NodeCount            *int64
	RootNodeID           string
	SessionStatus        string
	SnapshotSource       string
	Sequence             int64
	Details              map[string]any
}

// ClientDiagnosticsBatchInput は1リクエスト分の診断イベント。
// WorkspaceID / SessionID は認可済みの値をHTTP層が設定する。
type ClientDiagnosticsBatchInput struct {
	WorkspaceID          string
	SessionID            string
	TabID                string
	FrontendBuildVersion string
	UserID               string
	Events               []ClientDiagnosticEventInput
}

// ClientDiagnosticsResult は受理・拒否件数の内訳。
type ClientDiagnosticsResult struct {
	Accepted   int
	Rejected   int
	Suppressed int
	// Reasons は拒否・抑制理由ごとの件数。レスポンスとサーバーログの両方に出す。
	Reasons map[string]int
}

func (r *ClientDiagnosticsResult) addReason(reason string) {
	if r.Reasons == nil {
		r.Reasons = map[string]int{}
	}
	r.Reasons[reason]++
}

type throttleEntry struct {
	lastSeen time.Time
}

// ClientDiagnosticsService は診断イベントを検証・サニタイズし、sink群へ配る。
type ClientDiagnosticsService struct {
	sinks       []namedClientDiagnosticsSink
	limits      ClientDiagnosticsLimits
	now         func() time.Time
	reportError ClientDiagnosticsSinkErrorReporter

	mu       sync.Mutex
	throttle map[string]throttleEntry
}

type namedClientDiagnosticsSink struct {
	name string
	sink ClientDiagnosticsSink
}

type ClientDiagnosticsServiceOption func(*ClientDiagnosticsService)

func WithClientDiagnosticsLimits(limits ClientDiagnosticsLimits) ClientDiagnosticsServiceOption {
	return func(s *ClientDiagnosticsService) {
		s.limits = limits.withDefaults()
	}
}

func WithClientDiagnosticsClock(now func() time.Time) ClientDiagnosticsServiceOption {
	return func(s *ClientDiagnosticsService) {
		if now != nil {
			s.now = now
		}
	}
}

func WithClientDiagnosticsSinkErrorReporter(reporter ClientDiagnosticsSinkErrorReporter) ClientDiagnosticsServiceOption {
	return func(s *ClientDiagnosticsService) {
		s.reportError = reporter
	}
}

// WithClientDiagnosticsSink は名前付きsinkを追加する。名前は書き込み失敗ログに使う。
func WithClientDiagnosticsSink(name string, sink ClientDiagnosticsSink) ClientDiagnosticsServiceOption {
	return func(s *ClientDiagnosticsService) {
		if sink == nil {
			return
		}
		s.sinks = append(s.sinks, namedClientDiagnosticsSink{name: name, sink: sink})
	}
}

func NewClientDiagnosticsService(options ...ClientDiagnosticsServiceOption) *ClientDiagnosticsService {
	service := &ClientDiagnosticsService{
		limits:   DefaultClientDiagnosticsLimits(),
		now:      time.Now,
		throttle: map[string]throttleEntry{},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

// Record は1バッチを検証・サニタイズして sink へ書き出す。
// 個々のイベントが不正でもバッチ全体は失敗させず、理由別の件数を返す。
func (s *ClientDiagnosticsService) Record(ctx context.Context, batch ClientDiagnosticsBatchInput) (ClientDiagnosticsResult, error) {
	result := ClientDiagnosticsResult{}
	if len(batch.Events) == 0 {
		return result, ErrClientDiagnosticsBatchEmpty
	}
	events := batch.Events
	if len(events) > s.limits.MaxEventsPerRequest {
		result.Rejected += len(events) - s.limits.MaxEventsPerRequest
		for range events[s.limits.MaxEventsPerRequest:] {
			result.addReason("too_many_events")
		}
		events = events[:s.limits.MaxEventsPerRequest]
	}

	receivedAt := s.now().UTC()
	for _, input := range events {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		event, reason := s.sanitize(batch, input, receivedAt)
		if reason != "" {
			result.Rejected++
			result.addReason(reason)
			continue
		}
		if s.suppress(event) {
			result.Suppressed++
			result.addReason("throttled_duplicate")
			continue
		}
		s.write(event)
		result.Accepted++
	}
	return result, nil
}

func (s *ClientDiagnosticsService) write(event domain.ClientDiagnosticEvent) {
	for _, target := range s.sinks {
		if err := target.sink.WriteClientDiagnosticEvent(event); err != nil && s.reportError != nil {
			s.reportError(target.name, err)
		}
	}
}

// suppress は同一内容の高頻度イベントを時間窓で抑制する。
// tree_became_empty / react_error_captured は抑制しない。
func (s *ClientDiagnosticsService) suppress(event domain.ClientDiagnosticEvent) bool {
	if s.limits.ThrottleWindow <= 0 || domain.IsCriticalClientDiagnosticEvent(event.Event) {
		return false
	}
	key := throttleKey(event)
	now := s.now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.throttle[key]; ok && now.Sub(entry.lastSeen) < s.limits.ThrottleWindow {
		return true
	}
	if len(s.throttle) >= s.limits.ThrottleEntries {
		s.pruneThrottleLocked(now)
	}
	s.throttle[key] = throttleEntry{lastSeen: now}
	return false
}

func (s *ClientDiagnosticsService) pruneThrottleLocked(now time.Time) {
	for key, entry := range s.throttle {
		if now.Sub(entry.lastSeen) >= s.limits.ThrottleWindow {
			delete(s.throttle, key)
		}
	}
	if len(s.throttle) < s.limits.ThrottleEntries {
		return
	}
	// 時間窓内のエントリだけで上限に達した場合は、次のバッチのために全消去する。
	// 抑制が一時的に効かなくなるだけで、記録漏れにはならない。
	s.throttle = map[string]throttleEntry{}
}

func throttleKey(event domain.ClientDiagnosticEvent) string {
	var builder strings.Builder
	builder.WriteString(event.SessionID)
	builder.WriteByte(0)
	builder.WriteString(event.TabID)
	builder.WriteByte(0)
	builder.WriteString(event.Event)
	builder.WriteByte(0)
	builder.WriteString(event.Route)
	builder.WriteByte(0)
	builder.WriteString(event.SessionStatus)
	builder.WriteByte(0)
	builder.WriteString(event.SnapshotSource)
	builder.WriteByte(0)
	builder.WriteString(event.RootNodeID)
	builder.WriteByte(0)
	builder.WriteString(optionalInt64String(event.TreeVersion))
	builder.WriteByte(0)
	builder.WriteString(optionalInt64String(event.AnalysisVersion))
	builder.WriteByte(0)
	builder.WriteString(optionalInt64String(event.NodeCount))
	return builder.String()
}

func optionalInt64String(value *int64) string {
	if value == nil {
		return "-"
	}
	return strconv.FormatInt(*value, 10)
}

// sanitize は1件を検証・切り詰め・秘匿処理する。拒否する場合は理由を返す。
func (s *ClientDiagnosticsService) sanitize(
	batch ClientDiagnosticsBatchInput,
	input ClientDiagnosticEventInput,
	receivedAt time.Time,
) (domain.ClientDiagnosticEvent, string) {
	name := strings.TrimSpace(input.Event)
	if name == "" {
		return domain.ClientDiagnosticEvent{}, "missing_event_name"
	}
	if !domain.IsKnownClientDiagnosticEvent(name) {
		return domain.ClientDiagnosticEvent{}, "unknown_event_name"
	}

	sessionID := firstNonEmptyTrimmed(input.SessionID, batch.SessionID)
	if sessionID != batch.SessionID {
		return domain.ClientDiagnosticEvent{}, "session_id_mismatch"
	}
	workspaceID := firstNonEmptyTrimmed(input.WorkspaceID, batch.WorkspaceID)
	if workspaceID != batch.WorkspaceID {
		return domain.ClientDiagnosticEvent{}, "workspace_id_mismatch"
	}

	timestamp := receivedAt
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.Timestamp)); err == nil {
		timestamp = parsed.UTC()
	}

	details, detailsDropped := s.sanitizeDetails(input.Details)
	if detailsDropped {
		if details == nil {
			details = map[string]any{}
		}
		details["_truncated"] = true
	}

	event := domain.ClientDiagnosticEvent{
		Timestamp:            timestamp,
		ReceivedAt:           receivedAt,
		Event:                name,
		SessionID:            sessionID,
		WorkspaceID:          workspaceID,
		TabID:                s.clampIdentifier(firstNonEmptyTrimmed(input.TabID, batch.TabID)),
		Route:                s.clampText(scrubSecrets(strings.TrimSpace(input.Route)), s.limits.MaxRouteBytes),
		FrontendBuildVersion: s.clampIdentifier(firstNonEmptyTrimmed(input.FrontendBuildVersion, batch.FrontendBuildVersion)),
		TreeVersion:          input.TreeVersion,
		AnalysisVersion:      input.AnalysisVersion,
		UpdatedAt:            s.clampIdentifier(strings.TrimSpace(input.UpdatedAt)),
		NodeCount:            input.NodeCount,
		RootNodeID:           s.clampIdentifier(strings.TrimSpace(input.RootNodeID)),
		SessionStatus:        s.clampIdentifier(strings.TrimSpace(input.SessionStatus)),
		SnapshotSource:       s.clampIdentifier(strings.TrimSpace(input.SnapshotSource)),
		Details:              details,
		UserID:               strings.TrimSpace(batch.UserID),
		Sequence:             input.Sequence,
	}
	return event, ""
}

func (s *ClientDiagnosticsService) sanitizeDetails(details map[string]any) (map[string]any, bool) {
	if len(details) == 0 {
		return nil, false
	}
	sanitized, truncated := s.sanitizeValue(details, 0)
	result, ok := sanitized.(map[string]any)
	if !ok {
		return nil, true
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return map[string]any{"_unencodable": true}, true
	}
	if len(encoded) <= s.limits.MaxDetailsBytes {
		return result, truncated
	}
	// サイズ超過時はキーを落として上限内に収める。落としたキー名だけ残す。
	return s.shrinkDetails(result)
}

// shrinkDetails は MaxDetailsBytes を超えた details から、サイズの大きいキーを
// 順に落とす。落としたキーの名前は _droppedKeys に残す(何が失われたか分かるように)。
func (s *ClientDiagnosticsService) shrinkDetails(details map[string]any) (map[string]any, bool) {
	type sizedKey struct {
		key  string
		size int
	}
	sized := make([]sizedKey, 0, len(details))
	for key, value := range details {
		encoded, err := json.Marshal(value)
		if err != nil {
			sized = append(sized, sizedKey{key: key, size: s.limits.MaxDetailsBytes})
			continue
		}
		sized = append(sized, sizedKey{key: key, size: len(encoded)})
	}
	// 大きい順に落とす。同サイズはキー名で安定化する。
	for index := 1; index < len(sized); index++ {
		for inner := index; inner > 0; inner-- {
			left, right := sized[inner-1], sized[inner]
			if left.size > right.size || (left.size == right.size && left.key <= right.key) {
				break
			}
			sized[inner-1], sized[inner] = right, left
		}
	}

	result := map[string]any{}
	for key, value := range details {
		result[key] = value
	}
	dropped := make([]string, 0, len(sized))
	for _, candidate := range sized {
		encoded, err := json.Marshal(result)
		if err == nil && len(encoded) <= s.limits.MaxDetailsBytes {
			break
		}
		delete(result, candidate.key)
		dropped = append(dropped, candidate.key)
	}
	if len(dropped) > 0 {
		result["_droppedKeys"] = dropped
	}
	return result, true
}

func (s *ClientDiagnosticsService) sanitizeValue(value any, depth int) (any, bool) {
	if depth > s.limits.MaxDetailsDepth {
		return nil, true
	}
	switch typed := value.(type) {
	case nil:
		return nil, false
	case string:
		scrubbed := scrubSecrets(typed)
		clamped := s.clampText(scrubbed, s.limits.MaxStringBytes)
		return clamped, clamped != scrubbed
	case bool, float64, float32, int, int32, int64, json.Number:
		return typed, false
	case map[string]any:
		return s.sanitizeMap(typed, depth)
	case []any:
		return s.sanitizeSlice(typed, depth)
	default:
		// 想定外の型は数値・文字列いずれでもないため落とす。
		return nil, true
	}
}

func (s *ClientDiagnosticsService) sanitizeMap(source map[string]any, depth int) (any, bool) {
	result := map[string]any{}
	truncated := false
	keys := sortedKeys(source)
	if len(keys) > s.limits.MaxDetailsKeys {
		keys = keys[:s.limits.MaxDetailsKeys]
		truncated = true
	}
	for _, key := range keys {
		value := source[key]
		safeKey := s.clampText(key, s.limits.MaxIdentifierBytes)
		if shouldRedactKey(key, value) {
			result[safeKey] = redactedPlaceholder
			continue
		}
		sanitized, valueTruncated := s.sanitizeValue(value, depth+1)
		truncated = truncated || valueTruncated
		if sanitized == nil && value != nil {
			continue
		}
		result[safeKey] = sanitized
	}
	return result, truncated
}

func (s *ClientDiagnosticsService) sanitizeSlice(source []any, depth int) (any, bool) {
	truncated := false
	items := source
	if len(items) > s.limits.MaxDetailsArrayItems {
		items = items[:s.limits.MaxDetailsArrayItems]
		truncated = true
	}
	result := make([]any, 0, len(items))
	for _, item := range items {
		sanitized, itemTruncated := s.sanitizeValue(item, depth+1)
		truncated = truncated || itemTruncated
		result = append(result, sanitized)
	}
	return result, truncated
}

func sortedKeys(source map[string]any) []string {
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	for index := 1; index < len(keys); index++ {
		for inner := index; inner > 0 && keys[inner-1] > keys[inner]; inner-- {
			keys[inner-1], keys[inner] = keys[inner], keys[inner-1]
		}
	}
	return keys
}

const redactedPlaceholder = "[redacted]"

// 機密の可能性があるキー。正規化(小文字化・記号除去)後の部分一致で判定する。
var redactedKeySubstrings = []string{
	"authorization",
	"token",
	"cookie",
	"password",
	"passwd",
	"secret",
	"apikey",
	"credential",
	"bearer",
	"jwt",
	"privatekey",
	"email",
	"mail",
	"transcript",
	"utterance",
	"speech",
	"label",
	"title",
	"summary",
	"text",
	"content",
}

// 数値メトリクスであることが明らかなキーは、上の部分一致に当たっても残す。
// 例: textLength / transcriptCount は本文を含まないため観測に有用。
var metricKeySuffixes = []string{"length", "count", "chars", "bytes", "size"}

func shouldRedactKey(key string, value any) bool {
	normalized := normalizeKey(key)
	matched := false
	for _, candidate := range redactedKeySubstrings {
		if strings.Contains(normalized, candidate) {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	if isNumericValue(value) {
		for _, suffix := range metricKeySuffixes {
			if strings.HasSuffix(normalized, suffix) {
				return false
			}
		}
	}
	return true
}

func isNumericValue(value any) bool {
	switch value.(type) {
	case float64, float32, int, int32, int64, json.Number:
		return true
	default:
		return false
	}
}

func normalizeKey(key string) string {
	var builder strings.Builder
	for _, character := range strings.ToLower(key) {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') {
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

var (
	emailPattern  = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	jwtPattern    = regexp.MustCompile(`eyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}(?:\.[A-Za-z0-9_\-]+)?`)
	bearerPattern = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-]+`)
)

// scrubSecrets は値そのものに紛れ込んだメールアドレス・JWT・Bearerトークンを
// 伏せる。キー名での判定をすり抜けた場合の保険。
func scrubSecrets(value string) string {
	if value == "" {
		return value
	}
	scrubbed := jwtPattern.ReplaceAllString(value, redactedPlaceholder)
	scrubbed = bearerPattern.ReplaceAllString(scrubbed, redactedPlaceholder)
	scrubbed = emailPattern.ReplaceAllString(scrubbed, redactedPlaceholder)
	return scrubbed
}

func (s *ClientDiagnosticsService) clampIdentifier(value string) string {
	return s.clampText(scrubSecrets(value), s.limits.MaxIdentifierBytes)
}

func (s *ClientDiagnosticsService) clampText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	truncated := value[:limit]
	for len(truncated) > 0 && !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated
}
