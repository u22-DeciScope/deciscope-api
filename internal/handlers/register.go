// register.go
// 会員登録 API (/register) を実装するハンドラ。
// JSON で送られた name / email / password を受け取り、
// パスワードを bcrypt でハッシュ化し、SQLite にユーザー情報を保存する。
// 成功時には {"status":"ok"} を返す。
// 会員登録 API (/register)。
// 受け取ったパスワードを bcrypt でハッシュ化し、
// 平文を保存しないことでセキュリティを確保する。
package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"deciscope-core-api/internal/db"

	"golang.org/x/crypto/bcrypt"
)

type RegisterRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func Register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	// 入力チェック（最低限）
	if req.Name == "" || req.Email == "" || req.Password == "" {
		http.Error(w, "missing fields", http.StatusBadRequest)
		return
	}

	// パスワードをハッシュ化
	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "failed to hash password", http.StatusInternalServerError)
		return
	}

	// SQLite に INSERT
	_, err = db.Conn.Exec(`
		INSERT INTO t_Users (name, email, password)
		VALUES (?, ?, ?)
	`, req.Name, req.Email, string(hashed))

	if err != nil {
		// UNIQUE 制約（email 重複）
		if err.Error() == "UNIQUE constraint failed: t_Users.email" {
			http.Error(w, "email already exists", http.StatusConflict)
			return
		}

		log.Println("insert error:", err)
		http.Error(w, "failed to register", http.StatusInternalServerError)
		return
	}

	// 成功レスポンス
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}
