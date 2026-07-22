package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	postgresrepository "deciscope-core-api/internal/adapter/repository/postgres"
	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
	"deciscope-core-api/internal/infrastructure/azureopenai"
	"deciscope-core-api/internal/infrastructure/database"
)

func TestTreeAuditTombstoneSurvivesPostgreSQLReloadAndFinalization(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("DATABASE_TEST_URL"))
	if baseURL == "" {
		t.Skip("DATABASE_TEST_URL is required for isolated PostgreSQL integration")
	}
	requireTestDatabaseURL(t, baseURL)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, db := createIsolatedRealAIDatabase(t, ctx, baseURL)
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate isolated tombstone database: %v", err)
	}

	const sessionID = "session_tree_audit_postgres_reload"
	seedRealTreeAuditFixture(t, ctx, db, sessionID)
	payload := realTreeAuditFixturePayload(t)
	analysisRepo := postgresrepository.NewMeetingAIAnalysisRepository(db)
	if _, err := analysisRepo.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID: sessionID, Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 1, Payload: payload,
		Model: "fixture", SegmentCount: 3, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("persist initial live fixture: %v", err)
	}

	auditResponse := `{
  "basedOnTreeVersion":1,
  "summary":"談話的な導入だけのitemを除去",
  "findings":[{"findingId":"finding-discourse","type":"discourse_only_item","severity":"high","nodeIds":["item-discourse-only"],"currentParentIds":["group-additional"],"relatedNodeIds":[],"evidenceSequenceNos":[1],"reason":"独立命題を持たない話題転換","confidence":0.99}],
  "operations":[{"operationId":"deactivate-discourse","type":"deactivate_item","targetCanonicalItemId":"item-discourse-only","targetCanonicalNodeId":"","targetCanonicalItemIds":[],"targetCandidateId":"","fromParentCanonicalNodeId":"","toParentCanonicalNodeId":"","label":"","reason":"discourse_only_item","confidence":0.99,"evidenceSequenceNos":[1],"dependsOnOperationIds":[]}]
}`
	firstCompleter := newPersistenceCompleter(map[string][]string{
		"discussion_tree_audit": {auditResponse},
		"context_planner":       {`{"purpose":"VPN証明書対応","agendaItems":[],"aiDirectives":[]}`},
	})
	firstService := newPersistenceMeetingAnalysisService(db, firstCompleter, false)
	result, err := firstService.ReplayTreeAudit(ctx, sessionID, payload, 1)
	if err != nil {
		t.Fatalf("fake-provider tree audit: %v", err)
	}
	if result.FindingsCount != 1 || result.OperationsProposed < 1 || result.OperationsCanonicalized < 1 || result.OperationsValid < 1 ||
		result.OperationsApplied < 1 || result.ResultClassification != domain.MeetingTreeAuditResultApplied || result.ResultTreeVersion <= result.BaseTreeVersion || !result.IntegrityValid {
		t.Fatalf("fake-provider audit aggregate = %+v", result)
	}
	if err := firstService.Close(); err != nil {
		t.Fatalf("close first service: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close first database connection: %v", err)
	}

	// A brand-new connection, repositories, and application service emulate an
	// API process restart. No in-memory session state is carried across.
	reloadedDB, err := database.Open(ctx, database.Config{URL: databaseURL})
	if err != nil {
		t.Fatalf("reopen isolated database: %v", err)
	}
	t.Cleanup(func() { _ = reloadedDB.Close() })
	reloadedAnalysisRepo := postgresrepository.NewMeetingAIAnalysisRepository(reloadedDB)
	reloadedAuditRepo := postgresrepository.NewMeetingTreeAuditRepository(reloadedDB)
	if err := reloadedAuditRepo.CheckMeetingTreeAuditRepository(ctx); err != nil {
		t.Fatalf("reloaded audit repository is not ready: %v", err)
	}
	var startupLogs bytes.Buffer
	startupPreviousWriter := log.Writer()
	log.SetOutput(&startupLogs)
	startupService := buildMeetingAnalysisService(AIConfig{
		AzureOpenAI: azureopenai.Config{Endpoint: "https://example.invalid", APIKey: "integration-placeholder", Deployment: "fake-default"},
		TaskModels:  AITaskModelsConfig{TreeAudit: "fake-mini", FinalTreeReview: "fake-mini"},
		TreeAudit:   application.TreeAuditConfig{Enabled: true},
	}, reloadedDB, postgresrepository.NewMeetingSessionRepository(reloadedDB), nil)
	_ = startupService.Close()
	log.SetOutput(startupPreviousWriter)
	if configurationLog := startupLogs.String(); !strings.Contains(configurationLog, "enabled=true") ||
		!strings.Contains(configurationLog, "repositoryReady=true") || !strings.Contains(configurationLog, "schedulerRegistered=true") ||
		!strings.Contains(configurationLog, "reason=ready") {
		t.Fatalf("startup-equivalent audit wiring is not ready: %s", configurationLog)
	}
	audited, err := reloadedAnalysisRepo.GetMeetingAIAnalysis(ctx, sessionID, domain.MeetingAIAnalysisLive)
	if err != nil {
		t.Fatalf("reload audited live analysis: %v", err)
	}
	if audited.Version != 2 {
		t.Fatalf("audited live version = %d, want 2", audited.Version)
	}
	assertPersistedAuditTombstone(t, audited.Payload, result.AuditRunID)
	auditedTree := decodePersistenceTree(t, audited.Payload)

	resurrection := `{
  "summary":"VPN証明書対応","currentTopic":"VPN証明書の期限切れ対応","resolvedIds":[],"resolutionUpdates":[],
  "utteranceRoles":[{"sequenceNo":1,"role":"discourse_transition"}],
  "items":[{"clientKey":"same-discourse-item","kind":"todo","severity":"medium","title":"別の問題の存在を確認","body":"アジェンダ外の別問題があるとの紹介","status":"open","evidenceSequenceNos":[1]}],
  "newTopics":[{"id":"topic-additional","label":"追加論点","description":"別件"}],
  "assignments":[{"nodeId":"same-discourse-item","parentTopicId":"topic-additional","confidence":0.8,"reason":"別件"}]
}`
	finalReview := `{"basedOnTreeVersion":3,"summary":"変更なし","findings":[],"operations":[]}`
	finalSummary := `{"suggestedTitle":"VPN証明書対応","overview":"証明書更新を確認した。","decisions":[],"actionItems":[],"openIssues":[],"keyPoints":[],"nextMeetingTopics":[]}`
	restartCompleter := newPersistenceCompleter(map[string][]string{
		"live_analysis_diff":    {resurrection},
		"discussion_tree_audit": {finalReview},
		"final_summary":         {finalSummary},
		"context_planner":       {`{"purpose":"VPN証明書対応","agendaItems":[],"aiDirectives":[]}`},
	})
	restartedService := newPersistenceMeetingAnalysisService(reloadedDB, restartCompleter, true)

	var logs bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousWriter) })
	if err := restartedService.FinalizeMeetingSession(ctx, domain.MeetingSession{ID: sessionID}, application.MeetingSessionFinalizationRequest{
		BotLastForwardedFinalSequence: 3,
		TranscriptQueueDrained:        true,
	}); err != nil {
		t.Fatalf("finalize after service restart: %v", err)
	}
	log.SetOutput(previousWriter)
	if !strings.Contains(logs.String(), "itemResurrectionPrevented=1") {
		t.Fatalf("restart live round did not report tombstone prevention; logs=%s", logs.String())
	}
	if restartCompleter.callCount("live_analysis_diff") != 1 || restartCompleter.callCount("discussion_tree_audit") != 1 || restartCompleter.callCount("final_summary") != 1 {
		t.Fatalf("unexpected restart provider calls: %+v", restartCompleter.callSnapshot())
	}

	liveAfterFinalization, err := reloadedAnalysisRepo.GetMeetingAIAnalysis(ctx, sessionID, domain.MeetingAIAnalysisLive)
	if err != nil {
		t.Fatalf("load live analysis after finalization: %v", err)
	}
	if liveAfterFinalization.Version != 3 {
		t.Fatalf("live version after restart/finalization = %d, want 3", liveAfterFinalization.Version)
	}
	assertPersistedAuditTombstone(t, liveAfterFinalization.Payload, result.AuditRunID)
	liveTree := decodePersistenceTree(t, liveAfterFinalization.Payload)
	assertPersistenceTreeOutcome(t, liveTree)
	if len(liveTree.Nodes) != len(auditedTree.Nodes) {
		t.Fatalf("active node count increased after resurrection attempt: before=%d after=%d", len(auditedTree.Nodes), len(liveTree.Nodes))
	}

	finalTreeRow, err := reloadedAnalysisRepo.GetMeetingAIAnalysis(ctx, sessionID, domain.MeetingAIAnalysisTree)
	if err != nil {
		t.Fatalf("load final tree snapshot: %v", err)
	}
	finalTree := decodePersistenceTree(t, finalTreeRow.Payload)
	assertPersistenceTreeOutcome(t, finalTree)
	assertPersistenceTreeIntegrity(t, finalTree)
	finalization, err := reloadedAnalysisRepo.GetMeetingAIAnalysis(ctx, sessionID, domain.MeetingAIAnalysisFinalization)
	if err != nil || finalization.Status != domain.MeetingAIAnalysisCompleted {
		t.Fatalf("finalization row = %+v error=%v", finalization, err)
	}
	if err := restartedService.Close(); err != nil {
		t.Fatalf("close restarted service: %v", err)
	}
	if err := reloadedDB.Close(); err != nil {
		t.Fatalf("close reloaded database connection: %v", err)
	}

	// Reload the final snapshot through a third repository instance, then call
	// finalization again. A completed finalization must be a provider-free,
	// idempotent no-op and must not rewrite or resurrect the tree.
	finalDB, err := database.Open(ctx, database.Config{URL: databaseURL})
	if err != nil {
		t.Fatalf("open final reload database: %v", err)
	}
	t.Cleanup(func() { _ = finalDB.Close() })
	finalRepo := postgresrepository.NewMeetingAIAnalysisRepository(finalDB)
	reloadedFinalTreeRow, err := finalRepo.GetMeetingAIAnalysis(ctx, sessionID, domain.MeetingAIAnalysisTree)
	if err != nil {
		t.Fatalf("reload final tree through third repository: %v", err)
	}
	if !bytes.Equal(finalTreeRow.Payload, reloadedFinalTreeRow.Payload) || finalTreeRow.Version != reloadedFinalTreeRow.Version {
		t.Fatal("final tree payload/version changed during database round-trip")
	}
	idempotentCompleter := newPersistenceCompleter(nil)
	idempotentService := newPersistenceMeetingAnalysisService(finalDB, idempotentCompleter, true)
	if err := idempotentService.FinalizeMeetingSession(ctx, domain.MeetingSession{ID: sessionID}, application.MeetingSessionFinalizationRequest{
		BotLastForwardedFinalSequence: 3,
		TranscriptQueueDrained:        true,
	}); err != nil {
		t.Fatalf("idempotent finalization: %v", err)
	}
	if calls := idempotentCompleter.callSnapshot(); len(calls) != 0 {
		t.Fatalf("idempotent finalization called provider: %+v", calls)
	}
	afterIdempotent, err := finalRepo.GetMeetingAIAnalysis(ctx, sessionID, domain.MeetingAIAnalysisTree)
	if err != nil || !bytes.Equal(reloadedFinalTreeRow.Payload, afterIdempotent.Payload) || reloadedFinalTreeRow.Version != afterIdempotent.Version {
		t.Fatalf("tree changed after idempotent finalization: before=%+v after=%+v error=%v", reloadedFinalTreeRow, afterIdempotent, err)
	}
}

