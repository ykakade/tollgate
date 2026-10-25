package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/ykakade/tollgate/internal/apierror"
	"github.com/ykakade/tollgate/internal/auth"
	"github.com/ykakade/tollgate/internal/config"
	"github.com/ykakade/tollgate/internal/preflight"
	"github.com/ykakade/tollgate/internal/provider"
	"github.com/ykakade/tollgate/internal/provider/anthropic"
	"github.com/ykakade/tollgate/internal/provider/openai"
	"github.com/ykakade/tollgate/internal/redact"
	"github.com/ykakade/tollgate/internal/relay"
	"github.com/ykakade/tollgate/internal/respcache"
	"github.com/ykakade/tollgate/internal/telemetry"
)

// statusClientClosed is the status in the access log when the client
// disconnected before the response started. It is the nginx convention.
// Tollgate never sends it.
const statusClientClosed = 499

// countTokensPath is the path of the Anthropic token count endpoint.
//
//nolint:gosec // This is an API path, not a credential.
const countTokensPath = "/v1/messages/count_tokens"

// endpoint is one proxy path.
type endpoint struct {
	api  config.API
	path string
	// metered is true if responses of the endpoint have token usage.
	metered bool
}

// exchange holds the state of one request, for the response and the access
// log line.
type exchange struct {
	w     http.ResponseWriter
	r     *http.Request
	api   config.API
	id    string
	start time.Time

	// span is the server span of the request.
	span       trace.Span
	status     int
	identity   auth.Identity
	routeModel string
	// route is the model of the route that the request uses, or "" if it
	// has none. Unlike routeModel, it comes from the configuration, so it
	// is a bounded metric label.
	route         string
	provider      string
	providerModel string
	// responseModel is the model that the response names, or "".
	responseModel string
	stream        bool
	attempts      int
	// served is the index of the target of the last attempt, or -1.
	served     int
	readTime   time.Duration
	usage      provider.Usage
	usageKnown bool
	estimated  bool
	ttft       time.Duration
	// itl is the mean time between output tokens of a stream, or -1.
	itl        time.Duration
	overhead   time.Duration
	bytesIn    int
	bytesOut   int64
	upstreamID string
	err        error

	session string
	// sessionDerived is true if the session ID is a hash of the prompt
	// prefix (ADR 0027).
	sessionDerived bool
	// prevTarget is the index of the target of the previous request of
	// the session, or -1. switched is true if the request went to a
	// different target, and cacheLoss is then the upper bound of the extra
	// cost (ADR 0028).
	prevTarget int
	switched   bool
	cacheLoss  int64
	// cutoff is true if Tollgate stopped the stream because a hard budget
	// had no money left (ADR 0029).
	cutoff bool
	// cache is "hit", "miss" or "" (bypass) for the response cache.
	cache string
	// mapping holds the placeholders of the masked values of the request,
	// or is nil (ADR 0031).
	mapping *redact.Mapping
	// cost is the cost of the request in micro-USD.
	cost int64
	// limitHeader holds the rate limit headers of the response.
	limitHeader http.Header
	// denied is the limit that stopped the request, for the access log.
	denied string
	// wrote is true if the response status went to the client.
	wrote bool
	// incomplete is true if the response stopped after its first byte,
	// so the provider can have used more tokens than the usage shows.
	incomplete bool
}

// begin starts the exchange and sets the x-request-id header.
func (s *Server) begin(w http.ResponseWriter, r *http.Request, api config.API) *exchange {
	x := &exchange{w: w, r: r, api: api, id: rand.Text(), start: time.Now(), ttft: -1, itl: -1, served: -1, prevTarget: -1, span: trace.SpanFromContext(r.Context())}
	w.Header().Set("X-Request-Id", x.id)
	return x
}

// fail sends an error response in the format of the API.
func (x *exchange) fail(status int, message string, retryAfter int) {
	x.status = status
	x.wrote = true
	apierror.Write(x.w, x.api, status, message, retryAfter)
}

