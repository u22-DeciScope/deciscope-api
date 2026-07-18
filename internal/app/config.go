package app

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/infrastructure/azureopenai"
	"deciscope-core-api/internal/infrastructure/botcontrol"
	"deciscope-core-api/internal/infrastructure/database"
	"deciscope-core-api/internal/infrastructure/firebase"

	"github.com/joho/godotenv"
)

const ingestAPIKeyPlaceholder = "REPLACE_WITH_A_LONG_RANDOM_SECRET"
const minIngestAPIKeyLength = 32

const (
	defaultAzureOpenAIAPIVersion         = "2024-10-21"
	defaultAIRequestTimeoutSeconds       = 20
	defaultAIFinalSummaryTimeoutSeconds  = 60
	defaultAILiveAnalysisIntervalSeconds = 10
	minAILiveAnalysisIntervalSeconds     = 5
	defaultAILiveAnalysisMinChars        = 80
	defaultAILiveAnalysisMaxInputChars   = 4000
	defaultAIFinalSummaryMaxInputChars   = 12000
	defaultAIFinalizationWaitSeconds     = 10
	defaultAIFinalizationQuietMillis     = 750
	defaultAIFinalFlushMaxAttempts       = 3
)

const (
	defaultSessionWatchdogIntervalSeconds = 15
	minSessionWatchdogIntervalSeconds     = 5
	defaultSessionBotLostAfterSeconds     = 60
	minSessionBotLostAfterSeconds         = 30
	defaultSessionBotEndAfterSeconds      = 180
	defaultTranscriptDelayedAfterSeconds  = 30
	minTranscriptDelayedAfterSeconds      = 5
	defaultTranscriptStalledAfterSeconds  = 60
	defaultAudioSilenceAfterSeconds       = 30
	minAudioSilenceAfterSeconds           = 5
	defaultAudioStalledAfterSeconds       = 60
	minAudioStalledAfterSeconds           = 5
	defaultSpeechStalledAfterSeconds      = 60
	minSpeechStalledAfterSeconds          = 5
)

type Config struct {
	Database            database.Config
	TranscriptIngest    TranscriptIngestConfig
	TranscriptWebSocket TranscriptWebSocketConfig
	TranscriptOnly      bool
	BotControl          botcontrol.Config
	Firebase            firebase.Config
	AI                  AIConfig
	SessionWatchdog     MeetingSessionWatchdogConfig
	FrontendURL         string
	AllowedOrigins      string
	SessionCookieSecure bool
	// Environment は "development" / "production"。招待リンクのログ出力と
	// サンプル会議の既定値判定に使う。
	Environment string
	// CreateSampleMeetingOnFirstWorkspace は、初回作成ワークスペースへ
	// サンプル会議を投入するかどうか。既定: development=true / production=false。
	CreateSampleMeetingOnFirstWorkspace bool
}

type TranscriptIngestConfig struct {
	APIKey string
}

type TranscriptWebSocketConfig struct {
	ClientToken    string
	AllowedOrigins string
}

// AIConfig holds the AI meeting analysis configuration. Azure OpenAI
// credentials live in AzureOpenAI; the live/final feature flags below are
// automatically forced off (see MissingAzureOpenAIVars) whenever Azure
// OpenAI is not fully configured.
type AIConfig struct {
	AzureOpenAI               azureopenai.Config
	LiveAnalysisEnabled       bool
	LiveAnalysisInterval      time.Duration
	LiveAnalysisMinChars      int
	LiveAnalysisMaxInputChars int
	FinalSummaryEnabled       bool
	FinalSummaryMaxInputChars int
	FinalSummaryTimeout       time.Duration
	FinalizationWaitTimeout   time.Duration
	FinalizationQuietPeriod   time.Duration
	FinalFlushMaxAttempts     int
	// TaskModels are optional per-task AZURE_OPENAI_*_DEPLOYMENT names. The
	// legacy AI_MODEL_* aliases remain accepted; empty entries fall back to
	// the shared AZURE_OPENAI_DEPLOYMENT.
	TaskModels              AITaskModelsConfig
	TreeAudit               application.TreeAuditConfig
	TreeAuditEnabledInvalid bool
	// TreeAuditModeDeprecated is true when TREE_AUDIT_MODE is set to any
	// non-empty value. The mode switch was removed; the audit AI now runs a
	// single enabled/disabled pipeline controlled solely by TREE_AUDIT_ENABLED.
	TreeAuditModeDeprecated bool
	// TreeClassification は議論ツリーの意味分類ポリシー(AI_TREE_*)。ゼロ値の
	// 項目は application 側の既定値が使われる。
	TreeClassification application.TreeClassificationConfig
	// DebugDroppedNodes は破棄されたツリーノードの詳細(id/kind/title/reason)を
	// 開発用にログ出力するか。既定: false。
	DebugDroppedNodes bool
}

