package handler

import (
	"net/http"

	"deciscope-core-api/internal/domain"
	"deciscope-core-api/internal/handler/presenter"
)

func (h AuthHandler) handleAuthLogout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authContext, ok := currentAuthContext(r.Context())
		if !ok {
			presenter.WriteAppError(w, domain.Internal("internal_error"))
			return
		}

		_ = h.logout.Execute(r.Context(), authContext.Session.ID, h.clock.Now())
		clearAuthCookies(w)
		w.WriteHeader(http.StatusNoContent)
	}
}
