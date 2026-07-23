package application

import (
	"encoding/json"
	"testing"

	"deciscope-core-api/internal/domain"
)

// --- test helpers ------------------------------------------------------------

func agendaProgressTestMC(agendas ...agendaItem) *meetingContext {
	return &meetingContext{Title: "テスト会議", Agenda: agendas}
}

func agendaProgressTestTree(agendaIDs ...string) *liveAnalysisTree {
	nodes := []liveAnalysisTreeNode{{ID: treeRootNodeID, Kind: "topic", Label: "root", Origin: topicOriginSystem}}
	for _, id := range agendaIDs {
		nodes = append(nodes, liveAnalysisTreeNode{
			ID: id, Kind: "topic", ParentID: treeRootNodeID, Label: id,
			Origin: topicOriginAgenda, AgendaRefs: []string{id}, Materialized: true,
		})
	}
	return &liveAnalysisTree{Nodes: nodes}
}

func addAgendaProgressItemNode(tree *liveAnalysisTree, itemID, parentTopicID, kind string) {
	tree.Nodes = append(tree.Nodes, liveAnalysisTreeNode{ID: itemID, Kind: kind, ParentID: parentTopicID})
}

func addAgendaProgressDynamicTopicNode(tree *liveAnalysisTree, topicID, label string) {
	tree.Nodes = append(tree.Nodes, liveAnalysisTreeNode{ID: topicID, Kind: "topic", ParentID: treeRootNodeID, Label: label, Origin: topicOriginDynamic})
}

func agendaProgressEntryByID(state *agendaProgressState, id string) *agendaProgressEntry {
	if state == nil {
		return nil
	}
	for i := range state.Entries {
		if state.Entries[i].ID == id {
			return &state.Entries[i]
		}
	}
	return nil
}

func timelineWithPrimaryRoles(seqs ...int64) discourseTimeline {
	roles := make(map[int64]liveEvidenceRole, len(seqs))
	for _, seq := range seqs {
		roles[seq] = liveEvidencePrimary
	}
	return discourseTimeline{Roles: roles}
}

// --- state machine (§2.5) ----------------------------------------------------

func TestAgendaProgressStatusSingleMentionDoesNotBecomeDiscussing(t *testing.T) {
	mc := agendaProgressTestMC(
		agendaItem{ID: "agenda-1", Title: "議題A", Order: 1, Role: agendaRolePrimary},
		agendaItem{ID: "agenda-2", Title: "議題B", Order: 2, Role: agendaRolePrimary},
	)
	tree := agendaProgressTestTree("agenda-1", "agenda-2")
	addAgendaProgressItemNode(tree, "item-a1-1", "agenda-1", "fact")
	addAgendaProgressItemNode(tree, "item-a2-1", "agenda-2", "issue")
	items := []liveAnalysisItem{
		{ID: "item-a1-1", Kind: "fact", Title: "現状", EvidenceSequenceNos: []int64{3}},
		{ID: "item-a2-1", Kind: "issue", Title: "課題", EvidenceSequenceNos: []int64{1, 2}},
	}
	diffItems := items
	state := evaluateAgendaProgress(agendaProgressInputs{
		MC: mc, Tree: tree, Items: items, DiffItems: diffItems,
		RoundSeqNos: []int64{1, 2, 3},
		Timeline:    timelineWithPrimaryRoles(1, 2, 3),
		TreeVersion: 1,
	})
	agenda1 := agendaProgressEntryByID(state, "agenda-1")
	agenda2 := agendaProgressEntryByID(state, "agenda-2")
	if agenda1 == nil || agenda1.ComputedStatus != agendaProgressNotStarted {
		t.Fatalf("agenda-1 = %+v, want not_started (single mention must not out-score the round leader)", agenda1)
	}
	if agenda2 == nil || agenda2.ComputedStatus != agendaProgressDiscussing || state.ComputedCurrentTopicID != "agenda-2" {
		t.Fatalf("agenda-2 = %+v currentTopicId=%s, want discussing & current", agenda2, state.ComputedCurrentTopicID)
	}
}

func TestAgendaProgressStatusTwoSegmentsBecomeDiscussing(t *testing.T) {
	mc := agendaProgressTestMC(agendaItem{ID: "agenda-1", Title: "議題A", Order: 1, Role: agendaRolePrimary})
	tree := agendaProgressTestTree("agenda-1")
	addAgendaProgressItemNode(tree, "item-1", "agenda-1", "fact")
	items := []liveAnalysisItem{{ID: "item-1", Kind: "fact", EvidenceSequenceNos: []int64{1, 2}}}
	state := evaluateAgendaProgress(agendaProgressInputs{
		MC: mc, Tree: tree, Items: items, DiffItems: items,
		RoundSeqNos: []int64{1, 2}, Timeline: timelineWithPrimaryRoles(1, 2), TreeVersion: 1,
	})
	entry := agendaProgressEntryByID(state, "agenda-1")
	if entry == nil || entry.ComputedStatus != agendaProgressDiscussing {
		t.Fatalf("agenda-1 = %+v, want discussing", entry)
	}
}

