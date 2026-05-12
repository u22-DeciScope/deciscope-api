// server.go
// DeciScope API の HTTP サーバーを構築する。
// SQLite 初期化、ルーティング設定（/register, /register-form など）を行い、
// main.go に渡す http.Handler を生成する。

package app

import (
	"fmt"
	"net/http"

	"deciscope-core-api/internal/db"
	"deciscope-core-api/internal/handlers"
)

func NewServer() (http.Handler, error) {
	if err := db.InitSQLite(); err != nil {
		return nil, fmt.Errorf("init sqlite: %w", err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/register", handlers.Register)
	mux.HandleFunc("/register-form", handlers.RegisterForm)
	mux.HandleFunc("/health", handlers.Health)
	mux.HandleFunc("/login", handlers.Login)

	return mux, nil
}
