package application

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
)

//go:embed testdata/qualityeval/scenarios.json
var qualityServiceScenarioFixture []byte

const (
	qualityServiceContextDeployment = "quality-service-context-planner"
	qualityServiceLiveDeployment    = "quality-service-live-extraction"
	qualityServiceTreeDeployment    = "quality-service-tree-reorganizer"
	qualityServiceFinalDeployment   = "quality-service-final-summary"

	qualityServiceFinalResponse = `{"suggestedTitle":"品質評価会議","overview":"固定応答による最終要約","decisions":[],"actionItems":[],"openIssues":[],"keyPoints":[],"nextMeetingTopics":[]}`
	qualityServiceTreeResponse  = `{"operations":[]}`
)

// qualityServiceTranscriptRepository is deliberately a port-level fake. It
// behaves like the durable transcript adapter: final rows are de-duplicated by
// session/call/sequence and every read returns a detached, ordered copy.
type qualityServiceTranscriptRepository struct {
	mu       sync.Mutex
	segments map[string]domain.TranscriptSegment
}

func newQualityServiceTranscriptRepository() *qualityServiceTranscriptRepository {
	return &qualityServiceTranscriptRepository{segments: make(map[string]domain.TranscriptSegment)}
}

func qualityServiceSegmentKey(segment domain.TranscriptSegment) string {
	return fmt.Sprintf("%s\x00%s\x00%d", segment.SessionID, segment.CallID, segment.SequenceNo)
}

func (r *qualityServiceTranscriptRepository) SaveTranscriptSegment(_ context.Context, segment domain.TranscriptSegment) (domain.TranscriptSegmentStoreResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := qualityServiceSegmentKey(segment)
	if existing, ok := r.segments[key]; ok {
		return domain.TranscriptSegmentStoreResult{Status: domain.TranscriptSegmentAlreadyExists, EventID: existing.EventID}, nil
	}
	r.segments[key] = segment
	return domain.TranscriptSegmentStoreResult{Status: domain.TranscriptSegmentCreated, EventID: segment.EventID}, nil
}

func (r *qualityServiceTranscriptRepository) ListTranscriptSegments(_ context.Context, callID, sessionID string, limit int) ([]domain.TranscriptSegment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]domain.TranscriptSegment, 0, len(r.segments))
	for _, segment := range r.segments {
		if sessionID != "" && segment.SessionID != sessionID {
			continue
		}
		if callID != "" && segment.CallID != callID {
			continue
		}
		result = append(result, segment)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SequenceNo != result[j].SequenceNo {
			return result[i].SequenceNo < result[j].SequenceNo
		}
		return result[i].CallID < result[j].CallID
	})
	if limit > 0 && len(result) > limit {
		result = append([]domain.TranscriptSegment(nil), result[len(result)-limit:]...)
	}
	return append([]domain.TranscriptSegment(nil), result...), nil
}

type qualityServiceAnalysisWrite struct {
	Analysis domain.MeetingAIAnalysis
	CAS      bool
}

// qualityServiceAnalysisRepository models the current-row and live-history
// contracts, including the production CAS rule. Payloads are copied at every
// boundary so concurrent test goroutines cannot share mutable backing arrays.
type qualityServiceAnalysisRepository struct {
	mu      sync.Mutex
	rows    map[string]domain.MeetingAIAnalysis
	history map[string]map[int64]domain.MeetingAIAnalysis
	writes  chan qualityServiceAnalysisWrite
}

func newQualityServiceAnalysisRepository() *qualityServiceAnalysisRepository {
	return &qualityServiceAnalysisRepository{
		rows:    make(map[string]domain.MeetingAIAnalysis),
		history: make(map[string]map[int64]domain.MeetingAIAnalysis),
		writes:  make(chan qualityServiceAnalysisWrite, 256),
	}
}

func qualityServiceAnalysisKey(sessionID string, analysisType domain.MeetingAIAnalysisType) string {
	return sessionID + "\x00" + string(analysisType)
}

func cloneQualityServiceAnalysis(analysis domain.MeetingAIAnalysis) domain.MeetingAIAnalysis {
	analysis.Payload = append(json.RawMessage(nil), analysis.Payload...)
	return analysis
}

func (r *qualityServiceAnalysisRepository) storeLocked(analysis domain.MeetingAIAnalysis) domain.MeetingAIAnalysis {
	analysis = cloneQualityServiceAnalysis(analysis)
	key := qualityServiceAnalysisKey(analysis.SessionID, analysis.Type)
	if existing, ok := r.rows[key]; ok && analysis.CreatedAt.IsZero() {
		analysis.CreatedAt = existing.CreatedAt
	}
	if analysis.CreatedAt.IsZero() {
		analysis.CreatedAt = analysis.UpdatedAt
	}
	r.rows[key] = analysis
	return cloneQualityServiceAnalysis(analysis)
}

func (r *qualityServiceAnalysisRepository) emit(write qualityServiceAnalysisWrite) {
	write.Analysis = cloneQualityServiceAnalysis(write.Analysis)
	r.writes <- write
}

func (r *qualityServiceAnalysisRepository) UpsertMeetingAIAnalysis(_ context.Context, analysis domain.MeetingAIAnalysis) (*domain.MeetingAIAnalysis, error) {
	r.mu.Lock()
	stored := r.storeLocked(analysis)
	r.mu.Unlock()
	r.emit(qualityServiceAnalysisWrite{Analysis: stored})
	return ptrQualityServiceAnalysis(stored), nil
}

func (r *qualityServiceAnalysisRepository) CompareAndSwapMeetingAIAnalysis(_ context.Context, expectedVersion int64, analysis domain.MeetingAIAnalysis) (*domain.MeetingAIAnalysis, bool, error) {
	r.mu.Lock()
	current, exists := r.rows[qualityServiceAnalysisKey(analysis.SessionID, analysis.Type)]
	if (exists && current.Version != expectedVersion) || (!exists && expectedVersion != 0) {
		current = cloneQualityServiceAnalysis(current)
		r.mu.Unlock()
		if !exists {
			return nil, false, nil
		}
		return ptrQualityServiceAnalysis(current), false, nil
	}
	stored := r.storeLocked(analysis)
	r.mu.Unlock()
	r.emit(qualityServiceAnalysisWrite{Analysis: stored, CAS: true})
	return ptrQualityServiceAnalysis(stored), true, nil
}

func ptrQualityServiceAnalysis(analysis domain.MeetingAIAnalysis) *domain.MeetingAIAnalysis {
	copy := cloneQualityServiceAnalysis(analysis)
	return &copy
}

func (r *qualityServiceAnalysisRepository) GetMeetingAIAnalysis(_ context.Context, sessionID string, analysisType domain.MeetingAIAnalysisType) (*domain.MeetingAIAnalysis, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	analysis, ok := r.rows[qualityServiceAnalysisKey(sessionID, analysisType)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return ptrQualityServiceAnalysis(analysis), nil
}

func (r *qualityServiceAnalysisRepository) ListMeetingAIAnalysesForSessions(_ context.Context, sessionIDs []string, analysisType domain.MeetingAIAnalysisType) ([]domain.MeetingAIAnalysis, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]domain.MeetingAIAnalysis, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		if analysis, ok := r.rows[qualityServiceAnalysisKey(sessionID, analysisType)]; ok {
			result = append(result, cloneQualityServiceAnalysis(analysis))
		}
	}
	return result, nil
}

func (r *qualityServiceAnalysisRepository) AppendLiveAnalysisHistory(_ context.Context, analysis domain.MeetingAIAnalysis) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	versions := r.history[analysis.SessionID]
	if versions == nil {
		versions = make(map[int64]domain.MeetingAIAnalysis)
		r.history[analysis.SessionID] = versions
	}
	versions[analysis.Version] = cloneQualityServiceAnalysis(analysis)
	return nil
}

