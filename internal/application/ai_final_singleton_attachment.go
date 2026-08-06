package application

import (
	"sort"
	"strings"
	"unicode"
)

// このファイルは finalization の「追加論点(topic-unclassified)」直下に単独で
// 残ったitemを、既に意味の確立した既存topicへ接続する決定的repairを実装する。
//
// repairFinalUnclassifiedItems の component 修復(ai_final_unclassified.go)は
// 「複数itemから新しいtopicを作る」処理なので、item数>=2・異なるevidence
// sequence数>=2 を要求する。1発話から取り出された複数kindだけでtopicを量産
// しないための条件であり、この条件は維持する。
//
// 一方、ここで扱うのは「新しいtopicを作らず、既存topicへ移すだけ」の操作で、
// 箱を増やさないため上記のcomponent条件は要求しない。代わりに次を必須にする。
//
//  1. 対象一致    : itemとtopic(label / agenda semantic hint / 配下item全体)が
//     同じ具体的対象語を共有する。汎用語一語だけの一致は対象一致
//     として数えない。
//  2. 補助的意味関係: relation edge / 共有evidence / evidence近接 / agenda hint /
//     candidate系譜のいずれかが成立する。
//
// さらに best と second-best のスコア差が十分にない場合、矛盾する修飾語を持つ
// 場合、手動編集済みの場合は接続せず追加論点へ残す。誤接続より保留を優先する。

const (
	// singletonAttachmentSubjectFloor は接続を検討できる最小の対象一致強度。
	// 「非汎用語を1語だけ、しかもtopic label側に現れない」水準(0.45)では届かず、
	// topic label一致・複数語一致・3文字以上の対象語のいずれかを必要とする。
	singletonAttachmentSubjectFloor = 0.55
	// singletonAttachmentSupportFloor は必要な補助的意味関係の最小強度。
	// evidence近接(0.6)が下限で、話者一致やsequence近接だけの経路は存在しない。
	singletonAttachmentSupportFloor = 0.60
	// singletonAttachmentScoreFloor は総合スコアの下限。
	singletonAttachmentScoreFloor = 0.58
	// singletonAttachmentScoreMargin は second-best との必要差。同程度に適合する
	// topicが複数ある場合は接続せず保留する。
	singletonAttachmentScoreMargin = 0.08
	// singletonAttachmentMinTermRunes は対象語として認める最小長。
	singletonAttachmentMinTermRunes = 2
)

const (
	singletonAttachmentApplied         = "applied"
	singletonAttachmentDeferred        = "deferred"
	singletonAttachmentAmbiguous       = "ambiguous"
	singletonAttachmentManualPreserved  = "manual_preserved"
	singletonAttachmentAssignmentSource = "final_singleton_attachment"
)

// singletonAttachmentDecision は1件の単独itemに対する接続判定。観測ログ専用で、
// 発話本文・label・description・話者名は載せない。
type singletonAttachmentDecision struct {
	ItemID                  string
	SourceParentID          string
	TargetTopicID           string
	CandidateTopicCount     int
	BestScore               float64
	SecondBestScore         float64
	ScoreMargin             float64
	SubjectMatchStrength    float64
	PredicateCompatibility  float64
	RelationSupport         float64
	EvidenceSupport         float64
	AgendaSupport           float64
	DiscourseSupport        float64
	ContradictionPenalty    float64
	GenericOverlapPenalty   float64
	ManualProtectionPenalty float64
	Decision                string
	Reason                  string
	EvidenceSequenceNos     []int64
}

