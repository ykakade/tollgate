// Package pricing reads the price table and calculates the cost of a
// request in micro-USD (ADR 0011).
//
// The price table is a list of entries. Each entry gives the prices of one
// model of one provider from a date. To change a price, add an entry with a
// later date. Do not edit an old entry, because old usage events keep the
// cost that the old entry gave.
package pricing

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/ykakade/tollgate/internal/provider"
)

// Limits of the values in a price table. With these limits, the cost of
// one request fits in an int64.
const (
	// MaxPrice is the largest price: 1,000 USD for each million tokens.
	MaxPrice = 1_000_000_000
	// maxTokens is the largest token count that Cost uses for each kind.
	// A larger count is treated as maxTokens.
	maxTokens = 1_000_000_000
)

const dateLayout = time.DateOnly

// Prices are prices in micro-USD for each million tokens.
type Prices struct {
	Input      int64 `yaml:"input"`
	Output     int64 `yaml:"output"`
	CacheRead  int64 `yaml:"cache_read"`
	CacheWrite int64 `yaml:"cache_write"`
	// CacheWrite1h is the price of a write to the 1-hour prompt cache. If
	// it is 0, Cost uses CacheWrite.
	CacheWrite1h int64 `yaml:"cache_write_1h"`
}

// LongContext holds the prices of a request with a long prompt.
type LongContext struct {
	// AbovePromptTokens is the threshold. A request whose prompt has more
	// tokens uses Prices. The prompt counts the input, cache read and
	// cache write tokens.
	AbovePromptTokens int64  `yaml:"above_prompt_tokens"`
	Prices            Prices `yaml:"micro_usd_per_mtok"`
}

// Entry is the price of one model of one provider from a date.
type Entry struct {
	// Provider is a provider name of the configuration or a provider
	// kind. An entry for a name has priority over an entry for a kind.
	Provider      string       `yaml:"provider"`
	Model         string       `yaml:"model"`
	EffectiveFrom string       `yaml:"effective_from"`
	Prices        Prices       `yaml:"micro_usd_per_mtok"`
	LongContext   *LongContext `yaml:"long_context"`

	from time.Time
}

// Table is a validated price table.
type Table struct {
	// entries holds the entries for each provider and model, newest first.
	entries map[key][]*Entry
}

type key struct{ provider, model string }

// Error is one problem in a price table.
type Error struct {
	Line    int
	Message string
}

func (e Error) Error() string {
	return fmt.Sprintf("line %d: %s", e.Line, e.Message)
}

// Parse reads and validates a price table. On failure, it returns every
// problem that it found.
func Parse(data []byte) (*Table, []Error) {
	var errs []Error
	add := func(line int, format string, args ...any) {
		errs = append(errs, Error{line, fmt.Sprintf(format, args...)})
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		add(yamlLine(err), "The price table is not valid YAML. Correct the file.")
		return nil, errs
	}
	if root.Kind == 0 || len(root.Content) == 0 || root.Content[0].Kind != yaml.SequenceNode {
		add(1, "The price table must be a list of entries.")
		return nil, errs
	}
	items := root.Content[0].Content

	var entries []*Entry
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&entries); err != nil && !errors.Is(err, io.EOF) {
		if te, ok := errors.AsType[*yaml.TypeError](err); ok {
			for _, m := range te.Errors {
				add(yamlLine(errors.New(m)), "%s", yamlMessage(m))
			}
		} else {
			add(yamlLine(err), "The price table is not valid YAML. Correct the file.")
		}
		return nil, errs
	}

	t := &Table{entries: make(map[key][]*Entry)}
	for i, e := range entries {
		line := items[i].Line
		if e == nil {
			add(line, "An entry of the price table is empty. Remove it.")
			continue
		}
		if e.Provider == "" || e.Model == "" {
			add(line, "An entry of the price table has no provider or no model. Add both.")
			continue
		}
		from, err := time.Parse(dateLayout, e.EffectiveFrom)
		if err != nil {
			add(line, "The effective_from date of %s/%s is not valid. Use the format YYYY-MM-DD.", e.Provider, e.Model)
			continue
		}
		e.from = from
		checkPrices(e.Prices, func(field string) {
			add(line, "The price %s of %s/%s is not valid. Use a number from 0 to %d micro-USD for each million tokens.", field, e.Provider, e.Model, MaxPrice)
		})
		if lc := e.LongContext; lc != nil {
			if lc.AbovePromptTokens <= 0 {
				add(line, "The long_context of %s/%s has no above_prompt_tokens. Set it to the threshold in tokens.", e.Provider, e.Model)
			}
			checkPrices(lc.Prices, func(field string) {
				add(line, "The long_context price %s of %s/%s is not valid. Use a number from 0 to %d micro-USD for each million tokens.", field, e.Provider, e.Model, MaxPrice)
			})
		}
		k := key{e.Provider, e.Model}
		if slices.ContainsFunc(t.entries[k], func(o *Entry) bool { return o.from.Equal(from) }) {
			add(line, "The price table has two entries for %s/%s from %s. Remove one of them.", e.Provider, e.Model, e.EffectiveFrom)
			continue
		}
		t.entries[k] = append(t.entries[k], e)
	}
	if len(errs) > 0 {
		return nil, errs
	}
	for _, list := range t.entries {
		slices.SortFunc(list, func(a, b *Entry) int { return b.from.Compare(a.from) })
	}
	return t, nil
}

