package application

import (
	"sort"
	"strings"
)

// このファイルは finalization 時点の「追加論点(topic-unclassified)」直下に残った
// 実itemを、意味的な単位へ接地し直す決定的repairを実装する。
//
// live経路の候補統合(reconcileDiscourseTopicProposals)は、同一話者・近接
// sequence・同一no-agenda spanという discourse 条件を満たす提案しか束ねない。
// 実会議では同じ追加論点が複数話者・数発話にまたがって語られるため、そこで
// 束ねきれなかった候補が追加論点の箱の中でばらばらのまま最終ツリーに残る。
// ここでは会議全体の確定テキストを使い、
//   1. 同一の追加論点を構成するitem群を relation-aware に求める
//   2. その論点全体を既存topicへ接地できるか
//   3. できなければ論点名を作って1つのdynamic topicへmaterializeする
// の順に再評価する。統合は「同じ具体的対象を指していること」を必須条件にし、
// 語の一致・sequence近接・relation edgeの存在だけでは決して束ねない。

// finalUnclassifiedDecision は1件の未分類itemに対する判定。観測ログ専用で、
// 本文(title/body)は載せない。
type finalUnclassifiedDecision struct {
	ItemID        string
	CandidateID   string
	Decision      string
	TopicID       string
	ComponentSize int
	Signals       []string
}

const (
	finalUnclassifiedReparentedExisting = "reparented_existing_topic"
	finalUnclassifiedMaterialized       = "materialized_dynamic_topic"
	finalUnclassifiedRetained           = "retained_under_staging_topic"
)

// finalUnclassifiedEvidenceWindow は「同じ追加論点の連続した語り」とみなせる
// 最大のsequence距離。会議中の一区切り(問題提起→リスク→対応方針→担当割当)は
// 実データでは十数発話に収まるため、主題一致を必須にしたうえでこの窓を使う。
const finalUnclassifiedEvidenceWindow = 12

// finalUnclassifiedGenericWindow は具体的な業務対象を抽出できなかったitem同士に
// 適用する、より狭い窓。表層語の一致だけで束ねないための保険。
const finalUnclassifiedGenericWindow = 3