// singletonAttachmentGenericTerms は会議の進行・評価そのものを指す語。どの業務
// ドメインでも現れるため、これだけが一致しても「同じ対象を論じている」根拠に
// ならない。業務対象(設備名・システム名・業務名)は一切含めない。
var singletonAttachmentGenericTerms = map[string]struct{}{
	"確認": {}, "検討": {}, "実施": {}, "対応": {}, "決定": {}, "判断": {}, "必要": {},
	"予定": {}, "課題": {}, "問題": {}, "方法": {}, "結果": {}, "資料": {}, "説明": {},
	"共有": {}, "報告": {}, "調整": {}, "状況": {}, "場合": {}, "今回": {}, "次回": {},
	"今週": {}, "来週": {}, "今日": {}, "明日": {}, "可能": {}, "可能性": {}, "影響": {},
	"懸念": {}, "方針": {}, "検証": {}, "作業": {}, "議論": {}, "全体": {}, "部分": {},
	"内容": {}, "対策": {}, "実行": {}, "追加": {}, "変更": {}, "修正": {}, "見直": {},
	"改善": {}, "提案": {}, "以上": {}, "以下": {}, "現在": {}, "今後": {}, "本件": {},
}

// singletonAttachmentWeakTerms は業務対象を指しうるが、あらゆる文脈に現れる
// ほど一般的な運用語。単独で一致してもそれだけでは対象一致として扱わない
// (§汎用語一語だけの一致を対象一致にしない)。
var singletonAttachmentWeakTerms = map[string]struct{}{
	"通知": {}, "接続": {}, "設定": {}, "条件": {}, "対象": {}, "情報": {}, "管理": {},
	"運用": {}, "連絡": {}, "手順": {}, "基準": {}, "頻度": {}, "期限": {}, "担当": {},
	"承認": {}, "項目": {}, "範囲": {}, "記録": {}, "一覧": {}, "体制": {}, "状態": {},
}

type singletonAttachmentRuneClass int

const (
	singletonAttachmentClassNone singletonAttachmentRuneClass = iota
	singletonAttachmentClassHan
	singletonAttachmentClassKatakana
	singletonAttachmentClassLatin
)

// singletonAttachmentToken は1つの内容語。Compound は空白・ひらがな・記号で
// 区切られた連続塊、Term はその中を文字種(漢字/カタカナ/ラテン)で割った単位。
// Compound を保持するのは、共有語が複合語の主要語(末尾)である場合に修飾語が
// 一致するかを見るため。主要語が同じでも修飾語が違う複合語(修飾語X+主要語 と
// 修飾語Y+主要語)は、別の業務対象なので統合しない。
type singletonAttachmentToken struct {
	Compound string
	Term     string
	HanOnly  bool
}

func singletonAttachmentRuneClassOf(value rune) singletonAttachmentRuneClass {
	switch {
	case unicode.Is(unicode.Han, value):
		return singletonAttachmentClassHan
	case value == 'ー' || unicode.Is(unicode.Katakana, value):
		return singletonAttachmentClassKatakana
	case value >= 'a' && value <= 'z':
		return singletonAttachmentClassLatin
	default:
		return singletonAttachmentClassNone
	}
}

// singletonAttachmentTokens は内容語を抽出する。ひらがな・数字・記号は区切りと
// して扱う。語彙辞書を持たず文字種だけで区切るため、業務ドメインに依存しない。
func singletonAttachmentTokens(text string) []singletonAttachmentToken {
	lowered := strings.ToLower(strings.TrimSpace(text))
	if lowered == "" {
		return nil
	}
	tokens := make([]singletonAttachmentToken, 0, 8)
	runes := []rune(lowered)
	for start := 0; start < len(runes); {
		if singletonAttachmentRuneClassOf(runes[start]) == singletonAttachmentClassNone {
			start++
			continue
		}
		end := start
		for end < len(runes) && singletonAttachmentRuneClassOf(runes[end]) != singletonAttachmentClassNone {
			end++
		}
		compound := string(runes[start:end])
		for at := start; at < end; {
			class := singletonAttachmentRuneClassOf(runes[at])
			next := at
			for next < end && singletonAttachmentRuneClassOf(runes[next]) == class {
				next++
			}
			if next-at >= singletonAttachmentMinTermRunes {
				tokens = append(tokens, singletonAttachmentToken{
					Compound: compound,
					Term:     string(runes[at:next]),
					HanOnly:  class == singletonAttachmentClassHan,
				})
			}
			at = next
		}
		start = end
	}
	return tokens
}

