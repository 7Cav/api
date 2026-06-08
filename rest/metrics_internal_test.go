package rest

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ResponseController.Flush must keep working from inside the metrics layer.
// Since #174 statusWriter answers it directly (FlushError captures the
// flush-committed implied 200, then delegates); before that it tunneled
// through Unwrap. Observed against a real server connection for
// wire-faithfulness — NOT because a recorder would mask anything: the
// earlier rationale here ("the recorder implements Flusher directly and
// would mask the gap") was false. With the wrapper missing FlushError and
// Unwrap alike, the controller's method walk dead-ends AT statusWriter and
// fails loudly with ErrNotSupported — the recorder's Flusher below is
// unreachable either way, so it can mask nothing (#174 comment fix; same
// false-rationale family corrected in gzip_test.go on #167's branch).
func TestStatusWriter_ResponseControllerTunnelsThroughMetrics(t *testing.T) {
	flushErr := make(chan error, 1) // handler runs on the server goroutine
	// The probe mounts route-labeled, like every registration in the
	// assembled stack: the process-global registry's never-routed contract
	// (route="" ⇒ the auth tiers or a pre-routing panic, #173) is swept
	// post-run by TestMain, and an unlabeled probe would mint route="" 200.
	mux := http.NewServeMux()
	mux.Handle("GET /", routeLabel(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flushErr <- http.NewResponseController(w).Flush()
	})))
	h := metricsMiddleware(mux)

	srv := httptest.NewServer(h)
	defer srv.Close()

	res, err := srv.Client().Get(srv.URL + "/")
	require.NoError(t, err)
	res.Body.Close()
	require.NoError(t, <-flushErr,
		"ResponseController.Flush must reach the underlying writer via statusWriter.Unwrap")
}

// The verbs WITHOUT an explicit statusWriter method (deadline control here,
// as the witness; Hijacker rides the same route) reach the connection only
// through Unwrap — since FlushError landed (#174), the flush pin above no
// longer exercises Unwrap, so this is the test that keeps it from silently
// vanishing for every layer inside metrics. A real server is load-bearing
// here: a recorder has no deadlines to set (same pattern as the gzip-layer
// deadline pin in gzip_test.go).
func TestStatusWriter_ResponseControllerDeadlinesTunnelThroughMetrics(t *testing.T) {
	deadlineErr := make(chan error, 1) // handler runs on the server goroutine
	mux := http.NewServeMux()
	mux.Handle("GET /", routeLabel(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadlineErr <- http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Minute))
	})))
	h := metricsMiddleware(mux)

	srv := httptest.NewServer(h)
	defer srv.Close()

	res, err := srv.Client().Get(srv.URL + "/")
	require.NoError(t, err)
	res.Body.Close()
	require.NoError(t, <-deadlineErr,
		"deadline control must reach the underlying connection via statusWriter.Unwrap")
}

// The flush blind spot, closed (#174, from the #165 review): a
// ResponseController flush before any write commits the implied 200 on the
// wire, so the status capture must see it — without statusWriter.FlushError
// the flush tunnels past via Unwrap and a flush-then-panic meters 500
// against a wire-committed 200 (the panic relabel is only for the
// never-written case).
func TestStatusWriter_FlushThenPanicMetersCommitted200(t *testing.T) {
	flushErr := make(chan error, 1) // buffered: read after the panic recovery
	mux := http.NewServeMux()
	mux.Handle("GET /flush-boom", routeLabel(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flushErr <- http.NewResponseController(w).Flush()
		panic("handler exploded after the flush")
	})))
	h := metricsMiddleware(mux)

	counter200 := requestsTotal.WithLabelValues("GET /flush-boom", http.MethodGet, "200", "")
	counter500 := requestsTotal.WithLabelValues("GET /flush-boom", http.MethodGet, "500", "")
	before200 := testutil.ToFloat64(counter200)
	before500 := testutil.ToFloat64(counter500)

	req := httptest.NewRequest(http.MethodGet, "/flush-boom", nil)
	panicked := func() (p any) {
		defer func() { p = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), req)
		return nil
	}()
	require.Equal(t, "handler exploded after the flush", panicked,
		"the panic must propagate past the metrics layer")
	require.NoError(t, <-flushErr, "premise: the flush succeeded, so the implied 200 committed")
	require.Equal(t, before200+1, testutil.ToFloat64(counter200),
		`the flush committed the implied 200 on the wire — the capture must meter it as status="200"`)
	require.Equal(t, before500, testutil.ToFloat64(counter500),
		"the 500 relabel is only for panics with NOTHING committed — the flush committed")
}

