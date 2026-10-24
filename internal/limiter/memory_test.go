package limiter

import (
	"testing"
	"time"
)

func TestMemory(t *testing.T) {
	storeSuite(t, func(*testing.T) Store { return NewMemory() })
}

func TestMemoryExpires(t *testing.T) {
	m := NewMemory()
	b := Budget{Key: "b", Limit: 100, Amount: 60, Hard: true, TTL: time.Hour}
	if d := mustAdmit(t, m, &Request{Now: at(0), Budgets: []Budget{b}}); !d.Allowed {
		t.Fatal("first request denied")
	}
	if d := mustAdmit(t, m, &Request{Now: at(60), Budgets: []Budget{b}}); d.Allowed {
		t.Fatal("second request allowed in the TTL")
	}
	if got := m.Value("b", at(3599)); got != 60 {
		t.Errorf("Value before the end of the TTL = %d, want 60", got)
	}
	if d := mustAdmit(t, m, &Request{Now: at(3600), Budgets: []Budget{b}}); !d.Allowed {
		t.Error("request after the TTL denied")
	}
	// The sweep removes expired counters.
	mustAdmit(t, m, &Request{Now: at(3600 + 7200)})
	if n := len(m.items); n != 0 {
		t.Errorf("%d counters after the sweep, want 0", n)
	}
}
