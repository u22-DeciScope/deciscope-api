package application

import (
	"regexp"
	"sort"
	"strings"
)

const liveAnalysisItemLabelPreferredMaxRunes = 40

type incompleteItemLabelDecision struct {
	ItemID              string
	Kind                string
	EvidenceSequenceNos []int64
	EndingType          string
	RewriteAttempted    bool
	RewriteResult       string
	FinalDecision       string
}

var (
	itemLabelDanglingConnectorPattern = regexp.MustCompile(
		`(?:ですが|けれど|けれども|ので|ため|一方で|その後|としては|については|に関しては|という点では)$`,
	)
	itemLabelIncompleteConjugationPattern = regexp.MustCompile(
		`(?:漏れてい|確認してい|対応して|実施して|修正して|検討して|調査して|作成して|更新して|適用して|なってい|できてい)$`,
	)
	itemLabelDanglingParticlePattern  = regexp.MustCompile(`(?:の|が|を|に|へ|と|で|から|より|まで|では)$`)
	itemLabelCompletePredicatePattern = regexp.MustCompile(
		`(?:です|でした|ます|ました|する|した|している|していた|していない|` +
			`できる|できない|できていない|なった|なる|なっていない|` +
			`発生|停止|遅延|遅れ|障害|影響|混在|不安定|異常|漏れ|不足|不明|未解決|未確定|` +
			`期限切れ|可能性|懸念|リスク|おそれ|必要|完了|成功|失敗|判明|確認済み|` +
			`不整合|接続不可|復旧済み|正常化)$`,
	)
	itemLabelNaturalNominalizationPattern = regexp.MustCompile(
		`(?:化|導入|実施|必須化|採用|方針|対応|作成|確認|調査|管理|更新|修正|訂正|適用|検討|` +
			`計画|確定|決定|再発防止|ダブルチェック|チェックリスト)$`,
	)
	itemLabelClauseSplitPattern      = regexp.MustCompile(`[。.!！?？、,]`)
	itemLabelLeadingConnectorPattern = regexp.MustCompile(
		`^(?:その後|また|さらに|加えて|ただし|まずは|いえ|正確には|復旧対応としては)[[:space:]、,]*`,
	)
	itemLabelClauseSubjectPattern    = regexp.MustCompile(`(?:は|が|を|に|へ|で|から|より|まで)`)
	itemLabelAnaphoricSubjectPattern = regexp.MustCompile(`^(?:(?:この|その)(?:点|件|事項|問題)?|本件|それ)(?:は|を)?`)
)

// incompleteItemLabelEnding validates the user-visible title independently
// from Body. A complete Body must not mask a title whose predicate was cut by
// the 40-rune presentation cap.
func incompleteItemLabelEnding(item liveAnalysisItem) string {
	label := strings.TrimSpace(item.Title)
	if label == "" {
		return "missing_label"
	}
	body := strings.TrimSpace(item.Body)
	if len([]rune(label)) >= liveAnalysisItemLabelPreferredMaxRunes &&
		len([]rune(body)) > len([]rune(label)) &&
		strings.HasPrefix(body, label) {
		return "max_length_truncation"
	}
	switch {
	case itemLabelDanglingConnectorPattern.MatchString(label):
		return "dangling_connector"
	case itemLabelIncompleteConjugationPattern.MatchString(label):
		return "incomplete_conjugation"
	case itemLabelDanglingParticlePattern.MatchString(label):
		return "dangling_particle"
	default:
		return ""
	}
}

func itemLabelMissingPredicate(kind, label string) bool {
	normalized := strings.Trim(strings.TrimSpace(label), "。.!！?？ ")
	if normalized == "" {
		return true
	}
	if itemLabelCompletePredicatePattern.MatchString(normalized) {
		return false
	}
	if itemLabelNaturalNominalizationPattern.MatchString(normalized) {
		return false
	}
	switch kind {
	case "todo", "decision":
		return !itemLabelNaturalNominalizationPattern.MatchString(normalized)
	case "risk":
		return !lowInformationRiskPattern.MatchString(normalized)
	case "fact", "issue":
		return itemLabelClauseSubjectPattern.MatchString(normalized) &&
			!lowInformationAssertionPattern.MatchString(normalized) &&
			!lowInformationQuestionPattern.MatchString(normalized) &&
			!itemLabelCompletePredicatePattern.MatchString(normalized)
	default:
		return false
	}
}

