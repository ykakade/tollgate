package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.yaml.in/yaml/v3"
)

const testHash = "sha256:2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae"

func testEnv() Env {
	vars := map[string]string{"ANTHROPIC_API_KEY": "sk-ant-test", "OPENAI_API_KEY": "sk-openai-test", "EMPTY": ""}
	files := map[string]string{
		"/keys/openai":          "sk-from-file\n",
		"/etc/tollgate/rel-key": "sk-relative",
		"/etc/tollgate/prices.yaml": `- provider: anthropic
  model: claude-sonnet-5-5-20260101
  effective_from: 2026-01-01
  micro_usd_per_mtok: {input: 2000000, output: 10000000}
- provider: openai
  model: gpt-test
  effective_from: 2026-01-01
  micro_usd_per_mtok: {input: 1000000, output: 2000000}
- provider: local
  model: qwen
  effective_from: 2026-01-01
  micro_usd_per_mtok: {input: 0, output: 0}
`,
		"/etc/tollgate/bad-prices.yaml": "- provider: a\n  model: m\n  effective_from: never\n",
		"redis-url":                     "redis://cache:6379/0",
	}
	vars["REDIS_URL"] = "redis://localhost:6379/0"
	vars["ADMIN_TOKEN"] = "admin-secret"
	vars["BAD_REDIS_URL"] = "http://user:pw@localhost"
	return Env{
		LookupEnv: func(k string) (string, bool) { v, ok := vars[k]; return v, ok },
		ReadFile: func(p string) ([]byte, error) {
			if v, ok := files[p]; ok {
				return []byte(v), nil
			}
			return nil, fs.ErrNotExist
		},
	}
}

const validConfig = `version: 1
server:
  listen: "127.0.0.1:8081"
  max_body_bytes: 4MiB
  drain_timeout: 30s
providers:
  anthropic:
    kind: anthropic
    api_key: env:ANTHROPIC_API_KEY
  openai:
    kind: openai
    base_url: https://api.openai.com
    api_key: file:/keys/openai
  local:
    kind: openai
    base_url: http://localhost:11434
routes:
  - model: claude-sonnet-5-5
    api: anthropic
    targets:
      - provider: anthropic
        model: claude-sonnet-5-5-20260101
    timeouts:
      first_token: 45s
  - model: gpt-test
    api: openai
    targets:
      - provider: openai
  - model: local-coder
    api: openai
    targets:
      - provider: local
        model: qwen
tenants:
  - id: personal
    keys:
      - id: laptop
        hash: ` + testHash + `
    models: [claude-sonnet-5-5, gpt-test, local-coder]
`

func TestParseValid(t *testing.T) {
	cfg, err := Parse("/etc/tollgate/tollgate.yaml", []byte(validConfig), testEnv())
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Server.MaxBodyBytes; got != 4<<20 {
		t.Errorf("max_body_bytes = %d, want %d", got, 4<<20)
	}
	if got := cfg.Server.DrainTimeout.Std(); got != 30*time.Second {
		t.Errorf("drain_timeout = %v, want 30s", got)
	}
	if got := cfg.Providers["anthropic"].BaseURL; got != "https://api.anthropic.com" {
		t.Errorf("default anthropic base_url = %q", got)
	}
	if got := cfg.Providers["anthropic"].APIKey.Value(); got != "sk-ant-test" {
		t.Errorf("env key = %q", got)
	}
	if got := cfg.Providers["openai"].APIKey.Value(); got != "sk-from-file" {
		t.Errorf("file key = %q", got)
	}
	if cfg.Providers["local"].APIKey.IsSet() {
		t.Error("local provider has a key, want none")
	}
	want := Timeouts{
		Connect:    Duration(DefaultConnectTimeout),
		FirstToken: Duration(45 * time.Second),
		Idle:       Duration(DefaultIdleTimeout),
		Total:      Duration(DefaultTotalTimeout),
	}
	if diff := cmp.Diff(want, cfg.Routes[0].Timeouts); diff != "" {
		t.Errorf("timeouts differ (-want +got):\n%s", diff)
	}
	if got := cfg.Routes[1].Targets[0].Model; got != "gpt-test" {
		t.Errorf("default target model = %q, want the route model", got)
	}
}

