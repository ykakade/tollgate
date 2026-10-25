package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"strconv"

	"github.com/ykakade/tollgate/internal/budget"
	"github.com/ykakade/tollgate/internal/config"
	"github.com/ykakade/tollgate/internal/limiter"
	"github.com/ykakade/tollgate/internal/preflight"
	"github.com/ykakade/tollgate/internal/pricing"
	"github.com/ykakade/tollgate/internal/provider"
	"github.com/ykakade/tollgate/internal/redact"
	"github.com/ykakade/tollgate/internal/respcache"
)

// derivedPrefix is the start of a session ID that Tollgate derived from
// the prompt prefix (ADR 0027).
const derivedPrefix = "auto-"

// setSession sets the session of the request: the x-tollgate-session
// header, or, if derivation is on, a hash of the prompt prefix. It returns
// false if the header is not valid.
func (s *Server) setSession(x *exchange, pre *preflight.Request) bool {
	x.session = x.r.Header.Get("X-Tollgate-Session")
	if x.session != "" {
		if !budget.ValidSession(x.session) {
			x.session = ""
			return false
		}
		return true
	}
	if s.derive {
		if h := pre.PrefixHash(); h != "" {
			x.session, x.sessionDerived = derivedPrefix+h, true
		}
	}
	return true
}

// stickyValue is the value of a sticky routing entry for target i of a
// list: a hash of the target name, and i+1. A change of the configuration
// that moves the target makes the old value not valid.
func stickyValue(i int, t *target) int64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(t.name())) // A hash write does not fail.
	return int64(h.Sum32())<<8 | int64(i+1)
}

// stickyIndex returns the target index of a sticky routing entry, or -1 if
// the value does not name a target of the list.
func stickyIndex(v int64, l *targetList) int {
	i := int(v&0xff) - 1
	if v <= 0 || i < 0 || i >= len(l.targets) || stickyValue(i, l.targets[i]) != v {
		return -1
	}
	return i
}

// cacheLoss returns the upper bound of the extra cost of a request that
// went to a different target than the previous request of its session:
// the cost of its input and cache write tokens, less the cost of the same
// tokens as cache reads (ADR 0028). The new turn of the conversation is
// part of these tokens, so the real loss is smaller.
func cacheLoss(price *pricing.Entry, u provider.Usage) int64 {
	if price == nil {
		return 0
	}
	paid := price.Cost(provider.Usage{InputTokens: u.InputTokens, CacheWriteTokens: u.CacheWriteTokens, CacheWrite1hTokens: u.CacheWrite1hTokens})
	read := price.Cost(provider.Usage{CacheReadTokens: u.InputTokens + u.CacheWriteTokens})
	return max(paid-read, 0)
}

// errBudgetCutoff is the cause of a stream that Tollgate stopped because a
// hard budget had no money left (ADR 0029).
var errBudgetCutoff = errors.New("hard budget reached during the stream")

// cutoff is a hard budget limit for one stream.
type cutoff struct {
	// room is the money in micro-USD that the request can use: the
	// smallest amount left in a hard budget, with its own reservation.
	room int64
	// window is the window of that budget, and message the text of the
	// error event.
	window  string
	message string
}

// runningMeter is a stream meter that knows the usage of the stream so
// far.
type runningMeter interface {
	provider.Meter
	TrackOutput()
	Running() (provider.Usage, bool)
}

// cutoffMeter stops a stream when its cost so far is more than the room
// of the request. Write returns errBudgetCutoff, and the relay then
// cancels the upstream request.
type cutoffMeter struct {
	runningMeter
	price *pricing.Entry
	// inputTokens is the input estimate, for a stream that did not report
	// its input usage yet.
	inputTokens int64
	room        int64
	hit         bool
}

func (m *cutoffMeter) Write(p []byte) (int, error) {
	n, err := m.runningMeter.Write(p)
	if err != nil {
		return n, err
	}
	if m.price.Cost(m.usage()) > m.room {
		m.hit = true
		return n, errBudgetCutoff
	}
	return n, nil
}

// usage returns the usage so far, with the input estimate if the stream
// did not report its input usage.
func (m *cutoffMeter) usage() provider.Usage {
	u, inputKnown := m.Running()
	if !inputKnown {
		u.InputTokens = m.inputTokens
	}
	return u
}

// Unwrap returns the stream meter, so that the server finds its methods.
func (m *cutoffMeter) Unwrap() provider.Meter { return m.runningMeter }

// innerMeter returns the meter that m wraps, if it wraps one.
func innerMeter(m provider.Meter) provider.Meter {
	if u, ok := m.(interface{ Unwrap() provider.Meter }); ok {
		return u.Unwrap()
	}
	return m
}

// roomOf returns the cutoff of a request from its admission, or nil if the
// request has no hard budget.
func (s *Server) roomOf(x *exchange, a *admission) *cutoff {
	if a == nil || a.decision.Local || !a.decision.Allowed {
		return nil
	}
	var c *cutoff
	for j, b := range a.budgets {
		if !b.Hard {
			continue
		}
		room := b.Limit - (a.decision.Spend[j] - b.Amount)
		if c == nil || room < c.room {
			w := a.used[j]
			msg := fmt.Sprintf("The month budget of the tenant %q is used. Tollgate stopped the response. Wait for the next month, or increase the limit_usd of the month budget.", x.identity.TenantID)
			if w.Window == config.WindowSession {
				msg = fmt.Sprintf("The budget of the session %q is used. Tollgate stopped the response. Start a new session, or increase the limit_usd of the session budget.", x.session)
			}
			c = &cutoff{room: room, window: w.Window, message: msg}
		}
	}
	return c
}

