package rest_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/7cav/api/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func docsGet(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	return rr
}

// A tagged build stamps its version onto the served spec's info.version,
// replacing the "dev" sentinel the hand-owned spec ships with. This is the
// runtime substitute for the release workflow's old proto-sed step (#134);
// Phase 4 (#135) retired the generated Swagger 2.0 files and points the docs
// surface at the hand-owned OpenAPI 3.1 document (openapi/openapi.yaml).
func TestDocsHandler_StampsBuildVersionOntoSpec(t *testing.T) {
	h := rest.DocsHandler("v2.5.0")

	rr := docsGet(t, h, "/openapi.yaml")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Header().Get("Content-Type"), "yaml")

	body := rr.Body.String()
	assert.Contains(t, body, "version: v2.5.0",
		"the served spec must report the build-time version, not the dev sentinel")
	assert.NotContains(t, body, "version: dev",
		"the dev sentinel must be gone once a real version is stamped")
}

// A dev build serves the spec untouched: info.version stays "dev" (local
// `go run` and any docker build without --build-arg VERSION report dev).
func TestDocsHandler_DevBuildLeavesSentinel(t *testing.T) {
	h := rest.DocsHandler("dev")
	rr := docsGet(t, h, "/openapi.yaml")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "version: dev")
}

// The served document is the OpenAPI 3.1 spec, not the retired Swagger 2.0
// generated file. Pin the 3.1 marker so a regression to the 2.0 surface fails.
func TestDocsHandler_ServesOpenAPI31(t *testing.T) {
	h := rest.DocsHandler("dev")
	rr := docsGet(t, h, "/openapi.yaml")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "openapi: 3.1",
		"the docs surface must serve the hand-owned OpenAPI 3.1 document")
}

// The Swagger UI shell and its static assets serve verbatim from the embedded
// filesystem at the same URLs the old gateway used (everything outside /api),
// and the shell loads the 3.1 spec.
func TestDocsHandler_ServesSwaggerUIShell(t *testing.T) {
	h := rest.DocsHandler("dev")

	rr := docsGet(t, h, "/")
	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	assert.True(t, strings.Contains(body, "swagger") || strings.Contains(body, "Swagger"),
		"the index must be the Swagger UI shell")
	assert.Contains(t, body, "openapi.yaml",
		"the UI shell must load the hand-owned 3.1 spec")
}

// The retired Swagger 2.0 generated files are gone — requesting them 404s
// rather than serving a stale generated artifact.
func TestDocsHandler_RetiredSwagger2FilesAreGone(t *testing.T) {
	h := rest.DocsHandler("dev")
	for _, p := range []string{"/milpacs.swagger.json", "/tickets.swagger.json"} {
		t.Run(p, func(t *testing.T) {
			rr := docsGet(t, h, p)
			assert.Equal(t, http.StatusNotFound, rr.Code,
				"the retired generated Swagger 2.0 file must no longer be served")
		})
	}
}
