package main

import (
	"log"
	"net/http"
	"os"

	"deciscope-core-api/internal/app"
)

func main() {
	server, err := app.NewServer()
	if err != nil {
		log.Fatalf("build server: %v", err)
	}

	log.Println("server initialized")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
		log.Println("PORT not set, using default 8080")
	}

	log.Printf("starting server on http://localhost:%s", port)

	if err := http.ListenAndServe(":"+port, server); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
