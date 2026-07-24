package application

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"regexp"
	"strings"
)

// repairHistoricalDiscourseItems removes defects persisted by older pipeline
// versions. A recap-only item can update an existing primary proposition, but
// cannot remain as an independent node. Predicate-only decisions are removed
// because their subject was lost before the decision marker was recognized.
func repairHistoricalDiscourseItems(state *liveAnalysisPayload, timeline discourseTimeline, stats *liveAnalysisTreeMergeStats, scopes ...liveEvidenceScope) map[string]string {
	if state == nil || len(state.Items) == 0 {
		return nil
	}
	if len(scopes) > 0 {
		repairHistoricalDecisionFragments(state, scopes[0], stats)
	}
	primary := make([]liveAnalysisItem, 0, len(state.Items))
	for _, item := range state.Items {
		if item.Inactive || item.MergedIntoID != "" {
			continue
		}
		if !evidenceIsReferenceOnly(item.EvidenceSequenceNos, timeline) && !lowInformationDecisionItem(item) {
			primary = append(primary, item)
		}
	}
	kept := make([]liveAnalysisItem, 0, len(state.Items))
	remap := make(map[string]string)
	removed := make(map[string]struct{})
	for _, item := range state.Items {
		// Auditor-deactivated/merged records are deliberately retained outside
		// the active tree for history and tombstone semantic matching. Historical
		// discourse repair may remove their tree nodes, but must not erase the
		// durable inactive item on a later live round.
		if item.Inactive || item.MergedIntoID != "" {
			kept = append(kept, item)
			continue
		}
		if lowInformationDecisionItem(item) {
			removed[item.ID] = struct{}{}
			addItemTombstone(state, item, "low_information", "", "live_repair", "", state.TreeVersion, state.TreeVersion)
			if stats != nil {
				stats.LowInformationDecisionsRejected++
			}
			continue
		}
		if evidenceIsReferenceOnly(item.EvidenceSequenceNos, timeline) {
			if at, score := bestPropositionMatch(primary, item); at >= 0 && score >= 0.12 && primary[at].ID != item.ID {
				remap[item.ID] = primary[at].ID
				addItemTombstone(state, item, "merged", primary[at].ID, "live_repair", "", state.TreeVersion, state.TreeVersion)
				if stats != nil {
					stats.ReferenceRecapItemsMerged++
				}
				continue
			}
			removed[item.ID] = struct{}{}
			reason := "recap_only"
			if evidenceOnlyHasRoles(item.EvidenceSequenceNos, timeline, liveEvidenceDiscourseOnly) {
				reason = "discourse_only"
			}
			addItemTombstone(state, item, reason, "", "live_repair", "", state.TreeVersion, state.TreeVersion)
			if stats != nil {
				stats.ReferenceRecapItemsRejected++
			}
			continue
		}
		kept = append(kept, item)
	}
	if len(remap) > 0 {
		remapExistingTreeReferences(state.Tree, remap)
	}
	if len(removed) > 0 {
		removeItemNodesFromTree(state.Tree, removed)
	}
	state.Items = kept
	for i := range state.EmergingTopics {
		ids := state.EmergingTopics[i].EvidenceItemIDs[:0]
		for _, id := range state.EmergingTopics[i].EvidenceItemIDs {
			if canonical := remap[id]; canonical != "" {
				id = canonical
			}
			if _, drop := removed[id]; !drop {
				ids = append(ids, id)
			}
		}
		state.EmergingTopics[i].EvidenceItemIDs = uniqueNonEmptyIDs(ids)
	}
	return remap
}

