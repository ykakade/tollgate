package fakeprovider

import (
	"bufio"
	"bytes"
	"encoding/json/v2"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

// fixedTime is the clock of every test server, so golden files are stable.
var fixedTime = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	fp := New(Config{Now: func() time.Time { return fixedTime }})
	srv := httptest.NewServer(fp.Handler())
	t.Cleanup(srv.Close)
	return srv
}

// post sends body to path and returns the response status, headers and body.
func post(t *testing.T, srv *httptest.Server, path, body string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, got
}

const (
	anthropicBody = `{"model":"claude-test","max_tokens":64,"messages":[{"role":"user","content":"Say hello."}]}`
	openaiBody    = `{"model":"gpt-test","messages":[{"role":"user","content":"Say hello."}]}`
)

func withField(body, field string) string {
	return strings.TrimSuffix(body, "}") + "," + field + "}"
}

func TestGolden(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		body     string
		wantType string
	}{
		{"anthropic", "/v1/messages", anthropicBody, "application/json"},
		{"anthropic_stream", "/v1/messages", withField(anthropicBody, `"stream":true`), "text/event-stream"},
		{"anthropic_max_tokens", "/v1/messages", strings.Replace(anthropicBody, `"max_tokens":64`, `"max_tokens":2`, 1), "application/json"},
		{"openai", "/v1/chat/completions", openaiBody, "application/json"},
		{"openai_stream", "/v1/chat/completions", withField(openaiBody, `"stream":true`), "text/event-stream"},
		{"openai_stream_usage", "/v1/chat/completions", withField(openaiBody, `"stream":true,"stream_options":{"include_usage":true}`), "text/event-stream"},
		{"openai_max_completion_tokens", "/v1/chat/completions", withField(openaiBody, `"max_tokens":5,"max_completion_tokens":2`), "application/json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, header, got := post(t, newTestServer(t), tt.path, tt.body)
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", status, got)
			}
			if ct := header.Get("Content-Type"); ct != tt.wantType {
				t.Errorf("Content-Type = %q, want %q", ct, tt.wantType)
			}
			checkGolden(t, tt.name, got)
		})
	}
}

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden file: %v (run go test -update to write it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("response does not match %s\ngot:\n%s\nwant:\n%s", path, got, want)
	}
}

// event is one parsed server-sent event.
type event struct {
	name string
	data string
}

// parseEvents parses an event stream. It supports only the subset of the
// format that the fake provider writes: event and data lines, and one data
// line per event.
func parseEvents(t *testing.T, stream []byte) []event {
	t.Helper()
	var events []event
	var cur event
	sc := bufio.NewScanner(bytes.NewReader(stream))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			events = append(events, cur)
			cur = event{}
		case strings.HasPrefix(line, "event: "):
			cur.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		default:
			t.Fatalf("unexpected line %q", line)
		}
	}
	if cur != (event{}) {
		t.Fatalf("stream ends without a blank line after the last event")
	}
	return events
}

