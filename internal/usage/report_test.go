package usage

import (
	"strings"
	"testing"
	"time"
)

var reportStart = time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)

func ms(v int64) *int64 { return &v }

func reportEvents() []Event {
	return []Event{
		{TS: reportStart, TenantID: "a", KeyID: "k1", SessionID: "s1", RouteModel: "m", Provider: "p", ProviderModel: "x", Status: 200, Attempts: 1, Cache: "miss", InputTokens: 10, CacheReadTokens: 100, CostMicroUSD: 50, TTFTMs: ms(100)},
		{TS: reportStart.Add(30 * time.Minute), TenantID: "a", KeyID: "k1", SessionID: "s1", RouteModel: "m", Provider: "q", ProviderModel: "y", Status: 200, Attempts: 2, TargetSwitched: true, CacheLossMicroUSD: 7, CacheWriteTokens: 90, CostMicroUSD: 80, TTFTMs: ms(300)},
		{TS: reportStart.Add(90 * time.Minute), TenantID: "a", KeyID: "k2", SessionID: "auto-1", SessionDerived: true, RouteModel: "m", Status: 402},
		{TS: reportStart.Add(91 * time.Minute), TenantID: "b", KeyID: "k3", RouteModel: "n", Provider: "p", ProviderModel: "x", Status: 502, Attempts: 3, TTFTMs: ms(200)},
		{TS: reportStart.Add(92 * time.Minute), TenantID: "b", KeyID: "k3", SessionID: "s2", RouteModel: "n", Status: 200, Cache: "hit", BudgetCutoff: true, OutputTokens: 5, CostMicroUSD: 1},
	}
}

func TestSummarize(t *testing.T) {
	ev := reportEvents()
	all, err := Summarize(ev, ByNone)
	if err != nil {
		t.Fatal(err)
	}
	g := all[0]
	if len(all) != 1 || g.Requests != 5 || g.Errors != 1 || g.Rejected != 1 || g.CacheHits != 1 || g.Fallbacks != 2 || g.TargetSwitches != 1 || g.BudgetCutoffs != 1 || g.CostMicroUSD != 131 || g.CacheLossMicroUSD != 7 || g.CacheReadTokens != 100 || g.CacheWriteTokens != 90 {
		t.Errorf("totals = %+v", g)
	}
	if *g.TTFTP50Ms != 200 || *g.TTFTP95Ms != 300 {
		t.Errorf("TTFT p50 %d, p95 %d; want 200, 300", *g.TTFTP50Ms, *g.TTFTP95Ms)
	}
	hours, _ := Summarize(ev, ByHour)
	if len(hours) != 2 || hours[0].Key != "2026-10-01T10:00:00Z" || hours[0].Requests != 2 || hours[1].Requests != 3 {
		t.Errorf("hours = %+v", hours)
	}
	tenants, _ := Summarize(ev, ByTenant)
	if len(tenants) != 2 || tenants[0].Key != "a" || tenants[0].CostMicroUSD != 130 {
		t.Errorf("tenants = %+v", tenants)
	}
	prov, _ := Summarize(ev, ByProvider)
	if prov[0].Key != "q/y" || prov[1].Key != "p/x" || prov[1].TTFTP50Ms == nil || *prov[1].TTFTP50Ms != 100 {
		t.Errorf("providers = %+v", prov)
	}
	if _, err := Summarize(ev, "week"); err == nil {
		t.Error("Summarize accepted an unknown group")
	}
	empty, _ := Summarize(nil, ByNone)
	if len(empty) != 0 {
		t.Errorf("no events gave %+v", empty)
	}
}

func TestSessions(t *testing.T) {
	s := Sessions(reportEvents())
	if len(s) != 3 || s[0].SessionID != "s2" || s[2].SessionID != "s1" {
		t.Fatalf("sessions = %+v", s)
	}
	s1 := s[2]
	if s1.Requests != 2 || s1.CostMicroUSD != 130 || !s1.First.Equal(reportStart) || s1.Provider != "q" || s1.Model != "y" || s1.Derived {
		t.Errorf("s1 = %+v", s1)
	}
	if !s[1].Derived {
		t.Error("auto-1 is not derived")
	}
}

func TestIndex(t *testing.T) {
	ix := NewIndex(3)
	for i, e := range reportEvents() {
		e.RequestID = string(rune('a' + i))
		ix.Emit(e)
	}
	got := ix.Events(Filter{})
	var ids []string
	for _, e := range got {
		ids = append(ids, e.RequestID)
	}
	if strings.Join(ids, "") != "cde" || ix.Len() != 3 {
		t.Errorf("events %v, want the 3 newest in order", ids)
	}
	if got := ix.Events(Filter{TenantID: "b", From: reportStart.Add(92 * time.Minute)}); len(got) != 1 || got[0].RequestID != "e" {
		t.Errorf("filtered events = %+v", got)
	}
	if got := ix.Events(Filter{KeyID: "k3", To: reportStart.Add(92 * time.Minute)}); len(got) != 1 || got[0].RequestID != "d" {
		t.Errorf("events before To = %+v", got)
	}
}