func TestAgendaProgressStatusWithdrawnTwoRoundsBecomesDiscussed(t *testing.T) {
	mc := agendaProgressTestMC(
		agendaItem{ID: "agenda-1", Title: "共有事項", Order: 1, Role: agendaRolePrimary},
		agendaItem{ID: "agenda-2", Title: "議題B", Order: 2, Role: agendaRolePrimary},
	)
	tree := agendaProgressTestTree("agenda-1", "agenda-2")
	addAgendaProgressItemNode(tree, "item-a1-1", "agenda-1", "fact")
	addAgendaProgressItemNode(tree, "item-a1-2", "agenda-1", "fact")

	// v1: agenda-1 gets 2 substantive segments -> discussing & current.
	// Anchors are derived the same way the real merge pipeline derives them
	// (reconcileAgendaAnchors), since RelatedItemCounts (needed for the
	// discussed transition below) is keyed off each fixed entry's
	// MaterializedTopicIDs, which anchors provide.
	items := []liveAnalysisItem{{ID: "item-a1-1", Kind: "fact", EvidenceSequenceNos: []int64{1, 2}}}
	anchors1 := reconcileAgendaAnchors(nil, mc, tree, items, 1, false)
	state1 := evaluateAgendaProgress(agendaProgressInputs{
		MC: mc, Tree: tree, Items: items, DiffItems: items, Anchors: anchors1,
		RoundSeqNos: []int64{1, 2}, Timeline: timelineWithPrimaryRoles(1, 2), TreeVersion: 1,
	})
	if entry := agendaProgressEntryByID(state1, "agenda-1"); entry == nil || entry.ComputedStatus != agendaProgressDiscussing || state1.ComputedCurrentTopicID != "agenda-1" {
		t.Fatalf("v1 agenda-1 = %+v current=%s", entry, state1.ComputedCurrentTopicID)
	}

	// v2: agenda-1 continues (2 more segments) so ActiveRounds reaches 2.
	items2 := append(items, liveAnalysisItem{ID: "item-a1-2", Kind: "fact", EvidenceSequenceNos: []int64{3, 4}})
	anchors2 := reconcileAgendaAnchors(anchors1, mc, tree, items2, 2, false)
	state2 := evaluateAgendaProgress(agendaProgressInputs{
		Previous: state1, MC: mc, Tree: tree, Items: items2, DiffItems: []liveAnalysisItem{items2[1]}, Anchors: anchors2,
		RoundSeqNos: []int64{3, 4}, Timeline: timelineWithPrimaryRoles(3, 4), TreeVersion: 2,
	})
	if entry := agendaProgressEntryByID(state2, "agenda-1"); entry == nil || entry.ActiveRounds != 2 {
		t.Fatalf("v2 agenda-1 = %+v, want ActiveRounds=2", entry)
	}

	// v3: explicit transition switches current to agenda-2. agenda-1 goes idle (inactive=1).
	spans3 := []agendaContextSpan{{Mode: agendaContextModeFixed, AgendaID: "agenda-2", StartSequenceNo: 5, EndSequenceNo: 6, Explicit: true}}
	itemA2 := liveAnalysisItem{ID: "item-a2-1", Kind: "issue", EvidenceSequenceNos: []int64{5, 6}}
	tree.Nodes = append(tree.Nodes, liveAnalysisTreeNode{ID: "item-a2-1", Kind: "issue", ParentID: "agenda-2"})
	items3 := append(items2, itemA2)
	anchors3 := reconcileAgendaAnchors(anchors2, mc, tree, items3, 3, false)
	state3 := evaluateAgendaProgress(agendaProgressInputs{
		Previous: state2, MC: mc, Tree: tree, Items: items3, DiffItems: []liveAnalysisItem{itemA2}, Anchors: anchors3,
		RoundSeqNos: []int64{5, 6}, Timeline: timelineWithPrimaryRoles(5, 6), Spans: spans3, TreeVersion: 3,
	})
	if state3.ComputedCurrentTopicID != "agenda-2" {
		t.Fatalf("v3 currentTopicId = %s, want agenda-2 (explicit transition)", state3.ComputedCurrentTopicID)
	}
	if entry := agendaProgressEntryByID(state3, "agenda-1"); entry == nil || entry.InactiveRounds != 1 || entry.ComputedStatus != agendaProgressDiscussing {
		t.Fatalf("v3 agenda-1 = %+v, want discussing with inactiveRounds=1", entry)
	}

	// v4: agenda-1 still idle -> inactiveRounds reaches 2 -> discussed.
	anchors4 := reconcileAgendaAnchors(anchors3, mc, tree, items3, 4, false)
	state4 := evaluateAgendaProgress(agendaProgressInputs{
		Previous: state3, MC: mc, Tree: tree, Items: items3, DiffItems: nil, Anchors: anchors4,
		RoundSeqNos: nil, Timeline: discourseTimeline{Roles: map[int64]liveEvidenceRole{}}, TreeVersion: 4,
	})
	entry := agendaProgressEntryByID(state4, "agenda-1")
	if entry == nil || entry.ComputedStatus != agendaProgressDiscussed {
		t.Fatalf("v4 agenda-1 = %+v, want discussed", entry)
	}
}

