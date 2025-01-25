package middleware

import (
	"compress/gzip"
	"github.com/7cav/api/cache"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"
)

var (
	Info  = log.New(os.Stdout, "INFO: ", 0)
	Warn  = log.New(os.Stdout, "WARNING: ", 0)
	Error = log.New(os.Stdout, "ERROR: ", 0)
)

func CacheMiddleware(cache *cache.RedisCache, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		defer func() {
			Info.Printf("[CACHE] Request completed in %v", time.Since(start))
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

			if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
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
		acceptEncoding := r.Header.Get("Accept-Encoding")
		r.Header.Del("Accept-Encoding")

		Info.Printf("[CACHE] Forwarding request to handler: %s", r.URL.Path)
		next.ServeHTTP(rec, r)

		if rec.Code != http.StatusOK {
			Info.Printf("[CACHE] Non-200 response: %d, not caching", rec.Code)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(rec.Code)

			if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				Info.Printf("[CACHE] Compressing error response with gzip")
				w.Header().Set("Content-Encoding", "gzip")
				gz := gzip.NewWriter(w)
				gz.Write(rec.Body.Bytes())
				gz.Close()
				return
			}

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

		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") || rec.Code == http.StatusOK {
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