// semanticallyCompleteItemLabel prefers a complete clause within the historic
// 40-rune display target. When no such shortening is safe, it preserves the
// complete proposition (up to the existing 100-rune description bound) rather
// than cutting through its central predicate.
func semanticallyCompleteItemLabel(text, kind string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	text = strings.Trim(text, "。.!！?？ ")
	if text == "" {
		return ""
	}
	probe := liveAnalysisItem{Kind: kind, Title: text, Body: text}
	if len([]rune(text)) <= liveAnalysisItemLabelPreferredMaxRunes &&
		incompleteItemLabelEnding(probe) == "" {
		return text
	}
	if (kind == "todo" || kind == "decision") &&
		len([]rune(text)) <= liveAnalysisTreeDescriptionMaxRunes &&
		incompleteItemLabelEnding(probe) == "" {
		return text
	}
	parts := itemLabelClauseSplitPattern.Split(text, -1)
	// For a Todo, prefer the clause that actually names the action. Grounding
	// can legitimately rewrite a short model title to a longer transcript
	// sentence whose final clause only says that "the matter remains open".
	// Choosing that final clause would erase the independently actionable
	// proposition.
	for _, maxRunes := range []int{
		liveAnalysisItemLabelPreferredMaxRunes,
		liveAnalysisTreeDescriptionMaxRunes,
	} {
		for pass := 0; pass < 2; pass++ {
			requireTodoAction := kind == "todo" && pass == 0
			if kind != "todo" && pass > 0 {
				break
			}
			for index := len(parts) - 1; index >= 0; index-- {
				part := normalizeItemLabelClause(parts[index], kind)
				if index > 0 && itemLabelDanglingParticlePattern.MatchString(
					strings.TrimSpace(parts[index-1]),
				) {
					// ASR punctuation can split one proposition immediately
					// after its case particle. Rejoin that bounded pair; the
					// suffix alone would have an unresolved referent and the
					// prefix alone would have no predicate.
					part = normalizeItemLabelClause(
						strings.TrimSpace(parts[index-1])+"、"+part,
						kind,
					)
				}
				if part == "" || len([]rune(part)) > maxRunes {
					continue
				}
				if issueAnaphoraPattern.MatchString(part) {
					continue
				}
				if requireTodoAction &&
					!kindActionVerbPattern.MatchString(part) &&
					!lowInformationActionPattern.MatchString(part) {
					continue
				}
				candidate := liveAnalysisItem{Kind: kind, Title: part, Body: part}
				if incompleteItemLabelEnding(candidate) == "" {
					return part
				}
			}
		}
	}
	if len([]rune(text)) <= liveAnalysisTreeDescriptionMaxRunes &&
		incompleteItemLabelEnding(probe) == "" {
		return text
	}
	return ""
}

func normalizeItemLabelClause(raw, kind string) string {
	part := strings.TrimSpace(raw)
	part = itemLabelLeadingConnectorPattern.ReplaceAllString(part, "")
	part = strings.TrimSpace(part)
	if kind != "todo" {
		return part
	}

	// A causal subordinate clause such as
	// 「この点は気象データを確認してから判断するため」 contains a
	// concrete action even though the connective itself is not a complete
	// visible label. Remove only the bounded anaphoric subject and final
	// connective; the action/object remain transcript-verbatim.
	part = strings.TrimSpace(itemLabelAnaphoricSubjectPattern.ReplaceAllString(part, ""))
	for _, suffix := range []string{"ため", "ので"} {
		if !strings.HasSuffix(part, suffix) {
			continue
		}
		candidate := strings.TrimSpace(strings.TrimSuffix(part, suffix))
		if candidate != "" &&
			(kindActionVerbPattern.MatchString(candidate) ||
				lowInformationActionPattern.MatchString(candidate)) {
			part = candidate
		}
		break
	}
	return part
}

