package middleware

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/7cav/api/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Drift guard: *cache.RedisCache must keep satisfying responseCache. The
// cache import is test-only, so the production package keeps its
// dependency-edge removal.
var _ responseCache = (*cache.RedisCache)(nil)

// captureInfo redirects the package Info logger to a buffer for the duration
// of the test and restores it on cleanup. Not parallel-safe: swaps a shared
// package-level logger; do not add t.Parallel() to this package.
func captureInfo(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := Info.Writer()
	Info.SetOutput(buf)
	t.Cleanup(func() { Info.SetOutput(prev) })
	return buf
}

// fakeCache is an in-memory stand-in for cache.RedisCache, mirroring its
// key scheme. An empty fakeCache misses on every Get.
type fakeCache struct {
	data map[string][]byte
}

func (f *fakeCache) Get(key string) ([]byte, error) {
	if b, ok := f.data[key]; ok {
		return b, nil
	}
	return nil, errors.New("redis: nil")
}

func (f *fakeCache) Set(key string, response []byte) error {
	if f.data == nil {
		f.data = map[string][]byte{}
	}
	f.data[key] = response
	return nil
}

func (f *fakeCache) GenerateCacheKey(path string) string {
	return "api:response:" + path
}

var cacheDurationPattern = regexp.MustCompile(`duration=(\S+)`)

// completionLinePattern captures both renderings of the elapsed value on the
// completion line: the human-readable "in <v>" and the parseable "duration=<v>".
var completionLinePattern = regexp.MustCompile(`\[CACHE\] Request completed in (\S+) duration=(\S+)`)

// requireCompletionValuesEqual asserts both renderings are identical, locking
// the compute-elapsed-once contract: cache.go computes elapsed one time and
// prints it twice.
func requireCompletionValuesEqual(t *testing.T, logged string) {
	t.Helper()
	m := completionLinePattern.FindStringSubmatch(logged)
	require.Len(t, m, 3, "log output must carry a completion line with both values, got: %q", logged)
	assert.Equal(t, m[1], m[2],
		"human-readable and duration= values must come from a single elapsed computation")
}

// extractCacheDuration pulls the duration= value out of the captured log
// output and parses it as a real time.Duration.
func extractCacheDuration(t *testing.T, logged string) time.Duration {
	t.Helper()
	m := cacheDurationPattern.FindStringSubmatch(logged)
	require.Len(t, m, 2, "log output must carry a duration= field, got: %q", logged)
	d, err := time.ParseDuration(m[1])
	require.NoError(t, err, "duration= value must parse as a Go duration")
	return d
}

// Phase 0 measuring stick (#114): a cache-miss request must end with a
// completion line carrying a parseable duration= covering the handler, while
// the existing line text stays intact for the ad-hoc analytics.
func TestCacheMiddleware_MissLogsDuration(t *testing.T) {
	buf := captureInfo(t)
	const handlerDelay = 15 * time.Millisecond
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(handlerDelay)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})
	h := CacheMiddleware(&fakeCache{}, next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/roster", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "MISS", rec.Header().Get("X-Cache"))
	assert.Equal(t, `{"ok":true}`, rec.Body.String())

	logged := buf.String()
	// Existing consumer-visible lines unbroken.
	assert.Contains(t, logged, "[CACHE] MISS: Cache miss for key: api:response:/api/v1/roster")
	assert.Regexp(t, `\[CACHE\] Request completed in \S+ duration=\S+`, logged)
	d := extractCacheDuration(t, logged)
	assert.GreaterOrEqual(t, d, handlerDelay,
		"duration must cover the handler on the miss path")
	requireCompletionValuesEqual(t, logged)
}

// Tickets requests bypass the middleware before timing starts, so they emit
// no [CACHE] completion line — intent, not accident: the gRPC [REQ] line
// covers those requests downstream.
func TestCacheMiddleware_TicketsBypassEmitsNoCompletionLine(t *testing.T) {
	buf := captureInfo(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := CacheMiddleware(&fakeCache{}, next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tickets/123", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, buf.String(), "[CACHE] Request completed",
		"tickets bypass must return before the timing defer is installed")
}

// Non-GET requests skip the cache but must still get a timed completion line:
// the timing defer sits above the GET guard in cache.go, and this test makes
// that placement load-bearing instead of accidental. The skip line itself
// must stay verbatim for the ad-hoc analytics.
func TestCacheMiddleware_PostSkipsCacheButLogsDuration(t *testing.T) {
	buf := captureInfo(t)
	const handlerDelay = 15 * time.Millisecond
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(handlerDelay)
		w.WriteHeader(http.StatusOK)
	})
	h := CacheMiddleware(&fakeCache{}, next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/roster", nil))

	require.Equal(t, http.StatusOK, rec.Code)

	logged := buf.String()
	// Existing consumer-visible line unbroken.
	assert.Contains(t, logged, "[CACHE] Skipping cache for POST request: /api/v1/roster")
	assert.Regexp(t, `\[CACHE\] Request completed in \S+ duration=\S+`, logged)
	d := extractCacheDuration(t, logged)
	assert.GreaterOrEqual(t, d, handlerDelay,
		"duration must cover the handler on the non-GET bypass path")
}

// A cache-hit request never reaches the gRPC [REQ] line, so its duration must
// come from the [CACHE] completion line — otherwise the Phase 2 de-cache
// delta is invisible for hits.
func TestCacheMiddleware_HitLogsDuration(t *testing.T) {
	buf := captureInfo(t)
	fc := &fakeCache{data: map[string][]byte{
		"api:response:/api/v1/roster": []byte(`{"cached":true}`),
	}}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run on a cache hit")
	})
	h := CacheMiddleware(fc, next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/roster", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "HIT", rec.Header().Get("X-Cache"))
	assert.Equal(t, `{"cached":true}`, rec.Body.String())

	logged := buf.String()
	// Existing consumer-visible lines unbroken.
	assert.Contains(t, logged, "[CACHE] HIT: Found cached response for key: api:response:/api/v1/roster")
	assert.Regexp(t, `\[CACHE\] Request completed in \S+ duration=\S+`, logged)
	extractCacheDuration(t, logged)
	requireCompletionValuesEqual(t, logged)
}