func newPersistenceMeetingAnalysisService(db *sql.DB, completer application.AIChatCompleter, finalEnabled bool) *application.MeetingAnalysisService {
	service := application.NewMeetingAnalysisService(
		postgresrepository.NewMeetingAIAnalysisRepository(db),
		postgresrepository.NewTranscriptSegmentRepository(db),
		postgresrepository.NewMeetingSessionRepository(db),
		completer,
		application.MeetingAnalysisConfig{
			Enabled: true, LiveEnabled: true, LiveInterval: time.Hour, LiveMinChars: 1,
			LiveMaxInputChars: 12000, LiveRequestTimeout: 2 * time.Second,
			FinalEnabled: finalEnabled, FinalMaxInputChars: 12000, FinalRequestTimeout: 2 * time.Second,
			FinalizationWaitTimeout: 2 * time.Second, FinalizationQuietPeriod: time.Millisecond, FinalFlushMaxAttempts: 1,
			Model: "fake", TaskModels: application.AITaskModels{TreeAudit: "fake-mini", FinalTreeReview: "fake-mini", FinalSummary: "fake-summary"},
			TreeAudit: application.TreeAuditConfig{Enabled: true, MinInterval: time.Millisecond, Timeout: 2 * time.Second, MaxOutputTokens: 2500, UnappliedWarningThreshold: 3},
		},
	)
	service.SetMeetingTreeAuditRepository(postgresrepository.NewMeetingTreeAuditRepository(db))
	return service
}

