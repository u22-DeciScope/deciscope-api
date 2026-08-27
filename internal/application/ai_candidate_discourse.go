package application

import "strings"

// discourseTopicCluster is a transient view used before the canonical tree
// rebuild. It joins model topic proposals to an already materialized dynamic
// topic only when their evidence belongs to one short, coherent discourse
// span. The canonical item IDs and transcript remain unchanged.
type discourseTopicCluster struct {
	id          string
	label       string
	description string
	itemIDs     []string
	existing    bool
	order       int
}

// reconcileDiscourseTopicProposals prevents one agenda-external subject from
// fragmenting merely because each semantic kind received a different model
// topic label. Surface words alone are insufficient: evidence must also be
// nearby, from the same speaker, and within the same no-agenda discourse span.
func reconcileDiscourseTopicProposals(
	assignments []treeAssignment,
	newTopics []liveAnalysisTreeNode,
	previousTree *liveAnalysisTree,
	items []liveAnalysisItem,
	scope liveEvidenceScope,
	spans []agendaContextSpan,
	mc *meetingContext,
	stats *liveAnalysisTreeMergeStats,
) ([]treeAssignment, []liveAnalysisTreeNode) {
	for index := range newTopics {
		newTopics[index].Label = completeDynamicTopicLabel(newTopics[index].Label, newTopics[index].Description)
	}
	if len(newTopics) == 0 {
		return assignments, newTopics
	}

	itemByID := make(map[string]liveAnalysisItem, len(items))
	for _, item := range items {
		if item.ID != "" && !item.Inactive && item.MergedIntoID == "" && item.Status != "dismissed" {
			itemByID[item.ID] = item
		}
	}
	clusters := make([]discourseTopicCluster, 0, len(newTopics)+4)
	if previousTree != nil {
		for _, node := range previousTree.Nodes {
			// Agenda-linked mixed topics are not no-agenda discourse anchors.
			// Absorbing a nearby side topic into one would override the agenda
			// reconciliation that already ran before this repair.
			if node.Kind != "topic" || node.Origin != topicOriginDynamic || len(node.AgendaRefs) > 0 {
				continue
			}
			cluster := discourseTopicCluster{
				id: node.ID, label: node.Label, description: node.Description,
				existing: true, order: len(clusters),
			}
			for _, item := range itemByID {
				if treeItemTopic(previousTree, item.ID) == node.ID {
					cluster.itemIDs = appendUniqueStrings(cluster.itemIDs, item.ID)
				}
			}
			if len(cluster.itemIDs) > 0 {
				clusters = append(clusters, cluster)
			}
		}
	}
	proposalStart := len(clusters)
	for index, topic := range newTopics {
		cluster := discourseTopicCluster{
			id: topic.ID, label: topic.Label, description: topic.Description,
			order: proposalStart + index,
		}
		for _, assignment := range assignments {
			parentID := strings.TrimSpace(assignment.ParentTopicID)
			if parentID != topic.ID && parentID != normalizeProposedTopicID(topic.ID, topic.Label) {
				continue
			}
			if _, exists := itemByID[assignment.nodeID()]; exists {
				cluster.itemIDs = appendUniqueStrings(cluster.itemIDs, assignment.nodeID())
			}
		}
		clusters = append(clusters, cluster)
	}
	if len(clusters) < 2 {
		return assignments, newTopics
	}

	parent := make([]int, len(clusters))
	anchor := make([]int, len(clusters))
	for index := range parent {
		parent[index] = index
		anchor[index] = -1
		if clusters[index].existing {
			anchor[index] = index
		}
	}
	var find func(int) int
	find = func(value int) int {
		if parent[value] != value {
			parent[value] = find(parent[value])
		}
		return parent[value]
	}
	join := func(left, right int) {
		left, right = find(left), find(right)
		if left == right {
			return
		}
		// A proposal may resemble two durable topics. Do not let it bridge
		// those anchors through union-find transitivity; ambiguity is safer
		// than arbitrarily choosing the earlier materialized topic.
		if anchor[left] >= 0 && anchor[right] >= 0 && anchor[left] != anchor[right] {
			return
		}
		// Prefer a materialized topic, then the earliest proposal. This keeps the
		// durable topic ID stable across later semantic-kind rounds.
		if clusters[right].existing && !clusters[left].existing ||
			(clusters[right].existing == clusters[left].existing && clusters[right].order < clusters[left].order) {
			left, right = right, left
		}
		parent[right] = left
		if anchor[left] < 0 {
			anchor[left] = anchor[right]
		}
	}
	for left := 0; left < len(clusters); left++ {
		for right := left + 1; right < len(clusters); right++ {
			if clusters[left].existing && clusters[right].existing {
				continue
			}
			if discourseTopicClustersRelated(clusters[left], clusters[right], itemByID, scope, spans, mc) {
				join(left, right)
			}
		}
	}

	aliases := make(map[string]string)
	for index := range clusters {
		root := find(index)
		if root == index || clusters[index].existing {
			continue
		}
		targetID := clusters[root].id
		if targetID == "" || targetID == clusters[index].id {
			continue
		}
		aliases[clusters[index].id] = targetID
		aliases[normalizeProposedTopicID(clusters[index].id, clusters[index].label)] = targetID
		if stats != nil {
			stats.CandidateIDsMerged++
			stats.CompanionCandidateInherited++
		}
	}
	if len(aliases) == 0 {
		return assignments, newTopics
	}
	existingTopicIDs := make(map[string]struct{})
	for _, cluster := range clusters {
		if cluster.existing {
			existingTopicIDs[cluster.id] = struct{}{}
		}
	}
	for index := range assignments {
		if targetID := aliases[strings.TrimSpace(assignments[index].ParentTopicID)]; targetID != "" {
			// Preserve model-parent and span provenance. A materialized alias is a
			// deterministic repair, though: leaving its source as no_agenda_span
			// makes applyAssignments look for a non-existent candidate and demote
			// the item to topic-unclassified.
			assignments[index].ParentTopicID = targetID
			if _, materialized := existingTopicIDs[targetID]; materialized {
				assignments[index].ServerSource = assignmentSourceRule
			}
		}
	}
	kept := newTopics[:0]
	for _, topic := range newTopics {
		if aliases[topic.ID] == "" && aliases[normalizeProposedTopicID(topic.ID, topic.Label)] == "" {
			kept = append(kept, topic)
		}
	}
	return assignments, kept
}

