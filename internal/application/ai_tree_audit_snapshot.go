package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"deciscope-core-api/internal/domain"
)

type treeAuditSnapshot struct {
	SessionID                 string                       `json:"sessionId"`
	TreeVersion               int64                        `json:"treeVersion"`
	CoverageThroughSequenceNo int64                        `json:"coverageThroughSequenceNo"`
	Nodes                     []treeAuditSnapshotNode      `json:"nodes"`
	Candidates                []treeAuditSnapshotCandidate `json:"candidates"`
	// AgendaIDs lists logical pre-meeting agenda-record IDs. They are reference
	// values only and must never be used where an operation expects a canonical
	// tree-node ID; concrete topic IDs are in AgendaAnchors.MaterializedTopicIDs.
	AgendaIDs     []string                        `json:"agendaIds"`
	AgendaAnchors []treeAuditSnapshotAgendaAnchor `json:"agendaAnchors"`
	// ValidParentCanonicalNodeIDs lists the existing topic/group container
	// node IDs an operation may use as a toParent (action_summary agenda
	// excluded).
	ValidParentCanonicalNodeIDs []string                   `json:"validParentCanonicalNodeIds"`
	RecentTreeChanges           *liveAnalysisTreeChanges   `json:"recentTreeChanges,omitempty"`
	PrecheckFindings            []treeAuditPrecheckFinding `json:"precheckFindings"`
	EvidenceSegments            []treeAuditEvidenceSegment `json:"evidenceSegments"`
	RecentTranscript            []treeAuditEvidenceSegment `json:"recentTranscript"`
	Compressed                  bool                       `json:"compressed"`
}

type treeAuditSnapshotNode struct {
	CanonicalNodeID          string                 `json:"canonicalNodeId"`
	NodeType                 string                 `json:"nodeType"`
	Title                    string                 `json:"title"`
	Description              string                 `json:"description,omitempty"`
	ParentCanonicalNodeID    string                 `json:"parentCanonicalNodeId"`
	Fixed                    bool                   `json:"fixed"`
	Dynamic                  bool                   `json:"dynamic"`
	Tentative                bool                   `json:"tentative"`
	Status                   string                 `json:"status,omitempty"`
	Subtype                  string                 `json:"subtype,omitempty"`
	InformationStatus        string                 `json:"informationStatus,omitempty"`
	CreatedAtVersion         int64                  `json:"createdAtVersion,omitempty"`
	ClassificationConfidence float64                `json:"classificationConfidence,omitempty"`
	AssignmentReason         string                 `json:"assignmentReason,omitempty"`
	CandidateTopicID         string                 `json:"candidateTopicId,omitempty"`
	EvidenceSequenceNos      []int64                `json:"evidenceSequenceNos,omitempty"`
	EvidenceRoles            []treeAuditEvidenceRef `json:"evidenceRoles,omitempty"`
	AgendaRefs               []string               `json:"agendaRefs,omitempty"`
	MergedFromNodeIDs        []string               `json:"mergedFromNodeIds,omitempty"`
	AgendaSplitGroupID       string                 `json:"agendaSplitGroupId,omitempty"`
}

type treeAuditSnapshotAgendaAnchor struct {
	AgendaID             string   `json:"agendaId"`
	OriginalTitle        string   `json:"originalTitle"`
	Status               string   `json:"status"`
	MaterializedTopicIDs []string `json:"materializedTopicIds"`
}

type treeAuditEvidenceRef struct {
	SequenceNo int64                 `json:"sequenceNo"`
	Role       treeAuditEvidenceRole `json:"role"`
}

type treeAuditSnapshotCandidate struct {
	ID              string   `json:"candidateId"`
	Title           string   `json:"title"`
	Description     string   `json:"description,omitempty"`
	EvidenceItemIDs []string `json:"evidenceItemIds"`
	FirstVersion    int64    `json:"firstVersion"`
	LastVersion     int64    `json:"lastVersion"`
	RoundCount      int      `json:"roundCount"`
	Inactive        bool     `json:"inactive"`
	// PromotedNodeID is set when this candidate has already been promoted to
	// a dynamic topic tree node linked through SourceCandidateID. A
	// promoted candidate is removed from EmergingTopics tracking on the round
	// it is promoted, so seeing this set for a still-listed candidate is
	// unexpected but handled defensively.
	PromotedNodeID string `json:"promotedNodeId,omitempty"`
}

type treeAuditSnapshotBuild struct {
	Snapshot      treeAuditSnapshot
	Hash          string
	EvidenceRoles map[int64]treeAuditEvidenceRole
	InputJSON     json.RawMessage
}

