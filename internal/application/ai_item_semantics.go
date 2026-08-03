package application

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var correctionSpecificTokenPattern = regexp.MustCompile(`[\p{Katakana}ー]{4,}|[A-Za-z][A-Za-z0-9_-]{2,}`)
var strongCorrectionLeadPattern = regexp.MustCompile(`^(?:いえ[、,]?)?(?:正確には|厳密には|言い直すと|訂正(?:します|すると)?|先ほどの(?:説明|発言|内容))`)
var correctionPortSubjectPattern = regexp.MustCompile(`(?:スイッチ|ポート|インターフェース|interface|switch|port)`)
var correctionAccessModePattern = regexp.MustCompile(`(?:アクセスポート|アクセス(?:モード|設定)|access\s*port|portMode\s*=\s*access)`)
var correctionTrunkModePattern = regexp.MustCompile(`(?:トランク(?:ポート|モード|設定)?|trunk\s*port|portMode\s*=\s*trunk)`)
var correctionPortConfigurationPattern = regexp.MustCompile(`(?:トランク|アクセス|VLAN|許可(?:一覧|設定)?|ポート(?:モード|設定)?)`)
var correctionAdditiveScopePattern = regexp.MustCompile(`(?:でも|にも|加えて|あわせて|一部でも|別の|さらに)`)

type crossKindUpdateDecision struct {
	ExistingItemID string
	ModelItemID    string
	NewClientKey   string
	OldKind        string
	NewKind        string
	Decision       string
	Reason         string
	OldEvidence    []int64
	NewEvidence    []int64
	SubjectMatch   bool
	PredicateMatch bool
	ObjectMatch    bool
	QualifierMatch bool
	Correction     bool
	Similarity     float64
}

type propositionUpdateCompatibility struct {
	Compatible     bool
	Reason         string
	SubjectMatch   bool
	PredicateMatch bool
	ObjectMatch    bool
	QualifierMatch bool
	Correction     bool
	Similarity     float64
}

var propositionPredicateFamilies = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{"incident_state", regexp.MustCompile(`(?:発生|障害|停止|遅延|接続(?:不能|不可|できな)|影響|不安定|異常)`)},
	{"cause_or_configuration", regexp.MustCompile(`(?:原因|理由|要因|設定|構成|漏れ|不足|誤り|不整合|許可)`)},
	{"recovery_or_completion", regexp.MustCompile(`(?:復旧|回復|正常|解消|切り戻|修正(?:済|した|しました)|完了)`)},
	{"future_action", regexp.MustCompile(`(?:作成|策定|準備|確認(?:する|します)|調査(?:する|します)|対応(?:する|します)|更新(?:する|します)|適用|実施|導入|管理)`)},
	{"decision", regexp.MustCompile(`(?:決定|採用|必須|義務|方針|ことにします)`)},
	{"risk", regexp.MustCompile(`(?:可能性|おそれ|懸念|リスク|しかねない)`)},
	{"time_only", regexp.MustCompile(`(?:午前|午後|\d{1,2}時|\d{1,2}分|時刻)`)},
}

// detachCrossKindActionUpdates prevents a model reference from destructively
// changing an Issue into its follow-up Todo (or the reverse), and also
// prevents a same-kind ID from overwriting a later, semantically unrelated
// proposition. A genuinely misclassified persisted item has already passed
// through repairPersistedItemKinds.
func detachCrossKindActionUpdates(
	previous []liveAnalysisItem,
	diff []liveAnalysisItem,
	assignments []treeAssignment,
	scope liveEvidenceScope,
	stats *liveAnalysisTreeMergeStats,
) ([]liveAnalysisItem, []treeAssignment) {
	previousByRef := make(map[string]liveAnalysisItem, len(previous))
	for _, item := range previous {
		if item.ID == "" || item.Inactive || item.MergedIntoID != "" {
			continue
		}
		previousByRef[canonicalReferenceKey(item.ID)] = item
	}
	for index := range diff {
		modelRef := modelItemReference(diff[index])
		existing, ok := previousByRef[canonicalReferenceKey(modelRef)]
		if !ok {
			continue
		}
		reason := ""
		compatibility := propositionUpdateCompatibility{
			Compatible: existing.Kind == diff[index].Kind,
			Reason:     "same_proposition_not_evaluated",
		}
		switch {
		case issueTodoPair(existing.Kind, diff[index].Kind):
			reason = "issue_and_todo_are_distinct_propositions"
		default:
			compatibility = evaluatePropositionUpdateCompatibility(existing, diff[index], scope)
			if !compatibility.Compatible {
				if existing.Kind == diff[index].Kind {
					reason = compatibility.Reason
				} else {
					reason = "cross_kind_" + compatibility.Reason
				}
			}
		}
		if reason == "" {
			if stats != nil {
				stats.CrossKindUpdateDecisions = append(stats.CrossKindUpdateDecisions, crossKindUpdateDecision{
					ExistingItemID: existing.ID, ModelItemID: modelRef,
					OldKind: existing.Kind, NewKind: diff[index].Kind,
					Decision: "accepted", Reason: compatibility.Reason,
					OldEvidence:  append([]int64(nil), existing.EvidenceSequenceNos...),
					NewEvidence:  append([]int64(nil), diff[index].EvidenceSequenceNos...),
					SubjectMatch: compatibility.SubjectMatch, PredicateMatch: compatibility.PredicateMatch,
					ObjectMatch: compatibility.ObjectMatch, QualifierMatch: compatibility.QualifierMatch,
					Correction: compatibility.Correction, Similarity: compatibility.Similarity,
				})
			}
			continue
		}
		suffix := "companion"
		if existing.Kind == diff[index].Kind {
			suffix = "distinct"
		}
		newRef := fmt.Sprintf("%s-%s-%s", modelRef, diff[index].Kind, suffix)
		diff[index].ClientKey = newRef
		diff[index].ID = ""
		diff[index].CreatedThroughSequenceNo = 0
		diff[index].InitialEvidenceMaxSequenceNo = 0
		for assignmentIndex := range assignments {
			if canonicalReferenceKey(assignments[assignmentIndex].nodeID()) != canonicalReferenceKey(modelRef) {
				continue
			}
			assignments[assignmentIndex].NodeID = newRef
			assignments[assignmentIndex].ItemID = ""
			assignments[assignmentIndex].ModelNodeID = modelRef
			assignments[assignmentIndex].ServerSource = "cross_kind_update_detached"
		}
		if stats != nil {
			stats.CrossKindUpdatesDetached++
			if existing.Kind == diff[index].Kind {
				stats.DivergentUpdatesDetached++
			}
			stats.CrossKindUpdateDecisions = append(stats.CrossKindUpdateDecisions, crossKindUpdateDecision{
				ExistingItemID: existing.ID,
				ModelItemID:    modelRef,
				NewClientKey:   newRef,
				OldKind:        existing.Kind,
				NewKind:        diff[index].Kind,
				Decision:       "rejected",
				Reason:         reason,
				OldEvidence:    append([]int64(nil), existing.EvidenceSequenceNos...),
				NewEvidence:    append([]int64(nil), diff[index].EvidenceSequenceNos...),
				SubjectMatch:   compatibility.SubjectMatch,
				PredicateMatch: compatibility.PredicateMatch,
				ObjectMatch:    compatibility.ObjectMatch,
				QualifierMatch: compatibility.QualifierMatch,
				Correction:     compatibility.Correction,
				Similarity:     compatibility.Similarity,
			})
		}
	}
	return diff, assignments
}

