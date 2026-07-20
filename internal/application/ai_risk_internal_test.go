package application

import "testing"

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
