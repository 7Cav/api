package rest_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
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

// histogramSampleCount returns the observation count of the named histogram
// child whose label set matches exactly, or 0 when the child does not exist.
func histogramSampleCount(t *testing.T, families map[string]*dto.MetricFamily, name string, labels map[string]string) uint64 {
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
			return m.GetHistogram().GetSampleCount()
		}
	}
	return 0
}

// The duration histogram observes once per request, labeled route/method
// ONLY — no key_id label anywhere in the family (cardinality discipline: a
// per-key histogram would multiply every bucket by the key population).
func TestMetrics_DurationHistogramRouteMethodOnly(t *testing.T) {
	h := newStack(t)
	labels := map[string]string{
		"route":  "GET /api/v1/milpacs/ranks",
		"method": "GET",
	}

	before := histogramSampleCount(t, scrapeMetrics(t), "api_http_request_duration_seconds", labels)

	rr := do(h, http.MethodGet, "/api/v1/milpacs/ranks", "cav7_readkey")
	require.Equal(t, http.StatusOK, rr.Code)

	families := scrapeMetrics(t)
	after := histogramSampleCount(t, families, "api_http_request_duration_seconds", labels)
	assert.Equal(t, before+1, after, "one request must observe the route/method histogram once")

	mf := families["api_http_request_duration_seconds"]
	require.NotNil(t, mf)
	for _, m := range mf.GetMetric() {
		for _, lp := range m.GetLabel() {
			labelName := lp.GetName()
			assert.Contains(t, []string{"route", "method"}, labelName,
				"histogram label %q breaks cardinality discipline: route/method only, never key_id", labelName)
		}
	}
}

// Metrics sit OUTSIDE auth (PRD chain order), so rejected requests are
// counted too — error rates are half the point of #92. A request auth
// rejects never reaches routing and never validates a key: route and key_id
// stay empty, distinct from the catch-all's "/".
func TestMetrics_AuthRejectedRequestCountsWithEmptyRouteAndKey(t *testing.T) {
	h := newStack(t)
	labels := map[string]string{
		"route":  "",
		"method": "GET",
		"status": "401",
		"key_id": "",
	}

	before := counterValue(t, scrapeMetrics(t), "api_http_requests_total", labels)

	rr := do(h, http.MethodGet, "/api/v1/milpacs/ranks", "") // no credentials
	require.Equal(t, http.StatusUnauthorized, rr.Code)

	after := counterValue(t, scrapeMetrics(t), "api_http_requests_total", labels)
	assert.Equal(t, before+1, after, "401s must meter with empty route/key_id labels")
}

// A scope denial happens INSIDE the mux (per-route requireScope), after auth
// validated the key: the 403 meters under the route it was denied on, with
// the denied key's id — per-key error attribution.
func TestMetrics_ScopeDenialCountsUnderRouteWithKeyId(t *testing.T) {
	h := newStack(t)
	labels := map[string]string{
		"route":  "GET /api/v1/milpacs/ranks",
		"method": "GET",
		"status": "403",
		"key_id": "102",
	}

	before := counterValue(t, scrapeMetrics(t), "api_http_requests_total", labels)

	rr := do(h, http.MethodGet, "/api/v1/milpacs/ranks", "cav7_ticketskey") // read:tickets ≠ read
	require.Equal(t, http.StatusForbidden, rr.Code)

	after := counterValue(t, scrapeMetrics(t), "api_http_requests_total", labels)
	assert.Equal(t, before+1, after, "scope 403s must meter under the denied route with the key id")
}

// Unknown paths land on the route-labeled catch-all: bounded "/" route label
// (never the raw request path — unknown-path cardinality is attacker-
// controlled), still attributed to the authenticated key.
func TestMetrics_UnknownPathCountsUnderCatchAllPattern(t *testing.T) {
	h := newStack(t)
	labels := map[string]string{
		"route":  "/",
		"method": "GET",
		"status": "404",
		"key_id": "101",
	}

	before := counterValue(t, scrapeMetrics(t), "api_http_requests_total", labels)

	rr := do(h, http.MethodGet, "/api/v1/does/not/exist", "cav7_readkey")
	require.Equal(t, http.StatusNotFound, rr.Code)

	after := counterValue(t, scrapeMetrics(t), "api_http_requests_total", labels)
	assert.Equal(t, before+1, after, "404s must meter under the catch-all pattern, not the raw path")
}

