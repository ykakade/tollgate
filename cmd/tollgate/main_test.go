package main

import (
	"bytes"
	"strings"
	"testing"
)

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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("run() = %d, want %d", code, tt.wantCode)
			}
			checkContains(t, "stdout", stdout.String(), tt.wantStdout)
			checkContains(t, "stderr", stderr.String(), tt.wantStderr)
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
