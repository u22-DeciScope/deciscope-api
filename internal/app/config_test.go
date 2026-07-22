package app

import (
	"errors"
	"testing"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/infrastructure/database"
)

func TestAIConfigRoutesDeploymentsPerTaskAndFallsBackToShared(t *testing.T) {
	t.Setenv("AZURE_OPENAI_DEPLOYMENT", "shared-deployment")
	t.Setenv("AZURE_OPENAI_LIVE_EXTRACTION_DEPLOYMENT", "nano-deployment")
	t.Setenv("AZURE_OPENAI_TREE_AUDIT_DEPLOYMENT", "mini-audit-deployment")
	t.Setenv("AZURE_OPENAI_TREE_REORGANIZER_DEPLOYMENT", "mini-reorganizer-deployment")
	t.Setenv("AZURE_OPENAI_FINAL_TREE_REVIEW_DEPLOYMENT", "mini-review-deployment")
	t.Setenv("AZURE_OPENAI_FINAL_SUMMARY_DEPLOYMENT", "mini-summary-deployment")
	t.Setenv("AZURE_OPENAI_CONTEXT_PLANNER_DEPLOYMENT", "")
	t.Setenv("AI_MODEL_CONTEXT_PLANNER", "")
	config := aiConfigFromEnv()
	if config.TaskModels.LiveExtraction != "nano-deployment" ||
		config.TaskModels.TreeAudit != "mini-audit-deployment" ||
		config.TaskModels.TreeReorganizer != "mini-reorganizer-deployment" ||
		config.TaskModels.FinalTreeReview != "mini-review-deployment" ||
		config.TaskModels.FinalSummary != "mini-summary-deployment" {
		t.Fatalf("task deployments = %+v", config.TaskModels)
	}
	if config.TaskModels.ContextPlanner != "" || config.AzureOpenAI.Deployment != "shared-deployment" {
		t.Fatalf("shared fallback config = %+v task=%+v", config.AzureOpenAI, config.TaskModels)
	}
}

func TestAIConfigTreeAuditDefaultsToEnabled(t *testing.T) {
	t.Setenv("TREE_AUDIT_ENABLED", "")
	t.Setenv("TREE_AUDIT_MODE", "")
	config := aiConfigFromEnv()
	if !config.TreeAudit.Enabled {
		t.Fatal("tree audit must be enabled by default")
	}
	if config.TreeAuditModeDeprecated {
		t.Fatal("TreeAuditModeDeprecated must be false when TREE_AUDIT_MODE is unset")
	}
	if config.TreeAudit.Interval != 5*time.Minute || config.TreeAudit.MinInterval != 5*time.Minute ||
		config.TreeAudit.MaxRunsPerHour != 12 || config.TreeAudit.HighSeverityMinInterval != time.Minute ||
		config.TreeAudit.HighSeverityMaxRunsPerHour != 4 || config.TreeAudit.UnappliedWarningThreshold != 3 {
		t.Fatalf("tree audit scheduling defaults = %+v", config.TreeAudit)
	}
}

// TREE_AUDIT_MODEはモード切替の廃止により無視される。設定してもTreeAuditの
// 動作(enabled/scheduling)には影響せず、TreeAuditModeDeprecatedがtrueになる
// だけであることを確認する。
func TestAIConfigTreeAuditModeEnvIsDeprecatedAndDoesNotAffectBehavior(t *testing.T) {
	t.Setenv("TREE_AUDIT_ENABLED", "true")
	t.Setenv("TREE_AUDIT_MODE", "shadow")
	config := aiConfigFromEnv()
	if !config.TreeAudit.Enabled || config.TreeAuditEnabledInvalid {
		t.Fatalf("tree audit enablement = enabled:%t invalid:%t", config.TreeAudit.Enabled, config.TreeAuditEnabledInvalid)
	}
	if !config.TreeAuditModeDeprecated {
		t.Fatal("TreeAuditModeDeprecated must be true when TREE_AUDIT_MODE is set")
	}

	t.Setenv("TREE_AUDIT_MODE", "unsafe")
	config = aiConfigFromEnv()
	if !config.TreeAudit.Enabled || !config.TreeAuditModeDeprecated {
		t.Fatalf("an invalid TREE_AUDIT_MODE value must not disable tree audit: enabled=%t deprecated=%t", config.TreeAudit.Enabled, config.TreeAuditModeDeprecated)
	}
}

