package telemetry

import (
	"encoding/json/v2"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestDashboardUsesKnownMetrics checks that each query of the committed
// Grafana dashboard names only metrics that Tollgate has.
func TestDashboardUsesKnownMetrics(t *testing.T) {
	m := NewMetrics()
	m.ObserveRequest(Request{Route: "r", Provider: "p", Status: 200, Cache: "bypass", TTFT: time.Second, ITL: time.Millisecond, Overhead: time.Millisecond, Usage: true, InputTokens: 1, CostMicroUSD: 1})
	m.Retry("r", "p", "p/a", "p/b", "overloaded")
	m.RetryBudgetExhausted("p")
	m.StoreError("redis", "admit")
	m.WatchBreakers(func() []BreakerState { return []BreakerState{{Provider: "p", Model: "a"}} })
	m.WatchUsageDropped("jsonl", func() int64 { return 0 })
	families, err := m.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	known := make(map[string]bool)
	for _, f := range families {
		known[f.GetName()] = true
	}

	data, err := os.ReadFile("../../deploy/grafana/tollgate.json")
	if err != nil {
		t.Fatal(err)
	}
	var dash struct {
		Panels []struct {
			Title   string `json:"title"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(data, &dash, json.RejectUnknownMembers(false)); err != nil {
		t.Fatal(err)
	}
	name := regexp.MustCompile(`tollgate_[a-z_]+`)
	used := make(map[string]bool)
	for _, p := range dash.Panels {
		if len(p.Targets) == 0 {
			t.Errorf("panel %q has no query", p.Title)
		}
		for _, tg := range p.Targets {
			for _, n := range name.FindAllString(tg.Expr, -1) {
				base := n
				for _, suffix := range []string{"_bucket", "_count", "_sum"} {
					if b, ok := strings.CutSuffix(n, suffix); ok && known[b] {
						base = b
					}
				}
				if !known[base] {
					t.Errorf("panel %q uses the metric %s, which Tollgate does not have", p.Title, n)
				}
				used[base] = true
			}
		}
	}
	for _, want := range []string{
		"tollgate_requests_total", "tollgate_time_to_first_token_seconds", "tollgate_gateway_overhead_seconds",
		"tollgate_cost_micro_usd_total", "tollgate_fallbacks_total", "tollgate_breaker_state",
	} {
		if !used[want] {
			t.Errorf("the dashboard has no panel for %s", want)
		}
	}
}