// Metrics sit OUTSIDE auth, so unauthenticated clients reach the metering
// layer — and any RFC 7230 token is a syntactically valid method that arrives
// at handlers verbatim. The method label must clamp to the standard RFC 9110
// set: an exotic method meters as "OTHER" and must NOT mint a raw-token
// counter or histogram child (unbounded, attacker-controlled cardinality).
func TestMetrics_ExoticMethodClampsToOther(t *testing.T) {
	h := newStack(t)
	const junk = "ZZZ9X7Q4JUNKMETHOD"
	labels := map[string]string{
		"route":  "",
		"method": "OTHER",
		"status": "401",
		"key_id": "",
	}

	before := counterValue(t, scrapeMetrics(t), "api_http_requests_total", labels)

	rr := do(h, junk, "/api/v1/milpacs/ranks", "") // unauthenticated: auth answers 401
	require.Equal(t, http.StatusUnauthorized, rr.Code)

	families := scrapeMetrics(t)
	after := counterValue(t, families, "api_http_requests_total", labels)
	assert.Equal(t, before+1, after, `exotic methods must meter under method="OTHER"`)

	for _, name := range []string{"api_http_requests_total", "api_http_request_duration_seconds"} {
		mf, ok := families[name]
		require.True(t, ok)
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				assert.NotEqual(t, junk, lp.GetValue(),
					"%s minted a raw-token method child — unbounded cardinality", name)
			}
		}
	}
}

// A panicking handler must not bypass metering: recording is deferred, so the
// request still increments the counter — as status="500" when nothing was
// written (the dashboard-honesty case: panic-per-request must not flatline
// error rates while the service burns, #92). The panic itself must propagate
// unchanged so the sentry recovery layer outside this one (#132) — and
// net/http when sentry is disabled, as here — sees identical semantics.
func TestMetrics_PanickingHandlerMetersAs500AndPanicPropagates(t *testing.T) {
	h := rest.New(&fakeDatastore{findAllRanks: func() ([]*proto.RankExpanded, error) {
		panic("datastore exploded")
	}}, &stubReferenceCache{})
	labels := map[string]string{
		"route":  "GET /api/v1/milpacs/ranks",
		"method": "GET",
		"status": "500",
		"key_id": "101",
	}

	before := counterValue(t, scrapeMetrics(t), "api_http_requests_total", labels)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	panicked := func() (p any) {
		defer func() { p = recover() }()
		h.ServeHTTP(rr, req)
		return nil
	}()
	require.Equal(t, "datastore exploded", panicked, "the panic must propagate past the metrics layer")

	after := counterValue(t, scrapeMetrics(t), "api_http_requests_total", labels)
	assert.Equal(t, before+1, after, `a panicked never-written response must meter as status="500"`)
}

// The headline error-rate tier: a handler-written 500 (datastore outage
// surfacing through the writeError choke point) meters under the route it
// failed on, with the caller's key id — per-key error attribution.
func TestMetrics_HandlerError500MetersUnderRouteAndKey(t *testing.T) {
	h := rest.New(&fakeDatastore{findAllRanks: func() ([]*proto.RankExpanded, error) {
		return nil, io.ErrUnexpectedEOF
	}}, &stubReferenceCache{})
	labels := map[string]string{
		"route":  "GET /api/v1/milpacs/ranks",
		"method": "GET",
		"status": "500",
		"key_id": "101",
	}

	before := counterValue(t, scrapeMetrics(t), "api_http_requests_total", labels)

	rr := do(h, http.MethodGet, "/api/v1/milpacs/ranks", "cav7_readkey")
	require.Equal(t, http.StatusInternalServerError, rr.Code)

	after := counterValue(t, scrapeMetrics(t), "api_http_requests_total", labels)
	assert.Equal(t, before+1, after, "handler 500s must meter under the route with the key id")
}

// The auth-tier 503: ValidateApiKey failing (datastore outage during auth)
// answers Unavailable BEFORE routing and before any key validates — route and
// key_id stay empty, same as the 401 tier and distinct from the catch-all "/".
func TestMetrics_AuthDatastoreOutage503MetersWithEmptyRouteAndKey(t *testing.T) {
	h := rest.New(&fakeDatastore{validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
		return nil, io.ErrUnexpectedEOF
	}}, &stubReferenceCache{})
	labels := map[string]string{
		"route":  "",
		"method": "GET",
		"status": "503",
		"key_id": "",
	}

	before := counterValue(t, scrapeMetrics(t), "api_http_requests_total", labels)

	rr := do(h, http.MethodGet, "/api/v1/milpacs/ranks", "cav7_readkey")
	require.Equal(t, http.StatusServiceUnavailable, rr.Code)

	after := counterValue(t, scrapeMetrics(t), "api_http_requests_total", labels)
	assert.Equal(t, before+1, after, "auth-tier 503s must meter with empty route/key_id labels")
}