func sameKindUpdateDiverges(existing, update liveAnalysisItem, scope liveEvidenceScope) bool {
	return !evaluatePropositionUpdateCompatibility(existing, update, scope).Compatible
}

func evaluatePropositionUpdateCompatibility(existing, update liveAnalysisItem, scope liveEvidenceScope) propositionUpdateCompatibility {
	result := propositionUpdateCompatibility{
		Compatible: true, Reason: "same_or_strengthened_proposition",
		ObjectMatch: true, QualifierMatch: true,
	}
	if existing.Kind == "" || update.Kind == "" ||
		len(existing.EvidenceSequenceNos) == 0 || len(update.EvidenceSequenceNos) == 0 {
		result.Reason = "insufficient_identity_evidence_preserve_existing_behavior"
		return result
	}
	oldMax := maxEvidenceBefore(existing.EvidenceSequenceNos, 1<<62)
	newMin := int64(1<<62 - 1)
	for _, sequenceNo := range update.EvidenceSequenceNos {
		if sequenceNo > 0 && sequenceNo < newMin {
			newMin = sequenceNo
		}
	}
	if oldMax <= 0 || newMin <= oldMax {
		result.Reason = "non_later_update_preserved"
		return result
	}
	for _, sequenceNo := range update.EvidenceSequenceNos {
		if scope.EvidenceRoles[sequenceNo] == liveEvidenceCorrection ||
			discourseCorrectionPattern.MatchString(scope.TranscriptText[sequenceNo]) {
			result.Compatible = false
			result.Correction = true
			result.Reason = "explicit_correction_requires_new_proposition"
			return result
		}
	}
	existingText := itemKindSemanticText(existing, scope)
	updateText := itemKindSemanticText(update, scope)
	result.Similarity = semanticItemSimilarity(existingText, updateText)
	result.SubjectMatch = sharedTreeAuditSubjectTerm(existingText, updateText)
	oldPredicates := propositionPredicateSignature(existing.Title + " " + existing.Body)
	newPredicates := propositionPredicateSignature(update.Title + " " + update.Body)
	result.PredicateMatch = len(oldPredicates) == 0 || len(newPredicates) == 0 ||
		patternMatchIntersects(oldPredicates, newPredicates)
	existingDates := normalizedPatternMatches(kindEventDatePattern, existingText)
	updateDates := normalizedPatternMatches(kindEventDatePattern, updateText)
	if len(existingDates) > 0 && len(updateDates) > 0 &&
		!patternMatchIntersects(existingDates, updateDates) {
		result.QualifierMatch = false
	}
	oldObjects := normalizedPatternMatches(correctionSpecificTokenPattern, existingText)
	newObjects := normalizedPatternMatches(correctionSpecificTokenPattern, updateText)
	if len(oldObjects) > 0 && len(newObjects) > 0 &&
		!patternMatchIntersects(oldObjects, newObjects) {
		result.ObjectMatch = false
	}
	titleSimilarity := semanticItemSimilarity(existing.Title, update.Title)
	switch {
	case !result.QualifierMatch:
		result.Compatible = false
		result.Reason = "proposition_qualifier_incompatible"
	case !result.PredicateMatch && titleSimilarity < 0.55:
		result.Compatible = false
		result.Reason = "proposition_predicate_incompatible"
	case !result.ObjectMatch && result.Similarity < 0.55:
		result.Compatible = false
		result.Reason = "proposition_object_incompatible"
	case result.Similarity >= 0.62 || titleSimilarity >= 0.62:
		result.Reason = "high_semantic_equivalence"
	case result.SubjectMatch && result.PredicateMatch && result.Similarity >= 0.18:
		result.Reason = "subject_predicate_compatible_strengthening"
	default:
		result.Compatible = false
		result.Reason = "proposition_incompatible"
	}
	return result
}

func propositionPredicateSignature(text string) []string {
	result := make([]string, 0, len(propositionPredicateFamilies))
	for _, family := range propositionPredicateFamilies {
		if family.pattern.MatchString(text) {
			result = append(result, family.name)
		}
	}
	return result
}

func issueTodoPair(left, right string) bool {
	return (left == "issue" && right == "todo") ||
		(left == "todo" && right == "issue")
}

type evidenceLocalizationDecision struct {
	ItemID              string
	RetainedSequenceNos []int64
	RemovedSequenceNos  []int64
	Decision            string
	Reason              string
}

// localizeUpdatedItemEvidence removes inherited sequence references that no
// longer support the updated proposition. It runs only for existing items
// updated in this round; new items have already passed the grounding gate.
func localizeUpdatedItemEvidence(
	previous []liveAnalysisItem,
	merged []liveAnalysisItem,
	diff []liveAnalysisItem,
	scope liveEvidenceScope,
	stats *liveAnalysisTreeMergeStats,
) {
	previousIDs := make(map[string]struct{}, len(previous))
	for _, item := range previous {
		if item.ID != "" {
			previousIDs[item.ID] = struct{}{}
		}
	}
	updated := make(map[string]liveAnalysisItem, len(diff))
	for _, item := range diff {
		if _, exists := previousIDs[item.ID]; exists {
			updated[item.ID] = item
		}
	}
	for index := range merged {
		diffItem, exists := updated[merged[index].ID]
		if !exists || len(merged[index].EvidenceSequenceNos) < 2 {
			continue
		}
		retained := make([]int64, 0, len(merged[index].EvidenceSequenceNos))
		removed := make([]int64, 0, len(merged[index].EvidenceSequenceNos))
		for _, sequenceNo := range merged[index].EvidenceSequenceNos {
			evidence := strings.TrimSpace(scope.TranscriptText[sequenceNo])
			if evidence == "" {
				// Compatibility for narrow replay scopes that contain only
				// this round. Production full scopes normally take the
				// semantic branch below.
				retained = append(retained, sequenceNo)
			} else if referenceEvidenceSupportsItem(
				merged[index], evidence, scope.EvidenceRoles[sequenceNo],
			) || sequenceSupportsItemSemantics(merged[index], evidence) ||
				sequenceSuppliesItemReferent(merged[index], sequenceNo, scope) {
				retained = append(retained, sequenceNo)
			} else {
				removed = append(removed, sequenceNo)
			}
		}
		if len(removed) == 0 {
			continue
		}
		// The current diff has already passed validateLiveItemGrounding. Never
		// turn a grounded update into an evidence-less item if the conservative
		// per-sequence gate cannot retain a paraphrased source.
		if len(retained) == 0 {
			for _, sequenceNo := range diffItem.EvidenceSequenceNos {
				if strings.TrimSpace(scope.TranscriptText[sequenceNo]) != "" {
					retained = append(retained, sequenceNo)
				}
			}
			retained = uniqueSortedSequenceNos(sortedSequenceNos(retained))
		}
		if len(retained) == 0 {
			continue
		}
		merged[index].EvidenceSequenceNos = retained
		merged[index].EvidenceSnippets = localizedEvidenceSnippets(
			merged[index].EvidenceSnippets, retained, scope,
		)
		if stats != nil {
			stats.EvidenceReferencesPruned += len(removed)
			stats.EvidenceLocalizationDecisions = append(stats.EvidenceLocalizationDecisions, evidenceLocalizationDecision{
				ItemID:              merged[index].ID,
				RetainedSequenceNos: append([]int64(nil), retained...),
				RemovedSequenceNos:  append([]int64(nil), removed...),
				Decision:            "pruned",
				Reason:              "inherited_evidence_does_not_support_updated_proposition",
			})
		}
	}
}