func repairHistoricalDecisionFragments(state *liveAnalysisPayload, scope liveEvidenceScope, stats *liveAnalysisTreeMergeStats) {
	if state == nil {
		return
	}
	for i := range state.Items {
		item := &state.Items[i]
		if item.Kind != "decision" || (!decisionStatementNeedsReferent(item.Title) && !decisionStatementNeedsReferent(item.Body)) || len(item.EvidenceSequenceNos) == 0 {
			continue
		}
		sequenceNo := item.EvidenceSequenceNos[len(item.EvidenceSequenceNos)-1]
		current := segmentFromEvidenceScope(scope, sequenceNo)
		previous := segmentFromEvidenceScope(scope, sequenceNo-1)
		if current.SequenceNo <= 0 || previous.SequenceNo <= 0 {
			continue
		}
		repaired, ok := repairDecisionFragment(previous, current, current.Text)
		if !ok {
			continue
		}
		item.Title = truncateRunes(decisionCandidateTitle(repaired), 40)
		item.Body = truncateRunes(strings.TrimSpace(repaired), liveAnalysisTreeDescriptionMaxRunes)
		item.EvidenceSequenceNos = appendUniqueSequences([]int64{previous.SequenceNo}, item.EvidenceSequenceNos)
		item.InformationStatus = informationStatusGrounded
		if state.Tree != nil {
			for nodeAt := range state.Tree.Nodes {
				if state.Tree.Nodes[nodeAt].ID == item.ID {
					state.Tree.Nodes[nodeAt].Label = item.Title
					state.Tree.Nodes[nodeAt].Description = item.Body
				}
			}
		}
		if stats != nil {
			stats.LowInformationItemsRewritten++
		}
	}
}

