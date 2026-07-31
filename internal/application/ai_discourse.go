package application

import (
	"regexp"
	"sort"
	"strings"

	"deciscope-core-api/internal/domain"
)

// このファイルは会話制御発話(discourse act)の判定を持つ。
// 「以上をまとめます」「次の話題へ移ります」のような、会議の進行だけを表す
// 発話は議論内容ではないため、fact/issue/question/todo/decision/topicの
// いずれにもしない。判定は文字列完全一致の禁止リストではなく、正規化した
// 発話全体が制御表現で構成されているかどうかで行う。

type discourseAct string

type liveEvidenceRole string

// liveUtteranceRole is the model-facing utterance classification. The
// deterministic classifier below remains authoritative for obvious control
// speech; model roles add coverage for paraphrases that cannot be captured by
// a finite phrase list.
type liveUtteranceRole string

const (
	liveUtteranceSubstantive         liveUtteranceRole = "substantive"
	liveUtteranceCorrection          liveUtteranceRole = "correction"
	liveUtteranceRecap               liveUtteranceRole = "recap"
	liveUtteranceDiscourseTransition liveUtteranceRole = "discourse_transition"
	liveUtteranceFiller              liveUtteranceRole = "filler"
)

type liveUtteranceRoleRef struct {
	SequenceNo int64             `json:"sequenceNo"`
	Role       liveUtteranceRole `json:"role"`
}

const (
	liveEvidencePrimary        liveEvidenceRole = "primary"
	liveEvidenceSupporting     liveEvidenceRole = "supporting"
	liveEvidenceReferenceRecap liveEvidenceRole = "reference_recap"
	liveEvidenceDiscourseOnly  liveEvidenceRole = "discourse_only"
	liveEvidenceCorrection     liveEvidenceRole = "correction"
)

type liveEvidenceRoleRef struct {
	SequenceNo int64            `json:"sequenceNo"`
	Role       liveEvidenceRole `json:"role"`
}

type discourseTimeline struct {
	Roles         map[int64]liveEvidenceRole
	DetectedRoles map[int64]liveUtteranceRole
	Transitions   []discourseTimelineTransition
}

type discourseTimelineTransition struct {
	SequenceNo int64
	From       string
	To         string
	Act        discourseAct
}

const (
	// discourseContent: 通常の議論内容(既定)。
	discourseContent discourseAct = "content"
	// discourseRecapIntro: まとめ・整理の開始宣言。「以上をまとめます」等。
	discourseRecapIntro discourseAct = "recap_intro"
	// discourseTopicTransition: 議題遷移の宣言。「次の話題へ移ります」等。
	discourseTopicTransition discourseAct = "topic_transition"
	// discourseMeetingControl: 開始・終了・挨拶などの会議運営発話。
	discourseMeetingControl discourseAct = "meeting_control"
	// discourseFiller: 命題を持たない短いフィラー。
	discourseFiller discourseAct = "filler"
)

// discourseFillerPattern は判定前に取り除く前置き・フィラー。制御表現の
// 前後に付いても判定が揺れないようにする。
var discourseFillerPattern = regexp.MustCompile(`^(それでは|それじゃ|では|じゃあ|はい|ええと|えっと|ええ|あの|さて|続いて|最後に|次に|一旦|それで)+`)

// 各discourse actの中核表現。正規化(句読点・空白除去)済みの発話全体が
// これらで構成される場合のみ制御発話とみなす。部分一致では内容発話を
// 巻き込むため、アンカー付きで全体一致に近い形にしている。
var (
	recapIntroPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^(以上|ここまで|本日の内容|今日の内容|これまでの内容)?(を|で|は)?(まとめ|整理|要約|振り返り)(ます|ましょう|させていただきます|たいと思います|します|すると|ると)?$`),
		regexp.MustCompile(`^(結論|決定事項|本日のまとめ)(として|を)?(確認|整理|共有)(します|させてください|しましょう)?$`),
	}
	topicTransitionPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^次の(話題|議題|テーマ|アジェンダ|項目)(へ|に)?(移り|移し|進み|入り)(ます|ましょう|たいと思います)?$`),
		regexp.MustCompile(`^(以上で)?(この|本)(議題|話題|テーマ|項目)(は|を)?(終わり|終了|以上)(ます|です|とします|にします)?$`),
		regexp.MustCompile(`^(?:次に)?(?:進み|移り)(?:ます|ましょう)$`),
	}
	meetingControlPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^(会議|ミーティング|定例)(を|は)?(開始|終了|再開|中断)(します|しましょう|とします)?$`),
		regexp.MustCompile(`^(よろしくお願いします|お疲れ様でした|ありがとうございました|聞こえますか|始めましょう|終わりましょう)$`),
	}
	meetingEndControlPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^(?:今日|本日)(?:の(?:会議|ミーティング|議事))?(?:は)?(?:これで|ここまで|以上)(?:に|で|と)?(?:します|しましょう|です|終了します|終わります|とします)?(?:ありがとうございました)?$`),
		regexp.MustCompile(`^(?:今日|本日)?(?:は)?(?:これで|ここまで|以上で)(?:会議|ミーティング|議事)?(?:を|は)?(?:終了|終わり|終わります|終了します|終えます|お開き)(?:に|と)?(?:します|しましょう|です)?(?:ありがとうございました)?$`),
		regexp.MustCompile(`^(?:今日|本日)(?:の)?(?:会議|ミーティング|議事)(?:を)?(?:ここで)?(?:打ち切る|終了する|終える)(?:決定)?$`),
	}
)

