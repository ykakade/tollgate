package server

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/ykakade/tollgate/internal/telemetry"
)

// traceEnv returns a failure environment whose first target answers 529,
// with a span recorder.
func traceEnv(t *testing.T, api string) (*env, *tracetest.SpanRecorder) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	t.Cleanup(func() { _ = tp.Shutdown(t.Context()) })
	e, _, _ := failureEnvWith(t, api, api, "status:529", "", nil, func(o *Options) {
		o.Tracer = tp.Tracer(telemetry.TracerName)
	})
	return e, rec
}

func attrs(s sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	m := make(map[attribute.Key]attribute.Value)
	for _, kv := range s.Attributes() {
		m[kv.Key] = kv.Value
	}
	return m
}

// TestOneTracePerRequest is the M4 acceptance test: one request is one
// trace, with a child span for each upstream attempt.
func TestOneTracePerRequest(t *testing.T) {
	for _, api := range []string{"anthropic", "openai"} {
		t.Run(api, func(t *testing.T) {
			e, rec := traceEnv(t, api)
			const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
			const parentID = "00f067aa0ba902b7"
			req := e.request(t.Context(), failurePath(api), failureBody)
			req.Header.Set("Traceparent", "00-"+traceID+"-"+parentID+"-01")
			resp, _ := e.do(req)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d, want 200", resp.StatusCode)
			}

			spans := rec.Ended()
			var server sdktrace.ReadOnlySpan
			byName := make(map[string][]sdktrace.ReadOnlySpan)
			for _, s := range spans {
				if got := s.SpanContext().TraceID().String(); got != traceID {
					t.Errorf("span %q has trace %s, want %s", s.Name(), got, traceID)
				}
				byName[s.Name()] = append(byName[s.Name()], s)
				if s.SpanKind() == trace.SpanKindServer {
					server = s
				}
			}
			if server == nil {
				t.Fatalf("no server span in %d spans", len(spans))
			}
			if want := "POST " + failurePath(api); server.Name() != want {
				t.Errorf("server span name = %q, want %q", server.Name(), want)
			}
			if got := server.Parent().SpanID().String(); got != parentID {
				t.Errorf("server span parent = %s, want the traceparent span %s", got, parentID)
			}
			for _, name := range []string{"auth", "preflight", "limit check", "chat upstream-a", "chat upstream-b"} {
				got := byName[name]
				if len(got) != 1 {
					t.Errorf("%d spans with the name %q, want 1", len(got), name)
					continue
				}
				if got[0].Parent().SpanID() != server.SpanContext().SpanID() {
					t.Errorf("span %q is not a child of the server span", name)
				}
			}
			if len(spans) != 6 {
				t.Errorf("got %d spans, want 6", len(spans))
			}

			sa := attrs(server)
			for k, want := range map[attribute.Key]string{
				"tollgate.request_id":   resp.Header.Get("X-Request-Id"),
				"gen_ai.request.model":  "test",
				"gen_ai.response.model": "upstream-b",
				"tollgate.cache":        "bypass",
				"tollgate.tenant_id":    "personal",
				"tollgate.target":       "second/upstream-b",
			} {
				if got := sa[k].AsString(); got != want {
					t.Errorf("server span %s = %q, want %q", k, got, want)
				}
			}
			for k, want := range map[attribute.Key]int64{
				"tollgate.attempts":          2,
				"http.response.status_code":  200,
				"gen_ai.usage.output_tokens": 6,
			} {
				if got := sa[k].AsInt64(); got != want {
					t.Errorf("server span %s = %d, want %d", k, got, want)
				}
			}
			if _, ok := sa["gen_ai.usage.input_tokens"]; !ok {
				t.Error("server span has no gen_ai.usage.input_tokens")
			}
			if len(server.Events()) != 1 || server.Events()[0].Name != "retry" {
				t.Errorf("server span events = %v, want one retry event", server.Events())
			}

			first, second := byName["chat upstream-a"], byName["chat upstream-b"]
			if len(first) == 1 {
				fa := attrs(first[0])
				if fa["tollgate.attempt"].AsInt64() != 1 || fa["http.response.status_code"].AsInt64() != 529 || fa["error.type"].AsString() != "529" {
					t.Errorf("first attempt attributes = %v", first[0].Attributes())
				}
				if first[0].Status().Code != codes.Error {
					t.Errorf("first attempt status = %v, want error", first[0].Status())
				}
				if fa["gen_ai.provider.name"].AsString() != api || fa["gen_ai.operation.name"].AsString() != "chat" {
					t.Errorf("first attempt GenAI attributes = %v", first[0].Attributes())
				}
			}
			if len(second) == 1 {
				ba := attrs(second[0])
				if ba["tollgate.attempt"].AsInt64() != 2 || ba["gen_ai.usage.output_tokens"].AsInt64() != 6 || ba["gen_ai.response.model"].AsString() != "upstream-b" {
					t.Errorf("second attempt attributes = %v", second[0].Attributes())
				}
				if second[0].Status().Code == codes.Error {
					t.Errorf("second attempt status = %v, want not error", second[0].Status())
				}
				if second[0].SpanKind() != trace.SpanKindClient {
					t.Errorf("attempt span kind = %v, want client", second[0].SpanKind())
				}
			}
			// Spans never have prompt or response text (ADR 0010).
			for _, s := range spans {
				for _, kv := range s.Attributes() {
					if v := kv.Value.AsString(); strings.Contains(v, "hi") && kv.Key != "tollgate.request_id" || strings.Contains(v, "Hello") {
						t.Errorf("span %q attribute %s has message text: %q", s.Name(), kv.Key, v)
					}
				}
			}
		})
	}
}

