package application

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
)

type contextBarrierRepository struct {
	mu          sync.Mutex
	store       map[string]domain.MeetingAIAnalysis
	liveHistory map[string]map[int64]domain.MeetingAIAnalysis
}

func newContextBarrierRepository() *contextBarrierRepository {
	return &contextBarrierRepository{store: make(map[string]domain.MeetingAIAnalysis)}
}

func (r *contextBarrierRepository) UpsertMeetingAIAnalysis(_ context.Context, analysis domain.MeetingAIAnalysis) (*domain.MeetingAIAnalysis, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store[analysis.SessionID+"|"+string(analysis.Type)] = analysis
	saved := analysis
	return &saved, nil
}

func (r *contextBarrierRepository) GetMeetingAIAnalysis(_ context.Context, sessionID string, analysisType domain.MeetingAIAnalysisType) (*domain.MeetingAIAnalysis, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	analysis, ok := r.store[sessionID+"|"+string(analysisType)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	saved := analysis
	return &saved, nil
}

func (r *contextBarrierRepository) ListMeetingAIAnalysesForSessions(_ context.Context, sessionIDs []string, analysisType domain.MeetingAIAnalysisType) ([]domain.MeetingAIAnalysis, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]domain.MeetingAIAnalysis, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		if analysis, ok := r.store[sessionID+"|"+string(analysisType)]; ok {
			items = append(items, analysis)
		}
	}
	return items, nil
}

func (r *contextBarrierRepository) AppendLiveAnalysisHistory(_ context.Context, analysis domain.MeetingAIAnalysis) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.liveHistory == nil {
		r.liveHistory = make(map[string]map[int64]domain.MeetingAIAnalysis)
	}
	versions := r.liveHistory[analysis.SessionID]
	if versions == nil {
		versions = make(map[int64]domain.MeetingAIAnalysis)
		r.liveHistory[analysis.SessionID] = versions
	}
	if _, exists := versions[analysis.Version]; !exists {
		versions[analysis.Version] = analysis
	}
	return nil
}

func (r *contextBarrierRepository) ListLiveAnalysisHistory(_ context.Context, sessionID string, limit int) ([]domain.MeetingAIAnalysis, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	versions := r.liveHistory[sessionID]
	items := make([]domain.MeetingAIAnalysis, 0, len(versions))
	for _, analysis := range versions {
		items = append(items, analysis)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Version < items[j].Version })
	if limit > 0 && len(items) > limit {
		items = items[len(items)-limit:]
	}
	return items, nil
}

type contextBarrierCompleter struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
	result  AIChatResult
	err     error
	once    sync.Once
}

func (c *contextBarrierCompleter) Complete(ctx context.Context, _ AIChatRequest) (AIChatResult, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	c.once.Do(func() {
		if c.started != nil {
			close(c.started)
		}
	})
	if c.release != nil {
		select {
		case <-c.release:
		case <-ctx.Done():
			return AIChatResult{}, ctx.Err()
		}
	}
	return c.result, c.err
}

func (c *contextBarrierCompleter) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

const plannedContextJSON = `{"title":"計画済み","purpose":"品質確認","agendaItems":[{"title":"鳥類","order":1,"role":"primary"},{"title":"横断対応","order":2,"role":"action_summary"}],"aiDirectives":[]}`

func contextBarrierSession(id string) domain.MeetingSession {
	return domain.MeetingSession{ID: id, Title: "原題", Purpose: "品質確認", Agenda: "1. 鳥類\n2. 横断対応"}
}

func TestMeetingContextPrewarmIsSingleFlight(t *testing.T) {
	repo := newContextBarrierRepository()
	completer := &contextBarrierCompleter{started: make(chan struct{}), release: make(chan struct{}), result: AIChatResult{Content: plannedContextJSON}}
	service := NewMeetingAnalysisService(repo, nil, nil, completer, MeetingAnalysisConfig{Enabled: true, LiveEnabled: true, ContextWaitTimeout: time.Second, ContextRequestTimeout: time.Second})
	session := contextBarrierSession("session-prewarm")
	for range 20 {
		service.PrepareMeetingSession(session)
		service.ensureMeetingContextPlanning(session.ID, nil)
	}
	select {
	case <-completer.started:
	case <-time.After(time.Second):
		t.Fatal("context planner did not start")
	}
	if calls := completer.callCount(); calls != 1 {
		t.Fatalf("planner calls=%d, want 1", calls)
	}
	close(completer.release)
	context := service.sessionMeetingContext(context.Background(), session.ID)
	if context == nil || context.Title != "計画済み" || len(context.Agenda) != 2 {
		t.Fatalf("context=%+v", context)
	}
}

