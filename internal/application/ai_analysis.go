package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"deciscope-core-api/internal/domain"
)

const (
	defaultLiveAnalysisInterval         = 10 * time.Second
	meetingAnalysisMaxBackoff           = 5 * time.Minute
	meetingAnalysisSessionGCAfter       = 3 * time.Hour
	meetingAnalysisFinalTranscriptLimit = 2000
	// Token caps are ceilings, not targets. Reasoning models (gpt-5 family,
	// o-series) spend part of the completion budget on hidden reasoning
	// tokens before emitting the JSON answer, so these are sized well above
	// the expected visible output. The live cap covers the v2 payload
	// (items + tree) plus reasoning headroom.
	liveAnalysisMaxTokens  = 3000
	finalAnalysisMaxTokens = 4000
)

const (
	liveAnalysisTreeMaxNodes  = 12
	liveAnalysisItemsMaxCount = 30
)

const liveAnalysisSystemPrompt = "あなたは日本語の会議分析アシスタントです。与えられた「前回の分析状態」を新しい発言で更新し、指定されたJSONスキーマのオブジェクトだけを出力してください。JSON以外の説明文やコードフェンスは出力しないでください。"

const liveAnalysisSchemaDescription = `{
  "summary": "議論全体のこれまでの要約(毎回全文を出力、400字程度まで)",
  "currentTopic": "現在の主なトピック(毎回出力)",
  "resolvedIds": ["新しい発言によって解消・回答・完了した既存itemのid"],
  "items": [
    {
      "id": "英小文字・数字・ハイフンの安定ID(例: risk-db-migration)",
      "kind": "issue | question | risk | decision | todo",
      "severity": "low | medium | high",
      "title": "カード見出し(25字程度まで)",
      "body": "1〜2文の説明。todoで担当者や期限が分かる場合はここに含める",
      "status": "open | updated"
    }
  ],
  "tree": {
    "nodes": [
      {"id": "安定ID(itemsのidと共有可)", "kind": "topic | issue | question | risk | decision", "label": "短いラベル(20字程度)"}
    ],
    "edges": [
      {"source": "ノードid", "target": "ノードid"}
    ]
  }
}`

const liveAnalysisRulesDescription = `- summaryとcurrentTopicは毎回全文を出力してください。
- itemsには、このラウンドの新しい発言によって新しく生まれた論点・懸念・質問・決定事項・TODO、または内容が変化した既存item(idは既存のものを使う)だけを出力してください。変化のない既存itemは出力しないでください(サーバー側で保持されます)。
- 新しい発言に新規の論点・懸念・質問・決定事項・TODOが含まれる場合は、必ず対応するitemを出力してください。
- 新しく追加するitemはstatusを"open"に、既存itemを更新した場合はstatusを"updated"にしてください。
- 新しい発言によって解消された懸念(risk)・回答が出た質問(question)・対応が完了した論点があれば、そのitemのidを必ずresolvedIdsに列挙してください。該当が無ければresolvedIdsは空配列にしてください。「解消した」「対応済み」という内容のitemを出力してはいけません。決定事項(decision)は会議中は残してください。
  例: 前回の分析状態に {"id":"risk-x","kind":"risk",...} があり、新しい発言でその懸念が解消したと明言された場合、resolvedIdsに"risk-x"を入れます。
- treeも同様に、追加するノード・内容が変化したノード・新しいedgeだけを出力してください。既存の構造はサーバー側で保持されます。ノードのidは安定させ、対応するitemがある場合は同じidを使ってください。currentTopicが変わった場合は対応するtopicノードを出力してください。
- severityは影響度で判断してください(会議の結論を左右するものはhigh)。`

const finalAnalysisSystemPrompt = "あなたは日本語の会議分析アシスタントです。会議全体の文字起こしと事前情報から最終要約を作成し、指定されたJSONスキーマのオブジェクトだけを出力してください。JSON以外の説明文やコードフェンスは出力しないでください。"

const finalAnalysisSchemaDescription = `{
  "suggestedTitle": "会議タイトル案",
  "overview": "会議全体の要約(600字程度まで)",
  "decisions": [{"text": "", "importance": "high|medium|low"}],
  "actionItems": [{"text": "", "owner": "", "due": "", "priority": "high|medium|low"}],
  "openIssues": ["未解決事項"],
  "keyPoints": ["重要な論点"],
  "nextMeetingTopics": ["次回に持ち越すべき内容"]
}`

// MeetingAnalysisConfig controls MeetingAnalysisService behavior. Enabled
// gates both live analysis and the final summary at the Azure OpenAI
// configuration level; LiveEnabled/FinalEnabled further gate each feature
// independently.
type MeetingAnalysisConfig struct {
	Enabled bool

	LiveEnabled        bool
	LiveInterval       time.Duration
	LiveMinChars       int
	LiveMaxInputChars  int
	LiveRequestTimeout time.Duration

	FinalEnabled        bool
	FinalMaxInputChars  int
	FinalRequestTimeout time.Duration

	// Model is the Azure OpenAI deployment name recorded on every analysis
	// row and included in AI analysis log lines.
	Model string
}

func (c MeetingAnalysisConfig) liveActive() bool {
	return c.Enabled && c.LiveEnabled
}

func (c MeetingAnalysisConfig) finalActive() bool {
	return c.Enabled && c.FinalEnabled
}

// MeetingAnalysisService buffers final transcript segments per session,
// periodically asks Azure OpenAI to update a running live analysis, and
// generates a final summary once a session ends. It implements
// TranscriptSegmentPublisher (to receive final segments) and
// MeetingSessionEndedObserver (to trigger the final summary), and is always
// constructed non-nil; MeetingAnalysisConfig.Enabled/LiveEnabled/FinalEnabled
// make every operation a no-op when AI is not configured, so callers never
// need nil checks.
type MeetingAnalysisService struct {
	analysisRepo   MeetingAIAnalysisRepository
	completer      AIChatCompleter
	publisher      MeetingAIAnalysisPublisher
	transcriptRepo TranscriptSegmentRepository
	sessionRepo    MeetingSessionRepository
	config         MeetingAnalysisConfig
	now            func() time.Time

	mu       sync.Mutex
	sessions map[string]*liveAnalysisSessionState

	startOnce sync.Once
	closeOnce sync.Once
	stopCh    chan struct{}
}