// --- current topic (§2.6) -----------------------------------------------------

func TestAgendaProgressCurrentTopicSingleMentionDoesNotSwitch(t *testing.T) {
	mc := agendaProgressTestMC(
		agendaItem{ID: "agenda-1", Title: "議題A", Order: 1, Role: agendaRolePrimary},
		agendaItem{ID: "agenda-2", Title: "議題B", Order: 2, Role: agendaRolePrimary},
	)
	tree := agendaProgressTestTree("agenda-1", "agenda-2")
	itemA1 := liveAnalysisItem{ID: "item-a1-1", Kind: "fact", EvidenceSequenceNos: []int64{1, 2}}
	addAgendaProgressItemNode(tree, "item-a1-1", "agenda-1", "fact")
	state1 := evaluateAgendaProgress(agendaProgressInputs{
		MC: mc, Tree: tree, Items: []liveAnalysisItem{itemA1}, DiffItems: []liveAnalysisItem{itemA1},
		RoundSeqNos: []int64{1, 2}, Timeline: timelineWithPrimaryRoles(1, 2), TreeVersion: 1,
	})
	if state1.ComputedCurrentTopicID != "agenda-1" {
		t.Fatalf("v1 current = %s, want agenda-1", state1.ComputedCurrentTopicID)
	}

	itemA2 := liveAnalysisItem{ID: "item-a2-1", Kind: "issue", EvidenceSequenceNos: []int64{3, 4}}
	tree.Nodes = append(tree.Nodes, liveAnalysisTreeNode{ID: "item-a2-1", Kind: "issue", ParentID: "agenda-2"})
	state2 := evaluateAgendaProgress(agendaProgressInputs{
		Previous: state1, MC: mc, Tree: tree, Items: []liveAnalysisItem{itemA1, itemA2}, DiffItems: []liveAnalysisItem{itemA2},
		RoundSeqNos: []int64{3, 4}, Timeline: timelineWithPrimaryRoles(3, 4), TreeVersion: 2,
	})
	if state2.ComputedCurrentTopicID != "agenda-1" {
		t.Fatalf("v2 current = %s, want agenda-1 (one round of a new leader must not switch yet)", state2.ComputedCurrentTopicID)
	}
	if state2.CandidateTopicID != "agenda-2" || state2.CandidateRounds != 1 {
		t.Fatalf("v2 candidate tracking = %q/%d, want agenda-2/1", state2.CandidateTopicID, state2.CandidateRounds)
	}

	itemA2b := liveAnalysisItem{ID: "item-a2-2", Kind: "issue", EvidenceSequenceNos: []int64{5, 6}}
	tree.Nodes = append(tree.Nodes, liveAnalysisTreeNode{ID: "item-a2-2", Kind: "issue", ParentID: "agenda-2"})
	state3 := evaluateAgendaProgress(agendaProgressInputs{
		Previous: state2, MC: mc, Tree: tree, Items: []liveAnalysisItem{itemA1, itemA2, itemA2b}, DiffItems: []liveAnalysisItem{itemA2b},
		RoundSeqNos: []int64{5, 6}, Timeline: timelineWithPrimaryRoles(5, 6), TreeVersion: 3,
	})
	if state3.ComputedCurrentTopicID != "agenda-2" {
		t.Fatalf("v3 current = %s, want agenda-2 (two consecutive leader rounds must switch)", state3.ComputedCurrentTopicID)
	}
}

func TestAgendaProgressCurrentTopicExplicitTransitionSwitchesImmediately(t *testing.T) {
	mc := agendaProgressTestMC(
		agendaItem{ID: "agenda-1", Title: "議題A", Order: 1, Role: agendaRolePrimary},
		agendaItem{ID: "agenda-2", Title: "議題B", Order: 2, Role: agendaRolePrimary},
	)
	tree := agendaProgressTestTree("agenda-1", "agenda-2")
	itemA1 := liveAnalysisItem{ID: "item-a1-1", Kind: "fact", EvidenceSequenceNos: []int64{1, 2}}
	addAgendaProgressItemNode(tree, "item-a1-1", "agenda-1", "fact")
	state1 := evaluateAgendaProgress(agendaProgressInputs{
		MC: mc, Tree: tree, Items: []liveAnalysisItem{itemA1}, DiffItems: []liveAnalysisItem{itemA1},
		RoundSeqNos: []int64{1, 2}, Timeline: timelineWithPrimaryRoles(1, 2), TreeVersion: 1,
	})
	if state1.ComputedCurrentTopicID != "agenda-1" {
		t.Fatalf("v1 current = %s, want agenda-1", state1.ComputedCurrentTopicID)
	}

	// A single utterance with an explicit fixed-agenda transition switches
	// current immediately, without needing a second round.
	spans := []agendaContextSpan{{Mode: agendaContextModeFixed, AgendaID: "agenda-2", StartSequenceNo: 3, EndSequenceNo: 3, Explicit: true}}
	state2 := evaluateAgendaProgress(agendaProgressInputs{
		Previous: state1, MC: mc, Tree: tree, Items: []liveAnalysisItem{itemA1}, DiffItems: nil,
		RoundSeqNos: []int64{3}, Timeline: timelineWithPrimaryRoles(3), Spans: spans, TreeVersion: 2,
	})
	if state2.ComputedCurrentTopicID != "agenda-2" {
		t.Fatalf("v2 current = %s, want agenda-2 (explicit transition)", state2.ComputedCurrentTopicID)
	}
	if state2.CandidateTopicID != "" || state2.CandidateRounds != 0 {
		t.Fatalf("v2 candidate tracking should be cleared by an explicit switch: %q/%d", state2.CandidateTopicID, state2.CandidateRounds)
	}
}

