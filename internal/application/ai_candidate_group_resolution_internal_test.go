package application

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

func TestCandidateCreationIsAtomicWithCanonicalEvidence(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "既存議題", Role: agendaRolePrimary}}}
	content := `{"summary":"","currentTopic":"湿地","resolvedIds":[],"resolutionUpdates":[],"items":[{"clientKey":"wetland-issue","kind":"issue","severity":"high","title":"湿地に希少植物が存在する可能性","body":"種類は未確認","status":"open","evidenceSequenceNos":[1]}],"newTopics":[{"id":"topic-wetland","label":"湿地・希少植物","description":"アジェンダ外の調査課題"}],"assignments":[{"nodeId":"wetland-issue","parentTopicId":"topic-wetland","confidence":0.9,"reason":"追加論点"}]}`
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayload(content, nil, mc, 1, []int64{1}, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	if len(state.Items) != 1 || !strings.HasPrefix(state.Items[0].ID, "item-issue-") {
		t.Fatalf("items=%+v", state.Items)
	}
	item := state.Items[0]
	candidateID, _ := canonicalCandidateID("湿地・希少植物", "アジェンダ外の調査課題")
	if item.ClassificationStatus != classificationTentative || item.CandidateTopicID != candidateID {
		t.Fatalf("item=%+v", item)
	}
	if len(state.EmergingTopics) != 1 || len(state.EmergingTopics[0].EvidenceItemIDs) != 1 || state.EmergingTopics[0].EvidenceItemIDs[0] != item.ID {
		t.Fatalf("candidates=%+v item=%+v", state.EmergingTopics, item)
	}
	if stats.CandidateCreated != 1 || stats.CandidateEvidenceAdded != 1 || stats.CandidateCreationRejectedNoEvidence != 0 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestCandidateWithoutCanonicalEvidenceIsRejected(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "既存議題", Role: agendaRolePrimary}}}
	content := `{"summary":"候補のみ","currentTopic":"","resolvedIds":[],"resolutionUpdates":[],"items":[],"newTopics":[{"id":"topic-empty","label":"根拠のない候補"}],"assignments":[]}`
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayload(content, nil, mc, 1, nil, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	if state := previousLiveAnalysisState(raw); len(state.EmergingTopics) != 0 {
		t.Fatalf("emergingTopics=%+v", state.EmergingTopics)
	}
	if stats.CandidateCreationRejectedNoEvidence != 1 || stats.CandidateCreated != 0 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestDeterministicGroupIncludesResolvedHistoryWithoutCrossingAgenda(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-1", Title: "渡り鳥調査", Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "騒音測定", Role: agendaRolePrimary},
	}}
	items := []liveAnalysisItem{
		{ID: "risk-sites", Kind: "risk", Severity: "high", Title: "観測地点が不足している", Body: "飛行経路を確認できない", Status: "resolved", ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{4, 7}},
		{ID: "fact-sites", Kind: "fact", Severity: "medium", Title: "北側と南側にも観測地点を設置可能", Body: "三方向から観測できる", Status: "open", ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{5}},
		{ID: "decision-sites", Kind: "decision", Severity: "high", Title: "三地点で渡り鳥を調査する", Body: "海岸、北側、南側", Status: "open", ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{9}},
		{ID: "question-noise", Kind: "question", Severity: "high", Title: "観測地点の騒音値を確認するか", Body: "騒音測定の論点", Status: "open", ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{10}},
	}
	assignments := []treeAssignment{
		{NodeID: "risk-sites", ParentTopicID: "agenda-1", Confidence: 0.9},
		{NodeID: "fact-sites", ParentTopicID: "agenda-1", Confidence: 0.9},
		{NodeID: "decision-sites", ParentTopicID: "agenda-1", Confidence: 0.9},
		{NodeID: "question-noise", ParentTopicID: "agenda-2", Confidence: 0.9},
	}
	stats := &liveAnalysisTreeMergeStats{}
	tree, merged, _ := rebuildDiscussionTree(nil, mc, items, nil, assignments, map[string]struct{}{"risk-sites": {}}, nil, 2, TreeClassificationConfig{}, stats)
	if diagnostics := validateTreeIntegrity(tree, merged, mc); !diagnostics.Valid {
		t.Fatalf("diagnostics=%+v tree=%+v", diagnostics, tree)
	}
	groups := treeNodesByKind(tree, "group")
	agenda1 := agendaTopicNodeByRef(tree, "agenda-1")
	agenda2 := agendaTopicNodeByRef(tree, "agenda-2")
	if len(groups) != 1 || agenda1 == nil || groups[0].ParentID != agenda1.ID {
		t.Fatalf("groups=%+v stats=%+v", groups, stats)
	}
	children := directTreeChildren(tree, groups[0].ID)
	if len(children) != 3 || agenda2 == nil || itemTopicID(tree, "question-noise") != agenda2.ID {
		t.Fatalf("children=%+v tree=%+v", children, tree)
	}
	if treeDepthOf(tree) != 3 || computeTreeHealth(tree).SingleChildGroupCount != 0 || stats.GroupCandidates < 1 || stats.GroupsCreated != 1 {
		t.Fatalf("health=%+v stats=%+v", computeTreeHealth(tree), stats)
	}
}

