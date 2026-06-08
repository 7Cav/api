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
			gzw := &gzipResponseWriter{ResponseWriter: w, Writer: gz}
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
				if !bodyAllowedForStatus(gzw.status) {
					// The handler committed a bodyless final status (204/304;
					// 101 rides along — see bodyAllowedForStatus). net/http
					// accepts no body behind it, so gz.Close()'s empty-stream
					// header+trailer writes would only draw ErrBodyNotAllowed
					// and make the close log cry "truncated" on every
					// conditional-GET 304 (#134 mints them per cache hit) when
					// nothing was ever owed — no gzip stream started. Skip them
					// entirely. The per-request gzip.Writer holds no resources
					// beyond memory — not closing it leaks nothing (#175).
					return
				}
				if !gzw.wroteGzip && r.Method == http.MethodHead {
					// HEAD with nothing through the gzip layer (the
					// http.ServeContent HEAD shape: headers set, body skipped):
					// gz.Close()'s 23-byte empty stream would be counted by
					// net/http's HEAD bookkeeping and minted into
					// Content-Length: 23 — a length no GET would ever serve.
					// Skipped, net/http sets no Content-Length at all (its HEAD
					// finalization only computes one from bytes the handler
					// actually wrote), so the HEAD makes no length claim it
					// cannot back; Content-Encoding: gzip stays — the GET twin
					// serves gzip. A HEAD handler that DOES write through gz
					// takes the normal close path: net/http discards but counts
					// those bytes, so the computed length matches the buffered
					// GET twin's (#175).
					return
				}
				if err := gz.Close(); err != nil {
					// COARSE hijack carve-out (#175; faithfulness gap tracked in
					// #181). A deliberate ResponseController.Hijack (reachable
					// via the wrapper's Unwrap) leaves this deferred gz.Close()
					// failing with http.ErrHijacked — a takeover is not a
					// truncation, so suppress the "likely truncated" log. This
					// is DELIBERATELY coarse: it cannot tell a clean no-output
					// takeover from a hijack that abandoned committed gzip
					// output, so it can stay silent on a genuinely corrupt wire
					// (Content-Encoding: gzip flushed with no terminated
					// stream). The precise per-shape faithfulness — commit-then-
					// hijack smuggling, truncated-after-output, accurate
					// logging — is TRACKED IN #181 and is latent until a
					// hijacking handler exists. No handler hijacks today —
					// self-announcing, not remembered: the hijack tripwire
					// (wrapperconventions_test.go, #174) fails the suite the
					// moment production code in this package hijacks.
					if errors.Is(err, http.ErrHijacked) {
						return
					}
					// A genuine close failure means the gzip trailer never
					// reached the client — a corrupt body behind an
					// already-written status, invisible to the sentry layer
					// outside this one.
					Error.Printf("gzip close failed for %s %s (response likely truncated): %v", r.Method, r.URL.Path, err)
				}
			}()
			next.ServeHTTP(gzw, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	Writer *gzip.Writer
	// wroteGzip latches when the handler sends gzip-layer output: Write (the
	// gzip.Writer emits its lazy 10-byte header on the first call, even for a
	// zero-length p) or FlushError (header + sync block). The deferred close
	// reads it for the HEAD-no-output skip: a HEAD that wrote through gz takes
	// the normal close path, so net/http's discarded-but-counted length matches
	// the buffered GET twin's (#175). WriteHeader alone does NOT latch — it
	// commits the response but writes no gzip bytes.
	wroteGzip bool
	// status latches the FIRST committing WriteHeader code (informational
	// forwards latch nothing, mirroring net/http — see the informational
	// predicate, #165); 0 when the handler never called WriteHeader — then the
	// commit came from Write/FlushError (implicit 200) or falls to the
	// middleware's deferred close. The deferred close consults it to skip the
	// doomed empty-stream writes behind a bodyless status (#175). Later
	// superfluous WriteHeaders must not overwrite it: net/http honors only the
	// first, so only the first describes the wire.
	status int
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	w.wroteGzip = true               // the gzip stream starts here even if b is empty — the lazy header goes downstream
	w.Header().Del("Content-Length") // This is necessary as otherwise it will have the uncompressed length
	return w.Writer.Write(b)
}

