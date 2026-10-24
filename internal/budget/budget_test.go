package budget

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ykakade/tollgate/internal/config"
	"github.com/ykakade/tollgate/internal/limiter"
	"github.com/ykakade/tollgate/internal/pricing"
	"github.com/ykakade/tollgate/internal/provider"
)

var now = time.Date(2026, 10, 24, 15, 30, 0, 0, time.UTC)

func TestKeys(t *testing.T) {
	for _, tt := range []struct{ got, want string }{
		{RequestsKey("personal"), "tg:rl:req:{personal}"},
		{TokensKey("personal"), "tg:rl:tok:{personal}"},
		{MonthKey("personal", now), "tg:bud:{personal}:month:2026-10"},
		// The month is the month in UTC.
		{MonthKey("personal", time.Date(2026, 11, 1, 0, 30, 0, 0, time.FixedZone("x", 3600))), "tg:bud:{personal}:month:2026-10"},
		{SessionKey("personal", "task-1"), "tg:bud:sess:{personal}:task-1"},
	} {
		if tt.got != tt.want {
			t.Errorf("key = %q, want %q", tt.got, tt.want)
		}
	}
}

func TestMonthTTL(t *testing.T) {
	// To 2026-11-01 00:00 UTC, then 7 days.
	want := 7*24*time.Hour + 8*time.Hour + 30*time.Minute + 7*24*time.Hour
	if got := MonthTTL(now); got != want {
		t.Errorf("MonthTTL() = %v, want %v", got, want)
	}
	dec := time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC)
	if got := MonthTTL(dec); got != time.Hour+7*24*time.Hour {
		t.Errorf("MonthTTL() in December = %v", got)
	}
}

func TestValidSession(t *testing.T) {
	for _, s := range []string{"a", "task-1", "repo/branch:42", strings.Repeat("x", 128)} {
		if !ValidSession(s) {
			t.Errorf("ValidSession(%q) = false", s)
		}
	}
	for _, s := range []string{"", "a b", "tab\t", "é", strings.Repeat("x", 129)} {
		if ValidSession(s) {
			t.Errorf("ValidSession(%q) = true", s)
		}
	}
}

func TestRates(t *testing.T) {
	l := config.Limits{RequestsPerMinute: 10, TokensPerMinute: 1000}
	want := []limiter.Rate{
		{Key: "tg:rl:req:{p}", Limit: 10, Cost: 1},
		{Key: "tg:rl:tok:{p}", Limit: 1000, Cost: 77},
	}
	if diff := cmp.Diff(want, Rates("p", l, 77, true)); diff != "" {
		t.Errorf("Rates() differs (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(want[:1], Rates("p", l, 77, false)); diff != "" {
		t.Errorf("Rates() of a request without tokens differs (-want +got):\n%s", diff)
	}
	if got := Rates("p", config.Limits{}, 77, true); got != nil {
		t.Errorf("Rates() with no limits = %v", got)
	}
}

func TestBudgets(t *testing.T) {
	cb := []config.Budget{
		{Window: config.WindowMonth, LimitUSD: 50_000_000, Mode: config.ModeHard},
		{Window: config.WindowSession, LimitUSD: 5_000_000, Mode: config.ModeSoft},
	}
	got, used := Budgets("p", cb, "s1", now, 123)
	want := []limiter.Budget{
		{Key: "tg:bud:{p}:month:2026-10", Limit: 50_000_000, Amount: 123, Hard: true, TTL: MonthTTL(now)},
		{Key: "tg:bud:sess:{p}:s1", Limit: 5_000_000, Amount: 123, TTL: SessionTTL},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Budgets() differs (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(cb, used); diff != "" {
		t.Errorf("used budgets differ (-want +got):\n%s", diff)
	}
	got, used = Budgets("p", cb, "", now, 123)
	if len(got) != 1 || len(used) != 1 || used[0].Window != config.WindowMonth {
		t.Errorf("Budgets() without a session = %v, %v; want the month budget only", got, used)
	}
}

func TestEstimateAndCharge(t *testing.T) {
	price := &pricing.Entry{Prices: pricing.Prices{Input: 2_000_000, Output: 10_000_000}}
	for _, tt := range []struct {
		name               string
		maxTokens, reserve int64
		wantOut, wantCost  int64
	}{
		{"max_tokens below the cap", 100, 4096, 100, 2000 + 1000},
		{"max_tokens above the cap", 64000, 4096, 4096, 2000 + 40960},
		{"no max_tokens", 0, 4096, 4096, 2000 + 40960},
	} {
		e := NewEstimate(price, 1000, tt.maxTokens, tt.reserve)
		if e.InputTokens != 1000 || e.OutputTokens != tt.wantOut || e.Cost != tt.wantCost || e.Tokens() != 1000+tt.wantOut {
			t.Errorf("%s: NewEstimate() = %+v, want output %d, cost %d", tt.name, e, tt.wantOut, tt.wantCost)
		}
	}
	if e := NewEstimate(nil, 1000, 10, 4096); e.Cost != 0 || e.Tokens() != 1010 {
		t.Errorf("NewEstimate() with no price = %+v", e)
	}

	u := provider.Usage{InputTokens: 10, OutputTokens: 10}
	if got := Charge(price, u, false, 5000); got != 120 {
		t.Errorf("Charge() of reported usage = %d, want 120", got)
	}
	if got := Charge(price, u, true, 5000); got != 5000 {
		t.Errorf("Charge() of estimated usage = %d, want the reservation 5000", got)
	}
	if got := Charge(price, provider.Usage{InputTokens: 10_000}, true, 5000); got != 20000 {
		t.Errorf("Charge() of a large estimate = %d, want 20000", got)
	}
	if got := Charge(nil, u, false, 5000); got != 0 {
		t.Errorf("Charge() with no price = %d", got)
	}
}
