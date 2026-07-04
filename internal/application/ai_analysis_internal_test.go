package application

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
)

func TestBuildAnalysisTranscriptTruncatesFromOldest(t *testing.T) {
	segments := []domain.TranscriptSegment{
		{SpeakerName: "田中さん", Text: "最初の発言です。"},
		{SpeakerName: "佐藤さん", Text: "次の発言です。"},
		{SpeakerName: "鈴木さん", Text: "最後の発言です。"},
	}

	full, fullChars := buildAnalysisTranscript(segments, 0)
	if !strings.Contains(full, "最初の発言です。") || !strings.Contains(full, "最後の発言です。") {
		t.Fatalf("full transcript = %q, want all segments", full)
	}
	if fullChars != len([]rune(full)) {
		t.Fatalf("fullChars = %d, want %d", fullChars, len([]rune(full)))
	}

	truncated, truncatedChars, wasTruncated := buildAnalysisTranscriptTruncated(segments, fullChars-1)
	if !wasTruncated {
		t.Fatalf("expected truncation when maxChars < full length")
	}
	if strings.Contains(truncated, "最初の発言です。") {
		t.Fatalf("truncated transcript = %q, want oldest line dropped", truncated)
	}
	if !strings.Contains(truncated, "最後の発言です。") {
		t.Fatalf("truncated transcript = %q, want newest line kept", truncated)
	}
	if truncatedChars >= fullChars {
		t.Fatalf("truncatedChars = %d, want less than fullChars = %d", truncatedChars, fullChars)
	}
}

func TestBuildAnalysisTranscriptSkipsBlankSegments(t *testing.T) {
	segments := []domain.TranscriptSegment{
		{SpeakerName: "田中さん", Text: "   "},
		{SpeakerName: "", Text: "話者不明のテスト発言。"},
	}
	text, chars := buildAnalysisTranscript(segments, 0)
	if strings.Contains(text, "田中さん") {
		t.Fatalf("text = %q, blank segment should be skipped", text)
	}
	if !strings.HasPrefix(text, "話者不明: ") {
		t.Fatalf("text = %q, want fallback speaker label", text)
	}
	if chars != len([]rune(text)) {
		t.Fatalf("chars = %d, want %d", chars, len([]rune(text)))
	}
}

func TestParseAndValidateLiveAnalysisPayloadStripsCodeFence(t *testing.T) {
	content := "```json\n{\"summary\":\"要約です\",\"currentTopic\":\"進捗確認\",\"items\":[{\"id\":\"decision-release\",\"kind\":\"decision\",\"severity\":\"high\",\"title\":\"リリース延期\",\"body\":\"品質課題によりリリースを1週間延期する。\",\"status\":\"open\"}],\"tree\":null}\n```"

	payload, err := parseAndMergeLiveAnalysisPayload(content, nil)
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	if !strings.Contains(string(payload), "要約です") || !strings.Contains(string(payload), "リリース延期") {
		t.Fatalf("payload = %s", string(payload))
	}
}

func TestParseAndValidateLiveAnalysisPayloadParsesV2Schema(t *testing.T) {
	content := `{
		"summary": "要約です",
		"currentTopic": "DBマイグレーション",
		"items": [
			{"id": "risk-db-migration", "kind": "RISK", "severity": "HIGH", "title": "移行中のダウンタイム", "body": "移行作業でダウンタイムが発生する懸念。", "status": "OPEN"}
		],
		"tree": {
			"nodes": [
				{"id": "topic-db", "kind": "TOPIC", "label": "DBマイグレーション"},
				{"id": "risk-db-migration", "kind": "risk", "label": "ダウンタイム懸念"}
			],
			"edges": [
				{"source": "topic-db", "target": "risk-db-migration"}
			]
		}
	}`

	raw, err := parseAndMergeLiveAnalysisPayload(content, nil)
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var payload liveAnalysisPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal normalized payload: %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("items = %+v", payload.Items)
	}
	item := payload.Items[0]
	if item.Kind != "risk" || item.Severity != "high" || item.Status != "open" {
		t.Fatalf("item vocab not lowercased: %+v", item)
	}
	if payload.Tree == nil || len(payload.Tree.Nodes) != 2 || len(payload.Tree.Edges) != 1 {
		t.Fatalf("tree = %+v", payload.Tree)
	}
	if payload.Tree.Nodes[0].Kind != "topic" {
		t.Fatalf("node kind not lowercased: %+v", payload.Tree.Nodes[0])
	}
}

