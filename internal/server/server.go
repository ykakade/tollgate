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

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"golang.org/x/oauth2"

	"github.com/ykakade/tollgate/internal/auth"
	"github.com/ykakade/tollgate/internal/config"
	"github.com/ykakade/tollgate/internal/limiter"
	"github.com/ykakade/tollgate/internal/preflight"
	"github.com/ykakade/tollgate/internal/pricing"
	"github.com/ykakade/tollgate/internal/provider"
	"github.com/ykakade/tollgate/internal/provider/anthropic"
	"github.com/ykakade/tollgate/internal/provider/openai"
	"github.com/ykakade/tollgate/internal/provider/vertex"
	"github.com/ykakade/tollgate/internal/redact"
	"github.com/ykakade/tollgate/internal/relay"
	"github.com/ykakade/tollgate/internal/respcache"
	"github.com/ykakade/tollgate/internal/router"
	"github.com/ykakade/tollgate/internal/telemetry"
	"github.com/ykakade/tollgate/internal/usage"
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
	// Store holds the rate limit and budget state. If nil, the server
	// uses a new memory store.
	Store limiter.Store
	// Usage receives one usage event for each request. If nil, the server
	// records no events.
	Usage Emitter
	// Now returns the current time. If nil, the server uses time.Now.
	Now func() time.Time
	// Metrics receives the metrics of the requests. If nil, the server
	// uses new metrics that no endpoint shows.
	Metrics *telemetry.Metrics
	// Tracer makes the spans of the requests. If nil, the server makes no
	// spans.
	Tracer trace.Tracer
}

// Emitter receives usage events. It must not block. A *usage.Queue is an
// Emitter.
type Emitter interface {
	Emit(e usage.Event) bool
}

// Server is the data plane. Use New to make one.
type Server struct {
	keys          *auth.Keyring
	routes        map[routeKey]*route
	tenants       map[string]*tenantPolicy
	maxBody       int64
	drainTimeout  time.Duration
	logger        *slog.Logger
	tracker       tracker
	store         limiter.Store
	usage         Emitter
	now           func() time.Time
	prices        *pricing.Table
	reserveOutput int64
	router        *router.Router
	maxAttempts   int
	metrics       *telemetry.Metrics
	tracer        trace.Tracer
	propagator    propagation.TextMapPropagator

	// derive and sticky are the session settings (ADR 0027 and ADR 0028).
	derive, sticky bool
	warnRatio      float64
	// cache is the exact response cache of the routes that turn it on.
	cache *respcache.Cache
	// breakerKeys holds the breaker key of each target, for Breakers.
	breakerKeys []router.Key
}

type routeKey struct {
	api   config.API
	model string
}

type route struct {
	model string
	// all holds the targets of the route, in order. count holds the
	// targets that support /v1/messages/count_tokens.
	all, count targetList
	timeouts   relay.Timeouts
	// cacheTTL is the lifetime of a cached response, or 0 if the route
	// does not use the response cache.
	cacheTTL time.Duration
}

// targetList is an ordered list of targets and their breaker keys.
type targetList struct {
	targets []*target
	keys    []router.Key
}

func (l *targetList) add(t *target) {
	l.targets = append(l.targets, t)
	l.keys = append(l.keys, t.key)
}

// target is one provider and model of a route.
type target struct {
	config.Target
	// kind is the kind of the provider, for the price table and the body
	// edits.
	kind    string
	adapter provider.Adapter
	key     router.Key
	// cacheTTL is the prompt cache lifetime of the provider, and so the
	// lifetime of a sticky routing entry for the target.
	cacheTTL time.Duration
}

// name returns "provider/model" for messages.
func (t *target) name() string { return t.Provider + "/" + t.Model }

// body returns the request body for the target.
func (t *target) body(pre *preflight.Request, includeUsage bool) ([]byte, error) {
	if t.kind == config.KindVertex {
		b, err := pre.VertexBody(vertex.Version)
		if err != nil {
			return nil, fmt.Errorf("make vertex body: %w", err)
		}
		return b, nil
	}
	b, err := pre.Rewrite(t.Model, includeUsage)
	if err != nil {
		return nil, fmt.Errorf("rewrite body: %w", err)
	}
	return b, nil
}

