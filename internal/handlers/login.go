// login.go
// ログイン API (/login) を実装するハンドラ。
// フロントから送られた Firebase の ID トークンを検証し、ユーザーを確認する。
// 成功時には {"status":"ok"} を返す。
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"

	"deciscope-core-api/internal/db"
	"deciscope-core-api/internal/firebase" // 前に解決したインポート
)

type LoginRequest struct {
	IDToken string `json:"idToken"`
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

	if req.IDToken == "" {
		http.Error(w, "missing idToken", http.StatusBadRequest)
		return
	}

	// 1. Firebaseトークンの検証
	ctx := context.Background()

	authClient, err := firebase.AuthClient()
	if err != nil {
		http.Error(w, "auth client not initialized", http.StatusInternalServerError)
		return
	}

	token, err := authClient.VerifyIDToken(ctx, req.IDToken)
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	// トークンから情報を取り出す（FirebaseがMicrosoftから取得した情報を含みます）
	email, _ := token.Claims["email"].(string)
	name, _ := token.Claims["name"].(string) // ユーザーの表示名（「山田太郎」など）

	if email == "" {
		http.Error(w, "email empty in token", http.StatusUnauthorized)
		return
	}

	// 2. DB（t_Users）にユーザーが存在するか確認
	if db.Conn == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":        "ok",
			"uid":           token.UID,
			"email":         email,
			"name":          name,
			"auth_provider": "firebase",
			"user_store":    "none",
		})
		return
	}
	var userID int
	err = db.Conn.QueryRow(`
		SELECT id FROM t_Users WHERE email = ?
	`, email).Scan(&userID)

	// 💡 【ここがポイント！】もしDBにユーザーがいなかったら...
	if err == sql.ErrNoRows {
		// 🚀 その場で新規会員登録（INSERT）を実行！
		// ※ パスワード欄(password)がある場合は、空文字か "firebase_user" などのダミーを入れておきます
		_, insertErr := db.Conn.Exec(`
			INSERT INTO t_Users (email, name, password) VALUES (?, ?, ?)
		`, email, name, "firebase_auth")

		if insertErr != nil {
			http.Error(w, "failed to register user", http.StatusInternalServerError)
			return
		}

		// 登録直後のユーザーIDを再取得
		_ = db.Conn.QueryRow(`SELECT id FROM t_Users WHERE email = ?`, email).Scan(&userID)

		// ログを出しておくとデバッグが楽になります
		println("🎉 新規ユーザーが自動登録されました:", email)
	} else if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	// 成功レスポンス（フロントにユーザー情報を返す）
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":        "ok",
		"id":            userID,
		"uid":           token.UID,
		"email":         email,
		"name":          name,
		"auth_provider": "firebase",
		"user_store":    "sqlite",
	})
}
