package config

import (
	"encoding/hex"
	"fmt"
	"math"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ykakade/tollgate"
	"github.com/ykakade/tollgate/internal/pricing"
	"github.com/ykakade/tollgate/internal/redact"
)

// nameRE is the syntax of provider, tenant and key names. These names go
// into headers and logs, so they use a small set of characters.
var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidName reports whether s is a valid provider, tenant or key name.
func ValidName(s string) bool {
	return nameRE.MatchString(s)
}

// HashPrefix is the prefix of a key hash in the configuration.
const HashPrefix = "sha256:"

func (v *validator) validate(c *Config) {
	switch c.Version {
	case 1:
	case 0:
		v.add(v.line("version"), "The version field is missing. Add \"version: 1\".")
	default:
		v.add(v.line("version"), "The version %d is not supported. Use version 1.", c.Version)
	}
	v.validateServer(&c.Server)
	v.validateStore(&c.Store)
	v.validateUsage(&c.Usage)
	c.Prices = v.loadPrices(&c.Pricing)
	if r := c.Reservation.OutputTokens; r < 0 || r > maxReserveOutputTokens {
		v.add(v.line("reservation", "output_tokens"), "The value of reservation.output_tokens is not valid. Use a number from 1 to %d.", maxReserveOutputTokens)
	}
	v.validateProviders(c.Providers)
	routeModels := v.validateRoutes(c.Routes, c.Providers)
	v.validateTenants(c.Tenants, routeModels)
	v.validatePrices(c)
	v.validateRetry(c.Retry, c.Breaker)
	v.validateTelemetry(&c.Telemetry)
	v.validateSessions(c.Sessions)
	v.validateResponseCache(c.ResponseCache)
}

// maxCacheTTL is the largest prompt cache and response cache lifetime.
const maxCacheTTL = 24 * time.Hour

func (v *validator) validateSessions(s Sessions) {
	if math.IsNaN(s.WarnRatio) || s.WarnRatio <= 0 || s.WarnRatio > 1 {
		v.add(v.line("sessions", "warn_ratio"), "The value of sessions.warn_ratio is not valid. Use a number that is more than 0 and not more than 1, for example 0.8.")
	}
}

func (v *validator) validateResponseCache(c ResponseCache) {
	if c.MaxBytes > maxBodyBytesLimit {
		v.add(v.line("response_cache", "max_bytes"), "The value of response_cache.max_bytes is too large. Use 1GiB or less.")
	}
	if c.MaxEntryBytes > c.MaxBytes {
		v.add(v.line("response_cache", "max_entry_bytes"), "The value of response_cache.max_entry_bytes is larger than response_cache.max_bytes. Make it smaller.")
	}
}