func TestAgendaProgressCurrentTopicNoAgendaDoesNotClearImmediately(t *testing.T) {
	mc := agendaProgressTestMC(agendaItem{ID: "agenda-1", Title: "議題A", Order: 1, Role: agendaRolePrimary})
	tree := agendaProgressTestTree("agenda-1")
	itemA1 := liveAnalysisItem{ID: "item-a1-1", Kind: "fact", EvidenceSequenceNos: []int64{1, 2}}
	addAgendaProgressItemNode(tree, "item-a1-1", "agenda-1", "fact")
	state := evaluateAgendaProgress(agendaProgressInputs{
		MC: mc, Tree: tree, Items: []liveAnalysisItem{itemA1}, DiffItems: []liveAnalysisItem{itemA1},
		RoundSeqNos: []int64{1, 2}, Timeline: timelineWithPrimaryRoles(1, 2), TreeVersion: 1,
	})
	if state.ComputedCurrentTopicID != "agenda-1" {
		t.Fatalf("v1 current = %s, want agenda-1", state.ComputedCurrentTopicID)
	}
	emptyRound := func(previous *agendaProgressState, version int64) *agendaProgressState {
		return evaluateAgendaProgress(agendaProgressInputs{
			Previous: previous, MC: mc, Tree: tree, Items: []liveAnalysisItem{itemA1}, DiffItems: nil,
			RoundSeqNos: nil, Timeline: discourseTimeline{Roles: map[int64]liveEvidenceRole{}}, TreeVersion: version,
		})
	}
	state2 := emptyRound(state, 2)
	if state2.ComputedCurrentTopicID != "agenda-1" {
		t.Fatalf("v2 current = %s, want agenda-1 (1 leaderless round must not clear)", state2.ComputedCurrentTopicID)
	}
	state3 := emptyRound(state2, 3)
	if state3.ComputedCurrentTopicID != "agenda-1" {
		t.Fatalf("v3 current = %s, want agenda-1 (2 leaderless rounds must not clear)", state3.ComputedCurrentTopicID)
	}
	state4 := emptyRound(state3, 4)
	if state4.ComputedCurrentTopicID != "" {
		t.Fatalf("v4 current = %q, want cleared after 3 consecutive leaderless rounds", state4.ComputedCurrentTopicID)
	}
}

// --- weight (§2.4) ------------------------------------------------------------

func TestAgendaProgressWeightDoesNotDoubleCountMultiAgendaEvidence(t *testing.T) {
	mc := agendaProgressTestMC(
		agendaItem{ID: "agenda-1", Title: "議題A", Order: 1, Role: agendaRolePrimary},
		agendaItem{ID: "agenda-2", Title: "議題B", Order: 2, Role: agendaRolePrimary},
	)
	tree := agendaProgressTestTree("agenda-1", "agenda-2")
	// item-1's tree parent is agenda-1, but it also references agenda-2 via
	// RelatedAgendaIDs (a cross-cutting reference, e.g. an Action Summary
	// projection). Its evidence must count only once, toward agenda-1.
	addAgendaProgressItemNode(tree, "item-1", "agenda-1", "todo")
	item1 := liveAnalysisItem{ID: "item-1", Kind: "todo", EvidenceSequenceNos: []int64{1, 2}, RelatedAgendaIDs: []string{"agenda-2"}}
	stats := &liveAnalysisTreeMergeStats{}
	state := evaluateAgendaProgress(agendaProgressInputs{
		MC: mc, Tree: tree, Items: []liveAnalysisItem{item1}, DiffItems: []liveAnalysisItem{item1},
		RoundSeqNos: []int64{1, 2}, Timeline: timelineWithPrimaryRoles(1, 2), TreeVersion: 1, Stats: stats,
	})
	agenda1 := agendaProgressEntryByID(state, "agenda-1")
	agenda2 := agendaProgressEntryByID(state, "agenda-2")
	if agenda1 == nil || agenda1.WeightRaw <= 0 {
		t.Fatalf("agenda-1 weightRaw = %+v, want > 0", agenda1)
	}
	if agenda2 == nil || agenda2.WeightRaw != 0 || agenda2.ComputedStatus != agendaProgressNotStarted {
		t.Fatalf("agenda-2 = %+v, want untouched (no double counting)", agenda2)
	}
	if stats.AgendaProgressMultiAgendaEvidenceCount == 0 {
		t.Fatalf("multiAgendaEvidenceCount stat = 0, want > 0 (observability only, not double-counted)")
	}
}

