// main.go
// DeciScope API サーバーのエントリーポイント。
// app.NewServer() により HTTP サーバーを構築し、指定ポートで起動する。
// このファイル自体はルーティングやロジックを持たず、サーバー起動のみを担当する。
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"

	"deciscope-core-api/internal/app"
)

func main() {
	// Load .env file (ignore error if not present)
	_ = godotenv.Load()

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
