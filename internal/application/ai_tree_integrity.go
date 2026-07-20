package application

import (
	"sort"
	"strings"
)

// treeIntegrityDiagnostics is safe to persist and expose. It contains only
// machine IDs and counters, never transcript or item text.
type treeIntegrityDiagnostics struct {
	Valid                           bool     `json:"valid"`
	DuplicateNodeIDs                []string `json:"duplicateNodeIds,omitempty"`
	CrossKindIDCollisions           []string `json:"crossKindIdCollisions,omitempty"`
	ReservedItemIDs                 []string `json:"reservedItemIds,omitempty"`
	DuplicateItemIDs                []string `json:"duplicateItemIds,omitempty"`
	SelfParentNodeIDs               []string `json:"selfParentNodeIds,omitempty"`
	OrphanNodeIDs                   []string `json:"orphanNodeIds,omitempty"`
	CycleNodeIDs                    []string `json:"cycleNodeIds,omitempty"`
	InvalidParentKindNodeIDs        []string `json:"invalidParentKindNodeIds,omitempty"`
	RootDirectDetailNodeIDs         []string `json:"rootDirectDetailNodeIds,omitempty"`
	MissingFixedAgendaIDs           []string `json:"missingFixedAgendaIds,omitempty"`
	MovedFixedAgendaIDs             []string `json:"movedFixedAgendaIds,omitempty"`
	FixedAgendaKindMismatchIDs      []string `json:"fixedAgendaKindMismatchIds,omitempty"`
	RenamedFixedAgendaIDs           []string `json:"renamedFixedAgendaIds,omitempty"`
	ActionSummaryTreeNodeIDs        []string `json:"actionSummaryTreeNodeIds,omitempty"`
	InvalidKindNodeIDs              []string `json:"invalidKindNodeIds,omitempty"`
	EmptyGroupNodeIDs               []string `json:"emptyGroupNodeIds,omitempty"`
	SingleChildGroupNodeIDs         []string `json:"singleChildGroupNodeIds,omitempty"`
	HardDepthNodeIDs                []string `json:"hardDepthNodeIds,omitempty"`
	ExpectedFixedAgendaCount        int      `json:"expectedFixedAgendaCount"`
	ActualFixedAgendaCount          int      `json:"actualFixedAgendaCount"`
	RootCount                       int      `json:"rootCount"`
	EdgeCountMismatch               bool     `json:"edgeCountMismatch,omitempty"`
	EdgeParentMismatch              bool     `json:"edgeParentMismatch,omitempty"`
	AgendaRecordCount               int      `json:"agendaRecordCount"`
	AgendaRecordsPreserved          int      `json:"agendaRecordsPreserved"`
	AgendaRecordIntegrityValid      bool     `json:"agendaRecordIntegrityValid"`
	MissingAgendaRecordIDs          []string `json:"missingAgendaRecordIds,omitempty"`
	DuplicateAgendaRecordIDs        []string `json:"duplicateAgendaRecordIds,omitempty"`
	MutatedAgendaRecordIDs          []string `json:"mutatedAgendaRecordIds,omitempty"`
	PlannedAgendaCount              int      `json:"plannedAgendaCount"`
	MaterializedAgendaCount         int      `json:"materializedAgendaCount"`
	DiscussedAgendaCount            int      `json:"discussedAgendaCount"`
	MergedAgendaCount               int      `json:"mergedAgendaCount"`
	NotDiscussedAgendaCount         int      `json:"notDiscussedAgendaCount"`
	AgendaReferenceIntegrityValid   bool     `json:"agendaReferenceIntegrityValid"`
	AgendaNodeIDNamespaceValid      bool     `json:"agendaNodeIdNamespaceValid"`
	AgendaTopicIDCollisions         []string `json:"agendaTopicIdCollisions,omitempty"`
	UnknownAgendaRefs               []string `json:"unknownAgendaRefs,omitempty"`
	OrphanAgendaRefs                []string `json:"orphanAgendaRefs,omitempty"`
	OrphanMaterializedTopicIDs      []string `json:"orphanMaterializedTopicIds,omitempty"`
	AgendaMaterializationMismatches []string `json:"agendaMaterializationMismatches,omitempty"`
	LegacyAgendaEdgeNodeIDs         []string `json:"legacyAgendaEdgeNodeIds,omitempty"`
	DuplicateAgendaMaterializations []string `json:"duplicateAgendaMaterializations,omitempty"`
	EmptyAgendaTopicIDs             []string `json:"emptyAgendaTopicIds,omitempty"`
}

