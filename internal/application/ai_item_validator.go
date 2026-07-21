package application

import (
	"regexp"
	"strings"
)

// liveItemRejection contains only identifiers and sequence numbers so the
// structured rejection log never needs to print meeting text.
type liveItemRejection struct {
	ModelItemID         string
	CanonicalItemID     string
	Kind                string
	EvidenceSequenceNos []int64
	Reason              string
	DetectedRole        liveUtteranceRole
}

var (
	lowInformationMetaObjectPattern    = regexp.MustCompile(`(?i)(?:追加|別|次|新しい)?(?:論点|問題|話題|議題|テーマ|項目|件|アジェンダ|topic|issue|agenda|matter)`)
	lowInformationMetaPredicatePattern = regexp.MustCompile(`(?i)(?:存在|ある|あり|確認|紹介|移行|移る|進む|変える|切り替える|取り上げる|次へ|move|switch|introduce|exist)`)
	lowInformationAssigneePattern      = regexp.MustCompile(`(?:さん|氏|担当者|責任者|owner|assignee)`)
	lowInformationDeadlinePattern      = regexp.MustCompile(`(?:まで|今週|来週|本日|明日|月末|期限|\d{1,4}[年月日時])`)
	lowInformationActionPattern        = regexp.MustCompile(`(?:実施|作成|更新|修正|調査|確認(?:する|して|を行)|依頼|連絡|共有|提出|設定|適用|対応|検討|予約|準備|送付|レビュー|承認を得|お願いします|すること)`)
	lowInformationDecisionPattern      = regexp.MustCompile(`(?:承認|決定|採用|合意|確定|可決|見送|却下|とします|ことにします|進めます|適用します)`)
	lowInformationQuestionPattern      = regexp.MustCompile(`(?:[?？]|(?:何|いつ|誰|どこ|どの|どう|なぜ|方法|可否|有無|条件|原因|対象).*(?:か|確認)|(?:です|ます|する)か)`)
	lowInformationRiskPattern          = regexp.MustCompile(`(?:リスク|懸念|恐れ|可能性|影響|不能|停止|遅延|障害|期限切れ|損失|漏えい|危険)`)
	lowInformationAssertionPattern     = regexp.MustCompile(`(?:である|です|だった|になる|なった|している|した|判明|分か|確認され|存在する|漏れ|期限切れ|発生|完了|成功|失敗)`)
	lowInformationGenericOnlyPattern   = regexp.MustCompile(`^(?:追加|別|次|新しい|その他|未分類|論点|問題|話題|議題|テーマ|項目|件|アジェンダ|存在|確認|対応|検討|課題|事項|内容|あり|ある|です|する|します|を|が|の|へ|に|と|another|next|additional|topic|issue|agenda|matter)+$`)
)

func filterLowInformationLiveItems(previous, diff []liveAnalysisItem, timeline discourseTimeline, scope liveEvidenceScope, stats *liveAnalysisTreeMergeStats) []liveAnalysisItem {
	// The live gate is evidence-aware by design. Some internal replayers and
	// historical payload migrations intentionally operate without transcript
	// text; those paths are covered by the audit-time validator instead of
	// guessing from a short title alone.
	if len(scope.TranscriptText) == 0 {
		return diff
	}
	previousIDs := make(map[string]struct{}, len(previous))
	for _, item := range previous {
		previousIDs[item.ID] = struct{}{}
	}
	kept := make([]liveAnalysisItem, 0, len(diff))
	for _, item := range diff {
		_, updatesExisting := previousIDs[item.ID]
		if item.Kind == "issue" && item.InformationStatus == informationStatusTentative &&
			!isDiscourseOnlyItem(item.Title, item.Body) &&
			!evidenceOnlyHasRoles(item.EvidenceSequenceNos, timeline, liveEvidenceDiscourseOnly) {
			kept = append(kept, item)
			if stats != nil {
				stats.LowInformationTentativeRetained++
			}
			continue
		}
		reason, role := validateLiveItemInformation(item, updatesExisting, timeline, scope)
		if reason == "" {
			kept = append(kept, item)
			continue
		}
		if stats != nil {
			stats.LowInformationItemsRejected++
			if item.Kind == "decision" {
				stats.LowInformationDecisionsRejected++
			}
			if role == liveUtteranceDiscourseTransition || role == liveUtteranceFiller {
				stats.DiscourseOnlyItemsRejected++
			}
			stats.LowInformationRejections = append(stats.LowInformationRejections, liveItemRejection{
				ModelItemID: firstNonEmptyTrimmed(item.modelReference, item.ID), CanonicalItemID: item.ID,
				Kind: item.Kind, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...),
				Reason: reason, DetectedRole: role,
			})
		}
	}
	return kept
}

