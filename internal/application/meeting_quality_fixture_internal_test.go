package application

import (
	"embed"
	"encoding/json"
	"testing"
)

//go:embed testdata/qualityeval/*.json
var meetingQualityFixtureFS embed.FS

func loadDeterministicMeetingQualitySuite(t *testing.T) MeetingQualitySuite {
	t.Helper()
	raw, err := meetingQualityFixtureFS.ReadFile("testdata/qualityeval/scenarios.json")
	if err != nil {
		t.Fatalf("read quality scenarios: %v", err)
	}
	var suite MeetingQualitySuite
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatalf("decode quality scenarios: %v", err)
	}
	if err := ValidateMeetingQualitySuite(suite); err != nil {
		t.Fatalf("validate quality scenarios: %v", err)
	}
	return suite
}

func TestDeterministicMeetingQualitySuiteRegistersInitialRegressions(t *testing.T) {
	suite := loadDeterministicMeetingQualitySuite(t)
	required := []string{
		"agenda-misassignment",
		"unexpected-candidate-promotion",
		"same-batch-candidate-promotion",
		"recap-preserves-tree",
		"unspoken-information-contamination",
		"semantic-kind-classification",
		"low-information-node-repair",
		"correction-removes-stale-proposition",
		"semantic-evidence-mismatch",
		"split-fragment-future-evidence",
		"finalization-inflight-tail-flush",
		"label-rewrite-failure-preserves-risk",
		"network-logical-hierarchy",
		"semantic-duplicate-proposition",
		"vpn-certificate-card-tree-consistency",
	}
	if len(suite.Scenarios) < len(required) {
		t.Fatalf("scenario count=%d, want at least %d", len(suite.Scenarios), len(required))
	}
	registered := make(map[string]bool, len(suite.Scenarios))
	for _, scenario := range suite.Scenarios {
		registered[scenario.ID] = true
	}
	for _, id := range required {
		if !registered[id] {
			t.Errorf("required quality scenario %q is not registered", id)
		}
	}
}

func TestDeterministicMeetingQualitySuite(t *testing.T) {
	report := RunMeetingQualitySuite(loadDeterministicMeetingQualitySuite(t))
	if report.Passed {
		return
	}
	for _, scenario := range report.Scenarios {
		if scenario.Passed {
			continue
		}
		t.Errorf(
			"scenario %s failed: error=%q hard=%v missing=%v unsupported=%v relations=%v forbidden=%v safety=%v metrics=%+v",
			scenario.ID, scenario.Error, scenario.HardInvariantViolations,
			scenario.MissingRequiredPropositions, scenario.UnsupportedPropositions,
			scenario.RelationFailures, scenario.ForbiddenResultsFound,
			scenario.SafetyFailures, scenario.Metrics,
		)
	}
}

func TestDeterministicMeetingQualitySuiteDetectsKnownQualityShapes(t *testing.T) {
	report := RunMeetingQualitySuite(loadDeterministicMeetingQualitySuite(t))
	byID := make(map[string]MeetingQualityScenarioResult, len(report.Scenarios))
	for _, scenario := range report.Scenarios {
		byID[scenario.ID] = scenario
	}
	duplicate := byID["semantic-duplicate-proposition"]
	if duplicate.Metrics.SemanticDuplicateCount < 1 {
		t.Fatalf("duplicate metric=%d, want the known duplicate shape to be observable", duplicate.Metrics.SemanticDuplicateCount)
	}
	hierarchy := byID["network-logical-hierarchy"]
	if hierarchy.Metrics.HierarchyRelationAccuracy != 1 {
		t.Fatalf("network hierarchy accuracy=%v, want supported_by and limits represented", hierarchy.Metrics.HierarchyRelationAccuracy)
	}
	risk := byID["label-rewrite-failure-preserves-risk"]
	if risk.Metrics.RiskRecall != 1 || len(risk.SafetyFailures) != 0 {
		t.Fatalf("risk preservation result=%+v", risk)
	}
	t.Logf(
		"known metrics: duplicate=%d unspokenDuplicate=%d finalizationClassification=%.2f candidateFragmentation=%d",
		duplicate.Metrics.SemanticDuplicateCount,
		byID["unspoken-information-contamination"].Metrics.SemanticDuplicateCount,
		byID["finalization-inflight-tail-flush"].Metrics.ClassificationAccuracy,
		byID["semantic-kind-classification"].Metrics.CandidateFragmentationCount,
	)
}

