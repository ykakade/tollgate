package fakeprovider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseScenario(t *testing.T) {
	for _, tt := range []struct {
		text string
		want scenario
	}{
		{"", scenario{}},
		{"ok", scenario{}},
		{"status:529", scenario{kind: kindStatus, status: 529}},
		{"status:429:3", scenario{kind: kindStatus, status: 429, retryAfter: 3}},
		{"hang-before-headers", scenario{kind: kindHangBefore}},
		{"hang-after:2-tokens", scenario{kind: kindHangAfter, tokens: 2}},
		{"reset-after:0-tokens", scenario{kind: kindResetAfter}},
		{"malformed-after:1-tokens", scenario{kind: kindMalformedAfter, tokens: 1}},
		{"no-usage", scenario{kind: kindNoUsage}},
	} {
		got, err := parseScenario(tt.text)
		if err != nil || got != tt.want {
			t.Errorf("parseScenario(%q) = %+v, %v; want %+v", tt.text, got, err, tt.want)
		}
	}
	for _, text := range []string{"status", "status:200", "status:429:x", "hang-after:2", "hang-after:x-tokens", "no-usage:1", "explode"} {
		if _, err := parseScenario(text); !errors.Is(err, errScenario) {
			t.Errorf("parseScenario(%q) error = %v, want errScenario", text, err)
		}
	}
}

// postScenario sends body to path with the scenario header.
type scenarioResponse struct {
	StatusCode int
	Header     http.Header
}

func postScenario(ctx context.Context, t *testing.T, srv *httptest.Server, path, body, sc string) (*scenarioResponse, []byte, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(ScenarioHeader, sc)
	resp, err := srv.Client().Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return &scenarioResponse{StatusCode: resp.StatusCode, Header: resp.Header}, b, err
}

func TestScenarioStatus(t *testing.T) {
	srv := newTestServer(t)
	for _, tt := range []struct {
		path, body, sc string
		status         int
		retryAfter     string
		want           string
	}{
		{"/v1/messages", anthropicBody, "status:529", 529, "", `{"type":"error","error":{"type":"overloaded_error","message":"The fake provider returns status 529 on purpose."}}`},
		{"/v1/messages", anthropicBody, "status:429:2", 429, "2", `"type":"rate_limit_error"`},
		{"/v1/messages", anthropicBody, "status:500", 500, "", `"type":"api_error"`},
		{"/v1/chat/completions", openaiBody, "status:503", 503, "", `"type":"server_error"`},
		{"/v1/chat/completions", openaiBody, "status:429", 429, "", `"type":"rate_limit_exceeded"`},
		{"/v1/messages", anthropicBody, "nonsense", 400, "", `is not known`},
	} {
		resp, body, err := postScenario(t.Context(), t, srv, tt.path, tt.body, tt.sc)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != tt.status || resp.Header.Get("Retry-After") != tt.retryAfter || !strings.Contains(string(body), tt.want) {
			t.Errorf("%s: status %d, retry-after %q, body %s", tt.sc, resp.StatusCode, resp.Header.Get("Retry-After"), body)
		}
	}
}

func TestScenarioHangBeforeHeaders(t *testing.T) {
	srv := newTestServer(t)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	_, _, err := postScenario(ctx, t, srv, "/v1/messages", anthropicBody, "hang-before-headers")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want a deadline error", err)
	}
}

func TestScenarioHangAfterTokens(t *testing.T) {
	srv := newTestServer(t)
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	resp, body, err := postScenario(ctx, t, srv, "/v1/messages", withField(anthropicBody, `"stream":true`), "hang-after:2-tokens")
	if resp == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a deadline error after the headers", err)
	}
	if n := strings.Count(string(body), "event: content_block_delta"); n != 2 {
		t.Errorf("got %d token events, want 2:\n%s", n, body)
	}
	// Without a stream, the headers come and then nothing.
	ctx2, cancel2 := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel2()
	resp, body, err = postScenario(ctx2, t, srv, "/v1/chat/completions", openaiBody, "hang-after:0-tokens")
	if resp == nil || resp.StatusCode != http.StatusOK || len(body) != 0 || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("non-stream hang: resp %v, body %q, err %v", resp, body, err)
	}
}

