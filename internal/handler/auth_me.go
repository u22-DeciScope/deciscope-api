package handler

import (
	"net/http"

	"deciscope-core-api/internal/domain"
	"deciscope-core-api/internal/handler/presenter"
)

func (h AuthHandler) handleAuthMe() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authContext, ok := currentAuthContext(r.Context())
		if !ok {
			presenter.WriteAppError(w, domain.Internal("internal_error"))
			return
		}

		output, err := h.getMe.Execute(r.Context(), authContext)
		if err != nil {
			presenter.WriteError(w, err)
			return
		}

		presenter.WriteJSON(w, http.StatusOK, map[string]any{
			"user": presenter.User(output.User),
			"session": map[string]string{
				"id": output.Session.ID,
			},
		})
	}
}