// New makes a server from a validated configuration.
func New(opts Options) (*Server, error) {
	cfg := opts.Config
	keys, err := auth.NewKeyring(cfg.Tenants)
	if err != nil {
		return nil, fmt.Errorf("load keys: %w", err)
	}
	s := &Server{
		keys:          keys,
		routes:        make(map[routeKey]*route),
		tenants:       make(map[string]*tenantPolicy),
		maxBody:       int64(cfg.Server.MaxBodyBytes),
		drainTimeout:  cfg.Server.DrainTimeout.Std(),
		logger:        opts.Logger,
		store:         opts.Store,
		usage:         opts.Usage,
		now:           opts.Now,
		prices:        cfg.Prices,
		reserveOutput: cfg.Reservation.OutputTokens,
		maxAttempts:   cfg.Retry.MaxAttempts,
		metrics:       opts.Metrics,
		tracer:        opts.Tracer,
		propagator:    propagation.TraceContext{},
		derive:        cfg.Sessions.DeriveOn(),
		sticky:        cfg.Sessions.StickyOn(),
		warnRatio:     cfg.Sessions.WarnRatio,
		cache:         respcache.New(int64(cfg.ResponseCache.MaxBytes), int64(cfg.ResponseCache.MaxEntryBytes)),
	}
	if s.metrics == nil {
		s.metrics = telemetry.NewMetrics()
	}
	if s.tracer == nil {
		s.tracer = noop.NewTracerProvider().Tracer(telemetry.TracerName)
	}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	if s.store == nil {
		s.store = limiter.NewMemory()
	}
	if s.now == nil {
		s.now = time.Now
	}
	s.router = router.New(router.Settings{
		FailureRatio: cfg.Breaker.FailureRatio,
		MinRequests:  cfg.Breaker.MinRequests,
		OpenFor:      cfg.Breaker.OpenFor.Std(),
		BudgetRatio:  cfg.Retry.BudgetRatio,
		Now:          s.now,
		OnChange: func(k router.Key, from, to router.State) {
			s.logger.Warn("circuit breaker changed",
				"provider", k.Provider,
				"provider_model", k.Model,
				"region", k.Region,
				"from", from.String(),
				"to", to.String(),
			)
		},
	})
	for _, r := range cfg.Routes {
		if len(r.Targets) == 0 {
			return nil, fmt.Errorf("route %s has no targets", r.Model)
		}
		rt := &route{
			model: r.Model,
			timeouts: relay.Timeouts{
				Connect:    r.Timeouts.Connect.Std(),
				FirstToken: r.Timeouts.FirstToken.Std(),
				Idle:       r.Timeouts.Idle.Std(),
				Total:      r.Timeouts.Total.Std(),
			},
		}
		if r.ResponseCache.Enabled {
			rt.cacheTTL = r.ResponseCache.TTL.Std()
		}
		for _, tc := range r.Targets {
			adapter, ok := opts.Adapters[tc.Provider]
			if !ok {
				return nil, fmt.Errorf("route %s: no adapter for provider %s", r.Model, tc.Provider)
			}
			p := cfg.Providers[tc.Provider]
			t := &target{
				Target:   tc,
				kind:     p.Kind,
				adapter:  adapter,
				key:      router.Key{Provider: tc.Provider, Model: tc.Model, Region: p.Region},
				cacheTTL: p.CacheTTL.Std(),
			}
			rt.all.add(t)
			if p.Kind != config.KindVertex {
				rt.count.add(t)
			}
		}
		s.routes[routeKey{r.API, r.Model}] = rt
	}
	for _, t := range cfg.Tenants {
		models := make(map[string]bool)
		for _, m := range t.Models {
			models[m] = true
		}
		tp := &tenantPolicy{models: models, limits: t.Limits, budgets: t.Budgets}
		if t.Redact.On() {
			patterns := make([]redact.Pattern, len(t.Redact.Patterns))
			for i, p := range t.Redact.Patterns {
				patterns[i] = redact.Pattern{Name: p.Name, Regex: p.Regex}
			}
			if tp.redactor, err = redact.New(t.Redact.Kinds, patterns); err != nil {
				return nil, fmt.Errorf("tenant %s: redact: %w", t.ID, err)
			}
		}
		s.tenants[t.ID] = tp
	}
	s.breakerKeys = breakerKeys(cfg)
	s.metrics.WatchBreakers(s.Breakers)
	return s, nil
}