func (r *qualityServiceAnalysisRepository) ListLiveAnalysisHistory(_ context.Context, sessionID string, limit int) ([]domain.MeetingAIAnalysis, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	versions := r.history[sessionID]
	keys := make([]int64, 0, len(versions))
	for version := range versions {
		keys = append(keys, version)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	if limit > 0 && len(keys) > limit {
		keys = keys[len(keys)-limit:]
	}
	result := make([]domain.MeetingAIAnalysis, 0, len(keys))
	for _, version := range keys {
		result = append(result, cloneQualityServiceAnalysis(versions[version]))
	}
	return result, nil
}

type qualityServiceAIRequest struct {
	Deployment string
	Request    AIChatRequest
	TaskCall   int
}

// qualityServiceCompleter routes fixed responses by task deployment, never by
// global call order. Only the first live extraction can be gated.
type qualityServiceCompleter struct {
	mu                   sync.Mutex
	responses            map[string][]string
	calls                map[string]int
	requests             []qualityServiceAIRequest
	unexpected           []string
	firstLiveStarted     chan struct{}
	releaseFirstLive     chan struct{}
	gateFirstLive        bool
	firstLiveStartedOnce sync.Once
	releaseFirstLiveOnce sync.Once
	firstLiveReleased    bool
	startedBeforeRelease []string
}

func newQualityServiceCompleter(liveResponses []string) *qualityServiceCompleter {
	return &qualityServiceCompleter{
		responses: map[string][]string{
			qualityServiceLiveDeployment:  append([]string(nil), liveResponses...),
			qualityServiceTreeDeployment:  {qualityServiceTreeResponse},
			qualityServiceFinalDeployment: {qualityServiceFinalResponse},
		},
		calls:            make(map[string]int),
		firstLiveStarted: make(chan struct{}),
		releaseFirstLive: make(chan struct{}),
	}
}

func (c *qualityServiceCompleter) withContextResponse(response string) *qualityServiceCompleter {
	c.responses[qualityServiceContextDeployment] = []string{response}
	return c
}

func (c *qualityServiceCompleter) blockFirstLive() *qualityServiceCompleter {
	c.gateFirstLive = true
	return c
}

func (c *qualityServiceCompleter) Complete(ctx context.Context, request AIChatRequest) (AIChatResult, error) {
	c.mu.Lock()
	c.calls[request.Deployment]++
	taskCall := c.calls[request.Deployment]
	c.requests = append(c.requests, qualityServiceAIRequest{Deployment: request.Deployment, Request: request, TaskCall: taskCall})
	responses, known := c.responses[request.Deployment]
	gate := c.gateFirstLive && request.Deployment == qualityServiceLiveDeployment && taskCall == 1
	if c.gateFirstLive && !c.firstLiveReleased && !gate {
		c.startedBeforeRelease = append(c.startedBeforeRelease, request.Deployment)
	}
	if !known || len(responses) == 0 {
		c.unexpected = append(c.unexpected, request.Deployment)
	}
	c.mu.Unlock()
	if !known || len(responses) == 0 {
		return AIChatResult{}, fmt.Errorf("unexpected quality integration AI deployment %q", request.Deployment)
	}
	if gate {
		c.firstLiveStartedOnce.Do(func() { close(c.firstLiveStarted) })
		select {
		case <-c.releaseFirstLive:
		case <-ctx.Done():
			return AIChatResult{}, ctx.Err()
		}
	}
	index := taskCall - 1
	if index >= len(responses) {
		index = len(responses) - 1
	}
	return AIChatResult{Content: responses[index], Model: request.Deployment}, nil
}

func (c *qualityServiceCompleter) releaseLive() {
	c.releaseFirstLiveOnce.Do(func() {
		c.mu.Lock()
		c.firstLiveReleased = true
		c.mu.Unlock()
		close(c.releaseFirstLive)
	})
}

func (c *qualityServiceCompleter) callCount(deployment string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[deployment]
}

func (c *qualityServiceCompleter) taskRequests(deployment string) []qualityServiceAIRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]qualityServiceAIRequest, 0, c.calls[deployment])
	for _, request := range c.requests {
		if request.Deployment == deployment {
			result = append(result, request)
		}
	}
	return result
}

func (c *qualityServiceCompleter) trace() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	trace := make([]string, 0, len(c.requests))
	for _, request := range c.requests {
		trace = append(trace, fmt.Sprintf("%s#%d", request.Deployment, request.TaskCall))
	}
	return trace
}

func (c *qualityServiceCompleter) assertClean(t *testing.T) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.unexpected) != 0 {
		t.Fatalf("unexpected AI task routes = %v", c.unexpected)
	}
}

func qualityServiceConfig() MeetingAnalysisConfig {
	return MeetingAnalysisConfig{
		Enabled:                 true,
		LiveEnabled:             true,
		LiveInterval:            time.Hour,
		LiveDebounce:            time.Nanosecond,
		LiveCooldown:            time.Nanosecond,
		LiveMaxWait:             time.Nanosecond,
		LiveMinChars:            1,
		LiveMaxInputChars:       12000,
		LiveRequestTimeout:      5 * time.Second,
		ContextWaitTimeout:      5 * time.Second,
		ContextRequestTimeout:   5 * time.Second,
		FinalEnabled:            true,
		FinalMaxInputChars:      12000,
		FinalRequestTimeout:     5 * time.Second,
		FinalizationWaitTimeout: 5 * time.Second,
		FinalizationQuietPeriod: time.Nanosecond,
		FinalFlushMaxAttempts:   4,
		ReorganizeMinInterval:   time.Nanosecond,
		Model:                   "quality-service-default",
		TaskModels: AITaskModels{
			ContextPlanner:  qualityServiceContextDeployment,
			LiveExtraction:  qualityServiceLiveDeployment,
			TreeReorganizer: qualityServiceTreeDeployment,
			FinalSummary:    qualityServiceFinalDeployment,
		},
	}
}

type qualityServiceHarness struct {
	repo       *qualityServiceAnalysisRepository
	transcript *qualityServiceTranscriptRepository
	completer  *qualityServiceCompleter
	service    *MeetingAnalysisService
	ctx        context.Context
	cancel     context.CancelFunc
}

func newQualityServiceHarness(t *testing.T, liveResponses []string) *qualityServiceHarness {
	t.Helper()
	repo := newQualityServiceAnalysisRepository()
	transcript := newQualityServiceTranscriptRepository()
	completer := newQualityServiceCompleter(liveResponses)
	service := NewMeetingAnalysisService(repo, transcript, nil, completer, qualityServiceConfig())
	ctx, cancel := context.WithCancel(context.Background())
	harness := &qualityServiceHarness{repo: repo, transcript: transcript, completer: completer, service: service, ctx: ctx, cancel: cancel}
	t.Cleanup(func() {
		completer.releaseLive()
		cancel()
		_ = service.Close()
		completer.assertClean(t)
		t.Logf("AI task call trace=%v", completer.trace())
	})
	return harness
}

func (h *qualityServiceHarness) start() { h.service.Start(h.ctx) }

func qualityServiceLoadScenario(t *testing.T, id string) MeetingQualityScenario {
	t.Helper()
	var suite MeetingQualitySuite
	if err := json.Unmarshal(qualityServiceScenarioFixture, &suite); err != nil {
		t.Fatalf("decode deterministic quality fixture: %v", err)
	}
	for _, scenario := range suite.Scenarios {
		if scenario.ID == id {
			return scenario
		}
	}
	t.Fatalf("quality scenario %q not found", id)
	return MeetingQualityScenario{}
}

func qualityServiceRoundResponses(scenario MeetingQualityScenario) []string {
	responses := make([]string, 0, len(scenario.Rounds))
	for _, round := range scenario.Rounds {
		responses = append(responses, string(round.FixedAIResponse))
	}
	return responses
}

func qualityServiceSameBatchKindScenario() MeetingQualityScenario {
	return MeetingQualityScenario{
		ID:          "service-same-batch-kind-promotion",
		Description: "one service batch promotes an unplanned topic containing all required semantic kinds",
		TranscriptSegments: []MeetingQualityTranscriptSegment{
			{SequenceNo: 1, Speaker: "佐藤", Text: "VPN証明書の有効期限は来週金曜日です。"},
			{SequenceNo: 2, Speaker: "鈴木", Text: "更新しなければ取引先への接続が停止する可能性があります。"},
			{SequenceNo: 3, Speaker: "司会", Text: "VPN証明書を今週中に更新することを決定しました。"},
			{SequenceNo: 4, Speaker: "田中", Text: "田中が木曜日までにVPN証明書の更新手順を確認します。"},
		},
		Rounds: []MeetingQualityRound{{
			SequenceNos: []int64{1, 2, 3, 4},
			FixedAIResponse: json.RawMessage(`{
				"summary":"VPN証明書更新",
				"currentTopic":"VPN証明書更新",
				"items":[
					{"clientKey":"certificate-expiry-fact","kind":"fact","severity":"high","title":"VPN証明書の有効期限は来週金曜日","body":"有効期限は来週金曜日","status":"open","evidenceSequenceNos":[1],"evidenceSnippets":["VPN証明書の有効期限は来週金曜日です"]},
					{"clientKey":"certificate-outage-risk","kind":"risk","severity":"high","title":"未更新なら取引先への接続が停止するリスク","body":"更新しなければ取引先への接続が停止する可能性がある","status":"open","evidenceSequenceNos":[2],"evidenceSnippets":["更新しなければ取引先への接続が停止する可能性があります"]},
					{"clientKey":"certificate-update-decision","kind":"decision","severity":"high","title":"VPN証明書を今週中に更新する","body":"今週中の更新を決定した","status":"open","evidenceSequenceNos":[3],"evidenceSnippets":["VPN証明書を今週中に更新することを決定しました"]},
					{"clientKey":"certificate-procedure-todo","kind":"todo","severity":"high","title":"田中が木曜日までに更新手順を確認する","body":"VPN証明書の更新手順を確認する","status":"open","evidenceSequenceNos":[4],"evidenceSnippets":["田中が木曜日までにVPN証明書の更新手順を確認します"]}
				],
				"newTopics":[{"id":"topic-certificate-service","label":"VPN証明書更新","description":"期限、接続リスク、更新方針、担当作業"}],
				"assignments":[
					{"nodeId":"certificate-expiry-fact","parentTopicId":"topic-certificate-service","confidence":0.96},
					{"nodeId":"certificate-outage-risk","parentTopicId":"topic-certificate-service","confidence":0.96},
					{"nodeId":"certificate-update-decision","parentTopicId":"topic-certificate-service","confidence":0.96},
					{"nodeId":"certificate-procedure-todo","parentTopicId":"topic-certificate-service","confidence":0.96}
				]
			}`),
		}},
		RequiredPropositions: []MeetingQualityProposition{
			{ID: "certificate-expiry-fact", Text: "VPN証明書の有効期限は来週金曜日", RequiredKind: "fact", EvidenceSequenceNos: []int64{1}},
			{ID: "certificate-outage-risk", Text: "未更新なら取引先への接続が停止する可能性", RequiredKind: "risk", EvidenceSequenceNos: []int64{2}},
			{ID: "certificate-update-decision", Text: "VPN証明書を今週中に更新することを決定した", RequiredKind: "decision", EvidenceSequenceNos: []int64{3}},
			{ID: "certificate-procedure-todo", Text: "田中が木曜日までにVPN証明書の更新手順を確認する", RequiredKind: "todo", EvidenceSequenceNos: []int64{4}},
		},
		ForbiddenResults: []MeetingQualityForbiddenResult{{Type: "active_candidate", Text: "VPN証明書更新"}},
		FinalCoverage:    4,
		ApplyFinalRepair: true,
	}
}