func TestParseAndValidateLiveAnalysisPayloadDropsInvalidItemsOnly(t *testing.T) {
	content := `{
		"summary": "要約です",
		"currentTopic": "進捗確認",
		"items": [
			{"id": "ok-1", "kind": "issue", "severity": "low", "title": "有効なitem", "body": "", "status": "open"},
			{"id": "bad-kind", "kind": "unknown", "severity": "low", "title": "語彙外kind", "body": "落とされる", "status": "open"},
			{"id": "bad-empty", "kind": "risk", "severity": "low", "title": "", "body": "", "status": "open"},
			{"id": "fix-vocab", "kind": "todo", "severity": "urgent", "title": "severity不正は既定値", "body": "", "status": "done"}
		],
		"tree": null
	}`

	raw, err := parseAndMergeLiveAnalysisPayload(content, nil)
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var payload liveAnalysisPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal normalized payload: %v", err)
	}
	if len(payload.Items) != 2 {
		t.Fatalf("items = %+v, want invalid elements dropped", payload.Items)
	}
	if payload.Items[0].ID != "ok-1" || payload.Items[1].ID != "fix-vocab" {
		t.Fatalf("items = %+v", payload.Items)
	}
	if payload.Items[1].Severity != "medium" || payload.Items[1].Status != "open" {
		t.Fatalf("invalid severity/status should fall back to defaults: %+v", payload.Items[1])
	}
}

func TestParseAndValidateLiveAnalysisPayloadDropsEdgesReferencingMissingNodes(t *testing.T) {
	content := `{
		"summary": "要約です",
		"currentTopic": "進捗確認",
		"items": [],
		"tree": {
			"nodes": [
				{"id": "topic-main", "kind": "topic", "label": "進捗確認"},
				{"id": "", "kind": "issue", "label": "id無しノード"},
				{"id": "bad-kind-node", "kind": "todo", "label": "語彙外kind"}
			],
			"edges": [
				{"source": "topic-main", "target": "missing-node"},
				{"source": "topic-main", "target": "topic-main"}
			]
		}
	}`

	raw, err := parseAndMergeLiveAnalysisPayload(content, nil)
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var payload liveAnalysisPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal normalized payload: %v", err)
	}
	if payload.Tree == nil || len(payload.Tree.Nodes) != 1 || payload.Tree.Nodes[0].ID != "topic-main" {
		t.Fatalf("tree nodes = %+v", payload.Tree)
	}
	if len(payload.Tree.Edges) != 1 || payload.Tree.Edges[0].Target != "topic-main" {
		t.Fatalf("tree edges = %+v, want dangling edge dropped", payload.Tree.Edges)
	}
}

func TestParseAndValidateLiveAnalysisPayloadCapsTreeNodes(t *testing.T) {
	var nodes []string
	for i := 0; i < liveAnalysisTreeMaxNodes+3; i++ {
		nodes = append(nodes, fmt.Sprintf(`{"id":"node-%d","kind":"topic","label":"ノード%d"}`, i, i))
	}
	content := fmt.Sprintf(`{"summary":"要約","currentTopic":"","items":[],"tree":{"nodes":[%s],"edges":[{"source":"node-0","target":"node-%d"}]}}`,
		strings.Join(nodes, ","), liveAnalysisTreeMaxNodes+2)

	raw, err := parseAndMergeLiveAnalysisPayload(content, nil)
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var payload liveAnalysisPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal normalized payload: %v", err)
	}
	if payload.Tree == nil || len(payload.Tree.Nodes) != liveAnalysisTreeMaxNodes {
		t.Fatalf("tree nodes = %+v, want capped at %d", payload.Tree, liveAnalysisTreeMaxNodes)
	}
	if len(payload.Tree.Edges) != 0 {
		t.Fatalf("edges = %+v, want edge to trimmed node dropped", payload.Tree.Edges)
	}
}

func TestParseAndValidateLiveAnalysisPayloadRemovesResolvedItemsAndLinkedTreeNodes(t *testing.T) {
	content := `{
		"summary": "要約です",
		"currentTopic": "DBマイグレーション",
		"items": [
			{"id": "risk-db-migration", "kind": "risk", "severity": "high", "title": "移行中のダウンタイム", "body": "解消済み。", "status": "RESOLVED"},
			{"id": "decision-release", "kind": "decision", "severity": "medium", "title": "リリース承認", "body": "", "status": "updated"}
		],
		"tree": {
			"nodes": [
				{"id": "topic-db", "kind": "topic", "label": "DBマイグレーション"},
				{"id": "risk-db-migration", "kind": "risk", "label": "ダウンタイム懸念"},
				{"id": "decision-release", "kind": "decision", "label": "リリース承認"}
			],
			"edges": [
				{"source": "topic-db", "target": "risk-db-migration"},
				{"source": "topic-db", "target": "decision-release"}
			]
		}
	}`

	raw, err := parseAndMergeLiveAnalysisPayload(content, nil)
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var payload liveAnalysisPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal normalized payload: %v", err)
	}
	if len(payload.Items) != 1 || payload.Items[0].ID != "decision-release" {
		t.Fatalf("items = %+v, want resolved item removed", payload.Items)
	}
	if payload.Tree == nil || len(payload.Tree.Nodes) != 2 {
		t.Fatalf("tree = %+v, want resolved node removed", payload.Tree)
	}
	for _, node := range payload.Tree.Nodes {
		if node.ID == "risk-db-migration" {
			t.Fatalf("tree nodes = %+v, resolved node should be removed", payload.Tree.Nodes)
		}
	}
	if len(payload.Tree.Edges) != 1 || payload.Tree.Edges[0].Target != "decision-release" {
		t.Fatalf("tree edges = %+v, want edge to resolved node removed", payload.Tree.Edges)
	}
}

