package application

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

const (
	meetingQualityMetricEpsilon         = 1e-9
	meetingQualityBaselineSchemaVersion = 4
)

var meetingQualityMetricSchemaV3 = []string{
	"requiredPropositionRecall", "unsupportedPropositionCount", "classificationAccuracy",
	"temporalScopeAccuracy", "pastFactCount", "issueCount", "resolvedIssueCount",
	"incorrectResolvedIssueCount", "riskRecall", "todoRecall", "decisionRecall",
	"semanticDuplicateCount", "lowInformationLabelCount", "contextDependentLabelCount",
	"truncatedLabelCount", "hierarchyRelationAccuracy", "candidateFragmentationCount",
	"crossAgendaContaminationCount",
}

type meetingQualityMetricValue struct {
	Name         string
	Value        float64
	HigherIsGood bool
	Exact        bool
}

func NewMeetingQualityBaseline(report MeetingQualitySuiteReport) MeetingQualityBaseline {
	return MeetingQualityBaseline{
		SchemaVersion: meetingQualityBaselineSchemaVersion,
		Suite:         report.Suite,
		MetricSchema:  qualityMetricNames(),
		Scenarios:     append([]MeetingQualityScenarioResult(nil), report.Scenarios...),
	}
}

func ValidateMeetingQualityBaseline(baseline MeetingQualityBaseline) error {
	if baseline.SchemaVersion != meetingQualityBaselineSchemaVersion {
		return fmt.Errorf("baseline schemaVersion=%d, want %d", baseline.SchemaVersion, meetingQualityBaselineSchemaVersion)
	}
	if strings.TrimSpace(baseline.Suite) == "" {
		return fmt.Errorf("baseline suite is empty")
	}
	if !reflect.DeepEqual(baseline.MetricSchema, qualityMetricNames()) {
		return fmt.Errorf("baseline metricSchema=%v, want %v", baseline.MetricSchema, qualityMetricNames())
	}
	if len(baseline.Scenarios) == 0 {
		return fmt.Errorf("baseline contains no scenarios")
	}
	seen := make(map[string]struct{}, len(baseline.Scenarios))
	for _, scenario := range baseline.Scenarios {
		id := strings.TrimSpace(scenario.ID)
		if id == "" {
			return fmt.Errorf("baseline contains an empty scenario id")
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("baseline contains duplicate scenario id %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// CompareMeetingQualityBaseline compares every quality axis independently.
// A gain in one metric can therefore never compensate for a loss in another.
func CompareMeetingQualityBaseline(
	baseline MeetingQualityBaseline,
	current MeetingQualitySuiteReport,
) MeetingQualityComparisonReport {
	comparison := MeetingQualityComparisonReport{Passed: true}
	if err := ValidateMeetingQualityBaseline(baseline); err != nil {
		comparison.Passed = false
		comparison.NewFailures = append(comparison.NewFailures, "invalid_baseline:"+err.Error())
		return comparison
	}
	if current.SchemaVersion != meetingQualitySchemaVersion {
		comparison.Passed = false
		comparison.NewFailures = append(comparison.NewFailures,
			fmt.Sprintf("current_schema_version:%d", current.SchemaVersion))
		return comparison
	}
	if current.Suite != baseline.Suite {
		comparison.Passed = false
		comparison.NewFailures = append(comparison.NewFailures,
			fmt.Sprintf("suite_mismatch:%s!=%s", current.Suite, baseline.Suite))
		return comparison
	}
	beforeByID := make(map[string]MeetingQualityScenarioResult, len(baseline.Scenarios))
	afterByID := make(map[string]MeetingQualityScenarioResult, len(current.Scenarios))
	for _, scenario := range baseline.Scenarios {
		beforeByID[scenario.ID] = scenario
	}
	for _, scenario := range current.Scenarios {
		afterByID[scenario.ID] = scenario
	}
	for id := range beforeByID {
		if _, exists := afterByID[id]; !exists {
			comparison.NewFailures = append(comparison.NewFailures, "missing_current_scenario:"+id)
			comparison.Passed = false
		}
	}
	for id, after := range afterByID {
		before, exists := beforeByID[id]
		if !exists {
			comparison.NewScenarios = append(comparison.NewScenarios, id)
			comparison.BaselineUpdateRequired = true
			comparison.Passed = false
			continue
		}
		if before.Passed && !after.Passed {
			comparison.NewFailures = append(comparison.NewFailures, id)
			comparison.Passed = false
		}
		if !before.Passed && after.Passed {
			comparison.RepairedScenarios = append(comparison.RepairedScenarios, id)
		}
		beforeMetrics := qualityMetricValues(before.Metrics)
		afterMetrics := qualityMetricValues(after.Metrics)
		for index := range beforeMetrics {
			left, right := beforeMetrics[index], afterMetrics[index]
			if qualityMetricImproved(left, right) {
				comparison.ImprovedMetrics = append(comparison.ImprovedMetrics, MeetingQualityMetricChange{
					Scenario: id, Metric: left.Name, Before: left.Value, After: right.Value,
				})
			}
			if qualityMetricWorsened(left, right) {
				comparison.WorsenedMetrics = append(comparison.WorsenedMetrics, MeetingQualityMetricChange{
					Scenario: id, Metric: left.Name, Before: left.Value, After: right.Value,
				})
				comparison.Passed = false
			}
		}
		if lost := qualityStringSetDifference(after.MissingRequiredPropositions, before.MissingRequiredPropositions); len(lost) > 0 {
			comparison.LostRequiredPropositions = append(comparison.LostRequiredPropositions,
				MeetingQualityTextDiff{Scenario: id, Values: lost})
			comparison.Passed = false
		}
		if added := qualityStringSetDifference(after.UnsupportedPropositions, before.UnsupportedPropositions); len(added) > 0 {
			comparison.NewUnsupportedPropositions = append(comparison.NewUnsupportedPropositions,
				MeetingQualityTextDiff{Scenario: id, Values: added})
			comparison.Passed = false
		}
		appendNewResultFailures(&comparison.NewHardInvariantViolations, id,
			before.HardInvariantViolations, after.HardInvariantViolations, &comparison.Passed)
		appendNewResultFailures(&comparison.NewRelationFailures, id,
			before.RelationFailures, after.RelationFailures, &comparison.Passed)
		appendNewResultFailures(&comparison.NewKindMismatches, id,
			qualityKindMismatchKeys(before.KindMismatches), qualityKindMismatchKeys(after.KindMismatches), &comparison.Passed)
		appendNewResultFailures(&comparison.NewEvidenceMismatches, id,
			qualityEvidenceMismatchKeys(before.EvidenceMismatches), qualityEvidenceMismatchKeys(after.EvidenceMismatches), &comparison.Passed)
		appendNewResultFailures(&comparison.NewSemanticStateMismatches, id,
			qualitySemanticStateMismatchKeys(before.SemanticStateMismatches), qualitySemanticStateMismatchKeys(after.SemanticStateMismatches), &comparison.Passed)
		if !reflect.DeepEqual(before.ParentAssignments, after.ParentAssignments) {
			comparison.ParentRelationDiffs = append(comparison.ParentRelationDiffs, MeetingQualityParentDiff{
				Scenario: id, Before: before.ParentAssignments, After: after.ParentAssignments,
			})
		}
		if !reflect.DeepEqual(before.KindDistribution, after.KindDistribution) {
			comparison.KindDistributionDiffs = append(comparison.KindDistributionDiffs, MeetingQualityKindDistributionDiff{
				Scenario: id, Before: before.KindDistribution, After: after.KindDistribution,
			})
		}
	}
	sort.Strings(comparison.NewFailures)
	sort.Strings(comparison.NewScenarios)
	sort.Strings(comparison.RepairedScenarios)
	sort.Slice(comparison.ImprovedMetrics, func(i, j int) bool {
		if comparison.ImprovedMetrics[i].Scenario == comparison.ImprovedMetrics[j].Scenario {
			return comparison.ImprovedMetrics[i].Metric < comparison.ImprovedMetrics[j].Metric
		}
		return comparison.ImprovedMetrics[i].Scenario < comparison.ImprovedMetrics[j].Scenario
	})
	sort.Slice(comparison.WorsenedMetrics, func(i, j int) bool {
		if comparison.WorsenedMetrics[i].Scenario == comparison.WorsenedMetrics[j].Scenario {
			return comparison.WorsenedMetrics[i].Metric < comparison.WorsenedMetrics[j].Metric
		}
		return comparison.WorsenedMetrics[i].Scenario < comparison.WorsenedMetrics[j].Scenario
	})
	sortMeetingQualityTextDiffs(comparison.LostRequiredPropositions)
	sortMeetingQualityTextDiffs(comparison.NewUnsupportedPropositions)
	sortMeetingQualityTextDiffs(comparison.NewHardInvariantViolations)
	sortMeetingQualityTextDiffs(comparison.NewRelationFailures)
	sortMeetingQualityTextDiffs(comparison.NewKindMismatches)
	sortMeetingQualityTextDiffs(comparison.NewEvidenceMismatches)
	sortMeetingQualityTextDiffs(comparison.NewSemanticStateMismatches)
	sort.Slice(comparison.ParentRelationDiffs, func(i, j int) bool {
		return comparison.ParentRelationDiffs[i].Scenario < comparison.ParentRelationDiffs[j].Scenario
	})
	sort.Slice(comparison.KindDistributionDiffs, func(i, j int) bool {
		return comparison.KindDistributionDiffs[i].Scenario < comparison.KindDistributionDiffs[j].Scenario
	})
	if len(comparison.ImprovedMetrics) > 0 || len(comparison.RepairedScenarios) > 0 || len(comparison.NewScenarios) > 0 {
		comparison.BaselineUpdateRequired = true
		comparison.Passed = false
	}
	return comparison
}

func sortMeetingQualityTextDiffs(values []MeetingQualityTextDiff) {
	sort.Slice(values, func(i, j int) bool {
		return values[i].Scenario < values[j].Scenario
	})
}

// AcceptMeetingQualityImprovements ratchets only proven improvements into an
// existing baseline. It refuses scenario removal, metric-schema changes,
// failing current results, and every worsening before changing any value.
func AcceptMeetingQualityImprovements(
	baseline MeetingQualityBaseline,
	current MeetingQualitySuiteReport,
) (MeetingQualityBaseline, MeetingQualityBaselineUpdateReport, error) {
	migrated, addedMetricSchema, migratedExactMetrics, err := migrateMeetingQualityBaselineSchema(baseline, current)
	if err != nil {
		return baseline, MeetingQualityBaselineUpdateReport{}, err
	}
	baseline = migrated
	comparison := CompareMeetingQualityBaseline(baseline, current)
	if hasMeetingQualityRegression(comparison) || !current.Passed {
		return baseline, MeetingQualityBaselineUpdateReport{},
			fmt.Errorf(
				"refusing baseline update because current results contain a regression or failure: worsenedMetrics=%v newFailures=%v lostRequired=%v newUnsupported=%v newHardInvariants=%v newRelations=%v newKinds=%v newEvidence=%v newSemanticState=%v",
				comparison.WorsenedMetrics, comparison.NewFailures,
				comparison.LostRequiredPropositions, comparison.NewUnsupportedPropositions,
				comparison.NewHardInvariantViolations, comparison.NewRelationFailures,
				comparison.NewKindMismatches, comparison.NewEvidenceMismatches,
				comparison.NewSemanticStateMismatches,
			)
	}
	if !comparison.BaselineUpdateRequired && len(addedMetricSchema) == 0 && len(migratedExactMetrics) == 0 {
		return baseline, MeetingQualityBaselineUpdateReport{UnchangedBaseline: true}, nil
	}
	updated := baseline
	updated.MetricSchema = qualityMetricNames()
	indexByID := make(map[string]int, len(updated.Scenarios))
	currentByID := make(map[string]MeetingQualityScenarioResult, len(current.Scenarios))
	for index, scenario := range updated.Scenarios {
		indexByID[scenario.ID] = index
	}
	for _, scenario := range current.Scenarios {
		currentByID[scenario.ID] = scenario
	}
	update := MeetingQualityBaselineUpdateReport{
		AppliedMetrics:    append(append([]MeetingQualityMetricChange(nil), migratedExactMetrics...), comparison.ImprovedMetrics...),
		AppliedRepairs:    append([]string(nil), comparison.RepairedScenarios...),
		AddedScenarios:    append([]string(nil), comparison.NewScenarios...),
		AddedMetricSchema: append([]string(nil), addedMetricSchema...),
	}
	for _, change := range comparison.ImprovedMetrics {
		index := indexByID[change.Scenario]
		setMeetingQualityMetric(&updated.Scenarios[index].Metrics, change.Metric, change.After)
	}
	for _, id := range comparison.RepairedScenarios {
		index := indexByID[id]
		// A repaired scenario has already been checked for independent metric
		// worsening. Copying its diagnostics records the eliminated failure and
		// prevents it from being repeatedly proposed as an improvement.
		updated.Scenarios[index] = currentByID[id]
	}
	for _, id := range comparison.NewScenarios {
		updated.Scenarios = append(updated.Scenarios, currentByID[id])
	}
	return updated, update, nil
}

// migrateMeetingQualityBaselineSchema permits exactly one audited, additive
// migration: the known v3 metric list to v4. Existing metric values are kept;
// only the seven newly introduced axes are initialized from the passing current
// report because no historical value exists for them. Arbitrary deletion,
// reordering, or same-version schema drift remains an error.
func migrateMeetingQualityBaselineSchema(
	baseline MeetingQualityBaseline,
	current MeetingQualitySuiteReport,
) (MeetingQualityBaseline, []string, []MeetingQualityMetricChange, error) {
	if baseline.SchemaVersion == meetingQualityBaselineSchemaVersion {
		if err := ValidateMeetingQualityBaseline(baseline); err != nil {
			return baseline, nil, nil, err
		}
		return baseline, nil, nil, nil
	}
	if baseline.SchemaVersion != 3 || !reflect.DeepEqual(baseline.MetricSchema, meetingQualityMetricSchemaV3) {
		return baseline, nil, nil, fmt.Errorf(
			"baseline schema migration rejected: schemaVersion=%d metricSchema=%v",
			baseline.SchemaVersion, baseline.MetricSchema,
		)
	}
	if strings.TrimSpace(baseline.Suite) == "" || baseline.Suite != current.Suite || len(baseline.Scenarios) == 0 {
		return baseline, nil, nil, fmt.Errorf("baseline schema migration rejected: invalid suite or empty scenarios")
	}
	currentByID := make(map[string]MeetingQualityScenarioResult, len(current.Scenarios))
	for _, scenario := range current.Scenarios {
		currentByID[scenario.ID] = scenario
	}
	added := append([]string(nil), qualityMetricNames()[len(meetingQualityMetricSchemaV3):]...)
	var reviewedExact []MeetingQualityMetricChange
	for index := range baseline.Scenarios {
		currentScenario, ok := currentByID[baseline.Scenarios[index].ID]
		if !ok {
			return baseline, nil, nil, fmt.Errorf(
				"baseline schema migration rejected: missing current scenario %q",
				baseline.Scenarios[index].ID,
			)
		}
		if baseline.Scenarios[index].Metrics.PastFactCount != currentScenario.Metrics.PastFactCount &&
			currentScenario.Passed &&
			meetingQualityAddedTemporalExpectation(baseline.Scenarios[index], currentScenario) {
			reviewedExact = append(reviewedExact, MeetingQualityMetricChange{
				Scenario: currentScenario.ID, Metric: "pastFactCount",
				Before: float64(baseline.Scenarios[index].Metrics.PastFactCount),
				After:  float64(currentScenario.Metrics.PastFactCount),
			})
			baseline.Scenarios[index].Metrics.PastFactCount = currentScenario.Metrics.PastFactCount
		}
		currentMetrics := qualityMetricValues(currentScenario.Metrics)
		for metricIndex := len(meetingQualityMetricSchemaV3); metricIndex < len(currentMetrics); metricIndex++ {
			setMeetingQualityMetric(
				&baseline.Scenarios[index].Metrics,
				currentMetrics[metricIndex].Name,
				currentMetrics[metricIndex].Value,
			)
		}
	}
	baseline.SchemaVersion = meetingQualityBaselineSchemaVersion
	baseline.MetricSchema = qualityMetricNames()
	return baseline, added, reviewedExact, nil
}

func meetingQualityAddedTemporalExpectation(
	before MeetingQualityScenarioResult,
	after MeetingQualityScenarioResult,
) bool {
	beforeByID := make(map[string]MeetingQualityPropositionMatch, len(before.PropositionMatches))
	for _, match := range before.PropositionMatches {
		beforeByID[match.PropositionID] = match
	}
	for _, match := range after.PropositionMatches {
		previous, exists := beforeByID[match.PropositionID]
		if strings.TrimSpace(match.RequiredTemporalScope) != "" &&
			(!exists || strings.TrimSpace(previous.RequiredTemporalScope) == "") {
			return true
		}
	}
	return false
}

func hasMeetingQualityRegression(comparison MeetingQualityComparisonReport) bool {
	return len(comparison.WorsenedMetrics) > 0 ||
		len(comparison.NewFailures) > 0 ||
		len(comparison.LostRequiredPropositions) > 0 ||
		len(comparison.NewUnsupportedPropositions) > 0 ||
		len(comparison.NewHardInvariantViolations) > 0 ||
		len(comparison.NewRelationFailures) > 0 ||
		len(comparison.NewKindMismatches) > 0 ||
		len(comparison.NewEvidenceMismatches) > 0 ||
		len(comparison.NewSemanticStateMismatches) > 0
}

func appendNewResultFailures(
	target *[]MeetingQualityTextDiff,
	scenario string,
	before []string,
	after []string,
	passed *bool,
) {
	if added := qualityStringSetDifference(after, before); len(added) > 0 {
		*target = append(*target, MeetingQualityTextDiff{Scenario: scenario, Values: added})
		*passed = false
	}
}

func qualityKindMismatchKeys(values []MeetingQualityKindMismatch) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, fmt.Sprintf("%s:%s:%s:%s",
			value.PropositionID, strings.Join(value.ExpectedKinds, "|"), value.ActualItemID, value.ActualKind))
	}
	return result
}

func qualityEvidenceMismatchKeys(values []MeetingQualityEvidenceMismatch) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, fmt.Sprintf("%s:%s:%d:%v",
			value.PropositionID, value.ActualItemID, value.ExpectedSequence, value.ActualSequences))
	}
	return result
}