func localizePersistedItemEvidence(
	items []liveAnalysisItem,
	scope liveEvidenceScope,
	stats *liveAnalysisTreeMergeStats,
) {
	for index := range items {
		if items[index].Inactive || items[index].MergedIntoID != "" ||
			len(items[index].EvidenceSequenceNos) < 2 {
			continue
		}
		retained := make([]int64, 0, len(items[index].EvidenceSequenceNos))
		removed := make([]int64, 0, len(items[index].EvidenceSequenceNos))
		for _, sequenceNo := range items[index].EvidenceSequenceNos {
			evidence := strings.TrimSpace(scope.TranscriptText[sequenceNo])
			if evidence == "" {
				// Finalization can receive a deliberately narrow replay scope.
				// Absence from that scope is not evidence that a previously
				// validated historical reference is wrong.
				retained = append(retained, sequenceNo)
			} else if referenceEvidenceSupportsItem(
				items[index], evidence, scope.EvidenceRoles[sequenceNo],
			) || sequenceSupportsItemSemantics(items[index], evidence) ||
				sequenceSuppliesItemReferent(items[index], sequenceNo, scope) {
				retained = append(retained, sequenceNo)
			} else {
				removed = append(removed, sequenceNo)
			}
		}
		if len(removed) == 0 || len(retained) == 0 {
			continue
		}
		items[index].EvidenceSequenceNos = retained
		items[index].EvidenceSnippets = localizedEvidenceSnippets(
			items[index].EvidenceSnippets, retained, scope,
		)
		if stats != nil {
			stats.EvidenceReferencesPruned += len(removed)
			stats.EvidenceLocalizationDecisions = append(stats.EvidenceLocalizationDecisions, evidenceLocalizationDecision{
				ItemID:              items[index].ID,
				RetainedSequenceNos: append([]int64(nil), retained...),
				RemovedSequenceNos:  append([]int64(nil), removed...),
				Decision:            "pruned",
				Reason:              "persisted_evidence_does_not_support_current_proposition",
			})
		}
	}
}

func sequenceSuppliesItemReferent(item liveAnalysisItem, sequenceNo int64, scope liveEvidenceScope) bool {
	if !containsInt64(item.EvidenceSequenceNos, sequenceNo+1) {
		return false
	}
	antecedent := strings.TrimSpace(scope.TranscriptText[sequenceNo])
	dependent := strings.TrimSpace(scope.TranscriptText[sequenceNo+1])
	if antecedent == "" || dependent == "" {
		return false
	}
	itemText := strings.TrimSpace(item.Title + " " + item.Body)
	if item.Kind == "risk" && itemLabelConditionalWithoutSubjectPattern.MatchString(dependent) &&
		(kindScheduledEventPattern.MatchString(antecedent) || kindFutureEventPattern.MatchString(antecedent)) {
		return sharedTreeAuditSubjectTerm(itemText, antecedent)
	}
	if item.Kind == "issue" && itemLabelDeicticSettingPattern.MatchString(dependent) &&
		itemLabelSettingLeakPattern.MatchString(antecedent) {
		qualifier := itemLabelConcreteQualifierPattern.FindString(antecedent)
		return qualifier != "" && strings.Contains(strings.ToLower(itemText), strings.ToLower(qualifier))
	}
	return false
}

func sortedSequenceNos(values []int64) []int64 {
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted
}

func referenceEvidenceSupportsItem(item liveAnalysisItem, evidence string, role liveEvidenceRole) bool {
	if role != liveEvidenceReferenceRecap {
		return false
	}
	itemText := item.Title + " " + item.Body
	if item.Kind == "todo" {
		features := inferItemSemanticFeatures(item, liveEvidenceScope{})
		if features.OwnerPresent &&
			(!kindOwnerPattern.MatchString(evidence) || !sameOwnerMention(itemText, evidence)) {
			return false
		}
		if features.DeadlinePresent &&
			(!actionDeadlinePresent(evidence) || !sameDeadlineMention(itemText, evidence)) {
			return false
		}
	}
	return sharedTreeAuditSubjectTerm(itemText, evidence) ||
		semanticItemSimilarity(itemText, evidence) >= 0.08
}

func sequenceSupportsItemSemantics(item liveAnalysisItem, evidence string) bool {
	itemText := strings.TrimSpace(item.Title + " " + item.Body)
	if itemText == "" || evidence == "" {
		return false
	}
	itemFeatures := inferItemSemanticFeatures(item, liveEvidenceScope{})
	probe := liveAnalysisItem{Kind: item.Kind, Title: evidence, Body: evidence}
	evidenceFeatures := inferItemSemanticFeatures(probe, liveEvidenceScope{})
	switch item.Kind {
	case "todo":
		if !futureActionIntent(evidence) {
			return false
		}
		if itemFeatures.OwnerPresent &&
			(!kindOwnerPattern.MatchString(evidence) || !sameOwnerMention(itemText, evidence)) {
			return false
		}
		if itemFeatures.DeadlinePresent &&
			(!actionDeadlinePresent(evidence) || !sameDeadlineMention(itemText, evidence)) {
			return false
		}
	case "issue":
		if !evidenceFeatures.CurrentProblemPresent &&
			!kindOpenQuestionPattern.MatchString(evidence) &&
			!evidenceFeatures.CausalHypothesisPresent {
			return false
		}
	case "risk":
		if !(evidenceFeatures.FutureEventPresent &&
			evidenceFeatures.UncertaintyPresent &&
			evidenceFeatures.NegativeImpactPresent) {
			return false
		}
	case "fact":
		if !evidenceFeatures.ConfirmedEvidencePresent &&
			!evidenceFeatures.CompletedActionPresent &&
			!evidenceFeatures.ScheduledEventPresent &&
			evidenceFeatures.TemporalScope != "past" {
			return false
		}
		return (sharedTreeAuditSubjectTerm(itemText, evidence) &&
			semanticItemSimilarity(itemText, evidence) >= 0.12) ||
			semanticItemSimilarity(itemText, evidence) >= 0.18
	}
	return sharedTreeAuditSubjectTerm(itemText, evidence) ||
		semanticItemSimilarity(itemText, evidence) >= 0.10
}

func sameOwnerMention(itemText, evidence string) bool {
	itemOwners := normalizedPatternMatches(kindOwnerPattern, itemText)
	evidenceOwners := normalizedPatternMatches(kindOwnerPattern, evidence)
	return patternMatchIntersects(itemOwners, evidenceOwners)
}

func sameDeadlineMention(itemText, evidence string) bool {
	itemDeadlines := normalizedPatternMatches(kindDeadlineMarkerPattern, itemText)
	evidenceDeadlines := normalizedPatternMatches(kindDeadlineMarkerPattern, evidence)
	return patternMatchIntersects(itemDeadlines, evidenceDeadlines)
}

