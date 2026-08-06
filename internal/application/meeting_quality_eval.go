package application

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"deciscope-core-api/internal/domain"
)

const (
	meetingQualitySchemaVersion       = 1
	defaultPropositionMatchSimilarity = 0.40
)

var qualitySemanticQualifierPattern = regexp.MustCompile(`(?i)(?:\d+(?:\.\d+)?(?:階|時|日|週|月|年|台|人|件|円|万円|%|％)?|月曜(?:日)?|火曜(?:日)?|水曜(?:日)?|木曜(?:日)?|金曜(?:日)?|土曜(?:日)?|日曜(?:日)?|明日|昨日|今日|来週|今週|先週)`)

var supportedMeetingQualityRelations = map[string]struct{}{
	"supported_by": {},
	"caused_by":    {},
	"limits":       {},
	"resolves":     {},
	"action_for":   {},
	"contradicts":  {},
	"refines":      {},
}

type meetingQualityMatch struct {
	Expectation MeetingQualityProposition
	Item        liveAnalysisItem
	Node        liveAnalysisTreeNode
	Score       float64
	Found       bool
}

// RunMeetingQualitySuite replays fixed responses through the same merge,
// grounding, classification, candidate-promotion, tree-build, coverage, and
// final-repair functions used by live analysis. It performs no file, network,
// clock, or environment access.
func RunMeetingQualitySuite(suite MeetingQualitySuite) MeetingQualitySuiteReport {
	report := MeetingQualitySuiteReport{
		SchemaVersion: meetingQualitySchemaVersion,
		Suite:         strings.TrimSpace(suite.Name),
		Passed:        true,
		Scenarios:     make([]MeetingQualityScenarioResult, 0, len(suite.Scenarios)),
	}
	if report.Suite == "" {
		report.Suite = "deterministic"
	}
	for _, scenario := range suite.Scenarios {
		result := runMeetingQualityScenario(scenario)
		report.Scenarios = append(report.Scenarios, result)
		if !result.Passed {
			report.Passed = false
		}
	}
	return report
}

// EvaluateMeetingQualitySnapshot evaluates a payload already produced by a
// real deployment. It intentionally does not generate or approve a baseline:
// real-model runs remain observational/manual, while deterministic fixed
// responses are the only PR regression oracle.
func EvaluateMeetingQualitySnapshot(
	scenario MeetingQualityScenario,
	payload json.RawMessage,
) MeetingQualityScenarioResult {
	result := MeetingQualityScenarioResult{ID: scenario.ID}
	result.InputMode = "completed_snapshot_only"
	var state liveAnalysisPayload
	if err := json.Unmarshal(payload, &state); err != nil {
		result.Error = fmt.Sprintf("decode evaluated snapshot: %v", err)
		return result
	}
	segments := qualityDomainSegments(scenario)
	context := qualityMeetingContext(scenario.MeetingContext)
	result.FinalCoverage = state.CoveredThroughSequenceNo
	result.TreeVersion = state.TreeVersion
	evaluateMeetingQualityResult(&result, scenario, state, context, segments)
	result.Metrics.UnclassifiedGroundedSingletonCount = unclassifiedGroundedSingletonCount(state)
	result.Passed = result.Error == "" &&
		len(result.HardInvariantViolations) == 0 &&
		len(result.MissingRequiredPropositions) == 0 &&
		len(result.RelationFailures) == 0 &&
		len(result.ForbiddenResultsFound) == 0 &&
		len(result.SafetyFailures) == 0
	return result
}

func ValidateMeetingQualitySuite(suite MeetingQualitySuite) error {
	if suite.SchemaVersion != meetingQualitySchemaVersion {
		return fmt.Errorf("quality suite schemaVersion=%d, want %d", suite.SchemaVersion, meetingQualitySchemaVersion)
	}
	if len(suite.Scenarios) < 1 {
		return fmt.Errorf("quality suite contains no scenarios")
	}
	seen := make(map[string]struct{}, len(suite.Scenarios))
	for _, scenario := range suite.Scenarios {
		id := strings.TrimSpace(scenario.ID)
		if id == "" {
			return fmt.Errorf("quality scenario has an empty id")
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("duplicate quality scenario id %q", id)
		}
		seen[id] = struct{}{}
		if len(scenario.TranscriptSegments) == 0 {
			return fmt.Errorf("quality scenario %q has no transcript segments", id)
		}
		if len(scenario.Rounds) == 0 {
			return fmt.Errorf("quality scenario %q has no fixed AI response rounds", id)
		}
		if len(scenario.RequiredPropositions) == 0 {
			return fmt.Errorf("quality scenario %q has no required propositions", id)
		}
		propIDs := make(map[string]struct{}, len(scenario.RequiredPropositions))
		for _, proposition := range scenario.RequiredPropositions {
			if strings.TrimSpace(proposition.ID) == "" || strings.TrimSpace(proposition.Text) == "" {
				return fmt.Errorf("quality scenario %q has an incomplete proposition expectation", id)
			}
			if _, duplicate := propIDs[proposition.ID]; duplicate {
				return fmt.Errorf("quality scenario %q has duplicate proposition id %q", id, proposition.ID)
			}
			propIDs[proposition.ID] = struct{}{}
			if value := strings.TrimSpace(proposition.RequiredTemporalScope); value != "" && !qualityValidTemporalScope(value) {
				return fmt.Errorf("quality scenario %q proposition %q has invalid temporal scope %q", id, proposition.ID, value)
			}
			if value := strings.TrimSpace(proposition.RequiredEpistemicStatus); value != "" && !qualityValidEpistemicStatus(value) {
				return fmt.Errorf("quality scenario %q proposition %q has invalid epistemic status %q", id, proposition.ID, value)
			}
			if value := strings.TrimSpace(proposition.RequiredStatus); value != "" && value != "open" && value != "resolved" {
				return fmt.Errorf("quality scenario %q proposition %q has invalid status %q", id, proposition.ID, value)
			}
			if value := strings.TrimSpace(proposition.RequiredDescriptionStatus); value != "" && !qualityValidDescriptionStatus(value) {
				return fmt.Errorf("quality scenario %q proposition %q has invalid description status %q", id, proposition.ID, value)
			}
		}
		for _, relation := range scenario.RequiredRelations {
			if _, valid := supportedMeetingQualityRelations[strings.TrimSpace(relation.Kind)]; !valid {
				return fmt.Errorf("quality scenario %q has unsupported relation %q", id, relation.Kind)
			}
			if _, exists := propIDs[relation.From]; !exists {
				return fmt.Errorf("quality scenario %q relation references unknown from proposition %q", id, relation.From)
			}
			if _, exists := propIDs[relation.To]; !exists {
				return fmt.Errorf("quality scenario %q relation references unknown to proposition %q", id, relation.To)
			}
		}
	}
	return nil
}

func qualityValidTemporalScope(value string) bool {
	switch value {
	case "past", "current", "ongoing", "future", "unknown":
		return true
	default:
		return false
	}
}

func qualityValidDescriptionStatus(value string) bool {
	switch value {
	case descriptionStatusNormal, descriptionStatusGenerated, descriptionStatusRewritten,
		descriptionStatusIntentionallyOmitted, descriptionStatusRejectedUnsupported,
		descriptionStatusGenerationFailed, descriptionStatusTransportLost:
		return true
	default:
		return false
	}
}

func qualityValidEpistemicStatus(value string) bool {
	switch value {
	case "confirmed", "committed", "hypothesis", "unresolved", "proposed", "uncertain", "reported":
		return true
	default:
		return false
	}
}

