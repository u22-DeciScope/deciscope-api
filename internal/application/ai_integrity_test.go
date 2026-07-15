package application

import (
	"context"
	"encoding/json"
	"testing"

	"deciscope-core-api/internal/domain"
)

type evidenceTranscriptRepository struct {
	segments []domain.TranscriptSegment
}

func (r *evidenceTranscriptRepository) SaveTranscriptSegment(context.Context, domain.TranscriptSegment) (domain.TranscriptSegmentStoreResult, error) {
	return domain.TranscriptSegmentStoreResult{}, nil
}

func (r *evidenceTranscriptRepository) ListTranscriptSegments(context.Context, string, string, int) ([]domain.TranscriptSegment, error) {
	return r.segments, nil
}

func TestLiveMergeKeepsCanonicalIDAcrossDecisionPromotionAndReferences(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-3", Title: "住民説明資料の作成", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-4", Title: "今後の対応事項", Order: 2, Role: agendaRoleActionSummary},
	}}
	first := `{"summary":"","currentTopic":"資料","resolvedIds":[],"items":[{"id":"todo- Residents-doc-publicity-01","kind":"todo","severity":"high","title":"住民説明資料のWeb公開","body":"公開方法を検討する","status":"open","evidenceSequenceNos":[1]}],"newTopics":[],"assignments":[{"nodeId":"todo-Residents-doc-publicity-01","parentTopicId":"agenda-3","confidence":0.9,"reason":""}]}`
	firstStats := &liveAnalysisTreeMergeStats{}
	previous, err := parseAndMergeLiveAnalysisPayload(first, nil, mc, 1, []int64{1}, TreeClassificationConfig{}, firstStats)
	if err != nil {
		t.Fatal(err)
	}
	firstState := previousLiveAnalysisState(previous)
	if len(firstState.Items) != 1 {
		t.Fatalf("first items=%+v", firstState.Items)
	}
	canonicalID := firstState.Items[0].ID
	if canonicalID != "todo-residents-doc-publicity-01" || firstStats.UnknownAssignmentIDs != 0 || firstStats.AliasResolvedAssignmentIDs == 0 {
		t.Fatalf("id=%q stats=%+v", canonicalID, firstStats)
	}

	segments := []domain.TranscriptSegment{{SequenceNo: 2, Text: "調査結果は図と説明付きでWeb公開することにします。", IsFinal: true}}
	model := `{"summary":"","currentTopic":"資料","resolvedIds":["TODO- Residents-doc-publicity-01"],"items":[{"id":"todo-Residents-doc-publicity-01","kind":"todo","severity":"high","title":"図と説明付きでWeb公開","body":"調査結果をWeb公開する","status":"resolved","evidenceSequenceNos":[2]}],"newTopics":[],"assignments":[{"nodeId":"todo- Residents-doc-publicity-01","parentTopicId":"agenda-4","confidence":0.9,"reason":""}]}`
	reconciled, _, err := reconcileDecisionCandidates(model, previous, detectDecisionCandidates(segments))
	if err != nil {
		t.Fatal(err)
	}
	secondStats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayload(reconciled, previous, mc, 2, []int64{2}, TreeClassificationConfig{}, secondStats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	if len(state.Items) != 1 || state.Items[0].ID != canonicalID || state.Items[0].Kind != "decision" || state.Items[0].Status == "resolved" {
		t.Fatalf("items=%+v", state.Items)
	}
	if parentOf(state.Tree, canonicalID) != "agenda-3" {
		t.Fatalf("parent=%s tree=%+v", parentOf(state.Tree, canonicalID), state.Tree)
	}
	if secondStats.UnknownAssignmentIDs != 0 || secondStats.UnknownResolvedIDs != 0 {
		t.Fatalf("reference stats=%+v", secondStats)
	}
	if state.TreeChanges == nil || !containsString(state.TreeChanges.PromotedNodeIDs, canonicalID) || containsString(state.TreeChanges.NewNodeIDs, canonicalID) {
		t.Fatalf("tree changes=%+v", state.TreeChanges)
	}
}

func TestResolutionPolicyByKind(t *testing.T) {
	for _, kind := range []string{"question", "open_issue", "issue", "risk", "todo"} {
		if !resolvableItemKind(kind) {
			t.Fatalf("%s must be resolvable", kind)
		}
	}
	for _, kind := range []string{"decision", "fact", "topic", "group"} {
		if resolvableItemKind(kind) {
			t.Fatalf("%s must not be resolvable", kind)
		}
	}
}

func TestMergeRepairsLegacyResolvedDecisionAndFact(t *testing.T) {
	previous := liveAnalysisPayload{Items: []liveAnalysisItem{
		{ID: "decision-1", Kind: "decision", Severity: "high", Title: "決定", Status: "resolved"},
		{ID: "fact-1", Kind: "fact", Severity: "medium", Title: "事実", Status: "resolved"},
	}, Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "会議"},
		{ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: "追加論点"},
		{ID: "decision-1", Kind: "decision", ParentID: treeUnclassifiedTopicID, Label: "決定", Status: "resolved"},
		{ID: "fact-1", Kind: "fact", ParentID: treeUnclassifiedTopicID, Label: "事実", Status: "resolved"},
	}}}
	previousRaw, _ := json.Marshal(previous)
	content := `{"summary":"更新","currentTopic":"","resolvedIds":[],"items":[],"newTopics":[],"assignments":[]}`
	raw, err := parseAndMergeLiveAnalysisPayload(content, previousRaw, nil, 2, nil, TreeClassificationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	for _, id := range []string{"decision-1", "fact-1"} {
		item := findItemByID(state.Items, id)
		if item == nil || item.Status == "resolved" {
			t.Fatalf("item %s=%+v", id, item)
		}
		for _, node := range state.Tree.Nodes {
			if node.ID == id && node.Status == "resolved" {
				t.Fatalf("node %s remained resolved: %+v", id, node)
			}
		}
	}
}

