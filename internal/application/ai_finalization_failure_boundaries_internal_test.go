package application

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
)

type finalizationFailureRepository struct {
	*contextBarrierRepository
	mu       sync.Mutex
	failType domain.MeetingAIAnalysisType
}

func (r *finalizationFailureRepository) UpsertMeetingAIAnalysis(ctx context.Context, analysis domain.MeetingAIAnalysis) (*domain.MeetingAIAnalysis, error) {
	r.mu.Lock()
	fail := analysis.Type == r.failType
	r.mu.Unlock()
	if fail {
		return nil, errors.New("injected " + string(analysis.Type) + " repository failure")
	}
	return r.contextBarrierRepository.UpsertMeetingAIAnalysis(ctx, analysis)
}

type finalizationFailureTranscriptRepository struct {
	segments  []domain.TranscriptSegment
	listErr   error
	listCalls int
	mu        sync.Mutex
}

func (r *finalizationFailureTranscriptRepository) SaveTranscriptSegment(context.Context, domain.TranscriptSegment) (domain.TranscriptSegmentStoreResult, error) {
	return domain.TranscriptSegmentStoreResult{}, nil
}

func (r *finalizationFailureTranscriptRepository) ListTranscriptSegments(context.Context, string, string, int) ([]domain.TranscriptSegment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listCalls++
	if r.listErr != nil {
		return nil, r.listErr
	}
	return append([]domain.TranscriptSegment(nil), r.segments...), nil
}

type finalizationFailureSessionRepository struct {
	MeetingSessionRepository
}

func (r *finalizationFailureSessionRepository) GetMeetingSession(context.Context, string) (*domain.MeetingSession, error) {
	return nil, errors.New("injected meeting context repository failure")
}

type finalizationFailureCompleter struct {
	mu      sync.Mutex
	results []AIChatResult
}

func (c *finalizationFailureCompleter) Complete(context.Context, AIChatRequest) (AIChatResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.results) == 0 {
		return AIChatResult{}, errors.New("unexpected completer call")
	}
	result := c.results[0]
	c.results = c.results[1:]
	return result, nil
}

func failureBoundaryLivePayload(t *testing.T) json.RawMessage {
	t.Helper()
	raw, _, _ := finalReconciliationFixture(
		t,
		"切り戻しと設定修正でサービスを正常化",
		"旧スイッチへ切り戻し、トランク設定と許可VLANを修正した",
		"",
	)
	return raw
}

func TestFinalizationContinuesAcrossOptionalPersistenceFailures(t *testing.T) {
	tests := []struct {
		name     string
		failType domain.MeetingAIAnalysisType
	}{
		{name: "same-version live projection repository update", failType: domain.MeetingAIAnalysisLive},
		{name: "final tree snapshot save", failType: domain.MeetingAIAnalysisTree},
		{name: "finalization progress save", failType: domain.MeetingAIAnalysisFinalization},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := newContextBarrierRepository()
			_, err := base.UpsertMeetingAIAnalysis(context.Background(), domain.MeetingAIAnalysis{
				SessionID: "session-finalization-failure", Type: domain.MeetingAIAnalysisLive,
				Status: domain.MeetingAIAnalysisCompleted, Version: 12,
				Payload:   failureBoundaryLivePayload(t),
				UpdatedAt: time.Date(2026, 7, 26, 4, 0, 0, 0, time.UTC),
			})
			if err != nil {
				t.Fatal(err)
			}
			repository := &finalizationFailureRepository{
				contextBarrierRepository: base,
				failType:                 tt.failType,
			}
			completer := &finalizationFailureCompleter{results: []AIChatResult{
				{Content: `{"basedOnTreeVersion":12,"operations":[]}`},
				{Content: `{"suggestedTitle":"障害レビュー","overview":"最終要約","decisions":[],"actionItems":[],"openIssues":[],"keyPoints":[],"nextMeetingTopics":[]}`},
			}}
			transcripts := &finalizationFailureTranscriptRepository{segments: []domain.TranscriptSegment{{
				SessionID: "session-finalization-failure", SequenceNo: 10, IsFinal: true,
				Text: "復旧対応として旧スイッチへ切り戻し、許可VLANを修正して正常化を確認しました。",
			}}}
			service := NewMeetingAnalysisService(
				repository, transcripts, nil, completer,
				MeetingAnalysisConfig{
					Enabled: true, FinalEnabled: true, FinalMaxInputChars: 12000,
					FinalizationWaitTimeout: time.Second,
				},
			)
			_ = service.FinalizeMeetingSession(
				context.Background(),
				domain.MeetingSession{ID: "session-finalization-failure"},
				MeetingSessionFinalizationRequest{TranscriptQueueDrained: true},
			)
			summary, getErr := base.GetMeetingAIAnalysis(
				context.Background(), "session-finalization-failure", domain.MeetingAIAnalysisFinal,
			)
			if getErr != nil || summary == nil || summary.Status != domain.MeetingAIAnalysisCompleted {
				t.Fatalf("final summary stopped after %s failure: summary=%+v error=%v", tt.failType, summary, getErr)
			}
			live, getErr := base.GetMeetingAIAnalysis(
				context.Background(), "session-finalization-failure", domain.MeetingAIAnalysisLive,
			)
			if getErr != nil || live == nil || live.Version != 12 || len(live.Payload) == 0 {
				t.Fatalf("last-known-good live payload lost after %s failure: live=%+v error=%v", tt.failType, live, getErr)
			}
		})
	}
}