// The 405 half of the documented wrong-method ruling (#125): a POST to a
// known GET route lands on the route-labeled catch-all — it meters under the
// bounded "/" pattern (the fallback answered, no registered pattern matched),
// authenticated, status 405.
func TestMetrics_WrongMethod405MetersUnderCatchAll(t *testing.T) {
	h := newStack(t)
	labels := map[string]string{
		"route":  "/",
		"method": "POST",
		"status": "405",
		"key_id": "101",
	}

	before := counterValue(t, scrapeMetrics(t), "api_http_requests_total", labels)

	rr := do(h, http.MethodPost, "/api/v1/milpacs/ranks", "cav7_readkey")
	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)

	after := counterValue(t, scrapeMetrics(t), "api_http_requests_total", labels)
	assert.Equal(t, before+1, after, "wrong-method 405s must meter under the catch-all pattern")
}

// counterFamilyTotal sums every child of the named counter family — the
// family-wide request count, label-set independent.
func counterFamilyTotal(families map[string]*dto.MetricFamily, name string) float64 {
	var total float64
	if mf, ok := families[name]; ok {
		for _, m := range mf.GetMetric() {
			total += m.GetCounter().GetValue()
		}
	}
	return total
}

// Scraping the exposition must not meter itself: MetricsHandler is its own
// internal-only mount, never wrapped in the public chain — two consecutive
// scrapes with no API traffic in between leave the request-counter family
// total unchanged.
func TestMetrics_SelfScrapeDoesNotIncrementRequestCounter(t *testing.T) {
	first := counterFamilyTotal(scrapeMetrics(t), "api_http_requests_total")
	second := counterFamilyTotal(scrapeMetrics(t), "api_http_requests_total")
	assert.Equal(t, first, second, "the metrics scrape must not count itself as API traffic")
}

// Chain order seen from the metrics side: gzip sits INSIDE metrics, so a
// gzipped 200 still meters as status="200" — the statusWriter observes the
// status before the body ever hits the gzip writer.
func TestMetrics_GzippedRequestMetersStatus200(t *testing.T) {
	h := newStack(t)
	labels := map[string]string{
		"route":  "GET /api/v1/milpacs/ranks",
		"method": "GET",
		"status": "200",
		"key_id": "101",
	}

	before := counterValue(t, scrapeMetrics(t), "api_http_requests_total", labels)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "gzip", rr.Result().Header.Get("Content-Encoding"))

	after := counterValue(t, scrapeMetrics(t), "api_http_requests_total", labels)
	assert.Equal(t, before+1, after, `a gzipped 200 must meter as status="200"`)
}

// HEAD is a supported read verb (the mux matches HEAD against GET patterns):
// it meters under the GET route pattern with method="HEAD" — its own bounded
// child, not folded into GET.
func TestMetrics_HEADMetersUnderGetRoutePattern(t *testing.T) {
	h := newStack(t)
	labels := map[string]string{
		"route":  "GET /api/v1/milpacs/ranks",
		"method": "HEAD",
		"status": "200",
		"key_id": "101",
	}
	histLabels := map[string]string{
		"route":  "GET /api/v1/milpacs/ranks",
		"method": "HEAD",
	}

	families := scrapeMetrics(t)
	before := counterValue(t, families, "api_http_requests_total", labels)
	histBefore := histogramSampleCount(t, families, "api_http_request_duration_seconds", histLabels)

	rr := do(h, http.MethodHead, "/api/v1/milpacs/ranks", "cav7_readkey")
	require.Equal(t, http.StatusOK, rr.Code)

	families = scrapeMetrics(t)
	after := counterValue(t, families, "api_http_requests_total", labels)
	histAfter := histogramSampleCount(t, families, "api_http_request_duration_seconds", histLabels)
	assert.Equal(t, before+1, after, `HEAD must meter under the GET pattern with method="HEAD"`)
	assert.Equal(t, histBefore+1, histAfter, "HEAD must observe the histogram under its own method child")
}

