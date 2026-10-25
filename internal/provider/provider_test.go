package provider

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestForwardHeader(t *testing.T) {
	in := http.Header{
		"Authorization":       {"Bearer tg_x"},
		"X-Api-Key":           {"tg_x"},
		"Anthropic-Version":   {"2023-06-01"},
		"Anthropic-Beta":      {"a,b"},
		"Connection":          {"keep-alive, X-Remove-Me"},
		"X-Remove-Me":         {"1"},
		"Keep-Alive":          {"timeout=5"},
		"Accept-Encoding":     {"gzip"},
		"Cookie":              {"a=b"},
		"Content-Type":        {"application/json"},
		"X-Tollgate-Session":  {"s1"},
		"User-Agent":          {"claude-cli"},
		"Proxy-Authorization": {"Basic x"},
	}
	want := http.Header{
		"Anthropic-Version": {"2023-06-01"},
		"Anthropic-Beta":    {"a,b"},
		"Content-Type":      {"application/json"},
		"User-Agent":        {"claude-cli"},
	}
	got := ForwardHeader(in)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ForwardHeader differs (-want +got):\n%s", diff)
	}
	if in.Get("Authorization") == "" {
		t.Error("ForwardHeader changed its input")
	}
}

func TestCopyResponseHeader(t *testing.T) {
	src := http.Header{
		"Content-Type":   {"text/event-stream"},
		"Request-Id":     {"req_1"},
		"Connection":     {"close, X-Hop"},
		"X-Hop":          {"1"},
		"Set-Cookie":     {"__cf=1"},
		"Content-Length": {"10"},
	}
	dst := http.Header{"X-Request-Id": {"tg"}}
	CopyResponseHeader(dst, src)
	want := http.Header{
		"X-Request-Id":   {"tg"},
		"Content-Type":   {"text/event-stream"},
		"Request-Id":     {"req_1"},
		"Content-Length": {"10"},
	}
	if diff := cmp.Diff(want, dst); diff != "" {
		t.Errorf("CopyResponseHeader differs (-want +got):\n%s", diff)
	}
}

func TestHTTPAdapterSend(t *testing.T) {
	var got *http.Request
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		gotBody, _ = io.ReadAll(r.Body)
		if r.URL.Query().Get("redirect") == "1" {
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	a, err := NewHTTPAdapter(upstream.URL+"/prefix/", func(h http.Header) { h.Set("X-Api-Key", "provider-key") }, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a.Send(t.Context(), &Request{
		Path:     "/v1/messages",
		RawQuery: "beta=true",
		Header:   http.Header{"X-Api-Key": {"tg_client"}, "Anthropic-Beta": {"x"}},
		Body:     []byte(`{"model":"m"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got.URL.Path != "/prefix/v1/messages" || got.URL.RawQuery != "beta=true" {
		t.Errorf("upstream URL = %s, want /prefix/v1/messages?beta=true", got.URL)
	}
	if got.Header.Get("X-Api-Key") != "provider-key" || got.Header.Get("Anthropic-Beta") != "x" {
		t.Errorf("upstream headers = %v", got.Header)
	}
	if string(gotBody) != `{"model":"m"}` || got.ContentLength != int64(len(gotBody)) {
		t.Errorf("upstream body = %q, length %d", gotBody, got.ContentLength)
	}

	resp, err = a.Send(t.Context(), &Request{Path: "/v1/messages", RawQuery: "redirect=1", Header: http.Header{}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want 302: the adapter must not follow redirects", resp.StatusCode)
	}
}

func TestBodyMeter(t *testing.T) {
	calls := 0
	parse := func(b []byte) (Usage, bool) {
		calls++
		return Usage{InputTokens: int64(len(b))}, true
	}
	m := NewBodyMeter(10, parse)
	_, _ = m.Write([]byte("12345"))
	_, _ = m.Write([]byte("678"))
	if u, ok := m.Usage(); !ok || u.InputTokens != 8 {
		t.Errorf("Usage() = %v, %v, want 8 bytes", u, ok)
	}
	_, _ = m.Write([]byte("too much"))
	if _, ok := m.Usage(); ok {
		t.Error("Usage() after the limit = ok, want no usage")
	}
	if _, ok := NewBodyMeter(10, parse).Usage(); ok {
		t.Error("Usage() of an empty body = ok, want no usage")
	}
}

func TestBodyMeterModel(t *testing.T) {
	parse := func([]byte) (Usage, bool) { return Usage{}, true }
	for _, tt := range []struct {
		name  string
		limit int
		body  string
		want  string
	}{
		{"model", 100, `{"model":"m1"}`, "m1"},
		{"empty", 100, "", ""},
		{"not json", 100, "{", ""},
		{"over limit", 4, `{"model":"m1"}`, ""},
	} {
		m := NewBodyMeter(tt.limit, parse)
		_, _ = m.Write([]byte(tt.body))
		if got := m.Model(); got != tt.want {
			t.Errorf("%s: Model() = %q, want %q", tt.name, got, tt.want)
		}
	}
}