func buildTreeAuditSnapshot(sessionID string, payload json.RawMessage, segments []domain.TranscriptSegment, mc *meetingContext, cfg TreeAuditConfig) (treeAuditSnapshotBuild, error) {
	cfg = cfg.normalized()
	state := previousLiveAnalysisState(payload)
	normalizeLegacyAgendaTopicIDs(&state, mc, nil)
	if state.Tree == nil || len(state.Tree.Nodes) == 0 || state.TreeVersion <= 0 {
		return treeAuditSnapshotBuild{}, fmt.Errorf("tree audit snapshot requires a non-empty versioned tree")
	}
	roles := classifyTreeAuditEvidence(state, segments)
	precheck := deterministicTreeAuditPrecheck(state, mc, roles, cfg)
	if len(precheck) > 100 {
		precheck = precheck[:100]
	}
	selected, compressed := selectTreeAuditNodes(state, precheck, cfg.MaxNodes)
	items := make(map[string]liveAnalysisItem, len(state.Items))
	for _, item := range state.Items {
		items[item.ID] = item
	}
	promotedTopicByCandidate := make(map[string]string)
	for _, node := range state.Tree.Nodes {
		if node.Kind == "topic" && node.Origin == topicOriginDynamic {
			candidateID := strings.TrimSpace(node.SourceCandidateID)
			if candidateID == "" && strings.HasPrefix(node.ID, "candidate-") {
				candidateID = node.ID
			}
			if candidateID != "" {
				promotedTopicByCandidate[candidateID] = node.ID
			}
		}
	}
	nodes := make([]treeAuditSnapshotNode, 0, len(selected))
	var agendaIDs, validParentIDs []string
	for _, node := range selected {
		item := items[node.ID]
		refs := make([]treeAuditEvidenceRef, 0, len(item.EvidenceSequenceNos))
		for _, sequenceNo := range item.EvidenceSequenceNos {
			refs = append(refs, treeAuditEvidenceRef{SequenceNo: sequenceNo, Role: roles[sequenceNo]})
		}
		nodes = append(nodes, treeAuditSnapshotNode{
			CanonicalNodeID: node.ID, NodeType: node.Kind, Title: node.Label,
			Description:              firstNonEmptyTrimmed(node.Description, item.Body),
			ParentCanonicalNodeID:    node.ParentID,
			Fixed:                    node.ID == treeRootNodeID,
			Dynamic:                  node.Origin == topicOriginDynamic,
			Tentative:                item.ClassificationStatus == classificationTentative,
			Status:                   firstNonEmptyTrimmed(node.Status, item.Status),
			Subtype:                  firstNonEmptyTrimmed(node.Subtype, item.Subtype),
			InformationStatus:        item.InformationStatus,
			CreatedAtVersion:         node.CreatedAtVersion,
			ClassificationConfidence: item.AssignmentConfidence,
			AssignmentReason:         item.AssignmentReason,
			CandidateTopicID:         item.CandidateTopicID,
			EvidenceSequenceNos:      append([]int64(nil), item.EvidenceSequenceNos...),
			EvidenceRoles:            refs,
			AgendaRefs:               append([]string(nil), node.AgendaRefs...),
			MergedFromNodeIDs:        append([]string(nil), node.MergedFromNodeIDs...),
			AgendaSplitGroupID:       node.AgendaSplitGroupID,
		})
		if (node.Kind == "topic" || node.Kind == "group") && node.ID != treeRootNodeID && node.AgendaRole != agendaRoleActionSummary {
			validParentIDs = append(validParentIDs, node.ID)
		}
	}
	// root is a valid move_node destination (it is not a valid move_item
	// destination; that is enforced independently in the move_item applier),
	// so it belongs in the advisory validParentCanonicalNodeIds list even
	// though the loop above deliberately excludes it from itself.
	validParentIDs = append(validParentIDs, treeRootNodeID)
	anchors := make([]treeAuditSnapshotAgendaAnchor, 0, len(state.AgendaAnchors))
	for _, anchor := range reconcileAgendaAnchors(state.AgendaAnchors, mc, state.Tree, state.Items, state.TreeVersion, false) {
		agendaIDs = append(agendaIDs, anchor.AgendaID)
		anchors = append(anchors, treeAuditSnapshotAgendaAnchor{AgendaID: anchor.AgendaID, OriginalTitle: anchor.OriginalTitle, Status: anchor.Status, MaterializedTopicIDs: append([]string(nil), anchor.MaterializedTopicIDs...)})
	}
	candidates := make([]treeAuditSnapshotCandidate, 0, len(state.EmergingTopics))
	for _, candidate := range state.EmergingTopics {
		snapshotCandidate := treeAuditSnapshotCandidate{
			ID: candidate.ID, Title: candidate.Label, Description: candidate.Description,
			EvidenceItemIDs: append([]string(nil), candidate.EvidenceItemIDs...),
			FirstVersion:    candidate.FirstRound, LastVersion: candidate.LastRound,
			RoundCount: candidate.RoundCount, Inactive: candidate.Inactive,
		}
		if promotedNodeID := promotedTopicByCandidate[candidate.ID]; promotedNodeID != "" {
			snapshotCandidate.PromotedNodeID = promotedNodeID
		}
		candidates = append(candidates, snapshotCandidate)
	}
	evidence, recent := selectTreeAuditTranscript(state, segments, roles, precheck, cfg)
	snapshot := treeAuditSnapshot{
		SessionID: sessionID, TreeVersion: state.TreeVersion,
		CoverageThroughSequenceNo: state.CoveredThroughSequenceNo,
		Nodes:                     nodes, Candidates: candidates, RecentTreeChanges: state.TreeChanges,
		AgendaIDs: agendaIDs, AgendaAnchors: anchors, ValidParentCanonicalNodeIDs: validParentIDs,
		PrecheckFindings: precheck, EvidenceSegments: evidence,
		RecentTranscript: recent, Compressed: compressed,
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return treeAuditSnapshotBuild{}, fmt.Errorf("marshal tree audit snapshot: %w", err)
	}
	maxChars := cfg.MaxInputTokens * 2
	if maxChars > 0 && len([]rune(string(encoded))) > maxChars {
		snapshot.Compressed = true
		for len(snapshot.RecentTranscript) > 4 && utf8.RuneCount(encoded) > maxChars {
			snapshot.RecentTranscript = snapshot.RecentTranscript[1:]
			encoded, _ = json.Marshal(snapshot)
		}
		for len(snapshot.EvidenceSegments) > 8 && utf8.RuneCount(encoded) > maxChars {
			snapshot.EvidenceSegments = snapshot.EvidenceSegments[:len(snapshot.EvidenceSegments)-1]
			encoded, _ = json.Marshal(snapshot)
		}
		for index := range snapshot.Nodes {
			snapshot.Nodes[index].Description = truncateRunes(snapshot.Nodes[index].Description, 120)
			snapshot.Nodes[index].AssignmentReason = truncateRunes(snapshot.Nodes[index].AssignmentReason, 80)
		}
		prioritizeTreeAuditSnapshotNodes(snapshot.Nodes, snapshot.PrecheckFindings)
		encoded, _ = json.Marshal(snapshot)
		for len(snapshot.Nodes) > 8 && utf8.RuneCount(encoded) > maxChars {
			snapshot.Nodes = snapshot.Nodes[:len(snapshot.Nodes)-1]
			encoded, _ = json.Marshal(snapshot)
		}
		for len(snapshot.PrecheckFindings) > 10 && utf8.RuneCount(encoded) > maxChars {
			snapshot.PrecheckFindings = snapshot.PrecheckFindings[:len(snapshot.PrecheckFindings)-1]
			encoded, _ = json.Marshal(snapshot)
		}
		if utf8.RuneCount(encoded) > maxChars {
			return treeAuditSnapshotBuild{}, fmt.Errorf("compressed tree audit snapshot exceeds max input tokens")
		}
	}
	sum := sha256.Sum256(encoded)
	return treeAuditSnapshotBuild{
		Snapshot: snapshot, Hash: hex.EncodeToString(sum[:]),
		EvidenceRoles: roles, InputJSON: encoded,
	}, nil
}

func prioritizeTreeAuditSnapshotNodes(nodes []treeAuditSnapshotNode, findings []treeAuditPrecheckFinding) {
	priority := make(map[string]int)
	for _, finding := range findings {
		for _, id := range append(append([]string(nil), finding.NodeIDs...), finding.RelatedNodeIDs...) {
			priority[id] += 100
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		if priority[nodes[i].CanonicalNodeID] != priority[nodes[j].CanonicalNodeID] {
			return priority[nodes[i].CanonicalNodeID] > priority[nodes[j].CanonicalNodeID]
		}
		containerI := nodes[i].Fixed || nodes[i].NodeType == "topic" || nodes[i].NodeType == "group"
		containerJ := nodes[j].Fixed || nodes[j].NodeType == "topic" || nodes[j].NodeType == "group"
		if containerI != containerJ {
			return containerI
		}
		return nodes[i].CanonicalNodeID < nodes[j].CanonicalNodeID
	})
}

