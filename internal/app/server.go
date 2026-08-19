package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	httpadapter "deciscope-core-api/internal/adapter/http"
	authmiddleware "deciscope-core-api/internal/adapter/http/middleware"
	"deciscope-core-api/internal/adapter/realtime"
	postgresrepository "deciscope-core-api/internal/adapter/repository/postgres"
	"deciscope-core-api/internal/application"
	appaccess "deciscope-core-api/internal/application/access"
	appauth "deciscope-core-api/internal/application/auth"
	appworkspace "deciscope-core-api/internal/application/workspace"
	"deciscope-core-api/internal/domain"
	"deciscope-core-api/internal/infrastructure/azureopenai"
	"deciscope-core-api/internal/infrastructure/botcontrol"
	"deciscope-core-api/internal/infrastructure/clientdiagnostics"
	"deciscope-core-api/internal/infrastructure/database"
	"deciscope-core-api/internal/infrastructure/email"
	"deciscope-core-api/internal/infrastructure/firebase"

	"github.com/go-chi/chi/v5"
)

type ServerRuntime struct {
	Handler http.Handler
	closers []func() error
}

func (runtime *ServerRuntime) Close() error {
	if runtime == nil {
		return nil
	}
	var errs []error
	for _, closeFn := range runtime.closers {
		if err := closeFn(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func NewServerRuntime() (*ServerRuntime, error) {
	ctx := context.Background()
	config := ConfigFromEnv()
	if err := ValidateRuntimeConfig(config); err != nil {
		return nil, err
	}

	if config.TranscriptOnly {
		transcriptHub := realtime.NewTranscriptHub()
		transcriptRuntime, err := buildTranscriptIngest(ctx, config.Database, nil, transcriptHub)
		if err != nil {
			return nil, err
		}
		healthAPI := httpadapter.NewHealthAPI(transcriptRuntime.ready)
		handler := httpadapter.NewRouter(httpadapter.RouterDependencies{
			TranscriptAPI: httpadapter.NewTranscriptAPI(transcriptRuntime.service, config.TranscriptIngest.APIKey),
			Healthz:       healthAPI.Healthz,
			Readyz:        healthAPI.Readyz,
			CORS: httpadapter.CORSConfig{
				FrontendURL: config.FrontendURL, AllowedOrigins: config.AllowedOrigins,
			},
		})
		log.Printf("transcript-only mode enabled; core postgres repositories are not initialized")
		log.Printf("AI meeting analysis disabled; transcript-only mode does not initialize AI analysis")
		return &ServerRuntime{Handler: handler, closers: transcriptRuntime.closers}, nil
	}

	repositories, authRepository, postgresDB, err := buildRepositories(ctx, config.Database)
	if err != nil {
		return nil, err
	}
	closers := []func() error{postgresDB.Close}

	transcriptHub := realtime.NewTranscriptHub()
	meetingSessionRepository := postgresrepository.NewMeetingSessionRepository(postgresDB)
	analysisService := buildMeetingAnalysisService(config.AI, postgresDB, meetingSessionRepository, transcriptHub)
	transcriptActivityTracker := application.NewTranscriptActivityTracker()
	botMediaMetricsStore := application.NewBotMediaMetricsStore()
	botMediaHealthService := application.NewBotMediaHealthService(transcriptHub)
	transcriptPublisher := compositeTranscriptSegmentPublisher{publishers: []application.TranscriptSegmentPublisher{transcriptHub, analysisService, transcriptActivityTracker}}
	transcriptRuntime, err := buildTranscriptIngest(ctx, config.Database, postgresDB, transcriptPublisher)
	if err != nil {
		_ = closeAll(closers)
		return nil, err
	}
	closers = append(closers, transcriptRuntime.closers...)
	readyCheck := transcriptRuntime.ready
	healthAPI := httpadapter.NewHealthAPI(readyCheck)

	authClient, err := firebase.NewAuthClient(ctx, config.Firebase)
	if err != nil {
		log.Printf("firebase login disabled: %v", err)
	}

	hub := realtime.NewHub()
	meetingSessionService := application.NewMeetingSessionService(
		meetingSessionRepository,
		botcontrol.NewClient(config.BotControl),
		transcriptHub,
	)
	meetingSessionService.SetMeetingSessionEndedObserver(analysisService)
	meetingSessionService.SetMeetingSessionPreparingObserver(analysisService)
	analysisCtx, cancelAnalysis := context.WithCancel(context.Background())
	analysisService.Start(analysisCtx)
	closers = append(closers, func() error {
		cancelAnalysis()
		return analysisService.Close()
	})

	if config.SessionWatchdog.Enabled {
		watchdog := application.NewMeetingSessionWatchdog(
			meetingSessionRepository,
			meetingSessionService,
			transcriptHub,
			application.MeetingSessionWatchdogConfig{
				Interval:           config.SessionWatchdog.Interval,
				LostAfter:          config.SessionWatchdog.LostAfter,
				EndAfter:           config.SessionWatchdog.EndAfter,
				DelayedAfter:       config.SessionWatchdog.TranscriptDelayedAfter,
				StalledAfter:       config.SessionWatchdog.TranscriptStalledAfter,
				AudioSilenceAfter:  config.SessionWatchdog.AudioSilenceAfter,
				AudioStalledAfter:  config.SessionWatchdog.AudioStalledAfter,
				SpeechStalledAfter: config.SessionWatchdog.SpeechStalledAfter,
			},
		)
		watchdog.SetTranscriptActivity(transcriptActivityTracker)
		watchdog.SetTranscriptHealthPublisher(transcriptHub)
		watchdog.SetBotMetrics(botMediaMetricsStore)
		watchdogCtx, cancelWatchdog := context.WithCancel(context.Background())
		watchdog.Start(watchdogCtx)
		closers = append(closers, func() error {
			cancelWatchdog()
			return nil
		})
		log.Printf("meeting session bot watchdog enabled. interval=%s lostAfter=%s endAfter=%s transcriptDelayedAfter=%s transcriptStalledAfter=%s audioSilenceAfter=%s audioStalledAfter=%s speechStalledAfter=%s",
			config.SessionWatchdog.Interval, config.SessionWatchdog.LostAfter, config.SessionWatchdog.EndAfter, config.SessionWatchdog.TranscriptDelayedAfter, config.SessionWatchdog.TranscriptStalledAfter,
			config.SessionWatchdog.AudioSilenceAfter, config.SessionWatchdog.AudioStalledAfter, config.SessionWatchdog.SpeechStalledAfter)
	} else {
		log.Printf("meeting session bot watchdog disabled (DECISCOPE_SESSION_WATCHDOG_ENABLED=false)")
	}
	tokenVerifier := firebase.NewTokenVerifier(authClient)
	service := application.NewService(
		repositories.Meetings, repositories.Events, hub,
	)
	authService := appauth.NewService(authRepository, tokenVerifier, 7*24*time.Hour)
	workspaceService := appworkspace.NewService(authRepository, buildInvitationMailer(config), config.FrontendURL)
	if config.CreateSampleMeetingOnFirstWorkspace {
		workspaceService.SetSampleMeetingCreator(database.NewSampleMeetingSeeder(postgresDB))
		log.Printf("sample meeting on first workspace: enabled")
	} else {
		log.Printf("sample meeting on first workspace: disabled")
	}
	accessService := appaccess.NewService(authRepository)
	connectionCloser := workspaceConnectionCloser{hub: hub, transcripts: transcriptHub}
	clientDiagnosticsAPI := buildClientDiagnosticsAPI(config.ClientDiagnostics, workspaceService, meetingSessionService)

	// workspace経由のtranscript購読には認証済みユーザーを紐づけ、
	// メンバー削除時に既存WebSocket接続を切断できるようにする。
	workspaceTranscriptConfig := transcriptRealtimeConfig(config.TranscriptWebSocket)
	workspaceTranscriptConfig.ResolveMember = func(r *http.Request) (string, string) {
		session, ok := authmiddleware.SessionFromContext(r.Context())
		if !ok || session.User == nil {
			return "", ""
		}
		return chi.URLParam(r, "workspace_code"), session.User.ID
	}

	handler := httpadapter.NewRouter(httpadapter.RouterDependencies{
		CoreAPI:       httpadapter.NewCoreAPI(service),
		AuthAPI:       httpadapter.NewAuthAPI(authService, config.SessionCookieSecure, connectionCloser),
		WorkspaceAPI:  httpadapter.NewWorkspaceAPI(workspaceService, connectionCloser),
		TranscriptAPI: httpadapter.NewTranscriptAPI(transcriptRuntime.service, config.TranscriptIngest.APIKey),
		MeetingSessionAPI: httpadapter.NewMeetingSessionAPI(
			meetingSessionService,
			config.TranscriptIngest.APIKey,
			httpadapter.WithMeetingSessionTranscriptService(transcriptRuntime.service),
			httpadapter.WithMeetingSessionTranscriptRealtime(transcriptHub.ServeTranscriptSegments(workspaceTranscriptConfig)),
			httpadapter.WithMeetingSessionAIAnalysisService(analysisService),
			httpadapter.WithMeetingSessionBotMetricsStore(botMediaMetricsStore),
			httpadapter.WithMeetingSessionBotMediaHealth(botMediaHealthService),
		),
		ClientDiagnosticsAPI: clientDiagnosticsAPI,
		AuthService:          authService,
		Workspace:            workspaceService,
		Access:               accessService,
		Healthz:              healthAPI.Healthz,
		Readyz:               healthAPI.Readyz,
		Realtime: hub.ServeWS(service, func(r *http.Request) (realtime.ClientIdentity, bool) {
			session, ok := authmiddleware.SessionFromContext(r.Context())
			if !ok || session.User == nil || session.Session == nil {
				return realtime.ClientIdentity{}, false
			}
			return realtime.ClientIdentity{UserID: session.User.ID, SessionID: session.Session.ID}, true
		}),
		CORS: httpadapter.CORSConfig{
			FrontendURL: config.FrontendURL, AllowedOrigins: config.AllowedOrigins,
		},
	})
	return &ServerRuntime{Handler: handler, closers: closers}, nil
}

// buildClientDiagnosticsAPI はクライアント診断ログの受け口を組み立てる。
// 出力先ディレクトリを作れない場合でも nil を返してサーバー起動は続行する
// (診断機能の失敗が会議機能を止めないことを優先する)。
func buildClientDiagnosticsAPI(
	config ClientDiagnosticsConfig,
	workspace httpadapter.WorkspaceAccessUseCases,
	sessions httpadapter.ClientDiagnosticsSessionLookup,
) *httpadapter.ClientDiagnosticsAPI {
	if !config.Enabled {
		log.Printf("client diagnostics disabled (DECISCOPE_CLIENT_DIAGNOSTICS_ENABLED=false)")
		return nil
	}
	options := []application.ClientDiagnosticsServiceOption{
		application.WithClientDiagnosticsLimits(application.ClientDiagnosticsLimits{
			MaxEventsPerRequest: config.MaxEventsPerRequest,
			ThrottleWindow:      config.ThrottleWindow,
		}),
		application.WithClientDiagnosticsSink("stdlog", clientdiagnostics.NewLogSink(os.Stdout)),
		application.WithClientDiagnosticsSinkErrorReporter(func(sink string, err error) {
			log.Printf("client diagnostics sink write failed. sink=%s error=%v", sink, err)
		}),
	}

	fileSink, err := clientdiagnostics.NewFileSink(clientdiagnostics.FileSinkConfig{
		Directory:    config.Directory,
		MaxFileBytes: config.MaxFileBytes,
		Retention:    config.Retention,
	})
	if err != nil {
		log.Printf("client diagnostics JSONL file sink unavailable; falling back to standard log only. directory=%s error=%v", config.Directory, err)
	} else {
		options = append(options, application.WithClientDiagnosticsSink("jsonl", fileSink))
		log.Printf("client diagnostics enabled. directory=%s maxFileBytes=%d retentionHours=%.0f maxEventsPerRequest=%d throttleMs=%d",
			fileSink.Directory(), config.MaxFileBytes, config.Retention.Hours(), config.MaxEventsPerRequest, config.ThrottleWindow.Milliseconds())
	}

	return httpadapter.NewClientDiagnosticsAPI(
		application.NewClientDiagnosticsService(options...),
		workspace,
		sessions,
	)
}

func MigrateDatabase(ctx context.Context) error {
	config := ConfigFromEnv()
	conn, err := database.Open(ctx, config.Database)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer conn.Close()
	if err := database.Migrate(ctx, conn); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	log.Printf("postgres migrations applied")
	return nil
}

type repositorySet struct {
	Meetings application.MeetingRepository
	Events   application.EventRepository
}

type authWorkspaceRepository interface {
	appauth.Repository
	appworkspace.Repository
	appaccess.Repository
}

func buildRepositories(ctx context.Context, config database.Config) (repositorySet, authWorkspaceRepository, *sql.DB, error) {
	conn, err := database.Open(ctx, config)
	if err != nil {
		return repositorySet{}, nil, nil, fmt.Errorf("open database: %w", err)
	}
	store := postgresrepository.NewStore(conn)
	log.Printf("postgres database repository ready")
	authRepository := postgresrepository.NewAuthWorkspaceRepository(conn)
	return repositoriesFromStore(store), authRepository, conn, nil
}

type transcriptIngestRuntime struct {
	service *application.TranscriptIngestService
	ready   httpadapter.HealthCheckFunc
	closers []func() error
}

func buildTranscriptIngest(ctx context.Context, databaseConfig database.Config, postgresDB *sql.DB, publisher application.TranscriptSegmentPublisher) (transcriptIngestRuntime, error) {
	conn := postgresDB
	var closers []func() error
	if conn == nil {
		var err error
		conn, err = database.Open(ctx, databaseConfig)
		if err != nil {
			return transcriptIngestRuntime{}, fmt.Errorf("open transcript postgres: %w", err)
		}
		closers = append(closers, conn.Close)
	}
	repository := postgresrepository.NewTranscriptSegmentRepository(conn)
	log.Printf("postgres transcript repository ready")
	return transcriptIngestRuntime{
		service: application.NewTranscriptIngestService(repository, publisher),
		ready: func(ctx context.Context) error {
			return conn.PingContext(ctx)
		},
		closers: closers,
	}, nil
}

// compositeTranscriptSegmentPublisher fans a stored final transcript segment
// out to every listener (the realtime hub and the AI analysis service)
// without TranscriptIngestService knowing about either one.
type compositeTranscriptSegmentPublisher struct {
	publishers []application.TranscriptSegmentPublisher
}

func (p compositeTranscriptSegmentPublisher) PublishTranscriptSegment(segment domain.TranscriptSegment) {
	for _, publisher := range p.publishers {
		publisher.PublishTranscriptSegment(segment)
	}
}

// buildMeetingAnalysisService always returns a non-nil service. When Azure
// OpenAI is not fully configured, MeetingAnalysisConfig.Enabled is false and
// every operation on the service becomes a no-op, so callers never need nil
// checks.
func buildMeetingAnalysisService(config AIConfig, postgresDB *sql.DB, meetingSessionRepository application.MeetingSessionRepository, publisher application.MeetingAIAnalysisPublisher) *application.MeetingAnalysisService {
	enabled := config.Enabled()
	if !enabled {
		log.Printf("AI meeting analysis disabled; missing environment variables: %s", strings.Join(config.MissingAzureOpenAIVars(), ", "))
	} else {
		log.Printf("AI meeting analysis enabled. deployment=%s liveAnalysisEnabled=%t finalSummaryEnabled=%t liveIntervalSeconds=%.0f liveDebounceMs=%d liveCooldownSeconds=%.0f liveMaxWaitSeconds=%.0f",
			config.AzureOpenAI.Deployment, config.LiveAnalysisEnabled, config.FinalSummaryEnabled, config.LiveAnalysisInterval.Seconds(),
			config.LiveAnalysisDebounce.Milliseconds(), config.LiveAnalysisCooldown.Seconds(), config.LiveAnalysisMaxWait.Seconds())
	}

	analysisRepository := postgresrepository.NewMeetingAIAnalysisRepository(postgresDB)
	transcriptSegmentRepository := postgresrepository.NewTranscriptSegmentRepository(postgresDB)
	completer := azureopenai.NewClient(config.AzureOpenAI)
	auditRepository := postgresrepository.NewMeetingTreeAuditRepository(postgresDB)
	auditReason := treeAuditConfigurationIssue(config, enabled)
	repositoryReady := false
	if auditReason == "" {
		readinessCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		readinessErr := auditRepository.CheckMeetingTreeAuditRepository(readinessCtx)
		cancel()
		auditReason = treeAuditRepositoryIssue(readinessErr)
		if readinessErr == nil {
			repositoryReady = true
		}
	}
	effectiveTreeAudit := config.TreeAudit
	if auditReason != "" {
		effectiveTreeAudit.Enabled = false
	}
	schedulerRegistered := treeAuditSchedulerRegistered(effectiveTreeAudit, repositoryReady)
	logTreeAuditConfiguration(config, auditReason, repositoryReady, schedulerRegistered)

	service := application.NewMeetingAnalysisService(
		analysisRepository,
		transcriptSegmentRepository,
		meetingSessionRepository,
		completer,
		application.MeetingAnalysisConfig{
			Enabled:                 enabled,
			LiveEnabled:             config.LiveAnalysisEnabled,
			LiveInterval:            config.LiveAnalysisInterval,
			LiveDebounce:            config.LiveAnalysisDebounce,
			LiveCooldown:            config.LiveAnalysisCooldown,
			LiveMaxWait:             config.LiveAnalysisMaxWait,
			LiveMinChars:            config.LiveAnalysisMinChars,
			LiveMaxInputChars:       config.LiveAnalysisMaxInputChars,
			LiveRequestTimeout:      config.AzureOpenAI.Timeout,
			ContextRequestTimeout:   config.AzureOpenAI.Timeout,
			FinalEnabled:            config.FinalSummaryEnabled,
			FinalMaxInputChars:      config.FinalSummaryMaxInputChars,
			FinalRequestTimeout:     config.FinalSummaryTimeout,
			FinalizationWaitTimeout: config.FinalizationWaitTimeout,
			FinalizationQuietPeriod: config.FinalizationQuietPeriod,
			FinalFlushMaxAttempts:   config.FinalFlushMaxAttempts,
			Model:                   config.AzureOpenAI.Deployment,
			TaskModels: application.AITaskModels{
				ContextPlanner:  config.TaskModels.ContextPlanner,
				LiveExtraction:  config.TaskModels.LiveExtraction,
				TreeAudit:       config.TaskModels.TreeAudit,
				TreeReorganizer: config.TaskModels.TreeReorganizer,
				FinalTreeReview: config.TaskModels.FinalTreeReview,
				FinalSummary:    config.TaskModels.FinalSummary,
			},
			TreeClassification:         config.TreeClassification,
			DebugDroppedNodes:          config.DebugDroppedNodes,
			TreeAudit:                  effectiveTreeAudit,
			TreeAuditUnavailableReason: auditReason,
		},
		publisher,
	)
	if schedulerRegistered {
		service.SetMeetingTreeAuditRepository(auditRepository)
	}
	service.SetMeetingAgendaProgressOverridesRepository(postgresrepository.NewMeetingAgendaProgressOverridesRepository(postgresDB))
	return service
}

func logTreeAuditConfiguration(config AIConfig, auditReason string, repositoryReady, schedulerRegistered bool) {
	if auditReason == "feature_flag_false" {
		log.Printf("AI tree audit disabled. reason=%s", auditReason)
	} else if auditReason != "" {
		log.Printf("AI tree audit unavailable. reason=%s", auditReason)
	}
	log.Printf("AI tree audit configuration. enabled=%t treeAuditDeployment=%s finalTreeReviewDeployment=%s intervalVersions=%d intervalSeconds=%.0f minIntervalSeconds=%.0f maxRunsPerSession=%d maxRunsPerHour=%d highSeverityMinIntervalSeconds=%.0f highSeverityMaxRunsPerHour=%d repositoryReady=%t schedulerRegistered=%t reason=%s",
		config.TreeAudit.Enabled,
		strings.TrimSpace(config.TaskModels.TreeAudit), strings.TrimSpace(config.TaskModels.FinalTreeReview),
		config.TreeAudit.IntervalVersions, config.TreeAudit.Interval.Seconds(), config.TreeAudit.MinInterval.Seconds(),
		config.TreeAudit.MaxRunsPerSession, config.TreeAudit.MaxRunsPerHour,
		config.TreeAudit.HighSeverityMinInterval.Seconds(), config.TreeAudit.HighSeverityMaxRunsPerHour,
		repositoryReady, schedulerRegistered, firstNonEmpty(auditReason, "ready"))
}

func treeAuditSchedulerRegistered(config application.TreeAuditConfig, repositoryReady bool) bool {
	return config.Enabled && repositoryReady
}

func treeAuditConfigurationIssue(config AIConfig, aiEnabled bool) string {
	if config.TreeAuditEnabledInvalid {
		return "invalid_feature_flag"
	}
	if !config.TreeAudit.Enabled {
		return "feature_flag_false"
	}
	if !aiEnabled {
		return "azure_openai_not_configured"
	}
	if strings.TrimSpace(config.TaskModels.TreeAudit) == "" {
		return "tree_audit_deployment_empty"
	}
	if strings.TrimSpace(config.TaskModels.FinalTreeReview) == "" {
		return "final_tree_review_deployment_empty"
	}
	return ""
}

func treeAuditRepositoryIssue(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, application.ErrMeetingTreeAuditMigrationMissing) {
		return "migration_missing"
	}
	return "repository_not_ready"
}

func transcriptRealtimeConfig(config TranscriptWebSocketConfig) realtime.TranscriptWebSocketConfig {
	return realtime.TranscriptWebSocketConfig{
		AllowedOrigins: config.AllowedOrigins,
	}
}

// buildInvitationMailer は招待リンク通知の実装を環境に応じて選ぶ。
//   - development: 招待URLをログ出力する dev fallback
//   - production: 通知失敗にして招待を成功扱いにしない
func buildInvitationMailer(config Config) appworkspace.InvitationMailer {
	if config.Environment == "production" {
		log.Printf("invitation delivery is disabled in production")
		return email.DisabledMailer{}
	}
	log.Printf("invitation dev fallback enabled; invitation URLs are logged (DECISCOPE_ENV=development)")
	return email.LogMailer{}
}

// workspaceConnectionCloser はメンバー削除・ログアウト時に、
// realtime hub と transcript hub の両方の既存接続を閉じる。
type workspaceConnectionCloser struct {
	hub         *realtime.Hub
	transcripts *realtime.TranscriptHub
}

func (c workspaceConnectionCloser) CloseSession(sessionID string) {
	if c.hub != nil {
		c.hub.CloseSession(sessionID)
	}
}

func (c workspaceConnectionCloser) CloseWorkspaceMember(workspaceID, userID string) {
	if c.hub != nil {
		c.hub.CloseWorkspaceMember(workspaceID, userID)
	}
	if c.transcripts != nil {
		c.transcripts.CloseWorkspaceMember(workspaceID, userID)
	}
}

func closeAll(closers []func() error) error {
	var errs []error
	for _, closeFn := range closers {
		if err := closeFn(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type repositoryStore interface {
	application.MeetingRepository
	application.EventRepository
}

func repositoriesFromStore(store repositoryStore) repositorySet {
	return repositorySet{
		Meetings: store, Events: store,
	}
}