func singletonAttachmentTokensOf(values ...string) []singletonAttachmentToken {
	tokens := make([]singletonAttachmentToken, 0, 16)
	for _, value := range values {
		tokens = append(tokens, singletonAttachmentTokens(value)...)
	}
	return tokens
}

// singletonAttachmentSharedTerm は両側で共有された対象語。
type singletonAttachmentSharedTerm struct {
	Term        string
	Weak        bool
	Conflicting bool
}

func singletonAttachmentLongestCommonSubstring(left, right []rune) string {
	best, bestLength := 0, 0
	for i := range left {
		for j := range right {
			length := 0
			for i+length < len(left) && j+length < len(right) && left[i+length] == right[j+length] {
				length++
			}
			if length > bestLength {
				best, bestLength = i, length
			}
		}
	}
	if bestLength == 0 {
		return ""
	}
	return string(left[best : best+bestLength])
}

func singletonAttachmentTermAligned(term, source string) bool {
	return term == source || strings.HasPrefix(source, term) || strings.HasSuffix(source, term)
}

// singletonAttachmentHeadModifier は複合語の末尾が term のとき、その前置修飾語を
// 返す。term が末尾でなければ空文字を返す。
func singletonAttachmentHeadModifier(compound, term string) string {
	if compound == term || !strings.HasSuffix(compound, term) {
		return ""
	}
	return strings.TrimSuffix(compound, term)
}

// singletonAttachmentSharedTerms は2つの内容語集合が共有する対象語を返す。
// 漢字列は形態素境界を持たないため内部一致を許し、カタカナ/ラテン語は語の
// 断片同士がたまたま一致するのを避けて前方・後方一致だけを許す。
func singletonAttachmentSharedTerms(left, right []singletonAttachmentToken) []singletonAttachmentSharedTerm {
	type termState struct {
		weak       bool
		compatible bool
		seen       bool
	}
	states := make(map[string]*termState)
	order := make([]string, 0, 4)
	for _, leftToken := range left {
		for _, rightToken := range right {
			term := singletonAttachmentLongestCommonSubstring([]rune(leftToken.Term), []rune(rightToken.Term))
			if len([]rune(term)) < singletonAttachmentMinTermRunes {
				continue
			}
			if !leftToken.HanOnly || !rightToken.HanOnly {
				// カタカナ語・ラテン語は語全体で1形態素なので、別語同士がたまたま
				// 共有する末尾数文字を対象一致にしない。少なくとも片側では語全体、
				// もう一方でも前方/後方一致を要求する。
				if term != leftToken.Term && term != rightToken.Term {
					continue
				}
				if !singletonAttachmentTermAligned(term, leftToken.Term) ||
					!singletonAttachmentTermAligned(term, rightToken.Term) {
					continue
				}
			}
			if _, generic := singletonAttachmentGenericTerms[term]; generic {
				continue
			}
			leftModifier := singletonAttachmentHeadModifier(leftToken.Compound, term)
			rightModifier := singletonAttachmentHeadModifier(rightToken.Compound, term)
			compatible := leftModifier == "" || rightModifier == "" || leftModifier == rightModifier
			state, exists := states[term]
			if !exists {
				_, weak := singletonAttachmentWeakTerms[term]
				state = &termState{weak: weak}
				states[term] = state
				order = append(order, term)
			}
			state.seen = true
			if compatible {
				state.compatible = true
			}
		}
	}
	sort.Strings(order)
	shared := make([]singletonAttachmentSharedTerm, 0, len(order))
	for _, term := range order {
		state := states[term]
		shared = append(shared, singletonAttachmentSharedTerm{
			Term: term, Weak: state.weak, Conflicting: !state.compatible,
		})
	}
	return shared
}