func TestParseAndValidateLiveAnalysisPayloadRemovesItemsListedInResolvedIds(t *testing.T) {
	content := `{
		"summary": "要約です",
		"currentTopic": "DBマイグレーション",
		"resolvedIds": [" risk-db-migration ", "", "missing-id"],
		"items": [
			{"id": "risk-db-migration", "kind": "risk", "severity": "high", "title": "移行中のダウンタイム", "body": "懸念。", "status": "open"},
			{"id": "decision-release", "kind": "decision", "severity": "medium", "title": "リリース承認", "body": "", "status": "updated"}
		],
		"tree": {
			"nodes": [
				{"id": "topic-db", "kind": "topic", "label": "DBマイグレーション"},
				{"id": "risk-db-migration", "kind": "risk", "label": "ダウンタイム懸念"},
				{"id": "decision-release", "kind": "decision", "label": "リリース承認"}
			],
			"edges": [
				{"source": "topic-db", "target": "risk-db-migration"},
				{"source": "topic-db", "target": "decision-release"}
			]
		}
	}`

	raw, err := parseAndMergeLiveAnalysisPayload(content, nil)
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var payload liveAnalysisPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal normalized payload: %v", err)
	}
	if len(payload.Items) != 1 || payload.Items[0].ID != "decision-release" {
		t.Fatalf("items = %+v, want resolvedIds item removed and unknown id ignored", payload.Items)
	}
	if payload.Tree == nil || len(payload.Tree.Nodes) != 2 {
		t.Fatalf("tree = %+v, want resolved node removed", payload.Tree)
	}
	for _, node := range payload.Tree.Nodes {
		if node.ID == "risk-db-migration" {
			t.Fatalf("tree nodes = %+v, resolved node should be removed", payload.Tree.Nodes)
		}
	}
	if len(payload.Tree.Edges) != 1 || payload.Tree.Edges[0].Target != "decision-release" {
		t.Fatalf("tree edges = %+v, want edge to resolved node removed", payload.Tree.Edges)
	}
}

func TestParseAndValidateLiveAnalysisPayloadUnionsResolvedIdsAndResolvedStatus(t *testing.T) {
	content := `{
		"summary": "要約です",
		"currentTopic": "進捗確認",
		"resolvedIds": ["question-schedule"],
		"items": [
			{"id": "question-schedule", "kind": "question", "severity": "low", "title": "スケジュール確認", "body": "", "status": "open"},
			{"id": "risk-budget", "kind": "risk", "severity": "medium", "title": "予算超過懸念", "body": "", "status": "resolved"},
			{"id": "issue-remaining", "kind": "issue", "severity": "low", "title": "残課題", "body": "", "status": "open"}
		],
		"tree": {
			"nodes": [
				{"id": "topic-main", "kind": "topic", "label": "進捗確認"},
				{"id": "question-schedule", "kind": "question", "label": "スケジュール"},
				{"id": "risk-budget", "kind": "risk", "label": "予算"},
				{"id": "issue-remaining", "kind": "issue", "label": "残課題"}
			],
			"edges": [
				{"source": "topic-main", "target": "question-schedule"},
				{"source": "topic-main", "target": "risk-budget"},
				{"source": "topic-main", "target": "issue-remaining"}
			]
		}
	}`

	raw, err := parseAndMergeLiveAnalysisPayload(content, nil)
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var payload liveAnalysisPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal normalized payload: %v", err)
	}
	if len(payload.Items) != 1 || payload.Items[0].ID != "issue-remaining" {
		t.Fatalf("items = %+v, want union of resolvedIds and status resolved removed", payload.Items)
	}
	if payload.Tree == nil || len(payload.Tree.Nodes) != 2 {
		t.Fatalf("tree = %+v, want both resolved nodes removed", payload.Tree)
	}
	for _, node := range payload.Tree.Nodes {
		if node.ID == "question-schedule" || node.ID == "risk-budget" {
			t.Fatalf("tree nodes = %+v, resolved nodes should be removed", payload.Tree.Nodes)
		}
	}
	if len(payload.Tree.Edges) != 1 || payload.Tree.Edges[0].Target != "issue-remaining" {
		t.Fatalf("tree edges = %+v", payload.Tree.Edges)
	}
}

