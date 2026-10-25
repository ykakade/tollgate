package telemetry

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestStatusClass(t *testing.T) {
	for status, want := range map[int]string{0: "none", 200: "2xx", 299: "2xx", 404: "4xx", 499: "4xx", 529: "5xx", 600: "none"} {
		if got := statusClass(status); got != want {
			t.Errorf("statusClass(%d) = %q, want %q", status, got, want)
		}
	}
}

func TestObserveRequest(t *testing.T) {
	m := NewMetrics()
	m.ObserveRequest(Request{Route: "r", Provider: "p", Status: 200, Cache: "bypass", Duration: time.Second, TTFT: -1, ITL: -1, Overhead: -1})
	m.ObserveRequest(Request{
		Route: "r", Provider: "p", Status: 200, Cache: "bypass",
		Duration: 2 * time.Second, TTFT: time.Second, ITL: 10 * time.Millisecond, Overhead: time.Millisecond,
		Usage: true, InputTokens: 10, OutputTokens: 5, CacheReadTokens: 100, CacheWriteTokens: 0, CostMicroUSD: 42,
	})
	want := `
# HELP tollgate_tokens_total Tokens by route, provider and kind: input, output, cache_read or cache_write.
# TYPE tollgate_tokens_total counter
tollgate_tokens_total{kind="cache_read",provider="p",route="r"} 100
tollgate_tokens_total{kind="input",provider="p",route="r"} 10
tollgate_tokens_total{kind="output",provider="p",route="r"} 5
# HELP tollgate_cost_micro_usd_total Cost of the requests in micro-USD, from the price table.
# TYPE tollgate_cost_micro_usd_total counter
tollgate_cost_micro_usd_total{provider="p",route="r"} 42
# HELP tollgate_requests_total Requests by route, provider, status class and cache result.
# TYPE tollgate_requests_total counter
tollgate_requests_total{cache="bypass",provider="p",route="r",status_class="2xx"} 2
`
	if err := testutil.GatherAndCompare(m.Gatherer(), strings.NewReader(want), "tollgate_tokens_total", "tollgate_cost_micro_usd_total", "tollgate_requests_total"); err != nil {
		t.Error(err)
	}
	for name, want := range map[string]int{
		"tollgate_time_to_first_token_seconds": 1,
		"tollgate_inter_token_latency_seconds": 1,
		"tollgate_gateway_overhead_seconds":    1,
		"tollgate_request_duration_seconds":    1,
	} {
		if n, err := testutil.GatherAndCount(m.Gatherer(), name); err != nil || n != want {
			t.Errorf("%s: %d series (err %v), want %d", name, n, err, want)
		}
	}
}

func TestCollectors(t *testing.T) {
	m := NewMetrics()
	// No sources yet: the collectors report nothing.
	if n, err := testutil.GatherAndCount(m.Gatherer(), "tollgate_breaker_state", "tollgate_usage_events_dropped_total"); err != nil || n != 0 {
		t.Fatalf("got %d series (err %v), want 0", n, err)
	}
	state := 0
	m.WatchBreakers(func() []BreakerState {
		return []BreakerState{{Provider: "vertex", Model: "m", Region: "us-east5", State: state}}
	})
	var dropped atomic.Int64
	m.WatchUsageDropped("jsonl", dropped.Load)
	state = 2
	dropped.Store(3)
	m.Retry("r", "p", "p/a", "p/a", "server_error")
	m.RetryBudgetExhausted("p")
	m.StoreError("redis", "admit")
	m.StreamStarted()
	want := `
# HELP tollgate_breaker_state State of each circuit breaker: 0 closed, 1 half-open, 2 open.
# TYPE tollgate_breaker_state gauge
tollgate_breaker_state{model="m",provider="vertex",region="us-east5"} 2
# HELP tollgate_usage_events_dropped_total Usage events that Tollgate did not write, because the queue was full or the sink failed.
# TYPE tollgate_usage_events_dropped_total counter
tollgate_usage_events_dropped_total{sink="jsonl"} 3
# HELP tollgate_retries_total Extra upstream attempts after a failed attempt, by the provider of the failed attempt.
# TYPE tollgate_retries_total counter
tollgate_retries_total{provider="p"} 1
# HELP tollgate_retry_budget_exhausted_total Requests that did not get an extra attempt because the retry budget was empty.
# TYPE tollgate_retry_budget_exhausted_total counter
tollgate_retry_budget_exhausted_total{provider="p"} 1
# HELP tollgate_store_errors_total Failed calls to the limit store, by store and operation.
# TYPE tollgate_store_errors_total counter
tollgate_store_errors_total{op="admit",store="redis"} 1
# HELP tollgate_active_streams Event streams that Tollgate sends to clients now.
# TYPE tollgate_active_streams gauge
tollgate_active_streams 1
`
	names := []string{
		"tollgate_breaker_state", "tollgate_usage_events_dropped_total", "tollgate_retries_total",
		"tollgate_retry_budget_exhausted_total", "tollgate_store_errors_total", "tollgate_active_streams",
	}
	if err := testutil.GatherAndCompare(m.Gatherer(), strings.NewReader(want), names...); err != nil {
		t.Error(err)
	}
	// A retry to the same target is not a fallback.
	if n, _ := testutil.GatherAndCount(m.Gatherer(), "tollgate_fallbacks_total"); n != 0 {
		t.Errorf("tollgate_fallbacks_total has %d series, want 0", n)
	}
}

func TestHandler(t *testing.T) {
	m := NewMetrics()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{"tollgate_active_streams 0", "go_goroutines", `tollgate_limit_rejections_total{kind="budget"} 0`} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics does not contain %q", want)
		}
	}
}

func TestTracesURL(t *testing.T) {
	for in, want := range map[string]string{
		"http://localhost:4318":            "http://localhost:4318/v1/traces",
		"http://localhost:4318/":           "http://localhost:4318/v1/traces",
		"https://otel.example/base":        "https://otel.example/base/v1/traces",
		"http://localhost:4318/v1/traces":  "http://localhost:4318/v1/traces",
		"https://otel.example/x/v1/traces": "https://otel.example/x/v1/traces",
	} {
		got, err := TracesURL(in)
		if err != nil || got != want {
			t.Errorf("TracesURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := TracesURL("http://[::1"); err == nil {
		t.Error("TracesURL accepted a URL that is not valid")
	}
}

func TestProviderName(t *testing.T) {
	for kind, want := range map[string]string{"anthropic": "anthropic", "openai": "openai", "vertex": "gcp.vertex_ai", "ollama": "ollama"} {
		if got := ProviderName(kind); got != want {
			t.Errorf("ProviderName(%q) = %q, want %q", kind, got, want)
		}
	}
}

// TestTracerProviderExports sends one span to a fake OTLP/HTTP receiver.
func TestTracerProviderExports(t *testing.T) {
	got := make(chan string, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		select {
		case got <- r.Method + " " + r.URL.Path + " " + r.Header.Get("Content-Type") + " " + string(b):
		default:
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer receiver.Close()

	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment.name=test")
	tp, err := NewTracerProvider(t.Context(), receiver.URL, "v0.0.0-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, span := tp.Tracer(TracerName).Start(t.Context(), "POST /v1/messages")
	span.End()
	if err := tp.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case req := <-got:
		for _, want := range []string{"POST /v1/traces application/json", "POST /v1/messages", "tollgate", "v0.0.0-test", "deployment.environment.name"} {
			if !strings.Contains(req, want) {
				t.Errorf("export request does not contain %q", want)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the receiver got no spans")
	}
}
