package application

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
)

type snapshotContractPublisher struct {
	mu       sync.Mutex
	analyses []domain.MeetingAIAnalysis
}

func (p *snapshotContractPublisher) PublishMeetingAIAnalysis(analysis domain.MeetingAIAnalysis) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.analyses = append(p.analyses, analysis)
}

func (p *snapshotContractPublisher) snapshot() []domain.MeetingAIAnalysis {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]domain.MeetingAIAnalysis(nil), p.analyses...)
}

func TestPersistFinalizedLiveProjectionKeepsVersionAndStrictlyAdvancesUpdatedAt(t *testing.T) {
	repository := newContextBarrierRepository()
	previousTime := time.Date(2026, 7, 26, 4, 0, 0, 123000, time.UTC)
	previousPayload := json.RawMessage(`{
		"treeVersion":12,
		"tree":{"nodes":[{"id":"root","kind":"topic"},{"id":"topic-dynamic","kind":"topic","parentId":"root"},{"id":"fact-a","kind":"fact","parentId":"topic-dynamic"}],"edges":[]},
		"agendaProgress":{"entries":[{"id":"agenda-2","sourceType":"fixed_agenda","title":"復旧対応","computedStatus":"discussing"}]}
	}`)
	_, err := repository.UpsertMeetingAIAnalysis(context.Background(), domain.MeetingAIAnalysis{
		SessionID: "session-snapshot", Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 12,
		Payload: previousPayload, UpdatedAt: previousTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	contextPayload, err := marshalMeetingContext(&meetingContext{Agenda: []agendaItem{{
		ID: "agenda-2", Title: "復旧対応", Order: 1, Role: agendaRolePrimary,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpsertMeetingAIAnalysis(context.Background(), domain.MeetingAIAnalysis{
		SessionID: "session-snapshot", Type: domain.MeetingAIAnalysisContext,
		Status: domain.MeetingAIAnalysisCompleted, Version: 1,
		Payload: contextPayload, UpdatedAt: previousTime,
	}); err != nil {
		t.Fatal(err)
	}
	correctedPayload := json.RawMessage(`{
		"treeVersion":12,
		"tree":{"nodes":[{"id":"root","kind":"topic"},{"id":"topic-agenda","kind":"topic","parentId":"root","agendaRefs":["agenda-2"]},{"id":"fact-a","kind":"fact","parentId":"topic-agenda"}],"edges":[]},
		"agendaProgress":{"entries":[{"id":"agenda-2","sourceType":"fixed_agenda","title":"復旧対応","computedStatus":"discussed","materializedTopicIds":["topic-agenda"]}]}
	}`)
	publisher := &snapshotContractPublisher{}
	service := NewMeetingAnalysisService(
		repository, nil, nil, nil, MeetingAnalysisConfig{}, publisher,
	)
	// Even a frozen/behind test clock must produce a timestamp distinguishable
	// at the browser-visible millisecond precision.
	service.now = func() time.Time { return previousTime.Add(-time.Second) }
	if err := service.persistFinalizedLiveProjection(
		context.Background(), "session-snapshot", correctedPayload, 12,
	); err != nil {
		t.Fatal(err)
	}
	saved, err := repository.GetMeetingAIAnalysis(
		context.Background(), "session-snapshot", domain.MeetingAIAnalysisLive,
	)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != 12 {
		t.Fatalf("analysis version=%d, want unchanged 12", saved.Version)
	}
	if !saved.UpdatedAt.After(previousTime) ||
		saved.UpdatedAt.Sub(previousTime) < time.Millisecond {
		t.Fatalf("updatedAt=%s previous=%s, want >=1ms newer", saved.UpdatedAt, previousTime)
	}
	if string(saved.Payload) != string(correctedPayload) {
		t.Fatalf("saved payload=%s want=%s", saved.Payload, correctedPayload)
	}
	published := publisher.snapshot()
	if len(published) != 1 || published[0].Version != 12 ||
		!published[0].UpdatedAt.Equal(saved.UpdatedAt) {
		t.Fatalf("published=%+v saved=%+v", published, saved)
	}
	publishedState := previousLiveAnalysisState(published[0].Payload)
	topic := treeNodeByID(publishedState.Tree, "topic-agenda")
	progress := agendaProgressEntryByID(publishedState.AgendaProgress, "agenda-2")
	if topic == nil || !containsExactString(topic.AgendaRefs, "agenda-2") ||
		progress == nil || progress.ComputedStatus != agendaProgressDiscussed {
		t.Fatalf("published corrected projection=%+v", publishedState)
	}
	restSnapshot, err := service.GetMeetingAIAnalyses(context.Background(), "session-snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if restSnapshot.Live == nil ||
		restSnapshot.Live.Version != 12 ||
		!restSnapshot.Live.UpdatedAt.Equal(saved.UpdatedAt) {
		t.Fatalf("REST live snapshot=%+v, want the same corrected projection", restSnapshot.Live)
	}
	restState := previousLiveAnalysisState(restSnapshot.Live.Payload)
	restTopic := treeNodeByID(restState.Tree, "topic-agenda")
	restProgress := agendaProgressEntryByID(restState.AgendaProgress, "agenda-2")
	if restTopic == nil || !containsExactString(restTopic.AgendaRefs, "agenda-2") ||
		restProgress == nil || restProgress.ComputedStatus != agendaProgressDiscussed {
		t.Fatalf("REST corrected projection=%+v", restState)
	}
}
