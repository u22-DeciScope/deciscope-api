package application

import (
	"encoding/json"
	"testing"

	"deciscope-core-api/internal/domain"
)

func evidenceScopeFromTexts(texts map[int64]string, current ...int64) liveEvidenceScope {
	scope := liveEvidenceScope{Allowed: map[int64]struct{}{}, CurrentRound: map[int64]struct{}{}, TranscriptText: map[int64]string{}}
	for sequenceNo, text := range texts {
		scope.Allowed[sequenceNo] = struct{}{}
		scope.TranscriptText[sequenceNo] = text
		if sequenceNo > scope.CoveredThrough {
			scope.CoveredThrough = sequenceNo
		}
	}
	for _, sequenceNo := range current {
		scope.CurrentRound[sequenceNo] = struct{}{}
	}
	return scope
}

func TestResolutionUpdatesApplyOnlyGroundedItemAndRejectMassLegacy(t *testing.T) {
	previous := liveAnalysisPayload{Items: []liveAnalysisItem{
		{ID: "risk-sites", Kind: "risk", Severity: "high", Title: "観測地点が不足", Body: "一地点では渡り鳥の経路を確認できない", Status: "open"},
		{ID: "question-wind", Kind: "question", Severity: "high", Title: "強風日の基準風速は何m/sか", Body: "基準は未決定", Status: "open"},
		{ID: "open-date", Kind: "open_issue", Severity: "high", Title: "住民説明会の開催日が未決定", Body: "自治会と未調整", Status: "open"},
		{ID: "todo-plant", Kind: "todo", Severity: "medium", Title: "湿地の予備調査を検討する", Body: "次回検討", Status: "open"},
	}}
	previousRaw, _ := json.Marshal(previous)
	content := `{"summary":"","currentTopic":"湿地","resolvedIds":["question-wind","open-date","todo-plant"],"resolutionUpdates":[{"itemId":"risk-sites","status":"resolved","evidenceSequenceNos":[1,2],"reason":"追加地点で対応可能と明示"}],"items":[{"id":"todo-wetland-new","kind":"todo","severity":"medium","title":"湿地を確認する","body":"新しい話題","status":"open","evidenceSequenceNos":[3]}],"newTopics":[],"assignments":[]}`
	scope := evidenceScopeFromTexts(map[int64]string{
		1: "観測地点が不足しているため、北側と南側にも観測地点を設けます。",
		2: "観測地点不足は追加地点で対応できるため、この論点は解決済みとします。",
		3: "次に、湿地の希少植物についてです。",
	}, 1, 2, 3)
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(content, previousRaw, nil, 2, []int64{1, 2, 3}, scope, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	if item := findItemByID(state.Items, "risk-sites"); item == nil || item.Status != "resolved" || !equalInt64s(item.ResolutionEvidenceSequenceNos, []int64{1, 2}) {
		t.Fatalf("grounded risk=%+v", item)
	}
	for _, id := range []string{"question-wind", "open-date", "todo-plant"} {
		if item := findItemByID(state.Items, id); item == nil || item.Status == "resolved" {
			t.Fatalf("unrelated item %s=%+v", id, item)
		}
	}
	audit := summarizeResolutionEvaluations(stats.ResolutionDecisions)
	if audit.Applied != 1 || audit.RejectedNoEvidence != 3 {
		t.Fatalf("audit=%+v decisions=%+v", audit, stats.ResolutionDecisions)
	}
}

func TestIssueRecapReopensExistingQuestionAndOpenIssue(t *testing.T) {
	previous := liveAnalysisPayload{Items: []liveAnalysisItem{
		{ID: "question-wind", Kind: "question", Severity: "high", Title: "強風日の風速基準は何m/sか", Body: "基準風速を決める", Status: "resolved", ResolvedAtVersion: 4},
		{ID: "open-wind", Kind: "open_issue", Severity: "high", Title: "強風日の風速基準が未決定", Body: "基準風速を決める必要がある", Status: "resolved", ResolvedAtVersion: 4},
	}}
	previousRaw, _ := json.Marshal(previous)
	content := `{"summary":"まとめ","currentTopic":"まとめ","resolvedIds":[],"resolutionUpdates":[],"items":[],"newTopics":[],"assignments":[]}`
	statement := "未解決の課題は、強風日の風速基準です。"
	segments := []domain.TranscriptSegment{{SessionID: "session-resolution", SequenceNo: 35, Text: statement, IsFinal: true}}
	reconciled, audit, err := reconcileIssueCandidates(content, previousRaw, detectIssueCandidates(segments))
	if err != nil {
		t.Fatal(err)
	}
	if audit.RecapMerged == 0 {
		t.Fatalf("recap audit=%+v", audit)
	}
	scope := evidenceScopeFromTexts(map[int64]string{35: statement}, 35)
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(reconciled, previousRaw, nil, 5, []int64{35}, scope, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	if len(state.Items) != 2 {
		t.Fatalf("recap created duplicate items: %+v", state.Items)
	}
	for _, id := range []string{"question-wind", "open-wind"} {
		item := findItemByID(state.Items, id)
		if item == nil || item.Status != "open" || item.ReopenedAtVersion != 5 || !equalInt64s(item.ReopenEvidenceSequenceNos, []int64{35}) {
			t.Fatalf("reopened %s=%+v", id, item)
		}
	}
}

func TestDecisionResolutionIsLimitedToMatchingSubject(t *testing.T) {
	previous := liveAnalysisPayload{Items: []liveAnalysisItem{
		{ID: "question-count", Kind: "question", Severity: "high", Title: "騒音測定は何回にするか", Body: "測定回数を決める", Status: "open"},
		{ID: "question-wind", Kind: "question", Severity: "high", Title: "強風日の基準風速は何m/sか", Body: "風速基準を決める", Status: "open"},
	}}
	previousRaw, _ := json.Marshal(previous)
	statement := "騒音測定は昼1回、夜2回の合計3回実施することを決定事項とします。"
	base := `{"summary":"","currentTopic":"騒音","resolvedIds":[],"resolutionUpdates":[],"items":[],"newTopics":[],"assignments":[]}`
	segments := []domain.TranscriptSegment{{SessionID: "session-resolution", SequenceNo: 17, Text: statement, IsFinal: true}}
	reconciled, _, err := reconcileDecisionCandidates(base, previousRaw, detectDecisionCandidates(segments))
	if err != nil {
		t.Fatal(err)
	}
	scope := evidenceScopeFromTexts(map[int64]string{17: statement}, 17)
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(reconciled, previousRaw, nil, 2, []int64{17}, scope, TreeClassificationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	if findItemByID(state.Items, "question-count").Status != "resolved" {
		t.Fatalf("measurement question not resolved: %+v", state.Items)
	}
	if findItemByID(state.Items, "question-wind").Status == "resolved" {
		t.Fatalf("unrelated wind question resolved: %+v", state.Items)
	}
}

func TestActiveAgendaSpanPlacesResidentMaterialUnderAgendaThree(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-2", Title: "騒音測定の実施方法", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-3", Title: "住民説明資料の作成", Order: 2, Role: agendaRolePrimary},
		{ID: "agenda-4", Title: "今後の対応事項", Order: 3, Role: agendaRoleActionSummary},
	}}
	scope := evidenceScopeFromTexts(map[int64]string{
		12: "次に、騒音測定の実施方法についてです。",
		20: "続いて、住民説明資料について確認します。",
		23: "現在の住民説明資料には、調査結果をどのように公開するかが書かれていません。",
	}, 20, 23)
	content := `{"summary":"","currentTopic":"住民説明資料","resolvedIds":[],"resolutionUpdates":[],"items":[{"id":"question-public","kind":"question","severity":"high","title":"住民説明資料の公開方法は何か","body":"公開方法が未記載","status":"open","evidenceSequenceNos":[23]}],"newTopics":[],"assignments":[{"nodeId":"question-public","parentTopicId":"topic-unclassified","confidence":0.5,"reason":"model unclassified"}]}`
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(content, nil, mc, 1, []int64{20, 23}, scope, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	if got := itemTopicID(state.Tree, "question-public"); got != "agenda-3" {
		t.Fatalf("topic=%q tree=%+v assignments=%+v", got, state.Tree, stats.AssignmentDecisions)
	}
	if item := findItemByID(state.Items, "question-public"); item == nil || item.AssignmentSource != assignmentSourceActiveSpan {
		t.Fatalf("item=%+v", item)
	}
	for _, node := range state.Tree.Nodes {
		if node.ID == "agenda-4" {
			t.Fatal("action summary agenda rendered as canonical node")
		}
	}
}

func TestAgendaRoleInferenceAndPrimaryDistinction(t *testing.T) {
	items := parseAgendaItems("1. 今後の対応事項\n2. 実施体制と担当者の決定\n3. フォローアップ")
	if len(items) != 3 || items[0].Role != agendaRoleActionSummary || items[1].Role != agendaRolePrimary || items[2].Role != agendaRoleActionSummary {
		t.Fatalf("agenda roles=%+v", items)
	}
	planned, err := parseContextPlannerResult(`{"agendaItems":[{"title":"今後の対応事項","order":1,"role":"primary"},{"title":"スケジュール調整","order":2,"role":"primary"}]}`, nil)
	if err != nil || planned.Agenda[0].Role != agendaRoleActionSummary || planned.Agenda[1].Role != agendaRolePrimary {
		t.Fatalf("planned=%+v err=%v", planned, err)
	}
}

func TestReorganizerRejectsCrossPrimaryAgendaMove(t *testing.T) {
	tree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "会議"},
		{ID: "agenda-2", Kind: "topic", ParentID: treeRootNodeID, Label: "騒音", Origin: topicOriginAgenda},
		{ID: "agenda-3", Kind: "topic", ParentID: treeRootNodeID, Label: "住民資料", Origin: topicOriginAgenda},
		{ID: "question-public", Kind: "question", ParentID: "agenda-3", Label: "公開方法"},
	}}
	stats := &liveAnalysisTreeMergeStats{}
	result, applied := applyTreeOperations(tree, nil, []treeOperation{{Type: "move_node", NodeID: "question-public", ToParentID: "agenda-2"}}, TreeClassificationConfig{}, stats, 2)
	if applied != 0 || parentOf(result, "question-public") != "agenda-3" || stats.ReorganizeRejections["cross_primary_agenda"] != 1 {
		t.Fatalf("applied=%d parent=%s stats=%+v", applied, parentOf(result, "question-public"), stats)
	}
}

