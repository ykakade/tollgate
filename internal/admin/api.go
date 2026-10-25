package admin

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/ykakade/tollgate/internal/config"
	"github.com/ykakade/tollgate/internal/telemetry"
	"github.com/ykakade/tollgate/internal/usage"
)

// Limits of the admin API.
const (
	// defaultWindow is the time range of a query without "from".
	defaultWindow = 24 * time.Hour
	// maxSessionEvents is the largest number of events in the timeline of
	// a session.
	maxSessionEvents = 500
	defaultSessions  = 50
	maxSessions      = 500
)

// API serves the admin API under /admin/v1/ (ADR 0032). The usage data
// comes from the events in the index, so it shows the requests of this
// Tollgate copy since its start, and the events that it read from the
// usage file at start.
type API struct {
	Index    *usage.Index
	Config   *config.Config
	Breakers func() []telemetry.BreakerState
	Info     Info
	// Now returns the current time. If nil, the API uses time.Now.
	Now func() time.Time
}

// Info describes the running Tollgate.
type Info struct {
	Version        string    `json:"version"`
	Started        time.Time `json:"started"`
	Store          string    `json:"store"`
	UsageSink      string    `json:"usage_sink"`
	DeriveSessions bool      `json:"derive_sessions"`
	StickyRouting  bool      `json:"sticky_routing"`
	// IndexCapacity is the largest number of events in the index.
	IndexCapacity int `json:"index_capacity"`
}

// Handler returns the handler of the admin API.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/v1/info", a.info)
	mux.HandleFunc("GET /admin/v1/usage", a.usage)
	mux.HandleFunc("GET /admin/v1/sessions", a.sessions)
	mux.HandleFunc("GET /admin/v1/sessions/{id}", a.session)
	mux.HandleFunc("GET /admin/v1/breakers", a.breakers)
	mux.HandleFunc("GET /admin/v1/keys", a.keys)
	mux.HandleFunc("/admin/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("The admin API has no endpoint for %s %s. See docs/admin-api.md.", r.Method, r.URL.Path))
	})
	return mux
}

func (a *API) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.MarshalWrite(w, v); err != nil {
		writeError(w, http.StatusInternalServerError, "Tollgate cannot encode the response.")
	}
}

// filter reads the tenant, session, key, from and to parameters. The
// default time range is the 24 hours before now. If a parameter is not
// valid, filter returns a message for the client.
func (a *API) filter(r *http.Request) (usage.Filter, string) {
	q := r.URL.Query()
	f := usage.Filter{TenantID: q.Get("tenant"), SessionID: q.Get("session"), KeyID: q.Get("key")}
	now := a.now()
	f.From, f.To = now.Add(-defaultWindow), now.Add(time.Second)
	for _, p := range []struct {
		name string
		dst  *time.Time
	}{{"from", &f.From}, {"to", &f.To}} {
		v := q.Get(p.name)
		if v == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return f, fmt.Sprintf("The %s parameter %q is not a time. Use RFC 3339, for example 2026-10-01T00:00:00Z.", p.name, v)
		}
		*p.dst = t
	}
	if !f.From.Before(f.To) {
		return f, "The from time must be before the to time."
	}
	return f, ""
}

func (a *API) info(w http.ResponseWriter, _ *http.Request) {
	info := a.Info
	writeJSON(w, struct {
		Info
		Events int `json:"events"`
	}{info, a.Index.Len()})
}

type usageResponse struct {
	From    time.Time     `json:"from"`
	To      time.Time     `json:"to"`
	GroupBy string        `json:"group_by"`
	Groups  []usage.Group `json:"groups"`
}

func (a *API) usage(w http.ResponseWriter, r *http.Request) {
	f, msg := a.filter(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	groupBy := r.URL.Query().Get("group_by")
	if groupBy == "" {
		groupBy = usage.ByNone
	}
	groups, err := usage.Summarize(a.Index.Events(f), groupBy)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("The group_by value %q is not known. Use one of %v.", groupBy, usage.GroupByValues))
		return
	}
	writeJSON(w, usageResponse{From: f.From, To: f.To, GroupBy: groupBy, Groups: groups})
}

