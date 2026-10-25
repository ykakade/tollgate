package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ykakade/tollgate/internal/budget"
	"github.com/ykakade/tollgate/internal/config"
	"github.com/ykakade/tollgate/internal/limiter"
	"github.com/ykakade/tollgate/internal/preflight"
	"github.com/ykakade/tollgate/internal/pricing"
	"github.com/ykakade/tollgate/internal/provider"
	"github.com/ykakade/tollgate/internal/redact"
	"github.com/ykakade/tollgate/internal/telemetry"
	"github.com/ykakade/tollgate/internal/usage"
)

// Settings of the limit checks.
const (
	// storeRetryAfter is the retry-after value, in seconds, of the 503
	// response when the limit store is not available.
	storeRetryAfter = 5
	// settleTimeout is the longest time for the settle call after a
	// response.
	settleTimeout = 2 * time.Second
	// pingTimeout is the longest time for the store check of /readyz.
	pingTimeout = time.Second
)

// tenantPolicy is the configuration of one tenant.
type tenantPolicy struct {
	models  map[string]bool
	limits  config.Limits
	budgets []config.Budget
	// redactor masks personal data in the requests of the tenant, or is
	// nil.
	redactor *redact.Redactor
}

// admission is the result of the limit check of one request.
type admission struct {
	// prices and costs hold the price and the estimated cost of each
	// target. est has the largest cost, so the reservation covers each
	// target (ADR 0021).
	prices  []*pricing.Entry
	costs   []int64
	est     budget.Estimate
	rates   []limiter.Rate
	budgets []limiter.Budget
	// used holds the configured budget of each entry in budgets.
	used     []config.Budget
	decision limiter.Decision
	// sticky is the key of the sticky routing entry of the session, or "".
	sticky string
	list   *targetList
}

// counted reports whether the request uses the store.
func (a *admission) counted() bool {
	return len(a.rates) > 0 || len(a.budgets) > 0 || a.sticky != ""
}

// admit checks the rate limits and budgets of the request and reserves its
// estimated cost. If the request cannot continue, admit sends the error
// response and returns false.
func (s *Server) admit(ctx context.Context, x *exchange, ep endpoint, list *targetList, pre *preflight.Request, bodyLen int) (*admission, bool) {
	tenant := x.identity.TenantID
	tp := s.tenants[tenant]
	now := s.now()
	a := &admission{list: list}
	for i, t := range list.targets {
		price := s.prices.Find(t.Provider, t.kind, t.Model, now)
		est := budget.NewEstimate(price, preflight.EstimateInputTokens(bodyLen), pre.MaxTokens, s.reserveOutput)
		a.prices = append(a.prices, price)
		a.costs = append(a.costs, est.Cost)
		if i == 0 || est.Cost > a.est.Cost {
			a.est = est
		}
	}
	if ep.metered {
		a.budgets, a.used = budget.Budgets(tenant, tp.budgets, x.session, now, a.est.Cost)
	} else {
		// The response has no usage, so the request reserves nothing.
		a.est = budget.Estimate{}
	}
	a.rates = budget.Rates(tenant, tp.limits, a.est.Tokens(), ep.metered)
	req := &limiter.Request{Now: now, Rates: a.rates, Budgets: a.budgets}
	a.sticky = stickyRequest(req, tenant, x.session, list, ep.metered)
	if !a.counted() {
		a.decision.Allowed = true
		return a, true
	}

	d, err := s.store.Admit(ctx, req)
	if err != nil {
		x.err = err
		x.fail(http.StatusServiceUnavailable, fmt.Sprintf("Tollgate cannot check the budget of the tenant %q, because the limit store is not available. Send the request again later.", tenant), storeRetryAfter)
		return nil, false
	}
	a.decision = d
	x.limitHeader = rateHeader(d)
	for k, v := range x.limitHeader {
		x.w.Header()[k] = v
	}
	sessionHeader(x, a)
	if d.Allowed {
		s.warnSoftBudgets(ctx, x, a)
		return a, true
	}
	switch {
	case d.DeniedRate >= 0:
		r := a.rates[d.DeniedRate]
		secs := int(d.RetryAfter / time.Second)
		var msg string
		switch {
		case r.Key == budget.TokensKey(tenant) && r.Cost > r.Limit:
			msg = fmt.Sprintf("The request needs about %d tokens, but the tenant %q has a limit of %d tokens per minute. Make the request smaller, or increase limits.tokens_per_minute.", r.Cost, tenant, r.Limit)
		case r.Key == budget.TokensKey(tenant):
			msg = fmt.Sprintf("The tenant %q used its limit of %d tokens per minute. Wait %d seconds. Then send the request again.", tenant, r.Limit, secs)
		default:
			msg = fmt.Sprintf("The tenant %q sent its limit of %d requests per minute. Wait %d seconds. Then send the request again.", tenant, r.Limit, secs)
		}
		x.denied = "rate_limit"
		if r.Key == budget.TokensKey(tenant) {
			s.metrics.LimitRejection(telemetry.LimitTokens)
		} else {
			s.metrics.LimitRejection(telemetry.LimitRequests)
		}
		x.fail(http.StatusTooManyRequests, msg, secs)
	default:
		b := a.used[d.DeniedBudget]
		spend := config.USD(d.Spend[d.DeniedBudget])
		var msg string
		if b.Window == config.WindowSession {
			msg = fmt.Sprintf("The session %q does not have sufficient budget for this request. The session budget is %s USD, and the spend is %s USD. Start a new session, or increase the limit_usd of the session budget.", x.session, b.LimitUSD, spend)
		} else {
			msg = fmt.Sprintf("The tenant %q does not have sufficient budget for this request. The month budget is %s USD, and the spend is %s USD. Wait for the next month, or increase the limit_usd of the month budget.", tenant, b.LimitUSD, spend)
		}
		x.denied = "budget_" + b.Window
		s.metrics.LimitRejection(telemetry.LimitBudget)
		x.fail(http.StatusPaymentRequired, msg, 0)
	}
	return nil, false
}

