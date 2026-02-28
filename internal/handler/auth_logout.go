package handler

import (
	"net/http"

	"deciscope-core-api/internal/domain"
	"deciscope-core-api/internal/handler/presenter"
)

func (h AuthHandler) handleAuthLogout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionCookie, err := r.Cookie("sid")
		if err != nil || !domain.IsUUID(sessionCookie.Value) {
			clearAuthCookies(w)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		csrfCookie, err := r.Cookie("csrf")
		if err != nil || csrfCookie.Value == "" || csrfCookie.Value != r.Header.Get("X-CSRF-Token") {
			presenter.WriteAppError(w, domain.Forbidden("csrf"))
			return
		}

		_ = h.logout.Execute(r.Context(), sessionCookie.Value, h.clock.Now())
		clearAuthCookies(w)
		w.WriteHeader(http.StatusNoContent)
	}
}
