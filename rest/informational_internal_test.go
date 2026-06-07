package rest

// Shared latch coverage for the chain's writer wrappers (#165): a
// non-latching informational WriteHeader (1xx minus 101 — net/http commits
// on 101; rationale on the informational predicate) must pass through to the
// delegate and latch NOTHING — the stdlib response writer leaves its
// wroteHeader latch false for that set, so a wrapper that latches there
// diverges from the wire: metrics would meter a 103 as the final status, the
// panic recovery would abort a connection whose response is still rewritable,
// and Cache-Control would miss the real 200.
//
// The observations are wire-faithful: a real httptest server (the stdlib's
// actual 1xx machinery, which a ResponseRecorder does not have) with the
// forwarded 100 and 103 captured via httptrace on the client side.

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The predicate's boundaries pin the wire definition exactly: the
// non-latching set is [100, 199] minus the 101 carve-out (net/http commits
// on 101) — a guard that drifts to e.g. (100, 199] would silently latch on a
// forwarded 100 Continue.
func TestInformational_Boundaries(t *testing.T) {
	assert.False(t, informational(99), "99 is not a status class at all")
	assert.True(t, informational(http.StatusContinue), "100 opens the informational class")
	assert.False(t, informational(http.StatusSwitchingProtocols),
		"101 is the stdlib's one latching 1xx — net/http commits on it (no headers may follow a 101), so the wrappers must latch too")
	assert.True(t, informational(http.StatusEarlyHints), "103 is the one seen in the wild")
	assert.True(t, informational(199), "199 closes the informational class")
	assert.False(t, informational(http.StatusOK), "200 is a final status — it must latch")
}

// TestWriterWrappers_Informational1xxDoesNotLatch drives WriteHeader(100)
// then WriteHeader(103) through each writer wrapper's owning middleware and
// asserts (a) both reach the wire and (b) the wrapper's latch decision still
// belongs to the FINAL response — each row finishes the request the way that
// makes its own latch observable. A new writer wrapper joins this table.
func TestWriterWrappers_Informational1xxDoesNotLatch(t *testing.T) {
	// Counter readings the metrics row shares between build (before) and
	// assert (after) — rows run sequentially, never in parallel.
	var meter200Before, meter103Before float64
	// Event accessor the sentry row shares between build and assert.
	var sentryEvents func() []*sentry.Event
	meter := func(status string) float64 {
		// Route is "" (no routeLabel in this chain), method clamps to GET,
		// no key validated — the deferred recording's exact label set.
		return testutil.ToFloat64(requestsTotal.WithLabelValues("", http.MethodGet, status, ""))
	}

	cases := []struct {
		name string
		// build mounts the middleware that owns the wrapper under test.
		build func(t *testing.T, next http.Handler) http.Handler
		// finish completes the request after the shared WriteHeader(103).
		finish func(w http.ResponseWriter)
		// assert checks the final response for the wrapper's latch decision.
		assert func(t *testing.T, res *http.Response, body string)
	}{
		{
			name: "statusWriter meters the real 200",
			build: func(t *testing.T, next http.Handler) http.Handler {
				meter200Before = meter("200")
				meter103Before = meter("103")
				return metricsMiddleware(next)
			},
			finish: func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("{}"))
			},
			assert: func(t *testing.T, res *http.Response, body string) {
				require.Equal(t, http.StatusOK, res.StatusCode)
				assert.Equal(t, meter200Before+1, meter("200"),
					`the request must meter as status="200" — the final status, not the informational one`)
				assert.Equal(t, meter103Before, meter("103"),
					`a 103 is never a final status — no status="103" child may grow`)
			},
		},
		{
			// The 103 must not burn commitWriter's latch: the recovery can
			// still honestly write the contract 500 — an informational
			// response is not a final one, so the wire is still rewritable.
			// (Burned, it would re-panic into a connection abort instead.)
			name: "commitWriter keeps the contract 500 after a panic",
			build: func(t *testing.T, next http.Handler) http.Handler {
				sentryEvents = enableSentry(t).Events
				captureErrorLog(t)
				return sentryMiddleware(next)
			},
			finish: func(w http.ResponseWriter) {
				panic("exploded after the 103")
			},
			assert: func(t *testing.T, res *http.Response, body string) {
				require.Equal(t, http.StatusInternalServerError, res.StatusCode,
					"the contract 500 must land — the 103 committed nothing")
				assert.Equal(t, "application/json", res.Header.Get("Content-Type"))
				assert.JSONEq(t, `{"code":13,"message":"Internal Server Error","details":[]}`, body,
					"the panic 500 must keep the contract error shape")
				require.Len(t, sentryEvents(), 1, "one panic = one event")
			},
		},
		{
			name: "cacheControlWriter stamps the real 200",
			build: func(t *testing.T, next http.Handler) http.Handler {
				return cacheControl(600, next)
			},
			finish: func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("{}"))
			},
			assert: func(t *testing.T, res *http.Response, body string) {
				require.Equal(t, http.StatusOK, res.StatusCode)
				assert.Equal(t, "max-age=600", res.Header.Get("Cache-Control"),
					"the 103 must not burn the commit — the real 200 carries the freshness signal")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.build(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// The shared informational writes: the opening boundary plus
				// the code seen in the wild, both forwarded by the stdlib
				// without latching (100 opens the class, 103 is the 1xx real
				// traffic carries) — so an inlined-range drift in any
				// one wrapper fails here at wire level, not only in the
				// predicate unit pin.
				for _, code := range []int{http.StatusContinue, http.StatusEarlyHints} {
					w.WriteHeader(code)
				}
				tc.finish(w)
			}))
			srv := httptest.NewServer(h)
			defer srv.Close()

			var got1xx []int
			req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
			require.NoError(t, err)
			req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
				Got1xxResponse: func(code int, _ textproto.MIMEHeader) error {
					got1xx = append(got1xx, code)
					return nil
				},
			}))

			res, err := srv.Client().Do(req)
			require.NoError(t, err, "the final response must arrive — a burned latch must not abort the request")
			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			srv.Close() // wait out the handler so deferred recording has run

			require.Equal(t, []int{http.StatusContinue, http.StatusEarlyHints}, got1xx,
				"both informational writes must be forwarded to the wire, not swallowed")
			tc.assert(t, res, string(body))
		})
	}
}

