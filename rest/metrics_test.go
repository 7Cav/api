package rest_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/7cav/api/rest"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scrapeMetrics mounts rest.MetricsHandler() on its OWN listener — the
// internal-only mount (#130); the public chain never serves it — performs one
// scrape, and parses the exposition into metric families by name.
func scrapeMetrics(t *testing.T) map[string]*dto.MetricFamily {
	t.Helper()

	srv := httptest.NewServer(rest.MetricsHandler())
	defer srv.Close()

	res, err := srv.Client().Get(srv.URL + "/metrics")
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(res.Body)
	require.NoError(t, err, "exposition must parse as Prometheus text format")
	return families
}

// counterValue returns the current value of the named counter child whose
// label set matches exactly, or 0 when the child does not exist yet. The
// registry is process-global (production semantics), so tests assert DELTAS.
func counterValue(t *testing.T, families map[string]*dto.MetricFamily, name string, labels map[string]string) float64 {
	t.Helper()

	mf, ok := families[name]
	if !ok {
		return 0
	}
	for _, m := range mf.GetMetric() {
		if len(m.GetLabel()) != len(labels) {
			continue
		}
		match := true
		for _, lp := range m.GetLabel() {
			if labels[lp.GetName()] != lp.GetValue() {
				match = false
				break
			}
		}
		if match {
			return m.GetCounter().GetValue()
		}
	}
	return 0
}

// do drives one request through the public stack and returns the recorder.
func do(h http.Handler, method, path, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// The tracer: an authenticated 200 increments the request counter with all
// four labels — route (the mux pattern), method, status, and key_id (the
// validated key's id, never the bearer token). Per-key counters are the
// evidence base for the deferred rate-limiting decision (PRD #112).
func TestMetrics_CounterLabelsRouteMethodStatusKeyId(t *testing.T) {
	h := newStack(t)
	labels := map[string]string{
		"route":  "GET /api/v1/milpacs/ranks",
		"method": "GET",
		"status": "200",
		"key_id": "101",
	}

	before := counterValue(t, scrapeMetrics(t), "api_http_requests_total", labels)

	rr := do(h, http.MethodGet, "/api/v1/milpacs/ranks", "cav7_readkey")
	require.Equal(t, http.StatusOK, rr.Code)

	after := counterValue(t, scrapeMetrics(t), "api_http_requests_total", labels)
	assert.Equal(t, before+1, after, "one 200 must increment the fully-labeled counter child by one")
}

// keep imports referenced while later slices land
var _ = io.Discard