// warnSoftBudgets logs a warning for each soft budget that the request
// took over its limit.
func (s *Server) warnSoftBudgets(ctx context.Context, x *exchange, a *admission) {
	if a.decision.Local {
		return
	}
	for i, b := range a.used {
		if b.Mode == config.ModeSoft && a.decision.Spend[i] > int64(b.LimitUSD) {
			s.logger.WarnContext(ctx, "soft budget exceeded",
				"request_id", x.id,
				"tenant_id", x.identity.TenantID,
				"window", b.Window,
				"limit_micro_usd", int64(b.LimitUSD),
				"spend_micro_usd", a.decision.Spend[i],
			)
		}
	}
}

// rateHeader returns the rate limit headers for the limit with the least
// room left.
func rateHeader(d limiter.Decision) http.Header {
	if len(d.Rates) == 0 {
		return nil
	}
	best := 0
	for i, r := range d.Rates {
		// Compare remaining/limit without division.
		if r.Remaining*d.Rates[best].Limit < d.Rates[best].Remaining*r.Limit {
			best = i
		}
	}
	r := d.Rates[best]
	return http.Header{
		"Ratelimit-Limit":     {strconv.FormatInt(r.Limit, 10)},
		"Ratelimit-Remaining": {strconv.FormatInt(r.Remaining, 10)},
		"Ratelimit-Reset":     {strconv.FormatInt(int64((r.Reset+time.Second-1)/time.Second), 10)},
	}
}

