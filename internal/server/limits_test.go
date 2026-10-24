package server

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ykakade/tollgate/internal/budget"
	"github.com/ykakade/tollgate/internal/limiter"
	"github.com/ykakade/tollgate/internal/preflight"
	"github.com/ykakade/tollgate/internal/usage"
)

// testPrices gives the test models 1 micro-USD for each input token and 2
// micro-USD for each output token.
const testPrices = `
- provider: anthropic
  model: claude-upstream
  effective_from: 2026-01-01
  micro_usd_per_mtok: {input: 1000000, output: 2000000, cache_read: 100000, cache_write: 1250000}
- provider: openai
  model: gpt-test
  effective_from: 2026-01-01
  micro_usd_per_mtok: {input: 1000000, output: 2000000}
`

// testNow is the fixed time of the tests, in the middle of a minute.
var testNow = time.Date(2026, 11, 3, 10, 20, 30, 0, time.UTC)

func fixedNow() time.Time { return testNow }

// anthropicReply is a response with 100 input and 50 output tokens: a
// cost of 200 micro-USD.
const anthropicReply = `{"id":"m","type":"message","role":"assistant","content":[],"usage":{"input_tokens":100,"output_tokens":50}}`

const anthropicBody = `{"model":"claude-test","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`

// reservation returns the reservation of anthropicBody with the test
// prices. The estimate uses the body that goes upstream, with the model
// of the target.
func reservation() int64 {
	up := strings.Replace(anthropicBody, "claude-test", "claude-upstream", 1)
	return preflight.EstimateInputTokens(len(up)) + 2*10
}

// counting returns a handler that counts its requests and then calls h.
func counting(n *atomic.Int64, h http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		h.ServeHTTP(w, r)
	}
}

// capture is an Emitter that keeps the events.
type capture struct {
	mu     sync.Mutex
	events []usage.Event
}

func (c *capture) Emit(e usage.Event) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
	return true
}

func (c *capture) all() []usage.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]usage.Event(nil), c.events...)
}

// waitEvents waits until c has n events.
func waitEvents(t *testing.T, c *capture, n int) []usage.Event {
	t.Helper()
	waitFor(t, func() bool { return len(c.all()) >= n })
	return c.all()
}

func TestRequestRateLimit(t *testing.T) {
	var calls atomic.Int64
	e := newEnv(t, counting(&calls, fixed(nil, 200, "application/json", anthropicReply)), setup{
		tenant: "    limits: {requests_per_minute: 2}",
		now:    fixedNow,
	})
	for i, wantRemaining := range []string{"1", "0"} {
		resp, body := e.post("/v1/messages", anthropicBody)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: status %d, body %s", i, resp.StatusCode, body)
		}
		if got := resp.Header.Get("Ratelimit-Remaining"); got != wantRemaining || resp.Header.Get("Ratelimit-Limit") != "2" || resp.Header.Get("Ratelimit-Reset") != "30" {
			t.Errorf("request %d: rate limit headers %v", i, resp.Header)
		}
	}
	resp, body := e.post("/v1/messages", anthropicBody)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("third request: status %d, want 429", resp.StatusCode)
	}
	var got struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || got.Type != "error" || got.Error.Type != "rate_limit_error" {
		t.Errorf("429 body = %s, want an Anthropic rate_limit_error", body)
	}
	if !strings.Contains(got.Error.Message, `The tenant "personal" sent its limit of 2 requests per minute.`) {
		t.Errorf("message = %q", got.Error.Message)
	}
	if ra := resp.Header.Get("Retry-After"); ra == "" || ra == "0" || resp.Header.Get("Ratelimit-Remaining") != "0" {
		t.Errorf("429 headers: %v", resp.Header)
	}
	if calls.Load() != 2 {
		t.Errorf("the upstream got %d requests, want 2", calls.Load())
	}
	if line := e.logLine(resp.Header.Get("X-Request-Id")); line["denied_by"] != "rate_limit" || line["status"] != float64(429) {
		t.Errorf("access log line = %v", line)
	}
}

