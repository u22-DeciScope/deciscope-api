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

func TestParseAndValidateLiveAnalysisPayloadNormalizesTreeNodeDetails(t *testing.T) {
	content := `{
		"summary": "要約です",
		"currentTopic": "DBマイグレーション",
		"items": [
			{"id": "risk-db-migration", "kind": "risk", "severity": "high", "title": "移行中のダウンタイム", "body": "懸念。", "status": "open"},
			{"id": "question-maintenance", "kind": "question", "severity": "medium", "title": "保守時間の確認", "body": "時間帯を確認する。", "status": "open"}
		],
		"tree": {
			"nodes": [
				{
					"id": "risk-db-migration",
					"kind": "risk",
					"label": "ダウンタイム懸念",
					"description": "移行作業でサービス停止が起きる可能性を確認している。",
					"relatedItemIds": ["question-maintenance", "missing-id", "risk-db-migration", "question-maintenance"]
				}
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
	if payload.Tree == nil {
		t.Fatalf("tree = %+v", payload.Tree)
	}
	var node *liveAnalysisTreeNode
	for i := range payload.Tree.Nodes {
		if payload.Tree.Nodes[i].ID == "risk-db-migration" {
			node = &payload.Tree.Nodes[i]
			break
		}
	}
	if node == nil {
		t.Fatalf("tree nodes = %+v, want risk-db-migration", payload.Tree.Nodes)
	}
	if node.Description != "移行作業でサービス停止が起きる可能性を確認している。" {
		t.Fatalf("description = %q", node.Description)
	}
	wantIDs := []string{"risk-db-migration", "question-maintenance"}
	if fmt.Sprint(node.RelatedItemIDs) != fmt.Sprint(wantIDs) {
		t.Fatalf("relatedItemIds = %+v, want %+v", node.RelatedItemIDs, wantIDs)
	}
}

func TestParseAndMergeLiveAnalysisPayloadKeepsResolvedRelatedItemIDs(t *testing.T) {
	previous := `{
		"summary": "前回",
		"currentTopic": "移行計画",
		"items": [
			{"id": "risk-db-migration", "kind": "risk", "severity": "high", "title": "停止懸念", "body": "懸念。", "status": "open"},
			{"id": "question-maintenance", "kind": "question", "severity": "medium", "title": "保守時間", "body": "確認。", "status": "open"}
		],
		"tree": {
			"nodes": [
				{"id": "topic-db", "kind": "topic", "label": "DB移行", "relatedItemIds": ["risk-db-migration"]},
				{"id": "issue-plan", "kind": "issue", "label": "移行計画", "description": "前回説明", "relatedItemIds": ["risk-db-migration", "question-maintenance"]}
			],
			"edges": [{"source": "topic-db", "target": "issue-plan"}]
		}
	}`
	diff := `{
		"summary": "要約更新",
		"currentTopic": "",
		"resolvedIds": ["risk-db-migration"],
		"items": [],
		"tree": null
	}`

	raw, err := parseAndMergeLiveAnalysisPayload(diff, json.RawMessage(previous))
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var payload liveAnalysisPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal normalized payload: %v", err)
	}
	if len(payload.Items) != 2 || payload.Items[0].ID != "risk-db-migration" || payload.Items[0].Status != "resolved" {
		t.Fatalf("items = %+v, want resolved item retained", payload.Items)
	}
	var issueNode *liveAnalysisTreeNode
	for i := range payload.Tree.Nodes {
		if payload.Tree.Nodes[i].ID == "issue-plan" {
			issueNode = &payload.Tree.Nodes[i]
			break
		}
	}
	if issueNode == nil {
		t.Fatalf("tree nodes = %+v, want issue-plan", payload.Tree.Nodes)
	}
	wantIDs := []string{"risk-db-migration", "question-maintenance"}
	if fmt.Sprint(issueNode.RelatedItemIDs) != fmt.Sprint(wantIDs) {
		t.Fatalf("relatedItemIds = %+v, want %+v", issueNode.RelatedItemIDs, wantIDs)
	}
}

func TestParseAndMergeLiveAnalysisPayloadPreservesTreeDescriptionOnNodeUpsert(t *testing.T) {
	previous := `{
		"summary": "前回",
		"currentTopic": "移行計画",
		"items": [
			{"id": "issue-plan", "kind": "issue", "severity": "medium", "title": "移行計画", "body": "説明。", "status": "open"}
		],
		"tree": {
			"nodes": [
				{"id": "issue-plan", "kind": "issue", "label": "移行計画", "description": "前回の短い説明", "relatedItemIds": ["issue-plan"]}
			],
			"edges": []
		}
	}`
	diff := `{
		"summary": "要約更新",
		"currentTopic": "",
		"resolvedIds": [],
		"items": [],
		"tree": {
			"nodes": [
				{"id": "issue-plan", "kind": "issue", "label": "移行計画の見直し"}
			],
			"edges": []
		}
	}`

	raw, err := parseAndMergeLiveAnalysisPayload(diff, json.RawMessage(previous))
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var payload liveAnalysisPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal normalized payload: %v", err)
	}
	if payload.Tree == nil {
		t.Fatalf("tree = %+v", payload.Tree)
	}
	var node *liveAnalysisTreeNode
	for i := range payload.Tree.Nodes {
		if payload.Tree.Nodes[i].ID == "issue-plan" {
			node = &payload.Tree.Nodes[i]
			break
		}
	}
	if node == nil {
		t.Fatalf("tree nodes = %+v, want issue-plan", payload.Tree.Nodes)
	}
	if node.Label != "移行計画の見直し" || node.Description != "前回の短い説明" {
		t.Fatalf("node = %+v, want updated label and preserved description", node)
	}
	if fmt.Sprint(node.RelatedItemIDs) != fmt.Sprint([]string{"issue-plan"}) {
		t.Fatalf("relatedItemIds = %+v", node.RelatedItemIDs)
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
				{"id": "bad-kind-node", "kind": "unknown", "label": "語彙外kind"}
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
	// The original "node-0 -> node-(max+2)" edge must be dropped because
	// node-0 was evicted by capping. All surviving nodes are topic-kind and
	// none of them have an incoming edge, so connectOrphanLiveAnalysisTreeNodes
	// now wires every non-primary survivor to the primary (oldest surviving)
	// topic node rather than leaving them orphaned.
	if len(payload.Tree.Edges) != liveAnalysisTreeMaxNodes-1 {
		t.Fatalf("edges = %+v, want %d auto-connect edges from primary topic node", payload.Tree.Edges, liveAnalysisTreeMaxNodes-1)
	}
	primaryTopicID := payload.Tree.Nodes[0].ID
	for _, edge := range payload.Tree.Edges {
		if edge.Source != primaryTopicID {
			t.Fatalf("edge = %+v, want source = primary topic node %q", edge, primaryTopicID)
		}
	}
}

func TestParseAndValidateLiveAnalysisPayloadConnectsOrphanSecondTopicNode(t *testing.T) {
	content := `{
		"summary": "要約です",
		"currentTopic": "新しい話題",
		"items": [],
		"tree": {
			"nodes": [
				{"id": "topic-a", "kind": "topic", "label": "話題A"},
				{"id": "issue-x", "kind": "issue", "label": "課題X"},
				{"id": "topic-b", "kind": "topic", "label": "話題B"}
			],
			"edges": [
				{"source": "topic-a", "target": "issue-x"}
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
		t.Fatalf("tree nodes = %+v, want 3 nodes kept", payload.Tree)
	}
	found := false
	for _, edge := range payload.Tree.Edges {
		if edge.Source == "topic-a" && edge.Target == "topic-b" {
			found = true
		}
	}
	if !found {
		t.Fatalf("edges = %+v, want auto-connect edge from primary topic-a to orphaned topic-b", payload.Tree.Edges)
	}
	if len(payload.Tree.Edges) != 2 {
		t.Fatalf("edges = %+v, want exactly the original edge plus the auto-connect edge", payload.Tree.Edges)
	}
}

func TestParseAndValidateLiveAnalysisPayloadSkipsAutoConnectWhenSecondTopicHasIncomingEdge(t *testing.T) {
	content := `{
		"summary": "要約です",
		"currentTopic": "新しい話題",
		"items": [],
		"tree": {
			"nodes": [
				{"id": "topic-a", "kind": "topic", "label": "話題A"},
				{"id": "issue-x", "kind": "issue", "label": "課題X"},
				{"id": "topic-b", "kind": "topic", "label": "話題B"}
			],
			"edges": [
				{"source": "topic-a", "target": "issue-x"},
				{"source": "issue-x", "target": "topic-b"}
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
		t.Fatalf("tree nodes = %+v, want 3 nodes kept", payload.Tree)
	}
	// topic-b already has an incoming edge from issue-x, so no extra edge
	// from the primary topic should be added.
	if len(payload.Tree.Edges) != 2 {
		t.Fatalf("edges = %+v, want original 2 edges unchanged", payload.Tree.Edges)
	}
	for _, edge := range payload.Tree.Edges {
		if edge.Source == "topic-a" && edge.Target == "topic-b" {
			t.Fatalf("edges = %+v, want no redundant topic-a -> topic-b edge", payload.Tree.Edges)
		}
	}
}

func TestParseAndValidateLiveAnalysisPayloadKeepsResolvedItemsAndLinkedTreeNodes(t *testing.T) {
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
	if len(payload.Items) != 2 || payload.Items[0].ID != "risk-db-migration" || payload.Items[0].Status != "resolved" {
		t.Fatalf("items = %+v, want resolved item retained", payload.Items)
	}
	if payload.Tree == nil || len(payload.Tree.Nodes) != 3 {
		t.Fatalf("tree = %+v, want resolved node retained", payload.Tree)
	}
	var resolvedNode *liveAnalysisTreeNode
	for _, node := range payload.Tree.Nodes {
		if node.ID == "risk-db-migration" {
			node := node
			resolvedNode = &node
		}
	}
	if resolvedNode == nil || resolvedNode.Status != "resolved" {
		t.Fatalf("tree nodes = %+v, want resolved node marked resolved", payload.Tree.Nodes)
	}
	if len(payload.Tree.Edges) != 2 {
		t.Fatalf("tree edges = %+v, want edges to resolved node retained", payload.Tree.Edges)
	}
}

func TestParseAndValidateLiveAnalysisPayloadMarksItemsListedInResolvedIds(t *testing.T) {
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
	if len(payload.Items) != 2 || payload.Items[0].ID != "risk-db-migration" || payload.Items[0].Status != "resolved" {
		t.Fatalf("items = %+v, want resolvedIds item retained and marked resolved", payload.Items)
	}
	if payload.Tree == nil || len(payload.Tree.Nodes) != 3 {
		t.Fatalf("tree = %+v, want resolved node retained", payload.Tree)
	}
	var resolvedNode *liveAnalysisTreeNode
	for _, node := range payload.Tree.Nodes {
		if node.ID == "risk-db-migration" {
			node := node
			resolvedNode = &node
		}
	}
	if resolvedNode == nil || resolvedNode.Status != "resolved" {
		t.Fatalf("tree nodes = %+v, want resolved node marked resolved", payload.Tree.Nodes)
	}
	if len(payload.Tree.Edges) != 2 {
		t.Fatalf("tree edges = %+v, want edge to resolved node retained", payload.Tree.Edges)
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
	if len(payload.Items) != 3 {
		t.Fatalf("items = %+v, want resolved items retained", payload.Items)
	}
	statusByID := make(map[string]string)
	for _, item := range payload.Items {
		statusByID[item.ID] = item.Status
	}
	if statusByID["question-schedule"] != "resolved" || statusByID["risk-budget"] != "resolved" || statusByID["issue-remaining"] != "open" {
		t.Fatalf("items = %+v, want union of resolvedIds and status resolved marked resolved", payload.Items)
	}
	if payload.Tree == nil || len(payload.Tree.Nodes) != 4 {
		t.Fatalf("tree = %+v, want resolved nodes retained", payload.Tree)
	}
	nodeStatusByID := make(map[string]string)
	for _, node := range payload.Tree.Nodes {
		nodeStatusByID[node.ID] = node.Status
	}
	if nodeStatusByID["question-schedule"] != "resolved" || nodeStatusByID["risk-budget"] != "resolved" {
		t.Fatalf("tree nodes = %+v, want resolved nodes marked resolved", payload.Tree.Nodes)
	}
	if len(payload.Tree.Edges) != 3 {
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
		t.Fatalf("tree = %+v, want model nodes untouched", payload.Tree)
	}
	if len(payload.Tree.Edges) != 1 || payload.Tree.Edges[0].Source != "topic-main" || payload.Tree.Edges[0].Target != "risk-a" {
		t.Fatalf("edges = %+v, want existing topic to connect orphan non-topic node", payload.Tree.Edges)
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

func TestParseAndMergeLiveAnalysisPayloadMarksResolvedAndEvictsOldestOverItemCap(t *testing.T) {
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
	// liveAnalysisItemsMaxCount previous + 2 new items, of which item-5
	// becomes resolved via resolvedIds. Active (non-resolved) and resolved
	// items are now capped independently (liveAnalysisItemsMaxCount /
	// liveAnalysisResolvedItemsMaxCount), so the single resolved item never
	// counts against the active cap: the active items are capped at
	// liveAnalysisItemsMaxCount (oldest active, item-0, evicted) plus the
	// 1 resolved item (item-5, retained).
	wantLen := liveAnalysisItemsMaxCount + 1
	if len(merged.Items) != wantLen {
		t.Fatalf("items length = %d, want %d (active capped at %d plus 1 retained resolved item)", len(merged.Items), wantLen, liveAnalysisItemsMaxCount)
	}
	foundResolved := false
	foundItem1 := false
	for _, item := range merged.Items {
		if item.ID == "item-5" && item.Status == "resolved" {
			foundResolved = true
		}
		if item.ID == "item-1" {
			foundItem1 = true
		}
		if item.ID == "item-0" {
			t.Fatalf("items = %+v, oldest active item must be evicted over the active cap", merged.Items)
		}
	}
	if !foundResolved {
		t.Fatalf("items = %+v, resolved item must be retained and marked resolved", merged.Items)
	}
	if !foundItem1 {
		t.Fatalf("items = %+v, item-1 must be retained: only 1 active item is over cap so only item-0 is evicted", merged.Items)
	}
	if merged.Items[len(merged.Items)-1].ID != "item-new-2" || merged.Items[len(merged.Items)-2].ID != "item-new-1" {
		t.Fatalf("items tail = %+v, want new items appended", merged.Items[len(merged.Items)-2:])
	}
}

func TestParseAndMergeLiveAnalysisPayloadKeepsAllActiveItemsWhenManyResolvedItemsExist(t *testing.T) {
	// liveAnalysisResolvedItemsMaxCount previous resolved items already sit
	// at the resolved cap. A diff that adds liveAnalysisItemsMaxCount new
	// active items must not be squeezed by the resolved bucket: active and
	// resolved are capped independently, so all the new active items must
	// survive alongside all the resolved items.
	var previousItems []string
	for i := 0; i < liveAnalysisResolvedItemsMaxCount; i++ {
		previousItems = append(previousItems, fmt.Sprintf(`{"id":"item-r%d","kind":"issue","severity":"low","title":"解決済%d","body":"","status":"resolved"}`, i, i))
	}
	previous := fmt.Sprintf(`{"summary":"前回","currentTopic":"","items":[%s],"tree":null}`, strings.Join(previousItems, ","))

	var diffItems []string
	for i := 0; i < liveAnalysisItemsMaxCount; i++ {
		diffItems = append(diffItems, fmt.Sprintf(`{"id":"item-a%d","kind":"issue","severity":"low","title":"進行中%d","body":"","status":"open"}`, i, i))
	}
	diff := fmt.Sprintf(`{"summary":"更新","currentTopic":"","items":[%s],"tree":null}`, strings.Join(diffItems, ","))

	raw, err := parseAndMergeLiveAnalysisPayload(diff, json.RawMessage(previous))
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var merged liveAnalysisPayload
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("Unmarshal merged payload: %v", err)
	}
	wantLen := liveAnalysisResolvedItemsMaxCount + liveAnalysisItemsMaxCount
	if len(merged.Items) != wantLen {
		t.Fatalf("items length = %d, want %d (all resolved + all active retained)", len(merged.Items), wantLen)
	}
	seen := make(map[string]bool, len(merged.Items))
	for _, item := range merged.Items {
		seen[item.ID] = true
	}
	for i := 0; i < liveAnalysisResolvedItemsMaxCount; i++ {
		if !seen[fmt.Sprintf("item-r%d", i)] {
			t.Fatalf("items = %+v, want resolved item-r%d retained", merged.Items, i)
		}
	}
	for i := 0; i < liveAnalysisItemsMaxCount; i++ {
		if !seen[fmt.Sprintf("item-a%d", i)] {
			t.Fatalf("items = %+v, want active item-a%d retained (not evicted by resolved items)", merged.Items, i)
		}
	}
}

func TestParseAndMergeLiveAnalysisPayloadKeepsResolvedItemWhenManyActiveItemsExist(t *testing.T) {
	// A single resolved item plus liveAnalysisItemsMaxCount previous active
	// items (at the active cap). A diff flooding in far more new active
	// items must evict only active items over the active cap; the resolved
	// item must survive regardless of how much active churn happens
	// afterward.
	previousItems := []string{`{"id":"item-r","kind":"issue","severity":"low","title":"解決済","body":"","status":"resolved"}`}
	for i := 0; i < liveAnalysisItemsMaxCount; i++ {
		previousItems = append(previousItems, fmt.Sprintf(`{"id":"item-%d","kind":"issue","severity":"low","title":"項目%d","body":"","status":"open"}`, i, i))
	}
	previous := fmt.Sprintf(`{"summary":"前回","currentTopic":"","items":[%s],"tree":null}`, strings.Join(previousItems, ","))

	var diffItems []string
	for i := 0; i < 40; i++ {
		diffItems = append(diffItems, fmt.Sprintf(`{"id":"item-new-%d","kind":"issue","severity":"low","title":"新規%d","body":"","status":"open"}`, i, i))
	}
	diff := fmt.Sprintf(`{"summary":"更新","currentTopic":"","items":[%s],"tree":null}`, strings.Join(diffItems, ","))

	raw, err := parseAndMergeLiveAnalysisPayload(diff, json.RawMessage(previous))
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var merged liveAnalysisPayload
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("Unmarshal merged payload: %v", err)
	}
	wantLen := 1 + liveAnalysisItemsMaxCount
	if len(merged.Items) != wantLen {
		t.Fatalf("items length = %d, want %d (1 resolved + active capped at %d)", len(merged.Items), wantLen, liveAnalysisItemsMaxCount)
	}
	foundResolved := false
	for _, item := range merged.Items {
		if item.ID == "item-r" {
			foundResolved = true
			if item.Status != "resolved" {
				t.Fatalf("item-r status = %q, want resolved", item.Status)
			}
		}
	}
	if !foundResolved {
		t.Fatalf("items = %+v, resolved item-r must survive a flood of new active items", merged.Items)
	}
}

func TestParseAndMergeLiveAnalysisPayloadEvictsOldestResolvedItemsOverResolvedCap(t *testing.T) {
	// liveAnalysisResolvedItemsMaxCount resolved items already sit at the
	// resolved cap. A diff that marks 5 more items resolved must evict only
	// the oldest resolved items (not active items), keeping the resolved
	// bucket at its cap.
	var previousItems []string
	for i := 0; i < liveAnalysisResolvedItemsMaxCount; i++ {
		previousItems = append(previousItems, fmt.Sprintf(`{"id":"item-r%d","kind":"issue","severity":"low","title":"解決済%d","body":"","status":"resolved"}`, i, i))
	}
	previous := fmt.Sprintf(`{"summary":"前回","currentTopic":"","items":[%s],"tree":null}`, strings.Join(previousItems, ","))

	var diffItems []string
	for i := liveAnalysisResolvedItemsMaxCount; i < liveAnalysisResolvedItemsMaxCount+5; i++ {
		diffItems = append(diffItems, fmt.Sprintf(`{"id":"item-r%d","kind":"issue","severity":"low","title":"解決済%d","body":"","status":"resolved"}`, i, i))
	}
	diff := fmt.Sprintf(`{"summary":"更新","currentTopic":"","items":[%s],"tree":null}`, strings.Join(diffItems, ","))

	raw, err := parseAndMergeLiveAnalysisPayload(diff, json.RawMessage(previous))
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var merged liveAnalysisPayload
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("Unmarshal merged payload: %v", err)
	}
	if len(merged.Items) != liveAnalysisResolvedItemsMaxCount {
		t.Fatalf("items length = %d, want capped at %d", len(merged.Items), liveAnalysisResolvedItemsMaxCount)
	}
	seen := make(map[string]bool, len(merged.Items))
	for _, item := range merged.Items {
		seen[item.ID] = true
	}
	for i := 0; i < 5; i++ {
		if seen[fmt.Sprintf("item-r%d", i)] {
			t.Fatalf("items = %+v, oldest resolved item-r%d must be evicted over the resolved cap", merged.Items, i)
		}
	}
	for i := 5; i < liveAnalysisResolvedItemsMaxCount+5; i++ {
		if !seen[fmt.Sprintf("item-r%d", i)] {
			t.Fatalf("items = %+v, want item-r%d retained", merged.Items, i)
		}
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

func TestParseAndMergeLiveAnalysisPayloadKeepsResolvedAndActiveTreeNodesTogether(t *testing.T) {
	// 1 topic + (liveAnalysisTreeMaxNodes-1) active non-topic nodes (at the
	// active cap) plus liveAnalysisTreeMaxResolvedNodes resolved non-topic
	// nodes (at the resolved cap) must all coexist: resolved and active
	// nodes are capped in independent buckets, so being
	// at both caps simultaneously must not evict anything.
	previousNodes := []string{`{"id":"topic-main","kind":"topic","label":"トピック"}`}
	var previousEdges []string
	for i := 0; i < liveAnalysisTreeMaxNodes-1; i++ {
		previousNodes = append(previousNodes, fmt.Sprintf(`{"id":"node-a%d","kind":"issue","label":"進行中%d"}`, i, i))
		previousEdges = append(previousEdges, fmt.Sprintf(`{"source":"topic-main","target":"node-a%d"}`, i))
	}
	for i := 0; i < liveAnalysisTreeMaxResolvedNodes; i++ {
		previousNodes = append(previousNodes, fmt.Sprintf(`{"id":"node-r%d","kind":"issue","label":"解決済%d","status":"resolved"}`, i, i))
		previousEdges = append(previousEdges, fmt.Sprintf(`{"source":"topic-main","target":"node-r%d"}`, i))
	}
	previous := fmt.Sprintf(`{"summary":"前回","currentTopic":"トピック","items":[],"tree":{"nodes":[%s],"edges":[%s]}}`,
		strings.Join(previousNodes, ","), strings.Join(previousEdges, ","))
	diff := `{"summary":"更新","currentTopic":"トピック","items":[],"tree":null}`

	raw, err := parseAndMergeLiveAnalysisPayload(diff, json.RawMessage(previous))
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var merged liveAnalysisPayload
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("Unmarshal merged payload: %v", err)
	}
	wantLen := liveAnalysisTreeMaxNodes + liveAnalysisTreeMaxResolvedNodes
	if merged.Tree == nil || len(merged.Tree.Nodes) != wantLen {
		t.Fatalf("tree nodes = %+v, want %d (topic + active + resolved all retained)", merged.Tree, wantLen)
	}
	seen := make(map[string]bool, len(merged.Tree.Nodes))
	for _, node := range merged.Tree.Nodes {
		seen[node.ID] = true
	}
	if !seen["topic-main"] {
		t.Fatalf("nodes = %+v, want topic-main retained", merged.Tree.Nodes)
	}
	for i := 0; i < liveAnalysisTreeMaxNodes-1; i++ {
		if !seen[fmt.Sprintf("node-a%d", i)] {
			t.Fatalf("nodes = %+v, want active node-a%d retained", merged.Tree.Nodes, i)
		}
	}
	for i := 0; i < liveAnalysisTreeMaxResolvedNodes; i++ {
		if !seen[fmt.Sprintf("node-r%d", i)] {
			t.Fatalf("nodes = %+v, want resolved node-r%d retained", merged.Tree.Nodes, i)
		}
	}
}

func TestParseAndMergeLiveAnalysisPayloadEvictsOldestResolvedTreeNodesOverResolvedCap(t *testing.T) {
	// liveAnalysisTreeMaxResolvedNodes resolved nodes already sit at the
	// resolved cap. A diff that adds one more resolved node must evict only
	// the oldest resolved node, and must not touch the topic node or any
	// active node.
	previousNodes := []string{`{"id":"topic-main","kind":"topic","label":"トピック"}`}
	var previousEdges []string
	for i := 0; i < liveAnalysisTreeMaxResolvedNodes; i++ {
		previousNodes = append(previousNodes, fmt.Sprintf(`{"id":"node-r%d","kind":"issue","label":"解決済%d","status":"resolved"}`, i, i))
		previousEdges = append(previousEdges, fmt.Sprintf(`{"source":"topic-main","target":"node-r%d"}`, i))
	}
	previous := fmt.Sprintf(`{"summary":"前回","currentTopic":"トピック","items":[],"tree":{"nodes":[%s],"edges":[%s]}}`,
		strings.Join(previousNodes, ","), strings.Join(previousEdges, ","))
	diff := fmt.Sprintf(`{
		"summary": "更新",
		"currentTopic": "トピック",
		"items": [],
		"tree": {
			"nodes": [{"id": "node-r%d", "kind": "issue", "label": "解決済(新)", "status": "resolved"}],
			"edges": [{"source": "topic-main", "target": "node-r%d"}]
		}
	}`, liveAnalysisTreeMaxResolvedNodes, liveAnalysisTreeMaxResolvedNodes)

	raw, err := parseAndMergeLiveAnalysisPayload(diff, json.RawMessage(previous))
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var merged liveAnalysisPayload
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("Unmarshal merged payload: %v", err)
	}
	wantLen := 1 + liveAnalysisTreeMaxResolvedNodes
	if merged.Tree == nil || len(merged.Tree.Nodes) != wantLen {
		t.Fatalf("tree nodes = %+v, want %d (topic + resolved capped at %d)", merged.Tree, wantLen, liveAnalysisTreeMaxResolvedNodes)
	}
	seen := make(map[string]bool, len(merged.Tree.Nodes))
	for _, node := range merged.Tree.Nodes {
		seen[node.ID] = true
	}
	if !seen["topic-main"] {
		t.Fatalf("nodes = %+v, want topic-main retained", merged.Tree.Nodes)
	}
	if seen["node-r0"] {
		t.Fatalf("nodes = %+v, oldest resolved node-r0 must be evicted over the resolved cap", merged.Tree.Nodes)
	}
	for i := 1; i <= liveAnalysisTreeMaxResolvedNodes; i++ {
		if !seen[fmt.Sprintf("node-r%d", i)] {
			t.Fatalf("nodes = %+v, want node-r%d retained", merged.Tree.Nodes, i)
		}
	}
	for _, edge := range merged.Tree.Edges {
		if edge.Target == "node-r0" {
			t.Fatalf("edges = %+v, edge to evicted node-r0 must be removed", merged.Tree.Edges)
		}
	}
	foundNewEdge := false
	for _, edge := range merged.Tree.Edges {
		if edge.Source == "topic-main" && edge.Target == fmt.Sprintf("node-r%d", liveAnalysisTreeMaxResolvedNodes) {
			foundNewEdge = true
		}
	}
	if !foundNewEdge {
		t.Fatalf("edges = %+v, want edge to new resolved node kept", merged.Tree.Edges)
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

// The following tests cover the item->tree synthesis fix: when the model
// reports a new/updated item but omits the corresponding tree node (a
// pattern observed in production where items keep growing while
// tree.nodes stays fixed), mergeLiveAnalysisTree now synthesizes a node for
// it so every item card is guaranteed to have a matching tree node.

func TestParseAndMergeLiveAnalysisPayloadSynthesizesNodeForItemWithoutTreeNode(t *testing.T) {
	previous := `{
		"summary": "前回",
		"currentTopic": "進捗確認",
		"items": [],
		"tree": {
			"nodes": [{"id": "topic-main", "kind": "topic", "label": "進捗確認"}],
			"edges": []
		}
	}`
	diff := `{
		"summary": "更新",
		"currentTopic": "進捗確認",
		"items": [
			{"id": "issue-new", "kind": "issue", "severity": "medium", "title": "新しい課題", "body": "詳細説明です。", "status": "open"}
		],
		"tree": null
	}`

	raw, err := parseAndMergeLiveAnalysisPayload(diff, json.RawMessage(previous))
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var payload liveAnalysisPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal normalized payload: %v", err)
	}
	if payload.Tree == nil || len(payload.Tree.Nodes) != 2 {
		t.Fatalf("tree = %+v, want topic-main plus synthesized issue-new node", payload.Tree)
	}
	var node *liveAnalysisTreeNode
	for i := range payload.Tree.Nodes {
		if payload.Tree.Nodes[i].ID == "issue-new" {
			node = &payload.Tree.Nodes[i]
		}
	}
	if node == nil {
		t.Fatalf("tree nodes = %+v, want a synthesized issue-new node", payload.Tree.Nodes)
	}
	if node.Kind != "issue" || node.Label != "新しい課題" || node.Description != "詳細説明です。" {
		t.Fatalf("synthesized node = %+v, want fields derived from the item", node)
	}
	if node.Status != "" {
		t.Fatalf("synthesized node status = %q, want empty for a non-resolved item", node.Status)
	}
	if fmt.Sprint(node.RelatedItemIDs) != fmt.Sprint([]string{"issue-new"}) {
		t.Fatalf("synthesized node relatedItemIds = %+v, want [issue-new]", node.RelatedItemIDs)
	}
	foundEdge := false
	for _, edge := range payload.Tree.Edges {
		if edge.Source == "topic-main" && edge.Target == "issue-new" {
			foundEdge = true
		}
	}
	if !foundEdge {
		t.Fatalf("edges = %+v, want synthesized node auto-connected to the primary topic node", payload.Tree.Edges)
	}
}

func TestParseAndMergeLiveAnalysisPayloadDoesNotSynthesizeNodeWhenNodeAlreadyExists(t *testing.T) {
	previous := `{
		"summary": "前回",
		"currentTopic": "進捗確認",
		"items": [
			{"id": "issue-a", "kind": "issue", "severity": "low", "title": "課題A", "body": "既存の説明", "status": "open"}
		],
		"tree": {
			"nodes": [
				{"id": "topic-main", "kind": "topic", "label": "進捗確認"},
				{"id": "issue-a", "kind": "issue", "label": "課題A", "description": "既存の説明"}
			],
			"edges": [{"source": "topic-main", "target": "issue-a"}]
		}
	}`
	diff := `{
		"summary": "更新",
		"currentTopic": "進捗確認",
		"items": [
			{"id": "issue-a", "kind": "issue", "severity": "low", "title": "課題A(更新)", "body": "更新後の説明", "status": "open"}
		],
		"tree": null
	}`

	raw, err := parseAndMergeLiveAnalysisPayload(diff, json.RawMessage(previous))
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var payload liveAnalysisPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal normalized payload: %v", err)
	}
	if payload.Tree == nil || len(payload.Tree.Nodes) != 2 {
		t.Fatalf("tree = %+v, want no duplicate node created for issue-a", payload.Tree)
	}
	count := 0
	for _, node := range payload.Tree.Nodes {
		if node.ID == "issue-a" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("tree nodes = %+v, want exactly one issue-a node", payload.Tree.Nodes)
	}
}

func TestParseAndMergeLiveAnalysisPayloadSynthesizesResolvedNodeStatus(t *testing.T) {
	content := `{
		"summary": "要約です",
		"currentTopic": "進捗確認",
		"resolvedIds": ["risk-x"],
		"items": [
			{"id": "risk-x", "kind": "risk", "severity": "high", "title": "リスクX", "body": "解消済みの懸念", "status": "open"}
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
	if payload.Tree == nil {
		t.Fatalf("tree = %+v, want a synthesized node for the resolved item", payload.Tree)
	}
	var node *liveAnalysisTreeNode
	for i := range payload.Tree.Nodes {
		if payload.Tree.Nodes[i].ID == "risk-x" {
			node = &payload.Tree.Nodes[i]
		}
	}
	if node == nil {
		t.Fatalf("tree nodes = %+v, want synthesized risk-x node", payload.Tree.Nodes)
	}
	if node.Status != "resolved" {
		t.Fatalf("synthesized node status = %q, want resolved", node.Status)
	}
}

func TestParseAndMergeLiveAnalysisPayloadSynthesizesFallbackKindForTodoItem(t *testing.T) {
	content := `{
		"summary": "要約です",
		"currentTopic": "進捗確認",
		"items": [
			{"id": "todo-a", "kind": "todo", "severity": "low", "title": "資料作成", "body": "田中さんが来週までに作成", "status": "open"}
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
	if payload.Tree == nil {
		t.Fatalf("tree = %+v, want a synthesized node for the todo item", payload.Tree)
	}
	var node *liveAnalysisTreeNode
	for i := range payload.Tree.Nodes {
		if payload.Tree.Nodes[i].ID == "todo-a" {
			node = &payload.Tree.Nodes[i]
		}
	}
	if node == nil {
		t.Fatalf("tree nodes = %+v, want synthesized todo-a node", payload.Tree.Nodes)
	}
	// "todo" is not a valid tree node kind (validLiveAnalysisTreeNodeKind), so
	// it must be mapped via liveAnalysisItemKindToTreeNodeKindFallback to the
	// closest valid kind, "issue".
	if node.Kind != "issue" {
		t.Fatalf("synthesized node kind = %q, want fallback to issue for a todo item", node.Kind)
	}
}

func TestMergeLiveAnalysisTreeSkipsSynthesisForDismissedItem(t *testing.T) {
	// "dismissed" is not currently a value normalizeLiveAnalysisItems ever
	// produces (invalid statuses fall back to "open"), but mergeLiveAnalysisTree
	// itself must still treat a dismissed item defensively -- it must never
	// synthesize a tree node for one, whatever upstream code passes in.
	items := []liveAnalysisItem{
		{ID: "issue-a", Kind: "issue", Title: "課題A", Body: "説明", Status: "dismissed"},
	}
	tree := mergeLiveAnalysisTree(nil, nil, map[string]struct{}{}, "", items, nil)
	if tree != nil {
		t.Fatalf("tree = %+v, want nil: a dismissed item must not get a synthesized node", tree)
	}
}

func TestMergeLiveAnalysisTreeCollectsDiagnosticsOnStats(t *testing.T) {
	diff := &liveAnalysisTree{
		Nodes: []liveAnalysisTreeNode{
			{ID: "topic-main", Kind: "topic", Label: "進捗確認"},
			{ID: "bad-label", Kind: "issue", Label: ""},
			{ID: "bad-kind", Kind: "unknown", Label: "語彙外kind"},
		},
		Edges: []liveAnalysisTreeEdge{
			{Source: "topic-main", Target: "missing-node"},
		},
	}
	items := []liveAnalysisItem{
		{ID: "issue-new", Kind: "issue", Title: "新しい課題", Body: "詳細", Status: "open"},
	}

	stats := &liveAnalysisTreeMergeStats{}
	tree := mergeLiveAnalysisTree(nil, diff, map[string]struct{}{}, "進捗確認", items, stats)
	if tree == nil {
		t.Fatal("tree = nil, want topic-main plus synthesized issue-new node")
	}
	if stats.DroppedEmptyLabel != 1 {
		t.Fatalf("DroppedEmptyLabel = %d, want 1 (bad-label)", stats.DroppedEmptyLabel)
	}
	if stats.DroppedInvalidKind != 1 {
		t.Fatalf("DroppedInvalidKind = %d, want 1 (bad-kind)", stats.DroppedInvalidKind)
	}
	if stats.droppedNodes() != 2 {
		t.Fatalf("droppedNodes() = %d, want 2", stats.droppedNodes())
	}
	if stats.DroppedEdges != 1 {
		t.Fatalf("DroppedEdges = %d, want 1 (edge to missing-node)", stats.DroppedEdges)
	}
	if stats.SynthesizedNodes != 1 {
		t.Fatalf("SynthesizedNodes = %d, want 1 (issue-new)", stats.SynthesizedNodes)
	}
	if got := stats.droppedNodeReasons(); got != "[emptyLabel:1 invalidKind:1]" {
		t.Fatalf("droppedNodeReasons() = %q, want %q", got, "[emptyLabel:1 invalidKind:1]")
	}
}

func TestParseAndMergeLiveAnalysisPayloadRescuesTodoKindTreeNode(t *testing.T) {
	// モデルが tree.nodes に item専用の kind "todo" を直接出したケース。tree の
	// 語彙には "todo" が無いため、救済が無いと invalidKind として捨てられてしまう。
	content := `{
		"summary": "要約です",
		"currentTopic": "進捗確認",
		"items": [],
		"tree": {
			"nodes": [
				{"id": "x", "kind": "todo", "label": "作業", "description": "資料を作成する"}
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
	if payload.Tree == nil {
		t.Fatalf("tree = %+v, want kind \"todo\" node rescued as issue", payload.Tree)
	}
	var node *liveAnalysisTreeNode
	for i := range payload.Tree.Nodes {
		if payload.Tree.Nodes[i].ID == "x" {
			node = &payload.Tree.Nodes[i]
		}
	}
	if node == nil {
		t.Fatalf("tree nodes = %+v, want node x kept", payload.Tree.Nodes)
	}
	if node.Kind != "issue" {
		t.Fatalf("node kind = %q, want \"issue\" (todo rescued)", node.Kind)
	}
}

func TestMergeLiveAnalysisTreeCountsNormalizedTodoNodes(t *testing.T) {
	diff := &liveAnalysisTree{
		Nodes: []liveAnalysisTreeNode{
			{ID: "x", Kind: "todo", Label: "作業"},
		},
	}
	stats := &liveAnalysisTreeMergeStats{}

	tree := mergeLiveAnalysisTree(nil, diff, map[string]struct{}{}, "", nil, stats)
	if tree == nil {
		t.Fatal("tree = nil, want node x kept")
	}
	if stats.NormalizedTodoNodes != 1 {
		t.Fatalf("NormalizedTodoNodes = %d, want 1", stats.NormalizedTodoNodes)
	}
	if stats.droppedNodes() != 0 {
		t.Fatalf("droppedNodes() = %d, want 0 (todo must not be dropped)", stats.droppedNodes())
	}
}

func TestMergeLiveAnalysisTreeKeepsAllValidTreeNodeKinds(t *testing.T) {
	// 回帰確認: topic/issue/question/risk/decision は従来どおりすべて残る。
	diff := &liveAnalysisTree{
		Nodes: []liveAnalysisTreeNode{
			{ID: "n-topic", Kind: "topic", Label: "トピック"},
			{ID: "n-issue", Kind: "issue", Label: "課題"},
			{ID: "n-question", Kind: "question", Label: "質問"},
			{ID: "n-risk", Kind: "risk", Label: "リスク"},
			{ID: "n-decision", Kind: "decision", Label: "決定"},
		},
	}
	stats := &liveAnalysisTreeMergeStats{}

	tree := mergeLiveAnalysisTree(nil, diff, map[string]struct{}{}, "", nil, stats)
	if tree == nil || len(tree.Nodes) != 5 {
		t.Fatalf("tree = %+v, want all 5 nodes kept", tree)
	}
	if stats.droppedNodes() != 0 {
		t.Fatalf("droppedNodes() = %d, want 0", stats.droppedNodes())
	}
}

func TestMergeLiveAnalysisTreeStillDropsUnknownKind(t *testing.T) {
	// "todo" 以外の未知kindは、従来どおり invalidKind として drop されなければならない。
	diff := &liveAnalysisTree{
		Nodes: []liveAnalysisTreeNode{
			{ID: "n-bad", Kind: "foobar", Label: "未知kind"},
		},
	}
	stats := &liveAnalysisTreeMergeStats{}

	tree := mergeLiveAnalysisTree(nil, diff, map[string]struct{}{}, "", nil, stats)
	if tree != nil {
		t.Fatalf("tree = %+v, want nil (only node dropped)", tree)
	}
	if stats.DroppedInvalidKind != 1 {
		t.Fatalf("DroppedInvalidKind = %d, want 1", stats.DroppedInvalidKind)
	}
	if stats.NormalizedTodoNodes != 0 {
		t.Fatalf("NormalizedTodoNodes = %d, want 0 (foobar must not be rescued)", stats.NormalizedTodoNodes)
	}
}

func TestMergeLiveAnalysisTreeCountsDiffNewAndUpdatedNodes(t *testing.T) {
	previous := &liveAnalysisTree{
		Nodes: []liveAnalysisTreeNode{
			{ID: "existing", Kind: "issue", Label: "既存の課題"},
		},
	}
	diff := &liveAnalysisTree{
		Nodes: []liveAnalysisTreeNode{
			{ID: "existing", Kind: "issue", Label: "更新後の課題"},
			{ID: "new", Kind: "issue", Label: "新規の課題"},
		},
	}
	stats := &liveAnalysisTreeMergeStats{}

	tree := mergeLiveAnalysisTree(previous, diff, map[string]struct{}{}, "", nil, stats)
	if tree == nil || len(tree.Nodes) != 2 {
		t.Fatalf("tree = %+v, want existing + new nodes", tree)
	}
	if stats.DiffUpdatedNodes != 1 {
		t.Fatalf("DiffUpdatedNodes = %d, want 1 (existing)", stats.DiffUpdatedNodes)
	}
	if stats.DiffNewNodes != 1 {
		t.Fatalf("DiffNewNodes = %d, want 1 (new)", stats.DiffNewNodes)
	}
}

func TestFinalizeLiveAnalysisTreePrunesRedundantTopicFallbackEdgeWhenRealParentExists(t *testing.T) {
	// node-x was first rescued by connectOrphanLiveAnalysisTreeNodes in an
	// earlier round (topic-main -> node-x), but the model has since reported
	// its real parent node-y -> node-x. The stale topic fallback must be
	// pruned because node-x now has an incoming edge from a source other
	// than the topic. node-y itself has no incoming edge yet, so this round's
	// connectOrphanLiveAnalysisTreeNodes rescues it with topic-main -> node-y,
	// which must be left alone (it is node-y's only incoming edge).
	nodes := []liveAnalysisTreeNode{
		{ID: "topic-main", Kind: "topic", Label: "トピック"},
		{ID: "node-y", Kind: "issue", Label: "Y"},
		{ID: "node-x", Kind: "issue", Label: "X"},
	}
	edges := []liveAnalysisTreeEdge{
		{Source: "topic-main", Target: "node-x"},
		{Source: "node-y", Target: "node-x"},
	}
	stats := &liveAnalysisTreeMergeStats{}

	tree := finalizeLiveAnalysisTree(nodes, edges, "", stats)
	if tree == nil {
		t.Fatal("tree = nil, want non-nil")
	}
	for _, edge := range tree.Edges {
		if edge.Source == "topic-main" && edge.Target == "node-x" {
			t.Fatalf("edges = %+v, want redundant topic-main -> node-x fallback pruned", tree.Edges)
		}
	}
	foundRealEdge := false
	foundRescueEdge := false
	for _, edge := range tree.Edges {
		if edge.Source == "node-y" && edge.Target == "node-x" {
			foundRealEdge = true
		}
		if edge.Source == "topic-main" && edge.Target == "node-y" {
			foundRescueEdge = true
		}
	}
	if !foundRealEdge {
		t.Fatalf("edges = %+v, want node-y -> node-x kept", tree.Edges)
	}
	if !foundRescueEdge {
		t.Fatalf("edges = %+v, want topic-main -> node-y rescue edge kept (its only incoming edge)", tree.Edges)
	}
	if len(tree.Edges) != 2 {
		t.Fatalf("edges = %+v, want exactly 2 edges", tree.Edges)
	}
	if stats.PrunedTopicEdges != 1 {
		t.Fatalf("PrunedTopicEdges = %d, want 1", stats.PrunedTopicEdges)
	}
}

func TestFinalizeLiveAnalysisTreeKeepsSoleTopicFallbackEdge(t *testing.T) {
	// node-x has exactly one incoming edge, the topic fallback itself. This
	// is precisely the shape connectOrphanLiveAnalysisTreeNodes produces for
	// a freshly rescued node, and pruning must never touch it.
	nodes := []liveAnalysisTreeNode{
		{ID: "topic-main", Kind: "topic", Label: "トピック"},
		{ID: "node-x", Kind: "issue", Label: "X"},
	}
	edges := []liveAnalysisTreeEdge{
		{Source: "topic-main", Target: "node-x"},
	}
	stats := &liveAnalysisTreeMergeStats{}

	tree := finalizeLiveAnalysisTree(nodes, edges, "", stats)
	if tree == nil || len(tree.Edges) != 1 {
		t.Fatalf("tree = %+v, want the sole topic -> node-x edge kept", tree)
	}
	if tree.Edges[0].Source != "topic-main" || tree.Edges[0].Target != "node-x" {
		t.Fatalf("edges = %+v, want topic-main -> node-x unchanged", tree.Edges)
	}
	if stats.PrunedTopicEdges != 0 {
		t.Fatalf("PrunedTopicEdges = %d, want 0", stats.PrunedTopicEdges)
	}
}

func TestFinalizeLiveAnalysisTreePrunesChainedRedundantTopicFallbackEdges(t *testing.T) {
	// node-z has no incoming edge at all, so this round's
	// connectOrphanLiveAnalysisTreeNodes rescues it with topic-main ->
	// node-z (kept: it is node-z's only incoming edge). node-y and node-x
	// each carry a stale topic fallback from an earlier round, but each now
	// also has its real parent edge (node-z -> node-y, node-y -> node-x).
	// Both stale fallbacks must be pruned in a single pass because the
	// reduced graph (built from only the surviving, non-candidate edges)
	// already contains the full chain topic-main -> node-z -> node-y ->
	// node-x, so node-x stays reachable even with both topic-main -> node-x
	// and topic-main -> node-y removed.
	nodes := []liveAnalysisTreeNode{
		{ID: "topic-main", Kind: "topic", Label: "トピック"},
		{ID: "node-z", Kind: "issue", Label: "Z"},
		{ID: "node-y", Kind: "issue", Label: "Y"},
		{ID: "node-x", Kind: "issue", Label: "X"},
	}
	edges := []liveAnalysisTreeEdge{
		{Source: "topic-main", Target: "node-x"},
		{Source: "node-y", Target: "node-x"},
		{Source: "topic-main", Target: "node-y"},
		{Source: "node-z", Target: "node-y"},
	}
	stats := &liveAnalysisTreeMergeStats{}

	tree := finalizeLiveAnalysisTree(nodes, edges, "", stats)
	if tree == nil {
		t.Fatal("tree = nil, want non-nil")
	}
	for _, edge := range tree.Edges {
		if edge.Source == "topic-main" && (edge.Target == "node-x" || edge.Target == "node-y") {
			t.Fatalf("edges = %+v, want both chained topic fallbacks pruned", tree.Edges)
		}
	}
	want := map[string]bool{
		"node-y\x00node-x":     false,
		"node-z\x00node-y":     false,
		"topic-main\x00node-z": false,
	}
	for _, edge := range tree.Edges {
		want[edge.Source+"\x00"+edge.Target] = true
	}
	for key, found := range want {
		if !found {
			t.Fatalf("edges = %+v, want edge %q kept", tree.Edges, key)
		}
	}
	if len(tree.Edges) != 3 {
		t.Fatalf("edges = %+v, want exactly 3 edges", tree.Edges)
	}
	if stats.PrunedTopicEdges != 2 {
		t.Fatalf("PrunedTopicEdges = %d, want 2", stats.PrunedTopicEdges)
	}
}

func TestFinalizeLiveAnalysisTreeNeverPrunesEdgesFromNonPrimaryTopicNode(t *testing.T) {
	// topic-other is a second topic node (e.g. from an earlier topic
	// change) that has its own direct edge to node-x. Pruning must only
	// ever consider edges whose source is the primary topic (topic-main,
	// the first topic node); topic-other -> node-x must survive even though
	// node-x also has other incoming edges.
	nodes := []liveAnalysisTreeNode{
		{ID: "topic-main", Kind: "topic", Label: "トピック"},
		{ID: "topic-other", Kind: "topic", Label: "別のトピック"},
		{ID: "node-y", Kind: "issue", Label: "Y"},
		{ID: "node-x", Kind: "issue", Label: "X"},
	}
	edges := []liveAnalysisTreeEdge{
		{Source: "topic-main", Target: "node-y"},
		{Source: "topic-main", Target: "node-x"},
		{Source: "node-y", Target: "node-x"},
		{Source: "topic-other", Target: "node-x"},
	}
	stats := &liveAnalysisTreeMergeStats{}

	tree := finalizeLiveAnalysisTree(nodes, edges, "", stats)
	if tree == nil {
		t.Fatal("tree = nil, want non-nil")
	}
	foundOtherTopicEdge := false
	for _, edge := range tree.Edges {
		if edge.Source == "topic-main" && edge.Target == "node-x" {
			t.Fatalf("edges = %+v, want topic-main -> node-x pruned (node-x has real parent node-y)", tree.Edges)
		}
		if edge.Source == "topic-other" && edge.Target == "node-x" {
			foundOtherTopicEdge = true
		}
	}
	if !foundOtherTopicEdge {
		t.Fatalf("edges = %+v, want topic-other -> node-x kept: only the primary topic's edges are pruning candidates", tree.Edges)
	}
	if stats.PrunedTopicEdges != 1 {
		t.Fatalf("PrunedTopicEdges = %d, want 1 (only topic-main -> node-x)", stats.PrunedTopicEdges)
	}
}

func TestChooseOrphanParentIDAttachesDetailNodesUnderMajorTopicNotRoot(t *testing.T) {
	// root topic + 大分類(major category)topic 1個 + 詳細(issue)ノード5個をすべて
	// 孤立(edgeなし)の状態で渡す。平坦化バグでは全詳細ノードがrootへ直付けされて
	// いたが、大分類topicが存在するときは詳細ノードはそちら経由でぶら下がり、
	// rootのout-degreeは大分類1個分だけに収まらなければならない。
	diff := &liveAnalysisTree{
		Nodes: []liveAnalysisTreeNode{
			{ID: "topic-root", Kind: "topic", Label: "root"},
			{ID: "topic-major", Kind: "topic", Label: "大分類"},
			{ID: "issue-1", Kind: "issue", Label: "詳細1"},
			{ID: "issue-2", Kind: "issue", Label: "詳細2"},
			{ID: "issue-3", Kind: "issue", Label: "詳細3"},
			{ID: "issue-4", Kind: "issue", Label: "詳細4"},
			{ID: "issue-5", Kind: "issue", Label: "詳細5"},
		},
	}
	stats := &liveAnalysisTreeMergeStats{}

	tree := mergeLiveAnalysisTree(nil, diff, map[string]struct{}{}, "", nil, stats)
	if tree == nil {
		t.Fatal("tree = nil, want non-nil")
	}
	if got := countLiveAnalysisTopicChildren("topic-root", tree.Edges); got != 1 {
		t.Fatalf("root out-degree = %d, want 1 (only topic-major hangs directly off root)", got)
	}
	for _, id := range []string{"issue-1", "issue-2", "issue-3", "issue-4", "issue-5"} {
		found := false
		for _, edge := range tree.Edges {
			if edge.Source == "topic-major" && edge.Target == id {
				found = true
			}
			if edge.Source == "topic-root" && edge.Target == id {
				t.Fatalf("edges = %+v, want %s attached under topic-major, not directly under root", tree.Edges, id)
			}
		}
		if !found {
			t.Fatalf("edges = %+v, want %s attached under topic-major", tree.Edges, id)
		}
	}
}

func TestFinalizeLiveAnalysisTreeDetectsFlatTreeByAbsoluteChildCount(t *testing.T) {
	// 大分類topicが無いまま詳細ノード8個(flatTreeMinTopicChildren)が全てroot
	// 直下に並ぶと、平坦化(flat tree)として検知されなければならない。
	nodes := []liveAnalysisTreeNode{{ID: "topic-root", Kind: "topic", Label: "root"}}
	for i := 0; i < flatTreeMinTopicChildren; i++ {
		nodes = append(nodes, liveAnalysisTreeNode{ID: fmt.Sprintf("issue-%d", i), Kind: "issue", Label: fmt.Sprintf("詳細%d", i)})
	}
	stats := &liveAnalysisTreeMergeStats{}

	tree := finalizeLiveAnalysisTree(nodes, nil, "", stats)
	if tree == nil {
		t.Fatal("tree = nil, want non-nil")
	}
	if !stats.FlatTreeDetected {
		t.Fatalf("FlatTreeDetected = false, want true (topicChildren=%d maxDepth=%d)", stats.TopicChildCount, stats.MaxDepth)
	}
}

func TestFinalizeLiveAnalysisTreeDoesNotDetectFlatTreeWhenDeep(t *testing.T) {
	// root + 大分類1個 + その配下に詳細ノードが連なる深いツリーでは、
	// rootのout-degreeは1のままなので平坦化として検知してはいけない。
	nodes := []liveAnalysisTreeNode{
		{ID: "topic-root", Kind: "topic", Label: "root"},
		{ID: "topic-major", Kind: "topic", Label: "大分類"},
	}
	for i := 0; i < flatTreeMinTopicChildren; i++ {
		nodes = append(nodes, liveAnalysisTreeNode{ID: fmt.Sprintf("issue-%d", i), Kind: "issue", Label: fmt.Sprintf("詳細%d", i)})
	}
	edges := []liveAnalysisTreeEdge{{Source: "topic-root", Target: "topic-major"}}
	for i := 0; i < flatTreeMinTopicChildren; i++ {
		edges = append(edges, liveAnalysisTreeEdge{Source: "topic-major", Target: fmt.Sprintf("issue-%d", i)})
	}
	stats := &liveAnalysisTreeMergeStats{}

	tree := finalizeLiveAnalysisTree(nodes, edges, "", stats)
	if tree == nil {
		t.Fatal("tree = nil, want non-nil")
	}
	if stats.FlatTreeDetected {
		t.Fatalf("FlatTreeDetected = true, want false (topicChildren=%d maxDepth=%d, deep tree via topic-major)", stats.TopicChildCount, stats.MaxDepth)
	}
	if stats.MaxDepth < 2 {
		t.Fatalf("MaxDepth = %d, want >= 2", stats.MaxDepth)
	}
}

func TestFinalizeLiveAnalysisTreeDetectsFlatTreeByChildRatioEvenWithoutDroppedNodes(t *testing.T) {
	// droppedNodes=0 でも、topicChildren/totalNodes が
	// flatTreeChildRatioThreshold(0.7)を超えれば平坦化として検知しなければ
	// ならない。ここでは絶対数(flatTreeMinTopicChildren=8)には満たない5件の
	// 詳細ノードだけを使い、比率側の条件だけがトリガーになることを確認する。
	nodes := []liveAnalysisTreeNode{{ID: "topic-root", Kind: "topic", Label: "root"}}
	for i := 0; i < 5; i++ {
		nodes = append(nodes, liveAnalysisTreeNode{ID: fmt.Sprintf("issue-%d", i), Kind: "issue", Label: fmt.Sprintf("詳細%d", i)})
	}
	stats := &liveAnalysisTreeMergeStats{}

	tree := finalizeLiveAnalysisTree(nodes, nil, "", stats)
	if tree == nil {
		t.Fatal("tree = nil, want non-nil")
	}
	if stats.droppedNodes() != 0 {
		t.Fatalf("droppedNodes() = %d, want 0", stats.droppedNodes())
	}
	if stats.TopicChildCount >= flatTreeMinTopicChildren {
		t.Fatalf("TopicChildCount = %d, want below flatTreeMinTopicChildren so only the ratio condition can trigger", stats.TopicChildCount)
	}
	if !stats.FlatTreeDetected {
		t.Fatalf("FlatTreeDetected = false, want true via child-ratio threshold (topicChildren=%d totalNodes=%d)", stats.TopicChildCount, len(nodes))
	}
}

func TestFinalizeLiveAnalysisTreeReparentsRootFallbackNodeWhenMajorTopicAppearsLater(t *testing.T) {
	// previous ラウンドでは大分類が無く、issue-x が root(primaryTopicID)から
	// 直接ぶら下がる「救済」状態だった。今回のラウンドで大分類topicノードが
	// 追加されたら、issue-x はその大分類配下へ付け替わり、ツリーはroot直下の
	// 平坦な形から深さ2以上へ変わらなければならない。
	previous := `{
		"summary": "前回",
		"currentTopic": "進捗確認",
		"items": [],
		"tree": {
			"nodes": [
				{"id": "topic-root", "kind": "topic", "label": "進捗確認"},
				{"id": "issue-x", "kind": "issue", "label": "課題X"}
			],
			"edges": [
				{"source": "topic-root", "target": "issue-x"}
			]
		}
	}`
	diff := `{
		"summary": "更新",
		"currentTopic": "進捗確認",
		"items": [],
		"tree": {
			"nodes": [
				{"id": "topic-major", "kind": "topic", "label": "大分類"}
			],
			"edges": []
		}
	}`

	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayload(diff, json.RawMessage(previous), stats)
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var payload liveAnalysisPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal merged payload: %v", err)
	}
	if stats.ReparentedNodes < 1 {
		t.Fatalf("ReparentedNodes = %d, want >= 1", stats.ReparentedNodes)
	}
	if stats.MaxDepth < 2 {
		t.Fatalf("MaxDepth = %d, want >= 2 (issue-x moved under topic-major)", stats.MaxDepth)
	}
	foundReparented := false
	for _, edge := range payload.Tree.Edges {
		if edge.Source == "topic-major" && edge.Target == "issue-x" {
			foundReparented = true
		}
		if edge.Source == "topic-root" && edge.Target == "issue-x" {
			t.Fatalf("edges = %+v, want stale topic-root -> issue-x fallback removed by reparenting", payload.Tree.Edges)
		}
	}
	if !foundReparented {
		t.Fatalf("edges = %+v, want issue-x reparented under topic-major", payload.Tree.Edges)
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