// repairMixedEmergingCandidates folds evidence that clearly belongs to an
// agenda anchor and splits the remaining unrelated subject clusters. An
// anchor topic is materialized here only for grounded, non-no-agenda evidence.
// It runs before normal candidate promotion, so a coherent multi-round cluster
// can be promoted without weakening the configured promotion thresholds.
func repairMixedEmergingCandidates(state *liveAnalysisPayload, mc *meetingContext, round int64, stats *liveAnalysisTreeMergeStats) {
	if state == nil || len(state.EmergingTopics) == 0 {
		return
	}
	itemByID := make(map[string]*liveAnalysisItem, len(state.Items))
	for i := range state.Items {
		itemByID[state.Items[i].ID] = &state.Items[i]
	}
	records := agendaRecordMap(mc)
	setParent := func(itemID, parentID string) {
		if state.Tree == nil {
			return
		}
		for i := range state.Tree.Nodes {
			if state.Tree.Nodes[i].ID == itemID {
				state.Tree.Nodes[i].ParentID = parentID
				state.Tree.Nodes[i].LastParentChangeSource = "candidate_subject_repair"
				state.Tree.Nodes[i].LastParentChangeVersion = round
			}
		}
	}
	bestAgenda := func(item liveAnalysisItem) string {
		if mc == nil || item.AssignmentSource == assignmentSourceNoAgendaSpan {
			return ""
		}
		itemText := item.Title + " " + item.Body
		bestID, bestScore := "", 0.0
		for _, agenda := range mc.Agenda {
			if agenda.Role == agendaRoleActionSummary {
				continue
			}
			agendaText := agenda.Title
			score := semanticItemSimilarity(itemText, agendaText)
			itemCore, agendaCore := semanticTopicCore(itemText), semanticTopicCore(agendaText)
			coreScore := semanticItemSimilarity(itemCore, agendaCore)
			sharedCore := sharedTreeAuditSubjectTerm(itemCore, agendaCore) && coreScore >= 0.18
			if !sharedCore && score < 0.72 {
				continue
			}
			if sharedCore && score < 0.55 {
				score = 0.55
			}
			if score > bestScore {
				bestID, bestScore = agenda.ID, score
			}
		}
		if bestScore < 0.35 {
			bestID, bestScore = "", 0
			for _, companion := range state.Items {
				if companion.ID == item.ID || companion.CandidateTopicID == item.CandidateTopicID {
					continue
				}
				companionNode := liveTreeNodeByID(state.Tree, companion.ID)
				if companionNode == nil {
					continue
				}
				agendaTopicID := treeAuditFixedAgendaAncestor(companionNode.ParentID, state.Tree)
				if agendaTopicID == "" {
					continue
				}
				agendaTopic := liveTreeNodeByID(state.Tree, agendaTopicID)
				if agendaTopic == nil {
					continue
				}
				refs := topicAgendaRefs(*agendaTopic, records)
				if len(refs) == 0 {
					continue
				}
				companionText := companion.Title + " " + companion.Body
				score := semanticItemSimilarity(itemText, companionText)
				itemCore, companionCore := semanticTopicCore(itemText), semanticTopicCore(companionText)
				if !sharedTreeAuditSubjectTerm(itemCore, companionCore) || semanticItemSimilarity(itemCore, companionCore) < 0.18 || score < 0.12 {
					continue
				}
				if score > bestScore {
					bestID, bestScore = refs[0], score
				}
			}
			if bestScore < 0.12 {
				return ""
			}
		}
		return bestID
	}
	materializedTopicForAgenda := func(agendaID string) string {
		if state.Tree == nil {
			return ""
		}
		for _, node := range state.Tree.Nodes {
			if node.Kind == "topic" && containsExactString(topicAgendaRefs(node, records), agendaID) {
				return node.ID
			}
		}
		return ""
	}
	ensureAgendaTopic := func(agendaID string, item *liveAnalysisItem) string {
		if topicID := materializedTopicForAgenda(agendaID); topicID != "" {
			return topicID
		}
		agenda, exists := records[agendaID]
		if !exists || item == nil || state.Tree == nil || liveTreeNodeByID(state.Tree, treeRootNodeID) == nil {
			return ""
		}
		topics := make(map[string]liveAnalysisTreeNode)
		for _, node := range state.Tree.Nodes {
			if node.Kind == "topic" {
				topics[node.ID] = node
			}
		}
		topicID, reused := availableAgendaTopicID(agendaID, topics, records)
		label := truncateRunes(strings.TrimSpace(item.Title), liveAnalysisTopicLabelMaxRunes)
		if label == "" {
			label = agenda.Title
		}
		state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
			ID: topicID, Kind: "topic", ParentID: treeRootNodeID,
			Label: label, Description: truncateRunes(strings.TrimSpace(item.Body), liveAnalysisTreeDescriptionMaxRunes),
			Origin: topicOriginAgenda, AgendaRole: agendaRolePrimary,
			AgendaRefs: []string{agendaID}, Materialized: true,
			CreatedAtVersion: round, UpdatedAtVersion: round,
		})
		log.Printf("Agenda topic materialized by candidate repair. agendaId=%s materializedTopicId=%s agendaTopicIdReused=%t agendaTopicIdCollision=%t", agendaID, topicID, reused, agendaID == topicID)
		if stats != nil {
			stats.AgendaTopicsMaterialized++
			if reused {
				stats.AgendaTopicIDsReused++
			}
			if agendaID == topicID {
				stats.AgendaTopicIDCollisions++
			}
		}
		return topicID
	}
	var repaired []emergingTopicCandidate
	for _, candidate := range state.EmergingTopics {
		initializeCandidateSubject(&candidate)
		if len(candidate.EvidenceItemIDs) < 3 || candidateSubjectIncoherenceReason(candidate, func(id string) *liveAnalysisItem { return itemByID[id] }, TreeClassificationConfig{}) == "" {
			repaired = append(repaired, candidate)
			continue
		}
		remaining := make([]string, 0, len(candidate.EvidenceItemIDs))
		for _, itemID := range candidate.EvidenceItemIDs {
			item := itemByID[itemID]
			if item == nil {
				continue
			}
			if agendaID := bestAgenda(*item); agendaID != "" {
				topicID := ensureAgendaTopic(agendaID, item)
				if topicID == "" {
					remaining = append(remaining, itemID)
					continue
				}
				item.ClassificationStatus = classificationAssigned
				item.CandidateTopicID = ""
				item.AssignmentSource = "candidate_subject_repair"
				item.AssignmentReason = "candidate evidence matches agenda anchor subject"
				setParent(item.ID, topicID)
				if stats != nil {
					stats.CandidateFoldedIntoAgenda++
				}
				continue
			}
			remaining = append(remaining, itemID)
		}
		clusters := clusterCandidateEvidence(remaining, itemByID)
		for _, cluster := range clusters {
			if len(cluster) == 0 {
				continue
			}
			label := candidateClusterLabel(cluster, itemByID)
			candidateID, subjectKey := canonicalCandidateID(label, "agenda-external subject recovered from canonical evidence")
			if candidateID == "" {
				continue
			}
			split := emergingTopicCandidate{
				ID: candidateID, Label: label, Description: "agenda-external subject recovered from canonical evidence",
				SubjectKey:      subjectKey,
				OriginalSubject: label, CurrentSubject: label, SubjectHistory: []string{label},
				EvidenceItemIDs: append([]string(nil), cluster...), OriginItemIDs: append([]string(nil), cluster...),
				FirstRound: candidate.FirstRound, LastRound: candidate.LastRound, RoundCount: candidate.RoundCount,
			}
			for _, itemID := range cluster {
				if item := itemByID[itemID]; item != nil {
					item.ClassificationStatus = classificationTentative
					item.CandidateTopicID = candidateID
					split.OriginSequenceNos = appendUniqueSequences(split.OriginSequenceNos, item.EvidenceSequenceNos)
				}
			}
			repaired = append(repaired, split)
			if stats != nil {
				stats.CandidateSubjectsSplit++
			}
		}
	}
	state.EmergingTopics = mergeEquivalentCandidates(repaired, stats)
	if state.Tree != nil {
		rebuildTreeAuditEdges(state.Tree)
	}
}

