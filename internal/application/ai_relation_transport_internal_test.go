package application

import (
	"reflect"
	"testing"
)

func relationTransportSentinel(source, target string) liveAnalysisTreeRelation {
	return liveAnalysisTreeRelation{
		ID: "relation-sentinel-v1", Source: source, Target: target, Kind: itemRelationRefines,
		Confidence: 0.73125, EvidenceSequenceNos: []int64{17, 29},
		Origin: "relation_transport_sentinel", Status: "active",
		CreatedAtVersion: 41, UpdatedAtVersion: 43,
	}
}

func assertRelationSentinelEqual(t *testing.T, got, want liveAnalysisTreeRelation) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("relation sentinel changed:\n got=%+v\nwant=%+v", got, want)
	}
}

func findRelationSentinel(relations []liveAnalysisTreeRelation, id string) (liveAnalysisTreeRelation, bool) {
	for _, relation := range relations {
		if relation.ID == id {
			return relation, true
		}
	}
	return liveAnalysisTreeRelation{}, false
}

func TestRelationSentinelSurvivesCloneAsDeepCopy(t *testing.T) {
	want := relationTransportSentinel("item-source", "item-target")
	resolution := &labelResolutionMetadata{
		Status: "fallback_applied", Reason: "context_dependent",
		SourceEvidenceSequenceNos: []int64{16, 17},
	}
	original := liveAnalysisPayload{Tree: &liveAnalysisTree{
		Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議全体"},
			{ID: "item-source", Kind: "issue", ParentID: treeRootNodeID, Label: "原因仮説", LabelResolution: cloneLabelResolution(resolution)},
			{ID: "item-target", Kind: "fact", ParentID: treeRootNodeID, Label: "確認事実"},
		},
		Relations: []liveAnalysisTreeRelation{want},
	}, Items: []liveAnalysisItem{{
		ID: "item-source", Kind: "issue", Title: "原因仮説", LabelResolution: cloneLabelResolution(resolution),
	}}}
	cloned := cloneLiveAnalysisPayload(original)
	if cloned.Tree == nil || len(cloned.Tree.Relations) != 1 || len(cloned.Items) != 1 {
		t.Fatalf("clone lost relations: %+v", cloned.Tree)
	}
	assertRelationSentinelEqual(t, cloned.Tree.Relations[0], want)
	clonedNode := treeNodeByID(cloned.Tree, "item-source")
	if cloned.Items[0].LabelResolution == nil || clonedNode == nil || clonedNode.LabelResolution == nil ||
		!reflect.DeepEqual(cloned.Items[0].LabelResolution, resolution) || !reflect.DeepEqual(clonedNode.LabelResolution, resolution) {
		t.Fatalf("clone lost label resolution: item=%+v node=%+v", cloned.Items[0].LabelResolution, clonedNode)
	}

	cloned.Tree.Relations[0].EvidenceSequenceNos[0] = 999
	cloned.Tree.Relations[0].Origin = "mutated_clone"
	cloned.Items[0].LabelResolution.SourceEvidenceSequenceNos[0] = 998
	clonedNode.LabelResolution.SourceEvidenceSequenceNos[0] = 997
	if original.Tree.Relations[0].EvidenceSequenceNos[0] != 17 ||
		original.Tree.Relations[0].Origin != "relation_transport_sentinel" {
		t.Fatalf("clone mutation leaked into original: %+v", original.Tree.Relations[0])
	}
	if original.Items[0].LabelResolution.SourceEvidenceSequenceNos[0] != 16 ||
		treeNodeByID(original.Tree, "item-source").LabelResolution.SourceEvidenceSequenceNos[0] != 16 {
		t.Fatal("cloned label-resolution mutation leaked into original")
	}
}
