package server

import (
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ykakade/tollgate/internal/budget"
	"github.com/ykakade/tollgate/internal/limiter"
	"github.com/ykakade/tollgate/internal/preflight"
)

// clock is a test clock that all copies share.
type clock struct{ ns atomic.Int64 }

func (c *clock) now() time.Time       { return time.Unix(0, c.ns.Load()).UTC() }
func (c *clock) set(t time.Time)      { c.ns.Store(t.UnixNano()) }
func (c *clock) add(d time.Duration)  { c.ns.Add(int64(d)) }
func newClock(start time.Time) *clock { c := &clock{}; c.set(start); return c }

// randomMinute returns the start of a random minute from 2100 to 2195. The Redis
// counters of a test run then do not mix with those of an earlier run.
func randomMinute(t *testing.T) time.Time {
	t.Helper()
	n, err := rand.Int(rand.Reader, big.NewInt(50_000_000))
	if err != nil {
		t.Fatal(err)
	}
	return time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(n.Int64()) * time.Minute)
}

// sharedStores returns the stores that the copies of a cluster test share:
// one memory store, and Redis if TOLLGATE_TEST_REDIS_URL is set.
func sharedStores(t *testing.T) map[string]func(t *testing.T) limiter.Store {
	stores := map[string]func(t *testing.T) limiter.Store{
		"memory": func(*testing.T) limiter.Store { return limiter.NewMemory() },
	}
	if u := os.Getenv("TOLLGATE_TEST_REDIS_URL"); u != "" {
		stores["redis"] = func(t *testing.T) limiter.Store {
			s, err := limiter.NewRedis(u)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			return s
		}
	} else {
		t.Log("TOLLGATE_TEST_REDIS_URL is not set: the test uses only the memory store")
	}
	return stores
}

// copies starts n Tollgate copies that share one store and one clock.
func copies(t *testing.T, n int, upstream http.Handler, s setup) []*env {
	t.Helper()
	out := make([]*env, n)
	// These tests measure shared counters, not upstream latency. Allow
	// room for the burst under the race detector on a busy CI host.
	if s.timeouts == "" {
		s.timeouts = "{connect: 10s, first_token: 10s, idle: 10s, total: 60s}"
	}
	for i := range out {
		out[i] = newEnv(t, upstream, s)
	}
	return out
}

