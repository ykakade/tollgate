// Package config loads and validates the Tollgate configuration file.
//
// Load reads the file, applies the defaults, resolves the secret references
// and checks every rule. It returns all errors at once, each with its line
// number.
package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// API is the wire format of a route.
type API string

// The API formats.
const (
	APIAnthropic API = "anthropic"
	APIOpenAI    API = "openai"
)

// Provider kinds. A provider kind is also the API format that it speaks.
const (
	KindAnthropic = "anthropic"
	KindOpenAI    = "openai"
)

// Defaults.
const (
	DefaultListen       = ":8080"
	DefaultMaxBodyBytes = 16 << 20
	DefaultDrainTimeout = 10 * time.Minute

	DefaultConnectTimeout    = 5 * time.Second
	DefaultFirstTokenTimeout = 30 * time.Second
	DefaultIdleTimeout       = 60 * time.Second
	DefaultTotalTimeout      = 15 * time.Minute
)

// maxBodyBytesLimit is the largest value of server.max_body_bytes.
const maxBodyBytesLimit = 1 << 30

// defaultBaseURLs holds the base URL of each provider kind that has one.
var defaultBaseURLs = map[string]string{
	KindAnthropic: "https://api.anthropic.com",
	KindOpenAI:    "https://api.openai.com",
}

// laterKinds are provider kinds that the brief plans for a later version.
var laterKinds = map[string]bool{"vertex": true, "bedrock": true}

// Config is the complete configuration.
type Config struct {
	Version   int                 `yaml:"version"`
	Server    Server              `yaml:"server"`
	Providers map[string]Provider `yaml:"providers"`
	Routes    []Route             `yaml:"routes"`
	Tenants   []Tenant            `yaml:"tenants"`
}

// Server holds the settings of the data plane listener.
type Server struct {
	Listen       string   `yaml:"listen"`
	MaxBodyBytes Size     `yaml:"max_body_bytes"`
	DrainTimeout Duration `yaml:"drain_timeout"`
}

// Provider is one upstream service.
type Provider struct {
	Kind    string `yaml:"kind"`
	BaseURL string `yaml:"base_url"`
	APIKey  Secret `yaml:"api_key"`
}

// Route maps a model name that clients send to its targets.
type Route struct {
	Model    string   `yaml:"model"`
	API      API      `yaml:"api"`
	Targets  []Target `yaml:"targets"`
	Timeouts Timeouts `yaml:"timeouts"`
}

// Target is one provider and one model of a route.
type Target struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

// Timeouts are the four timeouts of an upstream request. See ADR 0016.
type Timeouts struct {
	Connect    Duration `yaml:"connect"`
	FirstToken Duration `yaml:"first_token"`
	Idle       Duration `yaml:"idle"`
	Total      Duration `yaml:"total"`
}

// Tenant is a user or a team with its own keys.
type Tenant struct {
	ID     string   `yaml:"id"`
	Keys   []Key    `yaml:"keys"`
	Models []string `yaml:"models"`
}

// Key is one virtual key of a tenant. Hash is "sha256:" and the hex SHA-256
// of the key.
type Key struct {
	ID   string `yaml:"id"`
	Hash string `yaml:"hash"`
}

// Env gives Load access to environment variables and files, for secret
// references. Tests replace it.
type Env struct {
	LookupEnv func(string) (string, bool)
	ReadFile  func(string) ([]byte, error)
}

// OSEnv returns an Env that uses the process environment and file system.
func OSEnv() Env {
	return Env{LookupEnv: os.LookupEnv, ReadFile: os.ReadFile}
}

//go:embed starter.yaml
var starter []byte

// Starter returns the commented starter configuration that "tollgate init"
// writes.
func Starter() []byte {
	return bytes.Clone(starter)
}

// Load reads and validates the configuration file at path.
func Load(path string, env Env) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // The operator gives the path of the configuration file.
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(path, data, env)
}

