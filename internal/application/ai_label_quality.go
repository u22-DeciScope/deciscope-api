package application

import (
	"log"
	"regexp"
	"strings"
	"unicode"
)

// Label quality gate for detail items. A detail node in the discussion tree is
// read on its own, so its label must be a self-contained proposition: a subject
// (or an evidence-supported relation) plus a predicate. A bare noun phrase such
// as 「有線LAN車内有線LANファイルサーバー」 carries no proposition and must never
// reach an active detail node, even when the model produced it confidently.
//
// Topic and group labels stay noun phrases by design and are not evaluated
// here; only the five detail kinds are.
const (
	labelQualityEndingBareEnumeration = "bare_enumeration"
	labelQualityEndingNonProposition  = "non_propositional"
)

var labelQualityDetailKinds = map[string]struct{}{
	"fact": {}, "issue": {}, "risk": {}, "todo": {}, "decision": {},
}

var (
	// A real predicate: an inflected verb/adjective/copula ending. Katakana and
	// kanji nouns deliberately do not match, so a noun phrase is never mistaken
	// for a proposition.
	labelQualityPredicateEndingPattern = regexp.MustCompile(
		`(?:です|でした|ですか|でしょう|である|であった|だった|だ|` +
			`ます|ました|ません|ませんでした|ましょう|` +
			`ない|なかった|ぬ|ず|` +
			`[うくぐすずつぬぶむる]|` +
			`[いきしちにひみりぎじびえけせてねへめれげぜでべ]た|った|んだ|` +
			`[しくき]い|かった` +
			`)[。．.!！?？]?$`)
	// State nouns that are used predicatively in this product's vocabulary.
	// They are accepted so the gate does not invalidate the existing corpus of
	// 「〜が未確認」/「〜が期限切れ」 style labels.
	labelQualityNominalStateEndingPattern = regexp.MustCompile(
		`(?:未確認|未決定|未確定|未解決|未定|未着手|未実施|不明|不足|不可|` +
			`期限切れ|漏れ|異常なし|問題なし|なし|要確認|要対応|必要|不能)[。．.!！?？]?$`)
	// A clause particle proves the label relates two things rather than listing
	// them. の (genitive) and enumerating と/や/・ deliberately do not count.
	labelQualityRelationParticlePattern = regexp.MustCompile(`(?:は|が|を|に|へ|で|から|より|まで|では|には|とは|でも)`)
	labelQualitySubjectParticlePattern  = regexp.MustCompile(`[^\s、,。]{1,40}?(?:は|が|も|には|では|とは)`)
	labelQualityEnumerationMarkerPattern = regexp.MustCompile(
		`(?:、|,|・|／|/|と|や|および|及び|ならびに|並びに)`)
	// A cleft sentence 「<述語>のは、<対象>です」 names its subject after the
	// predicate. Inverting it recovers the proposition without inventing words.
	labelQualityCleftPattern = regexp.MustCompile(`^(.{2,60}?)のは[、,]?(.{2,120}?)(?:です|でした|だ|である)[。．.!！]?$`)
)

// labelQualityAssessment is the structured verdict recorded for one label. It
// intentionally holds only booleans so the observability log never has to print
// meeting text.
type labelQualityAssessment struct {
	Evaluated                          bool
	LabelHasSubject                    bool
	LabelHasPredicateOrRelation        bool
	LabelIsStandaloneProposition       bool
	LabelIsBareEnumeration             bool
	LabelEndsWithIncompleteParticle    bool
	LabelEndsWithConjugationFragment   bool
	LabelLooksTruncated                bool
	LabelContainsRepeatedAdjacentTerms bool
	LabelContainsUnresolvedSTTNoise    bool
}

