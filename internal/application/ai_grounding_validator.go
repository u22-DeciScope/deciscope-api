package application

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type groundingSourceType string

const (
	groundingSourceFinalTranscript   groundingSourceType = "final_transcript"
	groundingSourcePartialTranscript groundingSourceType = "partial_transcript"
	groundingSourcePreMeetingInput   groundingSourceType = "pre_meeting_input"
	groundingSourceAgendaTitle       groundingSourceType = "agenda_title"
	groundingSourceAgendaMetadata    groundingSourceType = "agenda_metadata"
	groundingSourceSemanticHint      groundingSourceType = "semantic_hint"
	groundingSourceExistingTree      groundingSourceType = "existing_tree"
	groundingSourceAuditFinding      groundingSourceType = "audit_finding"
	groundingSourceRecapTranscript   groundingSourceType = "recap_transcript"
	groundingSourceModelInference    groundingSourceType = "model_inference"
)

type itemGroundingDecision struct {
	Stage                     string
	ItemID                    string
	ModelItemID               string
	SourceItemID              string
	EvidenceSequences         []int64
	SourceTypes               []groundingSourceType
	SubjectGrounded           bool
	PredicateGrounded         bool
	EntityGrounded            bool
	QualifierGrounded         bool
	UnsupportedAtomHashes     []string
	UnsupportedAtomCategories []string
	UnsupportedAtomCount      int
	ContextOnlyAtomCount      int
	FutureInformationDetected bool
	Decision                  string
	Reason                    string
	Confidence                float64
	SplitFragment             bool
}

type groundingAtom struct {
	Category string
	Value    string
}

type groundingContextEntry struct {
	Source groundingSourceType
	Text   string
}

type groundingContextCatalog []groundingContextEntry

var (
	groundingLatinIdentifierPattern = regexp.MustCompile(`(?i)[a-z][a-z0-9._/]*[-_]?[0-9]+[a-z0-9._/-]*`)
	groundingUpperTokenPattern      = regexp.MustCompile(`[A-ZＡ-Ｚ]{2,}`)
	groundingNumberPattern          = regexp.MustCompile(`[0-9０-９]+(?:[.:：．][0-9０-９]+)?`)
	groundingNumberWithUnitPattern  = regexp.MustCompile(`(?:午前|午後)?[0-9０-９一二三四五六七八九十百千]+(?:時|分|秒|階|棟|室|日|月|年|週|人|件|台|回|%|％|m/s|ms|gb|mb|kb)(?:[0-9０-９一二三四五六七八九十百千]+(?:時|分|秒))?(?:ごろ|頃)?`)
	groundingPersonPattern          = regexp.MustCompile(`[\p{Han}\p{Katakana}A-Za-z]{1,12}(?:さん|氏)`)
	groundingLocationPattern        = regexp.MustCompile(`[\p{Han}\p{Katakana}A-Za-z]{2,16}(?:支社|支店|本社|拠点|センター|会議室)`)
	groundingDeadlinePattern        = regexp.MustCompile(`(?:本日中|今日中|明日(?:まで)?|今週中|来週中|月末まで|週末まで|次回(?:会議)?まで)`)
	groundingOwnerGenericPattern    = regexp.MustCompile(`(?:担当者|責任者|オーナー|owner|assignee)`)
	groundingCausePattern           = regexp.MustCompile(`(?:(?:が|を)(?:直接|主な|根本)?原因|原因(?:で|と)|に起因|によって[^。]{0,24}(?:発生|障害)|直接要因|根本要因)`)
	groundingSubjectChunkPattern    = regexp.MustCompile(`[\p{Han}\p{Katakana}A-Za-z0-9０-９]{2,}`)
	groundingSentencePattern        = regexp.MustCompile(`[。！？、,\r\n]+`)
	groundingSentenceEndPattern     = regexp.MustCompile(`[。！？\r\n]+`)
)

type groundingPredicatePattern struct {
	Name    string
	Pattern *regexp.Regexp
}

var groundingPredicatePatterns = []groundingPredicatePattern{
	{Name: "outage", Pattern: regexp.MustCompile(`(?:障害|接続(?:できな|不能|不可)|通信(?:できな|不能|不可|切断)|不通)`)},
	{Name: "delay", Pattern: regexp.MustCompile(`(?:遅延|遅い|低速|時間がかか)`)},
	{Name: "report", Pattern: regexp.MustCompile(`(?:報告|連絡|申告|問い合わせがあ)`)},
	{Name: "occurrence", Pattern: regexp.MustCompile(`(?:発生|起き|生じ|報告があ|確認され)`)},
	{Name: "confirmation", Pattern: regexp.MustCompile(`(?:確認|判明|分か|特定|観測|検証でき|確定)`)},
	{Name: "recovery", Pattern: regexp.MustCompile(`(?:復旧|回復|正常にな|解消|疎通を確認)`)},
	{Name: "investigation", Pattern: regexp.MustCompile(`(?:調査|検証|確認する|切り分け|特定する)`)},
	{Name: "replacement", Pattern: regexp.MustCompile(`(?:交換|取り替え|入れ替え)`)},
	{Name: "monitoring", Pattern: regexp.MustCompile(`(?:監視|モニタリング|検知)`)},
	{Name: "configuration", Pattern: regexp.MustCompile(`(?:設定|許可|適用|構成|登録)`)},
	{Name: "update", Pattern: regexp.MustCompile(`(?:更新|修正|変更|追加|削除|見直し)`)},
	{Name: "assignment", Pattern: regexp.MustCompile(`(?:担当|依頼|任せ|アサイン)`)},
	{Name: "decision", Pattern: regexp.MustCompile(`(?:決定|合意|採用|承認|見送|却下|ことにする|とする)`)},
	{Name: "impact", Pattern: regexp.MustCompile(`(?:影響|損失|停止|過多|漏えい|危険|利用できな)`)},
	{Name: "expiry", Pattern: regexp.MustCompile(`(?:期限切れ|失効|有効期限)`)},
	{Name: "cause", Pattern: groundingCausePattern},
}

