package application

import (
	"regexp"
	"sort"
	"strings"

	"deciscope-core-api/internal/domain"
)

const (
	deterministicTodoAssignmentReason       = "explicit_owner_action_commitment"
	deterministicCorrectionAssignmentReason = "explicit_correction_reconstruction"
	deterministicLimitAssignmentReason      = "explicit_scope_limit_reconstruction"
)

func synthesizeExplicitScopeLimitIssues(
	existing []liveAnalysisItem,
	scope liveEvidenceScope,
	timeline discourseTimeline,
) []liveAnalysisItem {
	known := append([]liveAnalysisItem(nil), existing...)
	var result []liveAnalysisItem
	for _, sequenceNo := range correctionEvidenceSequenceNos(scope) {
		switch timeline.Roles[sequenceNo] {
		case liveEvidenceReferenceRecap, liveEvidenceDiscourseOnly:
			continue
		}
		for _, clause := range semanticKindClauses(scope.TranscriptText[sequenceNo]) {
			clause = strings.Trim(strings.TrimSpace(clause), "、。.!！?？ ")
			if clause == "" || !itemRelationLimitPattern.MatchString(clause) ||
				!kindOpenQuestionPattern.MatchString(clause) {
				continue
			}
			label := itemLabelLeadingConnectorPattern.ReplaceAllString(clause, "")
			label = semanticallyCompleteItemLabelOrOriginal(label, "issue")
			probe := liveAnalysisItem{
				Kind: "issue", Subtype: issueSubtypeInvestigation, Severity: "medium",
				Title: label, Body: clause, Status: "open",
				EvidenceSequenceNos: []int64{sequenceNo}, EvidenceSnippets: []string{clause},
				InformationStatus: informationStatusGrounded, evidenceSpecified: true,
				AssignmentSource: "rule", AssignmentReason: deterministicLimitAssignmentReason,
				CreatedThroughSequenceNo: scope.CoveredThrough, InitialEvidenceMaxSequenceNo: sequenceNo,
			}
			if scopeLimitIssueRepresented(known, probe) {
				continue
			}
			decision := evaluateLiveItemKind(probe, liveEvidenceScope{}, "scope_limit_reconstruction")
			if decision.CanonicalKind != "issue" ||
				decision.Confidence < itemKindValidationThreshold(itemKindValidationLive) {
				continue
			}
			probe.Subtype = decision.CanonicalSubtype
			probe.ID = serverGeneratedItemID(probe)
			result = append(result, probe)
			known = append(known, probe)
		}
	}
	return result
}

func scopeLimitIssueRepresented(items []liveAnalysisItem, probe liveAnalysisItem) bool {
	for _, item := range items {
		if item.Inactive || item.MergedIntoID != "" || item.Kind != "issue" ||
			!itemEvidenceOverlaps(item, probe) {
			continue
		}
		if semanticItemSimilarity(item.Title+" "+item.Body, probe.Title+" "+probe.Body) >= 0.30 ||
			sharedTreeAuditSubjectTerm(item.Title+" "+item.Body, probe.Title+" "+probe.Body) {
			return true
		}
	}
	return false
}

var (
	deterministicTodoObjectPattern      = regexp.MustCompile(`(?:を|について|に対して|の(?:確認|更新|作成|策定|調査|対応|実施|適用|管理|監視))`)
	correctionDependentStatementPattern = regexp.MustCompile(
		`^(?:(?:いや|いえ)[、,]?)?(?:違います|そうではありません|その(?:設定|件|点|内容)ではありません|それではありません|この(?:設定|件|点)(?:ではありません)?)$`,
	)
	correctionNegativeLeadClausePattern = regexp.MustCompile(
		`^(?:完全な|すべてが|全てが).{1,80}(?:ではありません|ではない)$`,
	)
	correctionGroundingQualifierPattern = regexp.MustCompile(
		`(?i)(?:VLAN\s*\d+|\d+\s*階|月曜(?:日)?|火曜(?:日)?|水曜(?:日)?|木曜(?:日)?|金曜(?:日)?|土曜(?:日)?|日曜(?:日)?|旧スイッチ|交換後スイッチ)`,
	)
)

type deterministicSynthesisDecision struct {
	SequenceNo        int64
	Kind              string
	OwnerPresent      bool
	ActionPresent     bool
	ObjectPresent     bool
	CommitmentPresent bool
	Decision          string
	Reason            string
	ItemID            string
}

