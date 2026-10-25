package router

import (
	"testing"
	"time"
)

var (
	keyB = Key{Provider: "b", Model: "m"}
	keyC = Key{Provider: "c", Model: "m", Region: "us"}
)

// walk runs a plan and returns the indexes that it tried, with the action
// of each attempt from actions, and the stop reason.
func walk(t *testing.T, p *Plan, actions ...Action) ([]int, Stop) {
	t.Helper()
	i, ok := p.First()
	if !ok {
		return nil, NoTarget
	}
	tried := []int{i}
	for _, a := range actions {
		_, o := Classify(Result{Status: map[Action]int{Done: 200, RetrySame: 500, NextTarget: 529}[a]})
		p.Report(o)
		next, stop, ok := p.Next(a)
		if !ok {
			return tried, stop
		}
		tried = append(tried, next)
	}
	t.Fatal("the plan did not stop")
	return nil, NoTarget
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPlan(t *testing.T) {
	keys := []Key{keyA, keyB, keyC}
	for _, tt := range []struct {
		name    string
		max     int
		actions []Action
		tried   []int
		stop    Stop
	}{
		{"success", 3, []Action{Done}, []int{0}, NoTarget},
		{"500 retries the same target once", 3, []Action{RetrySame, RetrySame, Done}, []int{0, 0, 1}, NoTarget},
		{"529 goes to the next target", 3, []Action{NextTarget, NextTarget, NextTarget}, []int{0, 1, 2}, MaxAttempts},
		{"max attempts", 2, []Action{NextTarget, NextTarget}, []int{0, 1}, MaxAttempts},
		{"retry once on each target", 5, []Action{RetrySame, RetrySame, RetrySame, RetrySame, RetrySame, RetrySame}, []int{0, 0, 1, 1, 2}, MaxAttempts},
		{"no target left", 10, []Action{NextTarget, NextTarget, NextTarget}, []int{0, 1, 2}, NoTarget},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newRouter(newClock(), Settings{MinRequests: 100, BudgetRatio: 1})
			tried, stop := walk(t, r.Plan(keys, tt.max), tt.actions...)
			if !equal(tried, tt.tried) || stop != tt.stop {
				t.Errorf("tried %v, stop %v; want %v, %v", tried, stop, tt.tried, tt.stop)
			}
		})
	}
}

func TestPlanSkipsBlockedTargets(t *testing.T) {
	c := newClock()
	r := newRouter(c, Settings{MinRequests: 1, BudgetRatio: 1})
	attempt(t, r, keyA, Failure) // Opens the breaker of A.
	r.CoolDown(keyB, time.Minute)
	p := r.Plan([]Key{keyA, keyB, keyC}, 3)
	i, ok := p.First()
	if !ok || i != 2 {
		t.Fatalf("First = %d, %v; want 2", i, ok)
	}
	want := []Skip{{0, BreakerOpen}, {1, CoolingDown}}
	if got := p.Skipped(); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Skipped = %v, want %v", got, want)
	}
	p.Report(Success)
	if p.Attempts() != 1 {
		t.Errorf("Attempts = %d, want 1", p.Attempts())
	}
}

func TestPlanAllBlocked(t *testing.T) {
	r := newRouter(newClock(), Settings{MinRequests: 1})
	attempt(t, r, keyA, Failure)
	attempt(t, r, keyB, Failure)
	p := r.Plan([]Key{keyA, keyB}, 3)
	if _, ok := p.First(); ok {
		t.Fatal("First found a target, want none")
	}
	if p.Attempts() != 0 || len(p.Skipped()) != 2 {
		t.Errorf("attempts %d, skipped %v", p.Attempts(), p.Skipped())
	}
}

func TestPlanRetrySameAfterBreakerOpens(t *testing.T) {
	r := newRouter(newClock(), Settings{MinRequests: 1, BudgetRatio: 1})
	p := r.Plan([]Key{keyA, keyB}, 3)
	if i, _ := p.First(); i != 0 {
		t.Fatal(i)
	}
	// The failure opens the breaker of A, so the retry goes to B.
	p.Report(Failure)
	i, _, ok := p.Next(RetrySame)
	if !ok || i != 1 {
		t.Errorf("Next = %d, %v; want 1", i, ok)
	}
	if len(p.Skipped()) != 0 {
		t.Errorf("Skipped = %v, want none: A was tried", p.Skipped())
	}
}

func TestPlanBudgetExhaustedFreesTheProbe(t *testing.T) {
	c := newClock()
	r := newRouter(c, Settings{MinRequests: 1, BudgetRatio: 0})
	attempt(t, r, keyB, Failure) // Opens B.
	c.add(30 * time.Second)      // B is ready for a test attempt.
	for range BudgetFloor {
		if !r.tryRetry() {
			t.Fatal("budget is too small")
		}
	}
	p := r.Plan([]Key{keyA, keyB}, 3)
	p.First()
	p.Report(Failure)
	if _, stop, ok := p.Next(NextTarget); ok || stop != BudgetExhausted {
		t.Fatalf("Next = %v, %v; want BudgetExhausted", ok, stop)
	}
	// The plan gave back the test attempt of B.
	if _, why := r.Allow(keyB); why != NotBlocked {
		t.Errorf("Allow(B) = %v, want the test attempt", why)
	}
}

func TestPlanFrom(t *testing.T) {
	keys := []Key{keyA, keyB, keyC}
	for _, tt := range []struct {
		first int
		tried []int
	}{
		{0, []int{0, 1, 2}},
		{1, []int{1, 0, 2}},
		{2, []int{2, 0, 1}},
		{-1, []int{0, 1, 2}},
		{3, []int{0, 1, 2}},
	} {
		r := New(Settings{FailureRatio: 0.5, MinRequests: 100, OpenFor: time.Minute, BudgetRatio: 1})
		got, stop := walk(t, r.PlanFrom(keys, 3, tt.first), NextTarget, NextTarget, NextTarget)
		if !equal(got, tt.tried) || stop != MaxAttempts {
			t.Errorf("PlanFrom(first %d) tried %v (stop %v), want %v", tt.first, got, stop, tt.tried)
		}
	}
	// A blocked sticky target is skipped, and the next targets keep their
	// order.
	r := New(Settings{FailureRatio: 0.5, MinRequests: 100, OpenFor: time.Minute, BudgetRatio: 1})
	r.CoolDown(keyC, time.Minute)
	p := r.PlanFrom(keys, 3, 2)
	got, _ := walk(t, p, Done)
	if !equal(got, []int{0}) || len(p.Skipped()) != 1 || p.Skipped()[0].Index != 2 {
		t.Errorf("tried %v, skipped %+v, want [0] and a skip of 2", got, p.Skipped())
	}
}