// sessionView is a session with its budget.
type sessionView struct {
	usage.Session
	Budget *Projection `json:"budget"`
}

func (a *API) sessions(w http.ResponseWriter, r *http.Request) {
	f, msg := a.filter(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	limit := defaultSessions
	if v := r.URL.Query().Get("limit"); v != "" {
		var err error
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 || limit > maxSessions {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("The limit parameter must be a number from 1 to %d.", maxSessions))
			return
		}
	}
	all := usage.Sessions(a.Index.Events(f))
	out := make([]sessionView, 0, min(limit, len(all)))
	for _, s := range all[:min(limit, len(all))] {
		out = append(out, sessionView{Session: s, Budget: a.projection(&s)})
	}
	writeJSON(w, struct {
		From     time.Time     `json:"from"`
		To       time.Time     `json:"to"`
		Total    int           `json:"total"`
		Sessions []sessionView `json:"sessions"`
	}{f.From, f.To, len(all), out})
}

func (a *API) session(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f := usage.Filter{SessionID: id, TenantID: r.URL.Query().Get("tenant")}
	events := a.Index.Events(f)
	sessions := usage.Sessions(events)
	switch {
	case len(sessions) == 0:
		writeError(w, http.StatusNotFound, fmt.Sprintf("Tollgate has no events of the session %q. The admin API shows only the events in memory.", id))
		return
	case len(sessions) > 1:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("More than one tenant has a session %q. Add the tenant parameter.", id))
		return
	}
	s := sessions[0]
	if len(events) > maxSessionEvents {
		events = events[len(events)-maxSessionEvents:]
	}
	writeJSON(w, struct {
		Session sessionView   `json:"session"`
		Events  []usage.Event `json:"events"`
	}{sessionView{Session: s, Budget: a.projection(&s)}, events})
}

// Projection is the state of the session budget of a session, and an
// estimate of when the session reaches it.
type Projection struct {
	LimitMicroUSD     int64   `json:"limit_micro_usd"`
	Mode              string  `json:"mode"`
	SpendMicroUSD     int64   `json:"spend_micro_usd"`
	RemainingMicroUSD int64   `json:"remaining_micro_usd"`
	UsedRatio         float64 `json:"used_ratio"`
	// WarnRatio is sessions.warn_ratio.
	WarnRatio float64 `json:"warn_ratio"`
	// AvgCostMicroUSD is the mean cost of a request of the session.
	AvgCostMicroUSD int64 `json:"avg_cost_micro_usd"`
	// RequestsLeft is the number of requests at the mean cost that the
	// budget still allows, or nil if the mean cost is 0.
	RequestsLeft *int64 `json:"requests_left"`
	// SpendPerMinuteMicroUSD is the spend divided by the time from the
	// first to the last request, with a minimum of one minute.
	SpendPerMinuteMicroUSD int64 `json:"spend_per_minute_micro_usd"`
	// MinutesLeft is the time to the limit at that rate, or nil if the
	// rate is 0.
	MinutesLeft *float64 `json:"minutes_left"`
}