func TestTokenRateLimit(t *testing.T) {
	e := newEnv(t, fixed(nil, 200, "application/json", anthropicReply), setup{
		tenant: "    limits: {tokens_per_minute: 470}",
		now:    fixedNow,
	})
	// The first request reserves its estimate. The settle step replaces it
	// with the 150 tokens of the response.
	for range 3 {
		if resp, body := e.post("/v1/messages", anthropicBody); resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d, body %s", resp.StatusCode, body)
		}
	}
	resp, body := e.post("/v1/messages", anthropicBody)
	if resp.StatusCode != http.StatusTooManyRequests || !strings.Contains(body, "used its limit of 470 tokens per minute") {
		t.Errorf("fourth request: status %d, body %s; want 429 after 450 tokens and a reservation of 32", resp.StatusCode, body)
	}
	big := `{"model":"claude-test","max_tokens":10,"messages":[{"role":"user","content":"` + strings.Repeat("x", 4000) + `"}]}`
	resp, body = e.post("/v1/messages", big)
	if resp.StatusCode != http.StatusTooManyRequests || !strings.Contains(body, "The request needs about") || resp.Header.Get("Retry-After") != "60" {
		t.Errorf("large request: status %d, body %s, headers %v", resp.StatusCode, body, resp.Header)
	}
}

func TestCountTokensUsesRequestLimitOnly(t *testing.T) {
	e := newEnv(t, fixed(nil, 200, "application/json", `{"input_tokens":3}`), setup{
		tenant: "    limits: {requests_per_minute: 5, tokens_per_minute: 1}\n    budgets: [{window: month, limit_usd: 0.000001}]",
		prices: testPrices,
		now:    fixedNow,
	})
	resp, body := e.post("/v1/messages/count_tokens", anthropicBody)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Ratelimit-Limit") != "5" {
		t.Errorf("count_tokens: status %d, body %s, headers %v", resp.StatusCode, body, resp.Header)
	}
}

func TestHardMonthBudget(t *testing.T) {
	store := limiter.NewMemory()
	var calls atomic.Int64
	e := newEnv(t, counting(&calls, fixed(nil, 200, "application/json", anthropicReply)), setup{
		tenant: "    budgets: [{window: month, limit_usd: 0.0005}]",
		prices: testPrices,
		now:    fixedNow,
		store:  store,
	})
	key := budget.MonthKey("personal", testNow)
	// Each response costs 200 micro-USD. A request is allowed while the
	// spend and its reservation fit in 500 micro-USD.
	for i, want := range []int64{200, 400, 600} {
		if resp, body := e.post("/v1/messages", anthropicBody); resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: status %d, body %s", i, resp.StatusCode, body)
		}
		if got := store.Value(key, testNow); got != want {
			t.Errorf("after request %d, spend = %d, want %d", i, got, want)
		}
	}
	resp, body := e.post("/v1/messages", anthropicBody)
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("fourth request: status %d, body %s; want 402", resp.StatusCode, body)
	}
	want := `{"type":"error","error":{"type":"budget_exceeded","message":"The tenant \"personal\" does not have sufficient budget for this request. The month budget is 0.0005 USD, and the spend is 0.0006 USD.`
	if !strings.HasPrefix(body, want) || resp.Header.Get("Retry-After") != "" {
		t.Errorf("402 body:\n%s\nwant prefix:\n%s", body, want)
	}
	if calls.Load() != 3 || store.Value(key, testNow) != 600 {
		t.Errorf("upstream calls %d, spend %d; want 3 and 600", calls.Load(), store.Value(key, testNow))
	}
}

func TestBudgetErrorOpenAIFormat(t *testing.T) {
	e := newEnv(t, fixed(nil, 200, "application/json", `{}`), setup{
		tenant: "    budgets: [{window: month, limit_usd: 0.000001}]",
		prices: testPrices,
		now:    fixedNow,
	})
	resp, body := e.post("/v1/chat/completions", `{"model":"gpt-test","max_tokens":10,"messages":[]}`)
	if resp.StatusCode != http.StatusPaymentRequired || !strings.HasPrefix(body, `{"error":{"message":"The tenant \"personal\" does not have sufficient budget`) || !strings.Contains(body, `"type":"budget_exceeded","param":null,"code":"budget_exceeded"}}`) {
		t.Errorf("status %d, body %s", resp.StatusCode, body)
	}
}

