package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"deciscope-core-api/internal/domain"
)

// decisionCandidate is a high-precision, server-detected candidate. Detection
// alone never changes persisted state: reconcileDecisionCandidates first
// compares the candidate with the model diff and previous canonical items,
// then rejects negated/future/consideration wording before accepting it.
type decisionCandidate struct {
	SequenceNo int64
	Statement  string
	Hash       string
	Recap      bool
}

type decisionExtractionAudit struct {
	MarkerSegments     int
	ModelDecisionItems int
	AcceptedDecisions  int
	MergedDecisions    int
	CandidateRefs      []decisionCandidate
}

var (
	decisionClauseSplitPattern = regexp.MustCompile(`[。！？!?\n]+`)
	decisionPositivePattern    = regexp.MustCompile(`(?:決定(?:事項)?(?:と)?します|決定事項とします|を採用します|で確定します|を方針とします|方針にします|で進めます|ことにします)`)
	decisionRecapPattern       = regexp.MustCompile(`決定事項(?:は|として)`)
	decisionNegativePattern    = regexp.MustCompile(`(?:未決定|決まってい(?:ない|ません)|決定してい(?:ない|ません)|決定せず|まだ決定|次回.{0,12}決定|決定.{0,12}(?:検討|候補)|採用.{0,12}(?:検討|候補)|候補にすぎ|したい|予定です)`)
	decisionSuffixPattern      = regexp.MustCompile(`(?:する)?ことを決定(?:事項)?と?します|決定(?:事項)?と?します|を採用します|で確定します|を方針とします|方針にします|で進めます|ことにします`)
)

func detectDecisionCandidates(segments []domain.TranscriptSegment) []decisionCandidate {
	var candidates []decisionCandidate
	seen := make(map[string]struct{})
	for _, segment := range segments {
		if !segment.IsFinal || segment.SequenceNo <= 0 {
			continue
		}
		for _, rawClause := range decisionClauseSplitPattern.Split(segment.Text, -1) {
			clause := strings.TrimSpace(rawClause)
			if clause == "" || decisionNegativePattern.MatchString(clause) {
				continue
			}
			recap := decisionRecapPattern.MatchString(clause)
			if !recap && !decisionPositivePattern.MatchString(clause) {
				continue
			}
			key := semanticItemKey(clause)
			if key == "" {
				continue
			}
			dedupeKey := strings.Join([]string{key, strconv.FormatInt(segment.SequenceNo, 10)}, "\x00")
			if _, duplicate := seen[dedupeKey]; duplicate {
				continue
			}
			seen[dedupeKey] = struct{}{}
			sum := sha256.Sum256([]byte(clause))
			candidates = append(candidates, decisionCandidate{
				SequenceNo: segment.SequenceNo,
				Statement:  clause,
				Hash:       hex.EncodeToString(sum[:8]),
				Recap:      recap,
			})
		}
	}
	return candidates
}