type liveAnalysisSessionState struct {
	pending      []domain.TranscriptSegment
	pendingChars int
	running      bool
	lastPayload  json.RawMessage
	lastVersion  int64
	// versionSeeded guards the one-time DB lookup that restores lastPayload
	// and lastVersion after a backend restart, so versions keep increasing
	// across restarts and clients never discard newer updates as stale.
	versionSeeded  bool
	failureCount   int
	nextAttemptAt  time.Time
	lastActivityAt time.Time
	contextLoaded  bool
	context        *meetingSessionPreContext
}

func NewMeetingAnalysisService(
	analysisRepo MeetingAIAnalysisRepository,
	transcriptRepo TranscriptSegmentRepository,
	sessionRepo MeetingSessionRepository,
	completer AIChatCompleter,
	config MeetingAnalysisConfig,
	publisher ...MeetingAIAnalysisPublisher,
) *MeetingAnalysisService {
	var analysisPublisher MeetingAIAnalysisPublisher
	if len(publisher) > 0 {
		analysisPublisher = publisher[0]
	}
	if config.LiveInterval <= 0 {
		config.LiveInterval = defaultLiveAnalysisInterval
	}
	return &MeetingAnalysisService{
		analysisRepo:   analysisRepo,
		transcriptRepo: transcriptRepo,
		sessionRepo:    sessionRepo,
		completer:      completer,
		publisher:      analysisPublisher,
		config:         config,
		now:            time.Now,
		sessions:       make(map[string]*liveAnalysisSessionState),
		stopCh:         make(chan struct{}),
	}
}

// PublishTranscriptSegment implements TranscriptSegmentPublisher. Only final
// segments with a non-empty session id and text are buffered.
func (s *MeetingAnalysisService) PublishTranscriptSegment(segment domain.TranscriptSegment) {
	if s == nil || !s.config.liveActive() {
		return
	}
	if !segment.IsFinal {
		return
	}
	sessionID := strings.TrimSpace(segment.SessionID)
	if sessionID == "" {
		return
	}
	if strings.TrimSpace(segment.Text) == "" {
		return
	}

	s.mu.Lock()
	state := s.sessionStateLocked(sessionID)
	state.pending = append(state.pending, segment)
	state.pendingChars = sumSegmentChars(state.pending)
	state.lastActivityAt = s.now()
	s.mu.Unlock()
}

// Start launches the periodic live-analysis scheduler. It is a no-op when
// live analysis is disabled. Stop the scheduler with Close.
func (s *MeetingAnalysisService) Start(ctx context.Context) {
	if s == nil || !s.config.liveActive() {
		return
	}
	s.startOnce.Do(func() {
		go s.run(ctx)
	})
}

// Close stops the scheduler started by Start. It does not cancel in-flight
// analysis calls.
func (s *MeetingAnalysisService) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		close(s.stopCh)
	})
	return nil
}

func (s *MeetingAnalysisService) run(ctx context.Context) {
	ticker := time.NewTicker(s.config.LiveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

type liveAnalysisJob struct {
	sessionID string
	segments  []domain.TranscriptSegment
}

func (s *MeetingAnalysisService) tick(ctx context.Context) {
	now := s.now()
	var jobs []liveAnalysisJob

	s.mu.Lock()
	for sessionID, state := range s.sessions {
		if now.Sub(state.lastActivityAt) > meetingAnalysisSessionGCAfter {
			delete(s.sessions, sessionID)
			continue
		}
		if state.running {
			continue
		}
		if state.pendingChars < s.config.LiveMinChars {
			continue
		}
		if !state.nextAttemptAt.IsZero() && now.Before(state.nextAttemptAt) {
			continue
		}
		segments := state.pending
		state.pending = nil
		state.pendingChars = 0
		state.running = true
		jobs = append(jobs, liveAnalysisJob{sessionID: sessionID, segments: segments})
	}
	s.mu.Unlock()

	for _, job := range jobs {
		go s.runLiveAnalysis(ctx, job.sessionID, job.segments)
	}
}

func (s *MeetingAnalysisService) sessionStateLocked(sessionID string) *liveAnalysisSessionState {
	state, ok := s.sessions[sessionID]
	if !ok {
		state = &liveAnalysisSessionState{}
		s.sessions[sessionID] = state
	}
	return state
}

func (s *MeetingAnalysisService) runLiveAnalysis(ctx context.Context, sessionID string, segments []domain.TranscriptSegment) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Live AI analysis panic recovered. sessionId=%s panic=%v", sessionID, r)
			s.mu.Lock()
			state := s.sessionStateLocked(sessionID)
			state.running = false
			state.pending = append(append([]domain.TranscriptSegment{}, segments...), state.pending...)
			state.pendingChars = sumSegmentChars(state.pending)
			s.mu.Unlock()
		}
	}()

	start := s.now()
	log.Printf("Live AI analysis started. sessionId=%s segmentCount=%d", sessionID, len(segments))

	s.mu.Lock()
	state := s.sessionStateLocked(sessionID)
	previousPayload := state.lastPayload
	previousVersion := state.lastVersion
	versionSeeded := state.versionSeeded
	s.mu.Unlock()

	if !versionSeeded {
		previousPayload, previousVersion = s.seedLiveAnalysisState(ctx, sessionID, previousPayload, previousVersion)
	}

	preContext := s.sessionPreContext(ctx, sessionID)
	diffText, inputChars := buildAnalysisTranscript(segments, s.config.LiveMaxInputChars)
	userPrompt := buildLiveAnalysisUserPrompt(previousPayload, preContext, diffText)

	if s.completer == nil {
		s.handleLiveAnalysisFailure(ctx, sessionID, segments, previousPayload, previousVersion, errors.New("azure openai completer is not configured"), len(segments), inputChars, s.now().Sub(start))
		return
	}

	analysisCtx := ctx
	if s.config.LiveRequestTimeout > 0 {
		var cancel context.CancelFunc
		analysisCtx, cancel = context.WithTimeout(ctx, s.config.LiveRequestTimeout)
		defer cancel()
	}
	// Ephemeral running notification so clients can show a "generating"
	// state. It is broadcast only (never written to the database) and keeps
	// the current version/payload so clients can safely replace their whole
	// state with it.
	s.publishAnalysis(domain.MeetingAIAnalysis{
		SessionID: sessionID,
		Type:      domain.MeetingAIAnalysisLive,
		Status:    domain.MeetingAIAnalysisRunning,
		Version:   previousVersion,
		Payload:   previousPayload,
		Model:     s.config.Model,
		UpdatedAt: s.now().UTC(),
	})
	result, err := s.completer.Complete(analysisCtx, AIChatRequest{
		System:    liveAnalysisSystemPrompt,
		User:      userPrompt,
		MaxTokens: liveAnalysisMaxTokens,
	})
	elapsed := s.now().Sub(start)
	if err != nil {
		s.handleLiveAnalysisFailure(ctx, sessionID, segments, previousPayload, previousVersion, err, len(segments), inputChars, elapsed)
		return
	}
	payload, parseErr := parseAndMergeLiveAnalysisPayload(result.Content, previousPayload)
	if parseErr != nil {
		s.handleLiveAnalysisFailure(ctx, sessionID, segments, previousPayload, previousVersion, parseErr, len(segments), inputChars, elapsed)
		return
	}

	newVersion := previousVersion + 1
	saved, upsertErr := s.analysisRepo.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID:    sessionID,
		Type:         domain.MeetingAIAnalysisLive,
		Status:       domain.MeetingAIAnalysisCompleted,
		Version:      newVersion,
		Payload:      payload,
		Model:        s.config.Model,
		SegmentCount: len(segments),
		InputChars:   inputChars,
		UpdatedAt:    s.now().UTC(),
	})

	s.mu.Lock()
	state = s.sessionStateLocked(sessionID)
	state.running = false
	if upsertErr == nil {
		state.lastPayload = payload
		state.lastVersion = newVersion
		state.failureCount = 0
		state.nextAttemptAt = time.Time{}
	}
	s.mu.Unlock()

	if upsertErr != nil {
		log.Printf("Live AI analysis persist failed. sessionId=%s version=%d error=%v", sessionID, newVersion, upsertErr)
		return
	}
	log.Printf("Live AI analysis completed. sessionId=%s segmentCount=%d inputChars=%d version=%d promptTokens=%d completionTokens=%d elapsed=%s",
		sessionID, len(segments), inputChars, newVersion, result.PromptTokens, result.CompletionTokens, elapsed)
	s.publishAnalysis(*saved)
}

