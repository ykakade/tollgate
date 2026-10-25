package telemetry

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// exportTimeout is the longest time of one export request.
const exportTimeout = 10 * time.Second

// maxResponseBytes is the most of an export response that the exporter
// reads.
const maxResponseBytes = 64 << 10

// Exporter sends spans to an OTLP/HTTP receiver in the OTLP JSON encoding
// (ADR 0026). It implements sdktrace.SpanExporter. Use NewExporter to make
// one.
//
// The exporter does not use the official OTLP exporter, because that
// package links gRPC into the binary, which Tollgate does not use.
type Exporter struct {
	url     string
	headers map[string]string
	client  *http.Client
	logger  *slog.Logger

	mu      sync.Mutex
	failing bool
	closed  bool
}

// NewExporter returns an exporter that posts to the traces URL url, with
// the extra request headers. If logger is nil, the exporter logs nothing.
func NewExporter(url string, headers map[string]string, logger *slog.Logger) *Exporter {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Exporter{
		url:     url,
		headers: headers,
		client:  &http.Client{Timeout: exportTimeout},
		logger:  logger,
	}
}

// ExportSpans sends spans in one request. A failed export drops the spans
// and logs one warning until an export works again. It returns an error
// only for an exporter that was shut down, so the SDK does not log each
// failure a second time.
func (e *Exporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed {
		return errors.New("the trace exporter is shut down")
	}
	if len(spans) == 0 {
		return nil
	}
	err := e.send(ctx, spans)
	e.mu.Lock()
	defer e.mu.Unlock()
	switch {
	case err != nil && !e.failing:
		e.failing = true
		e.logger.WarnContext(ctx, "spans not sent to the OTLP receiver. Tollgate drops spans until the receiver is available.", "url", e.url, "spans", len(spans), "error", err.Error())
	case err == nil && e.failing:
		e.failing = false
		e.logger.InfoContext(ctx, "spans sent to the OTLP receiver again", "url", e.url)
	}
	return nil
}

