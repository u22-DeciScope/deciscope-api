package application

import (
	"crypto/sha256"
	"encoding/hex"
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
//   - 構造は root(1つ) → topic(親はrootのみ) → group(入れ子可) → 詳細
//     ノードに制御する。通常は深さ4以内、十分な根拠と過密がある場合だけ
//     深さ5を許可する。詳細ノードを親にはできない。
//   - 分類できない詳細ノードは「最新topic」ではなく専用の未分類topic
//     (topic-unclassified)へ接続する。

// treeAssignment is a model-proposed parent assignment for one node.
type treeAssignment struct {
	NodeID                 string  `json:"nodeId"`
	ItemID                 string  `json:"itemId"` // alias: some models answer itemId
	ParentTopicID          string  `json:"parentTopicId"`
	Confidence             float64 `json:"confidence"`
	Reason                 string  `json:"reason"`
	ModelNodeID            string  `json:"-"`
	ServerSource           string  `json:"-"`
	EvidenceSequenceNos    []int64 `json:"-"`
	ResolvedAgendaSpanMode string  `json:"-"`
}

func (a treeAssignment) nodeID() string {
	if id := strings.TrimSpace(a.NodeID); id != "" {
		return id
	}
	return strings.TrimSpace(a.ItemID)
}

func containsExactString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// treeHealth summarizes the shape of the finished tree. It drives the
// reorganization trigger and the per-round metrics log, replacing the old
// root-only flatness flag with checks over every topic.
type treeHealth struct {
	TopicCount           int
	GroupCount           int
	DetailCount          int
	UnclassifiedChildren int
	MaxTopicChildren     int
	MaxTopicID           string
	// MaxConcentration is MaxTopicChildren / DetailCount (0 when no details).
	MaxConcentration       float64
	MaxChildren            int
	MaxChildrenParentID    string
	MaxGroupChildren       int
	MaxGroupID             string
	FlatTopicCount         int
	SingleChildGroupCount  int
	NestedGroupCount       int
	AverageDepth           float64
	AverageBranchingFactor float64
}

type groupCandidateDecision struct {
	ParentID                 string
	CandidateLabelHash       string
	CandidateItemCount       int
	ValidEvidenceItemCount   int
	TotalDetailItems         int
	EligibleDetailItems      int
	ExcludedDetailItems      int
	ExcludedByKind           int
	ExcludedByClassification int
	ExcludedByEvidence       int
	ExcludedByParent         int
	ExcludedByResolution     int
	SemanticClusterCount     int
	GroupCandidates          int
	GroupsCreated            int
	Result                   string
	Reason                   string
}

const (
	treeReorganizeMaxTopicChildren     = 8
	treeReorganizeConcentrationMin     = 0.5
	treeReorganizeConcentrationDetails = 6
	treeReorganizeUnclassifiedMin      = 5
	// A normal discussion should fit root→topic→group→subgroup→detail.
	// One additional group level is reserved for an already-overcrowded
	// subgroup with at least three evidence items; depth can never exceed 5.
	treeSoftMaxDepth                    = 4
	treeHardMaxDepth                    = 5
	treeMaxChildrenBeforeGrouping       = 4
	treeHardDepthMinEvidence            = 3
	groupFlattenGraceVersions     int64 = 2
)

// needsReorganization reports whether the tree shape warrants a local
// reorganization pass: an overcrowded topic, a topic holding most of all
// detail nodes, or a growing unclassified backlog.
func (h treeHealth) needsReorganization() bool {
	if h.MaxTopicChildren >= treeReorganizeMaxTopicChildren {
		return true
	}
	if h.MaxGroupChildren >= treeMaxChildrenBeforeGrouping {
		return true
	}
	if h.DetailCount >= treeReorganizeConcentrationDetails && h.MaxConcentration >= treeReorganizeConcentrationMin {
		return true
	}
	return h.UnclassifiedChildren >= treeReorganizeUnclassifiedMin
}

func (h treeHealth) String() string {
	return fmt.Sprintf("topics=%d groups=%d nestedGroups=%d details=%d unclassified=%d maxTopicChildren=%d maxTopicId=%s maxGroupChildren=%d maxGroupId=%s maxChildren=%d maxChildrenParentId=%s maxConcentration=%.2f flatTopics=%d singleChildGroups=%d averageDepth=%.2f averageBranchingFactor=%.2f",
		h.TopicCount, h.GroupCount, h.NestedGroupCount, h.DetailCount, h.UnclassifiedChildren, h.MaxTopicChildren, h.MaxTopicID, h.MaxGroupChildren, h.MaxGroupID, h.MaxChildren, h.MaxChildrenParentID, h.MaxConcentration, h.FlatTopicCount, h.SingleChildGroupCount, h.AverageDepth, h.AverageBranchingFactor)
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
	groupOrder := make([]string, 0)
	groups := make(map[string]liveAnalysisTreeNode)
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
	addGroup := func(node liveAnalysisTreeNode) {
		if _, exists := groups[node.ID]; !exists {
			groupOrder = append(groupOrder, node.ID)
		}
		groups[node.ID] = node
	}
	for _, node := range prevNodes {
		if node.ID == treeRootNodeID {
			continue
		}
		if node.Kind == "topic" {
			addTopic(node)
		} else if node.Kind == "group" {
			addGroup(node)
		} else {
			addDetail(node)
		}
	}

	agendaIDs := make(map[string]struct{})
	actionSummaryIDs := make(map[string]struct{})
	if mc != nil {
		for _, item := range mc.Agenda {
			agendaIDs[item.ID] = struct{}{}
			if effectiveAgendaRole(item.Role, item.Title, "") == agendaRoleActionSummary {
				actionSummaryIDs[item.ID] = struct{}{}
			}
		}
	}
	// action_summary agendas are projections, not canonical tree nodes. Remove
	// legacy nodes during the next merge and detach any legacy children so the
	// normal assignment/rescue pass can place them under a content topic.
	if len(actionSummaryIDs) > 0 {
		filtered := topicOrder[:0]
		for _, id := range topicOrder {
			if _, actionSummary := actionSummaryIDs[id]; actionSummary {
				delete(topics, id)
				delete(parents, id)
				continue
			}
			filtered = append(filtered, id)
		}
		topicOrder = filtered
		for id, parentID := range parents {
			if _, actionSummary := actionSummaryIDs[parentID]; actionSummary {
				parents[id] = ""
			}
		}
	}

	// Agenda topics are immutable server-owned containers. Reassert every
	// field and root parent each round so a legacy rename, move, or kind
	// collision cannot survive the next canonical rebuild.
	if mc != nil {
		for _, item := range mc.Agenda {
			if effectiveAgendaRole(item.Role, item.Title, "") == agendaRoleActionSummary {
				continue
			}
			if previousTopic, exists := topics[item.ID]; exists && stats != nil {
				if previousTopic.Kind != "topic" || previousTopic.Label != item.Title || parents[item.ID] != treeRootNodeID {
					stats.FixedAgendaMutationRejected++
				}
			}
			addTopic(liveAnalysisTreeNode{ID: item.ID, Kind: "topic", Label: item.Title, Origin: topicOriginAgenda, AgendaRole: agendaRolePrimary})
			parents[item.ID] = treeRootNodeID
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
		if _, isAgenda := agendaIDs[id]; isAgenda && topic.AgendaRole == "" && mc != nil {
			for _, item := range mc.Agenda {
				if item.ID == id {
					topic.AgendaRole = normalizeAgendaRole(item.Role)
					topics[id] = topic
					break
				}
			}
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
		if _, collision := topics[item.ID]; collision {
			if stats != nil {
				stats.CrossKindIDCollisions++
				stats.DuplicateNodeIDsDetected++
			}
			continue
		}
		if _, collision := groups[item.ID]; collision {
			if stats != nil {
				stats.CrossKindIDCollisions++
				stats.DuplicateNodeIDsDetected++
			}
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
			if resolvableItemKind(item.Kind) {
				node.Status = "resolved"
			} else {
				node.Status = "open"
			}
		case "updated":
			node.Status = "updated"
		default:
			node.Status = "open"
		}
		node.RelatedItemIDs = normalizeLiveAnalysisRelatedItemIDs(node.RelatedItemIDs, node.ID, itemIDs)
		addDetail(node)
	}

	// Resolved ids mark both details and topics (topics stay in place).
	for id := range resolvedIDs {
		if node, ok := details[id]; ok && resolvableItemKind(node.Kind) {
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
	for id, topic := range topics {
		for _, alias := range topic.ModelTopicIDs {
			if alias != "" && alias != id {
				topicAlias[alias] = id
			}
		}
	}
	labelIndex := make(map[string]string, len(topics))
	for id, topic := range topics {
		labelIndex[normalizeForMatch(topic.Label)] = id
	}
	candidates := append([]emergingTopicCandidate(nil), priorCandidates...)
	candidateAlias := make(map[string]string)
	initialCandidateIDs := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		initialCandidateIDs[candidate.ID] = struct{}{}
	}
	proposedCandidateIDs := make(map[string]struct{})
	candidateIndexByID := func(id string) int {
		if alias, ok := candidateAlias[id]; ok {
			id = alias
		}
		for i := range candidates {
			if candidates[i].ID == id || containsExactString(candidates[i].ModelTopicIDs, id) {
				return i
			}
		}
		return -1
	}
	addModelTopicID := func(candidate *emergingTopicCandidate, id string) {
		if candidate == nil || id == "" || id == candidate.ID || containsExactString(candidate.ModelTopicIDs, id) {
			return
		}
		candidate.ModelTopicIDs = append(candidate.ModelTopicIDs, id)
		sort.Strings(candidate.ModelTopicIDs)
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
	candidateIndexBySemantic := func(label, description string) int {
		bestAt, bestScore := -1, 0.0
		value := strings.TrimSpace(label + " " + description)
		core := emergingTopicCore(label)
		for i := range candidates {
			score := semanticItemSimilarity(value, candidates[i].Label+" "+candidates[i].Description)
			candidateCore := emergingTopicCore(candidates[i].Label)
			coreScore := semanticItemSimilarity(core, candidateCore)
			if len([]rune(core)) >= 3 && len([]rune(candidateCore)) >= 3 &&
				(strings.Contains(core, candidateCore) || strings.Contains(candidateCore, core)) && score < 0.75 {
				score = 0.75
			}
			if coreScore < 0.30 {
				continue
			}
			if score > bestScore {
				bestAt, bestScore = i, score
			}
		}
		if bestScore < 0.30 {
			return -1
		}
		return bestAt
	}
	recordEmerging := func(d emergingDecision) {
		if stats != nil {
			if d.SubjectKey == "" {
				for _, candidate := range candidates {
					if candidate.ID == d.CandidateID {
						_, d.SubjectKey = canonicalCandidateID(candidate.Label, candidate.Description)
						break
					}
				}
			}
			stats.EmergingDecisions = append(stats.EmergingDecisions, d)
		}
	}
	newCandidatesThisRound := 0
	for _, proposed := range newTopics {
		label := truncateRunes(strings.TrimSpace(proposed.Label), liveAnalysisTopicLabelMaxRunes)
		if label == "" {
			continue
		}
		// 「以上をまとめます」等の会話制御発話をtopic候補にしない。まとめ・
		// 進行の宣言は議論の主題ではないため、candidateも作らない。
		if isDiscourseOnlyText(label) {
			if stats != nil {
				stats.DiscourseOnlyCandidatesRejected++
			}
			recordEmerging(emergingDecision{CandidateID: normalizeProposedTopicID(proposed.ID, label), Decision: emergingRejectedNoEvidence, Reason: "discourse_only_label"})
			continue
		}
		proposedID := normalizeProposedTopicID(proposed.ID, label)
		if proposedID == "" {
			continue
		}
		// 実在しないagenda IDを新topicとして名乗らせない(stable IDの保護)。
		if strings.HasPrefix(proposedID, agendaTopicIDPrefix) {
			if _, isAgenda := agendaIDs[proposedID]; !isAgenda {
				proposedID = "topic-" + normalizeForMatch(label)
			}
		}
		if _, isDetail := details[proposedID]; isDetail {
			// 既存詳細ノードのidをtopicとして再利用させない(型の安定性)。
			continue
		}
		if existingID, dup := labelIndex[normalizeForMatch(label)]; dup {
			if existingID != proposedID {
				topicAlias[proposedID] = existingID
			}
			continue
		}
		if _, exists := topics[proposedID]; exists {
			continue
		}
		if bootstrap {
			if newCandidatesThisRound >= maxEmergingCandidatesPerRound {
				recordEmerging(emergingDecision{CandidateID: proposedID, Decision: emergingRejectedRoundCap})
				continue
			}
			addTopic(liveAnalysisTreeNode{
				ID:          proposedID,
				Kind:        "topic",
				Label:       label,
				Description: truncateRunes(strings.TrimSpace(proposed.Description), liveAnalysisTreeDescriptionMaxRunes),
				Origin:      topicOriginDynamic,
			})
			labelIndex[normalizeForMatch(label)] = proposedID
			dynamicTopicCount++
			newCandidatesThisRound++
			if stats != nil {
				stats.DiffNewNodes++
			}
			continue
		}
		description := truncateRunes(strings.TrimSpace(proposed.Description), liveAnalysisTreeDescriptionMaxRunes)
		if at := candidateIndexByID(proposedID); at >= 0 {
			addModelTopicID(&candidates[at], proposedID)
			if candidates[at].Description == "" && description != "" {
				candidates[at].Description = description
			}
			proposedCandidateIDs[candidates[at].ID] = struct{}{}
			continue
		}
		candidateID, subjectKey := canonicalCandidateID(label, description)
		if candidateID == "" {
			continue
		}
		candidateAlias[proposedID] = candidateID
		if stats != nil {
			stats.CandidateSubjectKeys = append(stats.CandidateSubjectKeys, subjectKey)
			if proposedID != candidateID {
				stats.CandidateIDsMerged++
			}
		}
		if at := candidateIndexByID(candidateID); at >= 0 {
			addModelTopicID(&candidates[at], proposedID)
			candidates[at].Label = label
			if description != "" {
				candidates[at].Description = description
			}
			proposedCandidateIDs[candidates[at].ID] = struct{}{}
			continue
		}
		if at := candidateIndexByLabel(label); at >= 0 {
			// 同じ意味の候補を別idで数えない。以降の割当が新idで来ても届くよう
			// aliasを張る。
			candidateAlias[proposedID] = candidates[at].ID
			addModelTopicID(&candidates[at], proposedID)
			proposedCandidateIDs[candidates[at].ID] = struct{}{}
			if stats != nil && proposedID != candidates[at].ID {
				stats.CandidateIDsMerged++
			}
			continue
		}
		if at := candidateIndexBySemantic(label, description); at >= 0 {
			candidateAlias[proposedID] = candidates[at].ID
			addModelTopicID(&candidates[at], proposedID)
			proposedCandidateIDs[candidates[at].ID] = struct{}{}
			if stats != nil && proposedID != candidates[at].ID {
				stats.CandidateIDsMerged++
			}
			if candidates[at].Description == "" {
				candidates[at].Description = description
			}
			continue
		}
		if newCandidatesThisRound >= maxEmergingCandidatesPerRound {
			recordEmerging(emergingDecision{CandidateID: candidateID, Decision: emergingRejectedRoundCap, SubjectKey: subjectKey})
			continue
		}
		candidates = append(candidates, emergingTopicCandidate{
			ID:          candidateID,
			Label:       label,
			Description: description,
			ModelTopicIDs: func() []string {
				if proposedID == candidateID {
					return nil
				}
				return []string{proposedID}
			}(),
			FirstRound: round,
		})
		proposedCandidateIDs[candidateID] = struct{}{}
		newCandidatesThisRound++
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
	reconcileCandidateCompanions(items, parents, candidates, round, stats)

	// A candidate becomes durable only together with at least one canonical
	// evidence item. Empty model proposals (including legacy zero-evidence
	// candidates) are removed before promotion and persistence.
	validDetailIDs := make(map[string]struct{}, len(details))
	for id := range details {
		validDetailIDs[id] = struct{}{}
	}
	keptCandidates := make([]emergingTopicCandidate, 0, len(candidates))
	rejectedCandidateIDs := make(map[string]struct{})
	for i := range candidates {
		candidate := &candidates[i]
		pruneCandidateEvidence(candidate, validDetailIDs)
		if len(candidate.EvidenceItemIDs) == 0 {
			rejectedCandidateIDs[candidate.ID] = struct{}{}
			if _, proposed := proposedCandidateIDs[candidate.ID]; proposed {
				recordEmerging(emergingDecision{CandidateID: candidate.ID, Decision: emergingRejectedNoEvidence, Reason: "no_canonical_evidence"})
				if stats != nil {
					stats.CandidateCreationRejectedNoEvidence++
				}
			}
			continue
		}
		decision := emergingUpdated
		if _, existed := initialCandidateIDs[candidate.ID]; !existed {
			decision = emergingCreated
			if stats != nil {
				stats.CandidateCreated++
			}
		}
		recordEmerging(emergingDecision{CandidateID: candidate.ID, EvidenceItemCount: len(candidate.EvidenceItemIDs), RoundCount: candidate.RoundCount, Decision: decision})
		keptCandidates = append(keptCandidates, *candidate)
	}
	candidates = keptCandidates
	if len(rejectedCandidateIDs) > 0 {
		for i := range items {
			if _, rejected := rejectedCandidateIDs[items[i].CandidateTopicID]; !rejected {
				continue
			}
			items[i].ClassificationStatus = classificationUnclassified
			items[i].CandidateTopicID = ""
			items[i].CandidateInactive = false
			parents[items[i].ID] = treeUnclassifiedTopicID
		}
	}

	// Reconciliation-created question/open_issue items and low-confidence
	// model items can arrive without a usable parent proposal. Inherit the
	// primary topic of a strong semantic companion before grouping/promotion.
	reconcileSemanticItemParents(items, parents, topics, groups, stats)

	// 昇格判定: 証拠が揃った候補だけを dynamic topic にする。
	candidates = promoteEmergingCandidates(promotionContext{
		candidates:        candidates,
		parents:           parents,
		details:           details,
		topics:            topics,
		labelIndex:        labelIndex,
		addTopic:          addTopic,
		dynamicTopicCount: &dynamicTopicCount,
		itemAt:            itemAt,
		round:             round,
		cfg:               cfg,
		stats:             stats,
	})
	// Promotion/folding can create the first assigned peer for another item in
	// the same discussion cluster. Reconcile once more so companions move in
	// the same canonical tree version.
	reconcileSemanticItemParents(items, parents, topics, groups, stats)
	candidates = capEmergingCandidates(candidates, maxEmergingCandidates)
	syncCandidateInactive(items, candidates)

	groupOrder = createSemanticDiscussionGroups(items, topics, groups, groupOrder, details, parents, round, stats)

	// Cap detail nodes (active/resolved separately, topics never evicted).
	detailNodes := make([]liveAnalysisTreeNode, 0, len(detailOrder))
	for _, id := range detailOrder {
		detailNodes = append(detailNodes, details[id])
	}
	detailNodes = capLiveAnalysisTreeNodes(detailNodes, liveAnalysisTreeMaxNodes, liveAnalysisTreeMaxResolvedNodes)

	tree := assembleTree(mc, topics, topicOrder, groups, groupOrder, detailNodes, parents, previousParents, relations, round, stats)
	syncItemClassificationWithTree(items, tree)
	syncRelatedAgendaIDs(items, mc, tree, stats)
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

func semanticPrimaryTopic(item *liveAnalysisItem, topics map[string]liveAnalysisTreeNode, agendaOnly bool) (string, float64) {
	if item == nil {
		return "", 0
	}
	text := strings.TrimSpace(item.Title + " " + item.Body)
	bestID, bestScore := "", 0.0
	for id, topic := range topics {
		if id == treeUnclassifiedTopicID || topic.AgendaRole == agendaRoleActionSummary {
			continue
		}
		if agendaOnly && topic.Origin != topicOriginAgenda && !strings.HasPrefix(id, agendaTopicIDPrefix) {
			continue
		}
		score := semanticItemSimilarity(topic.Label+" "+topic.Description, text)
		if titleScore := semanticItemSimilarity(topic.Label, item.Title); titleScore > score {
			score = titleScore
		}
		if core := semanticTopicCore(topic.Label); len([]rune(core)) >= 3 && strings.Contains(semanticTopicCore(text), core) && score < 0.75 {
			score = 0.75
		}
		if score > bestScore {
			bestID, bestScore = id, score
		}
	}
	if bestScore < 0.16 {
		return "", bestScore
	}
	return bestID, bestScore
}

func semanticTopicCore(value string) string {
	core := normalizeForMatch(value)
	for _, generic := range []string{"について", "できる", "すること", "の", "作成", "計画", "実施方法", "実施", "追加", "検討", "新規", "論点", "課題", "調査", "測定", "確認", "決定", "判断", "必要", "対応", "予定", "方法", "内容", "ため", "よう", "こと", "する"} {
		core = strings.ReplaceAll(core, generic, "")
	}
	return core
}

func emergingTopicCore(value string) string {
	core := semanticTopicCore(value)
	for _, generic := range []string{"話題", "論点", "観点", "確認", "関連"} {
		core = strings.ReplaceAll(core, generic, "")
	}
	return core
}

// candidateSubjectCoherenceThreshold は candidate label と証拠itemが同一主題と
// みなせる最小の意味的類似度。semanticPrimaryTopic の下限(0.16)と揃える。
const candidateSubjectCoherenceThreshold = 0.16

// candidateSubjectIncoherenceReason は昇格を保留すべき理由を返す(空なら昇格可)。
// subjectが空・会話制御発話・汎用語のみ、またはlabelと証拠itemの主題が一致
// しない(過半数が不一致、または一致itemが昇格最小件数未満)場合に保留する。
func candidateSubjectIncoherenceReason(candidate emergingTopicCandidate, itemAt func(string) *liveAnalysisItem, cfg TreeClassificationConfig) string {
	label := strings.TrimSpace(candidate.Label)
	if label == "" {
		return "subject_empty"
	}
	if isDiscourseOnlyText(label) {
		return "discourse_only_label"
	}
	if emergingTopicCore(label) == "" && emergingTopicCore(candidate.Description) == "" {
		return "subject_generic_only"
	}
	subject := strings.TrimSpace(label + " " + candidate.Description)
	subjectCore := emergingTopicCore(label)
	total, coherent := 0, 0
	for _, itemID := range candidate.EvidenceItemIDs {
		item := itemAt(itemID)
		if item == nil {
			continue
		}
		total++
		text := strings.TrimSpace(item.Title + " " + item.Body)
		score := semanticItemSimilarity(subject, text)
		itemCore := emergingTopicCore(item.Title)
		if len([]rune(subjectCore)) >= 3 && len([]rune(itemCore)) >= 3 &&
			(strings.Contains(itemCore, subjectCore) || strings.Contains(subjectCore, itemCore)) && score < 0.75 {
			score = 0.75
		}
		// 短いlabelと長い証拠文ではbigram Diceが過小評価になるため、主題語の
		// bigramを1つでも共有していれば同一主題候補として扱う(保留判定は
		// 「全証拠が完全に無関係」という極端なケースだけを対象にする)。
		if score >= candidateSubjectCoherenceThreshold || sharesSubjectBigram(subjectCore, text) {
			coherent++
		}
	}
	if total == 0 {
		return "no_canonical_evidence"
	}
	// 証拠itemが1件もlabelの主題と一致しないcandidateは昇格しない。語彙が
	// 違っても意味的に関連する証拠が集まる正当なcandidateを止めないよう、
	// 「全証拠が不一致」の場合だけ保留する(部分的な混入は監査AIが検出する)。
	if coherent == 0 {
		return "subject_incoherent"
	}
	return ""
}

// sharesSubjectBigram はsubjectの主題語bigramが証拠テキストに1つでも
// 含まれるかを返す。両者とも正規化してから比較する。
func sharesSubjectBigram(subjectCore, text string) bool {
	subjectKey := semanticItemKey(subjectCore)
	textKey := semanticItemKey(text)
	if subjectKey == "" || textKey == "" {
		return false
	}
	for gram := range runeBigrams(subjectKey) {
		if len([]rune(gram)) < 2 {
			continue
		}
		if strings.Contains(textKey, gram) {
			return true
		}
	}
	return false
}

func canonicalCandidateID(label, description string) (string, string) {
	subjectKey := emergingTopicCore(label)
	if subjectKey == "" {
		subjectKey = emergingTopicCore(description)
	}
	if subjectKey == "" {
		return "", ""
	}
	sum := sha256.Sum256([]byte(subjectKey))
	return "candidate-" + hex.EncodeToString(sum[:6]), subjectKey
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
			if len(d.EvidenceSequenceNos) == 0 {
				if item := ac.itemAt(d.ItemID); item != nil {
					d.EvidenceSequenceNos = append([]int64(nil), item.EvidenceSequenceNos...)
				}
			}
			if d.ResolvedAgendaSpanMode == "" && d.Source == assignmentSourceActiveSpan {
				d.ResolvedAgendaSpanMode = agendaContextModeFixed
			}
			if d.AssignmentReason == "" {
				d.AssignmentReason = d.Decision
			}
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
		item.CandidateInactive = false
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
			if ac.candidates[i].ID == id || containsExactString(ac.candidates[i].ModelTopicIDs, id) {
				return &ac.candidates[i]
			}
		}
		return nil
	}
	semanticPeerTopic := func(nodeID string, item *liveAnalysisItem) string {
		if item == nil {
			return ""
		}
		bestTopic, bestScore := "", 0.0
		for peerID := range ac.details {
			if peerID == nodeID {
				continue
			}
			peer := ac.itemAt(peerID)
			if peer == nil {
				continue
			}
			topicID := climbToTopic(ac.parents[peerID])
			topic, ok := ac.topics[topicID]
			if !ok || topicID == treeUnclassifiedTopicID || topic.AgendaRole == agendaRoleActionSummary {
				continue
			}
			score := semanticItemSimilarity(item.Title+" "+item.Body, peer.Title+" "+peer.Body)
			if score > bestScore {
				bestTopic, bestScore = topicID, score
			}
		}
		if bestScore < 0.20 {
			return ""
		}
		return bestTopic
	}
	knownDetailIDs := make([]string, 0, len(ac.details))
	for id := range ac.details {
		knownDetailIDs = append(knownDetailIDs, id)
	}
	detailResolver := newCanonicalReferenceResolver(knownDetailIDs...)

	for _, assignment := range ac.assignments {
		modelNodeID := firstNonEmptyTrimmed(assignment.ModelNodeID, assignment.nodeID())
		nodeID := assignment.nodeID()
		if canonical, _, ok := detailResolver.resolve(nodeID); ok {
			nodeID = canonical
		}
		if nodeID == "" {
			continue
		}
		requested := strings.TrimSpace(assignment.ParentTopicID)
		originalRequested := requested
		if alias, ok := ac.topicAlias[requested]; ok {
			requested = alias
		}
		if requested == "" {
			continue
		}
		if nodeID == requested {
			if ac.stats != nil {
				ac.stats.SelfParentRejected++
			}
			record(assignmentDecision{ModelItemID: modelNodeID, ItemID: nodeID, RequestedParentID: originalRequested, Decision: "rejected_self_parent"})
			continue
		}
		if _, isDetail := ac.details[nodeID]; !isDetail {
			record(assignmentDecision{ModelItemID: modelNodeID, ItemID: nodeID, RequestedParentID: requested, Decision: assignmentRejectedUnknownItem})
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
		if candidateAt(requested) == nil && item != nil && item.CandidateTopicID != "" && candidateAt(item.CandidateTopicID) != nil {
			// Candidate IDs are server-owned. A later model round can still refer
			// to its original proposal ID; the item's persisted candidate binding
			// is the authoritative alias across rounds.
			requested = item.CandidateTopicID
		}
		if assignment.ServerSource == assignmentSourceNoAgendaSpan {
			if candidate := candidateAt(requested); candidate != nil {
				beforeEvidence := len(candidate.EvidenceItemIDs)
				crossKindCompanion := false
				for _, evidenceItemID := range candidate.EvidenceItemIDs {
					evidenceItem := ac.itemAt(evidenceItemID)
					if evidenceItem != nil && item != nil && evidenceItem.Kind != item.Kind {
						crossKindCompanion = true
						break
					}
				}
				candidate.addEvidence(nodeID, ac.round)
				ac.parents[nodeID] = treeUnclassifiedTopicID
				setMeta(item, classificationTentative, assignmentSourceNoAgendaSpan, candidate.ID, confidence, reason)
				if ac.stats != nil {
					if len(candidate.EvidenceItemIDs) > beforeEvidence {
						ac.stats.CandidateEvidenceAdded++
						if beforeEvidence > 0 {
							ac.stats.CompanionCandidateInherited++
						}
						if crossKindCompanion {
							ac.stats.CrossKindCandidateInherited++
						}
					} else {
						ac.stats.CandidateEvidenceDeduplicated++
					}
				}
				record(assignmentDecision{ModelItemID: modelNodeID, ItemID: nodeID, RequestedParentID: originalRequested, SelectedParentID: treeUnclassifiedTopicID, Confidence: confidence, Source: assignmentSourceNoAgendaSpan, Decision: assignmentAcceptedNoAgendaSpan, Status: classificationTentative, CandidateTopicID: candidate.ID, EvidenceSequenceNos: append([]int64(nil), assignment.EvidenceSequenceNos...), ResolvedAgendaSpanMode: assignment.ResolvedAgendaSpanMode, AssignmentReason: reason})
				continue
			}
			// An explicit no-agenda span is still authoritative when a candidate
			// proposal was invalid. Keep the item staged rather than reviving a
			// stale fixed agenda.
			ac.parents[nodeID] = treeUnclassifiedTopicID
			setMeta(item, classificationUnclassified, assignmentSourceNoAgendaSpan, "", confidence, reason)
			record(assignmentDecision{ModelItemID: modelNodeID, ItemID: nodeID, RequestedParentID: originalRequested, SelectedParentID: treeUnclassifiedTopicID, Confidence: confidence, Source: assignmentSourceNoAgendaSpan, Decision: assignmentAcceptedNoAgendaSpan, Status: classificationUnclassified, EvidenceSequenceNos: append([]int64(nil), assignment.EvidenceSequenceNos...), ResolvedAgendaSpanMode: assignment.ResolvedAgendaSpanMode, AssignmentReason: reason})
			continue
		}
		if assignment.ServerSource == assignmentSourceActiveSpan {
			// A model-backed emerging assignment is explicit evidence for an
			// agenda-external subject. The synthetic active-span assignment must
			// not erase that tentative metadata later in the same round.
			if item != nil && item.ClassificationStatus == classificationTentative && candidateAt(item.CandidateTopicID) != nil {
				record(assignmentDecision{ModelItemID: modelNodeID, ItemID: nodeID, RequestedParentID: originalRequested, SelectedParentID: current, Confidence: confidence, Source: assignmentSourceActiveSpan, Decision: assignmentDeferredEmerging, Status: classificationTentative, CandidateTopicID: item.CandidateTopicID})
				continue
			}
			if topic, exists := ac.topics[requested]; exists && requested != treeUnclassifiedTopicID && topic.Origin == topicOriginAgenda && topic.AgendaRole != agendaRoleActionSummary {
				ac.parents[nodeID] = requested
				setMeta(item, classificationAssigned, assignmentSourceActiveSpan, "", confidence, reason)
				record(assignmentDecision{ModelItemID: modelNodeID, ItemID: nodeID, RequestedParentID: originalRequested, SelectedParentID: requested, Confidence: confidence, Source: assignmentSourceActiveSpan, Decision: assignmentAcceptedActiveSpan, Status: classificationAssigned})
				continue
			}
		}
		semanticAgenda, _ := semanticPrimaryTopic(item, ac.topics, true)
		semanticExisting, _ := semanticPrimaryTopic(item, ac.topics, false)
		semanticTarget := semanticAgenda
		if semanticTarget == "" {
			semanticTarget = semanticExisting
		}
		if semanticTarget == "" {
			semanticTarget = semanticPeerTopic(nodeID, item)
		}

		// An explicit emerging-topic reference is typed and evidence-bearing.
		// Stage it before semantic agenda correction; otherwise weak generic
		// overlap (for example "調査") can pull an agenda-external subject into
		// an unrelated fixed agenda and prevent promotion entirely.
		if candidate := candidateAt(requested); candidate != nil {
			beforeEvidence := len(candidate.EvidenceItemIDs)
			candidate.addEvidence(nodeID, ac.round)
			if ac.stats != nil {
				if len(candidate.EvidenceItemIDs) > beforeEvidence {
					ac.stats.CandidateEvidenceAdded++
				} else {
					ac.stats.CandidateEvidenceDeduplicated++
				}
			}
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
			record(assignmentDecision{ModelItemID: modelNodeID, ItemID: nodeID, RequestedParentID: requested, SelectedParentID: selected, Confidence: confidence, Source: assignmentSourceModel, Decision: assignmentDeferredEmerging, Status: status, CandidateTopicID: candidate.ID})
			continue
		}

		// Existing primary agendas outrank an unclassified, redundant emerging,
		// unknown, or action-summary parent when the item text clearly matches.
		_, requestedIsTopic := ac.topics[requested]
		requestedActionSummary := requestedIsTopic && ac.topics[requested].AgendaRole == agendaRoleActionSummary
		requestedUnusable := requested == treeUnclassifiedTopicID || !requestedIsTopic || requestedActionSummary
		if semanticTarget != "" && requestedUnusable {
			ac.parents[nodeID] = semanticTarget
			setMeta(item, classificationAssigned, assignmentSourceRule, "", confidence, reason)
			decision := assignmentCorrectedSemantic
			if requestedActionSummary {
				decision = assignmentRelatedActionSummary
			}
			record(assignmentDecision{ModelItemID: modelNodeID, ItemID: nodeID, RequestedParentID: originalRequested, SelectedParentID: semanticTarget, Confidence: confidence, Source: assignmentSourceRule, Decision: decision, Status: classificationAssigned})
			continue
		}

		// 明示的な未分類提案。
		if requested == treeUnclassifiedTopicID {
			ac.parents[nodeID] = treeUnclassifiedTopicID
			setMeta(item, classificationUnclassified, assignmentSourceModel, "", confidence, reason)
			record(assignmentDecision{ModelItemID: modelNodeID, ItemID: nodeID, RequestedParentID: requested, SelectedParentID: treeUnclassifiedTopicID, Confidence: confidence, Source: assignmentSourceModel, Decision: assignmentAcceptedUnclassified, Status: classificationUnclassified})
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
		if topic := ac.topics[requested]; topic.AgendaRole == agendaRoleActionSummary {
			selected := current
			status := classificationAssigned
			if selected == "" {
				selected = treeUnclassifiedTopicID
				ac.parents[nodeID] = selected
				status = classificationUnclassified
			}
			setMeta(item, status, assignmentSourceRule, "", confidence, reason)
			record(assignmentDecision{ItemID: nodeID, RequestedParentID: requested, SelectedParentID: selected, Confidence: confidence, Source: assignmentSourceRule, Decision: assignmentRelatedActionSummary, Status: status})
			continue
		}

		target := requested
		lowConfidence := confidence > 0 && confidence < ac.cfg.AgendaAssignmentThreshold
		repeat := item != nil && item.CandidateTopicID != "" && item.CandidateTopicID == target

		currentTopic := climbToTopic(current)
		switch {
		case current == target || currentTopic == target:
			// 同じ親の再主張: confidence/理由だけ更新する。
			setMeta(item, classificationAssigned, assignmentSourceModel, "", confidence, reason)
			record(assignmentDecision{ItemID: nodeID, RequestedParentID: target, SelectedParentID: current, Confidence: confidence, Source: assignmentSourceModel, Decision: assignmentAccepted, Status: classificationAssigned})
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

// reconcileCandidateCompanions keeps cross-kind items from the same
// agenda-external subject together. It only extends a candidate that already
// has explicit canonical evidence; it never invents a zero-evidence candidate.
func reconcileCandidateCompanions(items []liveAnalysisItem, parents map[string]string, candidates []emergingTopicCandidate, round int64, stats *liveAnalysisTreeMergeStats) {
	itemAt := make(map[string]*liveAnalysisItem, len(items))
	for i := range items {
		itemAt[items[i].ID] = &items[i]
	}
	for candidateAt := range candidates {
		candidate := &candidates[candidateAt]
		if len(candidate.EvidenceItemIDs) == 0 {
			continue
		}
		anchors := append([]string(nil), candidate.EvidenceItemIDs...)
		for i := range items {
			item := &items[i]
			alreadyEvidence := false
			for _, id := range candidate.EvidenceItemIDs {
				if id == item.ID {
					alreadyEvidence = true
					break
				}
			}
			if alreadyEvidence || item.Status == "dismissed" {
				continue
			}
			if current := parents[item.ID]; current != "" && current != treeUnclassifiedTopicID && item.ClassificationStatus == classificationAssigned {
				continue
			}
			candidateScore := semanticItemSimilarity(candidate.Label+" "+candidate.Description, item.Title+" "+item.Body)
			if !sharesSemanticTopicBigram(candidate.Label+" "+candidate.Description, item.Title+" "+item.Body) {
				continue
			}
			related := false
			for _, anchorID := range anchors {
				anchor := itemAt[anchorID]
				if anchor == nil {
					continue
				}
				companionScore := semanticCompanionScore(*anchor, *item)
				nearEvidence := itemEvidenceWithin(*anchor, *item, 2)
				if companionScore >= 0.55 || (companionScore >= 0.18 && nearEvidence) || (candidateScore >= 0.12 && companionScore >= 0.10 && nearEvidence) {
					related = true
					break
				}
			}
			if !related {
				continue
			}
			beforeEvidence := len(candidate.EvidenceItemIDs)
			candidate.addEvidence(item.ID, round)
			if len(candidate.EvidenceItemIDs) == beforeEvidence {
				if stats != nil {
					stats.CandidateEvidenceDeduplicated++
				}
				continue
			}
			parents[item.ID] = treeUnclassifiedTopicID
			item.ClassificationStatus = classificationTentative
			item.AssignmentSource = assignmentSourceRule
			item.CandidateTopicID = candidate.ID
			item.CandidateInactive = false
			if stats != nil {
				stats.CandidateEvidenceAdded++
				stats.CompanionCandidateInherited++
				if anchor := itemAt[anchors[0]]; anchor != nil && anchor.Kind != item.Kind {
					stats.CrossKindClustered++
					stats.CrossKindCandidateInherited++
				}
			}
		}
	}
}

func sharesSemanticTopicBigram(a, b string) bool {
	aGrams := runeBigrams(emergingTopicCore(a))
	bGrams := runeBigrams(emergingTopicCore(b))
	for gram := range aGrams {
		if _, shared := bGrams[gram]; shared {
			return true
		}
	}
	return false
}

// reconcileSemanticItemParents keeps cross-kind discussion companions as
// separate canonical items while assigning them to one primary topic. Only
// items currently staged under topic-unclassified are considered; existing
// assigned placement and its hysteresis are never overridden here.
func reconcileSemanticItemParents(items []liveAnalysisItem, parents map[string]string, topics map[string]liveAnalysisTreeNode, groups map[string]liveAnalysisTreeNode, stats *liveAnalysisTreeMergeStats) {
	itemAt := make(map[string]*liveAnalysisItem, len(items))
	for i := range items {
		itemAt[items[i].ID] = &items[i]
	}
	topicAncestor := func(nodeID string) string {
		seen := make(map[string]struct{})
		current := parents[nodeID]
		for current != "" {
			if _, loop := seen[current]; loop {
				return ""
			}
			seen[current] = struct{}{}
			if topic, ok := topics[current]; ok {
				if current == treeUnclassifiedTopicID || topic.AgendaRole == agendaRoleActionSummary {
					return ""
				}
				return current
			}
			if _, ok := groups[current]; !ok {
				return ""
			}
			current = parents[current]
		}
		return ""
	}
	for i := range items {
		item := &items[i]
		if item.ClassificationStatus == classificationTentative && item.CandidateTopicID != "" {
			continue
		}
		if parents[item.ID] != "" && parents[item.ID] != treeUnclassifiedTopicID {
			continue
		}

		selected, directScore := "", 0.0
		if item.ClassificationStatus != classificationTentative {
			selected, directScore = semanticPrimaryTopic(item, topics, false)
		}
		bestPeerID, bestPeerTopic, bestPeerScore := "", "", 0.0
		for peerID, peer := range itemAt {
			if peerID == item.ID {
				continue
			}
			peerTopic := topicAncestor(peerID)
			if peerTopic == "" {
				continue
			}
			score := semanticCompanionScore(*item, *peer)
			if score > bestPeerScore {
				bestPeerID, bestPeerTopic, bestPeerScore = peerID, peerTopic, score
			}
		}

		inherited := false
		if bestPeerTopic != "" {
			peer := itemAt[bestPeerID]
			strongPeer := bestPeerScore >= 0.30 || (bestPeerScore >= 0.14 && itemEvidenceWithin(*item, *peer, 2))
			if strongPeer && (selected == "" || bestPeerScore >= directScore) {
				selected = bestPeerTopic
				inherited = true
			}
		}
		if selected == "" {
			continue
		}
		parents[item.ID] = selected
		item.ClassificationStatus = classificationAssigned
		item.AssignmentSource = assignmentSourceRule
		item.CandidateTopicID = ""
		item.CandidateInactive = false
		if stats != nil {
			if inherited {
				stats.CompanionParentInherited++
				if peer := itemAt[bestPeerID]; peer != nil && peer.Kind != item.Kind {
					stats.CrossKindClustered++
				}
			} else {
				stats.SemanticParentCorrected++
			}
		}
	}
}

func semanticCompanionScore(a, b liveAnalysisItem) float64 {
	score := semanticItemSimilarity(a.Title+" "+a.Body, b.Title+" "+b.Body)
	if titleScore := semanticItemSimilarity(a.Title, b.Title); titleScore > score {
		score = titleScore
	}
	return score
}

// createSemanticDiscussionGroups deterministically clusters evidence-bearing
// canonical detail items below the same primary topic. It does not require a
// fixed combination of kinds: issue/risk, question, open state, fact,
// decision, and TODO are different records in one discussion lifecycle.
func createSemanticDiscussionGroups(items []liveAnalysisItem, topics map[string]liveAnalysisTreeNode, groups map[string]liveAnalysisTreeNode, groupOrder []string, details map[string]liveAnalysisTreeNode, parents map[string]string, round int64, stats *liveAnalysisTreeMergeStats) []string {
	// One completed prior version is the minimum hysteresis. This prevents a
	// single noisy extraction from immediately introducing hierarchy.
	if round < 2 {
		return groupOrder
	}
	itemByID := make(map[string]liveAnalysisItem, len(items))
	for _, item := range items {
		itemByID[item.ID] = item
	}
	record := func(d groupCandidateDecision) {
		if stats == nil {
			return
		}
		stats.GroupDecisions = append(stats.GroupDecisions, d)
		if d.Result == "skipped" {
			stats.GroupsSkipped++
			if stats.GroupSkipReasons == nil {
				stats.GroupSkipReasons = make(map[string]int)
			}
			stats.GroupSkipReasons[d.Reason]++
		}
	}
	evidenceRelated := func(a, b liveAnalysisItem) bool {
		for _, left := range a.EvidenceSequenceNos {
			for _, right := range b.EvidenceSequenceNos {
				delta := left - right
				if delta < 0 {
					delta = -delta
				}
				if delta <= 2 {
					return true
				}
			}
		}
		return false
	}

	topicIDs := make([]string, 0, len(topics))
	for id, topic := range topics {
		if id != treeUnclassifiedTopicID && topic.AgendaRole != agendaRoleActionSummary {
			topicIDs = append(topicIDs, id)
		}
	}
	sort.Strings(topicIDs)
	belongsToTopic := func(detailID, topicID string) (bool, bool) {
		parentID := parents[detailID]
		if parentID == topicID {
			return true, true
		}
		seen := make(map[string]struct{})
		for parentID != "" {
			if _, loop := seen[parentID]; loop {
				return false, false
			}
			seen[parentID] = struct{}{}
			if parentID == topicID {
				return true, false
			}
			if _, isGroup := groups[parentID]; !isGroup {
				return false, false
			}
			parentID = parents[parentID]
		}
		return false, false
	}
	for _, topicID := range topicIDs {
		direct := make([]liveAnalysisItem, 0)
		decisionBase := groupCandidateDecision{ParentID: topicID}
		for id := range details {
			belongs, isDirect := belongsToTopic(id, topicID)
			if !belongs {
				continue
			}
			decisionBase.TotalDetailItems++
			if !isDirect {
				decisionBase.ExcludedByParent++
				continue
			}
			if item, ok := itemByID[id]; ok {
				switch item.Kind {
				case "question", "open_issue", "issue", "risk", "todo", "decision", "fact":
				default:
					decisionBase.ExcludedByKind++
					continue
				}
				if item.ClassificationStatus == classificationTentative || item.ClassificationStatus == classificationUnclassified || item.CandidateInactive {
					decisionBase.ExcludedByClassification++
					continue
				}
				if len(item.EvidenceSequenceNos) == 0 {
					decisionBase.ExcludedByEvidence++
					continue
				}
				direct = append(direct, item)
			} else {
				decisionBase.ExcludedByParent++
			}
		}
		decisionBase.EligibleDetailItems = len(direct)
		decisionBase.ExcludedDetailItems = decisionBase.TotalDetailItems - decisionBase.EligibleDetailItems
		sort.Slice(direct, func(i, j int) bool { return direct[i].ID < direct[j].ID })
		if len(direct) < 3 {
			decision := decisionBase
			decision.Result, decision.Reason = "skipped", "insufficient_related_items"
			record(decision)
			continue
		}

		// Connected components keep clustering deterministic while allowing
		// cross-kind discussion companions. Weak evidence proximity is accepted
		// only when the texts also share a meaningful semantic core.
		visited := make([]bool, len(direct))
		clusters := make([][]liveAnalysisItem, 0)
		for start := range direct {
			if visited[start] {
				continue
			}
			queue := []int{start}
			visited[start] = true
			cluster := make([]liveAnalysisItem, 0)
			for len(queue) > 0 {
				at := queue[0]
				queue = queue[1:]
				cluster = append(cluster, direct[at])
				for next := range direct {
					if visited[next] || next == at {
						continue
					}
					if !sharesSemanticTopicBigram(direct[at].Title+" "+direct[at].Body, direct[next].Title+" "+direct[next].Body) {
						continue
					}
					score := semanticCompanionScore(direct[at], direct[next])
					if score < 0.18 && !(score >= 0.08 && evidenceRelated(direct[at], direct[next])) {
						continue
					}
					visited[next] = true
					queue = append(queue, next)
				}
			}
			if len(cluster) >= 3 {
				clusters = append(clusters, cluster)
			}
		}
		decisionBase.SemanticClusterCount = len(clusters)
		decisionBase.GroupCandidates = len(clusters)
		if stats != nil {
			stats.GroupCandidates += len(clusters)
		}
		if len(clusters) == 0 {
			decision := decisionBase
			decision.Result, decision.Reason = "skipped", "insufficient_related_items"
			record(decision)
			continue
		}

		created := false
		for _, cluster := range clusters {
			validEvidence := 0
			targetIDs := make([]string, 0, len(cluster))
			for _, candidate := range cluster {
				targetIDs = append(targetIDs, candidate.ID)
				if len(candidate.EvidenceSequenceNos) > 0 {
					validEvidence++
				}
			}
			anchor := semanticGroupAnchor(cluster)
			label := semanticGroupLabel(anchor.Title)
			hash := sha256.Sum256([]byte(normalizeForMatch(label)))
			decision := decisionBase
			decision.CandidateLabelHash = hex.EncodeToString(hash[:6])
			decision.CandidateItemCount = len(targetIDs)
			decision.ValidEvidenceItemCount = validEvidence
			if label == "" || genericGroupLabel(label) {
				decision.Result, decision.Reason = "skipped", "generic_group_label"
				record(decision)
				continue
			}
			if validEvidence < 3 {
				decision.Result, decision.Reason = "skipped", "insufficient_valid_evidence"
				record(decision)
				continue
			}
			groupID := stableGroupID(topicID, label)
			if _, exists := groups[groupID]; exists {
				decision.Result, decision.Reason = "skipped", "equivalent_group_exists"
				record(decision)
				created = true
				continue
			}
			if len(groups) >= maxGroupsPerMeeting {
				decision.Result, decision.Reason = "skipped", "group_cap"
				record(decision)
				continue
			}
			groups[groupID] = liveAnalysisTreeNode{ID: groupID, Kind: "group", Label: label, Origin: assignmentSourceRule, RelatedItemIDs: targetIDs, CreatedAtVersion: round, UpdatedAtVersion: round}
			groupOrder = append(groupOrder, groupID)
			parents[groupID] = topicID
			for _, id := range targetIDs {
				parents[id] = groupID
			}
			if stats != nil {
				stats.GroupsCreated++
			}
			decision.GroupsCreated = 1
			decision.Result = "created"
			record(decision)
			created = true
		}
		if !created {
			decision := decisionBase
			decision.Result, decision.Reason = "skipped", "no_group_created"
			record(decision)
		}
	}
	return groupOrder
}

func semanticGroupAnchor(items []liveAnalysisItem) liveAnalysisItem {
	if len(items) == 0 {
		return liveAnalysisItem{}
	}
	priority := map[string]int{"issue": 0, "risk": 1, "open_issue": 2, "question": 3, "decision": 4, "todo": 5, "fact": 6}
	best := items[0]
	for _, item := range items[1:] {
		bestPriority, ok := priority[best.Kind]
		if !ok {
			bestPriority = 100
		}
		itemPriority, ok := priority[item.Kind]
		if !ok {
			itemPriority = 100
		}
		if itemPriority < bestPriority || (itemPriority == bestPriority && len([]rune(item.Title)) < len([]rune(best.Title))) {
			best = item
		}
	}
	return best
}

func semanticGroupLabel(value string) string {
	label := strings.Trim(strings.TrimSpace(value), "、。？！? ")
	for _, suffix := range []string{"の未決定", "が未決定", "は未決定", "未決定", "が未確定", "は未確定", "未確定", "が未解決", "は未解決", "未解決", "が決まっていない", "は決まっていない", "を何m/sにするか", "は何m/sか"} {
		label = strings.TrimSpace(strings.TrimSuffix(label, suffix))
	}
	if label == "" {
		label = strings.TrimSpace(value)
	}
	return truncateRunes(label, liveAnalysisTopicLabelMaxRunes)
}

// promotionContext bundles the state promoteEmergingCandidates reads/mutates.
type promotionContext struct {
	candidates        []emergingTopicCandidate
	parents           map[string]string
	details           map[string]liveAnalysisTreeNode
	topics            map[string]liveAnalysisTreeNode
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
		orderKey := func(candidate emergingTopicCandidate) string {
			if len(candidate.ModelTopicIDs) > 0 {
				return candidate.ModelTopicIDs[0]
			}
			return candidate.ID
		}
		return orderKey(pc.candidates[order[a]]) < orderKey(pc.candidates[order[b]])
	})

	reparentEvidence := func(candidate emergingTopicCandidate, topicID string) {
		for _, itemID := range candidate.EvidenceItemIDs {
			current := pc.parents[itemID]
			if current != "" && current != treeUnclassifiedTopicID {
				continue
			}
			if current == topicID {
				continue
			}
			pc.parents[itemID] = topicID
			if item := pc.itemAt(itemID); item != nil {
				item.ClassificationStatus = classificationAssigned
				item.AssignmentSource = assignmentSourceRule
				item.CandidateTopicID = ""
				item.CandidateInactive = false
			}
			if pc.stats != nil {
				pc.stats.PromotedItemsReparented++
				pc.stats.PromotedItemIDs = append(pc.stats.PromotedItemIDs, itemID)
			}
		}
	}
	semanticExistingTopic := func(candidate emergingTopicCandidate) string {
		bestID, bestScore := "", 0.0
		candidateCore := semanticTopicCore(candidate.Label)
		for id, topic := range pc.topics {
			if id == treeUnclassifiedTopicID || topic.AgendaRole == agendaRoleActionSummary {
				continue
			}
			score := semanticItemSimilarity(candidate.Label+" "+candidate.Description, topic.Label+" "+topic.Description)
			if labelScore := semanticItemSimilarity(candidate.Label, topic.Label); labelScore > score {
				score = labelScore
			}
			topicCore := semanticTopicCore(topic.Label)
			if len([]rune(candidateCore)) >= 4 && len([]rune(topicCore)) >= 4 &&
				(strings.Contains(candidateCore, topicCore) || strings.Contains(topicCore, candidateCore)) && score < 0.90 {
				score = 0.90
			}
			if score > bestScore {
				bestID, bestScore = id, score
			}
		}
		if bestScore < 0.72 {
			return ""
		}
		return bestID
	}

	promotions := 0
	removed := make(map[string]struct{})
	for _, at := range order {
		candidate := &pc.candidates[at]
		pruneCandidateEvidence(candidate, detailIDs)
		// A candidate semantically equivalent to an existing agenda/dynamic
		// topic is a classification proposal, not a new topic. Fold it as soon
		// as it is recognized so its tentative items do not accumulate.
		if existingID := semanticExistingTopic(*candidate); existingID != "" {
			reparentEvidence(*candidate, existingID)
			removed[candidate.ID] = struct{}{}
			if pc.stats != nil {
				pc.stats.CandidateFoldedIntoAgenda++
			}
			record(emergingDecision{CandidateID: candidate.ID, EvidenceItemCount: len(candidate.EvidenceItemIDs), RoundCount: candidate.RoundCount, Decision: emergingFoldedIntoExisting, TopicID: existingID})
			continue
		}
		stableLongEnough := candidate.RoundCount >= pc.cfg.PromotionMinRounds
		if len(candidate.EvidenceItemIDs) < pc.cfg.PromotionMinItems || !stableLongEnough {
			if candidate.LastRound > 0 && pc.round-candidate.LastRound >= 4 &&
				(len(candidate.EvidenceItemIDs) < pc.cfg.PromotionMinItems || !stableLongEnough) {
				wasInactive := candidate.Inactive
				candidate.Inactive = true
				if candidate.InactiveSinceRound == 0 {
					candidate.InactiveSinceRound = pc.round
				}
				if pc.stats != nil && !wasInactive {
					pc.stats.StaleCandidatesHidden++
					pc.stats.CandidateInactive++
				}
				if !wasInactive {
					record(emergingDecision{CandidateID: candidate.ID, EvidenceItemCount: len(candidate.EvidenceItemIDs), RoundCount: candidate.RoundCount, Decision: emergingWaitingEvidence, Reason: "inactive_stale_no_evidence_growth"})
				}
			}
			continue
		}
		if existingID, dup := pc.labelIndex[normalizeForMatch(candidate.Label)]; dup {
			reparentEvidence(*candidate, existingID)
			removed[candidate.ID] = struct{}{}
			if pc.stats != nil {
				pc.stats.CandidateFoldedIntoAgenda++
			}
			record(emergingDecision{CandidateID: candidate.ID, EvidenceItemCount: len(candidate.EvidenceItemIDs), RoundCount: candidate.RoundCount, Decision: emergingFoldedIntoExisting, TopicID: existingID})
			continue
		}
		if *pc.dynamicTopicCount >= pc.cfg.MaxDynamicTopics {
			record(emergingDecision{CandidateID: candidate.ID, EvidenceItemCount: len(candidate.EvidenceItemIDs), RoundCount: candidate.RoundCount, Decision: emergingRejectedTopicCap})
			continue
		}
		// 昇格前にcandidate labelと証拠itemの意味的一貫性を検証する。subjectが
		// 空・汎用語のみ・会話制御発話、またはlabelと証拠の主題が一致しない
		// candidateはtopicにしない(候補としては保持し、証拠の入れ替わりを待つ)。
		if reason := candidateSubjectIncoherenceReason(*candidate, pc.itemAt, pc.cfg); reason != "" {
			if pc.stats != nil {
				pc.stats.CandidateSubjectIncoherentDeferred++
			}
			record(emergingDecision{CandidateID: candidate.ID, EvidenceItemCount: len(candidate.EvidenceItemIDs), RoundCount: candidate.RoundCount, Decision: emergingWaitingEvidence, Reason: reason})
			continue
		}
		if promotions >= maxPromotionsPerRound {
			record(emergingDecision{CandidateID: candidate.ID, EvidenceItemCount: len(candidate.EvidenceItemIDs), RoundCount: candidate.RoundCount, Decision: emergingDeferredPromoteCap})
			continue
		}
		pc.addTopic(liveAnalysisTreeNode{
			ID:            candidate.ID,
			Kind:          "topic",
			Label:         candidate.Label,
			Description:   candidate.Description,
			ModelTopicIDs: append([]string(nil), candidate.ModelTopicIDs...),
			Origin:        topicOriginDynamic,
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
			stats.CandidatePromoted++
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

func syncCandidateInactive(items []liveAnalysisItem, candidates []emergingTopicCandidate) {
	inactive := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		inactive[candidate.ID] = candidate.Inactive
	}
	for i := range items {
		items[i].CandidateInactive = items[i].CandidateTopicID != "" && inactive[items[i].CandidateTopicID]
	}
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

// syncRelatedAgendaIDs computes the cross-cutting agenda view without adding
// parent edges or duplicating canonical items. It keeps one representative
// per semantic discussion cluster: an active TODO when present, otherwise an
// active open_issue. Questions, decisions, facts, risks and resolved items
// stay solely in their content tree.
func syncRelatedAgendaIDs(items []liveAnalysisItem, mc *meetingContext, tree *liveAnalysisTree, statsValues ...*liveAnalysisTreeMergeStats) {
	var stats *liveAnalysisTreeMergeStats
	if len(statsValues) > 0 {
		stats = statsValues[0]
	}
	actionSummaryIDs := mc.actionSummaryAgendaIDs()
	logicalActionSummaryID := mc.logicalActionSummaryAgendaID()
	if stats != nil {
		stats.SourceActionSummaryAgendaCount = len(actionSummaryIDs)
		if logicalActionSummaryID != "" {
			stats.LogicalActionSummaryCount = 1
		}
	}
	if logicalActionSummaryID == "" || tree == nil {
		for i := range items {
			items[i].RelatedAgendaIDs = nil
		}
		return
	}
	for i := range tree.Nodes {
		if _, isActionSummary := actionSummaryIDs[tree.Nodes[i].ID]; isActionSummary {
			tree.Nodes[i].RelatedItemIDs = nil
		}
	}
	parents := make(map[string]string)
	kinds := make(map[string]string)
	if tree != nil {
		for _, node := range tree.Nodes {
			parents[node.ID] = node.ParentID
			kinds[node.ID] = node.Kind
		}
	}
	primaryTopic := func(itemID string) string {
		seen := make(map[string]struct{})
		current := parents[itemID]
		for current != "" {
			if _, loop := seen[current]; loop {
				return ""
			}
			seen[current] = struct{}{}
			if kinds[current] == "topic" {
				return current
			}
			current = parents[current]
		}
		return ""
	}
	sameCluster := func(a, b liveAnalysisItem) bool {
		if a.CandidateTopicID != "" && a.CandidateTopicID == b.CandidateTopicID {
			return true
		}
		if parents[a.ID] != "" && parents[a.ID] == parents[b.ID] && kinds[parents[a.ID]] == "group" {
			return true
		}
		if primaryTopic(a.ID) == "" || primaryTopic(a.ID) != primaryTopic(b.ID) {
			return false
		}
		score := semanticCompanionScore(a, b)
		return score >= 0.30 || (score >= 0.14 && itemEvidenceWithin(a, b, 2))
	}

	for i := range items {
		items[i].RelatedAgendaIDs = nil
	}
	representatives := make([]int, 0)
	activeTodos := make([]int, 0)
	for i := range items {
		if items[i].Status == "resolved" {
			if stats != nil {
				if items[i].Kind == "todo" {
					stats.CompletedTodoExcluded++
				} else {
					stats.ResolvedItemsExcluded++
				}
			}
			continue
		}
		if items[i].Kind == "todo" {
			activeTodos = append(activeTodos, i)
			if stats != nil {
				stats.ActionSummaryCandidates++
			}
		}
	}
	representatives = append(representatives, activeTodos...)
	for i := range items {
		if items[i].Status == "resolved" || items[i].Kind != "open_issue" || items[i].ClassificationStatus == classificationUnclassified {
			continue
		}
		if stats != nil {
			stats.ActionSummaryCandidates++
		}
		clustered := false
		for _, representativeAt := range representatives {
			if sameCluster(items[i], items[representativeAt]) {
				clustered = true
				break
			}
		}
		if clustered {
			if stats != nil {
				stats.ClusteredReferences++
			}
			continue
		}
		representatives = append(representatives, i)
		if stats != nil {
			stats.ActiveOpenIssueFallbacks++
		}
	}

	for _, itemAt := range representatives {
		primary := primaryTopic(items[itemAt].ID)
		if logicalActionSummaryID == primary {
			continue
		}
		items[itemAt].RelatedAgendaIDs = []string{logicalActionSummaryID}
		if stats != nil && items[itemAt].Kind == "todo" {
			stats.ActiveTodoReferences++
		}
	}
	if stats != nil {
		stats.RenderedActionItems = len(representatives)
		stats.DeduplicatedActionItems = stats.ActionSummaryCandidates - len(representatives)
		if stats.DeduplicatedActionItems < 0 {
			stats.DeduplicatedActionItems = 0
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
	groups map[string]liveAnalysisTreeNode,
	groupOrder []string,
	detailNodes []liveAnalysisTreeNode,
	parents map[string]string,
	previousParents map[string]string,
	relations []liveAnalysisTreeRelation,
	treeVersion int64,
	stats *liveAnalysisTreeMergeStats,
) *liveAnalysisTree {
	if len(topics) == 0 && len(groups) == 0 && len(detailNodes) == 0 {
		return nil
	}

	root := liveAnalysisTreeNode{
		ID:          treeRootNodeID,
		Kind:        "topic",
		Label:       mc.rootLabel(),
		Description: mc.rootDescription(),
		Origin:      topicOriginSystem,
	}

	// Central typed registry: the same machine ID can belong to exactly one
	// container/detail family. Identity repair should make this a no-op for
	// valid payloads; it is the final defense before serialization.
	for id := range groups {
		if id == treeRootNodeID {
			delete(groups, id)
			if stats != nil {
				stats.CrossKindIDCollisions++
			}
			continue
		}
		if _, collision := topics[id]; collision {
			delete(groups, id)
			if stats != nil {
				stats.CrossKindIDCollisions++
				stats.DuplicateNodeIDsDetected++
			}
		}
	}
	filteredDetails := detailNodes[:0]
	for _, node := range detailNodes {
		_, topicCollision := topics[node.ID]
		_, groupCollision := groups[node.ID]
		if node.ID == treeRootNodeID || topicCollision || groupCollision {
			if stats != nil {
				stats.CrossKindIDCollisions++
				stats.DuplicateNodeIDsDetected++
			}
			continue
		}
		filteredDetails = append(filteredDetails, node)
	}
	detailNodes = filteredDetails

	topicIDs := make(map[string]struct{}, len(topics)+1)
	for id := range topics {
		topicIDs[id] = struct{}{}
	}

	// Groups may nest, but only through other groups. Depth is resolved from
	// the typed parent chain; cycles, detail parents, unknown parents, and a
	// group depth that would force detail nodes beyond the hard limit are
	// discarded. root=0/topic=1, so the deepest valid group is depth 4.
	validGroups := make(map[string]liveAnalysisTreeNode, len(groups))
	groupParents := make(map[string]string, len(groups))
	groupDepths := make(map[string]int, len(groups))
	resolvingGroups := make(map[string]bool, len(groups))
	var resolveGroupDepth func(string) (int, bool)
	resolveGroupDepth = func(id string) (int, bool) {
		if depth, cached := groupDepths[id]; cached {
			return depth, true
		}
		group, exists := groups[id]
		if !exists || strings.TrimSpace(group.Label) == "" || resolvingGroups[id] {
			return 0, false
		}
		resolvingGroups[id] = true
		defer delete(resolvingGroups, id)
		parent := strings.TrimSpace(parents[id])
		depth := 0
		if _, isTopic := topicIDs[parent]; isTopic && parent != treeRootNodeID {
			depth = 2
		} else if _, isGroup := groups[parent]; isGroup {
			parentDepth, valid := resolveGroupDepth(parent)
			if !valid {
				return 0, false
			}
			depth = parentDepth + 1
		} else {
			return 0, false
		}
		if depth >= treeHardMaxDepth {
			return 0, false
		}
		group.Kind = "group"
		groupDepths[id] = depth
		groupParents[id] = parent
		validGroups[id] = group
		return depth, true
	}
	for id := range groups {
		_, _ = resolveGroupDepth(id)
	}

	// climbToContainer resolves a proposed parent to a valid group or topic.
	// Detail nodes found in a legacy detail→detail chain climb through it, but
	// the resulting persisted parent is always a typed container.
	climbToContainer := func(fromID string) string {
		seen := make(map[string]struct{})
		current := fromID
		for current != "" {
			if _, isGroup := validGroups[current]; isGroup {
				return current
			}
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
	enforcedParents := make(map[string]string, len(detailNodes)+len(topics)+len(validGroups))

	// topicの親は常にroot(型逆転・topic循環をここで遮断)。
	for id := range topics {
		enforcedParents[id] = treeRootNodeID
	}
	for id, parent := range groupParents {
		enforcedParents[id] = parent
	}

	for _, node := range detailNodes {
		proposed := parents[node.ID]
		parent := ""
		switch {
		case proposed == "" || proposed == node.ID || proposed == treeRootNodeID:
			// 親なし・自己参照・root直下は許可しない → topic配下へ。
		default:
			parent = climbToContainer(proposed)
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

	// Count descendant details, not merely direct children: a nested group is
	// meaningful when its subtree contains at least two details. Empty groups
	// are removed immediately. A one-detail group is retained for two tree
	// versions before flattening, preventing create/delete churn during live
	// dedup and recap updates.
	directDetailCounts := make(map[string]int, len(validGroups))
	for _, node := range detailNodes {
		if _, isGroup := validGroups[enforcedParents[node.ID]]; isGroup {
			directDetailCounts[enforcedParents[node.ID]]++
		}
	}
	groupChildren := make(map[string][]string, len(validGroups))
	for id, parent := range groupParents {
		if _, nested := validGroups[parent]; nested {
			groupChildren[parent] = append(groupChildren[parent], id)
		}
	}
	descendantMemo := make(map[string]int, len(validGroups))
	var descendantDetails func(string) int
	descendantDetails = func(id string) int {
		if count, cached := descendantMemo[id]; cached {
			return count
		}
		count := directDetailCounts[id]
		for _, childID := range groupChildren[id] {
			count += descendantDetails(childID)
		}
		descendantMemo[id] = count
		return count
	}
	groupIDsByDepth := append([]string(nil), groupOrder...)
	sort.SliceStable(groupIDsByDepth, func(i, j int) bool { return groupDepths[groupIDsByDepth[i]] > groupDepths[groupIDsByDepth[j]] })
	for _, id := range groupIDsByDepth {
		group, exists := validGroups[id]
		if !exists {
			continue
		}
		count := descendantDetails(id)
		flatten := count == 0
		if count == 1 {
			if group.UnderfilledSinceVersion == 0 {
				group.UnderfilledSinceVersion = treeVersion
				validGroups[id] = group
			} else if treeVersion > 0 && treeVersion-group.UnderfilledSinceVersion >= groupFlattenGraceVersions {
				flatten = true
			}
		} else {
			group.UnderfilledSinceVersion = 0
			validGroups[id] = group
		}
		if !flatten {
			continue
		}
		parent := groupParents[id]
		for _, node := range detailNodes {
			if enforcedParents[node.ID] == id {
				enforcedParents[node.ID] = parent
			}
		}
		for childID, childParent := range groupParents {
			if childParent == id {
				groupParents[childID] = parent
				enforcedParents[childID] = parent
			}
		}
		delete(validGroups, id)
		delete(enforcedParents, id)
		if stats != nil {
			stats.GroupsFlattened++
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

	// Assemble node list: root, topics (agenda order), groups (stable creation
	// order), then canonical detail items.
	nodes := make([]liveAnalysisTreeNode, 0, 1+len(topics)+len(validGroups)+len(detailNodes))
	nodes = append(nodes, root)
	for _, id := range topicOrder {
		topic, ok := topics[id]
		if !ok {
			continue
		}
		topic.ParentID = treeRootNodeID
		nodes = append(nodes, topic)
	}
	orderedGroups := append([]string(nil), groupOrder...)
	sort.SliceStable(orderedGroups, func(i, j int) bool {
		return groupDepths[orderedGroups[i]] < groupDepths[orderedGroups[j]]
	})
	for _, id := range orderedGroups {
		group, ok := validGroups[id]
		if !ok {
			continue
		}
		group.ParentID = enforcedParents[id]
		nodes = append(nodes, group)
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
	byID := make(map[string]liveAnalysisTreeNode, len(tree.Nodes))
	for _, node := range tree.Nodes {
		byID[node.ID] = node
	}
	children := make(map[string]int)
	activeDetailChildren := make(map[string]int)
	groupsByTopic := make(map[string]int)
	detailDepthTotal := 0
	topicAncestor := func(id string) string {
		seen := make(map[string]struct{})
		current := id
		for current != "" {
			node, ok := byID[current]
			if !ok {
				return ""
			}
			if node.Kind == "topic" {
				return node.ID
			}
			if _, looped := seen[current]; looped {
				return ""
			}
			seen[current] = struct{}{}
			current = node.ParentID
		}
		return ""
	}
	nodeDepth := func(id string) int {
		depth := 0
		seen := make(map[string]struct{})
		current := id
		for current != "" && current != treeRootNodeID {
			node, ok := byID[current]
			if !ok {
				break
			}
			if _, looped := seen[current]; looped {
				break
			}
			seen[current] = struct{}{}
			depth++
			current = node.ParentID
		}
		return depth
	}
	for _, node := range tree.Nodes {
		if node.ID == treeRootNodeID {
			continue
		}
		children[node.ParentID]++
		switch node.Kind {
		case "topic":
			health.TopicCount++
			continue
		case "group":
			health.GroupCount++
			if byID[node.ParentID].Kind == "group" {
				health.NestedGroupCount++
			}
			groupsByTopic[topicAncestor(node.ID)]++
			continue
		}
		if node.Status == "resolved" {
			// 解決済みノードは過密判定から除外する(再編成対象は活発な議論)。
			continue
		}
		health.DetailCount++
		detailDepthTotal += nodeDepth(node.ID)
		activeDetailChildren[node.ParentID]++
	}
	for parentID, count := range children {
		if count > health.MaxChildren {
			health.MaxChildren = count
			health.MaxChildrenParentID = parentID
		}
	}
	for topicID, count := range activeDetailChildren {
		parentKind := nodeKindByID(tree, topicID)
		if parentKind == "group" {
			if count > health.MaxGroupChildren {
				health.MaxGroupChildren = count
				health.MaxGroupID = topicID
			}
			continue
		}
		if parentKind != "topic" {
			continue
		}
		if topicID == treeUnclassifiedTopicID {
			health.UnclassifiedChildren = count
		}
		if count > health.MaxTopicChildren {
			health.MaxTopicChildren = count
			health.MaxTopicID = topicID
		}
		if count >= treeReorganizeConcentrationDetails && groupsByTopic[topicID] == 0 {
			health.FlatTopicCount++
		}
	}
	for _, node := range tree.Nodes {
		if node.Kind == "group" && children[node.ID] == 1 {
			health.SingleChildGroupCount++
		}
	}
	if health.DetailCount > 0 {
		health.MaxConcentration = float64(health.MaxTopicChildren) / float64(health.DetailCount)
	}
	branchParents := 0
	totalChildren := 0
	for _, count := range children {
		if count > 0 {
			branchParents++
			totalChildren += count
		}
	}
	if branchParents > 0 {
		health.AverageBranchingFactor = float64(totalChildren) / float64(branchParents)
	}
	if health.DetailCount > 0 {
		health.AverageDepth = float64(detailDepthTotal) / float64(health.DetailCount)
	}
	return health
}

func nodeKindByID(tree *liveAnalysisTree, id string) string {
	if tree == nil {
		return ""
	}
	for _, node := range tree.Nodes {
		if node.ID == id {
			return node.Kind
		}
	}
	return ""
}

// treeDepthOf returns the max depth of the enforced tree (root = 0). With
// the controlled group-only nesting shape this is at most treeHardMaxDepth;
// it is computed rather than hard-coded so metrics also expose legacy input.
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
	Type                  string   `json:"type"`
	TopicID               string   `json:"topicId"`
	GroupID               string   `json:"groupId"`
	NodeID                string   `json:"nodeId"`
	NodeIDs               []string `json:"nodeIds"`
	EvidenceItemIDs       []string `json:"evidenceItemIds"`
	Label                 string   `json:"label"`
	Title                 string   `json:"title"`
	Description           string   `json:"description"`
	ToParentID            string   `json:"toParentId"`
	ParentTopicID         string   `json:"parentTopicId"`
	ParentID              string   `json:"parentId"`
	FromTopicID           string   `json:"fromTopicId"`
	IntoTopicID           string   `json:"intoTopicId"`
	requestedTargetIDs    []string
	requestedParentID     string
	aliasResolvedCount    int
	nonCanonicalReference bool
}

type treeOperationEvaluation struct {
	Index              int
	Type               string
	RequestedTargetIDs []string
	TargetIDs          []string
	RequestedParentID  string
	CanonicalParentID  string
	Result             string
	Reason             string
}

const (
	treeOperationApplied  = "applied"
	treeOperationNoop     = "noop"
	treeOperationRejected = "rejected"
	treeOperationInvalid  = "invalid"
	maxGroupsPerTopic     = 6
	maxGroupsPerMeeting   = 24
)

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
//
// applyTreeOperations is the v4 reorganizer write path. Every proposed
// operation receives exactly one applied/noop/rejected/invalid evaluation;
// there are no silent continue branches. create_group is atomic: the backend
// generates its stable ID and moves at least two validated evidence items in
// the same operation.
func applyTreeOperations(tree *liveAnalysisTree, mc *meetingContext, operations []treeOperation, cfg TreeClassificationConfig, stats *liveAnalysisTreeMergeStats, versions ...int64) (*liveAnalysisTree, int) {
	if tree == nil {
		return nil, 0
	}
	cfg = cfg.normalized()
	treeVersion := int64(1)
	if len(versions) > 0 && versions[0] > 0 {
		treeVersion = versions[0]
	}
	nodes, parents, relations := treeStateFromPayloadTree(tree)
	topicOrder, groupOrder, detailOrder := []string{}, []string{}, []string{}
	topics := make(map[string]liveAnalysisTreeNode)
	groups := make(map[string]liveAnalysisTreeNode)
	details := make(map[string]liveAnalysisTreeNode)
	for _, node := range nodes {
		switch {
		case node.ID == treeRootNodeID:
		case node.Kind == "topic":
			topicOrder = appendIfMissing(topicOrder, node.ID)
			topics[node.ID] = node
		case node.Kind == "group":
			groupOrder = appendIfMissing(groupOrder, node.ID)
			groups[node.ID] = node
		default:
			detailOrder = appendIfMissing(detailOrder, node.ID)
			details[node.ID] = node
		}
	}
	previousParents := make(map[string]string, len(parents))
	for id, parent := range parents {
		previousParents[id] = parent
	}

	agendaIDs := make(map[string]struct{})
	fixedAgendaIDs := make(map[string]struct{})
	if mc != nil {
		for _, item := range mc.Agenda {
			agendaIDs[item.ID] = struct{}{}
			if effectiveAgendaRole(item.Role, item.Title, "") != agendaRoleActionSummary {
				fixedAgendaIDs[item.ID] = struct{}{}
			}
		}
	}
	isAgendaTopic := func(id string) bool {
		_, ok := agendaIDs[id]
		return ok || strings.HasPrefix(id, agendaTopicIDPrefix)
	}
	dynamicTopicCount := 0
	for id, topic := range topics {
		if topic.Origin == "" {
			topic.Origin = deriveTopicOrigin(id, agendaIDs)
			if isAgendaTopic(id) {
				topic.Origin = topicOriginAgenda
			}
			topics[id] = topic
		}
		if topic.Origin == topicOriginDynamic {
			dynamicTopicCount++
		}
	}
	knownTreeIDs := make(map[string]struct{}, len(tree.Nodes))
	for _, node := range tree.Nodes {
		knownTreeIDs[node.ID] = struct{}{}
	}
	// A create_topic and its companion moves are one atomic proposal batch;
	// its new machine ID is therefore a valid exact parent reference within
	// this response even though it is not in the input tree yet.
	for _, operation := range operations {
		if strings.EqualFold(strings.TrimSpace(operation.Type), "create_topic") {
			id := strings.TrimSpace(operation.TopicID)
			if strings.HasPrefix(id, "topic-") && !strings.ContainsAny(id, "[]") {
				knownTreeIDs[id] = struct{}{}
			}
		}
	}
	resolver := treeReferenceResolver(tree)
	for i := range operations {
		operations[i] = canonicalizeTreeOperation(operations[i], knownTreeIDs, resolver)
		if stats != nil {
			stats.AliasResolvedTreeOperationIDs += operations[i].aliasResolvedCount
		}
	}

	record := func(index int, op treeOperation, result, reason string) {
		if stats == nil {
			return
		}
		evaluation := treeOperationEvaluation{
			Index:              index,
			Type:               strings.ToLower(strings.TrimSpace(op.Type)),
			RequestedTargetIDs: append([]string(nil), op.requestedTargetIDs...),
			TargetIDs:          operationTargetIDs(op),
			RequestedParentID:  op.requestedParentID,
			CanonicalParentID:  firstNonEmptyTrimmed(op.ParentID, op.ParentTopicID, op.ToParentID, op.IntoTopicID),
			Result:             result,
			Reason:             reason,
		}
		stats.ReorganizeOperations = append(stats.ReorganizeOperations, evaluation)
		switch result {
		case treeOperationApplied:
			stats.ReorganizeApplied++
		case treeOperationNoop:
			stats.ReorganizeNoop++
		case treeOperationRejected:
			stats.ReorganizeRejected++
		case treeOperationInvalid:
			stats.ReorganizeInvalid++
		}
		if result == treeOperationRejected || result == treeOperationInvalid {
			if stats.ReorganizeRejections == nil {
				stats.ReorganizeRejections = make(map[string]int)
			}
			stats.ReorganizeRejections[reason]++
		}
	}
	if stats != nil {
		stats.ReorganizeProposed = len(operations)
	}

	originalOperations := operations
	if len(operations) > treeReorganizeMaxOperations {
		operations = operations[:treeReorganizeMaxOperations]
		for index := treeReorganizeMaxOperations; index < len(originalOperations); index++ {
			record(index, originalOperations[index], treeOperationInvalid, "operation_limit_exceeded")
		}
	}

	topicAncestor := func(id string) string {
		seen := make(map[string]struct{})
		current := id
		for current != "" {
			if _, isTopic := topics[current]; isTopic {
				return current
			}
			if _, loop := seen[current]; loop {
				return ""
			}
			seen[current] = struct{}{}
			current = parents[current]
		}
		return ""
	}
	nodeDepth := func(id string) int {
		depth := 0
		seen := make(map[string]struct{})
		current := id
		for current != "" && current != treeRootNodeID {
			if _, looped := seen[current]; looped {
				return treeHardMaxDepth + 1
			}
			seen[current] = struct{}{}
			depth++
			current = parents[current]
		}
		return depth
	}
	groupCountForTopic := func(topicID string) int {
		count := 0
		for id := range groups {
			if topicAncestor(id) == topicID {
				count++
			}
		}
		return count
	}
	directChildCount := func(parentID string) int {
		count := 0
		for _, parent := range parents {
			if parent == parentID {
				count++
			}
		}
		return count
	}

	// Legacy create_topic evidence is still derived from companion moves.
	movesInto := make(map[string]int)
	for _, op := range operations {
		typeName := strings.ToLower(strings.TrimSpace(op.Type))
		if typeName != "move_node" && typeName != "move_nodes" {
			continue
		}
		for _, id := range operationTargetIDs(op) {
			if _, ok := details[id]; ok {
				movesInto[strings.TrimSpace(op.ToParentID)]++
			}
		}
	}

	applied := 0
	for index, op := range operations {
		typeName := strings.ToLower(strings.TrimSpace(op.Type))
		if op.nonCanonicalReference {
			if stats != nil {
				stats.NonCanonicalNodeIDs++
			}
			record(index, op, treeOperationInvalid, "non_canonical_node_id")
			continue
		}
		if operationHasSelfParent(op) {
			if stats != nil {
				stats.SelfParentOperationsRejected++
			}
			record(index, op, treeOperationRejected, "self_parent")
			continue
		}
		if operationMutatesFixedAgenda(op, fixedAgendaIDs) {
			if stats != nil {
				stats.FixedAgendaOperationsRejected++
				stats.FixedAgendaMutationRejected++
			}
			record(index, op, treeOperationRejected, "fixed_agenda_immutable")
			continue
		}
		switch typeName {
		case "create_group":
			label := truncateRunes(strings.TrimSpace(firstNonEmptyTrimmed(op.Label, op.Title)), liveAnalysisTopicLabelMaxRunes)
			parentID := strings.TrimSpace(firstNonEmptyTrimmed(op.ParentID, op.ParentTopicID, op.ToParentID))
			targetIDs := uniqueNonEmptyIDs(firstNonEmptyIDs(op.EvidenceItemIDs, op.NodeIDs))
			if label == "" {
				record(index, op, treeOperationInvalid, "missing_group_label")
				continue
			}
			if genericGroupLabel(label) {
				record(index, op, treeOperationRejected, "generic_group_label")
				continue
			}
			parentTopic, parentIsTopic := topics[parentID]
			_, parentIsGroup := groups[parentID]
			if (!parentIsTopic && !parentIsGroup) || parentID == treeRootNodeID || (parentIsTopic && parentTopic.AgendaRole == agendaRoleActionSummary) {
				record(index, op, treeOperationInvalid, "unknown_or_invalid_group_parent")
				continue
			}
			if len(targetIDs) < cfg.PromotionMinItems {
				record(index, op, treeOperationRejected, "insufficient_evidence")
				continue
			}
			parentTopicID := parentID
			if parentIsGroup {
				parentTopicID = topicAncestor(parentID)
				if directChildCount(parentID) < treeMaxChildrenBeforeGrouping {
					record(index, op, treeOperationRejected, "parent_group_not_overcrowded")
					continue
				}
				if directChildCount(parentID)-len(targetIDs) < 1 {
					record(index, op, treeOperationRejected, "parent_would_be_single_group_chain")
					continue
				}
			}
			resultingDetailDepth := nodeDepth(parentID) + 2
			if resultingDetailDepth > treeHardMaxDepth {
				record(index, op, treeOperationRejected, "hard_depth_limit")
				continue
			}
			if resultingDetailDepth > treeSoftMaxDepth && len(targetIDs) < treeHardDepthMinEvidence {
				record(index, op, treeOperationRejected, "hard_depth_insufficient_evidence")
				continue
			}
			valid := true
			for _, id := range targetIDs {
				if _, exists := details[id]; !exists {
					if stats != nil {
						stats.UnknownGroupEvidenceIDs++
					}
					record(index, op, treeOperationInvalid, "unknown_node_id")
					valid = false
					break
				}
				if topicAncestor(id) != parentTopicID || parents[id] != parentID {
					record(index, op, treeOperationRejected, "cross_topic_group_evidence")
					valid = false
					break
				}
			}
			if !valid {
				continue
			}
			duplicate := ""
			for id, group := range groups {
				if parents[id] == parentID && normalizeForMatch(group.Label) == normalizeForMatch(label) {
					duplicate = id
					break
				}
			}
			if duplicate != "" {
				record(index, op, treeOperationNoop, "equivalent_group_exists")
				continue
			}
			if len(groups) >= maxGroupsPerMeeting || groupCountForTopic(parentTopicID) >= maxGroupsPerTopic {
				record(index, op, treeOperationRejected, "group_cap")
				continue
			}
			id := stableGroupID(parentID, label)
			if _, collision := topics[id]; collision {
				record(index, op, treeOperationInvalid, "group_id_collision")
				continue
			}
			if _, collision := details[id]; collision {
				record(index, op, treeOperationInvalid, "group_id_collision")
				continue
			}
			if _, exists := groups[id]; exists {
				record(index, op, treeOperationNoop, "group_already_exists")
				continue
			}
			groups[id] = liveAnalysisTreeNode{ID: id, Kind: "group", Label: label, Description: truncateRunes(strings.TrimSpace(op.Description), liveAnalysisTreeDescriptionMaxRunes), Origin: assignmentSourceReorganizer, RelatedItemIDs: append([]string(nil), targetIDs...), CreatedAtVersion: treeVersion, UpdatedAtVersion: treeVersion}
			groupOrder = append(groupOrder, id)
			parents[id] = parentID
			for _, nodeID := range targetIDs {
				parents[nodeID] = id
			}
			applied++
			if stats != nil {
				stats.GroupsCreated++
			}
			record(index, op, treeOperationApplied, "")

		case "move_node", "move_nodes":
			targetIDs := uniqueNonEmptyIDs(operationTargetIDs(op))
			toParent := strings.TrimSpace(op.ToParentID)
			if len(targetIDs) == 0 {
				record(index, op, treeOperationInvalid, "missing_node_id")
				continue
			}
			_, isTopic := topics[toParent]
			_, isGroup := groups[toParent]
			if !isTopic && !isGroup {
				record(index, op, treeOperationInvalid, "unknown_parent_id")
				continue
			}
			if topic, ok := topics[toParent]; ok && topic.AgendaRole == agendaRoleActionSummary {
				record(index, op, treeOperationRejected, "action_summary_parent")
				continue
			}
			invalid := false
			targetTopicID := toParent
			if isGroup {
				targetTopicID = topicAncestor(toParent)
			}
			for _, nodeID := range targetIDs {
				if _, exists := details[nodeID]; !exists {
					record(index, op, treeOperationInvalid, "unknown_node_id")
					invalid = true
					break
				}
				if isGroup && topicAncestor(nodeID) != topicAncestor(toParent) {
					record(index, op, treeOperationRejected, "cross_topic_group_move")
					invalid = true
					break
				}
				sourceTopicID := topicAncestor(nodeID)
				_, sourceAgenda := topics[sourceTopicID]
				_, targetAgenda := topics[targetTopicID]
				sourceAgenda = sourceAgenda && strings.HasPrefix(sourceTopicID, agendaTopicIDPrefix)
				targetAgenda = targetAgenda && strings.HasPrefix(targetTopicID, agendaTopicIDPrefix)
				if sourceAgenda && targetAgenda && sourceTopicID != targetTopicID {
					record(index, op, treeOperationRejected, "cross_primary_agenda")
					invalid = true
					break
				}
			}
			if invalid {
				continue
			}
			moved := 0
			for _, nodeID := range targetIDs {
				if parents[nodeID] == toParent {
					continue
				}
				parents[nodeID] = toParent
				moved++
			}
			if moved == 0 {
				record(index, op, treeOperationNoop, "already_under_requested_parent")
				continue
			}
			applied++
			record(index, op, treeOperationApplied, "")

		case "rename_group":
			id := strings.TrimSpace(firstNonEmptyTrimmed(op.GroupID, op.TopicID))
			label := truncateRunes(strings.TrimSpace(firstNonEmptyTrimmed(op.Label, op.Title)), liveAnalysisTopicLabelMaxRunes)
			group, exists := groups[id]
			if !exists {
				record(index, op, treeOperationInvalid, "unknown_group_id")
				continue
			}
			if label == "" {
				record(index, op, treeOperationInvalid, "missing_group_label")
				continue
			}
			if normalizeForMatch(group.Label) == normalizeForMatch(label) {
				record(index, op, treeOperationNoop, "label_unchanged")
				continue
			}
			group.Label = label
			group.UpdatedAtVersion = treeVersion
			groups[id] = group
			applied++
			record(index, op, treeOperationApplied, "")

		case "delete_empty_group":
			id := strings.TrimSpace(firstNonEmptyTrimmed(op.GroupID, op.TopicID))
			if _, exists := groups[id]; !exists {
				record(index, op, treeOperationInvalid, "unknown_group_id")
				continue
			}
			hasChild := false
			for _, parent := range parents {
				if parent == id {
					hasChild = true
					break
				}
			}
			if hasChild {
				record(index, op, treeOperationRejected, "group_not_empty")
				continue
			}
			delete(groups, id)
			delete(parents, id)
			applied++
			record(index, op, treeOperationApplied, "")

		case "create_topic":
			label := truncateRunes(strings.TrimSpace(firstNonEmptyTrimmed(op.Label, op.Title)), liveAnalysisTopicLabelMaxRunes)
			id := normalizeProposedTopicID(op.TopicID, label)
			if label == "" || id == "" {
				record(index, op, treeOperationInvalid, "missing_topic_identity")
				continue
			}
			if _, exists := topics[id]; exists {
				record(index, op, treeOperationNoop, "topic_already_exists")
				continue
			}
			if _, collision := details[id]; collision {
				record(index, op, treeOperationInvalid, "topic_id_collision")
				continue
			}
			duplicate := false
			for _, topic := range topics {
				duplicate = duplicate || normalizeForMatch(topic.Label) == normalizeForMatch(label)
			}
			if duplicate {
				record(index, op, treeOperationNoop, "equivalent_topic_exists")
				continue
			}
			if movesInto[id] < cfg.PromotionMinItems {
				record(index, op, treeOperationRejected, "create_topic_insufficient_moves")
				continue
			}
			if dynamicTopicCount >= cfg.MaxDynamicTopics {
				record(index, op, treeOperationRejected, "create_topic_dynamic_cap")
				continue
			}
			topics[id] = liveAnalysisTreeNode{ID: id, Kind: "topic", Label: label, Description: truncateRunes(strings.TrimSpace(op.Description), liveAnalysisTreeDescriptionMaxRunes), Origin: topicOriginDynamic}
			topicOrder = append(topicOrder, id)
			parents[id] = treeRootNodeID
			dynamicTopicCount++
			applied++
			record(index, op, treeOperationApplied, "")

		case "rename_topic":
			id := strings.TrimSpace(op.TopicID)
			label := truncateRunes(strings.TrimSpace(firstNonEmptyTrimmed(op.Label, op.Title)), liveAnalysisTopicLabelMaxRunes)
			topic, exists := topics[id]
			if !exists || label == "" {
				record(index, op, treeOperationInvalid, "unknown_topic_or_missing_label")
				continue
			}
			if isAgendaTopic(id) || id == treeUnclassifiedTopicID {
				record(index, op, treeOperationRejected, "rename_agenda_topic")
				continue
			}
			if normalizeForMatch(topic.Label) == normalizeForMatch(label) {
				record(index, op, treeOperationNoop, "label_unchanged")
				continue
			}
			topic.Label = label
			topics[id] = topic
			applied++
			record(index, op, treeOperationApplied, "")

		case "merge_topic":
			fromID := strings.TrimSpace(firstNonEmptyTrimmed(op.FromTopicID, op.TopicID))
			intoID := strings.TrimSpace(op.IntoTopicID)
			if fromID == intoID {
				record(index, op, treeOperationNoop, "same_topic")
				continue
			}
			if _, fromExists := topics[fromID]; !fromExists {
				record(index, op, treeOperationInvalid, "unknown_source_topic")
				continue
			}
			if _, intoExists := topics[intoID]; !intoExists {
				record(index, op, treeOperationInvalid, "unknown_target_topic")
				continue
			}
			if isAgendaTopic(fromID) || fromID == treeUnclassifiedTopicID {
				record(index, op, treeOperationRejected, "merge_protected_topic")
				continue
			}
			for nodeID, parent := range parents {
				if parent == fromID {
					parents[nodeID] = intoID
				}
			}
			delete(topics, fromID)
			applied++
			record(index, op, treeOperationApplied, "")

		default:
			record(index, op, treeOperationInvalid, "unknown_operation_type")
		}
	}
	if applied == 0 {
		return tree, 0
	}
	detailNodes := make([]liveAnalysisTreeNode, 0, len(detailOrder))
	for _, id := range detailOrder {
		detailNodes = append(detailNodes, details[id])
	}
	rebuilt := assembleTree(mc, topics, topicOrder, groups, groupOrder, detailNodes, parents, previousParents, relations, treeVersion, stats)
	integrity := validateTreeIntegrity(rebuilt, nil, mc)
	applyTreeIntegrityStats(stats, integrity)
	if !integrity.Valid {
		if stats != nil {
			stats.TreePayloadRejected++
			stats.PreviousTreePreserved++
		}
		return tree, 0
	}
	return rebuilt, applied
}

func stableGroupID(parentID, label string) string {
	sum := sha256.Sum256([]byte(parentID + "\x00" + normalizeForMatch(label)))
	return "group-" + hex.EncodeToString(sum[:6])
}

func genericGroupLabel(label string) bool {
	switch normalizeForMatch(label) {
	case "その他", "詳細", "関連事項", "項目", "other", "others", "detail", "details", "related":
		return true
	default:
		return false
	}
}

func appendIfMissing(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func firstNonEmptyIDs(values ...[]string) []string {
	for _, candidate := range values {
		if len(candidate) > 0 {
			return candidate
		}
	}
	return nil
}

func uniqueNonEmptyIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
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
	return result
}

func canonicalizeTreeOperation(op treeOperation, knownIDs map[string]struct{}, resolver *canonicalReferenceResolver) treeOperation {
	op.requestedTargetIDs = operationTargetIDs(op)
	op.requestedParentID = firstNonEmptyTrimmed(op.ParentID, op.ParentTopicID, op.ToParentID, op.IntoTopicID)
	resolve := func(value string) string {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return ""
		}
		if trimmed != value || strings.ContainsAny(trimmed, "[]") {
			op.nonCanonicalReference = true
		}
		if _, exact := knownIDs[trimmed]; exact {
			return trimmed
		}
		if canonical, aliased, ok := resolver.resolve(trimmed); ok {
			if aliased {
				op.aliasResolvedCount++
			}
			return canonical
		}
		return trimmed
	}
	resolveMany := func(values []string) []string {
		resolved := make([]string, 0, len(values))
		for _, value := range values {
			resolved = append(resolved, resolve(value))
		}
		return resolved
	}
	typeName := strings.ToLower(strings.TrimSpace(op.Type))
	switch typeName {
	case "create_group":
		op.EvidenceItemIDs = resolveMany(op.EvidenceItemIDs)
		op.NodeIDs = resolveMany(op.NodeIDs)
		op.ParentID = resolve(op.ParentID)
		op.ParentTopicID = resolve(op.ParentTopicID)
		op.ToParentID = resolve(op.ToParentID)
	case "move_node", "move_nodes":
		op.NodeID = resolve(op.NodeID)
		op.NodeIDs = resolveMany(op.NodeIDs)
		op.ToParentID = resolve(op.ToParentID)
	case "rename_group", "delete_empty_group":
		op.GroupID = resolve(op.GroupID)
	case "rename_topic":
		op.TopicID = resolve(op.TopicID)
	case "merge_topic":
		op.FromTopicID = resolve(op.FromTopicID)
		op.TopicID = resolve(op.TopicID)
		op.IntoTopicID = resolve(op.IntoTopicID)
	}
	return op
}

func operationHasSelfParent(op treeOperation) bool {
	parentID := firstNonEmptyTrimmed(op.ParentID, op.ParentTopicID, op.ToParentID, op.IntoTopicID)
	if parentID == "" {
		return false
	}
	for _, id := range operationTargetIDs(op) {
		if id == parentID {
			return true
		}
	}
	return false
}

func operationMutatesFixedAgenda(op treeOperation, fixedAgendaIDs map[string]struct{}) bool {
	if len(fixedAgendaIDs) == 0 {
		return false
	}
	typeName := strings.ToLower(strings.TrimSpace(op.Type))
	var targets []string
	switch typeName {
	case "move_node", "move_nodes", "rename_topic", "rename_group", "delete_empty_group":
		targets = operationTargetIDs(op)
	case "merge_topic":
		targets = []string{firstNonEmptyTrimmed(op.FromTopicID, op.TopicID), op.IntoTopicID}
	default:
		return false
	}
	for _, id := range targets {
		if _, fixed := fixedAgendaIDs[strings.TrimSpace(id)]; fixed {
			return true
		}
	}
	return false
}

func operationTargetIDs(op treeOperation) []string {
	if ids := firstNonEmptyIDs(op.EvidenceItemIDs, op.NodeIDs); len(ids) > 0 {
		return uniqueNonEmptyIDs(ids)
	}
	return uniqueNonEmptyIDs([]string{op.NodeID, op.GroupID, op.TopicID, op.FromTopicID})
}
