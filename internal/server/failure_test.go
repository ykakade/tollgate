package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/ykakade/tollgate/internal/auth"
	"github.com/ykakade/tollgate/internal/budget"
	"github.com/ykakade/tollgate/internal/config"
	"github.com/ykakade/tollgate/internal/fakeprovider"
	"github.com/ykakade/tollgate/internal/limiter"
	"github.com/ykakade/tollgate/internal/preflight"
	"github.com/ykakade/tollgate/internal/pricing"
	"github.com/ykakade/tollgate/internal/provider"
	"github.com/ykakade/tollgate/internal/provider/anthropic"
	"github.com/ykakade/tollgate/internal/router"
)

// failureEnv uses two independent fake providers, so call counts prove
// which targets Tollgate tried. All requests wait for settlement.
func failureEnv(t *testing.T, api, kind, scenario, extra string, now func() time.Time) (*env, *fakeprovider.Server, *fakeprovider.Server) {
	t.Helper()
	return failureEnvWith(t, api, kind, scenario, extra, now, nil)
}

// failureEnvWith is failureEnv with a function that can change the server
// options.
func failureEnvWith(t *testing.T, api, kind, scenario, extra string, now func() time.Time, edit func(*Options)) (*env, *fakeprovider.Server, *fakeprovider.Server) {
	t.Helper()
	a := fakeprovider.New(fakeprovider.Config{Scenario: scenario})
	b := fakeprovider.New(fakeprovider.Config{})
	first := httptest.NewServer(a.Handler())
	second := httptest.NewServer(b.Handler())
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)
	key, err := auth.NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	// The fallback tests send the same body again and again. Sticky routing
	// would keep those requests on the fallback target, so it is off unless
	// the test sets sessions.
	if !strings.Contains(extra, "sessions:") {
		extra += "\nsessions: {sticky: false}"
	}
	vertexFields := ""
	if kind == config.KindVertex {
		vertexFields = "\n    project: test-project\n    region: us-east5"
	}
	yaml := fmt.Sprintf(`version: 1
providers:
  first:
    kind: %s
    base_url: %s%s
  second:
    kind: %s
    base_url: %s
routes:
  - model: test
    api: %s
    targets: [{provider: first, model: upstream-a}, {provider: second, model: upstream-b}]
    timeouts: {connect: 1s, first_token: 100ms, idle: 100ms, total: 3s}
tenants:
  - id: personal
    keys: [{id: test, hash: %s}]
    models: [test]
%s
`, kind, first.URL, vertexFields, api, second.URL, api, auth.Hash(key), extra)
	cfg, err := config.Parse("failure.yaml", []byte(yaml), config.Env{})
	if err != nil {
		t.Fatal(err)
	}
	adapters, err := NewAdapters(t.Context(), cfg.Providers, AdapterOptions{GoogleTokens: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"})})
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	opts := Options{Config: cfg, Adapters: adapters, Logger: slog.New(slog.NewJSONHandler(logs, nil)), Now: now}
	if edit != nil {
		edit(&opts)
	}
	srv, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	done := &handlerDone{}
	gateway := httptest.NewServer(done.wrap(srv.Handler()))
	t.Cleanup(gateway.Close)
	return &env{t: t, srv: srv, url: gateway.URL, key: key, logs: logs, upstream: first, done: done}, a, b
}

const failureBody = `{"model":"test","stream":true,"max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`

func failurePath(api string) string {
	if api == string(config.APIAnthropic) {
		return "/v1/messages"
	}
	return "/v1/chat/completions"
}

// TestFailureScenarios is the M3 acceptance suite. The shared-store and
// shutdown cases use the existing integration tests below.
func TestFailureScenarios(t *testing.T) {
	for _, api := range []string{"anthropic", "openai"} {
		t.Run(api, func(t *testing.T) {
			for _, tt := range []struct {
				name, scenario string
				calls          uint64
			}{
				{"429_retry_after", "status:429:7", 1},
				{"529_overloaded", "status:529", 1},
				{"500_retry_once", "status:500", 2},
				{"hang_before_headers", "hang-before-headers", 1},
			} {
				t.Run(tt.name, func(t *testing.T) {
					clock := newClock(testNow)
					e, a, b := failureEnv(t, api, api, tt.scenario, "", clock.now)
					resp, body := e.post(failurePath(api), failureBody)
					if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Tollgate-Provider") != "second" || a.Requests() != tt.calls || b.Requests() != 1 || resp.Header.Get("X-Tollgate-Attempts") != fmt.Sprint(tt.calls+1) {
						t.Fatalf("response %d %v %s, calls %d/%d", resp.StatusCode, resp.Header, body, a.Requests(), b.Requests())
					}
					if tt.name == "429_retry_after" {
						for _, d := range []time.Duration{0, 6 * time.Second} {
							clock.set(testNow.Add(d))
							resp, _ = e.post(failurePath(api), failureBody)
							if a.Requests() != 1 || resp.Header.Get("X-Tollgate-Attempts") != "1" {
								t.Fatal("target retried before retry-after")
							}
						}
						clock.set(testNow.Add(7 * time.Second))
						if err := a.SetScenario("ok"); err != nil {
							t.Fatal(err)
						}
						resp, _ = e.post(failurePath(api), failureBody)
						if a.Requests() != 2 || resp.Header.Get("X-Tollgate-Provider") != "first" {
							t.Fatal("target did not recover after retry-after")
						}
					}
				})
			}
			for _, scenario := range []string{"hang-after:2-tokens", "reset-after:2-tokens", "malformed-after:2-tokens"} {
				t.Run(scenario, func(t *testing.T) {
					e, a, b := failureEnv(t, api, api, scenario, "breaker: {min_requests: 1}", nil)
					resp, body := e.post(failurePath(api), failureBody)
					if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"error"`) || a.Requests() != 1 || b.Requests() != 0 {
						t.Fatalf("status %d, calls %d/%d, body %s", resp.StatusCode, a.Requests(), b.Requests(), body)
					}
					line := e.logLine(resp.Header.Get("X-Request-Id"))
					if line["error"] == nil || line["input_tokens"] == nil {
						t.Fatalf("no partial usage/error: %v", line)
					}
					key := router.Key{Provider: "first", Model: "upstream-a"}
					if e.srv.router.State(key) != router.Open {
						t.Fatal("stream failure did not open the breaker")
					}
				})
			}
			t.Run("no_usage", func(t *testing.T) {
				e, _, b := failureEnv(t, api, api, "no-usage", "", nil)
				resp, _ := e.post(failurePath(api), failureBody)
				line := e.logLine(resp.Header.Get("X-Request-Id"))
				if resp.StatusCode != http.StatusOK || line["usage_estimated"] != true || line["input_tokens"].(float64) <= 0 || b.Requests() != 0 {
					t.Fatalf("log %v, calls %d", line, b.Requests())
				}
			})
			t.Run("all_breakers_open", func(t *testing.T) {
				clock := newClock(testNow)
				e, a, b := failureEnv(t, api, api, "status:529", "breaker: {min_requests: 1, open_for: 30s}", clock.now)
				if err := b.SetScenario("status:529"); err != nil {
					t.Fatal(err)
				}
				e.post(failurePath(api), failureBody)
				resp, body := e.post(failurePath(api), failureBody)
				if resp.StatusCode != http.StatusBadGateway || a.Requests() != 1 || b.Requests() != 1 || !strings.Contains(body, "first/upstream-a") || !strings.Contains(body, "second/upstream-b") || resp.Header.Get("Retry-After") != "30" {
					t.Fatalf("status %d, body %s, calls %d/%d", resp.StatusCode, body, a.Requests(), b.Requests())
				}
				clock.set(testNow.Add(30 * time.Second))
				if err := a.SetScenario("ok"); err != nil {
					t.Fatal(err)
				}
				resp, _ = e.post(failurePath(api), failureBody)
				if resp.StatusCode != http.StatusOK || a.Requests() != 2 || b.Requests() != 1 {
					t.Fatal("breaker did not recover")
				}
			})
		})
	}
	t.Run("client_disconnect", TestClientDisconnectCancelsUpstream)
	t.Run("slow_client", testSlowClient)
	t.Run("three_copies", TestThreeCopiesShareOneLimit)
	t.Run("redis_down", TestStoreDown)
}

// pipeWriter makes every response write block, without TCP send-buffer
// size or process memory measurements that vary between machines.
type pipeWriter struct {
	net.Conn
	header http.Header
}

func (w *pipeWriter) Header() http.Header { return w.header }
func (*pipeWriter) WriteHeader(int)       {}
func (*pipeWriter) Flush()                {}

func testSlowClient(t *testing.T) {
	e, a, b := failureEnv(t, "anthropic", "anthropic", "hang-after:2-tokens", "", nil)
	sender, receiver := net.Pipe()
	defer sender.Close()
	defer receiver.Close()
	w := &pipeWriter{Conn: sender, header: make(http.Header)}
	done := make(chan struct{})
	go func() { e.srv.Handler().ServeHTTP(w, e.request(t.Context(), "/v1/messages", failureBody)); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("slow client did not reach the write deadline")
	}
	if a.Requests() != 1 || b.Requests() != 0 {
		t.Fatalf("slow client caused a fallback: %d/%d", a.Requests(), b.Requests())
	}
	line := e.logLine(w.header.Get("X-Request-Id"))
	if !strings.Contains(fmt.Sprint(line["error"]), "client write failed") {
		t.Fatalf("log = %v", line)
	}
}

func TestFallbackRules(t *testing.T) {
	t.Run("other_4xx", func(t *testing.T) {
		e, a, b := failureEnv(t, "anthropic", "anthropic", "status:400", "", nil)
		resp, body := e.post("/v1/messages", failureBody)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "on purpose") || a.Requests() != 1 || b.Requests() != 0 {
			t.Fatalf("status %d, body %s, calls %d/%d", resp.StatusCode, body, a.Requests(), b.Requests())
		}
	})
	t.Run("last_status_unchanged", func(t *testing.T) {
		e, _, b := failureEnv(t, "anthropic", "anthropic", "status:529", "", nil)
		if err := b.SetScenario("status:429:9"); err != nil {
			t.Fatal(err)
		}
		resp, body := e.post("/v1/messages", failureBody)
		if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "9" || !strings.Contains(body, "429 on purpose") {
			t.Fatalf("status %d, headers %v, body %s", resp.StatusCode, resp.Header, body)
		}
	})
	t.Run("refused_connection", func(t *testing.T) {
		e, _, b := failureEnv(t, "anthropic", "anthropic", "ok", "", nil)
		e.upstream.Close()
		resp, _ := e.post("/v1/messages", failureBody)
		if resp.StatusCode != http.StatusOK || b.Requests() != 1 {
			t.Fatal("refused connection did not fall back")
		}
	})
	t.Run("last_timeout_lists_targets", func(t *testing.T) {
		e, _, b := failureEnv(t, "anthropic", "anthropic", "hang-before-headers", "", nil)
		if err := b.SetScenario("hang-before-headers"); err != nil {
			t.Fatal(err)
		}
		resp, body := e.post("/v1/messages", failureBody)
		if resp.StatusCode != http.StatusBadGateway || !strings.Contains(body, "first/upstream-a") || !strings.Contains(body, "second/upstream-b") {
			t.Fatalf("status %d, body %s", resp.StatusCode, body)
		}
	})
	t.Run("retry_budget", func(t *testing.T) {
		e, a, b := failureEnv(t, "anthropic", "anthropic", "status:529", "retry: {budget_ratio: 0}", fixedNow)
		for range router.BudgetFloor {
			e.post("/v1/messages", failureBody)
		}
		resp, _ := e.post("/v1/messages", failureBody)
		if resp.StatusCode != 529 || a.Requests() != router.BudgetFloor+1 || b.Requests() != router.BudgetFloor {
			t.Fatalf("status %d calls %d/%d", resp.StatusCode, a.Requests(), b.Requests())
		}
	})
	t.Run("max_attempts", func(t *testing.T) {
		e, a, b := failureEnv(t, "anthropic", "anthropic", "status:500", "retry: {max_attempts: 1}", nil)
		resp, _ := e.post("/v1/messages", failureBody)
		if resp.StatusCode != http.StatusInternalServerError || a.Requests() != 1 || b.Requests() != 0 {
			t.Fatal("max_attempts was not applied")
		}
	})
}

func TestCloudAndLocalTargets(t *testing.T) {
	for _, tt := range []struct{ api, kind string }{{"anthropic", "vertex"}, {"openai", "ollama"}} {
		t.Run(tt.kind, func(t *testing.T) {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprint(stream), func(t *testing.T) {
					e, a, b := failureEnv(t, tt.api, tt.kind, "ok", "", nil)
					body := strings.Replace(failureBody, `"stream":true`, fmt.Sprintf(`"stream":%t`, stream), 1)
					resp, got := e.post(failurePath(tt.api), body)
					if resp.StatusCode != http.StatusOK || a.Requests() != 1 || b.Requests() != 0 || !strings.Contains(got, "Hello") {
						t.Fatalf("status %d, body %s, calls %d/%d", resp.StatusCode, got, a.Requests(), b.Requests())
					}
					line := e.logLine(resp.Header.Get("X-Request-Id"))
					if line["usage_estimated"] != false {
						t.Fatalf("no usage: %v", line)
					}
				})
			}
		})
	}
	t.Run("count_tokens_skips_vertex", func(t *testing.T) {
		e, a, b := failureEnv(t, "anthropic", "vertex", "ok", "", nil)
		resp, _ := e.post(countTokensPath, failureBody)
		if resp.StatusCode != http.StatusNotFound || resp.Header.Get("X-Tollgate-Provider") != "second" || a.Requests() != 0 || b.Requests() != 0 {
			t.Fatal("count_tokens went to Vertex")
		}
	})
}

// reserveSpy records the reservation while the request is in flight.
type reserveSpy struct {
	limiter.Store
	amount atomic.Int64
}

func (s *reserveSpy) Admit(ctx context.Context, r *limiter.Request) (limiter.Decision, error) {
	if len(r.Budgets) > 0 {
		s.amount.Store(r.Budgets[0].Amount)
	}
	return s.Store.Admit(ctx, r)
}

func TestFallbackBudgetSettlement(t *testing.T) {
	for _, expensive := range []string{"first", "second"} {
		t.Run(expensive+"_expensive", func(t *testing.T) {
			e, _, _ := failureEnv(t, "anthropic", "anthropic", "status:529", "", fixedNow)
			prices := `
- provider: first
  model: upstream-a
  effective_from: 2026-01-01
  micro_usd_per_mtok: {input: 10000000, output: 20000000}
- provider: second
  model: upstream-b
  effective_from: 2026-01-01
  micro_usd_per_mtok: {input: 1000000, output: 2000000}
`
			if expensive == "second" {
				prices = strings.NewReplacer("provider: first", "provider: second", "provider: second", "provider: first", "model: upstream-a", "model: upstream-b", "model: upstream-b", "model: upstream-a").Replace(prices)
			}
			table, errs := pricing.Parse([]byte(prices))
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			e.srv.prices = table
			e.srv.tenants["personal"].budgets = []config.Budget{{Window: config.WindowMonth, Mode: config.ModeHard, LimitUSD: 1000000}}
			spy := &reserveSpy{Store: limiter.NewMemory()}
			e.srv.store = spy
			resp, _ := e.post("/v1/messages", failureBody)
			line := e.logLine(resp.Header.Get("X-Request-Id"))
			cost := int64(line["cost_micro_usd"].(float64))
			want := int64(line["input_tokens"].(float64)) + 2*int64(line["output_tokens"].(float64))
			if expensive == "second" {
				want *= 10
			}
			// Both model names have the same size. The high-price target needs
			// ten micro-USD per estimated input token and twenty per output token.
			upBody := strings.Replace(failureBody, `"test"`, `"upstream-a"`, 1)
			reserved := 10*preflight.EstimateInputTokens(len(upBody)) + 20*8
			if cost != want || cost <= 0 || spy.amount.Load() != reserved {
				t.Fatalf("cost %d want %d reservation %d want %d", cost, want, spy.amount.Load(), reserved)
			}
			d, err := spy.Admit(t.Context(), &limiter.Request{Now: testNow, Budgets: []limiter.Budget{{Key: budget.MonthKey("personal", testNow), Limit: 1000000}}})
			if err != nil || d.Spend[0] != cost {
				t.Fatalf("settled spend %v, err %v, want %d", d.Spend, err, cost)
			}
		})
	}
}

func TestRetryAfter(t *testing.T) {
	now := testNow
	for _, tt := range []struct {
		value string
		want  time.Duration
	}{
		{"7", 7 * time.Second},
		{now.Add(9 * time.Second).Format(http.TimeFormat), 9 * time.Second},
		{"-1", 0}, {"bad", 0}, {"", 0},
		{"9223372036854775807", maxCoolDown},
		{"9223372037", maxCoolDown},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0},
	} {
		if got := retryAfter(http.Header{"Retry-After": {tt.value}}, now); got != tt.want {
			t.Errorf("retryAfter(%q) = %s, want %s", tt.value, got, tt.want)
		}
	}
}

func TestFallbackAfterHeaders(t *testing.T) {
	for _, status := range []int{http.StatusOK, router.StatusOverloaded} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			e, _, b := failureEnv(t, "anthropic", "anthropic", "ok", "", nil)
			canceled := make(chan struct{})
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(status)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(canceled)
			}))
			defer up.Close()
			adapter, err := anthropic.New(up.URL, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			e.srv.routes[routeKey{config.APIAnthropic, "test"}].all.targets[0].adapter = adapter
			resp, body := e.post("/v1/messages", failureBody)
			if resp.StatusCode != http.StatusOK || b.Requests() != 1 || resp.Header.Get("X-Tollgate-Provider") != "second" {
				t.Fatalf("status %d, body %s, calls %d", resp.StatusCode, body, b.Requests())
			}
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("discarded attempt was not canceled")
			}
		})
	}
}

// waitingAdapter simulates a connection attempt that never becomes ready.
type waitingAdapter struct{}

func (waitingAdapter) Send(ctx context.Context, _ *provider.Request) (*http.Response, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestConnectTimeoutFallsBack(t *testing.T) {
	e, _, b := failureEnv(t, "anthropic", "anthropic", "ok", "", nil)
	rt := e.srv.routes[routeKey{config.APIAnthropic, "test"}]
	rt.timeouts.Connect = 50 * time.Millisecond
	rt.all.targets[0].adapter = waitingAdapter{}
	resp, body := e.post("/v1/messages", failureBody)
	if resp.StatusCode != http.StatusOK || b.Requests() != 1 {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(e.logs.String(), "connect timeout") {
		t.Fatal("connect timeout did not cause fallback")
	}
}
