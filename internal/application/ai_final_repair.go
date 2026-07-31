package application

import (
	"strings"

	"deciscope-core-api/internal/domain"
)

func finalRepairStatsChanged(stats finalRepairStats) bool {
	return stats.PromotedTopicDuplicatesFolded > 0 ||
		stats.CrossKindDuplicatesMerged > 0 ||
		stats.SameKindDuplicatesMerged > 0 ||
		stats.SameEvidenceSynthesisMerged > 0 ||
		stats.RecapDuplicatesMerged > 0 ||
		stats.LowInformationItemsRewritten > 0 ||
		stats.LowInformationItemsMerged > 0 ||
		stats.LowInformationItemsRejected > 0 ||
		stats.GroundingRewritten > 0 ||
		stats.GroundingTentative > 0 ||
		stats.GroundingCandidateOnly > 0 ||
		stats.GroundingRejected > 0 ||
		stats.KindValidationChanges > 0 ||
		stats.KindSemanticSplits > 0 ||
		stats.KindRelationsCreated > 0 ||
		stats.CorrectionItemsSuperseded > 0 ||
		stats.CorrectionItemsReconstructed > 0 ||
		stats.CorrectionItemsPending > 0 ||
		stats.StrongTodosSynthesized > 0 ||
		stats.StrongDecisionsSynthesized > 0 ||
		stats.EvidenceReferencesPruned > 0 ||
		stats.IssuesRecoveredFromTodoEvidence > 0 ||
		stats.DanglingCandidatesPruned > 0
}