func TestAnthropicStreamContent(t *testing.T) {
	_, _, body := post(t, newTestServer(t), "/v1/messages", withField(anthropicBody, `"stream":true`))
	events := parseEvents(t, body)

	var names []string
	var text strings.Builder
	var outputTokens int
	for _, ev := range events {
		names = append(names, ev.name)
		var v struct {
			Type  string `json:"type"`
			Delta struct {
				Text string `json:"text"`
			} `json:"delta"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(ev.data), &v); err != nil {
			t.Fatalf("event %q has data that is not valid JSON: %v", ev.name, err)
		}
		if v.Type != ev.name {
			t.Errorf("event %q has type %q, want the same as the event name", ev.name, v.Type)
		}
		switch ev.name {
		case "content_block_delta":
			text.WriteString(v.Delta.Text)
		case "message_delta":
			outputTokens = v.Usage.OutputTokens
		}
	}
	if text.String() != Reply {
		t.Errorf("text = %q, want %q", text.String(), Reply)
	}
	if want := len(splitReply(Reply)); outputTokens != want {
		t.Errorf("output_tokens = %d, want %d", outputTokens, want)
	}
	if names[0] != "message_start" || names[len(names)-1] != "message_stop" {
		t.Errorf("events = %v, want message_start first and message_stop last", names)
	}
}

func TestOpenAIStreamContent(t *testing.T) {
	_, _, body := post(t, newTestServer(t), "/v1/chat/completions", withField(openaiBody, `"stream":true,"stream_options":{"include_usage":true}`))
	events := parseEvents(t, body)
	if last := events[len(events)-1]; last.data != "[DONE]" {
		t.Fatalf("last event = %q, want [DONE]", last.data)
	}
	var text strings.Builder
	var completionTokens int
	for _, ev := range events[:len(events)-1] {
		var v struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(ev.data), &v); err != nil {
			t.Fatalf("chunk is not valid JSON: %v", err)
		}
		for _, c := range v.Choices {
			text.WriteString(c.Delta.Content)
		}
		if v.Usage != nil {
			completionTokens = v.Usage.CompletionTokens
		}
	}
	if text.String() != Reply {
		t.Errorf("text = %q, want %q", text.String(), Reply)
	}
	if want := len(splitReply(Reply)); completionTokens != want {
		t.Errorf("completion_tokens = %d, want %d", completionTokens, want)
	}
}

func TestInputTokens(t *testing.T) {
	tests := []struct {
		body string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"abcd", 1},
		{"abcde", 2},
	}
	for _, tt := range tests {
		if got := inputTokens([]byte(tt.body)); got != tt.want {
			t.Errorf("inputTokens(%q) = %d, want %d", tt.body, got, tt.want)
		}
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		body       string
		wantStatus int
		wantError  string
	}{
		{"anthropic bad json", "/v1/messages", `{`, http.StatusBadRequest, `{"type":"error","error":{"type":"invalid_request_error","message":"The request body is not valid JSON."}}`},
		{"anthropic no model", "/v1/messages", `{"max_tokens":1,"messages":[{}]}`, http.StatusBadRequest, `"message":"The model field is missing.`},
		{"anthropic no max_tokens", "/v1/messages", `{"model":"m","messages":[{}]}`, http.StatusBadRequest, `"message":"The max_tokens field is missing`},
		{"anthropic no messages", "/v1/messages", `{"model":"m","max_tokens":1}`, http.StatusBadRequest, `"message":"The messages field is missing`},
		{"anthropic too large", "/v1/messages", strings.Repeat(" ", MaxBodyBytes+1), http.StatusRequestEntityTooLarge, `"type":"request_too_large"`},
		{"openai bad json", "/v1/chat/completions", `{`, http.StatusBadRequest, `{"error":{"message":"The request body is not valid JSON.","type":"invalid_request_error","param":null,"code":null}}`},
		{"openai no model", "/v1/chat/completions", `{"messages":[{}]}`, http.StatusBadRequest, `"message":"The model field is missing.`},
		{"openai no messages", "/v1/chat/completions", `{"model":"m"}`, http.StatusBadRequest, `"message":"The messages field is missing`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, _, got := post(t, newTestServer(t), tt.path, tt.body)
			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
			if !strings.Contains(string(got), tt.wantError) {
				t.Errorf("body = %s, want it to contain %s", got, tt.wantError)
			}
		})
	}
}

func TestMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/v1/messages", "/v1/chat/completions"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: status = %d, want 405", path, resp.StatusCode)
		}
	}
}

func TestIDsAreUnique(t *testing.T) {
	srv := newTestServer(t)
	seen := map[string]bool{}
	for range 3 {
		_, _, body := post(t, srv, "/v1/messages", anthropicBody)
		var v struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &v); err != nil {
			t.Fatal(err)
		}
		if seen[v.ID] {
			t.Errorf("id %q is used two times", v.ID)
		}
		seen[v.ID] = true
	}
}