func repairFinalUnclassifiedItems(
	state *liveAnalysisPayload,
	mc *meetingContext,
	version int64,
	stats *finalRepairStats,
) {
	if state == nil || state.Tree == nil || stats == nil {
		return
	}
	unclassified := liveTreeNodeByID(state.Tree, treeUnclassifiedTopicID)
	if unclassified == nil {
		return
	}

	itemByID := make(map[string]*liveAnalysisItem, len(state.Items))
	for index := range state.Items {
		item := &state.Items[index]
		if item.ID == "" || item.Inactive || item.MergedIntoID != "" ||
			item.Status == "dismissed" {
			continue
		}
		itemByID[item.ID] = item
	}

	memberIDs := make([]string, 0, len(state.Items))
	for _, node := range state.Tree.Nodes {
		if node.Kind == "topic" || node.Kind == "group" {
			continue
		}
		if treeItemTopic(state.Tree, node.ID) != treeUnclassifiedTopicID {
			continue
		}
		item, active := itemByID[node.ID]
		if !active {
			continue
		}
		if treeAuditIsManualChangeSource(node.LastParentChangeSource) {
			// 手動で追加論点へ置かれたitemは自動修復の対象外。観測できるよう
			// 記録だけ残し、親も分類状態も変えない。
			stats.SingletonAttachmentManualPreserved++
			stats.SingletonAttachmentDecisions = append(stats.SingletonAttachmentDecisions, singletonAttachmentDecision{
				ItemID: node.ID, SourceParentID: node.ParentID,
				EvidenceSequenceNos:     append([]int64(nil), item.EvidenceSequenceNos...),
				ManualProtectionPenalty: 1,
				Decision:                singletonAttachmentManualPreserved,
				Reason:                  "manual_parent_change_source",
			})
			continue
		}
		memberIDs = append(memberIDs, node.ID)
	}
	if len(memberIDs) == 0 {
		return
	}
	sort.Strings(memberIDs)

	topics := make(map[string]liveAnalysisTreeNode)
	dynamicTopicCount := 0
	for _, node := range state.Tree.Nodes {
		if node.Kind != "topic" {
			continue
		}
		topics[node.ID] = node
		if node.ID != treeRootNodeID && node.ID != treeUnclassifiedTopicID &&
			deriveTopicOrigin(node) == topicOriginDynamic {
			dynamicTopicCount++
		}
	}

	remaining := memberIDs

	// 1. relation-aware な candidate graph を作り、同一追加論点の connected
	//    component を求める。union には常に「同じ具体的対象」を要求する。
	relations := activeUnclassifiedRelationPairs(state.Tree)
	parent := make([]int, len(remaining))
	for index := range parent {
		parent[index] = index
	}
	var find func(int) int
	find = func(value int) int {
		if parent[value] != value {
			parent[value] = find(parent[value])
		}
		return parent[value]
	}
	componentSignals := make(map[int][]string)
	for left := 0; left < len(remaining); left++ {
		for right := left + 1; right < len(remaining); right++ {
			signals, related := finalUnclassifiedItemsRelated(
				*itemByID[remaining[left]], *itemByID[remaining[right]], relations,
			)
			if !related {
				continue
			}
			leftRoot, rightRoot := find(left), find(right)
			if leftRoot == rightRoot {
				continue
			}
			if rightRoot < leftRoot {
				leftRoot, rightRoot = rightRoot, leftRoot
			}
			parent[rightRoot] = leftRoot
			componentSignals[leftRoot] = appendUniqueStrings(
				append(componentSignals[leftRoot], componentSignals[rightRoot]...), signals...,
			)
			delete(componentSignals, rightRoot)
		}
	}
	components := make(map[int][]string)
	order := make([]int, 0, len(remaining))
	for index, itemID := range remaining {
		root := find(index)
		if _, seen := components[root]; !seen {
			order = append(order, root)
		}
		components[root] = append(components[root], itemID)
	}

	// 2. component ごとに、既存topicへのfold → 新規materialize → 現状維持の
	//    順で処理する。node追加は必ず reparent より前に行う(依存順)。
	//    ここで扱うのは「複数itemから新しいtopicを作る」判断で、単独itemは
	//    3. の singleton attachment(既存topicへ移すだけで箱を増やさない)へ回す。
	//    単独itemから新しいdynamic topicを自動生成することは、引き続き行わない。
	type deferredUnclassifiedComponent struct {
		members []string
		reason  string
	}
	deferred := make([]deferredUnclassifiedComponent, 0, len(order))
	for _, root := range order {
		members := components[root]
		signals := componentSignals[root]
		if len(members) < 2 {
			deferred = append(deferred, deferredUnclassifiedComponent{members: members, reason: "single_additional_point"})
			continue
		}
		// 1つの発話から複数kindが取り出されただけの組は、同じ命題の別側面で
		// あって「複数回にわたって語られた追加論点」ではない。topicを作ると
		// 命題1件に対して箱を1つ増やすだけになるため対象外にする。
		if distinctUnclassifiedEvidenceCount(members, itemByID) < 2 {
			deferred = append(deferred, deferredUnclassifiedComponent{members: members, reason: "single_utterance_facets"})
			continue
		}
		label := unclassifiedComponentTopicLabel(members, itemByID)
		if label == "" || genericTopicLabel(label) || isDiscourseOnlyText(label) {
			deferred = append(deferred, deferredUnclassifiedComponent{members: members, reason: "topic_label_not_derivable"})
			continue
		}

		targetID := groundedExistingTopicForComponent(label, members, itemByID, topics, mc)
		decision := finalUnclassifiedReparentedExisting
		if targetID == "" {
			if dynamicTopicCount >= defaultMaxDynamicTopics ||
				len(state.Tree.Nodes) >= liveAnalysisTreeMaxNodes {
				deferred = append(deferred, deferredUnclassifiedComponent{members: members, reason: "dynamic_topic_budget_exhausted"})
				continue
			}
			targetID = stableDynamicTopicID(unclassifiedComponentCandidateID(members, itemByID))
			if liveTreeNodeByID(state.Tree, targetID) == nil {
				topic := liveAnalysisTreeNode{
					ID: targetID, Kind: "topic", ParentID: treeRootNodeID,
					Label:             label,
					SourceCandidateID: unclassifiedComponentCandidateID(members, itemByID),
					Origin:            topicOriginDynamic,
					CreatedAtVersion:  version, UpdatedAtVersion: version,
				}
				state.Tree.Nodes = append(state.Tree.Nodes, topic)
				topics[targetID] = topic
				dynamicTopicCount++
			}
			decision = finalUnclassifiedMaterialized
			stats.UnclassifiedTopicsMaterialized++
		}
		for _, itemID := range members {
			assignUnclassifiedItemToTopic(
				state, itemByID[itemID], targetID, "final_unclassified_component", version,
			)
			stats.UnclassifiedItemsReparented++
			stats.UnclassifiedDecisions = append(stats.UnclassifiedDecisions, finalUnclassifiedDecision{
				ItemID: itemID, CandidateID: itemByID[itemID].CandidateTopicID,
				Decision: decision, TopicID: targetID, ComponentSize: len(members),
				Signals: signals,
			})
		}
	}

	// 3. component修復のあとに、追加論点の箱へ単独で残ったgrounded itemを既存
	//    topicへ接続する。新規topicは作らないため、componentの
	//    item数>=2 / evidence sequence数>=2 の条件は要求しない。ここで移動した
	//    itemは 4. の edge再構築より前に確定させる。
	singletons := make([]string, 0, len(deferred))
	for _, component := range deferred {
		if len(component.members) == 1 && component.reason == "single_additional_point" {
			singletons = append(singletons, component.members[0])
		}
	}
	attached := attachUnclassifiedSingletonsToExistingTopics(
		state, mc, version, stats, singletons, itemByID,
	)
	for _, component := range deferred {
		remaining := make([]string, 0, len(component.members))
		for _, itemID := range component.members {
			if _, moved := attached[itemID]; !moved {
				remaining = append(remaining, itemID)
			}
		}
		if len(remaining) > 0 {
			retainUnclassifiedItems(state, remaining, itemByID, stats, component.reason)
		}
	}

	// 4. edge再構築 → 空dynamic topicのprune → 空の追加論点箱のprune。
	finalizeUnclassifiedContainer(state)
}