func TestParseAndValidateLiveAnalysisPayloadOmitsResolvedIdsFromNormalizedJSON(t *testing.T) {
	content := `{"summary":"要約です","currentTopic":"進捗確認","resolvedIds":["risk-x"],"items":[],"tree":null}`

	raw, err := parseAndMergeLiveAnalysisPayload(content, nil)
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	if strings.Contains(string(raw), "resolvedIds") {
		t.Fatalf("payload = %s, resolvedIds must not appear in the normalized payload", string(raw))
	}
}

func TestParseAndValidateLiveAnalysisPayloadInsertsCurrentTopicNodeWhenMissing(t *testing.T) {
	content := `{
		"summary": "要約です",
		"currentTopic": "とても長い現在のトピック名で二十文字を超える場合の切詰め確認",
		"items": [],
		"tree": {
			"nodes": [
				{"id": "risk-a", "kind": "risk", "label": "懸念A"},
				{"id": "decision-b", "kind": "decision", "label": "決定B"}
			],
			"edges": [
				{"source": "risk-a", "target": "decision-b"}
			]
		}
	}`

	raw, err := parseAndMergeLiveAnalysisPayload(content, nil)
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var payload liveAnalysisPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal normalized payload: %v", err)
	}
	if payload.Tree == nil || len(payload.Tree.Nodes) != 3 {
		t.Fatalf("tree = %+v, want topic-current inserted", payload.Tree)
	}
	root := payload.Tree.Nodes[0]
	if root.ID != "topic-current" || root.Kind != "topic" {
		t.Fatalf("first node = %+v, want synthetic topic-current at head", root)
	}
	if got := len([]rune(root.Label)); got != 20 {
		t.Fatalf("root label = %q (%d runes), want truncated to 20 runes", root.Label, got)
	}
	// risk-a has no incoming edge, so it must be connected from topic-current.
	// decision-b already has an incoming edge and must not get a second root edge.
	var rootEdges []liveAnalysisTreeEdge
	for _, edge := range payload.Tree.Edges {
		if edge.Source == "topic-current" {
			rootEdges = append(rootEdges, edge)
		}
	}
	if len(rootEdges) != 1 || rootEdges[0].Target != "risk-a" {
		t.Fatalf("root edges = %+v, want exactly topic-current -> risk-a", rootEdges)
	}
	if len(payload.Tree.Edges) != 2 {
		t.Fatalf("edges = %+v, want original edge kept plus one root edge", payload.Tree.Edges)
	}
}

func TestParseAndValidateLiveAnalysisPayloadKeepsExistingTopicNode(t *testing.T) {
	content := `{
		"summary": "要約です",
		"currentTopic": "進捗確認",
		"items": [],
		"tree": {
			"nodes": [
				{"id": "topic-main", "kind": "topic", "label": "進捗確認"},
				{"id": "risk-a", "kind": "risk", "label": "懸念A"}
			],
			"edges": []
		}
	}`

	raw, err := parseAndMergeLiveAnalysisPayload(content, nil)
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var payload liveAnalysisPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal normalized payload: %v", err)
	}
	if payload.Tree == nil || len(payload.Tree.Nodes) != 2 {
		t.Fatalf("tree = %+v, want model structure untouched", payload.Tree)
	}
	if len(payload.Tree.Edges) != 0 {
		t.Fatalf("edges = %+v, want no synthetic root edges when a topic node exists", payload.Tree.Edges)
	}
	for _, node := range payload.Tree.Nodes {
		if node.ID == "topic-current" {
			t.Fatalf("nodes = %+v, topic-current must not be inserted", payload.Tree.Nodes)
		}
	}
}

func TestParseAndValidateLiveAnalysisPayloadSkipsTopicCompletionWhenCurrentTopicEmpty(t *testing.T) {
	content := `{
		"summary": "要約です",
		"currentTopic": "",
		"items": [],
		"tree": {
			"nodes": [{"id": "risk-a", "kind": "risk", "label": "懸念A"}],
			"edges": []
		}
	}`

	raw, err := parseAndMergeLiveAnalysisPayload(content, nil)
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var payload liveAnalysisPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal normalized payload: %v", err)
	}
	if payload.Tree == nil || len(payload.Tree.Nodes) != 1 || payload.Tree.Nodes[0].ID != "risk-a" {
		t.Fatalf("tree = %+v, want no synthetic topic when currentTopic is empty", payload.Tree)
	}
}

func TestParseAndValidateLiveAnalysisPayloadSkipsTopicCompletionOnIDCollision(t *testing.T) {
	content := `{
		"summary": "要約です",
		"currentTopic": "進捗確認",
		"items": [],
		"tree": {
			"nodes": [{"id": "topic-current", "kind": "risk", "label": "idが衝突するノード"}],
			"edges": []
		}
	}`

	raw, err := parseAndMergeLiveAnalysisPayload(content, nil)
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var payload liveAnalysisPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal normalized payload: %v", err)
	}
	if payload.Tree == nil || len(payload.Tree.Nodes) != 1 {
		t.Fatalf("tree = %+v, want no insertion on id collision", payload.Tree)
	}
	if payload.Tree.Nodes[0].Kind != "risk" {
		t.Fatalf("node = %+v, existing node must be kept as-is", payload.Tree.Nodes[0])
	}
}

