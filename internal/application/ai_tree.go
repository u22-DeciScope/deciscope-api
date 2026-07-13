package application

import (
	"fmt"
	"strings"
)

// このファイルは議論ツリーの canonical な構造管理を持つ。
//
// 方針:
//   - ツリー表示用の親は各ノードにつき一つ(node.ParentID)。エッジは親から
//     導出されるビューであり、和集合で蓄積しない。
//   - AIは「ノード候補」と「親topicの割当(assignment)」だけを提案し、実際の
//     親エッジは必ずこのファイルの enforce 処理を通ってから保存される。
//   - 構造は root(1つ) → topic(親はrootのみ) → 詳細ノード(issue/question/
//     risk/decision、親はtopicのみ) に固定する。この形は構成上循環・型逆転・
//     複数親が発生し得ない。
//   - 分類できない詳細ノードは「最新topic」ではなく専用の未分類topic
//     (topic-unclassified)へ接続する。

// treeAssignment is a model-proposed parent assignment for one node.
type treeAssignment struct {
	NodeID        string  `json:"nodeId"`
	ItemID        string  `json:"itemId"` // alias: some models answer itemId
	ParentTopicID string  `json:"parentTopicId"`
	Confidence    float64 `json:"confidence"`
	Reason        string  `json:"reason"`
}

func (a treeAssignment) nodeID() string {
	if id := strings.TrimSpace(a.NodeID); id != "" {
		return id
	}
	return strings.TrimSpace(a.ItemID)
}

// treeHealth summarizes the shape of the finished tree. It drives the
// reorganization trigger and the per-round metrics log, replacing the old
// root-only flatness flag with checks over every topic.
type treeHealth struct {
	TopicCount           int
	DetailCount          int
	UnclassifiedChildren int
	MaxTopicChildren     int
	MaxTopicID           string
	// MaxConcentration is MaxTopicChildren / DetailCount (0 when no details).
	MaxConcentration float64
}

const (
	treeReorganizeMaxTopicChildren     = 8
	treeReorganizeConcentrationMin     = 0.5
	treeReorganizeConcentrationDetails = 6
	treeReorganizeUnclassifiedMin      = 5
)

// needsReorganization reports whether the tree shape warrants a local
// reorganization pass: an overcrowded topic, a topic holding most of all
// detail nodes, or a growing unclassified backlog.
func (h treeHealth) needsReorganization() bool {
	if h.MaxTopicChildren >= treeReorganizeMaxTopicChildren {
		return true
	}
	if h.DetailCount >= treeReorganizeConcentrationDetails && h.MaxConcentration >= treeReorganizeConcentrationMin {
		return true
	}
	return h.UnclassifiedChildren >= treeReorganizeUnclassifiedMin
}

func (h treeHealth) String() string {
	return fmt.Sprintf("topics=%d details=%d unclassified=%d maxTopicChildren=%d maxTopicId=%s maxConcentration=%.2f",
		h.TopicCount, h.DetailCount, h.UnclassifiedChildren, h.MaxTopicChildren, h.MaxTopicID, h.MaxConcentration)
}

// treeStateFromPayloadTree decomposes a stored tree into topics, detail
// nodes, and a parent map. Payloads written before the single-parent model
// (nodes without parentId) are converted by BFS from the first topic node --
// the same rule the frontend used -- and the leftover edges are preserved as
// relations so the semantic links are not lost.
func treeStateFromPayloadTree(tree *liveAnalysisTree) (nodes []liveAnalysisTreeNode, parents map[string]string, relations []liveAnalysisTreeRelation) {
	parents = make(map[string]string)
	if tree == nil {
		return nil, parents, nil
	}
	nodes = append(nodes, tree.Nodes...)
	relations = append(relations, tree.Relations...)

	hasParentIDs := false
	for _, node := range nodes {
		if strings.TrimSpace(node.ParentID) != "" {
			hasParentIDs = true
			break
		}
	}
	if hasParentIDs {
		for _, node := range nodes {
			if parent := strings.TrimSpace(node.ParentID); parent != "" {
				parents[node.ID] = parent
			}
		}
		return nodes, parents, relations
	}

	// Legacy conversion: derive one parent per node from the accumulated
	// edges (BFS from the first topic node), keep the remaining edges as
	// relations.
	rootID := ""
	for _, node := range nodes {
		if node.Kind == "topic" {
			rootID = node.ID
			break
		}
	}
	if rootID == "" && len(nodes) > 0 {
		rootID = nodes[0].ID
	}
	adjacency := make(map[string][]string, len(tree.Edges))
	for _, edge := range tree.Edges {
		adjacency[edge.Source] = append(adjacency[edge.Source], edge.Target)
	}
	visited := map[string]bool{rootID: true}
	queue := []string{rootID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range adjacency[current] {
			if visited[next] {
				continue
			}
			visited[next] = true
			parents[next] = current
			queue = append(queue, next)
		}
	}
	for _, edge := range tree.Edges {
		if parents[edge.Target] == edge.Source {
			continue
		}
		relations = append(relations, liveAnalysisTreeRelation{Source: edge.Source, Target: edge.Target, Kind: "related"})
	}
	return nodes, parents, relations
}

