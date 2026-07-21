package application

import (
	"encoding/json"
	"testing"
)

func riskFixtureTexts() (seq18, seq21, seq9 string) {
	seq18 = "ただし、間接対象を増やすとアラートが多くなりすぎるという可能性があります。監視間隔と通知条件については、次回までに検討が必要です。"
	seq21 = "今回の支社ネットワーク障害とは直接関係ありませんが、放置するとリモート接続ができなくなる可能性があります。VPN証明書の更新は、今回のvラン障害とは別の新しい対応事項として管理します。"
	seq9 = "現時点では、この設定漏れが障害の直接原因である可能性が最も高いと考えています。"
	return
}

func TestSynthesizeExplicitRiskItemsExtractsSeq18AndSeq21(t *testing.T) {
	seq18, seq21, _ := riskFixtureTexts()
	scope := evidenceScopeFromTexts(map[int64]string{18: seq18, 21: seq21}, 18, 21)
	timeline := discourseTimeline{Roles: map[int64]liveEvidenceRole{}}
	stats := &liveAnalysisTreeMergeStats{}
	risks := synthesizeExplicitRiskItems(nil, nil, scope, timeline, stats)
	if len(risks) != 2 {
		t.Fatalf("expected 2 risk items, got %d: %+v", len(risks), risks)
	}
	for _, item := range risks {
		if item.Kind != "risk" || item.Status != "open" || item.Severity != "medium" {
			t.Fatalf("unexpected risk item shape: %+v", item)
		}
	}
	if stats.RiskItemsSynthesized != 2 {
		t.Fatalf("expected RiskItemsSynthesized=2, got %d", stats.RiskItemsSynthesized)
	}
}

func TestSynthesizeExplicitRiskItemsExcludesCauseInference(t *testing.T) {
	_, _, seq9 := riskFixtureTexts()
	scope := evidenceScopeFromTexts(map[int64]string{9: seq9}, 9)
	timeline := discourseTimeline{Roles: map[int64]liveEvidenceRole{}}
	risks := synthesizeExplicitRiskItems(nil, nil, scope, timeline, nil)
	if len(risks) != 0 {
		t.Fatalf("expected no risk item from a cause-inference statement, got %+v", risks)
	}
}

func TestSynthesizeExplicitRiskItemsSuppressesSameSubjectExistingRisk(t *testing.T) {
	seq18, _, _ := riskFixtureTexts()
	existing := liveAnalysisItem{
		ID: "item-risk-existing", Kind: "risk", Status: "open",
		Title: "間接対象増加によるアラート過多", Body: seq18, EvidenceSequenceNos: []int64{18},
	}
	scope := evidenceScopeFromTexts(map[int64]string{18: seq18}, 18)
	timeline := discourseTimeline{Roles: map[int64]liveEvidenceRole{}}
	risks := synthesizeExplicitRiskItems([]liveAnalysisItem{existing}, nil, scope, timeline, nil)
	if len(risks) != 0 {
		t.Fatalf("expected no new risk item when an existing risk already covers the subject, got %+v", risks)
	}
}

func TestSynthesizeExplicitRiskItemsSkipsReferenceRecapUtterances(t *testing.T) {
	seq18, _, _ := riskFixtureTexts()
	scope := evidenceScopeFromTexts(map[int64]string{18: seq18}, 18)
	timeline := discourseTimeline{Roles: map[int64]liveEvidenceRole{18: liveEvidenceReferenceRecap}}
	risks := synthesizeExplicitRiskItems(nil, nil, scope, timeline, nil)
	if len(risks) != 0 {
		t.Fatalf("expected no risk item from a reference/recap utterance, got %+v", risks)
	}
}

// TestSynthesizeExplicitRiskItemsMigratesSameSentenceDiscussionIssueToRisk
// reproduces group-dd702579aa54's 4-child defect (W6): the model's own
// discussion issue and synthesizeExplicitRiskItems both react to the exact
// same sentence. The diff issue must migrate into a risk item (kind
// rewritten, subtype cleared) instead of a second, separately synthesized
// risk item coexisting alongside it. This fixture is a pure possibility
// statement with no distinct action/undecided marker of its own (unlike
// riskFixtureTexts's seq18, which now keeps its issue separate per F1's
// issueCarriesDistinctActionProposition guard -- see
// TestAlertRiskAndConditionReviewCoexist).
func TestSynthesizeExplicitRiskItemsMigratesSameSentenceDiscussionIssueToRisk(t *testing.T) {
	text := "放置すると、リモート接続ができなくなる可能性があります。"
	scope := evidenceScopeFromTexts(map[int64]string{18: text}, 18)
	timeline := discourseTimeline{Roles: map[int64]liveEvidenceRole{}}
	diffItems := []liveAnalysisItem{{
		ID: "item-issue-discussion-alert", Kind: "issue", Subtype: issueSubtypeDiscussion,
		Title: "リモート接続への影響懸念", Body: text, Status: "open", EvidenceSequenceNos: []int64{18},
	}}
	stats := &liveAnalysisTreeMergeStats{}
	risks := synthesizeExplicitRiskItems(nil, diffItems, scope, timeline, stats)
	if len(risks) != 0 {
		t.Fatalf("expected no separately-synthesized risk item (the diff issue migrates instead), got %+v", risks)
	}
	if diffItems[0].Kind != "risk" || diffItems[0].Subtype != "" {
		t.Fatalf("diff issue = %+v, want migrated to kind=risk with subtype cleared", diffItems[0])
	}
	if stats.SemanticKindMigrations == 0 {
		t.Fatalf("stats = %+v, want SemanticKindMigrations incremented", stats)
	}
}

