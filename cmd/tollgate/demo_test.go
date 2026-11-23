package main

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ykakade/tollgate/internal/config"
	"github.com/ykakade/tollgate/internal/provider"
	"github.com/ykakade/tollgate/internal/usage"
)

func TestDemoEvents(t *testing.T) {
	prices, err := demoPrices()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	events := demoEvents(now, prices)
	if diff := cmp.Diff(events, demoEvents(now, prices)); diff != "" {
		t.Fatalf("two runs with the same time gave different events (-first +second):\n%s", diff)
	}
	if len(events) < 1000 {
		t.Fatalf("got %d events, want a week of traffic", len(events))
	}

	var fallbacks, switches, derived, rejected, failed int
	for _, e := range events {
		if e.TS.Before(now.Add(-demoHistory)) || !e.TS.Before(now) {
			t.Fatalf("event %s at %v is outside the history", e.RequestID, e.TS)
		}
		if _, ok := demoKeyIDs[e.TenantID]; !ok {
			t.Fatalf("event %s has the unknown tenant %q", e.RequestID, e.TenantID)
		}
		if e.SessionDerived {
			derived++
			if !strings.HasPrefix(e.SessionID, "auto-") {
				t.Errorf("event %s: derived session %q has no auto- prefix", e.RequestID, e.SessionID)
			}
		}
		if e.Attempts > 1 {
			fallbacks++
		}
		if e.TargetSwitched {
			switches++
		}
		switch e.Status {
		case http.StatusOK:
			// The cost must be the price of the usage in the price table, so
			// that the history agrees with the live requests.
			u := provider.Usage{InputTokens: e.InputTokens, OutputTokens: e.OutputTokens, CacheReadTokens: e.CacheReadTokens, CacheWriteTokens: e.CacheWriteTokens}
			if want := prices[e.RouteModel].Cost(u); e.CostMicroUSD != want || want <= 0 {
				t.Fatalf("event %s: cost %d, want %d", e.RequestID, e.CostMicroUSD, want)
			}
			if e.TTFTMs == nil {
				t.Fatalf("event %s: status 200 with no TTFT", e.RequestID)
			}
		case http.StatusTooManyRequests:
			rejected++
			if e.Provider != "" || e.CostMicroUSD != 0 {
				t.Errorf("event %s: a rejected request has provider %q and cost %d", e.RequestID, e.Provider, e.CostMicroUSD)
			}
		default:
			failed++
		}
	}
	for name, n := range map[string]int{"fallbacks": fallbacks, "target switches": switches, "derived sessions": derived, "429 responses": rejected, "502 responses": failed} {
		if n == 0 {
			t.Errorf("the history has no %s", name)
		}
	}

	// The usage file must read back with no bad lines.
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	if err := writeDemoUsage(path, events); err != nil {
		t.Fatal(err)
	}
	n, bad, err := usage.ReadEvents(path, func(*usage.Event) {})
	if err != nil || n != len(events) || bad != 0 {
		t.Fatalf("ReadEvents: %d events, %d bad lines, error %v; want %d events", n, bad, err, len(events))
	}
}

func TestDemoConfigValid(t *testing.T) {
	urls := map[string]string{"fake-primary": "http://127.0.0.1:1", "fake-backup": "http://127.0.0.1:2", "fake-flaky": "http://127.0.0.1:3"}
	keys := map[string]string{"platform": "tg_platform", "research": "tg_research"}
	dir := t.TempDir()
	path := filepath.Join(dir, "tollgate.yaml")
	text := demoConfig("127.0.0.1:8080", "127.0.0.1:9090", filepath.Join(dir, "usage.jsonl"), urls, keys)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path, config.OSEnv())
	if err != nil {
		t.Fatalf("the demo configuration is not valid: %v\n%s", err, text)
	}
	if got := len(cfg.Tenants); got != len(demoKeyIDs) {
		t.Errorf("got %d tenants, want %d", got, len(demoKeyIDs))
	}
}

