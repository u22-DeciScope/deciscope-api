package application

import "testing"

func TestSemanticTreeHealthDetectsMultipleDiscussedAgendasCollapsedIntoOneTopic(t *testing.T) {
	state := liveAnalysisPayload{
		AgendaAnchors: []agendaAnchor{
			{AgendaID: "agenda-impact", Status: agendaStatusMaterialized, MaterializedTopicIDs: []string{"topic-impact"}},
			{AgendaID: "agenda-cause", Status: agendaStatusDiscussed},
		},
		AgendaProgress: &agendaProgressState{Entries: []agendaProgressEntry{
			{ID: "agenda-impact", ComputedStatus: agendaProgressDiscussed, ActiveItemIDs: []string{"fact-impact", "fact-floor"}},
			{ID: "agenda-cause", ComputedStatus: agendaProgressDiscussed, ActiveItemIDs: []string{"fact-vlan", "issue-cause"}},
		}},
		Items: []liveAnalysisItem{
			{ID: "fact-impact", Kind: "fact", Title: "社内ネットワークへ接続できない", ClassificationStatus: classificationAssigned},
			{ID: "fact-floor", Kind: "fact", Title: "2階でも通信遅延が発生した", ClassificationStatus: classificationAssigned},
			{ID: "fact-vlan", Kind: "fact", Title: "許可VLANからVLAN30が漏れていた", ClassificationStatus: classificationAssigned},
			{ID: "issue-cause", Kind: "issue", Title: "VLAN30漏れが直接原因かを調査する", ClassificationStatus: classificationAssigned},
		},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "root"},
			{ID: "topic-impact", Kind: "topic", ParentID: treeRootNodeID, Label: "障害対応", AgendaRefs: []string{"agenda-impact"}, Materialized: true},
			{ID: "fact-impact", Kind: "fact", ParentID: "topic-impact", Label: "接続障害"},
			{ID: "fact-floor", Kind: "fact", ParentID: "topic-impact", Label: "2階の遅延"},
			{ID: "fact-vlan", Kind: "fact", ParentID: "topic-impact", Label: "VLAN漏れ"},
			{ID: "issue-cause", Kind: "issue", ParentID: "topic-impact", Label: "原因調査"},
		}},
	}

	health := computeSemanticTreeHealth(state)
	if !health.NeedsReorganization || health.SemanticTopicConcentrationCount != 1 {
		t.Fatalf("collapsed multi-agenda tree was not detected: %+v", health)
	}
	if health.DiscussedAgendaCount != 2 || health.MaterializedAgendaCount != 1 || health.UnmaterializedDiscussedAgendaCount != 1 {
		t.Fatalf("agenda counts = %+v", health)
	}
	if health.MaxTopicConcentration != 1 || health.TopicSemanticDiversity < 2 {
		t.Fatalf("semantic concentration = %+v", health)
	}
}

func TestSemanticTreeHealthDoesNotReorganizeSingleAgendaOnlyForItemVolume(t *testing.T) {
	state := liveAnalysisPayload{
		AgendaAnchors: []agendaAnchor{{AgendaID: "agenda-impact", Status: agendaStatusMaterialized, MaterializedTopicIDs: []string{"topic-impact"}}},
		Items: []liveAnalysisItem{
			{ID: "fact-a", Title: "3階で接続障害が発生", ClassificationStatus: classificationAssigned},
			{ID: "fact-b", Title: "2階でも通信遅延が発生", ClassificationStatus: classificationAssigned},
		},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic"},
			{ID: "topic-impact", Kind: "topic", ParentID: treeRootNodeID, AgendaRefs: []string{"agenda-impact"}},
			{ID: "fact-a", Kind: "fact", ParentID: "topic-impact"},
			{ID: "fact-b", Kind: "fact", ParentID: "topic-impact"},
		}},
	}
	if health := computeSemanticTreeHealth(state); health.NeedsReorganization {
		t.Fatalf("single agenda volume alone triggered semantic reorganization: %+v", health)
	}
}
