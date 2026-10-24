// Package budget turns the limits and budgets of a tenant into the counters
// of the limit store, and estimates the reservation of a request (ADR 0005
// and ADR 0018).
//
// All keys of a tenant have the tenant in a Redis hash tag, so that the
// Lua script of one request touches one Redis Cluster slot.
package budget

import (
	"time"

	"github.com/ykakade/tollgate/internal/config"
	"github.com/ykakade/tollgate/internal/limiter"
	"github.com/ykakade/tollgate/internal/pricing"
	"github.com/ykakade/tollgate/internal/provider"
)

// SessionTTL is the lifetime of a session budget counter after its last
// change.
const SessionTTL = 24 * time.Hour

// monthGrace is the time that a month counter stays after the month ends.
const monthGrace = 7 * 24 * time.Hour

// maxSessionLen is the largest length of a session ID.
const maxSessionLen = 128

func tag(tenant string) string { return "{" + tenant + "}" }

// RequestsKey is the key prefix of the request rate counter of a tenant.
func RequestsKey(tenant string) string { return "tg:rl:req:" + tag(tenant) }

// TokensKey is the key prefix of the token rate counter of a tenant.
func TokensKey(tenant string) string { return "tg:rl:tok:" + tag(tenant) }

// MonthKey is the key of the month budget counter of a tenant for the
// month of t, in UTC.
func MonthKey(tenant string, t time.Time) string {
	return "tg:bud:" + tag(tenant) + ":month:" + t.UTC().Format("2006-01")
}

// SessionKey is the key of the session budget counter of a session.
func SessionKey(tenant, session string) string {
	return "tg:bud:sess:" + tag(tenant) + ":" + session
}

// MonthTTL is the lifetime of the month counter at t: to the end of the
// month in UTC and 7 days more.
func MonthTTL(t time.Time) time.Duration {
	u := t.UTC()
	next := time.Date(u.Year(), u.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	return next.Add(monthGrace).Sub(u)
}

// ValidSession reports whether s can be a session ID: 1 to 128 visible
// ASCII characters.
func ValidSession(s string) bool {
	if s == "" || len(s) > maxSessionLen {
		return false
	}
	for i := range len(s) {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// Rates returns the rate limits of a tenant for a request with an
// estimate of tokens. A limit of 0 has no counter. If metered is false,
// the request has no token count, and only the request limit applies.
func Rates(tenant string, l config.Limits, tokens int64, metered bool) []limiter.Rate {
	var out []limiter.Rate
	if l.RequestsPerMinute > 0 {
		out = append(out, limiter.Rate{Key: RequestsKey(tenant), Limit: l.RequestsPerMinute, Cost: 1})
	}
	if l.TokensPerMinute > 0 && metered {
		out = append(out, limiter.Rate{Key: TokensKey(tenant), Limit: l.TokensPerMinute, Cost: tokens})
	}
	return out
}

// Budgets returns the budget counters of a request with a reservation of
// amount micro-USD, and the configured budget of each counter. A session
// budget applies only if the request has a session ID.
func Budgets(tenant string, budgets []config.Budget, session string, now time.Time, amount int64) ([]limiter.Budget, []config.Budget) {
	var out []limiter.Budget
	var used []config.Budget
	for _, b := range budgets {
		lb := limiter.Budget{Limit: int64(b.LimitUSD), Amount: amount, Hard: b.Mode == config.ModeHard}
		switch b.Window {
		case config.WindowMonth:
			lb.Key, lb.TTL = MonthKey(tenant, now), MonthTTL(now)
		case config.WindowSession:
			if session == "" {
				continue
			}
			lb.Key, lb.TTL = SessionKey(tenant, session), SessionTTL
		default:
			continue
		}
		out = append(out, lb)
		used = append(used, b)
	}
	return out, used
}

// Estimate is the estimate of a request before it goes to the provider.
type Estimate struct {
	InputTokens  int64
	OutputTokens int64
	// Cost is the reservation in micro-USD. It is 0 if the request has no
	// price.
	Cost int64
}

// Tokens returns the token count that the request adds to the token rate
// counter.
func (e Estimate) Tokens() int64 { return e.InputTokens + e.OutputTokens }

// NewEstimate returns the estimate of a request with the given input
// token estimate and output limit. The output part is the smaller of
// maxTokens and reserveOutput. If maxTokens is 0 (no limit in the
// request), it is reserveOutput. The price can be nil.
func NewEstimate(price *pricing.Entry, inputTokens, maxTokens, reserveOutput int64) Estimate {
	out := reserveOutput
	if maxTokens > 0 {
		out = min(maxTokens, reserveOutput)
	}
	e := Estimate{InputTokens: inputTokens, OutputTokens: out}
	if price != nil {
		e.Cost = price.Cost(provider.Usage{InputTokens: inputTokens, OutputTokens: out})
	}
	return e
}

// Charge returns the cost to settle for a request. If the provider
// reported the full usage, it is the cost of the usage. If the usage is an
// estimate or is not complete, it is the larger of that cost and the
// reservation, because the output of the response is not known. The price
// can be nil.
func Charge(price *pricing.Entry, u provider.Usage, notExact bool, reserved int64) int64 {
	if price == nil {
		return 0
	}
	c := price.Cost(u)
	if notExact {
		c = max(c, reserved)
	}
	return c
}