func discourseTopicClustersRelated(
	left, right discourseTopicCluster,
	items map[string]liveAnalysisItem,
	scope liveEvidenceScope,
	spans []agendaContextSpan,
	mc *meetingContext,
) bool {
	for _, leftID := range left.itemIDs {
		leftItem, leftOK := items[leftID]
		if !leftOK {
			continue
		}
		for _, rightID := range right.itemIDs {
			rightItem, rightOK := items[rightID]
			if !rightOK || !sameNoAgendaDiscourseContext(leftItem, rightItem, scope, spans, mc) {
				continue
			}
			leftText := strings.TrimSpace(left.label + " " + left.description + " " + leftItem.Title + " " + leftItem.Body)
			rightText := strings.TrimSpace(right.label + " " + right.description + " " + rightItem.Title + " " + rightItem.Body)
			leftSubject := concreteBusinessSubject(leftText)
			rightSubject := concreteBusinessSubject(rightText)
			if leftSubject != "" && rightSubject != "" {
				// Two recognized business objects are authoritative. Shared time
				// or predicate wording (for example two different things expiring
				// next month) must not merge them.
				return normalizeForMatch(leftSubject) == normalizeForMatch(rightSubject) ||
					specificSubjectOverlapLength(leftSubject, rightSubject) >= 2
			}
			if specificSubjectOverlapLength(leftText, rightText) >= 4 {
				return true
			}
			if boundedConditionalAntecedentLink(leftItem, rightItem, scope) || boundedConditionalAntecedentLink(rightItem, leftItem, scope) {
				return true
			}
		}
	}
	return false
}

func primaryEvidenceSequence(item liveAnalysisItem, scope liveEvidenceScope) int64 {
	var result int64
	for _, sequenceNo := range item.EvidenceSequenceNos {
		if role, exists := scope.EvidenceRoles[sequenceNo]; exists &&
			role != liveEvidencePrimary && role != liveEvidenceSupporting && role != liveEvidenceCorrection {
			continue
		}
		if sequenceNo > result {
			result = sequenceNo
		}
	}
	return result
}

func sameNoAgendaDiscourseContext(left, right liveAnalysisItem, scope liveEvidenceScope, spans []agendaContextSpan, mc *meetingContext) bool {
	leftSequence, rightSequence := primaryEvidenceSequence(left, scope), primaryEvidenceSequence(right, scope)
	if leftSequence <= 0 || rightSequence <= 0 {
		return false
	}
	delta := leftSequence - rightSequence
	if delta < 0 {
		delta = -delta
	}
	if delta > 3 {
		return false
	}
	leftSegment, leftFound := scope.Segments[leftSequence]
	rightSegment, rightFound := scope.Segments[rightSequence]
	if !leftFound || !rightFound {
		return false
	}
	leftSpeaker := strings.TrimSpace(firstNonEmptyTrimmed(leftSegment.SpeakerID, leftSegment.SpeakerName))
	rightSpeaker := strings.TrimSpace(firstNonEmptyTrimmed(rightSegment.SpeakerID, rightSegment.SpeakerName))
	if leftSpeaker == "" || rightSpeaker == "" || leftSpeaker != rightSpeaker {
		return false
	}
	leftSpan, leftHasSpan := agendaContextSpanForEvidence([]int64{leftSequence}, spans)
	rightSpan, rightHasSpan := agendaContextSpanForEvidence([]int64{rightSequence}, spans)
	if leftHasSpan || rightHasSpan {
		return leftHasSpan && rightHasSpan && leftSpan.Mode == agendaContextModeNoAgenda &&
			rightSpan.Mode == agendaContextModeNoAgenda && leftSpan.StartSequenceNo == rightSpan.StartSequenceNo
	}
	return mc == nil || len(mc.Agenda) == 0
}

func boundedConditionalAntecedentLink(antecedent, conditional liveAnalysisItem, scope liveEvidenceScope) bool {
	antecedentSequence := primaryEvidenceSequence(antecedent, scope)
	conditionalSequence := primaryEvidenceSequence(conditional, scope)
	if conditionalSequence <= antecedentSequence || conditionalSequence-antecedentSequence > 1 {
		return false
	}
	conditionalText := strings.TrimSpace(scope.TranscriptText[conditionalSequence])
	if !itemLabelConditionalWithoutSubjectPattern.MatchString(conditionalText) {
		return false
	}
	antecedentText := strings.TrimSpace(antecedent.Title + " " + antecedent.Body)
	return concreteBusinessSubject(antecedentText) != ""
}