func checkPrices(p Prices, bad func(field string)) {
	for _, f := range []struct {
		name string
		v    int64
	}{
		{"input", p.Input},
		{"output", p.Output},
		{"cache_read", p.CacheRead},
		{"cache_write", p.CacheWrite},
		{"cache_write_1h", p.CacheWrite1h},
	} {
		if f.v < 0 || f.v > MaxPrice {
			bad(f.name)
		}
	}
}

// Find returns the entry for a model of a provider at time at. It first
// tries the provider name, then the provider kind. It returns nil if the
// table has no entry from at or earlier.
func (t *Table) Find(name, kind, model string, at time.Time) *Entry {
	if t == nil {
		return nil
	}
	for _, p := range []string{name, kind} {
		for _, e := range t.entries[key{p, model}] {
			if !e.from.After(at) {
				return e
			}
		}
	}
	return nil
}

// Has reports whether the table has an entry for the model at any date.
func (t *Table) Has(name, kind, model string) bool {
	return t.Find(name, kind, model, time.Unix(1<<40, 0)) != nil
}

// Cost returns the cost of u in micro-USD, rounded up (ADR 0011).
func (e *Entry) Cost(u provider.Usage) int64 {
	p := e.Prices
	if lc := e.LongContext; lc != nil && u.InputTokens+u.CacheReadTokens+u.CacheWriteTokens > lc.AbovePromptTokens {
		p = lc.Prices
	}
	write1h := p.CacheWrite1h
	if write1h == 0 {
		write1h = p.CacheWrite
	}
	write5m := max(u.CacheWriteTokens-u.CacheWrite1hTokens, 0)
	var sum int64
	for _, part := range [...][2]int64{
		{u.InputTokens, p.Input},
		{u.OutputTokens, p.Output},
		{u.CacheReadTokens, p.CacheRead},
		{write5m, p.CacheWrite},
		{min(u.CacheWrite1hTokens, u.CacheWriteTokens), write1h},
	} {
		sum += min(max(part[0], 0), maxTokens) * part[1]
	}
	return (sum + 999_999) / 1_000_000
}

// String returns the provider and the model of the entry.
func (e *Entry) String() string {
	return e.Provider + "/" + e.Model
}

func yamlLine(err error) int {
	var line int
	m := strings.TrimPrefix(err.Error(), "yaml: ")
	if _, scanErr := fmt.Sscanf(m, "line %d:", &line); scanErr != nil {
		return 0
	}
	return line
}

func yamlMessage(m string) string {
	m = strings.TrimPrefix(m, "yaml: ")
	if _, rest, ok := strings.Cut(m, ": "); ok && strings.HasPrefix(m, "line ") {
		m = rest
	}
	if field, ok := strings.CutPrefix(m, "field "); ok {
		name, _, _ := strings.Cut(field, " ")
		return fmt.Sprintf("The field %q is not known. Remove it, or correct its name.", name)
	}
	return "The value has the wrong type: " + m + "."
}