func semanticallyCompleteItemLabelOrOriginal(text, kind string) string {
	if label := semanticallyCompleteItemLabel(text, kind); label != "" {
		return label
	}
	return strings.Trim(
		strings.Join(strings.Fields(strings.TrimSpace(text)), " "),
		"。.!！?？ ",
	)
}

func repairIncompleteItemLabel(
	item liveAnalysisItem,
	scope liveEvidenceScope,
	timeline discourseTimeline,
) (liveAnalysisItem, incompleteItemLabelDecision, bool) {
	endingType := incompleteItemLabelEnding(item)
	decision := incompleteItemLabelDecision{
		ItemID:              firstNonEmptyTrimmed(item.ID, item.ClientKey, item.modelReference),
		Kind:                item.Kind,
		EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...),
		EndingType:          endingType,
		RewriteAttempted:    endingType != "",
		RewriteResult:       "not_needed",
		FinalDecision:       "accepted",
	}
	if endingType == "" {
		return item, decision, false
	}

	candidates := make([]string, 0, len(item.EvidenceSequenceNos)+1)
	if body := strings.TrimSpace(item.Body); body != "" && body != strings.TrimSpace(item.Title) {
		candidates = append(candidates, body)
	}
	for _, sequenceNo := range item.EvidenceSequenceNos {
		switch timeline.Roles[sequenceNo] {
		case liveEvidenceReferenceRecap, liveEvidenceDiscourseOnly:
			continue
		}
		if text := strings.TrimSpace(scope.TranscriptText[sequenceNo]); text != "" {
			candidates = append(candidates, text)
		}
	}
	for _, candidateText := range candidates {
		label := semanticallyCompleteItemLabel(candidateText, item.Kind)
		if label == "" {
			continue
		}
		repaired := item
		repaired.Title = label
		if strings.TrimSpace(repaired.Body) == "" ||
			incompleteItemLabelEnding(liveAnalysisItem{
				Kind: repaired.Kind, Title: repaired.Body, Body: repaired.Body,
			}) != "" {
			repaired.Body = strings.Trim(strings.TrimSpace(candidateText), "。.!！?？ ")
		}
		if incompleteItemLabelEnding(repaired) != "" {
			continue
		}
		decision.RewriteResult = "success"
		decision.FinalDecision = "rewritten"
		return repaired, decision, true
	}
	decision.RewriteResult = "failed"
	decision.FinalDecision = "rejected"
	return item, decision, false
}

func repairIncompleteDiffItemLabels(
	items []liveAnalysisItem,
	scope liveEvidenceScope,
	timeline discourseTimeline,
	stats *liveAnalysisTreeMergeStats,
) []liveAnalysisItem {
	for index := range items {
		repaired, decision, changed := repairIncompleteItemLabel(items[index], scope, timeline)
		if decision.EndingType == "" {
			continue
		}
		if changed {
			items[index] = repaired
			if stats != nil {
				stats.LowInformationItemsRewritten++
			}
		}
		if stats != nil {
			stats.IncompleteLabelDecisions = append(stats.IncompleteLabelDecisions, decision)
		}
	}
	return items
}

func repairIncompletePersistedItemLabels(
	state *liveAnalysisPayload,
	scope liveEvidenceScope,
	timeline discourseTimeline,
	version int64,
	stats *liveAnalysisTreeMergeStats,
) {
	if state == nil || state.Tree == nil {
		return
	}
	ids := activeFinalItemIDs(state.Items)
	sort.Strings(ids)
	for _, itemID := range ids {
		item, ok := finalItemByID(state.Items, itemID)
		if !ok {
			continue
		}
		repaired, decision, changed := repairIncompleteItemLabel(item, scope, timeline)
		if decision.EndingType == "" {
			continue
		}
		if changed {
			updateFinalItemAndNode(state, repaired)
			if stats != nil {
				stats.LowInformationItemsRewritten++
			}
		} else {
			rejectFinalItem(state, item.ID, "incomplete_item_label_rejected", version)
			if stats != nil {
				stats.LowInformationItemsRejected++
			}
		}
		if stats != nil {
			stats.IncompleteLabelDecisions = append(stats.IncompleteLabelDecisions, decision)
		}
	}
}
