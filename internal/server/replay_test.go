package server

import (
	"bufio"
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ykakade/tollgate/internal/auth"
	"github.com/ykakade/tollgate/internal/config"
	"github.com/ykakade/tollgate/internal/fakeprovider"
	"github.com/ykakade/tollgate/internal/usage"
)

// updateReplay writes the replay results and their chart again.
var updateReplay = flag.Bool("update-replay", false, "write testdata/replay/results.json and docs/assets/sticky-replay.svg again")

// The replay files. A test runs in the directory of its package.
const (
	replayResults = "../../testdata/replay/results.json"
	replayChart   = "../../docs/assets/sticky-replay.svg"
)

// Settings of the replay.
const (
	replayTurns = 40
	// replayGap is the time between two requests of the agent.
	replayGap = 20 * time.Second
	// replayCacheTTL is the prompt cache lifetime of both fake providers
	// and of the sticky routing entries.
	replayCacheTTL = 5 * time.Minute
	replaySeed     = 2026
)

// syntheticSession returns the request bodies of a synthetic agent session:
// a system prompt of about 16 KB, and in each turn a tool result of 2 KB to
// 8 KB and a reply of about 600 bytes. Each request has the full
// conversation so far, as coding agents send it.
func syntheticSession(t *testing.T) [][]byte {
	t.Helper()
	r := rand.New(rand.NewPCG(replaySeed, 1))
	words := strings.Fields("the a file test build error function value line change module package return result check output input path read write type field config route token cache stream")
	text := func(n int) string {
		var b strings.Builder
		for b.Len() < n {
			b.WriteString(words[r.IntN(len(words))])
			b.WriteByte(' ')
		}
		return b.String()
	}
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	system := text(16 << 10)
	var messages []message
	bodies := make([][]byte, 0, replayTurns)
	for turn := range replayTurns {
		if turn == 0 {
			messages = append(messages, message{"user", "Fix the failing test in the server package. " + text(400)})
		} else {
			messages = append(messages, message{"assistant", text(600)}, message{"user", "Tool result: " + text(2<<10+r.IntN(6<<10))})
		}
		b, err := json.Marshal(struct {
			Model     string    `json:"model"`
			MaxTokens int       `json:"max_tokens"`
			Stream    bool      `json:"stream"`
			System    string    `json:"system"`
			Messages  []message `json:"messages"`
		}{"claude-sonnet-5-5", 1024, true, system, messages})
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, b)
	}
	return bodies
}