func TestHistoricalEvidenceScopeAndExistingEvidencePreservation(t *testing.T) {
	previous := liveAnalysisPayload{Items: []liveAnalysisItem{{ID: "risk-1", Kind: "risk", Severity: "high", Title: "既存", Status: "open", EvidenceSequenceNos: []int64{1}}}}
	previousRaw, _ := json.Marshal(previous)
	content := `{"summary":"","currentTopic":"","resolvedIds":[],"items":[{"id":"risk-1","kind":"risk","severity":"high","title":"既存","body":"更新","status":"updated","evidenceSequenceNos":[1,2,3,4]}],"newTopics":[],"assignments":[]}`
	scope := liveEvidenceScope{Allowed: map[int64]struct{}{1: {}, 3: {}}, CurrentRound: map[int64]struct{}{3: {}}, CoveredThrough: 3}
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(content, previousRaw, nil, 2, []int64{3}, scope, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	if len(state.Items) != 1 || !equalInt64s(state.Items[0].EvidenceSequenceNos, []int64{1, 3}) {
		t.Fatalf("evidence=%v", state.Items[0].EvidenceSequenceNos)
	}
	if stats.HistoricalEvidenceAccepted != 1 || stats.CurrentRoundEvidenceAccepted != 1 || stats.MissingEvidenceRejected != 1 || stats.FutureEvidenceRejected != 1 || stats.ExistingEvidencePreserved == 0 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestLiveEvidenceScopeUsesOnlyFinalRowsFromTheSession(t *testing.T) {
	repository := &evidenceTranscriptRepository{segments: []domain.TranscriptSegment{
		{SessionID: "session-1", SequenceNo: 1, Text: "past", IsFinal: true},
		{SessionID: "session-1", SequenceNo: 2, Text: "partial", IsFinal: false},
		{SessionID: "other", SequenceNo: 3, Text: "other", IsFinal: true},
		{SessionID: "session-1", SequenceNo: 9, Text: "future", IsFinal: true},
	}}
	service := &MeetingAnalysisService{transcriptRepo: repository}
	previous, _ := json.Marshal(liveAnalysisPayload{CoveredThroughSequenceNo: 4})
	scope := service.liveEvidenceScope(context.Background(), "session-1", previous, []domain.TranscriptSegment{{SessionID: "session-1", SequenceNo: 4, Text: "now", IsFinal: true}})
	if _, ok := scope.Allowed[1]; !ok {
		t.Fatal("historical final sequence was rejected")
	}
	for _, rejected := range []int64{2, 3, 9} {
		if _, ok := scope.Allowed[rejected]; ok {
			t.Fatalf("sequence %d must be rejected: %+v", rejected, scope)
		}
	}
}

func TestHistoricalEvidenceSupportsEmergingTopicPromotionAcrossRounds(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "既存議題", Order: 1, Role: agendaRolePrimary}}}
	first := `{"summary":"","currentTopic":"湿地","resolvedIds":[],"items":[{"id":"todo-wetland-1","kind":"todo","severity":"medium","title":"湿地の植物を確認","body":"専門家に確認する","status":"open","evidenceSequenceNos":[1]}],"newTopics":[{"id":"topic-wetland","label":"湿地・希少植物","description":""}],"assignments":[{"nodeId":"todo-wetland-1","parentTopicId":"topic-wetland","confidence":0.8,"reason":""}]}`
	firstRaw, err := parseAndMergeLiveAnalysisPayload(first, nil, mc, 1, []int64{1}, TreeClassificationConfig{PromotionMinItems: 2, PromotionMinRounds: 2})
	if err != nil {
		t.Fatal(err)
	}
	firstState := previousLiveAnalysisState(firstRaw)
	if len(firstState.EmergingTopics) != 1 || len(firstState.EmergingTopics[0].EvidenceItemIDs) != 1 {
		t.Fatalf("first emerging=%+v", firstState.EmergingTopics)
	}
	second := `{"summary":"","currentTopic":"湿地","resolvedIds":[],"items":[{"id":"todo-wetland-2","kind":"todo","severity":"medium","title":"湿地の予備調査","body":"希少植物の予備調査を検討する","status":"open","evidenceSequenceNos":[1,2]}],"newTopics":[{"id":"topic-wetland","label":"湿地・希少植物","description":""}],"assignments":[{"nodeId":"todo-wetland-2","parentTopicId":"topic-wetland","confidence":0.8,"reason":""}]}`
	stats := &liveAnalysisTreeMergeStats{}
	scope := liveEvidenceScope{Allowed: map[int64]struct{}{1: {}, 2: {}}, CurrentRound: map[int64]struct{}{2: {}}, CoveredThrough: 2}
	secondRaw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(second, firstRaw, mc, 2, []int64{2}, scope, TreeClassificationConfig{PromotionMinItems: 2, PromotionMinRounds: 2}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(secondRaw)
	dynamicID, _ := canonicalCandidateID("湿地・希少植物", "")
	if len(state.EmergingTopics) != 0 || itemTopicID(state.Tree, "todo-wetland-1") != dynamicID || itemTopicID(state.Tree, "todo-wetland-2") != dynamicID || stats.HistoricalEvidenceAccepted != 1 {
		t.Fatalf("state=%+v stats=%+v", state, stats)
	}
}