func TestGenericExplicitClosureWithoutTargetDoesNotResolveAnything(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "既存議題", Role: agendaRolePrimary}}}
	content := `{"summary":"closure","currentTopic":"","resolvedIds":[],"resolutionUpdates":[],"items":[],"newTopics":[],"assignments":[]}`
	scope := evidenceScopeFromTexts(map[int64]string{1: "この論点は解決済みとします。"}, 1)
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(content, nil, mc, 1, []int64{1}, scope, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	if state := previousLiveAnalysisState(raw); len(state.Items) != 0 {
		t.Fatalf("items=%+v", state.Items)
	}
	if stats.ExplicitClosureCandidates != 1 || stats.ClosureTargetsFound != 0 || stats.ClosureTargetsNotFound != 1 {
		t.Fatalf("stats=%+v", stats)
	}
	audit := summarizeResolutionEvaluations(stats.ResolutionDecisions)
	if audit.RequestedResolved != 1 || audit.RejectedNoTarget != 1 || audit.Applied != 0 {
		t.Fatalf("resolution audit=%+v decisions=%+v", audit, stats.ResolutionDecisions)
	}
}

func TestSession0f9e20497397cedfDeterministicReplay(t *testing.T) {
	segments := session0f9e20497397cedfSegments()
	if len(segments) != 30 {
		t.Fatalf("segments=%d", len(segments))
	}
	mc := &meetingContext{Title: "沿岸部風力発電計画に関する環境アセスメント検討会１０", Agenda: []agendaItem{
		{ID: "agenda-1", Title: "渡り鳥調査計画", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "騒音測定実施方法", Order: 2, Role: agendaRolePrimary},
		{ID: "agenda-3", Title: "住民説明資料作成", Order: 3, Role: agendaRolePrimary},
		{ID: "agenda-4", Title: "今後の対応事項", Order: 4, Role: agendaRoleActionSummary},
	}}
	config := TreeClassificationConfig{PromotionMinItems: 2, PromotionMinRounds: 2}
	var raw json.RawMessage
	var finalStats *liveAnalysisTreeMergeStats
	allStats := make([]*liveAnalysisTreeMergeStats, 0, 6)

	round1 := `{"summary":"渡り鳥","currentTopic":"渡り鳥調査","resolvedIds":[],"resolutionUpdates":[],"items":[
		{"clientKey":"bird-sites-fact","kind":"fact","severity":"medium","title":"北側と南側にも観測地点を設置できる","body":"三方向から飛行経路を観測できる","status":"open","evidenceSequenceNos":[5]},
		{"clientKey":"bird-sites-decision","kind":"decision","severity":"high","title":"渡り鳥を三地点で調査する","body":"海岸側、北側、南側で実施する","status":"open","evidenceSequenceNos":[9]}
	],"newTopics":[],"assignments":[{"nodeId":"bird-sites-fact","parentTopicId":"agenda-1","confidence":0.9},{"nodeId":"bird-sites-decision","parentTopicId":"agenda-1","confidence":0.9}]}`
	raw, finalStats = replaySessionRound(t, raw, mc, config, segments, 1, 9, 1, round1)
	allStats = append(allStats, finalStats)

	round2 := `{"summary":"騒音","currentTopic":"騒音測定","resolvedIds":[],"resolutionUpdates":[],"items":[
		{"clientKey":"noise-decision","kind":"decision","severity":"high","title":"騒音を昼間一回と夜間二回測定する","body":"合計三回実施する","status":"open","evidenceSequenceNos":[13]},
		{"clientKey":"wind-question","kind":"question","severity":"high","title":"強風日の基準風速は何m/sか","body":"測定条件を決める必要がある","status":"open","evidenceSequenceNos":[14]},
		{"clientKey":"wind-open","kind":"open_issue","severity":"high","title":"強風日の測定条件が未決定","body":"どの風速を基準にするか決まっていない","status":"open","evidenceSequenceNos":[14,15]},
		{"clientKey":"weather-todo","kind":"todo","severity":"medium","title":"過去の気象データを確認する","body":"強風日の基準風速を判断する","status":"open","evidenceSequenceNos":[15]}
	],"newTopics":[],"assignments":[{"nodeId":"noise-decision","parentTopicId":"agenda-2","confidence":0.9},{"nodeId":"wind-question","parentTopicId":"agenda-2","confidence":0.9},{"nodeId":"wind-open","parentTopicId":"agenda-2","confidence":0.9},{"nodeId":"weather-todo","parentTopicId":"agenda-2","confidence":0.9}]}`
	raw, finalStats = replaySessionRound(t, raw, mc, config, segments, 10, 15, 2, round2)
	allStats = append(allStats, finalStats)

	round3 := `{"summary":"住民資料","currentTopic":"住民説明資料","resolvedIds":[],"resolutionUpdates":[],"items":[
		{"clientKey":"web-decision","kind":"decision","severity":"high","title":"調査結果の概要をWeb公開する","body":"住民が後から確認できるようにする","status":"open","evidenceSequenceNos":[17]},
		{"clientKey":"diagram-decision","kind":"decision","severity":"medium","title":"公開資料に図と簡単な説明を付ける","body":"専門用語だけにしない","status":"open","evidenceSequenceNos":[18]},
		{"clientKey":"date-open","kind":"open_issue","severity":"high","title":"住民説明会の開催日が未確定","body":"自治会から候補日を受け取る","status":"open","evidenceSequenceNos":[19,20]},
		{"clientKey":"publication-question","kind":"question","severity":"medium","title":"調査日程をどのように公開するか","body":"現在の資料に記載がない","status":"open","evidenceSequenceNos":[16]},
		{"clientKey":"date-todo","kind":"todo","severity":"medium","title":"自治会から説明会候補日を受け取る","body":"開催日を確定する","status":"open","evidenceSequenceNos":[20]}
	],"newTopics":[],"assignments":[{"nodeId":"web-decision","parentTopicId":"agenda-3","confidence":0.9},{"nodeId":"diagram-decision","parentTopicId":"agenda-3","confidence":0.9},{"nodeId":"date-open","parentTopicId":"agenda-3","confidence":0.9},{"nodeId":"publication-question","parentTopicId":"agenda-3","confidence":0.9},{"nodeId":"date-todo","parentTopicId":"agenda-3","confidence":0.9}]}`
	raw, finalStats = replaySessionRound(t, raw, mc, config, segments, 16, 20, 3, round3)
	allStats = append(allStats, finalStats)

	round4 := `{"summary":"湿地","currentTopic":"湿地・希少植物","resolvedIds":[],"resolutionUpdates":[],"items":[{"clientKey":"wetland-risk","kind":"risk","severity":"high","title":"湿地に希少植物が存在する可能性","body":"建設予定地近くで見つかった","status":"open","evidenceSequenceNos":[23]}],"newTopics":[{"id":"topic-wetland","label":"湿地・希少植物","description":"アジェンダ外の調査課題"}],"assignments":[{"nodeId":"wetland-risk","parentTopicId":"topic-wetland","confidence":0.9}]}`
	raw, finalStats = replaySessionRound(t, raw, mc, config, segments, 21, 23, 4, round4)
	allStats = append(allStats, finalStats)
	state4 := previousLiveAnalysisState(raw)
	if len(state4.EmergingTopics) != 1 || len(state4.EmergingTopics[0].EvidenceItemIDs) != 1 {
		t.Fatalf("round4 candidates=%+v", state4.EmergingTopics)
	}

	round5 := `{"summary":"湿地調査","currentTopic":"湿地・希少植物","resolvedIds":[],"resolutionUpdates":[],"items":[
		{"clientKey":"plant-open","kind":"open_issue","severity":"high","title":"湿地の植物の種類が未確認","body":"希少植物か確認できていない","status":"open","evidenceSequenceNos":[24]},
		{"clientKey":"survey-todo","kind":"todo","severity":"medium","title":"専門家による予備調査を検討する","body":"植物の種類を確認する","status":"open","evidenceSequenceNos":[25]}
	],"newTopics":[{"id":"topic-wetland-survey","label":"湿地・希少植物調査","description":"植物種別を確認する"}],"assignments":[{"nodeId":"plant-open","parentTopicId":"topic-wetland-survey","confidence":0.9},{"nodeId":"survey-todo","parentTopicId":"agenda-1","confidence":0.4}]}`
	raw, finalStats = replaySessionRound(t, raw, mc, config, segments, 24, 25, 5, round5)
	allStats = append(allStats, finalStats)
	state5 := previousLiveAnalysisState(raw)
	if state5.TreeChanges == nil || len(state5.TreeChanges.ReparentedNodeIDs) < 2 || finalStats.PromotedItemsReparented < 2 {
		t.Fatalf("promotion changes=%+v stats=%+v", state5.TreeChanges, finalStats)
	}
	wind := findItemByTitlePart(state5.Items, "強風日の測定条件")
	date := findItemByTitlePart(state5.Items, "住民説明会の開催日")
	if wind == nil || date == nil {
		t.Fatalf("wind=%+v date=%+v", wind, date)
	}

	round6 := fmt.Sprintf(`{"summary":"まとめ","currentTopic":"まとめ","resolvedIds":[],"resolutionUpdates":[{"itemId":%q,"status":"open","evidenceSequenceNos":[28],"reason":"未解決の再確認"},{"itemId":%q,"status":"open","evidenceSequenceNos":[28],"reason":"未解決の再確認"}],"items":[],"newTopics":[],"assignments":[]}`, wind.ID, date.ID)
	raw, finalStats = replaySessionRound(t, raw, mc, config, segments, 26, 30, 6, round6)
	allStats = append(allStats, finalStats)
	state := previousLiveAnalysisState(raw)

	if diagnostics := validateTreeIntegrity(state.Tree, state.Items, mc); !diagnostics.Valid {
		t.Fatalf("diagnostics=%+v tree=%+v", diagnostics, state.Tree)
	}
	if len(state.Tree.Edges) != len(state.Tree.Nodes)-1 {
		t.Fatalf("nodes=%d edges=%d", len(state.Tree.Nodes), len(state.Tree.Edges))
	}
	incomplete := state.CoveredThroughSequenceNo < int64(len(segments))
	if state.CoveredThroughSequenceNo != 30 || len(state.AnalyzedFinalSegments) != 30 || incomplete {
		t.Fatalf("coverage=%d analyzed=%d incomplete=%t", state.CoveredThroughSequenceNo, len(state.AnalyzedFinalSegments), incomplete)
	}
	if len(state.EmergingTopics) != 0 {
		t.Fatalf("remaining candidates=%+v", state.EmergingTopics)
	}
	dynamicTopicID := ""
	for _, node := range state.Tree.Nodes {
		if node.Kind == "topic" && node.Origin == topicOriginDynamic && strings.Contains(node.Label, "湿地") {
			dynamicTopicID = node.ID
		}
	}
	if dynamicTopicID == "" {
		t.Fatalf("dynamic wetland topic missing: %+v", state.Tree.Nodes)
	}
	for _, titlePart := range []string{"希少植物が存在", "植物の種類が未確認", "専門家による予備調査"} {
		item := findItemByTitlePart(state.Items, titlePart)
		if item == nil || itemTopicID(state.Tree, item.ID) != dynamicTopicID || item.ClassificationStatus != classificationAssigned || item.CandidateTopicID != "" {
			t.Fatalf("wetland item %q=%+v topic=%s", titlePart, item, itemTopicID(state.Tree, item.ID))
		}
	}
	birdIssue := findItemByTitlePart(state.Items, "観測地点が不足")
	birdTopic := agendaTopicNodeByRef(state.Tree, "agenda-1")
	if birdIssue == nil || birdTopic == nil || birdIssue.Kind != "issue" || birdIssue.Status != "resolved" || itemTopicID(state.Tree, birdIssue.ID) != birdTopic.ID {
		t.Fatalf("bird issue=%+v topic=%s", birdIssue, itemTopicID(state.Tree, birdIssue.ID))
	}
	if !containsSequence(birdIssue.ResolutionEvidenceSequenceNos, 7) || !containsSequence(birdIssue.ResolutionEvidenceSequenceNos, 27) {
		t.Fatalf("resolution evidence=%v", birdIssue.ResolutionEvidenceSequenceNos)
	}
	for _, titlePart := range []string{"強風日の測定条件", "住民説明会の開催日", "専門家による予備調査"} {
		item := findItemByTitlePart(state.Items, titlePart)
		if item == nil || item.Status == "resolved" {
			t.Fatalf("must remain open %q=%+v", titlePart, item)
		}
	}
	health := computeTreeHealth(state.Tree)
	if health.GroupCount < 1 || health.SingleChildGroupCount != 0 || health.MaxChildren < 1 || treeDepthOf(state.Tree) < 3 {
		t.Fatalf("health=%+v tree=%+v", health, state.Tree)
	}
	kinds, resolvedKinds := countReplayItemKinds(state.Items)
	explicitClosures, requestedResolved, appliedResolved, appliedOpen, appliedNoop, rejected := 0, 0, 0, 0, 0, 0
	groupCandidates, groupsCreated, promotedEvidence := 0, 0, 0
	for _, stats := range allStats {
		explicitClosures += stats.ExplicitClosureCandidates
		audit := summarizeResolutionEvaluations(stats.ResolutionDecisions)
		requestedResolved += audit.RequestedResolved
		appliedResolved += audit.AppliedResolved
		appliedOpen += audit.AppliedOpen
		appliedNoop += audit.AppliedNoop
		rejected += audit.Rejected
		groupCandidates += stats.GroupCandidates
		groupsCreated += stats.GroupsCreated
		for _, decision := range stats.EmergingDecisions {
			if decision.Decision == emergingPromoted && decision.EvidenceItemCount > promotedEvidence {
				promotedEvidence = decision.EvidenceItemCount
			}
		}
	}
	t.Logf("session_0f9 replay canonicalItems=%d trueUnclassified=%d tentative=%d candidates=%d zeroEvidenceCandidates=0 candidateEvidenceItems=%d dynamicTopics=%d groups=%d nestedGroups=%d singleChildGroups=%d maxDepth=%d maxChildren=%d question=%d resolvedQuestion=%d openIssue=%d resolvedOpenIssue=%d risk=%d resolvedRisk=%d todo=%d completedTodo=%d explicitClosureCandidates=%d appliedResolved=%d duplicateIds=0 selfParent=0 fixed=3 coverage=%d incomplete=%t",
		len(state.Items), countClassification(state.Items, classificationUnclassified), countClassification(state.Items, classificationTentative), len(state.EmergingTopics), promotedEvidence, countDynamicTopics(state.Tree), health.GroupCount, health.NestedGroupCount, health.SingleChildGroupCount, treeDepthOf(state.Tree), health.MaxChildren,
		kinds["question"], resolvedKinds["question"], kinds["open_issue"], resolvedKinds["open_issue"], kinds["risk"], resolvedKinds["risk"], kinds["todo"], resolvedKinds["todo"], explicitClosures, appliedResolved, state.CoveredThroughSequenceNo, incomplete)
	t.Logf("session_0f9 replay candidatePromotionEvidence=%d groupCandidates=%d groupsCreated=%d resolutionAppliedOpen=%d resolutionAppliedNoop=%d resolutionRejected=%d", promotedEvidence, groupCandidates, groupsCreated, appliedOpen, appliedNoop, rejected)
	t.Logf("session_0f9 replay resolutionRequestedResolved=%d", requestedResolved)
	for _, group := range treeNodesByKind(state.Tree, "group") {
		children := directTreeChildren(state.Tree, group.ID)
		childDetails := make([]string, 0, len(children))
		for _, child := range children {
			item := findItemByID(state.Items, child.ID)
			if item == nil {
				childDetails = append(childDetails, fmt.Sprintf("%s[%s]", child.Label, child.Kind))
				continue
			}
			childDetails = append(childDetails, fmt.Sprintf("%s[%s,evidence=%v,status=%s]", item.Title, item.Kind, item.EvidenceSequenceNos, item.Status))
		}
		t.Logf("session_0f9 group label=%q parent=%s children=%v detailDepth=3", group.Label, group.ParentID, childDetails)
	}
}

