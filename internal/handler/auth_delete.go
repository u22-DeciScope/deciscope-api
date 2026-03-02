package handler

import (
	"net/http"

	"deciscope-core-api/internal/domain"
	"deciscope-core-api/internal/handler/presenter"
)

func (h AuthHandler) handleAuthDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authContext, ok := currentAuthContext(r.Context())
		if !ok {
			presenter.WriteAppError(w, domain.Internal("internal_error"))
			return
		}

		if err := h.deleteAccount.Execute(r.Context(), authContext.User.ID, h.clock.Now()); err != nil {
			presenter.WriteError(w, err)
			return
		}

		clearAuthCookies(w)
		w.WriteHeader(http.StatusNoContent)
	}
}