func TestParseDefaults(t *testing.T) {
	cfg, err := Parse("t.yaml", []byte("version: 1\n"), testEnv())
	if err != nil {
		t.Fatal(err)
	}
	want := Server{Listen: DefaultListen, MaxBodyBytes: DefaultMaxBodyBytes, DrainTimeout: Duration(DefaultDrainTimeout), AdminListen: DefaultAdminListen}
	if diff := cmp.Diff(want, cfg.Server, cmp.AllowUnexported(Secret{})); diff != "" {
		t.Errorf("server differs (-want +got):\n%s", diff)
	}
}

func TestParseRejectsTrailingDocuments(t *testing.T) {
	for _, suffix := range []string{"---\nversion: 2\n", "---\n", "---\nversion: [\n"} {
		t.Run(suffix, func(t *testing.T) {
			cfg, err := Parse("t.yaml", []byte(validConfig+suffix), testEnv())
			ve, ok := errors.AsType[*ValidationError](err)
			if cfg != nil || !ok || len(ve.Errors) == 0 {
				t.Fatalf("Parse returned %v, %v; want a validation error", cfg, err)
			}
			if ve.Errors[0].Line < 40 {
				t.Errorf("error line = %d, want the trailing document", ve.Errors[0].Line)
			}
		})
	}
}

func TestStarterIsValid(t *testing.T) {
	cfg, err := Parse("tollgate.yaml", Starter(), testEnv())
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Routes) == 0 || len(cfg.Tenants) == 0 {
		t.Error("starter has no routes or no tenants")
	}
}

