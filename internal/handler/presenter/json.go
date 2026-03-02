package presenter

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"deciscope-core-api/internal/domain"
)

func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func WriteAppError(w http.ResponseWriter, appErr *domain.AppError) {
	WriteJSON(w, appErr.HTTPStatus, map[string]any{
		"error": map[string]string{
			"code":    appErr.Code,
			"message": appErr.Message,
		},
	})
}

func WriteError(w http.ResponseWriter, err error) {
	var appErr *domain.AppError
	if errors.As(err, &appErr) {
		WriteAppError(w, appErr)
		return
	}

	WriteAppError(w, domain.Internal("internal_error"))
}

func User(user domain.User) map[string]any {
	return map[string]any{
		"id":      user.ID,
		"status":  user.NormalizedStatus(),
		"can_use": user.CanUse(),
	}
}

func Sessions(items []domain.Session) []map[string]any {
	response := make([]map[string]any, 0, len(items))
	for _, item := range items {
		response = append(response, map[string]any{
			"id":           item.ID,
			"device_type":  item.DeviceType,
			"device_name":  item.DeviceName,
			"login_method": item.LoginMethod,
			"last_seen_at": item.LastSeenAt.Format(time.RFC3339),
		})
	}

	return response
}
