package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	appauth "deciscope-core-api/internal/auth"
	"deciscope-core-api/internal/users"
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

func (api *AuthAPI) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.Email == "" || req.Password == "" {
		http.Error(w, "missing fields", http.StatusBadRequest)
		return
	}

	err := api.service.Register(r.Context(), req.Name, req.Email, req.Password)
	if errors.Is(err, users.ErrEmailExists) {
		http.Error(w, "email already exists", http.StatusConflict)
		return
	}
	if errors.Is(err, appauth.ErrUnavailable) {
		http.Error(w, "user store is unavailable in this local runtime", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		http.Error(w, "failed to register", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
