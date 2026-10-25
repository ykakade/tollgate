package limiter

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// DefaultBackoff is the time that a Failover store uses only Local after
// Main fails.
const DefaultBackoff = 5 * time.Second

// Failover is a Store that uses Main, and Local when Main fails (ADR
// 0006):
//
//   - If Main fails and the request has a hard budget, Admit returns an
//     error that wraps ErrUnavailable. The request fails closed.
//   - Else Admit checks only the rate limits, in Local. Each Tollgate copy
//     then has its own local limits. Soft budgets record no spend.
//
// After Main fails in Admit, Failover does not call Main from Admit for
// the Backoff time, so that requests do not wait for the timeouts of Main
// during an outage.
type Failover struct {
	Main  Store
	Local *Memory
	// Backoff is the time to use only Local after Main fails. If it is 0,
	// Failover uses DefaultBackoff.
	Backoff time.Duration
	// OnError, if not nil, gets each error of Main, with the operation:
	// "admit", "settle" or "ping".
	OnError func(op string, err error)

	// downUntil is the Unix time in nanoseconds until which Admit does
	// not call Main.
	downUntil atomic.Int64
}

func (f *Failover) report(op string, err error) {
	if f.OnError != nil {
		f.OnError(op, err)
	}
}

// Admit implements Store.
func (f *Failover) Admit(ctx context.Context, r *Request) (Decision, error) {
	err := errBackoff
	if r.Now.UnixNano() >= f.downUntil.Load() {
		var d Decision
		if d, err = f.Main.Admit(ctx, r); err == nil {
			return d, nil
		}
		f.report("admit", err)
		backoff := f.Backoff
		if backoff == 0 {
			backoff = DefaultBackoff
		}
		f.downUntil.Store(r.Now.Add(backoff).UnixNano())
	}
	for _, b := range r.Budgets {
		if b.Hard {
			return Decision{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
	}
	local := &Request{Now: r.Now, Rates: r.Rates, Sticky: r.Sticky}
	d, err := f.Local.Admit(ctx, local)
	if err != nil {
		return Decision{}, fmt.Errorf("local admit: %w", err)
	}
	d.Spend = make([]int64, len(r.Budgets))
	d.Local = true
	return d, nil
}

// errBackoff is the cause of ErrUnavailable in the Backoff time.
var errBackoff = errors.New("the store failed a short time ago")

// Settle implements Store.
func (f *Failover) Settle(ctx context.Context, s *Settlement) error {
	if s.Local {
		local := *s
		local.Budgets = nil
		return f.Local.Settle(ctx, &local)
	}
	if err := f.Main.Settle(ctx, s); err != nil {
		f.report("settle", err)
		return err
	}
	return nil
}

// Ping implements Store. It reports the state of Main.
func (f *Failover) Ping(ctx context.Context) error {
	if err := f.Main.Ping(ctx); err != nil {
		f.report("ping", err)
		return err
	}
	return nil
}
