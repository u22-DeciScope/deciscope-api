package application

import (
	"bytes"
	"encoding/json"
	"log"
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

func TestTreeAuditResultClassificationAndConsecutiveUnappliedRuns(t *testing.T) {
	config := TreeAuditConfig{UnappliedWarningThreshold: 3}
	findings := json.RawMessage(`[{"findingId":"finding-1"}]`)

	findingsOnly := domain.MeetingTreeAuditRun{
		ID: "audit-1", SessionID: "session-1", BasedOnTreeVersion: 1,
		Status: domain.MeetingTreeAuditCompleted, Result: "no_safe_operations", ProviderCalled: true,
		Findings: findings, Operations: json.RawMessage(`[]`), ValidatorResult: json.RawMessage(`{"operationsProposed":0,"operationsApplied":0}`),
	}
	classifyTreeAuditRun(&findingsOnly, nil, config)
	if findingsOnly.ResultClassification != domain.MeetingTreeAuditResultFindingsOnly || findingsOnly.ConsecutiveUnappliedRuns != 1 {
		t.Fatalf("findings-only run = %+v", findingsOnly)
	}

	var logs bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousWriter) })
	rejected := domain.MeetingTreeAuditRun{
		ID: "audit-2", SessionID: "session-1", BasedOnTreeVersion: 2,
		Status: domain.MeetingTreeAuditCompleted, Result: "no_safe_operations", ProviderCalled: true,
		Findings: findings, Operations: json.RawMessage(`[{"operationId":"op-1"}]`),
		ValidatorResult: json.RawMessage(`{"operationsProposed":1,"operationsValid":0,"operationsApplied":0,"operationsRejected":1,"evaluations":[{"operationId":"op-1","result":"rejected","reason":"unsafe","valid":false,"applied":false,"modelConfidence":0.9,"effectiveConfidence":0.9}]}`),
	}
	classifyTreeAuditRun(&rejected, &findingsOnly, config)
	if rejected.ResultClassification != domain.MeetingTreeAuditResultRejected || rejected.ConsecutiveUnappliedRuns != 2 {
		t.Fatalf("rejected run = %+v", rejected)
	}
	if strings.Count(logs.String(), "Tree audit findings remain unapplied.") != 0 {
		t.Fatalf("warning emitted before third unapplied run: %q", logs.String())
	}

	third := rejected
	third.ID = "audit-3"
	third.BasedOnTreeVersion = 3
	classifyTreeAuditRun(&third, &rejected, config)
	if third.ConsecutiveUnappliedRuns != 3 || strings.Count(logs.String(), "Tree audit findings remain unapplied.") != 1 {
		t.Fatalf("third unapplied run did not emit exactly one warning: run=%+v logs=%q", third, logs.String())
	}
	fourth := third
	fourth.ID = "audit-4"
	fourth.BasedOnTreeVersion = 4
	classifyTreeAuditRun(&fourth, &third, config)
	if fourth.ConsecutiveUnappliedRuns != 4 || strings.Count(logs.String(), "Tree audit findings remain unapplied.") != 1 {
		t.Fatalf("warning repeated after threshold: run=%+v logs=%q", fourth, logs.String())
	}

	rateLimited := domain.MeetingTreeAuditRun{
		ID: "audit-rate-limited", SessionID: "session-1", BasedOnTreeVersion: 5,
		Status: domain.MeetingTreeAuditSkipped, Result: "rate_limited", ProviderCalled: false,
	}
	classifyTreeAuditRun(&rateLimited, &fourth, config)
	if rateLimited.ResultClassification != "" || rateLimited.ConsecutiveUnappliedRuns != 4 {
		t.Fatalf("rate-limited run changed the persisted streak: %+v", rateLimited)
	}

	clean := domain.MeetingTreeAuditRun{
		ID: "audit-clean", SessionID: "session-1", BasedOnTreeVersion: 6,
		Status: domain.MeetingTreeAuditCompleted, Result: "no_safe_operations", ProviderCalled: true,
		Findings: json.RawMessage(`[]`), Operations: json.RawMessage(`[]`), ValidatorResult: json.RawMessage(`{"operationsProposed":0,"operationsApplied":0}`),
	}
	classifyTreeAuditRun(&clean, &rateLimited, config)
	if clean.ResultClassification != domain.MeetingTreeAuditResultClean || clean.ConsecutiveUnappliedRuns != 0 {
		t.Fatalf("clean run did not reset: %+v", clean)
	}

	applied := domain.MeetingTreeAuditRun{
		ID: "audit-applied", SessionID: "session-1", BasedOnTreeVersion: 7, ResultingTreeVersion: 8,
		Status: domain.MeetingTreeAuditCompleted, Result: "applied", ProviderCalled: true,
		Findings: findings, Operations: json.RawMessage(`[{"operationId":"op-1"}]`), ValidatorResult: json.RawMessage(`{"operationsProposed":1,"operationsValid":1,"operationsApplied":1}`),
	}
	classifyTreeAuditRun(&applied, &fourth, config)
	if applied.ResultClassification != domain.MeetingTreeAuditResultApplied || applied.ConsecutiveUnappliedRuns != 0 {
		t.Fatalf("applied run did not reset: %+v", applied)
	}

	otherSession := findingsOnly
	otherSession.ID = "audit-other-session"
	otherSession.SessionID = "session-2"
	classifyTreeAuditRun(&otherSession, &third, config)
	if otherSession.ConsecutiveUnappliedRuns != 1 {
		t.Fatalf("unapplied streak leaked across ended/session boundary: %+v", otherSession)
	}
}
