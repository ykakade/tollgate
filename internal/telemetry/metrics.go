// Package telemetry holds the Prometheus metrics and the OpenTelemetry
// tracer of Tollgate.
//
// Metrics answer "is it healthy", traces answer "why was this request
// slow". Each Metrics value has its own registry, so tests and more than
// one server in a process do not share state.
package telemetry

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Namespace is the prefix of all Tollgate metrics.
const Namespace = "tollgate"

// None is the label value for a route or provider that a request did not
// reach, for example a request with a key that is not valid.
const None = "none"

// Token kinds of the tokens_total metric.
const (
	TokensInput      = "input"
	TokensOutput     = "output"
	TokensCacheRead  = "cache_read"
	TokensCacheWrite = "cache_write"
)

// Limit kinds of the limit_rejections_total metric.
const (
	LimitRequests = "requests"
	LimitTokens   = "tokens"
	LimitBudget   = "budget"
)

// Histogram buckets, in seconds.
var (
	// durationBuckets cover agent streams of up to the default total
	// timeout of 15 minutes.
	durationBuckets = []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600, 900}
	ttftBuckets     = []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 3, 5, 7.5, 10, 15, 30, 60}
	itlBuckets      = []float64{0.001, 0.0025, 0.005, 0.01, 0.02, 0.03, 0.05, 0.075, 0.1, 0.25, 0.5, 1}
	// overheadBuckets start at 50 µs: the overhead target is in
	// fractions of a millisecond.
	overheadBuckets = []float64{0.00005, 0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1}
)

// Metrics holds the Tollgate metrics and their registry. Use NewMetrics to
// make one. It is safe for concurrent use.
type Metrics struct {
	reg *prometheus.Registry

	requests        *prometheus.CounterVec
	duration        *prometheus.HistogramVec
	ttft            *prometheus.HistogramVec
	itl             *prometheus.HistogramVec
	overhead        *prometheus.HistogramVec
	tokens          *prometheus.CounterVec
	cost            *prometheus.CounterVec
	fallbacks       *prometheus.CounterVec
	retries         *prometheus.CounterVec
	retryExhausted  *prometheus.CounterVec
	limitRejections *prometheus.CounterVec
	activeStreams   prometheus.Gauge
	storeErrors     *prometheus.CounterVec

	breakers *breakerCollector
	dropped  *droppedCollector
}

// NewMetrics returns metrics with a new registry. The registry also has
// the Go runtime and process collectors.
func NewMetrics() *Metrics {
	labels := []string{"route", "provider"}
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace, Name: "requests_total",
			Help: "Requests by route, provider, status class and cache result.",
		}, []string{"route", "provider", "status_class", "cache"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace, Name: "request_duration_seconds",
			Help:    "Time from the start of a request to the end of its response.",
			Buckets: durationBuckets,
		}, labels),
		ttft: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace, Name: "time_to_first_token_seconds",
			Help:    "Time from the start of a request to the first response body byte.",
			Buckets: ttftBuckets,
		}, labels),
		itl: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace, Name: "inter_token_latency_seconds",
			Help:    "Mean time between output tokens of a stream. One value for each stream.",
			Buckets: itlBuckets,
		}, labels),
		overhead: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace, Name: "gateway_overhead_seconds",
			Help:    "Time in Tollgate before the first upstream request, without the time to read the request body.",
			Buckets: overheadBuckets,
		}, []string{"route"}),
		tokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace, Name: "tokens_total",
			Help: "Tokens by route, provider and kind: input, output, cache_read or cache_write.",
		}, []string{"route", "provider", "kind"}),
		cost: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace, Name: "cost_micro_usd_total",
			Help: "Cost of the requests in micro-USD, from the price table.",
		}, labels),
		fallbacks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace, Name: "fallbacks_total",
			Help: "Attempts that went to a different target after a failure, by route, old target, new target and reason.",
		}, []string{"route", "from", "to", "reason"}),
		retries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace, Name: "retries_total",
			Help: "Extra upstream attempts after a failed attempt, by the provider of the failed attempt.",
		}, []string{"provider"}),
		retryExhausted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace, Name: "retry_budget_exhausted_total",
			Help: "Requests that did not get an extra attempt because the retry budget was empty.",
		}, []string{"provider"}),
		limitRejections: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace, Name: "limit_rejections_total",
			Help: "Requests that a rate limit or a hard budget refused, by kind: requests, tokens or budget.",
		}, []string{"kind"}),
		activeStreams: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace, Name: "active_streams",
			Help: "Event streams that Tollgate sends to clients now.",
		}),
		storeErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace, Name: "store_errors_total",
			Help: "Failed calls to the limit store, by store and operation.",
		}, []string{"store", "op"}),
		breakers: newBreakerCollector(),
		dropped:  newDroppedCollector(),
	}
	for _, kind := range []string{LimitRequests, LimitTokens, LimitBudget} {
		m.limitRejections.WithLabelValues(kind)
	}
	m.reg.MustRegister(
		m.requests, m.duration, m.ttft, m.itl, m.overhead, m.tokens, m.cost,
		m.fallbacks, m.retries, m.retryExhausted, m.limitRejections,
		m.activeStreams, m.storeErrors, m.breakers, m.dropped,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// Handler returns the handler of the /metrics endpoint.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{Registry: m.reg})
}

// Gatherer returns the registry, for tests.
func (m *Metrics) Gatherer() prometheus.Gatherer { return m.reg }