func runMeetingQualityScenario(scenario MeetingQualityScenario) MeetingQualityScenarioResult {
	result := MeetingQualityScenarioResult{ID: scenario.ID}
	if err := validateOneMeetingQualityScenario(scenario); err != nil {
		result.Error = err.Error()
		result.Passed = false
		return result
	}

	segments := qualityDomainSegments(scenario)
	context := qualityMeetingContext(scenario.MeetingContext)
	bySequence := make(map[int64]domain.TranscriptSegment, len(segments))
	for _, segment := range segments {
		bySequence[segment.SequenceNo] = segment
	}
	raw := append(json.RawMessage(nil), scenario.SeedPayload...)
	if len(raw) == 0 {
		raw = nil
		result.InputMode = "transcript_context_fixed_ai"
	} else {
		result.InputMode = "seeded_transcript_context_fixed_ai"
	}
	result.ProductionStages = []string{
		"live_extraction_result_application",
		"semantic_grounding",
		"kind_validation",
		"evidence_normalization",
		"candidate_lifecycle",
		"dynamic_topic_promotion",
		"grouping",
	}
	cfg := TreeClassificationConfig{
		AgendaAssignmentThreshold: scenario.Classification.AgendaAssignmentThreshold,
		PromotionMinItems:         scenario.Classification.PromotionMinItems,
		PromotionMinRounds:        scenario.Classification.PromotionMinRounds,
		MaxDynamicTopics:          scenario.Classification.MaxDynamicTopics,
	}
	for index, round := range scenario.Rounds {
		roundSegments := make([]domain.TranscriptSegment, 0, len(round.SequenceNos))
		scope := newLiveEvidenceScope()
		maxRound := int64(0)
		for _, sequenceNo := range round.SequenceNos {
			segment, exists := bySequence[sequenceNo]
			if !exists {
				result.Error = fmt.Sprintf("round %d references unknown transcript sequence %d", index+1, sequenceNo)
				return result
			}
			roundSegments = append(roundSegments, segment)
			scope.CurrentRound[sequenceNo] = struct{}{}
			if sequenceNo > maxRound {
				maxRound = sequenceNo
			}
		}
		previous := previousLiveAnalysisState(raw)
		for _, segment := range segments {
			if segment.SequenceNo > maxRound {
				continue
			}
			scope.Allowed[segment.SequenceNo] = struct{}{}
			scope.TranscriptText[segment.SequenceNo] = segment.Text
			scope.Segments[segment.SequenceNo] = segment
			if segment.SequenceNo > scope.CoveredThrough {
				scope.CoveredThrough = segment.SequenceNo
			}
		}
		applyAdjacentFinalSegmentStitches(&scope, segments)
		classifyLiveRoundInputs(&scope, previous, roundSegments)
		merged, err := parseAndMergeLiveAnalysisPayloadWithEvidence(
			string(round.FixedAIResponse), raw, context, int64(index+1),
			append([]int64(nil), round.SequenceNos...), scope, cfg,
		)
		if err != nil {
			result.Error = fmt.Sprintf("round %d merge: %v", index+1, err)
			return result
		}
		merged, err = addLiveAnalysisCoverage(merged, roundSegments)
		if err != nil {
			result.Error = fmt.Sprintf("round %d coverage: %v", index+1, err)
			return result
		}
		roundState := previousLiveAnalysisState(merged)
		visibleSegments := make([]domain.TranscriptSegment, 0, len(segments))
		for _, segment := range segments {
			if segment.SequenceNo <= maxRound {
				visibleSegments = append(visibleSegments, segment)
			}
		}
		result.HardInvariantViolations = append(
			result.HardInvariantViolations,
			qualityFutureEvidenceViolations(roundState, visibleSegments)...,
		)
		raw = merged
	}
	var repairStats finalRepairStats
	if scenario.ApplyFinalRepair {
		raw, repairStats = applyDeterministicFinalTreeRepairs(raw, context, int64(len(scenario.Rounds)+1), finalRepairInput{
			Segments: segments,
			Audit:    TreeAuditConfig{},
		})
		result.ProductionStages = append(result.ProductionStages, "finalization_repair")
	}
	projected := sanitizeLiveAnalysisForDelivery(&domain.MeetingAIAnalysis{
		SessionID: scenario.ID,
		Type:      domain.MeetingAIAnalysisLive,
		Payload:   raw,
	}, context, cfg)
	if projected != nil {
		raw = projected.Payload
	}
	result.ProductionStages = append(result.ProductionStages, "final_delivery_projection")
	state := previousLiveAnalysisState(raw)
	result.TreeVersion = state.TreeVersion
	result.FinalCoverage = state.CoveredThroughSequenceNo
	evaluateMeetingQualityResult(&result, scenario, state, context, segments)
	recordSingletonAttachmentMetrics(&result, scenario, state, repairStats)
	result.Passed = result.Error == "" &&
		len(result.HardInvariantViolations) == 0 &&
		len(result.MissingRequiredPropositions) == 0 &&
		len(result.RelationFailures) == 0 &&
		len(result.ForbiddenResultsFound) == 0 &&
		len(result.SafetyFailures) == 0
	return result
}

// recordSingletonAttachmentMetrics records the final singleton-attachment axes.
// singletonAttachmentWrongParentCount counts only attachments that landed in a
// topic already holding a proposition the scenario declares must stay
// separated, so a negative fixture proves the misattachment rather than only
// the aggregate count changing.
func recordSingletonAttachmentMetrics(
	result *MeetingQualityScenarioResult,
	scenario MeetingQualityScenario,
	state liveAnalysisPayload,
	stats finalRepairStats,
) {
	result.Metrics.SingletonAttachmentEligibleCount = stats.SingletonAttachmentEligible
	result.Metrics.SingletonAttachmentAppliedCount = stats.SingletonAttachmentApplied
	result.Metrics.SingletonAttachmentDeferredCount = stats.SingletonAttachmentDeferred
	result.Metrics.SingletonAttachmentAmbiguousCount = stats.SingletonAttachmentAmbiguous
	result.Metrics.UnclassifiedGroundedSingletonCount = unclassifiedGroundedSingletonCount(state)
	if len(scenario.RequiredParentSeparations) == 0 || state.Tree == nil {
		return
	}
	propositionByItem := make(map[string]string, len(result.PropositionMatches))
	itemByProposition := make(map[string]string, len(result.PropositionMatches))
	for _, match := range result.PropositionMatches {
		if !match.Matched || match.BestActualCandidate == nil {
			continue
		}
		propositionByItem[match.BestActualCandidate.ID] = match.PropositionID
		itemByProposition[match.PropositionID] = match.BestActualCandidate.ID
	}
	for _, decision := range stats.SingletonAttachmentDecisions {
		if decision.Decision != singletonAttachmentApplied {
			continue
		}
		propositionID, known := propositionByItem[decision.ItemID]
		if !known {
			continue
		}
		for _, separation := range scenario.RequiredParentSeparations {
			counterpart := ""
			switch propositionID {
			case separation.From:
				counterpart = separation.To
			case separation.To:
				counterpart = separation.From
			default:
				continue
			}
			counterpartItemID, matched := itemByProposition[counterpart]
			if !matched || treeItemTopic(state.Tree, counterpartItemID) != decision.TargetTopicID {
				continue
			}
			result.Metrics.SingletonAttachmentWrongParentCount++
			result.MetricEvidence = append(result.MetricEvidence, MeetingQualityMetricEvidence{
				Metric:         "singletonAttachmentWrongParentCount",
				ExpectationIDs: []string{propositionID, counterpart},
				ActualItemIDs:  []string{decision.ItemID, counterpartItemID},
				Reason:         "singleton_attached_into_a_topic_required_to_stay_separate",
			})
		}
	}
}

func validateOneMeetingQualityScenario(scenario MeetingQualityScenario) error {
	if strings.TrimSpace(scenario.ID) == "" {
		return fmt.Errorf("scenario id is empty")
	}
	if len(scenario.TranscriptSegments) == 0 || len(scenario.Rounds) == 0 {
		return fmt.Errorf("scenario must include transcript segments and fixed rounds")
	}
	sequenceNos := make(map[int64]struct{}, len(scenario.TranscriptSegments))
	for _, segment := range scenario.TranscriptSegments {
		if segment.SequenceNo <= 0 || strings.TrimSpace(segment.Text) == "" {
			return fmt.Errorf("invalid transcript segment sequence=%d", segment.SequenceNo)
		}
		if _, duplicate := sequenceNos[segment.SequenceNo]; duplicate {
			return fmt.Errorf("duplicate transcript sequence %d", segment.SequenceNo)
		}
		sequenceNos[segment.SequenceNo] = struct{}{}
	}
	for index, round := range scenario.Rounds {
		if len(round.SequenceNos) == 0 || len(round.FixedAIResponse) == 0 {
			return fmt.Errorf("round %d is incomplete", index+1)
		}
	}
	return nil
}

