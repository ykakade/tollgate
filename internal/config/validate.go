package config

import (
	"encoding/hex"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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
	v.validateProviders(c.Providers)
	routeModels := v.validateRoutes(c.Routes, c.Providers)
	v.validateTenants(c.Tenants, routeModels)
}

func (v *validator) validateServer(s *Server) {
	if _, _, err := net.SplitHostPort(s.Listen); err != nil {
		v.add(v.line("server", "listen"), "The listen address %q is not valid. Use host:port, for example \":8080\" or \"127.0.0.1:8080\".", s.Listen)
	}
	if s.MaxBodyBytes > maxBodyBytesLimit {
		v.add(v.line("server", "max_body_bytes"), "The value of max_body_bytes is too large. Use 1GiB or less.")
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
		case p.Kind == KindAnthropic || p.Kind == KindOpenAI:
		case p.Kind == "":
			v.add(v.line("providers", name), "The provider %q has no kind. Set kind to anthropic or openai.", name)
		case laterKinds[p.Kind]:
			v.add(v.line("providers", name, "kind"), "The provider kind %q is not available in this version. Use anthropic or openai.", p.Kind)
		default:
			v.add(v.line("providers", name, "kind"), "The provider kind %q is not known. Use anthropic or openai.", p.Kind)
		}
		v.validateBaseURL(name, p)
		v.resolveSecret(&p.APIKey, name)
		providers[name] = p
	}
}

func (v *validator) validateBaseURL(name string, p Provider) {
	if p.BaseURL == "" {
		if p.Kind == KindAnthropic || p.Kind == KindOpenAI {
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
// env: and file: references (ADR 0012).
func (v *validator) resolveSecret(s *Secret, provider string) {
	if !s.IsSet() {
		return
	}
	scheme, ref, _ := strings.Cut(s.ref, ":")
	switch scheme {
	case "env":
		val, ok := v.env.LookupEnv(ref)
		switch {
		case ref == "":
			v.add(s.line, "The api_key of the provider %q has no variable name. Use env:NAME.", provider)
		case !ok || val == "":
			v.add(s.line, "The environment variable %s is not set. Set it to the API key of the provider %q.", ref, provider)
		default:
			s.value = val
		}
	case "file":
		if ref == "" {
			v.add(s.line, "The api_key of the provider %q has no file path. Use file:/path.", provider)
			return
		}
		if !filepath.IsAbs(ref) {
			ref = filepath.Join(v.dir, ref)
		}
		data, err := v.env.ReadFile(ref)
		val := strings.TrimSpace(string(data))
		switch {
		case err != nil:
			v.add(s.line, "Tollgate cannot read the key file %s of the provider %q. Make sure that the file exists and that Tollgate can read it.", ref, provider)
		case val == "":
			v.add(s.line, "The key file %s of the provider %q is empty.", ref, provider)
		default:
			s.value = val
		}
	default:
		v.add(s.line, "The api_key of the provider %q is not a reference. Do not put keys in the file. Use env:NAME or file:/path.", provider)
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

		switch len(r.Targets) {
		case 0:
			v.add(line, "The route %q has no targets. Add one target.", r.Model)
		case 1:
		default:
			v.add(v.line("routes", i, "targets", 1), "The route %q has more than one target. This version of Tollgate supports one target for each route. Remove the other targets.", r.Model)
		}
		for j, t := range r.Targets {
			p, ok := providers[t.Provider]
			switch {
			case t.Provider == "":
				v.add(v.line("routes", i, "targets", j), "A target of the route %q has no provider. Add the name of a provider.", r.Model)
			case !ok:
				v.add(v.line("routes", i, "targets", j, "provider"), "The provider %q is not defined. Add it to providers, or use a defined provider.", t.Provider)
			case string(r.API) != p.Kind && knownAPI(r.API) && knownAPI(API(p.Kind)):
				v.add(v.line("routes", i, "targets", j, "provider"), "The provider %q uses the %s format, but the route %q uses the %s format. Use a provider with the %s format.", t.Provider, p.Kind, r.Model, r.API, r.API)
			}
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
