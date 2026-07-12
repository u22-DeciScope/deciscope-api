package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
)

// ---------------------------------------------------------------------------
// 共通ヘルパ
// ---------------------------------------------------------------------------

// mergeForTest merges a model diff into a previous payload without a meeting
// context, mirroring the production call in runLiveAnalysis.
func mergeForTest(t *testing.T, diff string, previous json.RawMessage) liveAnalysisPayload {
	t.Helper()
	return mergeForTestWithContext(t, diff, previous, nil)
}

func mergeForTestWithContext(t *testing.T, diff string, previous json.RawMessage, mc *meetingContext) liveAnalysisPayload {
	t.Helper()
	raw, err := parseAndMergeLiveAnalysisPayload(diff, previous, mc, 1)
	if err != nil {
		t.Fatalf("parseAndMergeLiveAnalysisPayload() error = %v", err)
	}
	var merged liveAnalysisPayload
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("Unmarshal merged payload: %v", err)
	}
	return merged
}

func treeNodeByID(tree *liveAnalysisTree, id string) *liveAnalysisTreeNode {
	if tree == nil {
		return nil
	}
	for i := range tree.Nodes {
		if tree.Nodes[i].ID == id {
			return &tree.Nodes[i]
		}
	}
	return nil
}

// assertTreeInvariants checks every stored-tree invariant the backend must
// guarantee regardless of model output: exactly one root, topics parented on
// root only, detail nodes parented on topics only, one parent per node,
// edges exactly mirroring parents, no self references, no unknown ids, no
// cycles, and no type inversion (a non-topic node can never be a parent).
func assertTreeInvariants(t *testing.T, tree *liveAnalysisTree) {
	t.Helper()
	if tree == nil {
		return
	}
	byID := make(map[string]liveAnalysisTreeNode, len(tree.Nodes))
	rootCount := 0
	for _, node := range tree.Nodes {
		if _, dup := byID[node.ID]; dup {
			t.Fatalf("duplicate node id %q", node.ID)
		}
		byID[node.ID] = node
		if node.ParentID == "" {
			rootCount++
			if node.ID != treeRootNodeID {
				t.Fatalf("node %q has no parent but is not the root", node.ID)
			}
		}
	}
	if root, ok := byID[treeRootNodeID]; !ok || root.Kind != "topic" {
		t.Fatalf("tree must contain the topic root %q, got %+v", treeRootNodeID, byID[treeRootNodeID])
	}
	if rootCount != 1 {
		t.Fatalf("rootCount = %d, want exactly 1", rootCount)
	}
	for _, node := range tree.Nodes {
		if node.ID == treeRootNodeID {
			continue
		}
		if node.ParentID == node.ID {
			t.Fatalf("node %q is self-referencing", node.ID)
		}
		parent, ok := byID[node.ParentID]
		if !ok {
			t.Fatalf("node %q references missing parent %q", node.ID, node.ParentID)
		}
		if parent.Kind != "topic" {
			t.Fatalf("node %q has non-topic parent %q (kind=%s): type inversion", node.ID, parent.ID, parent.Kind)
		}
		if node.Kind == "topic" && parent.ID != treeRootNodeID {
			t.Fatalf("topic %q must be parented on root, got %q", node.ID, parent.ID)
		}
	}
	// Edges are exactly the parent map: one incoming edge per non-root node.
	incoming := make(map[string]int)
	for _, edge := range tree.Edges {
		if edge.Source == edge.Target {
			t.Fatalf("self edge %q", edge.Source)
		}
		if byID[edge.Target].ParentID != edge.Source {
			t.Fatalf("edge %s->%s does not match parentId %q", edge.Source, edge.Target, byID[edge.Target].ParentID)
		}
		incoming[edge.Target]++
	}
	for _, node := range tree.Nodes {
		want := 1
		if node.ID == treeRootNodeID {
			want = 0
		}
		if incoming[node.ID] != want {
			t.Fatalf("node %q has %d incoming edges, want %d", node.ID, incoming[node.ID], want)
		}
	}
	// 循環なし: 親を遡って必ずrootに到達する。
	for _, node := range tree.Nodes {
		seen := map[string]bool{}
		current := node.ID
		for current != treeRootNodeID {
			if seen[current] {
				t.Fatalf("cycle detected from node %q", node.ID)
			}
			seen[current] = true
			current = byID[current].ParentID
		}
	}
}

// ---------------------------------------------------------------------------
// 文字起こし整形
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// items のマージ(既存仕様の維持)
// ---------------------------------------------------------------------------

const mergeTestPreviousPayload = `{
	"summary": "前回の要約",
	"currentTopic": "進捗確認",
	"items": [
		{"id": "issue-a", "kind": "issue", "severity": "medium", "title": "課題A", "body": "説明A", "status": "open"},
		{"id": "risk-b", "kind": "risk", "severity": "high", "title": "リスクB", "body": "説明B", "status": "open"}
	],
	"tree": {
		"nodes": [
			{"id": "root", "kind": "topic", "label": "会議全体"},
			{"id": "topic-progress", "kind": "topic", "parentId": "root", "label": "進捗確認"},
			{"id": "issue-a", "kind": "issue", "parentId": "topic-progress", "label": "課題A"},
			{"id": "risk-b", "kind": "risk", "parentId": "topic-progress", "label": "リスクB"}
		],
		"edges": [
			{"source": "root", "target": "topic-progress"},
			{"source": "topic-progress", "target": "issue-a"},
			{"source": "topic-progress", "target": "risk-b"}
		]
	}
}`

func TestParseAndMergeLiveAnalysisPayloadStripsCodeFence(t *testing.T) {
	content := "```json\n{\"summary\":\"要約\",\"currentTopic\":\"話題\",\"items\":[{\"id\":\"i-1\",\"kind\":\"issue\",\"severity\":\"low\",\"title\":\"論点\",\"body\":\"\",\"status\":\"open\"}]}\n```"
	merged := mergeForTest(t, content, nil)
	if merged.Summary != "要約" || len(merged.Items) != 1 {
		t.Fatalf("merged = %+v, want fence stripped and parsed", merged)
	}
}