func repairFinalItemKinds(state *liveAnalysisPayload, segments []domain.TranscriptSegment, mc *meetingContext, version int64, stats *finalRepairStats) {
	if state == nil || state.Tree == nil || len(segments) == 0 || stats == nil {
		return
	}
	scope, timeline := agendaTimelineFromSegments(segments)
	kindStats := &liveAnalysisTreeMergeStats{}
	splitPersistedMultiAssignmentTodos(state, scope, kindStats)
	synthesized := synthesizeStrongTodoItems(state.Items, nil, scope, timeline, kindStats)
	synthesized = append(synthesized, synthesizeCorrectionFactItems(
		state.Items, synthesized, scope, timeline, kindStats,
	)...)
	synthesized = append(synthesized, synthesizeExplicitDecisionItems(
		append(append([]liveAnalysisItem(nil), state.Items...), synthesized...),
		segments, kindStats,
	)...)
	addOrUpdateFinalSynthesizedItems(state, synthesized, version)
	splitPersistedItemKinds(state, scope, itemKindValidationFinal, "final_semantic_split", kindStats)
	// Kind repair can turn a legacy compound Issue into a Todo. Split its
	// owner-local assignments before semantic dedup; otherwise the combined
	// body becomes a bridge that collapses different owners back together.
	splitPersistedMultiAssignmentTodos(state, scope, kindStats)
	restoreIssuesFromPollutedTodoEvidence(state, scope, version, kindStats)
	repairFinalItemGrounding(state, scope, mc, version, kindStats)
	repairPersistedItemKinds(state, scope, itemKindValidationFinal, "final_deterministic_repair", kindStats)
	splitPersistedMultiAssignmentTodos(state, scope, kindStats)
	localizePersistedItemEvidence(state.Items, scope, kindStats)
	repairCorrectionSupersessions(state, scope, timeline, version, kindStats)
	recordItemKindDistribution(state, scope, kindStats)
	stats.KindValidationChanges += kindStats.KindValidationChanges
	stats.KindValidationAmbiguous += kindStats.KindValidationAmbiguous
	stats.KindValidationDecisions = append(stats.KindValidationDecisions, kindStats.KindValidationDecisions...)
	stats.KindSemanticSplits += kindStats.KindSemanticSplits
	stats.KindSplitFragments += kindStats.KindSplitFragments
	stats.KindSplitRejected += kindStats.KindSplitRejected
	stats.KindSplitDecisions = append(stats.KindSplitDecisions, kindStats.KindSplitDecisions...)
	stats.KindRelationsCreated += reconcileSemanticKindRelations(
		state.Tree, state.Items, scope, version, "final_repair",
	)
	stats.KindDistributionWarnings = append(stats.KindDistributionWarnings, kindStats.KindDistributionWarnings...)
	stats.CorrectionItemsSuperseded += kindStats.CorrectionItemsSuperseded
	stats.CorrectionItemsReconstructed += kindStats.CorrectionItemsReconstructed
	stats.CorrectionItemsPending += kindStats.CorrectionItemsPending
	stats.CorrectionDecisions = append(stats.CorrectionDecisions, kindStats.CorrectionDecisions...)
	stats.StrongTodoCandidates += kindStats.StrongTodoCandidates
	stats.StrongTodosSynthesized += kindStats.StrongTodosSynthesized
	stats.StrongTodoDuplicatesSuppressed += kindStats.StrongTodoDuplicatesSuppressed
	stats.StrongDecisionCandidates += kindStats.StrongDecisionCandidates
	stats.StrongDecisionsSynthesized += kindStats.StrongDecisionsSynthesized
	stats.DeterministicSynthesisDecisions = append(
		stats.DeterministicSynthesisDecisions,
		kindStats.DeterministicSynthesisDecisions...,
	)
	stats.EvidenceReferencesPruned += kindStats.EvidenceReferencesPruned
	stats.EvidenceLocalizationDecisions = append(stats.EvidenceLocalizationDecisions, kindStats.EvidenceLocalizationDecisions...)
	stats.IssuesRecoveredFromTodoEvidence += kindStats.IssuesRecoveredFromTodoEvidence
	stats.IssueRecoveryDecisions = append(stats.IssueRecoveryDecisions, kindStats.IssueRecoveryDecisions...)
	stats.GroundingAccepted += kindStats.GroundingAccepted
	stats.GroundingRewritten += kindStats.GroundingRewritten
	stats.GroundingTentative += kindStats.GroundingTentative
	stats.GroundingCandidateOnly += kindStats.GroundingCandidateOnly
	stats.GroundingRejected += kindStats.GroundingRejected
	stats.GroundingUnsupportedAtoms += kindStats.GroundingUnsupportedAtoms
	stats.GroundingContextOnlyAtoms += kindStats.GroundingContextOnlyAtoms
	stats.FutureInformationLeaksPrevented += kindStats.GroundingFutureLeaksPrevented
	stats.GroundingDecisions = append(stats.GroundingDecisions, kindStats.GroundingDecisions...)
}

