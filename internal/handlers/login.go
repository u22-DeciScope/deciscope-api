// login.go
// ログイン API (/login) を実装するハンドラ。
// email でユーザーを検索し、bcrypt でパスワードを照合する。
// 成功時には {"status":"ok"} を返す。
// JWT などの認証トークンは後で追加可能。

package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"deciscope-core-api/internal/db"

	"golang.org/x/crypto/bcrypt"
)

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if req.Email == "" || req.Password == "" {
		http.Error(w, "missing fields", http.StatusBadRequest)
		return
	}

	// DB からユーザー取得
	var storedHash string
	err := db.Conn.QueryRow(`
        SELECT password FROM t_Users WHERE email = ?
    `, req.Email).Scan(&storedHash)

	if err == sql.ErrNoRows {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	// パスワード照合
	if err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(req.Password)); err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	// 成功レスポンス
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}