// WriteHeader strips the stale uncompressed Content-Length before the status
// commits (#175): net/http latches the length at the final WriteHeader, and
// behind this middleware a handler-set length is always the UNCOMPRESSED one.
// Committed, it corrupts the wire in one of two shapes — one client outcome
// (unexpected EOF mid-stream), different server-side visibility. When
// compression EXPANDS the payload (small or incompressible bodies) the
// compressed stream overruns the declared length and net/http cuts it
// mid-write — for a body that stays buffered in flate the overrun surfaces
// only at the deferred gz.Close, while a LARGE incompressible body (tens of
// KB — enough to force block emission mid-handler) hands it back as
// ErrContentLength from the handler's own Write, which most handlers ignore.
// When compression SHRINKS it (the compressible #134 file-serving shape) the
// COMPLETE compressed stream lands under the declared length and net/http
// closes the connection short of it — gz.Close SUCCEEDS and the server is
// fully silent, not even the close log; handler calls return nil at any
// size. So neither shape makes the strip optional. Same staleness Write and
// FlushError strip on their own commit paths. A forwarded informational WriteHeader
// (1xx minus 101 — rationale on the informational predicate) strips nothing
// (#165): it commits no response (the stdlib excludes Content-Length from
// interim responses on its own), so the strip decision belongs to the final
// status that follows.
func (w *gzipResponseWriter) WriteHeader(code int) {
	if !informational(code) {
		w.Header().Del("Content-Length")
		if w.status == 0 {
			w.status = code
			if !bodyAllowedForStatus(code) {
				// A bodyless status carries no representation, so the
				// middleware's eagerly-set Content-Encoding: gzip would
				// advertise an encoding that does not exist — on a 304 it
				// would misdescribe the stored representation the client is
				// revalidating (stdlib precedent: writeNotModified deletes
				// Content-Encoding for exactly this; the precedent is
				// 304-only — the 204 strip stands on the same reasoning by
				// analogy, since this middleware never encoded anything).
				// Strip it before the status commits the header map (#175).
				// The Del is not scoped to the middleware's own stamp: a
				// Content-Encoding the HANDLER set goes with it — behind a
				// bodyless status no encoding claim survives, whoever made
				// it. On a superfluous WriteHeader after a real commit this
				// mutates a dead map.
				w.Header().Del("Content-Encoding")
			}
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

// bodyAllowedForStatus mirrors net/http's unexported predicate of the same
// name: 1xx, 204, and 304 are the final statuses that permit no body —
// downstream body writes behind them fail with http.ErrBodyNotAllowed. The
// 1xx arm only ever sees 101 here (the informational predicate keeps
// non-latching 1xx codes out of the wrapper's status latch), where it is
// equally right: net/http allows nothing after a 101. The zero value —
// status never latched, response committed by Write/FlushError or the
// middleware's deferred close — lands in the default arm: body allowed.
func bodyAllowedForStatus(code int) bool {
	switch {
	case code >= 100 && code <= 199:
		return false
	case code == http.StatusNoContent:
		return false
	case code == http.StatusNotModified:
		return false
	}
	return true
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
	// A flush starts the gzip stream even before the first write — gz.Flush
	// pushes the lazy header and a sync block downstream (and on a flush
	// failure an arbitrary prefix may have landed), so the stream is owed a
	// trailer from here on. Latch before flushing.
	w.wroteGzip = true
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
// Hijack rides through too, but the middleware does NOT yet handle a hijacked
// gzip response faithfully: the deferred gz.Close() still fires after the
// hijack and fails with http.ErrHijacked, and the close-log carve-out above
// suppresses that COARSELY — it cannot tell a clean takeover from one that
// abandoned committed gzip output, so a real mid-stream corruption behind a
// hijack can go unlogged. Content-Encoding: gzip is already on the header map
// either way (a hijacker building its raw response from that map would
// advertise compression it does not perform). The precise faithfulness work —
// distinguishing the hijack shapes and logging each accurately — is TRACKED IN
// #181 and is latent until a hijacking handler exists. Flush can never take
// this route: the controller's method search prefers the explicit FlushError
// above, which is what keeps flushes from bypassing the gzip buffer and
// corrupting the stream.
func (w *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