// TestAlertRiskAndConditionReviewCoexist reproduces F1's motivating defect:
// an issue whose body carries BOTH a risk-shaped possibility clause and its
// own distinct pending-review proposition ("〜を検討します") must not be
// migrated/merged into a risk -- both the synthesized risk and the original
// issue must survive, including through applyDeterministicFinalTreeRepairs's
// cross-kind dedup sweep.
func TestAlertRiskAndConditionReviewCoexist(t *testing.T) {
	text := "監視対象を増やすとアラートが多くなりすぎる可能性があります。通知間隔と通知条件を検討します。"
	scope := evidenceScopeFromTexts(map[int64]string{14: text}, 14)
	timeline := discourseTimeline{Roles: map[int64]liveEvidenceRole{}}
	diffItems := []liveAnalysisItem{{
		ID: "item-issue-discussion-alert", Kind: "issue", Subtype: issueSubtypeDiscussion,
		Title: "通知間隔と通知条件の検討", Body: text, Status: "open", EvidenceSequenceNos: []int64{14},
	}}
	stats := &liveAnalysisTreeMergeStats{}
	risks := synthesizeExplicitRiskItems(nil, diffItems, scope, timeline, stats)
	if len(risks) != 1 {
		t.Fatalf("expected exactly 1 synthesized risk item, got %d: %+v", len(risks), risks)
	}
	if diffItems[0].Kind != "issue" || diffItems[0].Subtype != issueSubtypeDiscussion {
		t.Fatalf("diff issue = %+v, want kind left unchanged (issue/discussion), not migrated", diffItems[0])
	}
	if stats.SemanticKindMigrations != 0 {
		t.Fatalf("stats = %+v, want no kind migration", stats)
	}

	// applyDeterministicFinalTreeRepairs後もrisk/issueが統合されないこと。
	risk := risks[0]
	issue := diffItems[0]
	previous := liveAnalysisPayload{
		Summary: "previous",
		Items:   []liveAnalysisItem{issue, risk},
		Tree: &liveAnalysisTree{
			Nodes: []liveAnalysisTreeNode{
				{ID: treeRootNodeID, Kind: "topic", Label: "会議全体"},
				{ID: "topic-network", Kind: "topic", ParentID: treeRootNodeID, Label: "監視アラート運用", Origin: topicOriginDynamic},
				{ID: issue.ID, Kind: "issue", Subtype: issueSubtypeDiscussion, ParentID: "topic-network", Label: issue.Title, Status: "open"},
				{ID: risk.ID, Kind: "risk", ParentID: "topic-network", Label: risk.Title, Status: "open"},
			},
		},
	}
	rebuildTreeAuditEdges(previous.Tree)
	payload, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	repaired, repairStats := applyDeterministicFinalTreeRepairs(payload, nil, 5)
	if repairStats.Error != "" || repairStats.IntegrityRejected {
		t.Fatalf("repair stats = %+v, want a clean pass-through", repairStats)
	}
	if repairStats.CrossKindDuplicatesMerged != 0 {
		t.Fatalf("repair stats = %+v, want no cross-kind merge (distinct action proposition survives)", repairStats)
	}
	final := previousLiveAnalysisState(repaired)
	survivingIssue := findItemByID(final.Items, issue.ID)
	if survivingIssue == nil || survivingIssue.MergedIntoID != "" || survivingIssue.Kind != "issue" {
		t.Fatalf("issue after final repair = %+v, want unmerged and kind=issue", survivingIssue)
	}
	survivingRisk := findItemByID(final.Items, risk.ID)
	if survivingRisk == nil || survivingRisk.Kind != "risk" {
		t.Fatalf("risk after final repair = %+v, want present and kind=risk", survivingRisk)
	}
}

func TestSynthesizeExplicitRiskItemsCapsAtTwoPerRound(t *testing.T) {
	seq18, seq21, _ := riskFixtureTexts()
	seq24 := "また、権限設定を見直さないと、誤って別部署のvランへ接続できなくなる可能性があります。"
	scope := evidenceScopeFromTexts(map[int64]string{18: seq18, 21: seq21, 24: seq24}, 18, 21, 24)
	timeline := discourseTimeline{Roles: map[int64]liveEvidenceRole{}}
	risks := synthesizeExplicitRiskItems(nil, nil, scope, timeline, nil)
	if len(risks) != 2 {
		t.Fatalf("expected the round cap to limit synthesis to 2 items, got %d: %+v", len(risks), risks)
	}
}
