package rest_test

import (
	"encoding/json"
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
// replacing the "dev" sentinel the generated spec ships with. This is the
// runtime substitute for the release workflow's old proto-sed step (#134).
func TestDocsHandler_StampsBuildVersionOntoSpec(t *testing.T) {
	h := rest.DocsHandler("v2.5.0")

	for _, spec := range []string{"/milpacs.swagger.json", "/tickets.swagger.json"} {
		t.Run(spec, func(t *testing.T) {
			rr := docsGet(t, h, spec)
			require.Equal(t, http.StatusOK, rr.Code)
			assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

			var doc struct {
				Info struct {
					Version string `json:"version"`
				} `json:"info"`
			}
			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &doc))
			assert.Equal(t, "v2.5.0", doc.Info.Version,
				"the served spec must report the build-time version, not the dev sentinel")
			assert.NotContains(t, rr.Body.String(), `"version": "dev"`,
				"the dev sentinel must be gone once a real version is stamped")
		})
	}
}

// A dev build serves the spec untouched: info.version stays "dev" (local
// `go run` and any docker build without --build-arg VERSION report dev).
func TestDocsHandler_DevBuildLeavesSentinel(t *testing.T) {
	h := rest.DocsHandler("dev")
	rr := docsGet(t, h, "/milpacs.swagger.json")
	require.Equal(t, http.StatusOK, rr.Code)

	var doc struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &doc))
	assert.Equal(t, "dev", doc.Info.Version)
}

// The Swagger UI shell and its static assets serve verbatim from the embedded
// filesystem at the same URLs the old gateway used (everything outside /api).
func TestDocsHandler_ServesSwaggerUIShell(t *testing.T) {
	h := rest.DocsHandler("dev")

	rr := docsGet(t, h, "/")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.True(t, strings.Contains(rr.Body.String(), "swagger") ||
		strings.Contains(rr.Body.String(), "Swagger"),
		"the index must be the Swagger UI shell")
}
