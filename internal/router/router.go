// Package router chooses the upstream target of each attempt of a request.
//
// A Router keeps one circuit breaker for each provider, model and region
// (ADR 0023), a cooldown for each target that answered 429, and one retry
// budget for the extra attempts of all requests (ADR 0022). A Plan walks the
// ordered targets of one request and applies the retry rules of ADR 0021.
//
// The state is in the memory of one Tollgate copy. Copies do not share it.
package router

import (
	"net/http"
	"sync"
	"time"
)

// Key identifies one breaker.
type Key struct {
	Provider string
	Model    string
	// Region is empty for a provider that has no region.
	Region string
}

// Action is what a request does after an attempt.
type Action int

// The actions.
const (
	// Done: the attempt is the last one. Its response goes to the client.
	Done Action = iota
	// RetrySame: send the request to the same target one more time, then
	// to the next target.
	RetrySame
	// NextTarget: send the request to the next target.
	NextTarget
)

// Outcome is the result of an attempt for its breaker.
type Outcome int

// The outcomes.
const (
	// Neutral: the attempt says nothing about the health of the target,
	// for example a 4xx status or a client that disconnected.
	Neutral Outcome = iota
	Success
	Failure
)

// Result describes one attempt for Classify.
type Result struct {
	// Status is the upstream status, or 0 if no response came.
	Status int
	// Err is the error of the attempt, or nil.
	Err error
	// Started is true if a byte of the response went to the client.
	Started bool
	// ClientGone is true if the attempt stopped because of the client: a
	// disconnect or a failed write to the client.
	ClientGone bool
}

// StatusOverloaded is the status of the Anthropic API when it is
// overloaded.
const StatusOverloaded = 529

// Classify returns the action and the breaker outcome of an attempt
// (ADR 0021). An attempt that started can only be Done.
func Classify(r Result) (Action, Outcome) {
	switch {
	case r.ClientGone:
		return Done, Neutral
	case r.Started && r.Err != nil:
		return Done, Failure
	case r.Started:
		return Done, statusOutcome(r.Status)
	case r.Err != nil:
		// Connect and first-token timeouts, refused connections and
		// resets before the first byte.
		return NextTarget, Failure
	}
	switch s := r.Status; {
	case s >= 200 && s < 300:
		return Done, Success
	case s == http.StatusTooManyRequests:
		// A rate limit is not a fault of the target. The cooldown keeps
		// requests away from it until its retry-after time.
		return NextTarget, Neutral
	case s == StatusOverloaded:
		return NextTarget, Failure
	case s >= 500:
		return RetrySame, Failure
	default:
		return Done, Neutral
	}
}

func statusOutcome(status int) Outcome {
	switch {
	case status >= 200 && status < 300:
		return Success
	case status >= 500:
		return Failure
	default:
		return Neutral
	}
}

// Settings are the inputs of New.
type Settings struct {
	// FailureRatio opens a breaker when this part of the attempts in the
	// window failed.
	FailureRatio float64
	// MinRequests is the smallest number of attempts in the window that
	// can open a breaker.
	MinRequests int
	// OpenFor is the time that an open breaker refuses attempts.
	OpenFor time.Duration
	// BudgetRatio is the largest part of requests that can have an extra
	// attempt, above the floor of BudgetFloor extra attempts in each
	// window.
	BudgetRatio float64
	// Now returns the current time. If nil, the router uses time.Now.
	Now func() time.Time
	// OnChange, if not nil, is called when a breaker changes its state.
	// The router calls it with its lock held, so it must not call the
	// router.
	OnChange func(k Key, from, to State)
}

// Router holds the breakers, the cooldowns and the retry budget. Use New to
// make one. It is safe for concurrent use.
type Router struct {
	s Settings

	mu       sync.Mutex
	breakers map[Key]*breaker
	budget   counter // Requests and extra attempts in the window.
}

// New returns a Router with all breakers closed.
func New(s Settings) *Router {
	if s.Now == nil {
		s.Now = time.Now
	}
	return &Router{s: s, breakers: make(map[Key]*breaker)}
}

func (r *Router) breaker(k Key) *breaker {
	b, ok := r.breakers[k]
	if !ok {
		b = &breaker{key: k}
		r.breakers[k] = b
	}
	return b
}

// Ticket is the permission for one attempt. Give it to Report when the
// attempt ends.
type Ticket struct {
	b   *breaker
	gen uint64
}

