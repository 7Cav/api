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

// marshalJSON is json.Marshal behind a package seam so the marshal-failure
// fallback below stays testable: statusBody itself cannot fail to marshal, so
// the guard would otherwise be unreachable dead weight. Swapped only by tests.
var marshalJSON = json.Marshal

// writeError is the single error choke point of the new stack: every non-401
// error response is written here — one place to keep the wire shape, the
// code→HTTP mapping, the ≥500 server-side logging, and the 5xx Sentry
// reports (#132, reportServerError). The plain-text 401 tier deliberately bypasses
// it: the auth middleware writes those itself (two-tier behavior golden-pinned
// by #106). r supplies the method/path request context for the log lines.
//
// Handler-specific message strings are frozen behavior (including the ones
// that leak wrapped error text) — callers format them verbatim.
func writeError(w http.ResponseWriter, r *http.Request, c code, format string, args ...any) {
	writeStatusJSON(w, r, c.httpStatus(), c, format, args...)
}

// writeStatusJSON is writeError with the HTTP status decoupled from the code:
// for the rare response whose status the frozen code→HTTP table cannot
// produce. Everything else must go through writeError so code and status can
// never disagree by accident.
func writeStatusJSON(w http.ResponseWriter, r *http.Request, status int, c code, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if status >= 500 {
		// The log line keeps 5xx outages visible server-side even without a
		// SENTRY_DSN (local/dev — the report below is then a no-op).
		Error.Printf("%s %s: %d (code %d): %s", r.Method, r.URL.Path, status, c, msg)
		reportServerError(r, status)
	}
	body, err := marshalJSON(statusBody{
		Code:    c,
		Message: msg,
		Details: []any{},
	})
	if err != nil {
		// statusBody cannot fail to marshal; guard against future edits.
		// Hand-written fallback, NOT http.Error: that would emit text/plain +
		// X-Content-Type-Options: nosniff and append a newline — off-contract
		// in three ways. Log the marshal failure WITH the original code and
		// message, or the real error vanishes behind the generic body.
		Error.Printf("%s %s: marshaling error body failed (original code %d, message %q): %v",
			r.Method, r.URL.Path, c, msg, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		if _, werr := w.Write([]byte(`{"code":13,"message":"failed to encode error","details":[]}`)); werr != nil {
			Error.Printf("%s %s: writing fallback error response: %v", r.Method, r.URL.Path, werr)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		Error.Printf("%s %s: writing error response: %v", r.Method, r.URL.Path, err)
	}
}

// notFound is the JSON 404 for unknown paths under the API prefix — the
// gateway-mux body the corpus pinned, not the stdlib text 404.
func notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, codeNotFound, "Not Found")
}

// methodNotAllowed answers a wrong-method request on an EXISTING route: 405
// with Allow naming the supported verbs — today always "GET, HEAD", the whole
// read surface (HEAD rides along on every GET pattern; when write endpoints
// arrive their routes advertise their own Allow). Enumerated break (PRD #112,
// ruled at #125): the old stack answered 501.
//
// Code mapping, decided deliberately: the frozen code→HTTP table has no code
// that yields 405, so the body keeps the OLD stack's wrong-method body
// verbatim — code 12 (Unimplemented), message "Method Not Allowed" — and only
// the HTTP status (501→405) and the Allow header change. A consumer matching
// on the JSON body sees no difference; writeStatusJSON carries the explicit
// status override.
func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "GET, HEAD")
	writeStatusJSON(w, r, http.StatusMethodNotAllowed, codeUnimplemented, "Method Not Allowed")
}

// writeJSON writes a 200 application/json response. It marshals BEFORE
// touching the ResponseWriter so an encoding failure can still surface as a
// clean Internal error through the choke point instead of a truncated 200.
func writeJSON(w http.ResponseWriter, r *http.Request, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		Error.Printf("%s %s: encoding response: %v", r.Method, r.URL.Path, err)
		writeError(w, r, codeInternal, "failed to encode response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(body); err != nil {
		Error.Printf("%s %s: writing response: %v", r.Method, r.URL.Path, err)
	}
}