// reconcileDecisionCandidates augments only the model diff passed to the
// normal parser. It promotes a semantically matching todo to decision (same
// stable ID), updates an existing decision for a recap, or creates a
// deterministic decision ID only for an unambiguous positive candidate.
func reconcileDecisionCandidates(content string, previousPayload json.RawMessage, candidates []decisionCandidate) (string, decisionExtractionAudit, error) {
	audit := decisionExtractionAudit{MarkerSegments: distinctCandidateSegments(candidates), CandidateRefs: candidates}
	cleaned := stripJSONCodeFence(content)
	var diff liveAnalysisPayload
	if err := json.Unmarshal([]byte(cleaned), &diff); err != nil {
		return content, audit, err
	}
	for _, item := range diff.Items {
		if strings.EqualFold(strings.TrimSpace(item.Kind), "decision") {
			audit.ModelDecisionItems++
		}
	}
	if len(candidates) == 0 {
		return cleaned, audit, nil
	}

	previous := previousLiveAnalysisState(previousPayload)
	for _, candidate := range candidates {
		if candidate.Recap && reconcileDecisionRecap(&diff, previous.Items, candidate, &audit) {
			continue
		}

		// Diff items share the same small transcript round as the candidate, so
		// a lower semantic threshold is safe here than across persisted history.
		// This catches concise model titles such as "合計三回測定" without
		// promoting an unrelated item from an older round.
		// A decision may promote a proposed todo, but must never consume a
		// semantically nearby question/open issue. Those remain independently
		// resolvable items in the same discussion group.
		if at, score := bestDecisionMatch(diff.Items, candidate.Statement, false); at >= 0 && score >= 0.18 {
			item := &diff.Items[at]
			if item.Kind != "decision" {
				item.Kind = "decision"
				audit.AcceptedDecisions++
			} else {
				audit.MergedDecisions++
			}
			item.EvidenceSequenceNos = appendUniqueSequence(item.EvidenceSequenceNos, candidate.SequenceNo)
			continue
		}

		if at, score := bestDecisionMatch(previous.Items, candidate.Statement, false); at >= 0 && score >= 0.28 {
			previousItem := previous.Items[at]
			if previousItem.Kind == "decision" || previousItem.Kind == "todo" {
				updated := previousItem
				updated.Kind = "decision"
				updated.Status = "updated"
				updated.Title = decisionCandidateTitle(candidate.Statement)
				updated.Body = candidate.Statement
				updated.EvidenceSequenceNos = []int64{candidate.SequenceNo}
				diff.Items = append(diff.Items, updated)
				if previousItem.Kind == "decision" {
					audit.MergedDecisions++
				} else {
					audit.AcceptedDecisions++
				}
				continue
			}
		}

		id := stableDecisionID(candidate.Statement)
		if existing := findItemByID(previous.Items, id); existing != nil {
			updated := *existing
			updated.Kind = "decision"
			updated.Status = "updated"
			updated.EvidenceSequenceNos = []int64{candidate.SequenceNo}
			diff.Items = append(diff.Items, updated)
			audit.MergedDecisions++
			continue
		}
		diff.Items = append(diff.Items, liveAnalysisItem{
			ID:                  id,
			Kind:                "decision",
			Severity:            "high",
			Title:               decisionCandidateTitle(candidate.Statement),
			Body:                candidate.Statement,
			Status:              "open",
			EvidenceSequenceNos: []int64{candidate.SequenceNo},
		})
		diff.Assignments = append(diff.Assignments, treeAssignment{NodeID: id, ParentTopicID: treeUnclassifiedTopicID, Confidence: 0.5, Reason: "server decision candidate"})
		audit.AcceptedDecisions++
	}
	decisionIDs := make(map[string]struct{})
	for i := range diff.Items {
		if diff.Items[i].Kind != "decision" {
			continue
		}
		decisionIDs[modelItemReference(diff.Items[i])] = struct{}{}
		if diff.Items[i].Status == "resolved" {
			diff.Items[i].Status = "updated"
		}
	}
	keptResolved := diff.ResolvedIds[:0]
	for _, id := range diff.ResolvedIds {
		if _, isDecision := decisionIDs[strings.TrimSpace(id)]; !isDecision {
			keptResolved = append(keptResolved, id)
		}
	}
	diff.ResolvedIds = keptResolved
	// A positive decision can answer an existing question/open issue or
	// complete its supporting TODO, but the canonical issue item remains a
	// separate record. Never change that item's kind into decision.
	resolvedByDecision := make(map[string]struct{}, len(diff.ResolvedIds))
	for _, id := range diff.ResolvedIds {
		resolvedByDecision[canonicalReferenceKey(id)] = struct{}{}
	}
	for _, update := range diff.ResolutionUpdates {
		if normalizeResolutionStatus(update.Status) == "resolved" {
			resolvedByDecision[canonicalReferenceKey(update.ItemID)] = struct{}{}
		}
	}
	resolutionCandidates := append(append([]liveAnalysisItem(nil), previous.Items...), diff.Items...)
	for _, item := range resolutionCandidates {
		itemReference := modelItemReference(item)
		if itemReference == "" || !resolvableItemKind(item.Kind) || item.Status == "resolved" {
			continue
		}
		for _, candidate := range candidates {
			if semanticItemSimilarity(item.Title+" "+item.Body, candidate.Statement) < 0.16 {
				continue
			}
			key := canonicalReferenceKey(itemReference)
			if _, exists := resolvedByDecision[key]; !exists {
				diff.ResolutionUpdates = append(diff.ResolutionUpdates, resolutionUpdate{
					ItemID: itemReference, Status: "resolved", EvidenceSequenceNos: []int64{candidate.SequenceNo}, Reason: "subject-matched explicit decision",
				})
				resolvedByDecision[key] = struct{}{}
			}
			break
		}
	}

	encoded, err := json.Marshal(diff)
	if err != nil {
		return content, audit, err
	}
	return string(encoded), audit, nil
}

func reconcileDecisionRecap(diff *liveAnalysisPayload, previous []liveAnalysisItem, candidate decisionCandidate, audit *decisionExtractionAudit) bool {
	matched := false
	candidateKey := semanticItemKey(candidate.Statement)
	for _, item := range previous {
		if item.Kind != "decision" {
			continue
		}
		itemKey := semanticItemKey(item.Title + item.Body)
		if itemKey == "" || (!strings.Contains(candidateKey, itemKey) && semanticItemSimilarity(item.Title+item.Body, candidate.Statement) < 0.16) {
			continue
		}
		updated := item
		updated.Status = "updated"
		updated.EvidenceSequenceNos = []int64{candidate.SequenceNo}
		diff.Items = append(diff.Items, updated)
		audit.MergedDecisions++
		matched = true
	}
	return matched
}

