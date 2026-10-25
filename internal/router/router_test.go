package router

import (
	"errors"
	"testing"
	"time"
)

var errUpstream = errors.New("upstream failed")

func TestClassify(t *testing.T) {
	for _, tt := range []struct {
		name    string
		r       Result
		action  Action
		outcome Outcome
	}{
		{"200", Result{Status: 200}, Done, Success},
		{"400", Result{Status: 400}, Done, Neutral},
		{"401", Result{Status: 401}, Done, Neutral},
		{"429", Result{Status: 429}, NextTarget, Neutral},
		{"500", Result{Status: 500}, RetrySame, Failure},
		{"502", Result{Status: 502}, RetrySame, Failure},
		{"503", Result{Status: 503}, RetrySame, Failure},
		{"504", Result{Status: 504}, RetrySame, Failure},
		{"529", Result{Status: 529}, NextTarget, Failure},
		{"501", Result{Status: 501}, RetrySame, Failure},
		{"error before the first byte", Result{Err: errUpstream}, NextTarget, Failure},
		{"timeout after 200 headers", Result{Status: 200, Err: errUpstream}, NextTarget, Failure},
		{"error after the first byte", Result{Status: 200, Err: errUpstream, Started: true}, Done, Failure},
		{"stream complete", Result{Status: 200, Started: true}, Done, Success},
		{"relayed 500", Result{Status: 500, Started: true}, Done, Failure},
		{"relayed 429", Result{Status: 429, Started: true}, Done, Neutral},
		{"client gone", Result{Status: 200, Err: errUpstream, Started: true, ClientGone: true}, Done, Neutral},
		{"client gone before the response", Result{Err: errUpstream, ClientGone: true}, Done, Neutral},
	} {
		a, o := Classify(tt.r)
		if a != tt.action || o != tt.outcome {
			t.Errorf("%s: Classify = %v, %v; want %v, %v", tt.name, a, o, tt.action, tt.outcome)
		}
	}
}

// clock is a manual clock for tests.
type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock               { return &clock{t: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)} }
func newRouter(c *clock, s Settings) *Router {
	s.Now = c.now
	if s.FailureRatio == 0 {
		s.FailureRatio = 0.5
	}
	if s.MinRequests == 0 {
		s.MinRequests = 4
	}
	if s.OpenFor == 0 {
		s.OpenFor = 30 * time.Second
	}
	return New(s)
}

var keyA = Key{Provider: "a", Model: "m"}

// attempt runs one attempt to k with outcome o. It fails the test if the
// router refuses the attempt.
func attempt(t *testing.T, r *Router, k Key, o Outcome) {
	t.Helper()
	tk, why := r.Allow(k)
	if why != NotBlocked {
		t.Fatalf("Allow(%v) = %v, want an attempt", k, why)
	}
	r.Report(tk, o)
}

func TestBreakerOpensAndCloses(t *testing.T) {
	c := newClock()
	var changes []string
	r := newRouter(c, Settings{OnChange: func(_ Key, from, to State) {
		changes = append(changes, from.String()+">"+to.String())
	}})
	// 3 failures in 3 attempts: fewer than min_requests, so still closed.
	for range 3 {
		attempt(t, r, keyA, Failure)
	}
	if r.State(keyA) != Closed {
		t.Fatalf("state = %v after 3 attempts, want closed", r.State(keyA))
	}
	// Neutral outcomes do not count.
	attempt(t, r, keyA, Neutral)
	if r.State(keyA) != Closed {
		t.Fatalf("state = %v after a neutral outcome, want closed", r.State(keyA))
	}
	attempt(t, r, keyA, Failure)
	if r.State(keyA) != Open {
		t.Fatalf("state = %v after 4 failures, want open", r.State(keyA))
	}
	if _, why := r.Allow(keyA); why != BreakerOpen {
		t.Errorf("Allow while open = %v, want BreakerOpen", why)
	}
	if got := r.ReadyIn([]Key{keyA}); got != 30*time.Second {
		t.Errorf("ReadyIn = %v, want 30s", got)
	}

	// After the open time, one test attempt.
	c.add(30 * time.Second)
	probe, why := r.Allow(keyA)
	if why != NotBlocked {
		t.Fatalf("Allow after open_for = %v, want the test attempt", why)
	}
	if _, why := r.Allow(keyA); why != ProbeBusy {
		t.Errorf("second Allow while half-open = %v, want ProbeBusy", why)
	}
	// A failed test opens the breaker again.
	r.Report(probe, Failure)
	if r.State(keyA) != Open {
		t.Fatalf("state = %v after a failed test, want open", r.State(keyA))
	}
	c.add(30 * time.Second)
	probe, _ = r.Allow(keyA)
	// A neutral test frees the test slot, and the breaker stays half-open.
	r.Report(probe, Neutral)
	probe, why = r.Allow(keyA)
	if why != NotBlocked {
		t.Fatalf("Allow after a neutral test = %v", why)
	}
	r.Report(probe, Success)
	if r.State(keyA) != Closed {
		t.Fatalf("state = %v after a good test, want closed", r.State(keyA))
	}
	// The window starts again after the breaker closes.
	for range 3 {
		attempt(t, r, keyA, Failure)
	}
	if r.State(keyA) != Closed {
		t.Errorf("state = %v, want closed: old failures must not count", r.State(keyA))
	}
	want := []string{"closed>open", "open>half-open", "half-open>open", "open>half-open", "half-open>closed"}
	if len(changes) != len(want) {
		t.Fatalf("changes = %v, want %v", changes, want)
	}
	for i := range want {
		if changes[i] != want[i] {
			t.Errorf("changes = %v, want %v", changes, want)
			break
		}
	}
}