func repairFinalItemGrounding(state *liveAnalysisPayload, scope liveEvidenceScope, mc *meetingContext, version int64, stats *liveAnalysisTreeMergeStats) {
	if state == nil || state.Tree == nil {
		return
	}
	itemIDs := activeFinalItemIDs(state.Items)
	groundingMetadataPresent := false
	for _, itemID := range itemIDs {
		if item, ok := finalItemByID(state.Items, itemID); ok && strings.TrimSpace(item.GroundingDecision) != "" {
			groundingMetadataPresent = true
			break
		}
	}
	// Snapshots created before prompt v18 have no way to prove which semantic
	// gate produced their wording. Keep those snapshots backward compatible;
	// every current live item carries a grounding decision before persistence.
	if !groundingMetadataPresent {
		return
	}
	for _, itemID := range itemIDs {
		item, ok := finalItemByID(state.Items, itemID)
		if !ok {
			continue
		}
		contextItems := make([]liveAnalysisItem, 0, len(state.Items)-1)
		for _, candidate := range state.Items {
			if candidate.ID != item.ID {
				contextItems = append(contextItems, candidate)
			}
		}
		catalog := buildGroundingContextCatalog(mc, contextItems)
		decision, safe := evaluateItemGrounding(item, scope, catalog, "final_grounding_repair", item.semanticSplitFragment)
		recordGroundingDecision(stats, decision)
		previouslyGrounded := item.GroundingDecision == "accepted" || item.GroundingDecision == "rewritten"
		if previouslyGrounded && decision.Decision != "accepted" && decision.Decision != "rewritten" {
			// Finalization may receive only the unanalyzed tail in legacy
			// repositories. A narrower replay scope cannot overturn the
			// successful full live-round grounding decision.
			continue
		}
		if previouslyGrounded && decision.Decision == "rewritten" &&
			finalGroundingRewriteDegradesLabel(item, safe, scope) {
			// The canonical label may combine an antecedent with the immediately
			// following conditional/deictic clause. A per-sentence grounding
			// rewrite must not regress that independently readable label back to
			// the contextual transcript fragment when both cited sequences remain.
			continue
		}
		switch decision.Decision {
		case "accepted", "rewritten":
			if item.GroundingDecision == "rewritten" && decision.Decision == "accepted" {
				// "rewritten" is durable provenance: a later validation of
				// the already-sanitized proposition must not make it appear
				// as though the original model wording was accepted.
				safe.GroundingDecision = item.GroundingDecision
				safe.GroundingConfidence = item.GroundingConfidence
				safe.GroundingUnsupportedAtomHashes = append(
					[]string(nil), item.GroundingUnsupportedAtomHashes...,
				)
			} else {
				safe.GroundingDecision = decision.Decision
				safe.GroundingConfidence = decision.Confidence
				safe.GroundingUnsupportedAtomHashes = append(
					[]string(nil), decision.UnsupportedAtomHashes...,
				)
			}
			safe.GroundingSourceTypes = append([]groundingSourceType(nil), decision.SourceTypes...)
			updateFinalItemAndNode(state, safe)
		default:
			rejectFinalItem(state, item.ID, "final_semantic_grounding_rejected", version)
		}
	}
	pruneEmptyDynamicTopics(state.Tree)
	rebuildTreeAuditEdges(state.Tree)
}

func finalGroundingRewriteDegradesLabel(item, rewritten liveAnalysisItem, scope liveEvidenceScope) bool {
	if incompleteItemLabelEnding(item) != "" || incompleteItemLabelEnding(rewritten) == "" ||
		len(item.GroundingUnsupportedAtomHashes) > 0 {
		return false
	}
	timeline := classifyDiscourseTimeline(scope)
	if !labelFailureRetentionEligible(item, scope, timeline) {
		return false
	}
	for _, sequenceNo := range item.EvidenceSequenceNos {
		if sequenceSuppliesItemReferent(item, sequenceNo, scope) {
			return true
		}
	}
	return false
}

