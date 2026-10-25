package telemetry

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func testSpans(t *testing.T) []sdktrace.ReadOnlySpan {
	t.Helper()
	tid, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	parent, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	child, _ := trace.SpanIDFromHex("0000000000000002")
	start := time.Unix(1_700_000_000, 5)
	res := resource.NewSchemaless(attribute.String("service.name", "tollgate"))
	scope := instrumentation.Scope{Name: TracerName, Version: "v1"}
	stubs := tracetest.SpanStubs{
		{
			Name:        "POST /v1/messages",
			SpanContext: trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: parent, TraceFlags: trace.FlagsSampled}),
			SpanKind:    trace.SpanKindServer,
			StartTime:   start,
			EndTime:     start.Add(time.Second),
			Attributes: []attribute.KeyValue{
				attribute.String("s", "v"),
				attribute.Int64("i", 1<<60),
				attribute.Bool("b", true),
				attribute.Float64("f", 0.5),
				attribute.StringSlice("ss", []string{"a"}),
				attribute.Int64Slice("is", []int64{2}),
				attribute.BoolSlice("bs", []bool{false}),
				attribute.Float64Slice("fs", []float64{1.5}),
			},
			Events:               []sdktrace.Event{{Name: "retry", Time: start, Attributes: []attribute.KeyValue{attribute.Int("tollgate.attempt", 1)}}},
			Status:               sdktrace.Status{Code: codes.Error, Description: "Bad Gateway"},
			Resource:             res,
			InstrumentationScope: scope,
		},
		{
			Name:                 "chat m",
			SpanContext:          trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: child, TraceFlags: trace.FlagsSampled}),
			Parent:               trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: parent, Remote: true}),
			SpanKind:             trace.SpanKindClient,
			StartTime:            start,
			EndTime:              start.Add(time.Millisecond),
			Links:                []sdktrace.Link{{SpanContext: trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: parent})}},
			Status:               sdktrace.Status{Code: codes.Ok},
			Resource:             res,
			InstrumentationScope: scope,
		},
	}
	return stubs.Snapshots()
}

func TestEncodeSpans(t *testing.T) {
	b, err := json.Marshal(encodeSpans(testSpans(t)))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"tollgate"}}]},` +
		`"scopeSpans":[{"scope":{"name":"github.com/ykakade/tollgate","version":"v1"},"spans":[` +
		`{"traceId":"4bf92f3577b34da6a3ce929d0e0e4736","spanId":"00f067aa0ba902b7","flags":257,"name":"POST /v1/messages","kind":2,` +
		`"startTimeUnixNano":"1700000000000000005","endTimeUnixNano":"1700000001000000005","attributes":[` +
		`{"key":"s","value":{"stringValue":"v"}},{"key":"i","value":{"intValue":"1152921504606846976"}},{"key":"b","value":{"boolValue":true}},` +
		`{"key":"f","value":{"doubleValue":0.5}},{"key":"ss","value":{"arrayValue":{"values":[{"stringValue":"a"}]}}},` +
		`{"key":"is","value":{"arrayValue":{"values":[{"intValue":"2"}]}}},{"key":"bs","value":{"arrayValue":{"values":[{"boolValue":false}]}}},` +
		`{"key":"fs","value":{"arrayValue":{"values":[{"doubleValue":1.5}]}}}],` +
		`"events":[{"timeUnixNano":"1700000000000000005","name":"retry","attributes":[{"key":"tollgate.attempt","value":{"intValue":"1"}}]}],` +
		`"status":{"message":"Bad Gateway","code":2}},` +
		`{"traceId":"4bf92f3577b34da6a3ce929d0e0e4736","spanId":"0000000000000002","parentSpanId":"00f067aa0ba902b7","flags":769,"name":"chat m","kind":3,` +
		`"startTimeUnixNano":"1700000000000000005","endTimeUnixNano":"1700000000001000005",` +
		`"links":[{"traceId":"4bf92f3577b34da6a3ce929d0e0e4736","spanId":"00f067aa0ba902b7"}],"status":{"code":1}}]}]}]}`
	if diff := cmp.Diff(want, string(b)); diff != "" {
		t.Errorf("encoding differs (-want +got):\n%s", diff)
	}
}

// syncBuffer is a bytes.Buffer that is safe for concurrent use.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestExporter(t *testing.T) {
	var (
		mu     sync.Mutex
		status = http.StatusOK
		bodies []string
		auth   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		bodies = append(bodies, r.Header.Get("Content-Type")+" "+string(b))
		auth = r.Header.Get("Authorization")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "{}")
	}))
	defer srv.Close()
	logs := &syncBuffer{}
	e := NewExporter(srv.URL+"/v1/traces", map[string]string{"Authorization": "Bearer x"}, slog.New(slog.NewTextHandler(logs, nil)))
	ctx := t.Context()
	spans := testSpans(t)

	if err := e.ExportSpans(ctx, spans); err != nil {
		t.Fatal(err)
	}
	if err := e.ExportSpans(ctx, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(bodies) != 1 || !strings.HasPrefix(bodies[0], "application/json {\"resourceSpans\"") || auth != "Bearer x" {
		t.Errorf("requests = %q, authorization %q", bodies, auth)
	}
	status = http.StatusServiceUnavailable
	mu.Unlock()

	// Two failures log one warning. A success logs one line.
	for range 2 {
		if err := e.ExportSpans(ctx, spans); err != nil {
			t.Fatalf("a failed export returned %v, want nil", err)
		}
	}
	if n := strings.Count(logs.String(), "spans not sent"); n != 1 {
		t.Errorf("%d warnings, want 1:\n%s", n, logs)
	}
	if !strings.Contains(logs.String(), "status 503") {
		t.Errorf("warning does not name the status:\n%s", logs)
	}
	mu.Lock()
	status = http.StatusOK
	mu.Unlock()
	if err := e.ExportSpans(ctx, spans); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "spans sent to the OTLP receiver again") {
		t.Errorf("no recovery line:\n%s", logs)
	}

	if err := e.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.ExportSpans(ctx, spans); err == nil {
		t.Error("export after shutdown returned nil")
	}
}

func TestExporterUnreachable(t *testing.T) {
	logs := &syncBuffer{}
	e := NewExporter("http://127.0.0.1:1/v1/traces", nil, slog.New(slog.NewTextHandler(logs, nil)))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := e.ExportSpans(ctx, testSpans(t)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "spans not sent") {
		t.Errorf("no warning:\n%s", logs)
	}
	if err := NewExporter("://bad", nil, nil).ExportSpans(ctx, testSpans(t)); err != nil {
		t.Errorf("bad URL: %v, want nil and a warning", err)
	}
}

func TestParseHeaders(t *testing.T) {
	got, err := ParseHeaders(" api-key = a%20b ,x=1,, ")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(map[string]string{"api-key": "a b", "x": "1"}, got); diff != "" {
		t.Errorf("headers differ:\n%s", diff)
	}
	for _, bad := range []string{"novalue", "=v", "k=%zz"} {
		if _, err := ParseHeaders(bad); err == nil {
			t.Errorf("ParseHeaders(%q) = nil error", bad)
		}
	}
}

func TestTracerProviderHeaders(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "bad")
	if _, err := NewTracerProvider(t.Context(), "http://localhost:4318", "v", nil); err == nil {
		t.Error("NewTracerProvider accepted headers that are not valid")
	}
	if _, err := NewTracerProvider(t.Context(), "http://[::1", "v", nil); err == nil {
		t.Error("NewTracerProvider accepted an endpoint that is not valid")
	}
}
