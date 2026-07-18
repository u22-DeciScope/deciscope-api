package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	postgresrepository "deciscope-core-api/internal/adapter/repository/postgres"
	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
	"deciscope-core-api/internal/infrastructure/database"
)

// TestRealTreeAuditGPT5Mini is an opt-in destructive-to-its-own-temporary-DB
// integration test. It never accepts the normal DATABASE_URL and refuses a
// base database whose name does not visibly identify it as test-only.
func TestRealTreeAuditGPT5Mini(t *testing.T) {
	enabled, err := evaluateRealAITestEnvironment(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Skip("RUN_REAL_AI_INTEGRATION_TESTS=true is required")
	}
	baseURL := strings.TrimSpace(os.Getenv("DATABASE_REAL_AI_TEST_URL"))
	requireTestDatabaseURL(t, baseURL)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	databaseURL, db := createIsolatedRealAIDatabase(t, ctx, baseURL)
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate isolated real-AI database: %v", err)
	}

	const sessionID = "session_real_tree_audit_integration"
	seedRealTreeAuditFixture(t, ctx, db, sessionID)
	payload := realTreeAuditFixturePayload(t)
	analysisRepository := postgresrepository.NewMeetingAIAnalysisRepository(db)
	if _, err := analysisRepository.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID: sessionID, Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 1, Payload: payload,
		Model: "fixture", SegmentCount: 3, InputChars: 120, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("persist real-AI fixture live analysis: %v", err)
	}

	config := aiConfigFromEnv()
	deployment := strings.TrimSpace(os.Getenv("AZURE_OPENAI_TREE_AUDIT_DEPLOYMENT"))
	if strings.TrimSpace(config.AzureOpenAI.Deployment) == "" {
		config.AzureOpenAI.Deployment = deployment
	}
	config.TaskModels.TreeAudit = deployment
	config.TaskModels.FinalTreeReview = deployment
	config.TreeAudit.Enabled = true
	config.TreeAudit.Timeout = 60 * time.Second
	config.TreeAudit.MaxOutputTokens = 2500
	config.LiveAnalysisEnabled = false
	service := buildMeetingAnalysisService(config, db, postgresrepository.NewMeetingSessionRepository(db), nil)
	t.Cleanup(func() { _ = service.Close() })

	var resultVersion int64
	var finalResult string
	var finalRunID string
	for attempt := 1; attempt <= 2; attempt++ {
		current, err := analysisRepository.GetMeetingAIAnalysis(ctx, sessionID, domain.MeetingAIAnalysisLive)
		if err != nil {
			t.Fatalf("load live fixture before attempt %d: %v", attempt, err)
		}
		result, err := service.ReplayTreeAudit(ctx, sessionID, current.Payload, current.Version)
		finalResult, finalRunID, resultVersion = result.Result, result.AuditRunID, result.ResultTreeVersion
		t.Logf("real tree audit attempt=%d runId=%s result=%s classification=%s findings=%d proposed=%d canonicalized=%d valid=%d applied=%d baseVersion=%d resultVersion=%d integrity=%t database=%s",
			attempt, result.AuditRunID, result.Result, result.ResultClassification, result.FindingsCount,
			result.OperationsProposed, result.OperationsCanonicalized, result.OperationsValid,
			result.OperationsApplied, result.BaseTreeVersion, result.ResultTreeVersion,
			result.IntegrityValid, safeDatabaseName(databaseURL))
		if err == nil && result.OperationsApplied > 0 {
			if result.FindingsCount <= 0 || result.OperationsProposed <= 0 || result.OperationsCanonicalized <= 0 || result.OperationsValid <= 0 ||
				result.ResultTreeVersion <= result.BaseTreeVersion || !result.IntegrityValid || result.ResultClassification != domain.MeetingTreeAuditResultApplied {
				t.Fatalf("real audit aggregate expectations failed: %+v", result)
			}
			break
		}
		if err != nil && attempt == 2 {
			t.Fatalf("real audit failed after bounded retry: runId=%s result=%s error=%v", result.AuditRunID, result.Result, err)
		}
	}

	current, err := analysisRepository.GetMeetingAIAnalysis(ctx, sessionID, domain.MeetingAIAnalysisLive)
	if err != nil {
		t.Fatalf("load audited fixture: %v", err)
	}
	if current.Version <= 1 {
		t.Fatalf("real audit did not create a new tree version: runId=%s result=%s version=%d aggregateVersion=%d", finalRunID, finalResult, current.Version, resultVersion)
	}
	assertRealTreeAuditOutcome(t, current.Payload)

	// Carry the real auditor's persisted result across fresh DB/repository/
	// service instances. The follow-up provider is deliberately deterministic:
	// this stage validates persistence, resurrection filtering, final review,
	// and finalization rather than making an unrelated second Azure behavior
	// a prerequisite for the real auditor assertion above.
	if err := service.Close(); err != nil {
		t.Fatalf("close real-audit service before reload: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close real-audit database before reload: %v", err)
	}
	reloadedDB, err := database.Open(ctx, database.Config{URL: databaseURL})
	if err != nil {
		t.Fatalf("reopen real-audit database: %v", err)
	}
	t.Cleanup(func() { _ = reloadedDB.Close() })
	resurrection := `{
  "summary":"VPN証明書対応","currentTopic":"VPN証明書の期限切れ対応","resolvedIds":[],"resolutionUpdates":[],
  "utteranceRoles":[{"sequenceNo":1,"role":"discourse_transition"}],
  "items":[{"clientKey":"same-discourse-item","kind":"todo","severity":"medium","title":"別の問題の存在を確認","body":"アジェンダ外の別問題があるとの紹介","status":"open","evidenceSequenceNos":[1]}],
  "newTopics":[{"id":"topic-additional","label":"追加論点","description":"別件"}],
  "assignments":[{"nodeId":"same-discourse-item","parentTopicId":"topic-additional","confidence":0.8,"reason":"別件"}]
}`
	finalReview := fmt.Sprintf(`{"basedOnTreeVersion":%d,"summary":"変更なし","findings":[],"operations":[]}`, current.Version+1)
	restartCompleter := newPersistenceCompleter(map[string][]string{
		"live_analysis_diff":    {resurrection},
		"discussion_tree_audit": {finalReview},
		"final_summary":         {`{"suggestedTitle":"VPN証明書対応","overview":"証明書更新を確認した。","decisions":[],"actionItems":[],"openIssues":[],"keyPoints":[],"nextMeetingTopics":[]}`},
		"context_planner":       {`{"purpose":"VPN証明書対応","agendaItems":[],"aiDirectives":[]}`},
	})
	restartedService := newPersistenceMeetingAnalysisService(reloadedDB, restartCompleter, true)
	if err := restartedService.FinalizeMeetingSession(ctx, domain.MeetingSession{ID: sessionID}, application.MeetingSessionFinalizationRequest{
		BotLastForwardedFinalSequence: 3,
		TranscriptQueueDrained:        true,
	}); err != nil {
		t.Fatalf("finalize real-audit payload after service reload: %v", err)
	}
	_ = restartedService.Close()
	reloadedAnalysisRepo := postgresrepository.NewMeetingAIAnalysisRepository(reloadedDB)
	liveAfterRestart, err := reloadedAnalysisRepo.GetMeetingAIAnalysis(ctx, sessionID, domain.MeetingAIAnalysisLive)
	if err != nil {
		t.Fatalf("reload live tree after real-audit resurrection attempt: %v", err)
	}
	if liveAfterRestart.Version != current.Version+1 {
		t.Fatalf("live version after real-audit reload = %d, want %d", liveAfterRestart.Version, current.Version+1)
	}
	assertRealTreeAuditOutcome(t, liveAfterRestart.Payload)
	finalTree, err := reloadedAnalysisRepo.GetMeetingAIAnalysis(ctx, sessionID, domain.MeetingAIAnalysisTree)
	if err != nil {
		t.Fatalf("reload final tree after real audit: %v", err)
	}
	assertPersistenceTreeOutcome(t, decodePersistenceTree(t, finalTree.Payload))
	assertPersistenceTreeIntegrity(t, decodePersistenceTree(t, finalTree.Payload))
}