func qualityDomainSegments(scenario MeetingQualityScenario) []domain.TranscriptSegment {
	segments := make([]domain.TranscriptSegment, 0, len(scenario.TranscriptSegments))
	for _, value := range scenario.TranscriptSegments {
		callID := strings.TrimSpace(value.CallID)
		if callID == "" {
			callID = "quality-call"
		}
		isFinal := true
		if value.IsFinal != nil {
			isFinal = *value.IsFinal
		}
		segments = append(segments, domain.TranscriptSegment{
			SessionID:   scenario.ID,
			EventID:     fmt.Sprintf("%s-seq-%d", scenario.ID, value.SequenceNo),
			CallID:      callID,
			SequenceNo:  value.SequenceNo,
			SpeakerName: value.Speaker,
			Text:        value.Text,
			IsFinal:     isFinal,
		})
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i].SequenceNo < segments[j].SequenceNo })
	return segments
}

func qualityMeetingContext(value MeetingQualityMeetingContext) *meetingContext {
	context := &meetingContext{
		Title: value.Title, Purpose: value.Purpose, Background: value.Background,
		Directives: append([]string(nil), value.Directives...),
	}
	for index, value := range value.Agenda {
		order := value.Order
		if order <= 0 {
			order = index + 1
		}
		id := strings.TrimSpace(value.ID)
		if id == "" {
			id = fmt.Sprintf("%s%d", agendaIDPrefix, order)
		}
		context.Agenda = append(context.Agenda, agendaItem{
			ID: id, Title: value.Title, Description: value.Description, Goal: value.Goal,
			SemanticHints: append([]string(nil), value.SemanticHints...), Order: order, Role: value.Role,
		})
	}
	return context
}

func evaluateMeetingQualityResult(
	result *MeetingQualityScenarioResult,
	scenario MeetingQualityScenario,
	state liveAnalysisPayload,
	context *meetingContext,
	segments []domain.TranscriptSegment,
) {
	if result == nil {
		return
	}
	integrity := validateTreeIntegrity(state.Tree, state.Items, context, state.AgendaAnchors)
	result.HardInvariantViolations = append(result.HardInvariantViolations, qualityIntegrityViolations(integrity)...)
	result.HardInvariantViolations = append(result.HardInvariantViolations, qualityMissingEdgeEndpoints(state.Tree)...)
	result.HardInvariantViolations = append(result.HardInvariantViolations, qualityActiveItemsOutsideTree(state)...)
	semanticSplitSourceActive, semanticSplitDuplicates, semanticSplitMissing, semanticSplitViolations := qualitySemanticSplitState(state)
	result.HardInvariantViolations = append(result.HardInvariantViolations, semanticSplitViolations...)

	activeItems, nodes := qualityActiveItems(state)
	evidenceScope := qualityFullEvidenceScope(segments)
	matches := qualityMatchRequiredPropositions(scenario.RequiredPropositions, activeItems, nodes)
	for _, expectation := range scenario.RequiredPropositions {
		match := matches[expectation.ID]
		detail := MeetingQualityPropositionMatch{
			PropositionID:             expectation.ID,
			ExpectedText:              expectation.Text,
			RequiredKind:              expectation.RequiredKind,
			AllowedKinds:              append([]string(nil), expectation.AllowedKinds...),
			ExpectedEvidence:          append([]int64(nil), expectation.EvidenceSequenceNos...),
			RequiredTemporalScope:     expectation.RequiredTemporalScope,
			RequiredEpistemicStatus:   expectation.RequiredEpistemicStatus,
			RequiredStatus:            expectation.RequiredStatus,
			RequiredDescriptionStatus: expectation.RequiredDescriptionStatus,
			Matched:                   match.Found,
			Similarity:                match.Score,
		}
		if strings.TrimSpace(match.Item.ID) != "" {
			actual := qualityActualItem(match.Item)
			detail.BestActualCandidate = &actual
		}
		result.PropositionMatches = append(result.PropositionMatches, detail)
		if !match.Found {
			result.MissingRequiredPropositions = append(result.MissingRequiredPropositions, expectation.ID)
			result.HardInvariantViolations = append(result.HardInvariantViolations, "missing_required_proposition:"+expectation.ID)
			continue
		}
		allowed := qualityAllowedKinds(expectation)
		if len(allowed) > 0 && !allowed[match.Item.Kind] {
			result.KindMismatches = append(result.KindMismatches, MeetingQualityKindMismatch{
				PropositionID: expectation.ID,
				ExpectedKinds: qualitySortedKindSet(allowed),
				ActualItemID:  match.Item.ID,
				ActualKind:    match.Item.Kind,
			})
		}
		features := inferItemSemanticFeatures(match.Item, evidenceScope)
		for _, expected := range []struct {
			field  string
			value  string
			actual string
		}{
			{field: "temporalScope", value: expectation.RequiredTemporalScope, actual: features.TemporalScope},
			{field: "epistemicStatus", value: expectation.RequiredEpistemicStatus, actual: features.EpistemicStatus},
			{field: "status", value: expectation.RequiredStatus, actual: qualityItemStatus(match.Item)},
			{field: "descriptionStatus", value: expectation.RequiredDescriptionStatus, actual: qualityDescriptionStatus(match.Item)},
		} {
			if strings.TrimSpace(expected.value) == "" || strings.EqualFold(strings.TrimSpace(expected.value), strings.TrimSpace(expected.actual)) {
				continue
			}
			result.SemanticStateMismatches = append(result.SemanticStateMismatches, MeetingQualitySemanticStateMismatch{
				PropositionID: expectation.ID,
				ActualItemID:  match.Item.ID,
				Field:         expected.field,
				Expected:      strings.TrimSpace(expected.value),
				Actual:        strings.TrimSpace(expected.actual),
			})
			result.HardInvariantViolations = append(result.HardInvariantViolations,
				fmt.Sprintf("semantic_state_mismatch:%s:%s:%s!=%s", expectation.ID, expected.field,
					strings.TrimSpace(expected.actual), strings.TrimSpace(expected.value)))
		}
		for _, sequenceNo := range expectation.EvidenceSequenceNos {
			if !containsInt64(match.Item.EvidenceSequenceNos, sequenceNo) {
				result.EvidenceMismatches = append(result.EvidenceMismatches, MeetingQualityEvidenceMismatch{
					PropositionID:    expectation.ID,
					ActualItemID:     match.Item.ID,
					ExpectedSequence: sequenceNo,
					ActualSequences:  append([]int64(nil), match.Item.EvidenceSequenceNos...),
				})
				result.HardInvariantViolations = append(result.HardInvariantViolations,
					fmt.Sprintf("required_evidence_mismatch:%s:%d", expectation.ID, sequenceNo))
			}
		}
		if agendaID := strings.TrimSpace(expectation.RequiredAgendaID); agendaID != "" &&
			!qualityNodeReferencesAgenda(state.Tree, match.Node, agendaID) {
			result.HardInvariantViolations = append(result.HardInvariantViolations,
				"required_agenda_mismatch:"+expectation.ID+":"+agendaID)
		}
	}
	result.RequiredParentSeparations = append(
		[]MeetingQualityParentSeparation(nil), scenario.RequiredParentSeparations...,
	)
	for _, separation := range scenario.RequiredParentSeparations {
		from, to := matches[separation.From], matches[separation.To]
		if !from.Found || !to.Found {
			continue
		}
		_, fromTop := qualityParentPath(state.Tree, from.Item.ID)
		_, toTop := qualityParentPath(state.Tree, to.Item.ID)
		if fromTop == "" || toTop == "" || fromTop == toTop {
			result.HardInvariantViolations = append(result.HardInvariantViolations,
				fmt.Sprintf("required_parent_separation_mismatch:%s:%s", separation.From, separation.To))
		}
	}
	result.Metrics, result.MetricEvidence = qualityMetrics(scenario, state, context, segments, activeItems, nodes, matches)
	result.Metrics.SemanticSplitSourceActiveCount = semanticSplitSourceActive
	result.Metrics.SemanticSplitSourceFragmentDuplicateCount = semanticSplitDuplicates
	result.Metrics.SemanticSplitReplacementMissingCount = semanticSplitMissing
	result.Metrics.RecapExistingItemUpdateCount = len(uniqueNonEmptyIDs(state.RecapSpan.MatchedExistingItemIDs))
	result.Metrics.RecapNewInformationItemCount = state.RecapSpan.NewInformationAtoms
	result.UnsupportedPropositions = qualityUnsupportedPropositions(activeItems, context, segments)
	if len(result.UnsupportedPropositions) > 0 {
		itemByID := make(map[string]liveAnalysisItem, len(activeItems))
		for _, item := range activeItems {
			itemByID[item.ID] = item
		}
		for _, id := range result.UnsupportedPropositions {
			result.HardInvariantViolations = append(result.HardInvariantViolations, "unsupported_central_proposition:"+id)
			if item, exists := itemByID[id]; exists {
				result.UnsupportedItems = append(result.UnsupportedItems, qualityActualItem(item))
			}
		}
	}
	result.Metrics.UnsupportedPropositionCount = len(result.UnsupportedPropositions)
	result.HardInvariantViolations = append(result.HardInvariantViolations, qualityInactiveResurrections(state)...)
	result.HardInvariantViolations = append(result.HardInvariantViolations, qualityFutureEvidenceViolations(state, segments)...)
	if state.CoveredThroughSequenceNo < scenario.FinalCoverage {
		result.HardInvariantViolations = append(result.HardInvariantViolations,
			fmt.Sprintf("final_coverage:%d<%d", state.CoveredThroughSequenceNo, scenario.FinalCoverage))
	}

	result.RelationFailures = qualityRelationFailures(scenario.RequiredRelations, matches, state.Tree)
	result.ForbiddenResultsFound = qualityForbiddenResults(scenario.ForbiddenResults, state, activeItems, nodes)
	result.SafetyFailures = qualitySafetyFailures(
		scenario.SafetyExpectations,
		matches,
		scenario.SeedPayload,
	)
	result.ParentAssignments = qualityParentAssignments(matches, state.Tree)
	result.KindDistribution = qualityKindDistribution(activeItems)

	result.HardInvariantViolations = qualityUniqueSortedStrings(result.HardInvariantViolations)
	result.MissingRequiredPropositions = qualityUniqueSortedStrings(result.MissingRequiredPropositions)
	result.UnsupportedPropositions = qualityUniqueSortedStrings(result.UnsupportedPropositions)
	result.RelationFailures = qualityUniqueSortedStrings(result.RelationFailures)
	result.ForbiddenResultsFound = qualityUniqueSortedStrings(result.ForbiddenResultsFound)
	result.SafetyFailures = qualityUniqueSortedStrings(result.SafetyFailures)
}

