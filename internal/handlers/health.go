// health.go
// API サーバーの動作確認用エンドポイント (/health) を提供する。
// ブラウザから GET するだけで "ok" を返し、サーバーが正常に稼働していることを確認できる。
package handlers

import (
	"net/http"
)

func Health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}