func TestDemoFlags(t *testing.T) {
	code, _, stderr := runCmd("demo", "extra")
	if code != exitUsage {
		t.Errorf("demo extra: exit code %d, want %d", code, exitUsage)
	}
	checkContains(t, "stderr", stderr, `The argument "extra" is not known`)
}

// TestRunDemo starts the demo, waits for live traffic on top of the
// history, sends the request from the banner, and stops the demo.
func TestRunDemo(t *testing.T) {
	listen, adminListen := freeAddr(t), freeAddr(t)
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stdout, stderr := &syncBuffer{}, &syncBuffer{}
	done := make(chan int, 1)
	go func() {
		done <- runDemo(ctx, []string{"-listen", listen, "-admin-listen", adminListen, "-dir", dir}, stdout, stderr)
	}()

	var key string
	var seeded int
	deadline := time.Now().Add(15 * time.Second)
	for key == "" {
		if time.Now().After(deadline) {
			t.Fatalf("the demo did not show its banner. stdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		select {
		case code := <-done:
			t.Fatalf("the demo stopped with exit code %d. stderr:\n%s", code, stderr)
		case <-time.After(50 * time.Millisecond):
		}
		out := stdout.String()
		if m := regexp.MustCompile(`x-api-key: (tg_\w+)`).FindStringSubmatch(out); m != nil {
			key = m[1]
			n := regexp.MustCompile(`has (\d+) synthetic events`).FindStringSubmatch(out)
			if n == nil {
				t.Fatalf("the banner has no event count:\n%s", out)
			}
			seeded, _ = strconv.Atoi(n[1])
		}
	}
	for _, want := range []string{"http://" + adminListen, "http://" + listen, "Ctrl-C"} {
		checkContains(t, "banner", stdout.String(), want)
	}

	// The request from the banner works, with the session header.
	body := `{"model":"claude-sonnet-5-5","max_tokens":64,"messages":[{"role":"user","content":"Hello"}]}`
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+listen+"/v1/messages", strings.NewReader(body))
	req.Header.Set("X-Api-Key", key)
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tollgate-Session", "my-first-session")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Tollgate-Session-Remaining-Usd") == "" {
		t.Fatalf("banner request: status %d, headers %v", resp.StatusCode, resp.Header)
	}

	// The admin API has the history, the banner request, and at least one
	// live request.
	for {
		var info struct {
			Events int `json:"events"`
		}
		infoReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+adminListen+"/admin/v1/info", nil)
		r, err := http.DefaultClient.Do(infoReq)
		if err == nil {
			err = json.UnmarshalRead(r.Body, &info)
			r.Body.Close()
		}
		if err == nil && info.Events >= seeded+2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the admin API has %d events (error %v), want %d or more", info.Events, err, seeded+2)
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, name := range []string{"tollgate.yaml", "usage.jsonl", "tollgate.log"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("the demo directory has no %s: %v", name, err)
		}
	}

	cancel()
	select {
	case code := <-done:
		if code != exitOK {
			t.Errorf("exit code %d after the stop, want 0. stderr:\n%s", code, stderr)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the demo did not stop")
	}
}

func TestRunDemoPortInUse(t *testing.T) {
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	stdout, stderr := &syncBuffer{}, &syncBuffer{}
	code := runDemo(t.Context(), []string{"-listen", ln.Addr().String(), "-admin-listen", freeAddr(t)}, stdout, stderr)
	if code != exitError {
		t.Errorf("exit code %d, want %d", code, exitError)
	}
	checkContains(t, "stderr", stderr.String(), "cannot listen on "+ln.Addr().String())
	checkContains(t, "stderr", stderr.String(), "set -listen and -admin-listen")
	if stdout.String() != "" {
		t.Errorf("stdout has the banner, but the demo did not start:\n%s", stdout)
	}
}

// freeAddr returns a loopback address with a port that no program uses.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// syncBuffer is a bytes.Buffer that is safe for concurrent use.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
