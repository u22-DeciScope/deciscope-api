package application

import (
	"encoding/json"
	"testing"
)

func TestQuestionOpenIssueTodoSemanticFixtures(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "強風日の条件", Order: 1}}}
	tests := []struct {
		name string
		diff string
		want map[string]int
	}{
		{
			name: "question only",
			diff: `{"summary":"質問","items":[{"id":"question-threshold","kind":"question","severity":"medium","title":"強風日の基準風速は何m/sか","body":"基準値への回答が必要","status":"open"}],"assignments":[{"nodeId":"question-threshold","parentTopicId":"agenda-1","confidence":0.9}]}`,
			want: map[string]int{"question": 1},
		},
		{
			name: "open issue only",
			diff: `{"summary":"未解決","items":[{"id":"open-threshold","kind":"open_issue","severity":"high","title":"強風日の基準風速が未確定","body":"決める必要がある","status":"open"}],"assignments":[{"nodeId":"open-threshold","parentTopicId":"agenda-1","confidence":0.9}]}`,
			want: map[string]int{"open_issue": 1},
		},
		{
			name: "todo only",
			diff: `{"summary":"実施事項","items":[{"id":"todo-weather","kind":"todo","severity":"high","title":"過去5年間の気象データを確認する","body":"次回までに確認する","status":"open"}],"assignments":[{"nodeId":"todo-weather","parentTopicId":"agenda-1","confidence":0.9}]}`,
			want: map[string]int{"todo": 1},
		},
		{
			name: "open issue and todo",
			diff: `{"summary":"混在","items":[{"id":"open-threshold","kind":"open_issue","severity":"high","title":"強風日の基準風速が未確定","body":"決める必要がある","status":"open"},{"id":"todo-weather","kind":"todo","severity":"high","title":"気象データを確認する","body":"次回までに確認する","status":"open"}],"assignments":[{"nodeId":"open-threshold","parentTopicId":"agenda-1","confidence":0.9},{"nodeId":"todo-weather","parentTopicId":"agenda-1","confidence":0.9}]}`,
			want: map[string]int{"open_issue": 1, "todo": 1},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw, err := parseAndMergeLiveAnalysisPayload(test.diff, nil, mc, 1, []int64{1}, TreeClassificationConfig{})
			if err != nil {
				t.Fatal(err)
			}
			state := previousLiveAnalysisState(raw)
			got := make(map[string]int)
			for _, item := range state.Items {
				got[item.Kind]++
			}
			for kind, count := range test.want {
				if got[kind] != count {
					t.Fatalf("kind counts=%v want %v items=%+v", got, test.want, state.Items)
				}
			}
			if len(state.Items) != totalKindCount(test.want) {
				t.Fatalf("unexpected item kinds=%v", got)
			}
		})
	}
}

func TestResolvedQuestionAndTodoRemainSeparateFromDecisionAndRecap(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "強風日の条件", Order: 1}}}
	initial := `{"summary":"未解決","items":[{"id":"question-threshold","kind":"question","severity":"medium","title":"基準風速は何m/sか","body":"回答が必要","status":"open"},{"id":"open-threshold","kind":"open_issue","severity":"high","title":"基準風速が未確定","body":"決める必要がある","status":"open"},{"id":"todo-weather","kind":"todo","severity":"high","title":"気象データを確認する","body":"判断材料を確認する","status":"open"}],"assignments":[{"nodeId":"question-threshold","parentTopicId":"agenda-1","confidence":0.9},{"nodeId":"open-threshold","parentTopicId":"agenda-1","confidence":0.9},{"nodeId":"todo-weather","parentTopicId":"agenda-1","confidence":0.9}]}`
	raw1, err := parseAndMergeLiveAnalysisPayload(initial, nil, mc, 1, []int64{1}, TreeClassificationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	resolved := `{"summary":"解決","resolvedIds":[],"resolutionUpdates":[{"itemId":"question-threshold","status":"resolved","evidenceSequenceNos":[2],"reason":"基準を決定"},{"itemId":"open-threshold","status":"resolved","evidenceSequenceNos":[2],"reason":"基準を決定"},{"itemId":"todo-weather","status":"resolved","evidenceSequenceNos":[2],"reason":"確認後に決定"}],"items":[{"id":"decision-threshold","kind":"decision","severity":"high","title":"基準風速は12m/sとする","body":"今回の基準として採用する","status":"open"}],"assignments":[{"nodeId":"decision-threshold","parentTopicId":"agenda-1","confidence":0.9}]}`
	scope := liveEvidenceScope{Allowed: map[int64]struct{}{2: {}}, CurrentRound: map[int64]struct{}{2: {}}, TranscriptText: map[int64]string{2: "気象データを確認した結果、基準風速は12m/sとすることにします"}, CoveredThrough: 2}
	raw2, err := parseAndMergeLiveAnalysisPayloadWithEvidence(resolved, raw1, mc, 2, []int64{2}, scope, TreeClassificationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	state2 := previousLiveAnalysisState(raw2)
	if len(state2.Items) != 4 || itemByID(state2.Items, "decision-threshold").Kind != "decision" {
		t.Fatalf("items=%+v", state2.Items)
	}
	for _, id := range []string{"question-threshold", "open-threshold", "todo-weather"} {
		if item := itemByID(state2.Items, id); item == nil || item.Status != "resolved" {
			t.Fatalf("resolved item %s=%+v", id, item)
		}
	}

	previous, _ := json.Marshal(state2)
	recap := `{"summary":"まとめ","items":[{"id":"decision-threshold","kind":"decision","severity":"high","title":"基準風速は12m/sとする","body":"今回の基準として採用する","status":"open"}],"assignments":[{"nodeId":"decision-threshold","parentTopicId":"agenda-1","confidence":0.9}]}`
	raw3, err := parseAndMergeLiveAnalysisPayload(recap, previous, mc, 3, []int64{3}, TreeClassificationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	state3 := previousLiveAnalysisState(raw3)
	decision := itemByID(state3.Items, "decision-threshold")
	if len(state3.Items) != 4 || decision == nil || len(decision.EvidenceSequenceNos) != 2 {
		t.Fatalf("recap items=%+v", state3.Items)
	}
}

func totalKindCount(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}
