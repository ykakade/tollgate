package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/ykakade/tollgate/internal/config"
	"github.com/ykakade/tollgate/internal/preflight"
	"github.com/ykakade/tollgate/internal/pricing"
	"github.com/ykakade/tollgate/internal/provider"
	"github.com/ykakade/tollgate/internal/relay"
	"github.com/ykakade/tollgate/internal/respcache"
	"github.com/ykakade/tollgate/internal/router"
	"github.com/ykakade/tollgate/internal/telemetry"
)

// Settings of the attempts.
const (
	// maxCoolDown is the longest cooldown that a retry-after header can
	// set.
	maxCoolDown = 10 * time.Minute
)

// flow is the state of the attempts of one request.
type flow struct {
	ep   endpoint
	rt   *route
	list *targetList
	pre  *preflight.Request
	plan *router.Plan
	// bodies holds the request body for each target, made at the first
	// attempt to the target.
	bodies [][]byte
	// failures describes each failed attempt, for the error message.
	failures []string
	// clientBody is the request body as the client sent it, before
	// redaction, for the key of the response cache.
	clientBody []byte
	// first is the index of the sticky target, or -1.
	first int
	// cut is the hard budget limit of a stream, or nil.
	cut *cutoff
	// cacheKey is the response cache key, or nil if the route does not
	// use the response cache.
	cacheKey *respcache.Key
	// prices holds the price of each target, or nil.
	prices []*pricing.Entry
}

func (f *flow) body(i int) ([]byte, error) {
	if f.bodies[i] == nil {
		includeUsage := f.ep.api == config.APIOpenAI && f.ep.metered && f.pre.Stream
		b, err := f.list.targets[i].body(f.pre, includeUsage)
		if err != nil {
			return nil, err
		}
		f.bodies[i] = b
	}
	return f.bodies[i], nil
}

// forward sends the request to the targets of the route until one attempt
// gives a response for the client (ADR 0021).
func (s *Server) forward(ctx context.Context, x *exchange, f *flow) {
	f.plan = s.router.PlanFrom(f.list.keys, s.maxAttempts, f.first)
	i, ok := f.plan.First()
	if !ok {
		s.allBlocked(x, f)
		return
	}
	for {
		next, again := s.try(ctx, x, f, i)
		if !again {
			return
		}
		i = next
	}
}