// redactNameRE is the syntax of the name of a redaction pattern. The name
// goes into the placeholder text that the model sees.
var redactNameRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,31}$`)

func (v *validator) validateRedact(i int, t Tenant) {
	for j, k := range t.Redact.Kinds {
		if !redact.KnownKind(k) {
			v.add(v.line("tenants", i, "redact", "kinds", j), "The redaction kind %q of the tenant %q is not known. Use %s.", k, t.ID, strings.Join(redact.Kinds(), ", "))
		}
	}
	for j, p := range t.Redact.Patterns {
		line := v.line("tenants", i, "redact", "patterns", j)
		if !redactNameRE.MatchString(p.Name) {
			v.add(line, "The name %q of a redaction pattern of the tenant %q is not valid. Use 1 to 32 capital letters, digits and \"_\", for example EMPLOYEE_ID.", p.Name, t.ID)
		}
		if err := redact.CheckPattern(p.Regex); err != nil {
			v.add(v.line("tenants", i, "redact", "patterns", j, "regex"), "The regex of the redaction pattern %q of the tenant %q is not valid: %v.", p.Name, t.ID, err)
		}
	}
}

func (v *validator) validateRetry(r Retry, b Breaker) {
	if r.MaxAttempts < 1 || r.MaxAttempts > maxAttempts {
		v.add(v.line("retry", "max_attempts"), "The value of retry.max_attempts is not valid. Use a number from 1 to %d.", maxAttempts)
	}
	if math.IsNaN(r.BudgetRatio) || r.BudgetRatio < 0 || r.BudgetRatio > 1 {
		v.add(v.line("retry", "budget_ratio"), "The value of retry.budget_ratio is not valid. Use a number from 0 to 1, for example 0.1.")
	}
	if math.IsNaN(b.FailureRatio) || b.FailureRatio <= 0 || b.FailureRatio > 1 {
		v.add(v.line("breaker", "failure_ratio"), "The value of breaker.failure_ratio is not valid. Use a number that is more than 0 and not more than 1, for example 0.5.")
	}
	if b.MinRequests < 1 || b.MinRequests > maxMinRequests {
		v.add(v.line("breaker", "min_requests"), "The value of breaker.min_requests is not valid. Use a number from 1 to %d.", maxMinRequests)
	}
}

func (v *validator) validateStore(s *Store) {
	switch s.Kind {
	case StoreMemory:
		if s.RedisURL.IsSet() {
			v.add(v.line("store", "redis_url"), "The store kind is memory, but the store has a redis_url. Remove redis_url, or set kind to redis.")
		}
	case StoreRedis:
		if !s.RedisURL.IsSet() {
			v.add(v.line("store"), "The store kind is redis, but the store has no redis_url. Add redis_url, for example env:REDIS_URL.")
			return
		}
		v.resolveSecret(&s.RedisURL, "The redis_url of the store", "the URL of the Redis server")
		if s.RedisURL.value != "" {
			u, err := url.Parse(s.RedisURL.value)
			if err != nil || (u.Scheme != "redis" && u.Scheme != "rediss") || u.Host == "" {
				// Do not show the value: the URL can have a password.
				v.add(s.RedisURL.line, "The redis_url of the store is not a Redis URL. Use the format redis://host:port/0 or rediss://host:port/0.")
			}
		}
	default:
		v.add(v.line("store", "kind"), "The store kind %q is not known. Use memory or redis.", s.Kind)
	}
}

func (v *validator) validateUsage(u *Usage) {
	switch u.Sink {
	case SinkJSONL:
		if !filepath.IsAbs(u.Path) {
			u.Path = filepath.Join(v.dir, u.Path)
		}
	case SinkNone:
	case "postgres":
		v.add(v.line("usage", "sink"), "The usage sink postgres is not available in this version. Use jsonl or none.")
	default:
		v.add(v.line("usage", "sink"), "The usage sink %q is not known. Use jsonl or none.", u.Sink)
	}
	if u.QueueSize < 0 || u.QueueSize > maxUsageQueueSize {
		v.add(v.line("usage", "queue_size"), "The value of usage.queue_size is not valid. Use a number from 1 to %d.", maxUsageQueueSize)
	}
	if u.IndexEvents < 0 || u.IndexEvents > maxUsageIndexEvents {
		v.add(v.line("usage", "index_events"), "The value of usage.index_events is not valid. Use a number from 1 to %d.", maxUsageIndexEvents)
	}
}

// loadPrices reads the price table of the configuration, or the table in
// the binary.
func (v *validator) loadPrices(p *Pricing) *pricing.Table {
	name, data := "pricing.yaml (in the binary)", tollgate.PriceTable()
	if p.File != "" {
		if !filepath.IsAbs(p.File) {
			p.File = filepath.Join(v.dir, p.File)
		}
		name = p.File
		var err error
		if data, err = v.env.ReadFile(p.File); err != nil {
			v.add(v.line("pricing", "file"), "Tollgate cannot read the price table %s. Make sure that the file exists and that Tollgate can read it.", p.File)
			return nil
		}
	}
	t, errs := pricing.Parse(data)
	for _, e := range errs {
		v.errs = append(v.errs, Error{File: name, Line: e.Line, Message: e.Message})
	}
	return t
}

// validatePrices checks that each model that a tenant with a budget can
// use has a price.
func (v *validator) validatePrices(c *Config) {
	if c.Prices == nil {
		return
	}
	targets := make(map[string][]Target)
	for _, r := range c.Routes {
		targets[r.Model] = append(targets[r.Model], r.Targets...)
	}
	for i, t := range c.Tenants {
		if len(t.Budgets) == 0 {
			continue
		}
		for j, m := range t.Models {
			for _, tg := range targets[m] {
				p, ok := c.Providers[tg.Provider]
				if ok && p.Kind != KindOllama && !c.Prices.Has(tg.Provider, p.Kind, tg.Model) {
					v.add(v.line("tenants", i, "models", j), "The tenant %q has a budget, but the price table has no price for the model %q of the provider %q. Add an entry to the price table. See docs/configuration.md.", t.ID, tg.Model, tg.Provider)
				}
			}
		}
	}
}

func (v *validator) validateServer(s *Server) {
	if _, _, err := net.SplitHostPort(s.Listen); err != nil {
		v.add(v.line("server", "listen"), "The listen address %q is not valid. Use host:port, for example \":8080\" or \"127.0.0.1:8080\".", s.Listen)
	}
	if s.MaxBodyBytes > maxBodyBytesLimit {
		v.add(v.line("server", "max_body_bytes"), "The value of max_body_bytes is too large. Use 1GiB or less.")
	}
	host, _, err := net.SplitHostPort(s.AdminListen)
	if err != nil {
		v.add(v.line("server", "admin_listen"), "The admin_listen address %q is not valid. Use host:port, for example \"127.0.0.1:9090\".", s.AdminListen)
		return
	}
	if _, port, _ := net.SplitHostPort(s.AdminListen); s.AdminListen == s.Listen && port != "0" {
		v.add(v.line("server", "admin_listen"), "The admin_listen address is the same as the listen address. Use a different port.")
	}
	v.resolveSecret(&s.AdminToken, "The admin_token of the server", "the bearer token of the admin port")
	if !s.AdminToken.IsSet() && !IsLoopback(host) {
		v.add(v.line("server", "admin_listen"), "The admin port %q is not on a loopback address, but the server has no admin_token. Add admin_token, for example env:TOLLGATE_ADMIN_TOKEN, or use 127.0.0.1.", s.AdminListen)
	}
}

// IsLoopback reports whether host is "localhost" or a loopback IP address.
// An empty host means all addresses, so it is not a loopback host.
func IsLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (v *validator) validateTelemetry(t *Telemetry) {
	if t.OTLPEndpoint == "" {
		return
	}
	u, err := url.Parse(t.OTLPEndpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		v.add(v.line("telemetry", "otlp_endpoint"), "The otlp_endpoint %q is not valid. Use the URL of an OTLP/HTTP receiver, for example http://localhost:4318.", t.OTLPEndpoint)
	}
}

func (v *validator) validateProviders(providers map[string]Provider) {
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := providers[name]
		if !nameRE.MatchString(name) {
			v.add(v.line("providers", name), "The provider name %q is not valid. Use letters, digits, \".\", \"_\" and \"-\", with 64 characters or fewer.", name)
		}
		switch {
		case KindAPI(p.Kind) != "":
		case p.Kind == "":
			v.add(v.line("providers", name), "The provider %q has no kind. Set kind to %s.", name, kindList)
		case laterKinds[p.Kind]:
			v.add(v.line("providers", name, "kind"), "The provider kind %q is not available in this version. Use %s.", p.Kind, kindList)
		default:
			v.add(v.line("providers", name, "kind"), "The provider kind %q is not known. Use %s.", p.Kind, kindList)
		}
		v.validateVertex(name, p)
		v.validateBaseURL(name, p)
		if p.CacheTTL > Duration(maxCacheTTL) {
			v.add(v.line("providers", name, "cache_ttl"), "The cache_ttl of the provider %q is longer than 24h. Use the prompt cache lifetime of the provider, for example 5m or 1h.", name)
		}
		v.resolveSecret(&p.APIKey, fmt.Sprintf("The api_key of the provider %q", name), fmt.Sprintf("the API key of the provider %q", name))
		providers[name] = p
	}
}

const kindList = "anthropic, openai, vertex or ollama"

// regionRE is the syntax of a Vertex AI location, such as "global", "us"
// or "us-east5". The location goes into the host name of the provider.
var regionRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// projectRE is the syntax of a Google Cloud project ID.
var projectRE = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

// validateVertex checks the fields that only a vertex provider has.
func (v *validator) validateVertex(name string, p Provider) {
	if p.Kind != KindVertex {
		for _, f := range []struct{ field, value string }{{"project", p.Project}, {"region", p.Region}} {
			if f.value != "" {
				v.add(v.line("providers", name, f.field), "The provider %q has a %s, but only a vertex provider uses it. Remove %s.", name, f.field, f.field)
			}
		}
		return
	}
	switch {
	case p.Project == "":
		v.add(v.line("providers", name), "The vertex provider %q has no project. Add the ID of your Google Cloud project.", name)
	case !projectRE.MatchString(p.Project):
		v.add(v.line("providers", name, "project"), "The project %q of the provider %q is not a Google Cloud project ID. Use the project ID, not the project name.", p.Project, name)
	}
	switch {
	case p.Region == "":
		v.add(v.line("providers", name), "The vertex provider %q has no region. Add a region, for example global or us-east5.", name)
	case !regionRE.MatchString(p.Region):
		v.add(v.line("providers", name, "region"), "The region %q of the provider %q is not valid. Use a Vertex AI location, for example global or us-east5.", p.Region, name)
	}
	if p.APIKey.IsSet() {
		v.add(v.line("providers", name, "api_key"), "The vertex provider %q has an api_key. Remove it. Tollgate uses the Google Application Default Credentials for Vertex AI.", name)
	}
}

func (v *validator) validateBaseURL(name string, p Provider) {
	if p.BaseURL == "" {
		if p.Kind != KindVertex && KindAPI(p.Kind) != "" {
			v.add(v.line("providers", name), "The provider %q has no base_url. Add a base_url.", name)
		}
		return
	}
	line := v.line("providers", name, "base_url")
	u, err := url.Parse(p.BaseURL)
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		v.add(line, "The base_url %q is not valid. Use an http or https URL, for example https://api.anthropic.com.", p.BaseURL)
	case u.User != nil || u.RawQuery != "" || u.Fragment != "":
		v.add(line, "The base_url of the provider %q has a user name, a query or a fragment. Remove it. Put the key in api_key.", name)
	case strings.HasSuffix(strings.TrimSuffix(u.Path, "/"), "/v1"):
		v.add(line, "The base_url of the provider %q ends with /v1. Remove /v1. Tollgate adds the request path, for example /v1/messages.", name)
	}
}

// resolveSecret reads the value of a secret reference. It accepts only
// env: and file: references (ADR 0012). The field names the reference in
// messages, for example `The api_key of the provider "anthropic"`, and the
// value says what the secret holds.
func (v *validator) resolveSecret(s *Secret, field, value string) {
	if !s.IsSet() {
		return
	}
	scheme, ref, _ := strings.Cut(s.ref, ":")
	switch scheme {
	case "env":
		val, ok := v.env.LookupEnv(ref)
		switch {
		case ref == "":
			v.add(s.line, "%s has no variable name. Use env:NAME.", field)
		case !ok || val == "":
			v.add(s.line, "The environment variable %s is not set. Set it to %s.", ref, value)
		default:
			s.value = val
		}
	case "file":
		if ref == "" {
			v.add(s.line, "%s has no file path. Use file:/path.", field)
			return
		}
		if !filepath.IsAbs(ref) {
			ref = filepath.Join(v.dir, ref)
		}
		data, err := v.env.ReadFile(ref)
		val := strings.TrimSpace(string(data))
		switch {
		case err != nil:
			v.add(s.line, "Tollgate cannot read the file %s that has %s. Make sure that the file exists and that Tollgate can read it.", ref, value)
		case val == "":
			v.add(s.line, "The file %s that has %s is empty.", ref, value)
		default:
			s.value = val
		}
	default:
		v.add(s.line, "%s is not a reference. Do not put secrets in the file. Use env:NAME or file:/path.", field)
	}
}

// validateRoutes checks the routes and returns the set of route model names.
func (v *validator) validateRoutes(routes []Route, providers map[string]Provider) map[string]bool {
	models := make(map[string]bool)
	seen := make(map[API]map[string]bool)
	for i, r := range routes {
		line := v.line("routes", i)
		if r.Model == "" {
			v.add(line, "A route has no model. Add the model name that clients send.")
		}
		switch r.API {
		case APIAnthropic, APIOpenAI:
			if seen[r.API] == nil {
				seen[r.API] = make(map[string]bool)
			}
			if r.Model != "" && seen[r.API][r.Model] {
				v.add(line, "The model %q has two %s routes. Remove one of them.", r.Model, r.API)
			}
			seen[r.API][r.Model] = true
		case "":
			v.add(line, "The route %q has no api. Set api to anthropic or openai.", r.Model)
		default:
			v.add(v.line("routes", i, "api"), "The api %q of the route %q is not known. Use anthropic or openai.", r.API, r.Model)
		}
		models[r.Model] = true

		switch {
		case len(r.Targets) == 0:
			v.add(line, "The route %q has no targets. Add one target or more.", r.Model)
		case len(r.Targets) > maxTargets:
			v.add(v.line("routes", i, "targets", maxTargets), "The route %q has more than %d targets. Remove some targets.", r.Model, maxTargets)
		}
		targets := make(map[Target]bool)
		for j, t := range r.Targets {
			p, ok := providers[t.Provider]
			switch {
			case t.Provider == "":
				v.add(v.line("routes", i, "targets", j), "A target of the route %q has no provider. Add the name of a provider.", r.Model)
			case !ok:
				v.add(v.line("routes", i, "targets", j, "provider"), "The provider %q is not defined. Add it to providers, or use a defined provider.", t.Provider)
			case KindAPI(p.Kind) != "" && knownAPI(r.API) && KindAPI(p.Kind) != r.API:
				v.add(v.line("routes", i, "targets", j, "provider"), "The provider %q uses the %s format, but the route %q uses the %s format. Use a provider with the %s format.", t.Provider, KindAPI(p.Kind), r.Model, r.API, r.API)
			case targets[t]:
				v.add(v.line("routes", i, "targets", j), "The route %q has the target %s/%s two times. Remove one of them.", r.Model, t.Provider, t.Model)
			}
			targets[t] = true
		}

		for _, d := range []struct {
			name string
			val  Duration
		}{
			{"connect", r.Timeouts.Connect},
			{"first_token", r.Timeouts.FirstToken},
			{"idle", r.Timeouts.Idle},
			{"total", r.Timeouts.Total},
		} {
			if d.val > r.Timeouts.Total {
				v.add(v.line("routes", i, "timeouts", d.name), "The %s timeout of the route %q is longer than its total timeout. Make it shorter, or make the total timeout longer.", d.name, r.Model)
			}
		}
		if c := r.ResponseCache; c.TTL > Duration(maxCacheTTL) || (!c.Enabled && c.TTL != 0) {
			v.add(v.line("routes", i, "response_cache"), "The response_cache of the route %q is not valid. Set enabled to true, and use a ttl of 24h or less.", r.Model)
		}
	}
	return models
}

func (v *validator) validateTenants(tenants []Tenant, routeModels map[string]bool) {
	tenantIDs := make(map[string]bool)
	hashes := make(map[string]bool)
	for i, t := range tenants {
		line := v.line("tenants", i)
		switch {
		case t.ID == "":
			v.add(line, "A tenant has no id. Add an id.")
		case !nameRE.MatchString(t.ID):
			v.add(v.line("tenants", i, "id"), "The tenant id %q is not valid. Use letters, digits, \".\", \"_\" and \"-\", with 64 characters or fewer.", t.ID)
		case tenantIDs[t.ID]:
			v.add(v.line("tenants", i, "id"), "The tenant id %q is used two times. Use a different id for each tenant.", t.ID)
		}
		tenantIDs[t.ID] = true

		keyIDs := make(map[string]bool)
		for j, k := range t.Keys {
			switch {
			case k.ID == "":
				v.add(v.line("tenants", i, "keys", j), "A key of the tenant %q has no id. Add an id.", t.ID)
			case !nameRE.MatchString(k.ID):
				v.add(v.line("tenants", i, "keys", j, "id"), "The key id %q is not valid. Use letters, digits, \".\", \"_\" and \"-\", with 64 characters or fewer.", k.ID)
			case keyIDs[k.ID]:
				v.add(v.line("tenants", i, "keys", j, "id"), "The key id %q is used two times in the tenant %q. Use a different id for each key.", k.ID, t.ID)
			}
			keyIDs[k.ID] = true

			hashLine := v.line("tenants", i, "keys", j, "hash")
			switch {
			case !validHash(k.Hash):
				v.add(hashLine, "The hash of the key %q is not valid. Use the hash that \"tollgate keys create\" shows.", k.ID)
			case hashes[k.Hash]:
				v.add(hashLine, "The hash of the key %q is used two times. Make a new key with \"tollgate keys create\".", k.ID)
			}
			hashes[k.Hash] = true
		}

		if len(t.Models) == 0 {
			v.add(line, "The tenant %q has no models. Add one route model or more to models.", t.ID)
		}
		v.validateLimits(i, t)
		v.validateRedact(i, t)
		for j, m := range t.Models {
			switch {
			case m == "":
				v.add(v.line("tenants", i, "models", j), "The tenant %q has an empty model name. Remove it.", t.ID)
			case !routeModels[m]:
				v.add(v.line("tenants", i, "models", j), "The model %q of the tenant %q has no route. Add a route for it, or remove it from models.", m, t.ID)
			}
		}
	}
}

func (v *validator) validateLimits(i int, t Tenant) {
	if l := t.Limits.RequestsPerMinute; l < 0 || l > maxRequestsPerMinute {
		v.add(v.line("tenants", i, "limits", "requests_per_minute"), "The requests_per_minute of the tenant %q is not valid. Use a number from 0 (no limit) to %d.", t.ID, maxRequestsPerMinute)
	}
	if l := t.Limits.TokensPerMinute; l < 0 || l > maxTokensPerMinute {
		v.add(v.line("tenants", i, "limits", "tokens_per_minute"), "The tokens_per_minute of the tenant %q is not valid. Use a number from 0 (no limit) to %d.", t.ID, int64(maxTokensPerMinute))
	}
	windows := make(map[string]bool)
	for j, b := range t.Budgets {
		line := v.line("tenants", i, "budgets", j)
		switch b.Window {
		case WindowMonth, WindowSession:
			if windows[b.Window] {
				v.add(v.line("tenants", i, "budgets", j, "window"), "The tenant %q has two %s budgets. Remove one of them.", t.ID, b.Window)
			}
			windows[b.Window] = true
		case "":
			v.add(line, "A budget of the tenant %q has no window. Set window to month or session.", t.ID)
		default:
			v.add(v.line("tenants", i, "budgets", j, "window"), "The budget window %q of the tenant %q is not known. Use month or session.", b.Window, t.ID)
		}
		if b.LimitUSD <= 0 {
			v.add(v.line("tenants", i, "budgets", j, "limit_usd"), "The %s budget of the tenant %q has no limit_usd. Set limit_usd to an amount more than 0.", b.Window, t.ID)
		}
		if b.Mode != ModeHard && b.Mode != ModeSoft {
			v.add(v.line("tenants", i, "budgets", j, "mode"), "The budget mode %q of the tenant %q is not known. Use hard or soft.", b.Mode, t.ID)
		}
	}
}

func knownAPI(a API) bool {
	return a == APIAnthropic || a == APIOpenAI
}

func validHash(h string) bool {
	hexPart, ok := strings.CutPrefix(h, HashPrefix)
	if !ok || len(hexPart) != 64 || strings.ToLower(hexPart) != hexPart {
		return false
	}
	_, err := hex.DecodeString(hexPart)
	return err == nil
}
