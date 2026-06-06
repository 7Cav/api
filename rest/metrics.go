package rest

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Prometheus instrumentation (#130, closes #92): request counter labeled
// route/method/status/key_id and duration histogram labeled route/method
// ONLY — per-key counters are the evidence base for the deliberately deferred
// rate-limiting decision; per-key histograms would multiply every bucket by
// the key population, so the histogram deliberately carries no key_id label
// (cardinality discipline, PRD #112).
//
// Label values are key-IDs, mux patterns, and methods clamped to the standard
// RFC 9110 set via methodLabel — bounded sets. Bearer material
// must NEVER appear in metric names or labels; the only key-derived value is
// the numeric key id the datastore validated (same rule as the Sentry key_id
// tag).
var (
	// metricsRegistry is the process-global registry behind MetricsHandler.
	// Package-level (not per-New) because metrics are process-cumulative:
	// every stack instance in the process reports into the one exposition.
	metricsRegistry = newMetricsRegistry()

	requestsTotal = promauto.With(metricsRegistry).NewCounterVec(
		prometheus.CounterOpts{
			Name: "api_http_requests_total",
			Help: "API requests by mux route pattern, method, HTTP status, and validated key id. " +
				"route is empty when auth rejected the request before routing; \"/\" is the " +
				"catch-all (unknown path / wrong method). key_id is empty when no key validated.",
		},
		[]string{"route", "method", "status", "key_id"},
	)

	requestDuration = promauto.With(metricsRegistry).NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "api_http_request_duration_seconds",
			Help:    "API request latency by mux route pattern and method. Deliberately NO key_id label (cardinality discipline).",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"route", "method"},
	)
)

func newMetricsRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return reg
}

// MetricsHandler serves the Prometheus exposition for this process. It must
// be mounted on the INTERNAL-ONLY listener, never in the public chain —
// per-key traffic counts are operational data.
//
// Deploy-config assertion (for the cutover slice #134, which mounts this on
// its own listener): the metrics port is internal-only, meaning
//
//  1. docker-compose must NOT publish it (no entry under the api service's
//     `ports:`), and
//  2. the reverse proxy must NOT route it (no location/upstream forwarding
//     to the metrics port).
//
// Verify at cutover, from outside the host:
//
//	curl https://<public-host>/metrics        → 401 (the public chain's auth
//	                                            tier answers; this handler is
//	                                            not mounted there)
//	curl http://<public-host>:<metrics-port>/ → connection refused/timeout
//	                                            (port unpublished + unrouted)
//
// and from inside the host network: curl localhost:<metrics-port>/metrics
// serves this exposition.
func MetricsHandler() http.Handler {
	return promhttp.HandlerFor(metricsRegistry, promhttp.HandlerOpts{})
}

// metricLabels is the mutable label-holder the metrics middleware shares with
// the inner layers (#130). The middleware cannot read either label off its own
// *http.Request after next.ServeHTTP returns: AuthMiddleware's r.WithContext
// CLONES the request — the mux sets Pattern on that inner clone and auth
// attaches the key to the inner context — so the outer request keeps
// Pattern == "" and carries no key. A pointer to this struct travels in the
// context instead (context values survive WithContext clones because the
// clone wraps the same parent context): AuthMiddleware fills keyID after
// validation, and the route slot is filled from r.Pattern inside the mux,
// where the matched pattern is actually set.
type metricLabels struct {
	route string // mux pattern, e.g. "GET /api/v1/milpacs/ranks"; "" if never routed
	keyID string // decimal key id, e.g. "101"; "" if no key validated
}

// metricLabelsContextKey is the private context key for *metricLabels.
type metricLabelsContextKey struct{}

// metricLabelsFromContext returns the request's label-holder, or nil when the
// metrics middleware is not in the chain (e.g. the legacy gateway, which
// reuses AuthMiddleware until cutover deletes it).
func metricLabelsFromContext(ctx context.Context) *metricLabels {
	v, _ := ctx.Value(metricLabelsContextKey{}).(*metricLabels)
	return v
}

// routeLabel fills the label-holder's route slot from r.Pattern — it must run
// INSIDE the mux (wrapping each registered handler), the only place the
// matched pattern is set on the request the handler sees.
func routeLabel(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if labels := metricLabelsFromContext(r.Context()); labels != nil {
			labels.route = r.Pattern
		}
		next.ServeHTTP(w, r)
	})
}

// metricsMiddleware records one counter increment (route/method/status/key_id)
// and one duration observation (route/method) per request. It sits outside
// auth (PRD chain order: sentry → metrics → auth → gzip → mux) so rejected
// requests are counted too — error rates are half the point (#92).
func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		labels := &metricLabels{}
		sw := &statusWriter{ResponseWriter: w}
		start := time.Now()

		// Recording is DEFERRED so a panicking handler still meters —
		// otherwise panic-per-request reads as a flat error rate while the
		// service burns (#92). A panicked request that never wrote a response
		// has sw.code == 0, which status() reports as the implied 200; label
		// it 500 instead (what net/http will actually answer). The panic is
		// re-raised AFTER recording (the inner defer fires as this deferred
		// func returns) so net/http — and #132's recovery layer once it lands
		// outside this one — sees semantics unchanged.
		defer func() {
			status := sw.status()
			if p := recover(); p != nil {
				if sw.code == 0 {
					status = http.StatusInternalServerError
				}
				defer panic(p)
			}
			requestsTotal.WithLabelValues(
				labels.route, methodLabel(r.Method), strconv.Itoa(status), labels.keyID,
			).Inc()
			requestDuration.WithLabelValues(labels.route, methodLabel(r.Method)).
				Observe(time.Since(start).Seconds())
		}()

		next.ServeHTTP(sw, r.WithContext(
			context.WithValue(r.Context(), metricLabelsContextKey{}, labels)))
	})
}

// methodLabel clamps the method label to the standard RFC 9110 method set.
// Any RFC 7230 token is a syntactically valid method that reaches handlers
// verbatim, and metrics sit OUTSIDE auth — without the clamp an
// unauthenticated client mints unbounded counter children (and a full bucket
// set of histogram children) per junk method. Everything non-standard meters
// as "OTHER".
func methodLabel(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions,
		http.MethodConnect, http.MethodTrace:
		return m
	default:
		return "OTHER"
	}
}

// statusWriter captures the response status for the counter's status label.
// A handler that writes a body without an explicit WriteHeader gets the
// net/http implied 200.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// status returns the captured status; a handler that never wrote anything is
// the implied 200, same as net/http reports it.
func (w *statusWriter) status() int {
	if w.code == 0 {
		return http.StatusOK
	}
	return w.code
}
