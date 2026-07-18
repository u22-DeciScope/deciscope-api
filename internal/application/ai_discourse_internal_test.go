package application

import (
	"testing"
)

func TestClassifyDiscourseAct(t *testing.T) {
	cases := []struct {
		text string
		want discourseAct
	}{
		// 会話制御発話(session_497ed2b0aedf9dc6 の実発話を含む)。
		{"以上をまとめます。", discourseRecapIntro},
		{"ここまでを整理します。", discourseRecapIntro},
		{"まとめると", discourseRecapIntro},
		{"結論として確認します", discourseRecapIntro},
		{"それでは、以上をまとめます。", discourseRecapIntro},
		{"次の話題へ移ります。", discourseTopicTransition},
		{"では次の議題に移りましょう。", discourseTopicTransition},
		{"以上でこの議題を終わります。", discourseTopicTransition},
		{"会議を開始します。", discourseMeetingControl},
		{"よろしくお願いします。", discourseMeetingControl},
		{"お疲れ様でした。", discourseMeetingControl},
		// 議論内容(制御表現で始まっても実質内容を含むものは content)。
		{"以上をまとめますと、観測地点は3箇所になります。", discourseContent},
		{"渡り鳥の調査計画を検討します。", discourseContent},
		{"観測地点が不足している。", discourseContent},
		{"強風日の測定条件の決定基準を確定する", discourseContent},
		{"住民説明資料の公開方針を検討する", discourseContent},
		{"専門家による植物種の予備調査の検討", discourseContent},
		{"", discourseContent},
	}
	for _, tc := range cases {
		if got := classifyDiscourseAct(tc.text); got != tc.want {
			t.Errorf("classifyDiscourseAct(%q) = %s, want %s", tc.text, got, tc.want)
		}
	}
}

func TestIsDiscourseOnlyItem(t *testing.T) {
	if !isDiscourseOnlyItem("以上をまとめます", "") {
		t.Errorf("recap intro title must be discourse-only")
	}
	if isDiscourseOnlyItem("以上をまとめます", "観測地点は3箇所に増設する") {
		t.Errorf("item with substantive body must not be discourse-only")
	}
	if isDiscourseOnlyItem("観測地点の追加設置", "") {
		t.Errorf("substantive title must not be discourse-only")
	}
	if isDiscourseOnlyItem("", "") {
		t.Errorf("empty item is not classified as discourse-only")
	}
}

func TestDiscourseTimelineMarksRecapContentAsReferenceUntilTransition(t *testing.T) {
	scope := evidenceScopeFromTexts(map[int64]string{
		30: "以上をまとめます。",
		31: "決定事項は渡り鳥を三地点で調査することです。",
		32: "未解決の課題は強風日の風速基準です。",
		33: "次の議題へ移ります。",
		34: "新しい施工計画を検討します。",
	}, 30, 31, 32, 33, 34)
	timeline := classifyDiscourseTimeline(scope)
	if timeline.Roles[30] != liveEvidenceDiscourseOnly || timeline.Roles[31] != liveEvidenceReferenceRecap || timeline.Roles[32] != liveEvidenceReferenceRecap || timeline.Roles[33] != liveEvidenceDiscourseOnly || timeline.Roles[34] != liveEvidencePrimary {
		t.Fatalf("roles=%+v transitions=%+v", timeline.Roles, timeline.Transitions)
	}
}

// 対象session(session_497ed2b0aedf9dc6)の回帰: 「以上をまとめます」発話から
// fact itemと新topic候補が作られ、recap再言及から重複issueがcandidate証拠に
// なった。制御発話はitemにもcandidateにもしない。
func TestDiscourseOnlySpeechDoesNotBecomeItemOrCandidate(t *testing.T) {
	mc := classificationFixtureContext()
	diff := `{
		"summary": "まとめの区間",
		"currentTopic": "まとめ",
		"items": [
			{"id": "fact-recap-intro", "kind": "fact", "severity": "medium", "title": "以上をまとめます", "body": "", "status": "open"}
		],
		"newTopics": [{"id": "topic-recap", "label": "以上をまとめます"}],
		"assignments": [
			{"nodeId": "fact-recap-intro", "parentTopicId": "topic-recap", "confidence": 0.7, "reason": "まとめ"}
		]
	}`
	merged := mergeForTestWithContext(t, diff, nil, mc)
	assertTreeInvariants(t, merged.Tree)
	if item := itemByID(merged.Items, "fact-recap-intro"); item != nil {
		t.Fatalf("discourse-only item must be rejected, got %+v", item)
	}
	if node := treeNodeByID(merged.Tree, "fact-recap-intro"); node != nil {
		t.Fatalf("discourse-only node must not appear in tree, got %+v", node)
	}
	if len(merged.EmergingTopics) != 0 {
		t.Fatalf("discourse-only label must not create a candidate, got %+v", merged.EmergingTopics)
	}
}