// try runs one attempt to target i. If the request needs another attempt,
// it returns the index of the next target and true.
func (s *Server) try(ctx context.Context, x *exchange, f *flow, i int) (int, bool) {
	t := f.list.targets[i]
	body, err := f.body(i)
	if err != nil {
		f.plan.Report(router.Neutral)
		x.err = err
		x.fail(http.StatusBadRequest, decodeMessage(err), 0)
		return 0, false
	}
	x.served, x.provider, x.providerModel, x.attempts = i, t.Provider, t.Model, f.plan.Attempts()

	actx, span := s.startAttempt(ctx, x, f, t)
	defer span.End()
	a := relay.Start(actx, f.rt.timeouts)
	defer a.Stop()
	if x.attempts == 1 {
		x.overhead = time.Since(x.start) - x.readTime
	}
	resp, err := t.adapter.Send(a.Context(), &provider.Request{ //nolint:contextcheck // The attempt context derives from ctx.
		Path:     f.ep.path,
		RawQuery: x.r.URL.RawQuery,
		Header:   x.r.Header,
		Body:     body,
		Model:    t.Model,
		Stream:   f.pre.Stream,
	})
	if err != nil {
		if cause := a.Err(); cause != nil {
			err = cause
		}
		endAttempt(span, 0, err)
		return s.failed(ctx, x, f, t, router.Result{Err: err, ClientGone: ctx.Err() != nil})
	}
	defer resp.Body.Close()
	x.upstreamID = upstreamRequestID(resp.Header)
	span.SetAttributes(semconv.HTTPResponseStatusCode(resp.StatusCode))

	ok2xx := resp.StatusCode >= 200 && resp.StatusCode < 300
	reported := false
	if !ok2xx {
		action, outcome := router.Classify(router.Result{Status: resp.StatusCode})
		if action != router.Done {
			f.plan.Report(outcome)
			reported = true
			if resp.StatusCode == http.StatusTooManyRequests {
				s.router.CoolDown(t.key, retryAfter(resp.Header, s.now()))
			}
			reason := "status " + strconv.Itoa(resp.StatusCode)
			next, stop, ok := f.plan.Next(action)
			if ok {
				endAttempt(span, resp.StatusCode, nil)
				f.failures = append(f.failures, t.name()+" ("+reason+")")
				s.logRetry(ctx, x, f, t, f.list.targets[next], reason, statusLabel(resp.StatusCode))
				// Close the error body without a read. A stalled body must
				// not delay the next target.
				return next, true
			}
			if stop == router.BudgetExhausted {
				s.metrics.RetryBudgetExhausted(t.Provider)
			}
			// No next attempt: the client gets this response.
		}
	}

	isStream := isEventStream(resp.Header)
	streaming := isStream && ok2xx
	var meter provider.Meter
	if f.ep.metered && ok2xx {
		meter = newMeter(f.ep.api, isStream)
		if rm, ok := meter.(runningMeter); ok && streaming && f.cut != nil && i < len(f.prices) {
			if price := f.prices[i]; price != nil {
				rm.TrackOutput()
				meter = &cutoffMeter{runningMeter: rm, price: price, inputTokens: preflight.EstimateInputTokens(len(body)), room: f.cut.room}
			}
		}
	}
	// Keep the downstream response open until a body byte arrives. Provider
	// headers alone do not commit us to a successful response (ADR 0016).
	dst := &responseStartWriter{
		ResponseWriter: x.w,
		x:              x,
		header:         resp.Header,
		status:         resp.StatusCode,
	}
	if f.cacheKey != nil && ok2xx {
		dst.capture = &captureWriter{limit: s.cache.MaxEntry()}
	}
	var out http.ResponseWriter = dst
	var held *unmaskBody
	if x.mapping.Len() > 0 && ok2xx {
		if isStream {
			out = &unmaskStream{responseStartWriter: dst, u: x.mapping.NewStreamUnmasker(dst, redactFormat(f.ep.api))}
		} else {
			held = &unmaskBody{responseStartWriter: dst, limit: unmaskBodyLimit}
			out = held
		}
	}
	var meterW io.Writer
	if meter != nil {
		meterW = meter
	}
	if streaming {
		s.metrics.StreamStarted()
	}
	n, copyErr := a.Copy(out, resp.Body, meterW)
	if streaming {
		s.metrics.StreamEnded()
	}
	if held != nil && copyErr == nil {
		if err := held.finish(x.mapping); err != nil {
			copyErr = fmt.Errorf("%w: %w", relay.ErrClientWrite, err)
		}
	}
	cm, _ := meter.(*cutoffMeter)
	x.cutoff = cm != nil && cm.hit && errors.Is(copyErr, errBudgetCutoff)
	x.bytesOut = n
	endAttempt(span, resp.StatusCode, copyErr)
	if fb := a.FirstByte(); !fb.IsZero() {
		x.ttft = fb.Sub(x.start)
	}
	clientGone := ctx.Err() != nil || errors.Is(copyErr, relay.ErrClientWrite)
	if copyErr != nil && !dst.started && !reported {
		// For example a first-token timeout after the headers of a 200.
		return s.failed(ctx, x, f, t, router.Result{Status: resp.StatusCode, Err: copyErr, ClientGone: clientGone})
	}
	if !reported {
		// A budget cutoff says nothing about the health of the target.
		_, outcome := router.Classify(router.Result{Status: resp.StatusCode, Err: copyErr, Started: dst.started, ClientGone: clientGone || x.cutoff})
		f.plan.Report(outcome)
	}
	switch {
	case copyErr != nil && !dst.started:
		s.lastFailed(ctx, x, f, t, copyErr, router.NoTarget)
	case x.cutoff:
		x.err = copyErr
		x.incomplete = true
		s.metrics.BudgetCutoff(f.cut.window)
		s.writeStreamEvent(x, meter, f.cut.message)
	case copyErr != nil:
		x.err = copyErr
		x.incomplete = true
		if isStream && ok2xx && !clientGone {
			s.writeStreamError(x, t, f.rt.timeouts, meter, copyErr)
		}
	default:
		// An empty response can complete without calling Write.
		dst.commit()
		if dst.capture != nil && !dst.capture.over {
			s.cache.Put(*f.cacheKey, &respcache.Entry{ContentType: resp.Header.Get("Content-Type"), Body: dst.capture.buf.Bytes(), Provider: t.Provider, Model: t.Model}, s.now(), f.rt.cacheTTL)
		}
	}

	if meter != nil {
		x.usage, x.usageKnown = meter.Usage()
		if x.cutoff {
			x.usage, x.usageKnown, x.estimated = cm.usage(), true, true
		}
		if !x.usageKnown {
			x.usage = provider.Usage{InputTokens: preflight.EstimateInputTokens(len(body))}
			x.usageKnown, x.estimated = true, true
		}
		if mm, ok := meter.(modelMeter); ok {
			x.responseModel = mm.Model()
		}
		if streaming && !x.estimated && x.usage.OutputTokens > 1 {
			x.itl = a.LastByte().Sub(a.FirstByte()) / time.Duration(x.usage.OutputTokens-1)
		}
		if x.responseModel != "" {
			span.SetAttributes(telemetry.GenAIResponseModel.String(x.responseModel))
		}
		span.SetAttributes(usageAttrs(x.usage, x.estimated)...)
	}
	return 0, false
}

