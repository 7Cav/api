package rest

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// code is a gRPC status code number — the error contract of the public API
// is the gRPC-status JSON shape with these numeric codes, frozen by the
// golden corpus. Defined here (not imported from grpc-go) so the new stack
// carries no gRPC dependency into Phase 4.
type code int

const (
	codeOK                 code = 0
	codeCanceled           code = 1
	codeUnknown            code = 2
	codeInvalidArgument    code = 3
	codeDeadlineExceeded   code = 4
	codeNotFound           code = 5
	codeAlreadyExists      code = 6
	codePermissionDenied   code = 7
	codeResourceExhausted  code = 8
	codeFailedPrecondition code = 9
	codeAborted            code = 10
	codeOutOfRange         code = 11
	codeUnimplemented      code = 12
	codeInternal           code = 13
	codeUnavailable        code = 14
	codeDataLoss           code = 15
	codeUnauthenticated    code = 16
)

// httpStatus maps a gRPC code to its HTTP status — the table frozen from
// grpc-gateway's runtime.HTTPStatusFromCode (the mapping the old stack
// served). Unknown codes fail closed as 500.
func (c code) httpStatus() int {
	switch c {
	case codeOK:
		return http.StatusOK
	case codeCanceled:
		return 499 // client closed request (nginx convention, no stdlib name)
	case codeUnknown:
		return http.StatusInternalServerError
	case codeInvalidArgument:
		return http.StatusBadRequest
	case codeDeadlineExceeded:
		return http.StatusGatewayTimeout
	case codeNotFound:
		return http.StatusNotFound
	case codeAlreadyExists:
		return http.StatusConflict
	case codePermissionDenied:
		return http.StatusForbidden
	case codeResourceExhausted:
		return http.StatusTooManyRequests
	case codeFailedPrecondition:
		return http.StatusBadRequest
	case codeAborted:
		return http.StatusConflict
	case codeOutOfRange:
		return http.StatusBadRequest
	case codeUnimplemented:
		return http.StatusNotImplemented
	case codeInternal:
		return http.StatusInternalServerError
	case codeUnavailable:
		return http.StatusServiceUnavailable
	case codeDataLoss:
		return http.StatusInternalServerError
	case codeUnauthenticated:
		return http.StatusUnauthorized
	default:
		return http.StatusInternalServerError
	}
}

// statusBody is the gRPC-status JSON error shape, frozen by the golden
// corpus: {"code":N,"message":"...","details":[]}. Details has never been
// observed populated; it stays an always-present empty array
// (emit-everything).
type statusBody struct {
	Code    code   `json:"code"`
	Message string `json:"message"`
	Details []any  `json:"details"`
}

// writeError is the single error choke point of the new stack: every non-401
// error response is written here — one place to keep the wire shape, the
// code→HTTP mapping, and (extension point, #132) the 5xx Sentry reports.
// The plain-text 401 tier deliberately bypasses it: the auth middleware
// writes those itself (two-tier behavior golden-pinned by #106).
//
// Handler-specific message strings are frozen behavior (including the ones
// that leak wrapped error text) — callers format them verbatim.
func writeError(w http.ResponseWriter, c code, format string, args ...any) {
	body, err := json.Marshal(statusBody{
		Code:    c,
		Message: fmt.Sprintf(format, args...),
		Details: []any{},
	})
	if err != nil {
		// statusBody cannot fail to marshal; guard against future edits.
		http.Error(w, `{"code":13,"message":"failed to encode error","details":[]}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(c.httpStatus())
	if _, err := w.Write(body); err != nil {
		Error.Printf("writing error response: %v", err)
	}
}

// notFound is the JSON 404 for unknown paths under the API prefix — the
// gateway-mux body the corpus pinned, not the stdlib text 404.
func notFound(w http.ResponseWriter, _ *http.Request) {
	writeError(w, codeNotFound, "Not Found")
}

// writeJSON writes a 200 application/json response. It marshals BEFORE
// touching the ResponseWriter so an encoding failure can still surface as a
// clean Internal error through the choke point instead of a truncated 200.
func writeJSON(w http.ResponseWriter, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		Error.Printf("encoding response: %v", err)
		writeError(w, codeInternal, "failed to encode response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(body); err != nil {
		Error.Printf("writing response: %v", err)
	}
}