func evaluateRealAITestEnvironment(lookup func(string) string) (bool, error) {
	if !strings.EqualFold(strings.TrimSpace(lookup("RUN_REAL_AI_INTEGRATION_TESTS")), "true") {
		return false, nil
	}
	required := []string{
		"DATABASE_REAL_AI_TEST_URL",
		"AZURE_OPENAI_ENDPOINT",
		"AZURE_OPENAI_API_KEY",
		"AZURE_OPENAI_TREE_AUDIT_DEPLOYMENT",
	}
	missing := make([]string, 0, len(required))
	for _, name := range required {
		if strings.TrimSpace(lookup(name)) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return false, fmt.Errorf(
			"real AI integration test was explicitly enabled, but required environment variables are missing:\n- %s",
			strings.Join(missing, "\n- "),
		)
	}
	if strings.TrimSpace(lookup("AZURE_OPENAI_TREE_AUDIT_DEPLOYMENT")) != "ds-gpt-5-mini" {
		return false, fmt.Errorf("AZURE_OPENAI_TREE_AUDIT_DEPLOYMENT must select ds-gpt-5-mini")
	}
	return true, nil
}

func TestEvaluateRealAITestEnvironment(t *testing.T) {
	const secret = "must-not-appear-secret"
	complete := map[string]string{
		"RUN_REAL_AI_INTEGRATION_TESTS":      "true",
		"DATABASE_REAL_AI_TEST_URL":          "postgres://user:password@postgres/deciscope_real_ai_test",
		"AZURE_OPENAI_ENDPOINT":              "https://example.invalid",
		"AZURE_OPENAI_API_KEY":               secret,
		"AZURE_OPENAI_TREE_AUDIT_DEPLOYMENT": "ds-gpt-5-mini",
	}
	tests := []struct {
		name        string
		overrides   map[string]string
		wantRun     bool
		wantNames   []string
		wantNoError bool
	}{
		{name: "flag missing", overrides: map[string]string{"RUN_REAL_AI_INTEGRATION_TESTS": ""}, wantNoError: true},
		{name: "flag false", overrides: map[string]string{"RUN_REAL_AI_INTEGRATION_TESTS": "false"}, wantNoError: true},
		{name: "complete", wantRun: true, wantNoError: true},
		{name: "database missing", overrides: map[string]string{"DATABASE_REAL_AI_TEST_URL": ""}, wantNames: []string{"DATABASE_REAL_AI_TEST_URL"}},
		{name: "endpoint missing", overrides: map[string]string{"AZURE_OPENAI_ENDPOINT": ""}, wantNames: []string{"AZURE_OPENAI_ENDPOINT"}},
		{name: "api key missing", overrides: map[string]string{"AZURE_OPENAI_API_KEY": ""}, wantNames: []string{"AZURE_OPENAI_API_KEY"}},
		{name: "deployment missing", overrides: map[string]string{"AZURE_OPENAI_TREE_AUDIT_DEPLOYMENT": ""}, wantNames: []string{"AZURE_OPENAI_TREE_AUDIT_DEPLOYMENT"}},
		{name: "multiple missing", overrides: map[string]string{
			"DATABASE_REAL_AI_TEST_URL": "", "AZURE_OPENAI_ENDPOINT": "", "AZURE_OPENAI_API_KEY": "", "AZURE_OPENAI_TREE_AUDIT_DEPLOYMENT": "",
		}, wantNames: []string{"DATABASE_REAL_AI_TEST_URL", "AZURE_OPENAI_ENDPOINT", "AZURE_OPENAI_API_KEY", "AZURE_OPENAI_TREE_AUDIT_DEPLOYMENT"}},
		{name: "wrong deployment", overrides: map[string]string{"AZURE_OPENAI_TREE_AUDIT_DEPLOYMENT": "another-deployment"}, wantNames: []string{"AZURE_OPENAI_TREE_AUDIT_DEPLOYMENT", "ds-gpt-5-mini"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			values := make(map[string]string, len(complete))
			for name, value := range complete {
				values[name] = value
			}
			for name, value := range tc.overrides {
				values[name] = value
			}
			run, err := evaluateRealAITestEnvironment(func(name string) string { return values[name] })
			if tc.wantNoError && err != nil {
				t.Fatalf("evaluateRealAITestEnvironment() error = %v", err)
			}
			if !tc.wantNoError && err == nil {
				t.Fatal("evaluateRealAITestEnvironment() error = nil")
			}
			if run != tc.wantRun {
				t.Fatalf("evaluateRealAITestEnvironment() run = %t, want %t", run, tc.wantRun)
			}
			if err != nil {
				message := err.Error()
				for _, name := range tc.wantNames {
					if !strings.Contains(message, name) {
						t.Errorf("error %q does not include %s", message, name)
					}
				}
				if strings.Contains(message, secret) || strings.Contains(message, complete["DATABASE_REAL_AI_TEST_URL"]) || strings.Contains(message, complete["AZURE_OPENAI_ENDPOINT"]) {
					t.Fatalf("error contains a configured value: %q", message)
				}
			}
		})
	}
}