// groundedExistingTopicForComponent は component 全体を既存topicへ接地できる
// 場合だけそのIDを返す。semanticExistingTopicID の緩い fold 経路だけでは表層語
// の一致で別論点へ吸い込まれうるため、具体的な対象語の共有を追加条件にする。
func groundedExistingTopicForComponent(
	label string,
	members []string,
	items map[string]*liveAnalysisItem,
	topics map[string]liveAnalysisTreeNode,
	mc *meetingContext,
) string {
	texts := make([]string, 0, len(members))
	for _, itemID := range members {
		if item, ok := items[itemID]; ok {
			texts = append(texts, strings.TrimSpace(item.Title+" "+item.Body))
		}
	}
	componentText := strings.TrimSpace(strings.Join(texts, " "))
	if componentText == "" {
		return ""
	}
	candidates := make(map[string]liveAnalysisTreeNode, len(topics))
	for id, topic := range topics {
		if id == treeRootNodeID || id == treeUnclassifiedTopicID ||
			topic.AgendaRole == agendaRoleActionSummary {
			continue
		}
		candidates[id] = topic
	}
	targetID := semanticExistingTopicID(label, componentText, candidates)
	if targetID == "" {
		return ""
	}
	topic := candidates[targetID]
	topicText := strings.TrimSpace(topic.Label + " " + topic.Description)
	if inferredAgendaForTopic(topic, mc) != "" && specificSubjectOverlapLength(componentText, topicText) < 2 {
		// アジェンダ系topicへの取り込みは、共有する具体的対象が無いと
		// 「アジェンダ外の話が既存議題へ紛れ込む」誤りになる。
		return ""
	}
	if specificSubjectOverlapLength(componentText, topicText) >= 2 ||
		sharesSemanticTopicBigram(componentText, topicText) {
		return targetID
	}
	return ""
}