var groundingIgnoredSubjectChunks = map[string]struct{}{
	"発生": {}, "報告": {}, "確認": {}, "調査": {}, "検証": {}, "対応": {},
	"実施": {}, "予定": {}, "問題": {}, "課題": {}, "論点": {}, "可能性": {},
	"影響": {}, "原因": {}, "決定": {}, "更新": {}, "変更": {}, "追加": {},
	"今回": {}, "現在": {}, "今後": {}, "中心": {}, "結果": {}, "状態": {},
}

func buildGroundingContextCatalog(mc *meetingContext, previous []liveAnalysisItem) groundingContextCatalog {
	var catalog groundingContextCatalog
	appendEntry := func(source groundingSourceType, values ...string) {
		text := strings.TrimSpace(strings.Join(values, " "))
		if text != "" {
			catalog = append(catalog, groundingContextEntry{Source: source, Text: text})
		}
	}
	if mc != nil {
		appendEntry(groundingSourcePreMeetingInput,
			mc.Title, mc.Purpose, mc.Background, mc.DecisionPoints, mc.Concerns,
			mc.ExpectedOutput, strings.Join(mc.Directives, " "))
		for _, agenda := range mc.Agenda {
			appendEntry(groundingSourceAgendaTitle, agenda.Title)
			appendEntry(groundingSourceAgendaMetadata, agenda.Description, agenda.Goal)
			appendEntry(groundingSourceSemanticHint, agenda.SemanticHints...)
		}
	}
	for _, item := range previous {
		if item.Inactive || item.MergedIntoID != "" {
			continue
		}
		appendEntry(groundingSourceExistingTree, item.Title, item.Body)
	}
	return catalog
}

func validateLiveItemGrounding(previous, items []liveAnalysisItem, assignments []treeAssignment, scope liveEvidenceScope, mc *meetingContext, stage string, splitFragment bool, stats *liveAnalysisTreeMergeStats) ([]liveAnalysisItem, []treeAssignment) {
	if len(items) == 0 {
		return items, assignments
	}
	catalog := buildGroundingContextCatalog(mc, previous)
	kept := make([]liveAnalysisItem, 0, len(items))
	acceptedRefs := make(map[string]struct{}, len(items)*2)
	for _, item := range items {
		sourceRef := modelItemReference(item)
		evaluation, rewritten := evaluateItemGrounding(item, scope, catalog, stage, splitFragment || item.semanticSplitFragment)
		if evaluation.Decision == "accepted" || evaluation.Decision == "rewritten" {
			rewritten.GroundingDecision = evaluation.Decision
			rewritten.GroundingConfidence = evaluation.Confidence
			rewritten.GroundingSourceTypes = append([]groundingSourceType(nil), evaluation.SourceTypes...)
			rewritten.GroundingUnsupportedAtomHashes = append([]string(nil), evaluation.UnsupportedAtomHashes...)
			kept = append(kept, rewritten)
			for _, ref := range []string{sourceRef, item.ID, item.ClientKey, rewritten.ID, rewritten.ClientKey} {
				if ref != "" {
					acceptedRefs[canonicalReferenceKey(ref)] = struct{}{}
				}
			}
		}
		recordGroundingDecision(stats, evaluation)
		if evaluation.Decision != "accepted" && evaluation.Decision != "rewritten" &&
			(isDiscourseOnlyItem(item.Title, item.Body) || groundingEvidenceIsDiscourseOnly(item, scope)) &&
			stats != nil {
			stats.DiscourseOnlyItemsRejected++
			stats.LowInformationItemsRejected++
		}
	}
	filteredAssignments := assignments[:0]
	for _, assignment := range assignments {
		if _, ok := acceptedRefs[canonicalReferenceKey(assignment.nodeID())]; ok {
			filteredAssignments = append(filteredAssignments, assignment)
		}
	}
	return kept, filteredAssignments
}

func groundingEvidenceIsDiscourseOnly(item liveAnalysisItem, scope liveEvidenceScope) bool {
	if len(item.EvidenceSequenceNos) == 0 {
		return false
	}
	for _, sequenceNo := range item.EvidenceSequenceNos {
		role := semanticEvidenceRole(item.EvidenceRoles, sequenceNo)
		if role == "" {
			role = scope.EvidenceRoles[sequenceNo]
		}
		if role != liveEvidenceDiscourseOnly {
			return false
		}
	}
	return true
}