// The genuine-failure side of the capture's errors.Is discriminator (#174;
// mirror of the commitWriter #164 and cacheControlWriter #172 pins): a first
// flush failing with a real I/O error still COMMITTED — on a real server
// net/http snapshots the headers onto the wire before the conn-write error
// returns — so the captured implied 200 must keep. A handler reacting to the
// failed flush with an error status changes nothing the wire can honor; the
// meter must report the 200 that went out, not the 503 that never did.
func TestStatusWriter_GenuineFlushErrorKeepsCommitted200(t *testing.T) {
	errConnReset := errors.New("conn reset")
	mux := http.NewServeMux()
	mux.Handle("GET /flush-genuine-fail", routeLabel(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := http.NewResponseController(w).Flush()
		assert.ErrorIs(t, err, errConnReset, "the base writer's genuine flush error must surface")
		assert.NotErrorIs(t, err, http.ErrNotSupported, "premise: a real failure, not the refusal")
		w.WriteHeader(http.StatusServiceUnavailable) // handler reacts — superfluous on a real wire
	})))
	h := metricsMiddleware(mux)

	counter200 := requestsTotal.WithLabelValues("GET /flush-genuine-fail", http.MethodGet, "200", "")
	counter503 := requestsTotal.WithLabelValues("GET /flush-genuine-fail", http.MethodGet, "503", "")
	before200 := testutil.ToFloat64(counter200)
	before503 := testutil.ToFloat64(counter503)

	req := httptest.NewRequest(http.MethodGet, "/flush-genuine-fail", nil)
	h.ServeHTTP(&flushErrorWriter{rr: httptest.NewRecorder(), err: errConnReset}, req)

	require.Equal(t, before200+1, testutil.ToFloat64(counter200),
		"a genuinely failed flush really committed — the captured 200 must keep")
	require.Equal(t, before503, testutil.ToFloat64(counter503),
		"the later error status never reached the wire — it must not meter")
}

// The refusal side of the discriminator (#174; the #164 rollback shape): on
// an unflushable chain the delegated flush returns http.ErrNotSupported —
// nothing reached the wire — so the capture this flush made must roll back,
// leaving the status label to the final write that CAN still happen. A
// capture left latched would meter 200 against a wire that went on to carry
// the handler's 503.
func TestStatusWriter_RefusedFlushRollsBackCaptureForFinalStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("GET /flush-refused", routeLabel(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := http.NewResponseController(w).Flush()
		assert.ErrorIs(t, err, http.ErrNotSupported, "premise: an unflushable base refuses the flush")
		w.WriteHeader(http.StatusServiceUnavailable)
	})))
	h := metricsMiddleware(mux)

	counter200 := requestsTotal.WithLabelValues("GET /flush-refused", http.MethodGet, "200", "")
	counter503 := requestsTotal.WithLabelValues("GET /flush-refused", http.MethodGet, "503", "")
	before200 := testutil.ToFloat64(counter200)
	before503 := testutil.ToFloat64(counter503)

	req := httptest.NewRequest(http.MethodGet, "/flush-refused", nil)
	h.ServeHTTP(&noFlushWriter{rr: httptest.NewRecorder()}, req)

	require.Equal(t, before503+1, testutil.ToFloat64(counter503),
		"the refused flush sent nothing — the final 503 is what the wire carries and what must meter")
	require.Equal(t, before200, testutil.ToFloat64(counter200),
		"a rolled-back capture must not leave a phantom 200 child")
}