func qualityActiveItems(state liveAnalysisPayload) ([]liveAnalysisItem, map[string]liveAnalysisTreeNode) {
	nodes := make(map[string]liveAnalysisTreeNode)
	if state.Tree != nil {
		for _, node := range state.Tree.Nodes {
			nodes[node.ID] = node
		}
	}
	items := make([]liveAnalysisItem, 0, len(state.Items))
	for _, item := range state.Items {
		if item.Inactive || item.MergedIntoID != "" {
			continue
		}
		if _, inTree := nodes[item.ID]; !inTree {
			continue
		}
		items = append(items, item)
	}
	return items, nodes
}

func qualityActiveItemsOutsideTree(state liveAnalysisPayload) []string {
	nodes := make(map[string]struct{})
	if state.Tree != nil {
		for _, node := range state.Tree.Nodes {
			nodes[node.ID] = struct{}{}
		}
	}
	var violations []string
	for _, item := range state.Items {
		if item.Inactive || item.MergedIntoID != "" {
			continue
		}
		if _, exists := nodes[item.ID]; !exists {
			violations = append(violations, "active_item_missing_tree_node:"+item.ID)
		}
	}
	return violations
}

func qualityMatchRequiredPropositions(
	expectations []MeetingQualityProposition,
	items []liveAnalysisItem,
	nodes map[string]liveAnalysisTreeNode,
) map[string]meetingQualityMatch {
	matches := make(map[string]meetingQualityMatch, len(expectations))
	for _, expectation := range expectations {
		threshold := expectation.MinimumSimilarity
		if threshold <= 0 {
			threshold = defaultPropositionMatchSimilarity
		}
		best := meetingQualityMatch{Expectation: expectation}
		for _, item := range items {
			score := qualityPropositionSimilarity(expectation.Text, item.Title+" "+item.Body)
			if titleScore := qualityPropositionSimilarity(expectation.Text, item.Title); titleScore > score {
				score = titleScore
			}
			if bodyScore := qualityPropositionSimilarity(expectation.Text, item.Body); bodyScore > score {
				score = bodyScore
			}
			if score > best.Score {
				best.Score = score
				best.Item = item
				best.Node = nodes[item.ID]
			}
		}
		best.Found = best.Score >= threshold
		matches[expectation.ID] = best
	}
	return matches
}

func qualityPropositionSimilarity(expected, actual string) float64 {
	expectedKey := normalizeForMatch(expected)
	actualKey := normalizeForMatch(actual)
	if expectedKey == "" || actualKey == "" {
		return 0
	}
	expectedSemantics := qualitySemanticRepresentationOf(expected)
	actualSemantics := qualitySemanticRepresentationOf(actual)
	if qualitySemanticFacetConflicts(expectedSemantics.Qualifiers, actualSemantics.Qualifiers) ||
		qualitySemanticStatusConflicts(expectedSemantics.EpistemicStatus, actualSemantics.EpistemicStatus) ||
		qualitySemanticFacetConflicts(expectedSemantics.Predicates, actualSemantics.Predicates) {
		return 0
	}
	similarity := semanticItemSimilarity(expected, actual)
	if bigram := qualityBigramDice(expectedKey, actualKey); bigram > similarity {
		similarity = bigram
	}
	if strings.Contains(actualKey, expectedKey) || strings.Contains(expectedKey, actualKey) {
		shorter, longer := len([]rune(expectedKey)), len([]rune(actualKey))
		if shorter > longer {
			shorter, longer = longer, shorter
		}
		containment := 0.70 + 0.30*float64(shorter)/float64(longer)
		if containment > similarity {
			similarity = containment
		}
	}
	if !sharedTreeAuditSubjectTerm(expected, actual) && similarity < 0.62 {
		return 0
	}
	return similarity
}

type qualitySemanticRepresentation struct {
	SubjectTerms    []string
	Predicates      []string
	Objects         []string
	Qualifiers      []string
	TemporalScope   []string
	EpistemicStatus string
}

func qualitySemanticRepresentationOf(text string) qualitySemanticRepresentation {
	normalized := normalizeForMatch(text)
	result := qualitySemanticRepresentation{
		SubjectTerms: qualityDistinctiveNgrams(normalized),
		Qualifiers:   qualityUniqueSortedStrings(qualitySemanticQualifierPattern.FindAllString(strings.ToLower(text), -1)),
	}
	predicateFamilies := []struct {
		Name    string
		Markers []string
	}{
		{"expire", []string{"失効", "期限切れ", "有効期限"}},
		{"update", []string{"更新", "更改"}},
		{"investigate", []string{"調査", "解析", "詳しく調べ"}},
		{"confirm", []string{"確認", "検証"}},
		{"decide", []string{"決定", "決め"}},
		{"recover", []string{"復旧", "回復", "直り"}},
		{"outage", []string{"障害", "停止", "接続不能", "接続できな"}},
		{"missing", []string{"漏れ", "不足", "欠落"}},
		{"rollback", []string{"切り戻", "ロールバック"}},
		{"support", []string{"支持", "裏付け", "原因"}},
		{"limit", []string{"範囲", "限定", "説明できるか"}},
	}
	for _, family := range predicateFamilies {
		for _, marker := range family.Markers {
			if strings.Contains(text, marker) {
				result.Predicates = append(result.Predicates, family.Name)
				break
			}
		}
	}
	switch {
	case strings.Contains(text, "未確認") || strings.Contains(text, "未特定") ||
		strings.Contains(text, "仮説") || strings.Contains(text, "可能性"):
		result.EpistemicStatus = "uncertain"
	case strings.Contains(text, "確認しました") || strings.Contains(text, "判明") ||
		strings.Contains(text, "決定しました") || strings.Contains(text, "発生しています"):
		result.EpistemicStatus = "asserted"
	}
	for _, qualifier := range result.Qualifiers {
		if strings.ContainsAny(qualifier, "曜明昨今来先週月日時日年") {
			result.TemporalScope = append(result.TemporalScope, qualifier)
		} else {
			result.Objects = append(result.Objects, qualifier)
		}
	}
	result.Predicates = qualityUniqueSortedStrings(result.Predicates)
	return result
}

