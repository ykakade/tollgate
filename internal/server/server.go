// Package server is the data plane of Tollgate: the HTTP handlers, the
// proxy flow for each request, the health endpoints and the drain at
// shutdown.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/ykakade/tollgate/internal/auth"
	"github.com/ykakade/tollgate/internal/config"
	"github.com/ykakade/tollgate/internal/provider"
	"github.com/ykakade/tollgate/internal/provider/anthropic"
	"github.com/ykakade/tollgate/internal/provider/openai"
	"github.com/ykakade/tollgate/internal/relay"
)

// Server settings that the configuration does not set.
const (
	readHeaderTimeout = 10 * time.Second
	// bodyReadTimeout is the longest time to read a request body.
	bodyReadTimeout = 60 * time.Second
	idleConnTimeout = 2 * time.Minute
	maxHeaderBytes  = 1 << 20
	// drainRetryAfter is the retry-after value, in seconds, of the 503
	// response during a drain.
	drainRetryAfter = 5
	// shutdownGrace is the time for handlers to return after the drain
	// cancels them.
	shutdownGrace = 5 * time.Second
)

// Options are the inputs of New.
type Options struct {
	// Config must be a configuration that config.Load accepted.
	Config *config.Config
	// Adapters holds one adapter for each provider name in Config.
	Adapters map[string]provider.Adapter
	// Logger receives the access log. If nil, the server logs nothing.
	Logger *slog.Logger
}

// Server is the data plane. Use New to make one.
type Server struct {
	keys         *auth.Keyring
	routes       map[routeKey]*route
	tenants      map[string]map[string]bool
	maxBody      int64
	drainTimeout time.Duration
	logger       *slog.Logger
	tracker      tracker
}

type routeKey struct {
	api   config.API
	model string
}

type route struct {
	target   config.Target
	adapter  provider.Adapter
	timeouts relay.Timeouts
}

// New makes a server from a validated configuration.
func New(opts Options) (*Server, error) {
	cfg := opts.Config
	keys, err := auth.NewKeyring(cfg.Tenants)
	if err != nil {
		return nil, fmt.Errorf("load keys: %w", err)
	}
	s := &Server{
		keys:         keys,
		routes:       make(map[routeKey]*route),
		tenants:      make(map[string]map[string]bool),
		maxBody:      int64(cfg.Server.MaxBodyBytes),
		drainTimeout: cfg.Server.DrainTimeout.Std(),
		logger:       opts.Logger,
	}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	for _, r := range cfg.Routes {
		if len(r.Targets) == 0 {
			return nil, fmt.Errorf("route %s has no targets", r.Model)
		}
		target := r.Targets[0]
		adapter, ok := opts.Adapters[target.Provider]
		if !ok {
			return nil, fmt.Errorf("route %s: no adapter for provider %s", r.Model, target.Provider)
		}
		s.routes[routeKey{r.API, r.Model}] = &route{
			target:  target,
			adapter: adapter,
			timeouts: relay.Timeouts{
				Connect:    r.Timeouts.Connect.Std(),
				FirstToken: r.Timeouts.FirstToken.Std(),
				Idle:       r.Timeouts.Idle.Std(),
				Total:      r.Timeouts.Total.Std(),
			},
		}
	}
	for _, t := range cfg.Tenants {
		models := make(map[string]bool)
		for _, m := range t.Models {
			models[m] = true
		}
		s.tenants[t.ID] = models
	}
	return s, nil
}

// NewAdapters makes one adapter for each provider in a validated
// configuration. If rt is nil, each adapter has its own transport.
func NewAdapters(providers map[string]config.Provider, rt http.RoundTripper) (map[string]provider.Adapter, error) {
	out := make(map[string]provider.Adapter, len(providers))
	for name, p := range providers {
		var (
			a   provider.Adapter
			err error
		)
		switch p.Kind {
		case config.KindAnthropic:
			a, err = anthropic.New(p.BaseURL, p.APIKey.Value(), rt)
		case config.KindOpenAI:
			a, err = openai.New(p.BaseURL, p.APIKey.Value(), rt)
		default:
			err = fmt.Errorf("unknown provider kind %q", p.Kind)
		}
		if err != nil {
			return nil, fmt.Errorf("provider %s: %w", name, err)
		}
		out[name] = a
	}
	return out, nil
}

// Handler returns the handler of the data plane.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /v1/messages", s.proxy(endpoint{api: config.APIAnthropic, path: "/v1/messages", metered: true}))
	mux.Handle("POST /v1/messages/count_tokens", s.proxy(endpoint{api: config.APIAnthropic, path: "/v1/messages/count_tokens"}))
	mux.Handle("POST /v1/chat/completions", s.proxy(endpoint{api: config.APIOpenAI, path: "/v1/chat/completions", metered: true}))
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("/", s.notFound)
	return mux
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeStatus(w, http.StatusOK, "ok")
}