func replaySessionRound(t *testing.T, previous json.RawMessage, mc *meetingContext, config TreeClassificationConfig, segments []string, start, end int, version int64, content string) (json.RawMessage, *liveAnalysisTreeMergeStats) {
	t.Helper()
	scope := liveEvidenceScope{Allowed: map[int64]struct{}{}, CurrentRound: map[int64]struct{}{}, TranscriptText: map[int64]string{}, CoveredThrough: int64(end)}
	roundSeqNos := make([]int64, 0, end-start+1)
	for index := 1; index <= end; index++ {
		sequenceNo := int64(index)
		scope.Allowed[sequenceNo] = struct{}{}
		scope.TranscriptText[sequenceNo] = segments[index-1]
		if index >= start {
			scope.CurrentRound[sequenceNo] = struct{}{}
			roundSeqNos = append(roundSeqNos, sequenceNo)
		}
	}
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(content, previous, mc, version, roundSeqNos, scope, config, stats)
	if err != nil {
		t.Fatal(err)
	}
	roundSegments := make([]domain.TranscriptSegment, 0, len(roundSeqNos))
	for _, sequenceNo := range roundSeqNos {
		roundSegments = append(roundSegments, domain.TranscriptSegment{CallID: "fixture-call", SequenceNo: sequenceNo, Text: segments[sequenceNo-1], IsFinal: true})
	}
	covered, err := addLiveAnalysisCoverage(raw, roundSegments)
	if err != nil {
		t.Fatal(err)
	}
	return covered, stats
}

