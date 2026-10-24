package pricing

import (
	"strings"
	"testing"
	"time"

	"github.com/ykakade/tollgate"
	"github.com/ykakade/tollgate/internal/provider"
)

func mustParse(t *testing.T, data string) *Table {
	t.Helper()
	tab, errs := Parse([]byte(data))
	if errs != nil {
		t.Fatalf("Parse() errors: %v", errs)
	}
	return tab
}

func date(s string) time.Time {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestEmbeddedTableIsValid(t *testing.T) {
	tab := mustParse(t, string(tollgate.PriceTable()))
	for _, m := range []string{"claude-fable-5-1", "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"} {
		if !tab.Has("anthropic", "anthropic", m) {
			t.Errorf("the embedded table has no entry for %s", m)
		}
	}
}

const table = `
- provider: anthropic
  model: m
  effective_from: 2026-01-01
  micro_usd_per_mtok: {input: 3000000, output: 15000000, cache_read: 300000, cache_write: 3750000, cache_write_1h: 6000000}
- provider: anthropic
  model: m
  effective_from: 2026-06-01
  micro_usd_per_mtok: {input: 2000000, output: 10000000}
- provider: local
  model: m
  effective_from: 2026-01-01
  micro_usd_per_mtok: {input: 0, output: 0}
- provider: anthropic
  model: tiered
  effective_from: 2026-01-01
  micro_usd_per_mtok: {input: 100000, output: 500000, cache_read: 10000, cache_write: 125000}
  long_context:
    above_prompt_tokens: 100000
    micro_usd_per_mtok: {input: 500000, output: 2500000, cache_read: 50000, cache_write: 625000}
`

func TestFind(t *testing.T) {
	tab := mustParse(t, table)
	for _, tt := range []struct {
		name, kind string
		at         string
		wantInput  int64
		wantNil    bool
	}{
		{"anthropic", "anthropic", "2026-03-01", 3000000, false},
		{"anthropic", "anthropic", "2026-06-01", 2000000, false},
		{"anthropic", "anthropic", "2025-12-31", 0, true},
		// A provider name without entries uses the entries of its kind.
		{"vertex-east", "anthropic", "2026-07-01", 2000000, false},
		// An entry for the name has priority over the kind.
		{"local", "anthropic", "2026-07-01", 0, false},
	} {
		e := tab.Find(tt.name, tt.kind, "m", date(tt.at))
		switch {
		case tt.wantNil && e != nil:
			t.Errorf("Find(%s, %s, %s) = %v, want nil", tt.name, tt.kind, tt.at, e)
		case !tt.wantNil && (e == nil || e.Prices.Input != tt.wantInput):
			t.Errorf("Find(%s, %s, %s) = %+v, want input price %d", tt.name, tt.kind, tt.at, e, tt.wantInput)
		}
	}
	if tab.Find("anthropic", "anthropic", "other", date("2026-07-01")) != nil {
		t.Error("Find() of an unknown model is not nil")
	}
	var nilTable *Table
	if nilTable.Find("a", "a", "m", time.Now()) != nil {
		t.Error("Find() on a nil table is not nil")
	}
}

func TestCost(t *testing.T) {
	tab := mustParse(t, table)
	m := tab.Find("anthropic", "anthropic", "m", date("2026-03-01"))
	tiered := tab.Find("anthropic", "anthropic", "tiered", date("2026-03-01"))
	for _, tt := range []struct {
		name string
		e    *Entry
		u    provider.Usage
		want int64
	}{
		{"zero", m, provider.Usage{}, 0},
		// 1 input token at 3 USD per million tokens is 3 micro-USD.
		{"one input token", m, provider.Usage{InputTokens: 1}, 3},
		// 1 cache read token is 0.3 micro-USD, rounded up to 1.
		{"round up", m, provider.Usage{CacheReadTokens: 1}, 1},
		{"all kinds", m, provider.Usage{InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 10000, CacheWriteTokens: 2000}, 3000 + 7500 + 3000 + 7500},
		// 1,000 of the 2,000 cache write tokens use the 1-hour price.
		{"1-hour cache writes", m, provider.Usage{CacheWriteTokens: 2000, CacheWrite1hTokens: 1000}, 3750 + 6000},
		{"negative counts are 0", m, provider.Usage{InputTokens: -5, OutputTokens: 1}, 15},
		{"short prompt", tiered, provider.Usage{InputTokens: 100000, OutputTokens: 1000}, 10000 + 500},
		// The prompt counts cache tokens too: 100,001 tokens is over the threshold.
		{"long prompt", tiered, provider.Usage{InputTokens: 1, CacheReadTokens: 100000, OutputTokens: 1000}, 1 + 5000 + 2500},
		// The cost of the largest counts at the largest price fits in an int64.
		{"largest", &Entry{Prices: Prices{MaxPrice, MaxPrice, MaxPrice, MaxPrice, MaxPrice}}, provider.Usage{InputTokens: 1 << 62, OutputTokens: 1 << 62, CacheReadTokens: 1 << 62, CacheWriteTokens: 1 << 62}, 4 * maxTokens * MaxPrice / 1_000_000},
	} {
		if got := tt.e.Cost(tt.u); got != tt.want {
			t.Errorf("%s: Cost(%+v) = %d, want %d", tt.name, tt.u, got, tt.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, tt := range []struct {
		name, data, want string
	}{
		{"not a list", "a: 1", "line 1: The price table must be a list"},
		{"not yaml", "- [", "not valid YAML"},
		{"unknown field", "- provider: a\n  model: m\n  effective_from: 2026-01-01\n  price: 1\n", `line 4: The field "price" is not known`},
		{"wrong type", "- provider: a\n  model: m\n  effective_from: 2026-01-01\n  micro_usd_per_mtok: {input: x}\n", "line 4: The value has the wrong type"},
		{"no model", "- provider: a\n  effective_from: 2026-01-01\n", "line 1: An entry of the price table has no provider or no model"},
		{"bad date", "- provider: a\n  model: m\n  effective_from: 2026-13-01\n", "line 1: The effective_from date of a/m is not valid"},
		{"negative price", "- provider: a\n  model: m\n  effective_from: 2026-01-01\n  micro_usd_per_mtok: {input: -1}\n", "The price input of a/m is not valid"},
		{"too large price", "- provider: a\n  model: m\n  effective_from: 2026-01-01\n  micro_usd_per_mtok: {output: 1000000001}\n", "The price output of a/m is not valid"},
		{"no threshold", "- provider: a\n  model: m\n  effective_from: 2026-01-01\n  long_context: {micro_usd_per_mtok: {input: 1}}\n", "has no above_prompt_tokens"},
		{"bad long price", "- provider: a\n  model: m\n  effective_from: 2026-01-01\n  long_context: {above_prompt_tokens: 5, micro_usd_per_mtok: {cache_read: -2}}\n", "The long_context price cache_read of a/m"},
		{"duplicate", "- provider: a\n  model: m\n  effective_from: 2026-01-01\n- provider: a\n  model: m\n  effective_from: 2026-01-01\n", "line 4: The price table has two entries for a/m from 2026-01-01"},
		{"empty entry", "- \n", "line 1: An entry of the price table is empty"},
	} {
		tab, errs := Parse([]byte(tt.data))
		if tab != nil || len(errs) == 0 {
			t.Errorf("%s: Parse() = %v, %v, want errors", tt.name, tab, errs)
			continue
		}
		var all []string
		for _, e := range errs {
			all = append(all, e.Error())
		}
		if got := strings.Join(all, "\n"); !strings.Contains(got, tt.want) {
			t.Errorf("%s: errors\n%s\nwant %q", tt.name, got, tt.want)
		}
	}
}