func TestIssueCandidateReconciliationSeparatesKinds(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		modelItem string
		question  int
		openIssue int
		todo      int
	}{
		{name: "question", text: "強風日の基準風速は何m/sにするべきですか。", question: 1},
		{name: "open", text: "強風日の基準風速はまだ決まっていません。", openIssue: 1},
		{name: "todo", text: "次回までに過去5年間の気象データを確認します。", modelItem: `{"id":"todo-weather","kind":"todo","severity":"medium","title":"気象データを確認","body":"次回までに確認する","status":"open","evidenceSequenceNos":[1]}`, todo: 1},
		{name: "mixed", text: "基準風速はまだ決まっていません。何m/sにするか判断するため、次回までに気象データを確認します。", modelItem: `{"id":"todo-weather","kind":"todo","severity":"medium","title":"気象データを確認","body":"次回までに確認する","status":"open","evidenceSequenceNos":[1]}`, question: 1, openIssue: 1, todo: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := "[]"
			if tt.modelItem != "" {
				items = "[" + tt.modelItem + "]"
			}
			model := `{"summary":"","currentTopic":"","resolvedIds":[],"items":` + items + `,"newTopics":[],"assignments":[]}`
			segments := []domain.TranscriptSegment{{SequenceNo: 1, Text: tt.text, IsFinal: true}}
			reconciled, _, err := reconcileIssueCandidates(model, nil, detectIssueCandidates(segments))
			if err != nil {
				t.Fatal(err)
			}
			raw, err := parseAndMergeLiveAnalysisPayload(reconciled, nil, nil, 1, []int64{1}, TreeClassificationConfig{})
			if err != nil {
				t.Fatal(err)
			}
			counts := livePayloadItemKindCounts(raw)
			if counts["question"] != tt.question || counts["open_issue"] != tt.openIssue || counts["todo"] != tt.todo || counts["decision"] != 0 {
				t.Fatalf("counts=%v", counts)
			}
		})
	}
}

func TestDecisionResolvesExistingQuestionIssueAndTodoWithoutChangingKinds(t *testing.T) {
	previous := liveAnalysisPayload{Items: []liveAnalysisItem{
		{ID: "question-wind", Kind: "question", Severity: "medium", Title: "基準風速は何m/sか", Body: "強風日の基準風速を確認する", Status: "open"},
		{ID: "open-wind", Kind: "open_issue", Severity: "high", Title: "基準風速が未確定", Body: "強風日の条件が未確定", Status: "open"},
		{ID: "todo-weather", Kind: "todo", Severity: "medium", Title: "気象データを確認", Body: "基準風速の判断材料を確認する", Status: "open"},
	}}
	previousRaw, _ := json.Marshal(previous)
	segments := []domain.TranscriptSegment{{SequenceNo: 8, Text: "気象データを確認した結果、強風日の基準風速は12m/sとすることにします。", IsFinal: true}}
	model := `{"summary":"","currentTopic":"","resolvedIds":[],"items":[{"id":"todo-wind-decision","kind":"todo","severity":"high","title":"基準風速を12m/sとする","body":"気象データに基づき確定する","status":"open","evidenceSequenceNos":[8]}],"newTopics":[],"assignments":[]}`
	reconciled, _, err := reconcileDecisionCandidates(model, previousRaw, detectDecisionCandidates(segments))
	if err != nil {
		t.Fatal(err)
	}
	scope := liveEvidenceScope{Allowed: map[int64]struct{}{8: {}}, CurrentRound: map[int64]struct{}{8: {}}, TranscriptText: map[int64]string{8: segments[0].Text}, CoveredThrough: 8}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(reconciled, previousRaw, nil, 2, []int64{8}, scope, TreeClassificationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	for _, expected := range []struct{ id, kind string }{{"question-wind", "question"}, {"open-wind", "open_issue"}, {"todo-weather", "todo"}} {
		item := findItemByID(state.Items, expected.id)
		if item == nil || item.Kind != expected.kind || item.Status != "resolved" {
			t.Fatalf("item %s=%+v", expected.id, item)
		}
	}
	if livePayloadItemKindCounts(raw)["decision"] < 1 {
		t.Fatalf("decision missing: %+v", state.Items)
	}
}

func TestSemanticGroupingCreatesStableIssueGroup(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-2", Title: "騒音測定の実施方法", Order: 1, Role: agendaRolePrimary}}}
	content := `{"summary":"","currentTopic":"騒音","resolvedIds":[],"items":[
	{"id":"question-wind","kind":"question","severity":"medium","title":"強風日の基準風速は何m/sか","body":"基準を確認する","status":"open","evidenceSequenceNos":[10]},
	{"id":"open-wind","kind":"open_issue","severity":"high","title":"強風日の基準風速が未確定","body":"基準を決める必要がある","status":"open","evidenceSequenceNos":[10,11]},
	{"id":"todo-weather","kind":"todo","severity":"medium","title":"気象データを確認する","body":"基準風速の判断材料を確認する","status":"open","evidenceSequenceNos":[11]}
	],"newTopics":[],"assignments":[
	{"nodeId":"question-wind","parentTopicId":"agenda-2","confidence":0.9,"reason":""},
	{"nodeId":"open-wind","parentTopicId":"agenda-2","confidence":0.9,"reason":""},
	{"nodeId":"todo-weather","parentTopicId":"agenda-2","confidence":0.9,"reason":""}]}`
	raw, err := parseAndMergeLiveAnalysisPayload(content, nil, mc, 2, []int64{10, 11}, TreeClassificationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	health := computeTreeHealth(state.Tree)
	if health.GroupCount < 1 || treeDepthOf(state.Tree) < 3 || health.SingleChildGroupCount != 0 {
		t.Fatalf("health=%+v depth=%d tree=%+v", health, treeDepthOf(state.Tree), state.Tree)
	}
	next, err := parseAndMergeLiveAnalysisPayload(`{"summary":"次","currentTopic":"騒音","resolvedIds":[],"items":[],"newTopics":[],"assignments":[]}`, raw, mc, 3, nil, TreeClassificationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	nextHealth := computeTreeHealth(previousLiveAnalysisState(next).Tree)
	if nextHealth.GroupCount != health.GroupCount || nextHealth.SingleChildGroupCount != 0 {
		t.Fatalf("group churned: before=%+v after=%+v", health, nextHealth)
	}
}

func TestActionSummaryAssignmentSelectsSemanticPrimaryAgenda(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-1", Title: "渡り鳥の調査計画", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "騒音測定の実施方法", Order: 2, Role: agendaRolePrimary},
		{ID: "agenda-3", Title: "住民説明資料の作成", Order: 3, Role: agendaRolePrimary},
		{ID: "agenda-4", Title: "今後の対応事項", Order: 4, Role: agendaRoleActionSummary},
	}}
	content := `{"summary":"","currentTopic":"","resolvedIds":[],"items":[
	{"id":"todo-meeting-date","kind":"todo","severity":"medium","title":"住民説明会の開催日","body":"自治会から候補日を受け取る","status":"open","evidenceSequenceNos":[20]},
	{"id":"todo-wind","kind":"todo","severity":"medium","title":"強風日の基準風速","body":"騒音測定のため気象データを確認する","status":"open","evidenceSequenceNos":[14]},
	{"id":"todo-wetland","kind":"todo","severity":"medium","title":"湿地の希少植物","body":"専門家の予備調査を検討する","status":"open","evidenceSequenceNos":[25]}
	],"newTopics":[{"id":"topic-wetland","label":"湿地・希少植物","description":""}],"assignments":[
	{"nodeId":"todo-meeting-date","parentTopicId":"agenda-4","confidence":0.9,"reason":""},
	{"nodeId":"todo-wind","parentTopicId":"agenda-4","confidence":0.9,"reason":""},
	{"nodeId":"todo-wetland","parentTopicId":"topic-wetland","confidence":0.9,"reason":""}]}`
	raw, err := parseAndMergeLiveAnalysisPayload(content, nil, mc, 2, []int64{14, 20, 25}, TreeClassificationConfig{PromotionMinItems: 1, PromotionMinRounds: 1})
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	dynamicID, _ := canonicalCandidateID("湿地・希少植物", "")
	if itemTopicID(state.Tree, "todo-meeting-date") != "agenda-3" || itemTopicID(state.Tree, "todo-wind") != "agenda-2" || itemTopicID(state.Tree, "todo-wetland") != dynamicID {
		t.Fatalf("parents: meeting=%s wind=%s wetland=%s", parentOf(state.Tree, "todo-meeting-date"), parentOf(state.Tree, "todo-wind"), parentOf(state.Tree, "todo-wetland"))
	}
	for _, id := range []string{"todo-meeting-date", "todo-wind", "todo-wetland"} {
		item := findItemByID(state.Items, id)
		if item == nil || !containsString(item.RelatedAgendaIDs, "agenda-4") {
			t.Fatalf("action-summary relation missing for %s: %+v", id, item)
		}
	}
}

func TestTreeOperationResolvesLegacyItemAliases(t *testing.T) {
	tree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "会議"},
		{ID: "agenda-3", Kind: "topic", ParentID: treeRootNodeID, Label: "資料"},
		{ID: "todo- Residents-doc-publicity-01", Kind: "todo", ParentID: "agenda-3", Label: "公開"},
		{ID: "todo-date", Kind: "todo", ParentID: "agenda-3", Label: "日程"},
	}}
	stats := &liveAnalysisTreeMergeStats{}
	reorganized, applied := applyTreeOperations(tree, nil, []treeOperation{{Type: "create_group", ParentID: "agenda-3", Label: "住民向け対応", EvidenceItemIDs: []string{"todo-Residents-doc-publicity-01", "todo-date"}}}, TreeClassificationConfig{}, stats, 2)
	if applied != 1 || stats.AliasResolvedTreeOperationIDs == 0 || stats.UnknownGroupEvidenceIDs != 0 || computeTreeHealth(reorganized).GroupCount != 1 {
		t.Fatalf("applied=%d stats=%+v tree=%+v", applied, stats, reorganized)
	}
}