// finalUnclassifiedItemsRelated は2つの未分類itemが同じ追加論点かを判定する。
// 「同じ具体的対象を指していること」は常に必須で、relation edge や sequence
// 近接はそれ単独では統合根拠にならない(§候補統合の意味条件)。
func finalUnclassifiedItemsRelated(
	left, right liveAnalysisItem,
	relations map[string]struct{},
) ([]string, bool) {
	leftText := strings.TrimSpace(left.Title + " " + left.Body)
	rightText := strings.TrimSpace(right.Title + " " + right.Body)
	if leftText == "" || rightText == "" {
		return nil, false
	}
	leftSubject := concreteBusinessSubject(leftText)
	rightSubject := concreteBusinessSubject(rightText)

	signals := make([]string, 0, 4)
	window := finalUnclassifiedGenericWindow
	switch {
	case leftSubject != "" && rightSubject != "":
		// 具体的な業務対象が両側で認識できた場合はそれが正本。別対象なら、
		// 述語や時期が一致していても統合しない。
		if normalizeForMatch(leftSubject) != normalizeForMatch(rightSubject) &&
			specificSubjectOverlapLength(leftSubject, rightSubject) < 2 {
			return nil, false
		}
		signals = append(signals, "same_concrete_subject")
		window = finalUnclassifiedEvidenceWindow
	case leftSubject != "" || rightSubject != "":
		// 片側だけ具体的な業務対象を認識できた場合、もう一方がその対象に触れて
		// いなければ別の対象。表層の共通部分文字列(「接続」など)では代替できない。
		subject, otherText := leftSubject, rightText
		if subject == "" {
			subject, otherText = rightSubject, leftText
		}
		if !strings.Contains(normalizeForMatch(otherText), normalizeForMatch(subject)) {
			return nil, false
		}
		signals = append(signals, "shared_concrete_subject_mention")
		window = finalUnclassifiedEvidenceWindow
	case specificSubjectOverlapLength(leftText, rightText) >= 4:
		signals = append(signals, "specific_subject_overlap")
	default:
		return nil, false
	}

	if itemsShareEvidenceSequence(left, right) {
		signals = append(signals, "shared_evidence_sequence")
		return signals, true
	}
	if _, linked := relations[unclassifiedRelationKey(left.ID, right.ID)]; linked {
		signals = append(signals, "semantic_relation_edge")
		return signals, true
	}
	if distance, ok := minimumEvidenceDistance(left, right); ok && distance <= int64(window) {
		signals = append(signals, "evidence_proximity")
		return signals, true
	}
	return nil, false
}

func distinctUnclassifiedEvidenceCount(members []string, items map[string]*liveAnalysisItem) int {
	sequences := make(map[int64]struct{})
	for _, itemID := range members {
		item, ok := items[itemID]
		if !ok {
			continue
		}
		for _, sequenceNo := range item.EvidenceSequenceNos {
			sequences[sequenceNo] = struct{}{}
		}
	}
	return len(sequences)
}

func itemsShareEvidenceSequence(left, right liveAnalysisItem) bool {
	for _, sequenceNo := range left.EvidenceSequenceNos {
		if containsInt64(right.EvidenceSequenceNos, sequenceNo) {
			return true
		}
	}
	return false
}

func minimumEvidenceDistance(left, right liveAnalysisItem) (int64, bool) {
	best, found := int64(0), false
	for _, leftSequence := range left.EvidenceSequenceNos {
		for _, rightSequence := range right.EvidenceSequenceNos {
			distance := leftSequence - rightSequence
			if distance < 0 {
				distance = -distance
			}
			if !found || distance < best {
				best, found = distance, true
			}
		}
	}
	return best, found
}

