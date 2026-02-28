package handler

import (
	"net/http"

	"deciscope-core-api/internal/domain"
)

func setSessionCookie(w http.ResponseWriter, sessionID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "sid",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false,
		MaxAge:   int(domain.SessionTTL.Seconds()),
	})
}

func setCSRFCookie(w http.ResponseWriter, csrf string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "csrf",
		Value:    csrf,
		Path:     "/",
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		Secure:   false,
		MaxAge:   int(domain.SessionTTL.Seconds()),
	})
}

func clearAuthCookies(w http.ResponseWriter) {
	for _, name := range []string{"sid", "csrf"} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: name == "sid",
			SameSite: http.SameSiteLaxMode,
			Secure:   false,
			MaxAge:   -1,
		})
	}
}
