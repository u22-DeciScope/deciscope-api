package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	appauth "deciscope-core-api/internal/auth"
)

type AuthAPI struct {
	service *appauth.Service
}

func NewAuthAPI(service *appauth.Service) *AuthAPI {
	return &AuthAPI{service: service}
}

func (api *AuthAPI) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDToken string `json:"idToken"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.IDToken == "" {
		http.Error(w, "missing idToken", http.StatusBadRequest)
		return
	}

	result, err := api.service.Login(r.Context(), req.IDToken)
	if errors.Is(err, appauth.ErrUnavailable) {
		http.Error(w, "auth client not initialized", http.StatusInternalServerError)
		return
	}
	if errors.Is(err, appauth.ErrInvalidToken) || errors.Is(err, appauth.ErrEmailRequired) {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	response := map[string]any{
		"status":        "ok",
		"uid":           result.UID,
		"email":         result.Email,
		"name":          result.Name,
		"auth_provider": result.AuthProvider,
	}
	if result.UserID != 0 {
		response["id"] = result.UserID
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}