// seedLiveAnalysisState restores the previous live analysis payload/version
// from the database the first time a session is analyzed in this process, so
// a backend restart mid-meeting neither resets the version sequence nor loses
// the accumulated analysis state.
func (s *MeetingAnalysisService) seedLiveAnalysisState(ctx context.Context, sessionID string, payload json.RawMessage, version int64) (json.RawMessage, int64) {
	if version == 0 && len(payload) == 0 {
		existing, err := s.analysisRepo.GetMeetingAIAnalysis(ctx, sessionID, domain.MeetingAIAnalysisLive)
		switch {
		case err == nil && existing != nil:
			payload = existing.Payload
			version = existing.Version
		case err != nil && !errors.Is(err, domain.ErrNotFound):
			log.Printf("Live AI analysis previous state lookup failed. sessionId=%s error=%v", sessionID, err)
		}
	}

	s.mu.Lock()
	state := s.sessionStateLocked(sessionID)
	state.versionSeeded = true
	state.lastPayload = payload
	state.lastVersion = version
	s.mu.Unlock()
	return payload, version
}

func (s *MeetingAnalysisService) handleLiveAnalysisFailure(ctx context.Context, sessionID string, segments []domain.TranscriptSegment, previousPayload json.RawMessage, previousVersion int64, cause error, segmentCount, inputChars int, elapsed time.Duration) {
	log.Printf("Live AI analysis failed. sessionId=%s segmentCount=%d inputChars=%d elapsed=%s error=%v", sessionID, segmentCount, inputChars, elapsed, cause)

	s.mu.Lock()
	state := s.sessionStateLocked(sessionID)
	state.running = false
	state.pending = append(append([]domain.TranscriptSegment{}, segments...), state.pending...)
	state.pendingChars = sumSegmentChars(state.pending)
	state.failureCount++
	state.nextAttemptAt = s.now().Add(liveAnalysisBackoff(s.config.LiveInterval, state.failureCount))
	s.mu.Unlock()

	saved, err := s.analysisRepo.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID:    sessionID,
		Type:         domain.MeetingAIAnalysisLive,
		Status:       domain.MeetingAIAnalysisFailed,
		Version:      previousVersion,
		Payload:      previousPayload,
		Model:        s.config.Model,
		SegmentCount: segmentCount,
		InputChars:   inputChars,
		LastError:    truncateErrorMessage(cause, 300),
		UpdatedAt:    s.now().UTC(),
	})
	if err != nil {
		log.Printf("Live AI analysis failure persist failed. sessionId=%s error=%v", sessionID, err)
		return
	}
	s.publishAnalysis(*saved)
}

func liveAnalysisBackoff(interval time.Duration, failureCount int) time.Duration {
	if interval <= 0 {
		interval = defaultLiveAnalysisInterval
	}
	backoff := interval
	for i := 0; i < failureCount; i++ {
		backoff *= 2
		if backoff >= meetingAnalysisMaxBackoff {
			return meetingAnalysisMaxBackoff
		}
	}
	return backoff
}

// NotifyMeetingSessionEnded implements MeetingSessionEndedObserver. It
// launches the final summary generation in the background so the caller
// (MeetingSessionService) never blocks on it.
func (s *MeetingAnalysisService) NotifyMeetingSessionEnded(session domain.MeetingSession) {
	if s == nil || !s.config.finalActive() {
		return
	}
	sessionID := strings.TrimSpace(session.ID)
	if sessionID == "" {
		return
	}
	go s.generateFinalSummary(context.Background(), session)
}