// finish writes the access log line. It never logs headers or bodies (ADR
// 0010).
func (s *Server) finish(ctx context.Context, x *exchange) {
	args := []any{
		"request_id", x.id,
		"method", x.r.Method,
		"path", x.r.URL.Path,
		"status", x.status,
		"duration_ms", time.Since(x.start).Milliseconds(),
	}
	if x.identity.TenantID != "" {
		args = append(args, "tenant_id", x.identity.TenantID, "key_id", x.identity.KeyID)
	}
	if x.session != "" {
		args = append(args, "session_id", x.session, "session_derived", x.sessionDerived)
	}
	if x.routeModel != "" {
		args = append(args, "api", string(x.api), "route_model", x.routeModel, "stream", x.stream, "cache", x.cacheLabel())
	}
	if x.mapping.Len() > 0 {
		args = append(args, "redacted", x.mapping.Len())
	}
	if x.switched {
		args = append(args, "target_switched", true, "cache_loss_micro_usd", x.cacheLoss)
	}
	if x.cutoff {
		args = append(args, "budget_cutoff", true)
	}
	if x.attempts > 0 {
		args = append(args,
			"provider", x.provider,
			"provider_model", x.providerModel,
			"attempts", x.attempts,
			"overhead_us", x.overhead.Microseconds(),
			"bytes_in", x.bytesIn,
			"bytes_out", x.bytesOut,
		)
	}
	if x.usageKnown {
		args = append(args,
			"input_tokens", x.usage.InputTokens,
			"output_tokens", x.usage.OutputTokens,
			"cache_read_tokens", x.usage.CacheReadTokens,
			"cache_write_tokens", x.usage.CacheWriteTokens,
			"reasoning_tokens", x.usage.ReasoningTokens,
			"usage_estimated", x.estimated,
			"cost_micro_usd", x.cost,
		)
	}
	if x.denied != "" {
		args = append(args, "denied_by", x.denied)
	}
	if x.ttft >= 0 {
		args = append(args, "ttft_ms", x.ttft.Milliseconds())
	}
	if x.upstreamID != "" {
		args = append(args, "upstream_request_id", x.upstreamID)
	}
	if x.err != nil {
		args = append(args, "error", x.err.Error())
	}
	s.logger.InfoContext(ctx, "request done", args...)
	if s.usage != nil && x.identity.TenantID != "" && x.routeModel != "" {
		s.usage.Emit(x.event())
	}
	s.observe(x)
}

// observe records the metrics of the request and ends its server span.
func (s *Server) observe(x *exchange) {
	overhead := time.Duration(-1)
	if x.attempts > 0 {
		overhead = x.overhead
	}
	s.metrics.ObserveRequest(telemetry.Request{
		Route:             x.route,
		Provider:          x.provider,
		Status:            x.status,
		Cache:             x.cacheLabel(),
		Duration:          time.Since(x.start),
		TTFT:              x.ttft,
		ITL:               x.itl,
		Overhead:          overhead,
		Usage:             x.usageKnown,
		InputTokens:       x.usage.InputTokens,
		OutputTokens:      x.usage.OutputTokens,
		CacheReadTokens:   x.usage.CacheReadTokens,
		CacheWriteTokens:  x.usage.CacheWriteTokens,
		CostMicroUSD:      x.cost,
		Switched:          x.switched,
		CacheLossMicroUSD: x.cacheLoss,
	})

	sp := x.span
	if !sp.IsRecording() {
		return
	}
	attrs := []attribute.KeyValue{
		telemetry.AttrRequestID.String(x.id),
		telemetry.AttrCache.String(x.cacheLabel()),
	}
	if x.status > 0 {
		attrs = append(attrs, semconv.HTTPResponseStatusCode(x.status))
	}
	if x.identity.TenantID != "" {
		attrs = append(attrs, telemetry.AttrTenantID.String(x.identity.TenantID))
	}
	if x.session != "" {
		attrs = append(attrs, telemetry.AttrSessionID.String(x.session))
	}
	if x.routeModel != "" {
		attrs = append(attrs, telemetry.GenAIOperationName.String(telemetry.OperationChat), telemetry.GenAIRequestModel.String(x.routeModel))
	}
	if x.attempts > 0 {
		attrs = append(attrs, telemetry.AttrAttempts.Int(x.attempts), telemetry.AttrTarget.String(x.provider+"/"+x.providerModel))
	}
	if x.responseModel != "" {
		attrs = append(attrs, telemetry.GenAIResponseModel.String(x.responseModel))
	}
	if x.usageKnown {
		attrs = append(attrs, usageAttrs(x.usage, x.estimated)...)
		attrs = append(attrs, telemetry.AttrCostMicroUSD.Int64(x.cost))
	}
	if x.denied != "" {
		attrs = append(attrs, telemetry.AttrDeniedBy.String(x.denied))
	}
	sp.SetAttributes(attrs...)
	switch {
	case x.status >= 500:
		sp.SetStatus(codes.Error, http.StatusText(x.status))
	case x.incomplete:
		sp.SetStatus(codes.Error, "the response stopped after its first byte")
	}
	if x.err != nil && (x.status >= 500 || x.incomplete) {
		sp.SetAttributes(semconv.ErrorTypeKey.String(errorType(x.err)))
	}
	sp.End()
}