// statusOf sends a request and returns the status.
func statusOf(t *testing.T, e *env, body string) int {
	req := e.request(t.Context(), "/v1/messages", body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Error(err)
		return 0
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	e.done.wait(t, resp.Header.Get("X-Request-Id"))
	return resp.StatusCode
}

// TestThreeCopiesShareOneLimit is the cluster test of the brief: three
// Tollgate copies with one shared request limit. docs/limits.md gives the
// tolerance that it checks.
func TestThreeCopiesShareOneLimit(t *testing.T) {
	const limit = 50
	for name, newStore := range sharedStores(t) {
		t.Run(name, func(t *testing.T) {
			clk := newClock(randomMinute(t).Add(30 * time.Second))
			cs := copies(t, 3, fixed(nil, 200, "application/json", anthropicReply), setup{
				tenant: fmt.Sprintf("    limits: {requests_per_minute: %d}", limit),
				store:  newStore(t),
				now:    clk.now,
			})

			// A burst in one minute: 180 parallel requests, 60 to each
			// copy. The store admits exactly the limit.
			var ok atomic.Int64
			var wg sync.WaitGroup
			for i := range 180 {
				wg.Go(func() {
					if statusOf(t, cs[i%3], anthropicBody) == 200 {
						ok.Add(1)
					}
				})
			}
			wg.Wait()
			if got := ok.Load(); got != limit {
				t.Errorf("burst: the copies admitted %d requests, want exactly %d", got, limit)
			}

			// Steady overload for 4 minutes: each copy gets one request
			// each second, 3 requests each second in total.
			clk.set(clk.now().Truncate(time.Minute).Add(time.Minute))
			start := clk.now()
			var admitted []time.Duration
			for range 240 {
				for i := range 3 {
					if statusOf(t, cs[i], anthropicBody) == 200 {
						admitted = append(admitted, clk.now().Sub(start))
					}
				}
				clk.add(time.Second)
			}
			perMinute := make([]int, 4)
			for _, a := range admitted {
				perMinute[int(a/time.Minute)]++
			}
			maxRolling := 0
			for from := time.Duration(0); from+time.Minute <= 4*time.Minute; from += time.Second {
				n := 0
				for _, a := range admitted {
					if a >= from && a < from+time.Minute {
						n++
					}
				}
				maxRolling = max(maxRolling, n)
			}
			t.Logf("steady overload: admitted per calendar minute %v, largest count in a 60 s span %d", perMinute, maxRolling)
			for m, n := range perMinute {
				if n > limit {
					t.Errorf("minute %d: %d requests admitted, more than the limit %d", m, n, limit)
				}
			}
			// The first full minute after the burst minute and the later
			// minutes admit at least 90% of the limit.
			for m, n := range perMinute[1:] {
				if n < limit*9/10 {
					t.Errorf("minute %d: %d requests admitted, want %d or more", m+1, n, limit*9/10)
				}
			}
			if maxRolling > 2*limit {
				t.Errorf("a 60 s span has %d admitted requests, more than 2 × the limit", maxRolling)
			}
		})
	}
}

// TestWorstCaseOverspend checks the bound of ADR 0018: the spend of a hard
// budget can go over the limit by at most the sum, over the requests that
// are open at the same time, of (max_tokens − reserved output tokens) ×
// output price, when the input estimate is not too low. The test runs
// three copies with one shared budget, and every response uses all of
// max_tokens.
func TestWorstCaseOverspend(t *testing.T) {
	const (
		limitMicro  = 10_000 // 0.01 USD
		reserveOut  = 100
		maxTokens   = 1000
		outputPrice = 2 // micro-USD for each output token
		requests    = 120
	)
	body := fmt.Sprintf(`{"model":"claude-test","max_tokens":%d,"messages":[{"role":"user","content":"hi"}]}`, maxTokens)
	upBody := strings.Replace(body, "claude-test", "claude-upstream", 1)
	inputTokens := preflight.EstimateInputTokens(len(upBody))
	reserve := inputTokens + reserveOut*outputPrice
	actual := inputTokens + maxTokens*outputPrice

	for name, newStore := range sharedStores(t) {
		t.Run(name, func(t *testing.T) {
			now := randomMinute(t)
			store := newStore(t)
			// The provider holds each response until all requests have an
			// answer or wait at the provider. Then all admitted requests
			// are open at the same time: the worst case.
			gate := make(chan struct{})
			var arrived atomic.Int64
			upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				arrived.Add(1)
				<-gate
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"id":"m","usage":{"input_tokens":%d,"output_tokens":%d}}`, inputTokens, maxTokens)
			})
			cs := copies(t, 3, upstream, setup{
				tenant: "    budgets: [{window: month, limit_usd: 0.01}]",
				top:    fmt.Sprintf("reservation: {output_tokens: %d}", reserveOut),
				prices: testPrices,
				store:  store,
				now:    func() time.Time { return now },
			})
			var denied, ok atomic.Int64
			var wg sync.WaitGroup
			for i := range requests {
				wg.Go(func() {
					switch code := statusOf(t, cs[i%3], body); code {
					case 200:
						ok.Add(1)
					case 402:
						denied.Add(1)
					default:
						t.Errorf("status %d", code)
					}
				})
			}
			waitFor(t, func() bool { return arrived.Load()+denied.Load() == requests })
			close(gate)
			wg.Wait()

			open := ok.Load()
			if want := limitMicro / reserve; open != want {
				t.Errorf("%d requests admitted, want %d (limit / reservation)", open, want)
			}
			spend := spendOf(t, store, now)
			bound := int64(limitMicro) + open*(maxTokens-reserveOut)*outputPrice
			t.Logf("%d open requests, each reserved %d and cost %d micro-USD: spend %d, limit %d, bound %d", open, reserve, actual, spend, limitMicro, bound)
			if spend != open*actual {
				t.Errorf("spend = %d, want %d × %d", spend, open, actual)
			}
			if spend > bound {
				t.Errorf("spend %d is more than the documented bound %d", spend, bound)
			}
			if spend <= limitMicro {
				t.Errorf("spend %d is not over the limit: the test does not show the worst case", spend)
			}
			// After the overspend, the budget stops all requests.
			if code := statusOf(t, cs[1], body); code != 402 {
				t.Errorf("status after the overspend = %d, want 402", code)
			}
		})
	}
}

// spendOf returns the month spend of the tenant "personal" in store.
func spendOf(t *testing.T, store limiter.Store, now time.Time) int64 {
	t.Helper()
	if m, ok := store.(*limiter.Memory); ok {
		return m.Value(budget.MonthKey("personal", now), now)
	}
	// For Redis, read the spend with an Admit call that reserves nothing.
	d, err := store.Admit(t.Context(), &limiter.Request{Now: now, Budgets: []limiter.Budget{
		{Key: budget.MonthKey("personal", now), Limit: 1 << 50, TTL: time.Hour},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return d.Spend[0]
}
