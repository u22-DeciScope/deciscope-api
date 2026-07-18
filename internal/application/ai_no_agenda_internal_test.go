package application

import (
	"fmt"
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

func TestAnalysisTranscriptIncludesCanonicalSequenceNumbers(t *testing.T) {
	text, _ := buildAnalysisTranscript([]domain.TranscriptSegment{{SequenceNo: 18, SpeakerName: "進行", Text: "アジェンダ外の報告です"}}, 0)
	if !strings.Contains(text, "[sequenceNo=18]") {
		t.Fatalf("transcript=%q", text)
	}
}

func TestSession3b279189c5094e88NoAgendaCandidateReplay(t *testing.T) {
	segments := session3b279189c5094e88Segments()
	if len(segments) != 25 {
		t.Fatalf("segments=%d", len(segments))
	}
	mc := &meetingContext{Title: "沿岸部風力発電計画に関する環境アセスメント検討会", Agenda: []agendaItem{
		{ID: "agenda-1", Title: "渡り鳥調査", Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "騒音測定", Role: agendaRolePrimary},
		{ID: "agenda-3", Title: "住民説明資料", Role: agendaRolePrimary},
	}}
	config := TreeClassificationConfig{PromotionMinItems: 2, PromotionMinRounds: 2}
	scope := liveEvidenceScope{Allowed: map[int64]struct{}{}, CurrentRound: map[int64]struct{}{}, TranscriptText: map[int64]string{}, CoveredThrough: 25}
	for index, text := range segments {
		sequenceNo := int64(index + 1)
		scope.Allowed[sequenceNo] = struct{}{}
		scope.TranscriptText[sequenceNo] = text
	}

	first := `{"summary":"湿地報告","currentTopic":"湿地・希少植物","resolvedIds":[],"items":[
		{"clientKey":"wetland-risk","kind":"risk","severity":"high","title":"湿地に希少植物が生育している可能性","body":"植物種は未確認","status":"open","evidenceSequenceNos":[18]}
	],"newTopics":[{"id":"topic-未分類-湿地情報","label":"湿地・希少植物","description":"アジェンダ外の追加調査課題"}],"assignments":[{"nodeId":"wetland-risk","parentTopicId":"agenda-1","confidence":0.9}]}`
	stats1 := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(first, nil, mc, 8, []int64{18}, scope, config, stats1)
	if err != nil {
		t.Fatal(err)
	}
	state1 := previousLiveAnalysisState(raw)
	if stats1.NoAgendaSpanCount != 1 || len(stats1.NoAgendaSpanStartSequences) != 1 || stats1.NoAgendaSpanStartSequences[0] != 18 {
		t.Fatalf("no agenda stats=%+v transitions=%+v", stats1, stats1.AgendaTransitions)
	}
	if len(state1.EmergingTopics) != 1 || len(state1.EmergingTopics[0].EvidenceItemIDs) != 1 {
		t.Fatalf("first candidates=%+v", state1.EmergingTopics)
	}
	serverCandidateID := state1.EmergingTopics[0].ID
	if !strings.HasPrefix(serverCandidateID, "candidate-") {
		t.Fatalf("candidate id must be server-owned: %s", serverCandidateID)
	}
	risk := findItemByTitlePart(state1.Items, "希少植物が生育")
	if risk == nil || risk.CandidateTopicID != serverCandidateID || itemTopicID(state1.Tree, risk.ID) != treeUnclassifiedTopicID {
		t.Fatalf("risk=%+v topic=%s", risk, itemTopicID(state1.Tree, risk.ID))
	}

	second := `{"summary":"植物調査","currentTopic":"湿地・希少植物","resolvedIds":[],"items":[
		{"clientKey":"plant-question","kind":"question","severity":"high","title":"植物の種類が未確認","body":"専門家による確認が必要","status":"open","evidenceSequenceNos":[19]},
		{"clientKey":"plant-todo","kind":"todo","severity":"medium","title":"専門家による予備調査を検討","body":"次回会議で実施を判断する","status":"open","evidenceSequenceNos":[20]}
	],"newTopics":[{"id":"topic-plant-survey","label":"希少植物の予備調査","description":"湿地の植物種を確認"}],"assignments":[
		{"nodeId":"plant-question","parentTopicId":"agenda-1","confidence":0.8},
		{"nodeId":"plant-todo","parentTopicId":"agenda-1","confidence":0.8}
	]}`
	stats2 := &liveAnalysisTreeMergeStats{}
	raw, err = parseAndMergeLiveAnalysisPayloadWithEvidence(second, raw, mc, 9, []int64{19, 20}, scope, config, stats2)
	if err != nil {
		t.Fatal(err)
	}
	state2 := previousLiveAnalysisState(raw)
	if stats2.DynamicTopicsPromoted != 1 || stats2.PromotedItemsReparented < 3 || stats2.CandidateIDsMerged == 0 {
		t.Fatalf("promotion stats=%+v candidates=%+v", stats2, state2.EmergingTopics)
	}
	if stats2.CrossKindCandidateInherited < 2 || stats2.CompanionCandidateInherited < 2 {
		t.Fatalf("cross-kind inheritance stats=%+v", stats2)
	}
	dynamicTopicID := ""
	for _, node := range state2.Tree.Nodes {
		if node.Kind == "topic" && node.Origin == topicOriginDynamic && strings.Contains(node.Label, "湿地") {
			dynamicTopicID = node.ID
		}
	}
	if dynamicTopicID == "" || dynamicTopicID != serverCandidateID {
		t.Fatalf("dynamicTopicID=%s candidateID=%s nodes=%+v", dynamicTopicID, serverCandidateID, state2.Tree.Nodes)
	}
	for _, titlePart := range []string{"希少植物が生育", "植物の種類が未確認", "専門家による予備調査"} {
		item := findItemByTitlePart(state2.Items, titlePart)
		if item == nil || itemTopicID(state2.Tree, item.ID) != dynamicTopicID || item.CandidateTopicID != "" || item.ClassificationStatus != classificationAssigned {
			t.Fatalf("promoted item %q=%+v topic=%s", titlePart, item, itemTopicID(state2.Tree, item.ID))
		}
	}
	if stats2.FixedAgendaAssignmentRejectedByNoAgendaSpan < 2 || stats2.StaleAgendaFallbackRejected < 2 {
		t.Fatalf("stale agenda rejection stats=%+v", stats2)
	}
	if diagnostics := validateTreeIntegrity(state2.Tree, state2.Items, mc); !diagnostics.Valid || len(state2.Tree.Edges) != len(state2.Tree.Nodes)-1 {
		t.Fatalf("integrity=%+v nodes=%d edges=%d", diagnostics, len(state2.Tree.Nodes), len(state2.Tree.Edges))
	}

	// The closing recap updates the canonical item while retaining the promoted
	// dynamic parent; it must not recreate a candidate or revive agenda-1.
	recap := fmt.Sprintf(`{"summary":"まとめ","currentTopic":"湿地・希少植物","resolvedIds":[],"items":[{"id":%q,"kind":"risk","severity":"high","title":"湿地に希少植物が生育している可能性","body":"アジェンダ外の新しい論点として継続","status":"updated","evidenceSequenceNos":[25]}],"newTopics":[],"assignments":[{"nodeId":%q,"parentTopicId":"agenda-1","confidence":0.8}]}`, risk.ID, risk.ID)
	stats3 := &liveAnalysisTreeMergeStats{}
	raw, err = parseAndMergeLiveAnalysisPayloadWithEvidence(recap, raw, mc, 10, []int64{25}, scope, config, stats3)
	if err != nil {
		t.Fatal(err)
	}
	state3 := previousLiveAnalysisState(raw)
	if len(state3.EmergingTopics) != 0 || itemTopicID(state3.Tree, risk.ID) != dynamicTopicID {
		t.Fatalf("recap candidates=%+v topic=%s", state3.EmergingTopics, itemTopicID(state3.Tree, risk.ID))
	}
	t.Logf("session_3b279 replay noAgendaSpanCount=%d staleAgendaFallbackRejected=%d candidateIdsMerged=%d candidateEvidenceItems=3 dynamicTopicsPromoted=%d promotedItemsRemainingInFixedAgenda=0 promotedItemsRemainingInUnclassified=0 nodes=%d edges=%d duplicateNodeIds=0 selfParent=0 fixedAgendaPresent=3 coverage=25 incomplete=false", stats2.NoAgendaSpanCount, stats2.StaleAgendaFallbackRejected, stats2.CandidateIDsMerged, stats2.DynamicTopicsPromoted, len(state3.Tree.Nodes), len(state3.Tree.Edges))
}

func session3b279189c5094e88Segments() []string {
	return []string{
		"会議を始めます。", "まず、渡り鳥調査について確認します。", "渡り鳥が通過します。", "観測地点が不足しています。", "北側と南側も追加できます。",
		"三方向から観測します。", "追加地点で対応できます。", "この問題は解決済みです。", "三地点で調査すると決定します。", "続いて、騒音測定について確認します。",
		"昼間一回と夜間二回測定します。", "強風日の基準を検討します。", "基準は未決定です。", "次に、住民説明資料について確認します。", "公開方法が未指定です。",
		"Web公開し図を付けます。", "開催日は未確定です。", "アジェンダにはありませんでしたが、湿地に希少植物が生育している可能性があります。", "植物の種類は未確認で、新しい調査課題です。", "専門家による予備調査を次回検討します。",
		"ここまでをまとめます。", "渡り鳥の問題は解決しました。", "決定事項を確認します。", "未解決事項はありますか。", "湿地の希少植物はアジェンダ外から生まれた新しい論点として次回以降も検討します。",
	}
}
