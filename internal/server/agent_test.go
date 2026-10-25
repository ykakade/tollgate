package server

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ykakade/tollgate/internal/config"
	"github.com/ykakade/tollgate/internal/limiter"
	"github.com/ykakade/tollgate/internal/pricing"
	"github.com/ykakade/tollgate/internal/provider"
)

func TestDerivedSession(t *testing.T) {
	c := &capture{}
	e := newEnv(t, fixed(nil, 200, "application/json", anthropicReply), setup{usage: c})
	turn2 := `{"model":"claude-test","max_tokens":10,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"Hello."},{"role":"user","content":"Fix it."}]}`
	other := `{"model":"claude-test","max_tokens":10,"messages":[{"role":"user","content":"a different task"}]}`
	for _, body := range []string{anthropicBody, turn2, other} {
		if resp, b := e.post("/v1/messages", body); resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d, body %s", resp.StatusCode, b)
		}
	}
	req := e.request(t.Context(), "/v1/messages", anthropicBody)
	req.Header.Set("X-Tollgate-Session", "my-task")
	e.do(req)
	ev := waitEvents(t, c, 4)
	if !strings.HasPrefix(ev[0].SessionID, "auto-") || len(ev[0].SessionID) != len("auto-")+32 || !ev[0].SessionDerived {
		t.Errorf("first event: session %q, derived %v", ev[0].SessionID, ev[0].SessionDerived)
	}
	if ev[1].SessionID != ev[0].SessionID {
		t.Errorf("two turns of one conversation have the sessions %q and %q", ev[0].SessionID, ev[1].SessionID)
	}
	if ev[2].SessionID == ev[0].SessionID {
		t.Error("a different conversation has the same session")
	}
	if ev[3].SessionID != "my-task" || ev[3].SessionDerived {
		t.Errorf("the header gave the session %q, derived %v", ev[3].SessionID, ev[3].SessionDerived)
	}

	c = &capture{}
	e = newEnv(t, fixed(nil, 200, "application/json", anthropicReply), setup{usage: c, top: "sessions: {derive: false}"})
	e.post("/v1/messages", anthropicBody)
	if ev := waitEvents(t, c, 1); ev[0].SessionID != "" {
		t.Errorf("derive is off, but the session is %q", ev[0].SessionID)
	}
}

func TestStickyRouting(t *testing.T) {
	for _, sticky := range []bool{true, false} {
		t.Run(fmt.Sprint("sticky=", sticky), func(t *testing.T) {
			c := &capture{}
			e, a, b := failureEnvWith(t, "anthropic", "anthropic", "status:529", fmt.Sprintf("sessions: {sticky: %v}", sticky), nil, func(o *Options) { o.Usage = c })
			resp, body := e.post("/v1/messages", failureBody)
			if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Tollgate-Provider") != "second" {
				t.Fatalf("first request: status %d, provider %q, body %s", resp.StatusCode, resp.Header.Get("X-Tollgate-Provider"), body)
			}
			if err := a.SetScenario("ok"); err != nil {
				t.Fatal(err)
			}
			resp, _ = e.post("/v1/messages", failureBody)
			want, calls := "first", uint64(2)
			if sticky {
				want, calls = "second", 1
			}
			if got := resp.Header.Get("X-Tollgate-Provider"); got != want || a.Requests() != calls || resp.Header.Get("X-Tollgate-Attempts") != "1" {
				t.Errorf("second request went to %q after %d calls to first, want %q after %d", got, a.Requests(), want, calls)
			}
			if sticky && b.Requests() != 2 {
				t.Errorf("second target has %d requests, want 2", b.Requests())
			}
			ev := waitEvents(t, c, 2)
			if ev[0].TargetSwitched || ev[1].TargetSwitched == sticky {
				t.Errorf("target_switched = %v, %v; want false, %v", ev[0].TargetSwitched, ev[1].TargetSwitched, !sticky)
			}
		})
	}
}