func (s *MeetingAnalysisService) generateFinalSummary(ctx context.Context, session domain.MeetingSession) {
	sessionID := strings.TrimSpace(session.ID)
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Final AI summary panic recovered. sessionId=%s panic=%v", sessionID, r)
		}
	}()

	existing, err := s.analysisRepo.GetMeetingAIAnalysis(ctx, sessionID, domain.MeetingAIAnalysisFinal)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		log.Printf("Final AI summary existing lookup failed. sessionId=%s error=%v", sessionID, err)
		return
	}
	if err == nil && existing != nil && (existing.Status == domain.MeetingAIAnalysisRunning || existing.Status == domain.MeetingAIAnalysisCompleted) {
		log.Printf("Final AI summary skipped because it already exists. sessionId=%s status=%s", sessionID, existing.Status)
		return
	}

	segments, err := s.transcriptRepo.ListTranscriptSegments(ctx, "", sessionID, meetingAnalysisFinalTranscriptLimit)
	if err != nil {
		log.Printf("Final AI summary transcript lookup failed. sessionId=%s error=%v", sessionID, err)
		return
	}
	finalSegments := filterNonEmptySegments(segments)
	if len(finalSegments) == 0 {
		log.Printf("Final AI summary skipped because transcript is empty. sessionId=%s", sessionID)
		return
	}

	if s.completer == nil {
		log.Printf("Final AI summary skipped because azure openai completer is not configured. sessionId=%s", sessionID)
		return
	}

	version := int64(1)
	var previousPayload json.RawMessage
	if existing != nil {
		version = existing.Version + 1
		previousPayload = existing.Payload
	}
	runningSaved, err := s.analysisRepo.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID: sessionID,
		Type:      domain.MeetingAIAnalysisFinal,
		Status:    domain.MeetingAIAnalysisRunning,
		Version:   version,
		Payload:   previousPayload,
		Model:     s.config.Model,
		UpdatedAt: s.now().UTC(),
	})
	if err != nil {
		log.Printf("Final AI summary running state persist failed. sessionId=%s error=%v", sessionID, err)
		return
	}
	s.publishAnalysis(*runningSaved)

	s.mu.Lock()
	state, hasState := s.sessions[sessionID]
	var livePayload json.RawMessage
	if hasState {
		livePayload = state.lastPayload
	}
	delete(s.sessions, sessionID)
	s.mu.Unlock()

	preContext := s.sessionPreContext(ctx, sessionID)
	transcriptText, inputChars, truncated := buildAnalysisTranscriptTruncated(finalSegments, s.config.FinalMaxInputChars)
	userPrompt := buildFinalAnalysisUserPrompt(livePayload, preContext, transcriptText, truncated)

	start := s.now()
	log.Printf("Final AI summary started. sessionId=%s segmentCount=%d inputChars=%d", sessionID, len(finalSegments), inputChars)

	analysisCtx := ctx
	if s.config.FinalRequestTimeout > 0 {
		var cancel context.CancelFunc
		analysisCtx, cancel = context.WithTimeout(ctx, s.config.FinalRequestTimeout)
		defer cancel()
	}
	result, err := s.completer.Complete(analysisCtx, AIChatRequest{
		System:    finalAnalysisSystemPrompt,
		User:      userPrompt,
		MaxTokens: finalAnalysisMaxTokens,
	})
	elapsed := s.now().Sub(start)
	if err != nil {
		s.handleFinalAnalysisFailure(ctx, sessionID, version, previousPayload, err, len(finalSegments), inputChars, elapsed)
		return
	}
	payload, parseErr := parseAndValidateFinalAnalysisPayload(result.Content)
	if parseErr != nil {
		s.handleFinalAnalysisFailure(ctx, sessionID, version, previousPayload, parseErr, len(finalSegments), inputChars, elapsed)
		return
	}

	saved, err := s.analysisRepo.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID:    sessionID,
		Type:         domain.MeetingAIAnalysisFinal,
		Status:       domain.MeetingAIAnalysisCompleted,
		Version:      version,
		Payload:      payload,
		Model:        s.config.Model,
		SegmentCount: len(finalSegments),
		InputChars:   inputChars,
		UpdatedAt:    s.now().UTC(),
	})
	if err != nil {
		log.Printf("Final AI summary persist failed. sessionId=%s error=%v", sessionID, err)
		return
	}
	log.Printf("Final AI summary completed. sessionId=%s segmentCount=%d inputChars=%d version=%d promptTokens=%d completionTokens=%d elapsed=%s",
		sessionID, len(finalSegments), inputChars, saved.Version, result.PromptTokens, result.CompletionTokens, elapsed)
	s.publishAnalysis(*saved)
}

func (s *MeetingAnalysisService) handleFinalAnalysisFailure(ctx context.Context, sessionID string, version int64, previousPayload json.RawMessage, cause error, segmentCount, inputChars int, elapsed time.Duration) {
	log.Printf("Final AI summary failed. sessionId=%s segmentCount=%d inputChars=%d elapsed=%s error=%v", sessionID, segmentCount, inputChars, elapsed, cause)
	saved, err := s.analysisRepo.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID:    sessionID,
		Type:         domain.MeetingAIAnalysisFinal,
		Status:       domain.MeetingAIAnalysisFailed,
		Version:      version,
		Payload:      previousPayload,
		Model:        s.config.Model,
		SegmentCount: segmentCount,
		InputChars:   inputChars,
		LastError:    truncateErrorMessage(cause, 300),
		UpdatedAt:    s.now().UTC(),
	})
	if err != nil {
		log.Printf("Final AI summary failure persist failed. sessionId=%s error=%v", sessionID, err)
		return
	}
	s.publishAnalysis(*saved)
}

func (s *MeetingAnalysisService) publishAnalysis(analysis domain.MeetingAIAnalysis) {
	if s.publisher != nil {
		if analysis.Type == domain.MeetingAIAnalysisLive {
			analysis.IntervalSeconds = s.LiveAnalysisIntervalSeconds()
		}
		s.publisher.PublishMeetingAIAnalysis(analysis)
	}
}

// LiveAnalysisIntervalSeconds returns the live analysis check interval in
// seconds, or 0 when live analysis is disabled. Clients use it for a "next
// update in about N seconds" display.
func (s *MeetingAnalysisService) LiveAnalysisIntervalSeconds() int {
	if s == nil || !s.config.liveActive() {
		return 0
	}
	return int(s.config.LiveInterval.Seconds())
}

// MeetingAIAnalysesSnapshot is the latest live/final analysis pair for a
// session. Live and/or Final are nil when no analysis exists yet.
// LiveIntervalSeconds is the live analysis check interval (0 when AI or live
// analysis is disabled).
type MeetingAIAnalysesSnapshot struct {
	SessionID           string
	Live                *domain.MeetingAIAnalysis
	Final               *domain.MeetingAIAnalysis
	LiveIntervalSeconds int
}