func treeNodesByKind(tree *liveAnalysisTree, kind string) []liveAnalysisTreeNode {
	if tree == nil {
		return nil
	}
	result := make([]liveAnalysisTreeNode, 0)
	for _, node := range tree.Nodes {
		if node.Kind == kind {
			result = append(result, node)
		}
	}
	return result
}

func directTreeChildren(tree *liveAnalysisTree, parentID string) []liveAnalysisTreeNode {
	if tree == nil {
		return nil
	}
	result := make([]liveAnalysisTreeNode, 0)
	for _, node := range tree.Nodes {
		if node.ParentID == parentID {
			result = append(result, node)
		}
	}
	return result
}

func findItemByTitlePart(items []liveAnalysisItem, part string) *liveAnalysisItem {
	for i := range items {
		if strings.Contains(items[i].Title, part) {
			return &items[i]
		}
	}
	return nil
}

func countClassification(items []liveAnalysisItem, status string) int {
	count := 0
	for _, item := range items {
		if item.ClassificationStatus == status {
			count++
		}
	}
	return count
}

func countDynamicTopics(tree *liveAnalysisTree) int {
	count := 0
	if tree == nil {
		return count
	}
	for _, node := range tree.Nodes {
		if node.Kind == "topic" && node.Origin == topicOriginDynamic {
			count++
		}
	}
	return count
}

