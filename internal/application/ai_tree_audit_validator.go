package application

import (
	"encoding/json"
	"fmt"
	"strings"

	"deciscope-core-api/internal/domain"
)

func validateAndDryRunTreeAuditOperations(original liveAnalysisPayload, operations []treeAuditOperation, segments []domain.TranscriptSegment, mc *meetingContext, evidenceRoles map[int64]treeAuditEvidenceRole, cfg TreeAuditConfig, runID string, resultingVersion int64, markApplied bool) (liveAnalysisPayload, treeAuditValidatorResult) {
	cfg = cfg.normalized()
	legacyAgendaTopicRemap := normalizeLegacyAgendaTopicIDs(&original, mc, nil)
	if len(legacyAgendaTopicRemap) > 0 {
		for index := range operations {
			if canonical := legacyAgendaTopicRemap[strings.TrimSpace(operations[index].TargetCanonicalNodeID)]; canonical != "" {
				operations[index].TargetCanonicalNodeID = canonical
			}
			if canonical := legacyAgendaTopicRemap[strings.TrimSpace(operations[index].FromParentCanonicalNodeID)]; canonical != "" {
				operations[index].FromParentCanonicalNodeID = canonical
			}
			if canonical := legacyAgendaTopicRemap[strings.TrimSpace(operations[index].ToParentCanonicalNodeID)]; canonical != "" {
				operations[index].ToParentCanonicalNodeID = canonical
			}
		}
	}
	dry := cloneLiveAnalysisPayload(original)
	result := treeAuditValidatorResult{OperationsProposed: len(operations)}
	if original.Tree != nil {
		result.NodeCountBefore = len(original.Tree.Nodes)
	}
	accepted := make(map[string]bool, len(operations))
	segmentText := make(map[int64]string, len(segments))
	for _, segment := range segments {
		segmentText[segment.SequenceNo] = segment.Text
	}
	beforeFindings := deterministicTreeAuditPrecheck(original, mc, evidenceRoles, cfg)
	beforeQuality := auditHeuristicDefectCount(beforeFindings)
	result.HeuristicDefectCountBefore = beforeQuality
	result.LowInformationItemsBefore = countTreeAuditPrechecks(beforeFindings, TreeAuditLowInformationItem, TreeAuditLowInformationTitle, TreeAuditStatusOnlyNode, TreeAuditAnaphoraWithoutReferent)
	result.TopicOutliersBefore = countTreeAuditPrechecks(beforeFindings, TreeAuditTopicOutlier, TreeAuditSubjectMismatch)
	result.CandidateFragmentationBefore = countTreeAuditPrechecks(beforeFindings, TreeAuditCandidateFragmentation)
	result.CrossAgendaContaminationBefore = countTreeAuditPrechecks(beforeFindings, TreeAuditCrossAgendaContamination)

	for _, operation := range operations {
		evaluation := treeAuditValidatorEvaluation{OperationID: operation.OperationID, Type: operation.Type, Result: "rejected", ModelConfidence: operation.Confidence}
		classification := treeAuditOperationClassification(operation.Type)
		reject := func(reason string) {
			evaluation.Reason = reason
			if classification == treeAuditOperationUnsupported {
				evaluation.Category = "unsupported"
			} else {
				evaluation.Category = "unsafe"
			}
			result.Evaluations = append(result.Evaluations, evaluation)
		}
		dependencyOK := true
		for _, dependency := range operation.DependsOnOperationIDs {
			if !accepted[dependency] {
				dependencyOK = false
				break
			}
		}
		if !dependencyOK {
			reject("dependency_rejected")
			continue
		}
		if classification == treeAuditOperationUnsupported {
			reject("unsupported_operation")
			continue
		}
		isMoveType := treeAuditOperationIsMoveType(operation.Type)
		if treeAuditManualEditProtectedOperation(operation, dry) {
			reject("manual_edit_protected")
			continue
		}
		// The effective-confidence gate (design D4) only adjusts move-type
		// operations, which are the only ones with a meaningful "current
		// parent" to corroborate or discount against. Every other applicable
		// operation type is still gated on the model's own reported
		// confidence, unchanged.
		effectiveConfidence := operation.Confidence
		if isMoveType {
			effectiveConfidence = treeAuditEffectiveConfidence(operation, dry, beforeFindings, evidenceRoles, segmentText, mc, cfg)
		}
		evaluation.EffectiveConfidence = effectiveConfidence
		if effectiveConfidence < cfg.HighConfidenceThreshold {
			reject("below_effective_confidence_threshold")
			continue
		}

		candidate := cloneLiveAnalysisPayload(dry)
		currentScore, newScore, reason := applyOneTreeAuditOperation(&candidate, operation, segmentText, evidenceRoles, mc, cfg, runID, resultingVersion, beforeFindings)
		evaluation.CurrentParentScore = currentScore
		evaluation.NewParentScore = newScore
		evaluation.Improvement = newScore - currentScore
		if reason != "" {
			reject(reason)
			continue
		}
		// Any container the operation just took a child away from (by moving,
		// merging away, or deactivating it) may now be empty; prune it - and
		// cascade upward through its own ancestors - before checking integrity,
		// so an operation that legitimately empties a group/dynamic topic is
		// not itself rejected by the emptiness it just created (design brief
		// D5 addendum / §9.2).
		treeAuditCascadePruneEmptyContainers(&candidate, dry)
		integrity := validateTreeIntegrity(candidate.Tree, candidate.Items, mc)
		if !integrity.Valid {
			reject("tree_integrity_rejected")
			continue
		}
		afterFindings := deterministicTreeAuditPrecheck(candidate, mc, evidenceRoles, cfg)
		afterQuality := auditHeuristicDefectCount(afterFindings)
		worsened := afterQuality > beforeQuality
		// The non-worsening gate exists to catch an operation's side effects
		// on the rest of the tree, not to re-adjudicate the very placement
		// decision the fixed-agenda-return exemption (design brief D5
		// addendum / §8.2) already made a structural/confidence judgment
		// call on. A fixed-agenda-return move's own moved item routinely
		// still carries a subject_mismatch/cross_agenda_contamination
		// finding against its short, low-bigram-surface fixed-agenda
		// destination (or, symmetrically, already carried one against its
		// old dynamic-topic home before the move) - that is exactly the
		// surface-similarity gap the exemption was built to see past, so it
		// must not silently reject the same operation one gate later. When
		// (and only when) this operation is fixed-agenda-return exempt, and
		// only when the plain gate above would have rejected it, recompute
		// the before/after defect counts with the moved item's own
		// subject_mismatch/cross_agenda_contamination findings excluded from
		// *both* sides symmetrically - a finding is only ever excluded if it
		// names the moved item and no one else, so any defect the operation
		// introduces or leaves behind on other nodes, candidates, or groups
		// still counts normally.
		if worsened && treeAuditFixedAgendaReturnExempt(operation, dry, evidenceRoles, segmentText, cfg) {
			beforeFindingsForOperation := deterministicTreeAuditPrecheck(dry, mc, evidenceRoles, cfg)
			filteredBefore := auditHeuristicDefectCount(treeAuditExcludeSelfSubjectFindings(beforeFindingsForOperation, operation.TargetCanonicalItemID))
			filteredAfter := auditHeuristicDefectCount(treeAuditExcludeSelfSubjectFindings(afterFindings, operation.TargetCanonicalItemID))
			worsened = filteredAfter > filteredBefore
		}
		if worsened {
			reject("heuristic_structural_quality_worsened")
			continue
		}
		dry = candidate
		// beforeQuality always advances by the unfiltered afterQuality, never
		// the filtered one: the next operation in this batch must still be
		// judged against the tree's real, complete defect count.
		beforeQuality = afterQuality
		accepted[operation.OperationID] = true
		evaluation.Result = "validated"
		evaluation.Valid = true
		evaluation.Applied = markApplied
		result.OperationsValid++
		if markApplied {
			result.OperationsApplied++
			switch operation.Type {
			case TreeAuditRewriteItem, TreeAuditRewriteItemTitle, TreeAuditRewriteItemDescription:
				result.RewritesApplied++
			case TreeAuditMergeItems:
				result.MergesApplied++
			case TreeAuditReclassifyKind, TreeAuditReclassifySubtype:
				result.ReclassificationsApplied++
			case TreeAuditDeactivateItem:
				result.DeactivationsApplied++
			}
		}
		result.Evaluations = append(result.Evaluations, evaluation)
	}
	result.OperationsRejected = result.OperationsProposed - result.OperationsValid
	result.TreeIntegrityValid = validateTreeIntegrity(dry.Tree, dry.Items, mc).Valid
	afterFindings := deterministicTreeAuditPrecheck(dry, mc, evidenceRoles, cfg)
	result.TopicOutliersAfter = countTreeAuditPrechecks(afterFindings, TreeAuditTopicOutlier, TreeAuditSubjectMismatch)
	result.CandidateFragmentationAfter = countTreeAuditPrechecks(afterFindings, TreeAuditCandidateFragmentation)
	result.CrossAgendaContaminationAfter = countTreeAuditPrechecks(afterFindings, TreeAuditCrossAgendaContamination)
	result.HeuristicDefectCountAfter = auditHeuristicDefectCount(afterFindings)
	result.LowInformationItemsAfter = countTreeAuditPrechecks(afterFindings, TreeAuditLowInformationItem, TreeAuditLowInformationTitle, TreeAuditStatusOnlyNode, TreeAuditAnaphoraWithoutReferent)
	if dry.Tree != nil {
		result.NodeCountAfter = len(dry.Tree.Nodes)
	}
	if result.OperationsValid > 0 {
		dry.TreeVersion = resultingVersion
		dry.ChangeSource = "tree_auditor"
		dry.AuditRunID = runID
		dry.BasedOnTreeVersion = original.TreeVersion
		dry.TreeChanges = diffLiveAnalysisTrees(original.Tree, dry.Tree, resultingVersion)
		if dry.TreeChanges == nil {
			dry.TreeChanges = &liveAnalysisTreeChanges{TreeVersion: resultingVersion}
		}
		dry.TreeChanges.Source = "tree_auditor"
		dry.TreeChanges.AuditRunID = runID
	}
	dry.AgendaAnchors = reconcileAgendaAnchors(dry.AgendaAnchors, mc, dry.Tree, dry.Items, dry.TreeVersion, false)
	return dry, result
}

// treeAuditOperationClass is the coarse two-value classification of an
// operation type: "applicable" operations have a dedicated applier below and
// may be safely applied when their operation-specific conditions hold;
// "unsupported" operations are recognized by the v3 schema so the model may
// mention them, but are always rejected with reason "unsupported_operation"
// regardless of confidence, because no applier (and no focused safety tests)
// exists for them yet.
type treeAuditOperationClass string

const (
	treeAuditOperationApplicable  treeAuditOperationClass = "applicable"
	treeAuditOperationUnsupported treeAuditOperationClass = "unsupported"
)