// synthesizeStrongTodoItems is a bounded, transcript-grounded safety net. It
// does not replace model extraction: it adds only clauses that independently
// contain an owner, a future action and a commitment. This also makes a
// meaningful sibling after a low-information atom survive when the model
// returned no usable item for the shared analysis batch.
func synthesizeStrongTodoItems(
	previous, diff []liveAnalysisItem,
	scope liveEvidenceScope,
	timeline discourseTimeline,
	stats *liveAnalysisTreeMergeStats,
) []liveAnalysisItem {
	sequenceNos := currentEvidenceSequenceNos(scope)
	known := append(append([]liveAnalysisItem(nil), previous...), diff...)
	synthesized := make([]liveAnalysisItem, 0, 4)
	usedEnrichmentTargets := make(map[string]struct{})
	for _, sequenceNo := range sequenceNos {
		acceptedInSequence := 0
		role := timeline.Roles[sequenceNo]
		if role == liveEvidenceReferenceRecap || role == liveEvidenceDiscourseOnly {
			continue
		}
		for _, clause := range semanticKindClauses(scope.TranscriptText[sequenceNo]) {
			clause = strings.Trim(strings.TrimSpace(clause), "、。.!！ ")
			if clause == "" {
				continue
			}
			probe := liveAnalysisItem{
				Kind: "todo", Title: semanticallyCompleteItemLabelOrOriginal(clause, "todo"), Body: clause,
				Status: "open", EvidenceSequenceNos: []int64{sequenceNo},
				EvidenceSnippets: []string{clause},
			}
			if segment, exists := scope.Segments[sequenceNo]; exists {
				// Resolve first-person ownership before proposition matching.
				// Otherwise an existing sibling assigned to a named person can
				// be mistaken for the first-person action in the same segment.
				probe.Title = deterministicTodoTitle(clause, segment.SpeakerName)
			}
			features := inferItemSemanticFeatures(probe, liveEvidenceScope{})
			actionPresent := features.ActionVerbPresent && futureActionIntent(clause)
			objectPresent := deterministicTodoObjectPattern.MatchString(clause)
			rejection := ""
			switch {
			case !actionPresent:
				rejection = "no_future_action"
			case !features.OwnerPresent:
				rejection = "owner_missing"
			case !objectPresent:
				rejection = "action_object_missing"
			case !features.DecisionOrCommitment:
				rejection = "commitment_missing"
			case kindUnassignedNecessityPattern.MatchString(clause):
				rejection = "necessity_or_proposal_only"
			case acceptedInSequence >= 3:
				rejection = "per_sequence_synthesis_cap"
			}
			if rejection != "" {
				if stats != nil && (features.ActionVerbPresent || futureActionIntent(clause)) {
					stats.DeterministicSynthesisDecisions = append(
						stats.DeterministicSynthesisDecisions,
						deterministicSynthesisDecision{
							SequenceNo: sequenceNo, Kind: "todo",
							OwnerPresent: features.OwnerPresent, ActionPresent: actionPresent,
							ObjectPresent:     objectPresent,
							CommitmentPresent: features.DecisionOrCommitment,
							Decision:          "rejected", Reason: rejection,
						},
					)
				}
				continue
			}
			if stats != nil {
				stats.StrongTodoCandidates++
			}
			if todoClauseRepresented(known, probe, sequenceNo, scope) {
				if stats != nil {
					stats.StrongTodoDuplicatesSuppressed++
					stats.DeterministicSynthesisDecisions = append(
						stats.DeterministicSynthesisDecisions,
						deterministicSynthesisDecision{
							SequenceNo: sequenceNo, Kind: "todo",
							OwnerPresent: true, ActionPresent: true, ObjectPresent: true,
							CommitmentPresent: true, Decision: "rejected",
							Reason: "canonical_proposition_already_represented",
						},
					)
				}
				continue
			}
			probe.Severity = "medium"
			probe.evidenceSpecified = true
			probe.InformationStatus = informationStatusGrounded
			probe.AssignmentSource = "rule"
			probe.AssignmentReason = deterministicTodoAssignmentReason
			probe.CreatedThroughSequenceNo = scope.CoveredThrough
			probe.InitialEvidenceMaxSequenceNo = sequenceNo
			if targetID := todoEnrichmentTarget(
				previous, probe, scope, usedEnrichmentTargets,
			); targetID != "" {
				probe.ID = targetID
				usedEnrichmentTargets[targetID] = struct{}{}
			} else {
				probe.ID = serverGeneratedItemID(probe)
			}
			synthesized = append(synthesized, probe)
			known = append(known, probe)
			acceptedInSequence++
			if stats != nil {
				stats.StrongTodosSynthesized++
				stats.DeterministicSynthesisDecisions = append(
					stats.DeterministicSynthesisDecisions,
					deterministicSynthesisDecision{
						SequenceNo: sequenceNo, Kind: "todo",
						OwnerPresent: true, ActionPresent: true, ObjectPresent: true,
						CommitmentPresent: true, Decision: "accepted",
						Reason: deterministicTodoAssignmentReason, ItemID: probe.ID,
					},
				)
			}
		}
	}
	return synthesized
}