// replace returns validConfig with old replaced by new. It fails the test if
// old is not in validConfig.
func replace(t *testing.T, old, repl string) string {
	t.Helper()
	if !strings.Contains(validConfig, old) {
		t.Fatalf("validConfig does not contain %q", old)
	}
	return strings.Replace(validConfig, old, repl, 1)
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		// want holds "line: part of the message" for each error.
		want []string
	}{
		{"empty file", "", []string{"0: The file is empty."}},
		{"not YAML", "version: [1", []string{"1: The YAML is not valid"}},
		{"no version", "server: {}\n", []string{"1: The version field is missing."}},
		{"wrong version", "version: 2\n", []string{"1: The version 2 is not supported."}},
		{"unknown field", replace(t, "  drain_timeout: 30s\n", "  drain_timeout: 30s\n  drain_timout: 1s\n"), []string{`6: The field "drain_timout" is not known.`}},
		{"unknown top-level field", validConfig + "stores:\n  kind: memory\n", []string{`40: The field "stores" is not known.`}},
		{"bad duration", replace(t, "drain_timeout: 30s", "drain_timeout: soon"), []string{`5: The value "soon" is not a duration.`}},
		{"negative duration", replace(t, "drain_timeout: 30s", "drain_timeout: -1s"), []string{`5: The value "-1s" is not a duration.`}},
		{"bad size", replace(t, "max_body_bytes: 4MiB", "max_body_bytes: 4MB"), []string{`4: The value "4MB" is not a size.`}},
		{"size too large", replace(t, "max_body_bytes: 4MiB", "max_body_bytes: 2GiB"), []string{"4: The value of max_body_bytes is too large."}},
		{"bad listen", replace(t, `listen: "127.0.0.1:8081"`, `listen: "8081"`), []string{`3: The listen address "8081" is not valid.`}},
		{"wrong type", replace(t, "version: 1", "version: one"), []string{`1: The value "one" has the wrong type. Use a number.`}},
		{"plain-text key", replace(t, "api_key: env:ANTHROPIC_API_KEY", "api_key: sk-ant-secret"), []string{`9: The api_key of the provider "anthropic" is not a reference.`}},
		{"missing variable", replace(t, "env:ANTHROPIC_API_KEY", "env:NOT_SET"), []string{`9: The environment variable NOT_SET is not set.`}},
		{"empty variable", replace(t, "env:ANTHROPIC_API_KEY", "env:EMPTY"), []string{`9: The environment variable EMPTY is not set.`}},
		{"missing file", replace(t, "file:/keys/openai", "file:/keys/none"), []string{`13: Tollgate cannot read the file /keys/none that has the API key of the provider "openai".`}},
		{"unknown kind", replace(t, "kind: openai\n    base_url: https://api.openai.com", "kind: gemini\n    base_url: https://api.openai.com"), []string{`11: The provider kind "gemini" is not known.`}},
		{"later kind", replace(t, "kind: openai\n    base_url: https://api.openai.com", "kind: bedrock\n    base_url: https://api.openai.com"), []string{`11: The provider kind "bedrock" is not available in this version.`}},
		{"base url with v1", replace(t, "base_url: https://api.openai.com", "base_url: https://api.openai.com/v1"), []string{"12: The base_url of the provider \"openai\" ends with /v1."}},
		{"base url not http", replace(t, "base_url: https://api.openai.com", "base_url: ftp://x"), []string{`12: The base_url "ftp://x" is not valid.`}},
		{"undefined provider", replace(t, "- provider: openai", "- provider: openai2"), []string{`28: The provider "openai2" is not defined.`}},
		{"format mismatch", replace(t, "- provider: anthropic", "- provider: openai"), []string{`21: The provider "openai" uses the openai format, but the route "claude-sonnet-5-5" uses the anthropic format.`}},
		{"same target two times", replace(t, "- provider: openai\n", "- provider: openai\n      - provider: openai\n        model: gpt-test\n"), []string{`29: The route "gpt-test" has the target openai/gpt-test two times.`}},
		{"duplicate route", replace(t, "model: gpt-test\n    api: openai", "model: local-coder\n    api: openai"), []string{
			`29: The model "local-coder" has two openai routes.`,
			`39: The model "gpt-test" of the tenant "personal" has no route.`,
		}},
		{"no api", replace(t, "    api: openai\n    targets:\n      - provider: openai", "    targets:\n      - provider: openai"), []string{`25: The route "gpt-test" has no api.`}},
		{"timeout longer than total", replace(t, "first_token: 45s", "first_token: 20m"), []string{`24: The first_token timeout of the route "claude-sonnet-5-5" is longer than its total timeout.`}},
		{"bad hash", replace(t, testHash, "sha256:abc"), []string{`38: The hash of the key "laptop" is not valid.`}},
		{"uppercase hash", replace(t, testHash, strings.ToUpper(testHash)), []string{`38: The hash of the key "laptop" is not valid.`}},
		{"model without route", replace(t, "models: [claude-sonnet-5-5,", "models: [claude-opus,"), []string{`39: The model "claude-opus" of the tenant "personal" has no route.`}},
		{"no models", replace(t, "    models: [claude-sonnet-5-5, gpt-test, local-coder]\n", ""), []string{`35: The tenant "personal" has no models.`}},
		{"bad tenant id", replace(t, "id: personal", "id: my tenant"), []string{`35: The tenant id "my tenant" is not valid.`}},
		{"duplicate tenant and hash", validConfig + "  - id: personal\n    keys:\n      - id: k\n        hash: " + testHash + "\n    models: [gpt-test]\n", []string{
			`40: The tenant id "personal" is used two times.`,
			`43: The hash of the key "k" is used two times.`,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("t.yaml", []byte(tt.yaml), testEnv())
			ve, ok := errors.AsType[*ValidationError](err)
			if !ok {
				t.Fatalf("err = %v, want a *ValidationError", err)
			}
			var got []string
			for _, e := range ve.Errors {
				got = append(got, fmt.Sprintf("%d: %s", e.Line, e.Message))
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d errors, want %d:\n%s", len(got), len(tt.want), strings.Join(got, "\n"))
			}
			for i := range got {
				if !strings.HasPrefix(got[i], tt.want[i]) {
					t.Errorf("error %d = %q, want prefix %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseCollectsAllErrors(t *testing.T) {
	y := replace(t, "drain_timeout: 30s", "drain_timeout: x\n  extra: 1")
	y = strings.Replace(y, "env:ANTHROPIC_API_KEY", "env:NOT_SET", 1)
	_, err := Parse("dir/t.yaml", []byte(y), testEnv())
	ve, ok := errors.AsType[*ValidationError](err)
	if !ok || len(ve.Errors) != 3 {
		t.Fatalf("err = %v, want 3 errors", err)
	}
	if !strings.HasPrefix(err.Error(), "dir/t.yaml:5: ") {
		t.Errorf("Error() = %q, want the form file:line: message", err.Error())
	}
}

func TestRelativeKeyFile(t *testing.T) {
	y := replace(t, "file:/keys/openai", "file:rel-key")
	cfg, err := Parse("/etc/tollgate/tollgate.yaml", []byte(y), testEnv())
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Providers["openai"].APIKey.Value(); got != "sk-relative" {
		t.Errorf("key = %q, want the key from the file next to the config", got)
	}
}

func TestSecretIsRedacted(t *testing.T) {
	cfg, err := Parse("t.yaml", []byte(validConfig), testEnv())
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Providers["anthropic"]
	var logs strings.Builder
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("x", "provider", p.APIKey)
	out, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]string{
		"%v":     fmt.Sprintf("%v", p),
		"%+v":    fmt.Sprintf("%+v", p),
		"%#v":    fmt.Sprintf("%#v", p),
		"String": p.APIKey.String(),
		"slog":   logs.String(),
		"yaml":   string(out),
	} {
		if strings.Contains(s, "sk-ant-test") {
			t.Errorf("%s output shows the key: %s", name, s)
		}
	}
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"1024", 1024, true},
		{"16MiB", 16 << 20, true},
		{"16 MiB", 16 << 20, true},
		{"2KiB", 2048, true},
		{"1GiB", 1 << 30, true},
		{"10B", 10, true},
		{"-1", 0, false},
		{"1.5MiB", 0, false},
		{"MiB", 0, false},
		{"99999999999GiB", 0, false},
	}
	for _, tt := range tests {
		got, ok := parseSize(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("parseSize(%q) = %d, %v, want %d, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func FuzzConfigLoad(f *testing.F) {
	f.Add([]byte(validConfig))
	f.Add(Starter())
	f.Add([]byte("version: 1\nroutes: [{model: a, api: openai, targets: [{provider: x}]}]\n"))
	f.Add([]byte("a: &a [*a]\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		cfg, err := Parse("f.yaml", data, testEnv())
		if err == nil && cfg == nil {
			t.Fatal("Parse returned no config and no error")
		}
		if err != nil {
			if _, ok := errors.AsType[*ValidationError](err); !ok {
				t.Fatalf("err is %T, want *ValidationError", err)
			}
		}
	})
}

// TestExamples checks that each configuration file in examples/ is valid.
func TestExamples(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "examples", "*", "tollgate.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no example files (err %v)", err)
	}
	for _, f := range files {
		if _, err := Load(f, testEnv()); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}

// limitsConfig is validConfig with the M2 fields. It reads its prices from
// /etc/tollgate/prices.yaml.
const limitsConfig = validConfig + `    limits:
      requests_per_minute: 120
      tokens_per_minute: 2000000
    budgets:
      - window: month
        limit_usd: 50
      - window: session
        limit_usd: 0.25
        mode: soft
store:
  kind: redis
  redis_url: env:REDIS_URL
usage:
  path: data/usage.jsonl
  queue_size: 100
pricing:
  file: prices.yaml
reservation:
  output_tokens: 1000
`

func TestParseLimitsAndBudgets(t *testing.T) {
	cfg, err := Parse("/etc/tollgate/tollgate.yaml", []byte(limitsConfig), testEnv())
	if err != nil {
		t.Fatal(err)
	}
	tn := cfg.Tenants[0]
	if tn.Limits != (Limits{RequestsPerMinute: 120, TokensPerMinute: 2000000}) {
		t.Errorf("limits = %+v", tn.Limits)
	}
	want := []Budget{
		{Window: WindowMonth, LimitUSD: 50_000_000, Mode: ModeHard},
		{Window: WindowSession, LimitUSD: 250_000, Mode: ModeSoft},
	}
	if diff := cmp.Diff(want, tn.Budgets); diff != "" {
		t.Errorf("budgets differ (-want +got):\n%s", diff)
	}
	if !tn.HasHardBudget() {
		t.Error("HasHardBudget() = false, want true")
	}
	if cfg.Store.Kind != StoreRedis || cfg.Store.RedisURL.Value() != "redis://localhost:6379/0" {
		t.Errorf("store = %+v", cfg.Store)
	}
	if cfg.Usage != (Usage{Sink: SinkJSONL, Path: "/etc/tollgate/data/usage.jsonl", QueueSize: 100}) {
		t.Errorf("usage = %+v", cfg.Usage)
	}
	if cfg.Pricing.File != "/etc/tollgate/prices.yaml" || !cfg.Prices.Has("local", "openai", "qwen") {
		t.Errorf("pricing = %+v, prices for local/qwen = %v", cfg.Pricing, cfg.Prices.Has("local", "openai", "qwen"))
	}
	if cfg.Reservation.OutputTokens != 1000 {
		t.Errorf("reservation = %+v", cfg.Reservation)
	}
}

func TestParseM2Defaults(t *testing.T) {
	cfg, err := Parse("/srv/t.yaml", []byte("version: 1\n"), testEnv())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Store.Kind != StoreMemory || cfg.Usage != (Usage{Sink: SinkJSONL, Path: "/srv/data/usage.jsonl", QueueSize: DefaultUsageQueueSize}) || cfg.Reservation.OutputTokens != DefaultReserveOutputTokens {
		t.Errorf("defaults: store %+v, usage %+v, reservation %+v", cfg.Store, cfg.Usage, cfg.Reservation)
	}
	if !cfg.Prices.Has("anthropic", "anthropic", "claude-sonnet-5-5") {
		t.Error("the default price table has no claude-sonnet-5-5")
	}
}

func TestParseM2Errors(t *testing.T) {
	for _, tt := range []struct {
		name, old, repl string
		want            []string
	}{
		{"store kind", "kind: redis", "kind: etcd", []string{`The store kind "etcd" is not known.`}},
		{"redis without url", "  redis_url: env:REDIS_URL\n", "", []string{"The store kind is redis, but the store has no redis_url."}},
		{"memory with url", "kind: redis", "kind: memory", []string{"The store kind is memory, but the store has a redis_url."}},
		{"redis url plain text", "redis_url: env:REDIS_URL", "redis_url: redis://x", []string{"The redis_url of the store is not a reference."}},
		{"redis url not set", "redis_url: env:REDIS_URL", "redis_url: env:NO_REDIS", []string{"The environment variable NO_REDIS is not set. Set it to the URL of the Redis server."}},
		{"redis url not redis", "redis_url: env:REDIS_URL", "redis_url: env:BAD_REDIS_URL", []string{"The redis_url of the store is not a Redis URL."}},
		{"sink postgres", "  path: data/usage.jsonl\n", "  sink: postgres\n", []string{"The usage sink postgres is not available in this version."}},
		{"sink unknown", "  path: data/usage.jsonl\n", "  sink: kafka\n", []string{`The usage sink "kafka" is not known.`}},
		{"queue size", "queue_size: 100", "queue_size: -1", []string{"The value of usage.queue_size is not valid."}},
		{"reservation", "output_tokens: 1000", "output_tokens: 99999999", []string{"The value of reservation.output_tokens is not valid."}},
		{"rpm", "requests_per_minute: 120", "requests_per_minute: -1", []string{`The requests_per_minute of the tenant "personal" is not valid.`}},
		{"tpm", "tokens_per_minute: 2000000", "tokens_per_minute: 20000000000", []string{`The tokens_per_minute of the tenant "personal" is not valid.`}},
		{"window", "window: month", "window: year", []string{`The budget window "year" of the tenant "personal" is not known.`}},
		{"no window", "      - window: month\n        limit_usd: 50\n", "      - limit_usd: 50\n", []string{`A budget of the tenant "personal" has no window.`}},
		{"two windows", "window: session", "window: month", []string{`The tenant "personal" has two month budgets.`}},
		{"no limit", "limit_usd: 50", "limit_usd: 0", []string{`The month budget of the tenant "personal" has no limit_usd.`}},
		{"bad amount", "limit_usd: 50", "limit_usd: 1.0000001", []string{`The value "1.0000001" is not an amount in USD.`}},
		{"negative amount", "limit_usd: 50", "limit_usd: -5", []string{`The value "-5" is not an amount in USD.`}},
		{"mode", "mode: soft", "mode: warn", []string{`The budget mode "warn" of the tenant "personal" is not known.`}},
		{"no price file", "file: prices.yaml", "file: none.yaml", []string{"Tollgate cannot read the price table /etc/tollgate/none.yaml."}},
		{"bad price file", "file: prices.yaml", "file: bad-prices.yaml", []string{"The effective_from date of a/m is not valid."}},
		{"model without price", "file: prices.yaml", "file: ''", []string{
			`The tenant "personal" has a budget, but the price table has no price for the model "claude-sonnet-5-5-20260101" of the provider "anthropic".`,
			`The tenant "personal" has a budget, but the price table has no price for the model "gpt-test" of the provider "openai".`,
			`The tenant "personal" has a budget, but the price table has no price for the model "qwen" of the provider "local".`,
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(limitsConfig, tt.old) {
				t.Fatalf("limitsConfig does not contain %q", tt.old)
			}
			y := strings.Replace(limitsConfig, tt.old, tt.repl, 1)
			_, err := Parse("/etc/tollgate/tollgate.yaml", []byte(y), testEnv())
			ve, ok := errors.AsType[*ValidationError](err)
			if !ok {
				t.Fatalf("err = %v, want a *ValidationError", err)
			}
			if len(ve.Errors) != len(tt.want) {
				t.Fatalf("got %d errors, want %d:\n%s", len(ve.Errors), len(tt.want), ve.Error())
			}
			for i, e := range ve.Errors {
				if !strings.HasPrefix(e.Message, tt.want[i]) {
					t.Errorf("error %d = %q, want prefix %q", i, e.Message, tt.want[i])
				}
			}
		})
	}
}

func TestPriceFileErrorNamesTheFile(t *testing.T) {
	y := strings.Replace(limitsConfig, "file: prices.yaml", "file: bad-prices.yaml", 1)
	_, err := Parse("/etc/tollgate/tollgate.yaml", []byte(y), testEnv())
	if err == nil || !strings.HasPrefix(err.Error(), "/etc/tollgate/bad-prices.yaml:1: ") {
		t.Errorf("Error() = %v, want the form price-file:line: message", err)
	}
}

func TestUSD(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want int64
		str  string
	}{
		{"50", 50_000_000, "50.00"},
		{"0.25", 250_000, "0.25"},
		{"1.000001", 1_000_001, "1.000001"},
		{"1000000000", 1_000_000_000_000_000, "1000000000.00"},
		{"0.1", 100_000, "0.10"},
	} {
		v, ok := parseUSD(tt.in)
		if !ok || v != tt.want || USD(v).String() != tt.str {
			t.Errorf("parseUSD(%q) = %d, %v, String() %q; want %d, %q", tt.in, v, ok, USD(v).String(), tt.want, tt.str)
		}
	}
	for _, in := range []string{"", ".5", "1.", "-1", "1e3", "1000000001", "0.0000001", "1,5", "12345678901"} {
		if _, ok := parseUSD(in); ok {
			t.Errorf("parseUSD(%q) ok = true, want false", in)
		}
	}
}

const reliabilityConfig = `version: 1
providers:
  anthropic:
    kind: anthropic
    api_key: env:ANTHROPIC_API_KEY
  vertex-us:
    kind: vertex
    project: my-project-1
    region: us-east5
  ollama:
    kind: ollama
routes:
  - model: claude-sonnet-5-5
    api: anthropic
    targets:
      - provider: anthropic
      - provider: vertex-us
  - model: local-coder
    api: openai
    targets:
      - provider: ollama
        model: qwen
tenants:
  - id: personal
    models: [claude-sonnet-5-5, local-coder]
retry:
  max_attempts: 4
  budget_ratio: 0
breaker:
  failure_ratio: 0.25
  min_requests: 5
  open_for: 1m
`

func TestParseReliability(t *testing.T) {
	cfg, err := Parse("t.yaml", []byte(reliabilityConfig), testEnv())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(Retry{MaxAttempts: 4, BudgetRatio: 0}, cfg.Retry); diff != "" {
		t.Errorf("retry differs (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(Breaker{FailureRatio: 0.25, MinRequests: 5, OpenFor: Duration(time.Minute)}, cfg.Breaker); diff != "" {
		t.Errorf("breaker differs (-want +got):\n%s", diff)
	}
	if v := cfg.Providers["vertex-us"]; v.BaseURL != "" || v.Project != "my-project-1" || v.Region != "us-east5" {
		t.Errorf("vertex provider = %+v", v)
	}
	if got := cfg.Providers["ollama"].BaseURL; got != "http://localhost:11434" {
		t.Errorf("default ollama base_url = %q", got)
	}
	if len(cfg.Routes[0].Targets) != 2 {
		t.Errorf("targets = %+v, want 2", cfg.Routes[0].Targets)
	}
}

func TestParseReliabilityDefaults(t *testing.T) {
	cfg, err := Parse("t.yaml", []byte("version: 1\n"), testEnv())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(Retry{MaxAttempts: DefaultMaxAttempts, BudgetRatio: DefaultBudgetRatio}, cfg.Retry); diff != "" {
		t.Errorf("retry differs (-want +got):\n%s", diff)
	}
	want := Breaker{FailureRatio: DefaultFailureRatio, MinRequests: DefaultMinRequests, OpenFor: Duration(DefaultOpenFor)}
	if diff := cmp.Diff(want, cfg.Breaker); diff != "" {
		t.Errorf("breaker differs (-want +got):\n%s", diff)
	}
}

func TestParseReliabilityErrors(t *testing.T) {
	for _, tt := range []struct {
		name, old, repl string
		want            []string
	}{
		{"max attempts", "max_attempts: 4", "max_attempts: 11", []string{"The value of retry.max_attempts is not valid."}},
		{"NaN budget ratio", "budget_ratio: 0", "budget_ratio: .nan", []string{"The value of retry.budget_ratio is not valid."}},
		{"NaN failure ratio", "failure_ratio: 0.25", "failure_ratio: .nan", []string{"The value of breaker.failure_ratio is not valid."}},
		{"budget ratio", "budget_ratio: 0", "budget_ratio: 1.5", []string{"The value of retry.budget_ratio is not valid."}},
		{"failure ratio", "failure_ratio: 0.25", "failure_ratio: -1", []string{"The value of breaker.failure_ratio is not valid."}},
		{"min requests", "min_requests: 5", "min_requests: -5", []string{"The value of breaker.min_requests is not valid."}},
		{"no project", "    project: my-project-1\n", "", []string{`The vertex provider "vertex-us" has no project.`}},
		{"bad project", "project: my-project-1", "project: My Project", []string{`The project "My Project" of the provider "vertex-us" is not a Google Cloud project ID.`}},
		{"no region", "    region: us-east5\n", "", []string{`The vertex provider "vertex-us" has no region.`}},
		{"bad region", "region: us-east5", "region: evil.example.com/", []string{`The region "evil.example.com/" of the provider "vertex-us" is not valid.`}},
		{"vertex key", "    region: us-east5\n", "    region: us-east5\n    api_key: env:ANTHROPIC_API_KEY\n", []string{`The vertex provider "vertex-us" has an api_key.`}},
		{"region on anthropic", "    api_key: env:ANTHROPIC_API_KEY\n", "    api_key: env:ANTHROPIC_API_KEY\n    region: us\n", []string{`The provider "anthropic" has a region, but only a vertex provider uses it.`}},
		{"vertex in openai route", "      - provider: ollama", "      - provider: vertex-us", []string{`The provider "vertex-us" uses the anthropic format, but the route "local-coder" uses the openai format.`}},
		{"too many targets", "      - provider: vertex-us\n", func() string {
			var b strings.Builder
			b.WriteString("      - provider: vertex-us\n")
			for i := range 10 {
				fmt.Fprintf(&b, "      - provider: anthropic\n        model: m%d\n", i)
			}
			return b.String()
		}(), []string{`The route "claude-sonnet-5-5" has more than 10 targets.`}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(reliabilityConfig, tt.old) {
				t.Fatalf("reliabilityConfig does not contain %q", tt.old)
			}
			y := strings.Replace(reliabilityConfig, tt.old, tt.repl, 1)
			_, err := Parse("t.yaml", []byte(y), testEnv())
			ve, ok := errors.AsType[*ValidationError](err)
			if !ok {
				t.Fatalf("err = %v, want a *ValidationError", err)
			}
			if len(ve.Errors) != len(tt.want) {
				t.Fatalf("got %d errors, want %d:\n%s", len(ve.Errors), len(tt.want), ve.Error())
			}
			for i, e := range ve.Errors {
				if !strings.HasPrefix(e.Message, tt.want[i]) {
					t.Errorf("error %d = %q, want prefix %q", i, e.Message, tt.want[i])
				}
			}
		})
	}
}

func TestOllamaNeedsNoPrice(t *testing.T) {
	y := strings.Replace(reliabilityConfig, "    models: [claude-sonnet-5-5, local-coder]\n", "    models: [local-coder]\n    budgets: [{window: month, limit_usd: 5}]\n", 1)
	if _, err := Parse("t.yaml", []byte(y), testEnv()); err != nil {
		t.Fatal(err)
	}
}

func TestParseObservability(t *testing.T) {
	y := `version: 1
server:
  admin_listen: "0.0.0.0:9191"
  admin_token: env:ADMIN_TOKEN
  pprof: true
telemetry:
  otlp_endpoint: http://jaeger:4318
`
	cfg, err := Parse("/etc/tollgate/tollgate.yaml", []byte(y), testEnv())
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Server
	if s.AdminListen != "0.0.0.0:9191" || s.AdminToken.Value() != "admin-secret" || !s.PProf {
		t.Errorf("server = %+v, token %q", s, s.AdminToken.Value())
	}
	if got := cfg.Telemetry.OTLPEndpoint; got != "http://jaeger:4318" {
		t.Errorf("otlp_endpoint = %q", got)
	}
}

func TestParseObservabilityErrors(t *testing.T) {
	for _, tt := range []struct {
		name, yaml, want string
	}{
		{"admin address", "server:\n  admin_listen: nowhere\n", `The admin_listen address "nowhere" is not valid.`},
		{"admin same as listen", "server:\n  listen: 127.0.0.1:9000\n  admin_listen: 127.0.0.1:9000\n", "The admin_listen address is the same as the listen address."},
		{"public admin without token", "server:\n  admin_listen: :9090\n", `The admin port ":9090" is not on a loopback address, but the server has no admin_token.`},
		{"admin token not set", "server:\n  admin_token: env:NO_TOKEN\n", "The environment variable NO_TOKEN is not set."},
		{"otlp scheme", "telemetry:\n  otlp_endpoint: grpc://jaeger:4317\n", `The otlp_endpoint "grpc://jaeger:4317" is not valid.`},
		{"otlp no host", "telemetry:\n  otlp_endpoint: http://\n", `The otlp_endpoint "http://" is not valid.`},
		{"otlp user", "telemetry:\n  otlp_endpoint: http://u:p@jaeger:4318\n", "The otlp_endpoint"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("/etc/tollgate/tollgate.yaml", []byte("version: 1\n"+tt.yaml), testEnv())
			ve, ok := errors.AsType[*ValidationError](err)
			if !ok {
				t.Fatalf("err = %v, want a *ValidationError", err)
			}
			if len(ve.Errors) != 1 || !strings.HasPrefix(ve.Errors[0].Message, tt.want) {
				t.Fatalf("errors:\n%s\nwant one error with prefix %q", ve.Error(), tt.want)
			}
		})
	}
}

func TestIsLoopback(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "127.0.0.1": true, "::1": true, "127.1.2.3": true,
		"": false, "0.0.0.0": false, "10.0.0.1": false, "example.com": false,
	} {
		if got := IsLoopback(host); got != want {
			t.Errorf("IsLoopback(%q) = %v, want %v", host, got, want)
		}
	}
}
