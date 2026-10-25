package openai

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ykakade/tollgate/internal/provider"
)

func TestStreamMeter(t *testing.T) {
	stream := `data: {"id":"c","choices":[{"index":0,"delta":{"content":"the usage"}}],"usage":null}` + "\n\n" +
		`data: {"id":"c","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_tokens_details":{"cached_tokens":64},"completion_tokens_details":{"reasoning_tokens":8}}}` + "\n\n" +
		"data: [DONE]\n\n"
	m := NewStreamMeter()
	for i := 0; i < len(stream); i += 7 {
		_, _ = m.Write([]byte(stream[i:min(i+7, len(stream))]))
	}
	got, ok := m.Usage()
	want := provider.Usage{InputTokens: 36, OutputTokens: 20, CacheReadTokens: 64, ReasoningTokens: 8}
	if !ok || got != want {
		t.Errorf("Usage() = %+v, %v, want %+v", got, ok, want)
	}
	if !m.AtBoundary() {
		t.Error("AtBoundary() = false after [DONE]")
	}
}

func TestStreamMeterNoUsage(t *testing.T) {
	m := NewStreamMeter()
	_, _ = m.Write([]byte(`data: {"choices":[],"usage":null}` + "\n\ndata: [DONE]\n\n"))
	if _, ok := m.Usage(); ok {
		t.Error("Usage() ok = true, want false")
	}
}

func TestBodyMeter(t *testing.T) {
	m := NewBodyMeter()
	_, _ = m.Write([]byte(`{"id":"c","usage":{"prompt_tokens":9,"completion_tokens":3,"total_tokens":12}}`))
	got, ok := m.Usage()
	if want := (provider.Usage{InputTokens: 9, OutputTokens: 3}); !ok || got != want {
		t.Errorf("Usage() = %+v, %v, want %+v", got, ok, want)
	}
}

func TestNewSetsBearer(t *testing.T) {
	var got http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Header }))
	defer upstream.Close()
	for key, want := range map[string]string{"sk-x": "Bearer sk-x", "": ""} {
		a, err := New(upstream.URL, key, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := a.Send(t.Context(), &provider.Request{Path: "/v1/chat/completions", Header: http.Header{"Authorization": {"Bearer tg_client"}}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got.Get("Authorization") != want {
			t.Errorf("key %q: Authorization = %q, want %q", key, got.Get("Authorization"), want)
		}
	}
}

func TestStreamMeterMalformed(t *testing.T) {
	for _, tt := range []struct{ name, stream string }{
		{"data not json", "data: {\"id\":\"c\"}\n\ndata: {\"id\":\n\n"},
		{"event too large", "data: " + strings.Repeat("x", maxEventBytes+1)},
	} {
		m := NewStreamMeter()
		_, err := m.Write([]byte(tt.stream))
		if !errors.Is(err, provider.ErrMalformed) {
			t.Errorf("%s: Write error = %v, want ErrMalformed", tt.name, err)
		}
		if _, err := m.Write([]byte("data: [DONE]\n\n")); !errors.Is(err, provider.ErrMalformed) {
			t.Errorf("%s: Write after the error = %v, want ErrMalformed again", tt.name, err)
		}
	}
	m := NewStreamMeter()
	if _, err := m.Write([]byte("data: [DONE]\n\n: comment\n\n")); err != nil {
		t.Errorf("Write([DONE]) error = %v", err)
	}
}

func TestMeterModel(t *testing.T) {
	m := NewStreamMeter()
	_, _ = m.Write([]byte("data: {\"model\":\"gpt-x\",\"choices\":[]}\n\n"))
	if got := m.Model(); got != "" {
		t.Errorf("model before the usage chunk = %q, want empty", got)
	}
	_, _ = m.Write([]byte("data: {\"model\":\"gpt-x\",\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2}}\n\n"))
	if got := m.Model(); got != "gpt-x" {
		t.Errorf("stream model = %q, want gpt-x", got)
	}
}