// treeAuditOperationClassification is intentionally narrow: every operation
// type not listed in the applicable branch is unsupported, regardless of
// model confidence. Expanding the applicable set requires adding
// operation-specific safety invariants in applyOneTreeAuditOperation and
// focused tests.
func treeAuditOperationClassification(operationType TreeAuditOperationType) treeAuditOperationClass {
	switch operationType {
	case TreeAuditMoveItem, TreeAuditRestorePreviousParent, TreeAuditMoveNode,
		TreeAuditMergeItems, TreeAuditRewriteItem, TreeAuditRewriteItemTitle, TreeAuditRewriteItemDescription,
		TreeAuditReclassifyKind, TreeAuditReclassifySubtype,
		TreeAuditDeactivateItem, TreeAuditAssignItemToCandidate, TreeAuditChangeEvidenceRole,
		TreeAuditCreateTopicFromCandidate, TreeAuditFoldCandidateIntoTopic,
		TreeAuditDeactivateCandidate, TreeAuditRenameGroup, TreeAuditRemoveEmptyGroup:
		return treeAuditOperationApplicable
	default:
		return treeAuditOperationUnsupported
	}
}

// treeAuditOperationSupported is a boolean convenience wrapper kept for
// existing call sites; treeAuditOperationClassification is the source of
// truth for the applicable/unsupported split.
func treeAuditOperationSupported(operationType TreeAuditOperationType) bool {
	return treeAuditOperationClassification(operationType) == treeAuditOperationApplicable
}