func (s *MeetingAnalysisService) GetMeetingAIAnalyses(ctx context.Context, sessionID string) (*MeetingAIAnalysesSnapshot, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("%w: sessionId is required", domain.ErrInvalidArgument)
	}
	if s == nil {
		return &MeetingAIAnalysesSnapshot{SessionID: sessionID}, nil
	}
	live, err := s.getOptionalAnalysis(ctx, sessionID, domain.MeetingAIAnalysisLive)
	if err != nil {
		return nil, err
	}
	final, err := s.getOptionalAnalysis(ctx, sessionID, domain.MeetingAIAnalysisFinal)
	if err != nil {
		return nil, err
	}
	return &MeetingAIAnalysesSnapshot{
		SessionID:           sessionID,
		Live:                live,
		Final:               final,
		LiveIntervalSeconds: s.LiveAnalysisIntervalSeconds(),
	}, nil
}

func (s *MeetingAnalysisService) getOptionalAnalysis(ctx context.Context, sessionID string, analysisType domain.MeetingAIAnalysisType) (*domain.MeetingAIAnalysis, error) {
	analysis, err := s.analysisRepo.GetMeetingAIAnalysis(ctx, sessionID, analysisType)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return analysis, nil
}

// meetingSessionPreContext is the subset of session metadata that is
// injected into every AI prompt as background information.
type meetingSessionPreContext struct {
	Title             string
	Purpose           string
	Agenda            string
	DecisionPoints    string
	Concerns          string
	ExpectedOutput    string
	CustomInstruction string
}

func (c *meetingSessionPreContext) isEmpty() bool {
	return c.Title == "" && c.Purpose == "" && c.Agenda == "" && c.DecisionPoints == "" &&
		c.Concerns == "" && c.ExpectedOutput == "" && c.CustomInstruction == ""
}

func (c *meetingSessionPreContext) render() string {
	var lines []string
	if c.Title != "" {
		lines = append(lines, "タイトル: "+c.Title)
	}
	if c.Purpose != "" {
		lines = append(lines, "目的: "+c.Purpose)
	}
	if c.Agenda != "" {
		lines = append(lines, "アジェンダ: "+c.Agenda)
	}
	if c.DecisionPoints != "" {
		lines = append(lines, "決定すべき事項: "+c.DecisionPoints)
	}
	if c.Concerns != "" {
		lines = append(lines, "懸念点: "+c.Concerns)
	}
	if c.ExpectedOutput != "" {
		lines = append(lines, "期待される成果: "+c.ExpectedOutput)
	}
	if c.CustomInstruction != "" {
		lines = append(lines, "特別な指示: "+c.CustomInstruction)
	}
	return strings.Join(lines, "\n")
}

// sessionPreContext loads and caches the pre-meeting context for a session.
// The cache also stores negative lookups (nil) so a repeated tick does not
// call MeetingSessionRepository again.
func (s *MeetingAnalysisService) sessionPreContext(ctx context.Context, sessionID string) *meetingSessionPreContext {
	s.mu.Lock()
	state := s.sessionStateLocked(sessionID)
	if state.contextLoaded {
		cached := state.context
		s.mu.Unlock()
		return cached
	}
	s.mu.Unlock()

	preContext := s.fetchSessionPreContext(ctx, sessionID)

	s.mu.Lock()
	state = s.sessionStateLocked(sessionID)
	state.context = preContext
	state.contextLoaded = true
	s.mu.Unlock()

	return preContext
}

func (s *MeetingAnalysisService) fetchSessionPreContext(ctx context.Context, sessionID string) *meetingSessionPreContext {
	if s.sessionRepo == nil {
		return nil
	}
	session, err := s.sessionRepo.GetMeetingSession(ctx, sessionID)
	if err != nil || session == nil {
		return nil
	}
	preContext := &meetingSessionPreContext{
		Title:             strings.TrimSpace(session.Title),
		Purpose:           strings.TrimSpace(session.Purpose),
		Agenda:            strings.TrimSpace(session.Agenda),
		DecisionPoints:    strings.TrimSpace(session.DecisionPoints),
		Concerns:          strings.TrimSpace(session.Concerns),
		ExpectedOutput:    strings.TrimSpace(session.ExpectedOutput),
		CustomInstruction: strings.TrimSpace(session.CustomInstruction),
	}
	if preContext.isEmpty() {
		return nil
	}
	return preContext
}

func buildLiveAnalysisUserPrompt(previousPayload json.RawMessage, preContext *meetingSessionPreContext, diffText string) string {
	var b strings.Builder
	b.WriteString("[前回の分析状態(JSON)]\n")
	if len(previousPayload) == 0 {
		b.WriteString("null")
	} else {
		b.Write(previousPayload)
	}
	b.WriteString("\n\n")
	if preContext != nil {
		b.WriteString("[会議の事前情報]\n")
		b.WriteString(preContext.render())
		b.WriteString("\n\n")
	}
	b.WriteString("[新しい発言(差分)]\n")
	if diffText == "" {
		b.WriteString("(新しい発言はありません)")
	} else {
		b.WriteString(diffText)
	}
	b.WriteString("\n\n")
	b.WriteString("[更新ルール]\n")
	b.WriteString(liveAnalysisRulesDescription)
	b.WriteString("\n\n")
	b.WriteString("上記の情報とルールを踏まえて、前回の分析状態を更新し、次のJSONスキーマのオブジェクトだけを出力してください:\n")
	b.WriteString(liveAnalysisSchemaDescription)
	return b.String()
}

func buildFinalAnalysisUserPrompt(livePayload json.RawMessage, preContext *meetingSessionPreContext, transcriptText string, truncated bool) string {
	var b strings.Builder
	if len(livePayload) > 0 {
		b.WriteString("[会議中に生成されたライブ分析の最新状態(JSON)]\n")
		b.Write(livePayload)
		b.WriteString("\n\n")
	}
	if preContext != nil {
		b.WriteString("[会議の事前情報]\n")
		b.WriteString(preContext.render())
		b.WriteString("\n\n")
	}
	b.WriteString("[会議全体の文字起こし]\n")
	if truncated {
		b.WriteString("(注意: 文字数上限のため、冒頭の発言は省略されています。以降の発言のみが含まれます。)\n")
	}
	b.WriteString(transcriptText)
	b.WriteString("\n\n")
	b.WriteString("上記の情報を踏まえて、会議全体の最終要約として次のJSONスキーマのオブジェクトだけを出力してください:\n")
	b.WriteString(finalAnalysisSchemaDescription)
	return b.String()
}