func qualityServiceFallbackRelationScenario() MeetingQualityScenario {
	return MeetingQualityScenario{
		ID:          "service-label-fallback-and-logical-relations",
		Description: "fallback labels and logical relations survive final persistence",
		TranscriptSegments: []MeetingQualityTranscriptSegment{
			{SequenceNo: 1, Speaker: "佐藤", Text: "VPN証明書は来週失効します。"},
			{SequenceNo: 2, Speaker: "佐藤", Text: "放置すると全社員がリモート接続できなくなる可能性があります。"},
			{SequenceNo: 3, Speaker: "高橋", Text: "高橋が金曜日までにVPN証明書の更新手順を確認します。"},
			{SequenceNo: 4, Speaker: "田中", Text: "交換後スイッチの許可VLAN一覧からVLAN30が漏れていました。"},
			{SequenceNo: 5, Speaker: "佐藤", Text: "現時点では、この設定漏れが3階障害の直接原因である可能性が最も高いと考えています。"},
			{SequenceNo: 6, Speaker: "鈴木", Text: "ただし2階の通信遅延までこの設定漏れで説明できるかは未確認です。"},
		},
		Rounds: []MeetingQualityRound{{
			SequenceNos: []int64{1, 2, 3, 4, 5, 6},
			FixedAIResponse: json.RawMessage(`{
				"summary":"VPN証明書リスクとネットワーク障害原因",
				"currentTopic":"障害原因と適用範囲",
				"items":[
					{"clientKey":"vpn-risk","kind":"risk","severity":"high","title":"VPN証明書を放置すると全社員がリモート接続できてい","body":"VPN証明書を放置すると全社員がリモート接続できてい","status":"open","evidenceSequenceNos":[1,2]},
					{"clientKey":"vpn-todo","kind":"todo","severity":"high","title":"高橋が金曜日までにVPN証明書の更新手順を確認する","body":"高橋が金曜日までにVPN証明書の更新手順を確認する","status":"open","evidenceSequenceNos":[3]},
					{"clientKey":"vlan-fact","kind":"fact","severity":"high","title":"交換後スイッチの許可VLAN一覧からVLAN30が漏れていた","body":"交換後スイッチの許可VLAN一覧からVLAN30が漏れていた","status":"open","evidenceSequenceNos":[4]},
					{"clientKey":"cause-hypothesis","kind":"issue","subtype":"investigation","severity":"high","title":"現時点では、この設定漏れが3階障害の","body":"現時点では、この設定漏れが3階障害の","status":"open","evidenceSequenceNos":[4,5]},
					{"clientKey":"scope-limit","kind":"issue","subtype":"confirmation","severity":"high","title":"2階の通信遅延までこの設定漏れで説明できるかは未確認","body":"2階の通信遅延までこの設定漏れで説明できるかは未確認","status":"open","evidenceSequenceNos":[5,6]}
				],
				"newTopics":[
					{"id":"topic-vpn-fallback","label":"VPN証明書失効","description":"失効リスクと更新確認"},
					{"id":"topic-network-relations","label":"障害原因と適用範囲","description":"VLAN設定漏れの原因仮説と未確認範囲"}
				],
				"assignments":[
					{"nodeId":"vpn-risk","parentTopicId":"topic-vpn-fallback","confidence":0.96},
					{"nodeId":"vpn-todo","parentTopicId":"topic-vpn-fallback","confidence":0.96},
					{"nodeId":"vlan-fact","parentTopicId":"topic-network-relations","confidence":0.96},
					{"nodeId":"cause-hypothesis","parentTopicId":"topic-network-relations","confidence":0.96},
					{"nodeId":"scope-limit","parentTopicId":"topic-network-relations","confidence":0.96}
				]
			}`),
		}},
		RequiredPropositions: []MeetingQualityProposition{
			{ID: "vpn-risk", Text: "VPN証明書失効により全社員がリモート接続できなくなるリスク", RequiredKind: "risk", EvidenceSequenceNos: []int64{1, 2}},
			{ID: "vpn-todo", Text: "高橋が金曜日までにVPN証明書の更新手順を確認する", RequiredKind: "todo", EvidenceSequenceNos: []int64{3}},
			{ID: "vlan-fact", Text: "交換後スイッチの許可VLAN一覧からVLAN30が漏れていた", RequiredKind: "fact", EvidenceSequenceNos: []int64{4}},
			{ID: "cause-hypothesis", Text: "VLAN30設定漏れが3階障害の直接原因である可能性が高い", RequiredKind: "issue", EvidenceSequenceNos: []int64{5}},
			{ID: "scope-limit", Text: "2階の通信遅延までこの設定漏れで説明できるかは未確認", RequiredKind: "issue", EvidenceSequenceNos: []int64{6}},
		},
		RequiredRelations: []MeetingQualityRelation{
			{From: "cause-hypothesis", To: "vlan-fact", Kind: itemRelationSupportedBy, RequireSameBranch: true},
			{From: "scope-limit", To: "cause-hypothesis", Kind: itemRelationLimits, RequireSameBranch: true},
		},
		FinalCoverage:    6,
		ApplyFinalRepair: true,
	}
}

