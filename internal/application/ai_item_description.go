package application

import (
	"regexp"
	"sort"
	"strings"

	"deciscope-core-api/internal/domain"
)

var (
	descriptionDecisionSuffixPattern = regexp.MustCompile(
		`(?:する)?(?:こと)?(?:にします|にしました|にする|にした|を決定しました|を決定した|と決定しました|と決定した)$`,
	)
	descriptionCertificatePattern    = regexp.MustCompile(`(?:VPN|外部接続)?証明書`)
	descriptionExpiryPattern         = regexp.MustCompile(`(?:期限切れ|失効)`)
	descriptionConnectionPattern     = regexp.MustCompile(`(?:リモート)?接続(?:が)?(?:できな(?:く|い|かった)?|できません)|接続不能`)
	descriptionRemotePattern         = regexp.MustCompile(`リモート接続`)
	descriptionGroundedDetailPattern = regexp.MustCompile(`(?:までに|今週|来週|今月|来月|月末|午前|午後|場合|すると|しないと|なければ|未更新|放置|可能性|おそれ|理由|原因|ため|さん|担当者|作業者|対象|範囲|別の|以外|ごと|単位|一覧|手順|間隔|通知条件|接続|影響|確認|修正|切り戻)`)
)

const (
	descriptionStatusNormal               = "normal"
	descriptionStatusGenerated            = "generated"
	descriptionStatusRewritten            = "rewritten"
	descriptionStatusIntentionallyOmitted = "intentionally_omitted"
	descriptionStatusRejectedUnsupported  = "rejected_unsupported"
	descriptionStatusGenerationFailed     = "generation_failed"
	descriptionStatusTransportLost        = "transport_lost"
)

func cloneDescriptionResolution(value *descriptionResolutionMetadata) *descriptionResolutionMetadata {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.SourceEvidenceSequenceNos = append([]int64(nil), value.SourceEvidenceSequenceNos...)
	return &cloned
}

func withDescriptionResolution(item liveAnalysisItem, status, reason string) liveAnalysisItem {
	item.DescriptionResolution = &descriptionResolutionMetadata{
		Status: strings.TrimSpace(status), Reason: strings.TrimSpace(reason),
		SourceEvidenceSequenceNos: uniqueSortedSequenceNos(
			sortedSequenceNos(append([]int64(nil), item.EvidenceSequenceNos...)),
		),
	}
	return item
}

// repairPersistedItemDescriptions enforces the presentation contract:
// Title is an independently readable headline and Body is optional grounded
// detail. It never adds an atom that is absent from cited transcript evidence.
func repairPersistedItemDescriptions(
	state *liveAnalysisPayload,
	scope liveEvidenceScope,
) {
	if state == nil || state.Tree == nil {
		return
	}
	segments := make(map[int64]domain.TranscriptSegment, len(scope.Segments))
	for sequenceNo, segment := range scope.Segments {
		segments[sequenceNo] = segment
	}
	for _, itemID := range activeFinalItemIDs(state.Items) {
		item, ok := finalItemByID(state.Items, itemID)
		if !ok {
			continue
		}
		if headline := deterministicDescriptionHeadline(item, scope); headline != "" && headline != item.Title {
			item.Title = headline
		}
		body := strings.TrimSpace(item.Body)
		projectionMismatch := descriptionProjectionMismatch(state.Tree, item)
		switch {
		case body == "" && stableEmptyDescriptionResolution(item):
			// A prior deterministic pass already classified this empty body.
			// Preserve the exact status/reason while its cited evidence is
			// unchanged so final repair remains byte-idempotent.
		case body == "":
			fallback, detailAvailable := groundedDescriptionFallback(item, scope)
			if fallback != "" {
				item.Body = fallback
				item = withDescriptionResolution(item, descriptionStatusGenerated, "grounded_detail_recovered_from_evidence")
			} else if detailAvailable {
				item = withDescriptionResolution(item, descriptionStatusGenerationFailed, "grounded_detail_generation_failed")
			} else {
				item = withDescriptionResolution(item, descriptionStatusIntentionallyOmitted, "no_additional_grounded_detail")
			}
		case descriptionUnsupportedAtomCount(item, segments) > 0:
			original := item.Body
			item.Body = ""
			fallback, _ := groundedDescriptionFallback(item, scope)
			if fallback != "" && normalizedLabelDescriptionText(fallback) != normalizedLabelDescriptionText(original) {
				item.Body = fallback
				item = withDescriptionResolution(item, descriptionStatusRewritten, "unsupported_detail_replaced_from_evidence")
			} else {
				item = withDescriptionResolution(item, descriptionStatusRejectedUnsupported, "unsupported_detail_removed")
			}
		case descriptionOnlyRestatesHeadline(item) ||
			(itemLabelContextDependentPattern.MatchString(body) &&
				incompleteItemLabelEnding(item) == ""):
			item.Body = ""
			item = withDescriptionResolution(item, descriptionStatusIntentionallyOmitted, "redundant_with_label")
		case projectionMismatch:
			item = withDescriptionResolution(item, descriptionStatusTransportLost, "item_tree_description_projection_mismatch_repaired")
		default:
			status := descriptionStatusNormal
			if item.DescriptionResolution != nil &&
				(item.DescriptionResolution.Status == descriptionStatusGenerated || item.DescriptionResolution.Status == descriptionStatusRewritten) {
				status = item.DescriptionResolution.Status
			}
			item = withDescriptionResolution(item, status, "grounded_detail_beyond_label")
		}
		updateFinalItemAndNode(state, item)
	}
}

