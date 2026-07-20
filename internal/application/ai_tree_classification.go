package application

import (
	"sort"
	"strings"
)

// このファイルは議論ツリーの「意味分類ポリシー」を持つ。ai_tree.go が保証する
// 構造制約(root一意・単一親・循環なし・型制約)とは独立に、
//   - AI提案のconfidenceに基づく assigned / tentative / unclassified の判定
//   - 新topic候補(emerging topic)の証拠蓄積と dynamic topic への昇格
//   - ラウンド間で分類が揺れないための hysteresis
// を実装する。AIは候補・confidence・理由を提案するだけで、親エッジの確定・
// topic昇格・移動はすべてここ(サーバー側)が検証して決める。

// --- 分類状態・由来の語彙 ----------------------------------------------------

const (
	// classificationAssigned: 確定済み。親topicへの所属をサーバーが受理した。
	classificationAssigned = "assigned"
	// classificationTentative: 暫定。候補topicはあるが確信度が閾値未満のため
	// 追加論点(topic-unclassified)に置いたまま候補を保持し、後続ラウンドで
	// 再評価する。
	classificationTentative = "tentative"
	// classificationUnclassified: 未分類。候補が無い・不正・明示的に未分類。
	classificationUnclassified = "unclassified"

	// assignmentSource: この分類を最後に決めた主体。
	assignmentSourceModel        = "model"          // AI提案をそのまま受理
	assignmentSourceRule         = "rule"           // サーバー規則(昇格・repeat等)
	assignmentSourceReorganizer  = "reorganizer"    // 再編成タスクのmove_node
	assignmentSourceFallback     = "fallback"       // 不正・欠落時の救済
	assignmentSourceActiveSpan   = "active_span"    // 発話区間の明示的な議題転換
	assignmentSourceNoAgendaSpan = "no_agenda_span" // 明示的なアジェンダ外区間

	// topic.origin: topicノードの由来。
	topicOriginAgenda  = "agenda"  // 会議前アジェンダからmaterializeされたtopic
	topicOriginDynamic = "dynamic" // 会議中に昇格した動的topic
	topicOriginSystem  = "system"  // root / topic-unclassified
)

// --- 設定 --------------------------------------------------------------------

// TreeClassificationConfig は意味分類ポリシーの調整値。ゼロ値は normalized()
// で既定値になるため、未設定でも安全に動く。環境変数からの上書きは
// internal/app/config.go が行う。
type TreeClassificationConfig struct {
	// AgendaAssignmentThreshold 未満の明示的なconfidenceを持つ割当は、即confirm
	// せず tentative として追加論点へ置く。confidence省略時(0)は従来互換で受理
	// する(古いモデル出力・legacy変換がconfidenceを持たないため)。
	// 既定0.55: 実セッション(session_f91ff969等)とfixtureでは、モデルは確信の
	// あるアジェンダ割当に0.8以上、明示的な未分類提案に0.6以下を付けており、
	// 0.5台を「モデル自身が迷っている」境界として扱う。
	// env: AI_TREE_AGENDA_ASSIGNMENT_THRESHOLD
	AgendaAssignmentThreshold float64
	// PromotionMinItems は emerging topic を正式な dynamic topic へ昇格させる
	// ために必要な、現存する証拠itemの最小数。1にすると単一発言でtopicが
	// 生まれるため、既定は2。env: AI_TREE_TOPIC_PROMOTION_MIN_ITEMS
	PromotionMinItems int
	// PromotionMinRounds は昇格に必要な、候補に証拠が集まった分析ラウンドの
	// 最小数。同一ラウンド内の言い換え連投だけで昇格しないよう既定は2。
	// env: AI_TREE_TOPIC_PROMOTION_MIN_ROUNDS
	PromotionMinRounds int
	// MaxDynamicTopics は1会議あたりの dynamic topic(origin=dynamic)の上限。
	// 既定6: ツリーのノード上限(36)とアジェンダ上限(10)に対し、topicが
	// ノードの過半を占めない水準。env: AI_TREE_MAX_DYNAMIC_TOPICS
	MaxDynamicTopics int
}

