package main

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tollgate "github.com/ykakade/tollgate"
	"github.com/ykakade/tollgate/internal/auth"
	"github.com/ykakade/tollgate/internal/fakeprovider"
	"github.com/ykakade/tollgate/internal/pricing"
	"github.com/ykakade/tollgate/internal/provider"
	"github.com/ykakade/tollgate/internal/usage"
)

// The demo runs Tollgate with in-process fake providers, a usage file with
// synthetic history, and live requests from agent-like sessions. It needs
// no API key, no Node.js, and no Docker.

// demoSeed makes the synthetic history the same in each run.
const demoSeed = 2026

// demoHistory is the length of the synthetic history.
const demoHistory = 7 * 24 * time.Hour

// demoReadyTimeout is the longest time to wait for the gateway to start.
const demoReadyTimeout = 10 * time.Second

// demoSonnet and demoHaiku are the models of the demo routes.
const (
	demoSonnet = "claude-sonnet-5-5"
	demoHaiku  = "claude-haiku-5-5"
)

// demoKeyIDs are the key IDs of each demo tenant. The first key of each
// tenant is the live key that the demo traffic uses.
var demoKeyIDs = map[string][]string{
	"platform": {"ana-laptop", "raj-laptop", "ci"},
	"research": {"notebook"},
}

func runDemo(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlags("demo", "demo [-listen addr] [-admin-listen addr] [-dir path]", stderr)
	listen := fs.String("listen", "127.0.0.1:8080", "Address of the gateway.")
	adminListen := fs.String("admin-listen", "127.0.0.1:9090", "Address of the dashboard and the admin API.")
	dirFlag := fs.String("dir", "", "Directory for the demo configuration, usage file, and log. By default, the demo makes a temporary directory and removes it at exit.")
	if code := parseFlags(fs, args, stderr); code >= 0 {
		return code
	}

	dir := *dirFlag
	if dir == "" {
		d, err := os.MkdirTemp("", "tollgate-demo-")
		if err != nil {
			fmt.Fprintf(stderr, "Tollgate cannot make a directory for the demo: %v\n", err)
			return exitError
		}
		defer os.RemoveAll(d)
		dir = d
	} else if err := os.MkdirAll(dir, 0o750); err != nil {
		fmt.Fprintf(stderr, "Tollgate cannot make the demo directory %s: %v\n", dir, err)
		return exitError
	}

	fakes, err := startDemoProviders(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "Tollgate cannot start the fake providers: %v\n", err)
		return exitError
	}
	defer fakes.close()

	prices, err := demoPrices()
	if err != nil {
		fmt.Fprintf(stderr, "Tollgate cannot read its price table: %v\n", err)
		return exitError
	}
	usagePath := filepath.Join(dir, "usage.jsonl")
	events := demoEvents(time.Now(), prices)
	if err := writeDemoUsage(usagePath, events); err != nil {
		fmt.Fprintf(stderr, "Tollgate cannot write the demo usage file: %v\n", err)
		return exitError
	}

	keys := map[string]string{}
	for tenant := range demoKeyIDs {
		k, err := auth.NewRandomKey()
		if err != nil {
			fmt.Fprintf(stderr, "Tollgate cannot make a demo key: %v\n", err)
			return exitError
		}
		keys[tenant] = k
	}
	cfgPath := filepath.Join(dir, "tollgate.yaml")
	cfg := demoConfig(*listen, *adminListen, usagePath, fakes.urls, keys)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		fmt.Fprintf(stderr, "Tollgate cannot write the demo configuration: %v\n", err)
		return exitError
	}
	logPath := filepath.Join(dir, "tollgate.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // The path is in the demo directory.
	if err != nil {
		fmt.Fprintf(stderr, "Tollgate cannot open the demo log %s: %v\n", logPath, err)
		return exitError
	}
	defer logFile.Close()

	// The gateway logs go to the log file, so that the terminal shows only
	// the demo instructions.
	served := make(chan int, 1)
	go func() { served <- runServe(ctx, []string{"-config", cfgPath}, logFile) }()
	base := "http://" + *listen
	if code, ok := waitReady(ctx, base, served); !ok {
		if log, err := os.ReadFile(logPath); err == nil { //nolint:gosec // The path is in the demo directory.
			_, _ = stderr.Write(log)
		}
		fmt.Fprintf(stderr, "The demo gateway did not start. To use other ports, set -listen and -admin-listen.\n")
		if code == exitOK {
			code = exitError
		}
		return code
	}

	fmt.Fprint(stdout, demoBanner(base, "http://"+*adminListen, dir, *dirFlag == "", len(events), keys["platform"]))

	trafficCtx, stopTraffic := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { demoTraffic(trafficCtx, base, keys) })
	code := <-served
	stopTraffic()
	wg.Wait()
	return code
}

