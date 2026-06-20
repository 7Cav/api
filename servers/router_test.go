package servers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBuildPublicRouter_SplitsApiFromDocs pins the public listener's path split
// — new hand-written routing that replaced the old grpc-gateway's implicit
// /api-vs-docs dispatch. /api* reaches the API handler; everything else (the
// docs UI, the embedded specs, and notably /metrics — which is NOT mounted on
// the public listener) reaches the docs handler. The constituent handlers are
// covered elsewhere (the golden corpus for rest.New, docs_test.go for
// DocsHandler); this guards only the composition.
func TestBuildPublicRouter_SplitsApiFromDocs(t *testing.T) {
	var hit string
	api := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = "api" })
	docs := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = "docs" })
	router := buildPublicRouter(api, docs)

	cases := []struct {
		path string
		want string
	}{
		{"/api/v1/milpacs/ranks", "api"},
		{"/api/v1/tickets", "api"},
		{"/api", "api"},
		{"/", "docs"},
		{"/index.html", "docs"},
		{"/openapi.yaml", "docs"},
		// /metrics is internal-only — never the API surface; it falls to the
		// docs file server (which 404s it), it does NOT reach MetricsHandler.
		{"/metrics", "docs"},
	}
	for _, c := range cases {
		hit = ""
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, c.path, nil))
		if hit != c.want {
			t.Errorf("path %q routed to %q, want %q", c.path, hit, c.want)
		}
	}
}
