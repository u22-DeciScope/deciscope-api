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
	resolutionClosurePattern     = regexp.MustCompile(`(?:解決済み?|解消(?:した|済み)?|対応(?:できる|可能|済み|完了)|結論が出た|回答(?:した|済み|確定)|決定(?:した|事項|済み)?|確定(?:した|済み)?|採用(?:した)?|とすることに(?:する|します)|方針に(?:する|します)|完了(?:した|済み)?|実施済み|終え(?:た|ました)|復旧(?:した|しました|が?完了|済み)|正常(?:になった|になりました|化した|に戻った)|接続が正常|疎通(?:を|が)?確認)`)
	resolutionOpenPattern        = regexp.MustCompile(`(?:未解決|未決定|未確定|まだ(?:決まって|決定して|確定して)い(?:ない|ません)|決まってい(?:ない|ません)|決定しない|次回(?:の会議で)?検討|再検討|持ち越し|今後も検討|判断を保留|まだ.{0,16}(?:接続できない|できていない|できない)|一部.{0,12}(?:接続できない|つながらない|できない)|未解決事項として残|未確定事項として残)`)
	serverExplicitClosurePattern = regexp.MustCompile(`(?:解決済み|解決した|解消した|問題.{0,24}対応できる|対応できると判断|結論が出た|方針が確定した|この論点は閉じる|復旧(?:した|しました|が?完了|済み)|正常に(?:なった|なりました|戻った)|正常になったことを確認|接続が正常|疎通(?:を|が)確認)`)
	// recoveryClosurePattern marks a serverExplicitClosurePattern match as a
	// "recovery" closure (a fault/connectivity restoration statement) rather
	// than an ordinary decision/agreement closure. synthesizeExplicitClosureUpdates
	// treats recovery closures differently: they may resolve every matching
	// open issue/risk instead of a single best target, but never create a new
	// issue and never target investigation-subtype issues or todos (recovering
	// the fault is not the same as concluding the root-cause investigation).
	recoveryClosurePattern       = regexp.MustCompile(`(?:復旧|正常(?:に|化)|疎通)`)
	closureProblemSubjectPattern = regexp.MustCompile(`(?:問題|懸念|課題|不足|未確認|未確定|未決定)`)
	// itemUnderContinuedInvestigationPattern marks an item's own title/body as
	// stating that the item's subject itself is still under investigation or
	// confirmation (e.g. "原因確定には追加調査が必要"). A recovery closure
	// (see recoveryClosurePattern) only reports that connectivity/a fault was
	// restored; it must never resolve an item that explicitly says its own
	// root cause or subject is still unconfirmed, even when the recovery
	// sentence is loosely similar in subject matter.
	itemUnderContinuedInvestigationPattern = regexp.MustCompile(`(?:追加(?:の)?調査が必要|調査(?:を)?継続|原因確定には|確認できていない|特定できていない|究明が必要|調査中)`)
)