func TestBreakerFailureRatio(t *testing.T) {
	c := newClock()
	r := newRouter(c, Settings{MinRequests: 10})
	// 4 failures in 10 attempts is less than 0.5.
	for i := range 10 {
		o := Success
		if i < 4 {
			o = Failure
		}
		attempt(t, r, keyA, o)
	}
	if r.State(keyA) != Closed {
		t.Fatalf("state = %v, want closed at 40%% failures", r.State(keyA))
	}
	// After the window, the old attempts do not count.
	c.add(Window)
	for range 9 {
		attempt(t, r, keyA, Failure)
	}
	if r.State(keyA) != Closed {
		t.Fatalf("state = %v, want closed: 9 attempts in the window", r.State(keyA))
	}
	attempt(t, r, keyA, Failure)
	if r.State(keyA) != Open {
		t.Errorf("state = %v, want open at 10 failures", r.State(keyA))
	}
}

func TestStaleReportIsIgnored(t *testing.T) {
	c := newClock()
	r := newRouter(c, Settings{MinRequests: 1})
	old, _ := r.Allow(keyA) // A long stream that started while closed.
	attempt(t, r, keyA, Failure)
	c.add(30 * time.Second)
	probe, why := r.Allow(keyA)
	if why != NotBlocked {
		t.Fatal(why)
	}
	// The old stream ends well during the test. It must not close the
	// breaker or free the test slot.
	r.Report(old, Success)
	if _, why := r.Allow(keyA); why != ProbeBusy || r.State(keyA) != HalfOpen {
		t.Errorf("after a stale report: Allow = %v, state %v; want ProbeBusy, half-open", why, r.State(keyA))
	}
	r.Report(probe, Success)
	if r.State(keyA) != Closed {
		t.Errorf("state = %v, want closed", r.State(keyA))
	}
}

func TestCoolDown(t *testing.T) {
	c := newClock()
	r := newRouter(c, Settings{})
	r.CoolDown(keyA, 2*time.Second)
	if _, why := r.Allow(keyA); why != CoolingDown {
		t.Errorf("Allow = %v, want CoolingDown", why)
	}
	if got := r.ReadyIn([]Key{keyA, {Provider: "b"}}); got != 0 {
		t.Errorf("ReadyIn = %v, want 0: target b is ready", got)
	}
	if got := r.ReadyIn([]Key{keyA}); got != 2*time.Second {
		t.Errorf("ReadyIn = %v, want 2s", got)
	}
	// A shorter cooldown does not end a longer one.
	r.CoolDown(keyA, time.Second)
	c.add(1500 * time.Millisecond)
	if _, why := r.Allow(keyA); why != CoolingDown {
		t.Errorf("Allow = %v, want CoolingDown", why)
	}
	c.add(500 * time.Millisecond)
	attempt(t, r, keyA, Success)
	if r.State(keyA) != Closed {
		t.Errorf("a cooldown changed the breaker state to %v", r.State(keyA))
	}
}

func TestRetryBudget(t *testing.T) {
	c := newClock()
	r := newRouter(c, Settings{BudgetRatio: 0.1})
	for range 100 {
		r.request()
	}
	// The floor of 10 plus 10% of 100 requests.
	allowed := 0
	for range 30 {
		if r.tryRetry() {
			allowed++
		}
	}
	if allowed != BudgetFloor+10 {
		t.Errorf("allowed %d retries, want %d", allowed, BudgetFloor+10)
	}
	// The window moves on.
	c.add(Window)
	if !r.tryRetry() {
		t.Error("no retry allowed in a new window")
	}
}

func TestRetryBudgetZeroRatio(t *testing.T) {
	r := newRouter(newClock(), Settings{BudgetRatio: 0})
	for range 1000 {
		r.request()
	}
	allowed := 0
	for range 20 {
		if r.tryRetry() {
			allowed++
		}
	}
	if allowed != BudgetFloor {
		t.Errorf("allowed %d retries, want the floor %d", allowed, BudgetFloor)
	}
}
