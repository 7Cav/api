package rest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The error writer is the single choke point for every non-401 error the new
// stack emits: the gRPC-status JSON shape ({"code":N,"message":...,
// "details":[]}) with the frozen code→HTTP mapping. The plain-text 401 tier
// never passes through it — the auth middleware writes those directly.

func TestWriteError_GRPCStatusJSONShape(t *testing.T) {
	rr := httptest.NewRecorder()
	writeError(rr, codePermissionDenied, "scope required: %s", "read")

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
	writeJSON(rr, map[string]string{"hello": "world"})

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"hello":"world"}`, rr.Body.String())
}

func TestWriteJSON_MarshalFailureBecomesInternalError(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSON(rr, func() {}) // func values cannot marshal

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, float64(13), body["code"])
}
