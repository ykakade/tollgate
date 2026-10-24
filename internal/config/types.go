package config

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Duration is a time.Duration that YAML reads in Go syntax, such as "30s".
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil || v < 0 || n.Kind != yaml.ScalarNode {
		return typeError(n, "The value %q is not a duration. Use Go syntax, for example 30s or 10m.", n.Value)
	}
	*d = Duration(v)
	return nil
}

// Std returns d as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// String returns d in Go syntax.
func (d Duration) String() string { return time.Duration(d).String() }

// Size is a number of bytes. YAML reads it as a plain number or as a number
// with the unit KiB, MiB or GiB, such as "16MiB".
type Size int64

var sizeUnits = []struct {
	suffix string
	factor int64
}{
	{"GiB", 1 << 30},
	{"MiB", 1 << 20},
	{"KiB", 1 << 10},
	{"B", 1},
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (s *Size) UnmarshalYAML(n *yaml.Node) error {
	v, ok := parseSize(n.Value)
	if !ok || n.Kind != yaml.ScalarNode {
		return typeError(n, "The value %q is not a size. Use a number of bytes, or a number with KiB, MiB or GiB, for example 16MiB.", n.Value)
	}
	*s = Size(v)
	return nil
}

func parseSize(text string) (int64, bool) {
	text = strings.TrimSpace(text)
	factor := int64(1)
	for _, u := range sizeUnits {
		if num, found := strings.CutSuffix(text, u.suffix); found {
			text = strings.TrimSpace(num)
			factor = u.factor
			break
		}
	}
	v, err := strconv.ParseInt(text, 10, 64)
	if err != nil || v < 0 || v > (1<<62)/factor {
		return 0, false
	}
	return v * factor, true
}

// Secret is a reference to a secret value: "env:NAME" for an environment
// variable or "file:/path" for a file. Load resolves the reference. The
// String, LogValue and MarshalYAML methods never show the value.
type Secret struct {
	ref   string
	line  int
	value string
}

// UnmarshalYAML implements yaml.Unmarshaler. It keeps only the reference.
// Load resolves it.
func (s *Secret) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return typeError(n, "The value is not text. Use env:NAME or file:/path.")
	}
	*s = Secret{ref: n.Value, line: n.Line}
	return nil
}

// IsSet reports whether the configuration has a reference for the secret.
func (s Secret) IsSet() bool { return s.ref != "" }

// Value returns the resolved secret.
func (s Secret) Value() string { return s.value }

// String implements fmt.Stringer. It never returns the value.
func (s Secret) String() string { return redacted }

// LogValue implements slog.LogValuer. It never returns the value.
func (s Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalYAML implements yaml.Marshaler. It never returns the value.
func (s Secret) MarshalYAML() (any, error) { return redacted, nil }

// GoString implements fmt.GoStringer, so %#v does not show the value.
func (s Secret) GoString() string { return redacted }

const redacted = "<redacted>"

// typeError returns a yaml.TypeError for node n. The YAML decoder collects
// these errors and continues, so Load can show all errors at once.
func typeError(n *yaml.Node, format string, args ...any) error {
	return &yaml.TypeError{Errors: []string{fmt.Sprintf("line %d: %s", n.Line, fmt.Sprintf(format, args...))}}
}
