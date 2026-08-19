package application

import (
	"regexp"
	"sort"
	"strings"
)

// riskFuturePattern matches explicit future-adverse-impact language ("〜の
// 可能性がある", "おそれがある", "なりかねない"). riskExcludePattern rules
// out cause-inference phrasing ("〜が原因である可能性が高い") that shares
// the same "可能性" vocabulary but is not a risk. riskSupportPattern
// requires a conditional/adverse-consequence cue in the same sentence so a
// bare "可能性がある" without any named downside never synthesizes a risk.
var (
	riskFuturePattern  = regexp.MustCompile(`(?:可能性があ(?:る|ります)|お(?:それ|それ)があ|恐れがあ|リスクがあ|なりかねな(?:い|く)|なりかねません)`)
	riskExcludePattern = regexp.MustCompile(`(?:である可能性|可能性が(?:最も)?(?:高い|低い)|可能性が高いと)`)
	riskSupportPattern = regexp.MustCompile(`(?:すると|しないと|放置すると|のままだと|なければ|場合|できなくな|停止|障害|過多|多くなりすぎ|増えすぎ|通知が多発|アラート疲れ|運用負荷が高く|監視ノイズが増え|見落としにつなが|期限切れ|失われ|漏れ|遅延|接続できな)`)
	// riskTitleTrailingPattern strips the sentence-final risk boilerplate
	// ("という可能性があります" etc.) that explicitRiskItemTitle would
	// otherwise carry verbatim into the card title.
	riskTitleTrailingPattern = regexp.MustCompile(`(?:という)?(?:可能性があ(?:る|ります)|お(?:それ|それ)があ(?:る|ります)|恐れがあ(?:る|ります)|リスクがあ(?:る|ります)|なりかねな(?:い|く)(?:なります)?|なりかねません)$`)
	// issueDistinctActionPropositionPattern matches an explicit
	// action/undecided marker ("〜が必要", "次回までに検討", "〜を検討します")
	// that names its own proposition (a pending decision/action) distinct
	// from a co-occurring risk statement in the same sentence (e.g. 「監査
	// 対象を増やすとアラートが多くなりすぎる可能性がある。監視間隔と通知条件
	// については、次回までに検討が必要です。」). A bare possibility statement
	// without this marker (「放置するとリモート接続に影響する可能性がある」)
	// does not match and keeps the existing migrate/dedup behavior.
	issueDistinctActionPropositionPattern = regexp.MustCompile(`(?:検討|確認|調査|対応|見直し)(?:すること|する)?が必要|次回までに.{0,40}(?:検討|確認|調査)|を(?:検討|確認|調査)します`)
	riskExcessTitlePattern                = regexp.MustCompile(`^(.+?)を増やすと(.+?)(?:が)?(?:多くなりすぎる|増えすぎる|過多になる)$`)
	openIssueTitlePattern                 = regexp.MustCompile(`^(.+?)(?:について)?は?[、,]?(?:次回までに)?(?:検討|確認|調査|対応|見直し)(?:すること)?が必要(?:です)?$`)
)

// issueCarriesDistinctActionProposition reports whether item's own text
// names an explicit pending action/decision that is a separate proposition
// from a risk statement it may share evidence with. When true, the risk/issue
// same-evidence migration and dedup in this file (and
// mergeSameEvidenceCrossKindDuplicates in ai_analysis.go) must not collapse
// the issue into the risk -- both are kept so the action proposition is not
// lost.
func issueCarriesDistinctActionProposition(item liveAnalysisItem) bool {
	return issueDistinctActionPropositionPattern.MatchString(item.Title + item.Body)
}

// liveAnalysisRoundMaxSynthesizedRiskItems caps how many risk items
// synthesizeExplicitRiskItems will create from one round's utterances.
const liveAnalysisRoundMaxSynthesizedRiskItems = 2