func TestMeetingContextFirstWaitTimesOutToFallbackThenUpgrades(t *testing.T) {
	repo := newContextBarrierRepository()
	completer := &contextBarrierCompleter{started: make(chan struct{}), release: make(chan struct{}), result: AIChatResult{Content: plannedContextJSON}}
	service := NewMeetingAnalysisService(repo, nil, nil, completer, MeetingAnalysisConfig{Enabled: true, LiveEnabled: true, ContextWaitTimeout: 15 * time.Millisecond, ContextRequestTimeout: time.Second})
	session := contextBarrierSession("session-timeout")
	service.PrepareMeetingSession(session)
	select {
	case <-completer.started:
	case <-time.After(time.Second):
		t.Fatal("context planner did not start")
	}
	started := time.Now()
	fallback := service.sessionMeetingContext(context.Background(), session.ID)
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("fallback wait took %s", elapsed)
	}
	if fallback == nil || fallback.Title != "原題" {
		t.Fatalf("fallback=%+v", fallback)
	}
	close(completer.release)
	waitForInternal(t, time.Second, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return service.sessionStateLocked(session.ID).contextStatus == meetingContextStatusReady
	})
	upgraded := service.sessionMeetingContext(context.Background(), session.ID)
	if upgraded == nil || upgraded.Title != "計画済み" || completer.callCount() != 1 {
		t.Fatalf("upgraded=%+v calls=%d", upgraded, completer.callCount())
	}
}

func TestMeetingContextPlannerFailureUsesDeterministicFallback(t *testing.T) {
	repo := newContextBarrierRepository()
	completer := &contextBarrierCompleter{err: errors.New("planner unavailable")}
	service := NewMeetingAnalysisService(repo, nil, nil, completer, MeetingAnalysisConfig{Enabled: true, LiveEnabled: true, ContextWaitTimeout: time.Second, ContextRequestTimeout: time.Second})
	session := contextBarrierSession("session-failure")
	service.PrepareMeetingSession(session)
	context := service.sessionMeetingContext(context.Background(), session.ID)
	if context == nil || context.Title != "原題" {
		t.Fatalf("context=%+v", context)
	}
	service.mu.Lock()
	status := service.sessionStateLocked(session.ID).contextStatus
	service.mu.Unlock()
	if status != meetingContextStatusFailed || completer.callCount() != 1 {
		t.Fatalf("status=%s calls=%d", status, completer.callCount())
	}
}

func TestMeetingContextRestartLoadsStoredContextWithoutPlanner(t *testing.T) {
	repo := newContextBarrierRepository()
	stored := &meetingContext{Title: "保存済み", Agenda: []agendaItem{{ID: "agenda-1", Title: "鳥類", Order: 1}}}
	payload, err := marshalMeetingContext(stored)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = repo.UpsertMeetingAIAnalysis(context.Background(), domain.MeetingAIAnalysis{SessionID: "session-restart", Type: domain.MeetingAIAnalysisContext, Version: 4, Payload: payload})
	completer := &contextBarrierCompleter{result: AIChatResult{Content: plannedContextJSON}}
	service := NewMeetingAnalysisService(repo, nil, nil, completer, MeetingAnalysisConfig{Enabled: true, LiveEnabled: true, ContextWaitTimeout: time.Second})
	service.PrepareMeetingSession(contextBarrierSession("session-restart"))
	context := service.sessionMeetingContext(context.Background(), "session-restart")
	if context == nil || context.Title != "保存済み" || completer.callCount() != 0 {
		t.Fatalf("context=%+v calls=%d", context, completer.callCount())
	}
	service.mu.Lock()
	version := service.sessionStateLocked("session-restart").contextVersion
	service.mu.Unlock()
	if version != 4 {
		t.Fatalf("context version=%d", version)
	}
}

func TestFallbackContextUpgradeAddsActionSummaryRelationWithoutDuplicatingItem(t *testing.T) {
	fallback := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-1", Title: "騒音", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "横断対応", Order: 2, Role: agendaRolePrimary},
	}}
	planned := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-1", Title: "騒音", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "横断対応", Order: 2, Role: agendaRoleActionSummary},
	}}
	first := `{"summary":"更新","currentTopic":"騒音","items":[{"id":"todo-weather","kind":"todo","severity":"high","title":"気象データを確認する","body":"過去データを確認する","status":"open"}],"assignments":[{"nodeId":"todo-weather","parentTopicId":"agenda-1","confidence":0.9}]}`
	raw1, err := parseAndMergeLiveAnalysisPayload(first, nil, fallback, 1, []int64{1}, TreeClassificationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	second := `{"summary":"再確認","currentTopic":"騒音","items":[{"id":"todo-weather","kind":"todo","severity":"high","title":"気象データを確認する","body":"過去データを確認する","status":"open"}],"assignments":[{"nodeId":"todo-weather","parentTopicId":"agenda-1","confidence":0.9}]}`
	raw2, err := parseAndMergeLiveAnalysisPayload(second, raw1, planned, 2, []int64{2}, TreeClassificationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw2)
	if len(state.Items) != 1 || len(state.Items[0].RelatedAgendaIDs) != 1 || state.Items[0].RelatedAgendaIDs[0] != "agenda-2" {
		t.Fatalf("items=%+v", state.Items)
	}
	if len(state.Items[0].EvidenceSequenceNos) != 2 {
		t.Fatalf("evidence=%v", state.Items[0].EvidenceSequenceNos)
	}
	agendaTopic := agendaTopicNodeByRef(state.Tree, "agenda-1")
	if node := treeNodeByID(state.Tree, "todo-weather"); node == nil || agendaTopic == nil || node.ParentID != agendaTopic.ID {
		t.Fatalf("canonical item node=%+v", node)
	}
}

func waitForInternal(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if !condition() {
		t.Fatalf("condition not met within %s", timeout)
	}
}
