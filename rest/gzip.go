package rest

import (
	"compress/gzip"
	"net/http"
	"strings"
)

// GzipMiddleware moved here verbatim from servers/gateway (compression
// middleware) for the Phase 3 rewrite; the gateway delegates to it until
// cutover deletes that stack. It sits inside auth (401s are never gzipped)
// and outside the mux, so every routed response — including the JSON 404 —
// compresses when the client asks.
//
// Exported because the legacy gateway chain reuses it until cutover deletes
// that stack.
func GzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			defer func() {
				if err := gz.Close(); err != nil {
					// A Close failure means the gzip trailer never reached the
					// client — a corrupt body behind an already-written status,
					// invisible to the sentry layer outside this one.
					Error.Printf("gzip close failed for %s %s (response likely truncated): %v", r.Method, r.URL.Path, err)
				}
			}()
			gzw := &gzipResponseWriter{ResponseWriter: w, Writer: gz}
			next.ServeHTTP(gzw, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	Writer *gzip.Writer
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	w.Header().Del("Content-Length") // This is necessary as otherwise it will have the uncompressed length
	return w.Writer.Write(b)
}

// FlushError keeps http.ResponseController.Flush working behind gzip (#167):
// first flush the gzip.Writer — emitting a sync block so everything the
// handler wrote so far reaches the underlying writer as a decodable gzip
// prefix — THEN flush the underlying chain onto the wire. That order is the
// invariant: the bytes on the wire are always a valid gzip stream prefix. A
// bare Unwrap instead would let flushes bypass the gzip buffer entirely,
// pushing a stream the client cannot yet decode while the compressed tail
// sits buffered here. Delegating through a fresh ResponseController keeps the
// downstream search semantics identical (same pattern as commitWriter and
// cacheControlWriter). Any error from the delegated flush — including
// ErrNotSupported from an unflushable chain — is NOT a no-op: the gzip
// header and sync block are already downstream before it runs. (If gz.Flush
// itself fails, the failure came from a downstream write — what landed is
// an arbitrary prefix, possibly nothing.)
func (w *gzipResponseWriter) FlushError() error {
	// A flush before the first write commits the headers, so the stale
	// uncompressed Content-Length must go here too — same staleness Write
	// handles above; left in place it truncates the compressed stream. An
	// explicit WriteHeader still commits it (net/http latches the length at
	// WriteHeader) — pre-existing hole, reachable on develop without any
	// flush, tracked separately.
	w.Header().Del("Content-Length")
	if err := w.Writer.Flush(); err != nil {
		return err
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap exposes the underlying writer to http.ResponseController for the
// verbs that don't touch the compressed stream — deadline control is the
// deliberately supported set (EnableFullDuplex rides along harmlessly).
// Hijack is NOT like hijacking past other wrappers here: the middleware's
// deferred gz.Close() still fires after the hijack and fails with
// http.ErrHijacked, emitting the misleading "response likely truncated"
// log, and Content-Encoding: gzip is already on the header map. Flush can
// never take this route: the controller's method search prefers the
// explicit FlushError above, which is what keeps flushes from bypassing the
// gzip buffer and corrupting the stream.
func (w *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
