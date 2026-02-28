package domain

import (
	"errors"
	"net/http"
)

var ErrIdentityConflict = errors.New("identity_conflict")

type AppError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"-"`
}

func (e *AppError) Error() string {
	return e.Code
}

func Unauthorized(code string) *AppError {
	return &AppError{
		Code:       code,
		Message:    "authentication failed",
		HTTPStatus: http.StatusUnauthorized,
	}
}

func Forbidden(code string) *AppError {
	return &AppError{
		Code:       code,
		Message:    "request is not allowed",
		HTTPStatus: http.StatusForbidden,
	}
}

func Internal(code string) *AppError {
	return &AppError{
		Code:       code,
		Message:    "internal server error",
		HTTPStatus: http.StatusInternalServerError,
	}
}