func TestParseAndValidateLiveAnalysisPayloadCollapsesEmptyTreeToNull(t *testing.T) {
	content := `{"summary":"要約です","currentTopic":"進捗確認","items":[],"tree":{"nodes":[],"edges":[{"source":"a","target":"b"}]}}`

	raw, err := parseAndMergeLiveAnalysisPayload(content, nil)
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	if !strings.Contains(string(raw), `"tree":null`) {
		t.Fatalf("payload = %s, want tree collapsed to null", string(raw))
	}
}

func TestParseAndValidateLiveAnalysisPayloadRejectsEmptyPayload(t *testing.T) {
	// The payload counts as empty only when summary, currentTopic, items,
	// and tree nodes are all empty. Items and nodes fully dropped by
	// validation also count as empty.
	for name, content := range map[string]string{
		"all fields empty":              `{"summary":"","currentTopic":"","items":[],"tree":null}`,
		"only invalid items":            `{"summary":"","currentTopic":"","items":[{"id":"x","kind":"unknown","title":"落ちる"}],"tree":null}`,
		"only tree with no valid nodes": `{"summary":"","currentTopic":"","items":[],"tree":{"nodes":[{"id":"","kind":"topic","label":""}],"edges":[]}}`,
	} {
		if _, err := parseAndMergeLiveAnalysisPayload(content, nil); err == nil {
			t.Fatalf("%s: expected error for empty live analysis payload", name)
		}
	}
}

func TestParseAndValidateLiveAnalysisPayloadAcceptsTreeOnlyPayload(t *testing.T) {
	content := `{"summary":"","currentTopic":"","items":[],"tree":{"nodes":[{"id":"topic-a","kind":"topic","label":"論点A"}],"edges":[]}}`
	if _, err := parseAndMergeLiveAnalysisPayload(content, nil); err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v, tree-only payload should be valid", err)
	}
}

func TestParseAndValidateLiveAnalysisPayloadIgnoresLegacyV1Fields(t *testing.T) {
	content := `{"summary":"要約です","currentTopic":"進捗確認","decisions":[{"text":"旧フィールド"}],"actionItems":[],"openQuestions":["旧"],"concerns":[],"nextChecks":[]}`

	raw, err := parseAndMergeLiveAnalysisPayload(content, nil)
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	if strings.Contains(string(raw), "decisions") || strings.Contains(string(raw), "openQuestions") {
		t.Fatalf("payload = %s, want legacy v1 fields ignored", string(raw))
	}
	if !strings.Contains(string(raw), `"items":[]`) {
		t.Fatalf("payload = %s, want empty items array", string(raw))
	}
}

func TestParseAndValidateLiveAnalysisPayloadRejectsInvalidJSON(t *testing.T) {
	if _, err := parseAndMergeLiveAnalysisPayload("not json", nil); err == nil {
		t.Fatal("expected error for invalid json")
	}
}

const mergeTestPreviousPayload = `{
	"summary": "前回までの要約",
	"currentTopic": "進捗確認",
	"items": [
		{"id": "issue-a", "kind": "issue", "severity": "low", "title": "課題A", "body": "課題Aの説明", "status": "open"},
		{"id": "risk-b", "kind": "risk", "severity": "high", "title": "リスクB", "body": "リスクBの説明", "status": "updated"}
	],
	"tree": {
		"nodes": [
			{"id": "topic-main", "kind": "topic", "label": "進捗確認"},
			{"id": "issue-a", "kind": "issue", "label": "課題A"},
			{"id": "risk-b", "kind": "risk", "label": "リスクB"}
		],
		"edges": [
			{"source": "topic-main", "target": "issue-a"},
			{"source": "topic-main", "target": "risk-b"}
		]
	}
}`

func TestParseAndMergeLiveAnalysisPayloadAppendsNewItemsToPreviousState(t *testing.T) {
	diff := `{
		"summary": "更新後の要約",
		"currentTopic": "新しい話題",
		"resolvedIds": [],
		"items": [
			{"id": "question-c", "kind": "question", "severity": "medium", "title": "質問C", "body": "新しい質問", "status": "open"}
		],
		"tree": null
	}`

	raw, err := parseAndMergeLiveAnalysisPayload(diff, json.RawMessage(mergeTestPreviousPayload))
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var merged liveAnalysisPayload
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("Unmarshal merged payload: %v", err)
	}
	if merged.Summary != "更新後の要約" || merged.CurrentTopic != "新しい話題" {
		t.Fatalf("summary/currentTopic = %q/%q, want replaced by diff", merged.Summary, merged.CurrentTopic)
	}
	if len(merged.Items) != 3 {
		t.Fatalf("items = %+v, want previous 2 + new 1", merged.Items)
	}
	if merged.Items[0].ID != "issue-a" || merged.Items[1].ID != "risk-b" || merged.Items[2].ID != "question-c" {
		t.Fatalf("items order = %+v, want previous order preserved and new appended", merged.Items)
	}
	if merged.Items[2].Status != "open" {
		t.Fatalf("new item status = %q, want open", merged.Items[2].Status)
	}
}

