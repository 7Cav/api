package middleware

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureInfo redirects the package Info logger to a buffer for the duration
// of the test and restores it on cleanup.
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
}