func selectTreeAuditNodes(state liveAnalysisPayload, findings []treeAuditPrecheckFinding, max int) ([]liveAnalysisTreeNode, bool) {
	if state.Tree == nil || len(state.Tree.Nodes) <= max {
		if state.Tree == nil {
			return nil, false
		}
		return append([]liveAnalysisTreeNode(nil), state.Tree.Nodes...), false
	}
	priority := make(map[string]int)
	for _, finding := range findings {
		for _, id := range append(append([]string(nil), finding.NodeIDs...), finding.RelatedNodeIDs...) {
			priority[id] += 100
		}
	}
	if state.TreeChanges != nil {
		for _, id := range append(append(append([]string(nil), state.TreeChanges.NewNodeIDs...), state.TreeChanges.UpdatedNodeIDs...), state.TreeChanges.ReparentedNodeIDs...) {
			priority[id] += 50
		}
	}
	nodes := append([]liveAnalysisTreeNode(nil), state.Tree.Nodes...)
	sort.SliceStable(nodes, func(i, j int) bool {
		anchorLinkedI := nodes[i].ID == treeRootNodeID || len(nodes[i].AgendaRefs) > 0 || nodes[i].Origin == topicOriginAgenda
		anchorLinkedJ := nodes[j].ID == treeRootNodeID || len(nodes[j].AgendaRefs) > 0 || nodes[j].Origin == topicOriginAgenda
		if anchorLinkedI != anchorLinkedJ {
			return anchorLinkedI
		}
		if priority[nodes[i].ID] != priority[nodes[j].ID] {
			return priority[nodes[i].ID] > priority[nodes[j].ID]
		}
		return nodes[i].ID < nodes[j].ID
	})
	return nodes[:max], true
}

