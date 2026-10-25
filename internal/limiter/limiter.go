// Package limiter keeps the rate limit and budget state of Tollgate behind
// one Store interface (ADR 0004).
//
// For each request, one Admit call checks all rate limits and hard budgets
// and, if the request is allowed, adds its cost to each of them. The call is
// atomic: it changes all counters or none. After the response, one Settle
// call replaces the estimates with the real values.
//
// Rate limits use a sliding-window counter with one-minute windows. The
// estimate of the requests in the last minute is
//
//	previous minute × (1 − elapsed part of this minute) + this minute
//
// The Memory store keeps the counters in the process. The Redis store keeps
// them in Redis, with one Lua script for each call, so that more than one
// Tollgate copy can share them.
package limiter

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// Window is the length of a rate limit window.
const Window = time.Minute

// windowMs is Window in milliseconds.
const windowMs = int64(Window / time.Millisecond)

// rateTTL is the lifetime of a rate counter: its own minute and the next
// minute, in which it is the previous minute.
const rateTTL = 2 * Window

// ErrUnavailable means that the store cannot be used. Requests with a hard
// budget fail closed (ADR 0006).
var ErrUnavailable = errors.New("limit store is not available")

// Rate is one rate limit of a request.
type Rate struct {
	// Key is the prefix of the counter keys. The store adds ":" and the
	// minute number.
	Key string
	// Limit is the largest count in a window.
	Limit int64
	// Cost is the amount that the request adds to the counter.
	Cost int64
}

// Budget is one budget of a request.
type Budget struct {
	// Key is the key of the spend counter.
	Key string
	// Limit is the largest spend in micro-USD.
	Limit int64
	// Amount is the reservation in micro-USD.
	Amount int64
	// Hard is true if the budget stops requests. A soft budget only
	// records the spend.
	Hard bool
	// TTL is the lifetime of the counter after each change.
	TTL time.Duration
}

// Request is the input of Admit.
type Request struct {
	Now     time.Time
	Rates   []Rate
	Budgets []Budget
	// Sticky, if not empty, is the key of a sticky routing entry. Admit
	// reads it into Decision.Sticky. It does not change it.
	Sticky string
}

// RateState is the state of one rate limit after Admit.
type RateState struct {
	Limit int64
	// Remaining is the estimate of the count that the window still
	// allows.
	Remaining int64
	// Reset is the time to the end of the current minute.
	Reset time.Duration
}

// Decision is the result of Admit.
type Decision struct {
	// Allowed is true if the request can go to the provider. Then all
	// counters include its cost.
	Allowed bool
	// DeniedRate is the index in Request.Rates of the limit that stopped
	// the request, or -1.
	DeniedRate int
	// DeniedBudget is the index in Request.Budgets of the hard budget that
	// stopped the request, or -1.
	DeniedBudget int
	// RetryAfter is an estimate of the time until the denied rate limit
	// allows the request. It is 0 if a rate limit did not stop the
	// request.
	RetryAfter time.Duration
	// Rates holds the state of each rate limit.
	Rates []RateState
	// Spend holds the spend of each budget in micro-USD, with all open
	// reservations. If the request is allowed, it includes its own
	// reservation.
	Spend []int64
	// Minute is the minute number of the rate counters that Admit used.
	Minute int64
	// Local is true if a Failover store used its local store because the
	// main store failed. Then no budget has a reservation.
	Local bool
	// Sticky is the value of the sticky routing entry of the request, or
	// 0 if the request has none or the entry expired.
	Sticky int64
}

// Adjust is one change of a counter.
type Adjust struct {
	Key   string
	Delta int64
	// TTL is the lifetime of the counter after the change.
	TTL time.Duration
}

// Settlement is the input of Settle.
type Settlement struct {
	Now time.Time
	// Minute is Decision.Minute of the request.
	Minute int64
	// Rates holds changes of rate counters. Adjust.Key is the prefix, as
	// in Rate.Key, and the store adds Minute. The store ignores the
	// changes if Minute is older than the previous minute, and it ignores
	// Adjust.TTL.
	Rates []Adjust
	// Budgets holds changes of spend counters.
	Budgets []Adjust
	// Local is Decision.Local of the request.
	Local bool
	// Sticky, if not nil, sets a sticky routing entry.
	Sticky *Entry
}