// singletonAttachmentSubjectMatch は1つの既存topicに対する対象一致の評価結果。
type singletonAttachmentSubjectMatch struct {
	Strength       float64
	GenericPenalty float64
	Conflicting    bool
	FromTopicLabel bool
	FromAgendaHint bool
	// Anchored は共有した対象語がtopic自身のlabel/agenda metadataでも認識できるか、
	// または複数の異なる対象語で裏付けられていることを表す。配下item1件との
	// 偶発的な共有語(地名・組織名・共通の登場人物など)だけでtopic全体への
	// 所属を主張させないための構造条件。
	Anchored bool
	Terms    []string
}

func singletonAttachmentMergeTerms(
	target map[string]singletonAttachmentSharedTerm,
	terms []singletonAttachmentSharedTerm,
) {
	for _, term := range terms {
		existing, exists := target[term.Term]
		if !exists {
			target[term.Term] = term
			continue
		}
		// 同じ対象語が別経路でも共有されており、そこで修飾語が矛盾しなければ
		// 矛盾扱いを解除する。
		if !term.Conflicting {
			existing.Conflicting = false
		}
		target[term.Term] = existing
	}
}

func singletonAttachmentEvaluateSubject(
	itemTokens []singletonAttachmentToken,
	topic singletonAttachmentTopicSignature,
) singletonAttachmentSubjectMatch {
	labelTerms := singletonAttachmentSharedTerms(itemTokens, topic.LabelTokens)
	hintTerms := singletonAttachmentSharedTerms(itemTokens, topic.HintTokens)
	contextTerms := singletonAttachmentSharedTerms(itemTokens, topic.ContextTokens)
	descendantTerms := singletonAttachmentSharedTerms(itemTokens, topic.DescendantTokens)

	merged := make(map[string]singletonAttachmentSharedTerm, 4)
	singletonAttachmentMergeTerms(merged, labelTerms)
	singletonAttachmentMergeTerms(merged, hintTerms)
	singletonAttachmentMergeTerms(merged, contextTerms)
	singletonAttachmentMergeTerms(merged, descendantTerms)
	if len(merged) == 0 {
		return singletonAttachmentSubjectMatch{}
	}

	match := singletonAttachmentSubjectMatch{
		FromTopicLabel: len(labelTerms) > 0,
		FromAgendaHint: len(hintTerms) > 0,
		Terms:          make([]string, 0, len(merged)),
	}
	longest, weakCount := 0, 0
	for term, shared := range merged {
		match.Terms = append(match.Terms, term)
		if shared.Conflicting {
			match.Conflicting = true
		}
		if shared.Weak {
			weakCount++
		}
		if length := len([]rune(term)); length > longest {
			longest = length
		}
	}
	sort.Strings(match.Terms)
	match.Anchored = match.FromTopicLabel || match.FromAgendaHint || len(merged) >= 2

	switch {
	case longest >= 4:
		match.Strength = 0.85
	case longest == 3:
		match.Strength = 0.70
	default:
		match.Strength = 0.45
	}
	if len(merged) >= 2 {
		match.Strength += 0.10
	}
	if match.FromTopicLabel {
		match.Strength += 0.10
	}
	if match.Strength > 1 {
		match.Strength = 1
	}
	// 汎用的な運用語だけを共有している場合は対象一致とみなさない。
	if weakCount == len(merged) {
		match.GenericPenalty = match.Strength * 0.5
		match.Strength -= match.GenericPenalty
	}
	return match
}