// TestWriterWrappers_101Latches is the wire-level witness for the predicate's
// 101 carve-out: each wrapper must LATCH on WriteHeader(101) exactly as
// net/http commits on it. It cannot share the {100, 103} table above —
// nothing may follow a 101, so every row gets its own request.
//
// The dangerous wrapper is commitWriter: with the carve-out dropped (an
// inlined carve-out-less 1xx range, say) a 101 forwards without latching, so
// a 101-then-panic leaves committed == false and the recovery "writes" the
// contract 500 — which the stdlib swallows (superfluous WriteHeader, Write
// returns http.ErrBodyNotAllowed) before flushing the 101 it committed on —
// and the panic dissolves into a CLEAN 101 to the client. The honest
// behaviour, pinned here, is the committed path: re-panic, connection abort,
// hard client error. The sibling rows pin the carve-out's cheap consequences
// in the other two wrappers: statusWriter captures the 101 (a 101-then-panic
// meters the genuinely committed status="101", not the unwritten-case 500
// relabel), and cacheControlWriter commits on the 101 (no stamp — not a
// 200 — and the burned commit means a post-101 Write must not stamp either).
// That last one needs a live-map peek from inside the handler: the stdlib
// masks a late stamp at the wire all by itself (response.Header() snapshots
// the logically-written state on first access after commit), so the live map
// is the one observation point where a non-latching wrapper's stamp shows.
func TestWriterWrappers_101Latches(t *testing.T) {
	// Shared between build (before) and check (after) — rows run
	// sequentially, never in parallel. Same conventions as the table above.
	var meter101Before, meter500Before float64
	var sentryEvents func() []*sentry.Event
	meter := func(status string) float64 {
		return testutil.ToFloat64(requestsTotal.WithLabelValues("", http.MethodGet, status, ""))
	}

	cases := []struct {
		name string
		// build mounts the middleware that owns the wrapper under test.
		build func(t *testing.T, next http.Handler) http.Handler
		// finish completes the request after the shared WriteHeader(101).
		// It runs on the server goroutine — assert only, never require.
		finish func(t *testing.T, w http.ResponseWriter)
		// check sees the raw client outcome: a latched 101 makes some rows
		// end in a deliberate connection abort, not a response.
		check func(t *testing.T, res *http.Response, err error)
	}{
		{
			name: "commitWriter re-panics into a connection abort",
			build: func(t *testing.T, next http.Handler) http.Handler {
				sentryEvents = enableSentry(t).Events
				captureErrorLog(t)
				return sentryMiddleware(next)
			},
			finish: func(_ *testing.T, w http.ResponseWriter) {
				panic("exploded after the 101")
			},
			check: func(t *testing.T, res *http.Response, err error) {
				require.Error(t, err,
					"the 101 committed the response — the recovery must re-panic into a connection abort, not dissolve the panic into a clean 101")
				require.Len(t, sentryEvents(), 1, "one panic = one event — the re-panic path still captures")
			},
		},
		{
			name: "statusWriter meters the committed 101",
			build: func(t *testing.T, next http.Handler) http.Handler {
				meter101Before = meter("101")
				meter500Before = meter("500")
				return metricsMiddleware(next)
			},
			finish: func(_ *testing.T, w http.ResponseWriter) {
				panic("exploded after the 101")
			},
			check: func(t *testing.T, res *http.Response, err error) {
				require.Error(t, err,
					"no recovery layer in this chain — net/http aborts the connection")
				assert.Equal(t, meter101Before+1, meter("101"),
					`a 101 commits, so the capture keeps it: a 101-then-panic meters as status="101"`)
				assert.Equal(t, meter500Before, meter("500"),
					"the 500 relabel is only for panics with NO committed status — the 101 was committed")
			},
		},
		{
			name: "cacheControlWriter commits on the 101 and never stamps it",
			build: func(t *testing.T, next http.Handler) http.Handler {
				return cacheControl(600, next)
			},
			finish: func(t *testing.T, w http.ResponseWriter) {
				// Wire-dead after a 101 (the stdlib returns
				// http.ErrBodyNotAllowed), but a non-latching wrapper would
				// stamp the live header map here. assert (never require —
				// this runs on the server goroutine, and FailNow is not
				// goroutine-safe) on the map directly: the stdlib's own
				// Header() snapshot keeps such a stamp off the wire, so this
				// peek is what makes the row a mutant witness and not just a
				// wire pin — see the test doc.
				_, _ = w.Write([]byte("{}"))
				assert.Empty(t, w.Header().Get("Cache-Control"),
					"the 101 burned the commit — a post-101 Write must not stamp even the live map")
			},
			check: func(t *testing.T, res *http.Response, err error) {
				require.NoError(t, err, "a 101 without a panic is a clean response")
				require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)
				assert.Empty(t, res.Header.Get("Cache-Control"),
					"a 101 is not a 200 — the freshness signal must never reach the wire, not even via a post-101 Write")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewUnstartedServer(tc.build(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusSwitchingProtocols)
				tc.finish(t, w)
			})))
			// The panic rows re-raise into net/http's per-connection
			// recovery, which logs the abort — keep it out of test output.
			srv.Config.ErrorLog = log.New(io.Discard, "", 0)
			srv.Start()
			defer srv.Close()

			res, err := srv.Client().Get(srv.URL + "/")
			if res != nil {
				// A 101 body is the raw connection (protocol-switch
				// semantics, no Content-Length) — close it WITHOUT reading:
				// nothing more is coming, and a read could block on a socket
				// the server may hold open.
				require.NoError(t, res.Body.Close())
			}
			srv.Close() // wait out the handler so deferred recording has run
			tc.check(t, res, err)
		})
	}
}