// buildAnalysisTranscript formats final segments as "話者名: テキスト" lines
// and drops the oldest lines first when the joined text exceeds maxChars.
func buildAnalysisTranscript(segments []domain.TranscriptSegment, maxChars int) (string, int) {
	text, chars, _ := buildAnalysisTranscriptTruncated(segments, maxChars)
	return text, chars
}

func buildAnalysisTranscriptTruncated(segments []domain.TranscriptSegment, maxChars int) (string, int, bool) {
	lines := make([]string, 0, len(segments))
	for _, segment := range segments {
		text := strings.TrimSpace(segment.Text)
		if text == "" {
			continue
		}
		speaker := strings.TrimSpace(segment.SpeakerName)
		if speaker == "" {
			speaker = "話者不明"
		}
		lines = append(lines, speaker+": "+text)
	}
	originalChars := totalLineChars(lines)
	joined, chars := truncateLinesFromOldest(lines, maxChars)
	return joined, chars, chars < originalChars
}

func truncateLinesFromOldest(lines []string, maxChars int) (string, int) {
	if maxChars <= 0 {
		joined := strings.Join(lines, "\n")
		return joined, len([]rune(joined))
	}
	start := 0
	total := totalLineChars(lines)
	for total > maxChars && start < len(lines) {
		total -= lineChars(lines[start], start > 0)
		start++
	}
	joined := strings.Join(lines[start:], "\n")
	return joined, len([]rune(joined))
}

func totalLineChars(lines []string) int {
	total := 0
	for i, line := range lines {
		total += lineChars(line, i > 0)
	}
	return total
}

func lineChars(line string, withNewline bool) int {
	length := len([]rune(line))
	if withNewline {
		length++
	}
	return length
}

func sumSegmentChars(segments []domain.TranscriptSegment) int {
	total := 0
	for _, segment := range segments {
		total += len([]rune(strings.TrimSpace(segment.Text)))
	}
	return total
}

func filterNonEmptySegments(segments []domain.TranscriptSegment) []domain.TranscriptSegment {
	filtered := make([]domain.TranscriptSegment, 0, len(segments))
	for _, segment := range segments {
		if strings.TrimSpace(segment.Text) == "" {
			continue
		}
		filtered = append(filtered, segment)
	}
	return filtered
}

func truncateErrorMessage(err error, limit int) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	runes := []rune(message)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return message
}

// liveAnalysisPayload is the v2 live analysis schema. items follows the
// AnalysisItem vocabulary and tree follows the tree.update vocabulary from
// docs/events.md. Unknown fields in the model output (including the removed
// v1 fields decisions/actionItems/openQuestions/concerns/nextChecks) are
// silently ignored by json.Unmarshal.
//
// ResolvedIds is a model-to-server instruction channel only: the model lists
// the ids of items resolved by the new utterances, the server removes those
// items and tree nodes deterministically, and the field is cleared before
// persisting so it never appears in stored/broadcast payloads or in the next
// prompt's previous state.
type liveAnalysisPayload struct {
	Summary      string             `json:"summary"`
	CurrentTopic string             `json:"currentTopic"`
	ResolvedIds  []string           `json:"resolvedIds,omitempty"`
	Items        []liveAnalysisItem `json:"items"`
	Tree         *liveAnalysisTree  `json:"tree"`
}

type liveAnalysisItem struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Status   string `json:"status"`
}

type liveAnalysisTree struct {
	Nodes []liveAnalysisTreeNode `json:"nodes"`
	Edges []liveAnalysisTreeEdge `json:"edges"`
}

type liveAnalysisTreeNode struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

type liveAnalysisTreeEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

func (p liveAnalysisPayload) isEmpty() bool {
	return strings.TrimSpace(p.Summary) == "" && strings.TrimSpace(p.CurrentTopic) == "" &&
		len(p.Items) == 0 && (p.Tree == nil || len(p.Tree.Nodes) == 0)
}

func validLiveAnalysisItemKind(kind string) bool {
	switch kind {
	case "issue", "question", "risk", "decision", "todo":
		return true
	default:
		return false
	}
}

func validLiveAnalysisTreeNodeKind(kind string) bool {
	switch kind {
	case "topic", "issue", "question", "risk", "decision":
		return true
	default:
		return false
	}
}

func validLiveAnalysisSeverity(severity string) bool {
	switch severity {
	case "low", "medium", "high":
		return true
	default:
		return false
	}
}

func validLiveAnalysisItemStatus(status string) bool {
	switch status {
	case "open", "updated":
		return true
	default:
		return false
	}
}

// liveAnalysisCurrentTopicNodeID is the id of the synthetic topic node that
// normalizeLiveAnalysisTree inserts when the model omitted a topic node.
const liveAnalysisCurrentTopicNodeID = "topic-current"

const liveAnalysisTopicLabelMaxRunes = 20

// normalizeLiveAnalysisItems lowercases kind/severity/status and drops items
// with no usable text or an out-of-vocabulary kind. Partially invalid output
// never fails the whole payload; only the offending element is discarded.
//
// resolvedIDs carries the ids the model listed in resolvedIds; items with a
// matching id are removed deterministically. Items whose status is
// "resolved" are also removed as a defensive fallback, and their ids are
// added to the same set (mutating the passed map) so the caller can remove
// the matching tree nodes with the union of both sources. Because the
// persisted payload is this normalized form, resolved items disappear from
// the next prompt's previous state as well.
func normalizeLiveAnalysisItems(items []liveAnalysisItem, resolvedIDs map[string]struct{}) []liveAnalysisItem {
	normalized := make([]liveAnalysisItem, 0, len(items))
	for _, item := range items {
		item.ID = strings.TrimSpace(item.ID)
		item.Kind = strings.ToLower(strings.TrimSpace(item.Kind))
		item.Severity = strings.ToLower(strings.TrimSpace(item.Severity))
		item.Title = strings.TrimSpace(item.Title)
		item.Body = strings.TrimSpace(item.Body)
		item.Status = strings.ToLower(strings.TrimSpace(item.Status))
		if item.Status == "resolved" {
			if item.ID != "" {
				resolvedIDs[item.ID] = struct{}{}
			}
			continue
		}
		if _, resolved := resolvedIDs[item.ID]; item.ID != "" && resolved {
			continue
		}
		if item.Title == "" && item.Body == "" {
			continue
		}
		if !validLiveAnalysisItemKind(item.Kind) {
			continue
		}
		if !validLiveAnalysisSeverity(item.Severity) {
			item.Severity = "medium"
		}
		if !validLiveAnalysisItemStatus(item.Status) {
			item.Status = "open"
		}
		normalized = append(normalized, item)
	}
	return normalized
}

