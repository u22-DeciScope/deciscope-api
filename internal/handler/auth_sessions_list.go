package handler

import (
	"net/http"

	"deciscope-core-api/internal/domain"
	"deciscope-core-api/internal/handler/presenter"
)

func (h AuthHandler) handleAuthSessionsList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authContext, ok := currentAuthContext(r.Context())
		if !ok {
			presenter.WriteAppError(w, domain.Internal("internal_error"))
			return
		}

		sessions, err := h.listSessions.Execute(r.Context(), authContext.User.ID)
		if err != nil {
			presenter.WriteError(w, err)
			return
		}

		presenter.WriteJSON(w, http.StatusOK, map[string]any{
			"sessions": presenter.Sessions(sessions),
		})
	}
}
