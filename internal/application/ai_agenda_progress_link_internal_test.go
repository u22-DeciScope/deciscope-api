package application

import "testing"

func TestAgendaProgressAdditionalTopicLinkContract(t *testing.T) {
	const candidateID = "candidate-52bec832205a"
	t.Run("materialized topic", func(t *testing.T) {
		topicID := stableDynamicTopicID(candidateID)
		tree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Origin: topicOriginSystem},
			{ID: topicID, Kind: "topic", ParentID: treeRootNodeID, Label: "湿地・希少植物影響調査", Origin: topicOriginDynamic, SourceCandidateID: candidateID},
			{ID: "item-visible", Kind: "issue", ParentID: topicID, Label: "植物影響"},
		}}
		state := evaluateAgendaProgress(agendaProgressInputs{
			Tree:        tree,
			Items:       []liveAnalysisItem{{ID: "item-visible", Kind: "issue", Title: "植物影響", ClassificationStatus: classificationAssigned}},
			TreeVersion: 15,
		})
		entry := agendaProgressEntryByID(state, candidateID)
		if entry == nil || entry.CandidateID != candidateID || entry.MaterializedTopicID != topicID ||
			entry.LinkState != agendaProgressLinkMaterializedTopic || len(entry.FocusNodeIDs) != 1 || entry.FocusNodeIDs[0] != topicID {
			t.Fatalf("entry=%+v", entry)
		}
		if agendaProgressEntryByID(state, topicID) != nil {
			t.Fatalf("topic node id %s must not replace candidate entry id", topicID)
		}
	})

	t.Run("visible evidence item", func(t *testing.T) {
		tree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic"},
			{ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID},
			{ID: "item-visible", Kind: "issue", ParentID: treeUnclassifiedTopicID},
		}}
		state := evaluateAgendaProgress(agendaProgressInputs{
			Tree: tree,
			Items: []liveAnalysisItem{{
				ID: "item-visible", Kind: "issue", Title: "現地説明会", Status: "open",
				ClassificationStatus: classificationAssigned, CandidateTopicID: candidateID,
			}},
			Emerging: []emergingTopicCandidate{{
				ID: candidateID, Label: "現地説明会の開催準備", RoundCount: 2,
				EvidenceItemIDs: []string{"item-visible"},
			}},
			TreeVersion: 12,
		})
		entry := agendaProgressEntryByID(state, candidateID)
		if entry == nil || entry.MaterializedTopicID != "" || entry.PrimaryNodeID != "item-visible" ||
			entry.LinkState != agendaProgressLinkVisibleItems || len(entry.FocusNodeIDs) != 1 || entry.FocusNodeIDs[0] != "item-visible" {
			t.Fatalf("entry=%+v", entry)
		}
	})

	t.Run("tentative evidence is explicitly not linkable", func(t *testing.T) {
		tree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic"},
			{ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID},
			{ID: "item-tentative-a", Kind: "issue", ParentID: treeUnclassifiedTopicID},
			{ID: "item-tentative-b", Kind: "issue", ParentID: treeUnclassifiedTopicID},
		}}
		state := evaluateAgendaProgress(agendaProgressInputs{
			Tree: tree,
			Items: []liveAnalysisItem{
				{ID: "item-tentative-a", Kind: "issue", Title: "植物の種類", Status: "open", ClassificationStatus: classificationTentative, CandidateTopicID: candidateID},
				{ID: "item-tentative-b", Kind: "issue", Title: "予備調査", Status: "open", ClassificationStatus: classificationTentative, CandidateTopicID: candidateID},
			},
			Emerging: []emergingTopicCandidate{{
				ID: candidateID, Label: "湿地・希少植物影響調査", RoundCount: 1,
				EvidenceItemIDs: []string{"item-tentative-a", "item-tentative-b"},
			}},
			TreeVersion: 12,
		})
		entry := agendaProgressEntryByID(state, candidateID)
		if entry == nil || entry.CandidateID != candidateID || entry.PrimaryNodeID != "" ||
			entry.MaterializedTopicID != "" || len(entry.FocusNodeIDs) != 0 ||
			entry.LinkState != agendaProgressLinkNotLinkable {
			t.Fatalf("entry=%+v", entry)
		}
	})
}

