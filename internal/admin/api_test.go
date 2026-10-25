package admin

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ykakade/tollgate/internal/config"
	"github.com/ykakade/tollgate/internal/telemetry"
	"github.com/ykakade/tollgate/internal/usage"
)

var apiNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

const apiConfig = `version: 1
providers:
  anthropic: {kind: anthropic, api_key: env:K}
routes:
  - model: claude-sonnet-5-5
    api: anthropic
    targets: [{provider: anthropic}]
tenants:
  - id: team
    keys:
      - {id: laptop, hash: "sha256:0000000000000000000000000000000000000000000000000000000000000001"}
      - {id: ci, hash: "sha256:0000000000000000000000000000000000000000000000000000000000000002"}
    models: [claude-sonnet-5-5]
    limits: {requests_per_minute: 60}
    budgets:
      - {window: month, limit_usd: 50}
      - {window: session, limit_usd: 1}
    redact: {kinds: [email], patterns: [{name: TICKET, regex: 'T-[0-9]+'}]}
  - id: solo
    keys: [{id: phone, hash: "sha256:0000000000000000000000000000000000000000000000000000000000000003"}]
    models: [claude-sonnet-5-5]
`

func ttft(v int64) *int64 { return &v }

func newAPI(t *testing.T) *API {
	t.Helper()
	cfg, err := config.Parse("admin.yaml", []byte(apiConfig), config.Env{LookupEnv: func(string) (string, bool) { return "key", true }})
	if err != nil {
		t.Fatal(err)
	}
	ix := usage.NewIndex(100)
	for _, e := range []usage.Event{
		{TS: apiNow.Add(-30 * time.Hour), TenantID: "team", KeyID: "laptop", SessionID: "old", Status: 200, CostMicroUSD: 999},
		{TS: apiNow.Add(-50 * time.Minute), TenantID: "team", KeyID: "laptop", SessionID: "s1", Provider: "anthropic", ProviderModel: "m", Status: 200, Attempts: 1, CostMicroUSD: 100_000, TTFTMs: ttft(300)},
		{TS: apiNow.Add(-10 * time.Minute), TenantID: "team", KeyID: "laptop", SessionID: "s1", Provider: "anthropic", ProviderModel: "m", Status: 200, Attempts: 1, CostMicroUSD: 300_000, TTFTMs: ttft(500)},
		{TS: apiNow.Add(-5 * time.Minute), TenantID: "team", KeyID: "ci", SessionID: "auto-x", SessionDerived: true, Status: 502},
		{TS: apiNow.Add(-1 * time.Minute), TenantID: "solo", KeyID: "phone", SessionID: "s1", Status: 200, CostMicroUSD: 5},
	} {
		ix.Emit(e)
	}
	return &API{
		Index:  ix,
		Config: cfg,
		Breakers: func() []telemetry.BreakerState {
			return []telemetry.BreakerState{{Provider: "anthropic", Model: "m", State: 2}, {Provider: "vertex", Model: "m", Region: "us-east5", State: 0}}
		},
		Info: Info{Version: "v0.0.0-test", Store: "memory"},
		Now:  func() time.Time { return apiNow },
	}
}

func getJSON(t *testing.T, h http.Handler, path string, v any) int {
	t.Helper()
	code, body := get(t, h, path, "")
	if v != nil && code == http.StatusOK {
		if err := json.Unmarshal([]byte(body), v); err != nil {
			t.Fatalf("%s: %v\n%s", path, err, body)
		}
	}
	return code
}

func TestAPIUsage(t *testing.T) {
	h := newAPI(t).Handler()
	var r struct {
		GroupBy string        `json:"group_by"`
		Groups  []usage.Group `json:"groups"`
	}
	if code := getJSON(t, h, "/admin/v1/usage", &r); code != 200 || len(r.Groups) != 1 || r.Groups[0].Requests != 4 || r.Groups[0].CostMicroUSD != 400_005 || r.Groups[0].Errors != 1 {
		t.Errorf("usage of the last 24 hours: %d %+v", code, r)
	}
	if getJSON(t, h, "/admin/v1/usage?group_by=key&tenant=team", &r); len(r.Groups) != 2 || r.Groups[0].Key != "team/laptop" || *r.Groups[0].TTFTP95Ms != 500 {
		t.Errorf("usage by key: %+v", r.Groups)
	}
	if getJSON(t, h, "/admin/v1/usage?group_by=hour&from=2026-10-19T00:00:00Z", &r); len(r.Groups) != 2 || r.Groups[0].Key != "2026-10-19T06:00:00Z" || r.Groups[1].Requests != 4 {
		t.Errorf("usage by hour: %+v", r.Groups)
	}
	for path, msg := range map[string]string{
		"/admin/v1/usage?group_by=week":             "The group_by value",
		"/admin/v1/usage?from=yesterday":            "is not a time",
		"/admin/v1/usage?from=2026-10-21T00:00:00Z": "must be before",
		"/admin/v1/sessions?limit=0":                "The limit parameter",
		"/admin/v1/sessions/s1":                     "Add the tenant parameter",
		"/admin/v1/sessions/none":                   "has no events",
		"/admin/v1/nothing":                         "no endpoint",
	} {
		code, body := get(t, h, path, "")
		if code < 400 || !strings.Contains(body, msg) {
			t.Errorf("%s: %d %s", path, code, body)
		}
	}
}

