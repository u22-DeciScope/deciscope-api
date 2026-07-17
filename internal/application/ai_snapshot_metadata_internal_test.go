package application

import (
	"testing"
)

// 対象session(session_497ed2b0aedf9dc6)のversion 11→12相当: dynamic topic昇格
// ラウンドでも既存ノードは全て保持され、completed payloadはfull snapshotとして
// 自身のノード数・エッジ数・ハッシュ・削除ノード一覧を持つ。
func TestFullSnapshotMetadataAndNodePreservationOnPromotion(t *testing.T) {
	mc := classificationFixtureContext()
	round1 := `{
		"summary": "既存アジェンダの議論",
		"currentTopic": "騒音測定",
		"items": [
			{"id": "issue-noise", "kind": "issue", "severity": "medium", "title": "夜間低周波音への住民懸念", "body": "住民から夜間の低周波音への懸念", "status": "open"},
			{"id": "todo-plant-survey", "kind": "todo", "severity": "medium", "title": "専門家による植物種の予備調査の検討", "body": "希少植物の可能性を確認する", "status": "open"}
		],
		"newTopics": [{"id": "topic-plant", "label": "希少植物の事前調査"}],
		"assignments": [
			{"nodeId": "issue-noise", "parentTopicId": "agenda-1", "confidence": 0.9, "reason": "アジェンダ該当"},
			{"nodeId": "todo-plant-survey", "parentTopicId": "topic-plant", "confidence": 0.8, "reason": "新論点"}
		]
	}`
	state1 := mergeForTestAtRound(t, round1, nil, mc, 11)
	assertTreeInvariants(t, state1.Tree)
	if state1.PayloadKind != "full_snapshot" {
		t.Fatalf("payloadKind = %q, want full_snapshot", state1.PayloadKind)
	}
	if state1.NodeCount != len(state1.Tree.Nodes) || state1.EdgeCount != len(state1.Tree.Edges) {
		t.Fatalf("nodeCount=%d edgeCount=%d, want %d/%d", state1.NodeCount, state1.EdgeCount, len(state1.Tree.Nodes), len(state1.Tree.Edges))
	}
	if state1.TreeHash == "" {
		t.Fatalf("treeHash must be set on full snapshots")
	}

	round2 := `{
		"summary": "希少植物の議論が継続",
		"currentTopic": "希少植物",
		"items": [
			{"id": "question-plant", "kind": "question", "severity": "medium", "title": "植物の種類を確認するため予備調査を実施するか", "body": "次回会議で検討", "status": "open"}
		],
		"newTopics": [{"id": "topic-plant", "label": "希少植物の事前調査"}],
		"assignments": [
			{"nodeId": "question-plant", "parentTopicId": "topic-plant", "confidence": 0.85, "reason": "同一論点"}
		]
	}`
	state2 := mergeForTestAtRound(t, round2, marshalPayloadForTest(t, state1), mc, 12)
	assertTreeInvariants(t, state2.Tree)

	dynamicID, _ := canonicalCandidateID("希少植物の事前調査", "")
	if topic := treeNodeByID(state2.Tree, dynamicID); topic == nil || topic.Origin != topicOriginDynamic {
		t.Fatalf("promoted dynamic topic missing: %+v", state2.Tree.Nodes)
	}

	// version 11の全既存ノードがversion 12にも残る(removedNodeIdsでの説明なしに
	// ノードが消えない)。
	currentIDs := make(map[string]struct{}, len(state2.Tree.Nodes))
	for _, node := range state2.Tree.Nodes {
		currentIDs[node.ID] = struct{}{}
	}
	removed := make(map[string]struct{}, len(state2.RemovedNodeIDs))
	for _, id := range state2.RemovedNodeIDs {
		removed[id] = struct{}{}
	}
	for _, node := range state1.Tree.Nodes {
		if _, kept := currentIDs[node.ID]; kept {
			continue
		}
		if _, explained := removed[node.ID]; !explained {
			t.Errorf("node %s disappeared from v12 without removedNodeIds entry", node.ID)
		}
	}
	// このシナリオでは正当な削除は起きない(topic-unclassifiedの整理を除く)。
	for _, id := range state2.RemovedNodeIDs {
		if id != treeUnclassifiedTopicID {
			t.Errorf("unexpected removed node %s", id)
		}
	}
	if state2.PayloadKind != "full_snapshot" || state2.NodeCount != len(state2.Tree.Nodes) {
		t.Fatalf("v12 metadata: payloadKind=%q nodeCount=%d want full_snapshot/%d", state2.PayloadKind, state2.NodeCount, len(state2.Tree.Nodes))
	}
	if state2.BasedOnTreeVersion != 11 {
		t.Fatalf("basedOnTreeVersion = %d, want 11", state2.BasedOnTreeVersion)
	}
}
