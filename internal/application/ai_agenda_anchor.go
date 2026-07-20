package application

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"sort"
	"strconv"
	"strings"
)

const (
	agendaStatusPlanned                  = "planned"
	agendaStatusMaterialized             = "materialized"
	agendaStatusDiscussed                = "discussed"
	agendaStatusMerged                   = "merged"
	agendaStatusNotDiscussed             = "not_discussed"
	topicOriginMixed                     = "mixed"
	agendaDematerializeGraceRounds int64 = 2
)

// agendaAnchor is the canonical agenda lifecycle record. It is deliberately
// separate from liveAnalysisTreeNode so an agenda can remain planned without
// producing an empty branch in the discussion tree.
type agendaAnchor struct {
	AgendaID             string                   `json:"agendaId"`
	OriginalTitle        string                   `json:"originalTitle"`
	NormalizedSubject    string                   `json:"normalizedSubject"`
	Order                int                      `json:"order"`
	Role                 string                   `json:"role,omitempty"`
	Status               string                   `json:"status"`
	MaterializedTopicIDs []string                 `json:"materializedTopicIds,omitempty"`
	StatusHistory        []agendaStatusTransition `json:"statusHistory,omitempty"`
}

type agendaStatusTransition struct {
	From        string `json:"from,omitempty"`
	To          string `json:"to"`
	TreeVersion int64  `json:"treeVersion,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type agendaTreeObservability struct {
	EmptyTopics    int
	DynamicOverlap int
}

func observeAgendaTree(tree *liveAnalysisTree, mc *meetingContext) agendaTreeObservability {
	if tree == nil {
		return agendaTreeObservability{}
	}
	records := agendaRecordMap(mc)
	parents := make(map[string]string, len(tree.Nodes))
	for _, node := range tree.Nodes {
		parents[node.ID] = node.ParentID
	}
	agendaTopics := make([]liveAnalysisTreeNode, 0)
	dynamicTopics := make([]liveAnalysisTreeNode, 0)
	metrics := agendaTreeObservability{}
	for _, node := range tree.Nodes {
		if node.Kind != "topic" || node.ID == treeRootNodeID || node.ID == treeUnclassifiedTopicID {
			continue
		}
		if len(topicAgendaRefs(node, records)) > 0 {
			agendaTopics = append(agendaTopics, node)
			if !topicHasDescendants(node.ID, parents) {
				metrics.EmptyTopics++
			}
			continue
		}
		if node.Origin == topicOriginDynamic {
			dynamicTopics = append(dynamicTopics, node)
		}
	}
	for _, agendaTopic := range agendaTopics {
		for _, dynamicTopic := range dynamicTopics {
			if semanticItemSimilarity(agendaTopic.Label+" "+agendaTopic.Description, dynamicTopic.Label+" "+dynamicTopic.Description) >= 0.72 {
				metrics.DynamicOverlap++
			}
		}
	}
	return metrics
}

func agendaTopicMutationCounts(before, after *liveAnalysisTree, mc *meetingContext) (renamed, reparented int) {
	records := agendaRecordMap(mc)
	previous := make(map[string]liveAnalysisTreeNode)
	if before != nil {
		for _, node := range before.Nodes {
			if node.Kind == "topic" && len(topicAgendaRefs(node, records)) > 0 {
				previous[node.ID] = node
			}
		}
	}
	if after == nil {
		return 0, 0
	}
	for _, node := range after.Nodes {
		old, exists := previous[node.ID]
		if !exists || node.Kind != "topic" || len(topicAgendaRefs(node, records)) == 0 {
			continue
		}
		if normalizeForMatch(old.Label) != normalizeForMatch(node.Label) {
			renamed++
		}
		if old.ParentID != node.ParentID {
			reparented++
		}
	}
	return renamed, reparented
}

func agendaRecordMap(mc *meetingContext) map[string]agendaItem {
	records := make(map[string]agendaItem)
	if mc == nil {
		return records
	}
	for _, agenda := range mc.Agenda {
		if strings.TrimSpace(agenda.ID) != "" {
			records[agenda.ID] = agenda
		}
	}
	return records
}

func normalizedAgendaRefs(refs []string, records map[string]agendaItem) []string {
	seen := make(map[string]struct{}, len(refs))
	result := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if records != nil {
			if _, ok := records[ref]; !ok {
				continue
			}
		}
		if _, duplicate := seen[ref]; duplicate {
			continue
		}
		seen[ref] = struct{}{}
		result = append(result, ref)
	}
	sort.Strings(result)
	return result
}

func topicAgendaRefs(node liveAnalysisTreeNode, records map[string]agendaItem) []string {
	return normalizedAgendaRefs(node.AgendaRefs, records)
}

// stableAgendaTopicID owns the materialized-agenda topic namespace. The ID is
// derived only from the immutable agenda record ID, so label/description edits
// cannot churn node identity. generation is used only for a deterministic
// collision escape; ordinary materialization always uses generation zero.
func stableAgendaTopicID(agendaID string, generation int) string {
	seed := "agenda-topic\x00" + strings.TrimSpace(agendaID)
	if generation > 0 {
		seed += "\x00" + strconv.Itoa(generation)
	}
	sum := sha256.Sum256([]byte(seed))
	return "topic-agenda-" + hex.EncodeToString(sum[:6])
}

func materializedTopicIDForAgenda(topics map[string]liveAnalysisTreeNode, records map[string]agendaItem, agendaID string) string {
	for id, topic := range topics {
		if topic.Kind == "topic" && containsExactString(topicAgendaRefs(topic, records), agendaID) {
			return id
		}
	}
	return ""
}

func availableAgendaTopicID(agendaID string, topics map[string]liveAnalysisTreeNode, records map[string]agendaItem) (string, bool) {
	if existing := materializedTopicIDForAgenda(topics, records, agendaID); existing != "" {
		return existing, true
	}
	for generation := 0; ; generation++ {
		candidate := stableAgendaTopicID(agendaID, generation)
		if existing, occupied := topics[candidate]; !occupied {
			return candidate, false
		} else if containsExactString(topicAgendaRefs(existing, records), agendaID) {
			return candidate, true
		}
	}
}

// normalizeLegacyAgendaTopicIDs upgrades the historical representation where
// a logical agenda record doubled as a tree-node ID. It is deliberately an
// in-memory, idempotent compatibility step: database JSON is left untouched,
// while every emitted/rebuilt payload uses an independent topic namespace.
func normalizeLegacyAgendaTopicIDs(state *liveAnalysisPayload, mc *meetingContext, stats *liveAnalysisTreeMergeStats) map[string]string {
	if state == nil || state.Tree == nil || mc == nil {
		return nil
	}
	records := agendaRecordMap(mc)
	if len(records) == 0 {
		return nil
	}

	existingByAgenda := make(map[string][]string)
	occupied := make(map[string]liveAnalysisTreeNode, len(state.Tree.Nodes))
	for _, node := range state.Tree.Nodes {
		occupied[node.ID] = node
		if node.Kind != "topic" {
			continue
		}
		for _, ref := range topicAgendaRefs(node, records) {
			if node.ID != ref {
				existingByAgenda[ref] = append(existingByAgenda[ref], node.ID)
			}
		}
	}

	remap := make(map[string]string)
	for _, node := range state.Tree.Nodes {
		agenda, legacy := records[node.ID]
		if !legacy || node.Kind != "topic" {
			continue
		}
		targetID := ""
		if existing := uniqueNonEmptyIDs(existingByAgenda[agenda.ID]); len(existing) > 0 {
			sort.Strings(existing)
			targetID = existing[0]
		}
		if targetID == "" {
			for generation := 0; ; generation++ {
				candidate := stableAgendaTopicID(agenda.ID, generation)
				if _, exists := occupied[candidate]; !exists {
					targetID = candidate
					break
				}
			}
		}
		remap[node.ID] = targetID
		occupied[targetID] = node
	}
	if len(remap) == 0 {
		return nil
	}

	result := make([]liveAnalysisTreeNode, 0, len(state.Tree.Nodes))
	index := make(map[string]int, len(state.Tree.Nodes))
	for _, source := range state.Tree.Nodes {
		targetID := remap[source.ID]
		if targetID == "" {
			targetID = source.ID
		}
		if targetID != source.ID {
			agendaID := source.ID
			source.ID = targetID
			source.AgendaRefs = appendUniqueStrings(source.AgendaRefs, agendaID)
			source.Materialized = true
			if source.Origin == "" || source.Origin == topicOriginDynamic {
				source.Origin = topicOriginAgenda
			}
			if source.AgendaRole == "" {
				agenda := records[agendaID]
				source.AgendaRole = effectiveAgendaRole(agenda.Role, agenda.Title, "")
			}
		}
		if at, exists := index[targetID]; exists {
			target := result[at]
			target.AgendaRefs = appendUniqueStrings(target.AgendaRefs, source.AgendaRefs...)
			target.MergedFromNodeIDs = appendUniqueStrings(target.MergedFromNodeIDs, source.MergedFromNodeIDs...)
			target.Materialized = target.Materialized || source.Materialized
			if len(target.AgendaRefs) > 0 && target.Origin == topicOriginDynamic {
				target.Origin = topicOriginMixed
			}
			if target.Label == "" {
				target.Label = source.Label
			}
			if target.Description == "" {
				target.Description = source.Description
			}
			result[at] = target
			continue
		}
		index[targetID] = len(result)
		result = append(result, source)
	}
	state.Tree.Nodes = result

	remapID := func(id string) string {
		id = strings.TrimSpace(id)
		if canonical := remap[id]; canonical != "" {
			return canonical
		}
		return id
	}
	for i := range state.Tree.Nodes {
		node := &state.Tree.Nodes[i]
		node.ParentID = remapID(node.ParentID)
		aliases := node.ModelTopicIDs[:0]
		for _, alias := range node.ModelTopicIDs {
			if _, agendaID := records[strings.TrimSpace(alias)]; !agendaID {
				aliases = append(aliases, alias)
			}
		}
		node.ModelTopicIDs = uniqueNonEmptyIDs(aliases)
		history := make([]string, 0, len(node.MergedFromNodeIDs))
		for _, id := range node.MergedFromNodeIDs {
			id = remapID(id)
			if id != node.ID {
				history = append(history, id)
			}
		}
		node.MergedFromNodeIDs = uniqueNonEmptyIDs(history)
	}
	state.Tree.Edges = state.Tree.Edges[:0]
	for _, node := range state.Tree.Nodes {
		if node.ID != treeRootNodeID && node.ParentID != "" {
			state.Tree.Edges = append(state.Tree.Edges, liveAnalysisTreeEdge{Source: node.ParentID, Target: node.ID})
		}
	}
	for i := range state.Tree.Relations {
		state.Tree.Relations[i].Source = remapID(state.Tree.Relations[i].Source)
		state.Tree.Relations[i].Target = remapID(state.Tree.Relations[i].Target)
	}
	for i := range state.Items {
		state.Items[i].CandidateTopicID = remapID(state.Items[i].CandidateTopicID)
	}
	for i := range state.ItemTombstones {
		for at, id := range state.ItemTombstones[i].CandidateAliases {
			state.ItemTombstones[i].CandidateAliases[at] = remapID(id)
		}
		state.ItemTombstones[i].CandidateAliases = uniqueNonEmptyIDs(state.ItemTombstones[i].CandidateAliases)
	}
	for i := range state.EmergingTopics {
		aliases := state.EmergingTopics[i].ModelTopicIDs[:0]
		for _, alias := range state.EmergingTopics[i].ModelTopicIDs {
			if _, agendaID := records[strings.TrimSpace(alias)]; !agendaID {
				aliases = append(aliases, alias)
			}
		}
		state.EmergingTopics[i].ModelTopicIDs = uniqueNonEmptyIDs(aliases)
	}
	for i := range state.AgendaAnchors {
		for at, id := range state.AgendaAnchors[i].MaterializedTopicIDs {
			state.AgendaAnchors[i].MaterializedTopicIDs[at] = remapID(id)
		}
		state.AgendaAnchors[i].MaterializedTopicIDs = uniqueNonEmptyIDs(state.AgendaAnchors[i].MaterializedTopicIDs)
	}
	if changes := state.TreeChanges; changes != nil {
		remapList := func(ids []string) []string {
			for i, id := range ids {
				ids[i] = remapID(id)
			}
			return uniqueNonEmptyIDs(ids)
		}
		changes.NewNodeIDs = remapList(changes.NewNodeIDs)
		changes.UpdatedNodeIDs = remapList(changes.UpdatedNodeIDs)
		changes.ReparentedNodeIDs = remapList(changes.ReparentedNodeIDs)
		changes.ResolvedNodeIDs = remapList(changes.ResolvedNodeIDs)
		changes.PromotedNodeIDs = remapList(changes.PromotedNodeIDs)
	}
	if stats != nil {
		stats.LegacyAgendaTopicIDsNormalized += len(remap)
	}
	for agendaID, topicID := range remap {
		log.Printf("Legacy agenda topic identity normalized. agendaId=%s legacyTopicId=%s materializedTopicId=%s agendaAnchorIdEqualsTopicId=%t", agendaID, agendaID, topicID, agendaID == topicID)
	}
	return remap
}

func appendUniqueStrings(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	result := make([]string, 0, len(values)+len(additions))
	for _, value := range append(append([]string(nil), values...), additions...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func reconcileAgendaAnchors(previous []agendaAnchor, mc *meetingContext, tree *liveAnalysisTree, items []liveAnalysisItem, treeVersion int64, final bool) []agendaAnchor {
	if mc == nil || len(mc.Agenda) == 0 {
		return nil
	}
	previousByID := make(map[string]agendaAnchor, len(previous))
	for _, anchor := range previous {
		previousByID[anchor.AgendaID] = anchor
	}
	records := agendaRecordMap(mc)

	nodes := make(map[string]liveAnalysisTreeNode)
	children := make(map[string][]string)
	if tree != nil {
		for _, node := range tree.Nodes {
			nodes[node.ID] = node
			if node.ParentID != "" {
				children[node.ParentID] = append(children[node.ParentID], node.ID)
			}
		}
	}
	hasDetailDescendant := func(rootID string) bool {
		queue := append([]string(nil), children[rootID]...)
		seen := make(map[string]struct{}, len(queue))
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			seen[id] = struct{}{}
			node, ok := nodes[id]
			if !ok {
				continue
			}
			if node.Kind != "topic" && node.Kind != "group" {
				return true
			}
			queue = append(queue, children[id]...)
		}
		return false
	}

	result := make([]agendaAnchor, 0, len(mc.Agenda))
	for _, agenda := range mc.Agenda {
		anchor := previousByID[agenda.ID]
		anchor.AgendaID = agenda.ID
		anchor.OriginalTitle = agenda.Title
		anchor.NormalizedSubject = normalizeForMatch(agenda.Title)
		anchor.Order = agenda.Order
		anchor.Role = effectiveAgendaRole(agenda.Role, agenda.Title, "")
		materialized := make([]string, 0, 1)
		discussed, merged := false, false
		for _, node := range nodes {
			refs := topicAgendaRefs(node, records)
			if !containsExactString(refs, agenda.ID) {
				continue
			}
			materialized = append(materialized, node.ID)
			discussed = discussed || hasDetailDescendant(node.ID)
			merged = merged || node.Origin == topicOriginMixed || len(refs) > 1 || len(node.MergedFromNodeIDs) > 0
		}
		// action_summary agendas are projection records only. They can still be
		// considered discussed when canonical items refer to them.
		if anchor.Role == agendaRoleActionSummary {
			materialized = nil
			for _, item := range items {
				if containsExactString(item.RelatedAgendaIDs, agenda.ID) {
					discussed = true
					break
				}
			}
		}
		anchor.MaterializedTopicIDs = uniqueNonEmptyIDs(materialized)
		sort.Strings(anchor.MaterializedTopicIDs)
		status, reason := agendaStatusPlanned, "agenda_record_preserved"
		switch {
		case merged:
			status, reason = agendaStatusMerged, "equivalent_topics_merged"
		case discussed:
			status, reason = agendaStatusDiscussed, "grounded_discussion_present"
		case len(anchor.MaterializedTopicIDs) > 0:
			status, reason = agendaStatusMaterialized, "topic_materialized"
		case final:
			status, reason = agendaStatusNotDiscussed, "meeting_ended_without_discussion"
		}
		if anchor.Status != status {
			anchor.StatusHistory = append(anchor.StatusHistory, agendaStatusTransition{From: anchor.Status, To: status, TreeVersion: treeVersion, Reason: reason})
		}
		anchor.Status = status
		result = append(result, anchor)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Order < result[j].Order })
	return result
}

func summarizeAgendaAnchorStatuses(anchors []agendaAnchor) map[string]int {
	counts := make(map[string]int)
	for _, anchor := range anchors {
		counts[anchor.Status]++
	}
	return counts
}

func isGroundedAgendaItem(item *liveAnalysisItem, assignment treeAssignment) bool {
	if item == nil || item.Inactive || item.MergedIntoID != "" || item.Status == "dismissed" {
		return false
	}
	// At this point low-information/discourse-only proposals have already been
	// filtered. Older model payloads may omit evidenceSequenceNos; a surviving
	// canonical item with substantive title/body is therefore the compatibility
	// form of grounded evidence.
	return item.InformationStatus == informationStatusGrounded || len(item.EvidenceSequenceNos) > 0 || len(assignment.EvidenceSequenceNos) > 0 ||
		strings.TrimSpace(item.Title+item.Body) != ""
}

// materializePlannedAgendaTopics creates a topic only when an assignment has
// grounded evidence. A single low-confidence proposal stays planned; two
// independent grounded proposals are sufficient accumulation to materialize.
func materializePlannedAgendaTopics(
	mc *meetingContext,
	items []liveAnalysisItem,
	assignments []treeAssignment,
	topics map[string]liveAnalysisTreeNode,
	parents map[string]string,
	addTopic func(liveAnalysisTreeNode),
	round int64,
	cfg TreeClassificationConfig,
	stats *liveAnalysisTreeMergeStats,
) {
	if mc == nil {
		return
	}
	itemByID := make(map[string]*liveAnalysisItem, len(items))
	for index := range items {
		itemByID[items[index].ID] = &items[index]
	}
	type proposal struct {
		assignment treeAssignment
		item       *liveAnalysisItem
		score      float64
	}
	byAgenda := make(map[string][]proposal)
	records := agendaRecordMap(mc)
	noAgendaItemIDs := make(map[string]struct{})
	for _, assignment := range assignments {
		if assignment.ServerSource == assignmentSourceNoAgendaSpan || assignment.ResolvedAgendaSpanMode == agendaContextModeNoAgenda {
			noAgendaItemIDs[assignment.nodeID()] = struct{}{}
		}
	}
	// A cross-cutting action-summary target is not a parent. Resolve it to the
	// best matching planned primary agenda before materialization. A strongly
	// matching generic unclassified proposal may use the same priority rule;
	// explicit no-agenda assignments were already excluded above.
	for index := range assignments {
		assignment := assignments[index]
		if assignment.ServerSource == assignmentSourceNoAgendaSpan || assignment.ResolvedAgendaSpanMode == agendaContextModeNoAgenda {
			continue
		}
		requested := strings.TrimSpace(assignment.ParentTopicID)
		requestedAgenda, requestedIsAgenda := records[requested]
		requestedActionSummary := requestedIsAgenda && effectiveAgendaRole(requestedAgenda.Role, requestedAgenda.Title, "") == agendaRoleActionSummary
		if !requestedActionSummary && requested != treeUnclassifiedTopicID {
			continue
		}
		item := itemByID[assignment.nodeID()]
		if !isGroundedAgendaItem(item, assignment) {
			continue
		}
		bestID, bestScore := "", 0.0
		for _, agenda := range mc.Agenda {
			if effectiveAgendaRole(agenda.Role, agenda.Title, "") == agendaRoleActionSummary {
				continue
			}
			score := semanticItemSimilarity(agenda.Title, item.Title+" "+item.Body)
			if core := semanticTopicCore(agenda.Title); len([]rune(core)) >= 3 && strings.Contains(semanticTopicCore(item.Title+" "+item.Body), core) && score < 0.75 {
				score = 0.75
			}
			if score > bestScore {
				bestID, bestScore = agenda.ID, score
			}
		}
		threshold := 0.45
		if requestedActionSummary {
			threshold = 0.16
		}
		if bestID != "" && bestScore >= threshold {
			assignments[index].ParentTopicID = bestID
		}
	}
	// A detail has one primary parent. When the model proposes multiple agenda
	// anchors for the same item, only its strongest proposal may materialize;
	// the others remain logical references instead of empty sibling topics.
	bestAgendaByItem := make(map[string]string)
	bestConfidenceByItem := make(map[string]float64)
	for _, assignment := range assignments {
		agenda, exists := records[strings.TrimSpace(assignment.ParentTopicID)]
		if !exists || effectiveAgendaRole(agenda.Role, agenda.Title, "") == agendaRoleActionSummary || assignment.ServerSource == assignmentSourceNoAgendaSpan {
			continue
		}
		itemID := assignment.nodeID()
		confidence := assignment.Confidence
		if confidence == 0 {
			confidence = 1
		}
		if previous, exists := bestConfidenceByItem[itemID]; !exists || confidence > previous {
			bestConfidenceByItem[itemID] = confidence
			bestAgendaByItem[itemID] = agenda.ID
		}
	}
	for _, assignment := range assignments {
		if assignment.ServerSource == assignmentSourceNoAgendaSpan || assignment.ResolvedAgendaSpanMode == agendaContextModeNoAgenda {
			continue
		}
		agendaID := strings.TrimSpace(assignment.ParentTopicID)
		if _, overridden := noAgendaItemIDs[assignment.nodeID()]; overridden {
			continue
		}
		if best := bestAgendaByItem[assignment.nodeID()]; best != "" && best != agendaID {
			continue
		}
		agenda, exists := records[agendaID]
		if !exists || effectiveAgendaRole(agenda.Role, agenda.Title, "") == agendaRoleActionSummary {
			continue
		}
		if existingTopicID := materializedTopicIDForAgenda(topics, records, agendaID); existingTopicID != "" {
			log.Printf("Agenda topic materialization reused. agendaId=%s materializedTopicId=%s agendaTopicIdReused=true agendaTopicIdCollision=%t", agendaID, existingTopicID, agendaID == existingTopicID)
			if stats != nil {
				stats.AgendaTopicIDsReused++
			}
			continue
		}
		item := itemByID[assignment.nodeID()]
		if !isGroundedAgendaItem(item, assignment) {
			continue
		}
		score := semanticItemSimilarity(agenda.Title, item.Title+" "+item.Body)
		byAgenda[agendaID] = append(byAgenda[agendaID], proposal{assignment: assignment, item: item, score: score})
	}
	for _, agenda := range mc.Agenda {
		proposals := byAgenda[agenda.ID]
		if len(proposals) == 0 {
			continue
		}
		sort.SliceStable(proposals, func(i, j int) bool { return proposals[i].score > proposals[j].score })
		selected := proposals[0]
		confidence := selected.assignment.Confidence
		explicit := selected.assignment.ServerSource == assignmentSourceActiveSpan
		topicID, reused := availableAgendaTopicID(agenda.ID, topics, records)
		repeated := selected.item != nil && selected.item.CandidateTopicID == topicID
		qualified := explicit || repeated || confidence == 0 || confidence >= cfg.normalized().AgendaAssignmentThreshold || len(proposals) >= 2
		if !qualified {
			continue
		}
		label := truncateRunes(strings.TrimSpace(selected.item.Title), liveAnalysisTopicLabelMaxRunes)
		if label == "" {
			label = agenda.Title
		}
		description := truncateRunes(strings.TrimSpace(selected.item.Body), liveAnalysisTreeDescriptionMaxRunes)
		addTopic(liveAnalysisTreeNode{
			ID: topicID, Kind: "topic", Label: label, Description: description,
			Origin: topicOriginAgenda, AgendaRole: agendaRolePrimary,
			AgendaRefs: []string{agenda.ID}, Materialized: true,
			CreatedAtVersion: round, UpdatedAtVersion: round,
		})
		parents[topicID] = treeRootNodeID
		log.Printf("Agenda topic materialized. agendaId=%s materializedTopicId=%s agendaTopicIdReused=%t agendaTopicIdCollision=%t", agenda.ID, topicID, reused, agenda.ID == topicID)
		if stats != nil {
			stats.AgendaTopicsMaterialized++
			if reused {
				stats.AgendaTopicIDsReused++
			}
			if agenda.ID == topicID {
				stats.AgendaTopicIDCollisions++
			}
		}
	}
}

func topicHasDescendants(topicID string, parents map[string]string) bool {
	for id := range parents {
		seen := make(map[string]struct{})
		current := id
		for current != "" && current != treeRootNodeID {
			if _, loop := seen[current]; loop {
				break
			}
			seen[current] = struct{}{}
			parent := parents[current]
			if parent == topicID {
				return true
			}
			current = parent
		}
	}
	return false
}

// pruneEmptyAgendaTopics reverses materialization once no canonical evidence
// remains. Newly-created topics receive a short hysteresis window; legacy
// fixed skeleton nodes (which have no lifecycle version) are removed at once.
func pruneEmptyAgendaTopics(tree *liveAnalysisTree, mc *meetingContext, round int64, final bool, stats *liveAnalysisTreeMergeStats) {
	if tree == nil {
		return
	}
	records := agendaRecordMap(mc)
	parents := make(map[string]string, len(tree.Nodes))
	for _, node := range tree.Nodes {
		parents[node.ID] = node.ParentID
	}
	removed := make(map[string]struct{})
	for _, node := range tree.Nodes {
		if node.Kind != "topic" || node.ID == treeRootNodeID || len(topicAgendaRefs(node, records)) == 0 {
			continue
		}
		if topicHasDescendants(node.ID, parents) {
			continue
		}
		if treeAuditIsManualChangeSource(node.LastParentChangeSource) || len(node.RelatedItemIDs) > 0 {
			continue
		}
		referenced := false
		for _, relation := range tree.Relations {
			if relation.Source == node.ID || relation.Target == node.ID {
				referenced = true
				break
			}
		}
		if referenced {
			continue
		}
		withinGrace := node.CreatedAtVersion > 0 && round >= node.CreatedAtVersion && round-node.CreatedAtVersion < agendaDematerializeGraceRounds
		if !final && withinGrace {
			continue
		}
		removed[node.ID] = struct{}{}
		log.Printf("Agenda topic dematerialized. agendaIds=%v materializedTopicId=%s final=%t", topicAgendaRefs(node, records), node.ID, final)
	}
	if len(removed) == 0 {
		return
	}
	nodes := tree.Nodes[:0]
	for _, node := range tree.Nodes {
		if _, drop := removed[node.ID]; drop {
			continue
		}
		nodes = append(nodes, node)
	}
	tree.Nodes = nodes
	edges := tree.Edges[:0]
	for _, edge := range tree.Edges {
		if _, sourceRemoved := removed[edge.Source]; sourceRemoved {
			continue
		}
		if _, targetRemoved := removed[edge.Target]; targetRemoved {
			continue
		}
		edges = append(edges, edge)
	}
	tree.Edges = edges
	if stats != nil {
		stats.AgendaTopicsDematerialized += len(removed)
	}
}

func mergeEquivalentAgendaDynamicTopics(topics map[string]liveAnalysisTreeNode, topicOrder []string, parents map[string]string, round int64, stats *liveAnalysisTreeMergeStats) []string {
	removed := make(map[string]struct{})
	for agendaTopicID, agendaTopic := range topics {
		if len(agendaTopic.AgendaRefs) == 0 || agendaTopic.AgendaRole == agendaRoleActionSummary {
			continue
		}
		for dynamicID, dynamicTopic := range topics {
			if dynamicID == agendaTopicID || dynamicTopic.Origin != topicOriginDynamic {
				continue
			}
			score := semanticItemSimilarity(agendaTopic.Label+" "+agendaTopic.Description, dynamicTopic.Label+" "+dynamicTopic.Description)
			if score < 0.72 {
				continue
			}
			for nodeID, parentID := range parents {
				if parentID == dynamicID {
					parents[nodeID] = agendaTopicID
				}
			}
			agendaTopic.AgendaRefs = appendUniqueStrings(agendaTopic.AgendaRefs, dynamicTopic.AgendaRefs...)
			agendaTopic.MergedFromNodeIDs = appendUniqueStrings(agendaTopic.MergedFromNodeIDs, dynamicID)
			agendaTopic.Origin = topicOriginMixed
			agendaTopic.Materialized = true
			agendaTopic.UpdatedAtVersion = round
			if len([]rune(semanticTopicCore(dynamicTopic.Label))) > len([]rune(semanticTopicCore(agendaTopic.Label))) {
				agendaTopic.Label = dynamicTopic.Label
			}
			topics[agendaTopicID] = agendaTopic
			delete(topics, dynamicID)
			delete(parents, dynamicID)
			removed[dynamicID] = struct{}{}
			log.Printf("Agenda topic merged. agendaIds=%v sourceTopicId=%s targetTopicId=%s", agendaTopic.AgendaRefs, dynamicID, agendaTopicID)
			if stats != nil {
				stats.AgendaTopicsMerged++
			}
		}
	}
	if len(removed) == 0 {
		return topicOrder
	}
	kept := topicOrder[:0]
	for _, id := range topicOrder {
		if _, drop := removed[id]; !drop {
			kept = append(kept, id)
		}
	}
	return kept
}

func mergeEquivalentAgendaDynamicTopicsInTree(tree *liveAnalysisTree, mc *meetingContext, round int64, stats *liveAnalysisTreeMergeStats) *liveAnalysisTree {
	if tree == nil {
		return nil
	}
	compatibilityState := liveAnalysisPayload{Tree: tree}
	normalizeLegacyAgendaTopicIDs(&compatibilityState, mc, stats)
	tree = compatibilityState.Tree
	nodes, parents, relations := treeStateFromPayloadTree(tree)
	topics := make(map[string]liveAnalysisTreeNode)
	groups := make(map[string]liveAnalysisTreeNode)
	topicOrder, groupOrder := []string{}, []string{}
	details := make([]liveAnalysisTreeNode, 0)
	for _, node := range nodes {
		switch {
		case node.ID == treeRootNodeID:
		case node.Kind == "topic":
			topics[node.ID] = node
			topicOrder = append(topicOrder, node.ID)
		case node.Kind == "group":
			groups[node.ID] = node
			groupOrder = append(groupOrder, node.ID)
		default:
			details = append(details, node)
		}
	}
	previousParents := make(map[string]string, len(parents))
	for id, parentID := range parents {
		previousParents[id] = parentID
	}
	topicOrder = mergeEquivalentAgendaDynamicTopics(topics, topicOrder, parents, round, stats)
	merged := assembleTree(mc, topics, topicOrder, groups, groupOrder, details, parents, previousParents, relations, round, stats)
	if diagnostics := validateTreeIntegrity(merged, nil, mc); !diagnostics.Valid {
		if stats != nil {
			stats.PreviousTreePreserved++
		}
		return tree
	}
	return merged
}
