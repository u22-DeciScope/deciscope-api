package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"deciscope-core-api/internal/handler/presenter"
	"deciscope-core-api/internal/usecase"
)

func (h AuthHandler) handleAuthExchange() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request exchangeRequest
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&request)
		}

		output, err := h.exchangeSession.Execute(r.Context(), usecase.ExchangeSessionInput{
			IDToken:    bearerToken(r.Header.Get("Authorization")),
			DeviceType: request.DeviceType,
			DeviceName: request.DeviceName,
		})
		if err != nil {
			presenter.WriteError(w, err)
			return
		}

		csrf := newCSRFToken()
		setSessionCookie(w, output.Session.ID)
		setCSRFCookie(w, csrf)

		presenter.WriteJSON(w, http.StatusOK, map[string]any{
			"user": presenter.User(output.User),
		})
	}
}

func bearerToken(header string) string {
	value := strings.TrimSpace(header)
	if value == "" {
		return ""
	}

	parts := strings.SplitN(value, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}

	return strings.TrimSpace(parts[1])
}

func newCSRFToken() string {
	var bytes [16]byte
	_, _ = rand.Read(bytes[:])
	return hex.EncodeToString(bytes[:])
}