func synthesizeExplicitDecisionItems(
	existing []liveAnalysisItem,
	segments []domain.TranscriptSegment,
	stats *liveAnalysisTreeMergeStats,
) []liveAnalysisItem {
	candidates := detectDecisionCandidates(segments)
	_, timeline := agendaTimelineFromSegments(segments)
	known := append([]liveAnalysisItem(nil), existing...)
	result := make([]liveAnalysisItem, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Recap || candidate.Statement == "" {
			continue
		}
		switch timeline.Roles[candidate.SequenceNo] {
		case liveEvidenceReferenceRecap, liveEvidenceDiscourseOnly:
			continue
		}
		represented := false
		for _, item := range known {
			if item.Inactive || item.MergedIntoID != "" || item.Kind != "decision" {
				continue
			}
			similarity := semanticItemSimilarity(item.Title+" "+item.Body, candidate.Statement)
			if (containsInt64(item.EvidenceSequenceNos, candidate.SequenceNo) &&
				(sharedTreeAuditSubjectTerm(item.Title+" "+item.Body, candidate.Statement) ||
					similarity >= 0.24)) ||
				similarity >= 0.55 {
				represented = true
				break
			}
		}
		if represented {
			if stats != nil {
				stats.DeterministicSynthesisDecisions = append(
					stats.DeterministicSynthesisDecisions,
					deterministicSynthesisDecision{
						SequenceNo: candidate.SequenceNo, Kind: "decision",
						ActionPresent: true, ObjectPresent: true, CommitmentPresent: true,
						Decision: "rejected", Reason: "canonical_proposition_already_represented",
					},
				)
			}
			continue
		}
		item := liveAnalysisItem{
			Kind: "decision", Severity: "medium",
			Title: semanticallyCompleteItemLabel(candidate.Statement, "decision"), Body: candidate.Statement,
			Status: "open", EvidenceSequenceNos: append([]int64(nil), candidate.SourceSequenceNos...),
			AssignmentSource: "rule", AssignmentReason: "explicit_decision_commitment",
			InformationStatus: informationStatusGrounded,
			evidenceSpecified: true,
		}
		if item.Title == "" {
			continue
		}
		if targetID := explicitDecisionPromotionTarget(known, candidate); targetID != "" {
			item.ID = targetID
		}
		for _, sequenceNo := range candidate.SourceSequenceNos {
			if sequenceNo > item.InitialEvidenceMaxSequenceNo {
				item.InitialEvidenceMaxSequenceNo = sequenceNo
			}
		}
		item.CreatedThroughSequenceNo = item.InitialEvidenceMaxSequenceNo
		if item.ID == "" {
			item.ID = serverGeneratedItemID(item)
		}
		result = append(result, item)
		known = append(known, item)
		if stats != nil {
			stats.StrongDecisionCandidates++
			stats.StrongDecisionsSynthesized++
			stats.DeterministicSynthesisDecisions = append(
				stats.DeterministicSynthesisDecisions,
				deterministicSynthesisDecision{
					SequenceNo: candidate.SequenceNo, Kind: "decision",
					ActionPresent: true, ObjectPresent: true, CommitmentPresent: true,
					Decision: "accepted", Reason: "explicit_decision_commitment", ItemID: item.ID,
				},
			)
		}
	}
	return result
}

func explicitDecisionPromotionTarget(
	items []liveAnalysisItem,
	candidate decisionCandidate,
) string {
	probe := liveAnalysisItem{
		Kind: "decision", Title: candidate.Statement, Body: candidate.Statement,
		EvidenceSequenceNos: candidate.SourceSequenceNos,
	}
	bestID, bestScore := "", 0.0
	for _, item := range items {
		if item.Inactive || item.MergedIntoID != "" || item.Kind != "todo" ||
			!itemEvidenceOverlaps(item, probe) {
			continue
		}
		itemText := item.Title + " " + item.Body
		// A named, deadline-bearing assignment is independently actionable
		// even when its surrounding utterance also states a policy decision.
		if kindDeadlineMarkerPattern.MatchString(itemText) &&
			len(normalizedPatternMatches(kindOwnerPattern, itemText)) > 0 {
			continue
		}
		score := semanticItemSimilarity(itemText, candidate.Statement)
		if sharedTreeAuditSubjectTerm(itemText, candidate.Statement) {
			score += 0.20
		}
		explicitPolicy := decisionPositivePattern.MatchString(itemText)
		explicitAdoption := decisionAdoptionVerbPattern.MatchString(itemText) &&
			decisionAdoptionVerbPattern.MatchString(candidate.Statement)
		if !explicitPolicy && !explicitAdoption {
			continue
		}
		if score >= 0.38 && score > bestScore {
			bestID, bestScore = item.ID, score
		}
	}
	return bestID
}

func deterministicTodoTitle(clause, speakerName string) string {
	title := strings.TrimSpace(clause)
	owner := strings.TrimSpace(speakerName)
	if owner != "" && (strings.HasPrefix(title, "私は") ||
		strings.HasPrefix(title, "私が") ||
		strings.HasPrefix(title, "わたしは") ||
		strings.HasPrefix(title, "わたしが")) {
		owner = strings.Fields(owner)[0]
		if !strings.HasSuffix(owner, "さん") && !strings.HasSuffix(owner, "氏") {
			owner += "さん"
		}
		for _, prefix := range []string{"わたしは", "わたしが", "私は", "私が"} {
			if strings.HasPrefix(title, prefix) {
				title = owner + "が" + strings.TrimSpace(strings.TrimPrefix(title, prefix))
				break
			}
		}
	}
	return semanticallyCompleteItemLabelOrOriginal(title, "todo")
}

func currentEvidenceSequenceNos(scope liveEvidenceScope) []int64 {
	sequenceNos := make([]int64, 0, len(scope.CurrentRound))
	for sequenceNo := range scope.CurrentRound {
		if sequenceNo > 0 {
			sequenceNos = append(sequenceNos, sequenceNo)
		}
	}
	sort.Slice(sequenceNos, func(i, j int) bool { return sequenceNos[i] < sequenceNos[j] })
	return sequenceNos
}

func todoClauseRepresented(
	items []liveAnalysisItem,
	probe liveAnalysisItem,
	sequenceNo int64,
	scope liveEvidenceScope,
) bool {
	_ = scope
	probeText := probe.Title + " " + probe.Body
	for _, item := range items {
		if item.Inactive || item.MergedIntoID != "" ||
			!containsInt64(item.EvidenceSequenceNos, sequenceNo) {
			continue
		}
		// A model Issue that happens to contain an action must not suppress the
		// safety net. It may later be reclassified or merged with this exact
		// Todo, but it cannot prove that the committed action was preserved.
		if item.Kind != "todo" && item.Kind != "decision" {
			continue
		}
		itemText := strings.TrimSpace(item.Title + " " + item.Body)
		// A high lexical score cannot override an explicit owner change.
		// Two people receiving the same action in one ASR segment are two
		// assignments; only an unspecified or matching owner can deduplicate.
		if sameOwnerOrUnspecified(itemText, probeText) &&
			(semanticItemSimilarity(itemText, probeText) >= 0.28 ||
				sharedTreeAuditSubjectTerm(itemText, probeText)) {
			return true
		}
	}
	return false
}

