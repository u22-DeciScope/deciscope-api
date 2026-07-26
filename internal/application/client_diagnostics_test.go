package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
)

type recordingSink struct {
	events []domain.ClientDiagnosticEvent
	err    error
}

func (s *recordingSink) WriteClientDiagnosticEvent(event domain.ClientDiagnosticEvent) error {
	s.events = append(s.events, event)
	return s.err
}

func newTestService(t *testing.T, sink ClientDiagnosticsSink, options ...ClientDiagnosticsServiceOption) *ClientDiagnosticsService {
	t.Helper()
	base := []ClientDiagnosticsServiceOption{WithClientDiagnosticsSink("test", sink)}
	return NewClientDiagnosticsService(append(base, options...)...)
}

func batchOf(events ...ClientDiagnosticEventInput) ClientDiagnosticsBatchInput {
	return ClientDiagnosticsBatchInput{
		WorkspaceID:          "w_test",
		SessionID:            "session_abc",
		TabID:                "tab_1",
		FrontendBuildVersion: "abc1234",
		UserID:               "u_test",
		Events:               events,
	}
}

func int64Pointer(value int64) *int64 {
	return &value
}

func TestClientDiagnosticsRecordsKnownEvents(t *testing.T) {
	sink := &recordingSink{}
	service := newTestService(t, sink)

	result, err := service.Record(context.Background(), batchOf(ClientDiagnosticEventInput{
		Timestamp:      "2026-07-25T01:02:03.500Z",
		Event:          string(domain.ClientDiagnosticTreeStateChanged),
		Route:          "/w/w_test/meetings/session_abc",
		TreeVersion:    int64Pointer(12),
		NodeCount:      int64Pointer(5),
		RootNodeID:     "node_root",
		SessionStatus:  "recording",
		SnapshotSource: "websocket",
		Sequence:       7,
	}))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if result.Accepted != 1 || result.Rejected != 0 {
		t.Fatalf("result = %+v, want 1 accepted", result)
	}
	if len(sink.events) != 1 {
		t.Fatalf("sink events = %d, want 1", len(sink.events))
	}
	event := sink.events[0]
	if event.Event != string(domain.ClientDiagnosticTreeStateChanged) {
		t.Errorf("event = %q", event.Event)
	}
	if event.SessionID != "session_abc" || event.WorkspaceID != "w_test" {
		t.Errorf("session/workspace = %q/%q", event.SessionID, event.WorkspaceID)
	}
	if event.UserID != "u_test" {
		t.Errorf("userId = %q, want the authenticated user", event.UserID)
	}
	if event.TabID != "tab_1" || event.FrontendBuildVersion != "abc1234" {
		t.Errorf("tabId/build = %q/%q", event.TabID, event.FrontendBuildVersion)
	}
	if event.Timestamp.UTC().Format(time.RFC3339Nano) != "2026-07-25T01:02:03.5Z" {
		t.Errorf("timestamp = %s", event.Timestamp.UTC().Format(time.RFC3339Nano))
	}
	if event.NodeCount == nil || *event.NodeCount != 5 {
		t.Errorf("nodeCount = %v", event.NodeCount)
	}
}

func TestClientDiagnosticsRejectsUnknownEventName(t *testing.T) {
	sink := &recordingSink{}
	service := newTestService(t, sink)

	result, err := service.Record(context.Background(), batchOf(
		ClientDiagnosticEventInput{Event: "tree_exploded"},
		ClientDiagnosticEventInput{Event: ""},
		ClientDiagnosticEventInput{Event: string(domain.ClientDiagnosticTreeBecameEmpty)},
	))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if result.Accepted != 1 || result.Rejected != 2 {
		t.Fatalf("result = %+v, want 1 accepted / 2 rejected", result)
	}
	if result.Reasons["unknown_event_name"] != 1 || result.Reasons["missing_event_name"] != 1 {
		t.Fatalf("reasons = %v", result.Reasons)
	}
	if len(sink.events) != 1 || sink.events[0].Event != string(domain.ClientDiagnosticTreeBecameEmpty) {
		t.Fatalf("sink events = %+v", sink.events)
	}
}

