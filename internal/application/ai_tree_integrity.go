package application

import (
	"sort"
	"strings"
)

// treeIntegrityDiagnostics is safe to persist and expose. It contains only
// machine IDs and counters, never transcript or item text.
type treeIntegrityDiagnostics struct {
	Valid                      bool     `json:"valid"`
	DuplicateNodeIDs           []string `json:"duplicateNodeIds,omitempty"`
	CrossKindIDCollisions      []string `json:"crossKindIdCollisions,omitempty"`
	ReservedItemIDs            []string `json:"reservedItemIds,omitempty"`
	DuplicateItemIDs           []string `json:"duplicateItemIds,omitempty"`
	SelfParentNodeIDs          []string `json:"selfParentNodeIds,omitempty"`
	OrphanNodeIDs              []string `json:"orphanNodeIds,omitempty"`
	CycleNodeIDs               []string `json:"cycleNodeIds,omitempty"`
	InvalidParentKindNodeIDs   []string `json:"invalidParentKindNodeIds,omitempty"`
	RootDirectDetailNodeIDs    []string `json:"rootDirectDetailNodeIds,omitempty"`
	MissingFixedAgendaIDs      []string `json:"missingFixedAgendaIds,omitempty"`
	MovedFixedAgendaIDs        []string `json:"movedFixedAgendaIds,omitempty"`
	FixedAgendaKindMismatchIDs []string `json:"fixedAgendaKindMismatchIds,omitempty"`
	RenamedFixedAgendaIDs      []string `json:"renamedFixedAgendaIds,omitempty"`
	ActionSummaryTreeNodeIDs   []string `json:"actionSummaryTreeNodeIds,omitempty"`
	InvalidKindNodeIDs         []string `json:"invalidKindNodeIds,omitempty"`
	EmptyGroupNodeIDs          []string `json:"emptyGroupNodeIds,omitempty"`
	SingleChildGroupNodeIDs    []string `json:"singleChildGroupNodeIds,omitempty"`
	HardDepthNodeIDs           []string `json:"hardDepthNodeIds,omitempty"`
	ExpectedFixedAgendaCount   int      `json:"expectedFixedAgendaCount"`
	ActualFixedAgendaCount     int      `json:"actualFixedAgendaCount"`
	RootCount                  int      `json:"rootCount"`
	EdgeCountMismatch          bool     `json:"edgeCountMismatch,omitempty"`
	EdgeParentMismatch         bool     `json:"edgeParentMismatch,omitempty"`
}

func validateTreeIntegrity(tree *liveAnalysisTree, items []liveAnalysisItem, mc *meetingContext) treeIntegrityDiagnostics {
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
	d.ExpectedFixedAgendaCount = len(primaryAgenda)

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
		for id := range primaryAgenda {
			d.MissingFixedAgendaIDs = append(d.MissingFixedAgendaIDs, id)
		}
		sortTreeIntegrityDiagnostics(&d)
		d.Valid = len(primaryAgenda) == 0 && len(items) == 0
		return d
	}

	nodes := make(map[string]liveAnalysisTreeNode, len(tree.Nodes))
	firstKinds := make(map[string]string, len(tree.Nodes))
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
		if !validLiveAnalysisTreeNodeKind(node.Kind) {
			d.InvalidKindNodeIDs = append(d.InvalidKindNodeIDs, id)
		}
		if _, action := actionAgenda[id]; action {
			d.ActionSummaryTreeNodeIDs = append(d.ActionSummaryTreeNodeIDs, id)
		}
	}
	for id, itemKind := range itemIDs {
		if node, exists := nodes[id]; exists && (node.Kind == "topic" || node.Kind == "group" || node.ID == treeRootNodeID) {
			d.CrossKindIDCollisions = append(d.CrossKindIDCollisions, id+":"+itemKind+"/"+node.Kind)
		}
	}

	for id, agenda := range primaryAgenda {
		node, exists := nodes[id]
		if !exists {
			d.MissingFixedAgendaIDs = append(d.MissingFixedAgendaIDs, id)
			continue
		}
		d.ActualFixedAgendaCount++
		if node.Kind != "topic" || node.Origin != topicOriginAgenda {
			d.FixedAgendaKindMismatchIDs = append(d.FixedAgendaKindMismatchIDs, id)
		}
		if node.ParentID != treeRootNodeID {
			d.MovedFixedAgendaIDs = append(d.MovedFixedAgendaIDs, id)
		}
		if node.Label != agenda.Title {
			d.RenamedFixedAgendaIDs = append(d.RenamedFixedAgendaIDs, id)
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
			if node.ParentID != treeRootNodeID {
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
	for id, node := range nodes {
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
	d.Valid = d.RootCount == 1 && !d.EdgeCountMismatch && !d.EdgeParentMismatch &&
		len(d.DuplicateNodeIDs) == 0 && len(d.CrossKindIDCollisions) == 0 &&
		len(d.ReservedItemIDs) == 0 && len(d.DuplicateItemIDs) == 0 &&
		len(d.SelfParentNodeIDs) == 0 && len(d.OrphanNodeIDs) == 0 && len(d.CycleNodeIDs) == 0 &&
		len(d.InvalidParentKindNodeIDs) == 0 && len(d.RootDirectDetailNodeIDs) == 0 &&
		len(d.MissingFixedAgendaIDs) == 0 && len(d.MovedFixedAgendaIDs) == 0 &&
		len(d.FixedAgendaKindMismatchIDs) == 0 && len(d.RenamedFixedAgendaIDs) == 0 &&
		len(d.ActionSummaryTreeNodeIDs) == 0 && len(d.InvalidKindNodeIDs) == 0 &&
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
	}
	for _, value := range values {
		*value = uniqueNonEmptyIDs(*value)
		sort.Strings(*value)
	}
}

func fixedAgendaSkeleton(mc *meetingContext) *liveAnalysisTree {
	root := liveAnalysisTreeNode{ID: treeRootNodeID, Kind: "topic", Label: mc.rootLabel(), Description: mc.rootDescription(), Origin: topicOriginSystem}
	nodes := []liveAnalysisTreeNode{root}
	edges := make([]liveAnalysisTreeEdge, 0)
	if mc != nil {
		for _, agenda := range mc.Agenda {
			if effectiveAgendaRole(agenda.Role, agenda.Title, "") == agendaRoleActionSummary {
				continue
			}
			nodes = append(nodes, liveAnalysisTreeNode{ID: agenda.ID, Kind: "topic", ParentID: treeRootNodeID, Label: agenda.Title, Origin: topicOriginAgenda, AgendaRole: agendaRolePrimary})
			edges = append(edges, liveAnalysisTreeEdge{Source: treeRootNodeID, Target: agenda.ID})
		}
	}
	return &liveAnalysisTree{Nodes: nodes, Edges: edges}
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
	return fixedAgendaSkeleton(mc), diagnostics, true
}