// synthesizeExplicitClosureUpdates is a conservative server-side fallback for
// rounds where the model omitted resolutionUpdates. It only scans final
// transcript rows from the current round and either finds one unambiguous
// resolvable subject or creates an issue when the closure sentence itself
// explicitly names the problem. Generic "この論点" language never resolves an
// unrelated item without an immediately preceding target in the same round.
func synthesizeExplicitClosureUpdates(previous, diff []liveAnalysisItem, scope liveEvidenceScope, stats *liveAnalysisTreeMergeStats) ([]liveAnalysisItem, []resolutionUpdate) {
	sequenceNos := make([]int64, 0, len(scope.CurrentRound))
	for sequenceNo := range scope.CurrentRound {
		sequenceNos = append(sequenceNos, sequenceNo)
	}
	sort.Slice(sequenceNos, func(i, j int) bool { return sequenceNos[i] < sequenceNos[j] })
	items := append(append([]liveAnalysisItem(nil), previous...), diff...)
	itemIndex := make(map[string]int, len(items))
	for i := range items {
		itemIndex[items[i].ID] = i
	}
	updatesByID := make(map[string]resolutionUpdate)
	updateOrder := make([]string, 0)
	recentTarget := ""
	recentSequence := int64(0)

	addUpdate := func(itemID string, evidence ...int64) {
		update, exists := updatesByID[itemID]
		if !exists {
			update = resolutionUpdate{ItemID: itemID, Status: "resolved", Reason: "server explicit closure"}
			updateOrder = append(updateOrder, itemID)
		}
		for _, sequenceNo := range evidence {
			update.EvidenceSequenceNos = appendUniqueSequence(update.EvidenceSequenceNos, sequenceNo)
		}
		updatesByID[itemID] = update
	}

	for _, sequenceNo := range sequenceNos {
		text := strings.TrimSpace(scope.TranscriptText[sequenceNo])
		if text == "" || !serverExplicitClosurePattern.MatchString(text) || resolutionOpenPattern.MatchString(text) {
			continue
		}
		if stats != nil {
			stats.ExplicitClosureCandidates++
		}
		if recoveryClosurePattern.MatchString(text) {
			matchedAny := false
			for _, candidate := range items {
				if !recoveryClosureEligibleItem(candidate, text) {
					continue
				}
				addUpdate(candidate.ID, sequenceNo)
				matchedAny = true
			}
			if !matchedAny {
				if stats != nil {
					stats.ClosureTargetsNotFound++
				}
				recordResolution(stats, resolutionEvaluation{
					Requested:       true,
					RequestedStatus: "resolved",
					Result:          resolutionRejected,
					Reason:          "no_target",
				})
				continue
			}
			if stats != nil {
				stats.ClosureTargetsFound++
			}
			continue
		}
		generic := strings.Contains(text, "この論点") && !closureProblemSubjectPattern.MatchString(strings.ReplaceAll(text, "この論点", ""))
		target := ""
		if generic && recentTarget != "" && sequenceNo-recentSequence <= 2 {
			target = recentTarget
		} else {
			target = bestExplicitClosureTarget(items, text, sequenceNo, false)
		}
		if target == "" && !generic {
			title := explicitClosureIssueTitle(text)
			if title != "" && closureProblemSubjectPattern.MatchString(title) {
				item := liveAnalysisItem{
					Kind: "issue", Severity: "high", Title: title, Body: text, Status: "open",
					EvidenceSequenceNos: []int64{sequenceNo}, evidenceSpecified: true,
				}
				item.ID = serverGeneratedItemID(item)
				if at, exists := itemIndex[item.ID]; exists {
					target = items[at].ID
				} else {
					diff = append(diff, item)
					items = append(items, item)
					itemIndex[item.ID] = len(items) - 1
					target = item.ID
				}
			}
		}
		if target == "" && !generic {
			target = bestExplicitClosureTarget(items, text, sequenceNo, true)
		}
		if target == "" {
			if stats != nil {
				stats.ClosureTargetsNotFound++
			}
			recordResolution(stats, resolutionEvaluation{
				Requested:       true,
				RequestedStatus: "resolved",
				Result:          resolutionRejected,
				Reason:          "no_target",
			})
			continue
		}
		if stats != nil {
			stats.ClosureTargetsFound++
		}
		evidence := []int64{sequenceNo}
		if generic && recentSequence > 0 {
			evidence = append([]int64{recentSequence}, evidence...)
		}
		addUpdate(target, evidence...)
		recentTarget, recentSequence = target, sequenceNo
	}

	updates := make([]resolutionUpdate, 0, len(updateOrder))
	for _, id := range updateOrder {
		updates = append(updates, updatesByID[id])
	}
	return diff, updates
}

// recoveryClosureEligibleItem reports whether item is a valid resolved
// target for a recovery-type closure sentence (see recoveryClosurePattern):
// an open, non-investigation issue or risk whose subject is at least
// loosely related to the recovery sentence. todo is excluded -- restoring
// connectivity does not itself complete a follow-up action item --
// investigation-subtype issues are excluded so a fault recovery never
// silently resolves the root-cause investigation (recovery != root-cause
// resolution), and items whose own title/body says their subject is still
// under investigation (see itemUnderContinuedInvestigationPattern) are
// excluded for the same reason, regardless of subtype.
func recoveryClosureEligibleItem(item liveAnalysisItem, text string) bool {
	if item.Kind != "issue" && item.Kind != "risk" {
		return false
	}
	if item.Subtype == issueSubtypeInvestigation {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(item.Status))
	if status == "resolved" || status == "dismissed" {
		return false
	}
	if itemUnderContinuedInvestigationPattern.MatchString(item.Title + " " + item.Body) {
		return false
	}
	return semanticItemSimilarity(item.Title+" "+item.Body, text) >= 0.12
}