// projection returns the projection of a session, or nil if its tenant has
// no session budget.
func (a *API) projection(s *usage.Session) *Projection {
	i := slices.IndexFunc(a.Config.Tenants, func(t config.Tenant) bool { return t.ID == s.TenantID })
	if i < 0 {
		return nil
	}
	b, ok := a.Config.Tenants[i].SessionBudget()
	if !ok {
		return nil
	}
	limit := int64(b.LimitUSD)
	p := &Projection{
		LimitMicroUSD:     limit,
		Mode:              b.Mode,
		SpendMicroUSD:     s.CostMicroUSD,
		RemainingMicroUSD: max(limit-s.CostMicroUSD, 0),
		UsedRatio:         float64(s.CostMicroUSD) / float64(limit),
		WarnRatio:         a.Config.Sessions.WarnRatio,
	}
	if s.Requests > 0 {
		p.AvgCostMicroUSD = s.CostMicroUSD / s.Requests
	}
	if p.AvgCostMicroUSD > 0 {
		left := p.RemainingMicroUSD / p.AvgCostMicroUSD
		p.RequestsLeft = &left
	}
	minutes := max(s.Last.Sub(s.First).Minutes(), 1)
	p.SpendPerMinuteMicroUSD = int64(float64(s.CostMicroUSD) / minutes)
	if p.SpendPerMinuteMicroUSD > 0 {
		left := float64(p.RemainingMicroUSD) / float64(p.SpendPerMinuteMicroUSD)
		p.MinutesLeft = &left
	}
	return p
}

// breakerView is the state of one breaker.
type breakerView struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Region   string `json:"region,omitempty"`
	State    string `json:"state"`
}

var breakerStates = []string{"closed", "half_open", "open"}

func (a *API) breakers(w http.ResponseWriter, _ *http.Request) {
	var out []breakerView
	for _, b := range a.Breakers() {
		state := "unknown"
		if b.State >= 0 && b.State < len(breakerStates) {
			state = breakerStates[b.State]
		}
		out = append(out, breakerView{Provider: b.Provider, Model: b.Model, Region: b.Region, State: state})
	}
	writeJSON(w, struct {
		Breakers []breakerView `json:"breakers"`
	}{out})
}

type budgetView struct {
	Window   string `json:"window"`
	LimitUSD string `json:"limit_usd"`
	Mode     string `json:"mode"`
	// SpendMicroUSD is the spend of the current month in the index, for a
	// month budget.
	SpendMicroUSD *int64 `json:"spend_micro_usd,omitempty"`
}

type keyView struct {
	ID string `json:"id"`
	usage.Totals
}

type limitsView struct {
	RequestsPerMinute int64 `json:"requests_per_minute"`
	TokensPerMinute   int64 `json:"tokens_per_minute"`
}

type tenantView struct {
	ID       string       `json:"id"`
	Models   []string     `json:"models"`
	Limits   limitsView   `json:"limits"`
	Budgets  []budgetView `json:"budgets"`
	Redacted []string     `json:"redaction"`
	Keys     []keyView    `json:"keys"`
}

// keys lists the tenants and their keys, with the usage of each key in
// the current month. It never shows a key hash.
func (a *API) keys(w http.ResponseWriter, _ *http.Request) {
	now := a.now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	byKey := make(map[string]usage.Totals)
	if groups, err := usage.Summarize(a.Index.Events(usage.Filter{From: month}), usage.ByKey); err == nil {
		for _, g := range groups {
			byKey[g.Key] = g.Totals
		}
	}
	out := make([]tenantView, 0, len(a.Config.Tenants))
	for _, t := range a.Config.Tenants {
		tv := tenantView{ID: t.ID, Models: t.Models, Limits: limitsView(t.Limits), Budgets: []budgetView{}, Redacted: []string{}, Keys: []keyView{}}
		var spend int64
		for _, k := range t.Keys {
			tot := byKey[t.ID+"/"+k.ID]
			spend += tot.CostMicroUSD
			tv.Keys = append(tv.Keys, keyView{ID: k.ID, Totals: tot})
		}
		for _, b := range t.Budgets {
			bv := budgetView{Window: b.Window, LimitUSD: b.LimitUSD.String(), Mode: b.Mode}
			if b.Window == config.WindowMonth {
				bv.SpendMicroUSD = &spend
			}
			tv.Budgets = append(tv.Budgets, bv)
		}
		tv.Redacted = append(tv.Redacted, t.Redact.Kinds...)
		for _, p := range t.Redact.Patterns {
			tv.Redacted = append(tv.Redacted, p.Name)
		}
		out = append(out, tv)
	}
	writeJSON(w, struct {
		Tenants []tenantView `json:"tenants"`
	}{out})
}