func qualityServiceCorrectionTemporalScenario() MeetingQualityScenario {
	return MeetingQualityScenario{
		ID:          "service-correction-temporal-lifecycle",
		Description: "self-contained correction and temporal lifecycle survive service finalization",
		TranscriptSegments: []MeetingQualityTranscriptSegment{
			{SequenceNo: 1, Speaker: "田中", Text: "完全なアクセスポート設定ではありません。トランク設定自体は入っていましたが、許可VLAN一覧からVLAN30が漏れていました。"},
			{SequenceNo: 2, Speaker: "佐藤", Text: "現時点では、この設定漏れが3階障害の直接原因である可能性が最も高いです。"},
			{SequenceNo: 3, Speaker: "鈴木", Text: "ただし、2階の通信遅延までこの設定漏れで説明できるかは未確認です。"},
			{SequenceNo: 4, Speaker: "田中", Text: "インターネットが完全に停止したわけではなく、接続できる端末と接続できない端末が混在していました。"},
			{SequenceNo: 5, Speaker: "佐藤", Text: "午前10時5分に有線LAN、無線LAN、ファイルサーバーへの接続が正常になりました。"},
			{SequenceNo: 6, Speaker: "田中", Text: "現在も一部端末で接続できません。原因はまだ分かっておらず、調査が必要です。"},
			{SequenceNo: 7, Speaker: "佐藤", Text: "設定を修正し、すべての端末で接続を確認しました。"},
		},
		Rounds: []MeetingQualityRound{
			{
				SequenceNos: []int64{1, 2, 3, 4, 5, 6},
				FixedAIResponse: json.RawMessage(`{
					"summary":"VLAN設定漏れと接続状態の確認",
					"currentTopic":"障害原因と接続状態",
					"items":[
						{"clientKey":"cause-hypothesis","kind":"issue","subtype":"investigation","severity":"high","title":"現時点では、この設定漏れが3階障害の","body":"現時点では、この設定漏れが3階障害の","status":"open","evidenceSequenceNos":[1,2],"evidenceSnippets":["この設定漏れが3階障害の直接原因である可能性が最も高い"]},
						{"clientKey":"scope-limit","kind":"issue","subtype":"confirmation","severity":"medium","title":"2階の通信遅延までこの設定漏れで説明できるかは未確認","body":"2階の通信遅延までこの設定漏れで説明できるかは未確認","status":"open","evidenceSequenceNos":[3],"evidenceSnippets":["2階の通信遅延までこの設定漏れで説明できるかは未確認"]},
						{"clientKey":"historical-connectivity","kind":"issue","subtype":"discussion","severity":"medium","title":"障害時に接続できる端末と接続できない端末が混在","body":"インターネットが完全に停止したわけではなく、接続できる端末と接続できない端末が混在していました","status":"open","evidenceSequenceNos":[4],"evidenceSnippets":["接続できる端末と接続できない端末が混在していました"]},
						{"clientKey":"historical-recovery","kind":"fact","subtype":"","severity":"medium","title":"午前10時5分に有線LAN・無線LAN・ファイルサーバー接続が正常化","body":"午前10時5分に有線LAN、無線LAN、ファイルサーバーへの接続が正常になりました","status":"open","evidenceSequenceNos":[5],"evidenceSnippets":["午前10時5分に有線LAN、無線LAN、ファイルサーバーへの接続が正常になりました"]},
						{"clientKey":"current-connectivity","kind":"issue","subtype":"investigation","severity":"high","title":"現在も一部端末で接続できない","body":"現在も一部端末で接続できず、原因はまだ分かっていないため調査が必要","status":"open","evidenceSequenceNos":[6],"evidenceSnippets":["現在も一部端末で接続できません。原因はまだ分かっておらず、調査が必要です"]}
					],
					"newTopics":[
						{"id":"topic-network-cause","label":"VLAN設定漏れの原因と範囲","description":"確認済み設定、原因仮説、未確認範囲"},
						{"id":"topic-connectivity-state","label":"端末接続状態の時系列","description":"過去観測、復旧、現在の問題"}
					],
					"assignments":[
						{"nodeId":"cause-hypothesis","parentTopicId":"topic-network-cause","confidence":0.96},
						{"nodeId":"scope-limit","parentTopicId":"topic-network-cause","confidence":0.96},
						{"nodeId":"historical-connectivity","parentTopicId":"topic-connectivity-state","confidence":0.96},
						{"nodeId":"historical-recovery","parentTopicId":"topic-connectivity-state","confidence":0.96},
						{"nodeId":"current-connectivity","parentTopicId":"topic-connectivity-state","confidence":0.96}
					],
					"resolvedIds":[],
					"resolutionUpdates":[{"itemId":"historical-connectivity","status":"resolved","evidenceSequenceNos":[5],"reason":"接続が正常になった"}],
					"utteranceRoles":[]
				}`),
			},
			{
				SequenceNos: []int64{7},
				FixedAIResponse: json.RawMessage(`{
					"summary":"全端末の接続を確認",
					"currentTopic":"接続復旧",
					"items":[],
					"newTopics":[],
					"assignments":[],
					"resolvedIds":[],
					"resolutionUpdates":[{"itemId":"item-issue-investigation-6be3d76c146b","status":"resolved","evidenceSequenceNos":[7],"reason":"設定修正後にすべての端末で接続を確認した"}],
					"utteranceRoles":[]
				}`),
			},
		},
		RequiredPropositions: []MeetingQualityProposition{
			{ID: "vlan-fact", Text: "許可VLAN一覧からVLAN30が漏れていた", RequiredKind: "fact", EvidenceSequenceNos: []int64{1}, RequiredTemporalScope: "past", RequiredEpistemicStatus: "confirmed", RequiredStatus: "open"},
			{ID: "cause-hypothesis", Text: "VLAN30設定漏れが3階障害の直接原因である可能性が高い", RequiredKind: "issue", EvidenceSequenceNos: []int64{2}, RequiredEpistemicStatus: "hypothesis", RequiredStatus: "open"},
			{ID: "scope-limit", Text: "2階の通信遅延までVLAN30設定漏れで説明できるかは未確認", RequiredKind: "issue", EvidenceSequenceNos: []int64{3}, RequiredEpistemicStatus: "unresolved", RequiredStatus: "open"},
			{ID: "historical-connectivity", Text: "障害時に接続できる端末と接続できない端末が混在していた", RequiredKind: "fact", EvidenceSequenceNos: []int64{4}, RequiredTemporalScope: "past", RequiredEpistemicStatus: "confirmed", RequiredStatus: "open"},
			{ID: "historical-recovery", Text: "午前10時5分に有線LAN、無線LAN、ファイルサーバーへの接続が正常になった", RequiredKind: "fact", EvidenceSequenceNos: []int64{5}, RequiredTemporalScope: "past", RequiredEpistemicStatus: "confirmed", RequiredStatus: "open"},
			{ID: "current-connectivity", Text: "現在も一部端末で接続できず原因不明で調査が必要", RequiredKind: "issue", EvidenceSequenceNos: []int64{6}, RequiredTemporalScope: "current", RequiredEpistemicStatus: "unresolved", RequiredStatus: "resolved"},
		},
		RequiredRelations: []MeetingQualityRelation{
			{From: "cause-hypothesis", To: "vlan-fact", Kind: itemRelationSupportedBy},
			{From: "scope-limit", To: "cause-hypothesis", Kind: itemRelationLimits},
		},
		FinalCoverage:    7,
		ApplyFinalRepair: true,
	}
}

func qualityServiceDomainSegments(sessionID string, scenario MeetingQualityScenario) []domain.TranscriptSegment {
	segments := make([]domain.TranscriptSegment, 0, len(scenario.TranscriptSegments))
	for _, fixture := range scenario.TranscriptSegments {
		callID := fixture.CallID
		if callID == "" {
			callID = "quality-call"
		}
		segments = append(segments, domain.TranscriptSegment{
			SessionID: sessionID, EventID: fmt.Sprintf("quality-event-%d", fixture.SequenceNo),
			CallID: callID, SequenceNo: fixture.SequenceNo, SpeakerName: fixture.Speaker,
			Text: fixture.Text, IsFinal: fixture.IsFinal == nil || *fixture.IsFinal,
		})
	}
	return segments
}

func qualityServiceSaveDurable(t *testing.T, repo TranscriptSegmentRepository, segments []domain.TranscriptSegment) {
	t.Helper()
	for _, segment := range segments {
		result, err := repo.SaveTranscriptSegment(context.Background(), segment)
		if err != nil || result.Status != domain.TranscriptSegmentCreated {
			t.Fatalf("save durable transcript seq=%d: result=%+v err=%v", segment.SequenceNo, result, err)
		}
	}
}

func qualityServiceIngest(t *testing.T, ingest *TranscriptIngestService, segment domain.TranscriptSegment) {
	t.Helper()
	result, err := ingest.StoreTranscriptSegment(context.Background(), segment)
	if err != nil || result.Status != domain.TranscriptSegmentCreated {
		t.Fatalf("ingest transcript seq=%d: result=%+v err=%v", segment.SequenceNo, result, err)
	}
}

func qualityServiceWaitWrite(t *testing.T, repo *qualityServiceAnalysisRepository, analysisType domain.MeetingAIAnalysisType, status domain.MeetingAIAnalysisStatus, minimumVersion int64) domain.MeetingAIAnalysis {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case write := <-repo.writes:
			if write.Analysis.Type == analysisType && write.Analysis.Status == status && write.Analysis.Version >= minimumVersion {
				return write.Analysis
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for analysis write type=%s status=%s version>=%d", analysisType, status, minimumVersion)
		}
	}
}

func qualityServiceFinalize(t *testing.T, service *MeetingAnalysisService, sessionID string, target int64) {
	t.Helper()
	if err := service.FinalizeMeetingSession(context.Background(), domain.MeetingSession{ID: sessionID}, MeetingSessionFinalizationRequest{
		BotLastForwardedFinalSequence: target,
		TranscriptQueueDrained:        true,
	}); err != nil {
		t.Fatalf("FinalizeMeetingSession() error = %v", err)
	}
}

func qualityServiceReload(t *testing.T, h *qualityServiceHarness, sessionID string) *MeetingAIAnalysesSnapshot {
	t.Helper()
	reader := NewMeetingAnalysisService(h.repo, h.transcript, nil, h.completer, qualityServiceConfig())
	snapshot, err := reader.GetMeetingAIAnalyses(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("fresh GetMeetingAIAnalyses() error = %v", err)
	}
	if snapshot.Live == nil || snapshot.Tree == nil || snapshot.Final == nil || snapshot.Finalization == nil {
		t.Fatalf("reloaded snapshot is incomplete: live=%t tree=%t final=%t finalization=%t",
			snapshot.Live != nil, snapshot.Tree != nil, snapshot.Final != nil, snapshot.Finalization != nil)
	}
	return snapshot
}

