package application

import (
	"fmt"
	"sort"
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
//
// 意味分類はここで確定する: 割当はconfidenceとhysteresisの検証を通ってから
// 親になり、newTopicsは emerging topic 候補として蓄積され、昇格条件を満たした
// ものだけが dynamic topic になる(ai_tree_classification.go 参照)。更新後の
// item分類メタデータと候補一覧を返す。
func rebuildDiscussionTree(
	previous *liveAnalysisTree,
	mc *meetingContext,
	items []liveAnalysisItem,
	newTopics []liveAnalysisTreeNode,
	assignments []treeAssignment,
	resolvedIDs map[string]struct{},
	priorCandidates []emergingTopicCandidate,
	round int64,
	cfg TreeClassificationConfig,
	stats *liveAnalysisTreeMergeStats,
) (*liveAnalysisTree, []liveAnalysisItem, []emergingTopicCandidate) {
	cfg = cfg.normalized()
	prevNodes, parents, relations := treeStateFromPayloadTree(previous)
	previousParents := make(map[string]string, len(parents))
	for id, parent := range parents {
		previousParents[id] = parent
	}

	items = append([]liveAnalysisItem(nil), items...)
	itemIndex := make(map[string]int, len(items))
	for i := range items {
		if items[i].ID != "" {
			itemIndex[items[i].ID] = i
		}
	}
	itemAt := func(id string) *liveAnalysisItem {
		if at, ok := itemIndex[id]; ok {
			return &items[at]
		}
		return nil
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

	agendaIDs := make(map[string]struct{})
	if mc != nil {
		for _, item := range mc.Agenda {
			agendaIDs[item.ID] = struct{}{}
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
			addTopic(liveAnalysisTreeNode{ID: item.ID, Kind: "topic", Label: item.Title, Origin: topicOriginAgenda})
		}
	}
	// origin未設定の既存topic(旧payload)へ由来をバックフィルする。
	dynamicTopicCount := 0
	for id, topic := range topics {
		if topic.Origin == "" {
			topic.Origin = deriveTopicOrigin(id, agendaIDs)
			topics[id] = topic
		}
		if topic.Origin == topicOriginDynamic {
			dynamicTopicCount++
		}
	}

	// bootstrap: アジェンダも既存topicも無い会議では、newTopicsを従来通り即
	// 作成する(全ノードが追加論点に沈むよりも良い)。topicが1つでもできたら
	// 以降は emerging 候補フローに従う。
	bootstrap := true
	for id := range topics {
		if id != treeUnclassifiedTopicID {
			bootstrap = false
			break
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

	// Model-proposed new topics. Duplicated labels alias onto the surviving
	// topic (so the same 大分類 never appears twice under two ids); everything
	// else becomes an emerging topic candidate instead of an immediate topic
	// (bootstrap時を除く)。candidateAlias maps a re-proposed id onto the
	// tracked candidate so assignments keep working.
	topicAlias := make(map[string]string)
	labelIndex := make(map[string]string, len(topics))
	for id, topic := range topics {
		labelIndex[normalizeForMatch(topic.Label)] = id
	}
	candidates := append([]emergingTopicCandidate(nil), priorCandidates...)
	candidateAlias := make(map[string]string)
	candidateIndexByID := func(id string) int {
		if alias, ok := candidateAlias[id]; ok {
			id = alias
		}
		for i := range candidates {
			if candidates[i].ID == id {
				return i
			}
		}
		return -1
	}
	candidateIndexByLabel := func(label string) int {
		key := normalizeForMatch(label)
		for i := range candidates {
			if normalizeForMatch(candidates[i].Label) == key {
				return i
			}
		}
		return -1
	}
	recordEmerging := func(d emergingDecision) {
		if stats != nil {
			stats.EmergingDecisions = append(stats.EmergingDecisions, d)
		}
	}
	newCandidatesThisRound := 0
	for _, proposed := range newTopics {
		label := truncateRunes(strings.TrimSpace(proposed.Label), liveAnalysisTopicLabelMaxRunes)
		if label == "" {
			continue
		}
		id := normalizeProposedTopicID(proposed.ID, label)
		if id == "" {
			continue
		}
		// 実在しないagenda IDを新topicとして名乗らせない(stable IDの保護)。
		if strings.HasPrefix(id, agendaTopicIDPrefix) {
			if _, isAgenda := agendaIDs[id]; !isAgenda {
				id = "topic-" + normalizeForMatch(label)
			}
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
		if bootstrap {
			if newCandidatesThisRound >= maxEmergingCandidatesPerRound {
				recordEmerging(emergingDecision{CandidateID: id, Decision: emergingRejectedRoundCap})
				continue
			}
			addTopic(liveAnalysisTreeNode{
				ID:          id,
				Kind:        "topic",
				Label:       label,
				Description: truncateRunes(strings.TrimSpace(proposed.Description), liveAnalysisTreeDescriptionMaxRunes),
				Origin:      topicOriginDynamic,
			})
			labelIndex[normalizeForMatch(label)] = id
			dynamicTopicCount++
			newCandidatesThisRound++
			if stats != nil {
				stats.DiffNewNodes++
			}
			continue
		}
		if at := candidateIndexByID(id); at >= 0 {
			candidates[at].Label = label
			if description := truncateRunes(strings.TrimSpace(proposed.Description), liveAnalysisTreeDescriptionMaxRunes); description != "" {
				candidates[at].Description = description
			}
			candidates[at].addEvidence("", round)
			recordEmerging(emergingDecision{CandidateID: candidates[at].ID, EvidenceItemCount: len(candidates[at].EvidenceItemIDs), RoundCount: candidates[at].RoundCount, Decision: emergingUpdated})
			continue
		}
		if at := candidateIndexByLabel(label); at >= 0 {
			// 同じ意味の候補を別idで数えない。以降の割当が新idで来ても届くよう
			// aliasを張る。
			candidateAlias[id] = candidates[at].ID
			candidates[at].addEvidence("", round)
			recordEmerging(emergingDecision{CandidateID: candidates[at].ID, EvidenceItemCount: len(candidates[at].EvidenceItemIDs), RoundCount: candidates[at].RoundCount, Decision: emergingUpdated})
			continue
		}
		if newCandidatesThisRound >= maxEmergingCandidatesPerRound {
			recordEmerging(emergingDecision{CandidateID: id, Decision: emergingRejectedRoundCap})
			continue
		}
		candidates = append(candidates, emergingTopicCandidate{
			ID:          id,
			Label:       label,
			Description: truncateRunes(strings.TrimSpace(proposed.Description), liveAnalysisTreeDescriptionMaxRunes),
			FirstRound:  round,
			LastRound:   round,
			RoundCount:  1,
		})
		newCandidatesThisRound++
		recordEmerging(emergingDecision{CandidateID: id, RoundCount: 1, Decision: emergingCreated})
	}

	// Parent assignments from the model: confidenceとhysteresisを検証してから
	// 親を確定する。emerging候補への割当は tentative として追加論点に留まり、
	// 候補の証拠として記録される。
	applyAssignments(assignmentContext{
		assignments:    assignments,
		parents:        parents,
		topics:         topics,
		details:        details,
		topicAlias:     topicAlias,
		candidateAlias: candidateAlias,
		candidates:     candidates,
		itemAt:         itemAt,
		round:          round,
		cfg:            cfg,
		stats:          stats,
	})

	// 昇格判定: 証拠が揃った候補だけを dynamic topic にする。
	candidates = promoteEmergingCandidates(promotionContext{
		candidates:        candidates,
		parents:           parents,
		details:           details,
		labelIndex:        labelIndex,
		addTopic:          addTopic,
		dynamicTopicCount: &dynamicTopicCount,
		itemAt:            itemAt,
		round:             round,
		cfg:               cfg,
		stats:             stats,
	})
	candidates = capEmergingCandidates(candidates, maxEmergingCandidates)

	// Cap detail nodes (active/resolved separately, topics never evicted).
	detailNodes := make([]liveAnalysisTreeNode, 0, len(detailOrder))
	for _, id := range detailOrder {
		detailNodes = append(detailNodes, details[id])
	}
	detailNodes = capLiveAnalysisTreeNodes(detailNodes, liveAnalysisTreeMaxNodes, liveAnalysisTreeMaxResolvedNodes)

	tree := assembleTree(mc, topics, topicOrder, detailNodes, parents, previousParents, relations, stats)
	syncItemClassificationWithTree(items, tree)
	return tree, items, candidates
}

// assignmentContext bundles the state applyAssignments mutates: the parents
// map, item classification metadata, and candidate evidence.
type assignmentContext struct {
	assignments    []treeAssignment
	parents        map[string]string
	topics         map[string]liveAnalysisTreeNode
	details        map[string]liveAnalysisTreeNode
	topicAlias     map[string]string
	candidateAlias map[string]string
	candidates     []emergingTopicCandidate
	itemAt         func(string) *liveAnalysisItem
	round          int64
	cfg            TreeClassificationConfig
	stats          *liveAnalysisTreeMergeStats
}

// applyAssignments applies the model's parent proposals under the
// classification policy:
//   - 明示的に低いconfidence(0<c<閾値)の割当は tentative として追加論点へ。
//   - 追加論点からtopicへの引き上げは緩め(閾値以上、または同一候補の再提案)。
//   - assigned済みitemの移動は厳しめ(閾値以上かつ、confidenceが前回を
//     reparentConfidenceMargin以上上回るか、同一候補が2ラウンド連続)。
//   - 存在しない親は、未割当itemなら追加論点へfallback、割当済みitemなら
//     現在の親を保持(モデルの不正IDで配置を壊さない)。
//
// confidence省略時(0)は従来互換で受理する(legacy v2変換はconfidenceを持たない)。
func applyAssignments(ac assignmentContext) {
	record := func(d assignmentDecision) {
		if ac.stats != nil {
			ac.stats.AssignmentDecisions = append(ac.stats.AssignmentDecisions, d)
		}
	}
	setMeta := func(item *liveAnalysisItem, status, source, candidate string, confidence float64, reason string) {
		if item == nil {
			return
		}
		item.ClassificationStatus = status
		item.AssignmentSource = source
		item.CandidateTopicID = candidate
		item.AssignmentConfidence = confidence
		if reason != "" {
			item.AssignmentReason = reason
		}
	}
	// climbToTopic resolves a detail id to its current topic ancestor (cycle
	// guarded), mirroring the assembleTree rescue so "詳細ノードを親に指定"が
	// そのtopicへの割当として扱われる。
	climbToTopic := func(fromID string) string {
		seen := make(map[string]struct{})
		current := fromID
		for current != "" {
			if _, isTopic := ac.topics[current]; isTopic {
				return current
			}
			if _, looped := seen[current]; looped {
				return ""
			}
			seen[current] = struct{}{}
			current = ac.parents[current]
		}
		return ""
	}
	candidateAt := func(id string) *emergingTopicCandidate {
		if alias, ok := ac.candidateAlias[id]; ok {
			id = alias
		}
		for i := range ac.candidates {
			if ac.candidates[i].ID == id {
				return &ac.candidates[i]
			}
		}
		return nil
	}

	for _, assignment := range ac.assignments {
		nodeID := assignment.nodeID()
		if nodeID == "" {
			continue
		}
		requested := strings.TrimSpace(assignment.ParentTopicID)
		if alias, ok := ac.topicAlias[requested]; ok {
			requested = alias
		}
		if requested == "" {
			continue
		}
		if _, isDetail := ac.details[nodeID]; !isDetail {
			record(assignmentDecision{ItemID: nodeID, RequestedParentID: requested, Decision: assignmentRejectedUnknownItem})
			continue
		}
		confidence := assignment.Confidence
		if confidence < 0 {
			confidence = 0
		}
		if confidence > 1 {
			confidence = 1
		}
		reason := truncateRunes(strings.TrimSpace(assignment.Reason), assignmentReasonMaxRunes)
		item := ac.itemAt(nodeID)
		current := ac.parents[nodeID]

		// 明示的な未分類提案。
		if requested == treeUnclassifiedTopicID {
			ac.parents[nodeID] = treeUnclassifiedTopicID
			setMeta(item, classificationUnclassified, assignmentSourceModel, "", confidence, reason)
			record(assignmentDecision{ItemID: nodeID, RequestedParentID: requested, SelectedParentID: treeUnclassifiedTopicID, Confidence: confidence, Source: assignmentSourceModel, Decision: assignmentAcceptedUnclassified, Status: classificationUnclassified})
			continue
		}

		// emerging候補への割当: 昇格まで追加論点にtentativeで留める。既に
		// topicへ配置済みのitemは動かさず、候補の証拠だけを記録する。
		if candidate := candidateAt(requested); candidate != nil {
			candidate.addEvidence(nodeID, ac.round)
			status := classificationTentative
			selected := current
			if current == "" || current == treeUnclassifiedTopicID {
				ac.parents[nodeID] = treeUnclassifiedTopicID
				selected = treeUnclassifiedTopicID
				setMeta(item, classificationTentative, assignmentSourceModel, candidate.ID, confidence, reason)
			} else if item != nil {
				item.CandidateTopicID = candidate.ID
				status = item.ClassificationStatus
			}
			record(assignmentDecision{ItemID: nodeID, RequestedParentID: requested, SelectedParentID: selected, Confidence: confidence, Source: assignmentSourceModel, Decision: assignmentDeferredEmerging, Status: status, CandidateTopicID: candidate.ID})
			continue
		}

		// topic以外(詳細ノード)が親に指定された場合はそのtopic祖先へ解決する。
		if _, isTopic := ac.topics[requested]; !isTopic {
			if _, isDetail := ac.details[requested]; isDetail {
				if resolved := climbToTopic(requested); resolved != "" && resolved != treeUnclassifiedTopicID {
					requested = resolved
				}
			}
		}
		if _, isTopic := ac.topics[requested]; !isTopic || requested == treeUnclassifiedTopicID {
			// 存在しない親: 未割当なら追加論点へ、配置済みなら現状維持。
			if requested != treeUnclassifiedTopicID {
				selected := current
				if current == "" {
					ac.parents[nodeID] = treeUnclassifiedTopicID
					selected = treeUnclassifiedTopicID
					setMeta(item, classificationUnclassified, assignmentSourceFallback, "", confidence, reason)
				}
				record(assignmentDecision{ItemID: nodeID, RequestedParentID: requested, SelectedParentID: selected, Confidence: confidence, Source: assignmentSourceFallback, Decision: assignmentRejectedUnknown, Status: classificationUnclassified})
				continue
			}
			ac.parents[nodeID] = treeUnclassifiedTopicID
			setMeta(item, classificationUnclassified, assignmentSourceModel, "", confidence, reason)
			record(assignmentDecision{ItemID: nodeID, RequestedParentID: requested, SelectedParentID: treeUnclassifiedTopicID, Confidence: confidence, Source: assignmentSourceModel, Decision: assignmentAcceptedUnclassified, Status: classificationUnclassified})
			continue
		}

		target := requested
		lowConfidence := confidence > 0 && confidence < ac.cfg.AgendaAssignmentThreshold
		repeat := item != nil && item.CandidateTopicID != "" && item.CandidateTopicID == target

		switch {
		case current == target:
			// 同じ親の再主張: confidence/理由だけ更新する。
			setMeta(item, classificationAssigned, assignmentSourceModel, "", confidence, reason)
			record(assignmentDecision{ItemID: nodeID, RequestedParentID: target, SelectedParentID: target, Confidence: confidence, Source: assignmentSourceModel, Decision: assignmentAccepted, Status: classificationAssigned})
		case current == "" || current == treeUnclassifiedTopicID:
			// 新規または追加論点からの引き上げ(緩め)。
			if lowConfidence && !repeat {
				ac.parents[nodeID] = treeUnclassifiedTopicID
				setMeta(item, classificationTentative, assignmentSourceModel, target, confidence, reason)
				record(assignmentDecision{ItemID: nodeID, RequestedParentID: target, SelectedParentID: treeUnclassifiedTopicID, Confidence: confidence, Source: assignmentSourceModel, Decision: assignmentDeferredLowConf, Status: classificationTentative, CandidateTopicID: target})
				continue
			}
			decision := assignmentAccepted
			if repeat && lowConfidence {
				decision = assignmentAcceptedRepeat
			}
			ac.parents[nodeID] = target
			setMeta(item, classificationAssigned, assignmentSourceModel, "", confidence, reason)
			record(assignmentDecision{ItemID: nodeID, RequestedParentID: target, SelectedParentID: target, Confidence: confidence, Source: assignmentSourceModel, Decision: decision, Status: classificationAssigned})
		default:
			// assigned済みitemの別topicへの移動(厳しめ)。
			previousConfidence := 0.0
			if item != nil {
				previousConfidence = item.AssignmentConfidence
			}
			allowMove := confidence >= ac.cfg.AgendaAssignmentThreshold &&
				(repeat || previousConfidence == 0 || confidence >= previousConfidence+reparentConfidenceMargin)
			if !allowMove {
				// 候補として記録し、次ラウンドも同じ提案なら移動する(repeat)。
				if item != nil {
					item.CandidateTopicID = target
				}
				record(assignmentDecision{ItemID: nodeID, RequestedParentID: target, SelectedParentID: current, Confidence: confidence, Source: assignmentSourceModel, Decision: assignmentDeferredHysteresis, Status: classificationAssigned, CandidateTopicID: target})
				continue
			}
			decision := assignmentAccepted
			if repeat {
				decision = assignmentAcceptedRepeat
			}
			ac.parents[nodeID] = target
			setMeta(item, classificationAssigned, assignmentSourceModel, "", confidence, reason)
			record(assignmentDecision{ItemID: nodeID, RequestedParentID: target, SelectedParentID: target, Confidence: confidence, Source: assignmentSourceModel, Decision: decision, Status: classificationAssigned})
		}
	}
}

// promotionContext bundles the state promoteEmergingCandidates reads/mutates.
type promotionContext struct {
	candidates        []emergingTopicCandidate
	parents           map[string]string
	details           map[string]liveAnalysisTreeNode
	labelIndex        map[string]string
	addTopic          func(liveAnalysisTreeNode)
	dynamicTopicCount *int
	itemAt            func(string) *liveAnalysisItem
	round             int64
	cfg               TreeClassificationConfig
	stats             *liveAnalysisTreeMergeStats
}

// promoteEmergingCandidates promotes candidates that satisfy the evidence
// conditions (PromotionMinItems現存item・PromotionMinRoundsラウンド)を
// dynamic topic へ昇格させ、証拠itemを追加論点から新topicへ付け替える。
// 既存topicとラベルが重複するようになった候補は、そのtopicへ吸収する。
// 昇格は1ラウンドに maxPromotionsPerRound 件まで。
func promoteEmergingCandidates(pc promotionContext) []emergingTopicCandidate {
	record := func(d emergingDecision) {
		if pc.stats != nil {
			pc.stats.EmergingDecisions = append(pc.stats.EmergingDecisions, d)
		}
	}
	detailIDs := make(map[string]struct{}, len(pc.details))
	for id := range pc.details {
		detailIDs[id] = struct{}{}
	}
	// 昇格順は決定的に: 先に生まれた候補から。
	order := make([]int, len(pc.candidates))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		if pc.candidates[order[a]].FirstRound != pc.candidates[order[b]].FirstRound {
			return pc.candidates[order[a]].FirstRound < pc.candidates[order[b]].FirstRound
		}
		return pc.candidates[order[a]].ID < pc.candidates[order[b]].ID
	})

	reparentEvidence := func(candidate emergingTopicCandidate, topicID string) {
		for _, itemID := range candidate.EvidenceItemIDs {
			current := pc.parents[itemID]
			if current != "" && current != treeUnclassifiedTopicID {
				continue
			}
			pc.parents[itemID] = topicID
			if item := pc.itemAt(itemID); item != nil {
				item.ClassificationStatus = classificationAssigned
				item.AssignmentSource = assignmentSourceRule
				item.CandidateTopicID = ""
			}
		}
	}

	promotions := 0
	removed := make(map[string]struct{})
	for _, at := range order {
		candidate := &pc.candidates[at]
		pruneCandidateEvidence(candidate, detailIDs)
		if len(candidate.EvidenceItemIDs) < pc.cfg.PromotionMinItems || candidate.RoundCount < pc.cfg.PromotionMinRounds {
			continue
		}
		if existingID, dup := pc.labelIndex[normalizeForMatch(candidate.Label)]; dup {
			reparentEvidence(*candidate, existingID)
			removed[candidate.ID] = struct{}{}
			record(emergingDecision{CandidateID: candidate.ID, EvidenceItemCount: len(candidate.EvidenceItemIDs), RoundCount: candidate.RoundCount, Decision: emergingFoldedIntoExisting, TopicID: existingID})
			continue
		}
		if *pc.dynamicTopicCount >= pc.cfg.MaxDynamicTopics {
			record(emergingDecision{CandidateID: candidate.ID, EvidenceItemCount: len(candidate.EvidenceItemIDs), RoundCount: candidate.RoundCount, Decision: emergingRejectedTopicCap})
			continue
		}
		if promotions >= maxPromotionsPerRound {
			record(emergingDecision{CandidateID: candidate.ID, EvidenceItemCount: len(candidate.EvidenceItemIDs), RoundCount: candidate.RoundCount, Decision: emergingDeferredPromoteCap})
			continue
		}
		pc.addTopic(liveAnalysisTreeNode{
			ID:          candidate.ID,
			Kind:        "topic",
			Label:       candidate.Label,
			Description: candidate.Description,
			Origin:      topicOriginDynamic,
		})
		pc.labelIndex[normalizeForMatch(candidate.Label)] = candidate.ID
		reparentEvidence(*candidate, candidate.ID)
		*pc.dynamicTopicCount++
		promotions++
		removed[candidate.ID] = struct{}{}
		if pc.stats != nil {
			stats := pc.stats
			stats.DiffNewNodes++
			stats.DynamicTopicsPromoted++
		}
		record(emergingDecision{CandidateID: candidate.ID, EvidenceItemCount: len(candidate.EvidenceItemIDs), RoundCount: candidate.RoundCount, Decision: emergingPromoted, TopicID: candidate.ID})
	}
	if len(removed) == 0 {
		return pc.candidates
	}
	kept := make([]emergingTopicCandidate, 0, len(pc.candidates))
	for _, candidate := range pc.candidates {
		if _, drop := removed[candidate.ID]; !drop {
			kept = append(kept, candidate)
		}
	}
	return kept
}

// syncItemsWithReorganizedTree updates item classification metadata after a
// reorganizer pass changed parents: moved items are marked as decided by the
// reorganizer, and their tentative candidate is cleared when it was applied.
func syncItemsWithReorganizedTree(items []liveAnalysisItem, before, after *liveAnalysisTree) {
	if after == nil {
		return
	}
	previousParents := make(map[string]string)
	if before != nil {
		for _, node := range before.Nodes {
			previousParents[node.ID] = node.ParentID
		}
	}
	afterParents := make(map[string]string, len(after.Nodes))
	for _, node := range after.Nodes {
		afterParents[node.ID] = node.ParentID
	}
	for i := range items {
		parent, ok := afterParents[items[i].ID]
		if !ok || parent == previousParents[items[i].ID] {
			continue
		}
		items[i].AssignmentSource = assignmentSourceReorganizer
		if parent == treeUnclassifiedTopicID {
			items[i].ClassificationStatus = classificationUnclassified
		} else {
			items[i].ClassificationStatus = classificationAssigned
			if items[i].CandidateTopicID == parent {
				items[i].CandidateTopicID = ""
			}
		}
	}
}

// syncItemClassificationWithTree reconciles item classification metadata with
// the invariant-enforced final tree, so the persisted item state can never
// contradict the persisted parent (e.g. topicがcapで消えて追加論点へ退避した
// 場合の降格や、旧payload由来itemのステータス補完)。
func syncItemClassificationWithTree(items []liveAnalysisItem, tree *liveAnalysisTree) {
	if tree == nil {
		return
	}
	parents := make(map[string]string, len(tree.Nodes))
	for _, node := range tree.Nodes {
		parents[node.ID] = node.ParentID
	}
	for i := range items {
		parent, ok := parents[items[i].ID]
		if !ok {
			continue
		}
		if parent == treeUnclassifiedTopicID {
			switch items[i].ClassificationStatus {
			case classificationTentative, classificationUnclassified:
			case classificationAssigned:
				items[i].ClassificationStatus = classificationUnclassified
				items[i].AssignmentSource = assignmentSourceFallback
			default:
				items[i].ClassificationStatus = classificationUnclassified
			}
			continue
		}
		if parent != "" && parent != treeRootNodeID {
			items[i].ClassificationStatus = classificationAssigned
			if items[i].CandidateTopicID == parent {
				items[i].CandidateTopicID = ""
			}
		}
	}
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
		Origin:      topicOriginSystem,
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
				ID:     treeUnclassifiedTopicID,
				Kind:   "topic",
				Label:  treeUnclassifiedTopicLabel,
				Origin: topicOriginSystem,
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
//
// 再編成にも分類ポリシーの制約を適用する:
//   - create_topicは、同じバッチ内でPromotionMinItems件以上のmove_nodeが
//     そのtopicへ移される場合だけ有効(1ノードのための新topicを作らせない)。
//   - dynamic topic数はMaxDynamicTopicsを超えない。
//   - agenda topicはrename・merge元にできない(stable IDとユーザー入力の保護)。
func applyTreeOperations(tree *liveAnalysisTree, mc *meetingContext, operations []treeOperation, cfg TreeClassificationConfig, stats *liveAnalysisTreeMergeStats) (*liveAnalysisTree, int) {
	if tree == nil {
		return nil, 0
	}
	cfg = cfg.normalized()
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
	isAgendaTopic := func(id string) bool {
		if _, ok := agendaIDs[id]; ok {
			return true
		}
		// mcが無い呼び出しでもagenda IDの形は保護する(agenda-N はサーバー採番)。
		return strings.HasPrefix(id, agendaTopicIDPrefix)
	}
	dynamicTopicCount := 0
	for id, topic := range topics {
		origin := topic.Origin
		if origin == "" {
			origin = deriveTopicOrigin(id, agendaIDs)
			if isAgendaTopic(id) {
				origin = topicOriginAgenda
			}
			topic.Origin = origin
			topics[id] = topic
		}
		if origin == topicOriginDynamic {
			dynamicTopicCount++
		}
	}
	reject := func(reason string) {
		if stats == nil {
			return
		}
		if stats.ReorganizeRejections == nil {
			stats.ReorganizeRejections = make(map[string]int)
		}
		stats.ReorganizeRejections[reason]++
	}

	applied := 0
	if len(operations) > treeReorganizeMaxOperations {
		operations = operations[:treeReorganizeMaxOperations]
	}
	// create_topicの証拠条件: 同一バッチでそのtopicへ移されるノード数を先に数える。
	movesInto := make(map[string]int)
	for _, op := range operations {
		if strings.TrimSpace(strings.ToLower(op.Type)) != "move_node" {
			continue
		}
		nodeID := strings.TrimSpace(op.NodeID)
		if _, isDetail := details[nodeID]; !isDetail {
			continue
		}
		movesInto[strings.TrimSpace(op.ToParentID)]++
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
			// 証拠条件: 移すノードが足りない新topicは作らない(単一ノードの
			// ためのtopic生成が実会議でゴミtopicを残した実績への対策)。
			if movesInto[id] < cfg.PromotionMinItems {
				reject("create_topic_insufficient_moves")
				continue
			}
			if dynamicTopicCount >= cfg.MaxDynamicTopics {
				reject("create_topic_dynamic_cap")
				continue
			}
			topics[id] = liveAnalysisTreeNode{ID: id, Kind: "topic", Label: label, Description: truncateRunes(strings.TrimSpace(op.Description), liveAnalysisTreeDescriptionMaxRunes), Origin: topicOriginDynamic}
			topicOrder = append(topicOrder, id)
			dynamicTopicCount++
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
			// アジェンダtopicのラベルはユーザー入力なので書き換えさせない。
			if isAgendaTopic(topicID) {
				reject("rename_agenda_topic")
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
			if isAgendaTopic(fromID) || fromID == treeUnclassifiedTopicID {
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