func TestSettleCases(t *testing.T) {
	for _, tt := range []struct {
		name     string
		upstream http.Handler
		url      string
		want     func() int64
	}{
		// The provider reported the usage: the spend is its cost.
		{"usage", fixed(nil, 200, "application/json", anthropicReply), "", func() int64 { return 200 }},
		// No usage in a successful response: the spend is the reservation.
		{"no usage", fixed(nil, 200, "application/json", `{"id":"m"}`), "", reservation},
		// A provider error has no cost: the reservation goes back.
		{"provider error", fixed(nil, 400, "application/json", `{"type":"error"}`), "", func() int64 { return 0 }},
		{"provider down", nil, "http://127.0.0.1:1", func() int64 { return 0 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := limiter.NewMemory()
			up := tt.upstream
			if up == nil {
				up = http.NotFoundHandler()
			}
			ev := &capture{}
			e := newEnv(t, up, setup{
				tenant:      "    budgets: [{window: month, limit_usd: 10}]",
				prices:      testPrices,
				now:         fixedNow,
				store:       store,
				usage:       ev,
				upstreamURL: tt.url,
			})
			resp, _ := e.post("/v1/messages", anthropicBody)
			if got := store.Value(budget.MonthKey("personal", testNow), testNow); got != tt.want() {
				t.Errorf("status %d: spend = %d, want %d", resp.StatusCode, got, tt.want())
			}
			if events := waitEvents(t, ev, 1); events[0].CostMicroUSD != tt.want() {
				t.Errorf("event cost = %d, want %d", events[0].CostMicroUSD, tt.want())
			}
		})
	}
}

func TestSessionBudget(t *testing.T) {
	store := limiter.NewMemory()
	e := newEnv(t, fixed(nil, 200, "application/json", anthropicReply), setup{
		tenant: "    budgets: [{window: session, limit_usd: 0.0003}]",
		prices: testPrices,
		now:    fixedNow,
		store:  store,
	})
	send := func(session string) (response, string) {
		req := e.request(t.Context(), "/v1/messages", anthropicBody)
		if session != "" {
			req.Header.Set("X-Tollgate-Session", session)
		}
		return e.do(req)
	}
	for _, s := range []string{"s1", "s1", "s2", "", ""} {
		if resp, body := send(s); resp.StatusCode != http.StatusOK {
			t.Fatalf("session %q: status %d, body %s", s, resp.StatusCode, body)
		}
	}
	resp, body := send("s1")
	if resp.StatusCode != http.StatusPaymentRequired || !strings.Contains(body, `The session \"s1\" does not have sufficient budget for this request. The session budget is 0.0003 USD, and the spend is 0.0004 USD.`) {
		t.Errorf("third request of s1: status %d, body %s", resp.StatusCode, body)
	}
	if got := store.Value(budget.SessionKey("personal", "s2"), testNow); got != 200 {
		t.Errorf("spend of s2 = %d, want 200", got)
	}
	resp, body = send("bad session")
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "The x-tollgate-session header is not valid.") {
		t.Errorf("bad session: status %d, body %s", resp.StatusCode, body)
	}
}

func TestSoftBudgetWarns(t *testing.T) {
	e := newEnv(t, fixed(nil, 200, "application/json", anthropicReply), setup{
		tenant: "    budgets: [{window: month, limit_usd: 0.0001, mode: soft}]",
		prices: testPrices,
		now:    fixedNow,
	})
	for range 3 {
		if resp, body := e.post("/v1/messages", anthropicBody); resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d, body %s", resp.StatusCode, body)
		}
	}
	waitFor(t, func() bool { return strings.Count(e.logs.String(), `"msg":"soft budget exceeded"`) == 2 })
	if !strings.Contains(e.logs.String(), `"window":"month","limit_micro_usd":100,"spend_micro_usd":`) {
		t.Errorf("logs:\n%s", e.logs.String())
	}
}

// brokenStore is a limit store that always fails.
type brokenStore struct{}