// usageAttrs returns the span attributes of a token usage.
func usageAttrs(u provider.Usage, estimated bool) []attribute.KeyValue {
	return []attribute.KeyValue{
		telemetry.GenAIUsageInputTokens.Int64(u.InputTokens),
		telemetry.GenAIUsageOutputTokens.Int64(u.OutputTokens),
		telemetry.AttrCacheReadTokens.Int64(u.CacheReadTokens),
		telemetry.AttrCacheWriteTokens.Int64(u.CacheWriteTokens),
		telemetry.AttrUsageEstimated.Bool(estimated),
	}
}

// errorType returns the error.type attribute of an error.
func errorType(err error) string {
	switch {
	case errors.Is(err, provider.ErrMalformed):
		return "malformed_response"
	case errors.Is(err, relay.ErrClientWrite):
		return "client_write"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return failLabel(err)
	}
}

// traced starts the server span of each request and gives next the
// request with the span in its context. A traceparent header of the client
// makes the span a child of the client span. The handler ends the span (see
// observe).
func (s *Server) traced(path string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := s.propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, _ = s.tracer.Start(ctx, r.Method+" "+path,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				semconv.HTTPRequestMethodKey.String(r.Method),
				semconv.HTTPRoute(path),
			),
		)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) proxy(ep endpoint) http.Handler {
	return s.traced(ep.path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		x := s.begin(w, r, ep.api)
		defer s.finish(ctx, x)
		if !s.tracker.enter() {
			x.fail(http.StatusServiceUnavailable, "Tollgate is stopping and does not accept new requests. Send the request again.", drainRetryAfter)
			return
		}
		defer s.tracker.leave()
		s.serveProxy(ctx, x, ep)
	}))
}

func (s *Server) serveProxy(ctx context.Context, x *exchange, ep endpoint) {
	if !s.authenticate(ctx, x) {
		return
	}
	_, span := s.tracer.Start(ctx, "preflight")
	f, ok := s.preflight(x, ep)
	span.End()
	if !ok {
		return
	}
	lctx, span := s.tracer.Start(ctx, "limit check")
	adm, ok := s.admit(lctx, x, ep, f.list, f.pre, len(f.bodies[0]))
	span.End()
	if !ok {
		return
	}
	defer s.settle(ctx, x, adm)
	defer x.flush()
	f.cut = s.roomOf(x, adm)
	f.prices = adm.prices
	f.first = -1
	if adm.sticky != "" {
		x.prevTarget = stickyIndex(adm.decision.Sticky, f.list)
		if s.sticky {
			f.first = x.prevTarget
		}
	}
	if f.rt.cacheTTL > 0 && ep.metered {
		x.cache = "miss"
		key := respcache.NewKey(x.identity.TenantID, string(ep.api), f.rt.model, x.r.Header, f.clientBody)
		_, span := s.tracer.Start(ctx, "cache")
		e, ok := s.cache.Get(key, s.now())
		span.End()
		if ok {
			s.serveCached(x, e)
			return
		}
		f.cacheKey = &key
	}
	s.forward(ctx, x, f)
}

