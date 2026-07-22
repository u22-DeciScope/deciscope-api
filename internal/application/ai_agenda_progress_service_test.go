package application_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

// fakeAgendaProgressOverridesRepository is an in-memory
// application.MeetingAgendaProgressOverridesRepository fake, following the
// same pattern as this package's other fake*Repository test doubles
// (AGENTS.md: "Test Application with Fake Ports").
type fakeAgendaProgressOverridesRepository struct {
	byID map[string]json.RawMessage
	// upserts records every UpsertAgendaProgressOverrides call, for
	// assertions on what got persisted.
	upserts []json.RawMessage
}

func newFakeAgendaProgressOverridesRepository() *fakeAgendaProgressOverridesRepository {
	return &fakeAgendaProgressOverridesRepository{byID: make(map[string]json.RawMessage)}
}

func (f *fakeAgendaProgressOverridesRepository) GetAgendaProgressOverrides(_ context.Context, sessionID string) (json.RawMessage, error) {
	payload, ok := f.byID[sessionID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return payload, nil
}

func (f *fakeAgendaProgressOverridesRepository) UpsertAgendaProgressOverrides(_ context.Context, sessionID string, payload json.RawMessage, _ time.Time) error {
	f.byID[sessionID] = append(json.RawMessage(nil), payload...)
	f.upserts = append(f.upserts, payload)
	return nil
}

// agendaProgressWireEntry/agendaProgressWireState decode just enough of the
// public agendaProgress JSON shape (contract §1.1/§1.2) to assert on it from
// this external test package, which cannot see the unexported
// agendaProgressState/agendaProgressEntry types.
type agendaProgressWireEntry struct {
	ID              string `json:"id"`
	ComputedStatus  string `json:"computedStatus"`
	ManualStatus    string `json:"manualStatus"`
	EffectiveStatus string `json:"effectiveStatus"`
}
type agendaProgressWireState struct {
	ComputedCurrentTopicID  string                    `json:"computedCurrentTopicId"`
	ManualCurrentTopicID    string                    `json:"manualCurrentTopicId"`
	EffectiveCurrentTopicID string                    `json:"effectiveCurrentTopicId"`
	Entries                 []agendaProgressWireEntry `json:"entries"`
}

func decodeAgendaProgressWire(t *testing.T, raw json.RawMessage) agendaProgressWireState {
	t.Helper()
	var wrapped struct {
		AgendaProgress *agendaProgressWireState `json:"agendaProgress"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.AgendaProgress != nil {
		return *wrapped.AgendaProgress
	}
	var state agendaProgressWireState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("decode agendaProgress wire payload %s: %v", string(raw), err)
	}
	return state
}

func liveAgendaProgressPayload() json.RawMessage {
	return json.RawMessage(`{
		"summary": "テスト", "currentTopic": "議題A", "items": [],
		"tree": {"nodes": [{"id":"root","kind":"topic","label":"会議"}], "edges": []},
		"treeVersion": 3,
		"agendaProgress": {
			"computedCurrentTopicId": "agenda-1",
			"entries": [
				{"id": "agenda-1", "sourceType": "fixed_agenda", "title": "議題A", "order": 1, "computedStatus": "discussing"},
				{"id": "agenda-2", "sourceType": "fixed_agenda", "title": "議題B", "order": 2, "computedStatus": "not_started"}
			],
			"updatedAtVersion": 3
		}
	}`)
}

func TestMeetingAnalysisServiceUpdateAgendaProgressOverrideSetsManualStatusAndBroadcasts(t *testing.T) {
	analysisRepo := newFakeAIAnalysisRepository()
	analysisRepo.seed(domain.MeetingAIAnalysis{
		SessionID: "session_1", Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 3, Payload: liveAgendaProgressPayload(),
	})
	overridesRepo := newFakeAgendaProgressOverridesRepository()
	publisher := &fakeAIAnalysisPublisher{}
	service := application.NewMeetingAnalysisService(
		analysisRepo, &fakeAnalysisTranscriptRepository{}, &fakeAnalysisSessionRepository{}, &fakeAIChatCompleter{},
		testLiveOnlyConfig(time.Second, 1), publisher,
	)
	service.SetMeetingAgendaProgressOverridesRepository(overridesRepo)

	manualStatus := "discussed"
	raw, err := service.UpdateAgendaProgressOverride(context.Background(), "session_1", application.AgendaProgressOverrideInput{
		EntryID: "agenda-2", ManualStatus: &manualStatus,
	})
	if err != nil {
		t.Fatalf("UpdateAgendaProgressOverride() error = %v", err)
	}
	state := decodeAgendaProgressWire(t, raw)
	var agenda2 *agendaProgressWireEntry
	for i := range state.Entries {
		if state.Entries[i].ID == "agenda-2" {
			agenda2 = &state.Entries[i]
		}
	}
	if agenda2 == nil || agenda2.ManualStatus != "discussed" || agenda2.EffectiveStatus != "discussed" {
		t.Fatalf("stamped agenda-2 = %+v, want manual/effective=discussed", agenda2)
	}
	if len(overridesRepo.upserts) != 1 {
		t.Fatalf("upserts = %d, want 1", len(overridesRepo.upserts))
	}
	var stored application.AgendaProgressOverrides
	if err := json.Unmarshal(overridesRepo.upserts[0], &stored); err != nil {
		t.Fatalf("decode stored overrides: %v", err)
	}
	if stored.StatusOverrides["agenda-2"] != "discussed" {
		t.Fatalf("stored overrides = %+v", stored)
	}
	// A live analysis existed, so the update must trigger a WS rebroadcast
	// (stamped) via the same publisher runLiveAnalysis uses.
	published := publisher.snapshot()
	if len(published) != 1 || published[0].SessionID != "session_1" {
		t.Fatalf("published = %+v, want exactly one broadcast for session_1", published)
	}
	publishedState := decodeAgendaProgressWire(t, published[0].Payload)
	for _, entry := range publishedState.Entries {
		if entry.ID == "agenda-2" && entry.EffectiveStatus != "discussed" {
			t.Fatalf("broadcast payload agenda-2 = %+v, want effective=discussed", entry)
		}
	}
}

func TestMeetingAnalysisServiceUpdateAgendaProgressOverrideClearsToComputed(t *testing.T) {
	analysisRepo := newFakeAIAnalysisRepository()
	analysisRepo.seed(domain.MeetingAIAnalysis{
		SessionID: "session_1", Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 3, Payload: liveAgendaProgressPayload(),
	})
	overridesRepo := newFakeAgendaProgressOverridesRepository()
	overridesRepo.byID["session_1"] = json.RawMessage(`{"statusOverrides":{"agenda-2":"discussed"}}`)
	service := application.NewMeetingAnalysisService(
		analysisRepo, &fakeAnalysisTranscriptRepository{}, &fakeAnalysisSessionRepository{}, &fakeAIChatCompleter{},
		testLiveOnlyConfig(time.Second, 1),
	)
	service.SetMeetingAgendaProgressOverridesRepository(overridesRepo)

	empty := ""
	raw, err := service.UpdateAgendaProgressOverride(context.Background(), "session_1", application.AgendaProgressOverrideInput{
		EntryID: "agenda-2", ManualStatus: &empty,
	})
	if err != nil {
		t.Fatalf("UpdateAgendaProgressOverride() error = %v", err)
	}
	state := decodeAgendaProgressWire(t, raw)
	for _, entry := range state.Entries {
		if entry.ID == "agenda-2" && (entry.ManualStatus != "" || entry.EffectiveStatus != "not_started") {
			t.Fatalf("cleared agenda-2 = %+v, want manual cleared and effective back to computed (not_started)", entry)
		}
	}
	var stored application.AgendaProgressOverrides
	if err := json.Unmarshal(overridesRepo.byID["session_1"], &stored); err != nil {
		t.Fatalf("decode stored overrides: %v", err)
	}
	if _, exists := stored.StatusOverrides["agenda-2"]; exists {
		t.Fatalf("stored overrides = %+v, want agenda-2 override removed", stored)
	}
}

func TestMeetingAnalysisServiceUpdateAgendaProgressOverrideRejectsUnknownEntry(t *testing.T) {
	analysisRepo := newFakeAIAnalysisRepository()
	analysisRepo.seed(domain.MeetingAIAnalysis{
		SessionID: "session_1", Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 3, Payload: liveAgendaProgressPayload(),
	})
	service := application.NewMeetingAnalysisService(
		analysisRepo, &fakeAnalysisTranscriptRepository{}, &fakeAnalysisSessionRepository{}, &fakeAIChatCompleter{},
		testLiveOnlyConfig(time.Second, 1),
	)
	service.SetMeetingAgendaProgressOverridesRepository(newFakeAgendaProgressOverridesRepository())

	manualStatus := "discussed"
	_, err := service.UpdateAgendaProgressOverride(context.Background(), "session_1", application.AgendaProgressOverrideInput{
		EntryID: "agenda-unknown", ManualStatus: &manualStatus,
	})
	if err == nil {
		t.Fatal("UpdateAgendaProgressOverride() error = nil, want invalid_argument for an unknown entry id")
	}
}

func TestMeetingAnalysisServiceUpdateAgendaProgressOverrideSynthesizesWhenNoLiveAnalysisYet(t *testing.T) {
	analysisRepo := newFakeAIAnalysisRepository()
	analysisRepo.seed(domain.MeetingAIAnalysis{
		SessionID: "session_1", Type: domain.MeetingAIAnalysisContext, Status: domain.MeetingAIAnalysisCompleted,
		Payload: json.RawMessage(`{"title":"T","agendaItems":[{"id":"agenda-1","title":"議題A","order":1,"role":"primary"}]}`),
	})
	overridesRepo := newFakeAgendaProgressOverridesRepository()
	service := application.NewMeetingAnalysisService(
		analysisRepo, &fakeAnalysisTranscriptRepository{}, &fakeAnalysisSessionRepository{}, &fakeAIChatCompleter{},
		testLiveOnlyConfig(time.Second, 1),
	)
	service.SetMeetingAgendaProgressOverridesRepository(overridesRepo)

	raw, err := service.UpdateAgendaProgressOverride(context.Background(), "session_1", application.AgendaProgressOverrideInput{
		ManualCurrentSet: true, ManualCurrentID: "agenda-1",
	})
	if err != nil {
		t.Fatalf("UpdateAgendaProgressOverride() error = %v", err)
	}
	state := decodeAgendaProgressWire(t, raw)
	if len(state.Entries) != 1 || state.Entries[0].ID != "agenda-1" || state.Entries[0].ComputedStatus != "not_started" {
		t.Fatalf("synthesized state = %+v, want a single not_started agenda-1 entry", state)
	}
	if state.EffectiveCurrentTopicID != "agenda-1" || state.ManualCurrentTopicID != "agenda-1" {
		t.Fatalf("synthesized current = manual=%q effective=%q, want agenda-1/agenda-1", state.ManualCurrentTopicID, state.EffectiveCurrentTopicID)
	}
}