// synthesizeExplicitRiskItems is the deterministic server-side counterpart to
// the v15 risk extraction prompt rule: for rounds where the model omits an
// explicit future-adverse-impact utterance, it scans this round's final
// transcript rows and synthesizes a kind=risk item when the wording clearly
// names a future downside (not a cause-inference statement). It never
// suppresses a candidate because a related issue/todo already exists --
// risk and its mitigating issue/todo are meant to coexist -- and it never
// reads reference/recap/discourse-only utterances.
func synthesizeExplicitRiskItems(previous, diff []liveAnalysisItem, scope liveEvidenceScope, timeline discourseTimeline, stats *liveAnalysisTreeMergeStats) []liveAnalysisItem {
	sequenceNos := make([]int64, 0, len(scope.CurrentRound))
	for sequenceNo := range scope.CurrentRound {
		sequenceNos = append(sequenceNos, sequenceNo)
	}
	sort.Slice(sequenceNos, func(i, j int) bool { return sequenceNos[i] < sequenceNos[j] })

	existingRisks := make([]liveAnalysisItem, 0, len(previous)+len(diff))
	for _, item := range previous {
		if item.Kind == "risk" {
			existingRisks = append(existingRisks, item)
		}
	}
	for _, item := range diff {
		if item.Kind == "risk" {
			existingRisks = append(existingRisks, item)
		}
	}

	synthesized := make([]liveAnalysisItem, 0, liveAnalysisRoundMaxSynthesizedRiskItems)
	for _, sequenceNo := range sequenceNos {
		if len(synthesized) >= liveAnalysisRoundMaxSynthesizedRiskItems {
			break
		}
		text := strings.TrimSpace(scope.TranscriptText[sequenceNo])
		riskSentence := explicitRiskSentence(text)
		if text == "" || riskSentence == "" || riskExcludePattern.MatchString(riskSentence) || !riskSupportPattern.MatchString(riskSentence) {
			continue
		}
		if evidenceRoleIsReference(sequenceNo, timeline) {
			continue
		}
		title := explicitRiskItemTitle(text)
		if title == "" {
			continue
		}
		probe := liveAnalysisItem{
			Kind: "risk", Title: title, Body: riskSentence, Status: "open",
			EvidenceSequenceNos: []int64{sequenceNo},
		}
		validation := evaluateLiveItemKind(probe, liveEvidenceScope{}, "risk_synthesis")
		if validation.CanonicalKind != "risk" ||
			validation.Confidence < itemKindValidationThreshold(itemKindValidationLive) {
			continue
		}
		// 同一発話(sequenceNo)だけをevidenceに持つdiscussion issueが既に提案
		// されている場合、そのissueとこのrisk合成は実質同じ発話の重複表現
		// (modelのissue抽出とサーバーのrisk合成が併存してしまう)。diff側は
		// risk側へ移行(kind書き換え)してrisk合成をスキップし、previous
		// (確定済み)側は合成のみをスキップする(既存itemのkindは触らない)。
		if migrateSameSentenceDiscussionIssueToRisk(diff, text, sequenceNo, stats) {
			continue
		}
		if sameSentenceDiscussionIssueExists(previous, text, sequenceNo) {
			continue
		}
		if riskItemDuplicatesExisting(text, sequenceNo, existingRisks) {
			continue
		}
		item := liveAnalysisItem{
			Kind: "risk", Severity: "medium", Title: title, Body: riskSentence, Status: "open",
			EvidenceSequenceNos: []int64{sequenceNo}, evidenceSpecified: true,
		}
		item.ID = serverGeneratedItemID(item)
		synthesized = append(synthesized, item)
		existingRisks = append(existingRisks, item)
		if stats != nil {
			stats.RiskItemsSynthesized++
		}
	}
	return synthesized
}