// Structural transition detection intentionally combines three independent
// features instead of matching complete phrases: a discourse-relative
// selector, a meta discussion object, and a control/existence predicate. A
// concrete domain signal (number, identifier, assignee/deadline, impact, or
// explicit decision/action) keeps the utterance substantive.
var (
	discourseTransitionSelectorPattern  = regexp.MustCompile(`(?i)(?:次|別|追加|新(?:しい|たな)|少し|本題外|アジェンダ外|議題外|another|next|additional|different|separate)`)
	discourseTransitionObjectPattern    = regexp.MustCompile(`(?i)(?:話(?:題)?|議題|論点|問題|テーマ|項目|件|本題|アジェンダ|topic|issue|subject|agenda|matter)`)
	discourseTransitionPredicatePattern = regexp.MustCompile(`(?i)(?:あり|存在|移り|移し|進み|変え|切り替|入り|始め|取り上げ|話し|です|ですが|move|switch|change|proceed|have|thereis|introduce)`)
	discourseConcretePattern            = regexp.MustCompile(`(?i)(?:[0-9Ａ-Ｚａ-ｚA-Za-z]|まで|期限|担当|さん|氏|影響|不能|停止|遅延|漏れ|期限切れ|承認|決定|実施|作成|更新|修正|調査|確認して|依頼|対応する|リスク|可能性)`)
)

// discourseCorrectionPattern is intentionally limited to explicit
// self-correction language. Generic business actions such as
// 「設定を修正しました」 are substantive facts, not corrections.
var discourseCorrectionPattern = regexp.MustCompile(
	`(?:訂正(?:します|すると|です)?|撤回(?:します|する)?|取り消(?:します|す)?|正確には|厳密には|言い直すと|(?:先ほど|先程|さっき)(?:の(?:説明|発言|内容))?.{0,40}(?:ではなく|じゃなく|違|誤|改め)|いえ[、,]?(?:正確には|厳密には|そうではなく))`,
)

// A recap may quote a past corrective action ("設定を修正しました") without
// introducing a new correction now. The legacy broad marker is retained only
// as a recap-boundary escape hatch so such substantive recovery text is not
// downgraded to reference-only evidence.
var discourseRecapCorrectionPattern = regexp.MustCompile(`(?:訂正|修正|変更|撤回|取り消|追加事項|新たな(?:決定|論点|課題)|まとめに追加|先ほどの.+(?:ではなく|を改め))`)

// 議論再開の判定は、前置きマーカー(これから何をするかの宣言)と審議述語
// (決める・検討する・議論する等、これから行う行為)の共起で行う。単独の語句
// リストではなく2要素の共起にしているのは、「変更します」のような通常の内容
// 発話を再開宣言と誤認しないためである。
//
// この判定は classifyDiscourseTimelineWithModel のrecapモード中でのみ参照され、
// classifyDiscourseAct の結果は変えない。誤検知の最大の影響は「recap抑制を
// 早めに解除する」ことに限られ、item破棄側へは倒れない。
var (
	// 前置きマーカーは発話冒頭に限る。「原因は設定ではなく…と考えます」のように
	// 文中に「では」を含むだけの内容発話を再開宣言と誤認しないため。
	discussionResumptionLeadPattern      = regexp.MustCompile(`^(?:それでは|それじゃ|では|じゃあ|ここからは|ここから|これから|今後|次に|続いて|まずは)`)
	discussionResumptionPredicatePattern = regexp.MustCompile(`(?:決め(?:ましょう|ます|たい|ていきましょう)|検討(?:し(?:ます|ましょう|たい)|に入り(?:ます|ましょう))|議論(?:し(?:ます|ましょう|たい)|に入り(?:ます|ましょう))|話し(?:ます|ましょう|合いましょう)|相談し(?:ます|ましょう)|考え(?:ます|ましょう)|(?:に|へ)移り(?:ます|ましょう)|進め(?:ます|ましょう))`)
)