// readSession reads a session file: one request body in each line.
func readSession(t *testing.T, path string) [][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 64<<20)
	for sc.Scan() {
		if line := bytes.TrimSpace(sc.Bytes()); len(line) > 0 {
			if !jsontext.Value(line).IsValid() {
				t.Fatalf("a line of %s is not JSON", path)
			}
			out = append(out, bytes.Clone(line))
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// replayScenario is a time when the primary target fails.
type replayScenario struct {
	Name string `json:"name"`
	// Down holds the turns in which the primary target answers 529.
	From, To int
}

func (s replayScenario) down(turn int) bool { return turn >= s.From && turn < s.To }

var replayScenarios = []replayScenario{
	{Name: "no outage", From: -1, To: -1},
	// 5 turns: 100 s. The cache of the primary target is still valid
	// after the outage.
	{Name: "100 s outage", From: 10, To: 15},
	// 18 turns: 6 minutes. The cache of the primary target expires.
	{Name: "6 min outage", From: 10, To: 28},
}

// replayResult is the result of one run.
type replayResult struct {
	Scenario         string `json:"scenario"`
	Sticky           bool   `json:"sticky"`
	Requests         int64  `json:"requests"`
	Attempts         int64  `json:"attempts"`
	Switches         int64  `json:"target_switches"`
	InputTokens      int64  `json:"input_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CostMicroUSD     int64  `json:"cost_micro_usd"`
	CacheLoss        int64  `json:"cache_loss_micro_usd"`
}

// replay sends the bodies through a Tollgate with two fake providers. The
// primary target answers 529 in the turns of the scenario.
func replay(t *testing.T, bodies [][]byte, sc replayScenario, sticky bool) replayResult {
	t.Helper()
	clock := newClock(time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC))
	var turn atomic.Int64
	primary := fakeprovider.New(fakeprovider.Config{PromptCache: replayCacheTTL, Now: clock.now})
	backup := fakeprovider.New(fakeprovider.Config{PromptCache: replayCacheTTL, Now: clock.now})
	failing := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sc.down(int(turn.Load())) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(529)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
			return
		}
		primary.Handler().ServeHTTP(w, r)
	})
	up1 := httptest.NewServer(failing)
	up2 := httptest.NewServer(backup.Handler())
	t.Cleanup(up1.Close)
	t.Cleanup(up2.Close)
	key, err := auth.NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	yaml := fmt.Sprintf(`version: 1
server: {max_body_bytes: 32MiB}
sessions: {sticky: %v}
usage: {sink: none}
providers:
  primary: {kind: anthropic, base_url: %s, cache_ttl: %s}
  backup: {kind: anthropic, base_url: %s, cache_ttl: %s}
routes:
  - model: claude-sonnet-5-5
    api: anthropic
    targets: [{provider: primary}, {provider: backup}]
tenants:
  - id: agent
    keys: [{id: replay, hash: %s}]
    models: [claude-sonnet-5-5]
`, sticky, up1.URL, replayCacheTTL, up2.URL, replayCacheTTL, auth.Hash(key))
	cfg, err := config.Parse("replay.yaml", []byte(yaml), config.Env{})
	if err != nil {
		t.Fatal(err)
	}
	adapters, err := NewAdapters(t.Context(), cfg.Providers, AdapterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c := &capture{}
	srv, err := New(Options{Config: cfg, Adapters: adapters, Logger: slog.New(slog.DiscardHandler), Now: clock.now, Usage: c})
	if err != nil {
		t.Fatal(err)
	}
	done := &handlerDone{}
	gw := httptest.NewServer(done.wrap(srv.Handler()))
	t.Cleanup(gw.Close)
	e := &env{t: t, srv: srv, url: gw.URL, key: key, done: done}
	for i, body := range bodies {
		turn.Store(int64(i))
		resp, out := e.post("/v1/messages", string(body))
		if resp.StatusCode != http.StatusOK || !strings.Contains(out, "message_stop") {
			t.Fatalf("turn %d: status %d, body %s", i, resp.StatusCode, out[:min(len(out), 300)])
		}
		clock.add(replayGap)
	}
	events := waitEvents(t, c, len(bodies))
	g, _ := usage.Summarize(events, usage.ByNone)
	res := replayResult{
		Scenario: sc.Name, Sticky: sticky, Requests: g[0].Requests, Switches: g[0].TargetSwitches,
		InputTokens: g[0].InputTokens, CacheReadTokens: g[0].CacheReadTokens, CacheWriteTokens: g[0].CacheWriteTokens,
		OutputTokens: g[0].OutputTokens, CostMicroUSD: g[0].CostMicroUSD, CacheLoss: g[0].CacheLossMicroUSD,
	}
	for _, ev := range events {
		res.Attempts += int64(ev.Attempts)
	}
	return res
}

// TestStickyReplay is the M6 acceptance test: it replays an agent session
// with and without sticky routing, and reports the cache read tokens and
// the cost of each run. The numbers are in testdata/replay/results.json,
// docs/agent-features.md and docs/assets/sticky-replay.svg. Run
// "make replay" to write them again.
//
// TOLLGATE_REPLAY_FILE replays a session file instead of the synthetic
// session: one request body in each line. The test then prints the results
// and does not compare them.
func TestStickyReplay(t *testing.T) {
	bodies := syntheticSession(t)
	file := os.Getenv("TOLLGATE_REPLAY_FILE")
	if file != "" {
		bodies = readSession(t, file)
	}
	var results []replayResult
	for _, sc := range replayScenarios {
		for _, sticky := range []bool{true, false} {
			results = append(results, replay(t, bodies, sc, sticky))
		}
	}
	got, err := json.Marshal(results, jsontext.Multiline(true))
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if file != "" {
		t.Logf("results of %s:\n%s", file, got)
		return
	}
	if *updateReplay {
		if err := os.MkdirAll(filepath.Dir(replayResults), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(replayResults, got, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(replayChart, replaySVG(results), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(replayResults)
	if err != nil {
		t.Fatalf("read the results: %v (run make replay)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the replay results changed. Run make replay, and update docs/agent-features.md.\ngot:\n%s", got)
	}
	chart, err := os.ReadFile(replayChart)
	if err != nil || !bytes.Equal(chart, replaySVG(results)) {
		t.Errorf("docs/assets/sticky-replay.svg does not show the results. Run make replay.")
	}
	for i := 0; i+1 < len(results); i += 2 {
		on, off := results[i], results[i+1]
		if on.CostMicroUSD > off.CostMicroUSD || on.Switches > off.Switches {
			t.Errorf("%s: sticky routing cost %d and switched %d times; without it %d and %d", on.Scenario, on.CostMicroUSD, on.Switches, off.CostMicroUSD, off.Switches)
		}
	}
}
