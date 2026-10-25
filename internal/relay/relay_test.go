package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var shortTimeouts = Timeouts{
	Connect:    200 * time.Millisecond,
	FirstToken: 200 * time.Millisecond,
	Idle:       200 * time.Millisecond,
	Total:      5 * time.Second,
}

// send starts an attempt and sends a GET request to url with it. The
// caller closes the response body.
func send(ctx context.Context, t *testing.T, rt http.RoundTripper, url string, to Timeouts) (*Attempt, *http.Response, error) {
	t.Helper()
	a := Start(ctx, to)
	t.Cleanup(a.Stop)
	req, err := http.NewRequestWithContext(a.Context(), http.MethodGet, url, nil) //nolint:contextcheck // The attempt context derives from ctx.
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	return a, resp, err
}

// handler returns a server handler that writes each part with a flush and
// waits for the given delay before each part.
func handler(delay time.Duration, parts ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		for _, p := range parts {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
			_, _ = io.WriteString(w, p)
			w.(http.Flusher).Flush()
		}
	}
}

func TestCopy(t *testing.T) {
	parts := []string{"event: a\ndata: 1\n\n", "event: b\n", "data: 2\n\n"}
	upstream := httptest.NewServer(handler(10*time.Millisecond, parts...))
	defer upstream.Close()

	a, resp, err := send(t.Context(), t, http.DefaultTransport, upstream.URL, shortTimeouts)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rec := httptest.NewRecorder()
	var meter bytes.Buffer
	n, err := a.Copy(rec, resp.Body, &meter)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(parts, "")
	if rec.Body.String() != want || meter.String() != want || n != int64(len(want)) {
		t.Errorf("client got %q, meter got %q, n = %d; want %q", rec.Body.String(), meter.String(), n, want)
	}
	if !rec.Flushed {
		t.Error("Copy did not flush")
	}
	if a.FirstByte().IsZero() {
		t.Error("FirstByte() is zero")
	}
	if err := a.Err(); err != nil {
		t.Errorf("Err() = %v, want nil", err)
	}
	a.Stop()
	if err := a.Err(); err != nil {
		t.Errorf("Err() after Stop = %v, want nil", err)
	}
}

func TestConnectTimeout(t *testing.T) {
	rt := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	start := time.Now()
	a, resp, err := send(t.Context(), t, rt, "http://example.test", shortTimeouts)
	if err == nil {
		resp.Body.Close()
		t.Fatal("RoundTrip err = nil, want an error")
	}
	if !errors.Is(a.Err(), ErrConnectTimeout) {
		t.Errorf("Err() = %v, want ErrConnectTimeout", a.Err())
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("timeout took %v", d)
	}
}

func TestStaleTimeoutCallback(t *testing.T) {
	a := Start(t.Context(), Timeouts{Connect: time.Hour, Total: time.Hour})
	defer a.Stop()
	// Simulate a callback that was queued before the timer was paused,
	// reset, or stopped. AfterFunc.Stop does not wait for these callbacks.
	a.pause()
	a.fire()
	if err := a.Err(); err != nil {
		t.Fatalf("paused timer canceled the attempt: %v", err)
	}
	a.arm(time.Hour, ErrIdleTimeout)
	a.fire()
	if err := a.Err(); err != nil {
		t.Fatalf("old callback canceled the next read: %v", err)
	}
	a.Stop()
	a.fire()
	if err := a.Err(); err != nil {
		t.Fatalf("stopped timer canceled the attempt: %v", err)
	}
}

func TestFirstTokenTimeoutBeforeHeaders(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	defer close(release)

	a, resp, err := send(t.Context(), t, http.DefaultTransport, upstream.URL, shortTimeouts)
	if err == nil {
		resp.Body.Close()
		t.Fatal("RoundTrip err = nil, want an error")
	}
	if !errors.Is(a.Err(), ErrFirstTokenTimeout) {
		t.Errorf("Err() = %v, want ErrFirstTokenTimeout", a.Err())
	}
}

func TestFirstTokenTimeoutAfterHeaders(t *testing.T) {
	upstream := httptest.NewServer(handler(time.Hour, "never"))
	defer upstream.Close()

	a, resp, err := send(t.Context(), t, http.DefaultTransport, upstream.URL, shortTimeouts)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, err = a.Copy(httptest.NewRecorder(), resp.Body, nil)
	if !errors.Is(err, ErrFirstTokenTimeout) {
		t.Errorf("Copy err = %v, want ErrFirstTokenTimeout", err)
	}
}

func TestIdleTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "data: 1\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()

	a, resp, err := send(t.Context(), t, http.DefaultTransport, upstream.URL, shortTimeouts)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rec := httptest.NewRecorder()
	n, err := a.Copy(rec, resp.Body, nil)
	if !errors.Is(err, ErrIdleTimeout) {
		t.Errorf("Copy err = %v, want ErrIdleTimeout", err)
	}
	if rec.Body.String() != "data: 1\n\n" || n != 9 {
		t.Errorf("client got %q (n = %d), want the first event", rec.Body.String(), n)
	}
}

