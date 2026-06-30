package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"deciscope-core-api/internal/adapter/fixture"
	httpadapter "deciscope-core-api/internal/adapter/http"
	authmiddleware "deciscope-core-api/internal/adapter/http/middleware"
	"deciscope-core-api/internal/adapter/realtime"
	postgresrepository "deciscope-core-api/internal/adapter/repository/postgres"
	"deciscope-core-api/internal/application"
	appaccess "deciscope-core-api/internal/application/access"
	appauth "deciscope-core-api/internal/application/auth"
	appworkspace "deciscope-core-api/internal/application/workspace"
	"deciscope-core-api/internal/infrastructure/botcontrol"
	"deciscope-core-api/internal/infrastructure/database"
	"deciscope-core-api/internal/infrastructure/firebase"
	"deciscope-core-api/internal/infrastructure/storage"
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
		return &ServerRuntime{Handler: handler, closers: transcriptRuntime.closers}, nil
	}

	repositories, authRepository, postgresDB, err := buildRepositories(ctx, config.Database, config.SeedDemoData)
	if err != nil {
		return nil, err
	}
	closers := []func() error{postgresDB.Close}

	if config.SeedDemoData {
		if err := database.SeedDemoData(ctx, postgresDB); err != nil {
			_ = closeAll(closers)
			return nil, fmt.Errorf("seed demo data: %w", err)
		}
		log.Printf("demo seed data ensured (DECISCOPE_SEED_DEMO_DATA enabled)")
	}

	transcriptHub := realtime.NewTranscriptHub()
	transcriptRuntime, err := buildTranscriptIngest(ctx, config.TranscriptIngest, config.Database, postgresDB, transcriptHub)
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
		postgresrepository.NewMeetingSessionRepository(postgresDB),
		botcontrol.NewClient(config.BotControl),
		transcriptHub,
	)
	tokenVerifier := firebase.NewTokenVerifier(authClient)
	service := application.NewService(
		repositories.Meetings, repositories.Events, repositories.Reports,
		repositories.Jobs, repositories.Uploads, hub, storage.NewLocal(config.UploadDir),
	)
	replay := fixture.NewManager(service, fixture.NewLocalLoader(config.FixtureDir))
	authService := appauth.NewService(authRepository, tokenVerifier, 7*24*time.Hour)
	workspaceService := appworkspace.NewService(authRepository)
	accessService := appaccess.NewService(authRepository)

	handler := httpadapter.NewRouter(httpadapter.RouterDependencies{
		CoreAPI:       httpadapter.NewCoreAPI(service, replay),
		AuthAPI:       httpadapter.NewAuthAPI(authService, config.SessionCookieSecure, hub),
		WorkspaceAPI:  httpadapter.NewWorkspaceAPI(workspaceService, hub),
		TranscriptAPI: httpadapter.NewTranscriptAPI(transcriptRuntime.service, config.TranscriptIngest.APIKey, config.TranscriptWebSocket.ClientToken),
		MeetingSessionAPI: httpadapter.NewMeetingSessionAPI(
			meetingSessionService,
			config.TranscriptIngest.APIKey,
			httpadapter.WithMeetingSessionTranscriptService(transcriptRuntime.service),
			httpadapter.WithMeetingSessionTranscriptRealtime(transcriptHub.ServeTranscriptSegments(transcriptRealtimeConfig(config.TranscriptWebSocket))),
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

func buildRepositories(ctx context.Context, config database.Config, seedDemoData bool) (repositorySet, authWorkspaceRepository, *sql.DB, error) {
	conn, err := database.Open(ctx, config)
	if err != nil {
		return repositorySet{}, nil, nil, fmt.Errorf("open database: %w", err)
	}
	store := postgresrepository.NewStore(conn)
	log.Printf("postgres database repository ready")
	authRepository := postgresrepository.NewAuthWorkspaceRepository(conn).WithDemoWorkspace(seedDemoData)
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

func transcriptRealtimeConfig(config TranscriptWebSocketConfig) realtime.TranscriptWebSocketConfig {
	return realtime.TranscriptWebSocketConfig{
		ClientToken:    config.ClientToken,
		AllowedOrigins: config.AllowedOrigins,
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