const (
	defaultAgendaAssignmentThreshold = 0.55
	defaultPromotionMinItems         = 2
	defaultPromotionMinRounds        = 2
	defaultMaxDynamicTopics          = 6

	// reparentConfidenceMargin: assigned済みitemを別topicへ移すには、新しい
	// confidenceが記録済みconfidenceをこの分だけ上回るか、同じ候補が2ラウンド
	// 連続で提案される必要がある(揺れ防止のhysteresis)。
	reparentConfidenceMargin = 0.15

	// maxEmergingCandidatesPerRound は1ラウンドで新規に受け付ける新topic候補
	// の上限。live抽出プロンプトの「newTopicsは最大2件」をサーバー側でも強制する。
	maxEmergingCandidatesPerRound = 2
	// maxEmergingCandidates は保持する未昇格候補の総数上限(古いものから破棄)。
	maxEmergingCandidates = 8
	// maxPromotionsPerRound は1ラウンドで昇格させるtopic数の上限(バースト防止)。
	maxPromotionsPerRound = 1

	// itemEvidenceMaxSequenceNos / candidateEvidenceMaxItems は payload肥大化を
	// 防ぐための証拠リスト上限。
	itemEvidenceMaxSequenceNos = 8
	candidateEvidenceMaxItems  = 8

	// assignmentReasonMaxRunes はitemへ保持するAIの分類理由の上限文字数。
	assignmentReasonMaxRunes = 100
)

func (c TreeClassificationConfig) normalized() TreeClassificationConfig {
	if c.AgendaAssignmentThreshold <= 0 || c.AgendaAssignmentThreshold >= 1 {
		c.AgendaAssignmentThreshold = defaultAgendaAssignmentThreshold
	}
	if c.PromotionMinItems <= 0 {
		c.PromotionMinItems = defaultPromotionMinItems
	}
	if c.PromotionMinRounds <= 0 {
		c.PromotionMinRounds = defaultPromotionMinRounds
	}
	if c.MaxDynamicTopics <= 0 {
		c.MaxDynamicTopics = defaultMaxDynamicTopics
	}
	return c
}

// --- emerging topic 候補 -----------------------------------------------------

// emergingTopicCandidate は「まだ正式なtopicではない新分類候補」。liveペイロード
// の emergingTopics として永続化され、ラウンドをまたいで証拠を蓄積する。昇格
// するまでツリーには現れず、証拠itemは追加論点(topic-unclassified)に tentative
// で置かれる。
type emergingTopicCandidate struct {
	ID                string   `json:"id"`
	Label             string   `json:"label"`
	Description       string   `json:"description,omitempty"`
	OriginalSubject   string   `json:"originalSubject,omitempty"`
	CurrentSubject    string   `json:"currentSubject,omitempty"`
	SubjectHistory    []string `json:"subjectHistory,omitempty"`
	OriginItemIDs     []string `json:"originItemIds,omitempty"`
	OriginSequenceNos []int64  `json:"originSequenceNos,omitempty"`
	// ModelTopicIDs are non-authoritative aliases retained only so later model
	// rounds can refer to their original proposal after ID canonicalization.
	ModelTopicIDs []string `json:"modelTopicIds,omitempty"`
	// EvidenceItemIDs はこの候補への割当が提案されたitem(重複なし・上限あり)。
	EvidenceItemIDs []string `json:"evidenceItemIds,omitempty"`
	// FirstRound / LastRound / RoundCount は証拠が集まった分析ラウンド
	// (treeVersion)の追跡。RoundCountは「証拠が集まった異なるラウンド数」。
	FirstRound int64 `json:"firstRound,omitempty"`
	LastRound  int64 `json:"lastRound,omitempty"`
	RoundCount int   `json:"roundCount,omitempty"`
	// Stale candidates remain in the payload for audit, but clients can keep
	// them out of the visible tentative staging area until evidence returns.
	Inactive           bool  `json:"inactive,omitempty"`
	InactiveSinceRound int64 `json:"inactiveSinceRound,omitempty"`
}