func qualitySemanticStateMismatchKeys(values []MeetingQualitySemanticStateMismatch) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, fmt.Sprintf("%s:%s:%s:%s:%s",
			value.PropositionID, value.ActualItemID, value.Field, value.Expected, value.Actual))
	}
	return result
}

func qualityMetricValues(metrics MeetingQualityMetrics) []meetingQualityMetricValue {
	return []meetingQualityMetricValue{
		{Name: "requiredPropositionRecall", Value: metrics.RequiredPropositionRecall, HigherIsGood: true},
		{Name: "unsupportedPropositionCount", Value: float64(metrics.UnsupportedPropositionCount)},
		{Name: "classificationAccuracy", Value: metrics.ClassificationAccuracy, HigherIsGood: true},
		{Name: "temporalScopeAccuracy", Value: metrics.TemporalScopeAccuracy, HigherIsGood: true},
		{Name: "pastFactCount", Value: float64(metrics.PastFactCount), Exact: true},
		{Name: "issueCount", Value: float64(metrics.IssueCount), Exact: true},
		{Name: "resolvedIssueCount", Value: float64(metrics.ResolvedIssueCount), Exact: true},
		{Name: "incorrectResolvedIssueCount", Value: float64(metrics.IncorrectResolvedIssueCount)},
		{Name: "riskRecall", Value: metrics.RiskRecall, HigherIsGood: true},
		{Name: "todoRecall", Value: metrics.TodoRecall, HigherIsGood: true},
		{Name: "decisionRecall", Value: metrics.DecisionRecall, HigherIsGood: true},
		{Name: "semanticDuplicateCount", Value: float64(metrics.SemanticDuplicateCount)},
		{Name: "lowInformationLabelCount", Value: float64(metrics.LowInformationLabelCount)},
		{Name: "contextDependentLabelCount", Value: float64(metrics.ContextDependentLabelCount)},
		{Name: "truncatedLabelCount", Value: float64(metrics.TruncatedLabelCount)},
		{Name: "hierarchyRelationAccuracy", Value: metrics.HierarchyRelationAccuracy, HigherIsGood: true},
		{Name: "candidateFragmentationCount", Value: float64(metrics.CandidateFragmentationCount)},
		{Name: "crossAgendaContaminationCount", Value: float64(metrics.CrossAgendaContaminationCount)},
		{Name: "label_description_exact_duplicate_count", Value: float64(metrics.LabelDescriptionExactDuplicateCount)},
		{Name: "label_description_high_similarity_count", Value: float64(metrics.LabelDescriptionHighSimilarityCount)},
		{Name: "description_unsupported_atom_count", Value: float64(metrics.DescriptionUnsupportedAtomCount)},
		{Name: "description_added_grounded_detail_count", Value: float64(metrics.DescriptionAddedGroundedDetailCount), HigherIsGood: true},
		{Name: "label_transcript_copy_ratio", Value: metrics.LabelTranscriptCopyRatio},
		{Name: "label_compression_ratio", Value: metrics.LabelCompressionRatio},
		{Name: "description_redundant_count", Value: float64(metrics.DescriptionRedundantCount)},
	}
}