// AITaskModelsConfig holds per-task Azure OpenAI deployment overrides.
type AITaskModelsConfig struct {
	ContextPlanner  string
	LiveExtraction  string
	TreeAudit       string
	TreeReorganizer string
	FinalTreeReview string
	FinalSummary    string
}

// MissingAzureOpenAIVars returns the names of the required Azure OpenAI
// environment variables that are not set. A non-empty result means the AI
// feature is fully disabled regardless of the individual feature flags.
func (c AIConfig) MissingAzureOpenAIVars() []string {
	var missing []string
	if strings.TrimSpace(c.AzureOpenAI.Endpoint) == "" {
		missing = append(missing, "AZURE_OPENAI_ENDPOINT")
	}
	if strings.TrimSpace(c.AzureOpenAI.APIKey) == "" {
		missing = append(missing, "AZURE_OPENAI_API_KEY")
	}
	if strings.TrimSpace(c.AzureOpenAI.Deployment) == "" {
		missing = append(missing, "AZURE_OPENAI_DEPLOYMENT")
	}
	return missing
}

// Enabled reports whether Azure OpenAI is fully configured.
func (c AIConfig) Enabled() bool {
	return len(c.MissingAzureOpenAIVars()) == 0
}

// MeetingSessionWatchdogConfig controls the resident goroutine that detects a
// bot that has stopped sending heartbeats (e.g. the VM process died or was
// force-stopped) and ends the meeting session instead of leaving it active
// until the unrelated 2h stale cleanup eventually catches it. It also carries
// the thresholds for the separate transcript health check (is text still
// flowing, independent of the bot heartbeat), and for further classifying a
// transcript gap using bot-reported audio/transcript metrics
// (silent/audio_stalled/speech_stalled) when those metrics are available.
type MeetingSessionWatchdogConfig struct {
	Enabled                bool
	Interval               time.Duration
	LostAfter              time.Duration
	EndAfter               time.Duration
	TranscriptDelayedAfter time.Duration
	TranscriptStalledAfter time.Duration
	AudioSilenceAfter      time.Duration
	AudioStalledAfter      time.Duration
	SpeechStalledAfter     time.Duration
}

func ConfigFromEnv() Config {
	transcriptOnly := strings.EqualFold(os.Getenv("DECISCOPE_TRANSCRIPT_ONLY"), "true")
	environment := environmentFromEnv()
	return Config{
		Database: database.Config{URL: os.Getenv("DATABASE_URL")},
		TranscriptIngest: TranscriptIngestConfig{
			APIKey: strings.TrimSpace(os.Getenv("DECISCOPE_INGEST_API_KEY")),
		},
		TranscriptWebSocket: TranscriptWebSocketConfig{
			ClientToken:    strings.TrimSpace(os.Getenv("DECISCOPE_WS_CLIENT_TOKEN")),
			AllowedOrigins: os.Getenv("DECISCOPE_WS_ALLOWED_ORIGINS"),
		},
		TranscriptOnly: transcriptOnly,
		BotControl: botcontrol.Config{
			URL:     strings.TrimSpace(os.Getenv("DECISCOPE_BOT_CONTROL_URL")),
			Token:   strings.TrimSpace(os.Getenv("DECISCOPE_BOT_CONTROL_TOKEN")),
			Timeout: botControlTimeoutFromEnv(os.Getenv("DECISCOPE_BOT_CONTROL_TIMEOUT_SECONDS")),
		},
		Firebase: firebase.Config{
			CredentialsFile: os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"),
			CredentialsJSON: os.Getenv("FIREBASE_CREDENTIALS_JSON"),
			ProjectID:       os.Getenv("FIREBASE_PROJECT_ID"),
			Enabled:         os.Getenv("AUTH_PROVIDER") == "firebase",
		},
		AI:                                  aiConfigFromEnv(),
		SessionWatchdog:                     sessionWatchdogConfigFromEnv(),
		FrontendURL:                         os.Getenv("FRONTEND_URL"),
		AllowedOrigins:                      os.Getenv("ALLOWED_ORIGINS"),
		SessionCookieSecure:                 strings.EqualFold(os.Getenv("SESSION_COOKIE_SECURE"), "true"),
		Environment:                         environment,
		CreateSampleMeetingOnFirstWorkspace: sampleMeetingFlagFromEnv(environment),
	}
}

