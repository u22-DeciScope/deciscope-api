package app

import (
    "fmt"
    "net/http"

    "deciscope-core-api/internal/db"
    "deciscope-core-api/internal/handlers"
)

func NewServer() (http.Handler, error) {
    // SQLite 初期化
    if err := db.InitSQLite(); err != nil {
        return nil, fmt.Errorf("init sqlite: %w", err)
    }

    // ルーター作成
    mux := http.NewServeMux()

    // ルーティング
    mux.HandleFunc("/register", handlers.Register)

    return mux, nil
}