func normalizedPatternMatches(pattern interface{ FindAllString(string, int) []string }, text string) []string {
	matches := pattern.FindAllString(text, -1)
	normalized := make([]string, 0, len(matches))
	for _, match := range matches {
		trimmed := strings.TrimSpace(match)
		trimmed = strings.TrimSuffix(trimmed, "が")
		trimmed = strings.TrimSuffix(trimmed, "は")
		trimmed = strings.TrimSuffix(trimmed, "に")
		key := canonicalReferenceKey(trimmed)
		if key != "" {
			normalized = append(normalized, key)
		}
	}
	return uniqueNonEmptyIDs(normalized)
}

func patternMatchIntersects(left, right []string) bool {
	for _, leftValue := range left {
		for _, rightValue := range right {
			if leftValue == rightValue {
				return true
			}
		}
	}
	return false
}

func localizedEvidenceSnippets(snippets []string, retained []int64, scope liveEvidenceScope) []string {
	kept := make([]string, 0, len(snippets))
	for _, snippet := range snippets {
		normalized := normalizeGroundingText(snippet)
		if normalized == "" {
			continue
		}
		for _, sequenceNo := range retained {
			if strings.Contains(normalizeGroundingText(scope.TranscriptText[sequenceNo]), normalized) {
				kept = append(kept, strings.TrimSpace(snippet))
				break
			}
		}
	}
	return uniqueSortedStrings(kept)
}

type correctionSupersessionDecision struct {
	CorrectionSequenceNo  int64
	TargetSequenceNo      int64
	SupersededItemID      string
	ReplacementItemID     string
	Similarity            float64
	Decision              string
	Reason                string
	OldTargetSequenceNo   int64
	NewTargetSequenceNo   int64
	RelationChangeAllowed bool
	RelationLocked        bool
	AttemptedNextState    string
	TransitionRejected    bool
}

type correctionRelation struct {
	SourceSequenceNo     int64   `json:"sourceSequenceNo"`
	TargetSequenceNo     int64   `json:"targetSequenceNo,omitempty"`
	TargetItemID         string  `json:"targetItemId,omitempty"`
	ReplacementItemID    string  `json:"replacementItemId,omitempty"`
	Status               string  `json:"status"`
	Confidence           float64 `json:"confidence"`
	Locked               bool    `json:"locked"`
	Origin               string  `json:"origin,omitempty"`
	EstablishedAtVersion int64   `json:"establishedAtVersion,omitempty"`
}

const correctionRelationLockThreshold = 0.35

// repairCorrectionSupersessions retires exactly one contradicted proposition
// for an explicit correction. The relation is persisted in the live payload;
// a locked source/target pair is never re-searched into a different nearby
// item merely because its original target is now inactive.
func repairCorrectionSupersessions(
	state *liveAnalysisPayload,
	scope liveEvidenceScope,
	timeline discourseTimeline,
	treeVersion int64,
	stats *liveAnalysisTreeMergeStats,
) {
	if state == nil || state.Tree == nil {
		return
	}
	sequenceNos := make([]int64, 0)
	seenSequences := make(map[int64]struct{})
	for sequenceNo, text := range scope.TranscriptText {
		if discourseCorrectionPattern.MatchString(text) {
			sequenceNos = append(sequenceNos, sequenceNo)
			seenSequences[sequenceNo] = struct{}{}
		}
	}
	for sequenceNo, role := range timeline.Roles {
		if role == liveEvidenceCorrection {
			if _, exists := seenSequences[sequenceNo]; !exists {
				sequenceNos = append(sequenceNos, sequenceNo)
			}
		}
	}
	sort.Slice(sequenceNos, func(i, j int) bool { return sequenceNos[i] < sequenceNos[j] })
	for _, sequenceNo := range sequenceNos {
		correctionText := strings.TrimSpace(scope.TranscriptText[sequenceNo])
		if correctionText == "" || !discourseCorrectionPattern.MatchString(correctionText) {
			continue
		}
		replacementAt := bestCorrectionReplacement(state.Items, correctionText, sequenceNo, scope)
		replacementID := ""
		if replacementAt >= 0 {
			replacementID = state.Items[replacementAt].ID
		}
		relationAt := correctionRelationIndex(state.CorrectionRelations, sequenceNo)
		if relationAt >= 0 && state.CorrectionRelations[relationAt].Locked {
			relation := &state.CorrectionRelations[relationAt]
			if relation.ReplacementItemID == "" && replacementID != "" {
				relation.ReplacementItemID = replacementID
			}
			candidateAt, candidateConfidence := bestSupersededCorrectionItem(
				state.Items, correctionText, sequenceNo, relation.ReplacementItemID, scope,
			)
			candidateSequenceNo := int64(0)
			if candidateAt >= 0 {
				candidateSequenceNo = maxEvidenceBefore(
					state.Items[candidateAt].EvidenceSequenceNos, sequenceNo,
				)
			}
			if candidateSequenceNo > 0 && candidateSequenceNo != relation.TargetSequenceNo &&
				!manualCorrectionRelation(*relation) && candidateConfidence >= correctionRelationLockThreshold {
				oldTargetSequenceNo := relation.TargetSequenceNo
				relation.TargetSequenceNo = candidateSequenceNo
				relation.TargetItemID = state.Items[candidateAt].ID
				relation.Confidence = candidateConfidence
				relation.Status = "pending"
				if relation.ReplacementItemID != "" {
					relation.Status = "superseded"
				}
				if stats != nil {
					stats.CorrectionDecisions = append(stats.CorrectionDecisions, correctionSupersessionDecision{
						CorrectionSequenceNo: sequenceNo, TargetSequenceNo: candidateSequenceNo,
						SupersededItemID: relation.TargetItemID, ReplacementItemID: relation.ReplacementItemID,
						Similarity: candidateConfidence, Decision: "relation_revalidated",
						Reason:              "explicit_correction_overrode_ai_relation_lock",
						OldTargetSequenceNo: oldTargetSequenceNo, NewTargetSequenceNo: candidateSequenceNo,
						RelationChangeAllowed: true, RelationLocked: true,
					})
				}
			} else if candidateSequenceNo > 0 && candidateSequenceNo != relation.TargetSequenceNo && stats != nil {
				reason := "existing_high_confidence_relation_locked"
				if manualCorrectionRelation(*relation) {
					reason = "manual_correction_relation_protected"
				}
				stats.CorrectionDecisions = append(stats.CorrectionDecisions, correctionSupersessionDecision{
					CorrectionSequenceNo:  sequenceNo,
					TargetSequenceNo:      relation.TargetSequenceNo,
					SupersededItemID:      relation.TargetItemID,
					ReplacementItemID:     relation.ReplacementItemID,
					Similarity:            candidateConfidence,
					Decision:              "relation_change_blocked",
					Reason:                reason,
					OldTargetSequenceNo:   relation.TargetSequenceNo,
					NewTargetSequenceNo:   candidateSequenceNo,
					RelationChangeAllowed: false,
					RelationLocked:        true,
				})
			} else if stats != nil {
				stats.CorrectionDecisions = append(stats.CorrectionDecisions, correctionSupersessionDecision{
					CorrectionSequenceNo:  sequenceNo,
					TargetSequenceNo:      relation.TargetSequenceNo,
					SupersededItemID:      relation.TargetItemID,
					ReplacementItemID:     relation.ReplacementItemID,
					Similarity:            relation.Confidence,
					Decision:              "relation_preserved",
					Reason:                "existing_high_confidence_relation_locked",
					OldTargetSequenceNo:   relation.TargetSequenceNo,
					NewTargetSequenceNo:   relation.TargetSequenceNo,
					RelationChangeAllowed: false,
					RelationLocked:        true,
				})
			}
			applyLockedCorrectionRelation(state, relation, treeVersion, stats)
			continue
		}
		supersededAt, similarity := bestSupersededCorrectionItem(
			state.Items, correctionText, sequenceNo, replacementID, scope,
		)
		if supersededAt < 0 {
			upsertCorrectionRelation(state, correctionRelation{
				SourceSequenceNo: sequenceNo, Status: "pending",
				Confidence: 0, Locked: false, EstablishedAtVersion: treeVersion,
			})
			if stats != nil {
				stats.CorrectionItemsPending++
				stats.CorrectionDecisions = append(stats.CorrectionDecisions, correctionSupersessionDecision{
					CorrectionSequenceNo: sequenceNo, Decision: "pending",
					Reason:                "no_semantically_matching_target",
					RelationChangeAllowed: false,
				})
			}
			continue
		}
		superseded := state.Items[supersededAt]
		targetSequenceNo := maxEvidenceBefore(superseded.EvidenceSequenceNos, sequenceNo)
		locked := similarity >= correctionRelationLockThreshold
		relation := correctionRelation{
			SourceSequenceNo: sequenceNo, TargetSequenceNo: targetSequenceNo,
			TargetItemID: superseded.ID, ReplacementItemID: replacementID,
			Status: "pending", Confidence: similarity, Locked: locked,
			Origin: "explicit_correction", EstablishedAtVersion: treeVersion,
		}
		if !locked {
			upsertCorrectionRelation(state, relation)
			if stats != nil {
				stats.CorrectionItemsPending++
				stats.CorrectionDecisions = append(stats.CorrectionDecisions, correctionSupersessionDecision{
					CorrectionSequenceNo: sequenceNo, TargetSequenceNo: targetSequenceNo,
					SupersededItemID: superseded.ID, ReplacementItemID: replacementID,
					Similarity: similarity, Decision: "pending",
					Reason:                "target_similarity_below_lock_threshold",
					RelationChangeAllowed: false,
				})
			}
			continue
		}
		if replacementAt < 0 {
			upsertCorrectionRelation(state, relation)
			state.Items[supersededAt].InformationStatus = "tentative"
			state.Items[supersededAt].ClassificationStatus = classificationTentative
			state.Items[supersededAt].CandidateInactive = true
			state.Items[supersededAt].SuppressionReason = "correction_pending_replacement"
			removeItemNodesFromTree(state.Tree, map[string]struct{}{state.Items[supersededAt].ID: {}})
			removeSemanticRelationsForItems(state.Tree, map[string]struct{}{state.Items[supersededAt].ID: {}})
			if stats != nil {
				stats.CorrectionItemsPending++
				stats.CorrectionDecisions = append(stats.CorrectionDecisions, correctionSupersessionDecision{
					CorrectionSequenceNo: sequenceNo, TargetSequenceNo: targetSequenceNo,
					SupersededItemID: superseded.ID, Similarity: similarity,
					Decision:       "pending",
					Reason:         "explicit_correction_detected_replacement_not_grounded",
					RelationLocked: true,
				})
			}
			continue
		}
		relation.Status = "superseded"
		upsertCorrectionRelation(state, relation)
		applyLockedCorrectionRelation(state, &state.CorrectionRelations[correctionRelationIndex(state.CorrectionRelations, sequenceNo)], treeVersion, stats)
	}
	pruneEmptyDynamicTopics(state.Tree)
	rebuildTreeAuditEdges(state.Tree)
}

