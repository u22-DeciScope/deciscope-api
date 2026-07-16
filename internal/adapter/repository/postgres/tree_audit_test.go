package postgres

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
)

func TestMeetingTreeAuditRepositoryPersistsHistoryAndAppliesCAS(t *testing.T) {
	analysisRepository, db := newTestMeetingAIAnalysisRepository(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO meeting_sessions (id, join_url, join_url_hash, status, requested_at, created_at, updated_at)
		VALUES ('session_audit', 'https://example.test/meeting', 'hash', 'joined', $1, $1, $1)
	`, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert meeting session: %v", err)
	}
	if _, err := analysisRepository.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID: "session_audit", Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 4,
		Payload:   json.RawMessage(`{"treeVersion":4,"tree":{"nodes":[],"edges":[]}}`),
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed live analysis: %v", err)
	}

	repository := NewMeetingTreeAuditRepository(db)
	if err := repository.CheckMeetingTreeAuditRepository(ctx); err != nil {
		t.Fatalf("CheckMeetingTreeAuditRepository() error = %v", err)
	}
	completed := now.Add(time.Second)
	run := domain.MeetingTreeAuditRun{
		ID: "audit-1", SessionID: "session_audit", BasedOnTreeVersion: 4,
		ResultingTreeVersion: 5, Mode: domain.MeetingTreeAuditApplyHighConfidence,
		TriggerReason: "test", TriggerClass: domain.MeetingTreeAuditTriggerNormal,
		Task: "tree_audit", Deployment: "mini-deployment", ProviderCalled: true,
		Model: "gpt-5-mini", PromptVersion: "v1", SnapshotHash: "snapshot",
		Status: domain.MeetingTreeAuditCompleted, Result: "applied",
		Findings: json.RawMessage(`[]`), Operations: json.RawMessage(`[]`),
		ValidatorResult: json.RawMessage(`{"treeIntegrityValid":true}`),
		CreatedAt:       now, CompletedAt: &completed,
	}
	saved, applied, err := repository.ApplyMeetingTreeAudit(ctx, run, 4, domain.MeetingAIAnalysis{
		SessionID: "session_audit", Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 5,
		Payload: json.RawMessage(`{"treeVersion":5,"changeSource":"tree_auditor","tree":{"nodes":[],"edges":[]}}`),
		Model:   "gpt-5-mini", UpdatedAt: completed,
	})
	if err != nil || !applied || saved.Version != 5 {
		t.Fatalf("ApplyMeetingTreeAudit() saved=%+v applied=%t err=%v", saved, applied, err)
	}
	latest, err := repository.GetLatestMeetingTreeAuditRun(ctx, "session_audit")
	if err != nil || latest.ID != "audit-1" || latest.ResultingTreeVersion != 5 {
		t.Fatalf("latest run=%+v err=%v", latest, err)
	}
	if count, err := repository.CountMeetingTreeAuditProviderCalls(ctx, "session_audit", domain.MeetingTreeAuditTriggerNormal, time.Time{}); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}

	staleRun := run
	staleRun.ID = "audit-stale"
	staleRun.ResultingTreeVersion = 6
	if _, applied, err := repository.ApplyMeetingTreeAudit(ctx, staleRun, 4, domain.MeetingAIAnalysis{
		SessionID: "session_audit", Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 6,
		Payload: json.RawMessage(`{"treeVersion":6}`), UpdatedAt: completed.Add(time.Second),
	}); err != nil || applied {
		t.Fatalf("stale CAS applied=%t err=%v", applied, err)
	}
	if live, err := analysisRepository.GetMeetingAIAnalysis(ctx, "session_audit", domain.MeetingAIAnalysisLive); err != nil || live.Version != 5 {
		t.Fatalf("live after stale CAS=%+v err=%v", live, err)
	}
}