// repairFinalReferenceAndLowInformationItems is the deterministic final-review
// fallback for defects that are safe to resolve without another model call.
// It is transcript-grounded, protects manual parent edits, and removes an item
// only when it cannot be rewritten or merged unambiguously.
func repairFinalReferenceAndLowInformationItems(state *liveAnalysisPayload, segments []domain.TranscriptSegment, version int64, stats *finalRepairStats) {
	if state == nil || state.Tree == nil || len(state.Items) == 0 || len(segments) == 0 {
		return
	}
	scope, timeline := agendaTimelineFromSegments(segments)

	// A recap-only item first gets the same proposition-match opportunity as a
	// live recap diff. Unmatched recap content must satisfy the stricter final
	// novelty gate; corrupted or low-information restatements are retired.
	itemIDs := activeFinalItemIDs(state.Items)
	for _, itemID := range itemIDs {
		item, ok := finalItemByID(state.Items, itemID)
		if !ok || !evidenceIsReferenceOnly(item.EvidenceSequenceNos, timeline) {
			continue
		}
		primary := make([]liveAnalysisItem, 0, len(state.Items))
		for _, candidate := range state.Items {
			if candidate.ID == item.ID || candidate.Inactive || candidate.MergedIntoID != "" ||
				evidenceIsReferenceOnly(candidate.EvidenceSequenceNos, timeline) {
				continue
			}
			primary = append(primary, candidate)
		}
		if at, score := bestPropositionMatch(primary, item); at >= 0 && score >= 0.12 {
			if mergeFinalItemInto(state, item.ID, primary[at].ID, "final_recap_dedup", version) {
				stats.RecapDuplicatesMerged++
			}
			continue
		}
		if recapNovelItemStronglyGrounded(item, primary, scope) {
			continue
		}
		if rejectFinalItem(state, item.ID, "final_recap_low_information", version) {
			stats.LowInformationItemsRejected++
		}
	}

	itemIDs = activeFinalItemIDs(state.Items)
	for _, itemID := range itemIDs {
		item, ok := finalItemByID(state.Items, itemID)
		if !ok || !finalItemIsLowInformation(item) {
			continue
		}
		var incompleteDecision *incompleteItemLabelDecision
		if incompleteItemLabelEnding(item) != "" {
			repaired, decision, changed := repairIncompleteItemLabel(item, scope, timeline)
			incompleteDecision = &decision
			if changed {
				updateFinalItemAndNode(state, repaired)
				stats.LowInformationItemsRewritten++
				stats.IncompleteLabelDecisions = append(stats.IncompleteLabelDecisions, decision)
				continue
			}
		}
		targets := finalLowInformationMergeTargets(state.Items, item)
		if len(targets) == 1 {
			if mergeFinalItemInto(state, item.ID, targets[0], "final_low_information_merge", version) {
				stats.LowInformationItemsMerged++
				if incompleteDecision != nil {
					incompleteDecision.RewriteResult = "merged"
					incompleteDecision.FinalDecision = "merged"
					stats.IncompleteLabelDecisions = append(stats.IncompleteLabelDecisions, *incompleteDecision)
				}
			}
			continue
		}
		if item.Kind == "issue" {
			repaired := item
			if concrete := uniqueFinalIssueRepairText(item, scope, timeline); concrete != "" {
				concrete = normalizeIssueStatementForSubtype(concrete, item.Subtype)
				if title := semanticallyCompleteItemLabel(concrete, item.Kind); title != "" {
					repaired.Title = title
					repaired.Body = truncateRunes(concrete, liveAnalysisTreeDescriptionMaxRunes)
					repaired.Subtype = inferIssueSubtype(concrete, item.Subtype)
					repaired.InformationStatus = informationStatusGrounded
					if reason, _ := validateLiveItemInformation(repaired, true, timeline, scope); reason == "" &&
						splitIssueFragmentGrounded(repaired, scope) {
						updateFinalItemAndNode(state, repaired)
						stats.LowInformationItemsRewritten++
						if incompleteDecision != nil {
							incompleteDecision.RewriteResult = "success"
							incompleteDecision.FinalDecision = "rewritten"
							stats.IncompleteLabelDecisions = append(stats.IncompleteLabelDecisions, *incompleteDecision)
						}
						continue
					}
				}
			}
		}
		if item.Kind == "fact" {
			if repaired, ok := reconstructFinalFactFragment(item, scope, timeline); ok {
				updateFinalItemAndNode(state, repaired)
				stats.LowInformationItemsRewritten++
				if incompleteDecision != nil {
					incompleteDecision.RewriteResult = "success"
					incompleteDecision.FinalDecision = "rewritten"
					stats.IncompleteLabelDecisions = append(stats.IncompleteLabelDecisions, *incompleteDecision)
				}
				continue
			}
		}
		if incompleteDecision != nil &&
			labelFailureRetentionEligible(item, scope, timeline) {
			incompleteDecision.FinalDecision = "retained_degraded"
			stats.IncompleteLabelDecisions = append(stats.IncompleteLabelDecisions, *incompleteDecision)
			continue
		}
		if rejectFinalItem(state, item.ID, "final_low_information_rejected", version) {
			stats.LowInformationItemsRejected++
		}
		if incompleteDecision != nil {
			stats.IncompleteLabelDecisions = append(stats.IncompleteLabelDecisions, *incompleteDecision)
		}
	}
	pruneEmptyDynamicTopics(state.Tree)
	rebuildTreeAuditEdges(state.Tree)
}