// mergeLiveAnalysisItems merges the model's diff items into the previous
// state: previous items keep their order, a diff item with an existing id
// replaces that item in place (status forced to "updated"), and new ids are
// appended. Items whose id is in resolvedIDs are removed. The merged list is
// capped at liveAnalysisItemsMaxCount, evicting the oldest items first.
func mergeLiveAnalysisItems(previous, diff []liveAnalysisItem, resolvedIDs map[string]struct{}) []liveAnalysisItem {
	merged := make([]liveAnalysisItem, 0, len(previous)+len(diff))
	index := make(map[string]int, len(previous)+len(diff))
	for _, item := range previous {
		if item.ID != "" {
			if _, resolved := resolvedIDs[item.ID]; resolved {
				continue
			}
			index[item.ID] = len(merged)
		}
		merged = append(merged, item)
	}
	for _, item := range diff {
		if item.ID != "" {
			if at, ok := index[item.ID]; ok {
				item.Status = "updated"
				merged[at] = item
				continue
			}
			index[item.ID] = len(merged)
		}
		merged = append(merged, item)
	}
	if len(merged) > liveAnalysisItemsMaxCount {
		merged = merged[len(merged)-liveAnalysisItemsMaxCount:]
	}
	return merged
}

// mergeLiveAnalysisTree merges the model's diff tree into the previous tree:
// diff nodes upsert previous nodes by id (new nodes are appended), nodes with
// a resolved id are removed, and edges are the deduplicated union of previous
// and diff edges. The merged set then goes through finalizeLiveAnalysisTree
// for topic completion, the node cap, and edge validation.
func mergeLiveAnalysisTree(previous, diff *liveAnalysisTree, resolvedIDs map[string]struct{}, currentTopic string) *liveAnalysisTree {
	var nodes []liveAnalysisTreeNode
	index := make(map[string]int)
	addNode := func(node liveAnalysisTreeNode) {
		node.ID = strings.TrimSpace(node.ID)
		node.Kind = strings.ToLower(strings.TrimSpace(node.Kind))
		node.Label = strings.TrimSpace(node.Label)
		if node.ID == "" || node.Label == "" {
			return
		}
		if !validLiveAnalysisTreeNodeKind(node.Kind) {
			return
		}
		if at, ok := index[node.ID]; ok {
			nodes[at] = node
			return
		}
		index[node.ID] = len(nodes)
		nodes = append(nodes, node)
	}
	var rawEdges []liveAnalysisTreeEdge
	if previous != nil {
		for _, node := range previous.Nodes {
			addNode(node)
		}
		rawEdges = append(rawEdges, previous.Edges...)
	}
	if diff != nil {
		for _, node := range diff.Nodes {
			addNode(node)
		}
		rawEdges = append(rawEdges, diff.Edges...)
	}

	kept := make([]liveAnalysisTreeNode, 0, len(nodes))
	for _, node := range nodes {
		if _, resolved := resolvedIDs[node.ID]; resolved {
			continue
		}
		kept = append(kept, node)
	}

	edges := make([]liveAnalysisTreeEdge, 0, len(rawEdges))
	seenEdges := make(map[string]struct{}, len(rawEdges))
	for _, edge := range rawEdges {
		edge.Source = strings.TrimSpace(edge.Source)
		edge.Target = strings.TrimSpace(edge.Target)
		key := edge.Source + "\x00" + edge.Target
		if _, ok := seenEdges[key]; ok {
			continue
		}
		seenEdges[key] = struct{}{}
		edges = append(edges, edge)
	}
	return finalizeLiveAnalysisTree(kept, edges, currentTopic)
}

// finalizeLiveAnalysisTree applies the invariants of the stored tree to a
// merged node/edge set: an empty node list collapses the tree to nil
// (serialized as JSON null); when there is no topic node and currentTopic is
// non-empty a synthetic "topic-current" node is inserted at the head (unless
// a node already uses that id) and every node without an incoming edge is
// connected from it; nodes are capped at liveAnalysisTreeMaxNodes keeping
// topic nodes and evicting the oldest non-topic nodes first; and edges
// referencing missing nodes are dropped.
func finalizeLiveAnalysisTree(nodes []liveAnalysisTreeNode, edges []liveAnalysisTreeEdge, currentTopic string) *liveAnalysisTree {
	if len(nodes) == 0 {
		return nil
	}
	hasTopicNode := false
	hasCurrentTopicID := false
	for _, node := range nodes {
		if node.Kind == "topic" {
			hasTopicNode = true
		}
		if node.ID == liveAnalysisCurrentTopicNodeID {
			hasCurrentTopicID = true
		}
	}

	insertedTopic := false
	currentTopic = strings.TrimSpace(currentTopic)
	if !hasTopicNode && currentTopic != "" && !hasCurrentTopicID {
		nodes = append([]liveAnalysisTreeNode{{
			ID:    liveAnalysisCurrentTopicNodeID,
			Kind:  "topic",
			Label: truncateRunes(currentTopic, liveAnalysisTopicLabelMaxRunes),
		}}, nodes...)
		insertedTopic = true
	}

	nodes = capLiveAnalysisTreeNodes(nodes, liveAnalysisTreeMaxNodes)
	nodeIDs := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		nodeIDs[node.ID] = struct{}{}
	}
	validEdges := make([]liveAnalysisTreeEdge, 0, len(edges))
	for _, edge := range edges {
		if _, ok := nodeIDs[edge.Source]; !ok {
			continue
		}
		if _, ok := nodeIDs[edge.Target]; !ok {
			continue
		}
		validEdges = append(validEdges, edge)
	}

	if insertedTopic {
		hasIncoming := make(map[string]bool, len(nodes))
		for _, edge := range validEdges {
			hasIncoming[edge.Target] = true
		}
		for _, node := range nodes {
			if node.ID == liveAnalysisCurrentTopicNodeID {
				continue
			}
			if !hasIncoming[node.ID] {
				validEdges = append(validEdges, liveAnalysisTreeEdge{Source: liveAnalysisCurrentTopicNodeID, Target: node.ID})
			}
		}
	}
	return &liveAnalysisTree{Nodes: nodes, Edges: validEdges}
}