// sampleMeetingFlagFromEnv は DECISCOPE_CREATE_SAMPLE_MEETING_ON_FIRST_WORKSPACE を読む。
// 未設定時は development で true、production で false。
func sampleMeetingFlagFromEnv(environment string) bool {
	value := strings.TrimSpace(os.Getenv("DECISCOPE_CREATE_SAMPLE_MEETING_ON_FIRST_WORKSPACE"))
	if value == "" {
		return environment != "production"
	}
	return strings.EqualFold(value, "true")
}

// environmentFromEnv は DECISCOPE_ENV を読む。未設定時は development。
// production では招待URLのログ出力を無効にし、招待作成を失敗させる。
func environmentFromEnv() string {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("DECISCOPE_ENV")))
	if value == "production" {
		return "production"
	}
	return "development"
}

func aiConfigFromEnv() AIConfig {
	requestTimeout := secondsDurationFromEnv(os.Getenv("AI_REQUEST_TIMEOUT_SECONDS"), defaultAIRequestTimeoutSeconds, 1)
	treeAuditEnabled, treeAuditEnabledInvalid := treeAuditEnabledFromEnv(os.Getenv("TREE_AUDIT_ENABLED"))
	return AIConfig{
		AzureOpenAI: azureopenai.Config{
			Endpoint:   strings.TrimSpace(os.Getenv("AZURE_OPENAI_ENDPOINT")),
			APIKey:     strings.TrimSpace(os.Getenv("AZURE_OPENAI_API_KEY")),
			Deployment: strings.TrimSpace(os.Getenv("AZURE_OPENAI_DEPLOYMENT")),
			APIVersion: firstNonEmpty(strings.TrimSpace(os.Getenv("AZURE_OPENAI_API_VERSION")), defaultAzureOpenAIAPIVersion),
			Timeout:    requestTimeout,
		},
		LiveAnalysisEnabled:       boolFromEnvDefaultTrue(os.Getenv("AI_LIVE_ANALYSIS_ENABLED")),
		LiveAnalysisInterval:      secondsDurationFromEnv(os.Getenv("AI_LIVE_ANALYSIS_INTERVAL_SECONDS"), defaultAILiveAnalysisIntervalSeconds, minAILiveAnalysisIntervalSeconds),
		LiveAnalysisMinChars:      positiveIntFromEnv(os.Getenv("AI_LIVE_ANALYSIS_MIN_CHARS"), defaultAILiveAnalysisMinChars),
		LiveAnalysisMaxInputChars: positiveIntFromEnv(os.Getenv("AI_LIVE_ANALYSIS_MAX_INPUT_CHARS"), defaultAILiveAnalysisMaxInputChars),
		FinalSummaryEnabled:       boolFromEnvDefaultTrue(os.Getenv("AI_FINAL_SUMMARY_ENABLED")),
		FinalSummaryMaxInputChars: positiveIntFromEnv(os.Getenv("AI_FINAL_SUMMARY_MAX_INPUT_CHARS"), defaultAIFinalSummaryMaxInputChars),
		FinalSummaryTimeout:       secondsDurationFromEnv(os.Getenv("AI_FINAL_SUMMARY_TIMEOUT_SECONDS"), defaultAIFinalSummaryTimeoutSeconds, 1),
		FinalizationWaitTimeout:   secondsDurationFromEnv(os.Getenv("AI_FINALIZATION_WAIT_TIMEOUT_SECONDS"), defaultAIFinalizationWaitSeconds, 1),
		FinalizationQuietPeriod:   millisecondsDurationFromEnv(os.Getenv("AI_FINALIZATION_QUIET_PERIOD_MILLISECONDS"), defaultAIFinalizationQuietMillis, 100),
		FinalFlushMaxAttempts:     positiveIntFromEnv(os.Getenv("AI_FINAL_FLUSH_MAX_ATTEMPTS"), defaultAIFinalFlushMaxAttempts),
		TaskModels: AITaskModelsConfig{
			ContextPlanner:  firstNonEmpty(strings.TrimSpace(os.Getenv("AZURE_OPENAI_CONTEXT_PLANNER_DEPLOYMENT")), strings.TrimSpace(os.Getenv("AI_MODEL_CONTEXT_PLANNER"))),
			LiveExtraction:  firstNonEmpty(strings.TrimSpace(os.Getenv("AZURE_OPENAI_LIVE_EXTRACTION_DEPLOYMENT")), strings.TrimSpace(os.Getenv("AI_MODEL_LIVE_EXTRACTION"))),
			TreeAudit:       firstNonEmpty(strings.TrimSpace(os.Getenv("AZURE_OPENAI_TREE_AUDIT_DEPLOYMENT")), strings.TrimSpace(os.Getenv("AI_MODEL_TREE_AUDIT"))),
			TreeReorganizer: firstNonEmpty(strings.TrimSpace(os.Getenv("AZURE_OPENAI_TREE_REORGANIZER_DEPLOYMENT")), strings.TrimSpace(os.Getenv("AI_MODEL_TREE_REORGANIZER"))),
			FinalTreeReview: firstNonEmpty(strings.TrimSpace(os.Getenv("AZURE_OPENAI_FINAL_TREE_REVIEW_DEPLOYMENT")), strings.TrimSpace(os.Getenv("AI_MODEL_FINAL_TREE_REVIEW"))),
			FinalSummary:    firstNonEmpty(strings.TrimSpace(os.Getenv("AZURE_OPENAI_FINAL_SUMMARY_DEPLOYMENT")), strings.TrimSpace(os.Getenv("AI_MODEL_FINAL_SUMMARY"))),
		},
		TreeAudit: application.TreeAuditConfig{
			Enabled:                    treeAuditEnabled,
			IntervalVersions:           int64(positiveIntFromEnv(os.Getenv("TREE_AUDIT_INTERVAL_VERSIONS"), 3)),
			Interval:                   secondsDurationFromEnv(os.Getenv("TREE_AUDIT_INTERVAL_SECONDS"), 300, 1),
			MinInterval:                secondsDurationFromEnv(os.Getenv("TREE_AUDIT_MIN_INTERVAL_SECONDS"), 300, 1),
			MaxRunsPerSession:          positiveIntFromEnv(os.Getenv("TREE_AUDIT_MAX_RUNS_PER_SESSION"), 20),
			MaxRunsPerHour:             positiveIntFromEnv(os.Getenv("TREE_AUDIT_MAX_RUNS_PER_HOUR"), 12),
			HighSeverityMinInterval:    secondsDurationFromEnv(os.Getenv("TREE_AUDIT_HIGH_SEVERITY_MIN_INTERVAL_SECONDS"), 60, 1),
			HighSeverityMaxRunsPerHour: positiveIntFromEnv(os.Getenv("TREE_AUDIT_HIGH_SEVERITY_MAX_RUNS_PER_HOUR"), 4),
			Timeout:                    secondsDurationFromEnv(os.Getenv("TREE_AUDIT_TIMEOUT_SECONDS"), 25, 1),
			MaxOutputTokens:            positiveIntFromEnv(os.Getenv("TREE_AUDIT_MAX_OUTPUT_TOKENS"), 2500),
			MaxNodes:                   positiveIntFromEnv(os.Getenv("TREE_AUDIT_MAX_NODES"), 80),
			MaxRecentSegments:          positiveIntFromEnv(os.Getenv("TREE_AUDIT_MAX_RECENT_SEGMENTS"), 16),
			MaxEvidenceSegments:        positiveIntFromEnv(os.Getenv("TREE_AUDIT_MAX_EVIDENCE_SEGMENTS"), 24),
			MaxInputTokens:             positiveIntFromEnv(os.Getenv("TREE_AUDIT_MAX_INPUT_TOKENS"), 12000),
			MaxPersistedJSONBytes:      positiveIntFromEnv(os.Getenv("TREE_AUDIT_MAX_PERSISTED_JSON_BYTES"), 256*1024),
			HighConfidenceThreshold:    floatFromEnv(os.Getenv("TREE_AUDIT_HIGH_CONFIDENCE_THRESHOLD")),
			RequiredImprovementMargin:  floatFromEnv(os.Getenv("TREE_AUDIT_REQUIRED_IMPROVEMENT_MARGIN")),
			CohesionThreshold:          floatFromEnv(os.Getenv("TREE_AUDIT_COHESION_THRESHOLD")),
			TentativeMaxVersions:       int64(positiveIntFromEnv(os.Getenv("TREE_AUDIT_TENTATIVE_MAX_VERSIONS"), 3)),
		},
		TreeAuditEnabledInvalid: treeAuditEnabledInvalid,
		TreeAuditModeDeprecated: strings.TrimSpace(os.Getenv("TREE_AUDIT_MODE")) != "",
		// ゼロ値(未設定・不正値)は application 側の既定値に正規化されるため、
		// 既定値をここで二重管理しない。
		TreeClassification: application.TreeClassificationConfig{
			AgendaAssignmentThreshold: floatFromEnv(os.Getenv("AI_TREE_AGENDA_ASSIGNMENT_THRESHOLD")),
			PromotionMinItems:         intFromEnvOrZero(os.Getenv("AI_TREE_TOPIC_PROMOTION_MIN_ITEMS")),
			PromotionMinRounds:        intFromEnvOrZero(os.Getenv("AI_TREE_TOPIC_PROMOTION_MIN_ROUNDS")),
			MaxDynamicTopics:          intFromEnvOrZero(os.Getenv("AI_TREE_MAX_DYNAMIC_TOPICS")),
		},
		DebugDroppedNodes: strings.EqualFold(strings.TrimSpace(os.Getenv("AI_ANALYSIS_DEBUG_DROPPED_NODES")), "true"),
	}
}