// evaluateItemLabelQuality judges the user-visible label of a detail item.
func evaluateItemLabelQuality(item liveAnalysisItem) labelQualityAssessment {
	label := strings.Trim(strings.TrimSpace(item.Title), "。．.!！?？ ")
	if _, detail := labelQualityDetailKinds[item.Kind]; !detail || label == "" {
		return labelQualityAssessment{}
	}
	assessment := labelQualityAssessment{Evaluated: true}
	assessment.LabelEndsWithIncompleteParticle = itemLabelDanglingParticlePattern.MatchString(label)
	assessment.LabelEndsWithConjugationFragment = itemLabelIncompleteConjugationPattern.MatchString(label)
	assessment.LabelLooksTruncated = itemLabelDanglingConnectorPattern.MatchString(label)
	assessment.LabelContainsRepeatedAdjacentTerms = labelHasRepeatedAdjacentTerm(label)
	assessment.LabelContainsUnresolvedSTTNoise = assessment.LabelContainsRepeatedAdjacentTerms

	assessment.LabelHasPredicateOrRelation = labelQualityHasPredicate(item.Kind, label)
	assessment.LabelHasSubject = labelQualityHasSubject(label)
	// Japanese headline labels legitimately drop the subject
	// (「旧スイッチへ切り戻した」/「承認します」), so a missing subject is
	// reported but never rejected on its own. The predicate is the signal that
	// separates a proposition from a noun list.
	assessment.LabelIsStandaloneProposition = assessment.LabelHasPredicateOrRelation &&
		!assessment.LabelEndsWithIncompleteParticle &&
		!assessment.LabelEndsWithConjugationFragment &&
		!assessment.LabelLooksTruncated &&
		!assessment.LabelContainsUnresolvedSTTNoise
	// A list has no case particle tying its members to a predicate. Requiring
	// that keeps a conditional/conjunctive と inside a verb form
	// (「放置すると接続できてい」) from being read as a list separator.
	listShaped := !labelQualityRelationParticlePattern.MatchString(label) &&
		labelQualityEnumerationMarkerPattern.MatchString(label)
	assessment.LabelIsBareEnumeration = !assessment.LabelHasPredicateOrRelation &&
		(listShaped ||
			assessment.LabelContainsRepeatedAdjacentTerms ||
			labelHasAdjacentNounRuns(label))
	return assessment
}

// labelQualityEnding maps the assessment to the ending vocabulary the existing
// repair pipeline already understands, so a quality failure reuses the whole
// grounded-rewrite / reject machinery instead of a parallel code path.
func labelQualityEnding(item liveAnalysisItem) string {
	assessment := evaluateItemLabelQuality(item)
	if !assessment.Evaluated || assessment.LabelIsStandaloneProposition {
		return ""
	}
	if assessment.LabelIsBareEnumeration || assessment.LabelContainsUnresolvedSTTNoise {
		return labelQualityEndingBareEnumeration
	}
	// A predicate-less label that is not a list (「監視条件」「VLAN30の設定」) is
	// reported through LabelIsStandaloneProposition and the metrics, but is not
	// rejected: Japanese headline labels legitimately end in a verb stem
	// (「旧スイッチへ切り戻し」) or a state noun, and distinguishing those from a
	// truly empty noun phrase needs more than an ending rule. Acting on that
	// class would remove correct propositions from the tree.
	return ""
}

func labelQualityFailureEnding(endingType string) bool {
	return endingType == labelQualityEndingBareEnumeration ||
		endingType == labelQualityEndingNonProposition
}

// labelQualityActionable reports whether the item's own cited transcript is
// available. The gate only ever replaces a label with wording the evidence
// supports, so without that evidence there is nothing safe to do: internal
// replayers and historical payload migrations run without transcript text and
// must not lose items to a gate that cannot repair them.
func labelQualityActionable(item liveAnalysisItem, scope liveEvidenceScope) bool {
	if len(scope.TranscriptText) == 0 {
		return false
	}
	for _, sequenceNo := range item.EvidenceSequenceNos {
		if strings.TrimSpace(scope.TranscriptText[sequenceNo]) != "" {
			return true
		}
	}
	return false
}

// labelQualityHasPredicate reuses the product's established notion of a
// complete label ending and adds general Japanese inflection. An action
// nominalization (「〜の確認」「期限を金曜日に訂正」) states what was or will be
// done and therefore counts; a plain object noun (「監視条件」「VLAN30の設定」
// 「来月末の証明書」) matches nothing here and fails the gate.
func labelQualityHasPredicate(kind, label string) bool {
	_ = kind
	return itemLabelCompletePredicatePattern.MatchString(label) ||
		itemLabelNaturalNominalizationPattern.MatchString(label) ||
		labelQualityPredicateEndingPattern.MatchString(label) ||
		labelQualityNominalStateEndingPattern.MatchString(label)
}

// labelQualityHasSubject accepts either an explicit topic/subject particle or
// any case particle that ties the label's predicate to a named object. A
// Japanese proposition may legitimately drop its subject
// (「旧スイッチへ切り戻した」), so a case particle is enough.
func labelQualityHasSubject(label string) bool {
	if labelQualitySubjectParticlePattern.MatchString(label) {
		return true
	}
	return labelQualityRelationParticlePattern.MatchString(label)
}