func validateLiveItemInformation(item liveAnalysisItem, updatesExisting bool, timeline discourseTimeline, scope liveEvidenceScope) (string, liveUtteranceRole) {
	role := dominantLiveItemRole(item.EvidenceSequenceNos, timeline)
	if item.Kind == "decision" && isMeetingEndOnlyItem(item.Title, item.Body) {
		return "meeting_end_discourse", firstNonEmptyUtteranceRole(role, liveUtteranceDiscourseTransition)
	}
	if item.Kind == "decision" && (decisionStatementNeedsReferent(item.Title) || decisionStatementNeedsReferent(item.Body)) {
		return "decision_missing_object", role
	}
	if evidenceOnlyHasRoles(item.EvidenceSequenceNos, timeline, liveEvidenceDiscourseOnly) {
		return "low_information", firstNonEmptyUtteranceRole(role, liveUtteranceDiscourseTransition)
	}
	if !updatesExisting && evidenceOnlyHasRoles(item.EvidenceSequenceNos, timeline, liveEvidenceReferenceRecap) {
		return "low_information", firstNonEmptyUtteranceRole(role, liveUtteranceRecap)
	}
	// Historical payload fixtures and pre-evidence bootstrap rounds may not have
	// transcript text available. Only enforce an explicitly empty evidence list
	// when there is an actual transcript scope against which the model could have
	// grounded a newly-created item.
	if len(item.EvidenceSequenceNos) == 0 && item.evidenceSpecified && !updatesExisting && len(scope.TranscriptText) > 0 {
		return "low_information", role
	}
	text := strings.TrimSpace(item.Title + " " + item.Body)
	if isDiscourseOnlyItem(item.Title, item.Body) || structurallyDiscourseTransition(normalizeDiscourseText(text)) {
		return "low_information", firstNonEmptyUtteranceRole(role, liveUtteranceDiscourseTransition)
	}
	if metaOnlyLiveItemText(text) && !liveItemHasConcreteContext(item, scope) {
		return "low_information", role
	}

	contextual := liveItemHasConcreteContext(item, scope)
	specific := liveItemHasSpecificSubject(text)
	switch item.Kind {
	case "todo":
		if !specific && !lowInformationActionPattern.MatchString(text) && !lowInformationAssigneePattern.MatchString(text) && !lowInformationDeadlinePattern.MatchString(text) && !contextual {
			return "low_information", role
		}
	case "decision":
		if !specific && !lowInformationDecisionPattern.MatchString(text) && !completeDecisionStatement(strings.TrimSpace(item.Title)) && !contextual {
			return "low_information", role
		}
	case "fact":
		if !specific && !lowInformationAssertionPattern.MatchString(text) && !contextual {
			return "low_information", role
		}
	case "risk":
		if !specific && !lowInformationRiskPattern.MatchString(text) && !contextual {
			return "low_information", role
		}
	case "issue":
		if item.Subtype == issueSubtypeQuestion && !lowInformationQuestionPattern.MatchString(text) && !specific && !contextual {
			return "low_information", role
		}
		if !specific && !contextual {
			return "low_information", role
		}
	}
	return "", role
}

func evidenceOnlyHasRoles(sequenceNos []int64, timeline discourseTimeline, allowed ...liveEvidenceRole) bool {
	if len(sequenceNos) == 0 {
		return false
	}
	set := make(map[liveEvidenceRole]struct{}, len(allowed))
	for _, role := range allowed {
		set[role] = struct{}{}
	}
	for _, sequenceNo := range sequenceNos {
		if _, ok := set[timeline.Roles[sequenceNo]]; !ok {
			return false
		}
	}
	return true
}

func dominantLiveItemRole(sequenceNos []int64, timeline discourseTimeline) liveUtteranceRole {
	for _, sequenceNo := range sequenceNos {
		if role := timeline.DetectedRoles[sequenceNo]; role != "" {
			return role
		}
	}
	return ""
}

func firstNonEmptyUtteranceRole(values ...liveUtteranceRole) liveUtteranceRole {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func metaOnlyLiveItemText(text string) bool {
	normalized := normalizeDiscourseText(text)
	if normalized == "" {
		return true
	}
	if lowInformationGenericOnlyPattern.MatchString(strings.ToLower(normalized)) {
		return true
	}
	return lowInformationMetaObjectPattern.MatchString(normalized) &&
		lowInformationMetaPredicatePattern.MatchString(normalized) &&
		!discourseConcretePattern.MatchString(normalized)
}

func liveItemHasSpecificSubject(text string) bool {
	key := semanticItemKey(text)
	if key == "" || lowInformationGenericOnlyPattern.MatchString(strings.ToLower(key)) {
		return false
	}
	return len([]rune(key)) >= 2
}

func liveItemHasConcreteContext(item liveAnalysisItem, scope liveEvidenceScope) bool {
	text := item.Title + " " + item.Body
	if lowInformationAssigneePattern.MatchString(text) || lowInformationDeadlinePattern.MatchString(text) ||
		discourseCorrectionPattern.MatchString(text) || discourseConcretePattern.MatchString(text) {
		return true
	}
	for _, sequenceNo := range item.EvidenceSequenceNos {
		for _, nearby := range []int64{sequenceNo - 1, sequenceNo, sequenceNo + 1} {
			evidence := strings.TrimSpace(scope.TranscriptText[nearby])
			if evidence == "" || classifyDiscourseAct(evidence) != discourseContent {
				continue
			}
			if discourseConcretePattern.MatchString(evidence) || lowInformationActionPattern.MatchString(evidence) ||
				lowInformationDecisionPattern.MatchString(evidence) || lowInformationRiskPattern.MatchString(evidence) ||
				lowInformationAssertionPattern.MatchString(evidence) {
				return true
			}
		}
	}
	return false
}