func qualityDistinctiveNgrams(value string) []string {
	runes := []rune(value)
	var result []string
	for size := 4; size >= 3; size-- {
		for index := 0; index+size <= len(runes); index++ {
			part := string(runes[index : index+size])
			if strings.Contains("調査確認決定対応実施予定結果方法事項", part) {
				continue
			}
			result = append(result, part)
		}
		if len(result) > 0 {
			break
		}
	}
	return qualityUniqueSortedStrings(result)
}

func qualitySemanticFacetConflicts(expected, actual []string) bool {
	if len(expected) == 0 || len(actual) == 0 {
		return false
	}
	for _, left := range expected {
		if containsExactString(actual, left) {
			return false
		}
	}
	return true
}

func qualitySemanticStatusConflicts(expected, actual string) bool {
	return expected != "" && actual != "" && expected != actual
}

func qualityBigramDice(left, right string) float64 {
	leftRunes, rightRunes := []rune(left), []rune(right)
	if len(leftRunes) < 2 || len(rightRunes) < 2 {
		return 0
	}
	count := func(value []rune) map[string]int {
		result := make(map[string]int, len(value)-1)
		for index := 0; index+1 < len(value); index++ {
			result[string(value[index:index+2])]++
		}
		return result
	}
	leftPairs, rightPairs := count(leftRunes), count(rightRunes)
	overlap := 0
	for pair, leftCount := range leftPairs {
		rightCount := rightPairs[pair]
		if rightCount < leftCount {
			leftCount = rightCount
		}
		overlap += leftCount
	}
	return float64(2*overlap) / float64(len(leftRunes)-1+len(rightRunes)-1)
}

func qualityMetrics(
	scenario MeetingQualityScenario,
	state liveAnalysisPayload,
	context *meetingContext,
	segments []domain.TranscriptSegment,
	activeItems []liveAnalysisItem,
	nodes map[string]liveAnalysisTreeNode,
	matches map[string]meetingQualityMatch,
) (MeetingQualityMetrics, []MeetingQualityMetricEvidence) {
	metrics := MeetingQualityMetrics{}
	var metricEvidence []MeetingQualityMetricEvidence
	segmentBySequence := make(map[int64]domain.TranscriptSegment, len(segments))
	for _, segment := range segments {
		segmentBySequence[segment.SequenceNo] = segment
	}
	evidenceScope := qualityFullEvidenceScope(segments)
	found := 0
	classificationTotal, classificationCorrect := 0, 0
	temporalTotal, temporalCorrect := 0, 0
	kindExpected := map[string]int{"risk": 0, "todo": 0, "decision": 0}
	kindFound := map[string]int{"risk": 0, "todo": 0, "decision": 0}
	for _, expectation := range scenario.RequiredPropositions {
		match := matches[expectation.ID]
		if match.Found {
			found++
		}
		allowed := qualityAllowedKinds(expectation)
		if len(allowed) > 0 {
			classificationTotal++
			if match.Found && allowed[match.Item.Kind] {
				classificationCorrect++
			} else if match.Found {
				metricEvidence = append(metricEvidence, MeetingQualityMetricEvidence{
					Metric:         "classificationAccuracy",
					ExpectationIDs: []string{expectation.ID},
					ActualItemIDs:  []string{match.Item.ID},
					ActualLabels:   []string{match.Item.Title},
					Reason:         fmt.Sprintf("expected kind %v, actual kind %s", qualitySortedKindSet(allowed), match.Item.Kind),
				})
			}
		}
		if expectedTemporal := strings.TrimSpace(expectation.RequiredTemporalScope); expectedTemporal != "" {
			temporalTotal++
			if match.Found {
				actualTemporal := inferItemSemanticFeatures(match.Item, evidenceScope).TemporalScope
				if strings.EqualFold(expectedTemporal, actualTemporal) {
					temporalCorrect++
				} else {
					metricEvidence = append(metricEvidence, MeetingQualityMetricEvidence{
						Metric:         "temporalScopeAccuracy",
						ExpectationIDs: []string{expectation.ID},
						ActualItemIDs:  []string{match.Item.ID},
						ActualLabels:   []string{match.Item.Title},
						Reason:         fmt.Sprintf("expected temporal scope %s, actual %s", expectedTemporal, actualTemporal),
					})
				}
			}
		}
		for kind := range allowed {
			if _, tracked := kindExpected[kind]; tracked {
				kindExpected[kind]++
				if match.Found && match.Item.Kind == kind {
					kindFound[kind]++
				}
			}
		}
	}
	metrics.RequiredPropositionRecall = qualityRatio(found, len(scenario.RequiredPropositions))
	metrics.ClassificationAccuracy = qualityRatio(classificationCorrect, classificationTotal)
	if temporalTotal > 0 {
		metrics.TemporalScopeAccuracy = qualityRatio(temporalCorrect, temporalTotal)
	}
	metrics.RiskRecall = qualityRatio(kindFound["risk"], kindExpected["risk"])
	metrics.TodoRecall = qualityRatio(kindFound["todo"], kindExpected["todo"])
	metrics.DecisionRecall = qualityRatio(kindFound["decision"], kindExpected["decision"])

	for left := 0; left < len(activeItems); left++ {
		for right := left + 1; right < len(activeItems); right++ {
			if distinctCorrectionFactClauses(activeItems[left], activeItems[right]) ||
				distinctRecoveryFactClauses(activeItems[left], activeItems[right]) ||
				len(semanticKindRelations(activeItems[left], activeItems[right], evidenceScope)) > 0 {
				continue
			}
			duplicate, _ := sameKindSemanticDuplicate(activeItems[left], activeItems[right])
			similarity := qualityPropositionSimilarity(
				activeItems[left].Title+" "+activeItems[left].Body,
				activeItems[right].Title+" "+activeItems[right].Body,
			)
			if duplicate || similarity >= 0.88 ||
				(itemEvidenceOverlaps(activeItems[left], activeItems[right]) && similarity >= 0.40) {
				metrics.SemanticDuplicateCount++
				if itemHasEvidenceRole(activeItems[left], evidenceScope, liveEvidenceReferenceRecap) ||
					itemHasEvidenceRole(activeItems[right], evidenceScope, liveEvidenceReferenceRecap) {
					metrics.RecapDuplicateItemCount++
				}
				itemIDs := []string{activeItems[left].ID, activeItems[right].ID}
				metricEvidence = append(metricEvidence, MeetingQualityMetricEvidence{
					Metric:         "semanticDuplicateCount",
					ExpectationIDs: qualityExpectationIDsForItems(matches, itemIDs),
					ActualItemIDs:  itemIDs,
					ActualLabels:   []string{activeItems[left].Title, activeItems[right].Title},
					Reason:         fmt.Sprintf("same proposition pair similarity=%.3f", similarity),
				})
			}
		}
	}
	for _, item := range activeItems {
		features := inferItemSemanticFeatures(item, evidenceScope)
		if item.Kind == "fact" && features.TemporalScope == "past" {
			metrics.PastFactCount++
		}
		if item.Kind == "issue" {
			metrics.IssueCount++
			if qualityItemStatus(item) == "resolved" {
				metrics.ResolvedIssueCount++
				text := item.Title + " " + item.Body
				if features.TemporalScope == "past" && kindPastObservationPattern.MatchString(text) &&
					!kindExplicitCurrentIssuePattern.MatchString(text) {
					metrics.IncorrectResolvedIssueCount++
				}
			}
		}
		if finalItemIsLowInformation(item) {
			metrics.LowInformationLabelCount++
		}
		if liveItemTextNeedsReferent(item) {
			metrics.ContextDependentLabelCount++
		}
		if len([]rune(item.Title)) >= liveAnalysisItemLabelPreferredMaxRunes && incompleteItemLabelEnding(item) != "" {
			metrics.TruncatedLabelCount++
		}
		if labelDescriptionExactDuplicate(item) {
			metrics.LabelDescriptionExactDuplicateCount++
			metricEvidence = append(metricEvidence, MeetingQualityMetricEvidence{
				Metric: "label_description_exact_duplicate_count", ActualItemIDs: []string{item.ID},
				ActualLabels: []string{item.Title}, Reason: "normalized label and description are identical",
			})
		}
		if labelDescriptionHighSimilarity(item) {
			metrics.LabelDescriptionHighSimilarityCount++
		}
		unsupportedDescriptionAtoms := descriptionUnsupportedAtomCount(item, segmentBySequence)
		metrics.DescriptionUnsupportedAtomCount += unsupportedDescriptionAtoms
		if unsupportedDescriptionAtoms > 0 {
			metricEvidence = append(metricEvidence, MeetingQualityMetricEvidence{
				Metric: "description_unsupported_atom_count", ActualItemIDs: []string{item.ID},
				ActualLabels: []string{item.Title}, Reason: fmt.Sprintf("unsupported description atoms=%d", unsupportedDescriptionAtoms),
			})
		}
		if descriptionAddsGroundedDetail(item, segmentBySequence) {
			metrics.DescriptionAddedGroundedDetailCount++
		}
		if descriptionRedundant(item) {
			metrics.DescriptionRedundantCount++
		}
	}
	copyCount := 0
	compressionTotal := 0.0
	compressionCount := 0
	for _, item := range activeItems {
		if labelCopiesTranscript(item, segmentBySequence) {
			copyCount++
		}
		if ratio, ok := labelCompressionForItem(item, segmentBySequence); ok {
			compressionTotal += ratio
			compressionCount++
		}
	}
	metrics.LabelTranscriptCopyRatio = qualityRatio(copyCount, len(activeItems))
	if compressionCount > 0 {
		metrics.LabelCompressionRatio = compressionTotal / float64(compressionCount)
	}
	roles := make(map[int64]treeAuditEvidenceRole, len(segments))
	for _, segment := range segments {
		roles[segment.SequenceNo] = treeAuditEvidencePrimary
	}
	findings := deterministicTreeAuditPrecheck(state, context, roles, TreeAuditConfig{})
	metrics.CandidateFragmentationCount = countTreeAuditPrechecks(findings,
		TreeAuditCandidateFragmentation, TreeAuditRiskTodoSubjectFragmentation,
		TreeAuditRelatedActionOutsideRiskTopic, TreeAuditCandidateMixedSubjects)
	metrics.CrossAgendaContaminationCount = countTreeAuditPrechecks(findings, TreeAuditCrossAgendaContamination)
	for _, finding := range findings {
		switch finding.Type {
		case TreeAuditCandidateFragmentation, TreeAuditRiskTodoSubjectFragmentation,
			TreeAuditRelatedActionOutsideRiskTopic, TreeAuditCandidateMixedSubjects:
			metricEvidence = append(metricEvidence, MeetingQualityMetricEvidence{
				Metric:         "candidateFragmentationCount",
				ExpectationIDs: qualityExpectationIDsForItems(matches, finding.NodeIDs),
				ActualItemIDs:  append([]string(nil), finding.NodeIDs...),
				ActualLabels:   qualityLabelsForItemIDs(activeItems, finding.NodeIDs),
				Reason:         string(finding.Type) + ": " + finding.Reason,
			})
		case TreeAuditCrossAgendaContamination:
			metricEvidence = append(metricEvidence, MeetingQualityMetricEvidence{
				Metric:         "crossAgendaContaminationCount",
				ExpectationIDs: qualityExpectationIDsForItems(matches, finding.NodeIDs),
				ActualItemIDs:  append([]string(nil), finding.NodeIDs...),
				ActualLabels:   qualityLabelsForItemIDs(activeItems, finding.NodeIDs),
				Reason:         string(finding.Type) + ": " + finding.Reason,
			})
		}
	}
	relationFailures := qualityRelationFailures(scenario.RequiredRelations, matches, state.Tree)
	metrics.HierarchyRelationAccuracy = qualityRatio(len(scenario.RequiredRelations)-len(relationFailures), len(scenario.RequiredRelations))
	for _, failure := range relationFailures {
		metricEvidence = append(metricEvidence, MeetingQualityMetricEvidence{
			Metric: "hierarchyRelationAccuracy",
			Reason: failure,
		})
	}
	for _, expectation := range scenario.RequiredPropositions {
		allowed := qualityAllowedKinds(expectation)
		if allowed["risk"] {
			match := matches[expectation.ID]
			if !match.Found || match.Item.Kind != "risk" {
				evidence := MeetingQualityMetricEvidence{
					Metric:         "riskRecall",
					ExpectationIDs: []string{expectation.ID},
					Reason:         "required risk was missing or classified as another kind",
				}
				if match.Item.ID != "" {
					evidence.ActualItemIDs = []string{match.Item.ID}
					evidence.ActualLabels = []string{match.Item.Title}
				}
				metricEvidence = append(metricEvidence, evidence)
			}
		}
	}
	_ = nodes
	return metrics, metricEvidence
}