func bestDecisionMatch(items []liveAnalysisItem, statement string, allowAnyKind bool) (int, float64) {
	bestAt, bestScore := -1, 0.0
	for i, item := range items {
		if !allowAnyKind && item.Kind != "decision" && item.Kind != "todo" {
			continue
		}
		score := semanticItemSimilarity(item.Title+" "+item.Body, statement)
		if score > bestScore {
			bestAt, bestScore = i, score
		}
	}
	return bestAt, bestScore
}

func semanticItemSimilarity(a, b string) float64 {
	aKey, bKey := semanticItemKey(a), semanticItemKey(b)
	if aKey == "" || bKey == "" {
		return 0
	}
	if aKey == bKey || strings.Contains(aKey, bKey) || strings.Contains(bKey, aKey) {
		shorter, longer := utf8.RuneCountInString(aKey), utf8.RuneCountInString(bKey)
		if shorter > longer {
			shorter, longer = longer, shorter
		}
		if longer > 0 {
			return 0.7 + 0.3*float64(shorter)/float64(longer)
		}
	}
	aGrams, bGrams := runeBigrams(aKey), runeBigrams(bKey)
	intersection := 0
	for gram, aCount := range aGrams {
		bCount := bGrams[gram]
		if aCount < bCount {
			intersection += aCount
		} else {
			intersection += bCount
		}
	}
	aTotal, bTotal := 0, 0
	for _, count := range aGrams {
		aTotal += count
	}
	for _, count := range bGrams {
		bTotal += count
	}
	if aTotal+bTotal == 0 {
		return 0
	}
	return float64(2*intersection) / float64(aTotal+bTotal)
}

func semanticItemKey(value string) string {
	key := normalizeForMatch(value)
	for _, boilerplate := range []string{"決定事項", "決定します", "決定する", "新規", "すること", "こと", "方針", "対応"} {
		key = strings.ReplaceAll(key, boilerplate, "")
	}
	return key
}

func runeBigrams(value string) map[string]int {
	runes := []rune(value)
	grams := make(map[string]int)
	if len(runes) == 1 {
		grams[string(runes)] = 1
		return grams
	}
	for i := 0; i+1 < len(runes); i++ {
		grams[string(runes[i:i+2])]++
	}
	return grams
}

func decisionCandidateTitle(statement string) string {
	title := strings.TrimSpace(decisionSuffixPattern.ReplaceAllString(statement, ""))
	title = strings.Trim(title, "、。 ")
	if title == "" {
		title = strings.TrimSpace(statement)
	}
	return truncateRunes(title, 40)
}

func stableDecisionID(statement string) string {
	sum := sha256.Sum256([]byte(semanticItemKey(statement)))
	return "decision-auto-" + hex.EncodeToString(sum[:6])
}

func appendUniqueSequence(values []int64, sequenceNo int64) []int64 {
	if sequenceNo <= 0 {
		return values
	}
	for _, value := range values {
		if value == sequenceNo {
			return values
		}
	}
	values = append(values, sequenceNo)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values
}

func distinctCandidateSegments(candidates []decisionCandidate) int {
	seen := make(map[int64]struct{})
	for _, candidate := range candidates {
		seen[candidate.SequenceNo] = struct{}{}
	}
	return len(seen)
}

func findItemByID(items []liveAnalysisItem, id string) *liveAnalysisItem {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}

func auditModelItemKinds(content string) (map[string]int, int, map[string]int) {
	kinds := make(map[string]int)
	reasons := make(map[string]int)
	cleaned := stripJSONCodeFence(content)
	var diff liveAnalysisPayload
	if err := json.Unmarshal([]byte(cleaned), &diff); err != nil {
		reasons["invalid_json"]++
		return kinds, 1, reasons
	}
	rejected := 0
	for _, item := range diff.Items {
		kind := strings.ToLower(strings.TrimSpace(item.Kind))
		if strings.TrimSpace(item.Title) == "" && strings.TrimSpace(item.Body) == "" {
			rejected++
			reasons["empty_text"]++
			continue
		}
		if !validLiveAnalysisItemKind(kind) {
			rejected++
			reasons["invalid_kind"]++
			continue
		}
		kinds[kind]++
	}
	return kinds, rejected, reasons
}

func livePayloadItemKindCounts(payload json.RawMessage) map[string]int {
	counts := make(map[string]int)
	var parsed liveAnalysisPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return counts
	}
	for _, item := range parsed.Items {
		counts[item.Kind]++
	}
	return counts
}

func formatKindCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "[]"
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+":"+strconv.Itoa(counts[key]))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func formatDecisionCandidateRefs(candidates []decisionCandidate) string {
	if len(candidates) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		parts = append(parts, strconv.FormatInt(candidate.SequenceNo, 10)+":"+candidate.Hash)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
