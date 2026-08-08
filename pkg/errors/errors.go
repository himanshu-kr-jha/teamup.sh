package errors

import (
	"errors"
	"fmt"
)

// Error codes
const (
	CodeNotFound       = "NOT_FOUND"
	CodeUnauthorized   = "UNAUTHORIZED"
	CodeForbidden      = "FORBIDDEN"
	CodeBadRequest     = "BAD_REQUEST"
	CodeConflict       = "CONFLICT"
	CodeInternal       = "INTERNAL"
	CodeUnavailable    = "UNAVAILABLE"
	CodeValidation     = "VALIDATION"
	CodeRateLimited    = "RATE_LIMITED"
	CodeNotImplemented = "NOT_IMPLEMENTED"
)

var (
	ErrNotFound       = New(CodeNotFound, "resource not found")
	ErrUnauthorized   = New(CodeUnauthorized, "unauthorized")
	ErrForbidden      = New(CodeForbidden, "forbidden")
	ErrBadRequest     = New(CodeBadRequest, "bad request")
	ErrConflict       = New(CodeConflict, "resource already exists")
	ErrInternal       = New(CodeInternal, "internal server error")
	ErrUnavailable    = New(CodeUnavailable, "service unavailable")
	ErrValidation     = New(CodeValidation, "validation failed")
	ErrRateLimited    = New(CodeRateLimited, "rate limited")
	ErrNotImplemented = New(CodeNotImplemented, "not implemented")
)

type AppError struct {
	Code    string
	Message string
	Details map[string]any
	Cause   error
}

func (e *AppError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s (caused by: %v)", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error {
	return e.Cause
}

func New(code, message string) *AppError {
	return &AppError{Code: code, Message: message}
}

func Wrap(err error, code, message string) *AppError {
	return &AppError{Code: code, Message: message, Cause: err}
}

func IsCode(err error, code string) bool {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Code == code
	}
	return false
}

func GetCode(err error) string {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Code
	}
	return CodeInternal
}

func GetMessage(err error) string {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Message
	}
	return err.Error()
}

func GetDetails(err error) map[string]any {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Details
	}
	return nil
}