// singletonAttachmentTopicSignature は既存topicの意味的な指紋。topic label だけ
// でなく、agenda metadata と配下item全体(kind / 対象語 / evidence)を集約する。
type singletonAttachmentTopicSignature struct {
	TopicID           string
	Origin            string
	SourceCandidateID string
	AliasIDs          []string
	// LabelTokens は topic 自身の名前だけ。description や配下itemの語は
	// ContextTokens / DescendantTokens 側で扱い、topicの同一性を主張できる
	// 語(anchor)と、単に同じ会議に出てきた語を区別する。
	LabelTokens      []singletonAttachmentToken
	HintTokens       []singletonAttachmentToken
	ContextTokens    []singletonAttachmentToken
	DescendantTokens []singletonAttachmentToken
	DescendantIDs     []string
	Text              string
	descendantTokens  map[string][]singletonAttachmentToken
	descendantKind    map[string]string
	descendantSeqs    map[string][]int64
}

func buildSingletonAttachmentTopicSignatures(
	state *liveAnalysisPayload,
	mc *meetingContext,
	itemByID map[string]*liveAnalysisItem,
) []singletonAttachmentTopicSignature {
	if state == nil || state.Tree == nil {
		return nil
	}
	records := agendaRecordMap(mc)
	signatures := make([]singletonAttachmentTopicSignature, 0, len(state.Tree.Nodes))
	for _, node := range state.Tree.Nodes {
		if node.Kind != "topic" || node.ID == treeRootNodeID || node.ID == treeUnclassifiedTopicID ||
			node.AgendaRole == agendaRoleActionSummary {
			continue
		}
		signature := singletonAttachmentTopicSignature{
			TopicID:           node.ID,
			Origin:            deriveTopicOrigin(node),
			SourceCandidateID: strings.TrimSpace(node.SourceCandidateID),
			AliasIDs:          append(append([]string(nil), node.ModelTopicIDs...), node.MergedFromNodeIDs...),
			LabelTokens:       singletonAttachmentTokensOf(node.Label),
			ContextTokens:     singletonAttachmentTokensOf(node.Description),
			descendantTokens:  make(map[string][]singletonAttachmentToken),
			descendantKind:    make(map[string]string),
			descendantSeqs:    make(map[string][]int64),
		}
		hintValues := make([]string, 0, 4)
		for _, agendaID := range topicAgendaRefs(node, records) {
			record, exists := records[agendaID]
			if !exists {
				continue
			}
			hintValues = append(hintValues, record.Title, record.Description, record.Goal)
			hintValues = append(hintValues, record.SemanticHints...)
		}
		signature.HintTokens = singletonAttachmentTokensOf(hintValues...)
		texts := []string{node.Label, node.Description}
		for _, descendant := range state.Tree.Nodes {
			if descendant.Kind == "topic" || descendant.Kind == "group" ||
				treeItemTopic(state.Tree, descendant.ID) != node.ID {
				continue
			}
			item, active := itemByID[descendant.ID]
			if !active {
				continue
			}
			text := strings.TrimSpace(item.Title + " " + item.Body)
			tokens := singletonAttachmentTokensOf(text)
			signature.DescendantIDs = append(signature.DescendantIDs, item.ID)
			signature.DescendantTokens = append(signature.DescendantTokens, tokens...)
			signature.descendantTokens[item.ID] = tokens
			signature.descendantKind[item.ID] = item.Kind
			signature.descendantSeqs[item.ID] = append([]int64(nil), item.EvidenceSequenceNos...)
			texts = append(texts, text)
		}
		sort.Strings(signature.DescendantIDs)
		signature.Text = strings.TrimSpace(strings.Join(texts, " "))
		signatures = append(signatures, signature)
	}
	sort.Slice(signatures, func(i, j int) bool { return signatures[i].TopicID < signatures[j].TopicID })
	return signatures
}

// singletonAttachmentItemSignature は単独itemの意味的な指紋。
type singletonAttachmentItemSignature struct {
	ItemID               string
	Kind                 string
	Text                 string
	Tokens               []singletonAttachmentToken
	EvidenceSequenceNos  []int64
	CandidateTopicID     string
	ConcreteSubject      string
	ClassificationStatus string
	SemanticRole         string
	TemporalScope        string
	EpistemicStatus      string
	RelatedItemIDs       map[string]struct{}
}