// sessionHeader sets the x-tollgate-session-remaining-usd header: the money
// left in the session budget after the reservation of the request.
func sessionHeader(x *exchange, a *admission) {
	for j, b := range a.used {
		if b.Window != config.WindowSession || a.decision.Local {
			continue
		}
		left := max(int64(b.LimitUSD)-a.decision.Spend[j], 0)
		if x.limitHeader == nil {
			x.limitHeader = make(http.Header)
		}
		v := config.USD(left).String()
		x.limitHeader.Set("X-Tollgate-Session-Remaining-Usd", v)
		x.w.Header().Set("X-Tollgate-Session-Remaining-Usd", v)
	}
}

// warnSession writes a warning when the request takes the spend of its
// session to the warning ratio of the session budget.
func (s *Server) warnSession(ctx context.Context, x *exchange, a *admission) {
	if a.decision.Local {
		return
	}
	for j, b := range a.used {
		if b.Window != config.WindowSession {
			continue
		}
		limit := int64(b.LimitUSD)
		before := a.decision.Spend[j] - a.budgets[j].Amount
		after := before + x.cost
		threshold := int64(s.warnRatio * float64(limit))
		if before < threshold && after >= threshold {
			s.metrics.SessionBudgetWarning()
			s.logger.WarnContext(ctx, "session budget warning",
				"request_id", x.id,
				"tenant_id", x.identity.TenantID,
				"session_id", x.session,
				"limit_micro_usd", limit,
				"spend_micro_usd", after,
				"warn_ratio", s.warnRatio,
			)
		}
	}
}

// serveCached sends a response from the exact response cache.
func (s *Server) serveCached(x *exchange, e *respcache.Entry) {
	h := x.w.Header()
	for k, v := range x.limitHeader {
		h[k] = v
	}
	h.Set("Content-Type", e.ContentType)
	h.Set("Content-Length", strconv.Itoa(len(e.Body)))
	h.Set("X-Tollgate-Cache", "hit")
	x.cache, x.status, x.wrote = "hit", http.StatusOK, true
	x.w.WriteHeader(http.StatusOK)
	if _, err := x.w.Write(e.Body); err != nil { //nolint:gosec // The body is a provider response, sent without changes (ADR 0009).
		x.err = err
	}
}

// captureWriter keeps a copy of the response bytes for the response
// cache, up to a limit. If the response is larger, it drops the copy.
type captureWriter struct {
	buf   bytes.Buffer
	limit int64
	over  bool
}

func (c *captureWriter) add(p []byte) {
	if c.over {
		return
	}
	if int64(c.buf.Len()+len(p)) > c.limit {
		c.over = true
		c.buf = bytes.Buffer{}
		return
	}
	c.buf.Write(p)
}

// flushStarted flushes the response to the client if its status and
// headers went out. Before that, a flush would send the status and headers
// too early, without the headers of Tollgate.
func flushStarted(dst *responseStartWriter) error {
	if !dst.started {
		return nil
	}
	if err := http.NewResponseController(dst.ResponseWriter).Flush(); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	return nil
}

// unmaskStream restores the placeholders of a stream on the way to the
// client. Unwrap gives the response controller the next writer.
type unmaskStream struct {
	*responseStartWriter
	u *redact.StreamUnmasker
}

func (w *unmaskStream) Write(p []byte) (int, error) { return w.u.Write(p) }

func (w *unmaskStream) Unwrap() http.ResponseWriter { return w.responseStartWriter }

// FlushError implements the flush of http.ResponseController.
func (w *unmaskStream) FlushError() error { return flushStarted(w.responseStartWriter) }

// unmaskBody keeps a response body that is not a stream, so that the
// placeholders can be restored in the complete JSON. If the body is larger
// than the limit, the writer sends it without changes.
type unmaskBody struct {
	*responseStartWriter
	buf   bytes.Buffer
	limit int
	over  bool
}

func (w *unmaskBody) Write(p []byte) (int, error) {
	if w.over {
		return w.responseStartWriter.Write(p)
	}
	if w.buf.Len()+len(p) > w.limit {
		w.over = true
		if _, err := w.responseStartWriter.Write(w.buf.Bytes()); err != nil {
			return 0, err
		}
		w.buf = bytes.Buffer{}
		return w.responseStartWriter.Write(p)
	}
	return w.buf.Write(p)
}

func (w *unmaskBody) Unwrap() http.ResponseWriter { return w.responseStartWriter }

// FlushError implements the flush of http.ResponseController.
func (w *unmaskBody) FlushError() error { return flushStarted(w.responseStartWriter) }

// finish writes the kept body with the placeholders restored.
func (w *unmaskBody) finish(m *redact.Mapping) error {
	if w.over {
		return nil
	}
	_, err := w.responseStartWriter.Write(m.UnmaskJSON(w.buf.Bytes()))
	return err
}

// unmaskBodyLimit is the largest response body that Tollgate keeps to
// restore placeholders. A larger body goes to the client with the
// placeholders.
const unmaskBodyLimit = 8 << 20

// redactFormat returns the stream format of the redact package for an API.
func redactFormat(api config.API) string {
	if api == config.APIAnthropic {
		return redact.FormatAnthropic
	}
	return redact.FormatOpenAI
}

// stickyRequest sets the sticky routing entry of the request in r, if the
// request has a session and the route has more than one target.
func stickyRequest(r *limiter.Request, tenant, session string, list *targetList, metered bool) string {
	if session == "" || !metered || len(list.targets) < 2 {
		return ""
	}
	r.Sticky = budget.StickyKey(tenant, session)
	return r.Sticky
}

// cacheLabel is the cache label of a request for the metrics.
func (x *exchange) cacheLabel() string {
	if x.cache == "" {
		return "bypass"
	}
	return x.cache
}