// isDiscussionResumption reports whether an utterance declares that the meeting
// is moving from looking back to deciding or investigating what comes next
// (「では、再発防止策を決めましょう」「それでは原因の調査に移ります」など)。
// recapモードの解除条件としてのみ使用する。
func isDiscussionResumption(text string) bool {
	normalized := normalizeDiscourseText(text)
	if normalized == "" {
		return false
	}
	return discussionResumptionLeadPattern.MatchString(normalized) &&
		discussionResumptionPredicatePattern.MatchString(normalized)
}

// classifyDiscourseAct は発話テキストのdiscourse actを判定する。
// 制御表現とフィラーだけで構成された短い発話のみを制御発話として扱い、
// 実質的な内容を含む発話は content のままにする。
func classifyDiscourseAct(text string) discourseAct {
	normalized := normalizeDiscourseText(text)
	if normalized == "" {
		return discourseContent
	}
	// 制御発話は宣言のみの短い発話に限る。長い発話はまとめ宣言で始まっても
	// 具体的内容を含むため content とする。
	if len([]rune(normalized)) > 30 {
		return discourseContent
	}
	stripped := discourseFillerPattern.ReplaceAllString(normalized, "")
	if stripped == "" {
		return discourseFiller
	}
	if isMeetingEndControlNormalized(stripped) {
		return discourseMeetingControl
	}
	for _, pattern := range recapIntroPatterns {
		if pattern.MatchString(stripped) {
			return discourseRecapIntro
		}
	}
	for _, pattern := range topicTransitionPatterns {
		if pattern.MatchString(stripped) {
			return discourseTopicTransition
		}
	}
	if structurallyDiscourseTransition(stripped) {
		return discourseTopicTransition
	}
	for _, pattern := range meetingControlPatterns {
		if pattern.MatchString(stripped) {
			return discourseMeetingControl
		}
	}
	return discourseContent
}

func isMeetingEndControl(text string) bool {
	normalized := discourseFillerPattern.ReplaceAllString(normalizeDiscourseText(text), "")
	return isMeetingEndControlNormalized(normalized)
}

func isMeetingEndControlNormalized(normalized string) bool {
	for _, pattern := range meetingEndControlPatterns {
		if pattern.MatchString(normalized) {
			return true
		}
	}
	return false
}

func isMeetingEndOnlyItem(title, body string) bool {
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if title == "" && body == "" {
		return false
	}
	return (title == "" || isMeetingEndControl(title)) && (body == "" || isMeetingEndControl(body))
}

func structurallyDiscourseTransition(normalized string) bool {
	normalized = strings.ToLower(strings.TrimSpace(normalized))
	if normalized == "" || discourseConcretePattern.MatchString(normalized) {
		return false
	}
	return discourseTransitionSelectorPattern.MatchString(normalized) &&
		discourseTransitionObjectPattern.MatchString(normalized) &&
		discourseTransitionPredicatePattern.MatchString(normalized)
}

// hasLeadingRecapIntroClause reports whether text's first non-empty clause
// (split the same way detectDecisionCandidates splits sentences) is itself a
// recap-introduction declaration, even when the utterance as a whole is too
// long for classifyDiscourseAct's 30-rune control-speech budget (e.g. 「最後に
// ここまでをまとめます。今回の障害は…」のように、recap宣言が長い発話の先頭節に
// 埋め込まれているケース)。単独のrecap宣言発話(discourse_only)の既存判定は
// classifyDiscourseAct のまま変更しない -- これはあくまで最初の節だけを見る
// 追加の判定である。
func hasLeadingRecapIntroClause(text string) bool {
	for _, rawClause := range decisionClauseSplitPattern.Split(text, -1) {
		clause := strings.TrimSpace(rawClause)
		if clause == "" {
			continue
		}
		normalized := normalizeDiscourseText(clause)
		stripped := discourseFillerPattern.ReplaceAllString(normalized, "")
		if stripped == "" || len([]rune(stripped)) > 30 {
			return false
		}
		for _, pattern := range recapIntroPatterns {
			if pattern.MatchString(stripped) {
				return true
			}
		}
		return false
	}
	return false
}

// isDiscourseOnlyText は発話・ラベルが制御発話のみかどうかを返す。
func isDiscourseOnlyText(text string) bool {
	return classifyDiscourseAct(text) != discourseContent
}