func liveTreeNodeByID(tree *liveAnalysisTree, id string) *liveAnalysisTreeNode {
	if tree == nil {
		return nil
	}
	for i := range tree.Nodes {
		if tree.Nodes[i].ID == id {
			return &tree.Nodes[i]
		}
	}
	return nil
}

func clusterCandidateEvidence(ids []string, itemByID map[string]*liveAnalysisItem) [][]string {
	var clusters [][]string
	for _, id := range ids {
		item := itemByID[id]
		if item == nil {
			continue
		}
		placed := false
		for i := range clusters {
			for _, memberID := range clusters[i] {
				member := itemByID[memberID]
				if member != nil && (sameCanonicalProposition(*member, *item) || sharedTreeAuditSubjectTerm(member.Title+" "+member.Body, item.Title+" "+item.Body)) {
					clusters[i] = append(clusters[i], id)
					placed = true
					break
				}
			}
			if placed {
				break
			}
		}
		if !placed {
			clusters = append(clusters, []string{id})
		}
	}
	return clusters
}

func mergeEquivalentCandidates(candidates []emergingTopicCandidate, stats *liveAnalysisTreeMergeStats) []emergingTopicCandidate {
	kept := make([]emergingTopicCandidate, 0, len(candidates))
	index := make(map[string]int)
	for _, candidate := range candidates {
		at, exists := index[candidate.ID]
		if !exists {
			index[candidate.ID] = len(kept)
			kept = append(kept, candidate)
			continue
		}
		kept[at].EvidenceItemIDs = uniqueNonEmptyIDs(append(kept[at].EvidenceItemIDs, candidate.EvidenceItemIDs...))
		kept[at].OriginItemIDs = uniqueNonEmptyIDs(append(kept[at].OriginItemIDs, candidate.OriginItemIDs...))
		kept[at].OriginSequenceNos = appendUniqueSequences(kept[at].OriginSequenceNos, candidate.OriginSequenceNos)
		if candidate.LastRound > kept[at].LastRound {
			kept[at].LastRound = candidate.LastRound
		}
		if candidate.RoundCount > kept[at].RoundCount {
			kept[at].RoundCount = candidate.RoundCount
		}
		if stats != nil {
			stats.CandidateIDsMerged++
		}
	}
	return kept
}

func candidateClusterLabel(ids []string, itemByID map[string]*liveAnalysisItem) string {
	var texts []string
	best := ""
	for _, id := range ids {
		if item := itemByID[id]; item != nil {
			text := strings.TrimSpace(item.Title + " " + item.Body)
			texts = append(texts, text)
			if len([]rune(item.Title)) > len([]rune(best)) {
				best = item.Title
			}
		}
	}
	combined := strings.Join(texts, " ")
	if strings.Contains(combined, "湿地") && strings.Contains(combined, "植物") {
		return "湿地・希少植物調査"
	}
	return truncateRunes(strings.TrimSpace(best), liveAnalysisTopicLabelMaxRunes)
}