// Parse validates the configuration in data. The name is the file name for
// error messages. Relative file: references are relative to the directory of
// name. On failure, the error is a *ValidationError.
func Parse(name string, data []byte, env Env) (*Config, error) {
	v := &validator{file: name, dir: filepath.Dir(name), env: env}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		v.yamlError(err)
		return nil, v.result()
	}
	if root.Kind == 0 {
		v.add(0, "The file is empty. Run \"tollgate init\" to make a starter file.")
		return nil, v.result()
	}
	v.root = &root

	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		v.yamlError(err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		v.add(extra.Line, "The file has more than one YAML document. Use one document.")
	} else if !errors.Is(err, io.EOF) {
		v.yamlError(err)
	}
	cfg.applyDefaults()
	v.validate(&cfg)
	if err := v.result(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Server.Listen == "" {
		c.Server.Listen = DefaultListen
	}
	if c.Server.MaxBodyBytes == 0 {
		c.Server.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if c.Server.DrainTimeout == 0 {
		c.Server.DrainTimeout = Duration(DefaultDrainTimeout)
	}
	for name, p := range c.Providers {
		if p.BaseURL == "" {
			p.BaseURL = defaultBaseURLs[p.Kind]
			c.Providers[name] = p
		}
	}
	for i := range c.Routes {
		r := &c.Routes[i]
		for j := range r.Targets {
			if r.Targets[j].Model == "" {
				r.Targets[j].Model = r.Model
			}
		}
		t := &r.Timeouts
		setDefault(&t.Connect, DefaultConnectTimeout)
		setDefault(&t.FirstToken, DefaultFirstTokenTimeout)
		setDefault(&t.Idle, DefaultIdleTimeout)
		setDefault(&t.Total, DefaultTotalTimeout)
	}
}

func setDefault(d *Duration, def time.Duration) {
	if *d == 0 {
		*d = Duration(def)
	}
}

// Error is one problem in the configuration file.
type Error struct {
	Line    int // 0 if the problem has no line
	Message string
}

// ValidationError holds all problems in a configuration file.
type ValidationError struct {
	File   string
	Errors []Error
}

// Error returns one line for each problem, in the form "file:line: message".
func (e *ValidationError) Error() string {
	var b strings.Builder
	for i, ce := range e.Errors {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e.File)
		if ce.Line > 0 {
			b.WriteByte(':')
			b.WriteString(strconv.Itoa(ce.Line))
		}
		b.WriteString(": ")
		b.WriteString(ce.Message)
	}
	return b.String()
}

type validator struct {
	file string
	dir  string
	env  Env
	root *yaml.Node
	errs []Error
	// decodeLines holds the lines with an error from the YAML decoder.
	decodeLines map[int]bool
}

func (v *validator) add(line int, format string, args ...any) {
	// A value that YAML cannot decode stays empty, and the rule checks
	// would then report it a second time. Keep only the first error.
	if line > 0 && v.decodeLines[line] {
		return
	}
	v.errs = append(v.errs, Error{Line: line, Message: fmt.Sprintf(format, args...)})
}

func (v *validator) result() error {
	if len(v.errs) == 0 {
		return nil
	}
	sort.SliceStable(v.errs, func(i, j int) bool { return v.errs[i].Line < v.errs[j].Line })
	return &ValidationError{File: v.file, Errors: v.errs}
}

var (
	lineRE         = regexp.MustCompile(`^(?:yaml: )?line (\d+): (.*)$`)
	unknownFieldRE = regexp.MustCompile(`^field (\S+) not found in type \S+$`)
	unmarshalRE    = regexp.MustCompile("^cannot unmarshal !!(\\w+) (?:`(.*)` )?into (\\S+)$")
)

// yamlError adds the errors of the YAML decoder, with messages in the words
// of the Tollgate documents.
func (v *validator) yamlError(err error) {
	var msgs []string
	if te, ok := errors.AsType[*yaml.TypeError](err); ok {
		msgs = te.Errors
	} else {
		msgs = []string{err.Error()}
	}
	for _, m := range msgs {
		line := 0
		if sm := lineRE.FindStringSubmatch(m); sm != nil {
			line, _ = strconv.Atoi(sm[1])
			m = sm[2]
		}
		v.errs = append(v.errs, Error{Line: line, Message: yamlMessage(m)})
		if line > 0 {
			if v.decodeLines == nil {
				v.decodeLines = make(map[int]bool)
			}
			v.decodeLines[line] = true
		}
	}
}

func yamlMessage(m string) string {
	if sm := unknownFieldRE.FindStringSubmatch(m); sm != nil {
		return fmt.Sprintf("The field %q is not known. Remove it, or correct its name. See docs/configuration.md.", sm[1])
	}
	if sm := unmarshalRE.FindStringSubmatch(m); sm != nil {
		want := sm[3]
		switch {
		case strings.HasPrefix(want, "[]"):
			want = "a list"
		case strings.HasPrefix(want, "map[") || strings.HasPrefix(want, "config."):
			want = "a mapping"
		case strings.HasPrefix(want, "int"):
			want = "a number"
		}
		if sm[2] != "" {
			return fmt.Sprintf("The value %q has the wrong type. Use %s.", sm[2], want)
		}
		return fmt.Sprintf("The value has the wrong type. Use %s.", want)
	}
	if strings.HasPrefix(m, "The ") {
		return m
	}
	return "The YAML is not valid: " + m + "."
}

// line returns the line of the node at path in the YAML tree. A string in
// the path is a mapping key and an int is a list index. If a step is not in
// the tree, line returns the line of the last node that it found.
func (v *validator) line(path ...any) int {
	n := v.root
	if n == nil {
		return 0
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for _, step := range path {
		next := child(n, step)
		if next == nil {
			break
		}
		n = next
	}
	return n.Line
}

func child(n *yaml.Node, step any) *yaml.Node {
	switch s := step.(type) {
	case string:
		if n.Kind != yaml.MappingNode {
			return nil
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == s {
				return n.Content[i+1]
			}
		}
	case int:
		if n.Kind == yaml.SequenceNode && s >= 0 && s < len(n.Content) {
			return n.Content[s]
		}
	}
	return nil
}