func manualCorrectionRelation(relation correctionRelation) bool {
	origin := strings.ToLower(strings.TrimSpace(relation.Origin))
	return origin == "manual" || origin == "manual_user_edit" || origin == "user"
}

func correctionRelationIndex(relations []correctionRelation, sourceSequenceNo int64) int {
	for index := range relations {
		if relations[index].SourceSequenceNo == sourceSequenceNo {
			return index
		}
	}
	return -1
}

func upsertCorrectionRelation(state *liveAnalysisPayload, relation correctionRelation) {
	if state == nil || relation.SourceSequenceNo <= 0 {
		return
	}
	if index := correctionRelationIndex(state.CorrectionRelations, relation.SourceSequenceNo); index >= 0 {
		existing := state.CorrectionRelations[index]
		if existing.Locked && existing.TargetItemID != relation.TargetItemID {
			return
		}
		if existing.EstablishedAtVersion > 0 {
			relation.EstablishedAtVersion = existing.EstablishedAtVersion
		}
		state.CorrectionRelations[index] = relation
		return
	}
	state.CorrectionRelations = append(state.CorrectionRelations, relation)
	sort.Slice(state.CorrectionRelations, func(i, j int) bool {
		return state.CorrectionRelations[i].SourceSequenceNo <
			state.CorrectionRelations[j].SourceSequenceNo
	})
}