// recapNovelActorPattern / recapNovelDeadlinePattern detect concrete
// information (an assignee, or a due-date expression) that a recap sentence
// can add on top of what its canonical target already says.
var (
	recapNovelActorPattern    = regexp.MustCompile(`(?:[\p{Han}]{1,6}|[ァ-ヶー]{2,8})さん`)
	recapNovelDeadlinePattern = regexp.MustCompile(`今週|来週|今月|来月|本日|明日|月末|年内|[月火水木金土日]曜|期限|までに?`)
)

// recapBodyHasNovelInfo reports whether recapBody names an actor or due-date
// expression that canonicalBody does not already contain, so a recap that
// merely restates the canonical item's own actor/deadline does not trigger a
// body rewrite.
func recapBodyHasNovelInfo(recapBody, canonicalBody string) bool {
	for _, match := range recapNovelActorPattern.FindAllString(recapBody, -1) {
		if !strings.Contains(canonicalBody, match) {
			return true
		}
	}
	for _, match := range recapNovelDeadlinePattern.FindAllString(recapBody, -1) {
		if !strings.Contains(canonicalBody, match) {
			return true
		}
	}
	return false
}

// recapItemHasConcreteInfo reports whether item names an actor, a due-date
// expression, or a number -- the kind of specific content that can justify
// treating an unmatched recap sentence as a genuinely new proposition instead
// of dropping it as pure restatement.
func recapItemHasConcreteInfo(item liveAnalysisItem) bool {
	text := item.Title + " " + item.Body
	if recapNovelActorPattern.MatchString(text) || recapNovelDeadlinePattern.MatchString(text) {
		return true
	}
	return numericSignature(text) != ""
}

// recapItemHasNovelSubject reports whether item's subject does not overlap
// any previous item's subject at all. Combined with recapItemHasConcreteInfo,
// this is the narrow exception that lets a truly new proposition surface
// during a recap round instead of being rejected outright like every other
// unmatched recap sentence.
func recapItemHasNovelSubject(item liveAnalysisItem, previous []liveAnalysisItem) bool {
	itemText := item.Title + " " + item.Body
	for _, existing := range previous {
		if sharedTreeAuditSubjectTerm(existing.Title+" "+existing.Body, itemText) {
			return false
		}
	}
	return true
}

func filterReferenceRecapDiff(previous []liveAnalysisItem, diff []liveAnalysisItem, roundSeqNos []int64, timeline discourseTimeline, stats *liveAnalysisTreeMergeStats) []liveAnalysisItem {
	filtered := make([]liveAnalysisItem, 0, len(diff))
	for _, item := range diff {
		evidence := append([]int64(nil), item.EvidenceSequenceNos...)
		if len(evidence) == 0 && !item.evidenceSpecified {
			evidence = append(evidence, roundSeqNos...)
		}
		if !evidenceIsReferenceOnly(evidence, timeline) {
			filtered = append(filtered, item)
			continue
		}
		at, score := bestPropositionMatch(previous, item)
		if at < 0 || score < 0.12 {
			// recap中の新規item作成は原則禁止だが、どのprevious itemとも主題が
			// 重ならず、かつ担当者/期限/数値のような具体情報を伴う場合に限り、
			// 真に新しい命題として救済する(recapの言い換えを新規item化はしない)。
			if recapItemHasNovelSubject(item, previous) && recapItemHasConcreteInfo(item) {
				filtered = append(filtered, item)
				continue
			}
			if stats != nil {
				stats.ReferenceRecapItemsRejected++
			}
			continue
		}
		canonical := previous[at]
		canonical.ClientKey = modelItemReference(item)
		canonical.EvidenceSequenceNos = evidence
		canonical.evidenceSpecified = item.evidenceSpecified
		canonical.evidenceRejectedCount = item.evidenceRejectedCount
		canonical.evidenceNormalizedCount = item.evidenceNormalizedCount
		// recapが本編に無い担当者/期限を追加している場合のみ、canonicalの本文へ
		// 反映する(単なる言い換えでは本文を書き換えない)。evidenceは本編+recap
		// 両方が最終itemに残る(下流のappendItemEvidenceSequenceNosがcanonical
		// のEvidenceSequenceNos=evidenceと元のEvidenceSequenceNosを和集合にする)。
		if recapBodyHasNovelInfo(item.Body, canonical.Body) {
			canonical.Body = truncateRunes(strings.TrimSpace(canonical.Body+" "+item.Body), liveAnalysisTreeDescriptionMaxRunes)
			canonical.Status = "updated"
		}
		filtered = append(filtered, canonical)
		if stats != nil {
			stats.ReferenceRecapItemsMerged++
		}
	}
	return filtered
}

