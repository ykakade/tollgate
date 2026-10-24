package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ykakade/tollgate/internal/auth"
	"github.com/ykakade/tollgate/internal/config"
)

const (
	anthropicProviderKey = "sk-ant-provider-secret"
	openaiProviderKey    = "sk-openai-provider-secret"
)

// syncBuffer is a bytes.Buffer that is safe for concurrent use.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// env is the test environment: an upstream server, a Tollgate server and a
// client key.
type env struct {
	t        *testing.T
	srv      *Server
	url      string
	key      string
	logs     *syncBuffer
	upstream *httptest.Server
}

type setup struct {
	// extraYAML is added to the end of the routes list.
	extraRoutes string
	timeouts    string
	drain       string
	maxBody     string
}

// newEnv starts a Tollgate server in front of upstream. Both providers point
// to the same upstream server.
func newEnv(t *testing.T, upstream http.Handler, s setup) *env {
	t.Helper()
	up := httptest.NewServer(upstream)
	t.Cleanup(up.Close)
	key, err := auth.NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	if s.timeouts == "" {
		s.timeouts = "{connect: 1s, first_token: 2s, idle: 2s, total: 10s}"
	}
	if s.drain == "" {
		s.drain = "10s"
	}
	if s.maxBody == "" {
		s.maxBody = "1MiB"
	}
	yaml := fmt.Sprintf(`version: 1
server:
  max_body_bytes: %s
  drain_timeout: %s
providers:
  anthropic:
    kind: anthropic
    base_url: %s
    api_key: env:A_KEY
  openai:
    kind: openai
    base_url: %s
    api_key: env:O_KEY
routes:
  - model: claude-test
    api: anthropic
    targets: [{provider: anthropic, model: claude-upstream}]
    timeouts: %s
  - model: gpt-test
    api: openai
    targets: [{provider: openai}]
    timeouts: %s
  - model: not-for-personal
    api: openai
    targets: [{provider: openai}]
%s
tenants:
  - id: personal
    keys: [{id: laptop, hash: %s}]
    models: [claude-test, gpt-test]
`, s.maxBody, s.drain, up.URL, up.URL, s.timeouts, s.timeouts, s.extraRoutes, auth.Hash(key))
	vars := map[string]string{"A_KEY": anthropicProviderKey, "O_KEY": openaiProviderKey}
	cfg, err := config.Parse("test.yaml", []byte(yaml), config.Env{
		LookupEnv: func(k string) (string, bool) { v, ok := vars[k]; return v, ok },
	})
	if err != nil {
		t.Fatal(err)
	}
	adapters, err := NewAdapters(cfg.Providers, nil)
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	srv, err := New(Options{Config: cfg, Adapters: adapters, Logger: slog.New(slog.NewJSONHandler(logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	e := &env{t: t, srv: srv, url: ts.URL, key: key, logs: logs, upstream: up}
	t.Cleanup(func() {
		out := logs.String()
		if strings.Contains(out, key) || strings.Contains(out, anthropicProviderKey) || strings.Contains(out, openaiProviderKey) {
			t.Errorf("the logs contain a key:\n%s", out)
		}
	})
	return e
}

// request makes a request to Tollgate with the client key.
func (e *env) request(ctx context.Context, path, body string) *http.Request {
	e.t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.HasPrefix(path, "/v1/messages") {
		req.Header.Set("X-Api-Key", e.key)
		req.Header.Set("Anthropic-Version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+e.key)
	}
	return req
}

// response is the status and the headers of a response that do read.
type response struct {
	StatusCode int
	Header     http.Header
}

// do sends a request and returns the response with its body.
func (e *env) do(req *http.Request) (response, string) {
	e.t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return response{resp.StatusCode, resp.Header}, string(b)
}

func (e *env) post(path, body string) (response, string) {
	e.t.Helper()
	return e.do(e.request(e.t.Context(), path, body))
}

// logLine waits for the access log line of the request and returns it.
func (e *env) logLine(requestID string) map[string]any {
	e.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sc := bufio.NewScanner(strings.NewReader(e.logs.String()))
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) == nil && m["request_id"] == requestID && m["msg"] == "request done" {
				return m
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("no access log line for request %s in:\n%s", requestID, e.logs.String())
	return nil
}

// upstreamRecord holds what the upstream server received.
type upstreamRecord struct {
	mu     sync.Mutex
	path   string
	query  string
	header http.Header
	body   string
}

func (u *upstreamRecord) get() (string, string, http.Header, string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.path, u.query, u.header, u.body
}

// fixed returns an upstream handler that records the request and sends a
// fixed response.
func fixed(rec *upstreamRecord, status int, contentType, body string, header ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if rec != nil {
			rec.mu.Lock()
			rec.path, rec.query, rec.header, rec.body = r.URL.Path, r.URL.RawQuery, r.Header.Clone(), string(b)
			rec.mu.Unlock()
		}
		for i := 0; i+1 < len(header); i += 2 {
			w.Header().Set(header[i], header[i+1])
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

const anthropicStream = "event: message_start\n" +
	`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-upstream","content":[],"stop_reason":null,"usage":{"input_tokens":12,"cache_creation_input_tokens":3,"cache_read_input_tokens":4,"output_tokens":1}}}` + "\n\n" +
	"event: content_block_delta\n" +
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}` + "\n\n" +
	"event: message_delta\n" +
	`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":9}}` + "\n\n" +
	"event: message_stop\n" +
	`data: {"type":"message_stop"}` + "\n\n"

func TestProxyAnthropicStream(t *testing.T) {
	var rec upstreamRecord
	e := newEnv(t, fixed(&rec, 200, "text/event-stream", anthropicStream, "Request-Id", "req_up_1"), setup{})
	body := `{"model":"claude-test","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := e.request(t.Context(), "/v1/messages?beta=true", body)
	req.Header.Set("Anthropic-Beta", "prompt-caching")
	req.Header.Set("X-Tollgate-Session", "s1")
	resp, got := e.do(req)

	if resp.StatusCode != http.StatusOK || got != anthropicStream {
		t.Fatalf("status %d, body:\n%s\nwant the upstream stream unchanged", resp.StatusCode, got)
	}
	id := resp.Header.Get("X-Request-Id")
	if id == "" || resp.Header.Get("X-Tollgate-Provider") != "anthropic" || resp.Header.Get("X-Tollgate-Model") != "claude-upstream" || resp.Header.Get("X-Tollgate-Attempts") != "1" {
		t.Errorf("response headers = %v", resp.Header)
	}
	if resp.Header.Get("Request-Id") != "req_up_1" {
		t.Error("the provider request-id header is missing")
	}

	path, query, h, upBody := rec.get()
	if path != "/v1/messages" || query != "beta=true" {
		t.Errorf("upstream path %q query %q", path, query)
	}
	if h.Get("X-Api-Key") != anthropicProviderKey || h.Get("Authorization") != "" || h.Get("X-Tollgate-Session") != "" {
		t.Errorf("upstream auth headers = %v", h)
	}
	if h.Get("Anthropic-Beta") != "prompt-caching" {
		t.Error("the anthropic-beta header did not go upstream")
	}
	wantBody := strings.Replace(body, `"claude-test"`, `"claude-upstream"`, 1)
	if upBody != wantBody {
		t.Errorf("upstream body = %s, want %s", upBody, wantBody)
	}

	line := e.logLine(id)
	want := map[string]any{
		"status": 200.0, "tenant_id": "personal", "key_id": "laptop", "route_model": "claude-test",
		"provider": "anthropic", "provider_model": "claude-upstream", "stream": true,
		"input_tokens": 12.0, "output_tokens": 9.0, "cache_write_tokens": 3.0, "cache_read_tokens": 4.0,
		"usage_estimated": false, "upstream_request_id": "req_up_1",
	}
	for k, v := range want {
		if line[k] != v {
			t.Errorf("log %s = %v, want %v", k, line[k], v)
		}
	}
	if _, ok := line["ttft_ms"]; !ok {
		t.Error("log has no ttft_ms")
	}
}

func TestProxyOpenAIAddsIncludeUsage(t *testing.T) {
	stream := `data: {"id":"c","choices":[{"index":0,"delta":{"content":"Hi"}}]}` + "\n\n" +
		`data: {"id":"c","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}` + "\n\n" +
		"data: [DONE]\n\n"
	var rec upstreamRecord
	e := newEnv(t, fixed(&rec, 200, "text/event-stream; charset=utf-8", stream, "X-Request-Id", "up-2"), setup{})
	resp, got := e.post("/v1/chat/completions", `{"model":"gpt-test","stream":true,"messages":[]}`)
	if resp.StatusCode != http.StatusOK || got != stream {
		t.Fatalf("status %d, body %q", resp.StatusCode, got)
	}
	if resp.Header.Get("X-Request-Id") == "up-2" {
		t.Error("x-request-id is the provider ID, want the Tollgate ID")
	}
	_, _, h, upBody := rec.get()
	if want := `{"stream_options":{"include_usage":true},"model":"gpt-test","stream":true,"messages":[]}`; upBody != want {
		t.Errorf("upstream body = %s, want %s", upBody, want)
	}
	if h.Get("Authorization") != "Bearer "+openaiProviderKey {
		t.Error("the provider key did not go upstream")
	}
	line := e.logLine(resp.Header.Get("X-Request-Id"))
	if line["input_tokens"] != 7.0 || line["output_tokens"] != 2.0 || line["upstream_request_id"] != "up-2" {
		t.Errorf("log = %v", line)
	}
}

func TestProxyNonStream(t *testing.T) {
	body := `{"id":"msg_1","type":"message","content":[{"type":"text","text":"Hi"}],"usage":{"input_tokens":5,"output_tokens":1}}`
	e := newEnv(t, fixed(nil, 200, "application/json", body), setup{})
	resp, got := e.post("/v1/messages", `{"model":"claude-test","max_tokens":5,"messages":[]}`)
	if resp.StatusCode != http.StatusOK || got != body {
		t.Fatalf("status %d, body %q", resp.StatusCode, got)
	}
	line := e.logLine(resp.Header.Get("X-Request-Id"))
	if line["input_tokens"] != 5.0 || line["output_tokens"] != 1.0 || line["stream"] != false {
		t.Errorf("log = %v", line)
	}
}

func TestProxyEmptyResponse(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNoContent, http.StatusTooManyRequests} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			e := newEnv(t, fixed(nil, status, "application/json", "", "Request-Id", "empty-response"), setup{})
			resp, got := e.post("/v1/messages", `{"model":"claude-test","max_tokens":5,"messages":[]}`)
			if resp.StatusCode != status || got != "" || resp.Header.Get("Request-Id") != "empty-response" {
				t.Fatalf("status %d, headers %v, body %q", resp.StatusCode, resp.Header, got)
			}
		})
	}
}

func TestProxyNoUsageIsEstimated(t *testing.T) {
	stream := "data: {\"choices\":[]}\n\ndata: [DONE]\n\n"
	e := newEnv(t, fixed(nil, 200, "text/event-stream", stream), setup{})
	req := `{"model":"gpt-test","stream":true,"messages":[]}`
	resp, _ := e.post("/v1/chat/completions", req)
	line := e.logLine(resp.Header.Get("X-Request-Id"))
	// The upstream body has the added stream_options, so it is longer.
	wantTokens := float64((len(req) + len(`"stream_options":{"include_usage":true},`) + 3) / 4)
	if line["usage_estimated"] != true || line["input_tokens"] != wantTokens {
		t.Errorf("log = %v, want estimated usage with %v input tokens", line, wantTokens)
	}
}

func TestCountTokens(t *testing.T) {
	var rec upstreamRecord
	e := newEnv(t, fixed(&rec, 200, "application/json", `{"input_tokens":42}`), setup{})
	resp, got := e.post("/v1/messages/count_tokens", `{"model":"claude-test","messages":[]}`)
	if resp.StatusCode != http.StatusOK || got != `{"input_tokens":42}` {
		t.Fatalf("status %d, body %q", resp.StatusCode, got)
	}
	if path, _, _, _ := rec.get(); path != "/v1/messages/count_tokens" {
		t.Errorf("upstream path = %q", path)
	}
	if line := e.logLine(resp.Header.Get("X-Request-Id")); line["input_tokens"] != nil {
		t.Errorf("count_tokens has usage in the log: %v", line)
	}
}

func TestUpstreamErrorPassesThrough(t *testing.T) {
	errBody := `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`
	e := newEnv(t, fixed(nil, 429, "application/json", errBody, "Retry-After", "7"), setup{})
	resp, got := e.post("/v1/messages", `{"model":"claude-test","max_tokens":5,"messages":[],"stream":true}`)
	if resp.StatusCode != http.StatusTooManyRequests || got != errBody || resp.Header.Get("Retry-After") != "7" {
		t.Errorf("status %d, retry-after %q, body %q", resp.StatusCode, resp.Header.Get("Retry-After"), got)
	}
	if line := e.logLine(resp.Header.Get("X-Request-Id")); line["input_tokens"] != nil {
		t.Errorf("an error response has usage in the log: %v", line)
	}
}

func TestGatewayErrors(t *testing.T) {
	e := newEnv(t, fixed(nil, 200, "application/json", "{}"), setup{maxBody: "200B"})
	tests := []struct {
		name       string
		path       string
		body       string
		header     map[string]string
		wantStatus int
		wantBody   string
	}{
		{"no key anthropic", "/v1/messages", `{}`, map[string]string{"X-Api-Key": ""}, 401, `{"type":"error","error":{"type":"authentication_error","message":"The request has no API key.`},
		{"bad key openai", "/v1/chat/completions", `{}`, map[string]string{"Authorization": "Bearer tg_wrong"}, 401, `{"error":{"message":"The API key is not valid.`},
		{"not JSON", "/v1/messages", `{"model":`, nil, 400, `{"type":"error","error":{"type":"invalid_request_error","message":"The request body is not valid JSON.`},
		{"no model", "/v1/chat/completions", `{"messages":[]}`, nil, 400, `{"error":{"message":"The model field is missing`},
		{"model not allowed", "/v1/chat/completions", `{"model":"not-for-personal"}`, nil, 403, `{"error":{"message":"The key cannot use the model \"not-for-personal\".`},
		{"model of other API", "/v1/chat/completions", `{"model":"claude-test"}`, nil, 404, `{"error":{"message":"The model \"claude-test\" has no openai route.`},
		{"too large", "/v1/messages", `{"model":"claude-test","x":"` + strings.Repeat("x", 300) + `"}`, nil, 413, `{"type":"error","error":{"type":"request_too_large"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := e.request(t.Context(), tt.path, tt.body)
			for k, v := range tt.header {
				if v == "" {
					req.Header.Del(k)
				} else {
					req.Header.Set(k, v)
				}
			}
			resp, got := e.do(req)
			if resp.StatusCode != tt.wantStatus || !strings.HasPrefix(got, tt.wantBody) {
				t.Errorf("status %d, body %s\nwant %d, %s", resp.StatusCode, got, tt.wantStatus, tt.wantBody)
			}
			if resp.Header.Get("X-Request-Id") == "" {
				t.Error("no x-request-id")
			}
			if line := e.logLine(resp.Header.Get("X-Request-Id")); line["status"] != float64(tt.wantStatus) {
				t.Errorf("log status = %v", line["status"])
			}
		})
	}
}

func TestNotFoundAndHealth(t *testing.T) {
	e := newEnv(t, fixed(nil, 200, "application/json", "{}"), setup{})
	for _, tt := range []struct {
		method, path string
		want         int
		wantBody     string
	}{
		{"GET", "/healthz", 200, `{"status":"ok"}`},
		{"GET", "/readyz", 200, `{"status":"ready"}`},
		{"GET", "/v1/messages", 404, `{"error":{"message":"Tollgate has no endpoint for GET /v1/messages.`},
		{"POST", "/v1/embeddings", 404, `{"error":{"message":"Tollgate has no endpoint for POST /v1/embeddings.`},
	} {
		req, _ := http.NewRequestWithContext(t.Context(), tt.method, e.url+tt.path, nil)
		resp, got := e.do(req)
		if resp.StatusCode != tt.want || !strings.HasPrefix(got, tt.wantBody) {
			t.Errorf("%s %s: status %d, body %s", tt.method, tt.path, resp.StatusCode, got)
		}
	}
}

func TestUpstreamDown(t *testing.T) {
	e := newEnv(t, fixed(nil, 200, "application/json", "{}"), setup{})
	e.upstream.Close()
	resp, got := e.post("/v1/messages", `{"model":"claude-test","max_tokens":5,"messages":[]}`)
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(got, `Tollgate cannot send the request to the provider \"anthropic\"`) {
		t.Errorf("status %d, body %s", resp.StatusCode, got)
	}
}

func TestFirstTokenTimeoutGives502(t *testing.T) {
	// The handler reads the body first. Without that, the server does not
	// see that the connection closed, and the test server cannot stop.
	e := newEnv(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}),
		setup{timeouts: "{connect: 1s, first_token: 100ms, idle: 1s, total: 5s}"})
	resp, got := e.post("/v1/messages", `{"model":"claude-test","max_tokens":5,"messages":[]}`)
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(got, "first_token timeout") {
		t.Errorf("status %d, body %s", resp.StatusCode, got)
	}
}

func TestFirstTokenTimeoutAfterHeadersGives502(t *testing.T) {
	for _, tt := range []struct {
		name, path, body, contentType string
	}{
		{"anthropic stream", "/v1/messages", `{"model":"claude-test","stream":true,"messages":[]}`, "text/event-stream"},
		{"openai JSON", "/v1/chat/completions", `{"model":"gpt-test","messages":[]}`, "application/json"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", tt.contentType)
				w.Header().Set("Content-Length", "1000")
				w.Header().Set("Content-Encoding", "identity")
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}), setup{timeouts: "{connect: 1s, first_token: 100ms, idle: 1s, total: 5s}"})
			resp, got := e.post(tt.path, tt.body)
			if resp.StatusCode != http.StatusBadGateway || !strings.Contains(got, "first_token timeout") {
				t.Errorf("status %d, body %s; want a 502 timeout error", resp.StatusCode, got)
			}
			if resp.Header.Get("Content-Type") != "application/json" || resp.Header.Get("Content-Encoding") != "" {
				t.Errorf("gateway error has upstream headers: %v", resp.Header)
			}
			if line := e.logLine(resp.Header.Get("X-Request-Id")); line["status"] != 502.0 {
				t.Errorf("log status = %v, want 502", line["status"])
			}
		})
	}
}