// rebuildDiscussionTree is the single write path for the discussion tree. It
// merges the previous tree, the meeting context (agenda topics), the merged
// item list, and the model's proposals (new topics + parent assignments)
// into a tree that always satisfies the invariants listed in the file
// header. stats may be nil.
func rebuildDiscussionTree(
	previous *liveAnalysisTree,
	mc *meetingContext,
	items []liveAnalysisItem,
	newTopics []liveAnalysisTreeNode,
	assignments []treeAssignment,
	resolvedIDs map[string]struct{},
	stats *liveAnalysisTreeMergeStats,
) *liveAnalysisTree {
	prevNodes, parents, relations := treeStateFromPayloadTree(previous)
	previousParents := make(map[string]string, len(parents))
	for id, parent := range parents {
		previousParents[id] = parent
	}

	// Index previous nodes, split topics/details. The root node is rebuilt
	// below so it is skipped here.
	topicOrder := make([]string, 0)
	topics := make(map[string]liveAnalysisTreeNode)
	detailOrder := make([]string, 0)
	details := make(map[string]liveAnalysisTreeNode)
	addTopic := func(node liveAnalysisTreeNode) {
		if _, exists := topics[node.ID]; !exists {
			topicOrder = append(topicOrder, node.ID)
		}
		topics[node.ID] = node
	}
	addDetail := func(node liveAnalysisTreeNode) {
		if _, exists := details[node.ID]; !exists {
			detailOrder = append(detailOrder, node.ID)
		}
		details[node.ID] = node
	}
	for _, node := range prevNodes {
		if node.ID == treeRootNodeID {
			continue
		}
		if node.Kind == "topic" {
			addTopic(node)
		} else {
			addDetail(node)
		}
	}

	// Agenda topics: stable ids from the meeting context. Existing topics
	// keep their (possibly reorganizer-renamed) label; missing ones are
	// created. They always exist even while empty, so the agenda skeleton is
	// visible from the first round.
	if mc != nil {
		for _, item := range mc.Agenda {
			if _, exists := topics[item.ID]; exists {
				continue
			}
			addTopic(liveAnalysisTreeNode{ID: item.ID, Kind: "topic", Label: item.Title})
		}
	}

	// Model-proposed new topics: validated and deduplicated by normalized
	// label against every existing topic, so the same 大分類 never appears
	// twice under two ids. topicAlias maps a duplicate proposal's id to the
	// surviving topic so assignments keep working.
	topicAlias := make(map[string]string)
	labelIndex := make(map[string]string, len(topics))
	for id, topic := range topics {
		labelIndex[normalizeForMatch(topic.Label)] = id
	}
	for _, proposed := range newTopics {
		id := strings.TrimSpace(proposed.ID)
		label := truncateRunes(strings.TrimSpace(proposed.Label), liveAnalysisTopicLabelMaxRunes)
		if label == "" {
			continue
		}
		if id == "" || id == treeRootNodeID {
			id = "topic-" + normalizeForMatch(label)
		}
		if !strings.HasPrefix(id, "topic-") && !strings.HasPrefix(id, agendaTopicIDPrefix) {
			id = "topic-" + id
		}
		if _, isDetail := details[id]; isDetail {
			// 既存詳細ノードのidをtopicとして再利用させない(型の安定性)。
			continue
		}
		if existingID, dup := labelIndex[normalizeForMatch(label)]; dup {
			if existingID != id {
				topicAlias[id] = existingID
			}
			continue
		}
		if _, exists := topics[id]; exists {
			continue
		}
		addTopic(liveAnalysisTreeNode{
			ID:          id,
			Kind:        "topic",
			Label:       label,
			Description: truncateRunes(strings.TrimSpace(proposed.Description), liveAnalysisTreeDescriptionMaxRunes),
		})
		labelIndex[normalizeForMatch(label)] = id
		if stats != nil {
			stats.DiffNewNodes++
		}
	}

	// Detail nodes are upserted 1:1 from the merged items, so every card has
	// a matching tree node and vice versa. Nodes from previous rounds whose
	// item was evicted by the item cap survive as-is.
	itemIDs := liveAnalysisItemIDSet(items)
	for _, item := range items {
		if item.ID == "" || item.Status == "dismissed" {
			continue
		}
		node, exists := details[item.ID]
		if !exists {
			node = liveAnalysisTreeNode{ID: item.ID}
			if stats != nil {
				stats.SynthesizedNodes++
			}
		}
		node.Kind = liveAnalysisTreeNodeKindForItem(item.Kind)
		node.Label = truncateRunes(item.Title, 40)
		if body := truncateRunes(strings.TrimSpace(item.Body), liveAnalysisTreeDescriptionMaxRunes); body != "" {
			node.Description = body
		}
		switch item.Status {
		case "resolved":
			node.Status = "resolved"
		case "updated":
			node.Status = "updated"
		default:
			if node.Status == "" {
				node.Status = "open"
			}
		}
		node.RelatedItemIDs = normalizeLiveAnalysisRelatedItemIDs(node.RelatedItemIDs, node.ID, itemIDs)
		addDetail(node)
	}

	// Resolved ids mark both details and topics (topics stay in place).
	for id := range resolvedIDs {
		if node, ok := details[id]; ok {
			node.Status = "resolved"
			details[id] = node
		}
	}

	// Parent assignments from the model. Only the parent of a known detail
	// node can be assigned, and only onto a known topic; everything else is
	// resolved by the invariant pass below.
	for _, assignment := range assignments {
		nodeID := assignment.nodeID()
		if nodeID == "" {
			continue
		}
		parent := strings.TrimSpace(assignment.ParentTopicID)
		if alias, ok := topicAlias[parent]; ok {
			parent = alias
		}
		if parent == "" {
			continue
		}
		if _, isDetail := details[nodeID]; !isDetail {
			continue
		}
		parents[nodeID] = parent
	}

	// Cap detail nodes (active/resolved separately, topics never evicted).
	detailNodes := make([]liveAnalysisTreeNode, 0, len(detailOrder))
	for _, id := range detailOrder {
		detailNodes = append(detailNodes, details[id])
	}
	detailNodes = capLiveAnalysisTreeNodes(detailNodes, liveAnalysisTreeMaxNodes, liveAnalysisTreeMaxResolvedNodes)

	return assembleTree(mc, topics, topicOrder, detailNodes, parents, previousParents, relations, stats)
}