func TestParseAndMergeLiveAnalysisPayloadAppendsNewItemsToPreviousState(t *testing.T) {
	diff := `{
		"summary": "更新後の要約",
		"currentTopic": "新しい話題",
		"resolvedIds": [],
		"items": [
			{"id": "question-c", "kind": "question", "severity": "medium", "title": "質問C", "body": "新しい質問", "status": "open"}
		]
	}`
	merged := mergeForTest(t, diff, json.RawMessage(mergeTestPreviousPayload))
	if merged.Summary != "更新後の要約" || merged.CurrentTopic != "新しい話題" {
		t.Fatalf("summary/currentTopic = %q/%q, want replaced by diff", merged.Summary, merged.CurrentTopic)
	}
	if len(merged.Items) != 3 {
		t.Fatalf("items = %+v, want previous 2 + new 1", merged.Items)
	}
	if merged.Items[0].ID != "issue-a" || merged.Items[1].ID != "risk-b" || merged.Items[2].ID != "question-c" {
		t.Fatalf("items order = %+v, want previous order preserved and new appended", merged.Items)
	}
	assertTreeInvariants(t, merged.Tree)
	// 割当が無い新規itemは未分類topicへ接続される。
	node := treeNodeByID(merged.Tree, "question-c")
	if node == nil || node.ParentID != treeUnclassifiedTopicID {
		t.Fatalf("new item node = %+v, want parent %s", node, treeUnclassifiedTopicID)
	}
}

func TestParseAndMergeLiveAnalysisPayloadUpsertsExistingItemByID(t *testing.T) {
	diff := `{
		"summary": "更新後の要約",
		"currentTopic": "進捗確認",
		"items": [
			{"id": "issue-a", "kind": "issue", "severity": "high", "title": "課題A(悪化)", "body": "状況が悪化した", "status": "open"}
		]
	}`
	merged := mergeForTest(t, diff, json.RawMessage(mergeTestPreviousPayload))
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
	// 更新されても親は前回のtopicのまま維持される(旧親が置換される訳ではない)。
	node := treeNodeByID(merged.Tree, "issue-a")
	if node == nil || node.ParentID != "topic-progress" {
		t.Fatalf("updated node = %+v, want parent kept as topic-progress", node)
	}
	assertTreeInvariants(t, merged.Tree)
}

func TestParseAndMergeLiveAnalysisPayloadKeepsStateOnSummaryOnlyDiff(t *testing.T) {
	diff := `{"summary":"要約だけ更新","currentTopic":"","resolvedIds":[],"items":[]}`
	merged := mergeForTest(t, diff, json.RawMessage(mergeTestPreviousPayload))
	if merged.Summary != "要約だけ更新" {
		t.Fatalf("summary = %q", merged.Summary)
	}
	if merged.CurrentTopic != "進捗確認" {
		t.Fatalf("currentTopic = %q, want previous value kept when diff is empty", merged.CurrentTopic)
	}
	if len(merged.Items) != 2 || merged.Items[0].ID != "issue-a" || merged.Items[1].ID != "risk-b" {
		t.Fatalf("items = %+v, want previous items fully preserved", merged.Items)
	}
	if treeNodeByID(merged.Tree, "issue-a") == nil || treeNodeByID(merged.Tree, "risk-b") == nil || treeNodeByID(merged.Tree, "topic-progress") == nil {
		t.Fatalf("tree = %+v, want previous structure preserved", merged.Tree)
	}
	assertTreeInvariants(t, merged.Tree)
}