func TestStickyValue(t *testing.T) {
	l := &targetList{}
	for _, p := range []string{"a", "b", "c"} {
		l.add(&target{Target: configTarget(p, "m")})
	}
	for i := range l.targets {
		if got := stickyIndex(stickyValue(i, l.targets[i]), l); got != i {
			t.Errorf("stickyIndex(stickyValue(%d)) = %d", i, got)
		}
	}
	moved := &targetList{}
	moved.add(l.targets[2])
	moved.add(l.targets[0])
	if got := stickyIndex(stickyValue(0, l.targets[0]), moved); got != -1 {
		t.Errorf("a value for a target that moved gave index %d, want -1", got)
	}
	for _, v := range []int64{0, -1, 9, 1 << 40} {
		if got := stickyIndex(v, l); got != -1 {
			t.Errorf("stickyIndex(%d) = %d, want -1", v, got)
		}
	}
}

func TestCacheLoss(t *testing.T) {
	tbl, errs := pricing.Parse([]byte(testPrices))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	price := tbl.Find("anthropic", "anthropic", "claude-upstream", testNow)
	// 1,000 input and 9,000 cache write tokens cost 1,000 + 11,250
	// micro-USD. As 10,000 cache reads, they cost 1,000.
	if got := cacheLoss(price, provider.Usage{InputTokens: 1000, CacheWriteTokens: 9000, CacheReadTokens: 5, OutputTokens: 7}); got != 11250 {
		t.Errorf("cacheLoss = %d, want 11250", got)
	}
	if got := cacheLoss(nil, provider.Usage{InputTokens: 1000}); got != 0 {
		t.Errorf("cacheLoss with no price = %d", got)
	}
}

// longStream returns an upstream handler that sends an Anthropic stream
// with 100 input tokens and n text deltas of 8 bytes. It counts the
// requests that the client canceled.
func longStream(n int, canceled *atomic.Int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		rc := http.NewResponseController(w)
		write := func(s string) bool {
			if _, err := io.WriteString(w, s); err != nil {
				return false
			}
			return rc.Flush() == nil
		}
		write("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-upstream\",\"usage\":{\"input_tokens\":100,\"output_tokens\":1}}}\n\n")
		for range n {
			if r.Context().Err() != nil || !write("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"abcdefgh\"}}\n\n") {
				canceled.Add(1)
				return
			}
		}
		write("event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":" + fmt.Sprint(2*n) + "}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}
}

func TestSessionBudgetCutoff(t *testing.T) {
	var canceled atomic.Int64
	c := &capture{}
	store := limiter.NewMemory()
	e := newEnv(t, longStream(2000, &canceled), setup{
		tenant: "    budgets: [{window: session, limit_usd: 0.0005}]",
		top:    "reservation: {output_tokens: 10}",
		prices: testPrices,
		now:    fixedNow,
		store:  store,
		usage:  c,
	})
	body := `{"model":"claude-test","max_tokens":100000,"stream":true,"messages":[{"role":"user","content":"Write a long text."}]}`
	req := e.request(t.Context(), "/v1/messages", body)
	req.Header.Set("X-Tollgate-Session", "s1")
	resp, out := e.do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, out)
	}
	if !strings.Contains(out, "event: error\n") || !strings.Contains(out, `The budget of the session \"s1\" is used. Tollgate stopped the response.`) {
		t.Errorf("the stream has no budget error event:\n%s", out[max(len(out)-400, 0):])
	}
	// The room is 500 micro-USD. 100 input tokens cost 100, so the stream
	// stops after about 200 output tokens: 100 deltas of 2 tokens. The
	// check runs after each read from the provider, and one read holds up
	// to 32 KiB, so a slow relay can send more deltas before it stops.
	const deltaBytes = len("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"abcdefgh\"}}\n\n")
	maxDeltas := 100 + 32<<10/deltaBytes + 1
	if n := strings.Count(out, "abcdefgh"); n < 100 || n > maxDeltas {
		t.Errorf("the client got %d deltas before the cutoff, want 100 to %d", n, maxDeltas)
	}
	waitFor(t, func() bool { return canceled.Load() == 1 })
	ev := waitEvents(t, c, 1)[0]
	if !ev.BudgetCutoff || !ev.UsageEstimated || ev.CostMicroUSD < 500 || ev.CostMicroUSD > int64(100+4*maxDeltas) {
		t.Errorf("event: cutoff %v, estimated %v, cost %d; want a cutoff from 500 to %d micro-USD", ev.BudgetCutoff, ev.UsageEstimated, ev.CostMicroUSD, 100+4*maxDeltas)
	}
	resp, out = e.do(e.request(t.Context(), "/v1/messages", body))
	if resp.StatusCode != http.StatusOK {
		t.Errorf("a request with a derived session failed: %d %s", resp.StatusCode, out[:min(len(out), 200)])
	}
	req = e.request(t.Context(), "/v1/messages", body)
	req.Header.Set("X-Tollgate-Session", "s1")
	if resp, out = e.do(req); resp.StatusCode != http.StatusPaymentRequired {
		t.Errorf("the next request of the session: status %d, body %s, want 402", resp.StatusCode, out)
	}
}

