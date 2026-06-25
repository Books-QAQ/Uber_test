package auth

import "errors"

var ErrUserNotFound = errors.New("user not found")
var ErrInvalidCredentials = errors.New("invalid credentials")
var ErrDuplicatePhone = errors.New("phone already registered")
var ErrDuplicatePlateNo = errors.New("plate number already registered")
var ErrUnauthorized = errors.New("unauthorized")
var ErrForbidden = errors.New("forbidden")