func TestSameKindSemanticDedupMergesResidentTodoButKeepsQuestion(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-3", Title: "住民説明資料の作成", Role: agendaRolePrimary}}}
	first := `{"summary":"","currentTopic":"","resolvedIds":[],"items":[{"id":"todo-tbd-public","kind":"todo","severity":"medium","title":"住民説明資料の公開方法を決定する","body":"公開方法が未定","status":"open","evidenceSequenceNos":[5]}],"newTopics":[],"assignments":[{"nodeId":"todo-tbd-public","parentTopicId":"agenda-3","confidence":0.9}]}`
	previous, err := parseAndMergeLiveAnalysisPayload(first, nil, mc, 1, []int64{5}, TreeClassificationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	second := `{"summary":"","currentTopic":"","resolvedIds":[],"items":[
		{"id":"todo-public-policy","kind":"todo","severity":"high","title":"住民説明資料の公開方針を決定する","body":"自治会と公開日程を調整する","status":"open","evidenceSequenceNos":[6]},
		{"id":"question-public-policy","kind":"question","severity":"medium","title":"住民説明資料の公開方針を決定する","body":"公開日はいつか","status":"open","evidenceSequenceNos":[6]}
	],"newTopics":[],"assignments":[{"nodeId":"todo-public-policy","parentTopicId":"agenda-3","confidence":0.9},{"nodeId":"question-public-policy","parentTopicId":"agenda-3","confidence":0.9}]}`
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayload(second, previous, mc, 2, []int64{6}, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	if len(state.Items) != 2 || livePayloadItemKindCounts(raw)["todo"] != 1 || livePayloadItemKindCounts(raw)["question"] != 1 {
		t.Fatalf("items=%+v", state.Items)
	}
	todo := findItemByID(state.Items, "todo-tbd-public")
	if todo == nil || !equalInt64s(todo.EvidenceSequenceNos, []int64{5, 6}) || stats.SameKindSemanticMerged != 1 {
		t.Fatalf("todo=%+v stats=%+v", todo, stats)
	}
	if findItemByID(state.Items, "todo-public-policy") != nil {
		t.Fatal("duplicate model id remained canonical")
	}
}

func TestSession888431064fad3f97DeterministicCleanup(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-3", Title: "住民説明資料の作成", Role: agendaRolePrimary}}}
	previous := liveAnalysisPayload{Items: []liveAnalysisItem{
		{ID: "todo-public", Kind: "todo", Severity: "medium", Title: "住民説明資料の公開方法を決定する", Body: "公開方法が未定", Status: "resolved", ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{5}},
		{ID: "todo-public-policy", Kind: "todo", Severity: "high", Title: "住民説明資料の公開方針を決定する", Body: "公開日程を調整する", Status: "resolved", ClassificationStatus: classificationTentative, CandidateTopicID: "topic-resident", EvidenceSequenceNos: []int64{6, 7}},
		{ID: "open-date", Kind: "open_issue", Severity: "high", Title: "住民説明会の開催日が未確定", Body: "自治会と調整中", Status: "open", ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{23}},
		{ID: "open-recap", Kind: "open_issue", Severity: "medium", Title: "未確定の課題は住民説明会の開催日です", Body: "未解決の課題は住民説明会の開催日です", Status: "open", ClassificationStatus: classificationUnclassified, EvidenceSequenceNos: []int64{32}},
	}, Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "会議"},
		{ID: "agenda-3", Kind: "topic", ParentID: treeRootNodeID, Label: "住民説明資料の作成", Origin: topicOriginAgenda},
		{ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: "追加論点", Origin: topicOriginSystem},
		{ID: "todo-public", Kind: "todo", ParentID: "agenda-3", Label: "公開方法"},
		{ID: "todo-public-policy", Kind: "todo", ParentID: treeUnclassifiedTopicID, Label: "公開方針"},
		{ID: "open-date", Kind: "open_issue", ParentID: "agenda-3", Label: "開催日"},
		{ID: "open-recap", Kind: "open_issue", ParentID: treeUnclassifiedTopicID, Label: "未確定課題"},
	}}}
	previousRaw, _ := json.Marshal(previous)
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayload(`{"summary":"","currentTopic":"","resolvedIds":[],"items":[],"newTopics":[],"assignments":[]}`, previousRaw, mc, 12, nil, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	if livePayloadItemKindCounts(raw)["todo"] != 1 || findItemByID(state.Items, "todo-public-policy") != nil || findItemByID(state.Items, "open-recap") != nil {
		t.Fatalf("items=%+v", state.Items)
	}
	canonical := findItemByID(state.Items, "todo-public")
	if canonical == nil || !equalInt64s(canonical.EvidenceSequenceNos, []int64{5, 6, 7}) || canonical.ClassificationStatus != classificationAssigned {
		t.Fatalf("canonical=%+v", canonical)
	}
	if stats.SameKindSemanticMerged < 2 || stats.RecapMerged != 1 {
		t.Fatalf("stats=%+v", stats)
	}
	assertControlledTreeInvariants(t, state.Tree, treeHardMaxDepth)
}