func applyOneTreeAuditOperation(state *liveAnalysisPayload, operation treeAuditOperation, segmentText map[int64]string, evidenceRoles map[int64]treeAuditEvidenceRole, mc *meetingContext, cfg TreeAuditConfig, runID string, resultingVersion int64, beforeFindings []treeAuditPrecheckFinding) (float64, float64, string) {
	if state == nil || state.Tree == nil {
		return 0, 0, "missing_tree"
	}
	nodeIndex := make(map[string]int, len(state.Tree.Nodes))
	itemIndex := make(map[string]int, len(state.Items))
	for index, node := range state.Tree.Nodes {
		nodeIndex[node.ID] = index
	}
	for index, item := range state.Items {
		itemIndex[item.ID] = index
	}
	parentText := func(parentID string) string {
		var parts []string
		seen := map[string]struct{}{}
		for parentID != "" && parentID != treeRootNodeID {
			if _, loop := seen[parentID]; loop {
				break
			}
			seen[parentID] = struct{}{}
			index, exists := nodeIndex[parentID]
			if !exists {
				break
			}
			node := state.Tree.Nodes[index]
			parts = append(parts, node.Label, node.Description)
			parentID = node.ParentID
		}
		return strings.Join(parts, " ")
	}
	itemSemanticText := func(index int, evidence []int64) string {
		item := state.Items[index]
		parts := []string{item.Title, item.Body}
		for _, sequenceNo := range evidence {
			if evidenceRoles[sequenceNo] != treeAuditEvidenceReference {
				parts = append(parts, segmentText[sequenceNo])
			}
		}
		return strings.Join(parts, " ")
	}

	switch operation.Type {
	case TreeAuditMoveItem, TreeAuditRestorePreviousParent:
		nodeAt, nodeExists := nodeIndex[operation.TargetCanonicalItemID]
		itemAt, itemExists := itemIndex[operation.TargetCanonicalItemID]
		if nodeExists && !itemExists && state.Tree.Nodes[nodeAt].ID == treeRootNodeID {
			return 0, 0, "root_immutable"
		}
		if !nodeExists || !itemExists {
			return 0, 0, "unknown_target_node"
		}
		node := state.Tree.Nodes[nodeAt]
		if node.Kind == "topic" || node.Kind == "group" || node.ID == treeRootNodeID {
			return 0, 0, "target_kind_not_movable_detail"
		}
		if strings.TrimSpace(operation.FromParentCanonicalNodeID) == "" || node.ParentID != operation.FromParentCanonicalNodeID {
			return 0, 0, "from_parent_mismatch"
		}
		if operation.TargetCanonicalItemID == operation.ToParentCanonicalNodeID {
			return 0, 0, "self_parent"
		}
		toAt, toExists := nodeIndex[operation.ToParentCanonicalNodeID]
		if !toExists || operation.ToParentCanonicalNodeID == treeRootNodeID {
			return 0, 0, "unknown_or_root_target_parent"
		}
		to := state.Tree.Nodes[toAt]
		if to.Kind != "topic" && to.Kind != "group" {
			return 0, 0, "invalid_target_parent_kind"
		}
		if to.AgendaRole == agendaRoleActionSummary {
			return 0, 0, "action_summary_parent"
		}
		if operation.FromParentCanonicalNodeID == operation.ToParentCanonicalNodeID || operation.TargetCanonicalItemID == operation.ToParentCanonicalNodeID {
			return 0, 0, "self_or_noop_parent"
		}
		if state.Items[itemAt].Status == "resolved" {
			return 0, 0, "resolved_item_parent_sticky"
		}
		if len(operation.EvidenceSequenceNos) == 0 || allTreeAuditEvidenceReference(operation.EvidenceSequenceNos, evidenceRoles) {
			return 0, 0, "reference_evidence_only"
		}
		if !hasTreeAuditEvidenceRole(operation.EvidenceSequenceNos, evidenceRoles, treeAuditEvidencePrimary) {
			return 0, 0, "primary_evidence_required"
		}
		semanticText := itemSemanticText(itemAt, operation.EvidenceSequenceNos)
		currentScore := semanticItemSimilarity(semanticText, parentText(operation.FromParentCanonicalNodeID))
		// currentParentGeneric marks a current parent that is not itself a
		// meaningful classification verdict (the system unclassified bucket,
		// a generically-labeled group, or low subject cohesion): design D4
		// halves the stickiness margin and exempts the recent-parent-change
		// guard in exactly this situation, since there is no real prior
		// placement decision to protect.
		currentParentGeneric := operation.FromParentCanonicalNodeID == treeUnclassifiedTopicID
		if fromAt, exists := nodeIndex[operation.FromParentCanonicalNodeID]; exists && genericGroupLabel(state.Tree.Nodes[fromAt].Label) {
			currentParentGeneric = true
		}
		if currentScore < cfg.CohesionThreshold {
			currentParentGeneric = true
		}
		fromFixedAgenda := treeAuditFixedAgendaAncestor(operation.FromParentCanonicalNodeID, state.Tree)
		toFixedAgenda := treeAuditFixedAgendaAncestor(operation.ToParentCanonicalNodeID, state.Tree)
		fixedAgendaReturnExempt := fromFixedAgenda == "" && toFixedAgenda != "" && currentParentGeneric
		if node.LastParentChangeVersion > 0 && state.TreeVersion-node.LastParentChangeVersion < 2 {
			exempt := currentParentGeneric || fixedAgendaReturnExempt || treeAuditFindingMatchesNode(beforeFindings, TreeAuditReferenceEvidenceReparent, operation.TargetCanonicalItemID)
			if !exempt {
				return 0, 0, "recent_parent_change_sticky"
			}
		}
		for _, sequenceNo := range operation.EvidenceSequenceNos {
			if _, exists := segmentText[sequenceNo]; !exists || !containsInt64(state.Items[itemAt].EvidenceSequenceNos, sequenceNo) {
				return 0, 0, "unbound_operation_evidence"
			}
		}
		newScore := semanticItemSimilarity(semanticText, parentText(operation.ToParentCanonicalNodeID))
		margin := cfg.RequiredImprovementMargin
		if currentParentGeneric {
			margin *= 0.5
		}
		if operation.Type == TreeAuditRestorePreviousParent {
			margin *= 0.5
		}
		// redundantGroupFlattenExempt (design brief D5/9.1) waives the
		// stickiness margin for exactly one move_item shape: the current
		// parent is a group whose own parent is the operation's destination,
		// and the group's own label/description is essentially synonymous
		// with that destination (the group only restates its parent topic
		// under an extra node). parentText() concatenates every ancestor's
		// label/description, so currentScore already includes the
		// destination's own text via the group's parent chain - moving the
		// item up one level to the destination directly can score no better,
		// or even worse, even though flattening the redundant group is the
		// correct structural fix. Every other check (fromParent match,
		// evidence, cycle, depth, cross-agenda, integrity) still applies.
		redundantGroupFlattenExempt := false
		if operation.Type == TreeAuditMoveItem {
			if fromAt, exists := nodeIndex[operation.FromParentCanonicalNodeID]; exists {
				fromNode := state.Tree.Nodes[fromAt]
				if fromNode.Kind == "group" && fromNode.ParentID == operation.ToParentCanonicalNodeID {
					groupText := fromNode.Label + " " + fromNode.Description
					destinationText := to.Label + " " + to.Description
					if sharedTreeAuditSubjectTerm(groupText, destinationText) || semanticItemSimilarity(groupText, destinationText) >= 0.5 {
						redundantGroupFlattenExempt = true
					}
				}
			}
		}
		if newScore-currentScore < margin && !redundantGroupFlattenExempt && !fixedAgendaReturnExempt {
			return currentScore, newScore, "parent_stickiness_margin"
		}
		if treeAuditDepthFromParent(operation.ToParentCanonicalNodeID, state.Tree)+1 > treeHardMaxDepth {
			return currentScore, newScore, "hard_depth_limit"
		}
		state.Tree.Nodes[nodeAt].ParentID = operation.ToParentCanonicalNodeID
		state.Tree.Nodes[nodeAt].LastParentChangeSource = "tree_auditor"
		state.Tree.Nodes[nodeAt].LastParentChangeVersion = resultingVersion
		state.Tree.Nodes[nodeAt].ParentConfidence = operation.Confidence
		state.Items[itemAt].ClassificationStatus = classificationAssigned
		state.Items[itemAt].CandidateTopicID = ""
		state.Items[itemAt].CandidateInactive = false
		state.Items[itemAt].AssignmentConfidence = operation.Confidence
		state.Items[itemAt].AssignmentSource = "tree_auditor"
		state.Items[itemAt].AssignmentReason = operation.Reason
		rebuildTreeAuditEdges(state.Tree)
		return currentScore, newScore, ""

	case TreeAuditMoveNode:
		targetAt, targetExists := nodeIndex[operation.TargetCanonicalNodeID]
		if !targetExists {
			return 0, 0, "unknown_target_node"
		}
		target := state.Tree.Nodes[targetAt]
		if target.ID == treeRootNodeID {
			return 0, 0, "root_immutable"
		}
		if target.Kind != "topic" && target.Kind != "group" {
			return 0, 0, "target_kind_not_movable_container"
		}
		if strings.TrimSpace(operation.FromParentCanonicalNodeID) == "" || target.ParentID != operation.FromParentCanonicalNodeID {
			return 0, 0, "from_parent_mismatch"
		}
		toID := operation.ToParentCanonicalNodeID
		if toID == operation.TargetCanonicalNodeID {
			return 0, 0, "self_parent"
		}
		if operation.FromParentCanonicalNodeID == toID {
			return 0, 0, "self_or_noop_parent"
		}
		toAt, toExists := nodeIndex[toID]
		if !toExists {
			return 0, 0, "unknown_target_parent"
		}
		to := state.Tree.Nodes[toAt]
		if to.Kind != "topic" && to.Kind != "group" {
			return 0, 0, "invalid_target_parent_kind"
		}
		if to.AgendaRole == agendaRoleActionSummary {
			return 0, 0, "action_summary_parent"
		}
		if treeAuditIsAncestorOf(operation.TargetCanonicalNodeID, toID, state.Tree) {
			return 0, 0, "cycle_target_descendant"
		}
		if len(operation.EvidenceSequenceNos) == 0 || allTreeAuditEvidenceReference(operation.EvidenceSequenceNos, evidenceRoles) {
			return 0, 0, "reference_evidence_only"
		}
		if !hasTreeAuditEvidenceRole(operation.EvidenceSequenceNos, evidenceRoles, treeAuditEvidencePrimary) {
			return 0, 0, "primary_evidence_required"
		}
		for _, sequenceNo := range operation.EvidenceSequenceNos {
			if _, exists := segmentText[sequenceNo]; !exists {
				return 0, 0, "unbound_operation_evidence"
			}
		}
		if target.LastParentChangeVersion > 0 && state.TreeVersion-target.LastParentChangeVersion < 2 {
			exempt := operation.FromParentCanonicalNodeID == treeUnclassifiedTopicID || treeAuditFindingMatchesNode(beforeFindings, TreeAuditReferenceEvidenceReparent, operation.TargetCanonicalNodeID)
			if !exempt {
				return 0, 0, "recent_parent_change_sticky"
			}
		}
		if treeAuditDepthFromParent(toID, state.Tree)+1+treeAuditSubtreeHeight(operation.TargetCanonicalNodeID, state.Tree) > treeHardMaxDepth {
			return 0, 0, "hard_depth_limit"
		}
		subtreeText := treeAuditSubtreeText(operation.TargetCanonicalNodeID, state.Tree)
		currentScore := semanticItemSimilarity(subtreeText, parentText(operation.FromParentCanonicalNodeID))
		newScore := semanticItemSimilarity(subtreeText, parentText(toID))
		currentParentGeneric := operation.FromParentCanonicalNodeID == treeUnclassifiedTopicID
		if fromAt, exists := nodeIndex[operation.FromParentCanonicalNodeID]; exists && genericGroupLabel(state.Tree.Nodes[fromAt].Label) {
			currentParentGeneric = true
		}
		if currentScore < cfg.CohesionThreshold {
			currentParentGeneric = true
		}
		if newScore < currentScore && !currentParentGeneric {
			return currentScore, newScore, "subject_alignment_not_improved"
		}
		state.Tree.Nodes[targetAt].ParentID = toID
		state.Tree.Nodes[targetAt].LastParentChangeSource = "tree_auditor"
		state.Tree.Nodes[targetAt].LastParentChangeVersion = resultingVersion
		state.Tree.Nodes[targetAt].ParentConfidence = operation.Confidence
		rebuildTreeAuditEdges(state.Tree)
		return currentScore, newScore, ""

	case TreeAuditFoldCandidateIntoTopic:
		candidateAt := -1
		for index := range state.EmergingTopics {
			if state.EmergingTopics[index].ID == operation.TargetCandidateID {
				candidateAt = index
				break
			}
		}
		toAt, toExists := nodeIndex[operation.ToParentCanonicalNodeID]
		if candidateAt < 0 || !toExists || state.Tree.Nodes[toAt].Kind != "topic" || state.Tree.Nodes[toAt].ID == treeRootNodeID || state.Tree.Nodes[toAt].AgendaRole == agendaRoleActionSummary {
			return 0, 0, "invalid_candidate_or_topic"
		}
		targetIDs := operation.TargetCanonicalItemIDs
		if len(targetIDs) == 0 {
			targetIDs = state.EmergingTopics[candidateAt].EvidenceItemIDs
		}
		if len(targetIDs) == 0 || len(targetIDs) > 3 {
			return 0, 0, "candidate_fold_size_limit"
		}
		if len(operation.EvidenceSequenceNos) == 0 || allTreeAuditEvidenceReference(operation.EvidenceSequenceNos, evidenceRoles) {
			return 0, 0, "reference_evidence_only"
		}
		minCurrent, minNew := 1.0, 1.0
		for _, id := range targetIDs {
			nodeAt, nodeExists := nodeIndex[id]
			itemAt, itemExists := itemIndex[id]
			if !nodeExists || !itemExists || (state.Items[itemAt].CandidateTopicID != operation.TargetCandidateID && state.Items[itemAt].ClassificationStatus != classificationTentative) {
				return 0, 0, "candidate_evidence_mismatch"
			}
			boundEvidence := false
			for _, sequenceNo := range operation.EvidenceSequenceNos {
				if _, exists := segmentText[sequenceNo]; exists && containsInt64(state.Items[itemAt].EvidenceSequenceNos, sequenceNo) {
					boundEvidence = true
					break
				}
			}
			if !boundEvidence {
				return 0, 0, "unbound_operation_evidence"
			}
			semanticText := itemSemanticText(itemAt, operation.EvidenceSequenceNos)
			currentParentID := state.Tree.Nodes[nodeAt].ParentID
			currentScore := semanticItemSimilarity(semanticText, parentText(currentParentID))
			newScore := semanticItemSimilarity(semanticText, parentText(operation.ToParentCanonicalNodeID))
			margin := cfg.RequiredImprovementMargin
			currentParentGeneric := currentParentID == treeUnclassifiedTopicID
			if currentParentAt, exists := nodeIndex[currentParentID]; exists && genericGroupLabel(state.Tree.Nodes[currentParentAt].Label) {
				currentParentGeneric = true
			}
			if currentScore < cfg.CohesionThreshold {
				currentParentGeneric = true
			}
			if currentParentGeneric {
				margin *= 0.5
			}
			if newScore-currentScore < margin {
				return currentScore, newScore, "parent_stickiness_margin"
			}
			if currentScore < minCurrent {
				minCurrent = currentScore
			}
			if newScore < minNew {
				minNew = newScore
			}
			state.Tree.Nodes[nodeAt].ParentID = operation.ToParentCanonicalNodeID
			state.Tree.Nodes[nodeAt].LastParentChangeSource = "tree_auditor"
			state.Tree.Nodes[nodeAt].LastParentChangeVersion = resultingVersion
			state.Tree.Nodes[nodeAt].ParentConfidence = operation.Confidence
			state.Items[itemAt].ClassificationStatus = classificationAssigned
			state.Items[itemAt].CandidateTopicID = ""
			state.Items[itemAt].CandidateInactive = false
			state.Items[itemAt].AssignmentSource = "tree_auditor"
			state.Items[itemAt].AssignmentConfidence = operation.Confidence
			state.Items[itemAt].AssignmentReason = operation.Reason
		}
		state.EmergingTopics[candidateAt].Inactive = true
		state.EmergingTopics[candidateAt].InactiveSinceRound = resultingVersion
		rebuildTreeAuditEdges(state.Tree)
		return minCurrent, minNew, ""

	case TreeAuditDeactivateCandidate:
		for index := range state.EmergingTopics {
			candidate := &state.EmergingTopics[index]
			if candidate.ID != operation.TargetCandidateID {
				continue
			}
			if candidate.RoundCount > 1 && !candidate.Inactive {
				return 0, 0, "established_candidate_not_auto_deactivated"
			}
			candidate.Inactive = true
			candidate.InactiveSinceRound = resultingVersion
			for _, id := range candidate.EvidenceItemIDs {
				if itemAt, ok := itemIndex[id]; ok {
					state.Items[itemAt].CandidateInactive = true
				}
			}
			return 0, 1, ""
		}
		return 0, 0, "unknown_candidate"

	case TreeAuditMergeItems:
		targetIDs := operation.TargetCanonicalItemIDs
		if len(targetIDs) < 2 {
			return 0, 0, "insufficient_merge_targets"
		}
		seenTarget := make(map[string]struct{}, len(targetIDs))
		targetItems := make([]liveAnalysisItem, 0, len(targetIDs))
		targetAts := make([]int, 0, len(targetIDs))
		for _, id := range targetIDs {
			if _, duplicate := seenTarget[id]; duplicate {
				return 0, 0, "duplicate_target_item"
			}
			seenTarget[id] = struct{}{}
			itemAt, itemExists := itemIndex[id]
			if _, nodeExists := nodeIndex[id]; !itemExists || !nodeExists {
				return 0, 0, "unknown_target_item"
			}
			targetItems = append(targetItems, state.Items[itemAt])
			targetAts = append(targetAts, itemAt)
		}
		if !treeAuditMergeTargetsConnected(targetItems) {
			return 0, 0, "items_not_connected_duplicates"
		}
		if len(operation.EvidenceSequenceNos) == 0 || allTreeAuditEvidenceReference(operation.EvidenceSequenceNos, evidenceRoles) {
			return 0, 0, "reference_evidence_only"
		}
		if !hasTreeAuditEvidenceRole(operation.EvidenceSequenceNos, evidenceRoles, treeAuditEvidencePrimary) {
			return 0, 0, "primary_evidence_required"
		}
		unionEvidence := make(map[int64]struct{})
		for _, item := range targetItems {
			for _, sequenceNo := range item.EvidenceSequenceNos {
				unionEvidence[sequenceNo] = struct{}{}
			}
		}
		for _, sequenceNo := range operation.EvidenceSequenceNos {
			if _, exists := segmentText[sequenceNo]; !exists {
				return 0, 0, "unbound_operation_evidence"
			}
			if _, bound := unionEvidence[sequenceNo]; !bound {
				return 0, 0, "unbound_operation_evidence"
			}
		}
		survivorAt := targetAts[0]
		survivor := state.Items[survivorAt]
		for _, companion := range targetItems[1:] {
			survivor = mergeTreeAuditItemAttributes(survivor, companion)
		}
		state.Items[survivorAt] = survivor
		removeNodeIDs := make(map[string]struct{}, len(targetIDs)-1)
		for _, id := range targetIDs[1:] {
			removeNodeIDs[id] = struct{}{}
			if at, ok := itemIndex[id]; ok {
				addItemTombstone(state, state.Items[at], "merged", survivor.ID, "tree_auditor", runID, resultingVersion-1, resultingVersion)
				state.Items[at].MergedIntoID = survivor.ID
			}
		}
		keptNodes := state.Tree.Nodes[:0]
		for _, node := range state.Tree.Nodes {
			if _, drop := removeNodeIDs[node.ID]; drop {
				continue
			}
			keptNodes = append(keptNodes, node)
		}
		state.Tree.Nodes = keptNodes
		rebuildTreeAuditEdges(state.Tree)
		return 0, 1, ""

	case TreeAuditRewriteItem, TreeAuditRewriteItemTitle, TreeAuditRewriteItemDescription:
		nodeAt, nodeExists := nodeIndex[operation.TargetCanonicalItemID]
		itemAt, itemExists := itemIndex[operation.TargetCanonicalItemID]
		if !nodeExists || !itemExists {
			return 0, 0, "unknown_target_item"
		}
		node := state.Tree.Nodes[nodeAt]
		if node.Kind == "topic" || node.Kind == "group" || node.ID == treeRootNodeID {
			return 0, 0, "target_kind_not_rewritable"
		}
		label := strings.TrimSpace(operation.Label)
		if label == "" {
			return 0, 0, "empty_label"
		}
		item := state.Items[itemAt]
		oldText := item.Title + " " + item.Body
		subjectPreserved := sharedTreeAuditSubjectTerm(oldText, label)
		if !subjectPreserved {
			vocabText := oldText
			for _, sequenceNo := range operation.EvidenceSequenceNos {
				if evidenceRoles[sequenceNo] != treeAuditEvidenceReference {
					vocabText += " " + segmentText[sequenceNo]
				}
			}
			subjectPreserved = sharedTreeAuditSubjectTerm(vocabText, label)
		}
		if !subjectPreserved {
			return 0, 0, "subject_not_preserved"
		}
		if operation.Type == TreeAuditRewriteItemDescription {
			truncated := truncateRunes(label, liveAnalysisTreeDescriptionMaxRunes)
			state.Items[itemAt].Body = truncated
			state.Tree.Nodes[nodeAt].Description = truncated
		} else {
			truncated := truncateRunes(label, liveAnalysisTopicLabelMaxRunes)
			state.Items[itemAt].Title = truncated
			state.Tree.Nodes[nodeAt].Label = truncated
		}
		state.Tree.Nodes[nodeAt].UpdatedAtVersion = resultingVersion
		return 0, 1, ""

	case TreeAuditReclassifyKind, TreeAuditReclassifySubtype:
		nodeAt, nodeExists := nodeIndex[operation.TargetCanonicalItemID]
		itemAt, itemExists := itemIndex[operation.TargetCanonicalItemID]
		if !nodeExists || !itemExists {
			return 0, 0, "unknown_target_item"
		}
		item := state.Items[itemAt]
		node := state.Tree.Nodes[nodeAt]
		if node.Kind == "topic" || node.Kind == "group" || node.ID == treeRootNodeID {
			return 0, 0, "target_kind_not_reclassifiable"
		}
		if len(operation.EvidenceSequenceNos) == 0 || allTreeAuditEvidenceReference(operation.EvidenceSequenceNos, evidenceRoles) {
			return 0, 0, "primary_evidence_required"
		}
		var evidenceText strings.Builder
		for _, sequenceNo := range operation.EvidenceSequenceNos {
			text, exists := segmentText[sequenceNo]
			if !exists || evidenceRoles[sequenceNo] == treeAuditEvidenceReference {
				continue
			}
			evidenceText.WriteString(" ")
			evidenceText.WriteString(text)
		}
		if strings.TrimSpace(evidenceText.String()) == "" {
			return 0, 0, "primary_evidence_required"
		}
		requestedKind := item.Kind
		if operation.Type == TreeAuditReclassifyKind {
			requestedKind = strings.ToLower(strings.TrimSpace(operation.Kind))
		}
		requestedSubtype := strings.ToLower(strings.TrimSpace(operation.Subtype))
		requestedKind, requestedSubtype, _, _ = normalizeSemanticClassification(requestedKind, requestedSubtype, item.Status)
		if !validLiveAnalysisItemKind(requestedKind) {
			return 0, 0, "invalid_semantic_kind"
		}
		if operation.Type == TreeAuditReclassifySubtype {
			if item.Kind != "issue" || !validIssueSubtype(requestedSubtype) {
				return 0, 0, "invalid_issue_subtype"
			}
			requestedKind = "issue"
		} else if requestedKind != item.Kind {
			if item.Kind == "decision" || item.Kind == "todo" || item.Kind == "risk" || requestedKind == "decision" || requestedKind == "todo" || requestedKind == "risk" {
				return 0, 0, "protected_semantic_kind"
			}
			text := evidenceText.String()
			supported := requestedKind == "issue" && (openIssueMarkerPattern.MatchString(text) || lowInformationQuestionPattern.MatchString(text) || confirmationStatementPattern.MatchString(text))
			if requestedKind == "fact" {
				supported = lowInformationAssertionPattern.MatchString(text) && !openIssueMarkerPattern.MatchString(text) && !lowInformationQuestionPattern.MatchString(text)
			}
			if !supported {
				return 0, 0, "semantic_reclassification_not_grounded"
			}
		}
		state.Items[itemAt].Kind = requestedKind
		state.Items[itemAt].Subtype = requestedSubtype
		state.Items[itemAt].InformationStatus = informationStatusGrounded
		repairNonResolvableStatus(&state.Items[itemAt])
		state.Tree.Nodes[nodeAt].Kind = requestedKind
		state.Tree.Nodes[nodeAt].Subtype = requestedSubtype
		state.Tree.Nodes[nodeAt].Status = state.Items[itemAt].Status
		state.Tree.Nodes[nodeAt].UpdatedAtVersion = resultingVersion
		return 0, 1, ""

	case TreeAuditDeactivateItem:
		nodeAt, nodeExists := nodeIndex[operation.TargetCanonicalItemID]
		itemAt, itemExists := itemIndex[operation.TargetCanonicalItemID]
		if !nodeExists || !itemExists {
			return 0, 0, "unknown_target_item"
		}
		node := state.Tree.Nodes[nodeAt]
		if node.Kind == "topic" || node.Kind == "group" || node.ID == treeRootNodeID {
			return 0, 0, "target_kind_not_deactivatable"
		}
		item := state.Items[itemAt]
		// Low-information cleanup is deliberately narrower than duplicate
		// merge. Decisions, TODOs and risks are never auto-hidden solely due to
		// wording quality; they remain visible for human correction.
		switch item.Kind {
		case "decision", "todo", "risk":
			return 0, 0, "protected_semantic_kind"
		}
		if item.InformationStatus == informationStatusTentative {
			return 0, 0, "tentative_item_protected"
		}
		if node.CreatedAtVersion > 0 && resultingVersion-node.CreatedAtVersion < cfg.TentativeMaxVersions {
			return 0, 0, "item_too_recent"
		}
		for _, candidateNode := range state.Tree.Nodes {
			if candidateNode.ParentID == node.ID {
				return 0, 0, "item_has_children"
			}
			if candidateNode.ID != node.ID && containsExactString(candidateNode.RelatedItemIDs, node.ID) {
				return 0, 0, "item_is_referenced"
			}
		}
		for _, relation := range state.Tree.Relations {
			if relation.Source == node.ID || relation.Target == node.ID {
				return 0, 0, "item_is_referenced"
			}
		}
		// Repair priority is rewrite -> merge -> deactivate. A recoverable
		// referent stays on the same canonical item even when a superficially
		// similar sibling exists; only an unrecoverable duplicate is merged.
		repairScope := liveEvidenceScope{TranscriptText: segmentText}
		if item.Kind == "issue" && issueTextNeedsReferent(item.Title+" "+item.Body) && nearestConcreteIssueEvidence(item, repairScope) != "" {
			return 0, 0, "rewrite_preferred"
		}
		for _, sibling := range state.Items {
			if sibling.ID == item.ID {
				continue
			}
			if _, siblingActive := nodeIndex[sibling.ID]; !siblingActive {
				continue
			}
			if matched, _ := sameKindSemanticDuplicate(item, sibling); matched || sameCanonicalProposition(item, sibling) {
				return 0, 0, "merge_preferred"
			}
		}
		grounds := allTreeAuditEvidenceReference(item.EvidenceSequenceNos, evidenceRoles)
		if !grounds {
			grounds = isDiscourseOnlyItem(item.Title, item.Body)
		}
		if !grounds {
			grounds = lowInformationDecisionItem(item)
		}
		if !grounds {
			grounds = treeAuditLowInformationItem(item, segmentText, evidenceRoles)
		}
		if !grounds {
			return 0, 0, "deactivate_grounds_not_verified"
		}
		tombstoneReason := treeAuditDeactivationTombstoneReason(item, operation, segmentText, evidenceRoles)
		addItemTombstone(state, item, tombstoneReason, "", "tree_auditor", runID, resultingVersion-1, resultingVersion, node.ParentID)
		state.Tree.Nodes = append(state.Tree.Nodes[:nodeAt], state.Tree.Nodes[nodeAt+1:]...)
		state.Items[itemAt].Inactive = true
		state.Items[itemAt].SuppressionReason = tombstoneReason
		rebuildTreeAuditEdges(state.Tree)
		return 0, 1, ""

	case TreeAuditAssignItemToCandidate:
		itemAt, itemExists := itemIndex[operation.TargetCanonicalItemID]
		if _, nodeExists := nodeIndex[operation.TargetCanonicalItemID]; !itemExists || !nodeExists {
			return 0, 0, "unknown_target_item"
		}
		if state.Items[itemAt].Status == "resolved" {
			return 0, 0, "resolved_item_not_assignable"
		}
		candidateAt := -1
		for index := range state.EmergingTopics {
			if state.EmergingTopics[index].ID == operation.TargetCandidateID {
				candidateAt = index
				break
			}
		}
		if candidateAt < 0 {
			return 0, 0, "unknown_candidate"
		}
		if state.EmergingTopics[candidateAt].Inactive {
			return 0, 0, "candidate_inactive"
		}
		if _, promoted := nodeIndex[operation.TargetCandidateID]; promoted {
			return 0, 0, "candidate_already_promoted"
		}
		state.Items[itemAt].CandidateTopicID = operation.TargetCandidateID
		state.Items[itemAt].ClassificationStatus = classificationTentative
		state.Items[itemAt].CandidateInactive = false
		state.Items[itemAt].AssignmentSource = "tree_auditor"
		state.Items[itemAt].AssignmentConfidence = operation.Confidence
		state.Items[itemAt].AssignmentReason = operation.Reason
		found := false
		for _, id := range state.EmergingTopics[candidateAt].EvidenceItemIDs {
			if id == operation.TargetCanonicalItemID {
				found = true
				break
			}
		}
		if !found {
			state.EmergingTopics[candidateAt].EvidenceItemIDs = append(state.EmergingTopics[candidateAt].EvidenceItemIDs, operation.TargetCanonicalItemID)
		}
		return 0, 1, ""

	case TreeAuditChangeEvidenceRole:
		itemAt, itemExists := itemIndex[operation.TargetCanonicalItemID]
		if _, nodeExists := nodeIndex[operation.TargetCanonicalItemID]; !itemExists || !nodeExists {
			return 0, 0, "unknown_target_item"
		}
		if len(operation.EvidenceSequenceNos) == 0 {
			return 0, 0, "no_target_sequence"
		}
		targets := make(map[int64]struct{}, len(operation.EvidenceSequenceNos))
		for _, sequenceNo := range operation.EvidenceSequenceNos {
			if !containsInt64(state.Items[itemAt].EvidenceSequenceNos, sequenceNo) {
				return 0, 0, "sequence_not_bound_to_item"
			}
			targets[sequenceNo] = struct{}{}
		}
		remainingPrimary := false
		for _, sequenceNo := range state.Items[itemAt].EvidenceSequenceNos {
			if _, downgraded := targets[sequenceNo]; downgraded {
				continue
			}
			if evidenceRoles[sequenceNo] == treeAuditEvidencePrimary {
				remainingPrimary = true
				break
			}
		}
		if !remainingPrimary {
			return 0, 0, "last_primary_evidence"
		}
		for sequenceNo := range targets {
			found := false
			for index := range state.Items[itemAt].EvidenceRoles {
				if state.Items[itemAt].EvidenceRoles[index].SequenceNo == sequenceNo {
					state.Items[itemAt].EvidenceRoles[index].Role = liveEvidenceReferenceRecap
					found = true
					break
				}
			}
			if !found {
				state.Items[itemAt].EvidenceRoles = append(state.Items[itemAt].EvidenceRoles, liveEvidenceRoleRef{SequenceNo: sequenceNo, Role: liveEvidenceReferenceRecap})
			}
		}
		return 0, 1, ""

	case TreeAuditCreateTopicFromCandidate:
		candidateAt := -1
		for index := range state.EmergingTopics {
			if state.EmergingTopics[index].ID == operation.TargetCandidateID {
				candidateAt = index
				break
			}
		}
		if candidateAt < 0 {
			return 0, 0, "unknown_candidate"
		}
		candidate := state.EmergingTopics[candidateAt]
		if candidate.Inactive {
			return 0, 0, "candidate_inactive"
		}
		if _, promoted := nodeIndex[candidate.ID]; promoted {
			return 0, 0, "candidate_already_promoted"
		}
		evidenceItemIDs := make([]string, 0, len(candidate.EvidenceItemIDs))
		for _, id := range candidate.EvidenceItemIDs {
			if _, exists := itemIndex[id]; exists {
				evidenceItemIDs = append(evidenceItemIDs, id)
			}
		}
		if len(evidenceItemIDs) == 0 {
			return 0, 0, "no_current_evidence_items"
		}
		recapOnly := true
		for _, id := range evidenceItemIDs {
			if !allTreeAuditEvidenceReference(state.Items[itemIndex[id]].EvidenceSequenceNos, evidenceRoles) {
				recapOnly = false
				break
			}
		}
		if recapOnly {
			return 0, 0, "recap_only_candidate"
		}
		label := strings.TrimSpace(candidate.Label)
		candidateText := label + " " + candidate.Description
		for _, node := range state.Tree.Nodes {
			// Materialized agenda topics are checked separately below against
			// mc.Agenda (the legacy reason remains "should_fold_into_fixed_agenda"); this loop
			// only guards against duplicating an existing dynamic topic.
			if node.Kind != "topic" || node.ID == treeRootNodeID || node.ID == treeUnclassifiedTopicID || node.Origin == topicOriginAgenda || node.Origin == topicOriginMixed || len(node.AgendaRefs) > 0 {
				continue
			}
			topicText := node.Label + " " + node.Description
			if semanticItemSimilarity(candidateText, topicText) >= 0.5 && sharedTreeAuditSubjectTerm(candidateText, topicText) {
				return 0, 0, "duplicate_topic"
			}
		}
		if mc != nil {
			for _, agenda := range mc.Agenda {
				if agenda.Role == agendaRoleActionSummary {
					continue
				}
				if semanticItemSimilarity(candidateText, agenda.Title) >= 0.5 && sharedTreeAuditSubjectTerm(candidateText, agenda.Title) {
					return 0, 0, "should_fold_into_fixed_agenda"
				}
			}
		}
		if len(operation.EvidenceSequenceNos) == 0 || allTreeAuditEvidenceReference(operation.EvidenceSequenceNos, evidenceRoles) {
			return 0, 0, "reference_evidence_only"
		}
		if !hasTreeAuditEvidenceRole(operation.EvidenceSequenceNos, evidenceRoles, treeAuditEvidencePrimary) {
			return 0, 0, "primary_evidence_required"
		}
		for _, sequenceNo := range operation.EvidenceSequenceNos {
			if _, exists := segmentText[sequenceNo]; !exists {
				return 0, 0, "unbound_operation_evidence"
			}
		}
		state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
			ID: candidate.ID, Kind: "topic", ParentID: treeRootNodeID, Label: label,
			Description: candidate.Description, Origin: topicOriginDynamic,
			CreatedAtVersion: resultingVersion, UpdatedAtVersion: resultingVersion,
		})
		for _, id := range evidenceItemIDs {
			childAt, exists := nodeIndex[id]
			if !exists {
				continue
			}
			state.Tree.Nodes[childAt].ParentID = candidate.ID
			state.Tree.Nodes[childAt].LastParentChangeSource = "tree_auditor"
			state.Tree.Nodes[childAt].LastParentChangeVersion = resultingVersion
			state.Tree.Nodes[childAt].ParentConfidence = operation.Confidence
			itemAt := itemIndex[id]
			state.Items[itemAt].ClassificationStatus = classificationAssigned
			state.Items[itemAt].CandidateTopicID = ""
			state.Items[itemAt].CandidateInactive = false
			state.Items[itemAt].AssignmentSource = "tree_auditor"
			state.Items[itemAt].AssignmentConfidence = operation.Confidence
			state.Items[itemAt].AssignmentReason = operation.Reason
		}
		state.EmergingTopics = append(state.EmergingTopics[:candidateAt], state.EmergingTopics[candidateAt+1:]...)
		rebuildTreeAuditEdges(state.Tree)
		return 0, 1, ""

	case TreeAuditRenameGroup:
		groupAt, exists := nodeIndex[operation.TargetCanonicalNodeID]
		if !exists || state.Tree.Nodes[groupAt].Kind != "group" || operation.Label == "" || genericGroupLabel(operation.Label) {
			return 0, 0, "invalid_group_or_label"
		}
		var childText strings.Builder
		for _, node := range state.Tree.Nodes {
			if node.ParentID == operation.TargetCanonicalNodeID {
				childText.WriteString(node.Label + " " + node.Description + " ")
			}
		}
		currentScore := semanticItemSimilarity(childText.String(), state.Tree.Nodes[groupAt].Label)
		newScore := semanticItemSimilarity(childText.String(), operation.Label)
		if newScore+0.05 < currentScore {
			return currentScore, newScore, "group_label_cohesion_worsened"
		}
		state.Tree.Nodes[groupAt].Label = operation.Label
		state.Tree.Nodes[groupAt].UpdatedAtVersion = resultingVersion
		return currentScore, newScore, ""

	case TreeAuditRemoveEmptyGroup:
		targetAt, exists := nodeIndex[operation.TargetCanonicalNodeID]
		if !exists {
			return 0, 0, "unknown_or_immutable_container"
		}
		target := state.Tree.Nodes[targetAt]
		// A dynamic topic that ends up with zero children after other
		// operations move its content elsewhere is exactly as removable as an
		// empty group (treeAuditRemovableEmptyContainerKind, design brief
		// D5/9.2); the emptiness check below still guards both cases.
		if !treeAuditRemovableEmptyContainerKind(target) {
			return 0, 0, "unknown_or_immutable_container"
		}
		for _, node := range state.Tree.Nodes {
			if node.ParentID == operation.TargetCanonicalNodeID {
				return 0, 0, "group_not_empty"
			}
		}
		state.Tree.Nodes = append(state.Tree.Nodes[:targetAt], state.Tree.Nodes[targetAt+1:]...)
		rebuildTreeAuditEdges(state.Tree)
		return 0, 1, ""

	default:
		return 0, 0, "unsupported_operation"
	}
}