func (e *Exporter) send(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	body, err := json.Marshal(encodeSpans(spans))
	if err != nil {
		return fmt.Errorf("encode spans: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("make export request: %w", err)
	}
	for k, v := range e.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("send spans: %w", err)
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("the receiver answered status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// Shutdown stops the exporter. Later exports fail.
func (e *Exporter) Shutdown(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	e.client.CloseIdleConnections()
	return nil
}

// ParseHeaders reads the value of OTEL_EXPORTER_OTLP_HEADERS: a list of
// key=value pairs, separated by commas, with URL-encoded values.
func ParseHeaders(s string) (map[string]string, error) {
	out := make(map[string]string)
	for pair := range strings.SplitSeq(s, ",") {
		if strings.TrimSpace(pair) == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("the header %q has no key=value form", pair)
		}
		val, err := url.PathUnescape(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("the value of the header %q is not URL-encoded: %w", k, err)
		}
		out[k] = val
	}
	return out, nil
}

// The types below are the OTLP JSON encoding of an export request. See
// https://opentelemetry.io/docs/specs/otlp/#json-protobuf-encoding: the IDs
// are hex text, enums are numbers, and 64-bit numbers are text.

type otlpRequest struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

type otlpResourceSpans struct {
	Resource   otlpResource     `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
	SchemaURL  string           `json:"schemaUrl,omitzero"`
}

type otlpResource struct {
	Attributes []otlpKeyValue `json:"attributes"`
}

type otlpScopeSpans struct {
	Scope     otlpScope  `json:"scope"`
	Spans     []otlpSpan `json:"spans"`
	SchemaURL string     `json:"schemaUrl,omitzero"`
}

type otlpScope struct {
	Name    string `json:"name"`
	Version string `json:"version,omitzero"`
}

type otlpSpan struct {
	TraceID                string         `json:"traceId"`
	SpanID                 string         `json:"spanId"`
	TraceState             string         `json:"traceState,omitzero"`
	ParentSpanID           string         `json:"parentSpanId,omitzero"`
	Flags                  uint32         `json:"flags,omitzero"`
	Name                   string         `json:"name"`
	Kind                   int            `json:"kind"`
	StartTimeUnixNano      string         `json:"startTimeUnixNano"`
	EndTimeUnixNano        string         `json:"endTimeUnixNano"`
	Attributes             []otlpKeyValue `json:"attributes,omitzero"`
	DroppedAttributesCount int            `json:"droppedAttributesCount,omitzero"`
	Events                 []otlpEvent    `json:"events,omitzero"`
	DroppedEventsCount     int            `json:"droppedEventsCount,omitzero"`
	Links                  []otlpLink     `json:"links,omitzero"`
	DroppedLinksCount      int            `json:"droppedLinksCount,omitzero"`
	Status                 otlpStatus     `json:"status"`
}

type otlpEvent struct {
	TimeUnixNano string         `json:"timeUnixNano"`
	Name         string         `json:"name"`
	Attributes   []otlpKeyValue `json:"attributes,omitzero"`
}

type otlpLink struct {
	TraceID    string         `json:"traceId"`
	SpanID     string         `json:"spanId"`
	TraceState string         `json:"traceState,omitzero"`
	Attributes []otlpKeyValue `json:"attributes,omitzero"`
}

type otlpStatus struct {
	Message string `json:"message,omitzero"`
	Code    int    `json:"code,omitzero"`
}

type otlpKeyValue struct {
	Key   string       `json:"key"`
	Value otlpAnyValue `json:"value"`
}

type otlpAnyValue struct {
	StringValue *string         `json:"stringValue,omitzero"`
	BoolValue   *bool           `json:"boolValue,omitzero"`
	IntValue    *string         `json:"intValue,omitzero"`
	DoubleValue *float64        `json:"doubleValue,omitzero"`
	ArrayValue  *otlpArrayValue `json:"arrayValue,omitzero"`
}

type otlpArrayValue struct {
	Values []otlpAnyValue `json:"values"`
}

// OTLP status codes. They differ from the values of the codes package.
const (
	otlpStatusOK    = 1
	otlpStatusError = 2
)

// flagHasIsRemote and flagIsRemote are the span flags that tell whether the
// parent span is remote.
const (
	flagHasIsRemote = 0x100
	flagIsRemote    = 0x200
)

type scopeKey struct {
	res   *resource.Resource
	scope instrumentation.Scope
}

// encodeSpans groups spans by resource and scope.
func encodeSpans(spans []sdktrace.ReadOnlySpan) otlpRequest {
	var req otlpRequest
	resIndex := make(map[*resource.Resource]int)
	scopeIndex := make(map[scopeKey]int)
	for _, s := range spans {
		res := s.Resource()
		ri, ok := resIndex[res]
		if !ok {
			ri = len(req.ResourceSpans)
			resIndex[res] = ri
			var rs otlpResourceSpans
			if res != nil {
				rs.Resource.Attributes = encodeAttrs(res.Attributes())
				rs.SchemaURL = res.SchemaURL()
			}
			req.ResourceSpans = append(req.ResourceSpans, rs)
		}
		rs := &req.ResourceSpans[ri]
		key := scopeKey{res, s.InstrumentationScope()}
		si, ok := scopeIndex[key]
		if !ok {
			si = len(rs.ScopeSpans)
			scopeIndex[key] = si
			sc := s.InstrumentationScope()
			rs.ScopeSpans = append(rs.ScopeSpans, otlpScopeSpans{
				Scope:     otlpScope{Name: sc.Name, Version: sc.Version},
				SchemaURL: sc.SchemaURL,
			})
		}
		rs.ScopeSpans[si].Spans = append(rs.ScopeSpans[si].Spans, encodeSpan(s))
	}
	return req
}

func encodeSpan(s sdktrace.ReadOnlySpan) otlpSpan {
	sc := s.SpanContext()
	out := otlpSpan{
		TraceID:                sc.TraceID().String(),
		SpanID:                 sc.SpanID().String(),
		TraceState:             sc.TraceState().String(),
		Flags:                  uint32(sc.TraceFlags()) | flagHasIsRemote,
		Name:                   s.Name(),
		Kind:                   int(s.SpanKind()),
		StartTimeUnixNano:      nanos(s.StartTime()),
		EndTimeUnixNano:        nanos(s.EndTime()),
		Attributes:             encodeAttrs(s.Attributes()),
		DroppedAttributesCount: s.DroppedAttributes(),
		DroppedEventsCount:     s.DroppedEvents(),
		DroppedLinksCount:      s.DroppedLinks(),
	}
	if p := s.Parent(); p.SpanID().IsValid() {
		out.ParentSpanID = p.SpanID().String()
		if p.IsRemote() {
			out.Flags |= flagIsRemote
		}
	}
	for _, ev := range s.Events() {
		out.Events = append(out.Events, otlpEvent{TimeUnixNano: nanos(ev.Time), Name: ev.Name, Attributes: encodeAttrs(ev.Attributes)})
	}
	for _, l := range s.Links() {
		out.Links = append(out.Links, otlpLink{
			TraceID:    l.SpanContext.TraceID().String(),
			SpanID:     l.SpanContext.SpanID().String(),
			TraceState: l.SpanContext.TraceState().String(),
			Attributes: encodeAttrs(l.Attributes),
		})
	}
	switch st := s.Status(); st.Code {
	case codes.Error:
		out.Status = otlpStatus{Code: otlpStatusError, Message: st.Description}
	case codes.Ok:
		out.Status = otlpStatus{Code: otlpStatusOK}
	}
	return out
}

func nanos(t time.Time) string {
	if t.IsZero() {
		return "0"
	}
	return strconv.FormatInt(t.UnixNano(), 10)
}

// encodeAttrs returns nil for no attributes, so omitzero fields leave
// them out.
func encodeAttrs(kvs []attribute.KeyValue) []otlpKeyValue {
	if len(kvs) == 0 {
		return nil
	}
	out := make([]otlpKeyValue, 0, len(kvs))
	for _, kv := range kvs {
		out = append(out, otlpKeyValue{Key: string(kv.Key), Value: encodeValue(kv.Value)})
	}
	return out
}

func encodeValue(v attribute.Value) otlpAnyValue {
	switch v.Type() {
	case attribute.BOOL:
		b := v.AsBool()
		return otlpAnyValue{BoolValue: &b}
	case attribute.INT64:
		return intValue(v.AsInt64())
	case attribute.FLOAT64:
		f := v.AsFloat64()
		return otlpAnyValue{DoubleValue: &f}
	case attribute.BOOLSLICE:
		return arrayOf(v.AsBoolSlice(), func(b bool) otlpAnyValue { return otlpAnyValue{BoolValue: &b} })
	case attribute.INT64SLICE:
		return arrayOf(v.AsInt64Slice(), intValue)
	case attribute.FLOAT64SLICE:
		return arrayOf(v.AsFloat64Slice(), func(f float64) otlpAnyValue { return otlpAnyValue{DoubleValue: &f} })
	case attribute.STRINGSLICE:
		return arrayOf(v.AsStringSlice(), func(s string) otlpAnyValue { return otlpAnyValue{StringValue: &s} })
	default:
		s := v.AsString()
		return otlpAnyValue{StringValue: &s}
	}
}

func intValue(n int64) otlpAnyValue {
	s := strconv.FormatInt(n, 10)
	return otlpAnyValue{IntValue: &s}
}

func arrayOf[T any](vals []T, f func(T) otlpAnyValue) otlpAnyValue {
	arr := &otlpArrayValue{Values: make([]otlpAnyValue, 0, len(vals))}
	for _, v := range vals {
		arr.Values = append(arr.Values, f(v))
	}
	return otlpAnyValue{ArrayValue: arr}
}