func TestErrorSpan(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	e, _, _ := failureEnvWith(t, "anthropic", "anthropic", "status:529", "retry: {max_attempts: 1}", nil, func(o *Options) {
		o.Tracer = tp.Tracer(telemetry.TracerName)
	})
	resp, _ := e.post("/v1/messages", failureBody)
	if resp.StatusCode != 529 {
		t.Fatalf("status %d, want 529", resp.StatusCode)
	}
	for _, s := range rec.Ended() {
		if s.SpanKind() == trace.SpanKindServer {
			if s.Status().Code != codes.Error {
				t.Errorf("server span status = %v, want error for status 529", s.Status())
			}
			return
		}
	}
	t.Fatal("no server span")
}

func TestMetrics(t *testing.T) {
	m := telemetry.NewMetrics()
	e, _, _ := failureEnvWith(t, "anthropic", "anthropic", "status:529", "", nil, func(o *Options) {
		o.Metrics = m
	})
	resp, _ := e.post("/v1/messages", failureBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	ms, err := strconv.ParseFloat(resp.Header.Get("X-Tollgate-Overhead-Ms"), 64)
	if err != nil || ms < 0 {
		t.Errorf("x-tollgate-overhead-ms = %q, want a number of milliseconds", resp.Header.Get("X-Tollgate-Overhead-Ms"))
	}
	resp, _ = e.post("/v1/messages", `{"model":"other","messages":[]}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403", resp.StatusCode)
	}

	want := `
# HELP tollgate_requests_total Requests by route, provider, status class and cache result.
# TYPE tollgate_requests_total counter
tollgate_requests_total{cache="bypass",provider="none",route="none",status_class="4xx"} 1
tollgate_requests_total{cache="bypass",provider="second",route="test",status_class="2xx"} 1
# HELP tollgate_fallbacks_total Attempts that went to a different target after a failure, by route, old target, new target and reason.
# TYPE tollgate_fallbacks_total counter
tollgate_fallbacks_total{from="first/upstream-a",reason="overloaded",route="test",to="second/upstream-b"} 1
# HELP tollgate_retries_total Extra upstream attempts after a failed attempt, by the provider of the failed attempt.
# TYPE tollgate_retries_total counter
tollgate_retries_total{provider="first"} 1
# HELP tollgate_active_streams Event streams that Tollgate sends to clients now.
# TYPE tollgate_active_streams gauge
tollgate_active_streams 0
# HELP tollgate_breaker_state State of each circuit breaker: 0 closed, 1 half-open, 2 open.
# TYPE tollgate_breaker_state gauge
tollgate_breaker_state{model="upstream-a",provider="first",region=""} 0
tollgate_breaker_state{model="upstream-b",provider="second",region=""} 0
`
	names := []string{"tollgate_requests_total", "tollgate_fallbacks_total", "tollgate_retries_total", "tollgate_active_streams", "tollgate_breaker_state"}
	if err := testutil.GatherAndCompare(m.Gatherer(), strings.NewReader(want), names...); err != nil {
		t.Error(err)
	}
	for _, name := range []string{
		"tollgate_request_duration_seconds",
		"tollgate_time_to_first_token_seconds",
		"tollgate_inter_token_latency_seconds",
		"tollgate_gateway_overhead_seconds",
	} {
		// One series each: the 403 has no attempt, so no TTFT or overhead.
		// Its duration has the labels route="none" and provider="none".
		n, err := testutil.GatherAndCount(m.Gatherer(), name)
		if err != nil {
			t.Fatal(err)
		}
		wantN := 1
		if name == "tollgate_request_duration_seconds" {
			wantN = 2
		}
		if n != wantN {
			t.Errorf("%s has %d series, want %d", name, n, wantN)
		}
	}
	tokens, err := testutil.GatherAndCount(m.Gatherer(), "tollgate_tokens_total")
	if err != nil || tokens != 2 {
		t.Errorf("tollgate_tokens_total has %d series (err %v), want input and output", tokens, err)
	}
}

func TestLimitRejectionMetric(t *testing.T) {
	m := telemetry.NewMetrics()
	e, _, _ := failureEnvWith(t, "anthropic", "anthropic", "ok", "", nil, func(o *Options) {
		o.Metrics = m
		o.Config.Tenants[0].Limits.RequestsPerMinute = 1
		o.Config.Tenants[0].Limits.TokensPerMinute = 0
	})
	// New reads the tenant limits, so the edit above applies.
	for range 2 {
		e.post("/v1/messages", failureBody)
	}
	want := `
# HELP tollgate_limit_rejections_total Requests that a rate limit or a hard budget refused, by kind: requests, tokens or budget.
# TYPE tollgate_limit_rejections_total counter
tollgate_limit_rejections_total{kind="budget"} 0
tollgate_limit_rejections_total{kind="requests"} 1
tollgate_limit_rejections_total{kind="tokens"} 0
`
	if err := testutil.GatherAndCompare(m.Gatherer(), strings.NewReader(want), "tollgate_limit_rejections_total"); err != nil {
		t.Error(err)
	}
}
