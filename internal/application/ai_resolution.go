package application

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// Resolution is a server-owned lifecycle delta. Decisions and facts remain
// immutable records; TODO uses resolved on the wire for backwards-compatible
// completed-state rendering.
func resolvableItemKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "question", "open_issue", "issue", "risk", "todo":
		return true
	default:
		return false
	}
}

type resolutionUpdate struct {
	ItemID              string  `json:"itemId"`
	Status              string  `json:"status"`
	EvidenceSequenceNos []int64 `json:"evidenceSequenceNos"`
	Reason              string  `json:"reason"`
	Legacy              bool    `json:"-"`
}

// UnmarshalJSON preserves the numeric-string compatibility already supported
// by item evidence without letting a malformed value invalidate the response.
func (update *resolutionUpdate) UnmarshalJSON(data []byte) error {
	type plainUpdate resolutionUpdate
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	rawEvidence := fields["evidenceSequenceNos"]
	delete(fields, "evidenceSequenceNos")
	rest, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	var decoded plainUpdate
	if err := json.Unmarshal(rest, &decoded); err != nil {
		return err
	}
	*update = resolutionUpdate(decoded)
	if len(rawEvidence) == 0 || string(rawEvidence) == "null" {
		return nil
	}
	var values []json.RawMessage
	if err := json.Unmarshal(rawEvidence, &values); err != nil {
		return nil
	}
	for _, value := range values {
		sequenceNo, _, ok := parseEvidenceSequenceNo(value)
		if ok {
			update.EvidenceSequenceNos = append(update.EvidenceSequenceNos, sequenceNo)
		}
	}
	return nil
}

type validatedResolutionUpdate struct {
	ItemID              string
	Status              string
	EvidenceSequenceNos []int64
	Reason              string
	Version             int64
}

type resolutionEvaluation struct {
	ItemID                      string
	Kind                        string
	OldStatus                   string
	RequestedStatus             string
	NewStatus                   string
	EvidenceSequenceNos         []int64
	LatestContradictingSequence int64
	Requested                   bool
	Applied                     bool
	Reopened                    bool
	AliasResolved               bool
	Legacy                      bool
	Result                      string
	Reason                      string
}

const (
	resolutionApplied  = "applied"
	resolutionRejected = "rejected"
)

var (
	resolutionClosurePattern = regexp.MustCompile(`(?:解決済み?|解消(?:した|済み)?|対応(?:できる|可能|済み|完了)|結論が出た|回答(?:した|済み|確定)|決定(?:した|事項|済み)?|確定(?:した|済み)?|採用(?:した)?|とすることに(?:する|します)|方針に(?:する|します)|完了(?:した|済み)?|実施済み|終え(?:た|ました))`)
	resolutionOpenPattern    = regexp.MustCompile(`(?:未解決|未決定|未確定|まだ(?:決まって|決定して|確定して)い(?:ない|ません)|決まってい(?:ない|ません)|決定しない|次回(?:の会議で)?検討|再検討|持ち越し|今後も検討|判断を保留)`)
)

func recordResolution(stats *liveAnalysisTreeMergeStats, evaluation resolutionEvaluation) {
	if stats == nil {
		return
	}
	stats.ResolutionDecisions = append(stats.ResolutionDecisions, evaluation)
	if evaluation.AliasResolved {
		stats.AliasResolvedResolvedIDs++
	}
	if evaluation.Reason == "unknown_item_id" {
		stats.UnknownResolvedIDs++
	}
}

func normalizeResolutionStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "resolved":
		return "resolved"
	case "open", "active":
		return "open"
	default:
		return ""
	}
}

func legacyResolutionUpdates(ids map[string]struct{}, items []liveAnalysisItem) []resolutionUpdate {
	updates := make([]resolutionUpdate, 0, len(ids))
	for id := range ids {
		update := resolutionUpdate{ItemID: id, Status: "resolved", Legacy: true, Reason: "legacy resolvedIds"}
		key := canonicalReferenceKey(id)
		for _, item := range items {
			if canonicalReferenceKey(item.ID) == key {
				update.EvidenceSequenceNos = append([]int64(nil), item.EvidenceSequenceNos...)
				break
			}
		}
		updates = append(updates, update)
	}
	return updates
}

func resolutionItemIndex(previous, diff []liveAnalysisItem) map[string]liveAnalysisItem {
	items := make(map[string]liveAnalysisItem, len(previous)+len(diff))
	for _, item := range previous {
		items[item.ID] = item
	}
	for _, item := range diff {
		if prior, ok := items[item.ID]; ok {
			if item.Title == "" {
				item.Title = prior.Title
			}
			if item.Body == "" {
				item.Body = prior.Body
			}
			if item.Kind == "" {
				item.Kind = prior.Kind
			}
		}
		items[item.ID] = item
	}
	return items
}

