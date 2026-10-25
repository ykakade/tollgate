package limiter

import (
	"crypto/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// base is the start of a minute.
var base = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// at returns the time sec seconds after base.
func at(sec float64) time.Time {
	return base.Add(time.Duration(sec * float64(time.Second)))
}

// prefix returns a key prefix that no other test uses, with the tenant in a
// hash tag, as the server makes it.
func prefix() string {
	return "tg:test:{" + rand.Text() + "}"
}

// storeSuite runs the tests that every Store must pass.
func storeSuite(t *testing.T, newStore func(t *testing.T) Store) {
	t.Run("request limit", func(t *testing.T) {
		s := newStore(t)
		k := prefix()
		allowed := 0
		var last Decision
		for range 8 {
			d := mustAdmit(t, s, &Request{Now: at(10), Rates: []Rate{{Key: k, Limit: 5, Cost: 1}}})
			if d.Allowed {
				allowed++
				if d.Rates[0].Remaining != int64(5-allowed) {
					t.Errorf("after %d requests, Remaining = %d, want %d", allowed, d.Rates[0].Remaining, 5-allowed)
				}
			}
			last = d
		}
		if allowed != 5 {
			t.Errorf("allowed %d requests, want 5", allowed)
		}
		if last.Allowed || last.DeniedRate != 0 || last.DeniedBudget != -1 || last.RetryAfter < time.Second {
			t.Errorf("last decision = %+v, want denied by rate 0 with a retry-after", last)
		}
		if last.Rates[0].Reset != 50*time.Second {
			t.Errorf("Reset = %v, want the time to the end of the minute (50s)", last.Rates[0].Reset)
		}
	})

	t.Run("sliding window", func(t *testing.T) {
		s := newStore(t)
		k := prefix()
		r := func(sec float64) Decision {
			return mustAdmit(t, s, &Request{Now: at(sec), Rates: []Rate{{Key: k, Limit: 5, Cost: 1}}})
		}
		for range 5 {
			if !r(59).Allowed {
				t.Fatal("a request in the first minute was denied")
			}
		}
		// At the start of the next minute, the previous minute has its
		// full weight.
		if r(60).Allowed {
			t.Error("a request at the start of the next minute was allowed")
		}
		// Half-way, the estimate is 5 × 0.5 + this minute: 2 more fit.
		got := 0
		for range 5 {
			if r(90).Allowed {
				got++
			}
		}
		if got != 2 {
			t.Errorf("half-way through the next minute, %d requests were allowed, want 2", got)
		}
	})

	t.Run("token cost", func(t *testing.T) {
		s := newStore(t)
		k := prefix()
		for i, tt := range []struct {
			cost int64
			want bool
		}{{60, true}, {60, false}, {40, true}, {1, false}} {
			d := mustAdmit(t, s, &Request{Now: at(1), Rates: []Rate{{Key: k, Limit: 100, Cost: tt.cost}}})
			if d.Allowed != tt.want {
				t.Errorf("request %d with cost %d: Allowed = %v, want %v", i, tt.cost, d.Allowed, tt.want)
			}
		}
	})

	t.Run("hard and soft budgets", func(t *testing.T) {
		s := newStore(t)
		hard, soft := prefix()+":hard", prefix()+":soft"
		req := func() *Request {
			return &Request{Now: at(1), Budgets: []Budget{
				{Key: hard, Limit: 100, Amount: 40, Hard: true, TTL: time.Hour},
				{Key: soft, Limit: 50, Amount: 40, TTL: time.Hour},
			}}
		}
		d1, d2, d3 := mustAdmit(t, s, req()), mustAdmit(t, s, req()), mustAdmit(t, s, req())
		if !d1.Allowed || !d2.Allowed || d3.Allowed || d3.DeniedBudget != 0 || d3.DeniedRate != -1 || d3.RetryAfter != 0 {
			t.Fatalf("decisions = %+v, %+v, %+v; want the third denied by the hard budget", d1, d2, d3)
		}
		// The soft budget is over its limit but does not stop requests.
		if d2.Spend[0] != 80 || d2.Spend[1] != 80 {
			t.Errorf("spend after two requests = %v, want [80 80]", d2.Spend)
		}
		if d3.Spend[0] != 80 || d3.Spend[1] != 80 {
			t.Errorf("spend after the denied request = %v, want [80 80]", d3.Spend)
		}
	})

	t.Run("denied request changes nothing", func(t *testing.T) {
		s := newStore(t)
		k, b := prefix(), prefix()+":b"
		req := &Request{Now: at(1),
			Rates:   []Rate{{Key: k, Limit: 10, Cost: 1}},
			Budgets: []Budget{{Key: b, Limit: 10, Amount: 11, Hard: true, TTL: time.Hour}},
		}
		for range 3 {
			if d := mustAdmit(t, s, req); d.Allowed || d.DeniedBudget != 0 {
				t.Fatalf("decision = %+v, want denied by the budget", d)
			}
		}
		d := mustAdmit(t, s, &Request{Now: at(1), Rates: []Rate{{Key: k, Limit: 10, Cost: 1}}})
		if d.Rates[0].Remaining != 9 {
			t.Errorf("Remaining = %d, want 9: the denied requests changed the counter", d.Rates[0].Remaining)
		}
	})

	t.Run("settle", func(t *testing.T) {
		s := newStore(t)
		k, b := prefix(), prefix()+":b"
		req := func(sec float64) *Request {
			return &Request{Now: at(sec),
				Rates:   []Rate{{Key: k, Limit: 1000, Cost: 100}},
				Budgets: []Budget{{Key: b, Limit: 1000, Amount: 100, Hard: true, TTL: time.Hour}},
			}
		}
		d := mustAdmit(t, s, req(1))
		mustSettle(t, s, &Settlement{Now: at(2), Minute: d.Minute,
			Rates:   []Adjust{{Key: k, Delta: 400}},
			Budgets: []Adjust{{Key: b, Delta: -70, TTL: time.Hour}},
		})
		d = mustAdmit(t, s, req(3))
		if d.Spend[0] != 30+100 || d.Rates[0].Remaining != 1000-500-100 {
			t.Errorf("after settle: spend %d, remaining %d; want 130 and 400", d.Spend[0], d.Rates[0].Remaining)
		}
		// A counter does not go below 0.
		mustSettle(t, s, &Settlement{Now: at(4), Minute: d.Minute, Budgets: []Adjust{{Key: b, Delta: -10_000, TTL: time.Hour}}})
		d = mustAdmit(t, s, req(5))
		if d.Spend[0] != 100 {
			t.Errorf("spend after a large negative change = %d, want 100", d.Spend[0])
		}
		// A change to a minute before the previous minute is ignored.
		mustSettle(t, s, &Settlement{Now: at(200), Minute: d.Minute, Rates: []Adjust{{Key: k, Delta: 10_000}}})
		d = mustAdmit(t, s, req(240))
		if !d.Allowed {
			t.Errorf("a settle of an old minute changed the counters: %+v", d)
		}
	})

	t.Run("concurrent", func(t *testing.T) {
		s := newStore(t)
		k, b := prefix(), prefix()+":b"
		var allowed atomic.Int64
		var wg sync.WaitGroup
		for range 50 {
			wg.Go(func() {
				d, err := s.Admit(t.Context(), &Request{Now: at(1),
					Rates:   []Rate{{Key: k, Limit: 30, Cost: 1}},
					Budgets: []Budget{{Key: b, Limit: 200, Amount: 10, Hard: true, TTL: time.Hour}},
				})
				if err != nil {
					t.Error(err)
				}
				if d.Allowed {
					allowed.Add(1)
				}
			})
		}
		wg.Wait()
		if got := allowed.Load(); got != 20 {
			t.Errorf("allowed %d of 50 parallel requests, want 20 (the budget allows 20)", got)
		}
	})

	t.Run("sticky entry", func(t *testing.T) {
		s := newStore(t)
		k := prefix()
		sticky := k + ":sticky"
		if d := mustAdmit(t, s, &Request{Now: at(1), Sticky: sticky}); !d.Allowed || d.Sticky != 0 {
			t.Fatalf("first decision = %+v, want allowed with no sticky value", d)
		}
		mustSettle(t, s, &Settlement{Now: at(1), Sticky: &Entry{Key: sticky, Value: 1<<40 + 3, TTL: time.Minute}})
		d := mustAdmit(t, s, &Request{Now: at(2), Rates: []Rate{{Key: k, Limit: 5, Cost: 1}}, Budgets: []Budget{{Key: k + ":b", Limit: 10, Amount: 1, Hard: true, TTL: time.Hour}}, Sticky: sticky})
		if !d.Allowed || d.Sticky != 1<<40+3 || len(d.Spend) != 1 {
			t.Errorf("decision = %+v, want the sticky value %d", d, int64(1<<40+3))
		}
		// A denied request also reads the entry.
		d = mustAdmit(t, s, &Request{Now: at(3), Budgets: []Budget{{Key: k + ":b", Limit: 1, Amount: 5, Hard: true, TTL: time.Hour}}, Sticky: sticky})
		if d.Allowed || d.Sticky != 1<<40+3 {
			t.Errorf("denied decision = %+v, want the sticky value", d)
		}
		mustSettle(t, s, &Settlement{Now: at(3), Budgets: []Adjust{{Key: k + ":b", Delta: 2, TTL: time.Hour}}, Sticky: &Entry{Key: sticky, Value: 7, TTL: time.Minute}})
		if d := mustAdmit(t, s, &Request{Now: at(4), Sticky: sticky}); d.Sticky != 7 {
			t.Errorf("Sticky = %d after a new value, want 7", d.Sticky)
		}
	})

	t.Run("ping", func(t *testing.T) {
		if err := newStore(t).Ping(t.Context()); err != nil {
			t.Error(err)
		}
	})
}

func mustAdmit(t *testing.T, s Store, r *Request) Decision {
	t.Helper()
	d, err := s.Admit(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mustSettle(t *testing.T, s Store, st *Settlement) {
	t.Helper()
	if err := s.Settle(t.Context(), st); err != nil {
		t.Fatal(err)
	}
}

func TestRetryAfter(t *testing.T) {
	for _, tt := range []struct {
		name                         string
		prev, cur, cost, limit, elap int64
		want                         time.Duration
	}{
		// prev 10 at e=0: 10*(60-e)/60 + 1 <= 10 at e >= 6 s.
		{"later in this minute", 10, 0, 1, 10, 0, 6 * time.Second},
		{"rounds up", 10, 0, 1, 10, 100, 6 * time.Second},
		// This minute is full: wait for the next minute, then for the
		// weight of this minute to fall: 5*(60-e)/60 + 1 <= 5 at e >= 12 s.
		{"next minute", 0, 5, 1, 5, 30_000, 30*time.Second + 12*time.Second},
		// A request with cost = limit fits only when this minute has no
		// weight: at the end of the next minute.
		{"end of the next minute", 0, 5, 5, 5, 30_000, 90 * time.Second},
		{"cost larger than limit", 0, 0, 101, 100, 0, Window},
		{"at least one second", 10, 0, 1, 10, 5_999, time.Second},
	} {
		if got := retryAfter(tt.prev, tt.cur, tt.cost, tt.limit, tt.elap); got != tt.want {
			t.Errorf("%s: retryAfter = %v, want %v", tt.name, got, tt.want)
		}
	}
}
