package handler

import (
	"net/http"

	"deciscope-core-api/internal/domain"
	"deciscope-core-api/internal/handler/presenter"
)

func (h AuthHandler) csrfProtected(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.allowedOrigins != nil {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if _, ok := h.allowedOrigins[origin]; !ok {
					presenter.WriteAppError(w, domain.Forbidden("csrf"))
					return
				}
			}
		}

		csrfCookie, err := r.Cookie("csrf")
		if err != nil || csrfCookie.Value == "" {
			presenter.WriteAppError(w, domain.Forbidden("csrf"))
			return
		}

		if csrfCookie.Value != r.Header.Get("X-CSRF-Token") {
			presenter.WriteAppError(w, domain.Forbidden("csrf"))
			return
		}

		next.ServeHTTP(w, r)
	})
}