func evaluateItemGrounding(item liveAnalysisItem, scope liveEvidenceScope, catalog groundingContextCatalog, stage string, splitFragment bool) (itemGroundingDecision, liveAnalysisItem) {
	decision := itemGroundingDecision{
		Stage: stage, ItemID: item.ID, ModelItemID: firstNonEmptyTrimmed(item.modelReference, modelItemReference(item)),
		SourceItemID:      firstNonEmptyTrimmed(item.modelReference, modelItemReference(item)),
		EvidenceSequences: append([]int64(nil), item.EvidenceSequenceNos...),
		Decision:          "rejected", Reason: "semantic_grounding_failed", SplitFragment: splitFragment,
	}
	if !item.evidenceSpecified && len(item.EvidenceSnippets) == 0 {
		// Strict v18 model output always supplies both evidence fields. This
		// compatibility branch is limited to older persisted/json_object
		// payloads that could not express per-item semantic grounding.
		decision.SubjectGrounded = true
		decision.PredicateGrounded = true
		decision.EntityGrounded = true
		decision.QualifierGrounded = true
		decision.Decision = "accepted"
		decision.Reason = "legacy_item_without_grounding_fields"
		decision.Confidence = 0.4
		decision.SourceTypes = []groundingSourceType{groundingSourceFinalTranscript}
		return decision, item
	}
	if len(scope.TranscriptText) == 0 {
		// Production live/final paths always carry transcript text. A few
		// legacy in-package callers construct a structural-only scope; retain
		// their historical behavior without pretending that it was validated.
		decision.SubjectGrounded = true
		decision.PredicateGrounded = true
		decision.EntityGrounded = true
		decision.QualifierGrounded = true
		decision.Decision = "accepted"
		decision.Reason = "semantic_scope_unavailable_legacy_compatibility"
		decision.Confidence = 0.5
		decision.SourceTypes = []groundingSourceType{groundingSourceFinalTranscript}
		return decision, item
	}

	evidenceText, sourceTypes, structuralReason := groundingEvidenceText(item, scope)
	decision.SourceTypes = sourceTypes
	if structuralReason != "" {
		decision.Reason = structuralReason
		classifyContextOnlyGrounding(&decision, item.Title+" "+item.Body, nil, catalog)
		return decision, item
	}

	itemText := groundingItemText(item)
	validSnippets, invalidSnippets := validateGroundingSnippets(item, scope, itemText)
	if len(item.EvidenceSnippets) > 0 && len(validSnippets) == 0 {
		decision.Reason = "evidence_snippet_not_found_or_not_supporting_item"
		decision.UnsupportedAtomCount = invalidSnippets
		decision.UnsupportedAtomCategories = []string{"evidence_snippet"}
		decision.UnsupportedAtomHashes = []string{groundingAtomHash("evidence_snippet", itemText)}
		classifyContextOnlyGrounding(&decision, itemText, nil, catalog)
		return decision, item
	}

	unsupported := unsupportedGroundingAtoms(itemText, evidenceText)
	itemPredicates := groundingPredicateNames(itemText)
	evidencePredicates := groundingPredicateSet(evidenceText)
	similarity := semanticItemSimilarity(itemText, evidenceText)
	for _, predicate := range itemPredicates {
		if _, ok := evidencePredicates[predicate]; !ok &&
			(groundingPredicateRequiresDirectSupport(predicate) || similarity < 0.45) {
			unsupported = append(unsupported, groundingAtom{Category: "predicate", Value: predicate})
		}
	}
	unsupported = uniqueGroundingAtoms(unsupported)

	decision.SubjectGrounded = groundingSubjectSupported(itemText, evidenceText)
	decision.PredicateGrounded = groundingPredicatesSupported(itemPredicates, evidencePredicates, similarity)
	decision.EntityGrounded = !hasUnsupportedGroundingCategory(unsupported, "person", "location", "number", "identifier", "owner", "deadline")
	decision.QualifierGrounded = !hasUnsupportedGroundingCategory(unsupported, "cause", "predicate")
	decision.UnsupportedAtomCount = len(unsupported) + invalidSnippets
	for _, atom := range unsupported {
		decision.UnsupportedAtomCategories = append(decision.UnsupportedAtomCategories, atom.Category)
		decision.UnsupportedAtomHashes = append(decision.UnsupportedAtomHashes, groundingAtomHash(atom.Category, atom.Value))
	}
	decision.UnsupportedAtomCategories = uniqueSortedStrings(decision.UnsupportedAtomCategories)
	decision.UnsupportedAtomHashes = uniqueSortedStrings(decision.UnsupportedAtomHashes)
	classifyContextOnlyGrounding(&decision, itemText, unsupported, catalog)

	grounded := decision.SubjectGrounded && decision.PredicateGrounded &&
		decision.EntityGrounded && decision.QualifierGrounded && decision.UnsupportedAtomCount == 0
	recapOnly := len(sourceTypes) == 1 && sourceTypes[0] == groundingSourceRecapTranscript
	if recapOnly && len(validSnippets) == 0 && similarity < 0.55 {
		grounded = false
		decision.Reason = "recap_new_item_requires_direct_grounding"
	}
	if grounded {
		decision.Decision = "accepted"
		decision.Reason = "central_proposition_supported_by_final_transcript"
		decision.Confidence = 0.90
		if len(validSnippets) > 0 {
			decision.Confidence = 0.98
			decision.Reason = "verified_evidence_snippet_supports_proposition"
			item.EvidenceSnippets = validSnippets
		}
		return decision, item
	}

	if rewritten, sequenceNo, ok := rewriteItemToGroundedEvidence(item, scope); ok {
		recheck, safe := evaluateItemGroundingWithoutRewrite(rewritten, scope, stage, splitFragment)
		if recheck.Decision == "accepted" {
			decision.Decision = "rewritten"
			decision.Reason = "unsupported_detail_removed_to_final_transcript"
			decision.Confidence = 0.91
			decision.SubjectGrounded = true
			decision.PredicateGrounded = true
			decision.EntityGrounded = true
			decision.QualifierGrounded = true
			decision.EvidenceSequences = []int64{sequenceNo}
			rewritten.GroundingDecision = decision.Decision
			rewritten.GroundingConfidence = decision.Confidence
			rewritten.GroundingSourceTypes = append([]groundingSourceType(nil), decision.SourceTypes...)
			rewritten.GroundingUnsupportedAtomHashes = append([]string(nil), decision.UnsupportedAtomHashes...)
			return decision, safe
		}
	}

	if decision.ContextOnlyAtomCount > 0 && !decision.SubjectGrounded {
		decision.Decision = "candidate_only"
		decision.Reason = "content_supported_only_by_non_transcript_context"
		decision.Confidence = 0.97
	} else if decision.SubjectGrounded || decision.PredicateGrounded {
		decision.Decision = "tentative"
		decision.Reason = "partially_grounded_proposition_not_visible"
		decision.Confidence = 0.65
	} else {
		decision.Decision = "rejected"
		decision.Reason = "central_proposition_not_supported_by_final_transcript"
		decision.Confidence = 0.96
	}
	return decision, item
}

