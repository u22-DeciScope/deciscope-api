package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	migrationsPath, err := filepath.Abs("db/migrations")
	if err != nil {
		log.Fatalf("resolve migrations path: %v", err)
	}

	migrator, err := migrate.New("file://"+filepath.ToSlash(migrationsPath), databaseURL)
	if err != nil {
		log.Fatalf("build migrator: %v", err)
	}

	switch command {
	case "up":
		err = migrator.Up()
	case "down":
		err = migrator.Down()
	case "force":
		if len(os.Args) < 3 {
			log.Fatal("force requires a version argument")
		}
		var version int
		if _, scanErr := fmt.Sscanf(os.Args[2], "%d", &version); scanErr != nil {
			log.Fatalf("invalid force version: %v", scanErr)
		}
		err = migrator.Force(version)
	default:
		log.Fatalf("unsupported command: %s", command)
	}

	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		log.Fatalf("migration %s failed: %v", command, err)
	}

	log.Printf("migration %s finished", command)
}
