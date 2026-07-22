package application

import (
	"strings"
	"testing"
)

func TestReportWritersPreserveIssueSubtypeAndStatus(t *testing.T) {
	cards := []analysisCard{
		{ID: "issue-open", Kind: "issue", Subtype: issueSubtypeQuestion, Severity: "medium", Title: "基準は何か", Body: "回答が必要", Status: "open"},
		{ID: "issue-done", Kind: "issue", Subtype: issueSubtypeDiscussion, Severity: "high", Title: "基準が未確定", Body: "決定済み", Status: "resolved"},
		{ID: "decision", Kind: "decision", Severity: "high", Title: "基準を12m/sとする", Body: "採用する", Status: "open"},
	}

	var open strings.Builder
	if !writeOpenCardsByKind(&open, cards, "issue") {
		t.Fatal("open issue writer produced no output")
	}
	if got := open.String(); !strings.Contains(got, "issue/question") || strings.Contains(got, "基準が未確定") {
		t.Fatalf("open issue report=%q", got)
	}

	var decisions strings.Builder
	if !writeCardsByKind(&decisions, cards, "decision") || !strings.Contains(decisions.String(), "基準を12m/sとする") {
		t.Fatalf("decision report=%q", decisions.String())
	}
}