func applyLockedCorrectionRelation(
	state *liveAnalysisPayload,
	relation *correctionRelation,
	treeVersion int64,
	stats *liveAnalysisTreeMergeStats,
) {
	if state == nil || relation == nil || !relation.Locked ||
		relation.TargetItemID == "" {
		return
	}
	targetAt := -1
	replacementAt := -1
	for index := range state.Items {
		switch state.Items[index].ID {
		case relation.TargetItemID:
			targetAt = index
		case relation.ReplacementItemID:
			replacementAt = index
		}
	}
	if targetAt < 0 {
		return
	}
	if replacementAt < 0 {
		target := &state.Items[targetAt]
		target.InformationStatus = "tentative"
		target.ClassificationStatus = classificationTentative
		target.CandidateInactive = true
		target.SuppressionReason = "correction_pending_replacement"
		removeItemNodesFromTree(state.Tree, map[string]struct{}{target.ID: {}})
		removeSemanticRelationsForItems(state.Tree, map[string]struct{}{target.ID: {}})
		relation.Status = "pending"
		return
	}
	target := state.Items[targetAt]
	replacement := state.Items[replacementAt]
	if strings.TrimSpace(relation.Origin) == "" {
		relation.Origin = "explicit_correction"
	}
	supersessionOrigin := "explicit_correction"
	if manualCorrectionRelation(*relation) {
		supersessionOrigin = "manual_user_edit"
	}
	changed := !target.Inactive || target.MergedIntoID != replacement.ID ||
		target.SuppressionReason != "superseded_by_explicit_correction" ||
		target.InformationStatus != "superseded"
	state.Items[targetAt].Inactive = true
	state.Items[targetAt].MergedIntoID = replacement.ID
	state.Items[targetAt].CandidateInactive = false
	state.Items[targetAt].SuppressionReason = "superseded_by_explicit_correction"
	state.Items[targetAt].InformationStatus = "superseded"
	state.Items[targetAt].SupersededByItemID = replacement.ID
	if state.Items[targetAt].SupersededAtTreeVersion == 0 {
		state.Items[targetAt].SupersededAtTreeVersion = treeVersion
	}
	state.Items[targetAt].SupersessionOrigin = supersessionOrigin
	state.Items[targetAt].SupersessionEvidenceSequenceNos = appendUniqueSequence(
		state.Items[targetAt].SupersessionEvidenceSequenceNos, relation.SourceSequenceNo,
	)
	state.Items[targetAt].RestoredAtTreeVersion = 0
	state.Items[targetAt].RestorationOrigin = ""
	// Superseded is a correction lifecycle, not a successful resolution. A
	// recovery sentence can be processed before this relation is applied in the
	// same merge, and legacy payloads may already contain that invalid state.
	state.Items[targetAt].Status = "open"
	state.Items[targetAt].ResolvedAtVersion = 0
	state.Items[targetAt].ResolutionEvidenceSequenceNos = nil
	state.Items[targetAt].ResolutionReason = ""
	relation.Status = "superseded"
	relation.ReplacementItemID = replacement.ID
	addItemTombstone(
		state, target, "superseded", replacement.ID,
		supersessionOrigin, "", treeVersion-1, treeVersion,
	)
	removeItemNodesFromTree(state.Tree, map[string]struct{}{target.ID: {}})
	removeSemanticRelationsForItems(state.Tree, map[string]struct{}{target.ID: {}})
	for index := range state.EmergingTopics {
		for at, evidenceID := range state.EmergingTopics[index].EvidenceItemIDs {
			if evidenceID == target.ID {
				state.EmergingTopics[index].EvidenceItemIDs[at] = replacement.ID
			}
		}
		state.EmergingTopics[index].EvidenceItemIDs =
			uniqueNonEmptyIDs(state.EmergingTopics[index].EvidenceItemIDs)
	}
	if changed && stats != nil {
		stats.CorrectionItemsSuperseded++
		stats.CorrectionDecisions = append(stats.CorrectionDecisions, correctionSupersessionDecision{
			CorrectionSequenceNo: relation.SourceSequenceNo,
			TargetSequenceNo:     relation.TargetSequenceNo,
			SupersededItemID:     target.ID, ReplacementItemID: replacement.ID,
			Similarity: relation.Confidence, Decision: "superseded",
			Reason:         "explicit_correction_relation_locked",
			RelationLocked: true,
		})
	}
}

// enforceExplicitSupersessionMonotonicity makes the previous canonical item
// state authoritative over a later model diff, stale replay, validator pass,
// or audit clone. Only a trusted manual restore carrying explicit provenance
// may cross the superseded -> active boundary.
func enforceExplicitSupersessionMonotonicity(
	state *liveAnalysisPayload,
	previous liveAnalysisPayload,
	treeVersion int64,
	stats *liveAnalysisTreeMergeStats,
) {
	if state == nil || len(previous.Items) == 0 {
		return
	}
	currentByID := make(map[string]int, len(state.Items))
	for index := range state.Items {
		currentByID[state.Items[index].ID] = index
	}
	for _, old := range previous.Items {
		if old.SupersessionOrigin != "explicit_correction" ||
			old.SupersededAtTreeVersion <= 0 {
			continue
		}
		if manualItemRestore(old) {
			continue
		}
		at, exists := currentByID[old.ID]
		if !exists {
			continue
		}
		current := &state.Items[at]
		if manualItemRestore(*current) {
			continue
		}
		wasReactivated := !current.Inactive
		wasResolved := current.Status == "resolved"
		provenanceChanged := current.InformationStatus != "superseded" ||
			current.SupersededByItemID != old.SupersededByItemID ||
			current.SupersededAtTreeVersion != old.SupersededAtTreeVersion ||
			current.SupersessionOrigin != old.SupersessionOrigin
		if !wasReactivated && !wasResolved && !provenanceChanged {
			continue
		}
		current.Inactive = true
		current.MergedIntoID = firstNonEmptyTrimmed(old.SupersededByItemID, old.MergedIntoID)
		current.CandidateInactive = false
		current.InformationStatus = "superseded"
		current.SuppressionReason = "superseded_by_explicit_correction"
		current.SupersededByItemID = firstNonEmptyTrimmed(old.SupersededByItemID, old.MergedIntoID)
		current.SupersededByItemIDs = append([]string(nil), old.SupersededByItemIDs...)
		current.SupersededAtTreeVersion = old.SupersededAtTreeVersion
		current.SupersessionOrigin = old.SupersessionOrigin
		current.SupersessionEvidenceSequenceNos = append(
			[]int64(nil), old.SupersessionEvidenceSequenceNos...,
		)
		current.Status = "open"
		current.ResolvedAtVersion = 0
		current.ResolutionEvidenceSequenceNos = nil
		current.ResolutionReason = ""
		current.RestoredAtTreeVersion = 0
		current.RestorationOrigin = ""
		removeItemNodesFromTree(state.Tree, map[string]struct{}{current.ID: {}})
		removeSemanticRelationsForItems(state.Tree, map[string]struct{}{current.ID: {}})
		addItemTombstone(
			state, *current, "superseded", current.SupersededByItemID,
			"explicit_correction", "", old.SupersededAtTreeVersion-1,
			old.SupersededAtTreeVersion,
		)
		if stats != nil {
			if wasReactivated {
				stats.SupersededReactivated++
			}
			if wasResolved {
				stats.SupersededResolved++
			}
			stats.CorrectionMonotonicityViolations++
			attemptedState := "superseded_metadata_changed"
			switch {
			case wasReactivated:
				attemptedState = "active"
			case wasResolved:
				attemptedState = "resolved"
			}
			stats.CorrectionDecisions = append(stats.CorrectionDecisions, correctionSupersessionDecision{
				SupersededItemID:   old.ID,
				ReplacementItemID:  old.SupersededByItemID,
				Decision:           "transition_rejected",
				Reason:             "supersession_is_monotonic",
				AttemptedNextState: attemptedState,
				TransitionRejected: true,
			})
		}
	}
}

func manualItemRestore(item liveAnalysisItem) bool {
	origin := strings.ToLower(strings.TrimSpace(item.RestorationOrigin))
	return item.RestoredAtTreeVersion > item.SupersededAtTreeVersion &&
		(origin == "manual" || origin == "manual_user_edit" || origin == "user")
}