func TestValidateRealAITestDatabaseURLRejectsNonTestDatabases(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "dedicated test", url: "postgres://user:secret@localhost/deciscope_real_ai_test", wantErr: false},
		{name: "integration suffix", url: "postgresql://user:secret@localhost/deciscope_integration", wantErr: false},
		{name: "production name", url: "postgres://user:secret@localhost/deciscope", wantErr: true},
		{name: "normal development name", url: "postgres://user:secret@localhost/deciscope_dev", wantErr: true},
		{name: "wrong scheme", url: "mysql://user:secret@localhost/deciscope_test", wantErr: true},
		{name: "missing host", url: "postgres:///deciscope_test", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRealAITestDatabaseURL(tc.url)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateRealAITestDatabaseURL(%q) error=%v wantErr=%t", tc.url, err, tc.wantErr)
			}
		})
	}
}

func requireTestDatabaseURL(t *testing.T, rawURL string) {
	t.Helper()
	if err := validateRealAITestDatabaseURL(rawURL); err != nil {
		t.Fatal(err)
	}
}

func validateRealAITestDatabaseURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("DATABASE_REAL_AI_TEST_URL is not a valid URL")
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return fmt.Errorf("DATABASE_REAL_AI_TEST_URL must use postgres or postgresql scheme")
	}
	if strings.TrimSpace(parsed.Hostname()) == "" {
		return fmt.Errorf("DATABASE_REAL_AI_TEST_URL must include a database host")
	}
	databaseName := strings.ToLower(strings.Trim(path.Base(parsed.Path), "/"))
	if databaseName == "" || (!strings.HasSuffix(databaseName, "_test") && !strings.HasSuffix(databaseName, "_integration") && !strings.HasSuffix(databaseName, "_real_ai_test")) {
		return fmt.Errorf("DATABASE_REAL_AI_TEST_URL database %q is not test-only; required suffix: _test, _integration, or _real_ai_test", databaseName)
	}
	return nil
}