// settle replaces the estimates of the request with its real usage and
// cost, with the price of the target that served it. It runs after the
// response, also when the client disconnected. If the response stopped
// early, the usage can be lower than what the provider charges, so the
// request keeps at least the estimated cost of that target.
func (s *Server) settle(ctx context.Context, x *exchange, a *admission) {
	var tokens int64
	if x.usageKnown && x.served >= 0 {
		x.cost = budget.Charge(a.prices[x.served], x.usage, x.estimated || x.incomplete, a.costs[x.served])
		tokens = totalTokens(x.usage)
		if x.prevTarget >= 0 && x.prevTarget != x.served {
			x.switched = true
			x.cacheLoss = cacheLoss(a.prices[x.served], x.usage)
		}
	}
	if !a.counted() || !a.decision.Allowed {
		return
	}
	s.warnSession(ctx, x, a)
	st := &limiter.Settlement{Now: s.now(), Minute: a.decision.Minute, Local: a.decision.Local}
	if a.sticky != "" && x.served >= 0 && x.status >= 200 && x.status < 300 {
		t := a.list.targets[x.served]
		st.Sticky = &limiter.Entry{Key: a.sticky, Value: stickyValue(x.served, t), TTL: t.cacheTTL}
	}
	for _, r := range a.rates {
		if r.Key == budget.TokensKey(x.identity.TenantID) {
			st.Rates = append(st.Rates, limiter.Adjust{Key: r.Key, Delta: tokens - r.Cost})
		}
	}
	for _, b := range a.budgets {
		st.Budgets = append(st.Budgets, limiter.Adjust{Key: b.Key, Delta: x.cost - b.Amount, TTL: b.TTL})
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()
	if err := s.store.Settle(sctx, st); err != nil {
		s.logger.WarnContext(ctx, "settle failed", "request_id", x.id, "error", err.Error())
	}
}

func totalTokens(u provider.Usage) int64 {
	return u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

// event returns the usage event of the request.
func (x *exchange) event() usage.Event {
	e := usage.Event{
		RequestID:          x.id,
		TS:                 x.start.UTC(),
		TenantID:           x.identity.TenantID,
		KeyID:              x.identity.KeyID,
		SessionID:          x.session,
		RouteModel:         x.routeModel,
		Provider:           x.provider,
		ProviderModel:      x.providerModel,
		Status:             x.status,
		Attempts:           x.attempts,
		Cache:              x.cacheLabel(),
		InputTokens:        x.usage.InputTokens,
		OutputTokens:       x.usage.OutputTokens,
		CacheReadTokens:    x.usage.CacheReadTokens,
		CacheWriteTokens:   x.usage.CacheWriteTokens,
		CacheWrite1hTokens: x.usage.CacheWrite1hTokens,
		ReasoningTokens:    x.usage.ReasoningTokens,
		UsageEstimated:     x.estimated,
		CostMicroUSD:       x.cost,
		DurationMs:         time.Since(x.start).Milliseconds(),
		OverheadUs:         x.overhead.Microseconds(),
		SessionDerived:     x.sessionDerived,
		TargetSwitched:     x.switched,
		CacheLossMicroUSD:  x.cacheLoss,
		BudgetCutoff:       x.cutoff,
		Redacted:           x.mapping.Len(),
	}
	if x.ttft >= 0 {
		ms := x.ttft.Milliseconds()
		e.TTFTMs = &ms
	}
	return e
}

// storeState returns "ok" if the limit store answers, else "unavailable".
func (s *Server) storeState(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		return "unavailable"
	}
	return "ok"
}

// RestoreSpend adds the spend in sp to the limit store. Use it at start,
// before Serve, to rebuild the spend of a memory store from the usage
// file (ADR 0019). Do not use it with a Redis store: Redis keeps the spend
// when Tollgate stops.
func (s *Server) RestoreSpend(ctx context.Context, sp usage.Spend) error {
	now := s.now()
	st := &limiter.Settlement{Now: now}
	for tenant, cost := range sp.Month {
		if cost > 0 {
			st.Budgets = append(st.Budgets, limiter.Adjust{Key: budget.MonthKey(tenant, now), Delta: cost, TTL: budget.MonthTTL(now)})
		}
	}
	for _, ss := range sp.Sessions {
		if ss.Cost > 0 {
			ttl := budget.SessionTTL - now.Sub(ss.Last)
			st.Budgets = append(st.Budgets, limiter.Adjust{Key: budget.SessionKey(ss.TenantID, ss.SessionID), Delta: ss.Cost, TTL: ttl})
		}
	}
	if err := s.store.Settle(ctx, st); err != nil {
		return fmt.Errorf("restore spend: %w", err)
	}
	return nil
}
