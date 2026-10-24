//go:build unix

package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ykakade/tollgate/internal/auth"
)

// tollgateProcess is a Tollgate process that a test started.
type tollgateProcess struct {
	cmd  *exec.Cmd
	base string
	logs *lockedLines
}

// startTollgate starts "tollgate serve" with the configuration at path.
func startTollgate(t *testing.T, path string) *tollgateProcess {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "serve", "-config", path)
	cmd.Env = append(os.Environ(), "TOLLGATE_TEST_MAIN=1", "TEST_PROVIDER_KEY=sk-test")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &tollgateProcess{cmd: cmd, logs: &lockedLines{}}
	addr := make(chan string, 1)
	go p.logs.read(stderr, addr)
	select {
	case a := <-addr:
		p.base = "http://" + a
	case <-time.After(10 * time.Second):
		t.Fatalf("tollgate did not start. Logs:\n%s", p.logs)
	}
	return p
}

// stop sends SIGTERM and waits for the process to end.
func (p *tollgateProcess) stop(t *testing.T) {
	t.Helper()
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("tollgate exit: %v. Logs:\n%s", err, p.logs)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("tollgate did not stop. Logs:\n%s", p.logs)
	}
}

func (p *tollgateProcess) post(t *testing.T, key string) int {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, p.base+"/v1/messages",
		strings.NewReader(`{"model":"claude-test","max_tokens":10,"messages":[]}`))
	req.Header.Set("X-Api-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// TestRestartKeepsMonthSpend checks that a restart does not reset the
// spend of a month budget in single-node mode (ADR 0019).
func TestRestartKeepsMonthSpend(t *testing.T) {
	// Each response costs 100 × 1 + 50 × 2 = 200 micro-USD.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"m","usage":{"input_tokens":100,"output_tokens":50}}`)
	}))
	defer upstream.Close()

	key, err := auth.NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	prices := `- {provider: anthropic, model: claude-test, effective_from: 2026-01-01, micro_usd_per_mtok: {input: 1000000, output: 2000000}}`
	if err := os.WriteFile(filepath.Join(dir, "prices.yaml"), []byte(prices), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "tollgate.yaml")
	cfg := fmt.Sprintf(`version: 1
server:
  listen: "127.0.0.1:0"
pricing:
  file: prices.yaml
providers:
  anthropic: {kind: anthropic, base_url: %s, api_key: env:TEST_PROVIDER_KEY}
routes:
  - {model: claude-test, api: anthropic, targets: [{provider: anthropic}]}
tenants:
  - id: t
    keys: [{id: k, hash: %s}]
    models: [claude-test]
    budgets: [{window: month, limit_usd: 0.0005}]
`, upstream.URL, auth.Hash(key))
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	p := startTollgate(t, path)
	for i, want := range []int{200, 200, 200, 402} {
		if got := p.post(t, key); got != want {
			t.Fatalf("request %d: status %d, want %d. Logs:\n%s", i, got, want, p.logs)
		}
	}
	p.stop(t)

	data, err := os.ReadFile(filepath.Join(dir, "data", "usage.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(data, []byte("\n")); n != 4 || bytes.Count(data, []byte(`"cost_micro_usd":200`)) != 3 {
		t.Errorf("usage file has %d lines:\n%s\nwant 4 events, 3 with a cost of 200", n, data)
	}

	p = startTollgate(t, path)
	defer p.stop(t)
	if got := p.post(t, key); got != http.StatusPaymentRequired {
		t.Errorf("first request after the restart: status %d, want 402. Logs:\n%s", got, p.logs)
	}
	if !strings.Contains(p.logs.String(), `"msg":"spend restored from the usage file","events":4,"bad_lines":0,"tenants":1,"sessions":0,"month_spend_micro_usd":600`) {
		t.Errorf("logs do not show the restored spend:\n%s", p.logs)
	}
}
