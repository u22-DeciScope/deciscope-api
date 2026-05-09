// register_form.go
// ブラウザから直接 /register に POST を送るための簡易 HTML フォームを返す。
// フロントエンドを触らずに、会員登録 API の疎通確認を行うための補助エンドポイント。

package handlers

import "net/http"

func RegisterForm(w http.ResponseWriter, r *http.Request) {
	html := `
    <form action="/register" method="POST">
        <input name="name" placeholder="name"><br>
        <input name="email" placeholder="email"><br>
        <input name="password" placeholder="password" type="password"><br>
        <button type="submit">Register</button>
    </form>
    `
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}
