package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"deciscope-core-api/internal/adapter/fixture"
	httpadapter "deciscope-core-api/internal/adapter/http"
	authmiddleware "deciscope-core-api/internal/adapter/http/middleware"
	"deciscope-core-api/internal/adapter/realtime"
	"deciscope-core-api/internal/adapter/repository/memory"
	sqliterepository "deciscope-core-api/internal/adapter/repository/sqlite"
	"deciscope-core-api/internal/application"
	appaccess "deciscope-core-api/internal/application/access"
	appauth "deciscope-core-api/internal/application/auth"
	appworkspace "deciscope-core-api/internal/application/workspace"
	"deciscope-core-api/internal/infrastructure/database"
	"deciscope-core-api/internal/infrastructure/firebase"
	"deciscope-core-api/internal/infrastructure/storage"
)

func NewServer() (http.Handler, error) {
	ctx := context.Background()
	config := ConfigFromEnv()
	repositories, authRepository, err := buildRepositories(ctx, config.Database)
	if err != nil {
		return nil, err
	}

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

	return httpadapter.NewRouter(httpadapter.RouterDependencies{
		CoreAPI:      httpadapter.NewCoreAPI(service, replay),
		AuthAPI:      httpadapter.NewAuthAPI(authService, config.SessionCookieSecure, hub),
		WorkspaceAPI: httpadapter.NewWorkspaceAPI(workspaceService, hub),
		AuthService:  authService,
		Workspace:    workspaceService,
		Access:       accessService,
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
	}), nil
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

func buildRepositories(ctx context.Context, config database.Config) (repositorySet, authWorkspaceRepository, error) {
	conn, err := database.Open(ctx, config)
	if err != nil {
		log.Printf("database unavailable; /v1 uses in-memory local store: %v", err)
		store := memory.NewMemoryStore()
		return repositoriesFromStore(store), memory.NewAuthWorkspaceRepository(store), nil
	}
	if err := database.Migrate(ctx, conn, config.Driver); err != nil {
		_ = conn.Close()
		return repositorySet{}, nil, fmt.Errorf("migrate database: %w", err)
	}
	store := sqliterepository.NewStore(conn)
	log.Printf("database repository ready: driver=%q url=%q", config.Driver, config.URL)
	return repositoriesFromStore(store), sqliterepository.NewAuthWorkspaceRepository(conn), nil
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
