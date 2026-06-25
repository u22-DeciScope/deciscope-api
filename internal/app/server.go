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
	sqliterepository "deciscope-core-api/internal/adapter/repository/sqlite"
	"deciscope-core-api/internal/application"
	appaccess "deciscope-core-api/internal/application/access"
	appauth "deciscope-core-api/internal/application/auth"
	appworkspace "deciscope-core-api/internal/application/workspace"
	"deciscope-core-api/internal/infrastructure/database"
	"deciscope-core-api/internal/infrastructure/firebase"
	sqliteinfra "deciscope-core-api/internal/infrastructure/sqlite"
	"deciscope-core-api/internal/infrastructure/storage"
)

func NewServer() (http.Handler, error) {
	runtime, err := NewServerRuntime()
	if err != nil {
		return nil, err
	}
	return runtime.Handler, nil
}

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

	transcriptDB, err := buildTranscriptDatabase(ctx, config.TranscriptIngest.SQLite)
	if err != nil {
		return nil, err
	}
	closers := []func() error{transcriptDB.Close}
	transcriptRepository := sqliterepository.NewTranscriptSegmentRepository(transcriptDB)
	transcriptService := application.NewTranscriptIngestService(transcriptRepository)
	healthAPI := httpadapter.NewHealthAPI(func(ctx context.Context) error {
		return transcriptDB.PingContext(ctx)
	})

	if config.TranscriptOnly {
		handler := httpadapter.NewRouter(httpadapter.RouterDependencies{
			TranscriptAPI: httpadapter.NewTranscriptAPI(transcriptService, config.TranscriptIngest.APIKey),
			Healthz:       healthAPI.Healthz,
			CORS: httpadapter.CORSConfig{
				FrontendURL: config.FrontendURL, AllowedOrigins: config.AllowedOrigins,
			},
		})
		log.Printf("transcript-only mode enabled; postgres repositories are not initialized")
		return &ServerRuntime{Handler: handler, closers: closers}, nil
	}

	repositories, authRepository, postgresDB, err := buildRepositories(ctx, config.Database)
	if err != nil {
		_ = closeAll(closers)
		return nil, err
	}
	closers = append(closers, postgresDB.Close)

	authClient, err := firebase.NewAuthClient(ctx, config.Firebase)
	if err != nil {
		log.Printf("firebase login disabled: %v", err)
	}

	hub := realtime.NewHub()
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
		TranscriptAPI: httpadapter.NewTranscriptAPI(transcriptService, config.TranscriptIngest.APIKey),
		AuthService:   authService,
		Workspace:     workspaceService,
		Access:        accessService,
		Healthz:       healthAPI.Healthz,
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
	if err := database.Migrate(ctx, conn); err != nil {
		_ = conn.Close()
		return repositorySet{}, nil, nil, fmt.Errorf("migrate database: %w", err)
	}
	store := postgresrepository.NewStore(conn)
	log.Printf("postgres database repository ready")
	return repositoriesFromStore(store), postgresrepository.NewAuthWorkspaceRepository(conn), conn, nil
}

func buildTranscriptDatabase(ctx context.Context, config sqliteinfra.Config) (*sql.DB, error) {
	conn, err := sqliteinfra.Open(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open transcript sqlite: %w", err)
	}
	if err := sqliterepository.InitializeTranscriptSegments(ctx, conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("initialize transcript sqlite: %w", err)
	}
	log.Printf("sqlite transcript repository ready")
	return conn, nil
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
