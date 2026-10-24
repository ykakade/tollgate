package limiter

import (
	"context"
	"errors"
	"testing"
	"time"
)

// broken is a Store that always fails.
type broken struct{}

var errBroken = errors.New("broken")

func (broken) Admit(context.Context, *Request) (Decision, error) { return Decision{}, errBroken }
func (broken) Settle(context.Context, *Settlement) error         { return errBroken }
func (broken) Ping(context.Context) error                        { return errBroken }

func TestFailoverUsesMain(t *testing.T) {
	main := NewMemory()
	f := &Failover{Main: main, Local: NewMemory()}
	storeSuite(t, func(*testing.T) Store { return &Failover{Main: NewMemory(), Local: NewMemory()} })
	d := mustAdmit(t, f, &Request{Now: at(1), Budgets: []Budget{{Key: "b", Limit: 10, Amount: 5, Hard: true, TTL: time.Hour}}})
	if !d.Allowed || d.Local || main.Value("b", at(1)) != 5 {
		t.Errorf("decision %+v, main value %d; want the main store", d, main.Value("b", at(1)))
	}
}

func TestFailoverRatesFailOpen(t *testing.T) {
	var ops []string
	local := NewMemory()
	f := &Failover{Main: broken{}, Local: local, OnError: func(op string, err error) {
		if !errors.Is(err, errBroken) {
			t.Errorf("OnError(%q, %v), want the error of the main store", op, err)
		}
		ops = append(ops, op)
	}}
	req := &Request{Now: at(1),
		Rates:   []Rate{{Key: "r", Limit: 2, Cost: 1}},
		Budgets: []Budget{{Key: "soft", Limit: 1, Amount: 5, TTL: time.Hour}},
	}
	allowed := 0
	for range 4 {
		d := mustAdmit(t, f, req)
		if !d.Local || len(d.Spend) != 1 {
			t.Fatalf("decision = %+v, want a local decision", d)
		}
		if d.Allowed {
			allowed++
		}
	}
	if allowed != 2 {
		t.Errorf("the local limit allowed %d requests, want 2", allowed)
	}
	if local.Value("soft", at(1)) != 0 {
		t.Error("the local store has a soft budget reservation")
	}
	mustSettle(t, f, &Settlement{Now: at(2), Minute: 0, Local: true, Rates: []Adjust{{Key: "r", Delta: 1}}, Budgets: []Adjust{{Key: "soft", Delta: 5}}})
	if local.Value("soft", at(2)) != 0 {
		t.Error("a local settle changed a budget")
	}
	if err := f.Settle(t.Context(), &Settlement{Now: at(2)}); !errors.Is(err, errBroken) {
		t.Errorf("Settle() = %v, want the error of the main store", err)
	}
	if err := f.Ping(t.Context()); !errors.Is(err, errBroken) {
		t.Errorf("Ping() = %v, want the error of the main store", err)
	}
	// In the backoff time, Admit does not call the main store.
	if len(ops) != 3 || ops[0] != "admit" || ops[1] != "settle" || ops[2] != "ping" {
		t.Errorf("OnError operations = %v", ops)
	}
	mustAdmit(t, f, &Request{Now: at(1).Add(DefaultBackoff)})
	if len(ops) != 4 || ops[3] != "admit" {
		t.Errorf("after the backoff time, OnError operations = %v, want a new admit", ops)
	}
}

func TestFailoverHardBudgetFailsClosed(t *testing.T) {
	f := &Failover{Main: broken{}, Local: NewMemory(), Backoff: time.Second}
	req := &Request{Now: at(1), Budgets: []Budget{{Key: "b", Limit: 10, Amount: 1, Hard: true}}}
	_, err := f.Admit(t.Context(), req)
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, errBroken) {
		t.Errorf("Admit() error = %v, want ErrUnavailable that wraps the cause", err)
	}
	if _, err := f.Admit(t.Context(), req); !errors.Is(err, ErrUnavailable) || !errors.Is(err, errBackoff) {
		t.Errorf("Admit() in the backoff time: error = %v, want ErrUnavailable", err)
	}
}