type persistenceCompleter struct {
	mu        sync.Mutex
	responses map[string][]string
	calls     map[string]int
}

func newPersistenceCompleter(responses map[string][]string) *persistenceCompleter {
	copied := make(map[string][]string, len(responses))
	for key, values := range responses {
		copied[key] = append([]string(nil), values...)
	}
	return &persistenceCompleter{responses: copied, calls: make(map[string]int)}
}

func (c *persistenceCompleter) Complete(_ context.Context, request application.AIChatRequest) (application.AIChatResult, error) {
	key := "final_summary"
	if request.ResponseSchema != nil {
		key = request.ResponseSchema.Name
	} else if strings.Contains(request.System, "会議設計アシスタント") {
		key = "context_planner"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[key]++
	queue := c.responses[key]
	if len(queue) == 0 {
		return application.AIChatResult{}, errors.New("unexpected fake provider call: " + key)
	}
	content := queue[0]
	c.responses[key] = queue[1:]
	return application.AIChatResult{Content: content, Model: "fake-gpt-5-mini", PromptTokens: 10, CompletionTokens: 5}, nil
}

func (c *persistenceCompleter) callCount(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[key]
}

func (c *persistenceCompleter) callSnapshot() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make(map[string]int, len(c.calls))
	for key, value := range c.calls {
		result[key] = value
	}
	return result
}