func (c *emergingTopicCandidate) addEvidence(itemID string, round int64) {
	if itemID != "" || round > c.LastRound {
		c.Inactive = false
		c.InactiveSinceRound = 0
	}
	if itemID != "" {
		found := false
		for _, id := range c.EvidenceItemIDs {
			if id == itemID {
				found = true
				break
			}
		}
		if !found && len(c.EvidenceItemIDs) < candidateEvidenceMaxItems {
			c.EvidenceItemIDs = append(c.EvidenceItemIDs, itemID)
			if len(c.OriginItemIDs) < candidateEvidenceMaxItems {
				c.OriginItemIDs = append(c.OriginItemIDs, itemID)
			}
		}
	}
	if round > c.LastRound {
		c.LastRound = round
		c.RoundCount++
	}
}

func initializeCandidateSubject(candidate *emergingTopicCandidate) {
	if candidate == nil {
		return
	}
	subject := strings.TrimSpace(candidate.Label + " " + candidate.Description)
	if candidate.OriginalSubject == "" {
		candidate.OriginalSubject = subject
	}
	if candidate.CurrentSubject == "" {
		candidate.CurrentSubject = subject
	}
	if len(candidate.SubjectHistory) == 0 && subject != "" {
		candidate.SubjectHistory = []string{subject}
	}
}

func candidateSubjectCompatible(candidate emergingTopicCandidate, label, description string) bool {
	initializeCandidateSubject(&candidate)
	proposed := strings.TrimSpace(label + " " + description)
	if proposed == "" || candidate.CurrentSubject == "" {
		return true
	}
	return semanticItemSimilarity(candidate.CurrentSubject, proposed) >= 0.28 || sharedTreeAuditSubjectTerm(candidate.CurrentSubject, proposed)
}

func updateCandidateSubject(candidate *emergingTopicCandidate, label, description string) bool {
	if candidate == nil || !candidateSubjectCompatible(*candidate, label, description) {
		return false
	}
	initializeCandidateSubject(candidate)
	proposed := strings.TrimSpace(label + " " + description)
	if proposed != "" && semanticItemKey(proposed) != semanticItemKey(candidate.CurrentSubject) {
		candidate.CurrentSubject = proposed
		candidate.SubjectHistory = appendUniqueText(candidate.SubjectHistory, proposed)
	}
	if strings.TrimSpace(label) != "" {
		candidate.Label = strings.TrimSpace(label)
	}
	if strings.TrimSpace(description) != "" {
		candidate.Description = strings.TrimSpace(description)
	}
	return true
}

// pruneCandidateEvidence removes evidence ids whose item no longer exists
// (evicted or dismissed), so promotion never counts stale evidence.
func pruneCandidateEvidence(candidate *emergingTopicCandidate, itemIDs map[string]struct{}) {
	kept := candidate.EvidenceItemIDs[:0]
	for _, id := range candidate.EvidenceItemIDs {
		if _, ok := itemIDs[id]; ok {
			kept = append(kept, id)
		}
	}
	candidate.EvidenceItemIDs = kept
}

func candidatePromotionEvidenceCount(candidate emergingTopicCandidate) int {
	count := len(candidate.EvidenceItemIDs)
	if originCount := len(uniqueNonEmptyIDs(candidate.OriginItemIDs)); originCount > count {
		count = originCount
	}
	return count
}

// capEmergingCandidates keeps at most max candidates, evicting the ones with
// the oldest LastRound first (least recently supported).
func capEmergingCandidates(candidates []emergingTopicCandidate, max int) []emergingTopicCandidate {
	if len(candidates) <= max {
		return candidates
	}
	sorted := append([]emergingTopicCandidate(nil), candidates...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Inactive != sorted[j].Inactive {
			return !sorted[i].Inactive
		}
		return sorted[i].LastRound > sorted[j].LastRound
	})
	sorted = sorted[:max]
	keep := make(map[string]struct{}, len(sorted))
	for _, candidate := range sorted {
		keep[candidate.ID] = struct{}{}
	}
	kept := make([]emergingTopicCandidate, 0, max)
	for _, candidate := range candidates {
		if _, ok := keep[candidate.ID]; ok {
			kept = append(kept, candidate)
		}
	}
	return kept
}