// assembleTree runs the invariant pass and produces the final payload tree:
// root first, topics next (agenda order preserved), detail nodes last, and
// edges derived from the enforced single parents.
func assembleTree(
	mc *meetingContext,
	topics map[string]liveAnalysisTreeNode,
	topicOrder []string,
	detailNodes []liveAnalysisTreeNode,
	parents map[string]string,
	previousParents map[string]string,
	relations []liveAnalysisTreeRelation,
	stats *liveAnalysisTreeMergeStats,
) *liveAnalysisTree {
	if len(topics) == 0 && len(detailNodes) == 0 {
		return nil
	}

	root := liveAnalysisTreeNode{
		ID:          treeRootNodeID,
		Kind:        "topic",
		Label:       mc.rootLabel(),
		Description: mc.rootDescription(),
	}

	topicIDs := make(map[string]struct{}, len(topics)+1)
	for id := range topics {
		topicIDs[id] = struct{}{}
	}

	// climbToTopic resolves a proposed parent to a valid topic: a topic id is
	// returned as-is; a detail id climbs its parent chain (cycle-guarded)
	// until a topic is found. "" means no valid topic was reachable.
	climbToTopic := func(fromID string) string {
		seen := make(map[string]struct{})
		current := fromID
		for current != "" {
			if _, isTopic := topicIDs[current]; isTopic {
				return current
			}
			if _, looped := seen[current]; looped {
				return ""
			}
			seen[current] = struct{}{}
			current = parents[current]
		}
		return ""
	}

	needsUnclassified := false
	enforcedParents := make(map[string]string, len(detailNodes)+len(topics))

	// topicの親は常にroot(型逆転・topic循環をここで遮断)。
	for id := range topics {
		enforcedParents[id] = treeRootNodeID
	}

	for _, node := range detailNodes {
		proposed := parents[node.ID]
		parent := ""
		switch {
		case proposed == "" || proposed == node.ID || proposed == treeRootNodeID:
			// 親なし・自己参照・root直下は許可しない → topic配下へ。
		default:
			parent = climbToTopic(proposed)
		}
		if parent == "" || parent == treeRootNodeID {
			parent = treeUnclassifiedTopicID
			needsUnclassified = true
		}
		enforcedParents[node.ID] = parent
		if stats != nil {
			if previous, had := previousParents[node.ID]; had && previous != parent {
				stats.ReparentedNodes++
			}
			if parent == treeUnclassifiedTopicID && proposed != treeUnclassifiedTopicID {
				stats.OrphanRescuedEdges++
			}
		}
	}

	if needsUnclassified {
		if _, exists := topics[treeUnclassifiedTopicID]; !exists {
			topics[treeUnclassifiedTopicID] = liveAnalysisTreeNode{
				ID:    treeUnclassifiedTopicID,
				Kind:  "topic",
				Label: treeUnclassifiedTopicLabel,
			}
			topicOrder = append(topicOrder, treeUnclassifiedTopicID)
			topicIDs[treeUnclassifiedTopicID] = struct{}{}
			enforcedParents[treeUnclassifiedTopicID] = treeRootNodeID
		}
	} else {
		// 未分類topicは子が無ければ表示しない(空topicを増やさない)。
		if _, exists := topics[treeUnclassifiedTopicID]; exists {
			hasChild := false
			for _, parent := range enforcedParents {
				if parent == treeUnclassifiedTopicID {
					hasChild = true
					break
				}
			}
			if !hasChild {
				delete(topics, treeUnclassifiedTopicID)
				delete(topicIDs, treeUnclassifiedTopicID)
				delete(enforcedParents, treeUnclassifiedTopicID)
			}
		}
	}

	// Assemble node list: root, topics (in stable order), details.
	nodes := make([]liveAnalysisTreeNode, 0, 1+len(topics)+len(detailNodes))
	nodes = append(nodes, root)
	for _, id := range topicOrder {
		topic, ok := topics[id]
		if !ok {
			continue
		}
		topic.ParentID = treeRootNodeID
		nodes = append(nodes, topic)
	}
	for _, node := range detailNodes {
		node.ParentID = enforcedParents[node.ID]
		nodes = append(nodes, node)
	}

	// Edges are a pure view of the parent map.
	edges := make([]liveAnalysisTreeEdge, 0, len(nodes)-1)
	for _, node := range nodes {
		if node.ID == treeRootNodeID || node.ParentID == "" {
			continue
		}
		edges = append(edges, liveAnalysisTreeEdge{Source: node.ParentID, Target: node.ID})
	}

	// Relations: semantic links only. Never duplicated with a parent edge,
	// never self-referencing, and both endpoints must exist.
	nodeIDs := make(map[string]struct{}, len(nodes))
	parentKey := make(map[string]struct{}, len(edges))
	for _, node := range nodes {
		nodeIDs[node.ID] = struct{}{}
		if node.ParentID != "" {
			parentKey[node.ParentID+"\x00"+node.ID] = struct{}{}
		}
	}
	seenRelations := make(map[string]struct{}, len(relations))
	keptRelations := make([]liveAnalysisTreeRelation, 0, len(relations))
	for _, relation := range relations {
		relation.Source = strings.TrimSpace(relation.Source)
		relation.Target = strings.TrimSpace(relation.Target)
		if relation.Source == "" || relation.Target == "" || relation.Source == relation.Target {
			continue
		}
		if _, ok := nodeIDs[relation.Source]; !ok {
			continue
		}
		if _, ok := nodeIDs[relation.Target]; !ok {
			continue
		}
		if _, dup := parentKey[relation.Source+"\x00"+relation.Target]; dup {
			continue
		}
		key := relation.Source + "\x00" + relation.Target
		if _, dup := seenRelations[key]; dup {
			continue
		}
		seenRelations[key] = struct{}{}
		keptRelations = append(keptRelations, relation)
	}

	tree := &liveAnalysisTree{Nodes: nodes, Edges: edges, Relations: keptRelations}
	if stats != nil {
		health := computeTreeHealth(tree)
		stats.TotalEdges = len(edges)
		stats.TopicChildCount = health.TopicCount
		stats.MaxDepth = treeDepthOf(tree)
		stats.FlatTreeDetected = health.needsReorganization()
	}
	return tree
}

