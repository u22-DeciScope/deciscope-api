package application

import (
	"encoding/json"
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

func finalSegment(sequenceNo int64, text string) domain.TranscriptSegment {
	return domain.TranscriptSegment{SequenceNo: sequenceNo, Text: text, IsFinal: true}
}

func TestDetectDecisionCandidatesAcceptsExplicitAndRejectsUndecided(t *testing.T) {
	segments := []domain.TranscriptSegment{
		finalSegment(1, "海岸側・北側・南側の三地点で調査することを決定します。"),
		finalSegment(2, "説明会の日程はまだ決定していません。"),
		finalSegment(3, "次回決定するか検討します。"),
		finalSegment(4, "調査結果を図付きでウェブ公開する方針にします。"),
	}
	candidates := detectDecisionCandidates(segments)
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v, want two explicit decisions", candidates)
	}
	if candidates[0].SequenceNo != 1 || candidates[1].SequenceNo != 4 {
		t.Fatalf("candidate sequences = [%d %d], want [1 4]", candidates[0].SequenceNo, candidates[1].SequenceNo)
	}
}

func TestDecisionCandidateJoinsAdjacentSameSpeakerFragments(t *testing.T) {
	segments := []domain.TranscriptSegment{
		{SequenceNo: 10, SpeakerID: "speaker-1", Text: "渡り鳥の調査については、海岸側、北側、南側の合計三地点で。", IsFinal: true},
		{SequenceNo: 11, SpeakerID: "speaker-1", Text: "ええ。実施することを決定します。", IsFinal: true},
	}
	candidates := detectDecisionCandidates(segments)
	if len(candidates) != 1 {
		t.Fatalf("candidates=%+v", candidates)
	}
	if got := candidates[0].SourceSequenceNos; len(got) != 2 || got[0] != 10 || got[1] != 11 {
		t.Fatalf("sourceSequenceNos=%v", got)
	}
	if title := decisionCandidateTitle(candidates[0].Statement); !strings.Contains(title, "渡り鳥") || title == "実施" {
		t.Fatalf("title=%q statement=%q", title, candidates[0].Statement)
	}
}

func TestDecisionCandidateRejectsPredicateOnlyMarker(t *testing.T) {
	if candidates := detectDecisionCandidates([]domain.TranscriptSegment{finalSegment(11, "ええ。実施することを決定します。")}); len(candidates) != 0 {
		t.Fatalf("predicate-only candidates=%+v", candidates)
	}
}

func TestDecisionCandidateCanUsePriorFragmentAcrossAnalysisRounds(t *testing.T) {
	current := []domain.TranscriptSegment{{SequenceNo: 11, SpeakerID: "speaker-1", Text: "実施することを決定します。", IsFinal: true}}
	prior := domain.TranscriptSegment{SequenceNo: 10, SpeakerID: "speaker-1", Text: "渡り鳥の調査は三地点で。", IsFinal: true}
	scope := liveEvidenceScope{Allowed: map[int64]struct{}{10: {}, 11: {}}, CurrentRound: map[int64]struct{}{11: {}}, TranscriptText: map[int64]string{10: prior.Text, 11: current[0].Text}, Segments: map[int64]domain.TranscriptSegment{10: prior, 11: current[0]}}
	candidates := detectDecisionCandidates(extendDecisionSegmentsWithPriorFragment(current, scope))
	if len(candidates) != 1 || len(candidates[0].SourceSequenceNos) != 2 {
		t.Fatalf("candidates=%+v", candidates)
	}
}

func TestReconcileDecisionCandidatePromotesModelTodoWithoutChangingID(t *testing.T) {
	segments := []domain.TranscriptSegment{finalSegment(9, "渡り鳥の調査は海岸側、北側、南側の三地点で実施することを決定します。")}
	model := `{"summary":"更新","currentTopic":"渡り鳥","resolvedIds":[],"items":[{"id":"todo-bird-sites","kind":"todo","severity":"high","title":"三地点で調査する","body":"海岸側・北側・南側で調査する計画へ更新","status":"open"}],"assignments":[{"nodeId":"todo-bird-sites","parentTopicId":"agenda-1","confidence":0.9,"reason":"鳥類調査"}]}`
	reconciled, audit, err := reconcileDecisionCandidates(model, nil, detectDecisionCandidates(segments))
	if err != nil {
		t.Fatal(err)
	}
	var diff liveAnalysisPayload
	if err := json.Unmarshal([]byte(reconciled), &diff); err != nil {
		t.Fatal(err)
	}
	if len(diff.Items) != 1 || diff.Items[0].ID != "todo-bird-sites" || diff.Items[0].Kind != "decision" {
		t.Fatalf("items = %+v, want same id promoted to decision", diff.Items)
	}
	if len(diff.Items[0].EvidenceSequenceNos) != 1 || diff.Items[0].EvidenceSequenceNos[0] != 9 {
		t.Fatalf("evidence = %v, want [9]", diff.Items[0].EvidenceSequenceNos)
	}
	if audit.ModelDecisionItems != 0 || audit.AcceptedDecisions != 1 {
		t.Fatalf("audit = %+v", audit)
	}
}

