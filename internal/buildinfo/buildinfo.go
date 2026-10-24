// Package buildinfo reports the version, commit and toolchain of the running
// binary. The Go toolchain records these values at build time, so no linker
// flags are needed.
package buildinfo

import (
	"fmt"
	"runtime/debug"
	"strings"
)

// develVersion is the version shown when the toolchain recorded no version,
// for example in a build outside a module or a version control checkout.
const develVersion = "devel"

// shortCommitLen is the number of commit hash characters shown by String.
const shortCommitLen = 12

// Info describes one build of a binary.
type Info struct {
	// Version is the module version, for example "v0.1.0" or a pseudo-version.
	Version string
	// Commit is the full version control revision, or empty if not known.
	Commit string
	// CommitTime is the commit time in RFC 3339 format, or empty if not known.
	CommitTime string
	// Modified is true if the working tree had changes at build time.
	Modified bool
	// GoVersion is the version of the toolchain that built the binary.
	GoVersion string
}

// Read returns the build information of the running binary.
func Read() Info {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return Info{Version: develVersion}
	}
	return fromBuildInfo(bi)
}

func fromBuildInfo(bi *debug.BuildInfo) Info {
	info := Info{
		Version:   bi.Main.Version,
		GoVersion: bi.GoVersion,
	}
	if info.Version == "" || info.Version == "(devel)" {
		info.Version = develVersion
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			info.Commit = s.Value
		case "vcs.time":
			info.CommitTime = s.Value
		case "vcs.modified":
			info.Modified = s.Value == "true"
		}
	}
	return info
}

// String formats the information on one line, for example
// "v0.1.0 (commit 1a2b3c4d5e6f, 2026-10-24T10:00:00Z, go1.27.3)".
func (i Info) String() string {
	var details []string
	if i.Commit != "" {
		commit := i.Commit
		if len(commit) > shortCommitLen {
			commit = commit[:shortCommitLen]
		}
		if i.Modified {
			commit += "-dirty"
		}
		details = append(details, "commit "+commit)
	}
	if i.CommitTime != "" {
		details = append(details, i.CommitTime)
	}
	if i.GoVersion != "" {
		details = append(details, i.GoVersion)
	}
	if len(details) == 0 {
		return i.Version
	}
	return fmt.Sprintf("%s (%s)", i.Version, strings.Join(details, ", "))
}