func qualityMetricNames() []string {
	values := qualityMetricValues(MeetingQualityMetrics{})
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.Name)
	}
	return result
}

func setMeetingQualityMetric(metrics *MeetingQualityMetrics, name string, value float64) {
	if metrics == nil {
		return
	}
	switch name {
	case "requiredPropositionRecall":
		metrics.RequiredPropositionRecall = value
	case "unsupportedPropositionCount":
		metrics.UnsupportedPropositionCount = int(value)
	case "classificationAccuracy":
		metrics.ClassificationAccuracy = value
	case "temporalScopeAccuracy":
		metrics.TemporalScopeAccuracy = value
	case "pastFactCount":
		metrics.PastFactCount = int(value)
	case "issueCount":
		metrics.IssueCount = int(value)
	case "resolvedIssueCount":
		metrics.ResolvedIssueCount = int(value)
	case "incorrectResolvedIssueCount":
		metrics.IncorrectResolvedIssueCount = int(value)
	case "riskRecall":
		metrics.RiskRecall = value
	case "todoRecall":
		metrics.TodoRecall = value
	case "decisionRecall":
		metrics.DecisionRecall = value
	case "semanticDuplicateCount":
		metrics.SemanticDuplicateCount = int(value)
	case "lowInformationLabelCount":
		metrics.LowInformationLabelCount = int(value)
	case "contextDependentLabelCount":
		metrics.ContextDependentLabelCount = int(value)
	case "truncatedLabelCount":
		metrics.TruncatedLabelCount = int(value)
	case "hierarchyRelationAccuracy":
		metrics.HierarchyRelationAccuracy = value
	case "candidateFragmentationCount":
		metrics.CandidateFragmentationCount = int(value)
	case "crossAgendaContaminationCount":
		metrics.CrossAgendaContaminationCount = int(value)
	case "label_description_exact_duplicate_count":
		metrics.LabelDescriptionExactDuplicateCount = int(value)
	case "label_description_high_similarity_count":
		metrics.LabelDescriptionHighSimilarityCount = int(value)
	case "description_unsupported_atom_count":
		metrics.DescriptionUnsupportedAtomCount = int(value)
	case "description_added_grounded_detail_count":
		metrics.DescriptionAddedGroundedDetailCount = int(value)
	case "label_transcript_copy_ratio":
		metrics.LabelTranscriptCopyRatio = value
	case "label_compression_ratio":
		metrics.LabelCompressionRatio = value
	case "description_redundant_count":
		metrics.DescriptionRedundantCount = int(value)
	}
}

func qualityMetricImproved(before, after meetingQualityMetricValue) bool {
	if before.Exact {
		return false
	}
	if before.HigherIsGood {
		return after.Value > before.Value+meetingQualityMetricEpsilon
	}
	return after.Value < before.Value-meetingQualityMetricEpsilon
}

func qualityMetricWorsened(before, after meetingQualityMetricValue) bool {
	if before.Exact {
		return after.Value < before.Value-meetingQualityMetricEpsilon ||
			after.Value > before.Value+meetingQualityMetricEpsilon
	}
	if before.HigherIsGood {
		return after.Value < before.Value-meetingQualityMetricEpsilon
	}
	return after.Value > before.Value+meetingQualityMetricEpsilon
}

func qualityStringSetDifference(current, baseline []string) []string {
	known := make(map[string]struct{}, len(baseline))
	for _, value := range baseline {
		known[value] = struct{}{}
	}
	var difference []string
	for _, value := range current {
		if _, exists := known[value]; !exists {
			difference = append(difference, value)
		}
	}
	sort.Strings(difference)
	return difference
}