func todoEnrichmentTarget(
	previous []liveAnalysisItem,
	probe liveAnalysisItem,
	scope liveEvidenceScope,
	used map[string]struct{},
) string {
	_ = scope
	probeText := probe.Title + " " + probe.Body
	bestID, bestScore := "", 0.0
	for _, item := range previous {
		if item.Inactive || item.MergedIntoID != "" || item.Kind != "todo" {
			continue
		}
		if _, alreadyUsed := used[item.ID]; alreadyUsed {
			continue
		}
		itemText := strings.TrimSpace(item.Title + " " + item.Body)
		if !sameOwnerOrUnspecified(itemText, probeText) {
			continue
		}
		score := semanticItemSimilarity(itemText, probeText)
		if sharedTreeAuditSubjectTerm(itemText, probeText) {
			score += 0.20
		}
		// Enrichment replaces an existing item's visible proposition, so it
		// needs materially stronger identity evidence than topic grouping.
		// Loose subject overlap (for example two unrelated network checks)
		// must produce separate Todos.
		if score >= 0.50 && score > bestScore {
			bestID, bestScore = item.ID, score
		}
	}
	return bestID
}

func sameOwnerOrUnspecified(left, right string) bool {
	leftOwners := normalizedPatternMatches(kindOwnerPattern, left)
	rightOwners := normalizedPatternMatches(kindOwnerPattern, right)
	return len(leftOwners) == 0 || len(rightOwners) == 0 ||
		patternMatchIntersects(leftOwners, rightOwners)
}

// splitMultiAssignmentTodoDiff separates independently committed actions that
// share one ASR segment and one model Todo. Evidence sequence equality is not
// an identity key: owner/action/deadline boundaries define the propositions.
func splitMultiAssignmentTodoDiff(
	items []liveAnalysisItem,
	assignments []treeAssignment,
	scope liveEvidenceScope,
	stats *liveAnalysisTreeMergeStats,
) ([]liveAnalysisItem, []treeAssignment) {
	result := make([]liveAnalysisItem, 0, len(items)+2)
	outputAssignments := append([]treeAssignment(nil), assignments...)
	for _, item := range items {
		fragments := multiAssignmentTodoFragments(item, scope)
		if len(fragments) < 2 {
			result = append(result, item)
			continue
		}
		result = append(result, fragments...)
		for _, fragment := range fragments[1:] {
			for _, assignment := range assignments {
				if assignment.nodeID() != item.ID {
					continue
				}
				cloned := assignment
				cloned.NodeID = fragment.ID
				cloned.ItemID = ""
				cloned.ModelNodeID = fragment.ID
				cloned.EvidenceSequenceNos = append([]int64(nil), fragment.EvidenceSequenceNos...)
				outputAssignments = append(outputAssignments, cloned)
				break
			}
		}
		if stats != nil {
			stats.KindSemanticSplits++
			stats.KindSplitDecisions = append(stats.KindSplitDecisions, itemKindSplitDecision{
				SourceItemID: item.ID, FragmentCount: len(fragments),
				FragmentKinds: make([]string, len(fragments)),
			})
			for index := range stats.KindSplitDecisions[len(stats.KindSplitDecisions)-1].FragmentKinds {
				stats.KindSplitDecisions[len(stats.KindSplitDecisions)-1].FragmentKinds[index] = "todo"
			}
		}
	}
	return result, outputAssignments
}

func multiAssignmentTodoFragments(
	item liveAnalysisItem,
	scope liveEvidenceScope,
) []liveAnalysisItem {
	if item.Kind != "todo" || item.Inactive || item.MergedIntoID != "" {
		return nil
	}
	for _, sequenceNo := range item.EvidenceSequenceNos {
		// Use the item's own proposition span to decide whether it still needs
		// splitting. The full transcript evidence may contain several sibling
		// Todos; consulting it again after the first split would split every
		// already-canonical sibling on every final repair.
		text := strings.TrimSpace(item.Body)
		if text == "" && len(item.EvidenceSnippets) == 1 {
			text = strings.TrimSpace(item.EvidenceSnippets[0])
		}
		if text == "" {
			continue
		}
		qualifying := qualifyingMultiAssignmentTodoClauses(text, sequenceNo)
		if len(qualifying) < 2 && item.AssignmentSource != "rule" {
			// Legacy model summaries may collapse two owner-local actions while
			// omitting their owners from the visible body. Consult the cited
			// local utterance once, then mark resulting fragments as rule-owned
			// so later final-repair passes never split each sibling again.
			qualifying = qualifyingMultiAssignmentTodoClauses(
				scope.TranscriptText[sequenceNo], sequenceNo,
			)
		}
		if len(qualifying) < 2 {
			continue
		}
		fragments := make([]liveAnalysisItem, 0, len(qualifying))
		for index, clause := range qualifying {
			fragment := item
			fragment.Body = clause
			fragment.Title = semanticallyCompleteItemLabelOrOriginal(clause, fragment.Kind)
			if segment, exists := scope.Segments[sequenceNo]; exists {
				fragment.Title = deterministicTodoTitle(clause, segment.SpeakerName)
			}
			fragment.EvidenceSequenceNos = []int64{sequenceNo}
			fragment.EvidenceSnippets = []string{clause}
			fragment.evidenceSpecified = true
			fragment.semanticSplitFragment = true
			fragment.AssignmentSource = "rule"
			fragment.AssignmentReason = deterministicTodoAssignmentReason
			fragment.InitialEvidenceMaxSequenceNo = sequenceNo
			if index > 0 {
				fragment.ID = ""
				fragment.ClientKey = ""
				fragment.modelReference = ""
				fragment.ID = serverGeneratedItemID(fragment)
			}
			fragments = append(fragments, fragment)
		}
		return fragments
	}
	return nil
}

