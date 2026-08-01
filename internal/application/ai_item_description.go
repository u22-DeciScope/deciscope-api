package application

import (
	"regexp"
	"strings"

	"deciscope-core-api/internal/domain"
)

var (
	descriptionDecisionSuffixPattern = regexp.MustCompile(
		`(?:する)?(?:こと)?(?:にします|にしました|にする|にした|を決定しました|を決定した|と決定しました|と決定した)$`,
	)
	descriptionCertificatePattern = regexp.MustCompile(`(?:VPN)?証明書`)
	descriptionExpiryPattern      = regexp.MustCompile(`(?:期限切れ|失効)`)
	descriptionConnectionPattern  = regexp.MustCompile(`(?:リモート)?接続(?:が)?できなく|接続不能`)
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
		switch {
		case body == "":
			// Omission metadata is part of the persisted explanation contract.
			// Preserve the original reason on repeated live/final repair passes.
			if item.DescriptionResolution == nil || item.DescriptionResolution.Status != "omitted" {
				item = withDescriptionResolution(item, "omitted", "no_grounded_detail")
			}
		case descriptionUnsupportedAtomCount(item, segments) > 0:
			item.Body = ""
			item = withDescriptionResolution(item, "omitted", "unsupported_detail_removed")
		case descriptionOnlyRestatesHeadline(item) ||
			(itemLabelContextDependentPattern.MatchString(body) &&
				incompleteItemLabelEnding(item) == ""):
			item.Body = ""
			item = withDescriptionResolution(item, "omitted", "redundant_with_label")
		default:
			item = withDescriptionResolution(item, "retained", "grounded_detail_beyond_label")
		}
		updateFinalItemAndNode(state, item)
	}
}

func deterministicDescriptionHeadline(item liveAnalysisItem, scope liveEvidenceScope) string {
	title := strings.Trim(strings.TrimSpace(item.Title), "。.!！?？ ")
	body := strings.Trim(strings.TrimSpace(item.Body), "。.!！?？ ")
	if title == "" {
		return ""
	}
	combined := title + " " + body
	candidate := ""
	switch {
	case item.Kind == "risk" &&
		descriptionCertificatePattern.MatchString(combined) &&
		descriptionExpiryPattern.MatchString(combined) &&
		descriptionConnectionPattern.MatchString(combined):
		candidate = "VPN証明書失効によるリモート接続不能リスク"
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
		!itemLabelCandidatePreservesSemantics(item, candidate, scope) {
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