func TestParseAndMergeLiveAnalysisPayloadUpsertsExistingItemByID(t *testing.T) {
	diff := `{
		"summary": "更新後の要約",
		"currentTopic": "進捗確認",
		"items": [
			{"id": "issue-a", "kind": "issue", "severity": "high", "title": "課題A(悪化)", "body": "状況が悪化した", "status": "open"}
		],
		"tree": null
	}`

	raw, err := parseAndMergeLiveAnalysisPayload(diff, json.RawMessage(mergeTestPreviousPayload))
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var merged liveAnalysisPayload
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("Unmarshal merged payload: %v", err)
	}
	if len(merged.Items) != 2 {
		t.Fatalf("items = %+v, want in-place upsert without growth", merged.Items)
	}
	updated := merged.Items[0]
	if updated.ID != "issue-a" || updated.Title != "課題A(悪化)" || updated.Severity != "high" {
		t.Fatalf("upserted item = %+v, want content replaced in place", updated)
	}
	if updated.Status != "updated" {
		t.Fatalf("upserted status = %q, want forced to updated", updated.Status)
	}
	if merged.Items[1].ID != "risk-b" || merged.Items[1].Title != "リスクB" {
		t.Fatalf("untouched item = %+v, want preserved", merged.Items[1])
	}
}

func TestParseAndMergeLiveAnalysisPayloadKeepsStateOnSummaryOnlyDiff(t *testing.T) {
	diff := `{"summary":"要約だけ更新","currentTopic":"","resolvedIds":[],"items":[],"tree":null}`

	raw, err := parseAndMergeLiveAnalysisPayload(diff, json.RawMessage(mergeTestPreviousPayload))
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var merged liveAnalysisPayload
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("Unmarshal merged payload: %v", err)
	}
	if merged.Summary != "要約だけ更新" {
		t.Fatalf("summary = %q", merged.Summary)
	}
	if merged.CurrentTopic != "進捗確認" {
		t.Fatalf("currentTopic = %q, want previous value kept when diff is empty", merged.CurrentTopic)
	}
	if len(merged.Items) != 2 || merged.Items[0].ID != "issue-a" || merged.Items[1].ID != "risk-b" {
		t.Fatalf("items = %+v, want previous items fully preserved", merged.Items)
	}
	if merged.Tree == nil || len(merged.Tree.Nodes) != 3 || len(merged.Tree.Edges) != 2 {
		t.Fatalf("tree = %+v, want previous tree fully preserved", merged.Tree)
	}
}

func TestParseAndMergeLiveAnalysisPayloadRemovesResolvedAndEvictsOldestOverItemCap(t *testing.T) {
	previousItems := make([]string, 0, liveAnalysisItemsMaxCount)
	for i := 0; i < liveAnalysisItemsMaxCount; i++ {
		previousItems = append(previousItems, fmt.Sprintf(`{"id":"item-%d","kind":"issue","severity":"low","title":"項目%d","body":"","status":"open"}`, i, i))
	}
	previous := fmt.Sprintf(`{"summary":"前回","currentTopic":"","items":[%s],"tree":null}`, strings.Join(previousItems, ","))
	diff := `{
		"summary": "更新",
		"currentTopic": "",
		"resolvedIds": ["item-5"],
		"items": [
			{"id": "item-new-1", "kind": "risk", "severity": "high", "title": "新規1", "body": "", "status": "open"},
			{"id": "item-new-2", "kind": "todo", "severity": "low", "title": "新規2", "body": "", "status": "open"}
		],
		"tree": null
	}`

	raw, err := parseAndMergeLiveAnalysisPayload(diff, json.RawMessage(previous))
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var merged liveAnalysisPayload
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("Unmarshal merged payload: %v", err)
	}
	// 30 previous - 1 resolved + 2 new = 31 -> capped at 30, oldest evicted.
	if len(merged.Items) != liveAnalysisItemsMaxCount {
		t.Fatalf("items length = %d, want capped at %d", len(merged.Items), liveAnalysisItemsMaxCount)
	}
	for _, item := range merged.Items {
		if item.ID == "item-5" {
			t.Fatalf("items = %+v, resolved item must be removed", merged.Items)
		}
		if item.ID == "item-0" {
			t.Fatalf("items = %+v, oldest item must be evicted over the cap", merged.Items)
		}
	}
	if merged.Items[len(merged.Items)-1].ID != "item-new-2" || merged.Items[len(merged.Items)-2].ID != "item-new-1" {
		t.Fatalf("items tail = %+v, want new items appended", merged.Items[len(merged.Items)-2:])
	}
}

