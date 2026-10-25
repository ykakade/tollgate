package admin

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var metrics = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	_, _ = io.WriteString(w, "tollgate_active_streams 0\n")
})

func get(t *testing.T, h http.Handler, path, token string) (int, string) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestHandler(t *testing.T) {
	h := Handler(Options{Metrics: metrics})
	if code, body := get(t, h, "/metrics", ""); code != 200 || !strings.Contains(body, "tollgate_active_streams") {
		t.Errorf("/metrics: %d %q", code, body)
	}
	if code, body := get(t, h, "/debug/pprof/", ""); code != 404 || !strings.Contains(body, "Set server.pprof to true") {
		t.Errorf("pprof off: %d %q", code, body)
	}
	if code, body := get(t, h, "/other", ""); code != 404 || !strings.Contains(body, "Use GET /metrics") {
		t.Errorf("/other: %d %q", code, body)
	}
}

func TestPProf(t *testing.T) {
	h := Handler(Options{Metrics: metrics, PProf: true})
	if code, body := get(t, h, "/debug/pprof/", ""); code != 200 || !strings.Contains(body, "goroutine") {
		t.Errorf("pprof index: %d", code)
	}
	if code, _ := get(t, h, "/debug/pprof/goroutine?debug=1", ""); code != 200 {
		t.Errorf("goroutine profile: %d", code)
	}
	if code, _ := get(t, h, "/debug/pprof/cmdline", ""); code != 200 {
		t.Errorf("cmdline: %d", code)
	}
}

func TestToken(t *testing.T) {
	h := Handler(Options{Metrics: metrics, Token: "s3cret"})
	for _, tok := range []string{"", "wrong", "s3cret-longer"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 401 || rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("token %q: status %d, want 401 with WWW-Authenticate", tok, rec.Code)
		}
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Basic s3cret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("basic auth: status %d, want 401", rec.Code)
	}
	if code, _ := get(t, h, "/metrics", "s3cret"); code != 200 {
		t.Errorf("correct token: status %d, want 200", code)
	}
}

func TestServe(t *testing.T) {
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, Handler(Options{Metrics: metrics}), slog.New(slog.DiscardHandler)) }()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+ln.Addr().String()+"/metrics", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status %d, want 200", resp.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
	}
}

func TestServeError(t *testing.T) {
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()
	if err := Serve(t.Context(), ln, http.NotFoundHandler(), slog.New(slog.DiscardHandler)); err == nil {
		t.Error("Serve on a closed listener returned nil")
	}
}