func TestAgendaProgressWeightNormalizesRelativelyAndOmitsNotStarted(t *testing.T) {
	mc := agendaProgressTestMC(
		agendaItem{ID: "agenda-1", Title: "議題A", Order: 1, Role: agendaRolePrimary},
		agendaItem{ID: "agenda-2", Title: "議題B", Order: 2, Role: agendaRolePrimary},
		agendaItem{ID: "agenda-3", Title: "議題C", Order: 3, Role: agendaRolePrimary},
	)
	tree := agendaProgressTestTree("agenda-1", "agenda-2", "agenda-3")
	addAgendaProgressItemNode(tree, "item-a1", "agenda-1", "fact")
	addAgendaProgressItemNode(tree, "item-a2", "agenda-2", "issue")
	itemA1 := liveAnalysisItem{ID: "item-a1", Kind: "fact", EvidenceSequenceNos: []int64{1, 2, 3, 4}}
	itemA2 := liveAnalysisItem{ID: "item-a2", Kind: "issue", EvidenceSequenceNos: []int64{5, 6}}
	state := evaluateAgendaProgress(agendaProgressInputs{
		MC: mc, Tree: tree, Items: []liveAnalysisItem{itemA1, itemA2}, DiffItems: []liveAnalysisItem{itemA1, itemA2},
		RoundSeqNos: []int64{1, 2, 3, 4, 5, 6}, Timeline: timelineWithPrimaryRoles(1, 2, 3, 4, 5, 6), TreeVersion: 1,
	})
	agenda1 := agendaProgressEntryByID(state, "agenda-1")
	agenda2 := agendaProgressEntryByID(state, "agenda-2")
	agenda3 := agendaProgressEntryByID(state, "agenda-3")
	if agenda1 == nil || agenda1.DiscussionWeight != 1 {
		t.Fatalf("agenda-1 (max weight) = %+v, want discussionWeight=1", agenda1)
	}
	if agenda2 == nil || agenda2.DiscussionWeight <= 0 || agenda2.DiscussionWeight >= 1 {
		t.Fatalf("agenda-2 discussionWeight = %+v, want strictly between 0 and 1", agenda2)
	}
	if agenda3 == nil || agenda3.ComputedStatus != agendaProgressNotStarted || agenda3.DiscussionWeight != 0 {
		t.Fatalf("agenda-3 (not_started) = %+v, want discussionWeight omitted (0)", agenda3)
	}
}

// --- outcome (§2.7) ------------------------------------------------------------

func TestAgendaProgressOutcomeDecisionExpectation(t *testing.T) {
	entry := &agendaProgressEntry{ID: "agenda-1", OutcomeExpectation: outcomeExpectationDecision, ActiveRounds: 2}
	withDecision := []liveAnalysisItem{{ID: "d1", Kind: "decision"}}
	if got := computeAgendaOutcomeStatus(entry, true, withDecision); got != agendaOutcomeConcluded {
		t.Fatalf("decision present outcome = %q, want concluded", got)
	}
	if got := computeAgendaOutcomeStatus(entry, true, nil); got != agendaOutcomeUnresolved {
		t.Fatalf("decision absent (active >=2) outcome = %q, want unresolved", got)
	}
}

func TestAgendaProgressOutcomeNoneExpectationNeverUnresolved(t *testing.T) {
	entry := &agendaProgressEntry{ID: "agenda-1", OutcomeExpectation: outcomeExpectationNone, ActiveRounds: 5}
	if got := computeAgendaOutcomeStatus(entry, true, nil); got != "" {
		t.Fatalf("none expectation with no related items = %q, want no label", got)
	}
	withDecision := []liveAnalysisItem{{ID: "d1", Kind: "decision"}}
	if got := computeAgendaOutcomeStatus(entry, true, withDecision); got != agendaOutcomeConcluded {
		t.Fatalf("none expectation with an incidental decision = %q, want concluded", got)
	}
}

func TestAgendaProgressOutcomeCollectionExpectation(t *testing.T) {
	entry := &agendaProgressEntry{ID: "agenda-1", OutcomeExpectation: outcomeExpectationCollection, ActiveRounds: 5}
	oneRelated := []liveAnalysisItem{{ID: "i1", Kind: "issue"}}
	if got := computeAgendaOutcomeStatus(entry, true, oneRelated); got != "" {
		t.Fatalf("collection with 1 related item = %q, want no label (never unresolved)", got)
	}
	twoRelated := []liveAnalysisItem{{ID: "i1", Kind: "issue"}, {ID: "r1", Kind: "risk"}}
	if got := computeAgendaOutcomeStatus(entry, true, twoRelated); got != agendaOutcomeConcluded {
		t.Fatalf("collection with issue+risk = %q, want concluded", got)
	}
}

