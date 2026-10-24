package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestRunStopsWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var stderr bytes.Buffer
	if err := run(ctx, []string{"-listen", "127.0.0.1:0"}, &stderr); err != nil {
		t.Fatalf("run() = %v, want nil", err)
	}
	for _, msg := range []string{"fake provider started", "fake provider stopping"} {
		if !strings.Contains(stderr.String(), msg) {
			t.Errorf("log = %q, want it to contain %q", stderr.String(), msg)
		}
	}
}

func TestRunErrors(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr error
	}{
		{name: "help", args: []string{"-h"}, wantErr: flag.ErrHelp},
		{name: "unknown flag", args: []string{"-port", "1"}},
		{name: "bad address", args: []string{"-listen", "256.0.0.1:1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(t.Context(), tt.args, new(bytes.Buffer))
			if err == nil {
				t.Fatal("run() = nil, want an error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("run() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