func TestSessionWarningAndHeader(t *testing.T) {
	e := newEnv(t, fixed(nil, 200, "application/json", anthropicReply), setup{
		tenant: "    budgets: [{window: session, limit_usd: 0.0005, mode: soft}]",
		prices: testPrices,
		now:    fixedNow,
	})
	send := func() response {
		req := e.request(t.Context(), "/v1/messages", anthropicBody)
		req.Header.Set("X-Tollgate-Session", "s1")
		resp, _ := e.do(req)
		return resp
	}
	// Each request costs 200 micro-USD. The first leaves 500 − its
	// reservation; the second takes the spend from 200 to 400, over 80%.
	if got, want := send().Header.Get("X-Tollgate-Session-Remaining-Usd"), fmt.Sprintf("0.%06d", 500-reservation()); strings.TrimRight(got, "0") != strings.TrimRight(want, "0") {
		t.Errorf("remaining = %q, want %q", got, want)
	}
	if strings.Contains(e.logs.String(), "session budget warning") {
		t.Fatal("a warning after 40% of the budget")
	}
	send()
	waitFor(t, func() bool { return strings.Count(e.logs.String(), `"msg":"session budget warning"`) == 1 })
	send()
	if got := send().Header.Get("X-Tollgate-Session-Remaining-Usd"); got != "0.00" {
		t.Errorf("remaining after the budget = %q, want 0.00", got)
	}
	if n := strings.Count(e.logs.String(), `"msg":"session budget warning"`); n != 1 {
		t.Errorf("%d warnings, want 1", n)
	}
}

const cachedRoute = `
  - model: claude-cached
    api: anthropic
    targets: [{provider: anthropic, model: claude-upstream}]
    response_cache: {enabled: true, ttl: 1m}`