// treeAuditConfidenceBonusFloor is the minimum modelConfidence (design D4)
// below which no structural bonus is granted: a model that itself reports
// low confidence gets no help from server-side corroboration.
const treeAuditConfidenceBonusFloor = 0.60

// treeAuditConfidenceBonusStep is the size of each individual structural
// bonus (unclassifiedOrGenericParentBonus, precheckAgreementBonus,
// fixedAgendaMatchBonus).
const treeAuditConfidenceBonusStep = 0.05

// treeAuditConfidenceBonusCap is the maximum total bonus the three
// structural signals may add together; it never lets server corroboration
// alone carry an operation past 0.15 above the model's own confidence.
const treeAuditConfidenceBonusCap = 0.15

// treeAuditConfidenceContaminationPenalty is subtracted when an operation's
// own evidence mixes a reference/recap sequence with a primary/supporting
// one (recapContaminationPenalty, design D4). An operation whose evidence is
// entirely reference-role is already hard-rejected by the appliers
// themselves ("reference_evidence_only"), independent of this penalty.
const treeAuditConfidenceContaminationPenalty = 0.10

// treeAuditOperationIsMoveType reports whether operationType is one of the
// four move-type operations that treeAuditEffectiveConfidence adjusts
// (design D4). Every other applicable operation type is gated on the
// model's own reported confidence unchanged.
func treeAuditOperationIsMoveType(operationType TreeAuditOperationType) bool {
	switch operationType {
	case TreeAuditMoveItem, TreeAuditRestorePreviousParent, TreeAuditMoveNode, TreeAuditFoldCandidateIntoTopic:
		return true
	default:
		return false
	}
}