func TestParseAndMergeLiveAnalysisPayloadMergesTreeWithUpsertAndEdgeDedupe(t *testing.T) {
	diff := `{
		"summary": "更新",
		"currentTopic": "進捗確認",
		"items": [],
		"tree": {
			"nodes": [
				{"id": "issue-a", "kind": "issue", "label": "課題A(更新)"},
				{"id": "decision-d", "kind": "decision", "label": "決定D"}
			],
			"edges": [
				{"source": "topic-main", "target": "issue-a"},
				{"source": "topic-main", "target": "decision-d"}
			]
		}
	}`

	raw, err := parseAndMergeLiveAnalysisPayload(diff, json.RawMessage(mergeTestPreviousPayload))
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var merged liveAnalysisPayload
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("Unmarshal merged payload: %v", err)
	}
	if merged.Tree == nil || len(merged.Tree.Nodes) != 4 {
		t.Fatalf("tree nodes = %+v, want previous 3 + new 1", merged.Tree)
	}
	if merged.Tree.Nodes[1].ID != "issue-a" || merged.Tree.Nodes[1].Label != "課題A(更新)" {
		t.Fatalf("upserted node = %+v, want label updated in place", merged.Tree.Nodes[1])
	}
	if merged.Tree.Nodes[3].ID != "decision-d" {
		t.Fatalf("nodes = %+v, want new node appended", merged.Tree.Nodes)
	}
	// The duplicate topic-main -> issue-a edge must be deduplicated:
	// previous 2 edges + diff 2 edges - 1 duplicate = 3.
	if len(merged.Tree.Edges) != 3 {
		t.Fatalf("edges = %+v, want deduplicated union", merged.Tree.Edges)
	}
}

func TestParseAndMergeLiveAnalysisPayloadCapsMergedTreeKeepingTopicNodes(t *testing.T) {
	previousNodes := []string{`{"id":"topic-main","kind":"topic","label":"トピック"}`}
	var previousEdges []string
	for i := 0; i < liveAnalysisTreeMaxNodes; i++ {
		previousNodes = append(previousNodes, fmt.Sprintf(`{"id":"node-%d","kind":"issue","label":"ノード%d"}`, i, i))
		previousEdges = append(previousEdges, fmt.Sprintf(`{"source":"topic-main","target":"node-%d"}`, i))
	}
	previous := fmt.Sprintf(`{"summary":"前回","currentTopic":"トピック","items":[],"tree":{"nodes":[%s],"edges":[%s]}}`,
		strings.Join(previousNodes, ","), strings.Join(previousEdges, ","))
	diff := `{
		"summary": "更新",
		"currentTopic": "トピック",
		"items": [],
		"tree": {
			"nodes": [{"id": "node-new", "kind": "risk", "label": "新ノード"}],
			"edges": [{"source": "topic-main", "target": "node-new"}]
		}
	}`

	raw, err := parseAndMergeLiveAnalysisPayload(diff, json.RawMessage(previous))
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var merged liveAnalysisPayload
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("Unmarshal merged payload: %v", err)
	}
	if merged.Tree == nil || len(merged.Tree.Nodes) != liveAnalysisTreeMaxNodes {
		t.Fatalf("tree nodes = %+v, want capped at %d", merged.Tree, liveAnalysisTreeMaxNodes)
	}
	hasTopic := false
	for _, node := range merged.Tree.Nodes {
		if node.ID == "topic-main" {
			hasTopic = true
		}
		if node.ID == "node-0" {
			t.Fatalf("nodes = %+v, oldest non-topic node must be evicted", merged.Tree.Nodes)
		}
	}
	if !hasTopic {
		t.Fatalf("nodes = %+v, topic node must survive the cap", merged.Tree.Nodes)
	}
	for _, edge := range merged.Tree.Edges {
		if edge.Target == "node-0" {
			t.Fatalf("edges = %+v, edge to evicted node must be removed", merged.Tree.Edges)
		}
	}
	foundNewEdge := false
	for _, edge := range merged.Tree.Edges {
		if edge.Target == "node-new" {
			foundNewEdge = true
		}
	}
	if !foundNewEdge {
		t.Fatalf("edges = %+v, want new edge kept", merged.Tree.Edges)
	}
}

func TestParseAndMergeLiveAnalysisPayloadToleratesInvalidPreviousPayload(t *testing.T) {
	diff := `{"summary":"要約","currentTopic":"話題","items":[{"id":"issue-a","kind":"issue","severity":"low","title":"課題A","body":"","status":"open"}],"tree":null}`

	raw, err := parseAndMergeLiveAnalysisPayload(diff, json.RawMessage(`{broken json`))
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v, invalid previous payload must degrade to empty state", err)
	}
	var merged liveAnalysisPayload
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("Unmarshal merged payload: %v", err)
	}
	if len(merged.Items) != 1 || merged.Items[0].ID != "issue-a" {
		t.Fatalf("items = %+v, want diff applied to empty state", merged.Items)
	}
}

