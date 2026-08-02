package application

import (
	"regexp"
	"sort"
	"strings"

	"deciscope-core-api/internal/domain"
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
	itemLabelContextDependentPattern = regexp.MustCompile(
		`^(?:現時点では[、,]?)?(?:(?:この|その)(?:点|問題|件|条件|事項|設定漏れ)|本件|それ)(?:は|が|を|の|に|で|では)?|^完全な[^。]{1,80}(?:ではありません|ではない)$`,
	)
	itemLabelConditionalWithoutSubjectPattern = regexp.MustCompile(
		`^(?:放置すると|このまま(?:では|だと)?|そのまま(?:では|だと)?|対応しないと|更新しないと|その場合(?:は|に)?|それにより)`,
	)
	itemLabelDirectConditionalReferencePattern = regexp.MustCompile(
		`(?:放置すると|このまま(?:では|だと)?|そのまま(?:では|だと)?|対応しないと|更新しないと|その場合(?:は|に)?|それにより)`,
	)
	itemLabelAntecedentSubjectPattern = regexp.MustCompile(`^([^。！？]{1,40}?)(?:は|が|も)`)
	itemLabelParallelSubjectPattern   = regexp.MustCompile(`(?:と|や|または|又は|および|及び|ならびに|並びに|・|複数|両方|双方)`)
	itemLabelConcreteQualifierPattern = regexp.MustCompile(
		`(?i)(?:VLAN[[:space:]]*[0-9０-９]+|[0-9０-９]+階|[月火水木金土日]曜日|来週|来月|今週|本日|明日|[0-9０-９]{1,2}月[0-9０-９]{1,2}日|[一-龠々ぁ-んァ-ヶーA-Za-z]{1,24}さん)`,
	)
	itemLabelDeicticSettingPattern = regexp.MustCompile(`(?:この|その)設定漏れ`)
	itemLabelSettingLeakPattern    = regexp.MustCompile(`(?:設定|VLAN|許可)[^。]{0,60}漏れ`)
	itemLabelVLANQualifierPattern  = regexp.MustCompile(`(?i)VLAN[[:space:]]*[0-9０-９]+`)
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
	if unclosedItemLabelDelimiter(label) {
		return "unclosed_delimiter"
	}
	if len([]rune(label)) >= liveAnalysisItemLabelPreferredMaxRunes &&
		len([]rune(body)) > len([]rune(label)) &&
		strings.HasPrefix(body, label) {
		return "max_length_truncation"
	}
	switch {
	case itemLabelConditionalWithoutSubjectPattern.MatchString(label):
		return "context_dependent"
	case itemLabelContextDependentPattern.MatchString(label):
		return "context_dependent"
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

func unclosedItemLabelDelimiter(label string) bool {
	for _, pair := range [][2]string{{"（", "）"}, {"(", ")"}, {"「", "」"}, {"[", "]"}, {"［", "］"}} {
		if strings.Count(label, pair[0]) > strings.Count(label, pair[1]) {
			return true
		}
	}
	return false
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
	item, unusedAntecedentPruned := localizeConditionalItemEvidence(item, scope, timeline)
	item, referentEvidenceRestored := restoreLabelReferentEvidence(item, scope, timeline)
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
		return item, decision, referentEvidenceRestored || unusedAntecedentPruned
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
	type labelCandidate struct {
		text  string
		label string
		score int
	}
	var best labelCandidate
	for _, candidateText := range candidates {
		label := semanticallyCompleteItemLabel(candidateText, item.Kind)
		if label == "" || !itemLabelCandidatePreservesSemantics(item, label, scope) {
			continue
		}
		candidate := labelCandidate{
			text: candidateText, label: label,
			score: itemLabelCandidateScore(item, label, scope),
		}
		if candidate.score > best.score {
			best = candidate
		}
	}
	if best.label != "" {
		repaired := applyRepairedItemLabel(item, best.label, best.text)
		repaired = withLabelResolution(repaired, "rewritten", endingType, repaired.EvidenceSequenceNos)
		decision.RewriteResult = "success"
		decision.FinalDecision = "rewritten"
		return repaired, decision, true
	}
	grounded := item.GroundingDecision == "accepted" || item.GroundingDecision == "rewritten"
	if grounded {
		if label, source := deterministicFallbackItemLabel(item, scope, timeline); label != "" {
			repaired := applyRepairedItemLabel(item, label, source)
			repaired = withLabelResolution(repaired, "fallback_applied", endingType, repaired.EvidenceSequenceNos)
			decision.RewriteResult = "deterministic_fallback"
			decision.FinalDecision = "fallback_applied"
			return repaired, decision, true
		}
	} else if strings.TrimSpace(item.GroundingDecision) == "" && len(item.EvidenceSequenceNos) > 0 {
		decision.RewriteResult = "deferred"
		decision.FinalDecision = "deferred_until_grounded"
		return item, decision, referentEvidenceRestored
	}
	decision.RewriteResult = "failed"
	if labelFailureRetentionEligible(item, scope, timeline) {
		decision.FinalDecision = "retained_degraded"
		item = withLabelResolution(item, "retained_degraded", endingType+"_repair_failed", item.EvidenceSequenceNos)
	} else {
		decision.FinalDecision = "rejected"
	}
	return item, decision, decision.FinalDecision == "retained_degraded" || unusedAntecedentPruned
}

func withLabelResolution(item liveAnalysisItem, status, reason string, sourceEvidence []int64) liveAnalysisItem {
	item.LabelResolution = &labelResolutionMetadata{
		Status: strings.TrimSpace(status), Reason: strings.TrimSpace(reason),
		SourceEvidenceSequenceNos: uniqueSortedSequenceNos(sortedSequenceNos(append([]int64(nil), sourceEvidence...))),
	}
	return item
}

func cloneLabelResolution(value *labelResolutionMetadata) *labelResolutionMetadata {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.SourceEvidenceSequenceNos = append([]int64(nil), value.SourceEvidenceSequenceNos...)
	return &cloned
}

func localizeConditionalItemEvidence(
	item liveAnalysisItem,
	scope liveEvidenceScope,
	timeline discourseTimeline,
) (liveAnalysisItem, bool) {
	conditionalSequence := int64(0)
	for _, sequenceNo := range item.EvidenceSequenceNos {
		if itemLabelDirectConditionalReferencePattern.MatchString(strings.TrimSpace(scope.TranscriptText[sequenceNo])) &&
			sequenceNo > conditionalSequence {
			conditionalSequence = sequenceNo
		}
	}
	if conditionalSequence == 0 {
		return item, false
	}
	kept := []int64{conditionalSequence}
	if candidate, ok := safeAdjacentConditionalLabel(item, conditionalSequence, scope, timeline); ok &&
		itemLabelCandidatePreservesSemantics(item, candidate, scope) {
		kept = append(kept, conditionalSequence-1)
	}
	kept = uniqueSortedSequenceNos(sortedSequenceNos(kept))
	if sameItemLabelEvidence(item.EvidenceSequenceNos, kept) {
		return item, false
	}
	item.EvidenceSequenceNos = kept
	return item, true
}

func sameItemLabelEvidence(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func restoreLabelReferentEvidence(
	item liveAnalysisItem,
	scope liveEvidenceScope,
	timeline discourseTimeline,
) (liveAnalysisItem, bool) {
	restored := append([]int64(nil), item.EvidenceSequenceNos...)
	changed := false
	for _, sequenceNo := range item.EvidenceSequenceNos {
		current := strings.TrimSpace(scope.TranscriptText[sequenceNo])
		previousSequence := sequenceNo - 1
		previous := strings.TrimSpace(scope.TranscriptText[previousSequence])
		if current == "" || previous == "" || containsInt64(restored, previousSequence) {
			continue
		}
		switch timeline.Roles[previousSequence] {
		case liveEvidenceReferenceRecap, liveEvidenceDiscourseOnly:
			continue
		}
		restore := false
		if itemLabelConditionalWithoutSubjectPattern.MatchString(current) {
			if candidate, ok := safeAdjacentConditionalLabel(item, sequenceNo, scope, timeline); ok &&
				itemLabelCandidatePreservesSemantics(item, candidate, scope) {
				restore = true
			}
		}
		if itemLabelDeicticSettingPattern.MatchString(current) {
			_, restore = safeAdjacentSettingReferent(item, sequenceNo, scope, timeline)
		}
		if restore {
			restored = append(restored, previousSequence)
			changed = true
		}
	}
	if changed {
		item.EvidenceSequenceNos = uniqueSortedSequenceNos(sortedSequenceNos(restored))
	}
	return item, changed
}

func safeAdjacentSettingReferent(
	item liveAnalysisItem,
	currentSequence int64,
	scope liveEvidenceScope,
	timeline discourseTimeline,
) (string, bool) {
	current := strings.TrimSpace(scope.TranscriptText[currentSequence])
	previousSequence := currentSequence - 1
	previous := strings.TrimSpace(scope.TranscriptText[previousSequence])
	if !itemLabelDeicticSettingPattern.MatchString(current) ||
		!itemLabelSettingLeakPattern.MatchString(previous) {
		return "", false
	}
	previousSegment, previousOK := scope.Segments[previousSequence]
	currentSegment, currentOK := scope.Segments[currentSequence]
	if !previousOK || !currentOK || !previousSegment.IsFinal || !currentSegment.IsFinal ||
		previousSegment.SequenceNo <= 0 || currentSegment.SequenceNo != previousSegment.SequenceNo+1 {
		return "", false
	}
	for _, sequenceNo := range []int64{previousSequence, currentSequence} {
		switch timeline.Roles[sequenceNo] {
		case liveEvidenceReferenceRecap, liveEvidenceDiscourseOnly:
			return "", false
		}
	}
	qualifiers := uniqueSortedStrings(itemLabelVLANQualifierPattern.FindAllString(previous, -1))
	if len(qualifiers) != 1 || itemLabelParallelSubjectPattern.MatchString(previous) {
		return "", false
	}
	qualifier := qualifiers[0]
	itemText := strings.TrimSpace(item.Title + " " + item.Body)
	for _, currentQualifier := range itemLabelVLANQualifierPattern.FindAllString(itemText, -1) {
		if !strings.EqualFold(strings.TrimSpace(currentQualifier), strings.TrimSpace(qualifier)) {
			return "", false
		}
	}
	if earlierSegment, exists := scope.Segments[previousSequence-1]; exists && earlierSegment.IsFinal &&
		earlierSegment.SequenceNo+1 == previousSegment.SequenceNo {
		earlier := strings.TrimSpace(scope.TranscriptText[previousSequence-1])
		earlierQualifiers := uniqueSortedStrings(itemLabelVLANQualifierPattern.FindAllString(earlier, -1))
		if itemLabelSettingLeakPattern.MatchString(earlier) && len(earlierQualifiers) > 0 &&
			!containsFoldedString(earlierQualifiers, qualifier) {
			return "", false
		}
	}
	return qualifier, true
}

func safeAdjacentConditionalLabel(
	item liveAnalysisItem,
	currentSequence int64,
	scope liveEvidenceScope,
	timeline discourseTimeline,
) (string, bool) {
	current := strings.Trim(strings.TrimSpace(scope.TranscriptText[currentSequence]), "。.!！?？ ")
	if !itemLabelConditionalWithoutSubjectPattern.MatchString(current) {
		return "", false
	}
	previousSequence := currentSequence - 1
	previous := strings.Trim(strings.TrimSpace(scope.TranscriptText[previousSequence]), "。.!！?？ ")
	if previous == "" || (!kindScheduledEventPattern.MatchString(previous) &&
		!kindFutureEventPattern.MatchString(previous)) {
		return "", false
	}
	previousSegment, previousOK := scope.Segments[previousSequence]
	currentSegment, currentOK := scope.Segments[currentSequence]
	if !previousOK || !currentOK || !explicitAdjacentSameSpeaker(previousSegment, currentSegment) {
		return "", false
	}
	for _, sequenceNo := range []int64{previousSequence, currentSequence} {
		switch timeline.Roles[sequenceNo] {
		case liveEvidenceReferenceRecap, liveEvidenceDiscourseOnly:
			return "", false
		}
		if role := timeline.DetectedRoles[sequenceNo]; role == liveUtteranceDiscourseTransition || role == liveUtteranceRecap {
			return "", false
		}
	}
	subject, ok := uniqueAntecedentSubject(previous)
	if !ok || antecedentConflictsWithCurrent(item, subject, previous, current) {
		return "", false
	}
	if earlier, exists := scope.Segments[previousSequence-1]; exists &&
		explicitAdjacentSameSpeaker(earlier, previousSegment) {
		if competing, unique := uniqueAntecedentSubject(scope.TranscriptText[previousSequence-1]); unique &&
			!sharedTreeAuditSubjectTerm(competing, subject) {
			return "", false
		}
	}
	candidate := conditionalItemLabelWithSubject(subject, current)
	if candidate == "" || len([]rune(candidate)) > liveAnalysisTreeDescriptionMaxRunes {
		return "", false
	}
	currentFeatures := inferItemSemanticFeatures(
		liveAnalysisItem{Kind: item.Kind, Subtype: item.Subtype, Title: current, Body: current},
		liveEvidenceScope{},
	)
	candidateFeatures := inferItemSemanticFeatures(
		liveAnalysisItem{Kind: item.Kind, Subtype: item.Subtype, Title: candidate, Body: candidate},
		liveEvidenceScope{},
	)
	if currentFeatures.EpistemicStatus != candidateFeatures.EpistemicStatus {
		return "", false
	}
	return candidate, true
}

func explicitAdjacentSameSpeaker(previous, current domain.TranscriptSegment) bool {
	if !previous.IsFinal || !current.IsFinal || previous.SequenceNo <= 0 || current.SequenceNo != previous.SequenceNo+1 {
		return false
	}
	sameIdentity := false
	if previous.SpeakerID != "" && current.SpeakerID != "" {
		sameIdentity = previous.SpeakerID == current.SpeakerID
	} else if previous.SpeakerName != "" && current.SpeakerName != "" {
		sameIdentity = previous.SpeakerName == current.SpeakerName
	}
	return sameIdentity && adjacentSameSpeakerSegments(previous, current)
}

func uniqueAntecedentSubject(text string) (string, bool) {
	text = strings.Trim(strings.TrimSpace(text), "。.!！?？ ")
	match := itemLabelAntecedentSubjectPattern.FindStringSubmatch(text)
	if len(match) != 2 {
		return "", false
	}
	subject := strings.TrimSpace(match[1])
	if subject == "" || itemLabelContextDependentPattern.MatchString(subject) ||
		itemLabelParallelSubjectPattern.MatchString(subject) ||
		len([]rune(semanticItemKey(subject))) < 2 {
		return "", false
	}
	return subject, true
}

func antecedentConflictsWithCurrent(item liveAnalysisItem, subject, antecedent, current string) bool {
	currentText := strings.TrimSpace(item.Title + " " + item.Body + " " + current)
	if liveItemHasSpecificSubject(currentText) &&
		!itemLabelConditionalWithoutSubjectPattern.MatchString(strings.TrimSpace(item.Title)) &&
		!sharedTreeAuditSubjectTerm(currentText, subject) {
		return true
	}
	antecedentQualifiers := itemLabelConcreteQualifierPattern.FindAllString(antecedent, -1)
	currentQualifiers := itemLabelConcreteQualifierPattern.FindAllString(currentText, -1)
	for _, left := range antecedentQualifiers {
		for _, right := range currentQualifiers {
			if itemLabelQualifierFamily(left) == itemLabelQualifierFamily(right) &&
				!strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right)) {
				return true
			}
		}
	}
	return item.CandidateInactive || len(item.RelatedAgendaIDs) > 1
}

func itemLabelQualifierFamily(value string) string {
	switch {
	case strings.HasSuffix(value, "階"):
		return "floor"
	case strings.HasSuffix(value, "曜日"):
		return "weekday"
	case strings.Contains(strings.ToUpper(value), "VLAN"):
		return "vlan"
	case strings.HasSuffix(value, "さん"):
		return "person"
	case strings.Contains(value, "月") || value == "来週" || value == "来月" || value == "今週" || value == "本日" || value == "明日":
		return "time"
	default:
		return value
	}
}

func applyRepairedItemLabel(item liveAnalysisItem, label, source string) liveAnalysisItem {
	repaired := item
	repaired.Title = strings.TrimSpace(label)
	if strings.TrimSpace(repaired.Body) == "" ||
		incompleteItemLabelEnding(liveAnalysisItem{
			Kind: repaired.Kind, Title: repaired.Body, Body: repaired.Body,
		}) != "" {
		repaired.Body = strings.Trim(strings.TrimSpace(source), "。.!！?？ ")
	}
	return repaired
}

func itemLabelCandidatePreservesSemantics(item liveAnalysisItem, label string, scope liveEvidenceScope) bool {
	return itemLabelCandidatePreservesSemanticsWithQualifierPolicy(item, label, scope, true)
}

func itemLabelCandidatePreservesSemanticsWithQualifierPolicy(
	item liveAnalysisItem,
	label string,
	scope liveEvidenceScope,
	requireConcreteQualifiers bool,
) bool {
	probe := liveAnalysisItem{Kind: item.Kind, Subtype: item.Subtype, Title: label, Body: label}
	if incompleteItemLabelEnding(probe) != "" || liveItemTextNeedsReferent(probe) ||
		itemLabelConditionalWithoutSubjectPattern.MatchString(label) ||
		isDiscourseOnlyItem(label, "") {
		return false
	}
	semanticSource := itemLabelSemanticSourceText(item, scope)
	desired := inferItemSemanticFeatures(
		liveAnalysisItem{Kind: item.Kind, Subtype: item.Subtype, Title: semanticSource, Body: semanticSource},
		liveEvidenceScope{},
	)
	actual := inferItemSemanticFeatures(probe, liveEvidenceScope{})
	originalText := strings.TrimSpace(item.Title + " " + item.Body)
	if incompleteItemLabelEnding(item) != "context_dependent" &&
		liveItemHasSpecificSubject(originalText) &&
		!sharedTreeAuditSubjectTerm(originalText, label) &&
		semanticItemSimilarity(originalText, label) < 0.10 {
		return false
	}
	if requireConcreteQualifiers {
		for _, required := range itemLabelConcreteQualifierPattern.FindAllString(originalText, -1) {
			if !strings.Contains(strings.ToLower(label), strings.ToLower(required)) {
				return false
			}
		}
	}
	switch item.Kind {
	case "risk":
		compressedFutureRisk := !requireConcreteQualifiers && desired.FutureEventPresent &&
			strings.Contains(label, "リスク")
		if !actual.NegativeImpactPresent || !actual.UncertaintyPresent ||
			(!actual.FutureEventPresent && actual.TemporalScope != "ongoing" && !compressedFutureRisk) {
			return false
		}
	case "issue":
		if desired.CausalHypothesisPresent && !actual.CausalHypothesisPresent {
			return false
		}
		if desired.SemanticRole == "open_question" &&
			actual.SemanticRole != "open_question" && !kindOpenQuestionPattern.MatchString(label) {
			return false
		}
	case "todo":
		if !actual.ActionVerbPresent || actual.CompletedActionPresent {
			return false
		}
	case "decision":
		if !actual.DecisionOrCommitment && !lowInformationDecisionPattern.MatchString(label) {
			return false
		}
	case "fact":
		if actual.UncertaintyPresent && !desired.UncertaintyPresent {
			return false
		}
	}
	return !requireConcreteQualifiers || !itemLabelQualifierConflict(semanticSource, label)
}

func itemLabelCandidateScore(item liveAnalysisItem, label string, scope liveEvidenceScope) int {
	score := len([]rune(semanticItemKey(label)))
	features := inferItemSemanticFeatures(
		liveAnalysisItem{Kind: item.Kind, Subtype: item.Subtype, Title: label, Body: label},
		liveEvidenceScope{},
	)
	if features.ConfirmedEvidencePresent {
		score += 30
	}
	if features.CausalHypothesisPresent || features.DecisionOrCommitment {
		score += 40
	}
	for _, qualifier := range itemLabelConcreteQualifierPattern.FindAllString(itemLabelSemanticSourceText(item, scope), -1) {
		if strings.Contains(strings.ToLower(label), strings.ToLower(qualifier)) {
			score += 25
		}
	}
	return score
}

func itemLabelSemanticSourceText(item liveAnalysisItem, scope liveEvidenceScope) string {
	parts := make([]string, 0, len(item.EvidenceSequenceNos)+2)
	for _, text := range []string{item.Title, item.Body} {
		text = strings.TrimSpace(text)
		if text != "" && !containsExactString(parts, text) {
			parts = append(parts, text)
		}
	}
	for _, sequenceNo := range item.EvidenceSequenceNos {
		text := strings.TrimSpace(scope.TranscriptText[sequenceNo])
		if text != "" && !containsExactString(parts, text) {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "。")
}

func itemLabelQualifierConflict(source, candidate string) bool {
	sourceQualifiers := itemLabelConcreteQualifierPattern.FindAllString(source, -1)
	candidateQualifiers := itemLabelConcreteQualifierPattern.FindAllString(candidate, -1)
	if len(candidateQualifiers) == 0 {
		return false
	}
	for _, candidateQualifier := range candidateQualifiers {
		matched := false
		for _, sourceQualifier := range sourceQualifiers {
			if strings.EqualFold(candidateQualifier, sourceQualifier) {
				matched = true
				break
			}
		}
		if !matched {
			return true
		}
	}
	return false
}

func deterministicFallbackItemLabel(
	item liveAnalysisItem,
	scope liveEvidenceScope,
	timeline discourseTimeline,
) (string, string) {
	texts := make([]string, 0, len(item.EvidenceSequenceNos))
	for _, sequenceNo := range item.EvidenceSequenceNos {
		switch timeline.Roles[sequenceNo] {
		case liveEvidenceReferenceRecap, liveEvidenceDiscourseOnly:
			continue
		}
		text := strings.Trim(strings.TrimSpace(scope.TranscriptText[sequenceNo]), "。.!！?？ ")
		if text != "" && !containsExactString(texts, text) {
			texts = append(texts, text)
		}
	}
	if len(texts) == 0 {
		return "", ""
	}

	// A conditional risk clause often relies on the immediately preceding
	// sentence for its subject. Join only cited, adjacent evidence and retain
	// both clauses verbatim; this restores the referent without inventing one.
	if item.Kind == "risk" {
		clauses := make([]string, 0, len(texts)*2)
		for _, text := range texts {
			for _, clause := range semanticKindClauses(text) {
				clause = strings.Trim(strings.TrimSpace(clause), "。.!！?？ ")
				if clause != "" && !containsExactString(clauses, clause) {
					clauses = append(clauses, clause)
				}
			}
		}
		for index := len(clauses) - 1; index >= 0; index-- {
			candidate := clauses[index]
			if itemLabelConditionalWithoutSubjectPattern.MatchString(candidate) && index > 0 {
				candidate = conditionalItemLabelWithAntecedent(clauses[index-1], candidate)
			}
			if len([]rune(candidate)) <= liveAnalysisTreeDescriptionMaxRunes &&
				itemLabelCandidatePreservesSemantics(item, candidate, scope) {
				return candidate, candidate
			}
		}
		for index := len(texts) - 1; index >= 0; index-- {
			candidate := texts[index]
			if itemLabelConditionalWithoutSubjectPattern.MatchString(candidate) {
				sequenceNo := itemLabelEvidenceSequenceForText(item, scope, candidate)
				if supplemented, ok := safeAdjacentConditionalLabel(item, sequenceNo, scope, timeline); ok {
					candidate = supplemented
				} else {
					candidate = deterministicCurrentConditionalRiskLabel(candidate)
				}
			}
			if len([]rune(candidate)) <= liveAnalysisTreeDescriptionMaxRunes &&
				itemLabelCandidatePreservesSemantics(item, candidate, scope) {
				return candidate, candidate
			}
		}
	}
	if item.Kind == "issue" {
		for index := 1; index < len(texts); index++ {
			if !itemLabelDeicticSettingPattern.MatchString(texts[index]) ||
				!itemLabelSettingLeakPattern.MatchString(texts[index-1]) {
				continue
			}
			sequenceNo := itemLabelEvidenceSequenceForText(item, scope, texts[index])
			qualifier, safe := safeAdjacentSettingReferent(item, sequenceNo, scope, timeline)
			if !safe {
				continue
			}
			candidate := itemLabelDeicticSettingPattern.ReplaceAllString(
				texts[index], qualifier+"設定漏れ",
			)
			if len([]rune(candidate)) <= liveAnalysisTreeDescriptionMaxRunes &&
				itemLabelCandidatePreservesSemantics(item, candidate, scope) {
				return candidate, candidate
			}
		}
	}

	bestLabel, bestSource, bestScore := "", "", 0
	for _, text := range texts {
		label := semanticallyCompleteItemLabel(text, item.Kind)
		if label == "" || !itemLabelCandidatePreservesSemantics(item, label, scope) {
			continue
		}
		score := itemLabelCandidateScore(item, label, scope)
		if score > bestScore {
			bestLabel, bestSource, bestScore = label, text, score
		}
	}
	return bestLabel, bestSource
}

func itemLabelEvidenceSequenceForText(item liveAnalysisItem, scope liveEvidenceScope, text string) int64 {
	text = strings.Trim(strings.TrimSpace(text), "。.!！?？ ")
	for _, sequenceNo := range item.EvidenceSequenceNos {
		if strings.Trim(strings.TrimSpace(scope.TranscriptText[sequenceNo]), "。.!！?？ ") == text {
			return sequenceNo
		}
	}
	return 0
}

// conditionalItemLabelWithAntecedent carries only the referent from the
// preceding grounded clause into a subjectless conditional. Copying the whole
// preceding fact would turn one risk label into a fact+risk composite and make
// the downstream semantic splitter materialize a duplicate fact item.
func conditionalItemLabelWithAntecedent(antecedent, conditional string) string {
	antecedent = strings.Trim(strings.TrimSpace(antecedent), "。.!！?？ ")
	conditional = strings.Trim(strings.TrimSpace(conditional), "。.!！?？ ")
	subject, ok := uniqueAntecedentSubject(antecedent)
	if !ok {
		return ""
	}
	return conditionalItemLabelWithSubject(subject, conditional)
}

func conditionalItemLabelWithSubject(subject, conditional string) string {
	subject = strings.TrimSpace(subject)
	conditional = strings.Trim(strings.TrimSpace(conditional), "。.!！?？ ")
	switch {
	case strings.HasPrefix(conditional, "放置すると"), strings.HasPrefix(conditional, "更新しないと"):
		return subject + "を" + conditional
	case strings.HasPrefix(conditional, "対応しないと"):
		return subject + "に" + conditional
	case strings.HasPrefix(conditional, "このまま"), strings.HasPrefix(conditional, "そのまま"):
		return subject + "が" + conditional
	case strings.HasPrefix(conditional, "それにより"):
		return subject + "により" + strings.TrimPrefix(conditional, "それにより")
	default:
		return ""
	}
}

func deterministicCurrentConditionalRiskLabel(text string) string {
	text = strings.Trim(strings.TrimSpace(text), "。.!！?？ ")
	switch {
	case strings.HasPrefix(text, "放置すると"):
		return "放置状態の場合に" + strings.TrimPrefix(text, "放置すると")
	case strings.HasPrefix(text, "対応しないと"):
		return "未対応状態の場合に" + strings.TrimPrefix(text, "対応しないと")
	case strings.HasPrefix(text, "更新しないと"):
		return "未更新状態の場合に" + strings.TrimPrefix(text, "更新しないと")
	case strings.HasPrefix(text, "このままだと"):
		return "現状継続の場合に" + strings.TrimPrefix(text, "このままだと")
	case strings.HasPrefix(text, "このままでは"):
		return "現状継続の場合に" + strings.TrimPrefix(text, "このままでは")
	case strings.HasPrefix(text, "そのままだと"):
		return "現状継続の場合に" + strings.TrimPrefix(text, "そのままだと")
	case strings.HasPrefix(text, "そのままでは"):
		return "現状継続の場合に" + strings.TrimPrefix(text, "そのままでは")
	default:
		return ""
	}
}

func labelFailureRetentionEligible(item liveAnalysisItem, scope liveEvidenceScope, timeline discourseTimeline) bool {
	if item.GroundingDecision != "accepted" && item.GroundingDecision != "rewritten" {
		return false
	}
	if len(item.GroundingUnsupportedAtomHashes) > 0 || len(item.EvidenceSequenceNos) == 0 {
		return false
	}
	for _, sequenceNo := range item.EvidenceSequenceNos {
		if strings.TrimSpace(scope.TranscriptText[sequenceNo]) == "" {
			return false
		}
		switch timeline.Roles[sequenceNo] {
		case liveEvidenceReferenceRecap, liveEvidenceDiscourseOnly:
			return false
		}
	}
	originalText := strings.TrimSpace(item.Title + " " + item.Body)
	evidenceText := itemLabelEvidenceText(item, scope)
	for _, required := range itemLabelConcreteQualifierPattern.FindAllString(originalText, -1) {
		if !strings.Contains(strings.ToLower(evidenceText), strings.ToLower(required)) {
			return false
		}
	}
	if incompleteItemLabelEnding(item) != "context_dependent" &&
		liveItemHasSpecificSubject(originalText) &&
		!sharedTreeAuditSubjectTerm(originalText, evidenceText) {
		return false
	}
	semanticSource := itemLabelSemanticSourceText(item, scope)
	features := inferItemSemanticFeatures(
		liveAnalysisItem{Kind: item.Kind, Subtype: item.Subtype, Title: semanticSource, Body: semanticSource},
		liveEvidenceScope{},
	)
	switch item.Kind {
	case "risk":
		return features.NegativeImpactPresent && features.UncertaintyPresent &&
			(features.FutureEventPresent || features.TemporalScope == "ongoing")
	case "fact":
		return features.ConfirmedEvidencePresent || features.TemporalScope == "past"
	case "issue":
		return features.CausalHypothesisPresent || features.SemanticRole == "open_question" ||
			kindOpenQuestionPattern.MatchString(semanticSource)
	case "todo":
		return features.ActionVerbPresent && !features.CompletedActionPresent
	case "decision":
		return features.DecisionOrCommitment
	default:
		return false
	}
}

func itemLabelEvidenceText(item liveAnalysisItem, scope liveEvidenceScope) string {
	parts := make([]string, 0, len(item.EvidenceSequenceNos))
	for _, sequenceNo := range item.EvidenceSequenceNos {
		if text := strings.TrimSpace(scope.TranscriptText[sequenceNo]); text != "" &&
			!containsExactString(parts, text) {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "。")
}

func repairIncompleteDiffItemLabels(
	items []liveAnalysisItem,
	scope liveEvidenceScope,
	timeline discourseTimeline,
	stats *liveAnalysisTreeMergeStats,
) []liveAnalysisItem {
	for index := range items {
		repaired, decision, changed := repairIncompleteItemLabel(items[index], scope, timeline)
		if changed {
			items[index] = repaired
		}
		if decision.EndingType == "" {
			continue
		}
		if changed {
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
		} else if decision.FinalDecision != "retained_degraded" {
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