// stallAfter returns a handler that sends first and then waits until the
// request ends. It closes done when the upstream request context ends.
func stallAfter(first string, done chan<- struct{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, first)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		if done != nil {
			close(done)
		}
	}
}

func TestClientDisconnectCancelsUpstream(t *testing.T) {
	upstreamDone := make(chan struct{})
	first := "event: message_start\n" +
		`data: {"type":"message_start","message":{"usage":{"input_tokens":30,"output_tokens":1}}}` + "\n\n"
	e := newEnv(t, stallAfter(first, upstreamDone), setup{timeouts: "{connect: 1s, first_token: 5s, idle: 30s, total: 60s}"})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	resp, err := http.DefaultClient.Do(e.request(ctx, "/v1/messages", `{"model":"claude-test","max_tokens":5,"stream":true,"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, len(first))
	if _, err := io.ReadFull(resp.Body, buf); err != nil || string(buf) != first {
		t.Fatalf("first event = %q, err %v", buf, err)
	}
	cancel()

	select {
	case <-upstreamDone:
	case <-time.After(3 * time.Second):
		t.Fatal("the client disconnected, but the upstream request continued")
	}
	line := e.logLine(resp.Header.Get("X-Request-Id"))
	if line["input_tokens"] != 30.0 || line["usage_estimated"] != false || line["error"] == nil {
		t.Errorf("log = %v, want the usage received before the disconnect and an error", line)
	}
}

func TestIdleTimeoutSendsErrorEvent(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		body  string
		first string
		want  string
	}{
		{
			name:  "anthropic at boundary",
			path:  "/v1/messages",
			body:  `{"model":"claude-test","max_tokens":5,"stream":true,"messages":[]}`,
			first: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3}}}\n\n",
			want:  "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"The provider \\\"anthropic\\\" sent no data for 150ms (idle timeout). Tollgate stopped the response.\"}}\n\n",
		},
		{
			name:  "openai inside an event",
			path:  "/v1/chat/completions",
			body:  `{"model":"gpt-test","stream":true,"messages":[]}`,
			first: "data: {\"choices\":[]}\n\ndata: {\"cho",
			want:  "\n\ndata: {\"error\":{\"message\":\"The provider \\\"openai\\\" sent no data for 150ms (idle timeout). Tollgate stopped the response.\",\"type\":\"server_error\",\"param\":null,\"code\":\"gateway_error\"}}\n\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, stallAfter(tt.first, nil), setup{timeouts: "{connect: 1s, first_token: 1s, idle: 150ms, total: 5s}"})
			resp, got := e.post(tt.path, tt.body)
			if resp.StatusCode != http.StatusOK || got != tt.first+tt.want {
				t.Fatalf("status %d, body:\n%q\nwant:\n%q", resp.StatusCode, got, tt.first+tt.want)
			}
			line := e.logLine(resp.Header.Get("X-Request-Id"))
			if !strings.Contains(fmt.Sprint(line["error"]), "idle timeout") {
				t.Errorf("log error = %v", line["error"])
			}
		})
	}
}

func TestDrain(t *testing.T) {
	release := make(chan struct{})
	first := "event: message_start\ndata: {}\n\n"
	rest := "event: message_stop\ndata: {}\n\n"
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, first)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, rest)
	})
	e := newEnv(t, upstream, setup{drain: "10s", timeouts: "{connect: 1s, first_token: 5s, idle: 30s, total: 60s}"})

	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e.url = "http://" + ln.Addr().String()
	ctx, stop := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- e.srv.Serve(ctx, ln, nil) }()

	resp, err := http.DefaultClient.Do(e.request(t.Context(), "/v1/messages", `{"model":"claude-test","max_tokens":5,"stream":true,"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, len(first))
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatal(err)
	}

	stop()
	waitFor(t, func() bool { return e.srv.tracker.isDraining() })

	// A new connection: the listener stays open during the drain.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	readyReq, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, e.url+"/readyz", nil)
	ready, err := client.Do(readyReq)
	if err != nil {
		t.Fatal(err)
	}
	ready.Body.Close()
	if ready.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("/readyz = %d during the drain, want 503", ready.StatusCode)
	}
	newResp, err := client.Do(e.request(t.Context(), "/v1/messages", `{"model":"claude-test","max_tokens":5,"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	newBody, _ := io.ReadAll(newResp.Body)
	newResp.Body.Close()
	if newResp.StatusCode != http.StatusServiceUnavailable || newResp.Header.Get("Retry-After") == "" || !strings.Contains(string(newBody), "Tollgate is stopping") {
		t.Errorf("new request during the drain: status %d, retry-after %q, body %s", newResp.StatusCode, newResp.Header.Get("Retry-After"), newBody)
	}

	close(release)
	restGot, err := io.ReadAll(resp.Body)
	if err != nil || string(restGot) != rest {
		t.Errorf("rest of the open stream = %q, err %v; want the stream to finish", restGot, err)
	}
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after the drain")
	}
}

func TestDrainTimeoutCancelsStreams(t *testing.T) {
	upstreamDone := make(chan struct{})
	e := newEnv(t, stallAfter("event: a\ndata: {}\n\n", upstreamDone), setup{drain: "200ms", timeouts: "{connect: 1s, first_token: 5s, idle: 30s, total: 60s}"})
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e.url = "http://" + ln.Addr().String()
	ctx, stop := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- e.srv.Serve(ctx, ln, nil) }()

	resp, err := http.DefaultClient.Do(e.request(t.Context(), "/v1/messages", `{"model":"claude-test","max_tokens":5,"stream":true,"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	stop()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after the drain timeout")
	}
	select {
	case <-upstreamDone:
	case <-time.After(3 * time.Second):
		t.Fatal("the drain timeout did not cancel the upstream request")
	}
}

func TestDrainForce(t *testing.T) {
	e := newEnv(t, stallAfter("event: a\ndata: {}\n\n", nil), setup{drain: "1h", timeouts: "{connect: 1s, first_token: 5s, idle: 30s, total: 60s}"})
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e.url = "http://" + ln.Addr().String()
	ctx, stop := context.WithCancel(t.Context())
	force := make(chan struct{})
	served := make(chan error, 1)
	go func() { served <- e.srv.Serve(ctx, ln, force) }()

	resp, err := http.DefaultClient.Do(e.request(t.Context(), "/v1/messages", `{"model":"claude-test","max_tokens":5,"stream":true,"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	stop()
	waitFor(t, func() bool { return e.srv.tracker.isDraining() })
	close(force)
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after force")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not true after 3 s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