// floatFromEnv parses a float environment value; invalid or missing values
// yield 0 (= use the application-side default).
func floatFromEnv(value string) float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0
	}
	return parsed
}

// intFromEnvOrZero parses an int environment value; invalid or missing values
// yield 0 (= use the application-side default).
func intFromEnvOrZero(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

// sessionWatchdogConfigFromEnv reads DECISCOPE_SESSION_WATCHDOG_* /
// DECISCOPE_SESSION_BOT_*_AFTER_SECONDS. EndAfter is coerced to be strictly
// greater than LostAfter (falling back to the default when the configured
// value would not be) so the watchdog can never end a session before it has
// even been reported unhealthy. TranscriptStalledAfter is coerced the same
// way against TranscriptDelayedAfter.
func sessionWatchdogConfigFromEnv() MeetingSessionWatchdogConfig {
	lostAfter := secondsDurationFromEnv(os.Getenv("DECISCOPE_SESSION_BOT_LOST_AFTER_SECONDS"), defaultSessionBotLostAfterSeconds, minSessionBotLostAfterSeconds)
	endAfterSeconds, err := strconv.Atoi(strings.TrimSpace(os.Getenv("DECISCOPE_SESSION_BOT_END_AFTER_SECONDS")))
	endAfter := time.Duration(endAfterSeconds) * time.Second
	if err != nil || endAfterSeconds <= 0 || endAfter <= lostAfter {
		endAfter = time.Duration(defaultSessionBotEndAfterSeconds) * time.Second
		if endAfter <= lostAfter {
			endAfter = lostAfter + time.Duration(defaultSessionBotEndAfterSeconds)*time.Second
		}
	}
	delayedAfter := secondsDurationFromEnv(os.Getenv("DECISCOPE_TRANSCRIPT_DELAYED_AFTER_SECONDS"), defaultTranscriptDelayedAfterSeconds, minTranscriptDelayedAfterSeconds)
	stalledAfterSeconds, err := strconv.Atoi(strings.TrimSpace(os.Getenv("DECISCOPE_TRANSCRIPT_STALLED_AFTER_SECONDS")))
	stalledAfter := time.Duration(stalledAfterSeconds) * time.Second
	if err != nil || stalledAfterSeconds <= 0 || stalledAfter <= delayedAfter {
		stalledAfter = time.Duration(defaultTranscriptStalledAfterSeconds) * time.Second
		if stalledAfter <= delayedAfter {
			stalledAfter = delayedAfter + time.Duration(defaultTranscriptStalledAfterSeconds)*time.Second
		}
	}
	return MeetingSessionWatchdogConfig{
		Enabled:                boolFromEnvDefaultTrue(os.Getenv("DECISCOPE_SESSION_WATCHDOG_ENABLED")),
		Interval:               secondsDurationFromEnv(os.Getenv("DECISCOPE_SESSION_WATCHDOG_INTERVAL_SECONDS"), defaultSessionWatchdogIntervalSeconds, minSessionWatchdogIntervalSeconds),
		LostAfter:              lostAfter,
		EndAfter:               endAfter,
		TranscriptDelayedAfter: delayedAfter,
		TranscriptStalledAfter: stalledAfter,
		AudioSilenceAfter:      secondsDurationFromEnv(os.Getenv("DECISCOPE_AUDIO_SILENCE_AFTER_SECONDS"), defaultAudioSilenceAfterSeconds, minAudioSilenceAfterSeconds),
		AudioStalledAfter:      secondsDurationFromEnv(os.Getenv("DECISCOPE_AUDIO_STALLED_AFTER_SECONDS"), defaultAudioStalledAfterSeconds, minAudioStalledAfterSeconds),
		SpeechStalledAfter:     secondsDurationFromEnv(os.Getenv("DECISCOPE_SPEECH_STALLED_AFTER_SECONDS"), defaultSpeechStalledAfterSeconds, minSpeechStalledAfterSeconds),
	}
}

func secondsDurationFromEnv(value string, defaultSeconds, minSeconds int) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds <= 0 {
		seconds = defaultSeconds
	}
	if seconds < minSeconds {
		seconds = minSeconds
	}
	return time.Duration(seconds) * time.Second
}