func buildSingletonAttachmentItemSignature(
	item liveAnalysisItem,
	tree *liveAnalysisTree,
) singletonAttachmentItemSignature {
	text := strings.TrimSpace(item.Title + " " + item.Body)
	tokenSource := append([]string{item.Title, item.Body}, item.EvidenceSnippets...)
	features := inferItemSemanticFeatures(item, liveEvidenceScope{})
	signature := singletonAttachmentItemSignature{
		ItemID:               item.ID,
		Kind:                 item.Kind,
		Text:                 text,
		Tokens:               singletonAttachmentTokensOf(tokenSource...),
		EvidenceSequenceNos:  append([]int64(nil), item.EvidenceSequenceNos...),
		CandidateTopicID:     strings.TrimSpace(item.CandidateTopicID),
		ConcreteSubject:      concreteBusinessSubject(text),
		ClassificationStatus: item.ClassificationStatus,
		SemanticRole:         features.SemanticRole,
		TemporalScope:        features.TemporalScope,
		EpistemicStatus:      features.EpistemicStatus,
		RelatedItemIDs:       make(map[string]struct{}),
	}
	if tree != nil {
		for _, relation := range tree.Relations {
			if relation.Status == "inactive" {
				continue
			}
			if relation.Source == item.ID && relation.Target != "" {
				signature.RelatedItemIDs[relation.Target] = struct{}{}
			}
			if relation.Target == item.ID && relation.Source != "" {
				signature.RelatedItemIDs[relation.Source] = struct{}{}
			}
		}
	}
	return signature
}

// singletonAttachmentComplementaryKinds は同じ論点の別側面として自然に共存する
// kindの組。kind一致は要求せず、逆にkindの組だけを接続根拠にもしない。
var singletonAttachmentComplementaryKinds = map[string]struct{}{
	"fact|risk": {}, "issue|risk": {}, "issue|todo": {},
	"decision|todo": {}, "fact|issue": {}, "decision|risk": {},
}

func singletonAttachmentKindPairKey(left, right string) string {
	if left > right {
		left, right = right, left
	}
	return left + "|" + right
}

// singletonAttachmentCandidate は1つの既存topicに対する接続候補の評価。
type singletonAttachmentCandidate struct {
	TopicID   string
	Subject   singletonAttachmentSubjectMatch
	Predicate float64
	Relation  float64
	Evidence  float64
	Agenda    float64
	Discourse float64
	Support   float64
	Score     float64
	Eligible  bool
	Reason    string
}