// repairPersistedExplicitSupersessions upgrades legacy payloads and also
// protects fresh readers from a partially written/stale item state. A locked
// correction relation or explicit-correction tombstone is durable evidence;
// the old proposition must not be projected as active merely because its item
// fields came from an older snapshot.
func repairPersistedExplicitSupersessions(state *liveAnalysisPayload) {
	if state == nil {
		return
	}
	type provenance struct {
		replacementID string
		version       int64
		evidence      []int64
	}
	byID := make(map[string]provenance)
	for _, relation := range state.CorrectionRelations {
		if !relation.Locked || relation.Status != "superseded" || relation.TargetItemID == "" {
			continue
		}
		version := relation.EstablishedAtVersion
		if version <= 0 {
			version = state.TreeVersion
		}
		byID[relation.TargetItemID] = provenance{
			replacementID: relation.ReplacementItemID,
			version:       version,
			evidence:      []int64{relation.SourceSequenceNo},
		}
	}
	for _, tombstone := range state.ItemTombstones {
		if tombstone.Reason != "superseded" || tombstone.CanonicalItemID == "" {
			continue
		}
		origin := strings.ToLower(strings.TrimSpace(tombstone.CreatedBy))
		if origin != "explicit_correction" && origin != "correction_supersession" {
			continue
		}
		if _, exists := byID[tombstone.CanonicalItemID]; !exists {
			byID[tombstone.CanonicalItemID] = provenance{
				replacementID: tombstone.MergedIntoItemID,
				version:       tombstone.CreatedAtVersion,
			}
		}
	}
	for index := range state.Items {
		item := &state.Items[index]
		entry, exists := byID[item.ID]
		if !exists && item.SupersessionOrigin != "explicit_correction" {
			continue
		}
		if !exists {
			entry = provenance{
				replacementID: item.SupersededByItemID,
				version:       item.SupersededAtTreeVersion,
				evidence:      item.SupersessionEvidenceSequenceNos,
			}
		}
		if manualItemRestore(*item) {
			continue
		}
		item.Inactive = true
		item.Status = "open"
		item.InformationStatus = "superseded"
		item.MergedIntoID = firstNonEmptyTrimmed(entry.replacementID, item.MergedIntoID)
		item.SupersededByItemID = firstNonEmptyTrimmed(entry.replacementID, item.SupersededByItemID, item.MergedIntoID)
		if item.SupersededAtTreeVersion == 0 {
			item.SupersededAtTreeVersion = entry.version
		}
		item.SupersessionOrigin = "explicit_correction"
		for _, sequenceNo := range entry.evidence {
			item.SupersessionEvidenceSequenceNos = appendUniqueSequence(
				item.SupersessionEvidenceSequenceNos, sequenceNo,
			)
		}
		item.SuppressionReason = "superseded_by_explicit_correction"
		item.ResolvedAtVersion = 0
		item.ResolutionEvidenceSequenceNos = nil
		item.ResolutionReason = ""
		removeItemNodesFromTree(state.Tree, map[string]struct{}{item.ID: {}})
		removeSemanticRelationsForItems(state.Tree, map[string]struct{}{item.ID: {}})
	}
}

func bestCorrectionReplacement(
	items []liveAnalysisItem,
	correctionText string,
	sequenceNo int64,
	scope liveEvidenceScope,
) int {
	bestAt, bestScore := -1, -1.0
	combinedText := strings.TrimSpace(correctionText)
	if current, currentOK := scope.Segments[sequenceNo]; currentOK {
		if next, nextOK := scope.Segments[sequenceNo+1]; nextOK && explicitAdjacentSameSpeaker(current, next) {
			combinedText += " " + strings.TrimSpace(scope.TranscriptText[sequenceNo+1])
		}
	}
	for index, item := range items {
		if item.Inactive || item.MergedIntoID != "" ||
			(!containsInt64(item.EvidenceSequenceNos, sequenceNo) &&
				!containsInt64(item.EvidenceSequenceNos, sequenceNo+1)) {
			continue
		}
		score := semanticItemSimilarity(item.Title+" "+item.Body, combinedText)
		if score > bestScore {
			bestAt, bestScore = index, score
		}
	}
	return bestAt
}

func bestSupersededCorrectionItem(
	items []liveAnalysisItem,
	correctionText string,
	sequenceNo int64,
	replacementID string,
	scope liveEvidenceScope,
) (int, float64) {
	bestAt, bestEvidence := -1, int64(0)
	bestScore, bestRank := -1.0, -1.0
	oldReference := correctionReferencedOldContent(correctionText)
	for index, item := range items {
		if item.ID == replacementID || item.Inactive || item.MergedIntoID != "" ||
			item.Kind == "decision" || item.Kind == "todo" {
			continue
		}
		evidenceNo := maxEvidenceBefore(item.EvidenceSequenceNos, sequenceNo)
		if evidenceNo <= 0 || sequenceNo-evidenceNo > 2 {
			continue
		}
		itemText := item.Title + " " + item.Body
		score := semanticItemSimilarity(itemText, correctionText)
		structuredContradiction := structuredCorrectionPredicateContradiction(itemText, correctionText)
		sharedSubject := sharedTreeAuditSubjectTerm(itemText, correctionText) || structuredContradiction
		if correctionAdditiveScopePattern.MatchString(correctionText) && oldReference == "" && !structuredContradiction {
			// An additive location/entity qualifier expands the confirmed scope;
			// it does not contradict and retire the preceding observation.
			continue
		}
		// A correction marker alone is not enough to retire a proposition.
		// Require both a concrete shared subject and meaningful proposition
		// overlap so that a multi-fact recap containing "正確には" does not
		// suppress an adjacent but independent fact.
		priorEvidenceText := strings.TrimSpace(scope.TranscriptText[evidenceNo])
		priorSupportScore := semanticItemSimilarity(itemText, priorEvidenceText)
		priorPropositionSupported := priorSupportScore >= 0.35 ||
			(sharedTreeAuditSubjectTerm(itemText, priorEvidenceText) && priorSupportScore >= 0.16)
		immediateExplicitCorrection := sequenceNo-evidenceNo == 1 &&
			strongCorrectionLeadPattern.MatchString(strings.TrimSpace(correctionText)) &&
			priorPropositionSupported
		oldReferenceScore := 0.0
		oldReferenceTokenOverlap := false
		if oldReference != "" {
			oldReferenceScore = semanticItemSimilarity(itemText, oldReference)
			oldReferenceTokenOverlap = correctionSpecificTokenOverlap(itemText, oldReference)
			if !oldReferenceTokenOverlap && oldReferenceScore < 0.18 &&
				!sharedTreeAuditSubjectTerm(itemText, oldReference) {
				continue
			}
		}
		confidence := score
		if oldReferenceScore > confidence {
			confidence = oldReferenceScore
		}
		if oldReferenceTokenOverlap && confidence < 0.70 {
			confidence = 0.70
		}
		if correctionSpecificTokenOverlap(itemText, correctionText) && confidence < 0.45 {
			confidence = 0.45
		}
		if immediateExplicitCorrection && sharedSubject && confidence < 0.35 {
			confidence = 0.35
		}
		if immediateExplicitCorrection && structuredContradiction && confidence < 0.80 {
			confidence = 0.80
		}
		if (!sharedSubject && !immediateExplicitCorrection &&
			!oldReferenceTokenOverlap) || confidence < 0.20 {
			continue
		}
		rank := confidence
		if sequenceNo-evidenceNo == 1 {
			rank += 0.08
		}
		if rank > bestRank ||
			(rank == bestRank && evidenceNo > bestEvidence) {
			bestAt, bestEvidence, bestScore, bestRank =
				index, evidenceNo, confidence, rank
		}
	}
	return bestAt, bestScore
}

// structuredCorrectionPredicateContradiction is deliberately narrow: it
// bridges lexical variation only for the same port-configuration subject and
// requires the mutually exclusive access/trunk predicates. Proximity or the
// generic word "設定" alone can never satisfy it.
func structuredCorrectionPredicateContradiction(previous, correction string) bool {
	previousHasSubject := correctionPortSubjectPattern.MatchString(previous)
	correctionHasSubject := correctionPortSubjectPattern.MatchString(correction) ||
		correctionPortConfigurationPattern.MatchString(correction)
	if !previousHasSubject || !correctionHasSubject {
		return false
	}
	return (correctionAccessModePattern.MatchString(previous) && correctionTrunkModePattern.MatchString(correction)) ||
		(correctionTrunkModePattern.MatchString(previous) && correctionAccessModePattern.MatchString(correction))
}

