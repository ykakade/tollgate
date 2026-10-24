package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestFromBuildInfo(t *testing.T) {
	tests := []struct {
		name string
		in   *debug.BuildInfo
		want Info
	}{
		{
			name: "release build",
			in: &debug.BuildInfo{
				GoVersion: "go1.27.3",
				Main:      debug.Module{Version: "v0.1.0"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "1a2b3c4d5e6f7a8b9c0d"},
					{Key: "vcs.time", Value: "2026-10-24T10:00:00Z"},
					{Key: "vcs.modified", Value: "false"},
				},
			},
			want: Info{
				Version:    "v0.1.0",
				Commit:     "1a2b3c4d5e6f7a8b9c0d",
				CommitTime: "2026-10-24T10:00:00Z",
				GoVersion:  "go1.27.3",
			},
		},
		{
			name: "local build with changes",
			in: &debug.BuildInfo{
				GoVersion: "go1.27.3",
				Main:      debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abc"},
					{Key: "vcs.modified", Value: "true"},
				},
			},
			want: Info{
				Version:   "devel",
				Commit:    "abc",
				Modified:  true,
				GoVersion: "go1.27.3",
			},
		},
		{
			name: "no version control data",
			in:   &debug.BuildInfo{GoVersion: "go1.27.3"},
			want: Info{Version: "devel", GoVersion: "go1.27.3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fromBuildInfo(tt.in); got != tt.want {
				t.Errorf("fromBuildInfo() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestInfoString(t *testing.T) {
	tests := []struct {
		name string
		in   Info
		want string
	}{
		{
			name: "all fields",
			in: Info{
				Version:    "v0.1.0",
				Commit:     "1a2b3c4d5e6f7a8b9c0d",
				CommitTime: "2026-10-24T10:00:00Z",
				GoVersion:  "go1.27.3",
			},
			want: "v0.1.0 (commit 1a2b3c4d5e6f, 2026-10-24T10:00:00Z, go1.27.3)",
		},
		{
			name: "modified tree",
			in:   Info{Version: "devel", Commit: "abc", Modified: true, GoVersion: "go1.27.3"},
			want: "devel (commit abc-dirty, go1.27.3)",
		},
		{
			name: "version only",
			in:   Info{Version: "devel"},
			want: "devel",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadHasGoVersion(t *testing.T) {
	if got := Read(); got.GoVersion == "" {
		t.Errorf("Read().GoVersion is empty, want the toolchain version")
	}
}