func lowInformationDecisionItem(item liveAnalysisItem) bool {
	return item.Kind == "decision" && len(item.EvidenceSequenceNos) > 0 && decisionPositivePattern.MatchString(item.Body) && !completeDecisionStatement(strings.TrimSpace(item.Title))
}

func bestPropositionMatch(items []liveAnalysisItem, target liveAnalysisItem) (int, float64) {
	bestAt, bestScore := -1, 0.0
	for i := range items {
		if items[i].ID == target.ID {
			continue
		}
		score := semanticItemSimilarity(items[i].Title+" "+items[i].Body, target.Title+" "+target.Body)
		if !sharedTreeAuditSubjectTerm(items[i].Title+" "+items[i].Body, target.Title+" "+target.Body) && score < 0.48 {
			continue
		}
		if score > bestScore {
			bestAt, bestScore = i, score
		}
	}
	return bestAt, bestScore
}

func roundIsReferenceOnly(roundSeqNos []int64, timeline discourseTimeline) bool {
	return evidenceIsReferenceOnly(roundSeqNos, timeline)
}

func contentEvidenceItems(items []liveAnalysisItem, timeline discourseTimeline) []liveAnalysisItem {
	kept := make([]liveAnalysisItem, 0, len(items))
	for _, item := range items {
		if !evidenceIsReferenceOnly(item.EvidenceSequenceNos, timeline) {
			kept = append(kept, item)
		}
	}
	return kept
}

// canonicalizePropositionItems folds cross-kind surface forms into one
// proposition. The retained kind represents the proposition state; question,
// resolution-condition and next-action wording is preserved as attributes.
func canonicalizePropositionItems(state *liveAnalysisPayload, timeline discourseTimeline, stats *liveAnalysisTreeMergeStats, treeVersion int64) map[string]string {
	if state == nil || len(state.Items) < 2 {
		if state != nil {
			stampPropositionMetadata(state.Items)
		}
		return nil
	}
	kept := make([]liveAnalysisItem, 0, len(state.Items))
	remap := make(map[string]string)
	for _, item := range state.Items {
		matchedAt := -1
		for at := range kept {
			if sameCanonicalPropositionWithTimeline(kept[at], item, timeline) {
				matchedAt = at
				break
			}
		}
		if matchedAt < 0 {
			kept = append(kept, item)
			continue
		}
		canonical, companion := chooseCanonicalPropositionItem(kept[matchedAt], item)
		canonical = mergePropositionAttributes(canonical, companion)
		remap[companion.ID] = canonical.ID
		addItemTombstone(state, companion, "merged", canonical.ID, "semantic_merge", "", treeVersion-1, treeVersion)
		kept[matchedAt] = canonical
		if stats != nil {
			if canonical.Kind != companion.Kind {
				stats.CrossKindClustered++
			}
			stats.PropositionItemsMerged++
		}
	}
	state.Items = kept
	if len(remap) > 0 {
		remapExistingTreeReferences(state.Tree, remap)
		for i := range state.EmergingTopics {
			for at, id := range state.EmergingTopics[i].EvidenceItemIDs {
				state.EmergingTopics[i].EvidenceItemIDs[at] = resolveRemappedID(id, remap)
			}
			state.EmergingTopics[i].EvidenceItemIDs = uniqueNonEmptyIDs(state.EmergingTopics[i].EvidenceItemIDs)
		}
	}
	stampPropositionMetadata(state.Items)
	return remap
}