func TestCompanionItemsInheritPrimaryTopicWithoutCrossKindMerge(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-3", Title: "資料作成", Role: agendaRolePrimary}}}
	content := `{"summary":"","currentTopic":"","resolvedIds":[],"items":[
		{"id":"question-date","kind":"question","severity":"medium","title":"住民説明会の開催日はいつか","body":"自治会との日程確認が必要","status":"open","evidenceSequenceNos":[10]},
		{"id":"open-date","kind":"open_issue","severity":"high","title":"住民説明会の開催日が未確定","body":"自治会との日程が未調整","status":"open","evidenceSequenceNos":[11]},
		{"id":"todo-date","kind":"todo","severity":"high","title":"住民説明会の開催日を調整する","body":"自治会から候補日を得る","status":"open","evidenceSequenceNos":[11]}
	],"newTopics":[],"assignments":[
		{"nodeId":"question-date","parentTopicId":"topic-unclassified","confidence":0.7},
		{"nodeId":"open-date","parentTopicId":"topic-unclassified","confidence":0.7},
		{"nodeId":"todo-date","parentTopicId":"agenda-3","confidence":0.9}
	]}`
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayload(content, nil, mc, 2, []int64{10, 11}, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	if len(state.Items) != 3 {
		t.Fatalf("cross-kind companions were merged: %+v", state.Items)
	}
	for _, id := range []string{"question-date", "open-date", "todo-date"} {
		if got := itemTopicID(state.Tree, id); got != "agenda-3" {
			t.Fatalf("%s topic=%q tree=%+v", id, got, state.Tree)
		}
	}
	if stats.CompanionParentInherited < 2 || stats.CrossKindClustered < 2 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestActionSummaryProjectionSelectsTodoOrUnmatchedOpenIssue(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-2", Title: "騒音", Role: agendaRolePrimary},
		{ID: "agenda-3", Title: "住民説明", Role: agendaRolePrimary},
		{ID: "agenda-4", Title: "今後の対応事項", Role: agendaRoleActionSummary},
	}}
	tree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic"}, {ID: "agenda-2", Kind: "topic", ParentID: treeRootNodeID},
		{ID: "agenda-3", Kind: "topic", ParentID: treeRootNodeID}, {ID: "agenda-4", Kind: "topic", ParentID: treeRootNodeID, AgendaRole: agendaRoleActionSummary},
		{ID: "group-wind", Kind: "group", ParentID: "agenda-2"},
		{ID: "question-wind", Kind: "question", ParentID: "group-wind"}, {ID: "open-wind", Kind: "open_issue", ParentID: "group-wind"}, {ID: "todo-wind", Kind: "todo", ParentID: "group-wind"},
		{ID: "todo-done", Kind: "todo", ParentID: "agenda-2"}, {ID: "open-date", Kind: "open_issue", ParentID: "agenda-3"}, {ID: "decision-1", Kind: "decision", ParentID: "agenda-3"},
	}}
	items := []liveAnalysisItem{
		{ID: "question-wind", Kind: "question", Title: "基準風速は何か", Status: "open"},
		{ID: "open-wind", Kind: "open_issue", Title: "基準風速が未確定", Status: "open"},
		{ID: "todo-wind", Kind: "todo", Title: "気象データを確認", Status: "open"},
		{ID: "todo-done", Kind: "todo", Title: "測定回数を確定", Status: "resolved"},
		{ID: "open-date", Kind: "open_issue", Title: "説明会開催日が未確定", Status: "open"},
		{ID: "decision-1", Kind: "decision", Title: "公開方針", Status: "open"},
	}
	stats := &liveAnalysisTreeMergeStats{}
	syncRelatedAgendaIDs(items, mc, tree, stats)
	for _, id := range []string{"todo-wind", "open-date"} {
		item := findItemByID(items, id)
		if item == nil || !containsString(item.RelatedAgendaIDs, "agenda-4") {
			t.Fatalf("representative %s=%+v", id, item)
		}
	}
	for _, id := range []string{"question-wind", "open-wind", "todo-done", "decision-1"} {
		if item := findItemByID(items, id); item != nil && len(item.RelatedAgendaIDs) != 0 {
			t.Fatalf("non-representative %s=%+v", id, item)
		}
	}
	if stats.ActiveTodoReferences != 1 || stats.CompletedTodoExcluded != 1 || stats.ClusteredReferences != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestTentativeCandidatePromotesAtomicallyAfterStableVersions(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "既存議題", Role: agendaRolePrimary}}}
	first := `{"summary":"","currentTopic":"","resolvedIds":[],"items":[
		{"id":"todo-plant","kind":"todo","severity":"medium","title":"植物の予備調査","body":"専門家に依頼する","status":"open","evidenceSequenceNos":[1]},
		{"id":"question-plant","kind":"question","severity":"medium","title":"植物の予備調査を行うか","body":"次回検討する","status":"open","evidenceSequenceNos":[1]}
	],"newTopics":[{"id":"topic-plant","label":"湿地・希少植物"}],"assignments":[{"nodeId":"todo-plant","parentTopicId":"topic-plant","confidence":0.8},{"nodeId":"question-plant","parentTopicId":"topic-plant","confidence":0.8}]}`
	raw, err := parseAndMergeLiveAnalysisPayload(first, nil, mc, 1, []int64{1}, TreeClassificationConfig{PromotionMinItems: 2, PromotionMinRounds: 2})
	if err != nil {
		t.Fatal(err)
	}
	stats := &liveAnalysisTreeMergeStats{}
	second := `{"summary":"","currentTopic":"","resolvedIds":[],"items":[
		{"id":"todo-plant","kind":"todo","severity":"medium","title":"植物の予備調査","body":"専門家に依頼する","status":"updated","evidenceSequenceNos":[2]}
	],"newTopics":[],"assignments":[{"nodeId":"todo-plant","parentTopicId":"topic-plant","confidence":0.8}]}`
	raw, err = parseAndMergeLiveAnalysisPayload(second, raw, mc, 2, []int64{2}, TreeClassificationConfig{PromotionMinItems: 2, PromotionMinRounds: 2}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	dynamicID, _ := canonicalCandidateID("湿地・希少植物", "")
	if len(state.EmergingTopics) != 0 || itemTopicID(state.Tree, "todo-plant") != dynamicID || itemTopicID(state.Tree, "question-plant") != dynamicID {
		t.Fatalf("state=%+v", state)
	}
	if stats.PromotedItemsReparented != 2 || state.TreeChanges == nil || len(state.TreeChanges.ReparentedNodeIDs) != 2 {
		t.Fatalf("stats=%+v changes=%+v", stats, state.TreeChanges)
	}
}