// The KEEP side of the rollback's first-to-capture guard (#174 review; the
// symmetric pin to cachecontrol_internal_test.go's "FlushError after
// WriteHeader 500 does not stamp"): the rollback may only undo what THIS
// flush latched. A refused flush AFTER an explicit status must leave the
// capture alone — without the `captured &&` guard it wipes it, and a
// WriteHeader(503) → refused flush meters 200 against a wire-committed 5xx.
func TestStatusWriter_RefusedFlushAfterExplicitStatusKeepsCapture(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("GET /flush-refused-after-status", routeLabel(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		err := http.NewResponseController(w).Flush()
		assert.ErrorIs(t, err, http.ErrNotSupported, "premise: an unflushable base refuses the flush")
	})))
	h := metricsMiddleware(mux)

	counter200 := requestsTotal.WithLabelValues("GET /flush-refused-after-status", http.MethodGet, "200", "")
	counter503 := requestsTotal.WithLabelValues("GET /flush-refused-after-status", http.MethodGet, "503", "")
	before200 := testutil.ToFloat64(counter200)
	before503 := testutil.ToFloat64(counter503)

	req := httptest.NewRequest(http.MethodGet, "/flush-refused-after-status", nil)
	h.ServeHTTP(&noFlushWriter{rr: httptest.NewRecorder()}, req)

	require.Equal(t, before503+1, testutil.ToFloat64(counter503),
		"the 503 was captured before the refused flush — the rollback must not touch a capture it did not make")
	require.Equal(t, before200, testutil.ToFloat64(counter200),
		"a refused flush after an explicit status must not relabel the request 200")
}

// The #165 × #174 composition the issue comment named, pinned nowhere else:
// a forwarded non-latching 103 first (captures nothing, #165), then a flush
// commits the implied 200 (FlushError captures it, #174), then a panic — the
// meter must report the 200 the wire carries: not the never-written relabel
// 500, and never the 103 (a non-latching informational is never the final
// status label).
func TestStatusWriter_InformationalThenFlushThenPanicMetersCommitted200(t *testing.T) {
	flushErr := make(chan error, 1) // buffered: read after the panic recovery
	mux := http.NewServeMux()
	mux.Handle("GET /hints-flush-boom", routeLabel(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusEarlyHints) // forwarded, non-latching (#165)
		flushErr <- http.NewResponseController(w).Flush()
		panic("handler exploded after the 103 and the flush")
	})))
	h := metricsMiddleware(mux)

	counter200 := requestsTotal.WithLabelValues("GET /hints-flush-boom", http.MethodGet, "200", "")
	counter103 := requestsTotal.WithLabelValues("GET /hints-flush-boom", http.MethodGet, "103", "")
	counter500 := requestsTotal.WithLabelValues("GET /hints-flush-boom", http.MethodGet, "500", "")
	before200 := testutil.ToFloat64(counter200)
	before103 := testutil.ToFloat64(counter103)
	before500 := testutil.ToFloat64(counter500)

	req := httptest.NewRequest(http.MethodGet, "/hints-flush-boom", nil)
	panicked := func() (p any) {
		defer func() { p = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), req)
		return nil
	}()
	require.Equal(t, "handler exploded after the 103 and the flush", panicked,
		"the panic must propagate past the metrics layer")
	require.NoError(t, <-flushErr, "premise: the flush succeeded, so the implied 200 committed")
	require.Equal(t, before200+1, testutil.ToFloat64(counter200),
		`the flush after the 103 committed the implied 200 — it must meter as status="200"`)
	require.Equal(t, before103, testutil.ToFloat64(counter103),
		"a forwarded non-latching 103 must never become the final status label")
	require.Equal(t, before500, testutil.ToFloat64(counter500),
		"the 500 relabel is only for panics with NOTHING committed — the flush committed")
}

// A handler that commits a 200 (WriteHeader+Write) and THEN panics meters as
// status="200" — the status is already on the wire, so relabeling it 500
// would claim a response the client never received. The 500 relabel applies
// only to the never-written case (sw.code == 0). The panic still propagates
// unchanged.
func TestMetricsMiddleware_PanicAfterWritten200MetersAs200(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("GET /boom", routeLabel(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial body before the panic"))
		panic("handler exploded after the 200 was written")
	})))
	h := metricsMiddleware(mux)

	counter := requestsTotal.WithLabelValues("GET /boom", http.MethodGet, "200", "")
	before := testutil.ToFloat64(counter)

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	panicked := func() (p any) {
		defer func() { p = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), req)
		return nil
	}()
	require.Equal(t, "handler exploded after the 200 was written", panicked,
		"the panic must propagate past the metrics layer")
	require.Equal(t, before+1, testutil.ToFloat64(counter),
		`a panic after a written 200 must meter as status="200" under the route`)
}