func TestAIConfigTreeAuditRejectsInvalidEnablement(t *testing.T) {
	t.Setenv("TREE_AUDIT_ENABLED", "enabled")
	config := aiConfigFromEnv()
	if config.TreeAudit.Enabled || !config.TreeAuditEnabledInvalid {
		t.Fatalf("invalid tree audit enablement = enabled:%t invalid:%t", config.TreeAudit.Enabled, config.TreeAuditEnabledInvalid)
	}
	if got := treeAuditConfigurationIssue(config, true); got != "invalid_feature_flag" {
		t.Fatalf("treeAuditConfigurationIssue() = %q, want invalid_feature_flag", got)
	}
}

func TestTreeAuditConfigurationRequiresDedicatedDeployments(t *testing.T) {
	config := AIConfig{
		TreeAudit: application.TreeAuditConfig{Enabled: true},
	}
	if got := treeAuditConfigurationIssue(config, true); got != "tree_audit_deployment_empty" {
		t.Fatalf("missing tree audit deployment issue = %q", got)
	}
	config.TaskModels.TreeAudit = "gpt-5-mini-audit"
	if got := treeAuditConfigurationIssue(config, true); got != "final_tree_review_deployment_empty" {
		t.Fatalf("missing final review deployment issue = %q", got)
	}
	config.TaskModels.FinalTreeReview = "gpt-5-mini-final-review"
	if got := treeAuditConfigurationIssue(config, true); got != "" {
		t.Fatalf("configured tree audit issue = %q", got)
	}
}

func TestTreeAuditRepositoryIssueDistinguishesMigration(t *testing.T) {
	if got := treeAuditRepositoryIssue(nil); got != "" {
		t.Fatalf("nil repository issue = %q", got)
	}
	if got := treeAuditRepositoryIssue(errors.Join(errors.New("readiness"), application.ErrMeetingTreeAuditMigrationMissing)); got != "migration_missing" {
		t.Fatalf("migration repository issue = %q", got)
	}
	if got := treeAuditRepositoryIssue(errors.New("database unavailable")); got != "repository_not_ready" {
		t.Fatalf("generic repository issue = %q", got)
	}
}

func TestTreeAuditSchedulerRegistrationRequiresActiveConfigAndRepository(t *testing.T) {
	config := application.TreeAuditConfig{Enabled: true}
	if !treeAuditSchedulerRegistered(config, true) {
		t.Fatal("active tree audit with ready repository must register the scheduler")
	}
	if treeAuditSchedulerRegistered(config, false) {
		t.Fatal("tree audit without a ready repository must not register the scheduler")
	}
	config.Enabled = false
	if treeAuditSchedulerRegistered(config, true) {
		t.Fatal("disabled tree audit must not register the scheduler")
	}
}

func TestConfigFromEnvReadsDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://deciscope:secret@localhost:5432/deciscope")
	t.Setenv("DECISCOPE_INGEST_API_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("DECISCOPE_WS_CLIENT_TOKEN", "dev-ws-token")
	t.Setenv("DECISCOPE_WS_ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:5173")
	t.Setenv("DECISCOPE_TRANSCRIPT_ONLY", "true")
	t.Setenv("DECISCOPE_BOT_CONTROL_URL", "http://100.64.0.1:7071/internal/bot/join")
	t.Setenv("DECISCOPE_BOT_CONTROL_TOKEN", "bot-control-token")
	t.Setenv("DECISCOPE_BOT_CONTROL_TIMEOUT_SECONDS", "12")
	config := ConfigFromEnv()
	if config.Database.URL != "postgres://deciscope:secret@localhost:5432/deciscope" {
		t.Fatalf("ConfigFromEnv() = %+v", config)
	}
	if config.TranscriptWebSocket.ClientToken != "dev-ws-token" ||
		config.TranscriptWebSocket.AllowedOrigins != "http://localhost:3000,http://localhost:5173" {
		t.Fatalf("ConfigFromEnv() = %+v, want websocket config", config)
	}
	if !config.TranscriptOnly {
		t.Fatalf("ConfigFromEnv() = %+v, want transcript-only enabled", config)
	}
	if config.BotControl.URL != "http://100.64.0.1:7071/internal/bot/join" ||
		config.BotControl.Token != "bot-control-token" ||
		config.BotControl.Timeout != 12*time.Second {
		t.Fatalf("ConfigFromEnv() = %+v, want bot control config", config)
	}
}

func TestConfigFromEnvReadsGoogleApplicationCredentials(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "./serviceAccountKey.json")
	config := ConfigFromEnv()
	if config.Firebase.CredentialsFile != "./serviceAccountKey.json" {
		t.Fatalf("Firebase credentials file = %q", config.Firebase.CredentialsFile)
	}
}

func TestConfigFromEnvSessionWatchdogDefaults(t *testing.T) {
	t.Setenv("DECISCOPE_SESSION_WATCHDOG_ENABLED", "")
	t.Setenv("DECISCOPE_SESSION_WATCHDOG_INTERVAL_SECONDS", "")
	t.Setenv("DECISCOPE_SESSION_BOT_LOST_AFTER_SECONDS", "")
	t.Setenv("DECISCOPE_SESSION_BOT_END_AFTER_SECONDS", "")
	config := ConfigFromEnv()
	if !config.SessionWatchdog.Enabled {
		t.Fatalf("SessionWatchdog.Enabled = %v, want true by default", config.SessionWatchdog.Enabled)
	}
	if config.SessionWatchdog.Interval != 15*time.Second {
		t.Fatalf("SessionWatchdog.Interval = %s, want 15s", config.SessionWatchdog.Interval)
	}
	if config.SessionWatchdog.LostAfter != 60*time.Second {
		t.Fatalf("SessionWatchdog.LostAfter = %s, want 60s", config.SessionWatchdog.LostAfter)
	}
	if config.SessionWatchdog.EndAfter != 180*time.Second {
		t.Fatalf("SessionWatchdog.EndAfter = %s, want 180s", config.SessionWatchdog.EndAfter)
	}
}

func TestConfigFromEnvSessionWatchdogReadsOverrides(t *testing.T) {
	t.Setenv("DECISCOPE_SESSION_WATCHDOG_ENABLED", "false")
	t.Setenv("DECISCOPE_SESSION_WATCHDOG_INTERVAL_SECONDS", "20")
	t.Setenv("DECISCOPE_SESSION_BOT_LOST_AFTER_SECONDS", "90")
	t.Setenv("DECISCOPE_SESSION_BOT_END_AFTER_SECONDS", "240")
	config := ConfigFromEnv()
	if config.SessionWatchdog.Enabled {
		t.Fatalf("SessionWatchdog.Enabled = %v, want false", config.SessionWatchdog.Enabled)
	}
	if config.SessionWatchdog.Interval != 20*time.Second {
		t.Fatalf("SessionWatchdog.Interval = %s, want 20s", config.SessionWatchdog.Interval)
	}
	if config.SessionWatchdog.LostAfter != 90*time.Second {
		t.Fatalf("SessionWatchdog.LostAfter = %s, want 90s", config.SessionWatchdog.LostAfter)
	}
	if config.SessionWatchdog.EndAfter != 240*time.Second {
		t.Fatalf("SessionWatchdog.EndAfter = %s, want 240s", config.SessionWatchdog.EndAfter)
	}
}

