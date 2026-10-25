//go:build unix

package main

import (
	"bufio"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ykakade/tollgate/internal/auth"
)

// TestServeSIGTERM starts Tollgate as a process, opens a stream, and sends
// SIGTERM. The open stream must finish, and new requests must get 503
// (ADR 0013).
func TestServeSIGTERM(t *testing.T) {
	release := make(chan struct{})
	first := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}}\n\n"
	rest := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":4}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, first)
		w.(http.Flusher).Flush()
		select {
		case <-release:
			_, _ = io.WriteString(w, rest)
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()

	key, err := auth.NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tollgate.yaml")
	cfg := fmt.Sprintf(`version: 1
server:
  listen: "127.0.0.1:0"
  admin_listen: "127.0.0.1:0"
  drain_timeout: 30s
providers:
  anthropic: {kind: anthropic, base_url: %s, api_key: env:TEST_PROVIDER_KEY}
routes:
  - {model: claude-test, api: anthropic, targets: [{provider: anthropic}]}
tenants:
  - {id: t, keys: [{id: k, hash: %s}], models: [claude-test]}
`, upstream.URL, auth.Hash(key))
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), os.Args[0], "serve", "-config", path)
	cmd.Env = append(os.Environ(), "TOLLGATE_TEST_MAIN=1", "TEST_PROVIDER_KEY=sk-test")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	logs := &lockedLines{}
	addr := make(chan string, 1)
	go logs.read(stderr, addr)
	var base string
	select {
	case a := <-addr:
		base = "http://" + a
	case <-time.After(10 * time.Second):
		t.Fatalf("tollgate did not start. Logs:\n%s", logs)
	}

	newRequest := func(stream bool) *http.Request {
		body := fmt.Sprintf(`{"model":"claude-test","max_tokens":5,"stream":%t,"messages":[]}`, stream)
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, base+"/v1/messages", strings.NewReader(body))
		req.Header.Set("X-Api-Key", key)
		return req
	}
	resp, err := http.DefaultClient.Do(newRequest(true))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, len(first))
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatalf("read first event: %v", err)
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	deadline := time.Now().Add(5 * time.Second)
	for {
		readyReq, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/readyz", nil)
		r, err := client.Do(readyReq)
		if err == nil {
			r.Body.Close()
			if r.StatusCode == http.StatusServiceUnavailable {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("/readyz did not fail after SIGTERM. Logs:\n%s", logs)
		}
		time.Sleep(20 * time.Millisecond)
	}
	r, err := client.Do(newRequest(false))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("new request after SIGTERM: status %d, want 503", r.StatusCode)
	}

	// The admin listener stays open during the drain, and its metrics
	// show the open stream.
	adminAddr := logs.addr("admin port started")
	if adminAddr == "" {
		t.Fatalf("no admin port address in the logs:\n%s", logs)
	}
	metricsReq, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+adminAddr+"/metrics", nil)
	mr, err := client.Do(metricsReq)
	if err != nil {
		t.Fatalf("get /metrics during the drain: %v", err)
	}
	metricsBody, _ := io.ReadAll(mr.Body)
	mr.Body.Close()
	if mr.StatusCode != http.StatusOK || !strings.Contains(string(metricsBody), "\ntollgate_active_streams 1\n") {
		t.Errorf("/metrics during the drain: status %d, want 200 with tollgate_active_streams 1:\n%s", mr.StatusCode, metricsBody)
	}

	close(release)
	got, err := io.ReadAll(resp.Body)
	if err != nil || string(got) != rest {
		t.Errorf("rest of the stream = %q, err %v; want the stream to finish", got, err)
	}

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case err := <-waitErr:
		if err != nil {
			t.Errorf("tollgate exit: %v. Logs:\n%s", err, logs)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("tollgate did not stop after the drain. Logs:\n%s", logs)
	}
	for _, want := range []string{`"msg":"stop signal received"`, `"msg":"drain started"`, `"msg":"drain done"`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("logs do not contain %s:\n%s", want, logs)
		}
	}
}

// lockedLines collects log lines from a process.
type lockedLines struct {
	mu    sync.Mutex
	lines []string
}

// read reads lines from r and sends the address of the "server started"
// line to addr.
func (l *lockedLines) read(r io.Reader, addr chan<- string) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		l.mu.Lock()
		l.lines = append(l.lines, line)
		l.mu.Unlock()
		var m struct {
			Msg  string `json:"msg"`
			Addr string `json:"addr"`
		}
		if json.Unmarshal([]byte(line), &m) == nil && m.Msg == "server started" {
			addr <- m.Addr
		}
	}
}

// addr returns the address of the first log line with the message msg,
// or "".
func (l *lockedLines) addr(msg string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		var m struct {
			Msg  string `json:"msg"`
			Addr string `json:"addr"`
		}
		if json.Unmarshal([]byte(line), &m) == nil && m.Msg == msg {
			return m.Addr
		}
	}
	return ""
}

func (l *lockedLines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}
