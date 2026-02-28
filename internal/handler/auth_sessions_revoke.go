package handler

import (
	"net/http"

	"deciscope-core-api/internal/domain"
	"deciscope-core-api/internal/handler/presenter"
	"github.com/go-chi/chi/v5"
)

func (h AuthHandler) handleAuthSessionsRevoke() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authContext, ok := currentAuthContext(r.Context())
		if !ok {
			presenter.WriteAppError(w, domain.Internal("internal_error"))
			return
		}

		targetID := chi.URLParam(r, "id")
		if !domain.IsUUID(targetID) {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if err := h.revokeSession.Execute(r.Context(), authContext.User.ID, targetID, h.clock.Now()); err != nil {
			presenter.WriteError(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
