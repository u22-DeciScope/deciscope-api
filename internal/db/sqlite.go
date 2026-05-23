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

var Conn *sql.DB

func InitSQLite() error {
	var err error

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

	Conn, err = sql.Open("sqlite3", dsn)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	Conn.SetMaxOpenConns(1)

	// 接続確認
	if err := Conn.Ping(); err != nil {
		_ = Conn.Close()
		Conn = nil
		return fmt.Errorf("ping sqlite: %w", err)
	}

	if _, err := Conn.Exec(`PRAGMA journal_mode = WAL;`); err != nil {
		_ = Conn.Close()
		Conn = nil
		return fmt.Errorf("enable sqlite wal: %w", err)
	}

	// テーブル作成
	_, err = Conn.Exec(`
        CREATE TABLE IF NOT EXISTS t_Users (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT NOT NULL,
            email TEXT NOT NULL UNIQUE,
            password TEXT NOT NULL,
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        );
    `)
	if err != nil {
		_ = Conn.Close()
		Conn = nil
		return fmt.Errorf("create table: %w", err)
	}

	return nil
}