func TestParseAndMergeLiveAnalysisPayloadIsIdempotentWhenModelEchoesFullState(t *testing.T) {
	// A weak model may re-output the entire previous state instead of a
	// diff. Upsert-by-id must keep the result stable (no duplication).
	raw, err := parseAndMergeLiveAnalysisPayload(mergeTestPreviousPayload, json.RawMessage(mergeTestPreviousPayload))
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var merged liveAnalysisPayload
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("Unmarshal merged payload: %v", err)
	}
	if len(merged.Items) != 2 || merged.Items[0].ID != "issue-a" || merged.Items[1].ID != "risk-b" {
		t.Fatalf("items = %+v, want no duplication on full-state echo", merged.Items)
	}
	for _, item := range merged.Items {
		if item.Status != "updated" {
			t.Fatalf("item = %+v, echoed items are treated as updates", item)
		}
	}
	if merged.Tree == nil || len(merged.Tree.Nodes) != 3 || len(merged.Tree.Edges) != 2 {
		t.Fatalf("tree = %+v, want unchanged structure", merged.Tree)
	}
}

func TestParseAndValidateFinalAnalysisPayloadParsesSchema(t *testing.T) {
	content := `{"suggestedTitle":"週次定例","overview":"概要です","decisions":[{"text":"リリース承認","importance":"high"}],"actionItems":[{"text":"資料作成","owner":"田中","due":"来週","priority":"medium"}],"openIssues":["未確定の予算"],"keyPoints":["重要な論点"],"nextMeetingTopics":["次回議題"]}`

	payload, err := parseAndValidateFinalAnalysisPayload(content)
	if err != nil {
		t.Fatalf("parseAndValidateFinalAnalysisPayload() error = %v", err)
	}
	if !strings.Contains(string(payload), "週次定例") || !strings.Contains(string(payload), "リリース承認") {
		t.Fatalf("payload = %s", string(payload))
	}
}

func TestParseAndValidateFinalAnalysisPayloadRejectsEmptyPayload(t *testing.T) {
	if _, err := parseAndValidateFinalAnalysisPayload(`{"suggestedTitle":"","overview":"","decisions":[],"actionItems":[],"openIssues":[],"keyPoints":[],"nextMeetingTopics":[]}`); err == nil {
		t.Fatal("expected error for empty final analysis payload")
	}
}

func TestLiveAnalysisBackoffCapsAtMaxBackoff(t *testing.T) {
	interval := 10 * time.Second
	if got := liveAnalysisBackoff(interval, 0); got != interval {
		t.Fatalf("liveAnalysisBackoff(0) = %s, want %s", got, interval)
	}
	if got := liveAnalysisBackoff(interval, 1); got != 20*time.Second {
		t.Fatalf("liveAnalysisBackoff(1) = %s, want 20s", got)
	}
	if got := liveAnalysisBackoff(interval, 10); got != meetingAnalysisMaxBackoff {
		t.Fatalf("liveAnalysisBackoff(10) = %s, want capped at %s", got, meetingAnalysisMaxBackoff)
	}
}

func TestMeetingSessionPreContextRenderSkipsEmptyFields(t *testing.T) {
	preContext := &meetingSessionPreContext{Purpose: "意思決定", Concerns: "期限が近い"}
	if preContext.isEmpty() {
		t.Fatal("preContext should not be empty")
	}
	rendered := preContext.render()
	if !strings.Contains(rendered, "目的: 意思決定") || !strings.Contains(rendered, "懸念点: 期限が近い") {
		t.Fatalf("rendered = %q", rendered)
	}
	if strings.Contains(rendered, "アジェンダ") {
		t.Fatalf("rendered = %q, should not include empty agenda", rendered)
	}

	empty := &meetingSessionPreContext{}
	if !empty.isEmpty() {
		t.Fatal("empty preContext should report isEmpty() == true")
	}
}

func TestBuildLiveAnalysisUserPromptIncludesNullForFirstRun(t *testing.T) {
	prompt := buildLiveAnalysisUserPrompt(nil, nil, "田中さん: こんにちは")
	if !strings.Contains(prompt, "null") {
		t.Fatalf("prompt = %q, want null previous state", prompt)
	}
	if !strings.Contains(prompt, "田中さん: こんにちは") {
		t.Fatalf("prompt = %q, want diff text included", prompt)
	}
}

func TestBuildFinalAnalysisUserPromptNotesTruncation(t *testing.T) {
	prompt := buildFinalAnalysisUserPrompt(nil, nil, "田中さん: 発言", true)
	if !strings.Contains(prompt, "省略されています") {
		t.Fatalf("prompt = %q, want truncation notice", prompt)
	}
}
