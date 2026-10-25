package anthropic

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ykakade/tollgate/internal/provider"
)

func TestStreamMeter(t *testing.T) {
	stream := "event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"m","usage":{"input_tokens":10,"cache_creation_input_tokens":20,"cache_read_input_tokens":30,"output_tokens":1}}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"usage"}}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{},"usage":{"input_tokens":11,"output_tokens":7}}` + "\n\n"
	m := NewStreamMeter()
	for i := range len(stream) {
		_, _ = m.Write([]byte{stream[i]})
	}
	got, ok := m.Usage()
	want := provider.Usage{InputTokens: 11, OutputTokens: 7, CacheReadTokens: 30, CacheWriteTokens: 20}
	if !ok || got != want {
		t.Errorf("Usage() = %+v, %v, want %+v", got, ok, want)
	}
	if !m.AtBoundary() {
		t.Error("AtBoundary() = false at the end of an event")
	}
	_, _ = m.Write([]byte("event: x\n"))
	if m.AtBoundary() {
		t.Error("AtBoundary() = true in an event")
	}
}

func TestStreamMeterNoUsage(t *testing.T) {
	m := NewStreamMeter()
	_, _ = m.Write([]byte("event: message_start\ndata: {not json}\n\nevent: ping\ndata: {}\n\n"))
	if _, ok := m.Usage(); ok {
		t.Error("Usage() ok = true, want false")
	}
}

func TestBodyMeter(t *testing.T) {
	m := NewBodyMeter()
	_, _ = m.Write([]byte(`{"id":"m","content":[],"usage":{"input_tokens":3,"cache_read_input_tokens":4,"output_tokens":2}}`))
	got, ok := m.Usage()
	if want := (provider.Usage{InputTokens: 3, CacheReadTokens: 4, OutputTokens: 2}); !ok || got != want {
		t.Errorf("Usage() = %+v, %v, want %+v", got, ok, want)
	}
	m = NewBodyMeter()
	_, _ = m.Write([]byte(`{"usage":{"input_tokens":1,"cache_creation_input_tokens":9,"cache_creation":{"ephemeral_5m_input_tokens":3,"ephemeral_1h_input_tokens":6},"output_tokens":2}}`))
	got, ok = m.Usage()
	if want := (provider.Usage{InputTokens: 1, CacheWriteTokens: 9, CacheWrite1hTokens: 6, OutputTokens: 2}); !ok || got != want {
		t.Errorf("Usage() with 1-hour cache writes = %+v, %v, want %+v", got, ok, want)
	}
	m = NewBodyMeter()
	_, _ = m.Write([]byte(`{"input_tokens":3}`))
	if _, ok := m.Usage(); ok {
		t.Error("Usage() of a body without usage: ok = true")
	}
}

func TestNewSetsHeaders(t *testing.T) {
	var got http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Header }))
	defer upstream.Close()
	for _, tt := range []struct {
		name        string
		key         string
		version     string
		wantKey     string
		wantVersion string
	}{
		{"key and default version", "sk-ant", "", "sk-ant", DefaultVersion},
		{"client version kept", "sk-ant", "2099-01-01", "sk-ant", "2099-01-01"},
		{"no key", "", "", "", DefaultVersion},
	} {
		a, err := New(upstream.URL, tt.key, nil)
		if err != nil {
			t.Fatal(err)
		}
		h := http.Header{"X-Api-Key": {"tg_client"}}
		if tt.version != "" {
			h.Set("Anthropic-Version", tt.version)
		}
		resp, err := a.Send(t.Context(), &provider.Request{Path: "/v1/messages", Header: h})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got.Get("X-Api-Key") != tt.wantKey || got.Get("Anthropic-Version") != tt.wantVersion {
			t.Errorf("%s: x-api-key %q, anthropic-version %q", tt.name, got.Get("X-Api-Key"), got.Get("Anthropic-Version"))
		}
	}
}

func TestStreamMeterMalformed(t *testing.T) {
	m := NewStreamMeter()
	ok := "event: ping\ndata: {\"type\":\"ping\"}\n\n"
	if _, err := m.Write([]byte(ok)); err != nil {
		t.Fatalf("Write(ping) error = %v", err)
	}
	_, err := m.Write([]byte("event: content_block_delta\ndata: {\"type\":\n\n"))
	if !errors.Is(err, provider.ErrMalformed) {
		t.Errorf("Write error = %v, want ErrMalformed", err)
	}
	m = NewStreamMeter()
	if _, err := m.Write([]byte("data: " + strings.Repeat("x", maxEventBytes+1))); !errors.Is(err, provider.ErrMalformed) {
		t.Errorf("large event: Write error = %v, want ErrMalformed", err)
	}
}

func TestMeterModel(t *testing.T) {
	m := NewStreamMeter()
	if got := m.Model(); got != "" {
		t.Errorf("model before message_start = %q", got)
	}
	_, _ = m.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-x\"}}\n\n"))
	if got := m.Model(); got != "claude-x" {
		t.Errorf("stream model = %q, want claude-x", got)
	}
	b := NewBodyMeter()
	_, _ = b.Write([]byte(`{"model":"claude-y","usage":{"input_tokens":1}}`))
	if got := b.Model(); got != "claude-y" {
		t.Errorf("body model = %q, want claude-y", got)
	}
}
