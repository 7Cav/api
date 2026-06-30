package rest_test

// Vary: Accept-Encoding (#228). GzipMiddleware selects the response encoding
// from the request's Accept-Encoding: one URL yields a gzipped body for a gzip
// client and an identity body for everyone else. The header declares that the
// origin chose the representation by content negotiation, so a shared cache
// keyed on the URL alone keys each encoding variant separately instead of
// handing a gzipped body to a client that cannot decode it (or the reverse).
//
// The middleware sets it unconditionally at the top of the handler, so every
// response inherits it — both the gzip branch and the identity pass-through,
// across both surfaces the middleware fronts (/api and the docs handler).
//
// Like the Cache-Control pins next door (cachecontrol_test.go), these are
// deliberately new-stack-only: the golden corpus replays against the old
// stack, which sent no Vary, so the header stays off the contractHeaders
// allowlist and is pinned here instead.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/7cav/api/contract"
	"github.com/7cav/api/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An /api request that does not negotiate gzip is served identity, and still
// carries Vary: Accept-Encoding — the identity variant must declare it too, or
// a cache could store it without the header and mis-serve it to a gzip client.
func TestNewStack_VaryOnIdentityAPIResponse(t *testing.T) {
	h := newStack(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Empty(t, rr.Result().Header.Get("Content-Encoding"),
		"a request that does not advertise gzip is served identity")
	assert.Equal(t, "Accept-Encoding", rr.Result().Header.Get("Vary"))
}

// The gzip branch carries it too: a gzip-negotiated /api response is compressed
// (Content-Encoding: gzip) and still advertises Vary: Accept-Encoding.
func TestNewStack_VaryOnGzipAPIResponse(t *testing.T) {
	h := newStack(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "gzip", rr.Result().Header.Get("Content-Encoding"))
	assert.Equal(t, "Accept-Encoding", rr.Result().Header.Get("Vary"))
}

// Across the whole implemented /api battery, every response that passes through
// GzipMiddleware advertises Vary: Accept-Encoding — the 200s and every error
// status that reaches the handlers (scope 403s, binding 400s, not-found 404s,
// injected-outage 500s). The auth 401 tier is the one exception: AuthMiddleware
// sits OUTSIDE the gzip layer (the same boundary that keeps 401s un-gzipped), so
// it short-circuits before the header is set and carries no Vary. Riding
// implementedCases means a future route's battery cases are covered the moment
// they land. Observed through the wire-faithful commitSnapshot helper, so a
// header stamped after commit would not pass.
func TestNewStack_VaryAcceptEncodingAcrossBattery(t *testing.T) {
	var last http.Header
	h := captureHeader(newStack(t), &last)

	known := map[string]contract.Case{}
	for _, c := range contract.Cases() {
		known[c.Name] = c
	}

	sawGzipPath, sawAuthShortCircuit := false, false
	for _, name := range implementedCases {
		c, ok := known[name]
		require.True(t, ok, "implementedCases entry %q names no battery case", name)
		t.Run(name, func(t *testing.T) {
			g, _, err := contract.RunCase(h, c)
			require.NoError(t, err)

			if g.Status == http.StatusUnauthorized {
				assert.Empty(t, last.Get("Vary"),
					"the auth 401 tier short-circuits before GzipMiddleware, so it carries no Vary")
				sawAuthShortCircuit = true
				return
			}
			assert.Equal(t, "Accept-Encoding", last.Get("Vary"),
				"every response through GzipMiddleware carries Vary (status %d on %s)", g.Status, c.Path)
			sawGzipPath = true
		})
	}

	// Non-vacuousness: the battery must witness both arms — at least one
	// response through the gzip layer and at least one auth short-circuit —
	// otherwise either assertion could pass by absence.
	assert.True(t, sawGzipPath, "no battery response witnessed a path through GzipMiddleware")
	assert.True(t, sawAuthShortCircuit, "no battery response witnessed an auth 401 short-circuit")
}

// The docs surface inherits the header from the same GzipMiddleware, on both
// negotiations: the identity pass-through and the gzip branch.
func TestDocsHandler_VaryOnBothNegotiations(t *testing.T) {
	h := rest.DocsHandler("dev")

	t.Run("identity", func(t *testing.T) {
		rr := docsGet(t, h, "/openapi.yaml")
		require.Equal(t, http.StatusOK, rr.Code)
		require.Empty(t, rr.Result().Header.Get("Content-Encoding"),
			"an un-negotiated docs response is served verbatim")
		assert.Equal(t, "Accept-Encoding", rr.Result().Header.Get("Vary"))
	})

	t.Run("gzip", func(t *testing.T) {
		rr := docsGetGzip(t, h, "/openapi.yaml")
		require.Equal(t, http.StatusOK, rr.Code)
		require.Equal(t, "gzip", rr.Result().Header.Get("Content-Encoding"),
			"a gzip-negotiated docs response is compressed")
		assert.Equal(t, "Accept-Encoding", rr.Result().Header.Get("Vary"))
	})
}

// The header is appended, not written over. An upstream layer that already set
// its own Vary keeps it: GzipMiddleware uses Header().Add, so both values reach
// the wire. A Set would silently drop the upstream value.
func TestGzipMiddleware_VaryAppendsToExistingValue(t *testing.T) {
	inner := rest.GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	// An outer layer that set Vary before GzipMiddleware runs (e.g. a future
	// middleware varying on Origin). The chain has none today; the Add contract
	// is what keeps it safe if one is ever mounted outside the gzip layer.
	outer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Vary", "Origin")
		inner.ServeHTTP(w, r)
	})

	rr := httptest.NewRecorder()
	outer.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, http.StatusOK, rr.Code)
	got := rr.Result().Header.Values("Vary")
	assert.Contains(t, got, "Origin", "an upstream Vary value must survive (Add, not Set)")
	assert.Contains(t, got, "Accept-Encoding", "GzipMiddleware appends its own Vary value")
}

// Vary is a cache-keying hint, so it is correct to keep on bodyless final
// statuses (204/304) — unlike Content-Encoding, which the middleware strips off
// them because there is no body to encode. A gzip-negotiated 204 demonstrates
// both: Content-Encoding gone, Vary kept.
func TestGzipMiddleware_VaryKeptOnBodylessStatus(t *testing.T) {
	h := rest.GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.Empty(t, rr.Result().Header.Get("Content-Encoding"),
		"a bodyless final carries no Content-Encoding — there is no body to encode")
	assert.Equal(t, "Accept-Encoding", rr.Result().Header.Get("Vary"),
		"Vary is a cache-keying hint, kept on bodyless finals on purpose")
}