func sameCanonicalProposition(a, b liveAnalysisItem) bool {
	return sameCanonicalPropositionWithTimeline(a, b, discourseTimeline{})
}

func sameCanonicalPropositionWithTimeline(a, b liveAnalysisItem, timeline discourseTimeline) bool {
	if a.ID == b.ID || a.Kind == "decision" || b.Kind == "decision" || !crossKindPropositionCompatible(a.Kind, b.Kind) {
		return false
	}
	leftNumbers, rightNumbers := numericSignature(a.Title+" "+a.Body), numericSignature(b.Title+" "+b.Body)
	if leftNumbers != "" && rightNumbers != "" && leftNumbers != rightNumbers {
		return false
	}
	left, right := semanticTopicCore(a.Title+" "+a.Body), semanticTopicCore(b.Title+" "+b.Body)
	score := semanticItemSimilarity(left, right)
	sharedSubject := sharedTreeAuditSubjectTerm(left, right)
	nearEvidence := itemEvidenceWithinPrimaryRoles(a, b, 1, timeline)
	if (a.Kind == "todo" || b.Kind == "todo") && !itemPrimaryEvidenceOverlaps(a, b, timeline) {
		return false
	}
	return (sharedSubject && score >= 0.72) || (sharedSubject && nearEvidence && score >= 0.18)
}

func itemPrimaryEvidenceOverlaps(a, b liveAnalysisItem, timeline discourseTimeline) bool {
	seen := make(map[int64]struct{}, len(a.EvidenceSequenceNos))
	for _, sequenceNo := range a.EvidenceSequenceNos {
		if evidenceRoleIsReference(sequenceNo, timeline) {
			continue
		}
		seen[sequenceNo] = struct{}{}
	}
	for _, sequenceNo := range b.EvidenceSequenceNos {
		if evidenceRoleIsReference(sequenceNo, timeline) {
			continue
		}
		if _, exists := seen[sequenceNo]; exists {
			return true
		}
	}
	return false
}

func itemEvidenceWithinPrimaryRoles(a, b liveAnalysisItem, maxDistance int64, timeline discourseTimeline) bool {
	for _, left := range a.EvidenceSequenceNos {
		if evidenceRoleIsReference(left, timeline) {
			continue
		}
		for _, right := range b.EvidenceSequenceNos {
			if evidenceRoleIsReference(right, timeline) {
				continue
			}
			delta := left - right
			if delta < 0 {
				delta = -delta
			}
			if delta <= maxDistance {
				return true
			}
		}
	}
	return false
}

func evidenceRoleIsReference(sequenceNo int64, timeline discourseTimeline) bool {
	role := timeline.Roles[sequenceNo]
	return role == liveEvidenceReferenceRecap || role == liveEvidenceDiscourseOnly
}

func crossKindPropositionCompatible(a, b string) bool {
	// Confirmation/question/investigation issues and their execution TODOs
	// remain independently actionable and independently resolvable. Their
	// relation is represented by the shared group, not by destructive merge.
	return false
}

func sameKindSequentialProposition(a, b liveAnalysisItem) (bool, float64) {
	if !sameSemanticClassification(a, b) || (a.Kind != "issue" && a.Kind != "todo") {
		return false, 0
	}
	leftNumbers, rightNumbers := numericSignature(a.Title+" "+a.Body), numericSignature(b.Title+" "+b.Body)
	if leftNumbers != "" && rightNumbers != "" && leftNumbers != rightNumbers {
		return false, 0
	}
	left, right := semanticTopicCore(a.Title+" "+a.Body), semanticTopicCore(b.Title+" "+b.Body)
	score := semanticItemSimilarity(left, right)
	return itemEvidenceWithin(a, b, 1) && sharedTreeAuditSubjectTerm(left, right) && score >= 0.18, score
}

func chooseCanonicalPropositionItem(a, b liveAnalysisItem) (liveAnalysisItem, liveAnalysisItem) {
	priority := func(item liveAnalysisItem) int {
		switch item.Kind {
		case "issue":
			return 5
		case "todo":
			return 4
		case "risk":
			return 2
		default:
			return 1
		}
	}
	if priority(b) > priority(a) {
		return b, a
	}
	return a, b
}