func evaluateItemGroundingWithoutRewrite(item liveAnalysisItem, scope liveEvidenceScope, stage string, splitFragment bool) (itemGroundingDecision, liveAnalysisItem) {
	decision := itemGroundingDecision{
		Stage: stage, ItemID: item.ID, ModelItemID: firstNonEmptyTrimmed(item.modelReference, modelItemReference(item)),
		SourceItemID:      firstNonEmptyTrimmed(item.modelReference, modelItemReference(item)),
		EvidenceSequences: append([]int64(nil), item.EvidenceSequenceNos...),
		Decision:          "rejected", Reason: "rewritten_proposition_not_grounded", SplitFragment: splitFragment,
	}
	evidenceText, sourceTypes, structuralReason := groundingEvidenceText(item, scope)
	decision.SourceTypes = sourceTypes
	if structuralReason != "" {
		decision.Reason = structuralReason
		return decision, item
	}
	itemText := groundingItemText(item)
	unsupported := unsupportedGroundingAtoms(itemText, evidenceText)
	itemPredicates, evidencePredicates := groundingPredicateNames(itemText), groundingPredicateSet(evidenceText)
	similarity := semanticItemSimilarity(itemText, evidenceText)
	for _, predicate := range itemPredicates {
		if _, ok := evidencePredicates[predicate]; !ok &&
			(groundingPredicateRequiresDirectSupport(predicate) || similarity < 0.45) {
			unsupported = append(unsupported, groundingAtom{Category: "predicate", Value: predicate})
		}
	}
	unsupported = uniqueGroundingAtoms(unsupported)
	decision.SubjectGrounded = groundingSubjectSupported(itemText, evidenceText)
	decision.PredicateGrounded = groundingPredicatesSupported(itemPredicates, evidencePredicates, similarity)
	decision.EntityGrounded = !hasUnsupportedGroundingCategory(unsupported, "person", "location", "number", "identifier", "owner", "deadline")
	decision.QualifierGrounded = !hasUnsupportedGroundingCategory(unsupported, "cause", "predicate")
	if decision.SubjectGrounded && decision.PredicateGrounded && decision.EntityGrounded && decision.QualifierGrounded && len(unsupported) == 0 {
		decision.Decision = "accepted"
		decision.Reason = "rewritten_proposition_supported_by_final_transcript"
		decision.Confidence = 0.98
	}
	return decision, item
}

