// Package admin serves the admin listener of Tollgate: the Prometheus
// metrics and, when the operator turns them on, the net/http/pprof
// profiles (ADR 0025).
//
// The admin listener is separate from the data plane, so clients with a
// Tollgate key cannot read metrics or profiles. When the listener is not on
// a loopback address, each request needs the admin token.
package admin

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"
)

// Server settings.
const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 2 * time.Minute
	shutdownTimeout   = 5 * time.Second
)

// Options are the inputs of Handler.
type Options struct {
	// Metrics is the handler of /metrics.
	Metrics http.Handler
	// Token, if not empty, is the bearer token that each request needs.
	Token string
	// PProf adds the net/http/pprof handlers under /debug/pprof/.
	PProf bool
}

// Handler returns the handler of the admin listener.
func Handler(opts Options) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", opts.Metrics)
	if opts.PProf {
		mux.HandleFunc("GET /debug/pprof/", pprof.Index)
		mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		msg := "The admin port has no endpoint for this method and path. Use GET /metrics."
		if strings.HasPrefix(r.URL.Path, "/debug/pprof") && !opts.PProf {
			msg = "The profiles are off. Set server.pprof to true to turn them on."
		}
		writeError(w, http.StatusNotFound, msg)
	})
	if opts.Token == "" {
		return mux
	}
	want := sha256.Sum256([]byte(opts.Token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		// Compare hashes, so the time does not depend on the token length.
		sum := sha256.Sum256([]byte(got))
		if !ok || subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="tollgate-admin"`)
			writeError(w, http.StatusUnauthorized, "The admin token is missing or not correct. Send it as a bearer token in the Authorization header.")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "{\"error\":%q}\n", msg)
}

// Serve serves h on ln until ctx ends. Then it closes the listener, waits
// up to 5 seconds for open requests, and returns.
func Serve(ctx context.Context, ln net.Listener, h http.Handler, logger *slog.Logger) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	logger.InfoContext(ctx, "admin port started", "addr", ln.Addr().String())
	select {
	case err := <-errc:
		return fmt.Errorf("serve admin: %w", err)
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		_ = srv.Close()
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve admin: %w", err)
	}
	return nil
}
