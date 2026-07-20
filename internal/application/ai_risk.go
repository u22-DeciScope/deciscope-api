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
	riskSupportPattern = regexp.MustCompile(`(?:すると|しないと|放置すると|のままだと|なければ|場合|できなくな|停止|障害|過多|多くなりすぎ|期限切れ|失われ|漏れ|遅延|接続できな)`)
	// riskTitleTrailingPattern strips the sentence-final risk boilerplate
	// ("という可能性があります" etc.) that explicitRiskItemTitle would
	// otherwise carry verbatim into the card title.
	riskTitleTrailingPattern = regexp.MustCompile(`(?:という)?(?:可能性があ(?:る|ります)|お(?:それ|それ)があ(?:る|ります)|恐れがあ(?:る|ります)|リスクがあ(?:る|ります)|なりかねな(?:い|く)(?:なります)?|なりかねません)$`)
)

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
		if text == "" || !riskFuturePattern.MatchString(text) || riskExcludePattern.MatchString(text) || !riskSupportPattern.MatchString(text) {
			continue
		}
		if evidenceRoleIsReference(sequenceNo, timeline) {
			continue
		}
		if riskItemDuplicatesExisting(text, sequenceNo, existingRisks) {
			continue
		}
		title := explicitRiskItemTitle(text)
		if title == "" {
			continue
		}
		item := liveAnalysisItem{
			Kind: "risk", Severity: "medium", Title: title, Body: text, Status: "open",
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
	subject := text
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
	subject = riskTitleTrailingPattern.ReplaceAllString(subject, "")
	subject = strings.Trim(strings.TrimSpace(subject), "、。 ")
	if subject == "" {
		return ""
	}
	return truncateRunes(subject, 40)
}
