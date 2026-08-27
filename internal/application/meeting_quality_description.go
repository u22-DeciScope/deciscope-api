package application

import (
	"strings"
	"unicode"

	"deciscope-core-api/internal/domain"
)

func normalizedLabelDescriptionText(value string) string {
	value = strings.TrimSpace(value)
	for {
		before := value
		for _, prefix := range []string{"ええと", "えっと", "ええ", "あの", "先ほどの", "先程の"} {
			value = strings.TrimSpace(strings.TrimPrefix(value, prefix))
		}
		if value == before {
			break
		}
	}
	var b strings.Builder
	for _, r := range value {
		if unicode.IsSpace(r) || strings.ContainsRune("。、，．,.!！?？・（）()「」『』[]［］", r) {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	normalized := b.String()
	replacer := strings.NewReplacer(
		"ことにします", "ことにする",
		"ことにしました", "ことにする",
		"しています", "している",
		"していました", "していた",
		"でした", "だ",
		"です", "",
		"ました", "た",
		"ます", "る",
	)
	return strings.TrimSpace(replacer.Replace(normalized))
}

func labelDescriptionExactDuplicate(item liveAnalysisItem) bool {
	label := normalizedLabelDescriptionText(item.Title)
	description := normalizedLabelDescriptionText(item.Body)
	return label != "" && description != "" && label == description
}

func labelDescriptionHighSimilarity(item liveAnalysisItem) bool {
	if labelDescriptionExactDuplicate(item) || strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Body) == "" {
		return false
	}
	return semanticItemSimilarity(item.Title, item.Body) >= 0.82 ||
		qualityBigramDice(normalizedLabelDescriptionText(item.Title), normalizedLabelDescriptionText(item.Body)) >= 0.88
}

func descriptionRedundant(item liveAnalysisItem) bool {
	return labelDescriptionExactDuplicate(item) || labelDescriptionHighSimilarity(item)
}

func qualityEvidenceText(item liveAnalysisItem, segments map[int64]domain.TranscriptSegment) string {
	parts := make([]string, 0, len(item.EvidenceSequenceNos))
	for _, sequenceNo := range item.EvidenceSequenceNos {
		if text := strings.TrimSpace(segments[sequenceNo].Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "。")
}

func descriptionUnsupportedAtomCount(item liveAnalysisItem, segments map[int64]domain.TranscriptSegment) int {
	description := strings.TrimSpace(item.Body)
	if description == "" {
		return 0
	}
	evidence := qualityEvidenceText(item, segments)
	if evidence == "" {
		return 1
	}
	unsupported := 0
	evidenceQualifiers := make(map[string]struct{})
	for _, qualifier := range itemLabelConcreteQualifierPattern.FindAllString(evidence, -1) {
		evidenceQualifiers[canonicalReferenceKey(qualifier)] = struct{}{}
	}
	for _, qualifier := range itemLabelConcreteQualifierPattern.FindAllString(description, -1) {
		if _, ok := evidenceQualifiers[canonicalReferenceKey(qualifier)]; !ok {
			unsupported++
		}
	}
	descriptionKey := normalizedLabelDescriptionText(description)
	evidenceKey := normalizedLabelDescriptionText(evidence)
	if descriptionKey != "" && evidenceKey != "" &&
		!strings.Contains(evidenceKey, descriptionKey) &&
		qualityBigramDice(descriptionKey, evidenceKey) < 0.20 &&
		semanticItemSimilarity(description, evidence) < 0.18 {
		unsupported++
	}
	return unsupported
}

func descriptionAddsGroundedDetail(item liveAnalysisItem, segments map[int64]domain.TranscriptSegment) bool {
	if strings.TrimSpace(item.Body) == "" || descriptionRedundant(item) ||
		descriptionUnsupportedAtomCount(item, segments) != 0 {
		return false
	}
	labelKey := normalizedLabelDescriptionText(item.Title)
	descriptionKey := normalizedLabelDescriptionText(item.Body)
	return len([]rune(descriptionKey)) > len([]rune(labelKey)) ||
		!strings.Contains(descriptionKey, labelKey)
}

func labelCopiesTranscript(item liveAnalysisItem, segments map[int64]domain.TranscriptSegment) bool {
	labelKey := normalizedLabelDescriptionText(item.Title)
	if labelKey == "" {
		return false
	}
	for _, sequenceNo := range item.EvidenceSequenceNos {
		text := segments[sequenceNo].Text
		textKey := normalizedLabelDescriptionText(text)
		if textKey == "" {
			continue
		}
		if labelKey == textKey ||
			(strings.Contains(textKey, labelKey) && float64(len([]rune(labelKey)))/float64(len([]rune(textKey))) >= 0.80) ||
			semanticItemSimilarity(item.Title, text) >= 0.92 {
			return true
		}
	}
	return false
}

func labelCompressionForItem(item liveAnalysisItem, segments map[int64]domain.TranscriptSegment) (float64, bool) {
	labelLength := len([]rune(normalizedLabelDescriptionText(item.Title)))
	evidenceLength := len([]rune(normalizedLabelDescriptionText(qualityEvidenceText(item, segments))))
	if labelLength == 0 || evidenceLength == 0 {
		return 0, false
	}
	ratio := float64(labelLength) / float64(evidenceLength)
	if ratio > 1 {
		ratio = 1
	}
	return ratio, true
}