func createIsolatedRealAIDatabase(t *testing.T, ctx context.Context, baseURL string) (string, *sql.DB) {
	t.Helper()
	admin, err := database.Open(ctx, database.Config{URL: baseURL})
	if err != nil {
		t.Fatalf("open real-AI test database server: %v", err)
	}
	databaseName := fmt.Sprintf("deciscope_%d_real_ai_test", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE "`+databaseName+`"`); err != nil {
		_ = admin.Close()
		t.Fatalf("create isolated real-AI database: %v", err)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		_ = admin.Close()
		t.Fatalf("parse real-AI test database URL: %v", err)
	}
	parsed.Path = "/" + databaseName
	databaseURL := parsed.String()
	db, err := database.Open(ctx, database.Config{URL: databaseURL})
	if err != nil {
		_, _ = admin.ExecContext(context.Background(), `DROP DATABASE IF EXISTS "`+databaseName+`"`)
		_ = admin.Close()
		t.Fatalf("open isolated real-AI database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = admin.ExecContext(cleanupCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid <> pg_backend_pid()`, databaseName)
		_, _ = admin.ExecContext(cleanupCtx, `DROP DATABASE IF EXISTS "`+databaseName+`"`)
		_ = admin.Close()
	})
	return databaseURL, db
}

func safeDatabaseName(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "invalid"
	}
	return strings.Trim(path.Base(parsed.Path), "/")
}