func TestDeterministicMeetingQualitySuiteUsesProductionStages(t *testing.T) {
	report := RunMeetingQualitySuite(loadDeterministicMeetingQualitySuite(t))
	direct, seeded, completedOnly := 0, 0, 0
	requiredStages := []string{
		"live_extraction_result_application",
		"semantic_grounding",
		"kind_validation",
		"evidence_normalization",
		"candidate_lifecycle",
		"dynamic_topic_promotion",
		"grouping",
		"finalization_repair",
		"final_delivery_projection",
	}
	for _, scenario := range report.Scenarios {
		switch scenario.InputMode {
		case "transcript_context_fixed_ai":
			direct++
		case "seeded_transcript_context_fixed_ai":
			seeded++
		case "completed_snapshot_only":
			completedOnly++
		default:
			t.Errorf("scenario %s has unknown input mode %q", scenario.ID, scenario.InputMode)
		}
		for _, stage := range requiredStages {
			if !containsExactString(scenario.ProductionStages, stage) {
				t.Errorf("scenario %s did not record production stage %s: %v",
					scenario.ID, stage, scenario.ProductionStages)
			}
		}
	}
	if direct != 13 || seeded != 2 || completedOnly != 0 {
		t.Fatalf("pipeline modes direct=%d seeded=%d completedOnly=%d, want 13/2/0",
			direct, seeded, completedOnly)
	}
}

func TestDeterministicMeetingQualityKnownProblemsHaveMetricProvenance(t *testing.T) {
	report := RunMeetingQualitySuite(loadDeterministicMeetingQualitySuite(t))
	byID := make(map[string]MeetingQualityScenarioResult, len(report.Scenarios))
	for _, scenario := range report.Scenarios {
		byID[scenario.ID] = scenario
	}
	tests := []struct {
		scenario string
		metric   string
		want     int
	}{
		{"unspoken-information-contamination", "semanticDuplicateCount", 1},
		{"semantic-kind-classification", "candidateFragmentationCount", 2},
		{"semantic-duplicate-proposition", "semanticDuplicateCount", 1},
	}
	for _, test := range tests {
		t.Run(test.scenario+"/"+test.metric, func(t *testing.T) {
			var evidence []MeetingQualityMetricEvidence
			for _, value := range byID[test.scenario].MetricEvidence {
				if value.Metric == test.metric {
					evidence = append(evidence, value)
				}
			}
			if len(evidence) != test.want {
				t.Fatalf("metric evidence=%+v, want %d records", evidence, test.want)
			}
			for _, value := range evidence {
				if value.Reason == "" || (test.metric != "riskRecall" &&
					test.metric != "hierarchyRelationAccuracy" && len(value.ActualItemIDs) == 0) {
					t.Fatalf("metric evidence lacks expected/actual provenance: %+v", value)
				}
			}
		})
	}
	finalization := byID["finalization-inflight-tail-flush"]
	if finalization.Metrics.ClassificationAccuracy != 1 ||
		finalization.Metrics.RiskRecall != 1 || len(finalization.KindMismatches) != 0 {
		t.Fatalf("finalization risk regression was not repaired: %+v", finalization)
	}
}

func TestDeterministicMeetingQualitySuiteMatchesApprovedBaseline(t *testing.T) {
	report := RunMeetingQualitySuite(loadDeterministicMeetingQualitySuite(t))
	raw, err := meetingQualityFixtureFS.ReadFile("testdata/qualityeval/baseline.json")
	if err != nil {
		t.Fatalf("read quality baseline: %v", err)
	}
	var baseline MeetingQualityBaseline
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatalf("decode quality baseline: %v", err)
	}
	comparison := CompareMeetingQualityBaseline(baseline, report)
	if !comparison.Passed {
		t.Fatalf("quality baseline regression: %+v", comparison)
	}
	if len(comparison.ParentRelationDiffs) != 0 || len(comparison.KindDistributionDiffs) != 0 {
		t.Fatalf("unexpected structural baseline diff: parents=%+v kinds=%+v",
			comparison.ParentRelationDiffs, comparison.KindDistributionDiffs)
	}
}