// demoProviders are the fake providers of the demo, each on its own port.
type demoProviders struct {
	urls    map[string]string
	servers []*http.Server
}

func (d *demoProviders) close() {
	for _, s := range d.servers {
		_ = s.Close()
	}
}

// startDemoProviders starts two fake providers with a simulated prompt
// cache and one that always returns 529, so that the demo shows failover
// and an open circuit breaker.
func startDemoProviders(ctx context.Context) (*demoProviders, error) {
	quiet := slog.New(slog.DiscardHandler)
	specs := []struct {
		name string
		cfg  fakeprovider.Config
		scen string
	}{
		{"fake-primary", fakeprovider.Config{PromptCache: 5 * time.Minute, FirstTokenDelay: 350 * time.Millisecond, TokenInterval: 8 * time.Millisecond}, ""},
		{"fake-backup", fakeprovider.Config{PromptCache: 5 * time.Minute, FirstTokenDelay: 700 * time.Millisecond, TokenInterval: 12 * time.Millisecond}, ""},
		{"fake-flaky", fakeprovider.Config{}, "status:529"},
	}
	d := &demoProviders{urls: map[string]string{}}
	for _, s := range specs {
		s.cfg.Logger = quiet
		fp := fakeprovider.New(s.cfg)
		if err := fp.SetScenario(s.scen); err != nil {
			d.close()
			return nil, err
		}
		ln, err := new(net.ListenConfig).Listen(ctx, "tcp", "127.0.0.1:0")
		if err != nil {
			d.close()
			return nil, err
		}
		srv := &http.Server{Handler: fp.Handler(), ReadHeaderTimeout: 10 * time.Second}
		go func() { _ = srv.Serve(ln) }()
		d.servers = append(d.servers, srv)
		d.urls[s.name] = "http://" + ln.Addr().String()
	}
	return d, nil
}