func qualifyingMultiAssignmentTodoClauses(text string, sequenceNo int64) []string {
	clauses := semanticKindClauses(strings.TrimSpace(text))
	qualifying := make([]string, 0, len(clauses))
	for _, clause := range clauses {
		probe := liveAnalysisItem{
			Kind: "todo", Title: clause, Body: clause,
			EvidenceSequenceNos: []int64{sequenceNo},
		}
		features := inferItemSemanticFeatures(probe, liveEvidenceScope{})
		if features.OwnerPresent && features.DecisionOrCommitment &&
			futureActionIntent(clause) &&
			!kindUnassignedNecessityPattern.MatchString(clause) {
			qualifying = append(qualifying, strings.TrimSpace(clause))
		}
	}
	return qualifying
}

func splitPersistedMultiAssignmentTodos(
	state *liveAnalysisPayload,
	scope liveEvidenceScope,
	stats *liveAnalysisTreeMergeStats,
) {
	if state == nil || state.Tree == nil {
		return
	}
	original := append([]liveAnalysisItem(nil), state.Items...)
	state.Items = state.Items[:0]
	for _, item := range original {
		fragments := multiAssignmentTodoFragments(item, scope)
		if len(fragments) < 2 {
			state.Items = append(state.Items, item)
			continue
		}
		state.Items = append(state.Items, fragments...)
		sourceNode := liveTreeNodeByID(state.Tree, item.ID)
		if sourceNode != nil {
			sourceNode.Label = fragments[0].Title
			sourceNode.Kind = "todo"
			for _, fragment := range fragments[1:] {
				if liveTreeNodeByID(state.Tree, fragment.ID) != nil {
					continue
				}
				node := *sourceNode
				node.ID = fragment.ID
				node.Label = fragment.Title
				node.Kind = "todo"
				state.Tree.Nodes = append(state.Tree.Nodes, node)
			}
		}
		if stats != nil {
			stats.KindSemanticSplits++
			kinds := make([]string, len(fragments))
			for index := range kinds {
				kinds[index] = "todo"
			}
			stats.KindSplitDecisions = append(stats.KindSplitDecisions, itemKindSplitDecision{
				SourceItemID: item.ID, FragmentCount: len(fragments), FragmentKinds: kinds,
			})
		}
	}
	rebuildTreeAuditEdges(state.Tree)
}

// synthesizeCorrectionFactItems reconstructs a replacement only when an
// explicit correction contains a high-confidence factual statement and no
// accepted item for that correction sequence survived model grounding.
func synthesizeCorrectionFactItems(
	previous, diff []liveAnalysisItem,
	scope liveEvidenceScope,
	timeline discourseTimeline,
	stats *liveAnalysisTreeMergeStats,
) []liveAnalysisItem {
	known := append(append([]liveAnalysisItem(nil), previous...), diff...)
	var synthesized []liveAnalysisItem
	for _, sequenceNo := range correctionEvidenceSequenceNos(scope) {
		text := strings.TrimSpace(scope.TranscriptText[sequenceNo])
		// The discourse classifier also marks recap sentences that mention a
		// past "修正" as correction-like context. Reconstruction is narrower:
		// only an explicit self-correction may create a replacement Fact.
		if text == "" || !discourseCorrectionPattern.MatchString(text) {
			continue
		}
		for _, replacement := range correctionReplacementStatements(sequenceNo, scope, timeline) {
			statement := replacement.Text
			if correctionSequenceRepresented(known, replacement.SequenceNo, statement, scope) {
				continue
			}
			// A reference-dependent correction still requires a tracked target.
			// A self-contained replacement can stand on its own, so target absence
			// must not discard the grounded corrected proposition.
			selfContained := selfContainedCorrectionFact(statement, scope.TranscriptText[replacement.SequenceNo]) ||
				highConfidenceCorrectionContinuationFact(statement, scope.TranscriptText[replacement.SequenceNo])
			if targetAt, _ := bestSupersededCorrectionItem(
				known, text, sequenceNo, "", scope,
			); targetAt < 0 && !selfContained {
				continue
			}
			probe := liveAnalysisItem{
				Kind: "fact", Severity: "medium",
				Title: semanticallyCompleteItemLabelOrOriginal(statement, "fact"), Body: statement, Status: "open",
				EvidenceSequenceNos:          []int64{replacement.SequenceNo},
				EvidenceSnippets:             []string{statement},
				evidenceSpecified:            true,
				AssignmentSource:             "rule",
				AssignmentReason:             deterministicCorrectionAssignmentReason,
				CreatedThroughSequenceNo:     scope.CoveredThrough,
				InitialEvidenceMaxSequenceNo: replacement.SequenceNo,
			}
			decision := evaluateLiveItemKind(probe, liveEvidenceScope{}, "correction_reconstruction")
			historicalFact := highConfidenceCorrectionContinuationFact(
				statement, scope.TranscriptText[replacement.SequenceNo],
			) || (kindPastEventPattern.MatchString(statement) &&
				!futureActionIntent(statement) &&
				!kindOpenQuestionPattern.MatchString(statement) &&
				!kindUncertaintyPattern.MatchString(statement) &&
				!kindProposalPattern.MatchString(statement))
			if (decision.CanonicalKind != "fact" ||
				decision.Confidence < itemKindValidationThreshold(itemKindValidationLive)) &&
				!historicalFact {
				continue
			}
			probe.ID = serverGeneratedItemID(probe)
			synthesized = append(synthesized, probe)
			known = append(known, probe)
			if stats != nil {
				stats.CorrectionItemsReconstructed++
			}
		}
	}
	return synthesized
}