func qualityServiceAssertPersistence(t *testing.T, snapshot *MeetingAIAnalysesSnapshot, target int64) (liveAnalysisPayload, treeSnapshotPayload) {
	t.Helper()
	if snapshot.Live.Status != domain.MeetingAIAnalysisCompleted || snapshot.Tree.Status != domain.MeetingAIAnalysisCompleted ||
		snapshot.Final.Status != domain.MeetingAIAnalysisCompleted || snapshot.Finalization.Status != domain.MeetingAIAnalysisCompleted {
		t.Fatalf("persisted statuses: live=%s tree=%s final=%s finalization=%s",
			snapshot.Live.Status, snapshot.Tree.Status, snapshot.Final.Status, snapshot.Finalization.Status)
	}
	var live liveAnalysisPayload
	if err := json.Unmarshal(snapshot.Live.Payload, &live); err != nil {
		t.Fatalf("decode reloaded live payload: %v", err)
	}
	var tree treeSnapshotPayload
	if err := json.Unmarshal(snapshot.Tree.Payload, &tree); err != nil {
		t.Fatalf("decode reloaded tree payload: %v", err)
	}
	if live.CoveredThroughSequenceNo != target || tree.CoveredThroughSequenceNo != target {
		t.Fatalf("coverage live=%d tree=%d, want %d", live.CoveredThroughSequenceNo, tree.CoveredThroughSequenceNo, target)
	}
	if !tree.Final || tree.Tree == nil || live.Tree == nil || snapshot.Live.Version != snapshot.Tree.Version || live.TreeVersion != snapshot.Live.Version {
		t.Fatalf("final snapshot metadata mismatch: liveVersion=%d liveTreeVersion=%d treeVersion=%d final=%t liveTree=%t tree=%t",
			snapshot.Live.Version, live.TreeVersion, snapshot.Tree.Version, tree.Final, live.Tree != nil, tree.Tree != nil)
	}
	if string(snapshot.Live.Payload) == "" || string(snapshot.Tree.Payload) == "" || string(snapshot.Final.Payload) == "" {
		t.Fatal("a persisted final artifact has an empty payload")
	}
	return live, tree
}

func qualityServiceAssertEvaluation(t *testing.T, scenario MeetingQualityScenario, livePayload json.RawMessage) MeetingQualityScenarioResult {
	t.Helper()
	result := EvaluateMeetingQualitySnapshot(scenario, livePayload)
	if !result.Passed {
		t.Fatalf("semantic evaluation failed: missing=%v unsupported=%v hard=%v relations=%v kind=%v evidence=%v error=%s",
			result.MissingRequiredPropositions, result.UnsupportedPropositions, result.HardInvariantViolations,
			result.RelationFailures, result.KindMismatches, result.EvidenceMismatches, result.Error)
	}
	return result
}

func qualityServiceDynamicTopicCount(tree *liveAnalysisTree) int {
	if tree == nil {
		return 0
	}
	count := 0
	for _, node := range tree.Nodes {
		if node.Kind == "topic" && node.Origin == topicOriginDynamic {
			count++
		}
	}
	return count
}