// normalizeProposedTopicID gives a proposed topic id the same shape rules the
// tree uses for dynamic topics ("topic-" prefix, derived from the label when
// missing). Returns "" when no usable id can be built.
func normalizeProposedTopicID(id, label string) string {
	id = strings.TrimSpace(id)
	if id == "" || id == treeRootNodeID {
		slug := normalizeForMatch(label)
		if slug == "" {
			return ""
		}
		id = "topic-" + slug
	}
	if !strings.HasPrefix(id, "topic-") {
		id = "topic-" + id
	}
	return id
}

// deriveTopicOrigin backfills origin without interpreting the node ID. Agenda
// identity is carried exclusively by AgendaRefs after legacy normalization.
func deriveTopicOrigin(topic liveAnalysisTreeNode) string {
	if topic.ID == treeRootNodeID || topic.ID == treeUnclassifiedTopicID {
		return topicOriginSystem
	}
	if len(topic.AgendaRefs) > 0 {
		return topicOriginAgenda
	}
	return topicOriginDynamic
}

// --- 判定結果(観測ログ用) ---------------------------------------------------

// assignmentDecision は1つの割当提案に対するサーバー判定。ログ専用で、本文
// (title/body/理由文)は含めない。
type assignmentDecision struct {
	ModelItemID            string
	ItemID                 string
	RequestedParentID      string
	SelectedParentID       string
	Confidence             float64
	Source                 string
	Decision               string
	Status                 string
	CandidateTopicID       string
	EvidenceSequenceNos    []int64
	ResolvedAgendaSpanMode string
	AssignmentReason       string
	AgendaMaterialized     bool
	CandidateComparison    string
}

// assignmentDecision.Decision の語彙。
const (
	assignmentAccepted             = "accepted"                  // 提案をそのまま受理
	assignmentAcceptedRepeat       = "accepted_repeat"           // 同一候補が2ラウンド連続で受理
	assignmentAcceptedUnclassified = "accepted_unclassified"     // 明示的な未分類提案
	assignmentDeferredLowConf      = "deferred_low_confidence"   // 閾値未満→tentative
	assignmentDeferredHysteresis   = "deferred_hysteresis"       // assigned済みの移動を保留
	assignmentDeferredEmerging     = "deferred_emerging"         // 未昇格候補への割当→tentative
	assignmentRejectedUnknown      = "rejected_unknown_parent"   // 存在しない親→未分類へ
	assignmentRejectedUnknownItem  = "rejected_unknown_item"     // 存在しないitemへの割当
	assignmentRelatedActionSummary = "related_action_summary"    // 横断agendaは副次関係のみ
	assignmentCorrectedSemantic    = "corrected_semantic_parent" // 内容一致するprimary agendaへ補正
	assignmentAcceptedActiveSpan   = "accepted_active_span"      // 明示的な議題区間を優先
	assignmentAcceptedNoAgendaSpan = "accepted_no_agenda_span"   // 明示的な議題外区間を候補へ集約
)

// emergingDecision は新topic候補に対するサーバー判定(ログ専用)。
type emergingDecision struct {
	CandidateID        string
	EvidenceItemCount  int
	RoundCount         int
	Decision           string
	TopicID            string
	Reason             string
	SubjectKey         string
	MergedCandidateIDs []string
}

const (
	emergingCreated            = "created"
	emergingUpdated            = "updated"
	emergingPromoted           = "promoted"
	emergingFoldedIntoExisting = "folded_into_existing"
	emergingRejectedRoundCap   = "rejected_round_cap"
	emergingRejectedTopicCap   = "rejected_topic_cap"
	emergingDeferredPromoteCap = "deferred_promotion_cap"
	emergingWaitingEvidence    = "waiting_evidence"
	emergingRejectedNoEvidence = "rejected_no_evidence"
)
