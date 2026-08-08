package errors

import (
	"errors"
	"net/http"
)

// HTTPStatus maps an application error onto the status the client should see.
// Anything unrecognised is a 500: an unmapped error is a bug here, not a
// reason to leak a more specific status.
func HTTPStatus(err error) int {
	var appErr *AppError
	if !errors.As(err, &appErr) {
		return http.StatusInternalServerError
	}

	switch appErr.Code {
	case CodeNotFound:
		return http.StatusNotFound
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeBadRequest, CodeValidation:
		return http.StatusBadRequest
	case CodeConflict:
		return http.StatusConflict
	case CodeRateLimited:
		return http.StatusTooManyRequests
	case CodeUnavailable:
		return http.StatusServiceUnavailable
	case CodeNotImplemented:
		return http.StatusNotImplemented
	default:
		return http.StatusInternalServerError
	}
}

// PublicMessage is the text safe to return to a caller. Internal failures wrap
// database errors and similar detail, so those are replaced with the fallback
// rather than echoed back.
func PublicMessage(err error, fallback string) string {
	var appErr *AppError
	if !errors.As(err, &appErr) {
		return fallback
	}
	if HTTPStatus(err) >= http.StatusInternalServerError {
		return fallback
	}
	return appErr.Message
}