func seedRealTreeAuditFixture(t *testing.T, ctx context.Context, db *sql.DB, sessionID string) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin real-AI fixture transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO meeting_sessions (id, join_url, join_url_hash, status, requested_at, created_at, updated_at)
		VALUES ($1, $2, $3, 'recording', $4, $4, $4)
	`, sessionID, "https://example.invalid/real-ai-test", "real-ai-test-hash", now); err != nil {
		t.Fatalf("insert real-AI meeting session: %v", err)
	}
	segments := []string{
		"ここで、アジェンダにはなかった別の問題があります。",
		"VPN装置の証明書が来月末に期限切れになり、放置するとリモート接続ができなくなる可能性があります。",
		"高橋さんに今週中に証明書の更新手順と作業可能日を確認してもらいます。",
	}
	for index, text := range segments {
		sequenceNo := index + 1
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO transcript_segments (
				event_id, call_id, sequence_no, recognized_at_utc,
				offset_ticks, duration_ticks, text, received_at_utc, session_id
			) VALUES ($1, $2, $3, $4, 0, 0, $5, $4, $6)
		`, fmt.Sprintf("real-ai-event-%d", sequenceNo), "real-ai-call", sequenceNo, now, text, sessionID); err != nil {
			t.Fatalf("insert real-AI transcript %d: %v", sequenceNo, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit real-AI fixture: %v", err)
	}
}

