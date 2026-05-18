// Package goduct is the small runtime library used by code that goduct
// generates and by hand-written handlers that want to participate in
// goduct's wire format. It has no codegen-time dependencies.
package goduct

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
)

// Error is the wire format for an HTTP error response.
// Handlers can return one directly; the generated adapter will serialize it.
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// Convenience constructors. Add sparingly — these become part of the public API.
func BadRequest(msg string) *Error   { return &Error{Status: 400, Code: "bad_request", Message: msg} }
func Unauthorized(msg string) *Error { return &Error{Status: 401, Code: "unauthorized", Message: msg} }
func Forbidden(msg string) *Error    { return &Error{Status: 403, Code: "forbidden", Message: msg} }
func NotFound(msg string) *Error     { return &Error{Status: 404, Code: "not_found", Message: msg} }
func Conflict(msg string) *Error     { return &Error{Status: 409, Code: "conflict", Message: msg} }

// Internal wraps an unexpected error. The original is logged server-side but
// not exposed on the wire — clients see a generic "internal error" message.
func Internal(err error) *Error {
	if err != nil {
		log.Printf("goduct: internal error: %v", err)
	}
	return &Error{Status: 500, Code: "internal", Message: "internal error"}
}

// WriteError serializes err to w. If err is (or wraps) *Error, its status and
// fields are used; otherwise it is treated as Internal.
func WriteError(w http.ResponseWriter, err error) {
	var e *Error
	if !errors.As(err, &e) {
		e = Internal(err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(e)
}

// WriteJSON is a convenience for Mode B (raw) handlers that want to use the
// same response format as generated adapter code.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}