func bestExplicitClosureTarget(items []liveAnalysisItem, text string, sequenceNo int64, allowTodo bool) string {
	type scoredTarget struct {
		id       string
		priority int
		score    float64
	}
	scored := make([]scoredTarget, 0)
	priority := map[string]int{"question": 0, "open_issue": 0, "issue": 0, "risk": 0}
	if allowTodo {
		priority["todo"] = 1
	}
	for _, item := range items {
		kindPriority, eligible := priority[item.Kind]
		if !eligible || item.Status == "dismissed" {
			continue
		}
		score := semanticItemSimilarity(item.Title+" "+item.Body, text)
		near := false
		for _, evidenceSequence := range item.EvidenceSequenceNos {
			delta := sequenceNo - evidenceSequence
			if delta >= 0 && delta <= 4 {
				near = true
				break
			}
		}
		if near {
			score += 0.05
		}
		if score < 0.08 {
			continue
		}
		scored = append(scored, scoredTarget{id: item.ID, priority: kindPriority, score: score})
	}
	if len(scored) == 0 {
		return ""
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].priority != scored[j].priority {
			return scored[i].priority < scored[j].priority
		}
		return scored[i].score > scored[j].score
	})
	if len(scored) > 1 && scored[0].priority == scored[1].priority && scored[0].score-scored[1].score < 0.03 {
		return ""
	}
	return scored[0].id
}

func explicitClosureIssueTitle(text string) string {
	text = strings.Trim(strings.TrimSpace(text), "、。 ")
	for _, prefix := range []string{"したがって、", "したがって", "そのため、", "そのため"} {
		text = strings.TrimSpace(strings.TrimPrefix(text, prefix))
	}
	for _, marker := range []string{"という問題は", "という問題", "については", "について"} {
		if at := strings.Index(text, marker); at > 0 {
			text = strings.Trim(strings.TrimSpace(text[:at]), "、。 ")
			break
		}
	}
	if text == "" || strings.Contains(text, "この論点") {
		return ""
	}
	return truncateRunes(text, 40)
}

func mergeExplicitClosureUpdates(requested, fallback []resolutionUpdate, resolver *canonicalReferenceResolver) []resolutionUpdate {
	merged := append([]resolutionUpdate(nil), requested...)
	for _, candidate := range fallback {
		candidateID, _, candidateOK := resolver.resolve(candidate.ItemID)
		matched := false
		for i := range merged {
			if normalizeResolutionStatus(merged[i].Status) != "resolved" {
				continue
			}
			existingID, _, existingOK := resolver.resolve(merged[i].ItemID)
			if !candidateOK || !existingOK || candidateID != existingID {
				continue
			}
			for _, sequenceNo := range candidate.EvidenceSequenceNos {
				merged[i].EvidenceSequenceNos = appendUniqueSequence(merged[i].EvidenceSequenceNos, sequenceNo)
			}
			matched = true
			break
		}
		if !matched {
			merged = append(merged, candidate)
		}
	}
	return merged
}

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
		wasResolved := item.Status == "resolved"
		item.Status = "resolved"
		if !wasResolved || item.ResolvedAtVersion == 0 {
			item.ResolvedAtVersion = update.Version
		}
		for _, sequenceNo := range update.EvidenceSequenceNos {
			item.ResolutionEvidenceSequenceNos = appendUniqueSequence(item.ResolutionEvidenceSequenceNos, sequenceNo)
		}
		if strings.TrimSpace(update.Reason) != "" {
			item.ResolutionReason = update.Reason
		}
		return
	}
	wasResolved := item.Status == "resolved"
	item.Status = "open"
	if wasResolved {
		item.ReopenedAtVersion = update.Version
		item.ReopenEvidenceSequenceNos = append([]int64(nil), update.EvidenceSequenceNos...)
		item.ReopenReason = update.Reason
	}
}

func repairNonResolvableStatus(item *liveAnalysisItem) {
	if item == nil || item.Status != "resolved" || resolvableItemKind(item.Kind) {
		return
	}
	item.Status = "open"
}