// breakerKeys returns the breaker key of each target in the
// configuration, one time each, in the order of the routes.
func breakerKeys(cfg *config.Config) []router.Key {
	seen := make(map[router.Key]bool)
	var keys []router.Key
	for _, r := range cfg.Routes {
		for _, tc := range r.Targets {
			k := router.Key{Provider: tc.Provider, Model: tc.Model, Region: cfg.Providers[tc.Provider].Region}
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	return keys
}

// Breakers returns the state of the breaker of each target in the
// configuration, for the breaker_state metric and the admin API.
func (s *Server) Breakers() []telemetry.BreakerState {
	out := make([]telemetry.BreakerState, len(s.breakerKeys))
	for i, k := range s.breakerKeys {
		out[i] = telemetry.BreakerState{Provider: k.Provider, Model: k.Model, Region: k.Region, State: int(s.router.State(k))}
	}
	return out
}

// AdapterOptions are the options of NewAdapters.
type AdapterOptions struct {
	// Transport, if not nil, is the transport of all adapters. If nil,
	// each adapter has its own transport.
	Transport http.RoundTripper
	// GoogleTokens, if not nil, gives the access tokens of the vertex
	// providers. If nil, NewAdapters uses the Google Application Default
	// Credentials.
	GoogleTokens oauth2.TokenSource
}

// NewAdapters makes one adapter for each provider in a validated
// configuration.
func NewAdapters(ctx context.Context, providers map[string]config.Provider, opts AdapterOptions) (map[string]provider.Adapter, error) {
	out := make(map[string]provider.Adapter, len(providers))
	rt := opts.Transport
	for name, p := range providers {
		var (
			a   provider.Adapter
			err error
		)
		switch p.Kind {
		case config.KindAnthropic:
			a, err = anthropic.New(p.BaseURL, p.APIKey.Value(), rt)
		case config.KindOpenAI, config.KindOllama:
			a, err = openai.New(p.BaseURL, p.APIKey.Value(), rt)
		case config.KindVertex:
			if opts.GoogleTokens == nil {
				if opts.GoogleTokens, err = vertex.DefaultTokenSource(ctx); err != nil {
					return nil, fmt.Errorf("provider %s: %w. Run \"gcloud auth application-default login\", or set GOOGLE_APPLICATION_CREDENTIALS", name, err)
				}
			}
			a, err = vertex.New(p.BaseURL, p.Project, p.Region, opts.GoogleTokens, rt)
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
	mux.Handle("POST "+countTokensPath, s.proxy(endpoint{api: config.APIAnthropic, path: countTokensPath}))
	mux.Handle("POST /v1/chat/completions", s.proxy(endpoint{api: config.APIOpenAI, path: "/v1/chat/completions", metered: true}))
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("/", s.notFound)
	return mux
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeStatus(w, http.StatusOK, "ok")
}

// readyz reports whether the server accepts requests. A limit store that
// does not answer does not make the server not ready (ADR 0020): rate
// limits then fail open, and only tenants with a hard budget get errors.
// The body shows the state of the store.
func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	store := s.storeState(r.Context())
	if s.tracker.isDraining() {
		writeStatus(w, http.StatusServiceUnavailable, "draining", store)
		return
	}
	writeStatus(w, http.StatusOK, "ready", store)
}

func writeStatus(w http.ResponseWriter, code int, status string, store ...string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if len(store) > 0 {
		_, _ = fmt.Fprintf(w, "{\"status\":%q,\"store\":%q}\n", status, store[0])
		return
	}
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
	case errors.Is(cause, provider.ErrMalformed):
		return fmt.Sprintf("The provider %q sent a response that is not valid. Tollgate stopped the response.", p)
	case started:
		return fmt.Sprintf("The connection to the provider %q failed during the response. Tollgate stopped the response.", p)
	default:
		return fmt.Sprintf("Tollgate cannot send the request to the provider %q. Make sure that the provider is available. Then send the request again.", p)
	}
}