func TestStableDynamicTopicIDSeparatesNamespacesAndHydrates(t *testing.T) {
	const candidateID = "candidate-7584e6944e71"
	topicID := stableDynamicTopicID(candidateID)
	if topicID == candidateID || topicID != "topic-dynamic-7584e6944e71" {
		t.Fatalf("topicID=%q candidateID=%q", topicID, candidateID)
	}
	state := &agendaProgressState{Entries: []agendaProgressEntry{{
		ID: candidateID, CandidateID: candidateID, SourceType: agendaProgressSourceDynamic,
		MaterializedTopicID: topicID, MaterializedTopicIDs: []string{topicID},
		PrimaryNodeID: topicID, FocusNodeIDs: []string{topicID},
		LinkState: agendaProgressLinkMaterializedTopic,
	}}}
	tree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{{
		ID: topicID, Kind: "topic", Origin: topicOriginDynamic, SourceCandidateID: candidateID,
	}}}
	refreshAgendaProgressNodeRefs(state, tree)
	entry := &state.Entries[0]
	if entry.MaterializedTopicID != topicID || entry.PrimaryNodeID != topicID || entry.LinkState != agendaProgressLinkMaterializedTopic {
		t.Fatalf("hydrated entry=%+v", entry)
	}
	refreshAgendaProgressNodeRefs(state, &liveAnalysisTree{})
	if entry.MaterializedTopicID != "" || entry.PrimaryNodeID != "" || entry.LinkState != agendaProgressLinkNotLinkable {
		t.Fatalf("stale mapping survived: %+v", entry)
	}

	discovered := &agendaProgressState{Entries: []agendaProgressEntry{{
		ID: candidateID, CandidateID: candidateID, SourceType: agendaProgressSourceDynamic,
		LinkState: agendaProgressLinkNotLinkable,
	}}}
	refreshAgendaProgressNodeRefs(discovered, tree)
	discoveredEntry := &discovered.Entries[0]
	if discoveredEntry.MaterializedTopicID != topicID ||
		discoveredEntry.PrimaryNodeID != topicID ||
		discoveredEntry.LinkState != agendaProgressLinkMaterializedTopic {
		t.Fatalf("newly materialized audit topic was not linked during hydrate: %+v", discoveredEntry)
	}
}

func TestAgendaProgressAdditionalTopicLinksAreRequestScoped(t *testing.T) {
	stateFor := func(candidateID, itemID string) *agendaProgressState {
		return evaluateAgendaProgress(agendaProgressInputs{
			Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
				{ID: treeRootNodeID, Kind: "topic"},
				{ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID},
				{ID: itemID, Kind: "issue", ParentID: treeUnclassifiedTopicID},
			}},
			Items: []liveAnalysisItem{{
				ID: itemID, Kind: "issue", Status: "open",
				ClassificationStatus: classificationAssigned, CandidateTopicID: candidateID,
			}},
			Emerging: []emergingTopicCandidate{{
				ID: candidateID, Label: candidateID, RoundCount: 2,
				EvidenceItemIDs: []string{itemID},
			}},
		})
	}

	sessionA := stateFor("candidate-session-a", "item-session-a")
	sessionB := stateFor("candidate-session-b", "item-session-b")
	if agendaProgressEntryByID(sessionA, "candidate-session-b") != nil ||
		agendaProgressEntryByID(sessionB, "candidate-session-a") != nil {
		t.Fatalf("candidate links leaked across independent session evaluations: A=%+v B=%+v", sessionA, sessionB)
	}
	entryB := agendaProgressEntryByID(sessionB, "candidate-session-b")
	if entryB == nil || len(entryB.FocusNodeIDs) != 1 || entryB.FocusNodeIDs[0] != "item-session-b" {
		t.Fatalf("session B entry=%+v", entryB)
	}
}

func TestFinalizeAgendaProgressReconcilesAuditMaterializedTopic(t *testing.T) {
	const candidateID = "candidate-final-audit"
	topicID := stableDynamicTopicID(candidateID)
	state := &liveAnalysisPayload{
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{{
			ID: topicID, Kind: "topic", ParentID: treeRootNodeID,
			Origin: topicOriginDynamic, SourceCandidateID: candidateID,
		}}},
		AgendaProgress: &agendaProgressState{Entries: []agendaProgressEntry{{
			ID: candidateID, CandidateID: candidateID,
			SourceType: agendaProgressSourceDynamic,
			LinkState:  agendaProgressLinkNotLinkable,
		}}},
	}

	finalizeAgendaProgress(state, nil, 16)
	entry := &state.AgendaProgress.Entries[0]
	if entry.MaterializedTopicID != topicID ||
		entry.PrimaryNodeID != topicID ||
		entry.LinkState != agendaProgressLinkMaterializedTopic {
		t.Fatalf("finalized entry=%+v", entry)
	}
}
