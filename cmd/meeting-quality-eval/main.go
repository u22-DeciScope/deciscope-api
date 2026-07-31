package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"deciscope-core-api/internal/application"
)

const (
	defaultScenarioPath = "internal/application/testdata/qualityeval/scenarios.json"
	defaultBaselinePath = "internal/application/testdata/qualityeval/baseline.json"
)

type commandOutput struct {
	Result         application.MeetingQualitySuiteReport           `json:"result"`
	Comparison     *application.MeetingQualityComparisonReport     `json:"comparison,omitempty"`
	BaselineUpdate *application.MeetingQualityBaselineUpdateReport `json:"baselineUpdate,omitempty"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "meeting-quality-eval:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("meeting-quality-eval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	suiteName := flags.String("suite", "deterministic", "suite to run: deterministic")
	scenarioPath := flags.String("scenarios", defaultScenarioPath, "scenario JSON path")
	baselinePath := flags.String("baseline", defaultBaselinePath, "baseline JSON path")
	compareBaseline := flags.Bool("compare-baseline", false, "compare every scenario × metric with the approved baseline")
	acceptImprovements := flags.Bool("accept-improvements", false, "ratchet only improvements into the approved baseline")
	updateBaseline := flags.Bool("update-baseline", false, "disabled unsafe full replacement; use -accept-improvements")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if *suiteName != "deterministic" {
		return fmt.Errorf("unknown suite %q; real-deployment evaluation is the opt-in internal/app integration suite", *suiteName)
	}
	if *updateBaseline {
		return errors.New("-update-baseline is disabled because it could adopt regressions; use -accept-improvements")
	}
	if *compareBaseline && *acceptImprovements {
		return errors.New("-compare-baseline and -accept-improvements are mutually exclusive")
	}
	suite, err := readSuite(*scenarioPath)
	if err != nil {
		return err
	}
	if err := application.ValidateMeetingQualitySuite(suite); err != nil {
		return err
	}
	report := application.RunMeetingQualitySuite(suite)
	output := commandOutput{Result: report}

	if *acceptImprovements {
		if !report.Passed {
			return errors.New("refusing to accept improvements from a failing deterministic suite")
		}
		baseline, err := readBaseline(*baselinePath)
		if err != nil {
			return err
		}
		updated, update, err := application.AcceptMeetingQualityImprovements(baseline, report)
		if err != nil {
			return err
		}
		if !update.UnchangedBaseline {
			if err := writeBaseline(*baselinePath, updated); err != nil {
				return err
			}
		}
		comparison := application.CompareMeetingQualityBaseline(updated, report)
		output.Comparison = &comparison
		output.BaselineUpdate = &update
	}
	if *compareBaseline {
		baseline, err := readBaseline(*baselinePath)
		if err != nil {
			return err
		}
		comparison := application.CompareMeetingQualityBaseline(baseline, report)
		output.Comparison = &comparison
	}
	encoded, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	fmt.Fprintln(stdout, string(encoded))
	if err := commandFailure(report, output.Comparison); err != nil {
		return err
	}
	return nil
}

func commandFailure(
	report application.MeetingQualitySuiteReport,
	comparison *application.MeetingQualityComparisonReport,
) error {
	if !report.Passed {
		return errors.New("deterministic suite failed")
	}
	if comparison != nil && !comparison.Passed {
		if comparison.BaselineUpdateRequired && !comparisonContainsRegression(*comparison) {
			return errors.New("quality improved; run -accept-improvements and review the readable baseline diff")
		}
		return errors.New("one or more scenario × metric or scenario invariant checks regressed from baseline")
	}
	return nil
}

func comparisonContainsRegression(comparison application.MeetingQualityComparisonReport) bool {
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

func readSuite(path string) (application.MeetingQualitySuite, error) {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return application.MeetingQualitySuite{}, fmt.Errorf("read scenarios %q: %w", path, err)
	}
	var suite application.MeetingQualitySuite
	if err := json.Unmarshal(raw, &suite); err != nil {
		return application.MeetingQualitySuite{}, fmt.Errorf("decode scenarios %q: %w", path, err)
	}
	return suite, nil
}

func readBaseline(path string) (application.MeetingQualityBaseline, error) {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return application.MeetingQualityBaseline{}, fmt.Errorf("read baseline %q: %w", path, err)
	}
	var baseline application.MeetingQualityBaseline
	if err := json.Unmarshal(raw, &baseline); err != nil {
		return application.MeetingQualityBaseline{}, fmt.Errorf("decode baseline %q: %w", path, err)
	}
	return baseline, nil
}

func writeBaseline(path string, baseline application.MeetingQualityBaseline) error {
	encoded, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		return fmt.Errorf("encode baseline: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(filepath.Clean(path), encoded, 0o644); err != nil {
		return fmt.Errorf("write baseline %q: %w", path, err)
	}
	return nil
}