// Entry is a value with a lifetime.
type Entry struct {
	Key   string
	Value int64
	TTL   time.Duration
}

// Store holds the limit state.
type Store interface {
	// Admit checks the rate limits and hard budgets of r. If none stops
	// the request, it adds the costs and the reservations to the counters.
	Admit(ctx context.Context, r *Request) (Decision, error)
	// Settle adds the changes in s to the counters. A counter does not go
	// below 0.
	Settle(ctx context.Context, s *Settlement) error
	// Ping checks that the store can be used.
	Ping(ctx context.Context) error
}

// minute returns the minute number of t and the milliseconds since the
// start of that minute.
func minute(t time.Time) (n, elapsed int64) {
	ms := t.UnixMilli()
	return ms / windowMs, ms % windowMs
}

func rateKey(prefix string, minute int64) string {
	return prefix + ":" + strconv.FormatInt(minute, 10)
}

// counts holds the counter values that Admit reads.
type counts struct {
	cur, prev []int64 // for each rate
	spend     []int64 // for each budget
}

// over reports whether a request with cost would take the sliding-window
// estimate over limit. Lua in redis.go has the same formula.
func over(prev, cur, cost, limit, elapsed int64) bool {
	return prev*(windowMs-elapsed)+(cur+cost)*windowMs > limit*windowMs
}

// denied returns the 1-based index of the first limit that stops r, or 0:
// first the rates, then the budgets.
func denied(r *Request, c counts, elapsed int64) int {
	for i, rt := range r.Rates {
		if over(c.prev[i], c.cur[i], rt.Cost, rt.Limit, elapsed) {
			return i + 1
		}
	}
	for j, b := range r.Budgets {
		if b.Hard && c.spend[j]+b.Amount > b.Limit {
			return len(r.Rates) + j + 1
		}
	}
	return 0
}

// decide makes the Decision from the counters that Admit read before it
// added the costs, and the result of denied.
func decide(r *Request, c counts, deniedIndex int, minuteN, elapsed int64) Decision {
	d := Decision{
		Allowed:      deniedIndex == 0,
		DeniedRate:   -1,
		DeniedBudget: -1,
		Minute:       minuteN,
		Rates:        make([]RateState, len(r.Rates)),
		Spend:        make([]int64, len(r.Budgets)),
	}
	switch {
	case deniedIndex == 0:
	case deniedIndex <= len(r.Rates):
		d.DeniedRate = deniedIndex - 1
		rt := r.Rates[d.DeniedRate]
		d.RetryAfter = retryAfter(c.prev[d.DeniedRate], c.cur[d.DeniedRate], rt.Cost, rt.Limit, elapsed)
	default:
		d.DeniedBudget = deniedIndex - len(r.Rates) - 1
	}
	reset := time.Duration(windowMs-elapsed) * time.Millisecond
	for i, rt := range r.Rates {
		cur := c.cur[i]
		if d.Allowed {
			cur += rt.Cost
		}
		left := rt.Limit*windowMs - c.prev[i]*(windowMs-elapsed) - cur*windowMs
		d.Rates[i] = RateState{Limit: rt.Limit, Remaining: max(left/windowMs, 0), Reset: reset}
	}
	for j, b := range r.Budgets {
		d.Spend[j] = c.spend[j]
		if d.Allowed {
			d.Spend[j] += b.Amount
		}
	}
	return d
}

// retryAfter returns an estimate of the time until the sliding window
// allows a request with cost, if no other request comes. The result is a
// whole number of seconds from 1 s to two windows.
func retryAfter(prev, cur, cost, limit, elapsed int64) time.Duration {
	wait := windowMs
	switch {
	case cost > limit:
		// The request never fits. Tell the client to wait one window.
	case cur+cost <= limit && prev > 0:
		// Later in this minute, the previous minute has less weight.
		need := windowMs - (limit-cur-cost)*windowMs/prev
		if need < windowMs {
			wait = need - elapsed
			break
		}
		fallthrough
	default:
		// In the next minute, this minute becomes the previous minute.
		need := int64(0)
		if cur > 0 {
			need = max(windowMs-(limit-cost)*windowMs/cur, 0)
		}
		wait = windowMs - elapsed + need
	}
	secs := (wait + 999) / 1000
	return time.Duration(min(max(secs, 1), 2*windowMs/1000)) * time.Second
}