func normalizeResolutionEvidence(values []int64, scope liveEvidenceScope) []int64 {
	seen := make(map[int64]struct{}, len(values))
	result := make([]int64, 0, len(values))
	for _, sequenceNo := range values {
		if sequenceNo <= 0 || sequenceNo > scope.CoveredThrough {
			continue
		}
		if _, allowed := scope.Allowed[sequenceNo]; !allowed {
			continue
		}
		if _, duplicate := seen[sequenceNo]; duplicate {
			continue
		}
		seen[sequenceNo] = struct{}{}
		result = append(result, sequenceNo)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func resolutionEvidenceText(sequenceNos []int64, scope liveEvidenceScope) string {
	parts := make([]string, 0, len(sequenceNos))
	for _, sequenceNo := range sequenceNos {
		if text := strings.TrimSpace(scope.TranscriptText[sequenceNo]); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, " ")
}

func resolutionSemanticMatch(item liveAnalysisItem, evidence string) bool {
	evidence = strings.TrimSpace(evidence)
	if evidence == "" {
		return false
	}
	itemText := strings.TrimSpace(item.Title + " " + item.Body)
	if semanticItemSimilarity(itemText, evidence) >= 0.08 {
		return true
	}
	itemCore := semanticIssueKey(itemText)
	evidenceCore := semanticIssueKey(evidence)
	return len([]rune(itemCore)) >= 3 && (strings.Contains(evidenceCore, itemCore) || strings.Contains(itemCore, evidenceCore))
}

func latestRelevantResolutionEvidence(item liveAnalysisItem, sequenceNos []int64, scope liveEvidenceScope) (int64, string) {
	latestSequenceNo := int64(0)
	latestText := ""
	for _, sequenceNo := range sequenceNos {
		text := strings.TrimSpace(scope.TranscriptText[sequenceNo])
		if text == "" || !resolutionSemanticMatch(item, text) || sequenceNo < latestSequenceNo {
			continue
		}
		latestSequenceNo = sequenceNo
		latestText = text
	}
	return latestSequenceNo, latestText
}

func latestContradictingResolutionEvidence(item liveAnalysisItem, requestedStatus string, after int64, scope liveEvidenceScope) int64 {
	latest := int64(0)
	for sequenceNo, text := range scope.TranscriptText {
		if sequenceNo <= after || sequenceNo > scope.CoveredThrough || semanticItemSimilarity(item.Title+" "+item.Body, text) < 0.18 {
			continue
		}
		contradicts := requestedStatus == "resolved" && resolutionOpenPattern.MatchString(text)
		if requestedStatus == "open" {
			contradicts = resolutionClosurePattern.MatchString(text) && !resolutionOpenPattern.MatchString(text)
		}
		if contradicts && sequenceNo > latest {
			latest = sequenceNo
		}
	}
	return latest
}

// validateResolutionUpdates is the only path that can change a persisted
// lifecycle state. It validates canonical identity, kind, final transcript
// evidence, semantic subject match, explicit state language, and later
// contradictions. Multiple deltas for one item use the latest evidence.
func validateResolutionUpdates(requested []resolutionUpdate, resolver *canonicalReferenceResolver, previous, diff []liveAnalysisItem, scope liveEvidenceScope, version int64, stats *liveAnalysisTreeMergeStats) map[string]validatedResolutionUpdate {
	items := resolutionItemIndex(previous, diff)
	validated := make(map[string]validatedResolutionUpdate)
	for _, update := range requested {
		rawID := strings.TrimSpace(update.ItemID)
		status := normalizeResolutionStatus(update.Status)
		canonical, aliased, ok := resolver.resolve(rawID)
		evaluation := resolutionEvaluation{ItemID: rawID, RequestedStatus: status, Requested: true, AliasResolved: aliased, Legacy: update.Legacy}
		if !ok {
			evaluation.Result, evaluation.Reason = resolutionRejected, "unknown_item_id"
			recordResolution(stats, evaluation)
			continue
		}
		evaluation.ItemID = canonical
		item, exists := items[canonical]
		if !exists {
			evaluation.Result, evaluation.Reason = resolutionRejected, "unknown_item_id"
			recordResolution(stats, evaluation)
			continue
		}
		evaluation.Kind, evaluation.OldStatus = item.Kind, item.Status
		if status == "" {
			evaluation.Result, evaluation.Reason = resolutionRejected, "invalid_status"
			recordResolution(stats, evaluation)
			continue
		}
		if !resolvableItemKind(item.Kind) {
			evaluation.Result, evaluation.Reason = resolutionRejected, "kind_not_resolvable"
			recordResolution(stats, evaluation)
			continue
		}
		evidence := normalizeResolutionEvidence(update.EvidenceSequenceNos, scope)
		evaluation.EvidenceSequenceNos = evidence
		if len(evidence) == 0 {
			evaluation.Result, evaluation.Reason = resolutionRejected, "no_valid_evidence"
			recordResolution(stats, evaluation)
			continue
		}
		evidenceText := resolutionEvidenceText(evidence, scope)
		if evidenceText == "" {
			evaluation.Result, evaluation.Reason = resolutionRejected, "no_evidence_text"
			recordResolution(stats, evaluation)
			continue
		}
		if !resolutionSemanticMatch(item, evidenceText) {
			evaluation.Result, evaluation.Reason = resolutionRejected, "semantic_mismatch"
			recordResolution(stats, evaluation)
			continue
		}
		latestEvidence, latestEvidenceText := latestRelevantResolutionEvidence(item, evidence, scope)
		if latestEvidence == 0 {
			evaluation.Result, evaluation.Reason = resolutionRejected, "semantic_mismatch"
			recordResolution(stats, evaluation)
			continue
		}
		// State words in older evidence must never outrank a newer explicit
		// state for the same subject. In particular, "まだ決定せず…後に確定"
		// and recap statements such as "未解決の課題は…" remain open.
		if status == "resolved" && resolutionOpenPattern.MatchString(latestEvidenceText) {
			evaluation.LatestContradictingSequence = latestEvidence
			evaluation.Result, evaluation.Reason = resolutionRejected, "contradicted_by_latest_evidence"
			recordResolution(stats, evaluation)
			continue
		}
		if status == "open" && resolutionClosurePattern.MatchString(latestEvidenceText) && !resolutionOpenPattern.MatchString(latestEvidenceText) {
			evaluation.LatestContradictingSequence = latestEvidence
			evaluation.Result, evaluation.Reason = resolutionRejected, "contradicted_by_latest_evidence"
			recordResolution(stats, evaluation)
			continue
		}
		explicit := resolutionClosurePattern.MatchString(latestEvidenceText)
		if status == "open" {
			explicit = resolutionOpenPattern.MatchString(latestEvidenceText)
		}
		if !explicit {
			evaluation.Result = resolutionRejected
			if status == "open" {
				evaluation.Reason = "no_explicit_open_state"
			} else {
				evaluation.Reason = "no_explicit_closure"
			}
			recordResolution(stats, evaluation)
			continue
		}
		if contradiction := latestContradictingResolutionEvidence(item, status, latestEvidence, scope); contradiction > 0 {
			evaluation.LatestContradictingSequence = contradiction
			evaluation.Result, evaluation.Reason = resolutionRejected, "contradicted_by_later_evidence"
			recordResolution(stats, evaluation)
			continue
		}
		if existing, duplicate := validated[canonical]; duplicate && len(existing.EvidenceSequenceNos) > 0 && existing.EvidenceSequenceNos[len(existing.EvidenceSequenceNos)-1] > latestEvidence {
			evaluation.Result, evaluation.Reason = resolutionRejected, "superseded_by_later_update"
			recordResolution(stats, evaluation)
			continue
		}
		validated[canonical] = validatedResolutionUpdate{ItemID: canonical, Status: status, EvidenceSequenceNos: evidence, Reason: strings.TrimSpace(update.Reason), Version: version}
		evaluation.Applied = true
		evaluation.Reopened = status == "open" && item.Status == "resolved"
		evaluation.NewStatus = status
		evaluation.Result = resolutionApplied
		recordResolution(stats, evaluation)
	}
	return validated
}

func applyResolutionUpdate(item *liveAnalysisItem, update validatedResolutionUpdate) {
	if item == nil {
		return
	}
	if update.Status == "resolved" {
		item.Status = "resolved"
		item.ResolvedAtVersion = update.Version
		item.ResolutionEvidenceSequenceNos = append([]int64(nil), update.EvidenceSequenceNos...)
		item.ResolutionReason = update.Reason
		return
	}
	item.Status = "open"
	item.ReopenedAtVersion = update.Version
	item.ReopenEvidenceSequenceNos = append([]int64(nil), update.EvidenceSequenceNos...)
	item.ReopenReason = update.Reason
}

func repairNonResolvableStatus(item *liveAnalysisItem) {
	if item == nil || item.Status != "resolved" || resolvableItemKind(item.Kind) {
		return
	}
	item.Status = "open"
}