func (brokenStore) Admit(context.Context, *limiter.Request) (limiter.Decision, error) {
	return limiter.Decision{}, errors.New("connection refused")
}
func (brokenStore) Settle(context.Context, *limiter.Settlement) error {
	return errors.New("connection refused")
}
func (brokenStore) Ping(context.Context) error { return errors.New("connection refused") }

func TestStoreDown(t *testing.T) {
	t.Run("hard budget fails closed", func(t *testing.T) {
		var calls atomic.Int64
		e := newEnv(t, counting(&calls, fixed(nil, 200, "application/json", anthropicReply)), setup{
			tenant: "    limits: {requests_per_minute: 100}\n    budgets: [{window: month, limit_usd: 10}]",
			prices: testPrices,
			store:  &limiter.Failover{Main: brokenStore{}, Local: limiter.NewMemory()},
		})
		resp, body := e.post("/v1/messages", anthropicBody)
		if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") != "5" || !strings.Contains(body, "the limit store is not available") || calls.Load() != 0 {
			t.Errorf("status %d, body %s, upstream calls %d", resp.StatusCode, body, calls.Load())
		}
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, e.url+"/readyz", nil)
		if resp, body := e.do(req); resp.StatusCode != http.StatusOK || body != `{"status":"ready","store":"unavailable"}`+"\n" {
			t.Errorf("/readyz: status %d, body %s; want ready with the store state", resp.StatusCode, body)
		}
	})
	t.Run("rate limits fail open", func(t *testing.T) {
		e := newEnv(t, fixed(nil, 200, "application/json", anthropicReply), setup{
			tenant: "    limits: {requests_per_minute: 2}\n    budgets: [{window: month, limit_usd: 10, mode: soft}]",
			prices: testPrices,
			now:    fixedNow,
			store:  &limiter.Failover{Main: brokenStore{}, Local: limiter.NewMemory()},
		})
		for range 2 {
			if resp, body := e.post("/v1/messages", anthropicBody); resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d, body %s", resp.StatusCode, body)
			}
		}
		// The local backup limit still applies.
		if resp, _ := e.post("/v1/messages", anthropicBody); resp.StatusCode != http.StatusTooManyRequests {
			t.Errorf("third request: status %d, want 429 from the local limit", resp.StatusCode)
		}
	})
}

func TestUsageEvent(t *testing.T) {
	ev := &capture{}
	e := newEnv(t, fixed(nil, 200, "text/event-stream", anthropicStream), setup{
		tenant: "    budgets: [{window: month, limit_usd: 10}]",
		prices: testPrices,
		usage:  ev,
	})
	req := e.request(t.Context(), "/v1/messages", `{"model":"claude-test","max_tokens":10,"stream":true,"messages":[]}`)
	req.Header.Set("X-Tollgate-Session", "task-7")
	resp, _ := e.do(req)
	// No key: no tenant, so no event.
	noKey, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, e.url+"/v1/messages", strings.NewReader(anthropicBody))
	e.do(noKey)

	got := waitEvents(t, ev, 1)[0]
	// anthropicStream: 12 input, 4 cache read, 3 cache write, 9 output.
	wantCost := int64(12 + 18 + 1 + 4)
	if got.RequestID != resp.Header.Get("X-Request-Id") || got.TenantID != "personal" || got.KeyID != "laptop" || got.SessionID != "task-7" ||
		got.RouteModel != "claude-test" || got.Provider != "anthropic" || got.ProviderModel != "claude-upstream" || got.Status != 200 ||
		got.Attempts != 1 || got.Cache != "bypass" || got.InputTokens != 12 || got.OutputTokens != 9 || got.CacheReadTokens != 4 ||
		got.CacheWriteTokens != 3 || got.UsageEstimated || got.CostMicroUSD != wantCost || got.TTFTMs == nil || got.TS.IsZero() {
		t.Errorf("event = %+v, want cost %d", got, wantCost)
	}
	if line := e.logLine(got.RequestID); line["cost_micro_usd"] != float64(wantCost) || line["session_id"] != "task-7" {
		t.Errorf("access log line = %v", line)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(ev.all()); n != 1 {
		t.Errorf("%d events, want 1", n)
	}
}