func TestMixedSegmentProducesDecisionAndUnresolvedItem(t *testing.T) {
	segment := finalSegment(16, "昼一回、夜二回測定することを決定します。ただし、強風日の基準風速は未決定です。")
	model := `{"summary":"更新","currentTopic":"騒音","resolvedIds":[],"items":[{"id":"todo-noise-count","kind":"todo","severity":"high","title":"昼一回・夜二回測定","body":"合計三回測定する","status":"open","evidenceSequenceNos":[16]},{"id":"question-wind-speed","kind":"question","severity":"high","title":"強風日の基準風速が未決定","body":"気象データ確認後に決める","status":"open","evidenceSequenceNos":[16]}],"assignments":[{"nodeId":"todo-noise-count","parentTopicId":"agenda-2","confidence":0.9},{"nodeId":"question-wind-speed","parentTopicId":"agenda-2","confidence":0.9}]}`
	reconciled, _, err := reconcileDecisionCandidates(model, nil, detectDecisionCandidates([]domain.TranscriptSegment{segment}))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := parseAndMergeLiveAnalysisPayload(reconciled, nil, &meetingContext{Agenda: []agendaItem{{ID: "agenda-2", Title: "騒音測定", Order: 1}}}, 1, []int64{16}, TreeClassificationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	counts := map[string]int{}
	for _, item := range state.Items {
		counts[item.Kind]++
	}
	if counts["decision"] != 1 || counts["issue"] != 1 || len(state.Items) != 2 || findItemByID(state.Items, "question-wind-speed").Subtype != issueSubtypeQuestion {
		t.Fatalf("counts=%v items=%+v", counts, state.Items)
	}
}

func TestDecisionRecapMergesEvidenceIntoExistingDecision(t *testing.T) {
	previous := liveAnalysisPayload{
		Summary: "previous",
		Items:   []liveAnalysisItem{{ID: "decision-bird", Kind: "decision", Severity: "high", Title: "渡り鳥を三地点で調査", Body: "海岸側・北側・南側で調査する", Status: "open", EvidenceSequenceNos: []int64{9}}},
	}
	previousJSON, _ := json.Marshal(previous)
	segment := finalSegment(31, "決定事項は、渡り鳥を三地点で調査することです。")
	model := `{"summary":"まとめ","currentTopic":"まとめ","resolvedIds":[],"items":[],"assignments":[]}`
	reconciled, audit, err := reconcileDecisionCandidates(model, previousJSON, detectDecisionCandidates([]domain.TranscriptSegment{segment}))
	if err != nil {
		t.Fatal(err)
	}
	var diff liveAnalysisPayload
	if err := json.Unmarshal([]byte(reconciled), &diff); err != nil {
		t.Fatal(err)
	}
	if len(diff.Items) != 1 || diff.Items[0].ID != "decision-bird" {
		t.Fatalf("recap diff = %+v", diff.Items)
	}
	if audit.MergedDecisions != 1 {
		t.Fatalf("audit = %+v", audit)
	}
}

func TestTodoTransitionsToDecisionWithoutDuplicateCard(t *testing.T) {
	previous := liveAnalysisPayload{
		Summary: "previous",
		Items:   []liveAnalysisItem{{ID: "todo-three-sites", Kind: "todo", Severity: "medium", Title: "三地点での調査を検討", Body: "海岸・北・南の三地点案を検討する", Status: "open", EvidenceSequenceNos: []int64{5}}},
	}
	previousJSON, _ := json.Marshal(previous)
	segment := finalSegment(9, "海岸側・北側・南側の三地点で調査すると決定します。")
	model := `{"summary":"更新","currentTopic":"鳥類","resolvedIds":[],"items":[],"assignments":[]}`
	reconciled, _, err := reconcileDecisionCandidates(model, previousJSON, detectDecisionCandidates([]domain.TranscriptSegment{segment}))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := parseAndMergeLiveAnalysisPayload(reconciled, previousJSON, nil, 2, []int64{9}, TreeClassificationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	if len(state.Items) != 1 || state.Items[0].ID != "todo-three-sites" || state.Items[0].Kind != "decision" {
		t.Fatalf("items = %+v", state.Items)
	}
	if len(state.Items[0].EvidenceSequenceNos) != 2 {
		t.Fatalf("evidence = %v, want original and decision", state.Items[0].EvidenceSequenceNos)
	}
}

func TestSameRoundQuestionAndTodoRemainIndependentCanonicalPropositions(t *testing.T) {
	diff := `{"summary":"更新","currentTopic":"鳥類","resolvedIds":[],"items":[{"id":"question-sites","kind":"question","severity":"medium","title":"追加観測地点の設置基準を決める","body":"基準は何か","status":"open"},{"id":"todo-sites","kind":"todo","severity":"high","title":"追加観測地点の設置基準を決める","body":"風向と鳥の経路から基準を決める","status":"open"}],"assignments":[{"nodeId":"question-sites","parentTopicId":"agenda-1","confidence":0.9},{"nodeId":"todo-sites","parentTopicId":"agenda-1","confidence":0.9}]}`
	stats := &liveAnalysisTreeMergeStats{}
	raw, err := parseAndMergeLiveAnalysisPayload(diff, nil, &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "鳥類", Order: 1}}}, 1, []int64{1}, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	question := findItemByID(state.Items, "question-sites")
	todo := findItemByID(state.Items, "todo-sites")
	if len(state.Items) != 2 || question == nil || question.Kind != "issue" || question.Subtype != issueSubtypeQuestion || todo == nil || todo.Kind != "todo" {
		t.Fatalf("items=%+v", state.Items)
	}
	if stats.PropositionItemsMerged != 0 {
		t.Fatalf("propositionItemsMerged=%d", stats.PropositionItemsMerged)
	}
}