func activeFinalItemIDs(items []liveAnalysisItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if !item.Inactive && item.MergedIntoID == "" && item.ID != "" {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func finalItemByID(items []liveAnalysisItem, id string) (liveAnalysisItem, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return liveAnalysisItem{}, false
}

func finalItemIsLowInformation(item liveAnalysisItem) bool {
	return liveItemTextNeedsReferent(item) ||
		incompleteItemLabelEnding(item) != "" ||
		metaOnlyLiveItemText(strings.TrimSpace(item.Title+" "+item.Body)) ||
		isDiscourseOnlyItem(item.Title, item.Body) ||
		isMeetingEndOnlyItem(item.Title, item.Body) ||
		recapArtifactOnlyItem(item.Title, item.Body)
}

func reconstructFinalFactFragment(
	item liveAnalysisItem,
	scope liveEvidenceScope,
	timeline discourseTimeline,
) (liveAnalysisItem, bool) {
	primary := make([]string, 0, 1)
	for _, sequenceNo := range item.EvidenceSequenceNos {
		switch timeline.Roles[sequenceNo] {
		case liveEvidenceReferenceRecap, liveEvidenceDiscourseOnly:
			continue
		}
		text := strings.Trim(strings.TrimSpace(scope.TranscriptText[sequenceNo]), "。.!！ ")
		if text == "" || isDiscourseOnlyItem(text, "") {
			continue
		}
		if !containsExactString(primary, text) {
			primary = append(primary, text)
		}
	}
	if len(primary) != 1 {
		return item, false
	}
	repaired := item
	repaired.Title = semanticallyCompleteItemLabel(primary[0], item.Kind)
	if repaired.Title == "" {
		return item, false
	}
	repaired.Body = truncateRunes(primary[0], liveAnalysisTreeDescriptionMaxRunes)
	repaired.EvidenceSnippets = []string{primary[0]}
	repaired.InformationStatus = informationStatusGrounded
	repaired.GroundingDecision = "rewritten"
	repaired.GroundingConfidence = 0.91
	repaired.GroundingSourceTypes = []groundingSourceType{groundingSourceFinalTranscript}
	decision := evaluateLiveItemKind(repaired, scope, "final_low_information_fact_repair")
	if decision.CanonicalKind != "fact" || decision.Decision == "tentative" {
		return item, false
	}
	return repaired, true
}

func finalLowInformationMergeTargets(items []liveAnalysisItem, item liveAnalysisItem) []string {
	targets := make([]string, 0, 2)
	for _, candidate := range items {
		if candidate.ID == item.ID || candidate.Inactive || candidate.MergedIntoID != "" ||
			finalItemIsLowInformation(candidate) || !itemEvidenceOverlaps(item, candidate) {
			continue
		}
		if item.Kind == "issue" {
			candidateText := candidate.Title + " " + candidate.Body
			if !openIssueMarkerPattern.MatchString(candidateText) &&
				!(candidate.Kind == "issue" && candidate.Status != "resolved" && candidate.Status != "dismissed") {
				continue
			}
		}
		targets = append(targets, candidate.ID)
	}
	return uniqueNonEmptyIDs(targets)
}

func uniqueFinalIssueRepairText(item liveAnalysisItem, scope liveEvidenceScope, timeline discourseTimeline) string {
	concrete := concreteIssueRepairText(item, scope, timeline)
	if concrete == "" || issueTextNeedsReferent(concrete) {
		return ""
	}
	// Rewriting from adjacent context is allowed only if no other equally-near
	// concrete subject competes for the referent.
	bestSequence := int64(0)
	for _, sequenceNo := range item.EvidenceSequenceNos {
		if sequenceNo > bestSequence {
			bestSequence = sequenceNo
		}
	}
	bestDistance := int64(4)
	distinct := make([]string, 0, 2)
	for distance := int64(0); distance <= 3; distance++ {
		for _, sequenceNo := range []int64{bestSequence - distance, bestSequence + distance} {
			if sequenceNo <= 0 || crossesIssueDiscourseBoundary(bestSequence, sequenceNo, timeline) {
				continue
			}
			text := strings.Trim(strings.TrimSpace(scope.TranscriptText[sequenceNo]), "。.!！ ")
			if text == "" || issueTextNeedsReferent(text) || isDiscourseOnlyItem(text, "") {
				continue
			}
			if distance > bestDistance {
				continue
			}
			if distance < bestDistance {
				distinct = distinct[:0]
				bestDistance = distance
			}
			key := semanticIssueKey(text)
			if key != "" && !containsExactString(distinct, key) {
				distinct = append(distinct, key)
			}
		}
		if bestDistance == distance {
			break
		}
	}
	if len(distinct) != 1 {
		return ""
	}
	return concrete
}

func updateFinalItemAndNode(state *liveAnalysisPayload, repaired liveAnalysisItem) {
	for index := range state.Items {
		if state.Items[index].ID == repaired.ID {
			state.Items[index] = repaired
			break
		}
	}
	if node := liveTreeNodeByID(state.Tree, repaired.ID); node != nil {
		node.Label = repaired.Title
		node.Description = repaired.Body
		node.Subtype = repaired.Subtype
		node.LabelResolution = cloneLabelResolution(repaired.LabelResolution)
	}
}

func finalItemManualProtected(state *liveAnalysisPayload, itemID string) bool {
	node := liveTreeNodeByID(state.Tree, itemID)
	return node != nil && treeAuditIsManualChangeSource(node.LastParentChangeSource)
}

func mergeFinalItemInto(state *liveAnalysisPayload, duplicateID, targetID, source string, version int64) bool {
	if state == nil || duplicateID == "" || targetID == "" || duplicateID == targetID ||
		finalItemManualProtected(state, duplicateID) {
		return false
	}
	duplicateAt, targetAt := -1, -1
	for index := range state.Items {
		switch state.Items[index].ID {
		case duplicateID:
			duplicateAt = index
		case targetID:
			targetAt = index
		}
	}
	if duplicateAt < 0 || targetAt < 0 {
		return false
	}
	duplicate := state.Items[duplicateAt]
	state.Items[targetAt] = mergeTreeAuditItemAttributes(state.Items[targetAt], duplicate)
	addItemTombstone(state, duplicate, "merged", targetID, source, "", version, version)
	kept := make([]liveAnalysisItem, 0, len(state.Items)-1)
	for _, item := range state.Items {
		if item.ID != duplicateID {
			kept = append(kept, item)
		}
	}
	state.Items = kept
	// The canonical target is another detail item and therefore cannot become
	// a tree parent. Removing the duplicate edge/node is safe; the generic
	// remapper would re-parent any malformed children below the target item and
	// fail the post-repair integrity check.
	remap := map[string]string{duplicateID: targetID}
	removeItemNodesFromTree(state.Tree, map[string]struct{}{duplicateID: {}})
	if state.Tree != nil {
		for index := range state.Tree.Nodes {
			state.Tree.Nodes[index].RelatedItemIDs = remapIDList(state.Tree.Nodes[index].RelatedItemIDs, remap)
		}
		for index := range state.Tree.Relations {
			if state.Tree.Relations[index].Source == duplicateID {
				state.Tree.Relations[index].Source = targetID
			}
			if state.Tree.Relations[index].Target == duplicateID {
				state.Tree.Relations[index].Target = targetID
			}
		}
	}
	for index := range state.EmergingTopics {
		for at, evidenceID := range state.EmergingTopics[index].EvidenceItemIDs {
			if evidenceID == duplicateID {
				state.EmergingTopics[index].EvidenceItemIDs[at] = targetID
			}
		}
		state.EmergingTopics[index].EvidenceItemIDs = uniqueNonEmptyIDs(state.EmergingTopics[index].EvidenceItemIDs)
	}
	return true
}

func rejectFinalItem(state *liveAnalysisPayload, itemID, reason string, version int64) bool {
	if state == nil || finalItemManualProtected(state, itemID) {
		return false
	}
	item, ok := finalItemByID(state.Items, itemID)
	if !ok {
		return false
	}
	addItemTombstone(state, item, "low_information", "", reason, "", version, version)
	kept := make([]liveAnalysisItem, 0, len(state.Items)-1)
	for _, candidate := range state.Items {
		if candidate.ID != itemID {
			kept = append(kept, candidate)
		}
	}
	state.Items = kept
	removeItemNodesFromTree(state.Tree, map[string]struct{}{itemID: {}})
	for index := range state.EmergingTopics {
		keptEvidence := state.EmergingTopics[index].EvidenceItemIDs[:0]
		for _, evidenceID := range state.EmergingTopics[index].EvidenceItemIDs {
			if evidenceID != itemID {
				keptEvidence = append(keptEvidence, evidenceID)
			}
		}
		state.EmergingTopics[index].EvidenceItemIDs = keptEvidence
	}
	return true
}

func itemEvidenceOverlaps(left, right liveAnalysisItem) bool {
	for _, sequenceNo := range left.EvidenceSequenceNos {
		if containsInt64(right.EvidenceSequenceNos, sequenceNo) {
			return true
		}
	}
	return false
}

func mergeSameEvidenceSynthesisDuplicates(state *liveAnalysisPayload, version int64, stats *finalRepairStats) {
	if state == nil {
		return
	}
	itemIDs := activeFinalItemIDs(state.Items)
	for _, duplicateID := range itemIDs {
		item, ok := finalItemByID(state.Items, duplicateID)
		if !ok || item.Kind != "issue" ||
			(item.AssignmentReason != issueSynthesisAssignmentReason && !liveItemTextNeedsReferent(item)) {
			continue
		}
		targets := make([]string, 0, 2)
		itemText := item.Title + " " + item.Body
		for _, candidate := range state.Items {
			if candidate.ID == item.ID || candidate.Inactive || candidate.MergedIntoID != "" ||
				!itemEvidenceOverlaps(item, candidate) {
				continue
			}
			candidateText := candidate.Title + " " + candidate.Body
			if !openIssueMarkerPattern.MatchString(candidateText) &&
				!(candidate.Kind == "issue" && candidate.Status != "resolved" && candidate.Status != "dismissed") {
				continue
			}
			if !liveItemTextNeedsReferent(item) &&
				!sharedTreeAuditSubjectTerm(itemText, candidateText) &&
				semanticItemSimilarity(itemText, candidateText) < 0.18 {
				continue
			}
			targets = append(targets, candidate.ID)
		}
		targets = uniqueNonEmptyIDs(targets)
		if len(targets) != 1 {
			continue
		}
		if mergeFinalItemInto(state, item.ID, targets[0], "final_same_evidence_synthesis_dedup", version) {
			stats.SameEvidenceSynthesisMerged++
		}
	}
}

func pruneDanglingFinalCandidates(state *liveAnalysisPayload) int {
	if state == nil {
		return 0
	}
	active := make(map[string]struct{}, len(state.Items))
	for _, item := range state.Items {
		if !item.Inactive && item.MergedIntoID == "" {
			active[item.ID] = struct{}{}
		}
	}
	kept := make([]emergingTopicCandidate, 0, len(state.EmergingTopics))
	pruned := 0
	for _, candidate := range state.EmergingTopics {
		evidence := candidate.EvidenceItemIDs[:0]
		for _, itemID := range candidate.EvidenceItemIDs {
			if _, ok := active[itemID]; ok {
				evidence = append(evidence, itemID)
			}
		}
		candidate.EvidenceItemIDs = uniqueNonEmptyIDs(evidence)
		if len(candidate.EvidenceItemIDs) == 0 {
			if node := liveTreeNodeByID(state.Tree, candidate.ID); node != nil {
				treeAuditCascadeRemoveEmptyAncestors(state.Tree, candidate.ID)
			}
			pruned++
			continue
		}
		kept = append(kept, candidate)
	}
	state.EmergingTopics = kept
	pruneEmptyDynamicTopics(state.Tree)
	return pruned
}