type correctionReplacement struct {
	SequenceNo int64
	Text       string
}

func correctionReplacementStatements(
	sequenceNo int64,
	scope liveEvidenceScope,
	timeline discourseTimeline,
) []correctionReplacement {
	text := strings.TrimSpace(scope.TranscriptText[sequenceNo])
	result := make([]correctionReplacement, 0, 3)
	if statement := correctionReplacementStatement(text); statement != "" {
		result = append(result, correctionReplacement{SequenceNo: sequenceNo, Text: statement})
	}
	nextSequenceNo := sequenceNo + 1
	currentSegment, currentOK := scope.Segments[sequenceNo]
	nextSegment, nextOK := scope.Segments[nextSequenceNo]
	if !currentOK || !nextOK || !explicitAdjacentSameSpeaker(currentSegment, nextSegment) ||
		timeline.Roles[nextSequenceNo] == liveEvidenceReferenceRecap ||
		timeline.Roles[nextSequenceNo] == liveEvidenceDiscourseOnly {
		return result
	}
	// A purely negative correction is allowed to continue in the immediately
	// following same-speaker final segment. This is the common recognizer split
	// shape: "not a complete access port" followed by the positive facts.
	if correctionReplacementStatement(text) != "" &&
		!correctionNegativeLeadClausePattern.MatchString(strings.Trim(strings.TrimSpace(text), "、。.!！ ")) {
		return result
	}
	nextText := strings.TrimSpace(scope.TranscriptText[nextSequenceNo])
	for _, statement := range splitCorrectionContinuationFacts(nextText) {
		if !selfContainedCorrectionFact(statement, nextText) &&
			!highConfidenceCorrectionContinuationFact(statement, nextText) {
			continue
		}
		duplicate := false
		for _, existing := range result {
			if semanticItemSimilarity(existing.Text, statement) >= 0.82 {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result = append(result, correctionReplacement{SequenceNo: nextSequenceNo, Text: statement})
		}
	}
	return result
}

func highConfidenceCorrectionContinuationFact(statement, evidence string) bool {
	statement = strings.Trim(strings.TrimSpace(statement), "、。.!！ ")
	historical := kindPastEventPattern.MatchString(statement) ||
		strings.HasSuffix(statement, "いました") || strings.HasSuffix(statement, "いましたが")
	if statement == "" || !historical ||
		kindOpenQuestionPattern.MatchString(statement) || kindUncertaintyPattern.MatchString(statement) ||
		kindProposalPattern.MatchString(statement) || futureActionIntent(statement) {
		return false
	}
	if !strings.Contains(normalizeGroundingText(evidence), normalizeGroundingText(statement)) {
		return false
	}
	return strings.Contains(statement, "トランク設定") ||
		strings.Contains(statement, "アクセスポート設定") ||
		itemLabelVLANQualifierPattern.MatchString(statement)
}

func splitCorrectionContinuationFacts(text string) []string {
	text = strings.ReplaceAll(text, "が、", "。")
	text = strings.ReplaceAll(text, "が,", "。")
	var result []string
	for _, clause := range semanticKindClauses(text) {
		clause = strings.Trim(strings.TrimSpace(clause), "、。.!！ ")
		if clause != "" && !containsExactString(result, clause) {
			result = append(result, clause)
		}
	}
	return result
}

// deterministicSynthesizedAssignments inherits the durable topic of the
// proposition that a server-synthesized item updates. It does not invent a
// topic and leaves genuinely new subjects to the normal agenda-span and
// emerging-topic classifiers.
func deterministicSynthesizedAssignments(
	previous liveAnalysisPayload,
	items []liveAnalysisItem,
	scope liveEvidenceScope,
	existing []treeAssignment,
) []treeAssignment {
	if previous.Tree == nil || len(items) == 0 {
		return nil
	}
	assigned := make(map[string]struct{}, len(existing))
	for _, assignment := range existing {
		assigned[assignment.nodeID()] = struct{}{}
	}
	var result []treeAssignment
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		if _, exists := assigned[item.ID]; exists {
			continue
		}
		if topicID := treeItemTopic(previous.Tree, item.ID); topicID != "" &&
			topicID != treeUnclassifiedTopicID {
			continue
		}

		targetAt := -1
		if item.AssignmentReason == deterministicCorrectionAssignmentReason &&
			len(item.EvidenceSequenceNos) > 0 {
			sequenceNo := item.EvidenceSequenceNos[0]
			targetAt, _ = bestSupersededCorrectionItem(
				previous.Items, scope.TranscriptText[sequenceNo],
				sequenceNo, item.ID, scope,
			)
		}
		if targetAt < 0 {
			targetAt = bestPriorSynthesisTopicItem(previous, item)
		}
		if targetAt < 0 {
			continue
		}
		topicID := treeItemTopic(previous.Tree, previous.Items[targetAt].ID)
		if topicID == "" || topicID == treeUnclassifiedTopicID {
			continue
		}
		result = append(result, treeAssignment{
			NodeID: item.ID, ParentTopicID: topicID, Confidence: 0.95,
			Reason:              "deterministic synthesis inherited prior proposition topic",
			ServerSource:        assignmentSourceRule,
			EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...),
		})
		assigned[item.ID] = struct{}{}
	}
	return result
}