func validateTreeIntegrity(tree *liveAnalysisTree, items []liveAnalysisItem, mc *meetingContext, anchorValues ...[]agendaAnchor) treeIntegrityDiagnostics {
	d := treeIntegrityDiagnostics{}
	primaryAgenda := make(map[string]agendaItem)
	actionAgenda := make(map[string]struct{})
	if mc != nil {
		for _, agenda := range mc.Agenda {
			if effectiveAgendaRole(agenda.Role, agenda.Title, "") == agendaRoleActionSummary {
				actionAgenda[agenda.ID] = struct{}{}
				continue
			}
			primaryAgenda[agenda.ID] = agenda
		}
	}
	d.AgendaRecordCount = len(primaryAgenda) + len(actionAgenda)
	d.AgendaRecordsPreserved = d.AgendaRecordCount
	d.AgendaRecordIntegrityValid = true
	d.AgendaReferenceIntegrityValid = true
	d.AgendaNodeIDNamespaceValid = true
	anchorTopicIDs := make(map[string]map[string]struct{})
	anchorsProvided := len(anchorValues) > 0
	if len(anchorValues) > 0 {
		d.AgendaRecordsPreserved = 0
		expected := agendaRecordMap(mc)
		seen := make(map[string]struct{}, len(anchorValues[0]))
		for _, anchor := range anchorValues[0] {
			record, exists := expected[anchor.AgendaID]
			if !exists {
				d.MutatedAgendaRecordIDs = append(d.MutatedAgendaRecordIDs, anchor.AgendaID)
				continue
			}
			if _, duplicate := seen[anchor.AgendaID]; duplicate {
				d.DuplicateAgendaRecordIDs = append(d.DuplicateAgendaRecordIDs, anchor.AgendaID)
				continue
			}
			seen[anchor.AgendaID] = struct{}{}
			materialized := make(map[string]struct{}, len(anchor.MaterializedTopicIDs))
			for _, topicID := range anchor.MaterializedTopicIDs {
				if topicID = strings.TrimSpace(topicID); topicID != "" {
					materialized[topicID] = struct{}{}
				}
			}
			anchorTopicIDs[anchor.AgendaID] = materialized
			if anchor.OriginalTitle != record.Title || anchor.Order != record.Order || effectiveAgendaRole(anchor.Role, anchor.OriginalTitle, "") != effectiveAgendaRole(record.Role, record.Title, "") {
				d.MutatedAgendaRecordIDs = append(d.MutatedAgendaRecordIDs, anchor.AgendaID)
				continue
			}
			d.AgendaRecordsPreserved++
		}
		for id := range expected {
			if _, exists := seen[id]; !exists {
				d.MissingAgendaRecordIDs = append(d.MissingAgendaRecordIDs, id)
			}
		}
		d.AgendaRecordIntegrityValid = d.AgendaRecordsPreserved == d.AgendaRecordCount && len(d.MissingAgendaRecordIDs) == 0 && len(d.DuplicateAgendaRecordIDs) == 0 && len(d.MutatedAgendaRecordIDs) == 0
	}

	itemIDs := make(map[string]string, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		if reservedItemID(id) {
			d.ReservedItemIDs = append(d.ReservedItemIDs, id)
		}
		if _, duplicate := itemIDs[id]; duplicate {
			d.DuplicateItemIDs = append(d.DuplicateItemIDs, id)
		} else {
			itemIDs[id] = item.Kind
		}
	}

	if tree == nil {
		d.PlannedAgendaCount = len(primaryAgenda)
		sortTreeIntegrityDiagnostics(&d)
		d.Valid = len(items) == 0
		return d
	}

	nodes := make(map[string]liveAnalysisTreeNode, len(tree.Nodes))
	firstKinds := make(map[string]string, len(tree.Nodes))
	agendaTopics := make(map[string][]string)
	for _, node := range tree.Nodes {
		id := strings.TrimSpace(node.ID)
		if id == treeRootNodeID {
			d.RootCount++
		}
		if previousKind, duplicate := firstKinds[id]; duplicate {
			d.DuplicateNodeIDs = append(d.DuplicateNodeIDs, id)
			if previousKind != node.Kind {
				d.CrossKindIDCollisions = append(d.CrossKindIDCollisions, id)
			}
			continue
		}
		firstKinds[id] = node.Kind
		nodes[id] = node
		if _, collides := agendaRecordMap(mc)[id]; collides || strings.HasPrefix(strings.ToLower(id), agendaIDPrefix) {
			d.AgendaTopicIDCollisions = append(d.AgendaTopicIDCollisions, id)
		}
		if !validLiveAnalysisTreeNodeKind(node.Kind) {
			d.InvalidKindNodeIDs = append(d.InvalidKindNodeIDs, id)
		}
		if _, action := actionAgenda[id]; action {
			d.ActionSummaryTreeNodeIDs = append(d.ActionSummaryTreeNodeIDs, id)
		}
		for _, ref := range node.AgendaRefs {
			ref = strings.TrimSpace(ref)
			if _, primary := primaryAgenda[ref]; !primary {
				if _, action := actionAgenda[ref]; action {
					d.ActionSummaryTreeNodeIDs = append(d.ActionSummaryTreeNodeIDs, id)
				} else {
					d.UnknownAgendaRefs = append(d.UnknownAgendaRefs, ref)
				}
				continue
			}
			if node.Kind != "topic" {
				d.OrphanAgendaRefs = append(d.OrphanAgendaRefs, id+":"+ref)
				continue
			}
			agendaTopics[ref] = append(agendaTopics[ref], id)
		}
	}
	d.AgendaNodeIDNamespaceValid = len(d.AgendaTopicIDCollisions) == 0
	if anchorsProvided {
		for agendaID, topicIDs := range anchorTopicIDs {
			for topicID := range topicIDs {
				node, exists := nodes[topicID]
				if !exists || node.Kind != "topic" {
					d.OrphanMaterializedTopicIDs = append(d.OrphanMaterializedTopicIDs, topicID)
					continue
				}
				if !containsExactString(topicAgendaRefs(node, agendaRecordMap(mc)), agendaID) {
					d.AgendaMaterializationMismatches = append(d.AgendaMaterializationMismatches, agendaID+":"+topicID)
				}
			}
		}
		for agendaID, topicIDs := range agendaTopics {
			for _, topicID := range uniqueNonEmptyIDs(topicIDs) {
				if _, linked := anchorTopicIDs[agendaID][topicID]; !linked {
					d.AgendaMaterializationMismatches = append(d.AgendaMaterializationMismatches, agendaID+":"+topicID)
				}
			}
		}
	}
	for id, itemKind := range itemIDs {
		if node, exists := nodes[id]; exists && (node.Kind == "topic" || node.Kind == "group" || node.ID == treeRootNodeID) {
			d.CrossKindIDCollisions = append(d.CrossKindIDCollisions, id+":"+itemKind+"/"+node.Kind)
		}
	}

	for id := range primaryAgenda {
		topicIDs := uniqueNonEmptyIDs(agendaTopics[id])
		if len(topicIDs) == 0 {
			d.PlannedAgendaCount++
			continue
		}
		d.MaterializedAgendaCount++
		if len(topicIDs) > 1 {
			splitGroupID := ""
			intentionalSplit := true
			for _, topicID := range topicIDs {
				groupID := strings.TrimSpace(nodes[topicID].AgendaSplitGroupID)
				if groupID == "" {
					intentionalSplit = false
					break
				}
				if splitGroupID == "" {
					splitGroupID = groupID
				} else if splitGroupID != groupID {
					intentionalSplit = false
					break
				}
			}
			if !intentionalSplit {
				d.DuplicateAgendaMaterializations = append(d.DuplicateAgendaMaterializations, id)
			}
		}
		for _, topicID := range topicIDs {
			topic := nodes[topicID]
			if topic.Origin == topicOriginMixed || len(topic.AgendaRefs) > 1 || len(topic.MergedFromNodeIDs) > 0 {
				d.MergedAgendaCount++
				break
			}
		}
	}

	for id, node := range nodes {
		if id == treeRootNodeID {
			if node.ParentID != "" || node.Kind != "topic" {
				d.InvalidParentKindNodeIDs = append(d.InvalidParentKindNodeIDs, id)
			}
			continue
		}
		if node.ParentID == id {
			d.SelfParentNodeIDs = append(d.SelfParentNodeIDs, id)
			continue
		}
		parent, exists := nodes[node.ParentID]
		if !exists || node.ParentID == "" {
			d.OrphanNodeIDs = append(d.OrphanNodeIDs, id)
			continue
		}
		switch node.Kind {
		case "topic":
			if parent.Kind != "topic" {
				d.InvalidParentKindNodeIDs = append(d.InvalidParentKindNodeIDs, id)
			}
		case "group":
			if parent.Kind != "topic" && parent.Kind != "group" {
				d.InvalidParentKindNodeIDs = append(d.InvalidParentKindNodeIDs, id)
			}
		default:
			if node.ParentID == treeRootNodeID {
				d.RootDirectDetailNodeIDs = append(d.RootDirectDetailNodeIDs, id)
			}
			if parent.Kind != "topic" && parent.Kind != "group" {
				d.InvalidParentKindNodeIDs = append(d.InvalidParentKindNodeIDs, id)
			}
		}
	}

	childCounts := make(map[string]int)
	for id, node := range nodes {
		if id != treeRootNodeID {
			childCounts[node.ParentID]++
		}
	}
	discussedAgendaIDs := make(map[string]struct{})
	for id, node := range nodes {
		if node.Kind == "topic" && len(topicAgendaRefs(node, agendaRecordMap(mc))) > 0 {
			if childCounts[id] == 0 {
				d.EmptyAgendaTopicIDs = append(d.EmptyAgendaTopicIDs, id)
			} else {
				for _, agendaID := range topicAgendaRefs(node, agendaRecordMap(mc)) {
					discussedAgendaIDs[agendaID] = struct{}{}
				}
			}
		}
		if node.Kind != "group" {
			continue
		}
		switch childCounts[id] {
		case 0:
			d.EmptyGroupNodeIDs = append(d.EmptyGroupNodeIDs, id)
		case 1:
			// One-child groups are retained briefly by the existing hysteresis
			// policy, so report them without making the canonical tree invalid.
			d.SingleChildGroupNodeIDs = append(d.SingleChildGroupNodeIDs, id)
		}
	}
	d.DiscussedAgendaCount = len(discussedAgendaIDs)

	// Parent chains are authoritative; detect cycles independently of edges.
	for start := range nodes {
		seen := make(map[string]struct{})
		current := start
		for current != "" && current != treeRootNodeID {
			if _, loop := seen[current]; loop {
				d.CycleNodeIDs = append(d.CycleNodeIDs, start)
				break
			}
			seen[current] = struct{}{}
			node, exists := nodes[current]
			if !exists {
				break
			}
			current = node.ParentID
		}
		if len(seen) > treeHardMaxDepth {
			d.HardDepthNodeIDs = append(d.HardDepthNodeIDs, start)
		}
	}

	if len(tree.Edges) != len(tree.Nodes)-1 {
		d.EdgeCountMismatch = true
	}
	expectedEdges := make(map[string]struct{}, len(nodes)-1)
	for id, node := range nodes {
		if id != treeRootNodeID && node.ParentID != "" {
			expectedEdges[node.ParentID+"\x00"+id] = struct{}{}
		}
	}
	seenTargets := make(map[string]struct{}, len(tree.Edges))
	for _, edge := range tree.Edges {
		for _, endpoint := range []string{strings.TrimSpace(edge.Source), strings.TrimSpace(edge.Target)} {
			if strings.HasPrefix(strings.ToLower(endpoint), agendaIDPrefix) {
				d.LegacyAgendaEdgeNodeIDs = append(d.LegacyAgendaEdgeNodeIDs, endpoint)
			}
		}
		key := strings.TrimSpace(edge.Source) + "\x00" + strings.TrimSpace(edge.Target)
		if _, ok := expectedEdges[key]; !ok {
			d.EdgeParentMismatch = true
		}
		if _, duplicate := seenTargets[edge.Target]; duplicate {
			d.EdgeParentMismatch = true
		}
		seenTargets[edge.Target] = struct{}{}
	}
	if len(seenTargets) != len(expectedEdges) {
		d.EdgeParentMismatch = true
	}

	sortTreeIntegrityDiagnostics(&d)
	d.AgendaNodeIDNamespaceValid = len(d.AgendaTopicIDCollisions) == 0 && len(d.LegacyAgendaEdgeNodeIDs) == 0
	d.AgendaReferenceIntegrityValid = len(d.UnknownAgendaRefs) == 0 && len(d.OrphanAgendaRefs) == 0 &&
		len(d.OrphanMaterializedTopicIDs) == 0 && len(d.AgendaMaterializationMismatches) == 0 &&
		len(d.DuplicateAgendaMaterializations) == 0 && len(d.ActionSummaryTreeNodeIDs) == 0 && d.AgendaNodeIDNamespaceValid
	d.Valid = d.RootCount == 1 && !d.EdgeCountMismatch && !d.EdgeParentMismatch &&
		len(d.DuplicateNodeIDs) == 0 && len(d.CrossKindIDCollisions) == 0 &&
		len(d.ReservedItemIDs) == 0 && len(d.DuplicateItemIDs) == 0 &&
		len(d.SelfParentNodeIDs) == 0 && len(d.OrphanNodeIDs) == 0 && len(d.CycleNodeIDs) == 0 &&
		len(d.InvalidParentKindNodeIDs) == 0 && len(d.RootDirectDetailNodeIDs) == 0 &&
		d.AgendaRecordIntegrityValid && d.AgendaReferenceIntegrityValid && d.AgendaNodeIDNamespaceValid && len(d.InvalidKindNodeIDs) == 0 &&
		len(d.EmptyGroupNodeIDs) == 0 && len(d.HardDepthNodeIDs) == 0
	return d
}