// labelHasRepeatedAdjacentTerm detects the ASR splice shape where the same
// content run is transcribed twice with at most a short bridge between the two
// occurrences. It is a structural signal, not a dictionary of known errors.
func labelHasRepeatedAdjacentTerm(label string) bool {
	runes := []rune(strings.TrimSpace(label))
	if len(runes) < 6 {
		return false
	}
	const minTerm = 3
	const maxBridge = 3
	for length := len(runes) / 2; length >= minTerm; length-- {
		for start := 0; start+length <= len(runes); start++ {
			term := runes[start : start+length]
			// Only a content run (kanji/katakana/latin, no particles) counts.
			// Repeating a grammatical fragment is normal Japanese; repeating a
			// whole content term within a few characters is an ASR splice.
			if !labelIsContentRun(term) {
				continue
			}
			for next := start + length; next+length <= len(runes) && next <= start+length+maxBridge; next++ {
				if string(runes[next:next+length]) != string(term) {
					continue
				}
				// 「有線LAN・無線LAN」 repeats a term across an explicit list
				// separator: that is a legitimate enumeration, not a splice.
				// Only a content-only bridge (「有線LAN車内有線LAN」) is noise.
				if bridge := runes[start+length : next]; len(bridge) == 0 || labelIsContentRun(bridge) {
					return true
				}
			}
		}
	}
	return false
}

func labelIsContentRun(runes []rune) bool {
	for _, r := range runes {
		switch labelRuneClass(r) {
		case 1, 2, 3:
		default:
			return false
		}
	}
	return len(runes) > 0
}

// labelHasAdjacentNounRuns detects a noun pile-up: two or more content runs
// (kanji / katakana / latin) glued directly together with no particle, space or
// punctuation between them. 「有線LAN車内有線LANファイルサーバー」 has that shape;
// 「拠点間回線の冗長化」 does not, because a hiragana particle separates its
// nouns.
func labelHasAdjacentNounRuns(label string) bool {
	// A case particle proves the nouns are related to each other rather than
	// merely listed, so 「3階を中心に社内ネットワークへ接続不能」 is not a pile-up.
	if labelQualityRelationParticlePattern.MatchString(label) {
		return false
	}
	adjacent := 0
	previousClass := 0
	for _, r := range label {
		class := labelRuneClass(r)
		if class == 0 || class == 4 || unicode.IsSpace(r) {
			// Hiragana, punctuation and spaces end a noun run.
			previousClass = 0
			continue
		}
		if previousClass != 0 && class != previousClass {
			adjacent++
		}
		previousClass = class
	}
	return adjacent >= 2
}

func labelRuneClass(r rune) int {
	switch {
	case r == 0:
		return 0
	case unicode.Is(unicode.Han, r):
		return 1
	case unicode.Is(unicode.Katakana, r) || r == 'ー':
		return 2
	case (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
		return 3
	case unicode.Is(unicode.Hiragana, r):
		return 4
	default:
		return 0
	}
}

// abstractLabelFromCleftEnumeration is the §12.3 abstract rewrite. When the
// only grounded evidence is a cleft sentence whose listed subject carries ASR
// noise, the individual entities cannot be reproduced safely. The predicate is
// kept verbatim from the transcript and the subject is replaced by a neutral
// quantifier derived from the number of listed members - no entity is invented
// and no mishearing is "corrected".
func abstractLabelFromCleftEnumeration(
	item liveAnalysisItem,
	scope liveEvidenceScope,
	timeline discourseTimeline,
) (string, string, bool) {
	if _, detail := labelQualityDetailKinds[item.Kind]; !detail {
		return "", "", false
	}
	for _, sequenceNo := range item.EvidenceSequenceNos {
		switch timeline.Roles[sequenceNo] {
		case liveEvidenceReferenceRecap, liveEvidenceDiscourseOnly:
			continue
		}
		evidence := strings.TrimSpace(scope.TranscriptText[sequenceNo])
		match := labelQualityCleftPattern.FindStringSubmatch(evidence)
		if len(match) != 3 {
			continue
		}
		predicate := strings.TrimSpace(match[1])
		subject := strings.TrimSpace(match[2])
		if predicate == "" || subject == "" {
			continue
		}
		members := splitLabelEnumerationMembers(subject)
		if len(members) < 2 {
			continue
		}
		// Only abstract when reproducing the members verbatim would carry ASR
		// noise into the label. A clean enumeration keeps its normal grounded
		// rewrite path.
		noisy := false
		for _, member := range members {
			if labelHasRepeatedAdjacentTerm(member) {
				noisy = true
				break
			}
		}
		if !noisy {
			continue
		}
		candidate := labelQualityAbstractSubject + "が" + predicate
		probe := liveAnalysisItem{Kind: item.Kind, Subtype: item.Subtype, Title: candidate, Body: candidate}
		if incompleteItemLabelEnding(probe) != "" {
			continue
		}
		return candidate, evidence, true
	}
	return "", "", false
}

// labelQualityAbstractSubject is deliberately content-free. Naming a category
// ("ネットワークサービス") would add information the transcript does not
// support; the concrete elements stay in the description instead.
const labelQualityAbstractSubject = "複数の対象"

func splitLabelEnumerationMembers(value string) []string {
	parts := regexp.MustCompile(`[、,・／/]|および|及び|ならびに|並びに`).Split(value, -1)
	members := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(strings.TrimSpace(part), "、。 ")
		if part != "" {
			members = append(members, part)
		}
	}
	return members
}

