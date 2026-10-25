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

	"github.com/ykakade/tollgate/internal/apierror"
	"github.com/ykakade/tollgate/internal/auth"
	"github.com/ykakade/tollgate/internal/budget"
	"github.com/ykakade/tollgate/internal/config"
	"github.com/ykakade/tollgate/internal/preflight"
	"github.com/ykakade/tollgate/internal/provider"
	"github.com/ykakade/tollgate/internal/provider/anthropic"
	"github.com/ykakade/tollgate/internal/provider/openai"
	"github.com/ykakade/tollgate/internal/relay"
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

	status        int
	identity      auth.Identity
	routeModel    string
	provider      string
	providerModel string
	stream        bool
	attempts      int
	// served is the index of the target of the last attempt, or -1.
	served     int
	readTime   time.Duration
	usage      provider.Usage
	usageKnown bool
	estimated  bool
	ttft       time.Duration
	overhead   time.Duration
	bytesIn    int
	bytesOut   int64
	upstreamID string
	err        error

	session string
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
	x := &exchange{w: w, r: r, api: api, id: rand.Text(), start: time.Now(), ttft: -1, served: -1}
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
		args = append(args, "session_id", x.session)
	}
	if x.routeModel != "" {
		args = append(args, "api", string(x.api), "route_model", x.routeModel, "stream", x.stream)
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
}

func (s *Server) proxy(ep endpoint) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		x := s.begin(w, r, ep.api)
		defer s.finish(ctx, x)
		if !s.tracker.enter() {
			x.fail(http.StatusServiceUnavailable, "Tollgate is stopping and does not accept new requests. Send the request again.", drainRetryAfter)
			return
		}
		defer s.tracker.leave()
		s.serveProxy(ctx, x, ep)
	})
}

func (s *Server) serveProxy(ctx context.Context, x *exchange, ep endpoint) {
	w, r := x.w, x.r
	key, ok := auth.FromHeader(r.Header)
	if !ok {
		x.fail(http.StatusUnauthorized, "The request has no API key. Send a Tollgate key in the x-api-key header or as a bearer token.", 0)
		return
	}
	if x.identity, ok = s.keys.Find(key); !ok {
		x.fail(http.StatusUnauthorized, "The API key is not valid. Use a Tollgate key that starts with tg_.", 0)
		return
	}

	readStart := time.Now()
	body, err := s.readBody(w, r)
	x.readTime = time.Since(readStart)
	if err != nil {
		x.err = err
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			x.fail(http.StatusRequestEntityTooLarge, fmt.Sprintf("The request body is larger than the limit of %d bytes. Make the request smaller.", s.maxBody), 0)
			return
		}
		x.fail(http.StatusBadRequest, "Tollgate cannot read the request body. Send the request again.", 0)
		return
	}
	x.bytesIn = len(body)

	pre, err := preflight.Decode(body)
	if err != nil {
		x.err = err
		x.fail(http.StatusBadRequest, decodeMessage(err), 0)
		return
	}
	x.routeModel, x.stream = pre.Model, pre.Stream
	if x.session = r.Header.Get("X-Tollgate-Session"); x.session != "" && !budget.ValidSession(x.session) {
		x.session = ""
		x.fail(http.StatusBadRequest, "The x-tollgate-session header is not valid. Use 1 to 128 visible ASCII characters, with no spaces.", 0)
		return
	}
	if !s.tenants[x.identity.TenantID].models[pre.Model] {
		x.fail(http.StatusForbidden, fmt.Sprintf("The key cannot use the model %q. Use a model that the configuration of the tenant allows.", pre.Model), 0)
		return
	}
	rt := s.routes[routeKey{ep.api, pre.Model}]
	if rt == nil {
		x.fail(http.StatusNotFound, fmt.Sprintf("The model %q has no %s route. Add a route for it to the configuration.", pre.Model, ep.api), 0)
		return
	}
	list := &rt.all
	if ep.path == countTokensPath {
		list = &rt.count
	}
	if len(list.targets) == 0 {
		x.fail(http.StatusNotFound, fmt.Sprintf("No target of the model %q supports %s. Add a target with a provider of the kind anthropic.", pre.Model, ep.path), 0)
		return
	}
	f := &flow{ep: ep, rt: rt, list: list, pre: pre, bodies: make([][]byte, len(list.targets))}
	// The size of the first body gives the input token estimate of all
	// targets.
	first, err := f.body(0)
	if err != nil {
		x.err = err
		x.fail(http.StatusBadRequest, decodeMessage(err), 0)
		return
	}
	adm, ok := s.admit(ctx, x, ep, list, pre, len(first))
	if !ok {
		return
	}
	defer s.settle(ctx, x, adm)
	defer x.flush()
	s.forward(ctx, x, f)
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
	return w.ResponseWriter.Write(p)
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
	var event []byte
	if bm, ok := meter.(boundaryMeter); ok && !bm.AtBoundary() {
		event = append(event, "\n\n"...)
	}
	event = append(event, apierror.StreamEvent(x.api, errorMessage(t.Target, timeouts, cause, true))...)
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