func sortTreeIntegrityDiagnostics(d *treeIntegrityDiagnostics) {
	if d == nil {
		return
	}
	values := []*[]string{
		&d.DuplicateNodeIDs, &d.CrossKindIDCollisions, &d.ReservedItemIDs, &d.DuplicateItemIDs,
		&d.SelfParentNodeIDs, &d.OrphanNodeIDs, &d.CycleNodeIDs, &d.InvalidParentKindNodeIDs,
		&d.RootDirectDetailNodeIDs, &d.MissingFixedAgendaIDs, &d.MovedFixedAgendaIDs,
		&d.FixedAgendaKindMismatchIDs, &d.RenamedFixedAgendaIDs, &d.ActionSummaryTreeNodeIDs,
		&d.InvalidKindNodeIDs, &d.EmptyGroupNodeIDs, &d.SingleChildGroupNodeIDs, &d.HardDepthNodeIDs,
		&d.AgendaTopicIDCollisions, &d.UnknownAgendaRefs, &d.OrphanAgendaRefs, &d.OrphanMaterializedTopicIDs,
		&d.AgendaMaterializationMismatches, &d.LegacyAgendaEdgeNodeIDs, &d.DuplicateAgendaMaterializations, &d.EmptyAgendaTopicIDs,
		&d.MissingAgendaRecordIDs, &d.DuplicateAgendaRecordIDs, &d.MutatedAgendaRecordIDs,
	}
	for _, value := range values {
		*value = uniqueNonEmptyIDs(*value)
		sort.Strings(*value)
	}
}

