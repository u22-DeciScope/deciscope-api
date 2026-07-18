package application

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
	"time"
)

// 対象session(session_497ed2b0aedf9dc6)の回帰: 監査runのINSERTが失敗した後、
// providerを一度も呼んでいないのに後続の監査が min interval で rate limited に
// なり続けた。provider未呼び出しの失敗はmin intervalを消費せず、短いbackoff後に
// 再試行できること。
func TestTreeAuditRepositoryFailureDoesNotConsumeProviderMinInterval(t *testing.T) {
	service, _, auditRepo, _, completer, payload := newTreeAuditRunnerFixture(t, false)
	service.config.TreeAudit.MinInterval = 5 * time.Minute
	base := time.Date(2026, 7, 17, 6, 8, 0, 0, time.UTC)
	current := base
	service.now = func() time.Time { return current }

	auditRepo.tryStartErr = errors.New(`ERROR: INSERT has more expressions than target columns (SQLSTATE 42601)`)
	service.scheduleTreeAudit(context.Background(), "session_26959b9519c5f880", "semantic_anomaly", payload, 12)
	waitForInternalAudit(t, time.Second, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return !service.sessionStateLocked("session_26959b9519c5f880").auditRunning
	})

	service.mu.Lock()
	state := service.sessionStateLocked("session_26959b9519c5f880")
	lastAuditAt := state.lastAuditAt
	backoffUntil := state.auditRepoBackoffUntil
	service.mu.Unlock()
	if !lastAuditAt.IsZero() {
		t.Fatalf("lastAuditAt = %s, want zero (provider was never called)", lastAuditAt)
	}
	if !backoffUntil.Equal(base.Add(treeAuditRepositoryFailureBackoff)) {
		t.Fatalf("auditRepoBackoffUntil = %s, want %s", backoffUntil, base.Add(treeAuditRepositoryFailureBackoff))
	}
	if completer.callCount() != 0 {
		t.Fatalf("provider calls = %d, want 0", completer.callCount())
	}

	// backoff中はDBへ再試行しない(無限ループ防止)。
	auditRepo.tryStartErr = nil
	current = base.Add(treeAuditRepositoryFailureBackoff / 2)
	service.scheduleTreeAudit(context.Background(), "session_26959b9519c5f880", "semantic_anomaly", payload, 12)
	service.mu.Lock()
	pendingDuringBackoff := state.auditPending
	runningDuringBackoff := state.auditRunning
	service.mu.Unlock()
	if runningDuringBackoff || !pendingDuringBackoff {
		t.Fatalf("during backoff running=%t pending=%t, want deferred", runningDuringBackoff, pendingDuringBackoff)
	}

	// backoff経過後はmin intervalを待たずに再試行でき、providerまで到達する。
	current = base.Add(treeAuditRepositoryFailureBackoff + time.Second)
	service.scheduleTreeAudit(context.Background(), "session_26959b9519c5f880", "semantic_anomaly", payload, 12)
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 1 })
	waitForInternalAudit(t, time.Second, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return !service.sessionStateLocked("session_26959b9519c5f880").auditRunning
	})
	if run := auditRepo.latest(); run == nil || run.Result != "applied" || !run.ProviderCalled {
		t.Fatalf("recovered run = %+v, want applied run with providerCalled", run)
	}
	service.mu.Lock()
	if state.lastAuditAt.IsZero() {
		t.Fatalf("lastAuditAt must be set after a provider call")
	}
	service.mu.Unlock()
}

// fake providerでの監査とfinal tree reviewが、運用時に確認するログ
// (AI task completed. task=tree_audit / Tree audit completed. disposition= /
// task=final_tree_review)を出すこと。監査とfinal reviewは同一sessionの同一
// treeVersionを対象にすると片方の適用がもう片方をstale化させるため、CASの
// 干渉を避けて独立に確認できるよう別々のfixtureを使う。
func TestTreeAuditAndFinalReviewEmitOperationalLogs(t *testing.T) {
	auditService, _, auditRepo, _, auditCompleter, auditPayload := newTreeAuditRunnerFixture(t, false)
	reviewService, _, _, _, reviewCompleter, reviewPayload := newTreeAuditRunnerFixture(t, false)

	var buffer bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buffer)
	defer log.SetOutput(previous)

	execution, err := auditService.runTreeAudit(context.Background(), "session_26959b9519c5f880", "semantic_anomaly", aiTaskTreeAudit, auditPayload, 12, false)
	if err != nil || execution.Result != "applied" || !execution.ProviderCalled {
		t.Fatalf("tree audit execution = %+v err=%v", execution, err)
	}
	if run := auditRepo.latest(); run == nil || run.Task != "tree_audit" || run.Result != "applied" {
		t.Fatalf("tree audit run = %+v", run)
	}

	finalExecution, err := reviewService.runFinalTreeReview(context.Background(), "session_26959b9519c5f880", reviewPayload, 12)
	if err != nil || finalExecution.Result != "applied" {
		t.Fatalf("final review execution = %+v err=%v", finalExecution, err)
	}
	if auditCompleter.callCount() != 1 {
		t.Fatalf("tree audit provider calls = %d, want 1", auditCompleter.callCount())
	}
	if reviewCompleter.callCount() != 1 {
		t.Fatalf("final review provider calls = %d, want 1", reviewCompleter.callCount())
	}

	logged := buffer.String()
	for _, want := range []string{
		"AI task completed. task=tree_audit deployment=tree-audit-mini",
		"Tree audit completed.",
		"result=applied",
		"disposition=applied",
		"AI task completed. task=final_tree_review deployment=tree-audit-mini",
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("log output missing %q", want)
		}
	}
}