// waitReady waits until the gateway at base is ready. If the gateway
// stops first, waitReady returns its exit code and false.
func waitReady(ctx context.Context, base string, served <-chan int) (int, bool) {
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(demoReadyTimeout)
	for time.Now().Before(deadline) {
		select {
		case code := <-served:
			return code, false
		case <-ctx.Done():
			return exitError, false
		case <-time.After(50 * time.Millisecond):
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/readyz", nil)
		if err != nil {
			return exitError, false
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return exitOK, true
		}
	}
	return exitError, false
}

func demoBanner(base, admin, dir string, temp bool, seeded int, key string) string {
	removed := ""
	if temp {
		removed = " The demo removes it at exit."
	}
	return fmt.Sprintf(`Tollgate demo is running with fake providers. No API key is needed.

  Dashboard:  %s
  Gateway:    %s
  Files:      %s (configuration, usage file, and log).%s

The usage file has %d synthetic events from the last 7 days. Agent-like
sessions send live requests every few seconds. The first target of the
%s route always returns 529, so you can watch failover and the
circuit breaker on the dashboard.

Send a request yourself:

  curl %s/v1/messages \
    -H 'x-api-key: %s' \
    -H 'anthropic-version: 2023-06-01' -H 'content-type: application/json' \
    -H 'x-tollgate-session: my-first-session' \
    -d '{"model":"%s","max_tokens":64,"messages":[{"role":"user","content":"Hello"}]}'

Stop the demo with Ctrl-C.
`, admin, base, dir, removed, seeded, demoHaiku, base, key, demoSonnet)
}

// demoConfig returns the configuration of the demo gateway. Only the first
// key of each tenant has a known key. The others have placeholder hashes,
// so that the history of their key IDs shows on the dashboard.
func demoConfig(listen, adminListen, usagePath string, urls, keys map[string]string) string {
	return fmt.Sprintf(`# Tollgate demo configuration. All data is synthetic.
version: 1
server:
  listen: %q
  admin_listen: %q
usage:
  path: %q
breaker:
  min_requests: 5
  open_for: 30s
providers:
  fake-primary: {kind: anthropic, base_url: %q}
  fake-backup: {kind: anthropic, base_url: %q}
  fake-flaky: {kind: anthropic, base_url: %q}
routes:
  - model: %s
    api: anthropic
    targets: [{provider: fake-primary}, {provider: fake-backup}]
  - model: %s
    api: anthropic
    targets: [{provider: fake-flaky}, {provider: fake-backup}]
    response_cache: {enabled: true, ttl: 10m}
tenants:
  - id: platform
    keys:
      - {id: ana-laptop, hash: %q}
      - {id: raj-laptop, hash: "sha256:%s"}
      - {id: ci, hash: "sha256:%s"}
    models: [%s, %s]
    limits: {requests_per_minute: 600}
    budgets:
      - {window: month, limit_usd: 150}
      - {window: session, limit_usd: 2, mode: hard}
    redact: {kinds: [email, credit_card]}
  - id: research
    keys: [{id: notebook, hash: %q}]
    models: [%s, %s]
    budgets:
      - {window: month, limit_usd: 40}
`, listen, adminListen, usagePath,
		urls["fake-primary"], urls["fake-backup"], urls["fake-flaky"],
		demoSonnet, demoHaiku,
		auth.Hash(keys["platform"]), strings.Repeat("1", 64), strings.Repeat("2", 64),
		demoSonnet, demoHaiku,
		auth.Hash(keys["research"]),
		demoSonnet, demoHaiku)
}

// demoPrices returns the price entries of the demo models from the price
// table in the binary.
func demoPrices() (map[string]*pricing.Entry, error) {
	t, errs := pricing.Parse(tollgate.PriceTable())
	if len(errs) > 0 {
		return nil, errs[0]
	}
	prices := map[string]*pricing.Entry{}
	for _, m := range []string{demoSonnet, demoHaiku} {
		e := t.Find("", "anthropic", m, time.Unix(1<<40, 0))
		if e == nil {
			return nil, fmt.Errorf("the price table has no entry for %s", m)
		}
		prices[m] = e
	}
	return prices, nil
}

// demoEvents returns the synthetic usage events of the demoHistory before
// now: agent sessions with growing prompts, prompt cache reads, some
// failover, some 429 and 502 responses, and derived session IDs. The costs
// come from prices. All events are before now, and the events are the same
// for the same now.
func demoEvents(now time.Time, prices map[string]*pricing.Entry) []usage.Event {
	r := rand.New(rand.NewPCG(demoSeed, 0)) //nolint:gosec // Seeded synthetic data, not a secret.
	var events []usage.Event
	id := 0
	for t := now.Add(-demoHistory); t.Before(now.Add(-20 * time.Minute)); {
		busy := 0.2
		if h := t.UTC().Hour(); h >= 13 && h <= 23 {
			busy = 1
		}
		t = t.Add(time.Duration((6 + r.Float64()*40) / busy * float64(time.Minute)))
		tenant := "research"
		if r.Float64() < 0.75 {
			tenant = "platform"
		}
		keyIDs := demoKeyIDs[tenant]
		key := keyIDs[r.IntN(len(keyIDs))]
		derived := r.Float64() < 0.4
		session := fmt.Sprintf("%s-%x", key, t.Unix())
		if derived {
			session = fmt.Sprintf("auto-%016x%016x", r.Uint64(), r.Uint64())
		}
		model := demoHaiku
		if r.Float64() < 0.85 {
			model = demoSonnet
		}
		turns := 4 + r.IntN(40)
		prompt := int64(4000 + r.IntN(6000))
		ts := t
		target := "fake-primary"
		for i := range turns {
			ts = ts.Add(seconds(8 + r.Float64()*50))
			if !ts.Before(now) {
				// The live traffic takes over from here.
				break
			}
			added := int64(300 + r.IntN(2500))
			status, attempts, switched := http.StatusOK, 1, false
			x := r.Float64()
			switch {
			case model == demoHaiku:
				// The first target of the haiku route always fails.
				target, attempts = "fake-backup", 2
			case x < 0.025:
				attempts = 2
				if target == "fake-primary" {
					target, switched = "fake-backup", true
				}
			case x < 0.035:
				status = http.StatusTooManyRequests
			case x < 0.04:
				status = http.StatusBadGateway
			}
			e := usage.Event{
				RequestID:      fmt.Sprintf("SEED%08d", id),
				TS:             ts.UTC(),
				TenantID:       tenant,
				KeyID:          key,
				SessionID:      session,
				RouteModel:     model,
				Status:         status,
				Cache:          "bypass",
				OverheadUs:     int64(120 + r.IntN(400)),
				SessionDerived: derived,
				TargetSwitched: switched,
			}
			id++
			if status != http.StatusTooManyRequests {
				e.Provider, e.ProviderModel, e.Attempts = target, model, attempts
			}
			if status == http.StatusOK {
				u := provider.Usage{InputTokens: 3, OutputTokens: int64(80 + r.IntN(900))}
				if switched || i == 0 {
					u.CacheWriteTokens = prompt + added
				} else {
					u.CacheReadTokens, u.CacheWriteTokens = prompt, added
				}
				p := prices[model]
				e.InputTokens, e.OutputTokens = u.InputTokens, u.OutputTokens
				e.CacheReadTokens, e.CacheWriteTokens = u.CacheReadTokens, u.CacheWriteTokens
				e.CostMicroUSD = p.Cost(u)
				if switched {
					cold := p.Cost(provider.Usage{InputTokens: u.InputTokens, CacheWriteTokens: u.CacheWriteTokens})
					warm := p.Cost(provider.Usage{CacheReadTokens: u.InputTokens + u.CacheWriteTokens})
					e.CacheLossMicroUSD = max(cold-warm, 0)
				}
				ttftMax := 1200
				if target == "fake-primary" {
					ttftMax = 600
				}
				ttft := int64(300 + r.IntN(ttftMax))
				e.TTFTMs = &ttft
				e.DurationMs = ttft + u.OutputTokens*12
				prompt += added + u.OutputTokens
			}
			events = append(events, e)
			if model != demoHaiku && target == "fake-backup" && r.Float64() < 0.15 {
				target = "fake-primary"
			}
		}
	}
	return events
}

// writeDemoUsage writes events to a new usage file at path.
func writeDemoUsage(path string, events []usage.Event) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // The path is in the demo directory.
	if err != nil {
		return err
	}
	for i := range events {
		if err := json.MarshalWrite(f, &events[i]); err != nil {
			_ = f.Close()
			return err
		}
		if _, err := f.WriteString("\n"); err != nil {
			_ = f.Close()
			return err
		}
	}
	return f.Close()
}

