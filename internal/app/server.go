package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

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
	repositories, userRepository, err := buildRepositories(ctx)
	if err != nil {
		return nil, err
	}

	authClient, err := firebase.NewAuthClient(ctx)
	if err != nil {
		log.Printf("firebase auth disabled; protected routes accept Bearer dev:<uid>: %v", err)
	}

	hub := realtime.NewHub()
	service := application.NewService(repositories, hub, storage.NewLocal(os.Getenv("UPLOAD_DIR")))
	replay := fixture.NewManager(service, os.Getenv("FIXTURE_DIR"))

	return httpadapter.NewRouter(httpadapter.RouterDependencies{
		CoreAPI:    httpadapter.NewCoreAPI(service, replay),
		AuthAPI:    httpadapter.NewAuthAPI(appauth.NewService(userRepository, firebase.NewTokenVerifier(authClient))),
		Realtime:   hub.ServeWS(service),
		AuthClient: authClient,
	}), nil
}

func buildRepositories(ctx context.Context) (application.Repositories, appauth.UserRepository, error) {
	config := database.ConfigFromEnv()
	conn, err := database.Open(ctx, config)
	if err != nil {
		log.Printf("database unavailable; /v1 uses in-memory local store: %v", err)
		return memory.Repositories(memory.NewMemoryStore()), nil, nil
	}
	if err := database.Migrate(ctx, conn, config.Driver); err != nil {
		_ = conn.Close()
		return application.Repositories{}, nil, fmt.Errorf("migrate database: %w", err)
	}
	store := sqliterepository.NewStore(conn)
	return sqliterepository.Repositories(store), sqliterepository.NewUserRepository(conn), nil
}