func stableEmptyDescriptionResolution(item liveAnalysisItem) bool {
	if item.DescriptionResolution == nil ||
		!sameEvidenceSequenceSet(
			item.DescriptionResolution.SourceEvidenceSequenceNos,
			item.EvidenceSequenceNos,
		) {
		return false
	}
	switch item.DescriptionResolution.Status {
	case descriptionStatusIntentionallyOmitted,
		descriptionStatusRejectedUnsupported,
		descriptionStatusGenerationFailed:
		return true
	default:
		return false
	}
}

func descriptionProjectionMismatch(tree *liveAnalysisTree, item liveAnalysisItem) bool {
	if tree == nil {
		return false
	}
	for _, node := range tree.Nodes {
		if node.ID == item.ID {
			return strings.TrimSpace(node.Description) != strings.TrimSpace(item.Body)
		}
	}
	return false
}

// groundedDescriptionFallback selects only cited evidence clauses that add a
// deadline, condition, impact, reason, actor, scope or method not already
// conveyed by the headline. It is extractive and therefore cannot invent an
// unsupported atom.
func groundedDescriptionFallback(item liveAnalysisItem, scope liveEvidenceScope) (string, bool) {
	type candidate struct {
		sequenceNo int64
		text       string
		score      float64
	}
	var candidates []candidate
	detailAvailable := false
	for _, sequenceNo := range uniqueSortedSequenceNos(sortedSequenceNos(append([]int64(nil), item.EvidenceSequenceNos...))) {
		text := strings.TrimSpace(scope.TranscriptText[sequenceNo])
		for _, raw := range kindSentenceBoundaryPattern.Split(text, -1) {
			clause := strings.Trim(strings.TrimSpace(raw), "、。.!！ ")
			if clause == "" || !descriptionGroundedDetailPattern.MatchString(clause) {
				continue
			}
			detailAvailable = true
			probe := item
			probe.Body = clause
			if descriptionRedundant(probe) {
				continue
			}
			score := semanticItemSimilarity(item.Title, clause)
			if sharedTreeAuditSubjectTerm(item.Title, clause) {
				score += 0.20
			}
			if score < 0.12 {
				continue
			}
			candidates = append(candidates, candidate{sequenceNo: sequenceNo, text: clause, score: score})
		}
	}
	if len(candidates) == 0 {
		return "", detailAvailable
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].sequenceNo < candidates[j].sequenceNo
	})
	selected := candidates[0].text
	for _, candidate := range candidates[1:] {
		if candidate.sequenceNo == candidates[0].sequenceNo &&
			semanticItemSimilarity(selected, candidate.text) >= 0.10 &&
			len([]rune(selected+"。"+candidate.text)) <= liveAnalysisTreeDescriptionMaxRunes {
			selected += "。" + candidate.text
		}
	}
	return truncateRunes(selected, liveAnalysisTreeDescriptionMaxRunes), detailAvailable
}

type descriptionResolutionSummary struct {
	ItemCount                      int
	DescriptionPresentCount        int
	IntentionallyOmittedCount      int
	GenerationFailedCount          int
	RejectedUnsupportedCount       int
	TransportLostCount             int
	LabelDescriptionExactDuplicate int
	LabelDescriptionHighSimilarity int
	DescriptionAddedGroundedDetail int
}

