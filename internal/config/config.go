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

	"github.com/ykakade/tollgate/internal/pricing"
)

// API is the wire format of a route.
type API string

// The API formats.
const (
	APIAnthropic API = "anthropic"
	APIOpenAI    API = "openai"
)

// Provider kinds. KindAPI gives the API format of each kind.
const (
	KindAnthropic = "anthropic"
	KindOpenAI    = "openai"
	// KindVertex is Claude on Google Cloud Vertex AI. It speaks the
	// Anthropic format (ADR 0024).
	KindVertex = "vertex"
	// KindOllama is a local Ollama server. It speaks the OpenAI format,
	// and its requests cost nothing unless the price table has an entry.
	KindOllama = "ollama"
)

// KindAPI returns the API format of a provider kind, or "" if the kind is
// not known.
func KindAPI(kind string) API {
	switch kind {
	case KindAnthropic, KindVertex:
		return APIAnthropic
	case KindOpenAI, KindOllama:
		return APIOpenAI
	}
	return ""
}

// Defaults.
const (
	DefaultListen       = ":8080"
	DefaultAdminListen  = "127.0.0.1:9090"
	DefaultMaxBodyBytes = 16 << 20
	DefaultDrainTimeout = 10 * time.Minute

	DefaultConnectTimeout    = 5 * time.Second
	DefaultFirstTokenTimeout = 30 * time.Second
	DefaultIdleTimeout       = 60 * time.Second
	DefaultTotalTimeout      = 15 * time.Minute

	DefaultUsagePath           = "./data/usage.jsonl"
	DefaultUsageQueueSize      = 10000
	DefaultReserveOutputTokens = 4096

	DefaultMaxAttempts  = 3
	DefaultBudgetRatio  = 0.1
	DefaultFailureRatio = 0.5
	DefaultMinRequests  = 20
	DefaultOpenFor      = 30 * time.Second
)

// Store kinds.
const (
	StoreMemory = "memory"
	StoreRedis  = "redis"
)

// Usage sinks.
const (
	SinkJSONL = "jsonl"
	SinkNone  = "none"
)

// Budget windows.
const (
	WindowMonth   = "month"
	WindowSession = "session"
)

// Budget modes.
const (
	ModeHard = "hard"
	ModeSoft = "soft"
)

// Upper limits of numeric fields. With these limits, the limit store can
// do its arithmetic in float64 without a loss, as Redis Lua does.
const (
	maxUsageQueueSize      = 1_000_000
	maxReserveOutputTokens = 10_000_000
	maxRequestsPerMinute   = 1_000_000_000
	maxTokensPerMinute     = 10_000_000_000
	maxAttempts            = 10
	maxTargets             = 10
	maxMinRequests         = 1_000_000
)

// maxBodyBytesLimit is the largest value of server.max_body_bytes.
const maxBodyBytesLimit = 1 << 30

// defaultBaseURLs holds the base URL of each provider kind that has one.
// A vertex provider has no entry: its URL comes from its region.
var defaultBaseURLs = map[string]string{
	KindAnthropic: "https://api.anthropic.com",
	KindOpenAI:    "https://api.openai.com",
	KindOllama:    "http://localhost:11434",
}

// laterKinds are provider kinds that the brief plans for a later version.
var laterKinds = map[string]bool{"bedrock": true}

// Config is the complete configuration.
type Config struct {
	Version     int                 `yaml:"version"`
	Server      Server              `yaml:"server"`
	Store       Store               `yaml:"store"`
	Usage       Usage               `yaml:"usage"`
	Pricing     Pricing             `yaml:"pricing"`
	Reservation Reservation         `yaml:"reservation"`
	Providers   map[string]Provider `yaml:"providers"`
	Routes      []Route             `yaml:"routes"`
	Tenants     []Tenant            `yaml:"tenants"`
	Retry       Retry               `yaml:"retry"`
	Breaker     Breaker             `yaml:"breaker"`
	Telemetry   Telemetry           `yaml:"telemetry"`

	// Prices is the price table: the file in Pricing.File, or the table
	// in the binary.
	Prices *pricing.Table `yaml:"-"`
}

// Store holds the settings of the limit store (ADR 0004).
type Store struct {
	// Kind is StoreMemory or StoreRedis.
	Kind     string `yaml:"kind"`
	RedisURL Secret `yaml:"redis_url"`
}

// Usage holds the settings of the usage events.
type Usage struct {
	// Sink is SinkJSONL or SinkNone.
	Sink string `yaml:"sink"`
	// Path is the JSONL file. Load makes a relative path relative to the
	// directory of the configuration file.
	Path      string `yaml:"path"`
	QueueSize int    `yaml:"queue_size"`
}

// Pricing holds the settings of the price table.
type Pricing struct {
	// File is the price table. If it is empty, Tollgate uses the table in
	// the binary. Load makes a relative path relative to the directory of
	// the configuration file.
	File string `yaml:"file"`
}

// Reservation holds the settings of budget reservations (ADR 0018).
type Reservation struct {
	// OutputTokens is the largest number of output tokens that a request
	// reserves. A request with a lower max_tokens reserves that number.
	OutputTokens int64 `yaml:"output_tokens"`
}

// Server holds the settings of the data plane and admin listeners.
type Server struct {
	Listen       string   `yaml:"listen"`
	MaxBodyBytes Size     `yaml:"max_body_bytes"`
	DrainTimeout Duration `yaml:"drain_timeout"`
	// AdminListen is the address of the admin listener, which serves
	// /metrics and, with PProf, the profiles.
	AdminListen string `yaml:"admin_listen"`
	// AdminToken is the bearer token of the admin listener. It is
	// required when AdminListen is not a loopback address.
	AdminToken Secret `yaml:"admin_token"`
	// PProf turns on the net/http/pprof handlers on the admin listener.
	PProf bool `yaml:"pprof"`
}