// treeAuditIsManualChangeSource reports whether source marks a node's last
// parent change as a human/manual edit rather than a model or heuristic one.
// No current code path writes "user" or a "manual*" source value onto
// LastParentChangeSource, but treeAuditManualEditProtectedOperation checks
// for it defensively (design D4 point 2) so a future manual-edit feature is
// automatically protected from audit overwrite without a validator change.
func treeAuditIsManualChangeSource(source string) bool {
	normalized := strings.ToLower(strings.TrimSpace(source))
	if normalized == "" {
		return false
	}
	return normalized == "user" || strings.HasPrefix(normalized, "manual")
}

// treeAuditManualEditProtectedOperation reports whether an applicable
// operation's target (or, for fold_candidate_into_topic, any target item)
// currently carries a manual/user LastParentChangeSource. It is
// checked before any confidence bonus is computed, so a manual edit cannot
// be overridden regardless of modelConfidence.
func treeAuditManualEditProtectedOperation(operation treeAuditOperation, state liveAnalysisPayload) bool {
	isManual := func(id string) bool {
		if id == "" {
			return false
		}
		node := liveTreeNodeByID(state.Tree, id)
		return node != nil && treeAuditIsManualChangeSource(node.LastParentChangeSource)
	}
	switch operation.Type {
	case TreeAuditMoveItem, TreeAuditRestorePreviousParent, TreeAuditDeactivateItem,
		TreeAuditRewriteItem, TreeAuditRewriteItemTitle, TreeAuditRewriteItemDescription,
		TreeAuditReclassifyKind, TreeAuditReclassifySubtype:
		return isManual(operation.TargetCanonicalItemID)
	case TreeAuditMoveNode, TreeAuditRenameGroup, TreeAuditRemoveEmptyGroup:
		return isManual(operation.TargetCanonicalNodeID)
	case TreeAuditMergeItems:
		for _, id := range operation.TargetCanonicalItemIDs {
			if isManual(id) {
				return true
			}
		}
		return false
	case TreeAuditFoldCandidateIntoTopic:
		targetIDs := operation.TargetCanonicalItemIDs
		if len(targetIDs) == 0 {
			for _, candidate := range state.EmergingTopics {
				if candidate.ID == operation.TargetCandidateID {
					targetIDs = candidate.EvidenceItemIDs
					break
				}
			}
		}
		for _, id := range targetIDs {
			if isManual(id) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// treeAuditFindingMatchesNode reports whether any precheck finding of the
// given type names nodeID among its NodeIDs.
func treeAuditFindingMatchesNode(findings []treeAuditPrecheckFinding, findingType TreeAuditFindingType, nodeID string) bool {
	if nodeID == "" {
		return false
	}
	for _, finding := range findings {
		if finding.Type == findingType && containsExactString(finding.NodeIDs, nodeID) {
			return true
		}
	}
	return false
}

// treeAuditParentChainText concatenates the label/description of id and each
// of its container ancestors up to (excluding) root. It mirrors the
// applier's own local parentText closures so the confidence gate scores
// subject cohesion the same way the applier does, without sharing mutable
// state between the two passes.
func treeAuditParentChainText(tree *liveAnalysisTree, id string) string {
	if tree == nil {
		return ""
	}
	var parts []string
	seen := map[string]struct{}{}
	for id != "" && id != treeRootNodeID {
		if _, loop := seen[id]; loop {
			break
		}
		seen[id] = struct{}{}
		node := liveTreeNodeByID(tree, id)
		if node == nil {
			break
		}
		parts = append(parts, node.Label, node.Description)
		id = node.ParentID
	}
	return strings.Join(parts, " ")
}

// treeAuditTopContainerID returns id's root-level ancestor (the ancestor
// whose own parent is root), or "" if id does not resolve cleanly to one
// (a cycle, an unknown id, or id already being root).
func treeAuditTopContainerID(tree *liveAnalysisTree, id string) string {
	if tree == nil || id == "" {
		return ""
	}
	current := liveTreeNodeByID(tree, id)
	if current == nil {
		return ""
	}
	seen := map[string]struct{}{}
	for current.ParentID != "" && current.ParentID != treeRootNodeID {
		if _, loop := seen[current.ParentID]; loop {
			return ""
		}
		seen[current.ParentID] = struct{}{}
		next := liveTreeNodeByID(tree, current.ParentID)
		if next == nil {
			return ""
		}
		current = next
	}
	if current.ParentID == treeRootNodeID {
		return current.ID
	}
	return ""
}

// treeAuditOperationItemText builds the same subject text the appliers use
// for cohesion scoring: the item's own title/body plus any non-reference
// evidence segment text.
func treeAuditOperationItemText(item liveAnalysisItem, evidence []int64, segmentText map[int64]string, evidenceRoles map[int64]treeAuditEvidenceRole) string {
	parts := []string{item.Title, item.Body}
	for _, sequenceNo := range evidence {
		if evidenceRoles[sequenceNo] != treeAuditEvidenceReference {
			parts = append(parts, segmentText[sequenceNo])
		}
	}
	return strings.Join(parts, " ")
}

// treeAuditParentIsGenericOrUnclassified reports whether parentID is the
// system unclassified bucket, a generically-labeled group/topic, or has
// subject cohesion with subjectText below cfg.CohesionThreshold. All three
// conditions independently mean parentID is not a meaningful prior placement
// decision; treeAuditEffectiveConfidence's unclassifiedOrGenericParentBonus
// uses exactly this definition (design D4).
func treeAuditParentIsGenericOrUnclassified(tree *liveAnalysisTree, parentID string, subjectText string, cfg TreeAuditConfig) bool {
	if parentID == treeUnclassifiedTopicID {
		return true
	}
	if node := liveTreeNodeByID(tree, parentID); node != nil && genericGroupLabel(node.Label) {
		return true
	}
	return semanticItemSimilarity(subjectText, treeAuditParentChainText(tree, parentID)) < cfg.CohesionThreshold
}

// treeAuditFixedAgendaReturnExempt reports whether a move_item/
// restore_previous_parent operation qualifies for the fixed-agenda-return
// margin/stickiness exemption (design brief D5 addendum / §8.2), evaluated
// against state (the tree/items immediately before this operation applies).
// It is deliberately built from the same three primitives the move_item
// applier's own fixedAgendaReturnExempt and treeAuditEffectiveConfidence
// already use - treeAuditFixedAgendaAncestor, treeAuditOperationItemText,
// treeAuditParentIsGenericOrUnclassified - rather than duplicating their
// logic, so this is exactly the same condition computed once more, not an
// independent approximation of it. It exists because
// validateAndDryRunTreeAuditOperations's heuristic non-worsening gate needs
// to know whether *this* operation was fixed-agenda-return exempt, and
// threading an extra return value through every one of
// applyOneTreeAuditOperation's branches would be far more invasive than
// recomputing this specific, narrow condition here.
func treeAuditFixedAgendaReturnExempt(operation treeAuditOperation, state liveAnalysisPayload, evidenceRoles map[int64]treeAuditEvidenceRole, segmentText map[int64]string, cfg TreeAuditConfig) bool {
	if operation.Type != TreeAuditMoveItem && operation.Type != TreeAuditRestorePreviousParent {
		return false
	}
	if state.Tree == nil {
		return false
	}
	fromFixedAgenda := treeAuditFixedAgendaAncestor(operation.FromParentCanonicalNodeID, state.Tree)
	toFixedAgenda := treeAuditFixedAgendaAncestor(operation.ToParentCanonicalNodeID, state.Tree)
	if fromFixedAgenda != "" || toFixedAgenda == "" {
		return false
	}
	item := findItemByID(state.Items, operation.TargetCanonicalItemID)
	if item == nil {
		return false
	}
	subjectText := treeAuditOperationItemText(*item, operation.EvidenceSequenceNos, segmentText, evidenceRoles)
	return treeAuditParentIsGenericOrUnclassified(state.Tree, operation.FromParentCanonicalNodeID, subjectText, cfg)
}

// treeAuditExcludeSelfSubjectFindings removes any subject_mismatch or
// cross_agenda_contamination finding whose NodeIDs name itemID and no other
// node from findings. It is the symmetric exclusion the heuristic
// non-worsening gate applies to both the before- and after-state defect
// counts for a fixed-agenda-return-exempt operation (design brief D5
// addendum / §8.2): deterministicTreeAuditPrecheck's own findings never
// populate NodeIDs with more than the one flagged node for these two finding
// types, so "names itemID and no other node" is equivalent to "is about
// itemID alone" - a finding that also names other nodes (which the current
// finding shapes never produce, but a future change might) is conservatively
// left in place rather than assumed to be about the moved item alone.
func treeAuditExcludeSelfSubjectFindings(findings []treeAuditPrecheckFinding, itemID string) []treeAuditPrecheckFinding {
	filtered := make([]treeAuditPrecheckFinding, 0, len(findings))
	for _, finding := range findings {
		if (finding.Type == TreeAuditSubjectMismatch || finding.Type == TreeAuditCrossAgendaContamination) &&
			len(finding.NodeIDs) == 1 && finding.NodeIDs[0] == itemID {
			continue
		}
		filtered = append(filtered, finding)
	}
	return filtered
}

// treeAuditPrecheckAgrees implements precheckAgreementBonus (design D4): it
// reports whether the deterministic precheck already flagged one of
// targetIDs with a subject_mismatch / cross_agenda_contamination /
// candidate_should_fold_into_existing_topic finding whose RelatedNodeIDs
// name destinationID or its top-level container, or a
// reference_evidence_reparent finding naming the target at all.
// reference_evidence_reparent carries no RelatedNodeIDs (deterministicTreeAuditPrecheck
// never populates them for that finding type): it flags that the target's
// *current* placement is itself only reparented on recap/reference evidence,
// which corroborates moving it away regardless of the specific destination,
// so it is matched by target alone.
func treeAuditPrecheckAgrees(findings []treeAuditPrecheckFinding, targetIDs []string, destinationID string, tree *liveAnalysisTree) bool {
	if len(targetIDs) == 0 {
		return false
	}
	destinationTop := treeAuditTopContainerID(tree, destinationID)
	for _, finding := range findings {
		matchesTarget := false
		for _, id := range targetIDs {
			if containsExactString(finding.NodeIDs, id) {
				matchesTarget = true
				break
			}
		}
		if !matchesTarget {
			continue
		}
		switch finding.Type {
		case TreeAuditReferenceEvidenceReparent:
			return true
		case TreeAuditSubjectMismatch, TreeAuditCrossAgendaContamination, TreeAuditCandidateShouldFoldIntoTopic:
			if destinationID != "" && containsExactString(finding.RelatedNodeIDs, destinationID) {
				return true
			}
			if destinationTop != "" && containsExactString(finding.RelatedNodeIDs, destinationTop) {
				return true
			}
		}
	}
	return false
}

// treeAuditFixedAgendaMatches implements the compatibility-named
// fixedAgendaMatchBonus (design D4): the destination has an agenda-linked
// materialized topic ancestor, the destination's own
// cohesion with subjectText already clears CohesionThreshold, and the
// destination lineage text (which includes the fixed agenda ancestor's own
// label, since it is literally an ancestor node) shares a subject term with
// subjectText.
func treeAuditFixedAgendaMatches(destinationID, subjectText string, newScore float64, tree *liveAnalysisTree, mc *meetingContext, cfg TreeAuditConfig) bool {
	if destinationID == "" || mc == nil {
		return false
	}
	agendaTopicID := treeAuditFixedAgendaAncestor(destinationID, tree)
	if agendaTopicID == "" || newScore < cfg.CohesionThreshold {
		return false
	}
	destinationText := treeAuditParentChainText(tree, destinationID)
	agendaTopic := liveTreeNodeByID(tree, agendaTopicID)
	if agendaTopic == nil {
		return false
	}
	refs := topicAgendaRefs(*agendaTopic, agendaRecordMap(mc))
	for _, agenda := range mc.Agenda {
		if !containsExactString(refs, agenda.ID) {
			continue
		}
		return sharedTreeAuditSubjectTerm(subjectText, agenda.Title+" "+destinationText)
	}
	return false
}

// treeAuditHasRecapContamination implements recapContaminationPenalty
// (design D4): the operation's own evidence mixes at least one reference/
// recap-role sequence with at least one primary/supporting one. Evidence
// that is entirely reference-role is a separate hard rejection
// ("reference_evidence_only") in the appliers themselves, independent of
// this penalty.
func treeAuditHasRecapContamination(evidence []int64, evidenceRoles map[int64]treeAuditEvidenceRole) bool {
	hasReference, hasNonReference := false, false
	for _, sequenceNo := range evidence {
		if evidenceRoles[sequenceNo] == treeAuditEvidenceReference {
			hasReference = true
		} else {
			hasNonReference = true
		}
	}
	return hasReference && hasNonReference
}

// treeAuditEffectiveConfidence computes the server-adjusted confidence that
// the HighConfidenceThreshold gate compares against for a move-type
// operation (design D4): effective = clamp01(modelConfidence + bonuses -
// penalty). Bonuses (unclassifiedOrGenericParentBonus,
// precheckAgreementBonus, fixedAgendaMatchBonus; +0.05 each, capped at
// +0.15 total) are granted only when modelConfidence itself is already
// >= 0.60; recapContaminationPenalty (-0.10) applies regardless. It never
// rejects the operation itself or lowers HighConfidenceThreshold - it only
// changes what a given modelConfidence is compared against. Existence and
// structural validity (unknown target, wrong kind, cycles, depth, ...) are
// still verified by applyOneTreeAuditOperation afterward.
func treeAuditEffectiveConfidence(operation treeAuditOperation, state liveAnalysisPayload, beforeFindings []treeAuditPrecheckFinding, evidenceRoles map[int64]treeAuditEvidenceRole, segmentText map[int64]string, mc *meetingContext, cfg TreeAuditConfig) float64 {
	model := operation.Confidence
	var (
		targetIDs      []string
		currentGeneric bool
		destinationID  string
		subjectText    string
		newScore       float64
	)
	switch operation.Type {
	case TreeAuditMoveItem, TreeAuditRestorePreviousParent:
		item := findItemByID(state.Items, operation.TargetCanonicalItemID)
		if item == nil {
			return model
		}
		subjectText = treeAuditOperationItemText(*item, operation.EvidenceSequenceNos, segmentText, evidenceRoles)
		currentGeneric = treeAuditParentIsGenericOrUnclassified(state.Tree, operation.FromParentCanonicalNodeID, subjectText, cfg)
		destinationID = operation.ToParentCanonicalNodeID
		newScore = semanticItemSimilarity(subjectText, treeAuditParentChainText(state.Tree, destinationID))
		targetIDs = []string{operation.TargetCanonicalItemID}
	case TreeAuditMoveNode:
		subjectText = treeAuditSubtreeText(operation.TargetCanonicalNodeID, state.Tree)
		currentGeneric = treeAuditParentIsGenericOrUnclassified(state.Tree, operation.FromParentCanonicalNodeID, subjectText, cfg)
		destinationID = operation.ToParentCanonicalNodeID
		newScore = semanticItemSimilarity(subjectText, treeAuditParentChainText(state.Tree, destinationID))
		targetIDs = []string{operation.TargetCanonicalNodeID}
	case TreeAuditFoldCandidateIntoTopic:
		targetIDs = operation.TargetCanonicalItemIDs
		if len(targetIDs) == 0 {
			for _, candidate := range state.EmergingTopics {
				if candidate.ID == operation.TargetCandidateID {
					targetIDs = candidate.EvidenceItemIDs
					break
				}
			}
		}
		if len(targetIDs) == 0 {
			return model
		}
		destinationID = operation.ToParentCanonicalNodeID
		allGeneric := true
		minNew := 1.0
		var combined strings.Builder
		found := false
		for _, id := range targetIDs {
			item := findItemByID(state.Items, id)
			node := liveTreeNodeByID(state.Tree, id)
			if item == nil || node == nil {
				continue
			}
			found = true
			itemText := treeAuditOperationItemText(*item, operation.EvidenceSequenceNos, segmentText, evidenceRoles)
			combined.WriteString(" " + itemText)
			if !treeAuditParentIsGenericOrUnclassified(state.Tree, node.ParentID, itemText, cfg) {
				allGeneric = false
			}
			itemNewScore := semanticItemSimilarity(itemText, treeAuditParentChainText(state.Tree, destinationID))
			if itemNewScore < minNew {
				minNew = itemNewScore
			}
		}
		if !found {
			return model
		}
		subjectText = combined.String()
		currentGeneric = allGeneric
		newScore = minNew
	default:
		return model
	}
	bonus := 0.0
	if model >= treeAuditConfidenceBonusFloor {
		if currentGeneric {
			bonus += treeAuditConfidenceBonusStep
		}
		if treeAuditPrecheckAgrees(beforeFindings, targetIDs, destinationID, state.Tree) {
			bonus += treeAuditConfidenceBonusStep
		}
		if treeAuditFixedAgendaMatches(destinationID, subjectText, newScore, state.Tree, mc, cfg) {
			bonus += treeAuditConfidenceBonusStep
		}
		if bonus > treeAuditConfidenceBonusCap {
			bonus = treeAuditConfidenceBonusCap
		}
	}
	penalty := 0.0
	if treeAuditHasRecapContamination(operation.EvidenceSequenceNos, evidenceRoles) {
		penalty = treeAuditConfidenceContaminationPenalty
	}
	effective := model + bonus - penalty
	if effective < 0 {
		effective = 0
	}
	if effective > 1 {
		effective = 1
	}
	return effective
}

func cloneLiveAnalysisPayload(value liveAnalysisPayload) liveAnalysisPayload {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned liveAnalysisPayload
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return value
	}
	return cloned
}

// treeAuditRemovableEmptyContainerKind reports whether node is a container
// kind that a childless state makes safely deletable: a non-manual group, a
// promoted dynamic topic, or the synthetic system unclassified bucket.
// Fixed agenda topics (origin=agenda), root, action_summary, and manually
// changed containers are excluded. remove_empty_group's own applier and
// treeAuditCascadePruneEmptyContainers both use this single definition.
func treeAuditRemovableEmptyContainerKind(node liveAnalysisTreeNode) bool {
	if treeAuditIsManualChangeSource(node.LastParentChangeSource) {
		return false
	}
	if node.Kind == "group" {
		return true
	}
	if node.Kind == "topic" && node.ID == treeUnclassifiedTopicID {
		return node.Origin == topicOriginSystem
	}
	return node.Kind == "topic" && node.Origin == topicOriginDynamic &&
		node.ID != treeRootNodeID && node.AgendaRole != agendaRoleActionSummary
}

// treeAuditCascadeRemoveEmptyAncestors removes nodeID from tree if it is
// currently a childless removable container (treeAuditRemovableEmptyContainerKind),
// then repeats the same check on its own former parent, cascading upward
// until it reaches a node that either still has children or is not itself a
// removable container kind (or root). It does not rebuild tree.Edges - the
// caller does that once after all cascading from a single operation is
// done.
func treeAuditCascadeRemoveEmptyAncestors(tree *liveAnalysisTree, nodeID string) bool {
	if tree == nil {
		return false
	}
	removedAny := false
	seen := map[string]struct{}{}
	for nodeID != "" && nodeID != treeRootNodeID {
		if _, loop := seen[nodeID]; loop {
			break
		}
		seen[nodeID] = struct{}{}
		nodeAt := -1
		for index, candidate := range tree.Nodes {
			if candidate.ID == nodeID {
				nodeAt = index
				break
			}
		}
		if nodeAt < 0 {
			break
		}
		node := tree.Nodes[nodeAt]
		if !treeAuditRemovableEmptyContainerKind(node) {
			break
		}
		hasChild := false
		for _, other := range tree.Nodes {
			if other.ParentID == nodeID {
				hasChild = true
				break
			}
		}
		if hasChild {
			break
		}
		parentID := node.ParentID
		tree.Nodes = append(tree.Nodes[:nodeAt], tree.Nodes[nodeAt+1:]...)
		removedAny = true
		nodeID = parentID
	}
	return removedAny
}

// treeAuditCascadePruneEmptyContainers (design brief D5 addendum / §9.2)
// folds away any group/dynamic-topic container that an already-accepted
// operation just emptied, cascading up through its own ancestors, mirroring
// what the live (non-audit) pipeline's own pruneEmptyDynamicTopics
// (ai_proposition.go) does outside the audit path. validateTreeIntegrity
// treats a childless "group"-kind node as a hard integrity violation, so
// without this step an operation that moves/merges/deactivates a
// container's last remaining child would be rejected by the very emptiness
// it just created, even though removing that now-empty container in the
// same step is the correct structural outcome.
//
// It only considers containers that actually lost a child as a direct
// result of comparing before/after parent-of relationships - never
// unrelated pre-existing empty containers elsewhere in the tree (the run's
// starting tree was already integrity-checked, so none should exist
// entering this pass regardless).
func treeAuditCascadePruneEmptyContainers(state *liveAnalysisPayload, before liveAnalysisPayload) {
	if state == nil || state.Tree == nil || before.Tree == nil {
		return
	}
	beforeParent := make(map[string]string, len(before.Tree.Nodes))
	for _, node := range before.Tree.Nodes {
		beforeParent[node.ID] = node.ParentID
	}
	afterParent := make(map[string]string, len(state.Tree.Nodes))
	for _, node := range state.Tree.Nodes {
		afterParent[node.ID] = node.ParentID
	}
	seedParents := make(map[string]struct{})
	for id, oldParent := range beforeParent {
		if oldParent == "" {
			continue
		}
		newParent, stillPresent := afterParent[id]
		if !stillPresent || newParent != oldParent {
			seedParents[oldParent] = struct{}{}
		}
	}
	if len(seedParents) == 0 {
		return
	}
	removedAny := false
	for parentID := range seedParents {
		if treeAuditCascadeRemoveEmptyAncestors(state.Tree, parentID) {
			removedAny = true
		}
	}
	if removedAny {
		rebuildTreeAuditEdges(state.Tree)
	}
}

func rebuildTreeAuditEdges(tree *liveAnalysisTree) {
	if tree == nil {
		return
	}
	ids := make(map[string]struct{}, len(tree.Nodes))
	for _, node := range tree.Nodes {
		ids[node.ID] = struct{}{}
	}
	edges := make([]liveAnalysisTreeEdge, 0, len(tree.Nodes)-1)
	for _, node := range tree.Nodes {
		if node.ID == treeRootNodeID || node.ParentID == "" {
			continue
		}
		edges = append(edges, liveAnalysisTreeEdge{Source: node.ParentID, Target: node.ID})
	}
	validRelations := tree.Relations[:0]
	for _, relation := range tree.Relations {
		if _, source := ids[relation.Source]; !source {
			continue
		}
		if _, target := ids[relation.Target]; !target {
			continue
		}
		validRelations = append(validRelations, relation)
	}
	tree.Edges = edges
	tree.Relations = validRelations
}

func treeAuditFixedAgendaAncestor(id string, tree *liveAnalysisTree) string {
	if tree == nil {
		return ""
	}
	byID := make(map[string]liveAnalysisTreeNode, len(tree.Nodes))
	for _, node := range tree.Nodes {
		byID[node.ID] = node
	}
	seen := map[string]struct{}{}
	for id != "" && id != treeRootNodeID {
		if _, loop := seen[id]; loop {
			return ""
		}
		seen[id] = struct{}{}
		node, exists := byID[id]
		if !exists {
			return ""
		}
		if node.Kind == "topic" && node.AgendaRole != agendaRoleActionSummary && (node.Origin == topicOriginAgenda || node.Origin == topicOriginMixed || len(node.AgendaRefs) > 0) {
			return node.ID
		}
		id = node.ParentID
	}
	return ""
}

func treeAuditDepthFromParent(id string, tree *liveAnalysisTree) int {
	if tree == nil {
		return treeHardMaxDepth + 1
	}
	parents := make(map[string]string, len(tree.Nodes))
	for _, node := range tree.Nodes {
		parents[node.ID] = node.ParentID
	}
	depth := 0
	seen := map[string]struct{}{}
	for id != "" && id != treeRootNodeID {
		if _, loop := seen[id]; loop {
			return treeHardMaxDepth + 1
		}
		seen[id] = struct{}{}
		depth++
		id = parents[id]
	}
	return depth
}

// treeAuditIsAncestorOf reports whether ancestorID is nodeID itself or a
// strict ancestor of nodeID, walking up nodeID's parent chain. It is used by
// the move_node applier to reject moving a container into its own subtree
// (a cycle): the check is done against the destination parent, so
// ancestorID==nodeID (destination is the node itself) also reports true.
func treeAuditIsAncestorOf(ancestorID, nodeID string, tree *liveAnalysisTree) bool {
	if tree == nil || ancestorID == "" || nodeID == "" {
		return false
	}
	parents := make(map[string]string, len(tree.Nodes))
	for _, node := range tree.Nodes {
		parents[node.ID] = node.ParentID
	}
	seen := map[string]struct{}{}
	current := nodeID
	for current != "" {
		if current == ancestorID {
			return true
		}
		if _, loop := seen[current]; loop {
			return false
		}
		seen[current] = struct{}{}
		current = parents[current]
	}
	return false
}

// treeAuditSubtreeHeight returns the number of edges from id down to its
// deepest descendant (0 for a leaf). It is used by the move_node applier to
// verify the moved subtree still fits within treeHardMaxDepth at its new
// position.
func treeAuditSubtreeHeight(id string, tree *liveAnalysisTree) int {
	if tree == nil {
		return 0
	}
	children := make(map[string][]string, len(tree.Nodes))
	for _, node := range tree.Nodes {
		if node.ID != treeRootNodeID && node.ParentID != "" {
			children[node.ParentID] = append(children[node.ParentID], node.ID)
		}
	}
	var height func(string, map[string]struct{}) int
	height = func(nodeID string, seen map[string]struct{}) int {
		if _, loop := seen[nodeID]; loop {
			return 0
		}
		seen[nodeID] = struct{}{}
		best := 0
		for _, child := range children[nodeID] {
			if h := height(child, seen) + 1; h > best {
				best = h
			}
		}
		return best
	}
	return height(id, map[string]struct{}{})
}

// treeAuditSubtreeText concatenates id's own label/description with every
// descendant's label/description (recursively), for the move_node applier's
// subject-alignment check against the destination parent chain.
func treeAuditSubtreeText(id string, tree *liveAnalysisTree) string {
	if tree == nil {
		return ""
	}
	byID := make(map[string]liveAnalysisTreeNode, len(tree.Nodes))
	children := make(map[string][]string, len(tree.Nodes))
	for _, node := range tree.Nodes {
		byID[node.ID] = node
		if node.ID != treeRootNodeID && node.ParentID != "" {
			children[node.ParentID] = append(children[node.ParentID], node.ID)
		}
	}
	var parts []string
	var walk func(string, map[string]struct{})
	walk = func(nodeID string, seen map[string]struct{}) {
		if _, loop := seen[nodeID]; loop {
			return
		}
		seen[nodeID] = struct{}{}
		node := byID[nodeID]
		parts = append(parts, node.Label, node.Description)
		for _, child := range children[nodeID] {
			walk(child, seen)
		}
	}
	walk(id, map[string]struct{}{})
	return strings.Join(parts, " ")
}

// treeAuditItemsMergeable reports whether a and b are safe to fold into one
// canonical item under the merge_items applier: either the existing
// same-kind duplicate/canonical-proposition helpers already used elsewhere
// in this package agree they represent one proposition, or they were
// already stamped with the same PropositionKey by a previous
// canonicalizePropositionItems pass. sameCanonicalProposition itself never
// matches a pair where either side is kind "decision", so a decision/
// non-decision pair can only connect through an exact PropositionKey match -
// deliberately a very narrow bar, keeping cross-kind decision merges rare.
func treeAuditItemsMergeable(a, b liveAnalysisItem) bool {
	if matched, _ := sameKindSemanticDuplicate(a, b); matched {
		return true
	}
	if sameCanonicalProposition(a, b) {
		return true
	}
	if a.PropositionKey != "" && a.PropositionKey == b.PropositionKey {
		return true
	}
	return false
}

func treeAuditLowInformationItem(item liveAnalysisItem, segmentText map[int64]string, evidenceRoles map[int64]treeAuditEvidenceRole) bool {
	if metaOnlyLiveItemText(item.Title + " " + item.Body) {
		return true
	}
	scope := liveEvidenceScope{
		Allowed: make(map[int64]struct{}), CurrentRound: make(map[int64]struct{}),
		TranscriptText: make(map[int64]string), Segments: make(map[int64]domain.TranscriptSegment),
	}
	timeline := discourseTimeline{Roles: make(map[int64]liveEvidenceRole), DetectedRoles: make(map[int64]liveUtteranceRole)}
	for sequenceNo, text := range segmentText {
		scope.Allowed[sequenceNo] = struct{}{}
		scope.TranscriptText[sequenceNo] = text
		if sequenceNo > scope.CoveredThrough {
			scope.CoveredThrough = sequenceNo
		}
		switch evidenceRoles[sequenceNo] {
		case treeAuditEvidenceReference:
			timeline.Roles[sequenceNo] = liveEvidenceReferenceRecap
			timeline.DetectedRoles[sequenceNo] = liveUtteranceRecap
		case treeAuditEvidencePrimary:
			timeline.Roles[sequenceNo] = liveEvidencePrimary
			timeline.DetectedRoles[sequenceNo] = liveUtteranceSubstantive
		default:
			timeline.Roles[sequenceNo] = liveEvidenceSupporting
		}
	}
	reason, _ := validateLiveItemInformation(item, false, timeline, scope)
	return reason != ""
}

func treeAuditDeactivationTombstoneReason(item liveAnalysisItem, operation treeAuditOperation, segmentText map[int64]string, evidenceRoles map[int64]treeAuditEvidenceRole) string {
	for _, sequenceNo := range item.EvidenceSequenceNos {
		if classifyDiscourseAct(segmentText[sequenceNo]) == discourseTopicTransition || classifyDiscourseAct(segmentText[sequenceNo]) == discourseMeetingControl || classifyDiscourseAct(segmentText[sequenceNo]) == discourseFiller {
			return "discourse_only"
		}
	}
	if metaOnlyLiveItemText(item.Title+" "+item.Body) || treeAuditLowInformationItem(item, segmentText, evidenceRoles) {
		return "low_information"
	}
	if allTreeAuditEvidenceReference(item.EvidenceSequenceNos, evidenceRoles) {
		return "recap_only"
	}
	return operation.Reason
}

// treeAuditMergeTargetsConnected reports whether every item in items is
// reachable from items[0] via treeAuditItemsMergeable edges. merge_items
// does not require every pair to match directly, only that the whole target
// set forms one connected group of duplicates/paraphrases.
func treeAuditMergeTargetsConnected(items []liveAnalysisItem) bool {
	n := len(items)
	if n == 0 {
		return false
	}
	visited := make([]bool, n)
	visited[0] = true
	queue := []int{0}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for j := 0; j < n; j++ {
			if visited[j] {
				continue
			}
			if treeAuditItemsMergeable(items[current], items[j]) {
				visited[j] = true
				queue = append(queue, j)
			}
		}
	}
	for _, ok := range visited {
		if !ok {
			return false
		}
	}
	return true
}

// mergeTreeAuditItemAttributes folds companion's evidence and attributes
// into canonical (the merge survivor), unioning list-shaped fields and
// keeping the survivor's own non-empty resolution metadata unless it is
// empty and companion has a value. It never changes canonical's Title,
// Body, Kind, or classification fields - the merge_items applier is a
// consolidation of evidence/attributes, not a rewrite.
func mergeTreeAuditItemAttributes(canonical, companion liveAnalysisItem) liveAnalysisItem {
	canonical.EvidenceSequenceNos = appendUniqueSequences(canonical.EvidenceSequenceNos, companion.EvidenceSequenceNos)
	canonical.EvidenceRoles = mergeTreeAuditEvidenceRoles(canonical.EvidenceRoles, companion.EvidenceRoles)
	canonical.RelatedQuestions = appendUniqueText(canonical.RelatedQuestions, companion.RelatedQuestions...)
	canonical.ResolutionConditions = appendUniqueText(canonical.ResolutionConditions, companion.ResolutionConditions...)
	canonical.NextActions = appendUniqueText(canonical.NextActions, companion.NextActions...)
	canonical.RelatedAgendaIDs = uniqueNonEmptyIDs(append(canonical.RelatedAgendaIDs, companion.RelatedAgendaIDs...))
	if canonical.InformationStatus != informationStatusGrounded && companion.InformationStatus == informationStatusGrounded {
		canonical.InformationStatus = informationStatusGrounded
	}
	if canonical.Status != "resolved" && companion.Status == "resolved" {
		canonical.Status = companion.Status
	}
	if canonical.ResolvedAtVersion == 0 && companion.ResolvedAtVersion != 0 {
		canonical.ResolvedAtVersion = companion.ResolvedAtVersion
	}
	if len(canonical.ResolutionEvidenceSequenceNos) == 0 && len(companion.ResolutionEvidenceSequenceNos) > 0 {
		canonical.ResolutionEvidenceSequenceNos = append([]int64(nil), companion.ResolutionEvidenceSequenceNos...)
	}
	if canonical.ResolutionReason == "" && companion.ResolutionReason != "" {
		canonical.ResolutionReason = companion.ResolutionReason
	}
	if canonical.ReopenedAtVersion == 0 && companion.ReopenedAtVersion != 0 {
		canonical.ReopenedAtVersion = companion.ReopenedAtVersion
	}
	if len(canonical.ReopenEvidenceSequenceNos) == 0 && len(companion.ReopenEvidenceSequenceNos) > 0 {
		canonical.ReopenEvidenceSequenceNos = append([]int64(nil), companion.ReopenEvidenceSequenceNos...)
	}
	if canonical.ReopenReason == "" && companion.ReopenReason != "" {
		canonical.ReopenReason = companion.ReopenReason
	}
	return canonical
}

// mergeTreeAuditEvidenceRoles unions two liveEvidenceRoleRef lists by
// SequenceNo, keeping base's entry when both sides annotate the same
// sequence number.
func mergeTreeAuditEvidenceRoles(base, additions []liveEvidenceRoleRef) []liveEvidenceRoleRef {
	seen := make(map[int64]struct{}, len(base))
	for _, ref := range base {
		seen[ref.SequenceNo] = struct{}{}
	}
	for _, ref := range additions {
		if _, exists := seen[ref.SequenceNo]; exists {
			continue
		}
		seen[ref.SequenceNo] = struct{}{}
		base = append(base, ref)
	}
	return base
}

func auditHeuristicDefectCount(findings []treeAuditPrecheckFinding) int {
	count := 0
	for _, finding := range findings {
		switch finding.Type {
		case TreeAuditSubjectMismatch, TreeAuditCrossAgendaContamination,
			TreeAuditCandidateMixedSubjects, TreeAuditTopicOutlier,
			TreeAuditGroupOutlier:
			count++
		}
	}
	return count
}

func countTreeAuditPrechecks(findings []treeAuditPrecheckFinding, types ...TreeAuditFindingType) int {
	wanted := make(map[TreeAuditFindingType]struct{}, len(types))
	for _, findingType := range types {
		wanted[findingType] = struct{}{}
	}
	count := 0
	for _, finding := range findings {
		if _, ok := wanted[finding.Type]; ok {
			count++
		}
	}
	return count
}

func containsInt64(values []int64, target int64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func hasTreeAuditEvidenceRole(sequenceNos []int64, roles map[int64]treeAuditEvidenceRole, wanted treeAuditEvidenceRole) bool {
	for _, sequenceNo := range sequenceNos {
		if roles[sequenceNo] == wanted {
			return true
		}
	}
	return false
}

func marshalAuditedLivePayload(state liveAnalysisPayload) (json.RawMessage, error) {
	encoded, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("marshal audited live payload: %w", err)
	}
	return encoded, nil
}