func itemHasEvidenceRole(item liveAnalysisItem, scope liveEvidenceScope, role liveEvidenceRole) bool {
	for _, sequenceNo := range item.EvidenceSequenceNos {
		if scope.EvidenceRoles[sequenceNo] == role {
			return true
		}
	}
	return false
}

func qualitySemanticSplitState(state liveAnalysisPayload) (activeSources, duplicates, missing int, violations []string) {
	for _, source := range state.Items {
		if source.SupersessionOrigin != "semantic_split" {
			continue
		}
		replacements := append([]string(nil), source.SupersededByItemIDs...)
		if len(replacements) == 0 && source.SupersededByItemID != "" {
			replacements = append(replacements, source.SupersededByItemID)
		}
		if !source.Inactive || source.InformationStatus != "superseded" {
			activeSources++
			violations = append(violations, "semantic_split_source_active:"+source.ID)
		}
		activeReplacementCount := 0
		for _, replacementID := range uniqueNonEmptyIDs(replacements) {
			replacement := findItemByID(state.Items, replacementID)
			if replacement == nil || replacement.Inactive || replacement.MergedIntoID != "" {
				continue
			}
			activeReplacementCount++
			if !source.Inactive && sameCanonicalProposition(source, *replacement) {
				duplicates++
				violations = append(violations,
					"semantic_split_source_fragment_duplicate:"+source.ID+":"+replacement.ID)
			}
		}
		if activeReplacementCount == 0 {
			missing++
			violations = append(violations, "semantic_split_replacement_missing:"+source.ID)
		}
	}
	return activeSources, duplicates, missing, violations
}

func qualityActualItem(item liveAnalysisItem) MeetingQualityActualItem {
	return MeetingQualityActualItem{
		ID:                  item.ID,
		Kind:                item.Kind,
		Title:               item.Title,
		Body:                item.Body,
		Status:              qualityItemStatus(item),
		EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...),
	}
}

func qualityItemStatus(item liveAnalysisItem) string {
	if status := strings.TrimSpace(item.Status); status != "" {
		return status
	}
	return "open"
}

func qualityExpectationIDsForItems(matches map[string]meetingQualityMatch, itemIDs []string) []string {
	wanted := make(map[string]struct{}, len(itemIDs))
	for _, id := range itemIDs {
		wanted[id] = struct{}{}
	}
	var result []string
	for expectationID, match := range matches {
		if _, exists := wanted[match.Item.ID]; exists {
			result = append(result, expectationID)
		}
	}
	sort.Strings(result)
	return result
}

func qualityLabelsForItemIDs(items []liveAnalysisItem, itemIDs []string) []string {
	byID := make(map[string]string, len(items))
	for _, item := range items {
		byID[item.ID] = item.Title
	}
	result := make([]string, 0, len(itemIDs))
	for _, id := range itemIDs {
		result = append(result, byID[id])
	}
	return result
}

