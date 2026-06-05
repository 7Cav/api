package middleware

import (
	"compress/gzip"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"
)

var (
	Info  = log.New(os.Stdout, "INFO: ", log.LstdFlags)
	Warn  = log.New(os.Stdout, "WARNING: ", log.LstdFlags)
	Error = log.New(os.Stdout, "ERROR: ", log.LstdFlags)
)

// responseCache is the slice of cache.RedisCache the middleware actually
// uses. *cache.RedisCache satisfies it implicitly, so callers are unchanged;
// tests substitute an in-memory fake.
type responseCache interface {
	Get(key string) ([]byte, error)
	Set(key string, response []byte) error
	GenerateCacheKey(path string) string
}

func CacheMiddleware(cache responseCache, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/tickets" || strings.HasPrefix(r.URL.Path, "/api/v1/tickets/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		acceptEncoding := r.Header.Get("Accept-Encoding")
		r.Header.Del("Accept-Encoding")
		endodeGzip := strings.Contains(acceptEncoding, "gzip")
		defer func() {
			// Phase 0 measuring stick (#112/#114): duration= duplicates the
			// human-readable elapsed value as a parseable field, appended so
			// existing ad-hoc analytics keep matching the line. Fires on hit
			// and miss alike. Temporary; retires once Prometheus owns metrics.
			elapsed := time.Since(start)
			Info.Printf("[CACHE] Request completed in %v duration=%v", elapsed, elapsed)
		}()

		if r.Method != http.MethodGet {
			Info.Printf("[CACHE] Skipping cache for %s request: %s", r.Method, r.URL.Path)
			next.ServeHTTP(w, r)
			return
		}

		key := cache.GenerateCacheKey(r.URL.Path)
		Info.Printf("[CACHE] Generated cache key: %s for path: %s", key, r.URL.Path)

		if cached, err := cache.Get(key); err == nil {
			Info.Printf("[CACHE] HIT: Found cached response for key: %s (%d bytes)", key, len(cached))
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Cache", "HIT")

			if endodeGzip {
				Info.Printf("[CACHE] Compressing cached response with gzip")
				w.Header().Set("Content-Encoding", "gzip")
				gz := gzip.NewWriter(w)
				gz.Write(cached)
				gz.Close()
				return
			}

			w.Write(cached)
			return
		} else {
			Info.Printf("[CACHE] MISS: Cache miss for key: %s, error: %v", key, err)
		}

		rec := httptest.NewRecorder()

		Info.Printf("[CACHE] Forwarding request to handler: %s", r.URL.Path)
		next.ServeHTTP(rec, r)

		if rec.Code != http.StatusOK {
			Info.Printf("[CACHE] Non-200 response: %d, not caching", rec.Code)
			w.Header().Set("Content-Type", "application/json")

			if endodeGzip {
				Info.Printf("[CACHE] Compressing error response with gzip")
				w.Header().Set("Content-Encoding", "gzip")
				w.WriteHeader(rec.Code)
				gz := gzip.NewWriter(w)
				gz.Write(rec.Body.Bytes())
				gz.Close()
				return
			}
			w.WriteHeader(rec.Code)
			w.Write(rec.Body.Bytes())
			return
		}

		if rec.Code == http.StatusOK {
			Info.Printf("[CACHE] Caching successful response (%d bytes)", rec.Body.Len())
			cache.Set(key, rec.Body.Bytes())
		}

		if acceptEncoding != "" {
			r.Header.Set("Accept-Encoding", acceptEncoding)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "MISS")

		if endodeGzip {
			Info.Printf("[CACHE] Compressing response with gzip")
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			gz.Write(rec.Body.Bytes())
			gz.Close()
			return
		}

		w.Write(rec.Body.Bytes())
	})
}