func summarizeDescriptionResolutions(state liveAnalysisPayload, scope liveEvidenceScope) descriptionResolutionSummary {
	segments := make(map[int64]domain.TranscriptSegment, len(scope.Segments))
	for sequenceNo, segment := range scope.Segments {
		segments[sequenceNo] = segment
	}
	var summary descriptionResolutionSummary
	for _, itemID := range activeFinalItemIDs(state.Items) {
		item, ok := finalItemByID(state.Items, itemID)
		if !ok {
			continue
		}
		summary.ItemCount++
		if strings.TrimSpace(item.Body) != "" {
			summary.DescriptionPresentCount++
		}
		if item.DescriptionResolution != nil {
			switch item.DescriptionResolution.Status {
			case descriptionStatusIntentionallyOmitted:
				summary.IntentionallyOmittedCount++
			case descriptionStatusGenerationFailed:
				summary.GenerationFailedCount++
			case descriptionStatusRejectedUnsupported:
				summary.RejectedUnsupportedCount++
			case descriptionStatusTransportLost:
				summary.TransportLostCount++
			}
		}
		if labelDescriptionExactDuplicate(item) {
			summary.LabelDescriptionExactDuplicate++
		}
		if labelDescriptionHighSimilarity(item) {
			summary.LabelDescriptionHighSimilarity++
		}
		if descriptionAddsGroundedDetail(item, segments) {
			summary.DescriptionAddedGroundedDetail++
		}
	}
	return summary
}

func deterministicDescriptionHeadline(item liveAnalysisItem, scope liveEvidenceScope) string {
	title := strings.Trim(strings.TrimSpace(item.Title), "。.!！?？ ")
	body := strings.Trim(strings.TrimSpace(item.Body), "。.!！?？ ")
	if title == "" {
		return ""
	}
	combined := itemLabelSemanticSourceText(item, scope)
	if strings.TrimSpace(combined) == "" {
		combined = title + " " + body
	}
	candidate := ""
	switch {
	case item.Kind == "risk" &&
		descriptionCertificatePattern.MatchString(combined) &&
		descriptionExpiryPattern.MatchString(combined) &&
		descriptionConnectionPattern.MatchString(combined):
		certificateSubject := "証明書"
		switch {
		case strings.Contains(combined, "VPN証明書"):
			certificateSubject = "VPN証明書"
		case strings.Contains(combined, "外部接続証明書"):
			certificateSubject = "外部接続証明書"
		}
		candidate = certificateSubject + "失効による接続不能リスク"
		if descriptionRemotePattern.MatchString(combined) {
			candidate = certificateSubject + "失効によるリモート接続不能リスク"
		}
	case item.Kind == "decision":
		candidate = strings.TrimPrefix(title, "この")
		candidate = descriptionDecisionSuffixPattern.ReplaceAllString(candidate, "")
		candidate = strings.TrimSuffix(candidate, "する")
	}
	if candidate == "" && (labelDescriptionExactDuplicate(item) || labelDescriptionHighSimilarity(item)) {
		candidate = semanticallyCompleteItemLabel(title, item.Kind)
	}
	candidate = strings.Trim(strings.TrimSpace(candidate), "。.!！?？ ")
	if candidate == "" || len([]rune(candidate)) > liveAnalysisTreeDescriptionMaxRunes ||
		len([]rune(candidate)) > len([]rune(title)) ||
		!itemLabelCandidatePreservesSemanticsWithQualifierPolicy(item, candidate, scope, false) {
		return title
	}
	return candidate
}

func descriptionOnlyRestatesHeadline(item liveAnalysisItem) bool {
	if labelDescriptionExactDuplicate(item) {
		return true
	}
	label := normalizedLabelDescriptionText(item.Title)
	body := normalizedLabelDescriptionText(item.Body)
	if label == "" || body == "" {
		return false
	}
	labelQualifiers := itemLabelConcreteQualifierPattern.FindAllString(item.Title, -1)
	for _, qualifier := range itemLabelConcreteQualifierPattern.FindAllString(item.Body, -1) {
		if !containsFoldedString(labelQualifiers, qualifier) {
			return false
		}
	}
	if labelDescriptionHighSimilarity(item) {
		return true
	}
	if item.Kind != "decision" {
		return item.AssignmentReason == deterministicLimitAssignmentReason &&
			(qualityBigramDice(label, body) >= 0.62 ||
				semanticItemSimilarity(item.Title, item.Body) >= 0.52)
	}
	return strings.Contains(body, label) ||
		qualityBigramDice(label, body) >= 0.62 ||
		semanticItemSimilarity(item.Title, item.Body) >= 0.52
}

func containsFoldedString(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}
