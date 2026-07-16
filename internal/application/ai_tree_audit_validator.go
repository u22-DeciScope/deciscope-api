package application

import (
	"encoding/json"
	"fmt"
	"strings"

	"deciscope-core-api/internal/domain"
)

func validateAndDryRunTreeAuditOperations(original liveAnalysisPayload, operations []treeAuditOperation, segments []domain.TranscriptSegment, mc *meetingContext, evidenceRoles map[int64]treeAuditEvidenceRole, cfg TreeAuditConfig, runID string, resultingVersion int64, markApplied bool) (liveAnalysisPayload, treeAuditValidatorResult) {
	cfg = cfg.normalized()
	dry := cloneLiveAnalysisPayload(original)
	result := treeAuditValidatorResult{OperationsProposed: len(operations)}
	accepted := make(map[string]bool, len(operations))
	segmentText := make(map[int64]string, len(segments))
	for _, segment := range segments {
		segmentText[segment.SequenceNo] = segment.Text
	}
	beforeFindings := deterministicTreeAuditPrecheck(original, mc, evidenceRoles, cfg)
	beforeQuality := auditHeuristicDefectCount(beforeFindings)
	result.HeuristicDefectCountBefore = beforeQuality
	result.TopicOutliersBefore = countTreeAuditPrechecks(beforeFindings, TreeAuditTopicOutlier, TreeAuditSubjectMismatch)
	result.CandidateFragmentationBefore = countTreeAuditPrechecks(beforeFindings, TreeAuditCandidateFragmentation)
	result.CrossAgendaContaminationBefore = countTreeAuditPrechecks(beforeFindings, TreeAuditCrossAgendaContamination)

	for _, operation := range operations {
		evaluation := treeAuditValidatorEvaluation{OperationID: operation.OperationID, Type: operation.Type, Result: "rejected"}
		dependencyOK := true
		for _, dependency := range operation.DependsOnOperationIDs {
			if !accepted[dependency] {
				dependencyOK = false
				break
			}
		}
		if !dependencyOK {
			evaluation.Reason = "dependency_rejected"
			result.Evaluations = append(result.Evaluations, evaluation)
			continue
		}
		if operation.Confidence < cfg.HighConfidenceThreshold {
			evaluation.Reason = "below_high_confidence_threshold"
			result.Evaluations = append(result.Evaluations, evaluation)
			continue
		}
		if !treeAuditAutoApplyOperationAllowed(operation.Type) {
			evaluation.Reason = "shadow_only_operation"
			result.Evaluations = append(result.Evaluations, evaluation)
			continue
		}

		candidate := cloneLiveAnalysisPayload(dry)
		currentScore, newScore, reason := applyOneTreeAuditOperation(&candidate, operation, segmentText, evidenceRoles, mc, cfg, runID, resultingVersion)
		evaluation.CurrentParentScore = currentScore
		evaluation.NewParentScore = newScore
		evaluation.Improvement = newScore - currentScore
		if reason != "" {
			evaluation.Reason = reason
			result.Evaluations = append(result.Evaluations, evaluation)
			continue
		}
		integrity := validateTreeIntegrity(candidate.Tree, candidate.Items, mc)
		if !integrity.Valid {
			evaluation.Reason = "tree_integrity_rejected"
			result.Evaluations = append(result.Evaluations, evaluation)
			continue
		}
		afterQuality := auditHeuristicDefectCount(deterministicTreeAuditPrecheck(candidate, mc, evidenceRoles, cfg))
		if afterQuality > beforeQuality {
			evaluation.Reason = "heuristic_structural_quality_worsened"
			result.Evaluations = append(result.Evaluations, evaluation)
			continue
		}
		dry = candidate
		beforeQuality = afterQuality
		accepted[operation.OperationID] = true
		evaluation.Result = "validated"
		evaluation.WouldApply = true
		evaluation.Applied = markApplied
		result.OperationsWouldApply++
		if markApplied {
			result.OperationsApplied++
		}
		result.Evaluations = append(result.Evaluations, evaluation)
	}
	result.OperationsRejected = result.OperationsProposed - result.OperationsWouldApply
	result.TreeIntegrityValid = validateTreeIntegrity(dry.Tree, dry.Items, mc).Valid
	afterFindings := deterministicTreeAuditPrecheck(dry, mc, evidenceRoles, cfg)
	result.TopicOutliersAfter = countTreeAuditPrechecks(afterFindings, TreeAuditTopicOutlier, TreeAuditSubjectMismatch)
	result.CandidateFragmentationAfter = countTreeAuditPrechecks(afterFindings, TreeAuditCandidateFragmentation)
	result.CrossAgendaContaminationAfter = countTreeAuditPrechecks(afterFindings, TreeAuditCrossAgendaContamination)
	result.HeuristicDefectCountAfter = auditHeuristicDefectCount(afterFindings)
	if result.OperationsWouldApply > 0 {
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
	return dry, result
}

// treeAuditAutoApplyOperationAllowed is intentionally narrow. Every operation
// not listed here is recorded and dry-run as shadow-only, regardless of model
// confidence. Expanding this list requires adding operation-specific safety
// invariants and focused tests.
func treeAuditAutoApplyOperationAllowed(operationType TreeAuditOperationType) bool {
	switch operationType {
	case TreeAuditMoveItem:
		return true
	default:
		return false
	}
}

func applyOneTreeAuditOperation(state *liveAnalysisPayload, operation treeAuditOperation, segmentText map[int64]string, evidenceRoles map[int64]treeAuditEvidenceRole, mc *meetingContext, cfg TreeAuditConfig, runID string, resultingVersion int64) (float64, float64, string) {
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
		nodeAt, nodeExists := nodeIndex[operation.NodeID]
		itemAt, itemExists := itemIndex[operation.NodeID]
		if nodeExists && !itemExists && (state.Tree.Nodes[nodeAt].ID == treeRootNodeID || state.Tree.Nodes[nodeAt].Origin == topicOriginAgenda) {
			return 0, 0, "fixed_agenda_immutable"
		}
		if !nodeExists || !itemExists {
			return 0, 0, "unknown_target_node"
		}
		node := state.Tree.Nodes[nodeAt]
		if node.Kind == "topic" || node.Kind == "group" || node.ID == treeRootNodeID {
			return 0, 0, "target_kind_not_movable_detail"
		}
		if strings.TrimSpace(operation.FromParentID) == "" || node.ParentID != operation.FromParentID {
			return 0, 0, "from_parent_mismatch"
		}
		if operation.NodeID == operation.ToParentID {
			return 0, 0, "self_parent"
		}
		toAt, toExists := nodeIndex[operation.ToParentID]
		if !toExists || operation.ToParentID == treeRootNodeID {
			return 0, 0, "unknown_or_root_target_parent"
		}
		to := state.Tree.Nodes[toAt]
		if to.Kind != "topic" && to.Kind != "group" {
			return 0, 0, "invalid_target_parent_kind"
		}
		if to.AgendaRole == agendaRoleActionSummary {
			return 0, 0, "action_summary_parent"
		}
		if operation.FromParentID == operation.ToParentID || operation.NodeID == operation.ToParentID {
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
		if node.LastParentChangeVersion > 0 && state.TreeVersion-node.LastParentChangeVersion < 2 {
			return 0, 0, "recent_parent_change_sticky"
		}
		for _, sequenceNo := range operation.EvidenceSequenceNos {
			if _, exists := segmentText[sequenceNo]; !exists || !containsInt64(state.Items[itemAt].EvidenceSequenceNos, sequenceNo) {
				return 0, 0, "unbound_operation_evidence"
			}
		}
		semanticText := itemSemanticText(itemAt, operation.EvidenceSequenceNos)
		currentScore := semanticItemSimilarity(semanticText, parentText(operation.FromParentID))
		newScore := semanticItemSimilarity(semanticText, parentText(operation.ToParentID))
		margin := cfg.RequiredImprovementMargin
		if operation.Type == TreeAuditRestorePreviousParent {
			margin *= 0.5
		}
		if newScore-currentScore < margin {
			return currentScore, newScore, "parent_stickiness_margin"
		}
		fromAgenda := treeAuditFixedAgendaAncestor(operation.FromParentID, state.Tree)
		toAgenda := treeAuditFixedAgendaAncestor(operation.ToParentID, state.Tree)
		if fromAgenda != "" && toAgenda != "" && fromAgenda != toAgenda {
			return currentScore, newScore, "cross_fixed_agenda_boundary"
		}
		if treeAuditDepthFromParent(operation.ToParentID, state.Tree)+1 > treeHardMaxDepth {
			return currentScore, newScore, "hard_depth_limit"
		}
		state.Tree.Nodes[nodeAt].ParentID = operation.ToParentID
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

	case TreeAuditFoldCandidateIntoTopic:
		candidateAt := -1
		for index := range state.EmergingTopics {
			if state.EmergingTopics[index].ID == operation.CandidateID {
				candidateAt = index
				break
			}
		}
		toAt, toExists := nodeIndex[operation.ToParentID]
		if candidateAt < 0 || !toExists || state.Tree.Nodes[toAt].Kind != "topic" || state.Tree.Nodes[toAt].ID == treeRootNodeID || state.Tree.Nodes[toAt].AgendaRole == agendaRoleActionSummary {
			return 0, 0, "invalid_candidate_or_topic"
		}
		targetIDs := operation.NodeIDs
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
			if !nodeExists || !itemExists || (state.Items[itemAt].CandidateTopicID != operation.CandidateID && state.Items[itemAt].ClassificationStatus != classificationTentative) {
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
			currentScore := semanticItemSimilarity(semanticText, parentText(state.Tree.Nodes[nodeAt].ParentID))
			newScore := semanticItemSimilarity(semanticText, parentText(operation.ToParentID))
			if newScore-currentScore < cfg.RequiredImprovementMargin {
				return currentScore, newScore, "parent_stickiness_margin"
			}
			if currentScore < minCurrent {
				minCurrent = currentScore
			}
			if newScore < minNew {
				minNew = newScore
			}
			state.Tree.Nodes[nodeAt].ParentID = operation.ToParentID
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
			if candidate.ID != operation.CandidateID {
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

	case TreeAuditRenameGroup:
		groupAt, exists := nodeIndex[operation.GroupID]
		if !exists || state.Tree.Nodes[groupAt].Kind != "group" || operation.Label == "" || genericGroupLabel(operation.Label) {
			return 0, 0, "invalid_group_or_label"
		}
		var childText strings.Builder
		for _, node := range state.Tree.Nodes {
			if node.ParentID == operation.GroupID {
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
		groupAt, exists := nodeIndex[operation.GroupID]
		if !exists || state.Tree.Nodes[groupAt].Kind != "group" {
			return 0, 0, "unknown_group"
		}
		for _, node := range state.Tree.Nodes {
			if node.ParentID == operation.GroupID {
				return 0, 0, "group_not_empty"
			}
		}
		state.Tree.Nodes = append(state.Tree.Nodes[:groupAt], state.Tree.Nodes[groupAt+1:]...)
		rebuildTreeAuditEdges(state.Tree)
		return 0, 1, ""

	default:
		return 0, 0, "shadow_only_operation"
	}
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
		if node.Origin == topicOriginAgenda && node.AgendaRole != agendaRoleActionSummary {
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