func deterministicTreeAuditPrecheck(state liveAnalysisPayload, mc *meetingContext, roles map[int64]treeAuditEvidenceRole, cfg TreeAuditConfig) []treeAuditPrecheckFinding {
	normalizeLegacyAgendaTopicIDs(&state, mc, nil)
	cfg = cfg.normalized()
	if state.Tree == nil {
		return nil
	}
	byID := make(map[string]liveAnalysisTreeNode, len(state.Tree.Nodes))
	items := make(map[string]liveAnalysisItem, len(state.Items))
	for _, node := range state.Tree.Nodes {
		byID[node.ID] = node
	}
	for _, item := range state.Items {
		items[item.ID] = item
	}
	containerText := func(id string) string {
		node := byID[id]
		text := node.Label + " " + node.Description
		seen := map[string]struct{}{}
		for node.ParentID != "" && node.ParentID != treeRootNodeID {
			if _, loop := seen[node.ParentID]; loop {
				break
			}
			seen[node.ParentID] = struct{}{}
			node = byID[node.ParentID]
			text += " " + node.Label + " " + node.Description
		}
		return text
	}
	topContainer := func(id string) string {
		current := byID[id]
		seen := map[string]struct{}{}
		for current.ParentID != "" && current.ParentID != treeRootNodeID {
			if _, loop := seen[current.ParentID]; loop {
				return ""
			}
			seen[current.ParentID] = struct{}{}
			current = byID[current.ParentID]
		}
		if current.ParentID == treeRootNodeID {
			return current.ID
		}
		return ""
	}
	var findings []treeAuditPrecheckFinding
	seenFinding := make(map[string]struct{})
	add := func(f treeAuditPrecheckFinding) {
		sort.Strings(f.NodeIDs)
		sort.Strings(f.RelatedNodeIDs)
		key := string(f.Type) + "\x00" + strings.Join(f.NodeIDs, ",") + "\x00" + strings.Join(f.RelatedNodeIDs, ",")
		if _, exists := seenFinding[key]; exists {
			return
		}
		seenFinding[key] = struct{}{}
		findings = append(findings, f)
	}

	containers := make([]liveAnalysisTreeNode, 0)
	childCounts := make(map[string]int, len(state.Tree.Nodes))
	for _, node := range state.Tree.Nodes {
		if node.ParentID != "" {
			childCounts[node.ParentID]++
		}
	}
	for _, node := range state.Tree.Nodes {
		if node.Kind == "topic" && node.ID != treeRootNodeID && node.ID != treeUnclassifiedTopicID {
			containers = append(containers, node)
		}
		if node.Kind == "topic" && node.ID != treeRootNodeID && childCounts[node.ID] > 0 && genericTopicLabel(node.Label) {
			add(treeAuditPrecheckFinding{Type: TreeAuditGenericTopicLabel, NodeIDs: []string{node.ID}, Reason: "topic label does not identify the concrete child subject", Score: 1})
			add(treeAuditPrecheckFinding{Type: TreeAuditTopicLabelNotDerivedFromChildren, NodeIDs: []string{node.ID}, Reason: "generic topic label is not derived from its active children", Score: 1})
			if childCounts[node.ID] == 1 {
				add(treeAuditPrecheckFinding{Type: TreeAuditSingleChildGenericTopic, NodeIDs: []string{node.ID}, Reason: "single-child topic uses a staging label instead of the child subject", Score: 1})
			}
		}
		if childCounts[node.ID] == 0 && treeAuditRemovableEmptyContainerKind(node) {
			findingType := TreeAuditEmptyGroup
			if node.ID == treeUnclassifiedTopicID {
				findingType = TreeAuditEmptyUnclassifiedContainer
			}
			add(treeAuditPrecheckFinding{Type: findingType, NodeIDs: []string{node.ID}, Reason: "container has no active child", Score: 1})
		}
	}
	// Agenda lifecycle findings are computed from canonical records/references,
	// not from title prefixes. They are included even when the model later
	// returns no operations, making final-review gaps observable.
	agendaIntegrity := validateTreeIntegrity(state.Tree, state.Items, mc)
	for _, topicID := range agendaIntegrity.EmptyAgendaTopicIDs {
		add(treeAuditPrecheckFinding{Type: TreeAuditPlannedAgendaWithoutEvidence, NodeIDs: []string{topicID}, Reason: "agenda topic exists without grounded child evidence", Score: 1})
		add(treeAuditPrecheckFinding{Type: TreeAuditEmptyAgendaTopic, NodeIDs: []string{topicID}, Reason: "materialized agenda topic has no active child", Score: 1})
		add(treeAuditPrecheckFinding{Type: TreeAuditAgendaTopicShouldDematerialize, NodeIDs: []string{topicID}, Reason: "empty agenda topic should return to its logical planned record", Score: 1})
	}
	for _, agendaID := range agendaIntegrity.DuplicateAgendaMaterializations {
		add(treeAuditPrecheckFinding{Type: TreeAuditDuplicateAgendaMaterialization, RelatedNodeIDs: []string{agendaID}, Reason: "one agenda record is materialized by multiple topics", Score: 1})
	}
	records := agendaRecordMap(mc)
	materializedByAgenda := make(map[string]bool, len(records))
	for _, node := range state.Tree.Nodes {
		if node.Kind != "topic" {
			continue
		}
		refs := topicAgendaRefs(node, records)
		for _, agendaID := range refs {
			materializedByAgenda[agendaID] = true
		}
		if node.Origin == topicOriginAgenda && len(refs) == 0 {
			add(treeAuditPrecheckFinding{Type: TreeAuditTopicWithoutActiveAgendaRef, NodeIDs: []string{node.ID}, Reason: "agenda-origin topic has no active agenda reference", Score: 1})
		}
	}
	// Inspect the persisted lifecycle state before reconciliation. Reconciliation
	// intentionally derives the current status from the tree and would otherwise
	// turn a corrupt "discussed but missing topic" record back into planned,
	// hiding exactly the inconsistency the audit is expected to report.
	for _, anchor := range state.AgendaAnchors {
		record, exists := records[anchor.AgendaID]
		if !exists || effectiveAgendaRole(record.Role, record.Title, "") == agendaRoleActionSummary {
			continue
		}
		if anchor.Status == agendaStatusDiscussed && !materializedByAgenda[anchor.AgendaID] {
			add(treeAuditPrecheckFinding{Type: TreeAuditDiscussedAgendaMissingTopic, RelatedNodeIDs: []string{anchor.AgendaID}, Reason: "persisted discussed agenda record has no materialized topic", Score: 1})
		}
	}
	anchors := reconcileAgendaAnchors(state.AgendaAnchors, mc, state.Tree, state.Items, state.TreeVersion, false)
	for _, anchor := range anchors {
		if anchor.Status == agendaStatusDiscussed && len(anchor.MaterializedTopicIDs) == 0 {
			add(treeAuditPrecheckFinding{Type: TreeAuditDiscussedAgendaMissingTopic, RelatedNodeIDs: []string{anchor.AgendaID}, Reason: "discussed agenda record has no materialized topic", Score: 1})
		}
	}
	for _, agendaTopic := range containers {
		if len(topicAgendaRefs(agendaTopic, records)) == 0 {
			continue
		}
		for _, dynamicTopic := range containers {
			if dynamicTopic.ID == agendaTopic.ID || dynamicTopic.Origin != topicOriginDynamic {
				continue
			}
			if semanticItemSimilarity(agendaTopic.Label+" "+agendaTopic.Description, dynamicTopic.Label+" "+dynamicTopic.Description) >= 0.72 {
				add(treeAuditPrecheckFinding{Type: TreeAuditAgendaTopicShouldMergeDynamic, NodeIDs: []string{agendaTopic.ID, dynamicTopic.ID}, Reason: "agenda and dynamic topics represent the same concrete discussion", Score: 0.9})
			}
		}
	}
	// Action Summary is a reference-only projection. When no source agenda was
	// planned, the virtual projection ID still has to be attached to active,
	// canonical TODOs; no duplicate item or tree node is created.
	if mc != nil && len(mc.actionSummaryAgendaIDs()) == 0 {
		missing := make([]string, 0)
		for _, item := range state.Items {
			if item.Kind != "todo" || item.Status == "resolved" || item.Inactive || item.CandidateInactive ||
				item.ClassificationStatus == classificationTentative || item.ClassificationStatus == classificationUnclassified {
				continue
			}
			if !containsExactString(item.RelatedAgendaIDs, virtualActionSummaryProjectionID) {
				missing = append(missing, item.ID)
			}
		}
		if len(missing) > 0 {
			add(treeAuditPrecheckFinding{Type: TreeAuditActionSummaryMissingActiveTodos, NodeIDs: missing, Reason: "active TODOs are absent from the reference-only Action Summary fallback", Score: 1})
		}
	}
	forcedNoAgenda := make([]string, 0)
	for _, node := range state.Tree.Nodes {
		item, detail := items[node.ID]
		if !detail {
			continue
		}
		itemText := item.Title + " " + item.Body
		parent := byID[node.ParentID]
		// Agenda topics intentionally use the best concrete child as their
		// materialized display label. The redundant parent/child defect applies
		// to semantic groups, such as a generic question group containing an
		// identically named question item, not to that agenda projection rule.
		sameAsGroup := parent.Kind == "group" && normalizeForMatch(parent.Label) != "" && normalizeForMatch(parent.Label) == normalizeForMatch(node.Label)
		lowInformationChild := issueTextNeedsReferent(item.Title) || metaOnlyLiveItemText(item.Title+" "+item.Body)
		if sameAsGroup && lowInformationChild {
			add(treeAuditPrecheckFinding{Type: TreeAuditParentChildSameTitle, NodeIDs: []string{node.ID}, RelatedNodeIDs: []string{parent.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "child repeats its parent label without an independently distinguishable title", Score: 1})
			add(treeAuditPrecheckFinding{Type: TreeAuditLowInformationChild, NodeIDs: []string{node.ID}, RelatedNodeIDs: []string{parent.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "same-title child adds no concrete subject or proposition", Score: 1})
		}
		if item.Kind == "issue" && issueTextNeedsReferent(item.Title) {
			add(treeAuditPrecheckFinding{Type: TreeAuditGenericQuestionWithoutSubject, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "question title has no explicit target and requires evidence-grounded rewrite", Score: 1})
		}
		if parent.Kind == "topic" && (strings.TrimSpace(item.Body) == "" || semanticItemSimilarity(item.Title, item.Body) >= 0.88) {
			for _, agendaID := range topicAgendaRefs(parent, records) {
				record := records[agendaID]
				if normalizeForMatch(record.Title) == normalizeForMatch(item.Title) {
					add(treeAuditPrecheckFinding{Type: TreeAuditAgendaTitleCopiedAsItem, NodeIDs: []string{node.ID}, RelatedNodeIDs: []string{parent.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "agenda record title was copied into a child item without additional information", Score: 1})
					break
				}
			}
		}
		if item.Kind == "decision" && isMeetingEndOnlyItem(item.Title, item.Body) {
			add(treeAuditPrecheckFinding{Type: TreeAuditMeetingEndAsDecision, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "meeting closure was persisted as a business decision", Score: 1})
		}
		if item.AssignmentSource == assignmentSourceNoAgendaSpan {
			bestAgendaID, bestScore := "", 0.0
			for _, candidate := range containers {
				if len(topicAgendaRefs(candidate, records)) == 0 {
					continue
				}
				score := semanticItemSimilarity(itemText, candidate.Label+" "+candidate.Description)
				if score > bestScore {
					bestAgendaID, bestScore = candidate.ID, score
				}
			}
			if bestAgendaID != "" && bestScore >= 0.30 {
				forcedNoAgenda = append(forcedNoAgenda, node.ID)
				add(treeAuditPrecheckFinding{Type: TreeAuditAgendaItemForcedNoAgenda, NodeIDs: []string{node.ID}, RelatedNodeIDs: []string{bestAgendaID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "no-agenda assignment conflicts with a materially stronger canonical agenda subject", Score: bestScore})
				add(treeAuditPrecheckFinding{Type: TreeAuditAgendaReentryMissed, NodeIDs: []string{node.ID}, RelatedNodeIDs: []string{bestAgendaID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "agenda-aligned evidence remained under a no-agenda assignment after reentry", Score: bestScore})
				add(treeAuditPrecheckFinding{Type: TreeAuditNoAgendaFalsePositiveFromModifier, NodeIDs: []string{node.ID}, RelatedNodeIDs: []string{bestAgendaID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "no-agenda classification lacks an explicit topic transition and conflicts with the agenda subject; a modifier phrase must not open a new topic", Score: bestScore})
				if item.Kind == "todo" && topContainer(node.ID) == treeUnclassifiedTopicID {
					add(treeAuditPrecheckFinding{Type: TreeAuditUnclassifiedTodoAfterReentry, NodeIDs: []string{node.ID}, RelatedNodeIDs: []string{bestAgendaID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "active agenda TODO remained unclassified after semantic reentry", Score: bestScore})
				}
			}
		}
		if lowInformationDecisionItem(item) {
			add(treeAuditPrecheckFinding{Type: TreeAuditLowInformationDecision, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "decision predicate has no recoverable subject", Score: 1})
		}
		if item.Kind == "decision" {
			normalizedDecision := normalizeDiscourseText(item.Title + " " + item.Body)
			if leadingParticlePattern.MatchString(normalizedDecision) {
				add(treeAuditPrecheckFinding{Type: TreeAuditLeadingParticleFragment, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "decision begins with a particle and depends on a missing preceding STT fragment", Score: 1})
				add(treeAuditPrecheckFinding{Type: TreeAuditIncompleteSTTSegmentItem, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "an incomplete STT segment was persisted as an independent decision item", Score: 1})
				add(treeAuditPrecheckFinding{Type: TreeAuditDecisionMissingObject, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "decision predicate has no explicit business object", Score: 1})
			}
			if leadingAnaphoraPattern.MatchString(normalizedDecision) {
				add(treeAuditPrecheckFinding{Type: TreeAuditAnaphoraTargetMissing, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "decision uses an anaphoric target without an independently named referent", Score: 1})
				add(treeAuditPrecheckFinding{Type: TreeAuditDecisionMissingObject, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "decision predicate has no explicit business object", Score: 1})
			}
		}
		if isDiscourseOnlyItem(item.Title, item.Body) {
			add(treeAuditPrecheckFinding{Type: TreeAuditDiscourseOnlyItem, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "meeting-control speech was persisted as a discussion item", Score: 1})
			add(treeAuditPrecheckFinding{Type: TreeAuditMetaUtteranceNode, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "meeting-control utterance was promoted into a semantic node", Score: 1})
		}
		if treeAuditLowInformationItem(item, nil, roles) {
			add(treeAuditPrecheckFinding{Type: TreeAuditLowInformationItem, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "item has no independently actionable or factual proposition", Score: 1})
			add(treeAuditPrecheckFinding{Type: TreeAuditLowInformationTitle, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "title does not identify an independently understandable subject and proposition", Score: 1})
		}
		if issueTextNeedsReferent(item.Title+" "+item.Body) && issueAnaphoraPattern.MatchString(item.Title+" "+item.Body) {
			add(treeAuditPrecheckFinding{Type: TreeAuditAnaphoraWithoutReferent, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "anaphoric item text does not name its referent", Score: 1})
		} else if item.Kind == "issue" && metaOnlyLiveItemText(item.Title+" "+item.Body) {
			add(treeAuditPrecheckFinding{Type: TreeAuditStatusOnlyNode, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "node expresses only discussion state without a concrete subject", Score: 1})
		}
		if item.Kind == "issue" && len(splitCollapsedOpenIssueStatement(item.Title+" "+item.Body)) > 1 {
			add(treeAuditPrecheckFinding{Type: TreeAuditMultiplePropositionsCollapsed, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "multiple independent issue propositions were collapsed into one node", Score: 1})
		}
		if item.Kind == "issue" {
			inferred := inferIssueSubtype(item.Title+" "+item.Body, item.Subtype)
			if inferred != item.Subtype && validIssueSubtype(inferred) {
				add(treeAuditPrecheckFinding{Type: TreeAuditSubtypeMismatch, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "issue subtype conflicts with the proposition wording", Score: 0.85})
			}
		}
		kindDecision := evaluateLiveItemKind(item, liveEvidenceScope{}, "tree_audit_precheck")
		if kindDecision.CanonicalKind != item.Kind &&
			kindDecision.Confidence >= itemKindValidationThreshold(itemKindValidationAudit) {
			add(treeAuditPrecheckFinding{
				Type: TreeAuditSemanticKindMismatch, NodeIDs: []string{node.ID},
				EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...),
				Reason:              kindDecision.Reason, Score: kindDecision.Confidence,
			})
		}
		if allTreeAuditEvidenceReference(item.EvidenceSequenceNos, roles) {
			add(treeAuditPrecheckFinding{Type: TreeAuditRecapReferenceContamination, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "independent item is grounded only in recap/reference evidence", Score: 0.95})
			add(treeAuditPrecheckFinding{Type: TreeAuditRecapOnlyItem, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "item has no primary evidence outside recap or discourse control", Score: 0.95})
		}
		currentScore := semanticItemSimilarity(itemText, containerText(node.ParentID))
		bestID, bestScore := "", currentScore
		for _, candidate := range containers {
			if candidate.ID == topContainer(node.ID) {
				continue
			}
			score := semanticItemSimilarity(itemText, candidate.Label+" "+candidate.Description)
			if score > bestScore {
				bestID, bestScore = candidate.ID, score
			}
		}
		bestText := ""
		if bestID != "" {
			best := byID[bestID]
			bestText = best.Label + " " + best.Description
		}
		clearAlternative := bestScore-currentScore >= cfg.RequiredImprovementMargin ||
			(currentScore < cfg.CohesionThreshold && bestScore > currentScore && sharedTreeAuditSubjectTerm(itemText, bestText) && !sharedTreeAuditSubjectTerm(itemText, containerText(node.ParentID)))
		if bestID != "" && clearAlternative && (currentScore < cfg.CohesionThreshold || bestScore-currentScore >= 0.08) {
			typeName := TreeAuditSubjectMismatch
			currentTop := byID[topContainer(node.ID)]
			if currentTop.Origin == topicOriginAgenda || inferredAgendaForTopic(currentTop, mc) != "" {
				typeName = TreeAuditCrossAgendaContamination
			}
			add(treeAuditPrecheckFinding{Type: typeName, NodeIDs: []string{node.ID}, RelatedNodeIDs: []string{bestID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "current parent has low subject cohesion and another canonical topic is materially closer", Score: bestScore - currentScore})
			if typeName == TreeAuditCrossAgendaContamination {
				add(treeAuditPrecheckFinding{Type: TreeAuditSubjectMismatch, NodeIDs: []string{node.ID}, RelatedNodeIDs: []string{bestID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "detail subject does not match its current agenda family", Score: bestScore})
			}
		}
		if item.AssignmentConfidence > 0 && item.AssignmentConfidence < cfg.normalized().HighConfidenceThreshold {
			add(treeAuditPrecheckFinding{Type: TreeAuditParentLowConfidence, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "server classification confidence is below audit high-confidence threshold", Score: 1 - item.AssignmentConfidence})
		}
		if state.TreeChanges != nil && containsExactString(state.TreeChanges.ReparentedNodeIDs, node.ID) && allTreeAuditEvidenceReference(item.EvidenceSequenceNos, roles) {
			add(treeAuditPrecheckFinding{Type: TreeAuditReferenceEvidenceReparent, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "the latest parent change is supported only by reference or recap evidence", Score: 1})
		}
	}
	if len(forcedNoAgenda) >= 2 {
		add(treeAuditPrecheckFinding{Type: TreeAuditStaleNoAgendaSpan, NodeIDs: forcedNoAgenda, Reason: "multiple agenda-aligned items inherited the same stale no-agenda context", Score: 1})
	}

	for _, candidate := range state.EmergingTopics {
		if genericTopicLabel(candidate.Label) {
			add(treeAuditPrecheckFinding{Type: TreeAuditGenericCandidateLabel, NodeIDs: append([]string(nil), candidate.EvidenceItemIDs...), RelatedNodeIDs: []string{candidate.ID}, Reason: "candidate label is a staging phrase rather than its evidence subject", Score: 1})
		}
		var evidenceTexts []string
		for _, id := range candidate.EvidenceItemIDs {
			if item, ok := items[id]; ok {
				evidenceTexts = append(evidenceTexts, item.Title+" "+item.Body)
				candidateScore := semanticItemSimilarity(candidate.Label+" "+candidate.Description, item.Title+" "+item.Body)
				if candidateScore < cfg.CohesionThreshold || (candidateScore < 0.35 && !sharedTreeAuditSubjectTerm(candidate.Label+" "+candidate.Description, item.Title+" "+item.Body)) {
					add(treeAuditPrecheckFinding{Type: TreeAuditCandidateMixedSubjects, NodeIDs: []string{id}, RelatedNodeIDs: []string{candidate.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "candidate subject and evidence item subject have low cohesion", Score: 1 - candidateScore})
					bestTopic, bestScore := "", candidateScore
					for _, topic := range containers {
						score := semanticItemSimilarity(item.Title+" "+item.Body, topic.Label+" "+topic.Description)
						if score > bestScore {
							bestTopic, bestScore = topic.ID, score
						}
					}
					if bestTopic != "" && bestScore-candidateScore >= cfg.RequiredImprovementMargin {
						add(treeAuditPrecheckFinding{Type: TreeAuditCandidateShouldFoldIntoTopic, NodeIDs: []string{id}, RelatedNodeIDs: []string{candidate.ID, bestTopic}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "candidate evidence is materially more cohesive with an existing canonical topic", Score: bestScore - candidateScore})
					}
				}
			}
		}
		if candidate.RoundCount <= 1 && len(candidate.EvidenceItemIDs) <= 3 {
			add(treeAuditPrecheckFinding{Type: TreeAuditFloatingTentativeCandidate, NodeIDs: append([]string(nil), candidate.EvidenceItemIDs...), RelatedNodeIDs: []string{candidate.ID}, Reason: "single-round tentative candidate requires semantic review", Score: 0.7})
		}
		if candidate.Inactive || (candidate.FirstRound > 0 && state.TreeVersion-candidate.FirstRound >= cfg.TentativeMaxVersions) {
			add(treeAuditPrecheckFinding{Type: TreeAuditStaleTentative, NodeIDs: append([]string(nil), candidate.EvidenceItemIDs...), RelatedNodeIDs: []string{candidate.ID}, Reason: "tentative candidate has remained without promotion beyond the configured version window", Score: 0.8})
		}
		if candidate.RoundCount >= 2 && len(candidate.EvidenceItemIDs) >= 2 && candidateSubjectIncoherenceReason(candidate, func(id string) *liveAnalysisItem {
			item, exists := items[id]
			if !exists {
				return nil
			}
			return &item
		}, TreeClassificationConfig{}) == "" {
			add(treeAuditPrecheckFinding{Type: TreeAuditMissingRequiredTopic, NodeIDs: append([]string(nil), candidate.EvidenceItemIDs...), RelatedNodeIDs: []string{candidate.ID}, Reason: "multi-round coherent candidate has no promoted topic", Score: 0.85})
		}
		_ = evidenceTexts
	}

	// Similar detail subjects split across different top-level containers are
	// candidate-fragmentation hints, not automatic merge instructions.
	for i := 0; i < len(state.Items); i++ {
		leftTop := topContainer(state.Items[i].ID)
		if leftTop == "" {
			continue
		}
		for j := i + 1; j < len(state.Items); j++ {
			rightTop := topContainer(state.Items[j].ID)
			if leftTop == rightTop {
				if matched, score := sameKindSemanticDuplicate(state.Items[i], state.Items[j]); matched {
					add(treeAuditPrecheckFinding{Type: TreeAuditSemanticDuplicateSibling, NodeIDs: []string{state.Items[i].ID, state.Items[j].ID}, RelatedNodeIDs: []string{leftTop}, EvidenceSequenceNos: append(append([]int64(nil), state.Items[i].EvidenceSequenceNos...), state.Items[j].EvidenceSequenceNos...), Reason: "same-kind sibling nodes represent the same proposition", Score: score})
					add(treeAuditPrecheckFinding{Type: TreeAuditDuplicateItem, NodeIDs: []string{state.Items[i].ID, state.Items[j].ID}, RelatedNodeIDs: []string{leftTop}, EvidenceSequenceNos: append(append([]int64(nil), state.Items[i].EvidenceSequenceNos...), state.Items[j].EvidenceSequenceNos...), Reason: "active sibling items duplicate one proposition", Score: score})
					add(treeAuditPrecheckFinding{Type: TreeAuditDuplicateOrParaphrase, NodeIDs: []string{state.Items[i].ID, state.Items[j].ID}, RelatedNodeIDs: []string{leftTop}, EvidenceSequenceNos: append(append([]int64(nil), state.Items[i].EvidenceSequenceNos...), state.Items[j].EvidenceSequenceNos...), Reason: "sibling nodes duplicate or paraphrase one proposition", Score: score})
				} else if sameCanonicalProposition(state.Items[i], state.Items[j]) {
					add(treeAuditPrecheckFinding{Type: TreeAuditDuplicateCrossKindProposition, NodeIDs: []string{state.Items[i].ID, state.Items[j].ID}, RelatedNodeIDs: []string{leftTop}, EvidenceSequenceNos: append(append([]int64(nil), state.Items[i].EvidenceSequenceNos...), state.Items[j].EvidenceSequenceNos...), Reason: "cross-kind sibling nodes represent one canonical proposition", Score: 0.9})
				}
			}
			if rightTop == "" || leftTop == rightTop {
				continue
			}
			actionRelation := ((state.Items[i].Kind == "risk" || state.Items[i].Kind == "issue") && (state.Items[j].Kind == "todo" || state.Items[j].Kind == "decision")) ||
				((state.Items[j].Kind == "risk" || state.Items[j].Kind == "issue") && (state.Items[i].Kind == "todo" || state.Items[i].Kind == "decision"))
			if actionRelation && itemEvidenceWithin(state.Items[i], state.Items[j], 3) &&
				specificSubjectOverlapLength(state.Items[i].Title+" "+state.Items[i].Body, state.Items[j].Title+" "+state.Items[j].Body) >= 4 {
				evidence := append(append([]int64(nil), state.Items[i].EvidenceSequenceNos...), state.Items[j].EvidenceSequenceNos...)
				add(treeAuditPrecheckFinding{Type: TreeAuditRiskTodoSubjectFragmentation, NodeIDs: []string{state.Items[i].ID, state.Items[j].ID}, RelatedNodeIDs: []string{leftTop, rightTop}, EvidenceSequenceNos: evidence, Reason: "nearby risk/issue and action evidence for one concrete business object is split across topics", Score: 1})
				add(treeAuditPrecheckFinding{Type: TreeAuditRelatedActionOutsideRiskTopic, NodeIDs: []string{state.Items[i].ID, state.Items[j].ID}, RelatedNodeIDs: []string{leftTop, rightTop}, EvidenceSequenceNos: evidence, Reason: "action for a nearby risk/issue is outside the risk subject topic", Score: 1})
			}
			score := semanticItemSimilarity(state.Items[i].Title+" "+state.Items[i].Body, state.Items[j].Title+" "+state.Items[j].Body)
			if score >= 0.42 || sharedTreeAuditSubjectTerm(state.Items[i].Title+" "+state.Items[i].Body, state.Items[j].Title+" "+state.Items[j].Body) && score >= 0.12 {
				add(treeAuditPrecheckFinding{Type: TreeAuditCandidateFragmentation, NodeIDs: []string{state.Items[i].ID, state.Items[j].ID}, RelatedNodeIDs: []string{leftTop, rightTop}, EvidenceSequenceNos: append(append([]int64(nil), state.Items[i].EvidenceSequenceNos...), state.Items[j].EvidenceSequenceNos...), Reason: "semantically similar evidence is split across top-level subjects", Score: score})
			}
		}
	}

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Score != findings[j].Score {
			return findings[i].Score > findings[j].Score
		}
		return string(findings[i].Type) < string(findings[j].Type)
	})
	return findings
}

func inferredAgendaForTopic(topic liveAnalysisTreeNode, mc *meetingContext) string {
	if mc == nil || topic.ID == "" {
		return ""
	}
	if refs := topicAgendaRefs(topic, agendaRecordMap(mc)); len(refs) > 0 {
		return refs[0]
	}
	bestID, best := "", 0.0
	for _, agenda := range mc.Agenda {
		if agenda.Role == agendaRoleActionSummary {
			continue
		}
		score := semanticItemSimilarity(topic.Label+" "+topic.Description, agenda.Title)
		if score > best {
			bestID, best = agenda.ID, score
		}
	}
	if best >= 0.15 {
		return bestID
	}
	return ""
}

func classifyTreeAuditEvidence(state liveAnalysisPayload, segments []domain.TranscriptSegment) map[int64]treeAuditEvidenceRole {
	roles := make(map[int64]treeAuditEvidenceRole)
	segmentBySequence := make(map[int64]domain.TranscriptSegment, len(segments))
	scope := liveEvidenceScope{Allowed: make(map[int64]struct{}), CurrentRound: make(map[int64]struct{}), TranscriptText: make(map[int64]string), Segments: make(map[int64]domain.TranscriptSegment)}
	for _, segment := range segments {
		segmentBySequence[segment.SequenceNo] = segment
		roles[segment.SequenceNo] = treeAuditEvidenceSupporting
		scope.Allowed[segment.SequenceNo] = struct{}{}
		scope.TranscriptText[segment.SequenceNo] = segment.Text
		scope.Segments[segment.SequenceNo] = segment
		if segment.SequenceNo > scope.CoveredThrough {
			scope.CoveredThrough = segment.SequenceNo
		}
	}
	timeline := classifyDiscourseTimeline(scope)
	for sequenceNo, role := range timeline.Roles {
		switch role {
		case liveEvidenceReferenceRecap, liveEvidenceDiscourseOnly:
			roles[sequenceNo] = treeAuditEvidenceReference
		case liveEvidencePrimary, liveEvidenceCorrection:
			roles[sequenceNo] = treeAuditEvidencePrimary
		}
	}
	// persistedPrimary holds every sequence a previous round already stamped
	// primary/correction on some item's own EvidenceRoles (stampEvidenceRoles).
	// H1: when the deterministic timeline above (not just this heuristic's own
	// prior iteration) agrees a sequence is primary/correction, the
	// looksLikeTreeAuditReference heuristic below must not downgrade it. Left
	// unguarded, a primary utterance that also happens to resemble its own
	// freshly-extracted item/topic (or contains a generic status-review word
	// such as "確認") gets misjudged as a reference to *something else* and
	// wrongly demoted, which then makes a correct audit move_item repair get
	// rejected as reference_evidence_only (see the report).
	persistedPrimary := make(map[int64]struct{})
	for _, item := range state.Items {
		for _, ref := range item.EvidenceRoles {
			if ref.Role == liveEvidencePrimary || ref.Role == liveEvidenceCorrection {
				persistedPrimary[ref.SequenceNo] = struct{}{}
			}
		}
	}
	for _, item := range state.Items {
		if len(item.EvidenceSequenceNos) == 0 {
			continue
		}
		primary := item.EvidenceSequenceNos[0]
		if _, protected := persistedPrimary[primary]; protected && roles[primary] == treeAuditEvidencePrimary {
			// already primary by both the persisted record and the
			// deterministic timeline; keep it.
		} else if roles[primary] == treeAuditEvidenceReference || looksLikeTreeAuditReference(segmentBySequence[primary].Text, state, primary) {
			roles[primary] = treeAuditEvidenceReference
		} else {
			roles[primary] = treeAuditEvidencePrimary
		}
		for _, sequenceNo := range item.EvidenceSequenceNos[1:] {
			if _, protected := persistedPrimary[sequenceNo]; protected && roles[sequenceNo] == treeAuditEvidencePrimary {
				continue
			}
			if roles[sequenceNo] == treeAuditEvidenceReference || looksLikeTreeAuditReference(segmentBySequence[sequenceNo].Text, state, sequenceNo) {
				roles[sequenceNo] = treeAuditEvidenceReference
			}
		}
	}
	// A server-owned reference_recap downgrade (change_evidence_role applier,
	// ai_tree_audit_validator.go) takes priority over every heuristic above:
	// it is the audit's own explicit, previously-validated correction and
	// must stick across snapshots until something else revises it.
	for _, item := range state.Items {
		for _, ref := range item.EvidenceRoles {
			if ref.Role == liveEvidenceReferenceRecap {
				roles[ref.SequenceNo] = treeAuditEvidenceReference
			}
		}
	}
	return roles
}

// looksLikeTreeAuditReference judges whether text (the utterance behind one
// evidence sequenceNo) reads like a reference back to already-recorded
// content rather than the primary utterance that produced it. matchedItems
// deliberately excludes any item whose own EvidenceSequenceNos already
// include sequenceNo: an utterance always resembles the very item it was
// just extracted from, and that self-similarity is not evidence that the
// utterance is *referencing* some other, already-existing item (H1).
func looksLikeTreeAuditReference(text string, state liveAnalysisPayload, sequenceNo int64) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	matchedItems := 0
	for _, item := range state.Items {
		if containsInt64(item.EvidenceSequenceNos, sequenceNo) {
			continue
		}
		if semanticItemSimilarity(text, item.Title+" "+item.Body) >= 0.48 {
			matchedItems++
		}
	}
	matchedTopics := 0
	if state.Tree != nil {
		for _, node := range state.Tree.Nodes {
			if node.Kind == "topic" && node.ID != treeRootNodeID && semanticItemSimilarity(text, node.Label+" "+node.Description) >= 0.22 {
				matchedTopics++
			}
		}
	}
	statusReview := strings.Contains(text, "未解決") || strings.Contains(text, "解決済") || strings.Contains(text, "決定事項") || strings.Contains(text, "確認")
	explicitRecap := strings.Contains(text, "まとめ") || strings.Contains(text, "振り返") || strings.Contains(text, "以上")
	return explicitRecap || (matchedTopics >= 2 && matchedItems >= 1) || (statusReview && (matchedItems >= 1 || matchedTopics >= 1 || strings.Contains(text, "課題は")))
}

func sharedTreeAuditSubjectTerm(a, b string) bool {
	aKey, bKey := []rune(semanticItemKey(a)), []rune(semanticItemKey(b))
	if len(aKey) < 2 || len(bKey) < 2 {
		return false
	}
	ignored := map[string]struct{}{
		"調査": {}, "検討": {}, "決定": {}, "課題": {}, "実施": {}, "予定": {},
		"確認": {}, "対応": {}, "結果": {}, "説明": {}, "資料": {}, "方法": {},
	}
	for size := 4; size >= 2; size-- {
		if len(aKey) < size || len(bKey) < size {
			continue
		}
		for i := 0; i+size <= len(aKey); i++ {
			term := string(aKey[i : i+size])
			if _, skip := ignored[term]; skip {
				continue
			}
			if strings.Contains(string(bKey), term) {
				return true
			}
		}
	}
	return false
}

func allTreeAuditEvidenceReference(sequenceNos []int64, roles map[int64]treeAuditEvidenceRole) bool {
	if len(sequenceNos) == 0 {
		return false
	}
	for _, sequenceNo := range sequenceNos {
		if roles[sequenceNo] != treeAuditEvidenceReference {
			return false
		}
	}
	return true
}

func selectTreeAuditTranscript(state liveAnalysisPayload, segments []domain.TranscriptSegment, roles map[int64]treeAuditEvidenceRole, findings []treeAuditPrecheckFinding, cfg TreeAuditConfig) ([]treeAuditEvidenceSegment, []treeAuditEvidenceSegment) {
	bySequence := make(map[int64]domain.TranscriptSegment, len(segments))
	var ordered []int64
	for _, segment := range segments {
		if segment.SequenceNo <= 0 || strings.TrimSpace(segment.Text) == "" {
			continue
		}
		bySequence[segment.SequenceNo] = segment
		ordered = append(ordered, segment.SequenceNo)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	wanted := make(map[int64]struct{})
	for _, finding := range findings {
		for _, sequenceNo := range finding.EvidenceSequenceNos {
			for offset := int64(-2); offset <= 2; offset++ {
				if _, ok := bySequence[sequenceNo+offset]; ok {
					wanted[sequenceNo+offset] = struct{}{}
				}
			}
		}
	}
	evidence := make([]treeAuditEvidenceSegment, 0, len(wanted))
	for _, sequenceNo := range ordered {
		if _, ok := wanted[sequenceNo]; !ok {
			continue
		}
		segment := bySequence[sequenceNo]
		evidence = append(evidence, treeAuditEvidenceSegment{SequenceNo: sequenceNo, Speaker: segment.SpeakerName, Text: truncateRunes(segment.Text, 500), Role: roles[sequenceNo]})
		if len(evidence) >= cfg.MaxEvidenceSegments {
			break
		}
	}
	start := 0
	if len(ordered) > cfg.MaxRecentSegments {
		start = len(ordered) - cfg.MaxRecentSegments
	}
	recent := make([]treeAuditEvidenceSegment, 0, len(ordered)-start)
	for _, sequenceNo := range ordered[start:] {
		segment := bySequence[sequenceNo]
		recent = append(recent, treeAuditEvidenceSegment{SequenceNo: sequenceNo, Speaker: segment.SpeakerName, Text: truncateRunes(segment.Text, 500), Role: roles[sequenceNo]})
	}
	return evidence, recent
}