func TestUnsupportedEmergingCandidateBecomesInactiveAfterFourVersions(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "既存議題", Role: agendaRolePrimary}}}
	first := `{"summary":"","currentTopic":"","resolvedIds":[],"items":[{"id":"todo-other","kind":"todo","severity":"medium","title":"単発の新規課題","body":"追加情報なし","status":"open","evidenceSequenceNos":[1]}],"newTopics":[{"id":"topic-other","label":"単発候補"}],"assignments":[{"nodeId":"todo-other","parentTopicId":"topic-other","confidence":0.8}]}`
	raw, err := parseAndMergeLiveAnalysisPayload(first, nil, mc, 1, []int64{1}, TreeClassificationConfig{PromotionMinItems: 2, PromotionMinRounds: 2})
	if err != nil {
		t.Fatal(err)
	}
	var finalStats *liveAnalysisTreeMergeStats
	for version := int64(2); version <= 5; version++ {
		finalStats = &liveAnalysisTreeMergeStats{}
		raw, err = parseAndMergeLiveAnalysisPayload(`{"summary":"","currentTopic":"","resolvedIds":[],"items":[],"newTopics":[],"assignments":[]}`, raw, mc, version, nil, TreeClassificationConfig{PromotionMinItems: 2, PromotionMinRounds: 2}, finalStats)
		if err != nil {
			t.Fatal(err)
		}
	}
	state := previousLiveAnalysisState(raw)
	if len(state.EmergingTopics) != 1 || !state.EmergingTopics[0].Inactive || finalStats.StaleCandidatesHidden != 1 {
		t.Fatalf("candidates=%+v stats=%+v", state.EmergingTopics, finalStats)
	}
	item := findItemByID(state.Items, "todo-other")
	if item == nil || item.ClassificationStatus != classificationTentative || !item.CandidateInactive {
		t.Fatalf("audit item must remain staged in payload: %+v", item)
	}
}

