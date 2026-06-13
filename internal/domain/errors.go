package domain

import "errors"

var ErrNotFound = errors.New("not found")
var ErrForbidden = errors.New("forbidden")
var ErrInvalidArgument = errors.New("invalid argument")
var ErrConflict = errors.New("conflict")