// authenticate finds the tenant of the key of the request. If the key is
// missing or not valid, it sends the error response and returns false.
func (s *Server) authenticate(ctx context.Context, x *exchange) bool {
	_, span := s.tracer.Start(ctx, "auth")
	defer span.End()
	key, ok := auth.FromHeader(x.r.Header)
	if !ok {
		x.fail(http.StatusUnauthorized, "The request has no API key. Send a Tollgate key in the x-api-key header or as a bearer token.", 0)
		return false
	}
	if x.identity, ok = s.keys.Find(key); !ok {
		x.fail(http.StatusUnauthorized, "The API key is not valid. Use a Tollgate key that starts with tg_.", 0)
		return false
	}
	span.SetAttributes(telemetry.AttrTenantID.String(x.identity.TenantID))
	return true
}

// preflight reads and decodes the request body, checks the model and the
// session, and finds the targets. If the request cannot continue, it sends
// the error response and returns false.
func (s *Server) preflight(x *exchange, ep endpoint) (*flow, bool) {
	w, r := x.w, x.r
	readStart := time.Now()
	body, err := s.readBody(w, r)
	x.readTime = time.Since(readStart)
	if err != nil {
		x.err = err
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			x.fail(http.StatusRequestEntityTooLarge, fmt.Sprintf("The request body is larger than the limit of %d bytes. Make the request smaller.", s.maxBody), 0)
			return nil, false
		}
		x.fail(http.StatusBadRequest, "Tollgate cannot read the request body. Send the request again.", 0)
		return nil, false
	}
	x.bytesIn = len(body)

	pre, err := preflight.Decode(body)
	if err != nil {
		x.err = err
		x.fail(http.StatusBadRequest, decodeMessage(err), 0)
		return nil, false
	}
	x.routeModel, x.stream = pre.Model, pre.Stream
	if !s.setSession(x, pre) {
		x.fail(http.StatusBadRequest, "The x-tollgate-session header is not valid. Use 1 to 128 visible ASCII characters, with no spaces.", 0)
		return nil, false
	}
	tp := s.tenants[x.identity.TenantID]
	clientBody := body
	if tp.redactor != nil {
		masked, m, err := tp.redactor.Mask(body)
		if err != nil {
			x.err = err
			x.fail(http.StatusBadRequest, "Tollgate cannot redact the request body. Check the JSON strings of the request.", 0)
			return nil, false
		}
		if m.Len() > 0 {
			if pre, err = preflight.Decode(masked); err != nil {
				x.err = err
				x.fail(http.StatusBadRequest, decodeMessage(err), 0)
				return nil, false
			}
			x.mapping = m
		}
	}
	if !tp.models[pre.Model] {
		x.fail(http.StatusForbidden, fmt.Sprintf("The key cannot use the model %q. Use a model that the configuration of the tenant allows.", pre.Model), 0)
		return nil, false
	}
	rt := s.routes[routeKey{ep.api, pre.Model}]
	if rt != nil {
		x.route = rt.model
	}
	if rt == nil {
		x.fail(http.StatusNotFound, fmt.Sprintf("The model %q has no %s route. Add a route for it to the configuration.", pre.Model, ep.api), 0)
		return nil, false
	}
	list := &rt.all
	if ep.path == countTokensPath {
		list = &rt.count
	}
	if len(list.targets) == 0 {
		x.fail(http.StatusNotFound, fmt.Sprintf("No target of the model %q supports %s. Add a target with a provider of the kind anthropic.", pre.Model, ep.path), 0)
		return nil, false
	}
	f := &flow{ep: ep, rt: rt, list: list, pre: pre, clientBody: clientBody, bodies: make([][]byte, len(list.targets))}
	// The size of the first body gives the input token estimate of all
	// targets.
	if _, err := f.body(0); err != nil {
		x.err = err
		x.fail(http.StatusBadRequest, decodeMessage(err), 0)
		return nil, false
	}
	return f, true
}

// responseStartWriter commits the upstream status and headers on the first
// body write. Unwrap lets ResponseController set deadlines and flush the
// underlying connection.
type responseStartWriter struct {
	http.ResponseWriter
	x       *exchange
	header  http.Header
	status  int
	started bool
	// capture, if not nil, keeps a copy of the body for the response
	// cache.
	capture *captureWriter
}