func TestSession04e9dec1aaa164b3ReplayAcceptance(t *testing.T) {
	segments := session04e9dec1aaa164b3Segments()
	mc := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-1", Title: "渡り鳥の調査計画", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "騒音測定の実施方法", Order: 2, Role: agendaRolePrimary},
		{ID: "agenda-3", Title: "住民説明資料の作成", Order: 3, Role: agendaRolePrimary},
		{ID: "agenda-4", Title: "今後の対応事項", Order: 4, Role: agendaRoleActionSummary},
	}}
	// Deterministic reproduction of the observed failure classes: decisions
	// initially arrive as TODO, unresolved states as risk/TODO, an item ID and
	// its references differ by whitespace, and action-summary is requested as
	// the primary parent.
	model := `{"summary":"fixture","currentTopic":"まとめ","resolvedIds":["risk-bird-sites","todo-Residents-doc-publicity-01"],"items":[
	{"id":"risk-bird-sites","kind":"risk","severity":"high","title":"渡り鳥の観測地点不足","body":"一地点では経路を確認できない","status":"resolved","evidenceSequenceNos":[3,7]},
	{"id":"todo-bird-sites","kind":"todo","severity":"high","title":"三地点で渡り鳥を調査","body":"海岸側、北側、南側で調査する","status":"resolved","evidenceSequenceNos":[8]},
	{"id":"todo-noise-count","kind":"todo","severity":"high","title":"昼1回・夜2回の騒音測定","body":"合計3回実施する","status":"resolved","evidenceSequenceNos":[13]},
	{"id":"todo- Residents-doc-publicity-01","kind":"todo","severity":"high","title":"調査結果を図付きでWeb公開","body":"住民説明資料を公開する","status":"resolved","evidenceSequenceNos":[18,19]},
	{"id":"risk-strong-wind","kind":"risk","severity":"high","title":"強風日の基準風速","body":"騒音測定の基準が未確定","status":"open","evidenceSequenceNos":[14]},
	{"id":"todo-weather","kind":"todo","severity":"medium","title":"気象データを確認","body":"強風日の基準風速を判断する","status":"open","evidenceSequenceNos":[15]},
	{"id":"todo-meeting-date","kind":"todo","severity":"medium","title":"住民説明会の開催日","body":"自治会から候補日を受け取る","status":"open","evidenceSequenceNos":[22]},
	{"id":"todo-wetland","kind":"todo","severity":"medium","title":"湿地の予備調査を検討","body":"希少植物を専門家に確認する","status":"open","evidenceSequenceNos":[26]}
	],"newTopics":[{"id":"topic-wetland","label":"湿地・希少植物","description":""}],"assignments":[
	{"nodeId":"risk-bird-sites","parentTopicId":"agenda-1","confidence":0.9,"reason":""},
	{"nodeId":"todo-bird-sites","parentTopicId":"agenda-1","confidence":0.9,"reason":""},
	{"nodeId":"todo-noise-count","parentTopicId":"agenda-2","confidence":0.9,"reason":""},
	{"nodeId":"todo-Residents-doc-publicity-01","parentTopicId":"agenda-4","confidence":0.9,"reason":""},
	{"nodeId":"risk-strong-wind","parentTopicId":"agenda-2","confidence":0.9,"reason":""},
	{"nodeId":"todo-weather","parentTopicId":"agenda-2","confidence":0.9,"reason":""},
	{"nodeId":"todo-meeting-date","parentTopicId":"agenda-4","confidence":0.9,"reason":""},
	{"nodeId":"todo-wetland","parentTopicId":"topic-wetland","confidence":0.9,"reason":""}]}`
	issueContent, _, err := reconcileIssueCandidates(model, nil, detectIssueCandidates(segments))
	if err != nil {
		t.Fatal(err)
	}
	reconciled, _, err := reconcileDecisionCandidates(issueContent, nil, detectDecisionCandidates(segments))
	if err != nil {
		t.Fatal(err)
	}
	sequenceNos := make([]int64, 0, len(segments))
	for _, segment := range segments {
		sequenceNos = append(sequenceNos, segment.SequenceNo)
	}
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayload(reconciled, nil, mc, 2, sequenceNos, TreeClassificationConfig{PromotionMinItems: 1, PromotionMinRounds: 1}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	counts := livePayloadItemKindCounts(raw)
	if counts["decision"] < 3 || counts["question"] < 1 || counts["open_issue"] < 1 || counts["todo"] < 3 {
		t.Fatalf("kind counts=%v items=%+v", counts, state.Items)
	}
	for _, item := range state.Items {
		if (item.Kind == "decision" || item.Kind == "fact") && item.Status == "resolved" {
			t.Fatalf("non-resolvable item remained resolved: %+v", item)
		}
	}
	residentID := "todo-residents-doc-publicity-01"
	dynamicID, _ := canonicalCandidateID("湿地・希少植物", "")
	if itemTopicID(state.Tree, residentID) != "agenda-3" || itemTopicID(state.Tree, "todo-meeting-date") != "agenda-3" || itemTopicID(state.Tree, "todo-wetland") != dynamicID {
		t.Fatalf("parents resident=%s meeting=%s wetland=%s", parentOf(state.Tree, residentID), parentOf(state.Tree, "todo-meeting-date"), parentOf(state.Tree, "todo-wetland"))
	}
	health := computeTreeHealth(state.Tree)
	if health.GroupCount < 1 || treeDepthOf(state.Tree) < 3 || health.SingleChildGroupCount != 0 || stats.UnknownAssignmentIDs != 0 || stats.UnknownResolvedIDs != 0 {
		t.Fatalf("health=%+v depth=%d stats=%+v", health, treeDepthOf(state.Tree), stats)
	}
	agendaCounts := itemTopicCounts(state.Tree, state.Items)
	unclassified := make([]string, 0)
	for _, item := range state.Items {
		if itemTopicID(state.Tree, item.ID) == treeUnclassifiedTopicID {
			unclassified = append(unclassified, item.ID+":"+item.Kind+":"+item.Title)
		}
	}
	t.Logf("session_04e9 replay: items=%d decisions=%d questions=%d openIssues=%d todos=%d risks=%d groups=%d nestedGroups=%d maxDepth=%d unknownAssignments=%d unknownResolved=%d agendaCounts=%v",
		len(state.Items), counts["decision"], counts["question"], counts["open_issue"], counts["todo"], counts["risk"], health.GroupCount, health.NestedGroupCount, treeDepthOf(state.Tree), stats.UnknownAssignmentIDs, stats.UnknownResolvedIDs, agendaCounts)
	t.Logf("session_04e9 replay unclassified=%v", unclassified)
}

