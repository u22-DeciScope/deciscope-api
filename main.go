package main

import (
	"log"
	"net/http"
	"os"

	"deciscope-core-api/internal/app"
	"deciscope-core-api/internal/db"
)

func main() {
    server, err := app.NewServer()
    if err != nil {
        log.Fatalf("build server: %v", err)
    }

    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }

    if err := http.ListenAndServe(":"+port, server); err != nil {
        log.Fatalf("listen: %v", err)
    }
}