// modelMeter is a meter that knows the model that the response names.
type modelMeter interface {
	Model() string
}

// startAttempt starts the client span of one upstream attempt, with the
// attributes of the OpenTelemetry GenAI conventions.
func (s *Server) startAttempt(ctx context.Context, x *exchange, f *flow, t *target) (context.Context, trace.Span) {
	attrs := []attribute.KeyValue{
		telemetry.GenAIOperationName.String(telemetry.OperationChat),
		telemetry.GenAIProviderName.String(telemetry.ProviderName(t.kind)),
		telemetry.GenAIRequestModel.String(t.Model),
		telemetry.AttrAttempt.Int(x.attempts),
		telemetry.AttrTarget.String(t.name()),
		telemetry.AttrRequestID.String(x.id),
	}
	if f.pre.MaxTokens > 0 {
		attrs = append(attrs, telemetry.GenAIRequestMaxTokens.Int64(f.pre.MaxTokens))
	}
	return s.tracer.Start(ctx, telemetry.OperationChat+" "+t.Model,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)
}

// endAttempt sets the result of an attempt on its span. Status is 0 if no
// response came.
func endAttempt(span trace.Span, status int, err error) {
	switch {
	case err != nil:
		span.SetAttributes(semconv.ErrorTypeKey.String(errorType(err)))
		span.SetStatus(codes.Error, failReason(err))
	case status >= 400:
		span.SetAttributes(semconv.ErrorTypeKey.String(strconv.Itoa(status)))
		span.SetStatus(codes.Error, "status "+strconv.Itoa(status))
	}
}

// failed handles an attempt that failed before the downstream response
// started, and finds the next attempt.
func (s *Server) failed(ctx context.Context, x *exchange, f *flow, t *target, res router.Result) (int, bool) {
	action, outcome := router.Classify(res)
	f.plan.Report(outcome)
	if res.ClientGone {
		x.err = res.Err
		x.status = statusClientClosed
		return 0, false
	}
	next, stop, ok := f.plan.Next(action)
	if ok {
		reason := failReason(res.Err)
		f.failures = append(f.failures, t.name()+" ("+reason+")")
		s.logRetry(ctx, x, f, t, f.list.targets[next], reason, failLabel(res.Err))
		return next, true
	}
	s.lastFailed(ctx, x, f, t, res.Err, stop)
	return 0, false
}