func qualitySortedKindSet(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func qualityAllowedKinds(expectation MeetingQualityProposition) map[string]bool {
	kinds := make(map[string]bool)
	if kind := strings.TrimSpace(expectation.RequiredKind); kind != "" {
		kinds[kind] = true
	}
	for _, kind := range expectation.AllowedKinds {
		if kind = strings.TrimSpace(kind); kind != "" {
			kinds[kind] = true
		}
	}
	return kinds
}

func qualityUnsupportedPropositions(items []liveAnalysisItem, context *meetingContext, segments []domain.TranscriptSegment) []string {
	scope := qualityFullEvidenceScope(segments)
	catalog := buildGroundingContextCatalog(context, nil)
	var unsupported []string
	for _, item := range items {
		probe := item
		probe.evidenceSpecified = true
		decision, _ := evaluateItemGrounding(probe, scope, catalog, "quality_evaluation", probe.semanticSplitFragment)
		if decision.Decision != "accepted" && decision.Decision != "rewritten" {
			unsupported = append(unsupported, item.ID)
		}
	}
	return unsupported
}

func qualityFullEvidenceScope(segments []domain.TranscriptSegment) liveEvidenceScope {
	scope := newLiveEvidenceScope()
	for _, segment := range segments {
		if !segment.IsFinal || segment.SequenceNo <= 0 {
			continue
		}
		scope.Allowed[segment.SequenceNo] = struct{}{}
		scope.CurrentRound[segment.SequenceNo] = struct{}{}
		scope.FreshRound[segment.SequenceNo] = struct{}{}
		scope.TranscriptText[segment.SequenceNo] = segment.Text
		scope.Segments[segment.SequenceNo] = segment
		if segment.SequenceNo > scope.CoveredThrough {
			scope.CoveredThrough = segment.SequenceNo
		}
	}
	applyAdjacentFinalSegmentStitches(&scope, segments)
	timeline := classifyDiscourseTimeline(scope)
	scope.EvidenceRoles = timeline.Roles
	return scope
}

func qualityIntegrityViolations(value treeIntegrityDiagnostics) []string {
	var violations []string
	if value.RootCount != 1 {
		violations = append(violations, fmt.Sprintf("root_count:%d", value.RootCount))
	}
	appendIDs := func(prefix string, values []string) {
		for _, value := range values {
			violations = append(violations, prefix+":"+value)
		}
	}
	appendIDs("orphan_node", value.OrphanNodeIDs)
	appendIDs("duplicate_node_id", value.DuplicateNodeIDs)
	appendIDs("invalid_parent_kind", value.InvalidParentKindNodeIDs)
	appendIDs("self_parent", value.SelfParentNodeIDs)
	appendIDs("depth_limit", value.HardDepthNodeIDs)
	appendIDs("unknown_agenda_reference", value.UnknownAgendaRefs)
	appendIDs("orphan_agenda_reference", value.OrphanAgendaRefs)
	appendIDs("agenda_materialization_mismatch", value.AgendaMaterializationMismatches)
	appendIDs("duplicate_materialized_topic", value.DuplicateAgendaMaterializations)
	if !value.AgendaRecordIntegrityValid {
		violations = append(violations, "agenda_record_integrity")
	}
	if value.EdgeCountMismatch || value.EdgeParentMismatch {
		violations = append(violations, "tree_edge_parent_mismatch")
	}
	return violations
}

func qualityMissingEdgeEndpoints(tree *liveAnalysisTree) []string {
	if tree == nil {
		return nil
	}
	nodes := make(map[string]struct{}, len(tree.Nodes))
	for _, node := range tree.Nodes {
		nodes[node.ID] = struct{}{}
	}
	var violations []string
	for _, edge := range tree.Edges {
		if _, exists := nodes[edge.Source]; !exists {
			violations = append(violations, "missing_edge_endpoint:"+edge.Source)
		}
		if _, exists := nodes[edge.Target]; !exists {
			violations = append(violations, "missing_edge_endpoint:"+edge.Target)
		}
	}
	return violations
}

func qualityInactiveResurrections(state liveAnalysisPayload) []string {
	if state.Tree == nil {
		return nil
	}
	itemByID := make(map[string]liveAnalysisItem, len(state.Items))
	for _, item := range state.Items {
		itemByID[item.ID] = item
	}
	var violations []string
	for _, item := range state.Items {
		if (item.Inactive || item.MergedIntoID != "") && strings.EqualFold(strings.TrimSpace(item.Status), "resolved") {
			violations = append(violations, "superseded_item_resolved:"+item.ID)
		}
	}
	for _, node := range state.Tree.Nodes {
		item, exists := itemByID[node.ID]
		if exists && (item.Inactive || item.MergedIntoID != "") {
			violations = append(violations, "inactive_item_resurrected:"+item.ID)
		}
	}
	return violations
}

func qualityDescriptionStatus(item liveAnalysisItem) string {
	if item.DescriptionResolution == nil {
		return ""
	}
	return strings.TrimSpace(item.DescriptionResolution.Status)
}

func qualityFutureEvidenceViolations(state liveAnalysisPayload, segments []domain.TranscriptSegment) []string {
	known := make(map[int64]struct{}, len(segments))
	for _, segment := range segments {
		known[segment.SequenceNo] = struct{}{}
	}
	var violations []string
	for _, item := range state.Items {
		if item.Inactive || item.MergedIntoID != "" {
			continue
		}
		for _, sequenceNo := range item.EvidenceSequenceNos {
			if _, exists := known[sequenceNo]; !exists {
				violations = append(violations, fmt.Sprintf("future_evidence:%s:%d", item.ID, sequenceNo))
				continue
			}
		}
	}
	return violations
}

func qualityRelationFailures(relations []MeetingQualityRelation, matches map[string]meetingQualityMatch, tree *liveAnalysisTree) []string {
	var failures []string
	for _, relation := range relations {
		from := matches[relation.From]
		to := matches[relation.To]
		name := relation.From + ":" + relation.Kind + ":" + relation.To
		if !from.Found || !to.Found {
			failures = append(failures, name+":missing_proposition")
			continue
		}
		if qualityExplicitRelation(tree, from.Item.ID, to.Item.ID, relation.Kind) {
			continue
		}
		if !relation.RequireSameBranch && strings.TrimSpace(relation.RequiredAncestor) == "" {
			failures = append(failures, name+":missing_explicit_relation")
			continue
		}
		fromPath, fromTop := qualityParentPath(tree, from.Item.ID)
		toPath, toTop := qualityParentPath(tree, to.Item.ID)
		if relation.RequireSameBranch && (fromTop == "" || fromTop != toTop) {
			failures = append(failures, name+":different_branch")
			continue
		}
		if required := strings.TrimSpace(relation.RequiredAncestor); required != "" {
			pathText := strings.Join(append(fromPath, toPath...), " ")
			if qualityPropositionSimilarity(required, pathText) < defaultPropositionMatchSimilarity {
				failures = append(failures, name+":missing_ancestor")
			}
		}
	}
	return failures
}

func qualityExplicitRelation(tree *liveAnalysisTree, fromID, toID, kind string) bool {
	if tree == nil {
		return false
	}
	for _, relation := range tree.Relations {
		if relation.Source == fromID && relation.Target == toID && strings.EqualFold(strings.TrimSpace(relation.Kind), kind) {
			return true
		}
	}
	return false
}

func qualityParentPath(tree *liveAnalysisTree, itemID string) ([]string, string) {
	if tree == nil {
		return nil, ""
	}
	byID := make(map[string]liveAnalysisTreeNode, len(tree.Nodes))
	for _, node := range tree.Nodes {
		byID[node.ID] = node
	}
	node, exists := byID[itemID]
	if !exists {
		return nil, ""
	}
	var reverse []string
	top := ""
	seen := make(map[string]struct{})
	for node.ParentID != "" && node.ParentID != treeRootNodeID {
		if _, loop := seen[node.ParentID]; loop {
			break
		}
		seen[node.ParentID] = struct{}{}
		parent, exists := byID[node.ParentID]
		if !exists {
			break
		}
		reverse = append(reverse, parent.Label)
		node = parent
	}
	if node.ParentID == treeRootNodeID {
		top = node.ID
		if len(reverse) == 0 || reverse[len(reverse)-1] != node.Label {
			reverse = append(reverse, node.Label)
		}
	}
	for left, right := 0, len(reverse)-1; left < right; left, right = left+1, right-1 {
		reverse[left], reverse[right] = reverse[right], reverse[left]
	}
	return reverse, top
}

func qualityForbiddenResults(
	forbidden []MeetingQualityForbiddenResult,
	state liveAnalysisPayload,
	items []liveAnalysisItem,
	nodes map[string]liveAnalysisTreeNode,
) []string {
	var found []string
	for _, expectation := range forbidden {
		switch expectation.Type {
		case "proposition":
			for _, item := range items {
				if qualityForbiddenPropositionMatch(expectation.Text, item.Title+" "+item.Body) {
					found = append(found, "proposition:"+expectation.Text)
					break
				}
			}
		case "kind":
			for _, item := range items {
				if item.Kind == expectation.Kind {
					found = append(found, "kind:"+expectation.Kind)
					break
				}
			}
		case "agenda_assignment":
			for _, item := range items {
				if expectation.Text != "" && qualityPropositionSimilarity(expectation.Text, item.Title+" "+item.Body) < defaultPropositionMatchSimilarity {
					continue
				}
				node := nodes[item.ID]
				if qualityNodeReferencesAgenda(state.Tree, node, expectation.AgendaID) {
					found = append(found, "agenda_assignment:"+expectation.AgendaID+":"+item.ID)
				}
			}
		case "active_candidate":
			for _, candidate := range state.EmergingTopics {
				if candidate.Inactive {
					continue
				}
				if expectation.Text == "" || qualityPropositionSimilarity(expectation.Text, candidate.Label+" "+candidate.Description) >= defaultPropositionMatchSimilarity {
					found = append(found, "active_candidate:"+candidate.ID)
				}
			}
		case "zero_nodes":
			if state.Tree == nil || len(state.Tree.Nodes) == 0 {
				found = append(found, "zero_nodes")
			}
		case "semantic_duplicate":
			for left := 0; left < len(items); left++ {
				for right := left + 1; right < len(items); right++ {
					score := qualityPropositionSimilarity(items[left].Title+" "+items[left].Body, items[right].Title+" "+items[right].Body)
					if score >= 0.88 || (itemEvidenceOverlaps(items[left], items[right]) && score >= 0.40) {
						found = append(found, "semantic_duplicate:"+items[left].ID+":"+items[right].ID)
					}
				}
			}
		case "low_information":
			for _, item := range items {
				if finalItemIsLowInformation(item) {
					found = append(found, "low_information:"+item.ID)
				}
			}
		case "label_description_duplicate":
			for _, item := range items {
				if descriptionRedundant(item) {
					found = append(found, "label_description_duplicate:"+item.ID)
				}
			}
		case "context_dependent_label":
			for _, item := range items {
				if liveItemTextNeedsReferent(item) {
					found = append(found, "context_dependent_label:"+item.ID)
				}
			}
		case "bare_enumeration_label":
			// An active detail node whose label is only a noun list cannot be
			// understood on its own (§10). Reported per item so a regression
			// names the offending node.
			for _, item := range items {
				if evaluateItemLabelQuality(item).LabelIsBareEnumeration {
					found = append(found, "bare_enumeration_label:"+item.ID)
				}
			}
		case "unresolved_stt_noise_label":
			for _, item := range items {
				if evaluateItemLabelQuality(item).LabelContainsUnresolvedSTTNoise {
					found = append(found, "unresolved_stt_noise_label:"+item.ID)
				}
			}
		case "decision_issue_same_proposition":
			for _, decision := range items {
				if decision.Kind != "decision" {
					continue
				}
				for _, issue := range items {
					if issue.Kind != "issue" || !itemEvidenceOverlaps(decision, issue) {
						continue
					}
					if qualityPropositionSimilarity(decision.Title+" "+decision.Body, issue.Title+" "+issue.Body) >= 0.40 {
						found = append(found, "decision_issue_same_proposition:"+decision.ID+":"+issue.ID)
					}
				}
			}
		}
	}
	return found
}

func qualityForbiddenPropositionMatch(forbidden, actual string) bool {
	forbiddenKey := normalizeForMatch(forbidden)
	actualKey := normalizeForMatch(actual)
	if forbiddenKey == "" || actualKey == "" {
		return false
	}
	if strings.Contains(actualKey, forbiddenKey) {
		return true
	}
	// A forbidden outcome is a negative assertion: prefer a false negative
	// over rejecting a corrected proposition merely because most of its
	// subject is shared with the stale form. Broad paraphrase tolerance is
	// reserved for positive required-proposition matching.
	return qualityBigramDice(forbiddenKey, actualKey) >= 0.84
}

func qualityNodeReferencesAgenda(tree *liveAnalysisTree, node liveAnalysisTreeNode, agendaID string) bool {
	if tree == nil || strings.TrimSpace(agendaID) == "" {
		return false
	}
	byID := make(map[string]liveAnalysisTreeNode, len(tree.Nodes))
	for _, value := range tree.Nodes {
		byID[value.ID] = value
	}
	seen := make(map[string]struct{})
	for node.ID != "" {
		if containsExactString(node.AgendaRefs, agendaID) {
			return true
		}
		if _, loop := seen[node.ID]; loop {
			break
		}
		seen[node.ID] = struct{}{}
		node = byID[node.ParentID]
	}
	return false
}

func qualitySafetyFailures(
	expectations []MeetingQualitySafetyExpectation,
	matches map[string]meetingQualityMatch,
	seedPayload json.RawMessage,
) []string {
	seedMatches := make(map[string]meetingQualityMatch)
	if len(seedPayload) > 0 {
		seed := previousLiveAnalysisState(seedPayload)
		seedItems, seedNodes := qualityActiveItems(seed)
		required := make([]MeetingQualityProposition, 0, len(expectations))
		for _, expectation := range expectations {
			if match, exists := matches[expectation.PropositionID]; exists {
				required = append(required, match.Expectation)
			}
		}
		seedMatches = qualityMatchRequiredPropositions(required, seedItems, seedNodes)
	}
	var failures []string
	for _, expectation := range expectations {
		match := matches[expectation.PropositionID]
		seed := seedMatches[expectation.PropositionID]
		switch expectation.Principle {
		case "grounded_proposition_survives_label_failure":
			if !seed.Found || !match.Found || len(match.Item.EvidenceSequenceNos) == 0 {
				failures = append(failures, expectation.Principle+":"+expectation.PropositionID)
			}
		case "repair_failure_preserves_last_safe_representation",
			"new_repair_does_not_deactivate_valid_item",
			"temporary_validator_does_not_erase_durable_information":
			if !seed.Found || !match.Found || match.Item.Inactive || match.Item.MergedIntoID != "" {
				failures = append(failures, expectation.Principle+":"+expectation.PropositionID)
			}
		case "classification_change_preserves_proposition":
			if !seed.Found || !match.Found || seed.Item.Kind == match.Item.Kind ||
				qualityPropositionSimilarity(
					seed.Item.Title+" "+seed.Item.Body,
					match.Item.Title+" "+match.Item.Body,
				) < defaultPropositionMatchSimilarity {
				failures = append(failures, expectation.Principle+":"+expectation.PropositionID)
			}
		default:
			failures = append(failures, "unknown_safety_principle:"+expectation.Principle)
		}
	}
	return failures
}

func qualityParentAssignments(matches map[string]meetingQualityMatch, tree *liveAnalysisTree) []MeetingQualityParentAssignment {
	ids := make([]string, 0, len(matches))
	for id := range matches {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	assignments := make([]MeetingQualityParentAssignment, 0, len(ids))
	for _, id := range ids {
		match := matches[id]
		if !match.Found {
			continue
		}
		path, _ := qualityParentPath(tree, match.Item.ID)
		assignments = append(assignments, MeetingQualityParentAssignment{
			PropositionID: id,
			Kind:          match.Item.Kind,
			ParentPath:    path,
		})
	}
	return assignments
}

func qualityKindDistribution(items []liveAnalysisItem) []MeetingQualityKindCount {
	counts := make(map[string]int)
	for _, item := range items {
		counts[item.Kind]++
	}
	kinds := make([]string, 0, len(counts))
	for kind := range counts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	result := make([]MeetingQualityKindCount, 0, len(kinds))
	for _, kind := range kinds {
		result = append(result, MeetingQualityKindCount{Kind: kind, Count: counts[kind]})
	}
	return result
}

func qualityRatio(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 1
	}
	if numerator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func qualityUniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