func (s *Server) readyz(w http.ResponseWriter, _ *http.Request) {
	if s.tracker.isDraining() {
		writeStatus(w, http.StatusServiceUnavailable, "draining")
		return
	}
	writeStatus(w, http.StatusOK, "ready")
}

func writeStatus(w http.ResponseWriter, code int, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, "{\"status\":%q}\n", status)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	api := config.APIOpenAI
	if r.Header.Get("Anthropic-Version") != "" {
		api = config.APIAnthropic
	}
	x := s.begin(w, r, api)
	defer s.finish(r.Context(), x)
	x.fail(http.StatusNotFound, fmt.Sprintf("Tollgate has no endpoint for %s %s. Use POST /v1/messages or POST /v1/chat/completions.", r.Method, r.URL.Path), 0)
}

// Serve accepts connections on ln until ctx ends. Then it drains (ADR
// 0013): /readyz fails, new requests get 503, and open requests can
// continue for the drain timeout. When the drain timeout ends, or when
// force is closed, Serve cancels the open requests. Serve returns when all
// handlers returned.
func (s *Server) Serve(ctx context.Context, ln net.Listener, force <-chan struct{}) error {
	reqCtx, cancelRequests := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelRequests()
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleConnTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		BaseContext:       func(net.Listener) context.Context { return reqCtx },
		ErrorLog:          slog.NewLogLogger(s.logger.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	s.logger.InfoContext(ctx, "server started", "addr", ln.Addr().String())

	select {
	case err := <-errc:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	idle := s.tracker.drain()
	srv.SetKeepAlivesEnabled(false)
	s.logger.InfoContext(ctx, "drain started", "active_requests", s.tracker.active(), "drain_timeout", s.drainTimeout.String())
	timer := time.NewTimer(s.drainTimeout)
	defer timer.Stop()
	select {
	case <-idle:
		s.logger.InfoContext(ctx, "drain done")
	case <-timer.C:
		s.logger.WarnContext(ctx, "drain timeout reached, open requests canceled", "active_requests", s.tracker.active())
	case <-force:
		s.logger.WarnContext(ctx, "second stop signal, open requests canceled", "active_requests", s.tracker.active())
	}
	cancelRequests()

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		return fmt.Errorf("shut down: %w", err)
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	s.logger.InfoContext(ctx, "server stopped")
	return nil
}

// tracker counts the open proxy requests and refuses new requests during a
// drain.
type tracker struct {
	mu       sync.Mutex
	count    int
	draining bool
	idle     chan struct{} // closed when draining and count is 0
}

// enter adds a request. It returns false during a drain.
func (t *tracker) enter() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.draining {
		return false
	}
	t.count++
	return true
}

// leave removes a request that enter added.
func (t *tracker) leave() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.count--
	if t.draining && t.count == 0 {
		close(t.idle)
	}
}

// drain starts the drain. It returns a channel that is closed when no
// request is open.
func (t *tracker) drain() <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.draining {
		t.draining = true
		t.idle = make(chan struct{})
		if t.count == 0 {
			close(t.idle)
		}
	}
	return t.idle
}

func (t *tracker) isDraining() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.draining
}

func (t *tracker) active() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.count
}

// errorMessage returns a message for the client that tells why an upstream
// attempt failed.
func errorMessage(target config.Target, timeouts relay.Timeouts, cause error, started bool) string {
	p := target.Provider
	switch {
	case errors.Is(cause, relay.ErrConnectTimeout):
		return fmt.Sprintf("Tollgate cannot connect to the provider %q in %s. Make sure that the provider is available. Then send the request again.", p, timeouts.Connect)
	case errors.Is(cause, relay.ErrFirstTokenTimeout):
		return fmt.Sprintf("The provider %q sent no response in %s (first_token timeout). Send the request again later.", p, timeouts.FirstToken)
	case errors.Is(cause, relay.ErrIdleTimeout):
		return fmt.Sprintf("The provider %q sent no data for %s (idle timeout). Tollgate stopped the response.", p, timeouts.Idle)
	case errors.Is(cause, relay.ErrTotalTimeout):
		return fmt.Sprintf("The request took longer than %s (total timeout). Tollgate stopped the response.", timeouts.Total)
	case started:
		return fmt.Sprintf("The connection to the provider %q failed during the response. Tollgate stopped the response.", p)
	default:
		return fmt.Sprintf("Tollgate cannot send the request to the provider %q. Make sure that the provider is available. Then send the request again.", p)
	}
}