// Telemetry holds the settings of the traces.
type Telemetry struct {
	// OTLPEndpoint is the URL of an OTLP/HTTP trace receiver, for example
	// http://localhost:4318. Empty turns tracing off.
	OTLPEndpoint string `yaml:"otlp_endpoint"`
}

// Provider is one upstream service.
type Provider struct {
	Kind    string `yaml:"kind"`
	BaseURL string `yaml:"base_url"`
	APIKey  Secret `yaml:"api_key"`
	// Project is the Google Cloud project of a vertex provider.
	Project string `yaml:"project"`
	// Region is the Vertex AI location of a vertex provider: "global",
	// a multi-region such as "us", or a region such as "us-east5".
	Region string `yaml:"region"`
}

// Retry holds the retry rules of all routes (ADR 0021 and ADR 0022).
type Retry struct {
	// MaxAttempts is the largest number of upstream attempts for one
	// request, the first attempt included.
	MaxAttempts int `yaml:"max_attempts"`
	// BudgetRatio is the largest part of requests that can have an
	// extra attempt.
	BudgetRatio float64 `yaml:"budget_ratio"`
}

// Breaker holds the settings of the circuit breakers (ADR 0023).
type Breaker struct {
	// FailureRatio opens a breaker when this part of the recent attempts
	// failed.
	FailureRatio float64 `yaml:"failure_ratio"`
	// MinRequests is the smallest number of recent attempts that can
	// open a breaker.
	MinRequests int `yaml:"min_requests"`
	// OpenFor is the time that an open breaker refuses attempts.
	OpenFor Duration `yaml:"open_for"`
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
	ID      string   `yaml:"id"`
	Keys    []Key    `yaml:"keys"`
	Models  []string `yaml:"models"`
	Limits  Limits   `yaml:"limits"`
	Budgets []Budget `yaml:"budgets"`
}

// Limits are the rate limits of a tenant. A value of 0 means no limit.
type Limits struct {
	RequestsPerMinute int64 `yaml:"requests_per_minute"`
	TokensPerMinute   int64 `yaml:"tokens_per_minute"`
}

// Budget is the largest spend of a tenant in a window.
type Budget struct {
	// Window is WindowMonth or WindowSession.
	Window string `yaml:"window"`
	// LimitUSD is the limit in micro-USD.
	LimitUSD USD `yaml:"limit_usd"`
	// Mode is ModeHard or ModeSoft.
	Mode string `yaml:"mode"`
}

// HasHardBudget reports whether the tenant has a hard budget.
func (t *Tenant) HasHardBudget() bool {
	for _, b := range t.Budgets {
		if b.Mode == ModeHard {
			return true
		}
	}
	return false
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
	cfg.applyDefaults(v)
	v.validate(&cfg)
	if err := v.result(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults(v *validator) {
	if c.Server.Listen == "" {
		c.Server.Listen = DefaultListen
	}
	if c.Server.AdminListen == "" {
		c.Server.AdminListen = DefaultAdminListen
	}
	if c.Server.MaxBodyBytes == 0 {
		c.Server.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if c.Server.DrainTimeout == 0 {
		c.Server.DrainTimeout = Duration(DefaultDrainTimeout)
	}
	if c.Store.Kind == "" {
		c.Store.Kind = StoreMemory
	}
	if c.Usage.Sink == "" {
		c.Usage.Sink = SinkJSONL
	}
	if c.Usage.Path == "" {
		c.Usage.Path = DefaultUsagePath
	}
	if c.Usage.QueueSize == 0 {
		c.Usage.QueueSize = DefaultUsageQueueSize
	}
	if c.Reservation.OutputTokens == 0 {
		c.Reservation.OutputTokens = DefaultReserveOutputTokens
	}
	if c.Retry.MaxAttempts == 0 {
		c.Retry.MaxAttempts = DefaultMaxAttempts
	}
	if !v.isSet("retry", "budget_ratio") {
		c.Retry.BudgetRatio = DefaultBudgetRatio
	}
	if c.Breaker.FailureRatio == 0 {
		c.Breaker.FailureRatio = DefaultFailureRatio
	}
	if c.Breaker.MinRequests == 0 {
		c.Breaker.MinRequests = DefaultMinRequests
	}
	setDefault(&c.Breaker.OpenFor, DefaultOpenFor)
	for i := range c.Tenants {
		for j := range c.Tenants[i].Budgets {
			if c.Tenants[i].Budgets[j].Mode == "" {
				c.Tenants[i].Budgets[j].Mode = ModeHard
			}
		}
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

// Error is one problem in the configuration file, or in a file that it
// names.
type Error struct {
	// File is the file of the problem, if it is not the configuration
	// file.
	File    string
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
		if ce.File != "" {
			b.WriteString(ce.File)
		} else {
			b.WriteString(e.File)
		}
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
	sort.SliceStable(v.errs, func(i, j int) bool {
		a, b := v.errs[i], v.errs[j]
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
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

// isSet reports whether the YAML tree has a node at path.
func (v *validator) isSet(path ...any) bool {
	n := v.root
	if n == nil {
		return false
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for _, step := range path {
		if n = child(n, step); n == nil {
			return false
		}
	}
	return true
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