func groundingEvidenceText(item liveAnalysisItem, scope liveEvidenceScope) (string, []groundingSourceType, string) {
	if len(item.EvidenceSequenceNos) == 0 {
		return "", nil, "missing_final_transcript_evidence"
	}
	var texts []string
	sourceSet := make(map[groundingSourceType]struct{})
	seen := make(map[int64]struct{}, len(item.EvidenceSequenceNos))
	for _, sequenceNo := range item.EvidenceSequenceNos {
		if _, duplicate := seen[sequenceNo]; duplicate {
			continue
		}
		seen[sequenceNo] = struct{}{}
		if sequenceNo <= 0 || sequenceNo > scope.CoveredThrough {
			return "", nil, "future_or_out_of_range_evidence"
		}
		if segment, exists := scope.Segments[sequenceNo]; exists && !segment.IsFinal {
			return "", []groundingSourceType{groundingSourcePartialTranscript}, "partial_transcript_not_primary_evidence"
		}
		if _, allowed := scope.Allowed[sequenceNo]; !allowed {
			return "", nil, "missing_or_non_final_evidence"
		}
		text := strings.TrimSpace(scope.TranscriptText[sequenceNo])
		if text == "" {
			return "", nil, "missing_final_transcript_text"
		}
		texts = append(texts, text)
		role := semanticEvidenceRole(item.EvidenceRoles, sequenceNo)
		if role == "" {
			role = scope.EvidenceRoles[sequenceNo]
		}
		if role == liveEvidenceReferenceRecap {
			sourceSet[groundingSourceRecapTranscript] = struct{}{}
		} else {
			sourceSet[groundingSourceFinalTranscript] = struct{}{}
		}
	}
	sources := make([]groundingSourceType, 0, len(sourceSet))
	for source := range sourceSet {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i] < sources[j] })
	return strings.Join(texts, "。"), sources, ""
}

func validateGroundingSnippets(item liveAnalysisItem, scope liveEvidenceScope, itemText string) ([]string, int) {
	if len(item.EvidenceSnippets) == 0 {
		return nil, 0
	}
	var valid []string
	invalid := 0
	for _, snippet := range item.EvidenceSnippets {
		snippet = strings.TrimSpace(snippet)
		if snippet == "" {
			invalid++
			continue
		}
		normalizedSnippet := normalizeGroundingText(snippet)
		found := false
		for _, sequenceNo := range item.EvidenceSequenceNos {
			evidence := normalizeGroundingText(scope.TranscriptText[sequenceNo])
			if normalizedSnippet != "" && strings.Contains(evidence, normalizedSnippet) {
				found = true
				break
			}
		}
		if !found || (!groundingSubjectSupported(itemText, snippet) && semanticItemSimilarity(itemText, snippet) < 0.12) {
			invalid++
			continue
		}
		valid = append(valid, snippet)
	}
	return uniqueSortedStrings(valid), invalid
}

func groundingItemText(item liveAnalysisItem) string {
	title, body := strings.TrimSpace(item.Title), strings.TrimSpace(item.Body)
	if normalizeGroundingText(title) == normalizeGroundingText(body) {
		return title
	}
	return strings.TrimSpace(title + "。" + body)
}

func unsupportedGroundingAtoms(itemText, evidenceText string) []groundingAtom {
	itemAtoms := extractGroundingAtoms(itemText)
	evidenceNormalized := normalizeGroundingText(evidenceText)
	var unsupported []groundingAtom
	for _, atom := range itemAtoms {
		if !strings.Contains(evidenceNormalized, normalizeGroundingText(atom.Value)) {
			unsupported = append(unsupported, atom)
		}
	}
	if groundingCausePattern.MatchString(itemText) && !groundingCausePattern.MatchString(evidenceText) {
		unsupported = append(unsupported, groundingAtom{Category: "cause", Value: "causal_semantics"})
	}
	if groundingOwnerGenericPattern.MatchString(itemText) && !groundingOwnerGenericPattern.MatchString(evidenceText) &&
		len(groundingPersonPattern.FindAllString(itemText, -1)) == 0 {
		unsupported = append(unsupported, groundingAtom{Category: "owner", Value: "generic_owner"})
	}
	if kindDateMentionPattern.MatchString(itemText) && !kindDateMentionPattern.MatchString(evidenceText) &&
		len(groundingDeadlinePattern.FindAllString(itemText, -1)) == 0 {
		unsupported = append(unsupported, groundingAtom{Category: "deadline", Value: "deadline_semantics"})
	}
	return uniqueGroundingAtoms(unsupported)
}

func extractGroundingAtoms(text string) []groundingAtom {
	var atoms []groundingAtom
	appendMatches := func(category string, pattern *regexp.Regexp) {
		for _, value := range pattern.FindAllString(text, -1) {
			atoms = append(atoms, groundingAtom{Category: category, Value: value})
		}
	}
	appendMatches("identifier", groundingLatinIdentifierPattern)
	appendMatches("identifier", groundingUpperTokenPattern)
	appendMatches("person", groundingPersonPattern)
	appendMatches("location", groundingLocationPattern)
	appendMatches("deadline", groundingDeadlinePattern)
	appendMatches("number", groundingNumberWithUnitPattern)
	appendMatches("number", groundingNumberPattern)
	return uniqueGroundingAtoms(atoms)
}

func groundingPredicateNames(text string) []string {
	var predicates []string
	for _, candidate := range groundingPredicatePatterns {
		if candidate.Pattern.MatchString(text) {
			predicates = append(predicates, candidate.Name)
		}
	}
	return uniqueSortedStrings(predicates)
}

func groundingPredicatesSupported(itemPredicates []string, evidencePredicates map[string]struct{}, similarity float64) bool {
	if len(itemPredicates) == 0 {
		return similarity >= 0.12
	}
	for _, predicate := range itemPredicates {
		if _, ok := evidencePredicates[predicate]; !ok {
			if groundingPredicateRequiresDirectSupport(predicate) || similarity < 0.45 {
				return false
			}
		}
	}
	return true
}

