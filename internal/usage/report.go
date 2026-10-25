package usage

import (
	"cmp"
	"fmt"
	"slices"
	"sync"
	"time"
)

// Index keeps the most recent usage events in memory, for the admin API
// and the dashboard (ADR 0032). It is an Emitter: put it next to the usage
// queue. Use NewIndex to make one. It is safe for concurrent use.
type Index struct {
	mu     sync.RWMutex
	events []Event // a ring of at most cap events
	next   int     // the position of the next event when the ring is full
}

// NewIndex returns an index that keeps at most capacity events. When it is
// full, a new event replaces the oldest event.
func NewIndex(capacity int) *Index {
	return &Index{events: make([]Event, 0, max(capacity, 1))}
}

// Emit adds e to the index. It always returns true.
func (ix *Index) Emit(e Event) bool {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if len(ix.events) < cap(ix.events) {
		ix.events = append(ix.events, e)
		return true
	}
	ix.events[ix.next] = e
	ix.next = (ix.next + 1) % len(ix.events)
	return true
}

// Len returns the number of events in the index.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.events)
}

// Events returns a copy of the events that match f, in the order that the
// index got them.
func (ix *Index) Events(f Filter) []Event {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	var out []Event
	n := len(ix.events)
	for i := range n {
		e := &ix.events[(ix.next+i)%n]
		if f.Match(e) {
			out = append(out, *e)
		}
	}
	return out
}

// Filter selects events. An empty field matches all events.
type Filter struct {
	TenantID  string
	SessionID string
	KeyID     string
	// From and To select events with From <= TS < To. A zero time has no
	// limit.
	From, To time.Time
}

// Match reports whether e matches f.
func (f Filter) Match(e *Event) bool {
	return (f.TenantID == "" || e.TenantID == f.TenantID) &&
		(f.SessionID == "" || e.SessionID == f.SessionID) &&
		(f.KeyID == "" || e.KeyID == f.KeyID) &&
		(f.From.IsZero() || !e.TS.Before(f.From)) &&
		(f.To.IsZero() || e.TS.Before(f.To))
}