type persistencePayload struct {
	Items []struct {
		ID       string `json:"id"`
		Inactive bool   `json:"inactive"`
	} `json:"items"`
	ItemTombstones []struct {
		CanonicalItemID     string   `json:"canonicalItemId"`
		PropositionKey      string   `json:"propositionKey"`
		SemanticKeyHash     string   `json:"semanticKeyHash"`
		EvidenceFingerprint string   `json:"evidenceFingerprint"`
		CandidateAliases    []string `json:"candidateAliases"`
		Reason              string   `json:"reason"`
		MergedIntoItemID    string   `json:"mergedIntoItemId"`
		CreatedBy           string   `json:"createdBy"`
		CreatedAtVersion    int64    `json:"createdAtVersion"`
		SourceTreeVersion   int64    `json:"sourceTreeVersion"`
		AuditRunID          string   `json:"auditRunId"`
		ReopenedAtVersion   int64    `json:"reopenedAtVersion"`
		ReopenReason        string   `json:"reopenReason"`
	} `json:"itemTombstones"`
}

func assertPersistedAuditTombstone(t *testing.T, payload json.RawMessage, auditRunID string) {
	t.Helper()
	var state persistencePayload
	if err := json.Unmarshal(payload, &state); err != nil {
		t.Fatalf("decode persisted tombstone payload: %v", err)
	}
	if len(state.ItemTombstones) != 1 {
		t.Fatalf("persisted tombstones = %+v, want one", state.ItemTombstones)
	}
	tombstone := state.ItemTombstones[0]
	if tombstone.CanonicalItemID != "item-discourse-only" || tombstone.PropositionKey != "prop-discourse-only" ||
		tombstone.SemanticKeyHash == "" || tombstone.EvidenceFingerprint == "" || !containsPersistenceString(tombstone.CandidateAliases, "group-additional") ||
		tombstone.Reason != "discourse_only" || tombstone.MergedIntoItemID != "" || tombstone.CreatedBy != "tree_auditor" ||
		tombstone.CreatedAtVersion != 2 || tombstone.SourceTreeVersion != 1 || tombstone.AuditRunID != auditRunID ||
		tombstone.ReopenedAtVersion != 0 || tombstone.ReopenReason != "" {
		t.Fatalf("persisted tombstone fields = %+v", tombstone)
	}
	inactive := false
	for _, item := range state.Items {
		if item.ID == tombstone.CanonicalItemID {
			inactive = item.Inactive
		}
	}
	if !inactive {
		t.Fatalf("deactivated item did not remain inactive: %+v", state.Items)
	}
}