func realTreeAuditFixturePayload(t *testing.T) json.RawMessage {
	t.Helper()
	payload := map[string]any{
		"summary":      "ネットワーク障害の振り返りとVPN証明書対応",
		"currentTopic": "VPN証明書の期限切れ対応",
		"items": []map[string]any{
			{"id": "item-risk-vpn-expiry", "kind": "risk", "severity": "high", "title": "VPN証明書が来月末に期限切れ", "body": "放置するとリモート接続ができなくなる可能性がある", "status": "open", "classificationStatus": "assigned", "assignmentConfidence": 1.0, "evidenceSequenceNos": []int64{2}, "propositionKey": "prop-vpn-risk"},
			{"id": "item-todo-vpn-update", "kind": "todo", "severity": "high", "title": "VPN証明書の更新手順と作業日を確認", "body": "高橋さんが今週中に確認する", "status": "open", "classificationStatus": "assigned", "assignmentConfidence": 1.0, "evidenceSequenceNos": []int64{3}, "propositionKey": "prop-vpn-todo"},
			{"id": "item-discourse-only", "kind": "todo", "severity": "medium", "title": "別の問題の存在を確認", "body": "アジェンダ外の別問題があるとの紹介", "status": "open", "classificationStatus": "unclassified", "assignmentConfidence": 0.4, "evidenceSequenceNos": []int64{1}, "propositionKey": "prop-discourse-only"},
		},
		"tree": map[string]any{
			"nodes": []map[string]any{
				{"id": "root", "kind": "topic", "label": "障害振り返り", "origin": "system"},
				{"id": "agenda-impact", "kind": "topic", "parentId": "root", "label": "影響範囲", "origin": "agenda"},
				{"id": "agenda-cause", "kind": "topic", "parentId": "root", "label": "原因調査", "origin": "agenda"},
				{"id": "agenda-prevention", "kind": "topic", "parentId": "root", "label": "再発防止策", "origin": "agenda"},
				{"id": "topic-vpn-expiry", "kind": "topic", "parentId": "root", "label": "VPN証明書の期限切れ対応", "origin": "dynamic"},
				{"id": "group-additional", "kind": "group", "parentId": "root", "label": "追加論点", "origin": "rule"},
				{"id": "item-risk-vpn-expiry", "kind": "risk", "parentId": "topic-vpn-expiry", "label": "VPN証明書が来月末に期限切れ", "status": "open"},
				{"id": "item-todo-vpn-update", "kind": "todo", "parentId": "topic-vpn-expiry", "label": "VPN証明書の更新手順と作業日を確認", "status": "open"},
				{"id": "item-discourse-only", "kind": "todo", "parentId": "group-additional", "label": "別の問題の存在を確認", "status": "open"},
			},
			"edges": []map[string]any{
				{"source": "root", "target": "agenda-impact"}, {"source": "root", "target": "agenda-cause"},
				{"source": "root", "target": "agenda-prevention"}, {"source": "root", "target": "topic-vpn-expiry"},
				{"source": "root", "target": "group-additional"}, {"source": "topic-vpn-expiry", "target": "item-risk-vpn-expiry"},
				{"source": "topic-vpn-expiry", "target": "item-todo-vpn-update"}, {"source": "group-additional", "target": "item-discourse-only"},
			},
		},
		"treeVersion": 1, "payloadKind": "full_snapshot", "coveredThroughSequenceNo": 3,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal real tree audit fixture: %v", err)
	}
	return encoded
}

func assertRealTreeAuditOutcome(t *testing.T, payload json.RawMessage) {
	t.Helper()
	var state struct {
		Items []struct {
			ID       string `json:"id"`
			Inactive bool   `json:"inactive"`
		} `json:"items"`
		ItemTombstones []json.RawMessage `json:"itemTombstones"`
		Tree           struct {
			Nodes []struct {
				ID    string `json:"id"`
				Label string `json:"label"`
			} `json:"nodes"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(payload, &state); err != nil {
		t.Fatalf("decode audited real-AI payload: %v", err)
	}
	nodes := make(map[string]string)
	for _, node := range state.Tree.Nodes {
		nodes[node.ID] = node.Label
	}
	for _, id := range []string{"item-discourse-only", "group-additional"} {
		if _, active := nodes[id]; active {
			t.Fatalf("unwanted node %s remains active: %#v", id, nodes)
		}
	}
	for _, id := range []string{"topic-vpn-expiry", "item-risk-vpn-expiry", "item-todo-vpn-update", "agenda-impact", "agenda-cause", "agenda-prevention"} {
		if _, active := nodes[id]; !active {
			t.Fatalf("required node %s was removed: %#v", id, nodes)
		}
	}
	inactiveDiscourseItem := false
	for _, item := range state.Items {
		if item.ID == "item-discourse-only" {
			inactiveDiscourseItem = item.Inactive
			break
		}
	}
	if !inactiveDiscourseItem {
		t.Fatalf("discourse-only item was not retained as inactive: %+v", state.Items)
	}
	if len(state.ItemTombstones) == 0 {
		t.Fatal("audit cleanup did not persist an item tombstone")
	}
}
