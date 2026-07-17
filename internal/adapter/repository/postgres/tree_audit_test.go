package postgres

import (
	"context"
	"database/sql"
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

// TestMeetingTreeAuditRunLifecycleOnPostgreSQL exercises the exact INSERT /
// upsert paths that failed in session_497ed2b0aedf9dc6 with
// "INSERT has more expressions than target columns" (SQLSTATE 42601):
// migration 00011適用済みの実PostgreSQLに対し、shadow runのclaim INSERT、
// 冪等claim、raw input/response・findings・operations・validator保存、
// disposition更新、provider error更新、final tree review保存、rollbackを検証する。
func TestMeetingTreeAuditRunLifecycleOnPostgreSQL(t *testing.T) {
	_, db := newTestMeetingAIAnalysisRepository(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 6, 0, 0, 0, time.UTC)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO meeting_sessions (id, join_url, join_url_hash, status, requested_at, created_at, updated_at)
		VALUES ('session_lifecycle', 'https://example.test/meeting', 'hash-lifecycle', 'recording', $1, $1, $1)
	`, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert meeting session: %v", err)
	}
	repository := NewMeetingTreeAuditRepository(db)

	// 1. shadow audit runのclaim INSERT(全カラム: raw input含む)。
	run := domain.MeetingTreeAuditRun{
		ID: "audit-shadow-1", SessionID: "session_lifecycle", BasedOnTreeVersion: 11,
		Mode: domain.MeetingTreeAuditShadow, TriggerReason: "semantic_anomaly,interval_versions",
		TriggerClass: domain.MeetingTreeAuditTriggerHigh, Task: "tree_audit",
		Deployment: "ds-gpt-5-mini", PromptVersion: "v1", SnapshotHash: "snapshot-11",
		Status: domain.MeetingTreeAuditRunning, Result: "running", Disposition: "none",
		MeetingElapsedSeconds: 240,
		InputSummary:          json.RawMessage(`{"nodeCount":25}`),
		InputPayload:          json.RawMessage(`{"tree":{"nodes":[]}}`),
		CreatedAt:             now,
	}
	claimed, err := repository.TryStartMeetingTreeAuditRun(ctx, run)
	if err != nil || !claimed {
		t.Fatalf("TryStartMeetingTreeAuditRun() claimed=%t err=%v", claimed, err)
	}
	// 2. active claim: 同一(session, task, version, hash, prompt, deployment)の
	//    running中は二重claimできない。
	duplicate := run
	duplicate.ID = "audit-shadow-1-dup"
	if claimed, err := repository.TryStartMeetingTreeAuditRun(ctx, duplicate); err != nil || claimed {
		t.Fatalf("duplicate claim=%t err=%v, want false claim without error", claimed, err)
	}

	// 3. provider呼び出しマーク + raw response・findings・operations・validator保存
	//    + shadow disposition更新(ON CONFLICT DO UPDATE経路)。
	completed := now.Add(3 * time.Second)
	run.ProviderCalled = true
	run.Model = "gpt-5-mini-2025-08-07"
	run.RawResponse = `{"findings":[]}`
	run.Findings = json.RawMessage(`[{"type":"topic_outlier","severity":"medium"}]`)
	run.Operations = json.RawMessage(`[{"op":"move_node","nodeId":"item-1"}]`)
	run.ValidatorResult = json.RawMessage(`{"operationsWouldApply":1}`)
	run.PromptTokens = 1200
	run.CompletionTokens = 300
	run.ElapsedMilliseconds = 2100
	run.Status = domain.MeetingTreeAuditCompleted
	run.Result = "shadow"
	run.Disposition = "would_apply"
	run.CompletedAt = &completed
	if err := repository.SaveMeetingTreeAuditRun(ctx, run); err != nil {
		t.Fatalf("SaveMeetingTreeAuditRun(shadow completed) error = %v", err)
	}
	latest, err := repository.GetLatestMeetingTreeAuditRun(ctx, "session_lifecycle")
	if err != nil {
		t.Fatalf("GetLatestMeetingTreeAuditRun() error = %v", err)
	}
	if latest.ID != "audit-shadow-1" || latest.Result != "shadow" || latest.Disposition != "would_apply" ||
		!latest.ProviderCalled || latest.RawResponse == "" || len(latest.Findings) == 0 ||
		len(latest.Operations) == 0 || len(latest.ValidatorResult) == 0 || len(latest.InputPayload) == 0 {
		t.Fatalf("latest shadow run = %+v, want persisted fields", latest)
	}
	// disposition完了後は同一キーで再claimできる(部分uniqueはstatus=runningのみ)。
	duplicate.CreatedAt = now.Add(4 * time.Second)
	if claimed, err := repository.TryStartMeetingTreeAuditRun(ctx, duplicate); err != nil || !claimed {
		t.Fatalf("reclaim after completion claimed=%t err=%v", claimed, err)
	}

	// 4. provider error更新: 失敗runのerror_code/error_message保存。
	failedAt := now.Add(5 * time.Second)
	failed := duplicate
	failed.Status = domain.MeetingTreeAuditFailed
	failed.Result = "timeout"
	failed.Disposition = "rejected"
	failed.ProviderCalled = true
	failed.ErrorCode = "timeout"
	failed.ErrorMessage = "context deadline exceeded"
	failed.CompletedAt = &failedAt
	if err := repository.SaveMeetingTreeAuditRun(ctx, failed); err != nil {
		t.Fatalf("SaveMeetingTreeAuditRun(provider error) error = %v", err)
	}
	latest, err = repository.GetLatestMeetingTreeAuditRun(ctx, "session_lifecycle")
	if err != nil || latest.ErrorCode != "timeout" || latest.ErrorMessage == "" {
		t.Fatalf("failed run = %+v err=%v, want error fields", latest, err)
	}

	// 5. final tree review run保存。
	finalDone := now.Add(10 * time.Second)
	finalReview := domain.MeetingTreeAuditRun{
		ID: "audit-final-1", SessionID: "session_lifecycle", BasedOnTreeVersion: 15,
		Mode: domain.MeetingTreeAuditShadow, TriggerReason: "meeting_ended",
		TriggerClass: domain.MeetingTreeAuditTriggerFinal, Task: "final_tree_review",
		Deployment: "ds-gpt-5-mini", Model: "gpt-5-mini-2025-08-07", PromptVersion: "v1",
		SnapshotHash: "snapshot-15", Status: domain.MeetingTreeAuditCompleted,
		Result: "shadow", Disposition: "would_apply", ProviderCalled: true,
		Findings: json.RawMessage(`[]`), Operations: json.RawMessage(`[]`),
		ValidatorResult: json.RawMessage(`{"operationsWouldApply":0}`),
		CreatedAt:       now.Add(8 * time.Second), CompletedAt: &finalDone,
	}
	if claimed, err := repository.TryStartMeetingTreeAuditRun(ctx, finalReview); err != nil || !claimed {
		t.Fatalf("final review claim=%t err=%v", claimed, err)
	}
	if err := repository.SaveMeetingTreeAuditRun(ctx, finalReview); err != nil {
		t.Fatalf("SaveMeetingTreeAuditRun(final review) error = %v", err)
	}

	// 6. provider_called=falseで終了したrunはprovider call数に加算されない。
	suppressedAt := now.Add(12 * time.Second)
	suppressed := domain.MeetingTreeAuditRun{
		ID: "audit-suppressed-1", SessionID: "session_lifecycle", BasedOnTreeVersion: 16,
		Mode: domain.MeetingTreeAuditShadow, TriggerReason: "interval_versions",
		TriggerClass: domain.MeetingTreeAuditTriggerNormal, Task: "tree_audit",
		Deployment: "ds-gpt-5-mini", PromptVersion: "v1", SnapshotHash: "snapshot-16",
		Status: domain.MeetingTreeAuditSkipped, Result: "rate_limited",
		Disposition: "suppressed", SuppressionReason: "normal_hourly_limit",
		ProviderCalled: false, CreatedAt: now.Add(11 * time.Second), CompletedAt: &suppressedAt,
	}
	if _, err := repository.TryStartMeetingTreeAuditRun(ctx, suppressed); err != nil {
		t.Fatalf("suppressed claim error = %v", err)
	}
	if err := repository.SaveMeetingTreeAuditRun(ctx, suppressed); err != nil {
		t.Fatalf("SaveMeetingTreeAuditRun(suppressed) error = %v", err)
	}
	normalCalls, err := repository.CountMeetingTreeAuditProviderCalls(ctx, "session_lifecycle", domain.MeetingTreeAuditTriggerNormal, time.Time{})
	if err != nil || normalCalls != 0 {
		t.Fatalf("normal provider calls = %d err=%v, want 0 (suppressed run must not count)", normalCalls, err)
	}
	highCalls, err := repository.CountMeetingTreeAuditProviderCalls(ctx, "session_lifecycle", domain.MeetingTreeAuditTriggerHigh, time.Time{})
	if err != nil || highCalls != 2 {
		t.Fatalf("high provider calls = %d err=%v, want 2 (shadow + failed)", highCalls, err)
	}
	// final_tree_reviewはprovider call制限の対象外。
	finalCalls, err := repository.CountMeetingTreeAuditProviderCalls(ctx, "session_lifecycle", domain.MeetingTreeAuditTriggerFinal, time.Time{})
	if err != nil || finalCalls != 0 {
		t.Fatalf("final provider calls = %d err=%v, want 0", finalCalls, err)
	}

	// 7. transaction rollback: 存在しないliveバージョンへのapplyは何も書かない。
	before := auditRunRowCount(t, db)
	orphan := run
	orphan.ID = "audit-rollback-1"
	orphan.SnapshotHash = "snapshot-rollback"
	if _, applied, err := repository.ApplyMeetingTreeAudit(ctx, orphan, 999, domain.MeetingAIAnalysis{
		SessionID: "session_lifecycle", Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 1000,
		Payload: json.RawMessage(`{"treeVersion":1000}`), UpdatedAt: finalDone,
	}); err != nil || applied {
		t.Fatalf("rollback apply applied=%t err=%v, want no-op", applied, err)
	}
	if after := auditRunRowCount(t, db); after != before {
		t.Fatalf("audit run rows after rollback = %d, want %d", after, before)
	}
}

func auditRunRowCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM meeting_tree_audit_runs").Scan(&count); err != nil {
		t.Fatalf("count meeting_tree_audit_runs: %v", err)
	}
	return count
}