func activeUnclassifiedRelationPairs(tree *liveAnalysisTree) map[string]struct{} {
	pairs := make(map[string]struct{})
	if tree == nil {
		return pairs
	}
	for _, relation := range tree.Relations {
		if relation.Status == "inactive" || relation.Source == "" || relation.Target == "" {
			continue
		}
		pairs[unclassifiedRelationKey(relation.Source, relation.Target)] = struct{}{}
	}
	return pairs
}

func unclassifiedRelationKey(left, right string) string {
	if left > right {
		left, right = right, left
	}
	return left + "\x00" + right
}

// unclassifiedComponentTopicLabel は component 全体を表すtopic名を作る。
// item本文の切り貼りは使わない: 具体的な業務対象から名詞句を組み立てられた
// ときだけラベルを返し、作れない場合は "" を返して materialize を見送る。
// 「担当者に今週中にVPN証明書の更新対応」のような、特定item一件の文になる
// ラベルや途中で切れた断片を最終ツリーへ出さないための制約。
func unclassifiedComponentTopicLabel(members []string, items map[string]*liveAnalysisItem) string {
	subject := ""
	texts := make([]string, 0, len(members)*2)
	for _, itemID := range members {
		item, ok := items[itemID]
		if !ok {
			continue
		}
		text := strings.TrimSpace(item.Title + " " + item.Body)
		texts = append(texts, text)
		candidate := trimSubjectQualifiers(concreteBusinessSubject(text))
		if candidate == "" {
			continue
		}
		// 最短の対象語を選ぶ。実施者や時期を巻き込んだ長い抽出より、対象そのもの
		// を指す短い名詞句のほうがcomponent全体を表す。
		if subject == "" || len([]rune(candidate)) < len([]rune(subject)) {
			subject = candidate
		}
	}
	if subject == "" {
		return ""
	}
	combined := strings.Join(texts, " ")
	var label string
	switch {
	case strings.Contains(combined, "期限切れ") || strings.Contains(combined, "有効期限") ||
		strings.Contains(combined, "失効"):
		label = subject + "の期限切れ対応"
	case strings.Contains(combined, "更新"):
		label = subject + "の更新対応"
	case strings.Contains(combined, "作成") || strings.Contains(combined, "策定"):
		label = subject + "の作成"
	default:
		label = subject + "の対応"
	}
	label = truncateRunes(label, liveAnalysisTopicLabelMaxRunes)
	if dynamicTopicLabelNeedsRepair(label, label) {
		return ""
	}
	return label
}

// trimSubjectQualifiers は抽出した対象語から、先行する実施者・時期などの
// 修飾を落とす。助詞で区切られた最後の塊だけを対象名として残す。
func trimSubjectQualifiers(subject string) string {
	trimmed := strings.TrimSpace(subject)
	if trimmed == "" {
		return ""
	}
	for _, particle := range []string{"に", "が", "を", "は", "で", "へ", "も", "、", "，", ","} {
		if index := strings.LastIndex(trimmed, particle); index >= 0 {
			rest := strings.TrimSpace(trimmed[index+len(particle):])
			if len([]rune(rest)) >= 2 {
				trimmed = rest
			}
		}
	}
	return trimmed
}

// unclassifiedComponentCandidateID は component の安定IDを、構成itemの候補IDか
// item IDの最小値から決める。同じ入力なら同じtopic IDになる。
func unclassifiedComponentCandidateID(members []string, items map[string]*liveAnalysisItem) string {
	candidateIDs := make([]string, 0, len(members))
	for _, itemID := range members {
		if item, ok := items[itemID]; ok && strings.TrimSpace(item.CandidateTopicID) != "" {
			candidateIDs = append(candidateIDs, strings.TrimSpace(item.CandidateTopicID))
		}
	}
	if len(candidateIDs) > 0 {
		sort.Strings(candidateIDs)
		return candidateIDs[0]
	}
	sorted := append([]string(nil), members...)
	sort.Strings(sorted)
	return "final-unclassified-" + sorted[0]
}