// Request is the record of one finished request for the metrics.
type Request struct {
	// Route is the model name of the route, or None.
	Route string
	// Provider is the provider of the last attempt, or None.
	Provider string
	Status   int
	// Cache is "hit", "miss" or "bypass".
	Cache    string
	Duration time.Duration
	// TTFT, ITL and Overhead are negative if the request has no value.
	TTFT     time.Duration
	ITL      time.Duration
	Overhead time.Duration
	// Usage is true if the request has token usage.
	Usage            bool
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	CostMicroUSD     int64
}

// ObserveRequest records a finished request.
func (m *Metrics) ObserveRequest(r Request) {
	route, prov := orNone(r.Route), orNone(r.Provider)
	m.requests.WithLabelValues(route, prov, statusClass(r.Status), r.Cache).Inc()
	m.duration.WithLabelValues(route, prov).Observe(r.Duration.Seconds())
	if r.TTFT >= 0 {
		m.ttft.WithLabelValues(route, prov).Observe(r.TTFT.Seconds())
	}
	if r.ITL >= 0 {
		m.itl.WithLabelValues(route, prov).Observe(r.ITL.Seconds())
	}
	if r.Overhead >= 0 {
		m.overhead.WithLabelValues(route).Observe(r.Overhead.Seconds())
	}
	if !r.Usage {
		return
	}
	for _, t := range []struct {
		kind string
		n    int64
	}{
		{TokensInput, r.InputTokens},
		{TokensOutput, r.OutputTokens},
		{TokensCacheRead, r.CacheReadTokens},
		{TokensCacheWrite, r.CacheWriteTokens},
	} {
		if t.n > 0 {
			m.tokens.WithLabelValues(route, prov, t.kind).Add(float64(t.n))
		}
	}
	if r.CostMicroUSD > 0 {
		m.cost.WithLabelValues(route, prov).Add(float64(r.CostMicroUSD))
	}
}

// Retry records an extra attempt after a failed attempt to provider. If
// the extra attempt goes to a different target, Retry also records a
// fallback from the target from to the target to.
func (m *Metrics) Retry(route, provider, from, to, reason string) {
	m.retries.WithLabelValues(provider).Inc()
	if from != to {
		m.fallbacks.WithLabelValues(route, from, to, reason).Inc()
	}
}

// RetryBudgetExhausted records a request that did not get an extra
// attempt, because the retry budget was empty.
func (m *Metrics) RetryBudgetExhausted(provider string) {
	m.retryExhausted.WithLabelValues(provider).Inc()
}

// LimitRejection records a request that a limit refused. The kind is
// LimitRequests, LimitTokens or LimitBudget.
func (m *Metrics) LimitRejection(kind string) {
	m.limitRejections.WithLabelValues(kind).Inc()
}

// StreamStarted records an event stream that started. Call StreamEnded
// when it ends.
func (m *Metrics) StreamStarted() { m.activeStreams.Inc() }

// StreamEnded records the end of a stream that StreamStarted recorded.
func (m *Metrics) StreamEnded() { m.activeStreams.Dec() }

// StoreError records a failed call to a limit store.
func (m *Metrics) StoreError(store, op string) {
	m.storeErrors.WithLabelValues(store, op).Inc()
}

// BreakerState is the state of one circuit breaker. State is 0 for
// closed, 1 for half-open and 2 for open.
type BreakerState struct {
	Provider, Model, Region string
	State                   int
}

// WatchBreakers sets the function that gives the breaker states at each
// scrape. A new call replaces the function.
func (m *Metrics) WatchBreakers(f func() []BreakerState) { m.breakers.set(f) }

// WatchUsageDropped sets the function that gives the number of dropped
// usage events of a sink at each scrape.
func (m *Metrics) WatchUsageDropped(sink string, f func() int64) { m.dropped.set(sink, f) }

func orNone(s string) string {
	if s == "" {
		return None
	}
	return s
}

// statusClass returns "2xx" for 200 to 299, and so on. Status 0 is "none".
func statusClass(status int) string {
	if status < 100 || status > 599 {
		return None
	}
	return strconv.Itoa(status/100) + "xx"
}

// breakerCollector reports the breaker_state gauge from a function, so the
// value is correct at the scrape time.
type breakerCollector struct {
	desc *prometheus.Desc
	mu   sync.Mutex
	f    func() []BreakerState
}

func newBreakerCollector() *breakerCollector {
	return &breakerCollector{desc: prometheus.NewDesc(
		prometheus.BuildFQName(Namespace, "", "breaker_state"),
		"State of each circuit breaker: 0 closed, 1 half-open, 2 open.",
		[]string{"provider", "model", "region"}, nil,
	)}
}

func (c *breakerCollector) set(f func() []BreakerState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.f = f
}

func (c *breakerCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c *breakerCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	f := c.f
	c.mu.Unlock()
	if f == nil {
		return
	}
	for _, b := range f() {
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, float64(b.State), b.Provider, b.Model, b.Region)
	}
}

// droppedCollector reports the usage_events_dropped_total counter from the
// counts of the usage queues.
type droppedCollector struct {
	desc  *prometheus.Desc
	mu    sync.Mutex
	sinks map[string]func() int64
}

func newDroppedCollector() *droppedCollector {
	return &droppedCollector{
		desc: prometheus.NewDesc(
			prometheus.BuildFQName(Namespace, "", "usage_events_dropped_total"),
			"Usage events that Tollgate did not write, because the queue was full or the sink failed.",
			[]string{"sink"}, nil,
		),
		sinks: make(map[string]func() int64),
	}
}

func (c *droppedCollector) set(sink string, f func() int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sinks[sink] = f
}

func (c *droppedCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c *droppedCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for sink, f := range c.sinks {
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.CounterValue, float64(f()), sink)
	}
}
