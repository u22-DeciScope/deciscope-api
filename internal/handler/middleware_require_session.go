package handler

import (
	"net/http"

	"deciscope-core-api/internal/domain"
	"deciscope-core-api/internal/handler/presenter"
)

func (h AuthHandler) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionCookie, err := r.Cookie("sid")
		if err != nil || sessionCookie.Value == "" {
			presenter.WriteAppError(w, domain.Unauthorized("missing_sid"))
			return
		}

		if !domain.IsUUID(sessionCookie.Value) {
			presenter.WriteAppError(w, domain.Unauthorized("invalid_session"))
			return
		}

		session, found, err := h.repository.FindSessionByID(r.Context(), sessionCookie.Value)
		if err != nil {
			presenter.WriteAppError(w, domain.Internal("internal_error"))
			return
		}
		if !found {
			presenter.WriteAppError(w, domain.Unauthorized("invalid_session"))
			return
		}
		if session.RevokedAt != nil {
			presenter.WriteAppError(w, domain.Unauthorized("revoked_session"))
			return
		}

		now := h.clock.Now()
		if session.IsExpired(now) {
			_ = h.repository.RevokeSession(r.Context(), session.ID, now, "expired")
			presenter.WriteAppError(w, domain.Unauthorized("expired_session"))
			return
		}

		user, found, err := h.repository.FindUserByID(r.Context(), session.UserID)
		if err != nil {
			presenter.WriteAppError(w, domain.Internal("internal_error"))
			return
		}
		if !found {
			presenter.WriteAppError(w, domain.Unauthorized("invalid_session"))
			return
		}

		if appErr := domain.EnsureUserCanUse(user); appErr != nil {
			presenter.WriteAppError(w, appErr)
			return
		}

		if session.ShouldTouch(now) {
			_ = h.repository.TouchSession(r.Context(), session.ID, now)
			session.LastSeenAt = now
		}

		next.ServeHTTP(w, r.WithContext(withAuthContext(r.Context(), domain.AuthContext{
			User:    user,
			Session: session,
		})))
	})
}