func bestPriorSynthesisTopicItem(previous liveAnalysisPayload, item liveAnalysisItem) int {
	itemText := item.Title + " " + item.Body
	bestAt, bestScore := -1, 0.0
	for index, candidate := range previous.Items {
		if candidate.Inactive || candidate.MergedIntoID != "" ||
			treeItemTopic(previous.Tree, candidate.ID) == "" ||
			treeItemTopic(previous.Tree, candidate.ID) == treeUnclassifiedTopicID {
			continue
		}
		candidateText := candidate.Title + " " + candidate.Body
		score := semanticItemSimilarity(itemText, candidateText)
		if sharedTreeAuditSubjectTerm(itemText, candidateText) {
			score += 0.20
		}
		if !itemEvidenceWithin(item, candidate, 4) {
			score -= 0.15
		}
		if score >= 0.30 && score > bestScore {
			bestAt, bestScore = index, score
		}
	}
	return bestAt
}

func correctionEvidenceSequenceNos(scope liveEvidenceScope) []int64 {
	sequenceNos := make([]int64, 0, len(scope.Allowed))
	for sequenceNo := range scope.Allowed {
		if sequenceNo > 0 {
			sequenceNos = append(sequenceNos, sequenceNo)
		}
	}
	if len(sequenceNos) == 0 {
		return currentEvidenceSequenceNos(scope)
	}
	sort.Slice(sequenceNos, func(i, j int) bool { return sequenceNos[i] < sequenceNos[j] })
	return sequenceNos
}

func correctionSequenceRepresented(
	items []liveAnalysisItem,
	sequenceNo int64,
	statement string,
	scope liveEvidenceScope,
) bool {
	for _, item := range items {
		if item.Inactive || item.MergedIntoID != "" ||
			!containsInt64(item.EvidenceSequenceNos, sequenceNo) {
			continue
		}
		decision := evaluateLiveItemKind(item, scope, "correction_representation_check")
		if item.Kind != "fact" &&
			(decision.CanonicalKind != "fact" ||
				decision.Confidence < itemKindValidationThreshold(itemKindValidationLive)) {
			continue
		}
		itemText := itemKindSemanticText(item, scope)
		statementVLANs := uniqueSortedStrings(itemLabelVLANQualifierPattern.FindAllString(statement, -1))
		itemVLANs := uniqueSortedStrings(itemLabelVLANQualifierPattern.FindAllString(itemText, -1))
		if len(statementVLANs) > 0 && !allFoldedStringsPresent(statementVLANs, itemVLANs) {
			continue
		}
		if strings.Contains(statement, "トランク") && !strings.Contains(itemText, "トランク") {
			continue
		}
		if sharedTreeAuditSubjectTerm(itemText, statement) ||
			semanticItemSimilarity(itemText, statement) >= 0.18 {
			return true
		}
	}
	return false
}

func allFoldedStringsPresent(want, values []string) bool {
	for _, value := range want {
		if !containsFoldedString(values, value) {
			return false
		}
	}
	return true
}

func correctionReplacementStatement(text string) string {
	statement := strings.TrimSpace(text)
	for _, marker := range []string{
		"いえ、正確には", "いえ,正確には", "いえ正確には",
		"正確には", "厳密には", "言い直すと", "訂正すると", "訂正します",
	} {
		if at := strings.Index(statement, marker); at >= 0 {
			statement = strings.TrimSpace(statement[at+len(marker):])
			break
		}
	}
	statement = strings.Trim(strings.TrimSpace(statement), "、。.!！ ")
	clauses := semanticKindClauses(statement)
	for index := len(clauses) - 1; index >= 0; index-- {
		candidate := strings.Trim(strings.TrimSpace(clauses[index]), "、。.!！ ")
		for _, separator := range []string{"が、", "が,", "ものの、", "ものの,"} {
			if at := strings.LastIndex(candidate, separator); at > 0 {
				tail := strings.Trim(strings.TrimSpace(candidate[at+len(separator):]), "、。.!！ ")
				if selfContainedCorrectionFact(tail, text) {
					candidate = tail
				}
				break
			}
		}
		if correctionNegativeLeadClausePattern.MatchString(candidate) && len(clauses) > 1 {
			continue
		}
		if selfContainedCorrectionFact(candidate, text) {
			return candidate
		}
	}
	return ""
}