func TestScenarioResetAfterTokens(t *testing.T) {
	srv := newTestServer(t)
	_, body, err := postScenario(t.Context(), t, srv, "/v1/chat/completions", withField(openaiBody, `"stream":true`), "reset-after:3-tokens")
	if err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want a broken connection", err)
	}
	// The first chunk has the role, then 3 token chunks.
	if n := strings.Count(string(body), "data: "); n != 4 {
		t.Errorf("got %d events, want 4:\n%s", n, body)
	}
	if _, _, err := postScenario(t.Context(), t, srv, "/v1/messages", anthropicBody, "reset-after:0-tokens"); err == nil {
		t.Error("non-stream reset: no error")
	}
}

func TestScenarioMalformed(t *testing.T) {
	srv := newTestServer(t)
	_, body, err := postScenario(t.Context(), t, srv, "/v1/messages", withField(anthropicBody, `"stream":true`), "malformed-after:1-tokens")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	bad := "event: content_block_delta\ndata: {\"type\":\n\n"
	i := strings.Index(s, bad)
	if i < 0 || strings.Count(s[:i], "event: content_block_delta") != 1 || !strings.HasSuffix(s, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n") {
		t.Errorf("stream:\n%s", s)
	}
}

func TestScenarioNoUsage(t *testing.T) {
	srv := newTestServer(t)
	for _, tt := range []struct{ path, body string }{
		{"/v1/messages", anthropicBody},
		{"/v1/messages", withField(anthropicBody, `"stream":true`)},
		{"/v1/chat/completions", openaiBody},
		{"/v1/chat/completions", withField(openaiBody, `"stream":true,"stream_options":{"include_usage":true}`)},
	} {
		_, body, err := postScenario(t.Context(), t, srv, tt.path, tt.body, "no-usage")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), `"usage"`) || strings.Contains(string(body), "_tokens") {
			t.Errorf("%s %s: body has usage:\n%s", tt.path, tt.body, body)
		}
	}
}

func TestSetScenarioAndRequests(t *testing.T) {
	fp := New(Config{Scenario: "status:500"})
	srv := httptest.NewServer(fp.Handler())
	defer srv.Close()
	if status, _, _ := post(t, srv, "/v1/messages", anthropicBody); status != 500 {
		t.Errorf("status = %d, want 500 from Config.Scenario", status)
	}
	if err := fp.SetScenario("bad"); err == nil {
		t.Error("SetScenario accepted a bad scenario")
	}
	if err := fp.SetScenario("ok"); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := post(t, srv, "/v1/messages", anthropicBody); status != 200 {
		t.Errorf("status = %d, want 200 after SetScenario", status)
	}
	if fp.Requests() != 2 {
		t.Errorf("Requests() = %d, want 2", fp.Requests())
	}
}

func TestVertex(t *testing.T) {
	srv := newTestServer(t)
	const base = "/v1/projects/p/locations/us-east5/publishers/anthropic/models/"
	send := func(path, body, auth string) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	good := `{"anthropic_version":"vertex-2023-10-16","max_tokens":64,"messages":[{"role":"user","content":"Hi"}]}`
	status, body := send(base+"claude-x@1:rawPredict", good, "Bearer t")
	if status != 200 || !strings.Contains(body, `"model":"claude-x@1"`) || !strings.Contains(body, `"usage"`) {
		t.Errorf("rawPredict: %d %s", status, body)
	}
	status, body = send(base+"claude-x:streamRawPredict", withField(good, `"stream":true`), "Bearer t")
	if status != 200 || !strings.Contains(body, "event: message_stop") {
		t.Errorf("streamRawPredict: %d %s", status, body)
	}
	for _, tt := range []struct {
		name, path, body, auth string
		status                 int
		want                   string
	}{
		{"no token", base + "m:rawPredict", good, "", 401, "no access token"},
		{"model in body", base + "m:rawPredict", withField(good, `"model":"m"`), "Bearer t", 400, "has a model field"},
		{"no version", base + "m:rawPredict", `{"max_tokens":64,"messages":[{"role":"user","content":"Hi"}]}`, "Bearer t", 400, "anthropic_version"},
		{"stream mismatch", base + "m:streamRawPredict", good, "Bearer t", 400, "does not agree"},
		{"bad method", base + "m:predict", good, "Bearer t", 404, "rawPredict"},
	} {
		if status, body := send(tt.path, tt.body, tt.auth); status != tt.status || !strings.Contains(body, tt.want) {
			t.Errorf("%s: %d %s", tt.name, status, body)
		}
	}
}