func TestClassifyAgendaOutcomeExpectationSpecificPatternsBeforeDecision(t *testing.T) {
	// 「担当者を決める」「期限を決める」は「決め」でdecisionにもマッチするが、
	// 要求される成果はdecision itemではなくowner/due付きTODO(§7.1)。
	cases := []struct{ title, want string }{
		{"担当者を決める", outcomeExpectationOwnerTodo},
		{"期限を決める", outcomeExpectationDueTodo},
		{"改修案を比較して決定する", outcomeExpectationDecision},
		{"課題を洗い出す", outcomeExpectationCollection},
		{"現状を共有する", outcomeExpectationNone},
	}
	for _, tc := range cases {
		if got := classifyAgendaOutcomeExpectation(tc.title); got != tc.want {
			t.Fatalf("classifyAgendaOutcomeExpectation(%q) = %q, want %q", tc.title, got, tc.want)
		}
	}
}

// --- additional topics (§2.8) --------------------------------------------------

func TestAgendaProgressAdditionalTopicSingleRoundCandidateNotDisplayed(t *testing.T) {
	mc := agendaProgressTestMC(agendaItem{ID: "agenda-1", Title: "議題A", Order: 1, Role: agendaRolePrimary})
	tree := agendaProgressTestTree("agenda-1")
	candidate := emergingTopicCandidate{ID: "topic-side", Label: "サイドトピック", RoundCount: 1, EvidenceItemIDs: []string{"item-side-1"}}
	state := evaluateAgendaProgress(agendaProgressInputs{
		MC: mc, Tree: tree, Emerging: []emergingTopicCandidate{candidate}, TreeVersion: 1,
		Timeline: discourseTimeline{Roles: map[int64]liveEvidenceRole{}},
	})
	if entry := agendaProgressEntryByID(state, "topic-side"); entry != nil {
		t.Fatalf("single-round candidate must not be displayed yet: %+v", entry)
	}
}

func TestAgendaProgressAdditionalTopicTwoRoundsDisplayedAndPromotionCarriesTracking(t *testing.T) {
	mc := agendaProgressTestMC(agendaItem{ID: "agenda-1", Title: "議題A", Order: 1, Role: agendaRolePrimary})
	tree := agendaProgressTestTree("agenda-1")

	candidateRound2 := emergingTopicCandidate{ID: "topic-side", Label: "サイドトピック", RoundCount: 2, EvidenceItemIDs: []string{"item-side-1", "item-side-2"}}
	itemSide2 := liveAnalysisItem{ID: "item-side-2", Kind: "issue", CandidateTopicID: "topic-side", EvidenceSequenceNos: []int64{1, 2}}
	state2 := evaluateAgendaProgress(agendaProgressInputs{
		MC: mc, Tree: tree, Items: []liveAnalysisItem{itemSide2}, DiffItems: []liveAnalysisItem{itemSide2},
		Emerging:    []emergingTopicCandidate{candidateRound2},
		RoundSeqNos: []int64{1, 2}, Timeline: timelineWithPrimaryRoles(1, 2), TreeVersion: 2,
	})
	entry2 := agendaProgressEntryByID(state2, "topic-side")
	if entry2 == nil || entry2.ComputedStatus != agendaProgressDiscussing || entry2.SourceType != agendaProgressSourceDynamic {
		t.Fatalf("round-2 candidate = %+v, want a displayed discussing dynamic entry", entry2)
	}
	weightAfterRound2 := entry2.WeightRaw
	if weightAfterRound2 <= 0 {
		t.Fatalf("round-2 candidate weightRaw = %v, want > 0", weightAfterRound2)
	}

	// Round 3 uses a legacy persisted dynamic node whose ID equals the
	// candidate entry ID. New payloads use SourceCandidateID + a distinct
	// topic-dynamic-* ID, while this compatibility path must keep tracking.
	addAgendaProgressDynamicTopicNode(tree, "topic-side", "サイドトピック(昇格後)")
	itemSide3 := liveAnalysisItem{ID: "item-side-3", Kind: "issue", EvidenceSequenceNos: []int64{3, 4}}
	tree.Nodes = append(tree.Nodes, liveAnalysisTreeNode{ID: "item-side-3", Kind: "issue", ParentID: "topic-side"})
	state3 := evaluateAgendaProgress(agendaProgressInputs{
		Previous: state2, MC: mc, Tree: tree, Items: []liveAnalysisItem{itemSide2, itemSide3}, DiffItems: []liveAnalysisItem{itemSide3},
		RoundSeqNos: []int64{3, 4}, Timeline: timelineWithPrimaryRoles(3, 4), TreeVersion: 3,
	})
	entry3 := agendaProgressEntryByID(state3, "topic-side")
	if entry3 == nil {
		t.Fatalf("promoted topic-side entry missing after promotion")
	}
	if entry3.WeightRaw <= weightAfterRound2 {
		t.Fatalf("promoted entry weightRaw = %v, want > round-2 weightRaw %v (tracking carried over)", entry3.WeightRaw, weightAfterRound2)
	}
	if entry3.ActiveRounds != 2 {
		t.Fatalf("promoted entry activeRounds = %d, want 2 (both rounds counted)", entry3.ActiveRounds)
	}
	if entry3.Title != "サイドトピック(昇格後)" {
		t.Fatalf("promoted entry title = %q, want the promoted node's label", entry3.Title)
	}
}