// subjectが空・会話制御発話・証拠と主題不一致のcandidateは昇格しない。
func TestCandidateSubjectIncoherenceBlocksPromotion(t *testing.T) {
	cfg := TreeClassificationConfig{}.normalized()
	itemAt := func(items []liveAnalysisItem) func(string) *liveAnalysisItem {
		return func(id string) *liveAnalysisItem {
			for i := range items {
				if items[i].ID == id {
					return &items[i]
				}
			}
			return nil
		}
	}

	// 対象sessionの実例: candidate「公開方針・情報公開」に強風条件と
	// 説明会日程のitemだけが集まった(labelと証拠が全て不一致)。
	mismatched := emergingTopicCandidate{
		ID: "candidate-mismatch", Label: "公開方針・情報公開",
		EvidenceItemIDs: []string{"todo-wind", "todo-schedule"},
	}
	mismatchedItems := []liveAnalysisItem{
		{ID: "todo-wind", Kind: "todo", Title: "強風日の測定条件の決定基準を確定する"},
		{ID: "todo-schedule", Kind: "todo", Title: "住民説明会の開催日程を確定する"},
	}
	if reason := candidateSubjectIncoherenceReason(mismatched, itemAt(mismatchedItems), cfg); reason != "subject_incoherent" {
		t.Errorf("mismatched candidate reason = %q, want subject_incoherent", reason)
	}

	empty := emergingTopicCandidate{ID: "candidate-empty", Label: "", EvidenceItemIDs: []string{"todo-wind"}}
	if reason := candidateSubjectIncoherenceReason(empty, itemAt(mismatchedItems), cfg); reason != "subject_empty" {
		t.Errorf("empty label reason = %q, want subject_empty", reason)
	}

	discourse := emergingTopicCandidate{ID: "candidate-discourse", Label: "以上をまとめます", EvidenceItemIDs: []string{"todo-wind"}}
	if reason := candidateSubjectIncoherenceReason(discourse, itemAt(mismatchedItems), cfg); reason != "discourse_only_label" {
		t.Errorf("discourse label reason = %q, want discourse_only_label", reason)
	}

	// 主題が一致する証拠を持つcandidateは昇格を保留しない。
	coherentCandidate := emergingTopicCandidate{
		ID: "candidate-plant", Label: "希少植物の事前調査",
		EvidenceItemIDs: []string{"todo-plant", "question-plant"},
	}
	plantItems := []liveAnalysisItem{
		{ID: "todo-plant", Kind: "todo", Title: "専門家による植物種の予備調査の検討"},
		{ID: "question-plant", Kind: "question", Title: "植物の種類を確認するため予備調査を実施するか"},
	}
	if reason := candidateSubjectIncoherenceReason(coherentCandidate, itemAt(plantItems), cfg); reason != "" {
		t.Errorf("coherent candidate reason = %q, want empty", reason)
	}
}

func TestCandidateOriginalSubjectRejectsUnrelatedMutation(t *testing.T) {
	candidate := emergingTopicCandidate{ID: "candidate-plant", Label: "湿地・希少植物調査", Description: "植物種類の確認"}
	initializeCandidateSubject(&candidate)
	original := candidate.OriginalSubject
	if updateCandidateSubject(&candidate, "強風日の風速基準", "騒音測定条件") {
		t.Fatal("unrelated candidate subject mutation was accepted")
	}
	if candidate.OriginalSubject != original || candidate.Label != "湿地・希少植物調査" {
		t.Fatalf("candidate mutated=%+v", candidate)
	}
}
