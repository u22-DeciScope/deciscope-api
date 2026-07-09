package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
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
	"deciscope-core-api/internal/infrastructure/database"
	"deciscope-core-api/internal/infrastructure/email"
	"deciscope-core-api/internal/infrastructure/firebase"
	"deciscope-core-api/internal/infrastructure/storage"

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
		transcriptRuntime, err := buildTranscriptIngest(ctx, config.TranscriptIngest, config.Database, nil, transcriptHub)
		if err != nil {
			return nil, err
		}
		healthAPI := httpadapter.NewHealthAPI(transcriptRuntime.ready)
		handler := httpadapter.NewRouter(httpadapter.RouterDependencies{
			TranscriptAPI:      httpadapter.NewTranscriptAPI(transcriptRuntime.service, config.TranscriptIngest.APIKey, config.TranscriptWebSocket.ClientToken),
			TranscriptRealtime: transcriptHub.ServeTranscriptSegments(transcriptRealtimeConfig(config.TranscriptWebSocket)),
			Healthz:            healthAPI.Healthz,
			Readyz:             healthAPI.Readyz,
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

	// 固定デモワークスペースの seed は明示的な開発・検証用途のみ。
	// 通常フローには関与せず、ログイン時の自動参加も行わない (閲覧するには手動で
	// workspace_members に追加する必要がある)。
	if config.SeedDemoData {
		if err := database.SeedDemoData(ctx, postgresDB); err != nil {
			_ = closeAll(closers)
			return nil, fmt.Errorf("seed demo data: %w", err)
		}
		log.Printf("demo seed data ensured (DECISCOPE_SEED_DEMO_DATA enabled; dev/test only)")
	}

	transcriptHub := realtime.NewTranscriptHub()
	meetingSessionRepository := postgresrepository.NewMeetingSessionRepository(postgresDB)
	analysisService := buildMeetingAnalysisService(config.AI, postgresDB, meetingSessionRepository, transcriptHub)
	transcriptActivityTracker := application.NewTranscriptActivityTracker()
	transcriptPublisher := compositeTranscriptSegmentPublisher{publishers: []application.TranscriptSegmentPublisher{transcriptHub, analysisService, transcriptActivityTracker}}
	transcriptRuntime, err := buildTranscriptIngest(ctx, config.TranscriptIngest, config.Database, postgresDB, transcriptPublisher)
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
				Interval:     config.SessionWatchdog.Interval,
				LostAfter:    config.SessionWatchdog.LostAfter,
				EndAfter:     config.SessionWatchdog.EndAfter,
				DelayedAfter: config.SessionWatchdog.TranscriptDelayedAfter,
				StalledAfter: config.SessionWatchdog.TranscriptStalledAfter,
			},
		)
		watchdog.SetTranscriptActivity(transcriptActivityTracker)
		watchdog.SetTranscriptHealthPublisher(transcriptHub)
		watchdogCtx, cancelWatchdog := context.WithCancel(context.Background())
		watchdog.Start(watchdogCtx)
		closers = append(closers, func() error {
			cancelWatchdog()
			return nil
		})
		log.Printf("meeting session bot watchdog enabled. interval=%s lostAfter=%s endAfter=%s transcriptDelayedAfter=%s transcriptStalledAfter=%s",
			config.SessionWatchdog.Interval, config.SessionWatchdog.LostAfter, config.SessionWatchdog.EndAfter, config.SessionWatchdog.TranscriptDelayedAfter, config.SessionWatchdog.TranscriptStalledAfter)
	} else {
		log.Printf("meeting session bot watchdog disabled (DECISCOPE_SESSION_WATCHDOG_ENABLED=false)")
	}
	tokenVerifier := firebase.NewTokenVerifier(authClient)
	service := application.NewService(
		repositories.Meetings, repositories.Events, repositories.Reports,
		repositories.Jobs, repositories.Uploads, hub, storage.NewLocal(config.UploadDir),
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
		TranscriptAPI: httpadapter.NewTranscriptAPI(transcriptRuntime.service, config.TranscriptIngest.APIKey, config.TranscriptWebSocket.ClientToken),
		MeetingSessionAPI: httpadapter.NewMeetingSessionAPI(
			meetingSessionService,
			config.TranscriptIngest.APIKey,
			httpadapter.WithMeetingSessionTranscriptService(transcriptRuntime.service),
			httpadapter.WithMeetingSessionTranscriptRealtime(transcriptHub.ServeTranscriptSegments(workspaceTranscriptConfig)),
			httpadapter.WithMeetingSessionAIAnalysisService(analysisService),
		),
		TranscriptRealtime: transcriptHub.ServeTranscriptSegments(transcriptRealtimeConfig(config.TranscriptWebSocket)),
		AuthService:        authService,
		Workspace:          workspaceService,
		Access:             accessService,
		Healthz:            healthAPI.Healthz,
		Readyz:             healthAPI.Readyz,
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
	Reports  application.ReportRepository
	Jobs     application.JobRepository
	Uploads  application.UploadRepository
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

func buildTranscriptIngest(ctx context.Context, config TranscriptIngestConfig, databaseConfig database.Config, postgresDB *sql.DB, publisher application.TranscriptSegmentPublisher) (transcriptIngestRuntime, error) {
	if config.Store != "" && config.Store != TranscriptStorePostgres {
		return transcriptIngestRuntime{}, fmt.Errorf("unsupported transcript store %q", config.Store)
	}
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
		log.Printf("AI meeting analysis enabled. deployment=%s liveAnalysisEnabled=%t finalSummaryEnabled=%t liveIntervalSeconds=%.0f",
			config.AzureOpenAI.Deployment, config.LiveAnalysisEnabled, config.FinalSummaryEnabled, config.LiveAnalysisInterval.Seconds())
	}

	analysisRepository := postgresrepository.NewMeetingAIAnalysisRepository(postgresDB)
	transcriptSegmentRepository := postgresrepository.NewTranscriptSegmentRepository(postgresDB)
	completer := azureopenai.NewClient(config.AzureOpenAI)

	return application.NewMeetingAnalysisService(
		analysisRepository,
		transcriptSegmentRepository,
		meetingSessionRepository,
		completer,
		application.MeetingAnalysisConfig{
			Enabled:             enabled,
			LiveEnabled:         config.LiveAnalysisEnabled,
			LiveInterval:        config.LiveAnalysisInterval,
			LiveMinChars:        config.LiveAnalysisMinChars,
			LiveMaxInputChars:   config.LiveAnalysisMaxInputChars,
			LiveRequestTimeout:  config.AzureOpenAI.Timeout,
			FinalEnabled:        config.FinalSummaryEnabled,
			FinalMaxInputChars:  config.FinalSummaryMaxInputChars,
			FinalRequestTimeout: config.FinalSummaryTimeout,
			Model:               config.AzureOpenAI.Deployment,
			DebugDroppedNodes:   config.DebugDroppedNodes,
		},
		publisher,
	)
}