func TestRestoreSpend(t *testing.T) {
	store := limiter.NewMemory()
	e := newEnv(t, fixed(nil, 200, "application/json", anthropicReply), setup{
		tenant: "    budgets: [{window: month, limit_usd: 0.001}, {window: session, limit_usd: 1}]",
		prices: testPrices,
		now:    fixedNow,
		store:  store,
	})
	sp := usage.Spend{
		Month:    map[string]int64{"personal": 990, "other": 5, "zero": 0},
		Sessions: []usage.SessionSpend{{TenantID: "personal", SessionID: "s", Cost: 77, Last: testNow.Add(-time.Hour)}},
	}
	if err := e.srv.RestoreSpend(t.Context(), sp); err != nil {
		t.Fatal(err)
	}
	if got := store.Value(budget.MonthKey("personal", testNow), testNow); got != 990 {
		t.Errorf("restored month spend = %d, want 990", got)
	}
	sessKey := budget.SessionKey("personal", "s")
	if store.Value(sessKey, testNow) != 77 || store.Value(sessKey, testNow.Add(23*time.Hour)) != 0 {
		t.Error("the session spend does not end 24 hours after the last event")
	}
	if resp, _ := e.post("/v1/messages", anthropicBody); resp.StatusCode != http.StatusPaymentRequired {
		t.Errorf("status %d, want 402 after the restored spend", resp.StatusCode)
	}
}

func TestRestoreSpendError(t *testing.T) {
	e := newEnv(t, fixed(nil, 200, "application/json", anthropicReply), setup{store: brokenStore{}})
	err := e.srv.RestoreSpend(t.Context(), usage.Spend{Month: map[string]int64{"personal": 1}})
	if err == nil || !strings.HasPrefix(err.Error(), "restore spend: ") {
		t.Errorf("RestoreSpend() = %v", err)
	}
}

func TestRateHeaderPicksTightestLimit(t *testing.T) {
	h := rateHeader(limiter.Decision{Rates: []limiter.RateState{
		{Limit: 100, Remaining: 50, Reset: 1500 * time.Millisecond},
		{Limit: 1000, Remaining: 100, Reset: 1500 * time.Millisecond},
	}})
	if fmt.Sprint(h["Ratelimit-Limit"], h["Ratelimit-Remaining"], h["Ratelimit-Reset"]) != "[1000] [100] [2]" {
		t.Errorf("headers = %v", h)
	}
	if rateHeader(limiter.Decision{}) != nil {
		t.Error("headers without rate limits are not nil")
	}
}

// TestIncompleteStreamKeepsReservation checks that a stream that stops
// early costs at least its reservation, because the provider can charge
// for output that Tollgate did not receive.
func TestIncompleteStreamKeepsReservation(t *testing.T) {
	first := "event: message_start\n" +
		`data: {"type":"message_start","message":{"usage":{"input_tokens":3,"output_tokens":1}}}` + "\n\n"
	store := limiter.NewMemory()
	ev := &capture{}
	e := newEnv(t, stallAfter(first, nil), setup{
		tenant:   "    budgets: [{window: month, limit_usd: 10}]",
		prices:   testPrices,
		now:      fixedNow,
		store:    store,
		usage:    ev,
		timeouts: "{connect: 1s, first_token: 5s, idle: 200ms, total: 10s}",
	})
	body := `{"model":"claude-test","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	up := strings.Replace(body, "claude-test", "claude-upstream", 1)
	want := preflight.EstimateInputTokens(len(up)) + 2*10
	resp, got := e.post("/v1/messages", body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(got, "event: error") {
		t.Fatalf("status %d, body %s; want a stream that ends with an error event", resp.StatusCode, got)
	}
	if spend := store.Value(budget.MonthKey("personal", testNow), testNow); spend != want {
		t.Errorf("spend = %d, want the reservation %d", spend, want)
	}
	if events := waitEvents(t, ev, 1); events[0].CostMicroUSD != want || events[0].InputTokens != 3 {
		t.Errorf("event = %+v, want cost %d and the received usage", events[0], want)
	}
}