func TestClientDiagnosticsRejectsMismatchedSessionOrWorkspace(t *testing.T) {
	sink := &recordingSink{}
	service := newTestService(t, sink)

	result, err := service.Record(context.Background(), batchOf(
		ClientDiagnosticEventInput{Event: string(domain.ClientDiagnosticTreeStateChanged), SessionID: "session_other"},
		ClientDiagnosticEventInput{Event: string(domain.ClientDiagnosticTreeStateChanged), WorkspaceID: "w_other"},
	))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if result.Accepted != 0 || result.Rejected != 2 {
		t.Fatalf("result = %+v, want both rejected", result)
	}
	if result.Reasons["session_id_mismatch"] != 1 || result.Reasons["workspace_id_mismatch"] != 1 {
		t.Fatalf("reasons = %v", result.Reasons)
	}
}

func TestClientDiagnosticsRedactsSensitiveFields(t *testing.T) {
	sink := &recordingSink{}
	service := newTestService(t, sink)

	_, err := service.Record(context.Background(), batchOf(ClientDiagnosticEventInput{
		Event: string(domain.ClientDiagnosticSnapshotRejected),
		Route: "/w/w_test/meetings/session_abc?email=leaked@example.com",
		Details: map[string]any{
			"authorization":  "Bearer secret-value",
			"sessionToken":   "abc.def.ghi",
			"userEmail":      "user@example.com",
			"cookie":         "deciscope_session=xyz",
			"transcriptText": "会議の発言そのもの",
			"nodeLabel":      "議題タイトル",
			"textLength":     42,
			"reason":         "stale_tree_version",
			"nested": map[string]any{
				"apiKey":  "k-123",
				"comment": "contact me at person@example.com",
			},
		},
	}))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if len(sink.events) != 1 {
		t.Fatalf("sink events = %d", len(sink.events))
	}
	details := sink.events[0].Details
	for _, key := range []string{"authorization", "sessionToken", "userEmail", "cookie", "transcriptText", "nodeLabel"} {
		if details[key] != redactedPlaceholder {
			t.Errorf("details[%q] = %v, want redacted", key, details[key])
		}
	}
	if details["reason"] != "stale_tree_version" {
		t.Errorf("details[reason] = %v, want preserved", details["reason"])
	}
	if value, ok := details["textLength"].(float64); ok && value != 42 {
		t.Errorf("details[textLength] = %v, want the numeric metric preserved", details["textLength"])
	}
	nested, ok := details["nested"].(map[string]any)
	if !ok {
		t.Fatalf("details[nested] = %T", details["nested"])
	}
	if nested["apiKey"] != redactedPlaceholder {
		t.Errorf("nested[apiKey] = %v, want redacted", nested["apiKey"])
	}
	if comment, _ := nested["comment"].(string); strings.Contains(comment, "person@example.com") {
		t.Errorf("nested[comment] = %q, want the address scrubbed", comment)
	}
	if strings.Contains(sink.events[0].Route, "leaked@example.com") {
		t.Errorf("route = %q, want the address scrubbed", sink.events[0].Route)
	}
}

func TestClientDiagnosticsTruncatesOversizedDetails(t *testing.T) {
	sink := &recordingSink{}
	service := newTestService(t, sink, WithClientDiagnosticsLimits(ClientDiagnosticsLimits{
		MaxDetailsBytes: 512,
		MaxStringBytes:  128,
	}))

	_, err := service.Record(context.Background(), batchOf(ClientDiagnosticEventInput{
		Event: string(domain.ClientDiagnosticReactErrorCaptured),
		Details: map[string]any{
			"componentStack": strings.Repeat("a", 4000),
			"errorName":      "TypeError",
		},
	}))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	details := sink.events[0].Details
	stack, _ := details["componentStack"].(string)
	if len(stack) > 128 {
		t.Errorf("componentStack length = %d, want clamped to 128", len(stack))
	}
	if details["_truncated"] != true {
		t.Errorf("details[_truncated] = %v, want true", details["_truncated"])
	}
	if details["errorName"] != "TypeError" {
		t.Errorf("errorName = %v, want preserved", details["errorName"])
	}
}