func TestAgendaProgressDynamicEntryDroppedWhenBackingDisappears(t *testing.T) {
	mc := agendaProgressTestMC(agendaItem{ID: "agenda-1", Title: "議題A", Order: 1, Role: agendaRolePrimary})
	tree := agendaProgressTestTree("agenda-1")
	previous := &agendaProgressState{Entries: []agendaProgressEntry{
		{ID: "agenda-1", SourceType: agendaProgressSourceFixed, Title: "議題A", Order: 1, ComputedStatus: agendaProgressDiscussing},
		{ID: "topic-side", SourceType: agendaProgressSourceDynamic, Title: "サイドトピック", ComputedStatus: agendaProgressDiscussing, WeightRaw: 3},
	}}
	// このラウンドでは topic-side はdynamic topic nodeでも表示条件を満たす
	// candidateでもない(= agenda topicへ統合された/candidateがinactive化した)。
	// ゴースト行として残さず追加論点から除去する。
	state := evaluateAgendaProgress(agendaProgressInputs{
		Previous: previous, MC: mc, Tree: tree, TreeVersion: 5,
		Emerging: []emergingTopicCandidate{{ID: "topic-side", Label: "サイドトピック", RoundCount: 3, Inactive: true}},
		Timeline: discourseTimeline{Roles: map[int64]liveEvidenceRole{}},
	})
	if entry := agendaProgressEntryByID(state, "topic-side"); entry != nil {
		t.Fatalf("dynamic entry without backing must be dropped, got %+v", entry)
	}
	if entry := agendaProgressEntryByID(state, "agenda-1"); entry == nil {
		t.Fatalf("fixed agenda entry must remain")
	}
}

// --- stamp (§2.10) --------------------------------------------------------------

func TestApplyAgendaProgressOverridesManualTakesPriority(t *testing.T) {
	state := &agendaProgressState{
		ComputedCurrentTopicID: "agenda-1",
		Entries: []agendaProgressEntry{
			{ID: "agenda-1", ComputedStatus: agendaProgressDiscussing},
			{ID: "agenda-2", ComputedStatus: agendaProgressNotStarted},
		},
	}
	overrides := &AgendaProgressOverrides{StatusOverrides: map[string]string{"agenda-1": agendaProgressDiscussed}, CurrentTopicID: "agenda-2"}
	stamped := applyAgendaProgressOverrides(state, overrides)

	entry1 := agendaProgressEntryByID(stamped, "agenda-1")
	if entry1 == nil || entry1.ManualStatus != agendaProgressDiscussed || entry1.EffectiveStatus != agendaProgressDiscussed {
		t.Fatalf("stamped agenda-1 = %+v, want manual/effective=discussed", entry1)
	}
	if entry1.ComputedStatus != agendaProgressDiscussing {
		t.Fatalf("stamped agenda-1 computedStatus changed = %+v, want untouched", entry1)
	}
	if stamped.ManualCurrentTopicID != "agenda-2" || stamped.EffectiveCurrentTopicID != "agenda-2" {
		t.Fatalf("stamped current = manual=%q effective=%q, want agenda-2/agenda-2", stamped.ManualCurrentTopicID, stamped.EffectiveCurrentTopicID)
	}
	// The original (unstamped) state must never be mutated by stamping.
	if state.Entries[0].ManualStatus != "" || state.Entries[0].EffectiveStatus != "" || state.ManualCurrentTopicID != "" {
		t.Fatalf("original state was mutated by stamping: %+v", state)
	}
}

func TestApplyAgendaProgressOverridesRevertToComputed(t *testing.T) {
	state := &agendaProgressState{
		ComputedCurrentTopicID: "agenda-1",
		Entries:                []agendaProgressEntry{{ID: "agenda-1", ComputedStatus: agendaProgressDiscussing}},
	}
	stamped := applyAgendaProgressOverrides(state, nil)
	entry := agendaProgressEntryByID(stamped, "agenda-1")
	if entry == nil || entry.ManualStatus != "" || entry.EffectiveStatus != agendaProgressDiscussing {
		t.Fatalf("stamped with nil overrides = %+v, want effective falls back to computed", entry)
	}
	if stamped.ManualCurrentTopicID != "" || stamped.EffectiveCurrentTopicID != "agenda-1" {
		t.Fatalf("stamped current = manual=%q effective=%q, want empty/agenda-1", stamped.ManualCurrentTopicID, stamped.EffectiveCurrentTopicID)
	}
}

