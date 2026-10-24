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
	files := map[string]string{"/keys/openai": "sk-from-file\n", "/etc/tollgate/rel-key": "sk-relative"}
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
	want := Server{Listen: DefaultListen, MaxBodyBytes: DefaultMaxBodyBytes, DrainTimeout: Duration(DefaultDrainTimeout)}
	if diff := cmp.Diff(want, cfg.Server); diff != "" {
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
		{"unknown top-level field", validConfig + "store:\n  kind: memory\n", []string{`40: The field "store" is not known.`}},
		{"bad duration", replace(t, "drain_timeout: 30s", "drain_timeout: soon"), []string{`5: The value "soon" is not a duration.`}},
		{"negative duration", replace(t, "drain_timeout: 30s", "drain_timeout: -1s"), []string{`5: The value "-1s" is not a duration.`}},
		{"bad size", replace(t, "max_body_bytes: 4MiB", "max_body_bytes: 4MB"), []string{`4: The value "4MB" is not a size.`}},
		{"size too large", replace(t, "max_body_bytes: 4MiB", "max_body_bytes: 2GiB"), []string{"4: The value of max_body_bytes is too large."}},
		{"bad listen", replace(t, `listen: "127.0.0.1:8081"`, `listen: "8081"`), []string{`3: The listen address "8081" is not valid.`}},
		{"wrong type", replace(t, "version: 1", "version: one"), []string{`1: The value "one" has the wrong type. Use a number.`}},
		{"plain-text key", replace(t, "api_key: env:ANTHROPIC_API_KEY", "api_key: sk-ant-secret"), []string{`9: The api_key of the provider "anthropic" is not a reference.`}},
		{"missing variable", replace(t, "env:ANTHROPIC_API_KEY", "env:NOT_SET"), []string{`9: The environment variable NOT_SET is not set.`}},
		{"empty variable", replace(t, "env:ANTHROPIC_API_KEY", "env:EMPTY"), []string{`9: The environment variable EMPTY is not set.`}},
		{"missing file", replace(t, "file:/keys/openai", "file:/keys/none"), []string{`13: Tollgate cannot read the key file /keys/none`}},
		{"unknown kind", replace(t, "kind: openai\n    base_url: https://api.openai.com", "kind: gemini\n    base_url: https://api.openai.com"), []string{`11: The provider kind "gemini" is not known.`}},
		{"later kind", replace(t, "kind: openai\n    base_url: https://api.openai.com", "kind: vertex\n    base_url: https://api.openai.com"), []string{`11: The provider kind "vertex" is not available in this version.`}},
		{"base url with v1", replace(t, "base_url: https://api.openai.com", "base_url: https://api.openai.com/v1"), []string{"12: The base_url of the provider \"openai\" ends with /v1."}},
		{"base url not http", replace(t, "base_url: https://api.openai.com", "base_url: ftp://x"), []string{`12: The base_url "ftp://x" is not valid.`}},
		{"undefined provider", replace(t, "- provider: openai", "- provider: openai2"), []string{`28: The provider "openai2" is not defined.`}},
		{"format mismatch", replace(t, "- provider: anthropic", "- provider: openai"), []string{`21: The provider "openai" uses the openai format, but the route "claude-sonnet-5-5" uses the anthropic format.`}},
		{"two targets", replace(t, "- provider: openai\n", "- provider: openai\n      - provider: local\n"), []string{`29: The route "gpt-test" has more than one target.`}},
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