func TestFinalizationUsesLKGWhenTranscriptFetchFails(t *testing.T) {
	base := newContextBarrierRepository()
	_, err := base.UpsertMeetingAIAnalysis(context.Background(), domain.MeetingAIAnalysis{
		SessionID: "session-transcript-failure", Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 12,
		Payload:   failureBoundaryLivePayload(t),
		UpdatedAt: time.Date(2026, 7, 26, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	transcripts := &finalizationFailureTranscriptRepository{
		listErr: errors.New("injected transcript fetch failure"),
	}
	completer := &finalizationFailureCompleter{results: []AIChatResult{
		{Content: `{"basedOnTreeVersion":12,"operations":[]}`},
		{Content: `{"suggestedTitle":"障害レビュー","overview":"LKGから生成した最終要約","decisions":[],"actionItems":[],"openIssues":[],"keyPoints":[],"nextMeetingTopics":[]}`},
	}}
	service := NewMeetingAnalysisService(
		base, transcripts, nil, completer,
		MeetingAnalysisConfig{
			Enabled: true, FinalEnabled: true, FinalMaxInputChars: 12000,
			FinalizationWaitTimeout: time.Second,
		},
	)

	finalizeErr := service.FinalizeMeetingSession(
		context.Background(),
		domain.MeetingSession{ID: "session-transcript-failure"},
		MeetingSessionFinalizationRequest{TranscriptQueueDrained: true},
	)
	if finalizeErr == nil {
		t.Fatalf("finalization must report incomplete transcript coverage")
	}
	summary, getErr := base.GetMeetingAIAnalysis(
		context.Background(), "session-transcript-failure", domain.MeetingAIAnalysisFinal,
	)
	if getErr != nil || summary == nil || summary.Status != domain.MeetingAIAnalysisCompleted {
		t.Fatalf("transcript fetch failure stopped final summary: summary=%+v error=%v", summary, getErr)
	}
	snapshot, getErr := base.GetMeetingAIAnalysis(
		context.Background(), "session-transcript-failure", domain.MeetingAIAnalysisTree,
	)
	if getErr != nil || snapshot == nil || snapshot.Status != domain.MeetingAIAnalysisCompleted {
		t.Fatalf("transcript fetch failure stopped final tree snapshot: snapshot=%+v error=%v", snapshot, getErr)
	}
	progress, getErr := base.GetMeetingAIAnalysis(
		context.Background(), "session-transcript-failure", domain.MeetingAIAnalysisFinalization,
	)
	if getErr != nil || progress == nil || progress.Status != domain.MeetingAIAnalysisFailed {
		t.Fatalf("finalization progress=%+v error=%v, want observable incomplete result", progress, getErr)
	}
	var progressPayload finalizationProgressPayload
	if err := json.Unmarshal(progress.Payload, &progressPayload); err != nil {
		t.Fatal(err)
	}
	if !progressPayload.TranscriptFallbackUsed || !progressPayload.FinalizationIncomplete {
		t.Fatalf("progress payload=%+v, want transcript fallback/incomplete", progressPayload)
	}
	transcripts.mu.Lock()
	listCalls := transcripts.listCalls
	transcripts.mu.Unlock()
	if listCalls != 1 {
		t.Fatalf("transcript fetch calls=%d, want one fail-open attempt", listCalls)
	}
}

func TestFinalizationContinuesWhenMeetingContextFetchFails(t *testing.T) {
	base := newContextBarrierRepository()
	liveState := previousLiveAnalysisState(failureBoundaryLivePayload(t))
	liveState.CoveredThroughSequenceNo = 10
	livePayload, err := json.Marshal(liveState)
	if err != nil {
		t.Fatal(err)
	}
	_, err = base.UpsertMeetingAIAnalysis(context.Background(), domain.MeetingAIAnalysis{
		SessionID: "session-context-failure", Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 12,
		Payload:   livePayload,
		UpdatedAt: time.Date(2026, 7, 26, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	transcripts := &finalizationFailureTranscriptRepository{segments: []domain.TranscriptSegment{{
		SessionID: "session-context-failure", SequenceNo: 10, IsFinal: true,
		Text: "復旧対応として切り戻しと設定修正を行いました。",
	}}}
	completer := &finalizationFailureCompleter{results: []AIChatResult{
		{Content: `{"basedOnTreeVersion":12,"operations":[]}`},
		{Content: `{"suggestedTitle":"障害レビュー","overview":"最終要約","decisions":[],"actionItems":[],"openIssues":[],"keyPoints":[],"nextMeetingTopics":[]}`},
	}}
	service := NewMeetingAnalysisService(
		base, transcripts,
		&finalizationFailureSessionRepository{},
		completer,
		MeetingAnalysisConfig{
			Enabled: true, FinalEnabled: true, FinalMaxInputChars: 12000,
			FinalizationWaitTimeout: time.Second,
		},
	)

	if err := service.FinalizeMeetingSession(
		context.Background(),
		domain.MeetingSession{ID: "session-context-failure"},
		MeetingSessionFinalizationRequest{TranscriptQueueDrained: true},
	); err != nil {
		t.Fatalf("meeting context failure stopped finalization: %v", err)
	}
	summary, getErr := base.GetMeetingAIAnalysis(
		context.Background(), "session-context-failure", domain.MeetingAIAnalysisFinal,
	)
	if getErr != nil || summary == nil || summary.Status != domain.MeetingAIAnalysisCompleted {
		t.Fatalf("meeting context failure stopped final summary: summary=%+v error=%v", summary, getErr)
	}
}
