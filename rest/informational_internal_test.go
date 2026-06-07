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
// forwarded 103 captured via httptrace on the client side.

import (
	"io"
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

// The predicate's boundaries pin the wire definition exactly: 1xx is
// [100, 199], nothing more — a guard that drifts to e.g. (100, 199] would
// silently latch on a forwarded 100 Continue.
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
				// The shared informational writes: both class boundaries the
				// stdlib forwards without latching (100 opens it, 103 is the
				// one seen in the wild) — so an inlined-range drift in any
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
