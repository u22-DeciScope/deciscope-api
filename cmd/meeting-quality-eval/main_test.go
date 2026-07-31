package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"deciscope-core-api/internal/application"
)

func TestMeetingQualityCommandFailurePropagatesEveryEvaluatorMutationClass(t *testing.T) {
	hardFailures := map[string]bool{
		"required risk removed":                 true,
		"required todo removed":                 true,
		"unsupported proposition added":         true,
		"future evidence added":                 true,
		"orphan node created":                   true,
		"duplicate node id created":             true,
		"logical siblings split":                true,
		"dynamic topic item moved outside tree": true,
		"final coverage reduced":                true,
	}
	mutations := []string{
		"required risk removed",
		"required todo removed",
		"fact changed to wrong kind",
		"unsupported proposition added",
		"future evidence added",
		"orphan node created",
		"duplicate node id created",
		"semantic duplicate added",
		"truncated label added",
		"context dependent label added",
		"logical siblings split",
		"dynamic topic item moved outside tree",
		"final coverage reduced",
	}
	for _, mutation := range mutations {
		t.Run(mutation, func(t *testing.T) {
			report := application.MeetingQualitySuiteReport{Passed: !hardFailures[mutation]}
			var comparison *application.MeetingQualityComparisonReport
			if report.Passed {
				comparison = &application.MeetingQualityComparisonReport{
					Passed: false,
					WorsenedMetrics: []application.MeetingQualityMetricChange{{
						Scenario: "mutation-control",
						Metric:   mutation,
						Before:   1,
						After:    0,
					}},
				}
			}
			if err := commandFailure(report, comparison); err == nil {
				t.Fatal("CLI would return exit code 0 for detected mutation")
			}
		})
	}
}

func TestMeetingQualityCLIProcessExitIsNonZeroForEveryMutationClass(t *testing.T) {
	mutations := []string{
		"required risk removed",
		"required todo removed",
		"fact changed to wrong kind",
		"unsupported proposition added",
		"future evidence added",
		"orphan node created",
		"duplicate node id created",
		"semantic duplicate added",
		"truncated label added",
		"context dependent label added",
		"logical siblings split",
		"dynamic topic item moved outside tree",
		"final coverage reduced",
	}
	for _, mutation := range mutations {
		t.Run(mutation, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestMeetingQualityCLIExitHelper$")
			command.Env = append(os.Environ(),
				"MEETING_QUALITY_CLI_EXIT_HELPER=1",
				"MEETING_QUALITY_MUTATION="+mutation,
			)
			err := command.Run()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 1 {
				t.Fatalf("mutation %q exit error=%v, want exit code 1", mutation, err)
			}
		})
	}
}

func TestMeetingQualityCLIExitHelper(t *testing.T) {
	if os.Getenv("MEETING_QUALITY_CLI_EXIT_HELPER") != "1" {
		return
	}
	mutation := os.Getenv("MEETING_QUALITY_MUTATION")
	hard := strings.Contains(mutation, "removed") ||
		strings.Contains(mutation, "unsupported") ||
		strings.Contains(mutation, "future") ||
		strings.Contains(mutation, "orphan") ||
		strings.Contains(mutation, "duplicate node") ||
		strings.Contains(mutation, "siblings") ||
		strings.Contains(mutation, "outside tree") ||
		strings.Contains(mutation, "coverage")
	report := application.MeetingQualitySuiteReport{Passed: !hard}
	var comparison *application.MeetingQualityComparisonReport
	if report.Passed {
		comparison = &application.MeetingQualityComparisonReport{
			Passed: false,
			WorsenedMetrics: []application.MeetingQualityMetricChange{{
				Scenario: "mutation-control", Metric: mutation, Before: 1, After: 0,
			}},
		}
	}
	if commandFailure(report, comparison) != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestMeetingQualityCommandRejectsUnsafeFullBaselineReplacement(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"-update-baseline"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "-accept-improvements") {
		t.Fatalf("run error=%v, want unsafe replacement refusal", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unsafe update unexpectedly emitted success output: %s", stdout.String())
	}
}

func TestMeetingQualityCommandDoesNotDescribeMixedRegressionAsAcceptableImprovement(t *testing.T) {
	err := commandFailure(
		application.MeetingQualitySuiteReport{Passed: true},
		&application.MeetingQualityComparisonReport{
			Passed:                 false,
			BaselineUpdateRequired: true,
			ImprovedMetrics: []application.MeetingQualityMetricChange{{
				Scenario: "scenario", Metric: "riskRecall", Before: 0, After: 1,
			}},
			WorsenedMetrics: []application.MeetingQualityMetricChange{{
				Scenario: "scenario", Metric: "todoRecall", Before: 1, After: 0,
			}},
		},
	)
	if err == nil || strings.Contains(err.Error(), "-accept-improvements") {
		t.Fatalf("mixed regression was described as an acceptable improvement: %v", err)
	}
}