// Totals are the sums of a set of events.
type Totals struct {
	Requests int64 `json:"requests"`
	// Errors are requests with a status of 500 or more.
	Errors int64 `json:"errors"`
	// Rejected are requests with a status from 400 to 499, for example a
	// rate limit or a budget.
	Rejected          int64 `json:"rejected"`
	CacheHits         int64 `json:"cache_hits"`
	Fallbacks         int64 `json:"fallbacks"`
	TargetSwitches    int64 `json:"target_switches"`
	BudgetCutoffs     int64 `json:"budget_cutoffs"`
	InputTokens       int64 `json:"input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	CacheReadTokens   int64 `json:"cache_read_tokens"`
	CacheWriteTokens  int64 `json:"cache_write_tokens"`
	CostMicroUSD      int64 `json:"cost_micro_usd"`
	CacheLossMicroUSD int64 `json:"cache_loss_micro_usd"`
	// TTFTP50Ms and TTFTP95Ms are percentiles of the time to first token,
	// or nil if no event has one.
	TTFTP50Ms *int64 `json:"ttft_p50_ms"`
	TTFTP95Ms *int64 `json:"ttft_p95_ms"`

	ttft []int64
}

func (t *Totals) add(e *Event) {
	t.Requests++
	switch {
	case e.Status >= 500:
		t.Errors++
	case e.Status >= 400:
		t.Rejected++
	}
	if e.Cache == "hit" {
		t.CacheHits++
	}
	if e.Attempts > 1 {
		t.Fallbacks++
	}
	if e.TargetSwitched {
		t.TargetSwitches++
	}
	if e.BudgetCutoff {
		t.BudgetCutoffs++
	}
	t.InputTokens += e.InputTokens
	t.OutputTokens += e.OutputTokens
	t.CacheReadTokens += e.CacheReadTokens
	t.CacheWriteTokens += e.CacheWriteTokens
	t.CostMicroUSD += e.CostMicroUSD
	t.CacheLossMicroUSD += e.CacheLossMicroUSD
	if e.TTFTMs != nil {
		t.ttft = append(t.ttft, *e.TTFTMs)
	}
}

// finish sets the percentiles.
func (t *Totals) finish() {
	if len(t.ttft) == 0 {
		return
	}
	slices.Sort(t.ttft)
	t.TTFTP50Ms, t.TTFTP95Ms = percentile(t.ttft, 50), percentile(t.ttft, 95)
	t.ttft = nil
}

// percentile returns the nearest-rank percentile p of sorted values.
func percentile(sorted []int64, p int) *int64 {
	rank := (p*len(sorted) + 99) / 100
	v := sorted[max(rank-1, 0)]
	return &v
}

// Group is the totals of the events with one key.
type Group struct {
	Key string `json:"key"`
	Totals
}

// The values of the groupBy argument of Summarize.
const (
	ByNone     = "none"
	ByTenant   = "tenant"
	ByKey      = "key"
	BySession  = "session"
	ByRoute    = "route"
	ByProvider = "provider"
	ByHour     = "hour"
	ByDay      = "day"
)

// GroupByValues lists the values of the groupBy argument of Summarize.
var GroupByValues = []string{ByNone, ByTenant, ByKey, BySession, ByRoute, ByProvider, ByHour, ByDay}

// groupKey returns the key of e for groupBy.
func groupKey(e *Event, groupBy string) string {
	switch groupBy {
	case ByTenant:
		return e.TenantID
	case ByKey:
		return e.TenantID + "/" + e.KeyID
	case BySession:
		return e.TenantID + "/" + e.SessionID
	case ByRoute:
		return e.RouteModel
	case ByProvider:
		if e.Provider == "" {
			return ""
		}
		return e.Provider + "/" + e.ProviderModel
	case ByHour:
		return e.TS.UTC().Truncate(time.Hour).Format(time.RFC3339)
	case ByDay:
		return e.TS.UTC().Format(time.DateOnly)
	}
	return ""
}

// Summarize returns the totals of events for each key of groupBy, one of
// GroupByValues. Groups by time are in time order. Other groups are in
// order of cost, the largest first.
func Summarize(events []Event, groupBy string) ([]Group, error) {
	if !slices.Contains(GroupByValues, groupBy) {
		return nil, fmt.Errorf("unknown group %q", groupBy)
	}
	byKey := make(map[string]*Group)
	var out []*Group
	for i := range events {
		k := groupKey(&events[i], groupBy)
		g := byKey[k]
		if g == nil {
			g = &Group{Key: k}
			byKey[k] = g
			out = append(out, g)
		}
		g.add(&events[i])
	}
	result := make([]Group, len(out))
	for i, g := range out {
		g.finish()
		result[i] = *g
	}
	if groupBy == ByHour || groupBy == ByDay {
		slices.SortFunc(result, func(a, b Group) int { return cmp.Compare(a.Key, b.Key) })
	} else {
		slices.SortStableFunc(result, func(a, b Group) int {
			return cmp.Or(cmp.Compare(b.CostMicroUSD, a.CostMicroUSD), cmp.Compare(b.Requests, a.Requests), cmp.Compare(a.Key, b.Key))
		})
	}
	return result, nil
}

// Session is the summary of one session.
type Session struct {
	TenantID  string    `json:"tenant_id"`
	SessionID string    `json:"session_id"`
	Derived   bool      `json:"derived"`
	First     time.Time `json:"first"`
	Last      time.Time `json:"last"`
	// Provider and Model are the target of the last request.
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Totals
}

// Sessions returns the sessions of the events, the most recent first.
// Events without a session are not in a session.
func Sessions(events []Event) []Session {
	type key struct{ tenant, session string }
	byKey := make(map[key]*Session)
	for i := range events {
		e := &events[i]
		if e.SessionID == "" {
			continue
		}
		k := key{e.TenantID, e.SessionID}
		s := byKey[k]
		if s == nil {
			s = &Session{TenantID: e.TenantID, SessionID: e.SessionID, Derived: e.SessionDerived, First: e.TS}
			byKey[k] = s
		}
		if e.TS.Before(s.First) {
			s.First = e.TS
		}
		if !e.TS.Before(s.Last) {
			s.Last = e.TS
			if e.Provider != "" {
				s.Provider, s.Model = e.Provider, e.ProviderModel
			}
		}
		s.add(e)
	}
	out := make([]Session, 0, len(byKey))
	for _, s := range byKey {
		s.finish()
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b Session) int {
		return cmp.Or(b.Last.Compare(a.Last), cmp.Compare(a.TenantID, b.TenantID), cmp.Compare(a.SessionID, b.SessionID))
	})
	return out
}
