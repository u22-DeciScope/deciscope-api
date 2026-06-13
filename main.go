// main.go
// DeciScope API サーバーのエントリーポイント。
// app.NewServer() により HTTP サーバーを構築し、指定ポートで起動する。
// このファイル自体はルーティングやロジックを持たず、サーバー起動のみを担当する。
package main

import (
	"log"
	"net/http"

	"deciscope-core-api/internal/app"
)

func main() {
	app.LoadEnvironmentFiles()

	server, err := app.NewServer()
	if err != nil {
		log.Fatalf("build server: %v", err)
	}

	if err := http.ListenAndServe(app.ListenAddressFromEnv(), server); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