func TestClientDiagnosticsLimitsEventsPerRequest(t *testing.T) {
	sink := &recordingSink{}
	service := newTestService(t, sink, WithClientDiagnosticsLimits(ClientDiagnosticsLimits{
		MaxEventsPerRequest: 2,
		ThrottleWindow:      -1,
	}))

	events := make([]ClientDiagnosticEventInput, 0, 5)
	for index := 0; index < 5; index++ {
		events = append(events, ClientDiagnosticEventInput{
			Event:     string(domain.ClientDiagnosticTreeStateChanged),
			NodeCount: int64Pointer(int64(index)),
		})
	}
	result, err := service.Record(context.Background(), batchOf(events...))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if result.Accepted != 2 || result.Rejected != 3 {
		t.Fatalf("result = %+v, want 2 accepted / 3 rejected", result)
	}
	if result.Reasons["too_many_events"] != 3 {
		t.Fatalf("reasons = %v", result.Reasons)
	}
}

func TestClientDiagnosticsThrottlesDuplicatesButNotAnomalies(t *testing.T) {
	sink := &recordingSink{}
	now := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	service := newTestService(t, sink,
		WithClientDiagnosticsClock(func() time.Time { return now }),
		WithClientDiagnosticsLimits(ClientDiagnosticsLimits{ThrottleWindow: time.Second}),
	)

	duplicate := ClientDiagnosticEventInput{
		Event:       string(domain.ClientDiagnosticTreeStateChanged),
		NodeCount:   int64Pointer(5),
		TreeVersion: int64Pointer(3),
	}
	result, err := service.Record(context.Background(), batchOf(duplicate, duplicate, duplicate))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if result.Accepted != 1 || result.Suppressed != 2 {
		t.Fatalf("result = %+v, want 1 accepted / 2 suppressed", result)
	}

	// 内容が変われば抑制されない。
	changed := duplicate
	changed.NodeCount = int64Pointer(6)
	result, err = service.Record(context.Background(), batchOf(changed))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if result.Accepted != 1 {
		t.Fatalf("result = %+v, want the changed event accepted", result)
	}

	// 異常イベントは同一内容でも必ず記録する。
	anomaly := ClientDiagnosticEventInput{
		Event:     string(domain.ClientDiagnosticTreeBecameEmpty),
		NodeCount: int64Pointer(0),
	}
	result, err = service.Record(context.Background(), batchOf(anomaly, anomaly))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if result.Accepted != 2 || result.Suppressed != 0 {
		t.Fatalf("result = %+v, want both anomalies accepted", result)
	}

	// 時間窓を過ぎれば再び記録する。
	now = now.Add(2 * time.Second)
	result, err = service.Record(context.Background(), batchOf(duplicate))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if result.Accepted != 1 {
		t.Fatalf("result = %+v, want the event accepted after the window", result)
	}
}

func TestClientDiagnosticsReportsSinkFailureWithoutFailingBatch(t *testing.T) {
	failing := &recordingSink{err: context.DeadlineExceeded}
	var reported []string
	service := newTestService(t, failing, WithClientDiagnosticsSinkErrorReporter(func(sink string, _ error) {
		reported = append(reported, sink)
	}))

	result, err := service.Record(context.Background(), batchOf(ClientDiagnosticEventInput{
		Event: string(domain.ClientDiagnosticTreeBecameEmpty),
	}))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if result.Accepted != 1 {
		t.Fatalf("result = %+v, want the event accepted despite the sink error", result)
	}
	if len(reported) != 1 || reported[0] != "test" {
		t.Fatalf("reported = %v, want the sink failure surfaced", reported)
	}
}

func TestClientDiagnosticsRejectsEmptyBatch(t *testing.T) {
	service := newTestService(t, &recordingSink{})
	if _, err := service.Record(context.Background(), ClientDiagnosticsBatchInput{}); err == nil {
		t.Fatal("Record() error = nil, want ErrClientDiagnosticsBatchEmpty")
	}
}