func TestParseAndMergeLiveAnalysisPayloadMarksResolvedAndCapsIndependently(t *testing.T) {
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
		]
	}`
	merged := mergeForTest(t, diff, json.RawMessage(previous))
	wantLen := liveAnalysisItemsMaxCount + 1
	if len(merged.Items) != wantLen {
		t.Fatalf("items length = %d, want %d (active capped plus 1 retained resolved)", len(merged.Items), wantLen)
	}
	foundResolved := false
	for _, item := range merged.Items {
		if item.ID == "item-5" {
			if item.Status != "resolved" {
				t.Fatalf("item-5 status = %q, want resolved", item.Status)
			}
			foundResolved = true
		}
		if item.ID == "item-0" {
			t.Fatalf("oldest active item must be evicted over the active cap")
		}
	}
	if !foundResolved {
		t.Fatalf("resolved item must be retained")
	}
	assertTreeInvariants(t, merged.Tree)
}

func TestParseAndMergeLiveAnalysisPayloadRemapsDuplicateTitleToExistingID(t *testing.T) {
	// Task C: 既存itemとタイトルが同じ新規idのitemは既存idへ集約され、増殖しない。
	diff := `{
		"summary": "更新",
		"currentTopic": "進捗確認",
		"items": [
			{"id": "risk-duplicate-new", "kind": "risk", "severity": "high", "title": "リスクB", "body": "また同じ懸念", "status": "open"}
		]
	}`
	merged := mergeForTest(t, diff, json.RawMessage(mergeTestPreviousPayload))
	if len(merged.Items) != 2 {
		t.Fatalf("items = %+v, want duplicate remapped onto existing id (no growth)", merged.Items)
	}
	if treeNodeByID(merged.Tree, "risk-duplicate-new") != nil {
		t.Fatalf("tree must not contain a node for the duplicate id")
	}
	riskB := merged.Items[1]
	if riskB.ID != "risk-b" || riskB.Status != "updated" || riskB.Body != "また同じ懸念" {
		t.Fatalf("existing item = %+v, want updated in place via dedup remap", riskB)
	}
}

func TestParseAndMergeLiveAnalysisPayloadRejectsEmptyPayload(t *testing.T) {
	if _, err := parseAndMergeLiveAnalysisPayload(`{"summary":"","currentTopic":"","items":[]}`, nil, nil, 1); err == nil {
		t.Fatalf("expected error for empty payload")
	}
}

func TestParseAndMergeLiveAnalysisPayloadRejectsInvalidJSON(t *testing.T) {
	if _, err := parseAndMergeLiveAnalysisPayload(`not json`, nil, nil, 1); err == nil {
		t.Fatalf("expected error for invalid JSON")
	}
}

func TestParseAndMergeLiveAnalysisPayloadToleratesInvalidPreviousPayload(t *testing.T) {
	diff := `{"summary":"要約","currentTopic":"話題","items":[{"id":"i-1","kind":"issue","severity":"low","title":"論点","body":"","status":"open"}]}`
	merged := mergeForTest(t, diff, json.RawMessage(`{"broken":`))
	if len(merged.Items) != 1 {
		t.Fatalf("items = %+v, want merge to degrade to empty previous state", merged.Items)
	}
	assertTreeInvariants(t, merged.Tree)
}

// ---------------------------------------------------------------------------
// ツリー構造の制約(Phase 2 の中核)
// ---------------------------------------------------------------------------

func TestMergeAssignsParentTopicFromAssignments(t *testing.T) {
	diff := `{
		"summary": "要約",
		"currentTopic": "進捗確認",
		"items": [
			{"id": "question-x", "kind": "question", "severity": "low", "title": "質問X", "body": "", "status": "open"}
		],
		"assignments": [
			{"nodeId": "question-x", "parentTopicId": "topic-progress", "confidence": 0.9, "reason": "進捗の質問"}
		]
	}`
	merged := mergeForTest(t, diff, json.RawMessage(mergeTestPreviousPayload))
	node := treeNodeByID(merged.Tree, "question-x")
	if node == nil || node.ParentID != "topic-progress" {
		t.Fatalf("node = %+v, want assigned parent topic-progress", node)
	}
	assertTreeInvariants(t, merged.Tree)
}

func TestMergeReplacesOldParentOnReassignment(t *testing.T) {
	diff := `{
		"summary": "要約",
		"currentTopic": "進捗確認",
		"items": [],
		"newTopics": [{"id": "topic-quality", "label": "品質"}],
		"assignments": [
			{"nodeId": "issue-a", "parentTopicId": "topic-quality", "confidence": 0.8, "reason": "品質の議論"}
		]
	}`
	merged := mergeForTest(t, diff, json.RawMessage(mergeTestPreviousPayload))
	node := treeNodeByID(merged.Tree, "issue-a")
	if node == nil || node.ParentID != "topic-quality" {
		t.Fatalf("node = %+v, want moved to topic-quality", node)
	}
	// 旧親エッジ(topic-progress -> issue-a)が残っていないこと。
	for _, edge := range merged.Tree.Edges {
		if edge.Source == "topic-progress" && edge.Target == "issue-a" {
			t.Fatalf("old parent edge must be replaced, got %+v", merged.Tree.Edges)
		}
	}
	assertTreeInvariants(t, merged.Tree)
}

func TestMergeSendsUnknownParentToUnclassified(t *testing.T) {
	diff := `{
		"summary": "要約",
		"currentTopic": "",
		"items": [{"id": "todo-y", "kind": "todo", "severity": "low", "title": "作業Y", "body": "", "status": "open"}],
		"assignments": [{"nodeId": "todo-y", "parentTopicId": "topic-not-exists", "confidence": 0.4, "reason": ""}]
	}`
	merged := mergeForTest(t, diff, json.RawMessage(mergeTestPreviousPayload))
	node := treeNodeByID(merged.Tree, "todo-y")
	if node == nil || node.ParentID != treeUnclassifiedTopicID {
		t.Fatalf("node = %+v, want rescued into %s (not the latest topic)", node, treeUnclassifiedTopicID)
	}
	if node.Kind != "issue" {
		t.Fatalf("todo item must synthesize an issue node, got %q", node.Kind)
	}
	assertTreeInvariants(t, merged.Tree)
}

func TestMergeRejectsDetailNodeAsParentByClimbingToTopic(t *testing.T) {
	// 詳細ノード(issue-a)を親に指定しても、そのtopic祖先(topic-progress)へ
	// 正規化される(型逆転の防止)。
	diff := `{
		"summary": "要約",
		"currentTopic": "",
		"items": [{"id": "risk-z", "kind": "risk", "severity": "high", "title": "リスクZ", "body": "", "status": "open"}],
		"assignments": [{"nodeId": "risk-z", "parentTopicId": "issue-a", "confidence": 0.7, "reason": "課題Aに関連"}]
	}`
	merged := mergeForTest(t, diff, json.RawMessage(mergeTestPreviousPayload))
	node := treeNodeByID(merged.Tree, "risk-z")
	if node == nil || node.ParentID != "topic-progress" {
		t.Fatalf("node = %+v, want parent normalized to the topic ancestor topic-progress", node)
	}
	assertTreeInvariants(t, merged.Tree)
}

func TestMergeNormalizesLegacyMultiParentUnionEdges(t *testing.T) {
	// 旧形式(和集合エッジ・複数親・双方向・型逆転)の前回payloadが、単一親の
	// 正規化済みツリーへ変換されること。
	previous := `{
		"summary": "前回",
		"currentTopic": "全体",
		"items": [
			{"id": "issue-1", "kind": "issue", "severity": "low", "title": "論点1", "body": "", "status": "open"},
			{"id": "risk-1", "kind": "risk", "severity": "low", "title": "リスク1", "body": "", "status": "open"}
		],
		"tree": {
			"nodes": [
				{"id": "topic-main", "kind": "topic", "label": "全体"},
				{"id": "topic-sub", "kind": "topic", "label": "サブ"},
				{"id": "issue-1", "kind": "issue", "label": "論点1"},
				{"id": "risk-1", "kind": "risk", "label": "リスク1"}
			],
			"edges": [
				{"source": "topic-main", "target": "issue-1"},
				{"source": "topic-sub", "target": "issue-1"},
				{"source": "issue-1", "target": "risk-1"},
				{"source": "risk-1", "target": "issue-1"},
				{"source": "risk-1", "target": "topic-sub"},
				{"source": "topic-main", "target": "topic-sub"}
			]
		}
	}`
	diff := `{"summary":"更新","currentTopic":"全体","items":[]}`
	merged := mergeForTest(t, diff, json.RawMessage(previous))
	assertTreeInvariants(t, merged.Tree)
	if treeNodeByID(merged.Tree, "issue-1") == nil || treeNodeByID(merged.Tree, "risk-1") == nil {
		t.Fatalf("legacy nodes must survive normalization: %+v", merged.Tree)
	}
}

func TestMergeSurvivesLegacyCyclicEdgesWithoutHanging(t *testing.T) {
	previous := `{
		"summary": "前回",
		"currentTopic": "全体",
		"items": [
			{"id": "a", "kind": "issue", "severity": "low", "title": "A", "body": "", "status": "open"},
			{"id": "b", "kind": "issue", "severity": "low", "title": "B", "body": "", "status": "open"},
			{"id": "c", "kind": "issue", "severity": "low", "title": "C", "body": "", "status": "open"}
		],
		"tree": {
			"nodes": [
				{"id": "a", "kind": "issue", "label": "A"},
				{"id": "b", "kind": "issue", "label": "B"},
				{"id": "c", "kind": "issue", "label": "C"}
			],
			"edges": [
				{"source": "a", "target": "b"},
				{"source": "b", "target": "c"},
				{"source": "c", "target": "a"}
			]
		}
	}`
	diff := `{"summary":"更新","currentTopic":"","items":[]}`
	merged := mergeForTest(t, diff, json.RawMessage(previous))
	assertTreeInvariants(t, merged.Tree)
	// 長い循環しか無い詳細ノードは全て未分類topicへ救済される。
	for _, id := range []string{"a", "b", "c"} {
		node := treeNodeByID(merged.Tree, id)
		if node == nil || node.ParentID != treeUnclassifiedTopicID {
			t.Fatalf("node %s = %+v, want rescued into unclassified", id, node)
		}
	}
}

func TestMergeNeverCreatesSecondRoot(t *testing.T) {
	diff := `{
		"summary": "要約",
		"currentTopic": "",
		"items": [],
		"newTopics": [
			{"id": "root", "label": "偽root"},
			{"id": "topic-a", "label": "分類A"}
		]
	}`
	merged := mergeForTest(t, diff, json.RawMessage(mergeTestPreviousPayload))
	assertTreeInvariants(t, merged.Tree)
	root := treeNodeByID(merged.Tree, treeRootNodeID)
	if root == nil || root.Label == "偽root" {
		t.Fatalf("root = %+v, must not be replaced by model proposal", root)
	}
}

func TestMergeDeduplicatesNewTopicsByLabel(t *testing.T) {
	diff := `{
		"summary": "要約",
		"currentTopic": "",
		"items": [{"id": "q-1", "kind": "question", "severity": "low", "title": "質問1", "body": "", "status": "open"}],
		"newTopics": [{"id": "topic-shinchoku", "label": "進捗確認"}],
		"assignments": [{"nodeId": "q-1", "parentTopicId": "topic-shinchoku", "confidence": 0.9, "reason": ""}]
	}`
	merged := mergeForTest(t, diff, json.RawMessage(mergeTestPreviousPayload))
	if treeNodeByID(merged.Tree, "topic-shinchoku") != nil {
		t.Fatalf("duplicate-label topic must not be created: %+v", merged.Tree.Nodes)
	}
	node := treeNodeByID(merged.Tree, "q-1")
	if node == nil || node.ParentID != "topic-progress" {
		t.Fatalf("node = %+v, want assignment aliased onto existing topic-progress", node)
	}
	assertTreeInvariants(t, merged.Tree)
}

func TestMergeMarksResolvedIdsOnItemsAndNodes(t *testing.T) {
	diff := `{"summary":"更新","currentTopic":"進捗確認","resolvedIds":["risk-b"],"items":[]}`
	merged := mergeForTest(t, diff, json.RawMessage(mergeTestPreviousPayload))
	if merged.Items[1].ID != "risk-b" || merged.Items[1].Status != "resolved" {
		t.Fatalf("item = %+v, want resolved via resolvedIds", merged.Items[1])
	}
	node := treeNodeByID(merged.Tree, "risk-b")
	if node == nil || node.Status != "resolved" {
		t.Fatalf("node = %+v, want resolved status mirrored on tree node", node)
	}
	if merged.ResolvedIds != nil {
		t.Fatalf("resolvedIds must be cleared from the persisted payload")
	}
}

func TestConvertLegacyTreeDiffProducesProposals(t *testing.T) {
	// v2スキーマのまま出力するモデルへの後方互換: tree差分が提案へ変換される。
	diff := `{
		"summary": "要約",
		"currentTopic": "基盤",
		"items": [],
		"tree": {
			"nodes": [
				{"id": "topic-infra", "kind": "topic", "label": "基盤"},
				{"id": "issue-db", "kind": "issue", "label": "DB移行", "description": "移行手順が未定"}
			],
			"edges": [{"source": "topic-infra", "target": "issue-db"}]
		}
	}`
	merged := mergeForTest(t, diff, nil)
	assertTreeInvariants(t, merged.Tree)
	topic := treeNodeByID(merged.Tree, "topic-infra")
	if topic == nil || topic.Kind != "topic" || topic.ParentID != treeRootNodeID {
		t.Fatalf("topic = %+v, want converted to a root-parented topic", topic)
	}
	node := treeNodeByID(merged.Tree, "issue-db")
	if node == nil || node.ParentID != "topic-infra" {
		t.Fatalf("node = %+v, want legacy edge converted to assignment", node)
	}
	found := false
	for _, item := range merged.Items {
		if item.ID == "issue-db" {
			found = true
		}
	}
	if !found {
		t.Fatalf("legacy detail node must be converted into an item: %+v", merged.Items)
	}
}

// ---------------------------------------------------------------------------
// アジェンダ活用(Phase 3)
// ---------------------------------------------------------------------------

func fixtureMeetingContext() *meetingContext {
	return buildMeetingContext(&meetingSessionPreContext{
		Title:   "定例会議",
		Purpose: "文字起こしとAI分析の品質を確認する",
		Context: "DeciScopeはTeams会議をAIで分析するプロダクト",
		Agenda:  "1. 文字起こし精度\n2. AI分析の制御\n3. 今後の課題",
	})
}

func TestBuildMeetingContextParsesAgendaWithStableIDs(t *testing.T) {
	mc := fixtureMeetingContext()
	if mc == nil || len(mc.Agenda) != 3 {
		t.Fatalf("meetingContext = %+v, want 3 agenda items", mc)
	}
	wants := []struct{ id, title string }{
		{"agenda-1", "文字起こし精度"},
		{"agenda-2", "AI分析の制御"},
		{"agenda-3", "今後の課題"},
	}
	for i, want := range wants {
		if mc.Agenda[i].ID != want.id || mc.Agenda[i].Title != want.title {
			t.Fatalf("agenda[%d] = %+v, want %+v", i, mc.Agenda[i], want)
		}
	}
}

func TestParseAgendaItemsHandlesBulletsAndDedup(t *testing.T) {
	items := parseAgendaItems("・文字起こし精度\n- 文字起こし精度\n(2) AI分析の制御\n\n③ 今後の課題")
	if len(items) != 3 {
		t.Fatalf("items = %+v, want bullets stripped and duplicates removed", items)
	}
	if items[0].Title != "文字起こし精度" || items[1].Title != "AI分析の制御" || items[2].Title != "今後の課題" {
		t.Fatalf("items = %+v", items)
	}
}

func TestMergeBuildsInitialAgendaSkeleton(t *testing.T) {
	mc := fixtureMeetingContext()
	diff := `{"summary":"開始","currentTopic":"文字起こし精度","items":[{"id":"q-open","kind":"question","severity":"low","title":"最初の質問","body":"","status":"open"}],"assignments":[{"nodeId":"q-open","parentTopicId":"agenda-1","confidence":0.9,"reason":""}]}`
	merged := mergeForTestWithContext(t, diff, nil, mc)
	assertTreeInvariants(t, merged.Tree)
	root := treeNodeByID(merged.Tree, treeRootNodeID)
	if root == nil || root.Label != "定例会議" || !strings.Contains(root.Description, "品質を確認") {
		t.Fatalf("root = %+v, want label/description from meeting context", root)
	}
	for _, id := range []string{"agenda-1", "agenda-2", "agenda-3"} {
		topic := treeNodeByID(merged.Tree, id)
		if topic == nil || topic.Kind != "topic" || topic.ParentID != treeRootNodeID {
			t.Fatalf("agenda topic %s = %+v, want present under root", id, topic)
		}
	}
	node := treeNodeByID(merged.Tree, "q-open")
	if node == nil || node.ParentID != "agenda-1" {
		t.Fatalf("node = %+v, want classified into agenda-1", node)
	}
	// 未分類topicは子が無い限り生成されない。
	if treeNodeByID(merged.Tree, treeUnclassifiedTopicID) != nil {
		t.Fatalf("unclassified topic must not appear without children")
	}
}

func TestMergeClassifiesFixtureUtterancesAcrossAgendaTopics(t *testing.T) {
	// ユーザー指定のアジェンダ活用fixture: 4つの発言由来itemが3つのアジェンダ
	// topicと追加論点へ分散し、単一topicへ集中しないこと。
	mc := fixtureMeetingContext()
	diff := `{
		"summary": "文字起こしとAI分析について議論",
		"currentTopic": "文字起こし精度",
		"items": [
			{"id": "risk-speaker-id", "kind": "risk", "severity": "high", "title": "話者識別が不安定", "body": "複数人で話すと話者識別が不安定になる", "status": "open"},
			{"id": "risk-dup-cards", "kind": "risk", "severity": "medium", "title": "同じリスクを何度も作る", "body": "AIが同じリスクを繰り返し生成する", "status": "open"},
			{"id": "todo-model-compare", "kind": "todo", "severity": "medium", "title": "モデル別出力を比較", "body": "次回はモデルごとの出力を比較したい", "status": "open"},
			{"id": "issue-report-format", "kind": "issue", "severity": "low", "title": "レポート形式の見直し", "body": "会議終了後のレポート形式も見直したい", "status": "open"}
		],
		"assignments": [
			{"nodeId": "risk-speaker-id", "parentTopicId": "agenda-1", "confidence": 0.92, "reason": "話者識別は文字起こし精度の議題"},
			{"nodeId": "risk-dup-cards", "parentTopicId": "agenda-2", "confidence": 0.88, "reason": "AI分析の制御に関する問題"},
			{"nodeId": "todo-model-compare", "parentTopicId": "agenda-3", "confidence": 0.85, "reason": "今後の課題"},
			{"nodeId": "issue-report-format", "parentTopicId": "topic-unclassified", "confidence": 0.6, "reason": "アジェンダ外の議論"}
		]
	}`
	merged := mergeForTestWithContext(t, diff, nil, mc)
	assertTreeInvariants(t, merged.Tree)
	wants := map[string]string{
		"risk-speaker-id":     "agenda-1",
		"risk-dup-cards":      "agenda-2",
		"todo-model-compare":  "agenda-3",
		"issue-report-format": treeUnclassifiedTopicID,
	}
	parentCounts := map[string]int{}
	for id, wantParent := range wants {
		node := treeNodeByID(merged.Tree, id)
		if node == nil || node.ParentID != wantParent {
			t.Fatalf("node %s = %+v, want parent %s", id, node, wantParent)
		}
		parentCounts[node.ParentID]++
	}
	for parent, count := range parentCounts {
		if count > 1 {
			t.Fatalf("parent %s holds %d of 4 nodes, want spread across topics", parent, count)
		}
	}
	health := computeTreeHealth(merged.Tree)
	if health.MaxConcentration > 0.5 {
		t.Fatalf("health = %+v, want no topic holding most detail nodes", health)
	}
}

// ---------------------------------------------------------------------------
// ツリー再編成(Task E/F)
// ---------------------------------------------------------------------------

func overcrowdedTreePayload(topicID string, childCount int) *liveAnalysisTree {
	tree := &liveAnalysisTree{
		Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議"},
			{ID: topicID, Kind: "topic", ParentID: treeRootNodeID, Label: "過密topic"},
		},
	}
	for i := 0; i < childCount; i++ {
		id := fmt.Sprintf("issue-%d", i)
		tree.Nodes = append(tree.Nodes, liveAnalysisTreeNode{ID: id, Kind: "issue", ParentID: topicID, Label: fmt.Sprintf("論点%d", i)})
	}
	for _, node := range tree.Nodes {
		if node.ParentID != "" {
			tree.Edges = append(tree.Edges, liveAnalysisTreeEdge{Source: node.ParentID, Target: node.ID})
		}
	}
	return tree
}

func TestComputeTreeHealthDetectsOvercrowdedTopicAnywhere(t *testing.T) {
	tree := overcrowdedTreePayload("topic-busy", treeReorganizeMaxTopicChildren)
	health := computeTreeHealth(tree)
	if !health.needsReorganization() {
		t.Fatalf("health = %+v, want reorganization trigger", health)
	}
	if health.MaxTopicID != "topic-busy" {
		t.Fatalf("MaxTopicID = %q, want topic-busy", health.MaxTopicID)
	}

	small := overcrowdedTreePayload("topic-ok", 3)
	if computeTreeHealth(small).needsReorganization() {
		t.Fatalf("small tree must not trigger reorganization")
	}
}

func TestComputeTreeHealthIgnoresResolvedNodes(t *testing.T) {
	tree := overcrowdedTreePayload("topic-busy", treeReorganizeMaxTopicChildren)
	for i := range tree.Nodes {
		if tree.Nodes[i].Kind != "topic" {
			tree.Nodes[i].Status = "resolved"
		}
	}
	if computeTreeHealth(tree).needsReorganization() {
		t.Fatalf("resolved nodes must not count toward crowding")
	}
}

func TestApplyTreeOperationsSplitsOvercrowdedTopicLocally(t *testing.T) {
	tree := overcrowdedTreePayload("topic-busy", 8)
	ops := []treeOperation{
		{Type: "create_topic", TopicID: "topic-speech-quality", Label: "音声認識品質"},
		{Type: "move_node", NodeID: "issue-0", ToParentID: "topic-speech-quality"},
		{Type: "move_node", NodeID: "issue-1", ToParentID: "topic-speech-quality"},
		{Type: "rename_topic", TopicID: "topic-busy", Label: "分析ロジック"},
	}
	rebuilt, applied := applyTreeOperations(tree, nil, ops, nil)
	if applied != 4 {
		t.Fatalf("applied = %d, want 4", applied)
	}
	assertTreeInvariants(t, rebuilt)
	moved := treeNodeByID(rebuilt, "issue-0")
	if moved == nil || moved.ParentID != "topic-speech-quality" {
		t.Fatalf("moved node = %+v", moved)
	}
	renamed := treeNodeByID(rebuilt, "topic-busy")
	if renamed == nil || renamed.Label != "分析ロジック" {
		t.Fatalf("renamed topic = %+v", renamed)
	}
	// 残ったノードの親は維持される。
	stay := treeNodeByID(rebuilt, "issue-5")
	if stay == nil || stay.ParentID != "topic-busy" {
		t.Fatalf("unmoved node = %+v", stay)
	}
}

func TestApplyTreeOperationsSkipsInvalidOperations(t *testing.T) {
	tree := overcrowdedTreePayload("topic-busy", 3)
	mc := fixtureMeetingContext()
	ops := []treeOperation{
		{Type: "move_node", NodeID: "issue-0", ToParentID: "issue-1"},             // 詳細ノードを親にできない
		{Type: "move_node", NodeID: "missing", ToParentID: "topic-busy"},          // 存在しないノード
		{Type: "merge_topic", FromTopicID: "agenda-1", IntoTopicID: "topic-busy"}, // アジェンダは統合不可
		{Type: "unknown_op"},
	}
	rebuilt, applied := applyTreeOperations(tree, mc, ops, nil)
	if applied != 0 {
		t.Fatalf("applied = %d, want all invalid operations skipped", applied)
	}
	node := treeNodeByID(rebuilt, "issue-0")
	if node == nil || node.ParentID != "topic-busy" {
		t.Fatalf("node = %+v, want unchanged", node)
	}
}

func TestApplyTreeOperationsMergeTopicMovesChildren(t *testing.T) {
	tree := overcrowdedTreePayload("topic-busy", 2)
	tree.Nodes = append(tree.Nodes, liveAnalysisTreeNode{ID: "topic-dup", Kind: "topic", ParentID: treeRootNodeID, Label: "重複topic"})
	tree.Nodes = append(tree.Nodes, liveAnalysisTreeNode{ID: "issue-x", Kind: "issue", ParentID: "topic-dup", Label: "論点X"})
	tree.Edges = append(tree.Edges,
		liveAnalysisTreeEdge{Source: treeRootNodeID, Target: "topic-dup"},
		liveAnalysisTreeEdge{Source: "topic-dup", Target: "issue-x"})
	rebuilt, applied := applyTreeOperations(tree, nil, []treeOperation{
		{Type: "merge_topic", FromTopicID: "topic-dup", IntoTopicID: "topic-busy"},
	}, nil)
	if applied != 1 {
		t.Fatalf("applied = %d, want 1", applied)
	}
	assertTreeInvariants(t, rebuilt)
	if treeNodeByID(rebuilt, "topic-dup") != nil {
		t.Fatalf("merged topic must be removed")
	}
	moved := treeNodeByID(rebuilt, "issue-x")
	if moved == nil || moved.ParentID != "topic-busy" {
		t.Fatalf("moved child = %+v", moved)
	}
}

// scriptedCompleter returns queued results in order.
type scriptedCompleter struct {
	results  []AIChatResult
	err      error
	requests []AIChatRequest
}

func (c *scriptedCompleter) Complete(_ context.Context, request AIChatRequest) (AIChatResult, error) {
	c.requests = append(c.requests, request)
	if c.err != nil {
		return AIChatResult{}, c.err
	}
	if len(c.results) == 0 {
		return AIChatResult{}, fmt.Errorf("no scripted result")
	}
	result := c.results[0]
	c.results = c.results[1:]
	return result, nil
}

func newInternalTestService(completer AIChatCompleter, config MeetingAnalysisConfig) *MeetingAnalysisService {
	return NewMeetingAnalysisService(nil, nil, nil, completer, config)
}

func TestReorganizeTreeDiscardsStaleTreeVersion(t *testing.T) {
	completer := &scriptedCompleter{results: []AIChatResult{{
		Content: `{"basedOnTreeVersion": 3, "operations": [{"type":"create_topic","topicId":"topic-x","label":"分割"}]}`,
	}}}
	service := newInternalTestService(completer, MeetingAnalysisConfig{Enabled: true, LiveEnabled: true, Model: "gpt-test"})
	tree := overcrowdedTreePayload("topic-busy", 8)

	result, applied, err := service.reorganizeTree(context.Background(), "session-1", tree, nil, 12)
	if err != nil {
		t.Fatalf("reorganizeTree() error = %v", err)
	}
	if applied != 0 {
		t.Fatalf("applied = %d, want stale response discarded", applied)
	}
	if treeNodeByID(result, "topic-x") != nil {
		t.Fatalf("stale operations must not be applied")
	}
}

func TestReorganizeTreeAppliesMatchingTreeVersion(t *testing.T) {
	completer := &scriptedCompleter{results: []AIChatResult{{
		Content: `{"basedOnTreeVersion": 12, "operations": [
			{"type":"create_topic","topicId":"topic-x","label":"分割"},
			{"type":"move_node","nodeId":"issue-0","toParentId":"topic-x"}
		]}`,
	}}}
	service := newInternalTestService(completer, MeetingAnalysisConfig{Enabled: true, LiveEnabled: true, Model: "gpt-test"})
	tree := overcrowdedTreePayload("topic-busy", 8)

	result, applied, err := service.reorganizeTree(context.Background(), "session-1", tree, nil, 12)
	if err != nil {
		t.Fatalf("reorganizeTree() error = %v", err)
	}
	if applied != 2 {
		t.Fatalf("applied = %d, want 2", applied)
	}
	assertTreeInvariants(t, result)
	moved := treeNodeByID(result, "issue-0")
	if moved == nil || moved.ParentID != "topic-x" {
		t.Fatalf("moved = %+v", moved)
	}
}

// ---------------------------------------------------------------------------
// タスク別モデル設定
// ---------------------------------------------------------------------------

func TestTaskModelsFallBackToSharedModel(t *testing.T) {
	config := MeetingAnalysisConfig{
		Model: "gpt-shared",
		TaskModels: AITaskModels{
			TreeReorganizer: "gpt-strong",
		},
	}
	if got := config.modelNameFor(aiTaskLiveExtraction); got != "gpt-shared" {
		t.Fatalf("live model = %q, want fallback to shared", got)
	}
	if got := config.modelNameFor(aiTaskTreeReorganizer); got != "gpt-strong" {
		t.Fatalf("reorganizer model = %q, want dedicated deployment", got)
	}
	if got := config.TaskModels.deploymentFor(aiTaskFinalSummary); got != "" {
		t.Fatalf("final deployment = %q, want empty (use default)", got)
	}
}

func TestCompleteTaskRoutesDeploymentPerTask(t *testing.T) {
	completer := &scriptedCompleter{results: []AIChatResult{{Content: "{}"}, {Content: "{}"}}}
	service := newInternalTestService(completer, MeetingAnalysisConfig{
		Enabled: true,
		Model:   "gpt-shared",
		TaskModels: AITaskModels{
			TreeReorganizer: "gpt-strong",
		},
	})
	if _, model, err := service.completeTask(context.Background(), aiTaskLiveExtraction, AIChatRequest{}, 1); err != nil || model != "gpt-shared" {
		t.Fatalf("live task model = %q err = %v, want gpt-shared", model, err)
	}
	if _, model, err := service.completeTask(context.Background(), aiTaskTreeReorganizer, AIChatRequest{}, 1); err != nil || model != "gpt-strong" {
		t.Fatalf("reorganizer task model = %q err = %v, want gpt-strong", model, err)
	}
	if completer.requests[0].Deployment != "" {
		t.Fatalf("live request deployment = %q, want default (empty)", completer.requests[0].Deployment)
	}
	if completer.requests[1].Deployment != "gpt-strong" {
		t.Fatalf("reorganizer request deployment = %q, want gpt-strong", completer.requests[1].Deployment)
	}
}

// ---------------------------------------------------------------------------
// Meeting Context とプロンプト
// ---------------------------------------------------------------------------

func TestBuildLiveAnalysisUserPromptSeparatesRoles(t *testing.T) {
	mc := buildMeetingContext(&meetingSessionPreContext{
		Title:             "定例",
		Purpose:           "リリース可否を決める",
		Context:           "既にDBは移行済み",
		Agenda:            "1. 品質\n2. スケジュール",
		CustomInstruction: "技術的リスクを優先して抽出する",
	})
	prompt := buildLiveAnalysisUserPrompt(json.RawMessage(mergeTestPreviousPayload), mc, "田中: テストの発言", 5)

	for _, want := range []string{
		"目的・ゴール(何を重要な論点として採用するかの判断基準",
		"前提・背景(発言を解釈するための既知情報",
		"agenda-1: 品質(会議前アジェンダ)",
		"agenda-2: スケジュール(会議前アジェンダ)",
		"topic-unclassified",
		"tree version 5",
		"id=issue-a",
		"[新しい発言(差分)]",
		"[更新ルール]",
		"補足指示",
		"技術的リスクを優先して抽出する",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q\n---\n%s", want, prompt)
		}
	}
	// 補足指示は更新ルールより後に置かれ、制約を上書きできない位置にある。
	if strings.Index(prompt, "[更新ルール]") > strings.Index(prompt, "技術的リスクを優先して抽出する") {
		t.Fatalf("directives must appear after the update rules")
	}
	// 前回payload全体のJSONは埋め込まない(コンパクトな状態のみ)。
	if strings.Contains(prompt, `"parentId"`) {
		t.Fatalf("prompt must not embed the raw previous payload JSON")
	}
}

func TestBuildLiveAnalysisUserPromptWithoutContext(t *testing.T) {
	prompt := buildLiveAnalysisUserPrompt(nil, nil, "", 0)
	if !strings.Contains(prompt, "(新しい発言はありません)") {
		t.Fatalf("prompt = %q, want placeholder for empty diff", prompt)
	}
	if !strings.Contains(prompt, "newTopicsで大分類を作成") {
		t.Fatalf("prompt must instruct topic creation when no topics exist")
	}
}

func TestRenderMeetingContextSectionsSkipsEmptyFieldsAndDirectives(t *testing.T) {
	mc := buildMeetingContext(&meetingSessionPreContext{
		Purpose:           "目的X",
		CustomInstruction: "指示Y",
	})
	section := renderMeetingContextSections(mc)
	if !strings.Contains(section, "目的X") {
		t.Fatalf("section = %q", section)
	}
	if strings.Contains(section, "指示Y") {
		t.Fatalf("directives must not be rendered inside the context section")
	}
	if strings.Contains(section, "前提・背景") {
		t.Fatalf("empty fields must be omitted: %q", section)
	}
}

func TestMeetingContextRoundTripsThroughJSON(t *testing.T) {
	mc := fixtureMeetingContext()
	payload, err := marshalMeetingContext(mc)
	if err != nil {
		t.Fatalf("marshalMeetingContext() error = %v", err)
	}
	restored := unmarshalMeetingContext(payload)
	if restored == nil || len(restored.Agenda) != 3 || restored.Agenda[0].ID != "agenda-1" {
		t.Fatalf("restored = %+v, want stable agenda ids after round trip", restored)
	}
	if unmarshalMeetingContext(json.RawMessage(`{"bad`)) != nil {
		t.Fatalf("invalid payload must degrade to nil")
	}
}