func millisecondsDurationFromEnv(value string, defaultMilliseconds, minMilliseconds int) time.Duration {
	milliseconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || milliseconds <= 0 {
		milliseconds = defaultMilliseconds
	}
	if milliseconds < minMilliseconds {
		milliseconds = minMilliseconds
	}
	return time.Duration(milliseconds) * time.Millisecond
}

func positiveIntFromEnv(value string, defaultValue int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return defaultValue
	}
	return parsed
}

func boolFromEnvDefaultTrue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return true
	}
	return strings.EqualFold(trimmed, "true")
}

func treeAuditEnabledFromEnv(value string) (enabled bool, invalid bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return true, false
	}
	enabled, err := strconv.ParseBool(trimmed)
	if err != nil {
		return false, true
	}
	return enabled, false
}

func botControlTimeoutFromEnv(value string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds <= 0 {
		return 10 * time.Second
	}
	return time.Duration(seconds) * time.Second
}

func LoadEnvironmentFiles() {
	_ = godotenv.Load(".env")
	_ = godotenv.Overload(".env.local")
}

func ListenAddressFromEnv() string {
	if address := strings.TrimSpace(os.Getenv("DECISCOPE_BACKEND_ADDR")); address != "" {
		return address
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "9090"
	}
	return ":" + port
}

func ValidateRuntimeConfig(config Config) error {
	if err := validateIngestAPIKey(config.TranscriptIngest.APIKey); err != nil {
		return err
	}
	if strings.TrimSpace(config.Database.URL) == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	return nil
}

func validateIngestAPIKey(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("DECISCOPE_INGEST_API_KEY is required")
	}
	if value == ingestAPIKeyPlaceholder {
		return fmt.Errorf("DECISCOPE_INGEST_API_KEY must be replaced")
	}
	if len(value) < minIngestAPIKeyLength {
		return fmt.Errorf("DECISCOPE_INGEST_API_KEY must be at least %d characters", minIngestAPIKeyLength)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