// demoTraffic starts a new agent-like session every 8 to 16 seconds, and a
// burst of short requests with every third session, until ctx ends.
func demoTraffic(ctx context.Context, base string, keys map[string]string) {
	var wg sync.WaitGroup
	defer wg.Wait()
	client := &http.Client{}
	for n := 0; ; n++ {
		wg.Go(func() { demoAgent(ctx, client, base, keys, n) })
		if n%3 == 0 {
			wg.Go(func() { demoBurst(ctx, client, base, keys["platform"], n) })
		}
		if !sleepCtx(ctx, seconds(8+rand.Float64()*8)) { //nolint:gosec // Demo timing, not a secret.
			return
		}
	}
}

// demoAgent sends the 30 turns of one agent session. Each request has the
// conversation so far, so the prompt grows and the prompt cache matters.
// Every second session has a session header. The others get a derived
// session ID. The first message has an email address, which the redaction
// of the platform tenant masks.
func demoAgent(ctx context.Context, client *http.Client, base string, keys map[string]string, n int) {
	tenant := "platform"
	if n%3 == 2 {
		tenant = "research"
	}
	pkg := []string{"server", "router", "limiter"}[rand.IntN(3)] //nolint:gosec // Demo text, not a secret.
	system := strings.Repeat("You are a coding agent. ", 400)
	messages := []map[string]string{{"role": "user", "content": fmt.Sprintf("Task %d: fix the failing test in package %s. Mail the result to ana@example.com.", n, pkg)}}
	for turn := range 30 {
		model := demoSonnet
		if turn%7 == 6 {
			model = demoHaiku
		}
		body := map[string]any{"model": model, "max_tokens": 1024, "stream": true, "system": system, "messages": messages}
		header := http.Header{"X-Api-Key": {keys[tenant]}}
		if n%2 == 0 {
			header.Set("X-Tollgate-Session", fmt.Sprintf("live-%d", n))
		}
		if err := demoPost(ctx, client, base, header, body); err != nil {
			return
		}
		messages = append(messages,
			map[string]string{"role": "assistant", "content": strings.Repeat("I read the file and ran the tests. ", 10)},
			map[string]string{"role": "user", "content": fmt.Sprintf("Tool result %d: %s", turn, strings.Repeat("ok ", 200+rand.IntN(900)))}, //nolint:gosec // Demo text, not a secret.
		)
		if !sleepCtx(ctx, seconds(1.5+rand.Float64()*4)) { //nolint:gosec // Demo timing, not a secret.
			return
		}
	}
}

// demoBurst sends quick requests on the haiku route. Its first target
// always fails, so its circuit breaker opens. The last two requests have
// the same body, so the second one is a hit in the response cache.
func demoBurst(ctx context.Context, client *http.Client, base, key string, n int) {
	for i := range 8 {
		content := "Name this file."
		if i < 6 {
			content = fmt.Sprintf("Name this file %d-%d.", n, i)
		}
		body := map[string]any{"model": demoHaiku, "max_tokens": 64, "messages": []map[string]string{{"role": "user", "content": content}}}
		if err := demoPost(ctx, client, base, http.Header{"X-Api-Key": {key}}, body); err != nil {
			return
		}
	}
}

// demoPost sends one Messages API request and reads the whole response.
// It returns an error only if the request could not be sent.
func demoPost(ctx context.Context, client *http.Client, base string, header http.Header, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header = header
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// seconds returns s seconds as a duration.
func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// sleepCtx waits for d. It returns false if ctx ends first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