func discussionTreeSkeleton(mc *meetingContext) *liveAnalysisTree {
	root := liveAnalysisTreeNode{ID: treeRootNodeID, Kind: "topic", Label: mc.rootLabel(), Description: mc.rootDescription(), Origin: topicOriginSystem}
	return &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{root}, Edges: []liveAnalysisTreeEdge{}}
}

// fixedAgendaSkeleton is retained only as a source-compatible test/helper
// alias for older call sites. It no longer creates agenda topics.
func fixedAgendaSkeleton(mc *meetingContext) *liveAnalysisTree {
	return discussionTreeSkeleton(mc)
}

func applyTreeIntegrityStats(stats *liveAnalysisTreeMergeStats, diagnostics treeIntegrityDiagnostics) {
	if stats == nil {
		return
	}
	stats.DuplicateNodeIDsDetected += len(diagnostics.DuplicateNodeIDs)
	stats.CrossKindIDCollisions += len(diagnostics.CrossKindIDCollisions)
	stats.SelfParentRejected += len(diagnostics.SelfParentNodeIDs)
	stats.InvalidParentKindRejected += len(diagnostics.InvalidParentKindNodeIDs)
	stats.ExpectedFixedAgendaCount = diagnostics.ExpectedFixedAgendaCount
	stats.ActualFixedAgendaCount = diagnostics.ActualFixedAgendaCount
	stats.MissingFixedAgendaIDs = append([]string(nil), diagnostics.MissingFixedAgendaIDs...)
	stats.FixedAgendaMoved += len(diagnostics.MovedFixedAgendaIDs)
	stats.FixedAgendaRemoved += len(diagnostics.MissingFixedAgendaIDs)
	stats.FixedAgendaKindChanged += len(diagnostics.FixedAgendaKindMismatchIDs)
	stats.AgendaRecordCount = diagnostics.AgendaRecordCount
	stats.AgendaRecordsPreserved = diagnostics.AgendaRecordsPreserved
	stats.AgendaRecordIntegrityValid = diagnostics.AgendaRecordIntegrityValid
	stats.PlannedAgendaCount = diagnostics.PlannedAgendaCount
	stats.MaterializedAgendaCount = diagnostics.MaterializedAgendaCount
	stats.DiscussedAgendaCount = diagnostics.DiscussedAgendaCount
	stats.MergedAgendaCount = diagnostics.MergedAgendaCount
	stats.NotDiscussedAgendaCount = diagnostics.NotDiscussedAgendaCount
	stats.UnknownAgendaReferences = len(diagnostics.UnknownAgendaRefs)
	stats.OrphanAgendaReferences = len(diagnostics.OrphanAgendaRefs)
	stats.OrphanMaterializedTopicIDs = len(diagnostics.OrphanMaterializedTopicIDs)
	stats.AgendaTopicIDCollisions += len(diagnostics.AgendaTopicIDCollisions)
	stats.AgendaNodeIDNamespaceValid = diagnostics.AgendaNodeIDNamespaceValid
	stats.AgendaReferenceIntegrityValid = diagnostics.AgendaReferenceIntegrityValid
	stats.TreeIntegrityValid = diagnostics.Valid
	stats.DuplicateAgendaMaterializations = len(diagnostics.DuplicateAgendaMaterializations)
	stats.EmptyAgendaTopicsRejected = len(diagnostics.EmptyAgendaTopicIDs)
}

func preserveTreeOnIntegrityFailure(candidate, previous *liveAnalysisTree, currentItems, previousItems []liveAnalysisItem, mc *meetingContext, stats *liveAnalysisTreeMergeStats) (*liveAnalysisTree, treeIntegrityDiagnostics, bool) {
	diagnostics := validateTreeIntegrity(candidate, currentItems, mc)
	applyTreeIntegrityStats(stats, diagnostics)
	if diagnostics.Valid {
		return candidate, diagnostics, false
	}
	if stats != nil {
		stats.TreePayloadRejected++
	}
	previousDiagnostics := validateTreeIntegrity(previous, previousItems, mc)
	if previousDiagnostics.Valid {
		if stats != nil {
			stats.PreviousTreePreserved++
		}
		return previous, diagnostics, true
	}
	return discussionTreeSkeleton(mc), diagnostics, true
}