func evaluateSingletonAttachmentCandidate(
	item singletonAttachmentItemSignature,
	topic singletonAttachmentTopicSignature,
) singletonAttachmentCandidate {
	candidate := singletonAttachmentCandidate{TopicID: topic.TopicID}
	candidate.Subject = singletonAttachmentEvaluateSubject(item.Tokens, topic)
	if len(candidate.Subject.Terms) == 0 {
		candidate.Reason = "no_shared_subject"
		return candidate
	}
	if candidate.Subject.Conflicting {
		candidate.Reason = "contradictory_subject_qualifier"
		return candidate
	}
	if latinTokenConflict(item.Text, topic.Text) || numericSignatureIncompatible(item.Text, topic.Text) {
		candidate.Subject.Conflicting = true
		candidate.Reason = "contradictory_subject_signature"
		return candidate
	}

	// 補助的意味関係: relation edge / 共有evidence / evidence近接 /
	// agenda semantic hint / candidate系譜。
	candidate.Predicate = 0.5
	for _, descendantID := range topic.DescendantIDs {
		if _, linked := item.RelatedItemIDs[descendantID]; linked {
			candidate.Relation = 1
		}
		for _, sequenceNo := range topic.descendantSeqs[descendantID] {
			for _, own := range item.EvidenceSequenceNos {
				distance := own - sequenceNo
				if distance < 0 {
					distance = -distance
				}
				switch {
				case distance == 0:
					candidate.Evidence = 1
				case distance <= int64(finalUnclassifiedEvidenceWindow) && candidate.Evidence < 0.6:
					candidate.Evidence = 0.6
				}
			}
		}
		if len(singletonAttachmentSharedTerms(item.Tokens, topic.descendantTokens[descendantID])) == 0 {
			continue
		}
		compatibility := 0.75
		if descendantKind := topic.descendantKind[descendantID]; descendantKind != item.Kind {
			compatibility = 0.85
			if _, complementary := singletonAttachmentComplementaryKinds[singletonAttachmentKindPairKey(
				item.Kind, descendantKind,
			)]; complementary {
				compatibility = 1
			}
		}
		if compatibility > candidate.Predicate {
			candidate.Predicate = compatibility
		}
	}
	if candidate.Subject.FromAgendaHint {
		candidate.Agenda = 1
	}
	if item.CandidateTopicID != "" {
		if item.CandidateTopicID == topic.SourceCandidateID ||
			containsExactString(topic.AliasIDs, item.CandidateTopicID) {
			candidate.Discourse = 1
		}
	}
	for _, support := range []float64{candidate.Relation, candidate.Evidence, candidate.Agenda, candidate.Discourse} {
		if support > candidate.Support {
			candidate.Support = support
		}
	}

	candidate.Score = 0.5*candidate.Subject.Strength + 0.2*candidate.Predicate + 0.3*candidate.Support
	switch {
	case !candidate.Subject.Anchored:
		candidate.Reason = "subject_not_anchored_in_topic"
	case candidate.Subject.Strength < singletonAttachmentSubjectFloor:
		candidate.Reason = "subject_match_below_floor"
	case candidate.Support < singletonAttachmentSupportFloor:
		candidate.Reason = "no_supporting_semantic_relation"
	default:
		candidate.Eligible = true
	}
	return candidate
}

// singletonAttachmentGroundedItem は接続対象にできる確定命題かを判定する。
func singletonAttachmentGroundedItem(item liveAnalysisItem) bool {
	if strings.TrimSpace(item.Title+item.Body) == "" || liveItemTextNeedsReferent(item) {
		return false
	}
	switch item.GroundingDecision {
	case "accepted", "rewritten":
		return true
	case "":
		// grounding metadata を持たない旧snapshotは、サーバーが付けた
		// information status だけで判断する。
		return item.InformationStatus == informationStatusGrounded
	default:
		return false
	}
}

// unclassifiedGroundedSingletonCount は最終ツリーで追加論点の箱に残っている
// groundedな実itemの件数。修復後に残った保留数を観測・評価するために使う。
func unclassifiedGroundedSingletonCount(state liveAnalysisPayload) int {
	if state.Tree == nil {
		return 0
	}
	active := make(map[string]liveAnalysisItem, len(state.Items))
	for _, item := range state.Items {
		if item.Inactive || item.MergedIntoID != "" || item.Status == "dismissed" {
			continue
		}
		active[item.ID] = item
	}
	count := 0
	for _, node := range state.Tree.Nodes {
		if node.Kind == "topic" || node.Kind == "group" ||
			treeItemTopic(state.Tree, node.ID) != treeUnclassifiedTopicID {
			continue
		}
		item, ok := active[node.ID]
		if !ok || !singletonAttachmentGroundedItem(item) {
			continue
		}
		count++
	}
	return count
}

