// sqlite.go
// SQLite データベースの初期化と接続管理を行う。
// Conn はアプリ全体で共有される *sql.DB。

package db

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

var Conn *sql.DB

func InitSQLite() error {
	var err error

	Conn, err = sql.Open("sqlite3", "./db.sqlite")
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}

	// 接続確認
	if err := Conn.Ping(); err != nil {
		return fmt.Errorf("ping sqlite: %w", err)
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
		return fmt.Errorf("create table: %w", err)
	}

	return nil
}
