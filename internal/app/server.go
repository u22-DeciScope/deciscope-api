package app

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"deciscope-core-api/internal/adapter/fixture"
	httpadapter "deciscope-core-api/internal/adapter/http"
	"deciscope-core-api/internal/adapter/realtime"
	"deciscope-core-api/internal/adapter/repository/memory"
	sqliterepository "deciscope-core-api/internal/adapter/repository/sqlite"
	"deciscope-core-api/internal/application"
	appauth "deciscope-core-api/internal/application/auth"
	"deciscope-core-api/internal/infrastructure/database"
	"deciscope-core-api/internal/infrastructure/firebase"
	"deciscope-core-api/internal/infrastructure/storage"
)

func NewServer() (http.Handler, error) {
	ctx := context.Background()
	config := ConfigFromEnv()
	repositories, userRepository, err := buildRepositories(ctx, config.Database)
	if err != nil {
		return nil, err
	}

	authClient, err := firebase.NewAuthClient(ctx, config.Firebase)
	if err != nil {
		log.Printf("firebase auth disabled; protected routes accept Bearer dev:<uid>: %v", err)
	}

	hub := realtime.NewHub()
	tokenVerifier := firebase.NewTokenVerifier(authClient)
	service := application.NewService(
		repositories.Meetings, repositories.Events, repositories.Reports,
		repositories.Jobs, repositories.Uploads, hub, storage.NewLocal(config.UploadDir),
	)
	replay := fixture.NewManager(service, fixture.NewLocalLoader(config.FixtureDir))

	return httpadapter.NewRouter(httpadapter.RouterDependencies{
		CoreAPI:      httpadapter.NewCoreAPI(service, replay),
		AuthAPI:      httpadapter.NewAuthAPI(appauth.NewService(userRepository, tokenVerifier)),
		Realtime:     hub.ServeWS(service),
		AuthVerifier: tokenVerifier,
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

func buildRepositories(ctx context.Context, config database.Config) (repositorySet, appauth.UserRepository, error) {
	conn, err := database.Open(ctx, config)
	if err != nil {
		log.Printf("database unavailable; /v1 uses in-memory local store: %v", err)
		store := memory.NewMemoryStore()
		return repositoriesFromStore(store), nil, nil
	}
	if err := database.Migrate(ctx, conn, config.Driver); err != nil {
		_ = conn.Close()
		return repositorySet{}, nil, fmt.Errorf("migrate database: %w", err)
	}
	store := sqliterepository.NewStore(conn)
	return repositoriesFromStore(store), sqliterepository.NewUserRepository(conn), nil
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