func TestIdleTimerResetsOnData(t *testing.T) {
	// Each part comes after 120 ms. That is less than the idle timeout, but
	// the sum is more than it.
	parts := []string{"a", "b", "c", "d"}
	upstream := httptest.NewServer(handler(120*time.Millisecond, parts...))
	defer upstream.Close()

	a, resp, err := send(t.Context(), t, http.DefaultTransport, upstream.URL, shortTimeouts)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rec := httptest.NewRecorder()
	if _, err := a.Copy(rec, resp.Body, nil); err != nil {
		t.Fatalf("Copy err = %v, want nil", err)
	}
	if rec.Body.String() != "abcd" {
		t.Errorf("client got %q", rec.Body.String())
	}
}

func TestTotalTimeout(t *testing.T) {
	parts := make([]string, 100)
	for i := range parts {
		parts[i] = "x"
	}
	upstream := httptest.NewServer(handler(50*time.Millisecond, parts...))
	defer upstream.Close()

	to := shortTimeouts
	to.Total = 300 * time.Millisecond
	a, resp, err := send(t.Context(), t, http.DefaultTransport, upstream.URL, to)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, err = a.Copy(httptest.NewRecorder(), resp.Body, nil)
	if !errors.Is(err, ErrTotalTimeout) {
		t.Errorf("Copy err = %v, want ErrTotalTimeout", err)
	}
}

// failWriter is a ResponseWriter whose writes fail.
type failWriter struct{ *httptest.ResponseRecorder }

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestClientWriteCancelsUpstream(t *testing.T) {
	upstreamDone := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "data: 1\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(upstreamDone)
	}))
	defer upstream.Close()

	a, resp, err := send(t.Context(), t, http.DefaultTransport, upstream.URL, shortTimeouts)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, err = a.Copy(failWriter{httptest.NewRecorder()}, resp.Body, nil)
	if !errors.Is(err, ErrClientWrite) {
		t.Errorf("Copy err = %v, want ErrClientWrite", err)
	}
	if !errors.Is(a.Err(), ErrClientWrite) {
		t.Errorf("Err() = %v, want ErrClientWrite", a.Err())
	}
	select {
	case <-upstreamDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the upstream request was not canceled")
	}
}

func TestParentCancel(t *testing.T) {
	upstream := httptest.NewServer(handler(time.Hour, "never"))
	defer upstream.Close()

	ctx, cancel := context.WithCancelCause(t.Context())
	a, resp, err := send(ctx, t, http.DefaultTransport, upstream.URL, shortTimeouts)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	gone := errors.New("client gone")
	time.AfterFunc(20*time.Millisecond, func() { cancel(gone) })
	_, err = a.Copy(httptest.NewRecorder(), resp.Body, nil)
	if !errors.Is(err, gone) {
		t.Errorf("Copy err = %v, want the parent cause", err)
	}
}

func TestUpstreamReset(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "part")
		w.(http.Flusher).Flush()
		conn, _, err := http.NewResponseController(w).Hijack()
		if err == nil {
			conn.Close()
		}
	}))
	defer upstream.Close()

	a, resp, err := send(t.Context(), t, http.DefaultTransport, upstream.URL, shortTimeouts)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, err = a.Copy(httptest.NewRecorder(), resp.Body, nil)
	if err == nil || a.Err() != nil {
		t.Errorf("Copy err = %v, Err() = %v; want a read error and no cause", err, a.Err())
	}
}

var errBadStream = errors.New("bad stream")

// failMeter is a meter that fails on the first write.
type failMeter struct{}

func (failMeter) Write([]byte) (int, error) { return 0, errBadStream }

func TestMeterErrorCancelsUpstream(t *testing.T) {
	upstreamDone := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "data: {\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(upstreamDone)
	}))
	defer upstream.Close()

	a, resp, err := send(t.Context(), t, http.DefaultTransport, upstream.URL, shortTimeouts)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rec := httptest.NewRecorder()
	n, err := a.Copy(rec, resp.Body, failMeter{})
	if !errors.Is(err, errBadStream) || n != 9 || rec.Body.String() != "data: {\n\n" {
		t.Errorf("Copy = %d, %v, body %q; want 9, errBadStream and the chunk", n, err, rec.Body.String())
	}
	if !errors.Is(a.Err(), errBadStream) {
		t.Errorf("Err() = %v, want errBadStream", a.Err())
	}
	select {
	case <-upstreamDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the upstream request was not canceled")
	}
}

// endlessBody records how far Copy reads from an unlimited upstream.
type endlessBody struct{ read int }

func (b *endlessBody) Read(p []byte) (int, error) { b.read += len(p); return len(p), nil }

type blockedWriter struct {
	net.Conn
	header http.Header
}

func (w *blockedWriter) Header() http.Header { return w.header }
func (*blockedWriter) WriteHeader(int)       {}
func (*blockedWriter) Flush()                {}

func TestSlowClientBoundsReads(t *testing.T) {
	sender, receiver := net.Pipe()
	defer sender.Close()
	defer receiver.Close()
	a := Start(t.Context(), shortTimeouts)
	defer a.Stop()
	body := &endlessBody{}
	_, err := a.Copy(&blockedWriter{Conn: sender, header: make(http.Header)}, body, nil)
	if !errors.Is(err, ErrClientWrite) || !errors.Is(a.Err(), ErrClientWrite) {
		t.Fatalf("Copy error %v, cause %v", err, a.Err())
	}
	// Backpressure stops reads after one fixed-size buffer, even when the
	// upstream could produce an unlimited body.
	if body.read != bufferSize {
		t.Fatalf("read %d bytes while blocked; want %d", body.read, bufferSize)
	}
}