func (w *responseStartWriter) commit() {
	if !w.started {
		w.started = true
		h := w.Header()
		provider.CopyResponseHeader(h, w.header)
		h.Set("X-Request-Id", w.x.id)
		h.Set("X-Tollgate-Provider", w.x.provider)
		h.Set("X-Tollgate-Model", w.x.providerModel)
		h.Set("X-Tollgate-Attempts", strconv.Itoa(w.x.attempts))
		h.Set("X-Tollgate-Overhead-Ms", strconv.FormatFloat(float64(w.x.overhead)/float64(time.Millisecond), 'f', 3, 64))
		h.Set("X-Tollgate-Cache", w.x.cacheLabel())
		if w.x.mapping.Len() > 0 {
			// The restored values change the length of the body.
			h.Del("Content-Length")
		}
		for k, v := range w.x.limitHeader {
			h[k] = v
		}
		w.x.status = w.status
		w.x.wrote = true
		w.WriteHeader(w.status)
	}
}

func (w *responseStartWriter) Write(p []byte) (int, error) {
	w.commit()
	if w.capture != nil {
		w.capture.add(p)
	}
	return w.ResponseWriter.Write(p) //nolint:gosec // The body is a provider response, sent without changes (ADR 0009).
}

func (w *responseStartWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// flush sends the buffered response bytes to the client, so that the
// settle step after the response does not delay them.
func (x *exchange) flush() {
	if x.wrote {
		_ = http.NewResponseController(x.w).Flush()
	}
}

// readBody reads the request body up to the size limit, with a deadline.
func (s *Server) readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(bodyReadTimeout))
	defer func() { _ = rc.SetReadDeadline(time.Time{}) }()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.maxBody))
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	return body, nil
}

// boundaryMeter is a meter that knows if the stream is between two events.
type boundaryMeter interface {
	AtBoundary() bool
}

// writeStreamError ends a stream that failed after its first byte with an
// error event (ADR 0007). If the stream stopped inside an event, it first
// ends that event, so the error event is a separate event.
func (s *Server) writeStreamError(x *exchange, t *target, timeouts relay.Timeouts, meter provider.Meter, cause error) {
	s.writeStreamEvent(x, meter, errorMessage(t.Target, timeouts, cause, true))
}

// writeStreamEvent ends a stream with an error event with the message.
func (s *Server) writeStreamEvent(x *exchange, meter provider.Meter, message string) {
	var event []byte
	if bm, ok := innerMeter(meter).(boundaryMeter); ok && !bm.AtBoundary() {
		event = append(event, "\n\n"...)
	}
	event = append(event, apierror.StreamEvent(x.api, message)...)
	if _, err := x.w.Write(event); err == nil { //nolint:gosec // An event stream is not HTML, and the message is JSON-encoded.
		_ = http.NewResponseController(x.w).Flush()
	}
}

func newMeter(api config.API, stream bool) provider.Meter {
	switch {
	case api == config.APIAnthropic && stream:
		return anthropic.NewStreamMeter()
	case api == config.APIAnthropic:
		return anthropic.NewBodyMeter()
	case stream:
		return openai.NewStreamMeter()
	default:
		return openai.NewBodyMeter()
	}
}

func isEventStream(h http.Header) bool {
	mt, _, err := mime.ParseMediaType(h.Get("Content-Type"))
	return err == nil && mt == "text/event-stream"
}

// upstreamRequestID returns the request ID of the provider: Anthropic sends
// request-id, OpenAI sends x-request-id.
func upstreamRequestID(h http.Header) string {
	if id := h.Get("Request-Id"); id != "" {
		return id
	}
	return h.Get("X-Request-Id")
}

func decodeMessage(err error) string {
	switch {
	case errors.Is(err, preflight.ErrNoModel):
		return "The model field is missing or is not text. Add the model name and send the request again."
	case errors.Is(err, preflight.ErrStream):
		return "The stream field must be true or false."
	case errors.Is(err, preflight.ErrNotObject):
		return "The request body must be a JSON object."
	default:
		return "The request body is not valid JSON. Correct the body and send the request again."
	}
}