// labelQualityStats aggregates the per-round label quality decisions. Only
// counts and item identifiers are recorded.
type labelQualityStats struct {
	BareEnumerationLabels     int
	NonPropositionalLabels    int
	MissingSubjectLabels      int
	MissingPredicateLabels    int
	TruncatedLabels           int
	RepeatedTermLabels        int
	GroundedLabelRewrites     int
	AbstractLabelRewrites     int
	UnsupportedLabelRewrites  int
	LabelRewritesFailed       int
	LowQualityItemsHidden     int
	LowQualityItemsRejected   int
	ManualLabelsPreserved     int
	EvaluatedDetailItemLabels int
}

func (s *labelQualityStats) record(assessment labelQualityAssessment) {
	if s == nil || !assessment.Evaluated {
		return
	}
	s.EvaluatedDetailItemLabels++
	if assessment.LabelIsBareEnumeration {
		s.BareEnumerationLabels++
	}
	if !assessment.LabelIsStandaloneProposition {
		s.NonPropositionalLabels++
	}
	if !assessment.LabelHasSubject {
		s.MissingSubjectLabels++
	}
	if !assessment.LabelHasPredicateOrRelation {
		s.MissingPredicateLabels++
	}
	if assessment.LabelLooksTruncated {
		s.TruncatedLabels++
	}
	if assessment.LabelContainsRepeatedAdjacentTerms {
		s.RepeatedTermLabels++
	}
}

func (s labelQualityStats) empty() bool {
	return s.EvaluatedDetailItemLabels == 0
}

// logLabelQualitySummary emits the per-round label quality metrics. Only
// counts are recorded; no meeting text ever reaches the log.
func logLabelQualitySummary(sessionID string, analysisVersion int64, phase string, stats *liveAnalysisTreeMergeStats) {
	if stats == nil {
		return
	}
	logLabelQuality(sessionID, analysisVersion, phase, stats.LabelQuality, stats.ManualLabelsPreserved)
}

func logFinalLabelQualitySummary(sessionID string, analysisVersion int64, stats finalRepairStats) {
	logLabelQuality(sessionID, analysisVersion, "final_repair", stats.LabelQuality, stats.ManualLabelsPreserved)
}

func logLabelQuality(sessionID string, analysisVersion int64, phase string, quality labelQualityStats, manualPreserved int) {
	if quality.empty() {
		return
	}
	log.Printf("Label quality evaluated. event=label_quality_summary sessionId=%s analysisVersion=%d phase=%s evaluatedDetailItemLabels=%d bareEnumerationLabelCount=%d nonPropositionalLabelCount=%d missingSubjectLabelCount=%d missingPredicateLabelCount=%d truncatedLabelCount=%d repeatedTermLabelCount=%d groundedLabelRewriteCount=%d abstractLabelRewriteCount=%d unsupportedLabelRewriteCount=%d labelRewriteFailedCount=%d lowQualityItemHiddenCount=%d lowQualityItemRejectedCount=%d manualLabelPreservedCount=%d",
		sessionID, analysisVersion, phase, quality.EvaluatedDetailItemLabels,
		quality.BareEnumerationLabels, quality.NonPropositionalLabels,
		quality.MissingSubjectLabels, quality.MissingPredicateLabels,
		quality.TruncatedLabels, quality.RepeatedTermLabels,
		quality.GroundedLabelRewrites, quality.AbstractLabelRewrites,
		quality.UnsupportedLabelRewrites, quality.LabelRewritesFailed,
		quality.LowQualityItemsHidden, quality.LowQualityItemsRejected,
		manualPreserved)
}
