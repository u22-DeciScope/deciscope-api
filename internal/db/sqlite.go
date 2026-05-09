package db

import (
    "database/sql"
    "fmt"
    "io/ioutil"
    "log"

    _ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

// InitSQLite は SQLite を初期化して、init_users.sql を流す
func InitSQLite() error {
    var err error

    // DB ファイルを開く（なければ作られる）
    DB, err = sql.Open("sqlite3", "./db.sqlite")
    if err != nil {
        return fmt.Errorf("failed to open sqlite: %w", err)
    }

    // 実際に接続できるかチェック
    if err := DB.Ping(); err != nil {
        return fmt.Errorf("failed to ping sqlite: %w", err)
    }

    log.Println("SQLite connected")

    // 初期化 SQL を読み込む
    sqlBytes, err := ioutil.ReadFile("internal/db/init_users.sql")
    if err != nil {
        return fmt.Errorf("failed to read init_users.sql: %w", err)
    }

    // 初期化 SQL を実行
    _, err = DB.Exec(string(sqlBytes))
    if err != nil {
        return fmt.Errorf("failed to exec init_users.sql: %w", err)
    }

    log.Println("SQLite initialized (t_Users ready)")

    return nil
}