func selfContainedCorrectionFact(statement, evidenceText string) bool {
	statement = strings.Trim(strings.TrimSpace(statement), "、。.!！ ")
	if statement == "" || correctionDependentStatementPattern.MatchString(statement) ||
		correctionNegativeLeadClausePattern.MatchString(statement) ||
		strongCorrectionLeadPattern.MatchString(statement) {
		return false
	}
	probe := liveAnalysisItem{Kind: "fact", Title: statement, Body: statement}
	if liveItemTextNeedsReferent(probe) || finalItemIsLowInformation(probe) ||
		!liveItemHasSpecificSubject(statement) ||
		kindOpenQuestionPattern.MatchString(statement) ||
		kindUncertaintyPattern.MatchString(statement) ||
		kindProposalPattern.MatchString(statement) || futureActionIntent(statement) {
		return false
	}
	if semanticItemSimilarity(statement, evidenceText) < 0.12 &&
		!strings.Contains(canonicalReferenceKey(evidenceText), canonicalReferenceKey(statement)) {
		return false
	}
	if !correctionQualifiersGrounded(statement, evidenceText) {
		return false
	}
	decision := evaluateLiveItemKind(probe, liveEvidenceScope{}, "self_contained_correction")
	historicalFact := kindPastEventPattern.MatchString(statement) &&
		!futureActionIntent(statement) &&
		!kindOpenQuestionPattern.MatchString(statement) &&
		!kindUncertaintyPattern.MatchString(statement) &&
		!kindProposalPattern.MatchString(statement)
	return historicalFact || (decision.CanonicalKind == "fact" &&
		decision.Confidence >= itemKindValidationThreshold(itemKindValidationLive) &&
		(decision.Features.TemporalScope == "past" || decision.Features.TemporalScope == "unknown"))
}

func correctionQualifiersGrounded(statement, evidenceText string) bool {
	evidenceQualifiers := make(map[string]struct{})
	for _, qualifier := range correctionGroundingQualifierPattern.FindAllString(evidenceText, -1) {
		evidenceQualifiers[canonicalReferenceKey(qualifier)] = struct{}{}
	}
	for _, qualifier := range correctionGroundingQualifierPattern.FindAllString(statement, -1) {
		if _, grounded := evidenceQualifiers[canonicalReferenceKey(qualifier)]; !grounded {
			return false
		}
	}
	return true
}

func addOrUpdateFinalSynthesizedItems(
	state *liveAnalysisPayload,
	items []liveAnalysisItem,
	version int64,
) {
	if state == nil || state.Tree == nil {
		return
	}
	for _, item := range items {
		existingAt := -1
		for index := range state.Items {
			if state.Items[index].ID == item.ID {
				existingAt = index
				break
			}
		}
		if existingAt >= 0 {
			existing := state.Items[existingAt]
			item.ClassificationStatus = classificationAssigned
			item.CandidateTopicID = ""
			item.CandidateInactive = false
			item.AssignmentConfidence = 0.95
			item.AssignmentSource = "rule"
			// A transcript-grounded deterministic replacement is stronger
			// than an older tentative recap item. Only its durable agenda
			// relations and tree placement are inherited below.
			item.RelatedAgendaIDs = append([]string(nil), existing.RelatedAgendaIDs...)
			item.EvidenceSequenceNos = appendUniqueSequences(existing.EvidenceSequenceNos, item.EvidenceSequenceNos)
			item.EvidenceSnippets = uniqueSortedStrings(append(existing.EvidenceSnippets, item.EvidenceSnippets...))
			item.GroundingDecision = ""
			state.Items[existingAt] = item
			if node := liveTreeNodeByID(state.Tree, item.ID); node != nil {
				node.Kind, node.Subtype = item.Kind, item.Subtype
				node.Label, node.Description, node.Status = item.Title, item.Body, item.Status
				node.UpdatedAtVersion = version
			}
			continue
		}

		parentID := finalSynthesizedItemParent(state, item)
		item.ClassificationStatus = classificationAssigned
		item.AssignmentConfidence = 0.95
		if parentID == treeUnclassifiedTopicID {
			item.ClassificationStatus = classificationUnclassified
		}
		state.Items = append(state.Items, item)
		state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
			ID: item.ID, Kind: item.Kind, Subtype: item.Subtype,
			ParentID: parentID, Label: item.Title, Description: item.Body,
			Status: item.Status, CreatedAtVersion: version, UpdatedAtVersion: version,
			LastParentChangeSource:  "deterministic_synthesis",
			LastParentChangeVersion: version, ParentConfidence: 0.95,
		})
	}
	rebuildTreeAuditEdges(state.Tree)
}

func finalSynthesizedItemParent(state *liveAnalysisPayload, item liveAnalysisItem) string {
	itemText := item.Title + " " + item.Body
	bestParent, bestScore := "", 0.0
	for _, candidate := range state.Items {
		if candidate.ID == item.ID || candidate.Inactive || candidate.MergedIntoID != "" {
			continue
		}
		node := liveTreeNodeByID(state.Tree, candidate.ID)
		if node == nil || node.ParentID == "" {
			continue
		}
		score := semanticItemSimilarity(itemText, candidate.Title+" "+candidate.Body)
		if sharedTreeAuditSubjectTerm(itemText, candidate.Title+" "+candidate.Body) {
			score += 0.20
		}
		if score > bestScore {
			bestParent, bestScore = node.ParentID, score
		}
	}
	if bestParent != "" && bestScore >= 0.25 {
		return bestParent
	}
	if liveTreeNodeByID(state.Tree, treeUnclassifiedTopicID) == nil {
		state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
			ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID,
			Label: treeUnclassifiedTopicLabel, Origin: topicOriginSystem,
			CreatedAtVersion: 1, UpdatedAtVersion: 1,
		})
	}
	return treeUnclassifiedTopicID
}