// computeTreeHealth inspects every topic (not just root) for crowding.
func computeTreeHealth(tree *liveAnalysisTree) treeHealth {
	health := treeHealth{}
	if tree == nil {
		return health
	}
	children := make(map[string]int)
	for _, node := range tree.Nodes {
		if node.ID == treeRootNodeID {
			continue
		}
		if node.Kind == "topic" {
			health.TopicCount++
			continue
		}
		if node.Status == "resolved" {
			// 解決済みノードは過密判定から除外する(再編成対象は活発な議論)。
			continue
		}
		health.DetailCount++
		children[node.ParentID]++
	}
	for topicID, count := range children {
		if topicID == treeUnclassifiedTopicID {
			health.UnclassifiedChildren = count
		}
		if count > health.MaxTopicChildren {
			health.MaxTopicChildren = count
			health.MaxTopicID = topicID
		}
	}
	if health.DetailCount > 0 {
		health.MaxConcentration = float64(health.MaxTopicChildren) / float64(health.DetailCount)
	}
	return health
}

// treeDepthOf returns the max depth of the enforced tree (root = 0). With
// the fixed root→topic→detail shape this is at most 2; it is computed rather
// than hard-coded so the metric stays honest if the shape ever changes.
func treeDepthOf(tree *liveAnalysisTree) int {
	if tree == nil {
		return 0
	}
	depth := 0
	byID := make(map[string]string, len(tree.Nodes))
	for _, node := range tree.Nodes {
		byID[node.ID] = node.ParentID
	}
	for _, node := range tree.Nodes {
		d := 0
		current := node.ParentID
		guard := 0
		for current != "" && guard < len(tree.Nodes)+1 {
			d++
			current = byID[current]
			guard++
		}
		if d > depth {
			depth = d
		}
	}
	return depth
}

