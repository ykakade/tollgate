package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ykakade/tollgate/internal/config"
)

// TestMain runs main instead of the tests when the test binary is started
// by a test as a Tollgate process. See TestServeSIGTERM.
func TestMain(m *testing.M) {
	if os.Getenv("TOLLGATE_TEST_MAIN") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

func runCmd(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "no command", args: nil, wantCode: exitUsage, wantStderr: "Usage:"},
		{name: "version", args: []string{"version"}, wantCode: exitOK, wantStdout: "tollgate "},
		{name: "version with argument", args: []string{"version", "x"}, wantCode: exitUsage, wantStderr: "takes no arguments"},
		{name: "help", args: []string{"help"}, wantCode: exitOK, wantStdout: "Commands:"},
		{name: "help flag", args: []string{"--help"}, wantCode: exitOK, wantStdout: "Commands:"},
		{name: "unknown command", args: []string{"serve-all"}, wantCode: exitUsage, wantStderr: `The command "serve-all" is not known.`},
		{name: "init", args: []string{"init"}, wantCode: exitOK, wantStdout: "version: 1"},
		{name: "init with argument", args: []string{"init", "x"}, wantCode: exitUsage, wantStderr: `The argument "x" is not known.`},
		{name: "keys without create", args: []string{"keys"}, wantCode: exitUsage, wantStderr: "Usage: tollgate keys create"},
		{name: "keys create without id", args: []string{"keys", "create"}, wantCode: exitUsage, wantStderr: "Give a name for the key with -id."},
		{name: "keys create bad id", args: []string{"keys", "create", "-id", "my laptop"}, wantCode: exitUsage, wantStderr: "Give a name for the key"},
		{name: "keys create help", args: []string{"keys", "create", "-h"}, wantCode: exitOK, wantStderr: "Usage: tollgate keys create -id <name>"},
		{name: "config without validate", args: []string{"config"}, wantCode: exitUsage, wantStderr: "Usage: tollgate config validate"},
		{name: "validate missing file", args: []string{"config", "validate", "-config", "/no/such/file.yaml"}, wantCode: exitError, wantStderr: "does not exist. To make one, run"},
		{name: "serve missing file", args: []string{"serve", "-config", "/no/such/file.yaml"}, wantCode: exitError, wantStderr: "does not exist."},
		{name: "serve bad flag", args: []string{"serve", "-port", "1"}, wantCode: exitUsage, wantStderr: "flag provided but not defined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runCmd(tt.args...)
			if code != tt.wantCode {
				t.Errorf("run() = %d, want %d; stderr: %s", code, tt.wantCode, stderr)
			}
			checkContains(t, "stdout", stdout, tt.wantStdout)
			checkContains(t, "stderr", stderr, tt.wantStderr)
		})
	}
}

func checkContains(t *testing.T, name, got, want string) {
	t.Helper()
	if want == "" {
		if got != "" {
			t.Errorf("%s = %q, want empty", name, got)
		}
		return
	}
	if !strings.Contains(got, want) {
		t.Errorf("%s = %q, want it to contain %q", name, got, want)
	}
}

func TestKeysCreate(t *testing.T) {
	code, stdout, _ := runCmd("keys", "create", "-id", "laptop")
	if code != exitOK {
		t.Fatalf("code = %d", code)
	}
	m := regexp.MustCompile(`(?s)^Key: (tg_[0-9A-Za-z]{43})\n.*\n      - id: laptop\n        hash: (sha256:[0-9a-f]{64})\n$`).FindStringSubmatch(stdout)
	if m == nil {
		t.Fatalf("output does not have the expected form:\n%s", stdout)
	}
	// The printed lines must work in a configuration file.
	starter := strings.Replace(string(config.Starter()), "keys: []", "keys:\n"+strings.Join(strings.Split(stdout, "\n")[6:8], "\n"), 1)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	dir := t.TempDir()
	path := filepath.Join(dir, "tollgate.yaml")
	if err := os.WriteFile(path, []byte(starter), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path, config.OSEnv())
	if err != nil {
		t.Fatalf("the printed lines do not make a valid file: %v\n%s", err, starter)
	}
	if got := cfg.Tenants[0].Keys[0].Hash; got != m[2] {
		t.Errorf("hash in config = %q, want %q", got, m[2])
	}
}

func TestConfigValidate(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.yaml")
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(good, config.Starter(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("version: 1\nserver:\n  listen: x\n  listen_port: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	code, stdout, stderr := runCmd("config", "validate", "-config", good)
	if code != exitOK || !strings.Contains(stdout, "is valid") {
		t.Errorf("good file: code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	code, _, stderr = runCmd("config", "validate", "-config", bad)
	if code != exitError {
		t.Errorf("bad file: code = %d, want %d", code, exitError)
	}
	for _, want := range []string{bad + ":3: The listen address", bad + `:4: The field "listen_port" is not known.`, "has 2 errors"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
}