// Blocked tells why Allow refused an attempt.
type Blocked int

// The reasons of Allow.
const (
	NotBlocked Blocked = iota
	// BreakerOpen: the breaker is open.
	BreakerOpen
	// ProbeBusy: the breaker is half-open, and its one test attempt is
	// running.
	ProbeBusy
	// CoolingDown: the target answered 429, and its retry-after time did
	// not end.
	CoolingDown
)

// Allow reports whether an attempt to k can start now. If the breaker is
// open and its open time ended, Allow makes it half-open and gives this
// caller its one test attempt. A caller that gets a ticket must call Report.
func (r *Router) Allow(k Key) (Ticket, Blocked) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.s.Now()
	b := r.breaker(k)
	if now.Before(b.coolUntil) {
		return Ticket{}, CoolingDown
	}
	switch b.state {
	case Open:
		if now.Before(b.openUntil) {
			return Ticket{}, BreakerOpen
		}
		r.setState(b, HalfOpen, now)
		b.probing = true
	case HalfOpen:
		if b.probing {
			return Ticket{}, ProbeBusy
		}
		b.probing = true
	}
	return Ticket{b: b, gen: b.gen}, NotBlocked
}

// Report records the outcome of the attempt of t. An outcome of an attempt
// that started before the last state change of the breaker has no effect.
func (r *Router) Report(t Ticket, o Outcome) {
	if t.b == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b := t.b
	if t.gen != b.gen {
		return
	}
	now := r.s.Now()
	switch b.state {
	case HalfOpen:
		b.probing = false
		switch o {
		case Success:
			r.setState(b, Closed, now)
		case Failure:
			r.setState(b, Open, now)
		}
	case Closed:
		if o == Neutral {
			return
		}
		b.window.add(now, 1, boolInt(o == Failure))
		total, failures := b.window.sum(now)
		if total >= int64(r.s.MinRequests) && float64(failures) >= r.s.FailureRatio*float64(total) {
			r.setState(b, Open, now)
		}
	}
}

// CoolDown makes Allow refuse k until d has passed. Use it for a 429
// response with a retry-after header.
func (r *Router) CoolDown(k Key, d time.Duration) {
	if d <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.breaker(k)
	if until := r.s.Now().Add(d); until.After(b.coolUntil) {
		b.coolUntil = until
	}
}

// ReadyIn returns the shortest time until one of keys can get an attempt
// again. It returns 0 if one of them can get an attempt now.
func (r *Router) ReadyIn(keys []Key) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.s.Now()
	var best time.Duration
	for i, k := range keys {
		b := r.breaker(k)
		at := b.coolUntil
		switch {
		case b.state == Open && b.openUntil.After(at):
			at = b.openUntil
		case b.state == HalfOpen && b.probing:
			// The test attempt can take a long time. Ask the client to
			// wait one open time.
			at = now.Add(r.s.OpenFor)
		}
		d := max(at.Sub(now), 0)
		if i == 0 || d < best {
			best = d
		}
	}
	return best
}

// State returns the state of the breaker of k.
func (r *Router) State(k Key) State {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.breakers[k]
	if !ok {
		return Closed
	}
	if b.state == Open && !r.s.Now().Before(b.openUntil) {
		return HalfOpen
	}
	return b.state
}

func (r *Router) setState(b *breaker, to State, now time.Time) {
	from := b.state
	b.state = to
	b.gen++
	b.probing = false
	switch to {
	case Open:
		b.openUntil = now.Add(r.s.OpenFor)
	case Closed:
		b.window = counter{}
	}
	if r.s.OnChange != nil && from != to {
		r.s.OnChange(b.key, from, to)
	}
}

// BudgetFloor is the number of extra attempts in each window that the retry
// budget always allows, so a Tollgate with little traffic can still fall
// back.
const BudgetFloor = 10

// request counts one request for the retry budget.
func (r *Router) request() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.budget.add(r.s.Now(), 1, 0)
}

// tryRetry reports whether the retry budget has room for one more extra
// attempt, and counts the attempt if it has.
func (r *Router) tryRetry() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.s.Now()
	requests, retries := r.budget.sum(now)
	if float64(retries+1) > BudgetFloor+r.s.BudgetRatio*float64(requests) {
		return false
	}
	r.budget.add(now, 0, 1)
	return true
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
