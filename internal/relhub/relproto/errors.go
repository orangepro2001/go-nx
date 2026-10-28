// Package relproto is the contract shared by the release-publish server, its
// Go SDK and its CLI: manifest and signature format, request/response types,
// identifier rules and the error model.
package relproto

import (
	"errors"
	"fmt"
	"net/http"
)

// Error codes. They travel as {"error":{"code":...}} over REST and in
// error.data.code over JSON-RPC.
const (
	CodeInvalid      = "invalid_argument"
	CodeNotFound     = "not_found"
	CodeUnauthorized = "unauthorized"
	CodeForbidden    = "forbidden"
	CodeConflict     = "conflict"
	CodeTooLarge     = "too_large"
	CodeUnavailable  = "unavailable"
	CodeInternal     = "internal"
)

// Error is the one error type that crosses the API boundary.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Errorf builds an *Error.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// AsError returns err as an *Error; anything that is not one becomes "internal".
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: CodeInternal, Message: err.Error()}
}

// HTTPStatus maps an error code to the HTTP status used by the REST API.
func HTTPStatus(code string) int {
	switch code {
	case CodeInvalid:
		return http.StatusBadRequest
	case CodeNotFound:
		return http.StatusNotFound
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeConflict:
		return http.StatusConflict
	case CodeTooLarge:
		return http.StatusRequestEntityTooLarge
	case CodeUnavailable:
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}

// CodeFromStatus is the inverse of HTTPStatus for responses that carry no
// error body (proxies, 404 from a wrong base URL, ...).
func CodeFromStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return CodeInvalid
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusUnauthorized:
		return CodeUnauthorized
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusConflict:
		return CodeConflict
	case http.StatusRequestEntityTooLarge:
		return CodeTooLarge
	case http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
		return CodeUnavailable
	}
	return CodeInternal
}

// ErrorBody is the REST error envelope.
type ErrorBody struct {
	Error *Error `json:"error"`
}