func qualityServiceActiveItemIDs(items []liveAnalysisItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if !item.Inactive && item.MergedIntoID == "" {
			ids = append(ids, item.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func qualityServiceTreeDetailIDs(tree *liveAnalysisTree) []string {
	if tree == nil {
		return nil
	}
	ids := make([]string, 0, len(tree.Nodes))
	for _, node := range tree.Nodes {
		if node.Kind != "topic" && node.Kind != "group" {
			ids = append(ids, node.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func TestMeetingQualityServiceIntegration(t *testing.T) {
	t.Run("A_finalization_waits_for_frozen_inflight_batch_and_flushes_tail", testMeetingQualityServiceIntegrationFinalizationWait)
	t.Run("B_durable_pending_recovery_is_gap_free_and_idempotent", testMeetingQualityServiceIntegrationDurableRecovery)
	t.Run("C_same_batch_promotes_one_dynamic_topic_and_preserves_kinds", testMeetingQualityServiceIntegrationSameBatchPromotion)
	t.Run("D_multiple_rounds_promote_one_dynamic_topic", testMeetingQualityServiceIntegrationMultipleRoundPromotion)
	t.Run("E_final_artifacts_reload_with_agenda_metadata", testMeetingQualityServiceIntegrationReload)
	t.Run("F_stale_live_CAS_cannot_overwrite_final_projection", testMeetingQualityServiceIntegrationStaleCAS)
	t.Run("G_label_fallback_and_relations_survive_fresh_reader", testMeetingQualityServiceIntegrationFallbackRelations)
	t.Run("H_correction_and_temporal_lifecycle_survive_fresh_reader", testMeetingQualityServiceIntegrationCorrectionTemporal)
}

func testMeetingQualityServiceIntegrationCorrectionTemporal(t *testing.T) {
	scenario := qualityServiceCorrectionTemporalScenario()
	h := newQualityServiceHarness(t, qualityServiceRoundResponses(scenario))
	segments := qualityServiceDomainSegments("quality-correction-temporal", scenario)
	qualityServiceSaveDurable(t, h.transcript, segments[:6])
	h.start()
	h.service.PrepareMeetingSession(domain.MeetingSession{ID: segments[0].SessionID})
	qualityServiceWaitWrite(t, h.repo, domain.MeetingAIAnalysisLive, domain.MeetingAIAnalysisCompleted, 1)
	qualityServiceIngest(t, NewTranscriptIngestService(h.transcript, h.service), segments[6])
	qualityServiceWaitWrite(t, h.repo, domain.MeetingAIAnalysisLive, domain.MeetingAIAnalysisCompleted, 2)

	qualityServiceFinalize(t, h.service, segments[0].SessionID, 7)
	snapshot := qualityServiceReload(t, h, segments[0].SessionID)
	live, tree := qualityServiceAssertPersistence(t, snapshot, 7)
	result := qualityServiceAssertEvaluation(t, scenario, snapshot.Live.Payload)
	if live.TreeVersion < 2 || snapshot.Live.Version < 2 {
		t.Fatalf("tree version rolled back: payload=%d row=%d", live.TreeVersion, snapshot.Live.Version)
	}
	if result.Metrics.RequiredPropositionRecall != 1 || result.Metrics.HierarchyRelationAccuracy != 1 ||
		result.Metrics.ClassificationAccuracy != 1 || result.Metrics.TemporalScopeAccuracy != 1 ||
		result.Metrics.PastFactCount < 3 || result.Metrics.ResolvedIssueCount != 1 ||
		result.Metrics.IncorrectResolvedIssueCount != 0 {
		t.Fatalf("correction/temporal quality metrics=%+v", result.Metrics)
	}

	matchedIDs := make(map[string]string, len(result.PropositionMatches))
	for _, match := range result.PropositionMatches {
		if match.Matched && match.BestActualCandidate != nil {
			matchedIDs[match.PropositionID] = match.BestActualCandidate.ID
		}
	}
	active := make(map[string]liveAnalysisItem)
	for _, item := range live.Items {
		if !item.Inactive && item.MergedIntoID == "" {
			active[item.ID] = item
		}
	}
	vlanFact := active[matchedIDs["vlan-fact"]]
	hypothesis := active[matchedIDs["cause-hypothesis"]]
	limit := active[matchedIDs["scope-limit"]]
	historical := active[matchedIDs["historical-connectivity"]]
	current := active[matchedIDs["current-connectivity"]]
	if vlanFact.Kind != "fact" || !strings.Contains(vlanFact.Title, "VLAN30") || vlanFact.Status == "resolved" ||
		hypothesis.Kind != "issue" || limit.Kind != "issue" {
		t.Fatalf("correction structure not preserved: fact=%+v hypothesis=%+v limit=%+v", vlanFact, hypothesis, limit)
	}
	if historical.Kind != "fact" || historical.Status == "resolved" {
		t.Fatalf("historical observation was not retained as an open fact: %+v", historical)
	}
	if current.Kind != "issue" || current.Status != "resolved" ||
		!containsInt64(current.ResolutionEvidenceSequenceNos, 7) {
		t.Fatalf("current issue did not resolve from explicit later evidence: %+v", current)
	}

	relationKeys := make(map[string]bool, len(live.Tree.Relations))
	for _, relation := range live.Tree.Relations {
		if _, ok := active[relation.Source]; !ok {
			t.Fatalf("relation source is not active: %+v", relation)
		}
		if _, ok := active[relation.Target]; !ok {
			t.Fatalf("relation target is not active: %+v", relation)
		}
		relationKeys[relationKey(relation)] = true
	}
	for _, key := range []string{
		matchedIDs["cause-hypothesis"] + "\x00" + itemRelationSupportedBy + "\x00" + matchedIDs["vlan-fact"],
		matchedIDs["scope-limit"] + "\x00" + itemRelationLimits + "\x00" + matchedIDs["cause-hypothesis"],
	} {
		if !relationKeys[key] {
			t.Fatalf("required relation %q missing after fresh reload: %+v", key, live.Tree.Relations)
		}
	}
	if !reflect.DeepEqual(live.Tree.Relations, tree.Tree.Relations) {
		t.Fatalf("live/final relations differ: live=%+v final=%+v", live.Tree.Relations, tree.Tree.Relations)
	}
	if got, want := qualityServiceActiveItemIDs(live.Items), qualityServiceTreeDetailIDs(tree.Tree); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("assistant/card and final tree item ids differ: live=%v tree=%v", got, want)
	}
	if got := h.completer.callCount(qualityServiceLiveDeployment); got != 2 {
		t.Fatalf("live_extraction calls=%d, want 2 task-routed rounds", got)
	}
	if got := h.completer.callCount(qualityServiceTreeDeployment); got != 3 {
		t.Fatalf("tree_reorganizer calls=%d, want two live reviews plus one final review after atomic correction expansion", got)
	}
	if got := h.completer.callCount(qualityServiceFinalDeployment); got != 1 {
		t.Fatalf("final_summary calls=%d, want 1", got)
	}
}

func testMeetingQualityServiceIntegrationFallbackRelations(t *testing.T) {
	scenario := qualityServiceFallbackRelationScenario()
	h := newQualityServiceHarness(t, qualityServiceRoundResponses(scenario))
	segments := qualityServiceDomainSegments("quality-fallback-relations", scenario)
	qualityServiceSaveDurable(t, h.transcript, segments)
	h.start()
	h.service.PrepareMeetingSession(domain.MeetingSession{ID: segments[0].SessionID})
	qualityServiceWaitWrite(t, h.repo, domain.MeetingAIAnalysisLive, domain.MeetingAIAnalysisCompleted, 1)
	if got := h.completer.callCount(qualityServiceLiveDeployment); got != 1 {
		t.Fatalf("live_extraction calls=%d, want one fixed task-routed batch", got)
	}
	qualityServiceFinalize(t, h.service, segments[0].SessionID, 6)
	snapshot := qualityServiceReload(t, h, segments[0].SessionID)
	live, tree := qualityServiceAssertPersistence(t, snapshot, 6)
	result := qualityServiceAssertEvaluation(t, scenario, snapshot.Live.Payload)

	active := make(map[string]liveAnalysisItem)
	var risk *liveAnalysisItem
	for index := range live.Items {
		item := live.Items[index]
		if item.Inactive || item.MergedIntoID != "" {
			continue
		}
		active[item.ID] = item
		if item.Kind == "risk" && strings.Contains(item.Title, "VPN証明書") {
			copy := item
			risk = &copy
		}
	}
	if risk == nil || !strings.Contains(risk.Title, "リモート接続") ||
		(!strings.Contains(risk.Title, "可能性") && !strings.Contains(risk.Title, "リスク")) ||
		!equalInt64s(risk.EvidenceSequenceNos, []int64{1, 2}) {
		t.Fatalf("fallback risk did not survive final reload: %+v", risk)
	}
	if risk.LabelResolution == nil || risk.LabelResolution.Status != "fallback_applied" ||
		!equalInt64s(risk.LabelResolution.SourceEvidenceSequenceNos, []int64{1, 2}) {
		t.Fatalf("fallback risk label resolution did not survive final reload: %+v", risk.LabelResolution)
	}
	riskTopic := itemTopicID(live.Tree, risk.ID)
	if riskTopic == "" || treeNodeByID(live.Tree, riskTopic) == nil ||
		treeNodeByID(live.Tree, riskTopic).Origin != topicOriginDynamic {
		t.Fatalf("fallback risk lost dynamic topic: risk=%+v topic=%+v", risk, treeNodeByID(live.Tree, riskTopic))
	}
	if got, want := qualityServiceActiveItemIDs(live.Items), qualityServiceTreeDetailIDs(tree.Tree); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("assistant/card and final tree item ids differ: live=%v tree=%v", got, want)
	}
	liveRiskNode := treeNodeByID(live.Tree, risk.ID)
	finalRiskNode := treeNodeByID(tree.Tree, risk.ID)
	if liveRiskNode == nil || finalRiskNode == nil ||
		!reflect.DeepEqual(liveRiskNode.LabelResolution, risk.LabelResolution) ||
		!reflect.DeepEqual(finalRiskNode.LabelResolution, risk.LabelResolution) {
		t.Fatalf("tree projections lost label resolution: item=%+v live=%+v final=%+v",
			risk.LabelResolution, liveRiskNode, finalRiskNode)
	}

	relationKeys := make(map[string]liveAnalysisTreeRelation)
	for _, relation := range live.Tree.Relations {
		relationKeys[relationKey(relation)] = relation
		if _, ok := active[relation.Source]; !ok {
			t.Fatalf("relation source is not active: %+v", relation)
		}
		if _, ok := active[relation.Target]; !ok {
			t.Fatalf("relation target is not active: %+v", relation)
		}
		if relation.ID == "" || relation.Confidence <= 0 || relation.Status != "active" ||
			len(relation.EvidenceSequenceNos) == 0 || relation.Origin == "" {
			t.Fatalf("relation metadata missing after reload: %+v", relation)
		}
	}
	matchedIDs := make(map[string]string, len(result.PropositionMatches))
	for _, match := range result.PropositionMatches {
		if match.Matched && match.BestActualCandidate != nil {
			matchedIDs[match.PropositionID] = match.BestActualCandidate.ID
		}
	}
	hypothesisID := matchedIDs["cause-hypothesis"]
	factID := matchedIDs["vlan-fact"]
	limitID := matchedIDs["scope-limit"]
	for _, key := range []string{
		hypothesisID + "\x00" + itemRelationSupportedBy + "\x00" + factID,
		limitID + "\x00" + itemRelationLimits + "\x00" + hypothesisID,
	} {
		if _, ok := relationKeys[key]; !ok {
			t.Fatalf("required persisted relation %q missing: %+v", key, live.Tree.Relations)
		}
	}
	if !reflect.DeepEqual(tree.Tree.Relations, live.Tree.Relations) {
		t.Fatalf("live/final relation metadata differs: live=%+v final=%+v", live.Tree.Relations, tree.Tree.Relations)
	}

	// Persist a relation whose every optional field is non-default, then prove
	// both repository rows survive a fresh reader and a rejected stale CAS.
	sentinel := relationTransportSentinel(hypothesisID, factID)
	sentinel.ID = "relation-sentinel-service-v1"
	live.Tree.Relations = append(live.Tree.Relations, sentinel)
	tree.Tree.Relations = append(tree.Tree.Relations, sentinel)
	livePayload, err := json.Marshal(live)
	if err != nil {
		t.Fatalf("marshal live sentinel payload: %v", err)
	}
	treePayload, err := json.Marshal(tree)
	if err != nil {
		t.Fatalf("marshal tree sentinel payload: %v", err)
	}
	liveRow := cloneQualityServiceAnalysis(*snapshot.Live)
	treeRow := cloneQualityServiceAnalysis(*snapshot.Tree)
	liveRow.Payload = livePayload
	treeRow.Payload = treePayload
	if _, err := h.repo.UpsertMeetingAIAnalysis(context.Background(), liveRow); err != nil {
		t.Fatalf("persist live sentinel: %v", err)
	}
	if _, err := h.repo.UpsertMeetingAIAnalysis(context.Background(), treeRow); err != nil {
		t.Fatalf("persist final-tree sentinel: %v", err)
	}
	stale := liveRow
	stale.Payload = json.RawMessage(`{"summary":"stale","items":[],"tree":{"nodes":[],"edges":[],"relations":[]}}`)
	current, applied, err := h.repo.CompareAndSwapMeetingAIAnalysis(context.Background(), liveRow.Version-1, stale)
	if err != nil || applied || current == nil || current.Version != liveRow.Version {
		t.Fatalf("stale sentinel CAS err=%v applied=%t current=%+v", err, applied, current)
	}
	fresh := qualityServiceReload(t, h, segments[0].SessionID)
	freshLive, freshTree := qualityServiceAssertPersistence(t, fresh, 6)
	gotLiveSentinel, liveFound := findRelationSentinel(freshLive.Tree.Relations, sentinel.ID)
	gotTreeSentinel, treeFound := findRelationSentinel(freshTree.Tree.Relations, sentinel.ID)
	if !liveFound || !treeFound {
		t.Fatalf("fresh reader lost persisted sentinel: live=%+v final=%+v", freshLive.Tree.Relations, freshTree.Tree.Relations)
	}
	assertRelationSentinelEqual(t, gotLiveSentinel, sentinel)
	assertRelationSentinelEqual(t, gotTreeSentinel, sentinel)
	if !reflect.DeepEqual(freshLive.Tree.Relations, freshTree.Tree.Relations) {
		t.Fatalf("fresh live/final relation metadata differs: live=%+v final=%+v", freshLive.Tree.Relations, freshTree.Tree.Relations)
	}
	t.Logf("orchestration=durable batch+finalize persistence=fresh-reader fallbackRisk=%s relations=%d semanticPassed=%t",
		risk.ID, len(freshLive.Tree.Relations), result.Passed)
}

func testMeetingQualityServiceIntegrationFinalizationWait(t *testing.T) {
	scenario := qualityServiceLoadScenario(t, "finalization-inflight-tail-flush")
	scenario.MeetingContext = MeetingQualityMeetingContext{}
	h := newQualityServiceHarness(t, qualityServiceRoundResponses(scenario))
	h.completer.blockFirstLive()
	h.start()
	segments := qualityServiceDomainSegments("quality-finalization-wait", scenario)
	ingest := NewTranscriptIngestService(h.transcript, h.service)
	qualityServiceIngest(t, ingest, segments[0])
	select {
	case <-h.completer.firstLiveStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first live_extraction did not start")
	}
	qualityServiceIngest(t, ingest, segments[1])

	finalized := make(chan error, 1)
	go func() {
		finalized <- h.service.FinalizeMeetingSession(context.Background(), domain.MeetingSession{ID: segments[0].SessionID}, MeetingSessionFinalizationRequest{
			BotLastForwardedFinalSequence: 2, TranscriptQueueDrained: true,
		})
	}()
	qualityServiceWaitWrite(t, h.repo, domain.MeetingAIAnalysisFinalization, domain.MeetingAIAnalysisRunning, 1)
	select {
	case err := <-finalized:
		t.Fatalf("finalization completed before live release: %v", err)
	default:
	}
	h.completer.mu.Lock()
	beforeRelease := append([]string(nil), h.completer.startedBeforeRelease...)
	h.completer.mu.Unlock()
	if len(beforeRelease) != 0 {
		t.Fatalf("AI tasks started before first live release = %v", beforeRelease)
	}
	h.completer.releaseLive()
	select {
	case err := <-finalized:
		if err != nil {
			t.Fatalf("FinalizeMeetingSession() after release error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("finalization did not complete after live release")
	}
	if got := h.completer.callCount(qualityServiceLiveDeployment); got != 2 {
		t.Fatalf("live_extraction calls=%d, want initial + tail flush", got)
	}
	if got := h.completer.callCount(qualityServiceTreeDeployment); got != 1 {
		t.Fatalf("tree_reorganizer calls=%d, want 1", got)
	}
	if got := h.completer.callCount(qualityServiceFinalDeployment); got != 1 {
		t.Fatalf("final_summary calls=%d, want 1", got)
	}
	requests := h.completer.taskRequests(qualityServiceLiveDeployment)
	if len(requests) != 2 || !strings.Contains(requests[0].Request.User, segments[0].Text) ||
		strings.Contains(requests[0].Request.User, segments[1].Text) || !strings.Contains(requests[1].Request.User, segments[1].Text) {
		t.Fatalf("live input batches were not frozen at seq1 then flushed through seq2: %+v", requests)
	}
	snapshot := qualityServiceReload(t, h, segments[0].SessionID)
	live, _ := qualityServiceAssertPersistence(t, snapshot, 2)
	result := qualityServiceAssertEvaluation(t, scenario, snapshot.Live.Payload)
	t.Logf("orchestration=channel-gated in-flight wait liveCalls=%d persistence=reload-ok coverage=%d semanticPassed=%t",
		len(requests), live.CoveredThroughSequenceNo, result.Passed)
}

func testMeetingQualityServiceIntegrationDurableRecovery(t *testing.T) {
	scenario := qualityServiceLoadScenario(t, "same-batch-candidate-promotion")
	scenario.MeetingContext = MeetingQualityMeetingContext{}
	h := newQualityServiceHarness(t, qualityServiceRoundResponses(scenario))
	segments := qualityServiceDomainSegments("quality-durable-recovery", scenario)
	qualityServiceSaveDurable(t, h.transcript, segments)
	h.start()
	h.service.PrepareMeetingSession(domain.MeetingSession{ID: segments[0].SessionID})
	qualityServiceWaitWrite(t, h.repo, domain.MeetingAIAnalysisLive, domain.MeetingAIAnalysisCompleted, 1)
	qualityServiceFinalize(t, h.service, segments[0].SessionID, 2)
	snapshot := qualityServiceReload(t, h, segments[0].SessionID)
	live, _ := qualityServiceAssertPersistence(t, snapshot, 2)
	seen := make(map[string]struct{}, len(live.AnalyzedFinalSegments))
	for _, ref := range live.AnalyzedFinalSegments {
		key := finalSegmentKey(ref.CallID, ref.SequenceNo)
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf("duplicate analyzed final segment %q", key)
		}
		seen[key] = struct{}{}
	}
	for _, segment := range segments {
		if _, ok := seen[finalSegmentKey(segment.CallID, segment.SequenceNo)]; !ok {
			t.Fatalf("durable recovery omitted seq=%d: analyzed=%+v", segment.SequenceNo, live.AnalyzedFinalSegments)
		}
	}
	result := qualityServiceAssertEvaluation(t, scenario, snapshot.Live.Payload)
	finalRequests := h.completer.taskRequests(qualityServiceFinalDeployment)
	if len(finalRequests) != 1 {
		t.Fatalf("final_summary calls=%d, want 1", len(finalRequests))
	}
	for _, segment := range segments {
		if !strings.Contains(finalRequests[0].Request.User, segment.Text) {
			t.Fatalf("final_summary input omitted recovered seq=%d", segment.SequenceNo)
		}
	}
	t.Logf("orchestration=durable recovery persistence=unique-final-keys(%d) coverage=%d semanticPassed=%t",
		len(seen), live.CoveredThroughSequenceNo, result.Passed)
}

func testMeetingQualityServiceIntegrationSameBatchPromotion(t *testing.T) {
	scenario := qualityServiceSameBatchKindScenario()
	h := newQualityServiceHarness(t, qualityServiceRoundResponses(scenario))
	segments := qualityServiceDomainSegments("quality-same-batch", scenario)
	qualityServiceSaveDurable(t, h.transcript, segments)
	h.start()
	h.service.PrepareMeetingSession(domain.MeetingSession{ID: segments[0].SessionID})
	qualityServiceWaitWrite(t, h.repo, domain.MeetingAIAnalysisLive, domain.MeetingAIAnalysisCompleted, 1)
	if got := h.completer.callCount(qualityServiceLiveDeployment); got != 1 {
		t.Fatalf("same-batch live_extraction calls=%d, want 1", got)
	}
	qualityServiceFinalize(t, h.service, segments[0].SessionID, 4)
	snapshot := qualityServiceReload(t, h, segments[0].SessionID)
	live, tree := qualityServiceAssertPersistence(t, snapshot, 4)
	if len(live.EmergingTopics) != 0 {
		t.Fatalf("active candidate remains after same-batch finalization: %+v", live.EmergingTopics)
	}
	if got := qualityServiceDynamicTopicCount(live.Tree); got != 1 {
		t.Fatalf("same-batch materialized dynamic topics=%d, want 1", got)
	}
	dynamicTopicID := ""
	for _, node := range live.Tree.Nodes {
		if node.Kind == "topic" && node.Origin == topicOriginDynamic {
			dynamicTopicID = node.ID
		}
	}
	kinds := make(map[string]bool)
	for _, item := range live.Items {
		if !item.Inactive && item.MergedIntoID == "" {
			kinds[item.Kind] = true
		}
	}
	for _, kind := range []string{"fact", "risk", "decision", "todo"} {
		if !kinds[kind] {
			t.Fatalf("same-batch final live is missing kind %q: %+v", kind, kinds)
		}
	}
	for _, item := range live.Items {
		if item.Inactive || item.MergedIntoID != "" {
			continue
		}
		if parent := itemTopicID(live.Tree, item.ID); parent != dynamicTopicID {
			t.Fatalf("same-batch item %s parent=%s, want promoted topic %s", item.ID, parent, dynamicTopicID)
		}
	}
	if got, want := qualityServiceActiveItemIDs(live.Items), qualityServiceTreeDetailIDs(tree.Tree); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("assistant/card and final tree item ids differ: live=%v tree=%v", got, want)
	}
	result := qualityServiceAssertEvaluation(t, scenario, snapshot.Live.Payload)
	t.Logf("orchestration=one recovered batch persistence=card/tree IDs aligned semanticPassed=%t kinds=%v", result.Passed, kinds)
}

func testMeetingQualityServiceIntegrationMultipleRoundPromotion(t *testing.T) {
	scenario := qualityServiceLoadScenario(t, "unexpected-candidate-promotion")
	scenario.MeetingContext = MeetingQualityMeetingContext{}
	h := newQualityServiceHarness(t, qualityServiceRoundResponses(scenario))
	h.start()
	segments := qualityServiceDomainSegments("quality-multi-round", scenario)
	ingest := NewTranscriptIngestService(h.transcript, h.service)
	qualityServiceIngest(t, ingest, segments[0])
	first := qualityServiceWaitWrite(t, h.repo, domain.MeetingAIAnalysisLive, domain.MeetingAIAnalysisCompleted, 1)
	qualityServiceIngest(t, ingest, segments[1])
	qualityServiceWaitWrite(t, h.repo, domain.MeetingAIAnalysisLive, domain.MeetingAIAnalysisCompleted, first.Version+1)
	if got := h.completer.callCount(qualityServiceLiveDeployment); got != 2 {
		t.Fatalf("multi-round live_extraction calls=%d, want 2", got)
	}
	qualityServiceFinalize(t, h.service, segments[0].SessionID, 2)
	snapshot := qualityServiceReload(t, h, segments[0].SessionID)
	live, _ := qualityServiceAssertPersistence(t, snapshot, 2)
	if got := qualityServiceDynamicTopicCount(live.Tree); got != 1 {
		t.Fatalf("materialized dynamic topics=%d, want 1", got)
	}
	dynamicTopicID := ""
	for _, node := range live.Tree.Nodes {
		if node.Kind == "topic" && node.Origin == topicOriginDynamic {
			dynamicTopicID = node.ID
		}
	}
	for _, item := range live.Items {
		if item.Inactive || item.MergedIntoID != "" {
			continue
		}
		if parent := itemTopicID(live.Tree, item.ID); parent != dynamicTopicID {
			t.Fatalf("multi-round item %s parent=%s, want promoted topic %s", item.ID, parent, dynamicTopicID)
		}
	}
	if len(live.EmergingTopics) != 0 {
		t.Fatalf("candidate was not consumed by promotion: %+v", live.EmergingTopics)
	}
	result := qualityServiceAssertEvaluation(t, scenario, snapshot.Live.Payload)
	if result.Metrics.CandidateFragmentationCount != 0 {
		t.Fatalf("candidate fragmentation=%d, want 0", result.Metrics.CandidateFragmentationCount)
	}
	t.Logf("orchestration=two explicit repository-observed rounds persistence=one dynamic topic semanticPassed=%t", result.Passed)
}

func qualityServiceAgendaScenarioHarness(t *testing.T, sessionID string) (*qualityServiceHarness, MeetingQualityScenario, []domain.TranscriptSegment) {
	t.Helper()
	scenario := qualityServiceLoadScenario(t, "agenda-misassignment")
	contextResponse := `{"purpose":"VPN運用上のリスク確認","agendaItems":[{"title":"VPNセキュリティ","order":1,"role":"primary","semanticHints":["認証","不正接続"]},{"title":"次期予算","order":2,"role":"primary","semanticHints":["費用","見積"]}],"aiDirectives":[]}`
	h := newQualityServiceHarness(t, qualityServiceRoundResponses(scenario))
	h.completer.withContextResponse(contextResponse)
	h.start()
	segments := qualityServiceDomainSegments(sessionID, scenario)
	h.service.PrepareMeetingSession(domain.MeetingSession{ID: sessionID, Title: "VPN運用会議", Purpose: "VPN運用上のリスク確認", Agenda: "1. VPNセキュリティ\n2. 次期予算"})
	qualityServiceWaitWrite(t, h.repo, domain.MeetingAIAnalysisContext, domain.MeetingAIAnalysisCompleted, 1)
	ingest := NewTranscriptIngestService(h.transcript, h.service)
	for _, segment := range segments {
		qualityServiceIngest(t, ingest, segment)
	}
	qualityServiceWaitWrite(t, h.repo, domain.MeetingAIAnalysisLive, domain.MeetingAIAnalysisCompleted, 1)
	qualityServiceFinalize(t, h.service, sessionID, segments[len(segments)-1].SequenceNo)
	return h, scenario, segments
}

func testMeetingQualityServiceIntegrationReload(t *testing.T) {
	h, scenario, segments := qualityServiceAgendaScenarioHarness(t, "quality-reload")
	snapshot := qualityServiceReload(t, h, segments[0].SessionID)
	live, tree := qualityServiceAssertPersistence(t, snapshot, segments[len(segments)-1].SequenceNo)
	if live.NodeCount != len(live.Tree.Nodes) || tree.TreeVersion != snapshot.Tree.Version {
		t.Fatalf("reloaded projection metadata mismatch: nodeCount=%d nodes=%d treeEnvelopeVersion=%d rowVersion=%d",
			live.NodeCount, len(live.Tree.Nodes), tree.TreeVersion, snapshot.Tree.Version)
	}
	var agendaTopic *liveAnalysisTreeNode
	for index := range live.Tree.Nodes {
		node := &live.Tree.Nodes[index]
		if containsExactString(node.AgendaRefs, "agenda-1") {
			agendaTopic = node
			break
		}
	}
	if agendaTopic == nil || agendaTopic.Origin != topicOriginAgenda || !agendaTopic.Materialized {
		t.Fatalf("agenda-1 materialized topic missing: %+v", agendaTopic)
	}
	anchorFound := false
	for _, anchor := range live.AgendaAnchors {
		if anchor.AgendaID == "agenda-1" && containsExactString(anchor.MaterializedTopicIDs, agendaTopic.ID) {
			anchorFound = true
		}
	}
	if !anchorFound {
		t.Fatalf("agenda anchor does not reference materialized topic %q: %+v", agendaTopic.ID, live.AgendaAnchors)
	}
	for _, item := range live.Items {
		if item.Inactive || item.MergedIntoID != "" {
			continue
		}
		if itemTopicID(live.Tree, item.ID) != agendaTopic.ID || !containsInt64(item.EvidenceSequenceNos, 1) {
			t.Fatalf("reloaded item lost parent/evidence metadata: item=%+v parent=%s", item, itemTopicID(live.Tree, item.ID))
		}
	}
	result := qualityServiceAssertEvaluation(t, scenario, snapshot.Live.Payload)
	t.Logf("orchestration=finalize+fresh-reader persistence=agenda/tree metadata retained coverage=%d semanticPassed=%t",
		live.CoveredThroughSequenceNo, result.Passed)
}

func testMeetingQualityServiceIntegrationStaleCAS(t *testing.T) {
	h, scenario, segments := qualityServiceAgendaScenarioHarness(t, "quality-stale-cas")
	before := qualityServiceReload(t, h, segments[0].SessionID)
	beforeLive := cloneQualityServiceAnalysis(*before.Live)
	beforeTree := cloneQualityServiceAnalysis(*before.Tree)
	stale := beforeLive
	stale.Payload = json.RawMessage(`{"summary":"stale","items":[],"tree":{"nodes":[],"edges":[]}}`)
	stale.UpdatedAt = beforeLive.UpdatedAt.Add(time.Hour)
	current, applied, err := h.repo.CompareAndSwapMeetingAIAnalysis(context.Background(), beforeLive.Version-1, stale)
	if err != nil {
		t.Fatalf("stale live CAS error = %v", err)
	}
	if applied || current == nil || current.Version != beforeLive.Version {
		t.Fatalf("stale live CAS applied=%t current=%+v, want rejection at version %d", applied, current, beforeLive.Version)
	}
	after := qualityServiceReload(t, h, segments[0].SessionID)
	if string(after.Live.Payload) != string(beforeLive.Payload) || after.Live.Version != beforeLive.Version {
		t.Fatal("rejected stale CAS changed finalized live projection")
	}
	if string(after.Tree.Payload) != string(beforeTree.Payload) || after.Tree.Version != beforeTree.Version {
		t.Fatal("rejected stale live CAS changed the separately keyed final tree snapshot")
	}
	qualityServiceAssertPersistence(t, after, segments[len(segments)-1].SequenceNo)
	result := qualityServiceAssertEvaluation(t, scenario, after.Live.Payload)
	t.Logf("orchestration=production-version-CAS conflict persistence=live/tree unchanged semanticPassed=%t", result.Passed)
}

var _ TranscriptSegmentRepository = (*qualityServiceTranscriptRepository)(nil)
var _ MeetingAIAnalysisRepository = (*qualityServiceAnalysisRepository)(nil)
var _ MeetingAIAnalysisCompareAndSwapRepository = (*qualityServiceAnalysisRepository)(nil)
var _ AIChatCompleter = (*qualityServiceCompleter)(nil)