func session04e9dec1aaa164b3Segments() []domain.TranscriptSegment {
	texts := []string{
		"それでは、沿岸部風力発電計画に関する環境アセスメント検討会を始めます。",
		"まず、渡り鳥の調査計画について確認します。",
		"現在の計画では、会館側の観測地点が一カ所しかなく、鳥の移動経路を十分に確認できていないのではないかという懸念が出ていました。",
		"これについて、現地担当者から開眼側に加えて、予定地の北側と南側にも観測地点も設置できるという回答がありました。",
		"3方向から観測すれば、主な飛行経路と飛行度を確認できる見込みです。",
		"したがって、観測地点が不足しているという問題は、追加地点を設けることで対応できると判断します。",
		"この論点は解決済みとします。",
		"渡り鳥の調査については、海岸側、北側、南側の合計3時点で実施することを決定します。",
		"次に、騒音測定の実施方法についてです。",
		"周辺住民からは、昼間よりも夜間の低周波音を心配する声が出ています。",
		"当社の計画では昼間のみ2回測定する予定でしたが、それでは住民の懸念に十分対応できていません。",
		"そこで、昼間に1回、夜間に2回測定する案を採用したいと思います。",
		"夜間の測定は、風邪が比較的弱い人を強い日に1回ずつ実施します。騒音測定は昼間1回、夜間2回の合計3回実施することを決定事項とします。",
		"ただし、強風日の測定条件については、どの風速を基準にするかは決まっていません。",
		"この点は気象データを確認してから判断するため、現時点では未解決の課題として起こします。",
		"続いて、住民説明資料についてです。",
		"現在の資料には設備の位置と調査日程は記載されていますが、調査結果をどのように公開するかが書いていません。",
		"住民が後から確認できるよう、調査結果の概要を第三の上、ウェブサイトで公開する方針にします。",
		"公開する資料には、専門用語だけではなく、渦や簡単な説明をつけることも決定します。",
		"一方説明会そのものの開催日は。",
		"地域の自治会と調整で言ってきていません。",
		"開催日はまだ決定せず、自治会から候補日を受け取った後に改めて確定します。",
		"最後に。アジェンダにはありませんでしたが、現地担当者から新しい報告があります。",
		"建設予定地の近くに小規模な湿地が見つかり、希少な植物が生育している可能性があるとのことです。",
		"現時点では、植物の種類が確認できていないために、既存の鳥類調査や騒音調査の中に無理に含めず、新しい調査課題として扱う必要があります。",
		"植物の種類を確認するため、専門家による予備調査を実施するかどうかを次回の会議で検討します。",
		"ビジョンをまとめます。",
		"ええ。渡り鳥の観測支店不足については、三地点で調査することで解決済みです。",
		"決定事項は渡り鳥を三地点で調査すること、騒音を中間1回と夜間2回測定すること。",
		"そして、調査結果を頭突きでウェブ公開することです。",
		"未解決の課題は強風日の風速基準と住民説明会の開催日です。",
		"また、湿地の希少植物についてはアジェンダ外から生まれた。",
		"新しい論点として、次回以降も検討をします。",
	}
	segments := make([]domain.TranscriptSegment, 0, len(texts))
	for index, text := range texts {
		segments = append(segments, domain.TranscriptSegment{SessionID: "session_04e9dec1aaa164b3", SequenceNo: int64(index + 1), Text: text, IsFinal: true})
	}
	return segments
}

func parentOf(tree *liveAnalysisTree, id string) string {
	if tree != nil {
		for _, node := range tree.Nodes {
			if node.ID == id {
				return node.ParentID
			}
		}
	}
	return ""
}

func itemTopicCounts(tree *liveAnalysisTree, items []liveAnalysisItem) map[string]int {
	parents := make(map[string]string)
	kinds := make(map[string]string)
	if tree != nil {
		for _, node := range tree.Nodes {
			parents[node.ID] = node.ParentID
			kinds[node.ID] = node.Kind
		}
	}
	counts := make(map[string]int)
	for _, item := range items {
		current := parents[item.ID]
		seen := make(map[string]struct{})
		for current != "" {
			if _, loop := seen[current]; loop {
				break
			}
			seen[current] = struct{}{}
			if kinds[current] == "topic" {
				counts[current]++
				break
			}
			current = parents[current]
		}
	}
	return counts
}

func itemTopicID(tree *liveAnalysisTree, itemID string) string {
	parents := make(map[string]string)
	kinds := make(map[string]string)
	if tree != nil {
		for _, node := range tree.Nodes {
			parents[node.ID] = node.ParentID
			kinds[node.ID] = node.Kind
		}
	}
	current := parents[itemID]
	seen := make(map[string]struct{})
	for current != "" {
		if _, loop := seen[current]; loop {
			return ""
		}
		seen[current] = struct{}{}
		if kinds[current] == "topic" {
			return current
		}
		current = parents[current]
	}
	return ""
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func equalInt64s(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