func groundingPredicateRequiresDirectSupport(predicate string) bool {
	switch predicate {
	case "recovery", "cause", "replacement", "assignment", "decision", "expiry":
		return true
	default:
		return false
	}
}

func groundingPredicateSet(text string) map[string]struct{} {
	predicates := make(map[string]struct{})
	for _, candidate := range groundingPredicatePatterns {
		if candidate.Pattern.MatchString(text) {
			predicates[candidate.Name] = struct{}{}
		}
	}
	return predicates
}

func groundingSubjectSupported(itemText, evidenceText string) bool {
	itemChunks := groundingSubjectChunks(itemText)
	evidenceChunks := groundingSubjectChunks(evidenceText)
	for _, left := range itemChunks {
		for _, right := range evidenceChunks {
			if left == right || (utf8.RuneCountInString(left) >= 3 && strings.Contains(right, left)) ||
				(utf8.RuneCountInString(right) >= 3 && strings.Contains(left, right)) {
				return true
			}
		}
	}
	return sharedTreeAuditSubjectTerm(itemText, evidenceText)
}

func groundingSubjectChunks(text string) []string {
	var chunks []string
	for _, raw := range groundingSubjectChunkPattern.FindAllString(text, -1) {
		value := normalizeGroundingText(raw)
		if utf8.RuneCountInString(value) < 2 {
			continue
		}
		if _, ignored := groundingIgnoredSubjectChunks[value]; ignored {
			continue
		}
		if groundingNumberPattern.MatchString(value) && len(strings.Trim(value, "0123456789")) == 0 {
			continue
		}
		chunks = append(chunks, value)
	}
	return uniqueSortedStrings(chunks)
}

func rewriteItemToGroundedEvidence(item liveAnalysisItem, scope liveEvidenceScope) (liveAnalysisItem, int64, bool) {
	itemText := groundingItemText(item)
	if body := strings.TrimSpace(item.Body); body != "" &&
		normalizeGroundingText(body) != normalizeGroundingText(item.Title) &&
		groundingSubjectSupported(item.Title, body) {
		probe := item
		probe.Title = body
		probe.Body = body
		if decision, _ := evaluateItemGroundingWithoutRewrite(probe, scope, "grounding_body_contraction", item.semanticSplitFragment); decision.Decision == "accepted" {
			rewritten := item
			rewritten.Title = semanticallyCompleteItemLabelOrOriginal(body, item.Kind)
			return rewritten, maxEvidenceSequence(rewritten), true
		}
	}
	if title := strings.TrimSpace(item.Title); title != "" &&
		normalizeGroundingText(title) != normalizeGroundingText(item.Body) &&
		groundingSubjectSupported(item.Body, title) {
		probe := item
		probe.Body = title
		if decision, _ := evaluateItemGroundingWithoutRewrite(probe, scope, "grounding_title_contraction", item.semanticSplitFragment); decision.Decision == "accepted" {
			rewritten := item
			rewritten.Body = truncateRunes(title, liveAnalysisTreeDescriptionMaxRunes)
			return rewritten, maxEvidenceSequence(rewritten), true
		}
	}
	bestScore := 0.0
	bestText := ""
	var bestSequence int64
	for _, sequenceNo := range item.EvidenceSequenceNos {
		text := strings.TrimSpace(scope.TranscriptText[sequenceNo])
		if text == "" {
			continue
		}
		for _, sentence := range groundingRewriteCandidates(text) {
			sentence = strings.TrimSpace(sentence)
			if utf8.RuneCountInString(sentence) < 6 || classifyDiscourseAct(sentence) != discourseContent {
				continue
			}
			score := semanticItemSimilarity(itemText, sentence)
			if groundingSubjectSupported(itemText, sentence) {
				score += 0.20
			}
			itemPredicates, evidencePredicates := groundingPredicateNames(itemText), groundingPredicateSet(sentence)
			for _, predicate := range itemPredicates {
				if _, ok := evidencePredicates[predicate]; ok {
					score += 0.05
				}
			}
			if score > bestScore {
				bestScore, bestText, bestSequence = score, sentence, sequenceNo
			}
		}
	}
	if bestText == "" || bestScore < 0.28 || !liveItemHasSpecificSubject(bestText) {
		return item, 0, false
	}
	rewritten := item
	if groundingRewriteCanPreserveCompleteTitle(item, bestText) {
		// The model title can be the concise canonical proposition while the
		// cited transcript sentence is the authoritative evidence/body. Do not
		// replace a complete, concrete title merely because an unsupported
		// qualifier elsewhere in the item required contraction.
		rewritten.Title = strings.TrimSpace(item.Title)
	} else {
		rewritten.Title = semanticallyCompleteItemLabelOrOriginal(bestText, item.Kind)
	}
	rewritten.Body = truncateRunes(bestText, liveAnalysisTreeDescriptionMaxRunes)
	rewritten.EvidenceSequenceNos = []int64{bestSequence}
	rewritten.EvidenceSnippets = []string{bestText}
	rewritten.EvidenceRoles = semanticFragmentEvidenceRoles(item.EvidenceRoles, bestSequence)
	rewritten.evidenceSpecified = true
	return rewritten, bestSequence, true
}

