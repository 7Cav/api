package rest

import (
	"compress/gzip"
	"errors"
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
//
// A handler-sent 1xx on a gzip-negotiated request carries Content-Encoding:
// gzip in the interim response — the stdlib sends the live header map per
// RFC 8297 — pre-existing, adjacent to the stale-Content-Length holes #175
// closed.
func GzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			defer func() {
				// A handler that set the stale uncompressed Content-Length and
				// returned without writing reaches here uncommitted — Close's
				// direct downstream writes (gzip header + empty-stream trailer)
				// are then the commit, and they bypass the wrapper's strips
				// entirely (gz writes to w, not through gzipResponseWriter), so
				// the stale length must go HERE before they latch it (#175,
				// fourth variant). On every committed response this Del mutates
				// a dead map — net/http snapshotted the headers at commit.
				w.Header().Del("Content-Length")
				if err := gz.Close(); err != nil {
					if errors.Is(err, http.ErrHijacked) {
						// A deliberate takeover (ResponseController.Hijack via
						// the wrapper's Unwrap): the connection belongs to the
						// handler and the trailer was never owed to the client.
						// Logging "truncated" here would cry wolf on every
						// hijack — and this line is the only server-side signal
						// of the real corruption class, so it must stay
						// trustworthy (#175).
						return
					}
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

// WriteHeader strips the stale uncompressed Content-Length before the status
// commits (#175): net/http latches the length at the final WriteHeader, and
// behind this middleware a handler-set length is always the UNCOMPRESSED one
// — committed, it truncates the longer compressed stream on the wire while
// every handler call still returns nil. Same staleness Write and FlushError
// strip on their own commit paths. A forwarded informational WriteHeader
// (1xx minus 101 — rationale on the informational predicate) strips nothing
// (#165): it commits no response (the stdlib excludes Content-Length from
// interim responses on its own), so the strip decision belongs to the final
// status that follows.
func (w *gzipResponseWriter) WriteHeader(code int) {
	if !informational(code) {
		w.Header().Del("Content-Length")
	}
	w.ResponseWriter.WriteHeader(code)
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
	// and WriteHeader handle above; left in place it truncates the
	// compressed stream.
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
// http.ErrHijacked — carved out of the close log above (#175): a takeover is
// deliberate, not a truncation — and Content-Encoding: gzip is already on
// the header map (a hijacker building its raw response from that map would
// advertise compression it does not perform). Flush can
// never take this route: the controller's method search prefers the
// explicit FlushError above, which is what keeps flushes from bypassing the
// gzip buffer and corrupting the stream.
func (w *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