func correctionReferencedOldContent(text string) string {
	trimmed := strings.TrimSpace(text)
	trimmed = strongCorrectionLeadPattern.ReplaceAllString(trimmed, "")
	for _, separator := range []string{
		"というわけではなく", "ということではなく", "ではありません", "ではなく",
		"じゃありません", "じゃなく", "でなく",
	} {
		if at := strings.Index(trimmed, separator); at > 0 {
			return strings.Trim(strings.TrimSpace(trimmed[:at]), "、,「」")
		}
	}
	return ""
}

func correctionSpecificTokenOverlap(left, right string) bool {
	leftTokens := normalizedPatternMatches(correctionSpecificTokenPattern, left)
	rightTokens := normalizedPatternMatches(correctionSpecificTokenPattern, right)
	return patternMatchIntersects(leftTokens, rightTokens)
}

func maxEvidenceBefore(sequenceNos []int64, before int64) int64 {
	var best int64
	for _, sequenceNo := range sequenceNos {
		if sequenceNo < before && sequenceNo > best {
			best = sequenceNo
		}
	}
	return best
}

func removeSemanticRelationsForItems(tree *liveAnalysisTree, removed map[string]struct{}) {
	if tree == nil || len(removed) == 0 {
		return
	}
	kept := tree.Relations[:0]
	for _, relation := range tree.Relations {
		if _, drop := removed[relation.Source]; drop {
			continue
		}
		if _, drop := removed[relation.Target]; drop {
			continue
		}
		kept = append(kept, relation)
	}
	tree.Relations = kept
}

type issueRecoveryDecision struct {
	SourceTodoID       string
	RecoveredIssueID   string
	EvidenceSequenceNo int64
	Decision           string
	Reason             string
}

// restoreIssuesFromPollutedTodoEvidence repairs legacy snapshots where a
// recap action overwrote an Issue under the same canonical ID. The current
// Todo wording is retained, while a clearly open-question fragment from its
// older evidence is restored as a distinct Issue. New live rounds are
// protected earlier by detachCrossKindActionUpdates.
func restoreIssuesFromPollutedTodoEvidence(
	state *liveAnalysisPayload,
	scope liveEvidenceScope,
	version int64,
	stats *liveAnalysisTreeMergeStats,
) {
	if state == nil || state.Tree == nil {
		return
	}
	usedIDs := make(map[string]struct{}, len(state.Items))
	for _, item := range state.Items {
		usedIDs[item.ID] = struct{}{}
	}
	originalCount := len(state.Items)
	for index := 0; index < originalCount; index++ {
		source := state.Items[index]
		if source.Kind != "todo" || source.Inactive || source.MergedIntoID != "" ||
			len(source.EvidenceSequenceNos) < 2 {
			continue
		}
		todoSequenceNo := latestTodoSupportingEvidence(source, scope)
		if todoSequenceNo <= 0 {
			continue
		}
		parentNode := liveTreeNodeByID(state.Tree, source.ID)
		for _, sequenceNo := range source.EvidenceSequenceNos {
			// ID-overwrite pollution has the older Issue evidence followed by
			// a materially later action update. Adjacent clauses in one
			// discussion are handled by semantic splitting instead.
			if sequenceNo >= todoSequenceNo || todoSequenceNo-sequenceNo < 2 {
				continue
			}
			evidence := strings.TrimSpace(scope.TranscriptText[sequenceNo])
			if evidence == "" {
				continue
			}
			for _, rawSentence := range kindSentenceBoundaryPattern.Split(evidence, -1) {
				sentence := strings.Trim(strings.TrimSpace(rawSentence), "、, ")
				if len([]rune(sentence)) < 6 {
					continue
				}
				probe := liveAnalysisItem{
					Kind: "issue", Subtype: issueSubtypeDiscussion,
					Title: sentence, Body: sentence, Status: "open",
					EvidenceSequenceNos: []int64{sequenceNo},
				}
				decision := evaluateLiveItemKind(probe, liveEvidenceScope{}, "legacy_issue_recovery")
				if decision.CanonicalKind != "issue" ||
					decision.Confidence < itemKindValidationThreshold(itemKindValidationFinal) ||
					recoveredIssueAlreadyRepresented(state.Items, sentence) {
					continue
				}
				probe.Subtype = decision.CanonicalSubtype
				probe.Title = semanticallyCompleteItemLabelOrOriginal(sentence, probe.Kind)
				probe.Body = truncateRunes(sentence, liveAnalysisTreeDescriptionMaxRunes)
				probe.Severity = source.Severity
				probe.ClassificationStatus = source.ClassificationStatus
				probe.AssignmentConfidence = source.AssignmentConfidence
				probe.AssignmentSource = "final_evidence_recovery"
				probe.AssignmentReason = "restored open proposition from legacy cross-kind evidence"
				probe.RelatedAgendaIDs = append([]string(nil), source.RelatedAgendaIDs...)
				probe.CreatedThroughSequenceNo = source.CreatedThroughSequenceNo
				probe.InitialEvidenceMaxSequenceNo = sequenceNo
				probe.evidenceSpecified = true
				probe.ID = serverGeneratedItemID(probe)
				if _, duplicate := usedIDs[probe.ID]; duplicate {
					continue
				}
				usedIDs[probe.ID] = struct{}{}
				state.Items = append(state.Items, probe)
				if parentNode != nil {
					state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
						ID: probe.ID, Kind: probe.Kind, Subtype: probe.Subtype,
						ParentID: parentNode.ParentID, Label: probe.Title,
						Description: probe.Body, Status: probe.Status,
						CreatedAtVersion: version, UpdatedAtVersion: version,
						LastParentChangeSource:  "final_evidence_recovery",
						LastParentChangeVersion: version,
					})
				}
				if stats != nil {
					stats.IssuesRecoveredFromTodoEvidence++
					stats.IssueRecoveryDecisions = append(stats.IssueRecoveryDecisions, issueRecoveryDecision{
						SourceTodoID: source.ID, RecoveredIssueID: probe.ID,
						EvidenceSequenceNo: sequenceNo, Decision: "recovered",
						Reason: "open_issue_fragment_found_in_legacy_todo_evidence",
					})
				}
			}
		}
	}
	rebuildTreeAuditEdges(state.Tree)
}

func latestTodoSupportingEvidence(item liveAnalysisItem, scope liveEvidenceScope) int64 {
	var latest int64
	for _, sequenceNo := range item.EvidenceSequenceNos {
		if sequenceNo > latest &&
			sequenceSupportsItemSemantics(item, strings.TrimSpace(scope.TranscriptText[sequenceNo])) {
			latest = sequenceNo
		}
	}
	return latest
}

func recoveredIssueAlreadyRepresented(items []liveAnalysisItem, sentence string) bool {
	for _, item := range items {
		if item.Kind != "issue" || item.Inactive || item.MergedIntoID != "" {
			continue
		}
		itemText := item.Title + " " + item.Body
		if sharedTreeAuditSubjectTerm(itemText, sentence) &&
			semanticItemSimilarity(itemText, sentence) >= 0.18 {
			return true
		}
	}
	return false
}
