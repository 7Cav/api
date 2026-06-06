package rest

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureErrorLog redirects the package Error logger into a buffer for one
// test and restores the previous writer afterwards.
func captureErrorLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := Error.Writer()
	Error.SetOutput(&buf)
	t.Cleanup(func() { Error.SetOutput(prev) })
	return &buf
}

// The error writer is the single choke point for every non-401 error the new
// stack emits: the gRPC-status JSON shape ({"code":N,"message":...,
// "details":[]}) with the frozen code→HTTP mapping. The plain-text 401 tier
// never passes through it — the auth middleware writes those directly.

func TestWriteError_GRPCStatusJSONShape(t *testing.T) {
	rr := httptest.NewRecorder()
	writeError(rr, httptest.NewRequest(http.MethodGet, "/api/v1/x", nil), codePermissionDenied, "scope required: %s", "read")

	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var body map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, map[string]any{
		"code":    float64(7),
		"message": "scope required: read",
		"details": []any{}, // always present, always an array — emit-everything
	}, body)
}

// The complete code→HTTP table, frozen from grpc-gateway's
// runtime.HTTPStatusFromCode so no fan-out slice ever re-decides a mapping.
func TestCodeHTTPStatusMapping(t *testing.T) {
	cases := []struct {
		c    code
		want int
	}{
		{codeOK, http.StatusOK},
		{codeCanceled, 499}, // client closed request (nginx convention)
		{codeUnknown, http.StatusInternalServerError},
		{codeInvalidArgument, http.StatusBadRequest},
		{codeDeadlineExceeded, http.StatusGatewayTimeout},
		{codeNotFound, http.StatusNotFound},
		{codeAlreadyExists, http.StatusConflict},
		{codePermissionDenied, http.StatusForbidden},
		{codeResourceExhausted, http.StatusTooManyRequests},
		{codeFailedPrecondition, http.StatusBadRequest},
		{codeAborted, http.StatusConflict},
		{codeOutOfRange, http.StatusBadRequest},
		{codeUnimplemented, http.StatusNotImplemented},
		{codeInternal, http.StatusInternalServerError},
		{codeUnavailable, http.StatusServiceUnavailable},
		{codeDataLoss, http.StatusInternalServerError},
		{codeUnauthenticated, http.StatusUnauthorized},
		{code(42), http.StatusInternalServerError}, // unknown code: fail closed as a 500
	}
	for _, c := range cases {
		assert.Equal(t, c.want, c.c.httpStatus(), "code %d", c.c)
	}
}

// Unknown paths under the API prefix return the gateway-mux-shaped JSON 404
// — {"code":5,"message":"Not Found","details":[]} — not the stdlib text 404.
// Golden-pinned by auth/unknown_path_authenticated.
func TestNotFoundHandler_JSON404(t *testing.T) {
	rr := httptest.NewRecorder()
	notFound(rr, httptest.NewRequest(http.MethodGet, "/api/v1/does/not/exist", nil))

	assert.Equal(t, http.StatusNotFound, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"code":5,"message":"Not Found","details":[]}`, rr.Body.String())
}

// writeJSON is the success-path twin: application/json Content-Type and the
// marshaled body. A marshal failure must surface as an Internal error through
// the choke point instead of a half-written 200.
func TestWriteJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSON(rr, httptest.NewRequest(http.MethodGet, "/api/v1/x", nil), map[string]string{"hello": "world"})

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"hello":"world"}`, rr.Body.String())
}

func TestWriteJSON_MarshalFailureBecomesInternalError(t *testing.T) {
	captureErrorLog(t)
	rr := httptest.NewRecorder()
	writeJSON(rr, httptest.NewRequest(http.MethodGet, "/api/v1/x", nil), func() {}) // func values cannot marshal

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, float64(13), body["code"])
}

// Every ≥500 response is Error-logged at the choke point with code, message,
// method, and path — the log line keeps outages visible server-side even
// without a SENTRY_DSN (local/dev), where the choke point's Sentry report
// (#132) is a no-op.
func TestWriteError_500sAreLoggedWithRequestContext(t *testing.T) {
	buf := captureErrorLog(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks", nil)
	writeError(rr, req, codeInternal, "error fetching ranks: %v", io.ErrUnexpectedEOF)

	logged := buf.String()
	assert.Contains(t, logged, "GET")
	assert.Contains(t, logged, "/api/v1/milpacs/ranks")
	assert.Contains(t, logged, "code 13")
	assert.Contains(t, logged, "error fetching ranks: unexpected EOF")
}

func TestWriteError_4xxIsNotErrorLogged(t *testing.T) {
	buf := captureErrorLog(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks", nil)
	writeError(rr, req, codePermissionDenied, "scope required: read")

	assert.Empty(t, buf.String(), "client errors must not spam the Error log")
}

// The marshal-failure fallback (statusBody cannot fail to marshal today; the
// guard protects future edits) must stay on-contract: a hand-written constant
// JSON body with application/json — NOT http.Error, which would emit
// text/plain + X-Content-Type-Options: nosniff and append a newline — and the
// failure must be logged with the original code/message and request context,
// or the original error would vanish without a trace.
func TestWriteError_MarshalFailureFallback(t *testing.T) {
	buf := captureErrorLog(t)
	prev := marshalJSON
	marshalJSON = func(any) ([]byte, error) { return nil, errors.New("marshal exploded") }
	t.Cleanup(func() { marshalJSON = prev })

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks", nil)
	writeError(rr, req, codePermissionDenied, "scope required: %s", "read")

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.Empty(t, rr.Header().Get("X-Content-Type-Options"),
		"fallback must be hand-written, not http.Error (which adds nosniff)")
	assert.Equal(t, `{"code":13,"message":"failed to encode error","details":[]}`, rr.Body.String(),
		"constant fallback body, verbatim — no trailing newline")

	logged := buf.String()
	assert.Contains(t, logged, "marshal exploded")
	assert.Contains(t, logged, "code 7", "the original code must survive into the log")
	assert.Contains(t, logged, "scope required: read", "the original message must survive into the log")
	assert.Contains(t, logged, "GET")
	assert.Contains(t, logged, "/api/v1/milpacs/ranks")
}