// capLiveAnalysisTreeNodes trims the node list to max entries, evicting the
// oldest non-topic nodes first so topic nodes survive. If topic nodes alone
// exceed the cap, the oldest topics are evicted too.
func capLiveAnalysisTreeNodes(nodes []liveAnalysisTreeNode, max int) []liveAnalysisTreeNode {
	if len(nodes) <= max {
		return nodes
	}
	excess := len(nodes) - max
	kept := make([]liveAnalysisTreeNode, 0, max)
	for _, node := range nodes {
		if excess > 0 && node.Kind != "topic" {
			excess--
			continue
		}
		kept = append(kept, node)
	}
	if len(kept) > max {
		kept = kept[len(kept)-max:]
	}
	return kept
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

// previousLiveAnalysisState parses the previously stored payload. A missing
// or invalid previous payload degrades to an empty state instead of failing,
// so a corrupt row can never wedge live analysis.
func previousLiveAnalysisState(previousPayload json.RawMessage) liveAnalysisPayload {
	if len(previousPayload) == 0 {
		return liveAnalysisPayload{}
	}
	var previous liveAnalysisPayload
	if err := json.Unmarshal(previousPayload, &previous); err != nil {
		return liveAnalysisPayload{}
	}
	previous.Summary = strings.TrimSpace(previous.Summary)
	previous.CurrentTopic = strings.TrimSpace(previous.CurrentTopic)
	return previous
}

// parseAndMergeLiveAnalysisPayload parses the model output as a diff (only
// new/changed items, new/changed tree nodes, new edges, and resolvedIds) and
// merges it into the previous payload, producing the complete state that is
// stored and broadcast. The model only reports changes; the server owns
// state retention, so weak-reasoning models cannot lose accumulated items by
// echoing a stale snapshot.
func parseAndMergeLiveAnalysisPayload(content string, previousPayload json.RawMessage) (json.RawMessage, error) {
	cleaned := stripJSONCodeFence(content)
	var diff liveAnalysisPayload
	if err := json.Unmarshal([]byte(cleaned), &diff); err != nil {
		return nil, fmt.Errorf("parse live analysis payload: %w", err)
	}
	previous := previousLiveAnalysisState(previousPayload)

	resolvedIDs := make(map[string]struct{}, len(diff.ResolvedIds))
	for _, id := range diff.ResolvedIds {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			resolvedIDs[trimmed] = struct{}{}
		}
	}
	diffItems := normalizeLiveAnalysisItems(diff.Items, resolvedIDs)

	merged := liveAnalysisPayload{
		Summary:      firstNonEmptyTrimmed(diff.Summary, previous.Summary),
		CurrentTopic: firstNonEmptyTrimmed(diff.CurrentTopic, previous.CurrentTopic),
	}
	merged.Items = mergeLiveAnalysisItems(previous.Items, diffItems, resolvedIDs)
	merged.Tree = mergeLiveAnalysisTree(previous.Tree, diff.Tree, resolvedIDs, merged.CurrentTopic)
	if merged.isEmpty() {
		return nil, fmt.Errorf("live analysis payload is empty")
	}
	if merged.Items == nil {
		merged.Items = []liveAnalysisItem{}
	}
	normalized, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshal normalized live analysis payload: %w", err)
	}
	return normalized, nil
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

type finalAnalysisDecisionItem struct {
	Text       string `json:"text"`
	Importance string `json:"importance"`
}

type finalAnalysisActionItem struct {
	Text     string `json:"text"`
	Owner    string `json:"owner"`
	Due      string `json:"due"`
	Priority string `json:"priority"`
}

type finalAnalysisPayload struct {
	SuggestedTitle    string                      `json:"suggestedTitle"`
	Overview          string                      `json:"overview"`
	Decisions         []finalAnalysisDecisionItem `json:"decisions"`
	ActionItems       []finalAnalysisActionItem   `json:"actionItems"`
	OpenIssues        []string                    `json:"openIssues"`
	KeyPoints         []string                    `json:"keyPoints"`
	NextMeetingTopics []string                    `json:"nextMeetingTopics"`
}

func (p finalAnalysisPayload) isEmpty() bool {
	return strings.TrimSpace(p.SuggestedTitle) == "" && strings.TrimSpace(p.Overview) == "" &&
		len(p.Decisions) == 0 && len(p.ActionItems) == 0 && len(p.OpenIssues) == 0 &&
		len(p.KeyPoints) == 0 && len(p.NextMeetingTopics) == 0
}

func parseAndValidateFinalAnalysisPayload(content string) (json.RawMessage, error) {
	cleaned := stripJSONCodeFence(content)
	var payload finalAnalysisPayload
	if err := json.Unmarshal([]byte(cleaned), &payload); err != nil {
		return nil, fmt.Errorf("parse final analysis payload: %w", err)
	}
	if payload.isEmpty() {
		return nil, fmt.Errorf("final analysis payload is empty")
	}
	normalized, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal normalized final analysis payload: %w", err)
	}
	return normalized, nil
}

// stripJSONCodeFence removes ```json ... ``` style code fences that models
// sometimes add despite being asked for a bare JSON object, and trims any
// leading/trailing text outside the outermost braces.
func stripJSONCodeFence(content string) string {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```json")
		trimmed = strings.TrimPrefix(trimmed, "```JSON")
		trimmed = strings.TrimPrefix(trimmed, "```")
		if idx := strings.LastIndex(trimmed, "```"); idx >= 0 {
			trimmed = trimmed[:idx]
		}
		trimmed = strings.TrimSpace(trimmed)
	}
	if start := strings.Index(trimmed, "{"); start > 0 {
		trimmed = trimmed[start:]
	}
	if end := strings.LastIndex(trimmed, "}"); end >= 0 && end < len(trimmed)-1 {
		trimmed = trimmed[:end+1]
	}
	return strings.TrimSpace(trimmed)
}