// --- ツリー再編成(Task E / F) ----------------------------------------------

// treeOperation is one differential reorganization step proposed by the
// reorganizer model. Unknown types and invalid references are skipped
// individually; a bad operation can never corrupt the tree.
type treeOperation struct {
	Type        string `json:"type"`
	TopicID     string `json:"topicId"`
	NodeID      string `json:"nodeId"`
	Label       string `json:"label"`
	Title       string `json:"title"`
	Description string `json:"description"`
	ToParentID  string `json:"toParentId"`
	FromTopicID string `json:"fromTopicId"`
	IntoTopicID string `json:"intoTopicId"`
}

const treeReorganizeMaxOperations = 24

// applyTreeOperations applies reorganizer operations to a payload tree and
// re-runs the invariant pass. It returns the new tree and how many
// operations were actually applied.
func applyTreeOperations(tree *liveAnalysisTree, mc *meetingContext, operations []treeOperation, stats *liveAnalysisTreeMergeStats) (*liveAnalysisTree, int) {
	if tree == nil {
		return nil, 0
	}
	nodes, parents, relations := treeStateFromPayloadTree(tree)

	topicOrder := make([]string, 0)
	topics := make(map[string]liveAnalysisTreeNode)
	detailOrder := make([]string, 0)
	details := make(map[string]liveAnalysisTreeNode)
	for _, node := range nodes {
		if node.ID == treeRootNodeID {
			continue
		}
		if node.Kind == "topic" {
			if _, exists := topics[node.ID]; !exists {
				topicOrder = append(topicOrder, node.ID)
			}
			topics[node.ID] = node
		} else {
			if _, exists := details[node.ID]; !exists {
				detailOrder = append(detailOrder, node.ID)
			}
			details[node.ID] = node
		}
	}
	previousParents := make(map[string]string, len(parents))
	for id, parent := range parents {
		previousParents[id] = parent
	}

	agendaIDs := make(map[string]struct{})
	if mc != nil {
		for _, item := range mc.Agenda {
			agendaIDs[item.ID] = struct{}{}
		}
	}

	applied := 0
	if len(operations) > treeReorganizeMaxOperations {
		operations = operations[:treeReorganizeMaxOperations]
	}
	for _, op := range operations {
		switch strings.TrimSpace(strings.ToLower(op.Type)) {
		case "create_topic":
			label := truncateRunes(strings.TrimSpace(firstNonEmptyTrimmed(op.Label, op.Title)), liveAnalysisTopicLabelMaxRunes)
			id := strings.TrimSpace(op.TopicID)
			if label == "" {
				continue
			}
			if id == "" || id == treeRootNodeID {
				id = "topic-" + normalizeForMatch(label)
			}
			if !strings.HasPrefix(id, "topic-") && !strings.HasPrefix(id, agendaTopicIDPrefix) {
				id = "topic-" + id
			}
			if _, exists := topics[id]; exists {
				continue
			}
			if _, isDetail := details[id]; isDetail {
				continue
			}
			duplicate := false
			for _, topic := range topics {
				if normalizeForMatch(topic.Label) == normalizeForMatch(label) {
					duplicate = true
					break
				}
			}
			if duplicate {
				continue
			}
			topics[id] = liveAnalysisTreeNode{ID: id, Kind: "topic", Label: label, Description: truncateRunes(strings.TrimSpace(op.Description), liveAnalysisTreeDescriptionMaxRunes)}
			topicOrder = append(topicOrder, id)
			applied++
		case "move_node":
			nodeID := strings.TrimSpace(op.NodeID)
			toParent := strings.TrimSpace(op.ToParentID)
			if _, isDetail := details[nodeID]; !isDetail {
				continue
			}
			if _, isTopic := topics[toParent]; !isTopic {
				continue
			}
			if parents[nodeID] == toParent {
				continue
			}
			parents[nodeID] = toParent
			applied++
		case "rename_topic":
			topicID := strings.TrimSpace(op.TopicID)
			label := truncateRunes(strings.TrimSpace(firstNonEmptyTrimmed(op.Label, op.Title)), liveAnalysisTopicLabelMaxRunes)
			topic, exists := topics[topicID]
			if !exists || label == "" || topicID == treeUnclassifiedTopicID {
				continue
			}
			topic.Label = label
			topics[topicID] = topic
			applied++
		case "merge_topic":
			fromID := strings.TrimSpace(firstNonEmptyTrimmed(op.FromTopicID, op.TopicID))
			intoID := strings.TrimSpace(op.IntoTopicID)
			if fromID == intoID {
				continue
			}
			if _, exists := topics[fromID]; !exists {
				continue
			}
			if _, exists := topics[intoID]; !exists {
				continue
			}
			// アジェンダtopicと未分類topicはstable IDを守るため削除しない。
			if _, isAgenda := agendaIDs[fromID]; isAgenda || fromID == treeUnclassifiedTopicID {
				continue
			}
			for nodeID, parent := range parents {
				if parent == fromID {
					parents[nodeID] = intoID
				}
			}
			delete(topics, fromID)
			applied++
		}
	}
	if applied == 0 {
		return tree, 0
	}

	detailNodes := make([]liveAnalysisTreeNode, 0, len(detailOrder))
	for _, id := range detailOrder {
		detailNodes = append(detailNodes, details[id])
	}
	rebuilt := assembleTree(mc, topics, topicOrder, detailNodes, parents, previousParents, relations, stats)
	return rebuilt, applied
}
