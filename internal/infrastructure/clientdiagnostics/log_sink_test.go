package clientdiagnostics

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestLogSinkWritesTreeRenderAnomalyMetricsAsStructuredJSON(t *testing.T) {
	var output bytes.Buffer
	sink := NewLogSink(&output)
	event := newEvent("session_render", "tree_render_anomaly")
	event.TreeVersion = int64Pointer(16)
	event.Details = map[string]any{
		"reason":               "react_flow_store_mismatch",
		"storeNodeCount":       22,
		"reactFlowNodeCount":   0,
		"renderedDomNodeCount": 0,
		"containerWidth":       900,
		"containerHeight":      600,
		"zoom":                 1,
		"renderCommitted":      false,
	}

	if err := sink.WriteClientDiagnosticEvent(event); err != nil {
		t.Fatalf("WriteClientDiagnosticEvent: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &decoded); err != nil {
		t.Fatalf("decode Docker-compatible JSON log: %v\n%s", err, output.String())
	}
	if decoded["component"] != "client_diagnostics" ||
		decoded["event"] != "tree_render_anomaly" ||
		decoded["sessionId"] != "session_render" ||
		decoded["treeVersion"] != float64(16) {
		t.Fatalf("unexpected structured fields: %v", decoded)
	}
	details, ok := decoded["details"].(map[string]any)
	if !ok {
		t.Fatalf("details missing from structured log: %v", decoded)
	}
	for _, key := range []string{
		"storeNodeCount", "reactFlowNodeCount", "renderedDomNodeCount",
		"containerWidth", "containerHeight", "zoom", "renderCommitted",
	} {
		if _, exists := details[key]; !exists {
			t.Errorf("details.%s missing: %v", key, details)
		}
	}
}

func int64Pointer(value int64) *int64 {
	return &value
}
