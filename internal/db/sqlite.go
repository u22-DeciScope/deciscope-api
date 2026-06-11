// sqlite.go
// SQLite データベースの初期化と接続管理を行う。
// Conn はアプリ全体で共有される *sql.DB。

package db

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

func InitSQLite() (*sql.DB, error) {
	dbPath := os.Getenv("SQLITE_PATH")
	if dbPath == "" {
		dbPath = os.Getenv("AUTH_SQLITE_PATH")
	}
	if dbPath == "" {
		dbPath = "./db.sqlite"
	}

	dsn := dbPath
	if !strings.Contains(dsn, "?") {
		dsn += "?_foreign_keys=on&_busy_timeout=5000"
	}

	conn, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	conn.SetMaxOpenConns(1)

	// 接続確認
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if _, err := conn.Exec(`PRAGMA journal_mode = WAL;`); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("enable sqlite wal: %w", err)
	}

	// テーブル作成
	_, err = conn.Exec(`
        CREATE TABLE IF NOT EXISTS t_Users (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT NOT NULL,
            email TEXT NOT NULL UNIQUE,
            password TEXT NOT NULL,
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        );
    `)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("create table: %w", err)
	}

	return conn, nil
}