// This fixture replays every persisted final transcript segment from
// session_0f1ade26ee8babed without reading from or writing to the database.
// The model response deliberately reproduces the observed failure modes:
// unrelated legacy resolvedIds and a resident-material parent under agenda-2.
func TestSession0f1ade26ee8babedDeterministicReplay(t *testing.T) {
	segments := []domain.TranscriptSegment{
		finalSegment(1, "それでは、沿岸部風力発電計画に関する環境アセスメント検討会を始めます。"),
		finalSegment(2, "まず、渡り鳥の調査計画について確認します。"),
		finalSegment(3, "事前調査では、風力発電設備の建設予定地付金を春と秋に複数の渡り鳥が通過する可能性があるとされています。"),
		finalSegment(4, "現代の計画では、沿岸海岸側の。"),
		finalSegment(5, "観測地点が一カ所しかなく。"),
		finalSegment(6, "鳥の移動経路を十分に確認できていないのではないか？という懸念が出ていました。"),
		finalSegment(7, "これについて、現地担当者から会談側に加えて、予定地の北側と南側にも観測地点を設置できるという回答がありました。"),
		finalSegment(8, "3方向から観測すれば、主な飛行経路と飛行度を確認できる見込みです。"),
		finalSegment(9, "したがって、観測地点が不足しているという問題は、追加地点を設けることで対応できると判断します。"),
		finalSegment(10, "この論点は解決済みとします。"),
		finalSegment(11, "渡り鳥の調査については、海岸側、北側、南側の合計三地点で実施することを決定します。"),
		finalSegment(12, "次に、騒音測定の実施方法についてです。"),
		finalSegment(13, "現周辺住民からは、昼間よりも夜間の低周波音を心配する声が出ています。"),
		finalSegment(14, "当社の計画では昼間のみ2回測定する予定でしたが、それでは住民の懸念に十分対応できません。"),
		finalSegment(15, "そこで、昼間に1回、夜間に2回測定する案を採用したいと思います。"),
		finalSegment(16, "夜間の測定は、風邪が比較的弱い人、強い日に1回ずつ実施します。"),
		finalSegment(17, "騒音測定は昼間1回、夜間2回の合計3回実施することを決定事項とします。"),
		finalSegment(18, "ただし、教育日の測定条件については、どの風速を規律にするか決まっていません。"),
		finalSegment(19, "この点は気象データを確認してから判断するため、現時点では未解決の課題として残します。"),
		finalSegment(20, "続いて、住民説明資料についてです。"),
		finalSegment(21, "現在の資料には。"),
		finalSegment(22, "設備の位置と調査日程は記載されていますか？"),
		finalSegment(23, "調査結果をどのように公開するかが書かれていません。"),
		finalSegment(24, "住民が後から確認できるよう、調査結果の概要を団体のウェブサイトで公開する方針にします。"),
		finalSegment(25, "公開する資料には、専門用語だけではなく、図や簡単な説明をつけることも決定します。"),
		finalSegment(26, "一方説明会そのものの開催日は地域の自治会。こう調整できていません。"),
		finalSegment(27, "開催日はまだ決定せず、自治会から候補日を受け取った後に改めて確定します。"),
		finalSegment(28, "最後に。アジェンダにはありませんでしたが、現地担当者から新しい報告があります。"),
		finalSegment(29, "建設予定地の近くに小規模な湿地が見つかり、希少な植物が生育している可能性があるとのことです。"),
		finalSegment(30, "現時点では植物の種類が確認できてないため、依存の鳥類調査や騒音調査の中に無理に含めず、新しい調査課題として扱う必要があります。"),
		finalSegment(31, "植物の種類を確認するため、専門家による予備調査を実施するかどうかを次回の会議で検討します。"),
		finalSegment(32, "以上をまとめます。"),
		finalSegment(33, "渡り鳥の観測地点不足については、三地点で調査することで解決済みです。"),
		finalSegment(34, "決定事項は、渡り鳥を三地点で調査すること、騒音を昼間1回と夜間2回測定すること、そして調査結果を頭突きでウェブ公開することです。"),
		finalSegment(35, "未解決の課題は強風日の風速基準と住民説明会の開催日です。"),
		finalSegment(36, "また、湿地の気象を植物についてはアジェンダ街から生まれた。"),
		finalSegment(37, "ええ。新しい論点として、次回以降も検討をします。"),
	}
	mc := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-1", Title: "渡り鳥の調査計画", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "騒音測定の実施方法", Order: 2, Role: agendaRolePrimary},
		{ID: "agenda-3", Title: "住民説明資料の作成", Order: 3, Role: agendaRolePrimary},
		{ID: "agenda-4", Title: "今後の対応事項", Order: 4, Role: agendaRolePrimary}, // legacy planner output is repaired deterministically.
	}}
	model := `{"summary":"対象会議の再生","currentTopic":"まとめ","resolvedIds":["question-wind","open-date","todo-wetland"],"resolutionUpdates":[{"itemId":"risk-sites","status":"resolved","evidenceSequenceNos":[9,10],"reason":"追加地点で対応可能かつ解決済みと明示"}],"items":[
		{"id":"risk-sites","kind":"risk","severity":"high","title":"渡り鳥の観測地点が不足している","body":"一地点では移動経路を確認できない","status":"open","evidenceSequenceNos":[5,6]},
		{"id":"question-count","kind":"question","severity":"high","title":"騒音測定を何回実施するか","body":"昼夜の測定回数を決める","status":"open","evidenceSequenceNos":[13,14]},
		{"id":"question-wind","kind":"question","severity":"high","title":"強風日の基準風速を何m/sにするか","body":"測定条件の風速基準が未決定","status":"open","evidenceSequenceNos":[18]},
		{"id":"open-wind","kind":"open_issue","severity":"high","title":"強風日の風速基準が未解決","body":"気象データを確認して判断する","status":"open","evidenceSequenceNos":[18,19]},
		{"id":"todo-weather","kind":"todo","severity":"high","title":"気象データを確認する","body":"風速基準の判断材料を確認する","status":"open","evidenceSequenceNos":[19]},
		{"id":"question-public","kind":"question","severity":"high","title":"住民説明資料の公開方法は何か","body":"調査結果の公開方法が書かれていない","status":"open","evidenceSequenceNos":[23]},
		{"id":"open-date","kind":"open_issue","severity":"high","title":"住民説明会の開催日が未決定","body":"自治会から候補日を受け取った後に確定する","status":"open","evidenceSequenceNos":[26,27]},
		{"id":"todo-date","kind":"todo","severity":"medium","title":"自治会から候補日を受け取る","body":"候補日の受領後に開催日を確定する","status":"open","evidenceSequenceNos":[27]},
		{"id":"todo-wetland","kind":"todo","severity":"medium","title":"湿地の植物の予備調査を検討する","body":"専門家による予備調査を次回検討する","status":"open","evidenceSequenceNos":[31]}
	],"newTopics":[{"id":"topic-wetland","label":"湿地・希少植物"}],"assignments":[
		{"nodeId":"risk-sites","parentTopicId":"agenda-1","confidence":0.9},
		{"nodeId":"question-count","parentTopicId":"agenda-2","confidence":0.9},
		{"nodeId":"question-wind","parentTopicId":"agenda-2","confidence":0.9},
		{"nodeId":"open-wind","parentTopicId":"agenda-2","confidence":0.9},
		{"nodeId":"todo-weather","parentTopicId":"agenda-2","confidence":0.9},
		{"nodeId":"question-public","parentTopicId":"agenda-2","confidence":0.9},
		{"nodeId":"open-date","parentTopicId":"agenda-3","confidence":0.9},
		{"nodeId":"todo-date","parentTopicId":"agenda-3","confidence":0.9},
		{"nodeId":"todo-wetland","parentTopicId":"topic-wetland","confidence":0.9}
	]}`
	issueContent, issueAudit, err := reconcileIssueCandidates(model, nil, detectIssueCandidates(segments))
	if err != nil {
		t.Fatal(err)
	}
	if issueAudit.RecapMerged == 0 {
		t.Fatalf("recap was not reconciled: %+v", issueAudit)
	}
	reconciled, _, err := reconcileDecisionCandidates(issueContent, nil, detectDecisionCandidates(segments))
	if err != nil {
		t.Fatal(err)
	}
	sequenceNos := make([]int64, 0, len(segments))
	scope := liveEvidenceScope{Allowed: map[int64]struct{}{}, CurrentRound: map[int64]struct{}{}, TranscriptText: map[int64]string{}, CoveredThrough: 37}
	for _, segment := range segments {
		sequenceNos = append(sequenceNos, segment.SequenceNo)
		scope.Allowed[segment.SequenceNo] = struct{}{}
		scope.CurrentRound[segment.SequenceNo] = struct{}{}
		scope.TranscriptText[segment.SequenceNo] = segment.Text
	}
	previousRaw, _ := json.Marshal(liveAnalysisPayload{CoveredThroughSequenceNo: 37})
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(reconciled, previousRaw, mc, 15, sequenceNos, scope, TreeClassificationConfig{PromotionMinItems: 1, PromotionMinRounds: 1}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	if item := findItemByID(state.Items, "risk-sites"); item == nil || item.Status != "resolved" {
		t.Fatalf("observation risk=%+v", item)
	}
	for _, id := range []string{"question-wind", "open-wind", "open-date", "todo-weather", "todo-date", "todo-wetland"} {
		if item := findItemByID(state.Items, id); item == nil || item.Status == "resolved" {
			t.Fatalf("item %s must remain open/active: %+v", id, item)
		}
	}
	if item := findItemByID(state.Items, "question-count"); item == nil || item.Status != "resolved" {
		t.Fatalf("measurement-count question=%+v resolutions=%+v", item, stats.ResolutionDecisions)
	}
	if got := itemTopicID(state.Tree, "question-public"); got != "agenda-3" {
		t.Fatalf("resident-material parent=%q, want agenda-3", got)
	}
	if item := findItemByID(state.Items, "question-public"); item == nil || item.AssignmentSource != assignmentSourceActiveSpan {
		t.Fatalf("resident-material assignment=%+v", item)
	}
	actionRows := 0
	for _, item := range state.Items {
		for _, agendaID := range item.RelatedAgendaIDs {
			if agendaID == "agenda-4" {
				actionRows++
			}
		}
	}
	for _, node := range state.Tree.Nodes {
		if node.ID == "agenda-4" {
			t.Fatal("legacy action-summary agenda must not be a canonical tree node")
		}
	}
	if actionRows < 3 {
		t.Fatalf("action summary rows=%d items=%+v", actionRows, state.Items)
	}
	resolutionAudit := summarizeResolutionEvaluations(stats.ResolutionDecisions)
	if resolutionAudit.Rejected < 3 || stats.ActiveAgendaSpanCount != 4 || stats.NoAgendaSpanCount != 1 || state.CoveredThroughSequenceNo != 37 {
		t.Fatalf("resolutionAudit=%+v agendaSpans=%d coverage=%d", resolutionAudit, stats.ActiveAgendaSpanCount, state.CoveredThroughSequenceNo)
	}
	kindCounts := make(map[string]int)
	resolvedKindCounts := make(map[string]int)
	agendaItemCounts := make(map[string]int)
	allowedResolvedItems := map[string]struct{}{"risk-sites": {}, "question-count": {}, "question-public": {}}
	liveTabItems, resolvedTabItems := 0, 0
	for _, item := range state.Items {
		kindCounts[item.Kind]++
		if item.Status == "resolved" {
			if _, allowed := allowedResolvedItems[item.ID]; !allowed {
				t.Fatalf("unrelated item resolved: %+v", item)
			}
			resolvedKindCounts[item.Kind]++
			if resolvableItemKind(item.Kind) {
				resolvedTabItems++
			}
		} else {
			liveTabItems++
		}
		if topicID := itemTopicID(state.Tree, item.ID); topicID != "" {
			agendaItemCounts[topicID]++
		}
	}
	t.Logf("replay metrics: kinds=%v resolvedKinds=%v resolutionRequested=%d resolutionApplied=%d resolutionRejected=%d resolutionReopened=%d agendaItems=%v actionSummaryAgendaCount=1 canonicalActionSummaryNodes=0 renderedActionSummaryRows=%d liveTabItems=%d resolvedTabItems=%d maxDepth=%d coverage=%d incomplete=false",
		kindCounts, resolvedKindCounts, resolutionAudit.Requested, resolutionAudit.Applied, resolutionAudit.Rejected, resolutionAudit.Reopened, agendaItemCounts, actionRows, liveTabItems, resolvedTabItems, treeDepthOf(state.Tree), state.CoveredThroughSequenceNo)
	assertControlledTreeInvariants(t, state.Tree, 5)
}
