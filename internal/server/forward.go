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

	"github.com/ykakade/tollgate/internal/config"
	"github.com/ykakade/tollgate/internal/preflight"
	"github.com/ykakade/tollgate/internal/provider"
	"github.com/ykakade/tollgate/internal/relay"
	"github.com/ykakade/tollgate/internal/router"
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
	f.plan = s.router.Plan(f.list.keys, s.maxAttempts)
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

	a := relay.Start(ctx, f.rt.timeouts)
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
		return s.failed(ctx, x, f, t, router.Result{Err: err, ClientGone: ctx.Err() != nil})
	}
	defer resp.Body.Close()
	x.upstreamID = upstreamRequestID(resp.Header)

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
			if next, _, ok := f.plan.Next(action); ok {
				f.failures = append(f.failures, t.name()+" ("+reason+")")
				s.logRetry(ctx, x, t, f.list.targets[next], reason)
				// Close the error body without a read. A stalled body must
				// not delay the next target.
				return next, true
			}
			// No next attempt: the client gets this response.
		}
	}

	isStream := isEventStream(resp.Header)
	var meter provider.Meter
	if f.ep.metered && ok2xx {
		meter = newMeter(f.ep.api, isStream)
	}
	// Keep the downstream response open until a body byte arrives. Provider
	// headers alone do not commit us to a successful response (ADR 0016).
	dst := &responseStartWriter{
		ResponseWriter: x.w,
		x:              x,
		header:         resp.Header,
		status:         resp.StatusCode,
	}
	var meterW io.Writer
	if meter != nil {
		meterW = meter
	}
	n, copyErr := a.Copy(dst, resp.Body, meterW)
	x.bytesOut = n
	if fb := a.FirstByte(); !fb.IsZero() {
		x.ttft = fb.Sub(x.start)
	}
	clientGone := ctx.Err() != nil || errors.Is(copyErr, relay.ErrClientWrite)
	if copyErr != nil && !dst.started && !reported {
		// For example a first-token timeout after the headers of a 200.
		return s.failed(ctx, x, f, t, router.Result{Status: resp.StatusCode, Err: copyErr, ClientGone: clientGone})
	}
	if !reported {
		_, outcome := router.Classify(router.Result{Status: resp.StatusCode, Err: copyErr, Started: dst.started, ClientGone: clientGone})
		f.plan.Report(outcome)
	}
	switch {
	case copyErr != nil && !dst.started:
		s.lastFailed(ctx, x, f, t, copyErr, router.NoTarget)
	case copyErr != nil:
		x.err = copyErr
		x.incomplete = true
		if isStream && ok2xx && !clientGone {
			s.writeStreamError(x, t, f.rt.timeouts, meter, copyErr)
		}
	default:
		// An empty response can complete without calling Write.
		dst.commit()
	}

	if meter != nil {
		x.usage, x.usageKnown = meter.Usage()
		if !x.usageKnown {
			x.usage = provider.Usage{InputTokens: preflight.EstimateInputTokens(len(body))}
			x.usageKnown, x.estimated = true, true
		}
	}
	return 0, false
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
		s.logRetry(ctx, x, t, f.list.targets[next], reason)
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

// logRetry writes the warning for an attempt that failed before the next
// attempt.
func (s *Server) logRetry(ctx context.Context, x *exchange, from, to *target, reason string) {
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