func TestAPISessions(t *testing.T) {
	h := newAPI(t).Handler()
	var list struct {
		Total    int           `json:"total"`
		Sessions []sessionView `json:"sessions"`
	}
	if getJSON(t, h, "/admin/v1/sessions?tenant=team", &list); list.Total != 2 || list.Sessions[0].SessionID != "auto-x" || !list.Sessions[0].Derived {
		t.Fatalf("sessions: %+v", list)
	}
	var one struct {
		Session sessionView   `json:"session"`
		Events  []usage.Event `json:"events"`
	}
	if code := getJSON(t, h, "/admin/v1/sessions/s1?tenant=team", &one); code != 200 || len(one.Events) != 2 {
		t.Fatalf("session: %d %+v", code, one)
	}
	p := one.Session.Budget
	// 400,000 of 1,000,000 micro-USD in 40 minutes: 10,000 each minute,
	// 60 minutes and 3 requests at the mean cost left.
	if p == nil || p.SpendMicroUSD != 400_000 || p.RemainingMicroUSD != 600_000 || p.UsedRatio != 0.4 || p.AvgCostMicroUSD != 200_000 || *p.RequestsLeft != 3 || p.SpendPerMinuteMicroUSD != 10_000 || *p.MinutesLeft != 60 || p.WarnRatio != 0.8 {
		t.Errorf("projection: %+v", p)
	}
	if getJSON(t, h, "/admin/v1/sessions/s1?tenant=solo", &one); one.Session.Budget != nil {
		t.Errorf("a tenant with no session budget has a projection: %+v", one.Session.Budget)
	}
}

func TestAPIBreakersKeysInfo(t *testing.T) {
	h := newAPI(t).Handler()
	var b struct {
		Breakers []breakerView `json:"breakers"`
	}
	if getJSON(t, h, "/admin/v1/breakers", &b); len(b.Breakers) != 2 || b.Breakers[0].State != "open" || b.Breakers[1].State != "closed" || b.Breakers[1].Region != "us-east5" {
		t.Errorf("breakers: %+v", b)
	}
	code, body := get(t, h, "/admin/v1/keys", "")
	if code != 200 || strings.Contains(body, "sha256") {
		t.Fatalf("keys: %d %s", code, body)
	}
	var k struct {
		Tenants []tenantView `json:"tenants"`
	}
	getJSON(t, h, "/admin/v1/keys", &k)
	team := k.Tenants[0]
	if team.ID != "team" || len(team.Keys) != 2 || team.Keys[0].CostMicroUSD != 400_999 || team.Keys[1].Errors != 1 || team.Limits.RequestsPerMinute != 60 {
		t.Errorf("team: %+v", team)
	}
	if *team.Budgets[0].SpendMicroUSD != 400_999 || team.Budgets[1].SpendMicroUSD != nil || strings.Join(team.Redacted, ",") != "email,TICKET" {
		t.Errorf("team budgets: %+v, redaction %v", team.Budgets, team.Redacted)
	}
	var info struct {
		Version string `json:"version"`
		Events  int    `json:"events"`
	}
	if getJSON(t, h, "/admin/v1/info", &info); info.Version != "v0.0.0-test" || info.Events != 5 {
		t.Errorf("info: %+v", info)
	}
}

func TestDashboardNeedsNoToken(t *testing.T) {
	files := http.FileServerFS(fstest.MapFS{"index.html": {Data: []byte("<html>dashboard</html>")}})
	h := Handler(Options{Metrics: metrics, Token: "s3cret", API: newAPI(t).Handler(), Dashboard: files})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "dashboard") || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Errorf("dashboard: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	if code, _ := get(t, h, "/admin/v1/info", ""); code != http.StatusUnauthorized {
		t.Errorf("the API without the token: %d", code)
	}
	if code, _ := get(t, h, "/admin/v1/info", "s3cret"); code != http.StatusOK {
		t.Errorf("the API with the token: %d", code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("POST / without the token: %d", rec.Code)
	}
}
