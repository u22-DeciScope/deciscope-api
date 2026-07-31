package application

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

const (
	meetingQualityMetricEpsilon         = 1e-9
	meetingQualityBaselineSchemaVersion = 2
)

type meetingQualityMetricValue struct {
	Name         string
	Value        float64
	HigherIsGood bool
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
			comparison.NewFailures = append(comparison.NewFailures, "missing_baseline_scenario:"+id)
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
	sort.Slice(comparison.ParentRelationDiffs, func(i, j int) bool {
		return comparison.ParentRelationDiffs[i].Scenario < comparison.ParentRelationDiffs[j].Scenario
	})
	sort.Slice(comparison.KindDistributionDiffs, func(i, j int) bool {
		return comparison.KindDistributionDiffs[i].Scenario < comparison.KindDistributionDiffs[j].Scenario
	})
	if len(comparison.ImprovedMetrics) > 0 || len(comparison.RepairedScenarios) > 0 {
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
	comparison := CompareMeetingQualityBaseline(baseline, current)
	if hasMeetingQualityRegression(comparison) || !current.Passed {
		return baseline, MeetingQualityBaselineUpdateReport{},
			fmt.Errorf("refusing baseline update because current results contain a regression or failure")
	}
	if !comparison.BaselineUpdateRequired {
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
		AppliedMetrics: append([]MeetingQualityMetricChange(nil), comparison.ImprovedMetrics...),
		AppliedRepairs: append([]string(nil), comparison.RepairedScenarios...),
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
	return updated, update, nil
}

func hasMeetingQualityRegression(comparison MeetingQualityComparisonReport) bool {
	return len(comparison.WorsenedMetrics) > 0 ||
		len(comparison.NewFailures) > 0 ||
		len(comparison.LostRequiredPropositions) > 0 ||
		len(comparison.NewUnsupportedPropositions) > 0 ||
		len(comparison.NewHardInvariantViolations) > 0 ||
		len(comparison.NewRelationFailures) > 0 ||
		len(comparison.NewKindMismatches) > 0 ||
		len(comparison.NewEvidenceMismatches) > 0
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

func qualityMetricValues(metrics MeetingQualityMetrics) []meetingQualityMetricValue {
	return []meetingQualityMetricValue{
		{Name: "requiredPropositionRecall", Value: metrics.RequiredPropositionRecall, HigherIsGood: true},
		{Name: "unsupportedPropositionCount", Value: float64(metrics.UnsupportedPropositionCount)},
		{Name: "classificationAccuracy", Value: metrics.ClassificationAccuracy, HigherIsGood: true},
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
	}
}

func qualityMetricImproved(before, after meetingQualityMetricValue) bool {
	if before.HigherIsGood {
		return after.Value > before.Value+meetingQualityMetricEpsilon
	}
	return after.Value < before.Value-meetingQualityMetricEpsilon
}

func qualityMetricWorsened(before, after meetingQualityMetricValue) bool {
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