// lastFailed sends the 502 response after the last attempt failed before
// the downstream response started.
func (s *Server) lastFailed(ctx context.Context, x *exchange, f *flow, t *target, err error, stop router.Stop) {
	x.err = err
	if ctx.Err() != nil {
		x.status = statusClientClosed
		return
	}
	if len(f.failures) == 0 && len(f.plan.Skipped()) == 0 {
		x.fail(http.StatusBadGateway, errorMessage(t.Target, f.rt.timeouts, err, false), 0)
		return
	}
	f.failures = append(f.failures, t.name()+" ("+failReason(err)+")")
	msg := fmt.Sprintf("All targets of the model %q failed: %s.", f.rt.model, strings.Join(append(f.failures, skipped(f)...), ", "))
	switch stop {
	case router.MaxAttempts:
		msg += fmt.Sprintf(" Tollgate stopped after %d attempts (retry.max_attempts).", s.maxAttempts)
	case router.BudgetExhausted:
		s.metrics.RetryBudgetExhausted(t.Provider)
		msg += " Tollgate did not try again, because the retry budget is empty."
	}
	x.fail(http.StatusBadGateway, msg+" Send the request again later.", 0)
}

// allBlocked sends the response when no target can get an attempt, for
// example because all breakers are open. It sends no upstream request.
func (s *Server) allBlocked(x *exchange, f *flow) {
	wait := s.router.ReadyIn(f.list.keys)
	secs := max(int((wait+time.Second-1)/time.Second), 1)
	x.denied = "no_target"
	x.fail(http.StatusBadGateway, fmt.Sprintf("No target of the model %q is available now: %s. Send the request again in %d seconds.", f.rt.model, strings.Join(skipped(f), ", "), secs), secs)
}

// skipped describes the targets that the plan did not try.
func skipped(f *flow) []string {
	var out []string
	for _, sk := range f.plan.Skipped() {
		var why string
		switch sk.Reason {
		case router.BreakerOpen:
			why = "circuit breaker open"
		case router.ProbeBusy:
			why = "circuit breaker half-open"
		case router.CoolingDown:
			why = "rate limited"
		}
		out = append(out, f.list.targets[sk.Index].name()+" ("+why+")")
	}
	return out
}

// failReason is the short reason of a failed attempt for messages and logs.
func failReason(err error) string {
	switch {
	case errors.Is(err, relay.ErrConnectTimeout):
		return "connect timeout"
	case errors.Is(err, relay.ErrFirstTokenTimeout):
		return "first_token timeout"
	case errors.Is(err, relay.ErrIdleTimeout):
		return "idle timeout"
	case errors.Is(err, relay.ErrTotalTimeout):
		return "total timeout"
	default:
		return "connection failed"
	}
}

// failLabel is the reason of a failed attempt for metric labels.
func failLabel(err error) string {
	return strings.ReplaceAll(failReason(err), " ", "_")
}

// statusLabel is the reason of an attempt with an error status for metric
// labels.
func statusLabel(status int) string {
	switch status {
	case http.StatusTooManyRequests:
		return "rate_limited"
	case router.StatusOverloaded:
		return "overloaded"
	default:
		return "server_error"
	}
}

// logRetry records an attempt that failed before the next attempt: a
// warning, the retry and fallback metrics, and an event on the server span.
// The label is the reason for the metrics.
func (s *Server) logRetry(ctx context.Context, x *exchange, f *flow, from, to *target, reason, label string) {
	s.metrics.Retry(f.rt.model, from.Provider, from.name(), to.name(), label)
	x.span.AddEvent("retry", trace.WithAttributes(
		telemetry.AttrAttempt.Int(x.attempts),
		attribute.String("tollgate.from", from.name()),
		attribute.String("tollgate.to", to.name()),
		attribute.String("tollgate.reason", label),
	))
	s.logger.WarnContext(ctx, "upstream attempt failed, trying again",
		"request_id", x.id,
		"attempt", x.attempts,
		"provider", from.Provider,
		"provider_model", from.Model,
		"reason", reason,
		"next_provider", to.Provider,
		"next_provider_model", to.Model,
	)
}

// retryAfter returns the time in a retry-after header: a number of seconds
// or an HTTP date. It returns 0 if the header is missing or not valid. The
// time is at most maxCoolDown.
func retryAfter(h http.Header, now time.Time) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	var d time.Duration
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		// Clamp seconds before conversion to avoid duration overflow.
		secs = min(max(secs, 0), int64(maxCoolDown/time.Second))
		d = time.Duration(secs) * time.Second
	} else if at, err := http.ParseTime(v); err == nil {
		d = at.Sub(now)
	}
	return min(max(d, 0), maxCoolDown)
}