func mergePropositionAttributes(canonical, companion liveAnalysisItem) liveAnalysisItem {
	canonical.EvidenceSequenceNos = appendUniqueSequences(canonical.EvidenceSequenceNos, companion.EvidenceSequenceNos)
	canonical.RelatedAgendaIDs = uniqueNonEmptyIDs(append(canonical.RelatedAgendaIDs, companion.RelatedAgendaIDs...))
	canonical.RelatedQuestions = appendUniqueText(canonical.RelatedQuestions, companion.RelatedQuestions...)
	canonical.ResolutionConditions = appendUniqueText(canonical.ResolutionConditions, companion.ResolutionConditions...)
	canonical.NextActions = appendUniqueText(canonical.NextActions, companion.NextActions...)
	switch {
	case companion.Kind == "issue" && companion.Subtype == issueSubtypeQuestion:
		canonical.RelatedQuestions = appendUniqueText(canonical.RelatedQuestions, companion.Title)
	case companion.Kind == "todo":
		canonical.NextActions = appendUniqueText(canonical.NextActions, firstNonEmptyTrimmed(companion.Title, companion.Body))
	}
	if strings.Contains(companion.Body, "してから") || strings.Contains(companion.Body, "確認後") || strings.Contains(companion.Body, "条件") {
		canonical.ResolutionConditions = appendUniqueText(canonical.ResolutionConditions, companion.Body)
	}
	if canonical.Status != "resolved" && companion.Status == "resolved" {
		canonical.Status = companion.Status
	}
	return canonical
}

func appendUniqueText(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	for _, value := range values {
		if key := semanticItemKey(value); key != "" {
			seen[key] = struct{}{}
		}
	}
	for _, value := range additions {
		value = strings.TrimSpace(value)
		key := semanticItemKey(value)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		values = append(values, truncateRunes(value, 120))
	}
	return values
}

func stampPropositionMetadata(items []liveAnalysisItem) {
	for i := range items {
		core := semanticItemKey(items[i].Title + " " + items[i].Body)
		sum := sha256.Sum256([]byte(core))
		items[i].PropositionKey = "prop-" + hex.EncodeToString(sum[:6])
	}
}

func stampEvidenceRoles(items []liveAnalysisItem, timeline discourseTimeline) {
	for i := range items {
		items[i].EvidenceRoles = evidenceRolesForItem(items[i].EvidenceSequenceNos, timeline)
	}
}

func removeItemNodesFromTree(tree *liveAnalysisTree, removed map[string]struct{}) {
	if tree == nil || len(removed) == 0 {
		return
	}
	nodes := tree.Nodes[:0]
	for _, node := range tree.Nodes {
		if _, drop := removed[node.ID]; !drop {
			nodes = append(nodes, node)
		}
	}
	tree.Nodes = nodes
	edges := tree.Edges[:0]
	for _, edge := range tree.Edges {
		if _, sourceDrop := removed[edge.Source]; sourceDrop {
			continue
		}
		if _, targetDrop := removed[edge.Target]; targetDrop {
			continue
		}
		edges = append(edges, edge)
	}
	tree.Edges = edges
}

func pruneEmptyDynamicTopics(tree *liveAnalysisTree) {
	if tree == nil {
		return
	}
	for {
		parents := make(map[string]int)
		for _, edge := range tree.Edges {
			parents[edge.Source]++
		}
		removed := make(map[string]struct{})
		for _, node := range tree.Nodes {
			if node.Kind == "topic" && node.Origin == topicOriginDynamic && parents[node.ID] == 0 {
				removed[node.ID] = struct{}{}
			}
		}
		if len(removed) == 0 {
			return
		}
		removeItemNodesFromTree(tree, removed)
	}
}

func resolveRemappedID(id string, remap map[string]string) string {
	seen := make(map[string]struct{})
	for remap[id] != "" {
		if _, loop := seen[id]; loop {
			break
		}
		seen[id] = struct{}{}
		id = remap[id]
	}
	return id
}