func groundingRewriteCanPreserveCompleteTitle(item liveAnalysisItem, evidence string) bool {
	title := strings.TrimSpace(item.Title)
	if title == "" || incompleteItemLabelEnding(item) != "" {
		return false
	}
	titleOnly := liveAnalysisItem{Kind: item.Kind, Title: title, Body: title}
	if liveItemTextNeedsReferent(titleOnly) ||
		!groundingSubjectSupported(title, evidence) {
		return false
	}
	evidencePredicates := groundingPredicateSet(evidence)
	for _, predicate := range groundingPredicateNames(title) {
		if _, supported := evidencePredicates[predicate]; !supported {
			return false
		}
	}
	return semanticItemSimilarity(title, evidence) >= 0.08
}

func groundingRewriteCandidates(text string) []string {
	sentences := groundingSentenceEndPattern.Split(text, -1)
	candidates := make([]string, 0, len(sentences)*2)
	seen := map[string]struct{}{}
	for _, sentence := range sentences {
		sentence = strings.TrimSpace(sentence)
		if sentence == "" {
			continue
		}
		key := normalizeGroundingText(sentence)
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			candidates = append(candidates, sentence)
		}
		for _, clause := range groundingSentencePattern.Split(sentence, -1) {
			clause = strings.TrimSpace(clause)
			key = normalizeGroundingText(clause)
			if clause != "" {
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				candidates = append(candidates, clause)
			}
		}
	}
	return candidates
}

func semanticFragmentGroundingSequenceNo(fragment string, evidenceSequenceNos []int64, scope liveEvidenceScope) int64 {
	bestScore := 0.0
	var bestSequence int64
	for _, sequenceNo := range evidenceSequenceNos {
		evidence := strings.TrimSpace(scope.TranscriptText[sequenceNo])
		if evidence == "" {
			continue
		}
		probe := liveAnalysisItem{
			Title: fragment, Body: fragment, EvidenceSequenceNos: []int64{sequenceNo},
			evidenceSpecified: true,
		}
		decision, _ := evaluateItemGroundingWithoutRewrite(probe, scope, "semantic_split_sequence", true)
		if decision.Decision != "accepted" {
			continue
		}
		score := semanticItemSimilarity(fragment, evidence)
		if score > bestScore {
			bestScore, bestSequence = score, sequenceNo
		}
	}
	return bestSequence
}

func groundingSnippetsForFragment(snippets []string, fragment string, sequenceNo int64, scope liveEvidenceScope) []string {
	var kept []string
	for _, snippet := range snippets {
		normalized := normalizeGroundingText(snippet)
		if normalized == "" || !strings.Contains(normalizeGroundingText(scope.TranscriptText[sequenceNo]), normalized) {
			continue
		}
		if groundingSubjectSupported(fragment, snippet) || semanticItemSimilarity(fragment, snippet) >= 0.12 {
			kept = append(kept, strings.TrimSpace(snippet))
		}
	}
	return uniqueSortedStrings(kept)
}

func classifyContextOnlyGrounding(decision *itemGroundingDecision, itemText string, unsupported []groundingAtom, catalog groundingContextCatalog) {
	if decision == nil {
		return
	}
	sourceSet := make(map[groundingSourceType]struct{})
	for _, source := range decision.SourceTypes {
		sourceSet[source] = struct{}{}
	}
	contextOnly := 0
	for _, atom := range unsupported {
		found := false
		needle := normalizeGroundingText(atom.Value)
		for _, entry := range catalog {
			if needle != "" && strings.Contains(normalizeGroundingText(entry.Text), needle) {
				sourceSet[entry.Source] = struct{}{}
				found = true
			}
		}
		if found {
			contextOnly++
		}
	}
	if len(unsupported) == 0 {
		for _, entry := range catalog {
			if semanticItemSimilarity(itemText, entry.Text) >= 0.55 {
				sourceSet[entry.Source] = struct{}{}
				contextOnly++
			}
		}
	}
	if contextOnly == 0 && !decision.SubjectGrounded && !decision.PredicateGrounded {
		sourceSet[groundingSourceModelInference] = struct{}{}
	}
	decision.ContextOnlyAtomCount = contextOnly
	decision.FutureInformationDetected = contextOnly > 0
	decision.SourceTypes = decision.SourceTypes[:0]
	for source := range sourceSet {
		decision.SourceTypes = append(decision.SourceTypes, source)
	}
	sort.Slice(decision.SourceTypes, func(i, j int) bool { return decision.SourceTypes[i] < decision.SourceTypes[j] })
}

func recordGroundingDecision(stats *liveAnalysisTreeMergeStats, decision itemGroundingDecision) {
	if stats == nil {
		return
	}
	stats.GroundingDecisions = append(stats.GroundingDecisions, decision)
	stats.GroundingUnsupportedAtoms += decision.UnsupportedAtomCount
	stats.GroundingContextOnlyAtoms += decision.ContextOnlyAtomCount
	if decision.FutureInformationDetected {
		stats.GroundingFutureLeaksPrevented++
	}
	switch decision.Decision {
	case "accepted":
		stats.GroundingAccepted++
	case "rewritten":
		stats.GroundingRewritten++
	case "tentative":
		stats.GroundingTentative++
	case "candidate_only":
		stats.GroundingCandidateOnly++
	default:
		stats.GroundingRejected++
	}
}

