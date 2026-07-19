package application

import (
	"encoding/json"
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

// This fixture is a compact, offline reproduction of the persisted defects
// observed in session_85dcefa3f7f785d7. It contains the original 31-node
// topology and the evidence sequences needed by deterministic repair; no
// provider or database access is performed by the replay.
func TestSession85dcefa3f7f785d7OfflineQualityReplay(t *testing.T) {
	mc := &meetingContext{Title: "環境アセスメント検討会", Agenda: []agendaItem{
		{ID: "agenda-1", Title: "渡り鳥調査計画", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "騒音測定実施方法", Order: 2, Role: agendaRolePrimary},
		{ID: "agenda-3", Title: "住民説明資料作成", Order: 3, Role: agendaRolePrimary},
		{ID: "agenda-4", Title: "今後の対応事項", Order: 4, Role: agendaRoleActionSummary},
	}}
	items := []liveAnalysisItem{
		{ID: "decision-auto-low", Kind: "decision", Severity: "high", Title: "実施", Body: "ええ。実施することを決定します。", Status: "open", EvidenceSequenceNos: []int64{11}},
		{ID: "todo-bird-sites", Kind: "todo", Severity: "medium", Title: "渡り鳥を三地点で調査", Body: "海岸側、北側、南側", Status: "open", EvidenceSequenceNos: []int64{10}},
		{ID: "wind-question-1", Kind: "question", Severity: "high", Title: "強風日の基準風速は何m/sか", Body: "基準が未決定", Status: "open", EvidenceSequenceNos: []int64{18}},
		{ID: "wind-open-1", Kind: "open_issue", Severity: "high", Title: "強風日の測定条件が未決定", Body: "どの風速を基準にするか", Status: "open", EvidenceSequenceNos: []int64{18}},
		{ID: "wind-todo-1", Kind: "todo", Severity: "medium", Title: "強風日の測定条件を検討", Body: "風速基準を決める", Status: "open", EvidenceSequenceNos: []int64{18}},
		{ID: "wind-open-2", Kind: "open_issue", Severity: "high", Title: "強風日の風速基準が未解決", Body: "気象データを確認してから判断", Status: "open", EvidenceSequenceNos: []int64{19, 32}},
		{ID: "wind-question-2", Kind: "question", Severity: "high", Title: "強風日の風速基準", Body: "未解決の課題", Status: "open", EvidenceSequenceNos: []int64{32}},
		{ID: "wind-todo-2", Kind: "todo", Severity: "medium", Title: "気象データを確認", Body: "強風日の基準風速を判断", Status: "open", EvidenceSequenceNos: []int64{19}},
		{ID: "recap-decision-bird", Kind: "decision", Severity: "high", Title: "渡り鳥を三地点で調査", Body: "決定事項のまとめ", Status: "open", EvidenceSequenceNos: []int64{31}},
		{ID: "recap-decision-noise", Kind: "decision", Severity: "high", Title: "騒音を昼一回・夜二回測定", Body: "決定事項のまとめ", Status: "open", EvidenceSequenceNos: []int64{31}},
		{ID: "recap-decision-web", Kind: "decision", Severity: "high", Title: "調査結果をWeb公開", Body: "決定事項のまとめ", Status: "open", EvidenceSequenceNos: []int64{31}},
		{ID: "plant-question", Kind: "question", Severity: "high", Title: "湿地の植物の種類が未確認", Body: "希少植物か確認できていない", Status: "open", EvidenceSequenceNos: []int64{29}},
		{ID: "plant-todo-1", Kind: "todo", Severity: "medium", Title: "専門家による予備調査を検討", Body: "湿地の植物種類を確認", Status: "open", EvidenceSequenceNos: []int64{28, 29}},
		{ID: "plant-todo-2", Kind: "todo", Severity: "medium", Title: "希少植物調査を次回も検討", Body: "湿地周辺の新しい論点", Status: "open", EvidenceSequenceNos: []int64{33}},
		{ID: "date-open", Kind: "open_issue", Severity: "high", Title: "住民説明会の開催日が未確定", Body: "自治会と調整", Status: "open", EvidenceSequenceNos: []int64{20}},
		{ID: "date-todo", Kind: "todo", Severity: "medium", Title: "住民説明会の候補日を受領", Body: "開催日を確定", Status: "open", EvidenceSequenceNos: []int64{20, 32}},
		{ID: "noise-decision", Kind: "decision", Severity: "high", Title: "騒音を昼一回・夜二回測定", Body: "合計三回実施", Status: "open", EvidenceSequenceNos: []int64{16}},
		{ID: "web-decision", Kind: "decision", Severity: "high", Title: "調査結果をWeb公開", Body: "住民が後から確認可能", Status: "open", EvidenceSequenceNos: []int64{23}},
		{ID: "bird-risk", Kind: "risk", Severity: "high", Title: "渡り鳥の観測地点不足", Body: "飛行経路を確認できない", Status: "resolved", EvidenceSequenceNos: []int64{7}},
		{ID: "bird-fact", Kind: "fact", Severity: "medium", Title: "三地点から観測可能", Body: "海岸側、北側、南側", Status: "open", EvidenceSequenceNos: []int64{9}},
		{ID: "noise-fact", Kind: "fact", Severity: "medium", Title: "夜間の低周波音への懸念", Body: "住民意見", Status: "open", EvidenceSequenceNos: []int64{14}},
		{ID: "web-todo", Kind: "todo", Severity: "medium", Title: "公開資料に図を付ける", Body: "簡単な説明も付記", Status: "open", EvidenceSequenceNos: []int64{24}},
		{ID: "recap-control", Kind: "fact", Severity: "low", Title: "以上をまとめます", Body: "以上をまとめます", Status: "open", EvidenceSequenceNos: []int64{30}},
	}
	containers := []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: mc.Title, Origin: topicOriginSystem},
		{ID: "agenda-1", Kind: "topic", ParentID: treeRootNodeID, Label: mc.Agenda[0].Title, Origin: topicOriginAgenda},
		{ID: "agenda-2", Kind: "topic", ParentID: treeRootNodeID, Label: mc.Agenda[1].Title, Origin: topicOriginAgenda},
		{ID: "agenda-3", Kind: "topic", ParentID: treeRootNodeID, Label: mc.Agenda[2].Title, Origin: topicOriginAgenda},
		{ID: "agenda-4", Kind: "topic", ParentID: treeRootNodeID, Label: mc.Agenda[3].Title, Origin: topicOriginAgenda, AgendaRole: agendaRoleActionSummary},
		{ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: "追加論点", Origin: topicOriginSystem},
		{ID: "candidate-be582d17ab85", Kind: "topic", ParentID: treeRootNodeID, Label: "観測地点の配置拡大", Origin: topicOriginDynamic},
		{ID: "group-wind", Kind: "group", ParentID: "agenda-2", Label: "強風日の測定条件"},
	}
	parents := map[string]string{
		"decision-auto-low": "agenda-1", "todo-bird-sites": "agenda-1",
		"wind-question-1": "group-wind", "wind-open-1": "group-wind", "wind-todo-1": "group-wind",
		"wind-open-2": treeUnclassifiedTopicID, "wind-question-2": treeUnclassifiedTopicID, "wind-todo-2": treeUnclassifiedTopicID,
		"recap-decision-bird": "candidate-be582d17ab85", "recap-decision-noise": "candidate-be582d17ab85", "recap-decision-web": "candidate-be582d17ab85",
		"plant-question": treeUnclassifiedTopicID, "plant-todo-1": treeUnclassifiedTopicID, "plant-todo-2": treeUnclassifiedTopicID,
		"date-open": "agenda-3", "date-todo": treeUnclassifiedTopicID, "noise-decision": "agenda-2", "web-decision": "agenda-3",
		"bird-risk": "agenda-1", "bird-fact": "agenda-1", "noise-fact": "agenda-2", "web-todo": "agenda-3", "recap-control": treeUnclassifiedTopicID,
	}
	tree := &liveAnalysisTree{Nodes: append([]liveAnalysisTreeNode(nil), containers...)}
	for i := range items {
		items[i].ClassificationStatus = classificationAssigned
		if parents[items[i].ID] == treeUnclassifiedTopicID {
			items[i].ClassificationStatus = classificationTentative
			items[i].CandidateTopicID = "candidate-ec47fdba9f29"
		}
		tree.Nodes = append(tree.Nodes, liveAnalysisTreeNode{ID: items[i].ID, Kind: liveAnalysisTreeNodeKindForItem(items[i].Kind), ParentID: parents[items[i].ID], Label: items[i].Title, Status: items[i].Status})
	}
	rebuildTreeAuditEdges(tree)
	state := liveAnalysisPayload{Summary: "persisted target defect", Items: items, Tree: tree, TreeVersion: 13, CoveredThroughSequenceNo: 33,
		EmergingTopics: []emergingTopicCandidate{{ID: "candidate-ec47fdba9f29", Label: "現地環境の新情報", Description: "アジェンダ外の新情報", EvidenceItemIDs: []string{"wind-open-2", "wind-question-2", "wind-todo-2", "plant-question", "plant-todo-1", "plant-todo-2", "date-todo"}, FirstRound: 10, LastRound: 13, RoundCount: 3}},
	}
	if got := len(state.Tree.Nodes); got != 31 {
		t.Fatalf("pre-replay nodes=%d, want 31", got)
	}
	previous, _ := json.Marshal(state)
	texts := map[int64]string{
		10: "渡り鳥の調査については、海岸側、北側、南側の合計三地点で。", 11: "ええ。実施することを決定します。",
		18: "強風日の測定条件は、どの風速を基準にするか決まっていません。", 19: "気象データを確認してから判断します。",
		28: "湿地周辺の希少植物について専門家の予備調査を検討します。", 29: "植物の種類は未確認です。", 30: "以上をまとめます。",
		31: "決定事項は渡り鳥、騒音、Web公開です。", 32: "未解決は強風日の風速基準と説明会日程です。", 33: "希少植物は次回以降も検討します。",
	}
	scope := liveEvidenceScope{Allowed: map[int64]struct{}{}, CurrentRound: map[int64]struct{}{}, TranscriptText: texts, Segments: map[int64]domain.TranscriptSegment{}, CoveredThrough: 33}
	for sequenceNo, text := range texts {
		scope.Allowed[sequenceNo] = struct{}{}
		scope.Segments[sequenceNo] = domain.TranscriptSegment{SequenceNo: sequenceNo, SpeakerID: "speaker-1", Text: text, IsFinal: true}
	}
	diff := `{"summary":"offline repaired","currentTopic":"","resolvedIds":[],"resolutionUpdates":[],"items":[],"newTopics":[],"assignments":[]}`
	decisionCandidates := detectDecisionCandidates([]domain.TranscriptSegment{scope.Segments[10], scope.Segments[11]})
	if len(decisionCandidates) != 1 || len(decisionCandidates[0].SourceSequenceNos) != 2 {
		t.Fatalf("logical decision candidates=%+v", decisionCandidates)
	}
	diff, _, err := reconcileDecisionCandidates(diff, previous, decisionCandidates)
	if err != nil {
		t.Fatal(err)
	}
	stats := &liveAnalysisTreeMergeStats{}
	replayed, err := parseAndMergeLiveAnalysisPayloadWithEvidence(diff, previous, mc, 14, []int64{10, 11}, scope, TreeClassificationConfig{PromotionMinItems: 2, PromotionMinRounds: 2}, stats)
	if err != nil {
		t.Fatal(err)
	}
	after := previousLiveAnalysisState(replayed)
	if len(after.Tree.Nodes) >= 31 {
		t.Fatalf("post-replay nodes=%d, want fewer than 31", len(after.Tree.Nodes))
	}
	if diagnostics := validateTreeIntegrity(after.Tree, after.Items, mc); !diagnostics.Valid {
		t.Fatalf("post-replay integrity=%+v", diagnostics)
	}
	for _, item := range after.Items {
		if lowInformationDecisionItem(item) || evidenceIsReferenceOnly(item.EvidenceSequenceNos, classifyDiscourseTimeline(scope)) && strings.HasPrefix(item.ID, "recap-") {
			t.Fatalf("unrepaired item=%+v", item)
		}
	}
	strongWindItems := 0
	strongWindSubtypes := map[string]int{}
	strongWindTodos := 0
	for _, item := range after.Items {
		if strings.Contains(item.Title+item.Body, "強風") {
			strongWindItems++
			strongWindSubtypes[item.Subtype]++
			if item.Kind == "todo" {
				strongWindTodos++
			}
		}
	}
	if strongWindItems != 3 || strongWindSubtypes[issueSubtypeQuestion] != 1 || strongWindSubtypes[issueSubtypeDiscussion] != 1 || strongWindTodos != 1 {
		t.Fatalf("strong-wind canonical items=%d items=%+v", strongWindItems, after.Items)
	}
	plantTopicID := ""
	for _, node := range after.Tree.Nodes {
		if node.Kind == "topic" && node.Origin == topicOriginDynamic && strings.Contains(node.Label, "植物") {
			plantTopicID = node.ID
		}
	}
	if plantTopicID == "" {
		t.Fatalf("plant dynamic topic missing: nodes=%+v candidates=%+v stats=%+v", after.Tree.Nodes, after.EmergingTopics, stats)
	}
	for _, candidate := range after.EmergingTopics {
		if candidate.ID == "candidate-ec47fdba9f29" {
			t.Fatalf("mixed candidate survived: %+v", candidate)
		}
	}
	decisionCount, plantItemCount, recapDerivedItems := 0, 0, 0
	for _, item := range after.Items {
		if item.Kind == "decision" {
			decisionCount++
		}
		if itemTopicID(after.Tree, item.ID) == plantTopicID {
			plantItemCount++
		}
		if evidenceIsReferenceOnly(item.EvidenceSequenceNos, classifyDiscourseTimeline(scope)) {
			recapDerivedItems++
		}
	}
	segments := make([]domain.TranscriptSegment, 0, len(scope.Segments))
	for _, segment := range scope.Segments {
		segments = append(segments, segment)
	}
	findings := deterministicTreeAuditPrecheck(after, mc, classifyTreeAuditEvidence(after, segments), TreeAuditConfig{})
	criticalFindings := 0
	for _, finding := range findings {
		switch finding.Type {
		case TreeAuditLowInformationDecision, TreeAuditSemanticDuplicateSibling, TreeAuditDuplicateCrossKindProposition, TreeAuditMissingRequiredTopic, TreeAuditRecapReferenceContamination, TreeAuditDiscourseOnlyItem:
			criticalFindings++
		}
	}
	if decisionCount != 3 || plantItemCount < 1 || plantItemCount > 2 || recapDerivedItems != 0 || criticalFindings != 0 {
		t.Fatalf("metrics decisions=%d plantItems=%d recapDerived=%d criticalFindings=%d findings=%+v", decisionCount, plantItemCount, recapDerivedItems, criticalFindings, findings)
	}
	t.Logf("session_85dcefa3f7f785d7 replay: nodes_before=31 nodes_after=%d items_before=23 items_after=%d decisions=%d strong_wind_items=%d plant_topic=%s plant_items=%d recap_derived_items=%d critical_audit_findings=%d", len(after.Tree.Nodes), len(after.Items), decisionCount, strongWindItems, plantTopicID, plantItemCount, recapDerivedItems, criticalFindings)
}