// Concurrent requests across tiers must each meter exactly once — the
// counter vec, the label-holder plumbing, and the statusWriter are all
// per-request or internally synchronized; run with -race to verify.
func TestMetrics_ConcurrentMixedTierRequestsAllMeter(t *testing.T) {
	h := newStack(t)

	const perTier = 8
	tiers := []struct {
		method, path, bearer string
		wantCode             int
		labels               map[string]string
	}{
		{http.MethodGet, "/api/v1/milpacs/ranks", "cav7_readkey", http.StatusOK,
			map[string]string{"route": "GET /api/v1/milpacs/ranks", "method": "GET", "status": "200", "key_id": "101"}},
		{http.MethodGet, "/api/v1/milpacs/ranks", "", http.StatusUnauthorized,
			map[string]string{"route": "", "method": "GET", "status": "401", "key_id": ""}},
		{http.MethodGet, "/api/v1/milpacs/ranks", "cav7_ticketskey", http.StatusForbidden,
			map[string]string{"route": "GET /api/v1/milpacs/ranks", "method": "GET", "status": "403", "key_id": "102"}},
		{http.MethodGet, "/api/v1/does/not/exist", "cav7_readkey", http.StatusNotFound,
			map[string]string{"route": "/", "method": "GET", "status": "404", "key_id": "101"}},
	}

	before := scrapeMetrics(t)
	befores := make([]float64, len(tiers))
	for i, tier := range tiers {
		befores[i] = counterValue(t, before, "api_http_requests_total", tier.labels)
	}

	var wg sync.WaitGroup
	codes := make([]int, len(tiers)*perTier)
	for i := 0; i < len(tiers)*perTier; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tier := tiers[i%len(tiers)]
			codes[i] = do(h, tier.method, tier.path, tier.bearer).Code
		}(i)
	}
	wg.Wait()

	for i, code := range codes {
		require.Equal(t, tiers[i%len(tiers)].wantCode, code, "request %d answered the wrong tier", i)
	}

	after := scrapeMetrics(t)
	for i, tier := range tiers {
		assert.Equal(t, befores[i]+perTier,
			counterValue(t, after, "api_http_requests_total", tier.labels),
			"tier %d (%s %s → %d) must meter exactly %d times", i, tier.method, tier.path, tier.wantCode, perTier)
	}
}

// The exposition carries the default Go runtime and process collectors
// alongside the request metrics.
func TestMetrics_RuntimeCollectorsServed(t *testing.T) {
	families := scrapeMetrics(t)

	assert.Contains(t, families, "go_goroutines", "default Go collector must be registered")
	assert.Contains(t, families, "go_memstats_alloc_bytes", "default Go collector must be registered")
	assert.Contains(t, families, "process_cpu_seconds_total", "process collector must be registered")
}

// Bearer material must NEVER appear in metric names or labels — the only
// key-derived label value is the validated numeric key id. Drive every auth
// tier with its real token, then sweep the ENTIRE raw exposition (names,
// labels, help text) for the cav7_ key prefix.
func TestMetrics_BearerMaterialAbsentFromExposition(t *testing.T) {
	h := newStack(t)

	for _, bearer := range []string{
		"cav7_readkey",    // valid, scoped — 200
		"cav7_ticketskey", // valid, wrong scope — 403
		"cav7_noscopekey", // valid, no scopes — 403
		"cav7_unknownkey", // unknown — generic 401
		"",                // missing header — scheme 401
	} {
		do(h, http.MethodGet, "/api/v1/milpacs/ranks", bearer)
	}

	// The 503 tier (ValidateApiKey errors mid-outage) sees the bearer too —
	// drive it so the sweep covers every tier that handles key material.
	outage := rest.New(&fakeDatastore{validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
		return nil, io.ErrUnexpectedEOF
	}}, &stubReferenceCache{})
	do(outage, http.MethodGet, "/api/v1/milpacs/ranks", "cav7_readkey")

	srv := httptest.NewServer(rest.MetricsHandler())
	defer srv.Close()
	res, err := srv.Client().Get(srv.URL + "/metrics")
	require.NoError(t, err)
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	assert.NotContains(t, string(raw), "cav7_",
		"bearer material leaked into the exposition — key_id is the only permitted key-derived value")
	assert.Contains(t, string(raw), `key_id="101"`,
		"the validated key id (not the token) is how consumers are attributed")
}

// The exposition is served on its OWN listener only (#130: a port the
// reverse proxy never routes and compose never publishes — deploy-config
// assertion for cutover documented on MetricsHandler). Through the PUBLIC
// chain, /metrics is just another path: auth answers 401 without
// credentials, and an authenticated probe gets the JSON 404 — never the
// exposition.
func TestMetrics_NotServedThroughPublicChain(t *testing.T) {
	h := newStack(t)

	rr := do(h, http.MethodGet, "/metrics", "")
	assert.Equal(t, http.StatusUnauthorized, rr.Code, "public chain: auth precedes everything")

	rr = do(h, http.MethodGet, "/metrics", "cav7_readkey")
	assert.Equal(t, http.StatusNotFound, rr.Code, "public chain mounts no metrics route")
	assert.JSONEq(t, `{"code":5,"message":"Not Found","details":[]}`, rr.Body.String())

	// The internal mount serves it: same process, separate handler/listener.
	srv := httptest.NewServer(rest.MetricsHandler())
	defer srv.Close()
	res, err := srv.Client().Get(srv.URL + "/metrics")
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "api_http_requests_total", "internal listener serves the exposition")
}