// sameSentenceDiscussionIssueEvidence reports whether item is a discussion
// issue whose sole evidence is sequenceNo and whose text closely matches the
// sentence at that sequence -- the shape produced when the model's issue
// extraction and synthesizeExplicitRiskItems both react to the same sentence
// (e.g. group-dd702579aa54's issue と risk が同一発話由来で併存するケース)。
func sameSentenceDiscussionIssueEvidence(item liveAnalysisItem, text string, sequenceNo int64) bool {
	if item.Kind != "issue" || item.Subtype != issueSubtypeDiscussion {
		return false
	}
	if len(item.EvidenceSequenceNos) != 1 || item.EvidenceSequenceNos[0] != sequenceNo {
		return false
	}
	if issueCarriesDistinctActionProposition(item) {
		return false
	}
	return semanticItemSimilarity(item.Title+" "+item.Body, text) >= 0.5
}

// migrateSameSentenceDiscussionIssueToRisk migrates the first diff issue
// matching sameSentenceDiscussionIssueEvidence into a risk item in place
// (clearing Subtype), so the same sentence does not end up with both a
// model-proposed discussion issue and a server-synthesized risk item.
func migrateSameSentenceDiscussionIssueToRisk(diff []liveAnalysisItem, text string, sequenceNo int64, stats *liveAnalysisTreeMergeStats) bool {
	for i := range diff {
		if !sameSentenceDiscussionIssueEvidence(diff[i], text, sequenceNo) {
			continue
		}
		diff[i].Kind = "risk"
		diff[i].Subtype = ""
		if stats != nil {
			stats.SemanticKindMigrations++
		}
		return true
	}
	return false
}

// sameSentenceDiscussionIssueExists reports whether previous (already
// persisted, canonical state) holds a discussion issue matching
// sameSentenceDiscussionIssueEvidence. previous items are never rewritten
// here -- only the pending risk synthesis for this sentence is skipped.
func sameSentenceDiscussionIssueExists(previous []liveAnalysisItem, text string, sequenceNo int64) bool {
	for _, item := range previous {
		if sameSentenceDiscussionIssueEvidence(item, text, sequenceNo) {
			return true
		}
	}
	return false
}

// riskItemDuplicatesExisting reports whether a candidate risk sentence
// already has a same-subject risk item on record, so a risk already named in
// an earlier round is never re-synthesized under a new id.
func riskItemDuplicatesExisting(text string, sequenceNo int64, existingRisks []liveAnalysisItem) bool {
	for _, existing := range existingRisks {
		existingText := existing.Title + " " + existing.Body
		if semanticItemSimilarity(existingText, text) >= 0.48 {
			return true
		}
		if sharedTreeAuditSubjectTerm(semanticTopicCore(existingText), semanticTopicCore(text)) && semanticItemSimilarity(existingText, text) >= 0.30 {
			return true
		}
		for _, evidenceSequence := range existing.EvidenceSequenceNos {
			if evidenceSequence == sequenceNo {
				return true
			}
		}
	}
	return false
}

// explicitRiskItemTitle derives a card title from the sentence within text
// that actually carries the risk language, trimming the sentence-final
// "という可能性があります" style boilerplate and truncating to the same
// 40-rune budget explicitClosureIssueTitle uses.
func explicitRiskItemTitle(text string) string {
	subject := explicitRiskSentence(text)
	if subject == "" {
		return ""
	}
	subject = riskTitleTrailingPattern.ReplaceAllString(subject, "")
	subject = strings.Trim(strings.TrimSpace(subject), "、。 ")
	subject = strings.TrimPrefix(subject, "ただし、")
	subject = strings.TrimPrefix(subject, "ただし")
	if matches := riskExcessTitlePattern.FindStringSubmatch(subject); len(matches) == 3 {
		subject = strings.TrimSpace(matches[1]) + "拡大による" + strings.TrimSpace(matches[2]) + "過多リスク"
	}
	if subject == "" {
		return ""
	}
	return truncateRunes(subject, 40)
}

