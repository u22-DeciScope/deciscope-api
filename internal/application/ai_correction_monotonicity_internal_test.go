package application

import (
	"encoding/json"
	"testing"

	"deciscope-core-api/internal/domain"
)

func TestExplicitCorrectionSupersessionPersistsMonotonicProvenance(t *testing.T) {
	state := liveAnalysisPayload{
		Items: []liveAnalysisItem{
			{ID: "old-access", Kind: "fact", Title: "交換後スイッチはアクセスポート設定だった", Status: "open", EvidenceSequenceNos: []int64{1}},
			{ID: "vlan-fact", Kind: "fact", Title: "許可VLAN一覧からVLAN30が漏れていた", Status: "open", EvidenceSequenceNos: []int64{2}},
		},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: "root", Kind: "topic", Label: "会議全体"},
			{ID: "old-access", Kind: "fact", ParentID: "root", Label: "交換後スイッチはアクセスポート設定だった"},
			{ID: "vlan-fact", Kind: "fact", ParentID: "root", Label: "許可VLAN一覧からVLAN30が漏れていた"},
		}},
	}
	relation := correctionRelation{
		SourceSequenceNo: 2, TargetSequenceNo: 1,
		TargetItemID: "old-access", ReplacementItemID: "vlan-fact",
		Status: "pending", Confidence: 0.92, Locked: true,
	}
	applyLockedCorrectionRelation(&state, &relation, 2, &liveAnalysisTreeMergeStats{})

	raw, err := json.Marshal(state.Items[0])
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]any{
		"inactive":                true,
		"informationStatus":       "superseded",
		"supersededByItemId":      "vlan-fact",
		"supersededAtTreeVersion": float64(2),
		"supersessionOrigin":      "explicit_correction",
	} {
		if got := persisted[field]; got != want {
			t.Fatalf("%s=%v, want %v; payload=%s", field, got, want, raw)
		}
	}
	evidence, ok := persisted["supersessionEvidenceSequenceNos"].([]any)
	if !ok || len(evidence) != 1 || evidence[0] != float64(2) {
		t.Fatalf("supersessionEvidenceSequenceNos=%v, want [2]; payload=%s", persisted["supersessionEvidenceSequenceNos"], raw)
	}
}

func TestFinalSegmentCoverageClassifiesRecapAndLowInformationWithoutRetry(t *testing.T) {
	state := liveAnalysisPayload{
		Items: []liveAnalysisItem{{
			ID: "vlan-fact", Kind: "fact", Title: "VLAN30が許可一覧から漏れていた",
			Status: "open", EvidenceSequenceNos: []int64{1},
		}},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{{ID: "root", Kind: "topic", Label: "会議全体"}}},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	segments := []domain.TranscriptSegment{
		{CallID: "call", SequenceNo: 2, IsFinal: true, Text: "改めて整理すると、VLAN30が許可一覧から漏れていました。"},
		{CallID: "call", SequenceNo: 3, IsFinal: true, Text: "えーと、以上です。"},
	}
	covered, decisions, err := addLiveAnalysisCoverageWithResult(raw, segments, "no_accepted_item")
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 2 {
		t.Fatalf("decisions=%+v, want 2", decisions)
	}
	want := []string{"recap_of_existing_items", "low_information_ignored"}
	for index, decision := range decisions {
		if decision.Disposition != want[index] || !decision.MeaningfullyCovered || decision.RetryEligible {
			t.Fatalf("decision[%d]=%+v, want disposition=%s covered without retry; payload=%s", index, decision, want[index], covered)
		}
	}
}

