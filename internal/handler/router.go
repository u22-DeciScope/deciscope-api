package handler

import (
	"net/http"

	"deciscope-core-api/internal/contract"
	"deciscope-core-api/internal/handler/presenter"
	"deciscope-core-api/internal/usecase"
	"github.com/go-chi/chi/v5"
)

type AuthHandler struct {
	exchangeSession usecase.ExchangeSession
	getMe           usecase.GetMe
	logout          usecase.Logout
	logoutAll       usecase.LogoutAll
	listSessions    usecase.ListSessions
	revokeSession   usecase.RevokeSession
	repository      contract.AuthRepository
	clock           contract.Clock
	allowedOrigins  map[string]struct{}
}

func NewAuthHandler(
	exchangeSession usecase.ExchangeSession,
	getMe usecase.GetMe,
	logout usecase.Logout,
	logoutAll usecase.LogoutAll,
	listSessions usecase.ListSessions,
	revokeSession usecase.RevokeSession,
	repository contract.AuthRepository,
	clock contract.Clock,
	allowedOrigins map[string]struct{},
) AuthHandler {
	return AuthHandler{
		exchangeSession: exchangeSession,
		getMe:           getMe,
		logout:          logout,
		logoutAll:       logoutAll,
		listSessions:    listSessions,
		revokeSession:   revokeSession,
		repository:      repository,
		clock:           clock,
		allowedOrigins:  allowedOrigins,
	}
}

func (h AuthHandler) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(h.corsAllowedOrigin)
	r.Get("/", h.handleIndex())

	r.Route("/api/auth", func(r chi.Router) {
		r.Post("/exchange", h.handleAuthExchange())
		r.With(h.requireSession, h.csrfProtected).Post("/logout", h.handleAuthLogout())

		r.With(h.requireSession).Get("/me", h.handleAuthMe())
		r.With(h.requireSession).Get("/sessions", h.handleAuthSessionsList())
		r.With(h.requireSession, h.csrfProtected).Post("/logout_all", h.handleAuthLogoutAll())
		r.With(h.requireSession, h.csrfProtected).Post("/sessions/{id}/revoke", h.handleAuthSessionsRevoke())
	})

	return r
}

func (h AuthHandler) handleIndex() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		presenter.WriteJSON(w, http.StatusOK, map[string]string{
			"message": "deciscope-core-api",
		})
	}
}