// synthesizeExplicitOpenIssueItems keeps a pending decision/action named in a
// compound risk utterance as its own Issue. It also rewrites a model-proposed
// aggregate Issue to the atomic sentence, preventing the adjacent Risk from
// being absorbed into the open question merely because they cite one segment.
func synthesizeExplicitOpenIssueItems(previous, diff []liveAnalysisItem, scope liveEvidenceScope, timeline discourseTimeline) []liveAnalysisItem {
	known := append(append([]liveAnalysisItem(nil), previous...), diff...)
	var synthesized []liveAnalysisItem
	for _, sequenceNo := range currentEvidenceSequenceNos(scope) {
		if evidenceRoleIsReference(sequenceNo, timeline) {
			continue
		}
		evidenceText := strings.TrimSpace(scope.TranscriptText[sequenceNo])
		riskSentence := explicitRiskSentence(evidenceText)
		// This repair is intentionally limited to a compound utterance that
		// contains both a future adverse impact and a separate pending action.
		// A normal current issue such as "原因不明で調査が必要" must retain its
		// full proposition and resolution identity.
		if riskSentence == "" || riskExcludePattern.MatchString(riskSentence) ||
			!riskSupportPattern.MatchString(riskSentence) {
			continue
		}
		for _, sentence := range kindSentenceBoundaryPattern.Split(evidenceText, -1) {
			sentence = strings.Trim(strings.TrimSpace(sentence), "、。.!！ ")
			if sentence == "" || !issueDistinctActionPropositionPattern.MatchString(sentence) {
				continue
			}
			title := explicitOpenIssueTitle(sentence)
			if title == "" {
				continue
			}
			rewritten := false
			for index := range diff {
				if diff[index].Kind != "issue" || !containsInt64(diff[index].EvidenceSequenceNos, sequenceNo) ||
					!issueCarriesDistinctActionProposition(diff[index]) {
					continue
				}
				diff[index].Title = title
				diff[index].Body = sentence
				diff[index].Subtype = issueSubtypeDiscussion
				diff[index].EvidenceSequenceNos = []int64{sequenceNo}
				diff[index].EvidenceSnippets = []string{sentence}
				rewritten = true
				break
			}
			if rewritten || explicitOpenIssueRepresented(known, sentence, sequenceNo) {
				continue
			}
			item := liveAnalysisItem{
				Kind: "issue", Subtype: issueSubtypeDiscussion, Severity: "medium",
				Title: title, Body: sentence, Status: "open",
				InformationStatus:   informationStatusGrounded,
				EvidenceSequenceNos: []int64{sequenceNo}, EvidenceSnippets: []string{sentence},
				evidenceSpecified: true,
			}
			item.ID = serverGeneratedItemID(item)
			synthesized = append(synthesized, item)
			known = append(known, item)
		}
	}
	return synthesized
}

func explicitOpenIssueTitle(sentence string) string {
	trimmed := strings.Trim(strings.TrimSpace(sentence), "、。.!！ ")
	if matches := openIssueTitlePattern.FindStringSubmatch(trimmed); len(matches) == 2 {
		return truncateRunes(strings.TrimSpace(matches[1])+"が未決定", 40)
	}
	return semanticallyCompleteItemLabelOrOriginal(trimmed, "issue")
}

func explicitOpenIssueRepresented(items []liveAnalysisItem, sentence string, sequenceNo int64) bool {
	for _, item := range items {
		if item.Inactive || item.MergedIntoID != "" || item.Kind != "issue" ||
			!containsInt64(item.EvidenceSequenceNos, sequenceNo) {
			continue
		}
		itemText := item.Title + " " + item.Body
		if semanticItemSimilarity(itemText, sentence) >= 0.18 ||
			sharedTreeAuditSubjectTerm(itemText, sentence) {
			return true
		}
	}
	return false
}

func explicitRiskSentence(text string) string {
	subject := ""
	for _, sentence := range strings.Split(text, "。") {
		trimmed := strings.TrimSpace(sentence)
		if trimmed == "" {
			continue
		}
		if riskFuturePattern.MatchString(trimmed) && !riskExcludePattern.MatchString(trimmed) {
			subject = trimmed
			break
		}
	}
	return subject
}
