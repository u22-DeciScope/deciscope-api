package app

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"deciscope-core-api/internal/infrastructure/azureopenai"
	"deciscope-core-api/internal/infrastructure/botcontrol"
	"deciscope-core-api/internal/infrastructure/database"
	"deciscope-core-api/internal/infrastructure/firebase"

	"github.com/joho/godotenv"
)

const ingestAPIKeyPlaceholder = "REPLACE_WITH_A_LONG_RANDOM_SECRET"
const minIngestAPIKeyLength = 32

const (
	TranscriptStorePostgres = "postgres"
)

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
	SeedDemoData        bool
	// Environment は "development" / "production"。招待メールの dev fallback 判定に使う。
	Environment string
	InviteEmail InviteEmailConfig
	// CreateSampleMeetingOnFirstWorkspace は、初回作成ワークスペースへ
	// サンプル会議を投入するかどうか。既定: development=true / production=false。
	CreateSampleMeetingOnFirstWorkspace bool
}

type InviteEmailConfig struct {
	SMTPHost     string
	SMTPPort     string
	SMTPUsername string
	SMTPPassword string
	From         string
}

func (c InviteEmailConfig) Configured() bool {
	return strings.TrimSpace(c.SMTPHost) != "" && strings.TrimSpace(c.From) != ""
}

type TranscriptIngestConfig struct {
	Store  string
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
	// TaskModels are optional per-task deployment names (AI_MODEL_*). Empty
	// entries fall back to the shared AZURE_OPENAI_DEPLOYMENT.
	TaskModels AITaskModelsConfig
	// DebugDroppedNodes は破棄されたツリーノードの詳細(id/kind/title/reason)を
	// 開発用にログ出力するか。既定: false。
	DebugDroppedNodes bool
}

// AITaskModelsConfig holds per-task Azure OpenAI deployment overrides.
type AITaskModelsConfig struct {
	ContextPlanner  string
	LiveExtraction  string
	TreeReorganizer string
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
	transcriptStore := strings.ToLower(strings.TrimSpace(os.Getenv("DECISCOPE_TRANSCRIPT_STORE")))
	if transcriptStore == "" {
		transcriptStore = TranscriptStorePostgres
	}
	return Config{
		Database: database.Config{URL: os.Getenv("DATABASE_URL")},
		TranscriptIngest: TranscriptIngestConfig{
			Store:  transcriptStore,
			APIKey: strings.TrimSpace(os.Getenv("DECISCOPE_INGEST_API_KEY")),
		},
		TranscriptWebSocket: TranscriptWebSocketConfig{
			ClientToken:    strings.TrimSpace(os.Getenv("DECISCOPE_WS_CLIENT_TOKEN")),
			AllowedOrigins: os.Getenv("DECISCOPE_WS_ALLOWED_ORIGINS"),
		},
		TranscriptOnly: transcriptOnly,
		BotControl: botcontrol.Config{
			URL:              strings.TrimSpace(os.Getenv("DECISCOPE_BOT_CONTROL_URL")),
			Token:            strings.TrimSpace(os.Getenv("DECISCOPE_BOT_CONTROL_TOKEN")),
			Timeout:          botControlTimeoutFromEnv(os.Getenv("DECISCOPE_BOT_CONTROL_TIMEOUT_SECONDS")),
			CandidateUserIDs: meetingTitleLookupUserIDsFromEnv(),
		},
		Firebase: firebase.Config{
			CredentialsFile: firstNonEmpty(os.Getenv("FIREBASE_SERVICE_ACCOUNT_JSON"), os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")),
			CredentialsJSON: os.Getenv("FIREBASE_CREDENTIALS_JSON"),
			ProjectID:       os.Getenv("FIREBASE_PROJECT_ID"),
			Enabled:         os.Getenv("AUTH_PROVIDER") == "firebase",
		},
		AI:                                  aiConfigFromEnv(),
		SessionWatchdog:                     sessionWatchdogConfigFromEnv(),
		FrontendURL:                         os.Getenv("FRONTEND_URL"),
		AllowedOrigins:                      os.Getenv("ALLOWED_ORIGINS"),
		SessionCookieSecure:                 strings.EqualFold(os.Getenv("SESSION_COOKIE_SECURE"), "true"),
		SeedDemoData:                        strings.EqualFold(strings.TrimSpace(os.Getenv("DECISCOPE_SEED_DEMO_DATA")), "true"),
		Environment:                         environmentFromEnv(),
		CreateSampleMeetingOnFirstWorkspace: sampleMeetingFlagFromEnv(environmentFromEnv()),
		InviteEmail: InviteEmailConfig{
			SMTPHost:     strings.TrimSpace(os.Getenv("DECISCOPE_SMTP_HOST")),
			SMTPPort:     strings.TrimSpace(os.Getenv("DECISCOPE_SMTP_PORT")),
			SMTPUsername: strings.TrimSpace(os.Getenv("DECISCOPE_SMTP_USERNAME")),
			SMTPPassword: os.Getenv("DECISCOPE_SMTP_PASSWORD"),
			From:         strings.TrimSpace(os.Getenv("DECISCOPE_SMTP_FROM")),
		},
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
// production ではメール未設定時に招待作成が失敗し、dev fallback (URLのログ出力) は無効になる。
func environmentFromEnv() string {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("DECISCOPE_ENV")))
	if value == "production" {
		return "production"
	}
	return "development"
}

func aiConfigFromEnv() AIConfig {
	requestTimeout := secondsDurationFromEnv(os.Getenv("AI_REQUEST_TIMEOUT_SECONDS"), defaultAIRequestTimeoutSeconds, 1)
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
			ContextPlanner:  strings.TrimSpace(os.Getenv("AI_MODEL_CONTEXT_PLANNER")),
			LiveExtraction:  strings.TrimSpace(os.Getenv("AI_MODEL_LIVE_EXTRACTION")),
			TreeReorganizer: strings.TrimSpace(os.Getenv("AI_MODEL_TREE_REORGANIZER")),
			FinalSummary:    strings.TrimSpace(os.Getenv("AI_MODEL_FINAL_SUMMARY")),
		},
		DebugDroppedNodes: strings.EqualFold(strings.TrimSpace(os.Getenv("AI_ANALYSIS_DEBUG_DROPPED_NODES")), "true"),
	}
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

func botControlTimeoutFromEnv(value string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds <= 0 {
		return 10 * time.Second
	}
	return time.Duration(seconds) * time.Second
}

func meetingTitleLookupUserIDsFromEnv() []string {
	return splitEnvList(firstNonEmpty(
		os.Getenv("MEETING_TITLE_LOOKUP_USER_IDS"),
		os.Getenv("DECISCOPE_MEETING_TITLE_LOOKUP_USER_IDS"),
	))
}

func splitEnvList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', ';', '\n', '\r', '\t', ' ':
			return true
		default:
			return false
		}
	})
	values := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		values = append(values, trimmed)
	}
	return values
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
	if !config.TranscriptOnly && strings.TrimSpace(config.Database.URL) == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	switch config.TranscriptIngest.Store {
	case "", TranscriptStorePostgres:
		if strings.TrimSpace(config.Database.URL) == "" {
			return fmt.Errorf("DATABASE_URL is required")
		}
	default:
		return fmt.Errorf("DECISCOPE_TRANSCRIPT_STORE must be postgres")
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