func TestParseContextPlannerResultReassignsStableIDs(t *testing.T) {
	fallback := fixtureMeetingContext()
	content := `{"title":"定例会議","purpose":"品質確認","background":"","agendaItems":[{"title":"文字起こし精度","order":1},{"title":"AI分析の制御","order":2}],"aiDirectives":["リスク優先"]}`
	mc, err := parseContextPlannerResult(content, fallback)
	if err != nil {
		t.Fatalf("parseContextPlannerResult() error = %v", err)
	}
	if len(mc.Agenda) != 2 || mc.Agenda[0].ID != "agenda-1" || mc.Agenda[1].ID != "agenda-2" {
		t.Fatalf("agenda = %+v, want server-assigned stable ids", mc.Agenda)
	}
	if mc.Background != fallback.Background {
		t.Fatalf("background = %q, want fallback fill for empty planner field", mc.Background)
	}
	if _, err := parseContextPlannerResult(`{}`, nil); err == nil {
		t.Fatalf("empty planner output without fallback must fail")
	}
}

// ---------------------------------------------------------------------------
// 最終要約・その他
// ---------------------------------------------------------------------------

func TestParseAndValidateFinalAnalysisPayloadParsesSchema(t *testing.T) {
	content := `{"suggestedTitle":"週次定例","overview":"概要","decisions":[{"text":"リリースする","importance":"high"}],"actionItems":[],"openIssues":[],"keyPoints":[],"nextMeetingTopics":[]}`
	payload, err := parseAndValidateFinalAnalysisPayload(content)
	if err != nil {
		t.Fatalf("parseAndValidateFinalAnalysisPayload() error = %v", err)
	}
	var parsed finalAnalysisPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.SuggestedTitle != "週次定例" || len(parsed.Decisions) != 1 {
		t.Fatalf("parsed = %+v", parsed)
	}
}