func TestApplyAgendaProgressOverridesIgnoresUnknownEntry(t *testing.T) {
	state := &agendaProgressState{
		ComputedCurrentTopicID: "agenda-1",
		Entries:                []agendaProgressEntry{{ID: "agenda-1", ComputedStatus: agendaProgressDiscussing}},
	}
	overrides := &AgendaProgressOverrides{StatusOverrides: map[string]string{"agenda-missing": agendaProgressDiscussed}, CurrentTopicID: "agenda-missing"}
	stamped := applyAgendaProgressOverrides(state, overrides)
	entry := agendaProgressEntryByID(stamped, "agenda-1")
	if entry == nil || entry.ManualStatus != "" || entry.EffectiveStatus != agendaProgressDiscussing {
		t.Fatalf("stamped agenda-1 = %+v, want unaffected by an override for an unknown entry", entry)
	}
	if stamped.ManualCurrentTopicID != "" || stamped.EffectiveCurrentTopicID != "agenda-1" {
		t.Fatalf("stamped current = manual=%q effective=%q, want the unknown override ignored (falls back to computed)", stamped.ManualCurrentTopicID, stamped.EffectiveCurrentTopicID)
	}
}

// --- sanitize compatibility (§2.11) ---------------------------------------------

func TestSanitizeLiveAnalysisSynthesizesAgendaProgressForLegacyPayload(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-1", Title: "渡り鳥", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "議題B", Order: 2, Role: agendaRolePrimary},
	}}
	// A legacy payload predating AgendaProgress: agenda-1 is materialized with
	// a grounded (non-topic/group) descendant, so reconcileAgendaAnchors will
	// mark it "discussed"; agenda-2 has no topic at all ("planned").
	legacy := liveAnalysisPayload{
		Items: []liveAnalysisItem{{ID: "item-1", Kind: "fact", Title: "個体数調査", Status: "open"}},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議"},
			{ID: "topic-agenda-1", Kind: "topic", ParentID: treeRootNodeID, Label: "渡り鳥", Origin: topicOriginAgenda, AgendaRole: agendaRolePrimary, AgendaRefs: []string{"agenda-1"}, Materialized: true},
			{ID: "item-1", Kind: "fact", ParentID: "topic-agenda-1", Label: "個体数調査"},
		}},
	}
	payload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	stored := &domain.MeetingAIAnalysis{SessionID: "session", Type: domain.MeetingAIAnalysisLive, Version: 1, Payload: payload}

	delivered := sanitizeLiveAnalysisForDelivery(stored, mc, TreeClassificationConfig{})
	state := previousLiveAnalysisState(delivered.Payload)
	if state.AgendaProgress == nil {
		t.Fatalf("legacy payload must get a synthesized agendaProgress projection")
	}
	agenda1 := agendaProgressEntryByID(state.AgendaProgress, "agenda-1")
	if agenda1 == nil || agenda1.ComputedStatus != agendaProgressDiscussed {
		t.Fatalf("synthesized agenda-1 = %+v, want discussed (grounded descendant)", agenda1)
	}
	agenda2 := agendaProgressEntryByID(state.AgendaProgress, "agenda-2")
	if agenda2 == nil || agenda2.ComputedStatus != agendaProgressNotStarted {
		t.Fatalf("synthesized agenda-2 = %+v, want not_started (never materialized)", agenda2)
	}
	if string(stored.Payload) != string(payload) {
		t.Fatalf("sanitizer must not mutate the stored payload")
	}
}

func TestSanitizeLiveAnalysisRefreshesStaleAgendaProgressNodeRefs(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "渡り鳥", Order: 1, Role: agendaRolePrimary}}}
	current := liveAnalysisPayload{
		Items: []liveAnalysisItem{{ID: "item-1", Kind: "fact", Title: "個体数調査", Status: "open"}},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議"},
			{ID: "topic-agenda-1", Kind: "topic", ParentID: treeRootNodeID, Label: "渡り鳥", Origin: topicOriginAgenda, AgendaRole: agendaRolePrimary, AgendaRefs: []string{"agenda-1"}, Materialized: true},
			{ID: "item-1", Kind: "fact", ParentID: "topic-agenda-1", Label: "個体数調査"},
		}},
		AgendaProgress: &agendaProgressState{
			Entries: []agendaProgressEntry{{
				ID: "agenda-1", SourceType: agendaProgressSourceFixed, Title: "渡り鳥", Order: 1,
				ComputedStatus: agendaProgressDiscussed,
				// primaryNodeId/materializedTopicIds reference a node that no
				// longer exists in the tree (e.g. after a repair/merge).
				PrimaryNodeID:        "topic-agenda-1-stale",
				MaterializedTopicIDs: []string{"topic-agenda-1-stale"},
			}},
		},
	}
	payload, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	stored := &domain.MeetingAIAnalysis{SessionID: "session", Type: domain.MeetingAIAnalysisLive, Version: 1, Payload: payload}

	delivered := sanitizeLiveAnalysisForDelivery(stored, mc, TreeClassificationConfig{})
	state := previousLiveAnalysisState(delivered.Payload)
	agenda1 := agendaProgressEntryByID(state.AgendaProgress, "agenda-1")
	if agenda1 == nil {
		t.Fatalf("agenda-1 entry missing after sanitize")
	}
	if agenda1.PrimaryNodeID == "topic-agenda-1-stale" || containsExactString(agenda1.MaterializedTopicIDs, "topic-agenda-1-stale") {
		t.Fatalf("agenda-1 = %+v, want the stale node reference removed", agenda1)
	}
}