// isDiscourseOnlyItem はitemのtitle/bodyがどちらも制御発話(または空)で、
// 議論内容を含まない場合にtrueを返す。
func isDiscourseOnlyItem(title, body string) bool {
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if title == "" && body == "" {
		return false
	}
	if title != "" && !isDiscourseOnlyText(title) {
		return false
	}
	if body != "" && !isDiscourseOnlyText(body) {
		return false
	}
	return true
}

// normalizeDiscourseText は句読点・空白・記号を除いた判定用の文字列を返す。
func normalizeDiscourseText(text string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(text) {
		switch r {
		case '。', '、', '，', '．', '!', '！', '?', '？', ' ', '　', '\t', '\n', '\r', '・', '…':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// classifyDiscourseTimeline interprets discourse acts as state transitions.
// Content after a recap introduction is reference evidence until a genuine
// topic transition or an explicit correction occurs. This prevents a recap
// from creating a second decision, issue, or topic while preserving it as
// supporting evidence for an existing canonical proposition.
func classifyDiscourseTimeline(scope liveEvidenceScope) discourseTimeline {
	return classifyDiscourseTimelineWithModel(scope, nil)
}

func classifyDiscourseTimelineWithModel(scope liveEvidenceScope, modelRoles []liveUtteranceRoleRef) discourseTimeline {
	timeline := discourseTimeline{Roles: make(map[int64]liveEvidenceRole), DetectedRoles: make(map[int64]liveUtteranceRole)}
	modelBySequence := make(map[int64]liveUtteranceRole, len(modelRoles))
	for _, ref := range modelRoles {
		if ref.SequenceNo <= 0 || !validLiveUtteranceRole(ref.Role) {
			continue
		}
		if _, allowed := scope.Allowed[ref.SequenceNo]; !allowed {
			if _, current := scope.CurrentRound[ref.SequenceNo]; !current {
				continue
			}
		}
		modelBySequence[ref.SequenceNo] = ref.Role
	}
	sequenceNos := make([]int64, 0, len(scope.Allowed)+len(scope.CurrentRound))
	seen := make(map[int64]struct{})
	for sequenceNo := range scope.Allowed {
		seen[sequenceNo] = struct{}{}
		sequenceNos = append(sequenceNos, sequenceNo)
	}
	for sequenceNo := range scope.CurrentRound {
		if _, exists := seen[sequenceNo]; !exists {
			sequenceNos = append(sequenceNos, sequenceNo)
		}
	}
	sort.Slice(sequenceNos, func(i, j int) bool { return sequenceNos[i] < sequenceNos[j] })
	mode := "content"
	for _, sequenceNo := range sequenceNos {
		text := scope.TranscriptText[sequenceNo]
		if segment, exists := scope.Segments[sequenceNo]; exists && strings.TrimSpace(segment.Text) != "" {
			text = segment.Text
		}
		act := classifyDiscourseAct(text)
		switch act {
		case discourseRecapIntro:
			timeline.Roles[sequenceNo] = liveEvidenceDiscourseOnly
			timeline.DetectedRoles[sequenceNo] = liveUtteranceRecap
			timeline.Transitions = append(timeline.Transitions, discourseTimelineTransition{SequenceNo: sequenceNo, From: mode, To: "recap", Act: act})
			mode = "recap"
		case discourseTopicTransition:
			timeline.Roles[sequenceNo] = liveEvidenceDiscourseOnly
			timeline.DetectedRoles[sequenceNo] = liveUtteranceDiscourseTransition
			timeline.Transitions = append(timeline.Transitions, discourseTimelineTransition{SequenceNo: sequenceNo, From: mode, To: "content", Act: act})
			mode = "content"
		case discourseMeetingControl:
			timeline.Roles[sequenceNo] = liveEvidenceDiscourseOnly
			timeline.DetectedRoles[sequenceNo] = liveUtteranceFiller
		case discourseFiller:
			timeline.Roles[sequenceNo] = liveEvidenceDiscourseOnly
			timeline.DetectedRoles[sequenceNo] = liveUtteranceFiller
		case discourseContent:
			if mode != "recap" && hasLeadingRecapIntroClause(text) {
				timeline.Transitions = append(timeline.Transitions, discourseTimelineTransition{SequenceNo: sequenceNo, From: mode, To: "recap", Act: discourseRecapIntro})
				mode = "recap"
				timeline.Roles[sequenceNo] = liveEvidenceReferenceRecap
				timeline.DetectedRoles[sequenceNo] = liveUtteranceRecap
				continue
			}
			modelRole := modelBySequence[sequenceNo]
			switch modelRole {
			case liveUtteranceDiscourseTransition, liveUtteranceFiller:
				timeline.Roles[sequenceNo] = liveEvidenceDiscourseOnly
				timeline.DetectedRoles[sequenceNo] = modelRole
				if modelRole == liveUtteranceDiscourseTransition {
					timeline.Transitions = append(timeline.Transitions, discourseTimelineTransition{SequenceNo: sequenceNo, From: mode, To: "content", Act: discourseTopicTransition})
					mode = "content"
				}
			case liveUtteranceCorrection:
				timeline.Roles[sequenceNo] = liveEvidenceCorrection
				timeline.DetectedRoles[sequenceNo] = liveUtteranceCorrection
			case liveUtteranceRecap:
				timeline.Roles[sequenceNo] = liveEvidenceReferenceRecap
				timeline.DetectedRoles[sequenceNo] = liveUtteranceRecap
			case liveUtteranceSubstantive:
				// モデルが明示的に実質的発話だと判断した場合は、ルールベースの
				// recapモードより意味判定を優先する。単独の「振り返ります。」の
				// ようなSTT断片で始まったrecapが、実際には続いている通常議論を
				// 飲み込み続けるのを防ぐ。
				if mode == "recap" {
					timeline.Transitions = append(timeline.Transitions, discourseTimelineTransition{SequenceNo: sequenceNo, From: mode, To: "content", Act: discourseTopicTransition})
					mode = "content"
				}
				timeline.Roles[sequenceNo] = liveEvidencePrimary
				timeline.DetectedRoles[sequenceNo] = liveUtteranceSubstantive
			default:
				if mode == "recap" {
					// 「では、再発防止策を決めましょう」のような議論再開宣言は
					// recapの終端である。明示的な話題転換表現だけを解除条件に
					// すると、自然な議論再開で会議の残り全部がrecapのままになる。
					if isDiscussionResumption(text) {
						timeline.Roles[sequenceNo] = liveEvidenceDiscourseOnly
						timeline.DetectedRoles[sequenceNo] = liveUtteranceDiscourseTransition
						timeline.Transitions = append(timeline.Transitions, discourseTimelineTransition{SequenceNo: sequenceNo, From: mode, To: "content", Act: discourseTopicTransition})
						mode = "content"
						continue
					}
					if discourseRecapCorrectionPattern.MatchString(text) {
						timeline.Roles[sequenceNo] = liveEvidenceCorrection
						timeline.DetectedRoles[sequenceNo] = liveUtteranceCorrection
					} else {
						timeline.Roles[sequenceNo] = liveEvidenceReferenceRecap
						timeline.DetectedRoles[sequenceNo] = liveUtteranceRecap
					}
				} else {
					timeline.Roles[sequenceNo] = liveEvidencePrimary
					timeline.DetectedRoles[sequenceNo] = liveUtteranceSubstantive
				}
			}
		}
	}
	return timeline
}

func validLiveUtteranceRole(role liveUtteranceRole) bool {
	switch role {
	case liveUtteranceSubstantive, liveUtteranceCorrection, liveUtteranceRecap,
		liveUtteranceDiscourseTransition, liveUtteranceFiller:
		return true
	default:
		return false
	}
}

func evidenceRolesForItem(sequenceNos []int64, timeline discourseTimeline) []liveEvidenceRoleRef {
	refs := make([]liveEvidenceRoleRef, 0, len(sequenceNos))
	for _, sequenceNo := range sequenceNos {
		role := timeline.Roles[sequenceNo]
		if role == "" {
			role = liveEvidenceSupporting
		}
		refs = append(refs, liveEvidenceRoleRef{SequenceNo: sequenceNo, Role: role})
	}
	return refs
}

func evidenceIsReferenceOnly(sequenceNos []int64, timeline discourseTimeline) bool {
	if len(sequenceNos) == 0 {
		return false
	}
	for _, sequenceNo := range sequenceNos {
		role := timeline.Roles[sequenceNo]
		if role != liveEvidenceReferenceRecap && role != liveEvidenceDiscourseOnly {
			return false
		}
	}
	return true
}

func segmentFromEvidenceScope(scope liveEvidenceScope, sequenceNo int64) domain.TranscriptSegment {
	if segment, exists := scope.Segments[sequenceNo]; exists {
		return segment
	}
	return domain.TranscriptSegment{SequenceNo: sequenceNo, Text: scope.TranscriptText[sequenceNo], IsFinal: true}
}