func TestFinalSegmentCoverageCarriesRecapAcrossSegmentsBeforeNewItemClassification(t *testing.T) {
	previous := liveAnalysisPayload{Items: []liveAnalysisItem{{
		ID: "todo-settings-check", Kind: "todo", Status: "open",
		Title: "標準設定との差分確認", Body: "佐藤さんが金曜日までに標準設定との差分を確認する",
		EvidenceSequenceNos: []int64{2},
	}}}
	state := previous
	state.Items = append(append([]liveAnalysisItem(nil), previous.Items...), liveAnalysisItem{
		ID: "todo-settings-check-recap-duplicate", Kind: "todo", Status: "open",
		Title: "標準設定との差分確認", Body: "佐藤さんは金曜日までに標準設定との差分を確認する",
		EvidenceSequenceNos: []int64{4},
	})
	currentJSON, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	previousJSON, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	_, decisions, err := addLiveAnalysisCoverageWithResult(currentJSON, []domain.TranscriptSegment{
		{SequenceNo: 3, IsFinal: true, Text: "最後にここまでをまとめます。"},
		{SequenceNo: 4, IsFinal: true, Text: "佐藤さんは金曜日までに標準設定との差分を確認します。"},
		{SequenceNo: 5, IsFinal: true, Text: "以上で振り返りを終了します。"},
	}, "no_accepted_item", previousJSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, decision := range decisions {
		if decision.SequenceNo == 4 && decision.Disposition != segmentDispositionRecap {
			t.Fatalf("recap body disposition=%+v decisions=%+v", decision, decisions)
		}
	}
}

func TestExplicitCorrectionSupersessionRejectsStaleStateReplay(t *testing.T) {
	previous := liveAnalysisPayload{
		Items: []liveAnalysisItem{{
			ID: "old-access", Kind: "fact", Title: "アクセスポート設定だった", Status: "open",
			Inactive: true, MergedIntoID: "vlan-fact", InformationStatus: "superseded",
			SuppressionReason:  "superseded_by_explicit_correction",
			SupersededByItemID: "vlan-fact", SupersededAtTreeVersion: 2,
			SupersessionOrigin: "explicit_correction", SupersessionEvidenceSequenceNos: []int64{2},
		}},
		TreeVersion: 2,
	}
	current := cloneLiveAnalysisPayload(previous)
	current.Items[0].Inactive = false
	current.Items[0].MergedIntoID = ""
	current.Items[0].Status = "resolved"
	current.Items[0].InformationStatus = ""
	current.Items[0].SupersededByItemID = ""
	current.Items[0].SupersededAtTreeVersion = 0
	current.Items[0].SupersessionOrigin = ""
	stats := &liveAnalysisTreeMergeStats{}

	enforceExplicitSupersessionMonotonicity(&current, previous, 3, stats)

	item := current.Items[0]
	if !item.Inactive || item.Status == "resolved" || item.InformationStatus != "superseded" ||
		item.SupersededByItemID != "vlan-fact" || item.SupersededAtTreeVersion != 2 ||
		item.SupersessionOrigin != "explicit_correction" {
		t.Fatalf("stale state won over correction provenance: %+v", item)
	}
	if stats.SupersededReactivated != 1 || stats.SupersededResolved != 1 ||
		stats.CorrectionMonotonicityViolations != 1 {
		t.Fatalf("monotonicity diagnostics=%+v", stats)
	}
}

func TestFreshReaderRepairsLegacyCorrectionSnapshot(t *testing.T) {
	raw := json.RawMessage(`{
		"items":[{"id":"old-access","kind":"fact","title":"アクセスポート設定だった","status":"resolved","evidenceSequenceNos":[1]}],
		"tree":{"nodes":[{"id":"root","kind":"topic"},{"id":"old-access","kind":"fact","parentId":"root"}],"edges":[{"source":"root","target":"old-access"}]},
		"correctionRelations":[{"sourceSequenceNo":2,"targetSequenceNo":1,"targetItemId":"old-access","replacementItemId":"vlan-fact","status":"superseded","confidence":0.9,"locked":true,"establishedAtVersion":2}],
		"treeVersion":3
	}`)

	state := previousLiveAnalysisState(raw)
	item := state.Items[0]
	if !item.Inactive || item.Status == "resolved" || item.InformationStatus != "superseded" ||
		item.SupersededAtTreeVersion != 2 || item.SupersessionOrigin != "explicit_correction" {
		t.Fatalf("fresh reader did not repair legacy supersession: %+v", item)
	}
	for _, node := range state.Tree.Nodes {
		if node.ID == item.ID {
			t.Fatalf("fresh reader retained superseded tree node: %+v", node)
		}
	}
}

func TestTrustedManualRestoreOutranksExplicitSupersession(t *testing.T) {
	previous := liveAnalysisPayload{Items: []liveAnalysisItem{{
		ID: "old-access", Kind: "fact", Status: "open", Inactive: true,
		SupersededByItemID: "vlan-fact", SupersededAtTreeVersion: 2,
		SupersessionOrigin: "explicit_correction",
	}}}
	current := cloneLiveAnalysisPayload(previous)
	current.Items[0].Inactive = false
	current.Items[0].RestoredAtTreeVersion = 4
	current.Items[0].RestorationOrigin = "manual_user_edit"

	enforceExplicitSupersessionMonotonicity(&current, previous, 4, &liveAnalysisTreeMergeStats{})

	if current.Items[0].Inactive {
		t.Fatalf("trusted manual restore was overwritten: %+v", current.Items[0])
	}
}
