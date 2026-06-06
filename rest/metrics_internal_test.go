package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

// statusWriter wraps every routed ResponseWriter, so it must expose Unwrap
// for http.ResponseController to tunnel through it — otherwise Flusher,
// Hijacker, and deadline control silently vanish for every inner layer.
// Observed behaviorally: a handler flushing via ResponseController against a
// real server connection (the recorder implements Flusher directly and would
// mask the gap).
func TestStatusWriter_ResponseControllerTunnelsThroughMetrics(t *testing.T) {
	flushErr := make(chan error, 1) // handler runs on the server goroutine
	h := metricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flushErr <- http.NewResponseController(w).Flush()
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()

	res, err := srv.Client().Get(srv.URL + "/")
	require.NoError(t, err)
	res.Body.Close()
	require.NoError(t, <-flushErr,
		"ResponseController.Flush must reach the underlying writer via statusWriter.Unwrap")
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