func TestResponseCache(t *testing.T) {
	var calls atomic.Int64
	c := &capture{}
	e := newEnv(t, counting(&calls, fixed(nil, 200, "application/json", anthropicReply)), setup{
		extraRoutes: cachedRoute,
		models:      "[claude-test, claude-cached]",
		prices:      testPrices,
		now:         fixedNow,
		usage:       c,
	})
	body := strings.Replace(anthropicBody, "claude-test", "claude-cached", 1)
	r1, b1 := e.post("/v1/messages", body)
	r2, b2 := e.post("/v1/messages", body)
	if r1.Header.Get("X-Tollgate-Cache") != "miss" || r2.Header.Get("X-Tollgate-Cache") != "hit" || b1 != b2 || calls.Load() != 1 {
		t.Errorf("cache headers %q, %q, calls %d, bodies equal %v", r1.Header.Get("X-Tollgate-Cache"), r2.Header.Get("X-Tollgate-Cache"), calls.Load(), b1 == b2)
	}
	if r2.Header.Get("Content-Type") != "application/json" || r2.Header.Get("X-Request-Id") == r1.Header.Get("X-Request-Id") {
		t.Errorf("hit headers: %v", r2.Header)
	}
	e.post("/v1/messages", strings.Replace(body, `"hi"`, `"hello"`, 1))
	r4, _ := e.post("/v1/messages", anthropicBody)
	if calls.Load() != 3 || r4.Header.Get("X-Tollgate-Cache") != "bypass" {
		t.Errorf("calls %d, cache header %q for a route without the cache", calls.Load(), r4.Header.Get("X-Tollgate-Cache"))
	}
	ev := waitEvents(t, c, 4)
	if ev[1].Cache != "hit" || ev[1].CostMicroUSD != 0 || ev[1].Attempts != 0 || ev[0].Cache != "miss" || ev[0].CostMicroUSD != 200 {
		t.Errorf("events: %+v / %+v", ev[0], ev[1])
	}
}

func TestRedaction(t *testing.T) {
	var rec upstreamRecord
	reply := `{"id":"m","type":"message","role":"assistant","content":[{"type":"text","text":"I sent it to [EMAIL_1]."}],"usage":{"input_tokens":10,"output_tokens":5}}`
	c := &capture{}
	e := newEnv(t, fixed(&rec, 200, "application/json", reply, "Content-Length", fmt.Sprint(len(reply))), setup{
		tenant: "    redact: {kinds: [email]}",
		usage:  c,
	})
	body := `{"model":"claude-test","max_tokens":10,"messages":[{"role":"user","content":"Send the report to ann@example.com."}]}`
	resp, out := e.post("/v1/messages", body)
	_, _, _, up := rec.get()
	if strings.Contains(up, "ann@example.com") || !strings.Contains(up, `"Send the report to [EMAIL_1]."`) {
		t.Errorf("the provider got %s", up)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(out, `"I sent it to ann@example.com."`) {
		t.Errorf("status %d, the client got %s", resp.StatusCode, out)
	}
	if ev := waitEvents(t, c, 1); ev[0].Redacted != 1 {
		t.Errorf("redacted = %d, want 1", ev[0].Redacted)
	}

	stream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"To [EMA\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"IL_1].\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":4}}\n\n"
	e = newEnv(t, fixed(nil, 200, "text/event-stream", stream), setup{tenant: "    redact: {kinds: [email]}"})
	_, out = e.post("/v1/messages", strings.Replace(body, `"max_tokens":10`, `"max_tokens":10,"stream":true`, 1))
	if !strings.Contains(out, `"text":"To "`) || !strings.Contains(out, `"text":"ann@example.com."`) || strings.Contains(out, "EMA") {
		t.Errorf("the client got:\n%s", out)
	}
}

func configTarget(p, m string) config.Target { return config.Target{Provider: p, Model: m} }

// Distinct original prompts must stay distinct after both mask to EMAIL_1.
func TestRedactionPreservesSessionIdentity(t *testing.T) {
	c := &capture{}
	e := newEnv(t, fixed(nil, 200, "application/json", anthropicReply), setup{
		tenant: "    redact: {kinds: [email]}", usage: c,
	})
	for _, email := range []string{"ann@example.com", "bob@example.com"} {
		body := fmt.Sprintf(`{"model":"claude-test","max_tokens":10,"messages":[{"role":"user","content":"Contact %s"}]}`, email)
		if resp, out := e.post("/v1/messages", body); resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d: %s", resp.StatusCode, out)
		}
	}
	ev := waitEvents(t, c, 2)
	if ev[0].SessionID == ev[1].SessionID {
		t.Fatal("redaction merged two distinct sessions")
	}
}