// attachUnclassifiedSingletonsToExistingTopics は追加論点の箱に単独で残った
// grounded item を、既存topicへ接続する。新しいtopicは作らない。接続できた
// itemIDの集合を返す。
func attachUnclassifiedSingletonsToExistingTopics(
	state *liveAnalysisPayload,
	mc *meetingContext,
	version int64,
	stats *finalRepairStats,
	singletonIDs []string,
	itemByID map[string]*liveAnalysisItem,
) map[string]struct{} {
	attached := make(map[string]struct{})
	if state == nil || state.Tree == nil || stats == nil || len(singletonIDs) == 0 {
		return attached
	}
	signatures := buildSingletonAttachmentTopicSignatures(state, mc, itemByID)
	if len(signatures) == 0 {
		return attached
	}
	ordered := append([]string(nil), singletonIDs...)
	sort.Strings(ordered)
	for _, itemID := range ordered {
		item, active := itemByID[itemID]
		if !active {
			continue
		}
		node := liveTreeNodeByID(state.Tree, itemID)
		if node == nil {
			continue
		}
		decision := singletonAttachmentDecision{
			ItemID:              itemID,
			SourceParentID:      node.ParentID,
			CandidateTopicCount: len(signatures),
			EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...),
			Decision:            singletonAttachmentDeferred,
		}
		if !singletonAttachmentGroundedItem(*item) {
			decision.Reason = "item_not_grounded"
			stats.SingletonAttachmentDeferred++
			stats.SingletonAttachmentDecisions = append(stats.SingletonAttachmentDecisions, decision)
			continue
		}
		stats.SingletonAttachmentEligible++

		signature := buildSingletonAttachmentItemSignature(*item, state.Tree)
		candidates := make([]singletonAttachmentCandidate, 0, len(signatures))
		for _, topic := range signatures {
			candidates = append(candidates, evaluateSingletonAttachmentCandidate(signature, topic))
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].Eligible != candidates[j].Eligible {
				return candidates[i].Eligible
			}
			if candidates[i].Score != candidates[j].Score {
				return candidates[i].Score > candidates[j].Score
			}
			return candidates[i].TopicID < candidates[j].TopicID
		})
		best := candidates[0]
		second := singletonAttachmentCandidate{}
		if len(candidates) > 1 && candidates[1].Eligible {
			second = candidates[1]
		}
		decision.SubjectMatchStrength = best.Subject.Strength
		decision.PredicateCompatibility = best.Predicate
		decision.RelationSupport = best.Relation
		decision.EvidenceSupport = best.Evidence
		decision.AgendaSupport = best.Agenda
		decision.DiscourseSupport = best.Discourse
		decision.GenericOverlapPenalty = best.Subject.GenericPenalty
		if best.Subject.Conflicting {
			decision.ContradictionPenalty = 1
		}
		decision.SecondBestScore = second.Score
		if best.Eligible {
			decision.BestScore = best.Score
			decision.ScoreMargin = best.Score - second.Score
		}

		switch {
		case !best.Eligible:
			decision.Reason = best.Reason
			stats.SingletonAttachmentDeferred++
		case best.Score < singletonAttachmentScoreFloor:
			decision.Reason = "combined_score_below_floor"
			stats.SingletonAttachmentDeferred++
		case decision.ScoreMargin < singletonAttachmentScoreMargin:
			decision.Decision = singletonAttachmentAmbiguous
			decision.Reason = "multiple_topics_equally_plausible"
			stats.SingletonAttachmentAmbiguous++
		default:
			decision.Decision = singletonAttachmentApplied
			decision.Reason = "grounded_subject_and_supporting_relation"
			decision.TargetTopicID = best.TopicID
			assignUnclassifiedItemToTopic(
				state, item, best.TopicID, singletonAttachmentAssignmentSource, version,
			)
			attached[itemID] = struct{}{}
			stats.SingletonAttachmentApplied++
			stats.UnclassifiedItemsReparented++
			stats.UnclassifiedDecisions = append(stats.UnclassifiedDecisions, finalUnclassifiedDecision{
				ItemID: itemID, CandidateID: signature.CandidateTopicID,
				Decision: finalUnclassifiedReparentedExisting, TopicID: best.TopicID,
				ComponentSize: 1, Signals: best.Subject.Terms,
			})
		}
		stats.SingletonAttachmentDecisions = append(stats.SingletonAttachmentDecisions, decision)
	}
	return attached
}