func assignUnclassifiedItemToTopic(
	state *liveAnalysisPayload,
	item *liveAnalysisItem,
	topicID, source string,
	version int64,
) {
	if item == nil {
		return
	}
	if node := liveTreeNodeByID(state.Tree, item.ID); node != nil {
		node.ParentID = topicID
		node.LastParentChangeSource = source
		node.LastParentChangeVersion = version
		node.ParentConfidence = 0.9
	}
	item.ClassificationStatus = classificationAssigned
	item.CandidateTopicID = ""
	item.CandidateInactive = false
	item.AssignmentConfidence = 0.9
	item.AssignmentSource = assignmentSourceRule
	item.AssignmentReason = source
}

// retainUnclassifiedItems は追加論点へ残すitemを記録するだけで、分類状態も
// 本文も変えない。topic名を作れないという理由でgroundedな命題を非表示へ
// 落とすと、ユーザーが実際に議論した内容が最終ツリーから消えてしまうため。
func retainUnclassifiedItems(
	_ *liveAnalysisPayload,
	members []string,
	items map[string]*liveAnalysisItem,
	stats *finalRepairStats,
	reason string,
) {
	for _, itemID := range members {
		item, ok := items[itemID]
		if !ok {
			continue
		}
		stats.UnclassifiedItemsRetained++
		stats.UnclassifiedDecisions = append(stats.UnclassifiedDecisions, finalUnclassifiedDecision{
			ItemID: itemID, CandidateID: item.CandidateTopicID,
			Decision: finalUnclassifiedRetained, ComponentSize: len(members),
			Signals: []string{reason},
		})
	}
}

func finalizeUnclassifiedContainer(state *liveAnalysisPayload) {
	if state == nil || state.Tree == nil {
		return
	}
	// 追加論点から出ていったitemの候補は、証拠を失った時点で pruneDangling…
	// が非活性化する。ここでは残った候補の証拠だけを整合させる。
	assigned := make(map[string]struct{})
	for _, item := range state.Items {
		if item.ClassificationStatus == classificationAssigned {
			assigned[item.ID] = struct{}{}
		}
	}
	for index := range state.EmergingTopics {
		kept := state.EmergingTopics[index].EvidenceItemIDs[:0]
		for _, itemID := range state.EmergingTopics[index].EvidenceItemIDs {
			if _, promoted := assigned[itemID]; !promoted {
				kept = append(kept, itemID)
			}
		}
		state.EmergingTopics[index].EvidenceItemIDs = uniqueNonEmptyIDs(kept)
	}
	// pruneEmptyDynamicTopics は tree.Edges を親子関係の正本として読む。
	// reparent 直後の stale な edge のままだと、いま materialize した topic が
	// 「子を持たない」と判定されて即座に取り除かれてしまう。依存順として、
	// edge の再構築は必ず prune より前に行う。
	rebuildTreeAuditEdges(state.Tree)
	pruneEmptyDynamicTopics(state.Tree)
	pruneEmptyFinalUnclassifiedTopic(state.Tree)
	rebuildTreeAuditEdges(state.Tree)
}

// finalUnclassifiedActiveChildCount は最終ツリーで「追加論点」の箱に残る、
// ユーザーへ表示される子の数。tentative はクライアント側で描画されないため
// 数えない。回帰テストと観測の両方で使う。
func finalUnclassifiedActiveChildCount(state liveAnalysisPayload) int {
	if state.Tree == nil {
		return 0
	}
	tentative := make(map[string]struct{})
	active := make(map[string]struct{})
	for _, item := range state.Items {
		if item.Inactive || item.MergedIntoID != "" || item.Status == "dismissed" {
			continue
		}
		active[item.ID] = struct{}{}
		if item.ClassificationStatus == classificationTentative {
			tentative[item.ID] = struct{}{}
		}
	}
	count := 0
	for _, node := range state.Tree.Nodes {
		if node.Kind == "topic" || node.Kind == "group" {
			continue
		}
		if treeItemTopic(state.Tree, node.ID) != treeUnclassifiedTopicID {
			continue
		}
		if _, isActive := active[node.ID]; !isActive {
			continue
		}
		if _, isTentative := tentative[node.ID]; isTentative {
			continue
		}
		count++
	}
	return count
}