func countReplayItemKinds(items []liveAnalysisItem) (map[string]int, map[string]int) {
	kinds, resolved := map[string]int{}, map[string]int{}
	for _, item := range items {
		kinds[item.Kind]++
		if item.Status == "resolved" {
			resolved[item.Kind]++
		}
	}
	return kinds, resolved
}

func containsSequence(values []int64, target int64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func session0f9e20497397cedfSegments() []string {
	return []string{
		"それでは、沿岸部風力発電計画に関する環境アセスメント検討会を始めます。",
		"まず、綿木鶏の調査計画について確認します。",
		"事前調査では、風力発電設備の建設予定地付近を春と秋に複数の渡り鳥が通過する可能性があるとされています。",
		"現在の計画では海岸側の観測地点が一カ所しかなく、鳥の移動経路を十分に確認できていないのではないかという懸念が出ていました。",
		"これについて、現地担当者から海岸側に加えて、予定地の北側と南側にも観測地点を設置できるという回答がありました。",
		"3方向から観測すれば、主な飛行経路と飛行行動を確認できる見込みです。",
		"したがって、観測地点が不足しているという問題は、追加地点を設けることで対応できると判断します。",
		"この論点は解決済みとします。",
		"渡り鳥の調査については、会館側北側、南側の合計三地点で実施することを決定します。",
		"次に、騒音測定の実施方法についてです。",
		"周辺住民からは昼間よりも夜間の低周波音を心配する声が出ています。当初の計画では昼間のみ2回測定する予定でしたが、それでは住民の懸念に十分対応できません。",
		"そこで、ええ、昼間に1回、夜間に2回測定する案を採用したいと思います。夜間の測定は、風邪が比較的弱い人、強い日に1回ずつ実施します。",
		"騒音測定は広間1回、夜間2回の合計3回実施することを決定事項とします。",
		"ただし、強風日の測定条件については、どの風速を基準にするか決まっていません。",
		"そこの点は気象データを確認してから判断するため、現時点では未解決の課題として残します。",
		"続いて、住民説明資料についてです。現在の資料には設備の位置と調査日程は記載されていますが、調査日程をどのように公開するかが書かれていません。",
		"住民が後から確認できるよう、調査結果の概要を団体のウェブサイトで公開するようようにします。",
		"公開する資料には、専門用語だけでなく、図や簡単な説明をつけることも決定します。",
		"一方説明会そのものの開催日は、市域の自治会と調整できていません。",
		"開催日はまだ決定せず、自治会から候補日を受け取った後に改めて確定します。",
		"最後に。アジェンダにはありませんでしたが。",
		"現地担当者から新しい報告があります。",
		"建設予定地の近くに小規模な湿地が見つかり、希少な植物が生育している可能性があるとのことです。",
		"現時点では植物の種類が確認できていないため、既存の鳥類調査やええ騒音問題の中にええ無理に含めず、新しい調査課題として扱う必要がありそうです。",
		"植物の種類を確認するため、専門家による予備調査を実施するかどうかを次回の会議で検討します。",
		"以上をまとめます。",
		"渡り鳥の観測地点不足については、三地点で調査することで解決済みです。決定事項は、渡り鳥を三地点で調査すること、騒音を広間1回と夜間2回測定すること、そして調査結果を頭突きでウェブ公開することです。",
		"未解決の課題は強風日の風速基準と住民説明会の開催日です。",
		"また、設置の。",
		"希少植物については、アジェンダ街から生まれた新しい論点として、次回以降も検討をします。",
	}
}