func transcriptRealtimeConfig(config TranscriptWebSocketConfig) realtime.TranscriptWebSocketConfig {
	return realtime.TranscriptWebSocketConfig{
		ClientToken:    config.ClientToken,
		AllowedOrigins: config.AllowedOrigins,
	}
}

// buildInvitationMailer は招待メール送信の実装を環境に応じて選ぶ。
//   - SMTP設定あり: 実際に送信
//   - 未設定 + development: 招待URLをログ出力する dev fallback
//   - 未設定 + production: 送信失敗にして招待を成功扱いにしない
func buildInvitationMailer(config Config) appworkspace.InvitationMailer {
	if config.InviteEmail.Configured() {
		log.Printf("invitation email: SMTP mailer enabled (host=%s)", config.InviteEmail.SMTPHost)
		return email.NewSMTPMailer(email.SMTPConfig{
			Host:     config.InviteEmail.SMTPHost,
			Port:     config.InviteEmail.SMTPPort,
			Username: config.InviteEmail.SMTPUsername,
			Password: config.InviteEmail.SMTPPassword,
			From:     config.InviteEmail.From,
		})
	}
	if config.Environment == "production" {
		log.Printf("invitation email: SMTP is not configured in production; invitation creation will fail until DECISCOPE_SMTP_* is set")
		return email.DisabledMailer{}
	}
	log.Printf("invitation email: dev fallback enabled; invitation URLs are logged instead of sent (DECISCOPE_ENV=development)")
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
	application.ReportRepository
	application.JobRepository
	application.UploadRepository
}

func repositoriesFromStore(store repositoryStore) repositorySet {
	return repositorySet{
		Meetings: store, Events: store, Reports: store, Jobs: store, Uploads: store,
	}
}