func TestConfigFromEnvSessionWatchdogCorrectsEndAfterBelowLostAfter(t *testing.T) {
	t.Setenv("DECISCOPE_SESSION_WATCHDOG_ENABLED", "")
	t.Setenv("DECISCOPE_SESSION_WATCHDOG_INTERVAL_SECONDS", "")
	t.Setenv("DECISCOPE_SESSION_BOT_LOST_AFTER_SECONDS", "200")
	t.Setenv("DECISCOPE_SESSION_BOT_END_AFTER_SECONDS", "50")
	config := ConfigFromEnv()
	if config.SessionWatchdog.LostAfter != 200*time.Second {
		t.Fatalf("SessionWatchdog.LostAfter = %s, want 200s", config.SessionWatchdog.LostAfter)
	}
	if config.SessionWatchdog.EndAfter <= config.SessionWatchdog.LostAfter {
		t.Fatalf("SessionWatchdog.EndAfter = %s, want strictly greater than LostAfter = %s", config.SessionWatchdog.EndAfter, config.SessionWatchdog.LostAfter)
	}
}

func TestConfigFromEnvLeavesMissingDatabaseURLBlank(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	config := ConfigFromEnv()
	if config.Database.URL != "" {
		t.Fatalf("ConfigFromEnv() = %+v", config)
	}
}

func TestListenAddressFromEnv(t *testing.T) {
	t.Setenv("DECISCOPE_BACKEND_ADDR", "")
	t.Setenv("PORT", "")
	if got := ListenAddressFromEnv(); got != ":9090" {
		t.Fatalf("default address = %q, want :9090", got)
	}
	t.Setenv("PORT", "18080")
	if got := ListenAddressFromEnv(); got != ":18080" {
		t.Fatalf("configured address = %q, want :18080", got)
	}
	t.Setenv("DECISCOPE_BACKEND_ADDR", "127.0.0.1:18080")
	if got := ListenAddressFromEnv(); got != "127.0.0.1:18080" {
		t.Fatalf("configured backend address = %q, want 127.0.0.1:18080", got)
	}
}

func TestValidateRuntimeConfigRequiresTranscriptSettings(t *testing.T) {
	config := Config{
		Database: databaseConfigForTest(),
		TranscriptIngest: TranscriptIngestConfig{
			APIKey: ingestAPIKeyPlaceholder,
		},
	}
	if err := ValidateRuntimeConfig(config); err == nil {
		t.Fatal("ValidateRuntimeConfig() error = nil, want placeholder api key error")
	}
	config.TranscriptIngest.APIKey = "short"
	if err := ValidateRuntimeConfig(config); err == nil {
		t.Fatal("ValidateRuntimeConfig() error = nil, want short api key error")
	}
	config.TranscriptIngest.APIKey = "0123456789abcdef0123456789abcdef"
	if err := ValidateRuntimeConfig(config); err != nil {
		t.Fatalf("ValidateRuntimeConfig() error = %v", err)
	}
}

func TestValidateRuntimeConfigRequiresDatabaseURL(t *testing.T) {
	config := Config{
		TranscriptOnly: true,
		TranscriptIngest: TranscriptIngestConfig{
			APIKey: "0123456789abcdef0123456789abcdef",
		},
	}
	if err := ValidateRuntimeConfig(config); err == nil {
		t.Fatal("ValidateRuntimeConfig() error = nil, want missing database URL")
	}
	config.Database = databaseConfigForTest()
	if err := ValidateRuntimeConfig(config); err != nil {
		t.Fatalf("ValidateRuntimeConfig() error = %v", err)
	}
}

func databaseConfigForTest() database.Config {
	return database.Config{URL: "postgres://deciscope:secret@localhost:5432/deciscope"}
}
