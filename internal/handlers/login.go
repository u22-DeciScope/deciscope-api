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
	"deciscope-core-api/internal/firebase" // ⭕️ ここを追加！
)

type LoginRequest struct {
	IDToken string `json:"idToken"` // フロントから送られてくるFirebaseのIDトークン
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

	// 1. Firebase ID トークンの検証
	// ※ internal/firebase/client.go 等で初期化した auth.Client (例: firebase.AuthClient) を使います
	ctx := context.Background()
	token, err := firebase.AuthClient.VerifyIDToken(ctx, req.IDToken)
	if err != nil {
		// トークンが無効、または期限切れ
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	// トークンからメールアドレスを取得
	email, ok := token.Claims["email"].(string)
	if !ok || email == "" {
		http.Error(w, "email not found in token", http.StatusUnauthorized)
		return
	}

	// 2. DB からユーザー取得（既存のロジックを活用）
	// パスワードの照合(bcrypt)は不要になったので、ユーザーが存在するかどうかだけチェックします
	var userID int // 必要に応じて、idやemailをスキャンする
	err = db.Conn.QueryRow(`
		SELECT id FROM t_Users WHERE email = ?
	`, email).Scan(&userID)

	if err == sql.ErrNoRows {
		// 【ヒント】もし「Googleログインした時点で自動会員登録」にしたい場合は、
		// ここでエラーにせず、INSERT文を発行してユーザーを新規作成するとフロントがとても楽になります！
		http.Error(w, "user not registered in database", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	// 成功レスポンス
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}