func stampItemGroundingLifecycle(items, previous []liveAnalysisItem, coveredThrough int64) {
	previousByID := make(map[string]liveAnalysisItem, len(previous))
	for _, item := range previous {
		previousByID[item.ID] = item
	}
	for index := range items {
		if prior, ok := previousByID[items[index].ID]; ok {
			if items[index].CreatedThroughSequenceNo == 0 {
				items[index].CreatedThroughSequenceNo = prior.CreatedThroughSequenceNo
			}
			if items[index].InitialEvidenceMaxSequenceNo == 0 {
				items[index].InitialEvidenceMaxSequenceNo = prior.InitialEvidenceMaxSequenceNo
			}
			continue
		}
		if items[index].CreatedThroughSequenceNo == 0 {
			items[index].CreatedThroughSequenceNo = coveredThrough
		}
		if items[index].InitialEvidenceMaxSequenceNo == 0 {
			items[index].InitialEvidenceMaxSequenceNo = maxEvidenceSequence(items[index])
		}
	}
}

func inheritItemGroundingLifecycle(previous, diff []liveAnalysisItem) {
	previousByID := make(map[string]liveAnalysisItem, len(previous))
	for _, item := range previous {
		previousByID[item.ID] = item
	}
	for index := range diff {
		reference := modelItemReference(diff[index])
		prior, ok := previousByID[reference]
		if !ok {
			continue
		}
		diff[index].CreatedThroughSequenceNo = prior.CreatedThroughSequenceNo
		diff[index].InitialEvidenceMaxSequenceNo = prior.InitialEvidenceMaxSequenceNo
	}
}

func normalizeGroundingText(value string) string {
	var b strings.Builder
	runes := []rune(strings.ToLower(value))
	for index := 0; index < len(runes); {
		if isJapaneseNumericRune(runes[index]) {
			end := index
			for end < len(runes) && isJapaneseNumericRune(runes[end]) {
				end++
			}
			if end < len(runes) && isGroundingNumericUnit(runes[end]) {
				if number, ok := parseJapaneseNumber(runes[index:end]); ok {
					b.WriteString(strconv.Itoa(number))
					index = end
					continue
				}
			}
		}
		r := runes[index]
		if r >= '０' && r <= '９' {
			r = '0' + (r - '０')
		} else if r >= 'ａ' && r <= 'ｚ' {
			r = 'a' + (r - 'ａ')
		} else if r >= 'Ａ' && r <= 'Ｚ' {
			r = 'a' + (r - 'Ａ')
		}
		switch {
		case unicode.IsSpace(r):
		case strings.ContainsRune("、。,.!?！？:：;；()（）[]「」『』・-_〜~／/", r):
		default:
			b.WriteRune(r)
		}
		index++
	}
	return b.String()
}

func isJapaneseNumericRune(r rune) bool {
	return strings.ContainsRune("〇零一二三四五六七八九十百千", r)
}

func isGroundingNumericUnit(r rune) bool {
	return strings.ContainsRune("年月日時分秒階棟室週人件台回", r)
}

func parseJapaneseNumber(runes []rune) (int, bool) {
	digit := func(r rune) (int, bool) {
		switch r {
		case '〇', '零':
			return 0, true
		case '一':
			return 1, true
		case '二':
			return 2, true
		case '三':
			return 3, true
		case '四':
			return 4, true
		case '五':
			return 5, true
		case '六':
			return 6, true
		case '七':
			return 7, true
		case '八':
			return 8, true
		case '九':
			return 9, true
		}
		return 0, false
	}
	total, current := 0, 0
	hasUnit := false
	for _, r := range runes {
		if value, ok := digit(r); ok {
			current = value
			if !hasUnit && len(runes) > 1 {
				total = total*10 + value
				current = 0
			}
			continue
		}
		multiplier := 0
		switch r {
		case '十':
			multiplier = 10
		case '百':
			multiplier = 100
		case '千':
			multiplier = 1000
		default:
			return 0, false
		}
		hasUnit = true
		if current == 0 {
			current = 1
		}
		total += current * multiplier
		current = 0
	}
	return total + current, true
}

func uniqueGroundingAtoms(values []groundingAtom) []groundingAtom {
	seen := make(map[string]struct{}, len(values))
	result := make([]groundingAtom, 0, len(values))
	for _, value := range values {
		key := value.Category + "\x00" + normalizeGroundingText(value.Value)
		if key == "\x00" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func hasUnsupportedGroundingCategory(values []groundingAtom, categories ...string) bool {
	wanted := make(map[string]struct{}, len(categories))
	for _, category := range categories {
		wanted[category] = struct{}{}
	}
	for _, value := range values {
		if _, ok := wanted[value.Category]; ok {
			return true
		}
	}
	return false
}

func groundingAtomHash(category, value string) string {
	sum := sha256.Sum256([]byte(category + "\x00" + normalizeGroundingText(value)))
	return category + ":" + hex.EncodeToString(sum[:6])
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func formatGroundingSourceTypes(values []groundingSourceType) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, string(value))
	}
	return fmt.Sprintf("%v", parts)
}