type persistenceTree struct {
	Nodes []struct {
		ID       string `json:"id"`
		ParentID string `json:"parentId"`
		Label    string `json:"label"`
	} `json:"nodes"`
	Edges []struct {
		Source string `json:"source"`
		Target string `json:"target"`
	} `json:"edges"`
}

func decodePersistenceTree(t *testing.T, payload json.RawMessage) persistenceTree {
	t.Helper()
	var envelope struct {
		Tree persistenceTree `json:"tree"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode persisted tree: %v", err)
	}
	return envelope.Tree
}

func assertPersistenceTreeOutcome(t *testing.T, tree persistenceTree) {
	t.Helper()
	nodes := make(map[string]string, len(tree.Nodes))
	for _, node := range tree.Nodes {
		nodes[node.ID] = node.Label
	}
	for _, id := range []string{"item-discourse-only", "group-additional", "topic-additional"} {
		if _, exists := nodes[id]; exists {
			t.Fatalf("unwanted node %s is active: %+v", id, nodes)
		}
	}
	for _, id := range []string{"root", "topic-vpn-expiry", "item-risk-vpn-expiry", "item-todo-vpn-update", "agenda-impact", "agenda-cause", "agenda-prevention"} {
		if _, exists := nodes[id]; !exists {
			t.Fatalf("required node %s is missing: %+v", id, nodes)
		}
	}
}

func assertPersistenceTreeIntegrity(t *testing.T, tree persistenceTree) {
	t.Helper()
	nodes := make(map[string]struct{}, len(tree.Nodes))
	for _, node := range tree.Nodes {
		if node.ID == "" {
			t.Fatal("tree contains an empty node ID")
		}
		if _, duplicate := nodes[node.ID]; duplicate {
			t.Fatalf("tree contains duplicate node ID %s", node.ID)
		}
		nodes[node.ID] = struct{}{}
	}
	for _, node := range tree.Nodes {
		if node.ParentID == "" {
			continue
		}
		if node.ParentID == node.ID {
			t.Fatalf("node %s is self-parented", node.ID)
		}
		if _, exists := nodes[node.ParentID]; !exists {
			t.Fatalf("node %s has missing parent %s", node.ID, node.ParentID)
		}
	}
	for _, edge := range tree.Edges {
		if edge.Source == edge.Target {
			t.Fatalf("self edge = %+v", edge)
		}
		if _, exists := nodes[edge.Source]; !exists {
			t.Fatalf("edge has missing source: %+v", edge)
		}
		if _, exists := nodes[edge.Target]; !exists {
			t.Fatalf("edge has missing target: %+v", edge)
		}
	}
}

func containsPersistenceString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
