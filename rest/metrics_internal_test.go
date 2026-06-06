package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