func TestParseAndValidateFinalAnalysisPayloadRejectsEmptyPayload(t *testing.T) {
	if _, err := parseAndValidateFinalAnalysisPayload(`{}`); err == nil {
		t.Fatalf("expected error for empty final payload")
	}
}

func TestBuildFinalAnalysisUserPromptNotesTruncation(t *testing.T) {
	prompt := buildFinalAnalysisUserPrompt(nil, fixtureMeetingContext(), "田中: 発言", true)
	if !strings.Contains(prompt, "冒頭の発言は省略") {
		t.Fatalf("prompt = %q, want truncation note", prompt)
	}
	if !strings.Contains(prompt, "agenda-1: 文字起こし精度") {
		t.Fatalf("prompt must include the agenda topics")
	}
	if !strings.Contains(prompt, "目的・ゴールに対してどこまで到達したか") {
		t.Fatalf("prompt must ask for goal-attainment coverage")
	}
}

func TestLiveAnalysisBackoffCapsAtMaxBackoff(t *testing.T) {
	interval := 10 * time.Second
	if got := liveAnalysisBackoff(interval, 1); got != 20*time.Second {
		t.Fatalf("backoff(1) = %s", got)
	}
	if got := liveAnalysisBackoff(interval, 100); got != meetingAnalysisMaxBackoff {
		t.Fatalf("backoff(100) = %s, want capped at %s", got, meetingAnalysisMaxBackoff)
	}
}
