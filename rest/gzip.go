package rest

import (
	"compress/gzip"
	"errors"
	"io"
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
			wire := &countingWriter{w: w}
			gz := gzip.NewWriter(wire)
			gzw := &gzipResponseWriter{ResponseWriter: w, Writer: gz}
			defer func() {
				// A handler that set the stale uncompressed Content-Length and
				// returned without writing reaches here uncommitted — Close's
				// direct downstream writes (gzip header + empty-stream trailer)
				// are then the commit, and they bypass the wrapper's strips
				// entirely (gz writes below the wrapper, not through it), so
				// the stale length must go HERE before they latch it (#175,
				// fourth variant). On every committed response this Del mutates
				// a dead map — net/http snapshotted the headers at commit.
				w.Header().Del("Content-Length")
				if wire.bytesOut == 0 && !bodyAllowedForStatus(gzw.status) {
					// The handler committed a bodyless final status (204/304;
					// 101 rides along — see bodyAllowedForStatus) and net/http
					// accepted no gzip byte behind it: the wire carries the
					// bare status, no stream started, none owed. gz.Close()'s
					// empty-stream header+trailer writes would only hit
					// net/http's ErrBodyNotAllowed and make the close log cry
					// "truncated" on every conditional-GET 304 (#134 mints
					// them per cache hit), so skip them entirely. Keyed on
					// bytesOut, NOT the attempt latches: a handler that wrote
					// or flushed against the bodyless status was rejected
					// wholesale downstream (zero bytes accepted), so the wire
					// is exactly as clean and the write/flush already handed
					// ErrBodyNotAllowed back to its caller. A rejected FLUSH
					// stays quiet — a flush-happy wrapper around ServeContent
					// lands here once per #134 cache hit — but a body WRITE is
					// a handler bug worth one accurate line, naming the real
					// shape and never claiming truncation (#175). The
					// per-request gzip.Writer holds no resources beyond
					// memory — not closing it leaks nothing.
					if gzw.wroteBody {
						Error.Printf("gzip body written behind bodyless %d for %s %s — net/http rejected every byte; the client received the bare status (handler bug, wire intact)", gzw.status, r.Method, r.URL.Path)
					}
					return
				}
				if !gzw.wroteGzip && r.Method == http.MethodHead {
					// HEAD with nothing through the gzip layer (the
					// http.ServeContent HEAD shape: headers set, body
					// skipped): gz.Close()'s 23-byte empty stream would be
					// counted by net/http's HEAD bookkeeping and minted into
					// Content-Length: 23 — a length no GET would ever serve.
					// Skipped, net/http sets no Content-Length at all (its
					// HEAD finalization only computes one from bytes the
					// handler actually wrote), so the HEAD makes no length
					// claim it cannot back; Content-Encoding: gzip stays —
					// the GET twin serves gzip. A HEAD handler that DOES
					// write through gz takes the normal close path: net/http
					// discards but counts those bytes, so the computed
					// length matches the buffered GET twin's (#175).
					return
				}
				if err := gz.Close(); err != nil {
					if errors.Is(err, http.ErrHijacked) {
						if gzw.status == 0 && wire.bytesOut == 0 {
							// A pristine takeover (ResponseController.Hijack via
							// the wrapper's Unwrap): nothing committed — the
							// status never latched, and gzip bytes, which would
							// have committed an implicit 200, never got
							// downstream — so net/http flushed nothing before
							// handing over the connection and the wire carries
							// ONLY what the hijacker wrote. No stream was owed;
							// logging "truncated" here would cry wolf on every
							// clean hijack — and the close log is the only
							// server-side signal of the real corruption class,
							// so it must stay trustworthy (#175). A stray Write
							// through this dead wrapper after the takeover lands
							// here too (rejected wholesale, zero bytes accepted,
							// an intact wire): the stdlib already logs
							// "response.Write on hijacked connection" for that
							// misuse.
							return
						}
						if wire.bytesOut == 0 {
							// Committed, then hijacked, with no gzip output. The
							// status is necessarily body-allowed — the bodyless
							// arm above peeled off the rest — and net/http's
							// Hijack FLUSHES committed headers before handing
							// over the connection (server.go: "if w.wroteHeader
							// { w.cw.flush() }"): the client holds a flushed
							// status with Content-Encoding: gzip and chunked
							// framing that never carries a stream, and anything
							// the hijacker writes lands AFTER those flushed
							// bytes, inside that framing. Not a truncation — no
							// stream ever started — but corruption all the same,
							// and every handler call returned nil, so this line
							// is its only witness (#175).
							Error.Printf("gzip-advertised %d committed and flushed before hijack with no gzip output for %s %s — client received Content-Encoding: gzip with no stream: %v", gzw.status, r.Method, r.URL.Path, err)
							return
						}
						// Gzip output reached net/http before the hijack: a
						// stream started (header downstream, payload in the
						// flate buffer or a flushed prefix on the wire) and was
						// never terminated — the client got Content-Encoding:
						// gzip with a body that dies mid-decode, while every
						// handler call returned nil. Keyed on bytesOut: only
						// bytes net/http actually accepted can have been
						// truncated (#175).
						Error.Printf("gzip stream abandoned by hijack after output for %s %s — client received a truncated body: %v", r.Method, r.URL.Path, err)
						return
					}
					if wire.bytesOut == 0 {
						// A genuine close failure before a single gzip byte was
						// accepted downstream (the first write rejected — torn
						// connection, write timeout): the client got no part of
						// the stream, so "truncated" would overstate it, but the
						// failure still needs its line — it is invisible
						// everywhere else (#175).
						Error.Printf("gzip close failed for %s %s before any gzip byte reached the client: %v", r.Method, r.URL.Path, err)
						return
					}
					// A Close failure after output means the gzip trailer never
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

// countingWriter sits between the gzip.Writer and the real ResponseWriter
// and counts the bytes net/http ACCEPTED from the gzip layer — the n of each
// delegated Write, error or not. The wrapper's latches record what the
// handler ATTEMPTED; bytesOut records what got past net/http, and the two
// diverge exactly when net/http rejects the push wholesale (ErrHijacked
// after a takeover, ErrBodyNotAllowed behind a 204/304: n is 0 both ways).
// The middleware's deferred close keys every wire claim — truncated,
// abandoned, clean — on bytesOut, because only accepted bytes can have been
// lost (#175).
type countingWriter struct {
	w        io.Writer
	bytesOut int
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.bytesOut += n
	return n, err
}

type gzipResponseWriter struct {
	http.ResponseWriter
	Writer *gzip.Writer
	// wroteGzip latches when the handler ATTEMPTS gzip-layer output: Write
	// (the gzip.Writer emits its lazy 10-byte header on the first call, even
	// for a zero-length p) or FlushError (header + sync block). It records
	// the attempt only — whether those bytes were ACCEPTED downstream is the
	// countingWriter's bytesOut, and the deferred close must consult bytesOut
	// for any claim about the wire (#175). WriteHeader alone does NOT latch —
	// it commits the response but writes no gzip bytes.
	wroteGzip bool
	// wroteBody latches on Write only: the handler claimed to have body
	// bytes. The deferred close uses it to name the body-behind-bodyless-
	// status handler bug, where a FLUSH in the same square (the ServeContent-
	// behind-a-flush-happy-wrapper shape, once per #134 conditional-GET hit)
	// stays quiet (#175).
	wroteBody bool
	// status latches the FIRST committing WriteHeader code (informational
	// forwards latch nothing, mirroring net/http — see the informational
	// predicate, #165); 0 when the handler never called WriteHeader — then
	// the commit came from Write/FlushError (implicit 200) or falls to the
	// middleware's deferred close. The deferred close consults it to skip the
	// doomed empty-stream writes behind a bodyless status (#175). Later
	// superfluous WriteHeaders must not overwrite it: net/http honors only
	// the first, so only the first describes the wire.
	status int
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	w.wroteGzip = true // the gzip stream starts here even if b is empty — the lazy header goes downstream
	w.wroteBody = true
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
// Hijack is NOT like hijacking past other wrappers here: the middleware's
// deferred gz.Close() still fires after the hijack and fails with
// http.ErrHijacked — quiet in the close log above ONLY when the takeover was
// pristine, nothing committed AND no gzip byte accepted downstream (#175).
// Only then does the wire carry the hijacker's bytes alone; after a commit,
// net/http's Hijack flushes the committed headers before handing over the
// connection, so the hijacker's bytes land AFTER whatever net/http already
// flushed — inside the committed framing — and the close log says so, as it
// does for a hijack that abandons a started stream. Content-Encoding: gzip
// is already on the header map either way (a hijacker building its raw
// response from that map would advertise compression it does not perform).
// Flush can never take this route: the controller's method search prefers
// the explicit FlushError above, which is what keeps flushes from bypassing
// the gzip buffer and corrupting the stream.
func (w *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
