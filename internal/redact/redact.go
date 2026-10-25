// Package redact masks personal data in request bodies and restores it in
// responses (ADR 0031).
//
// Mask finds values such as email addresses in the JSON strings of a
// request body and replaces each value with a placeholder, for example
// "[EMAIL_1]". The provider sees only the placeholders. The model can copy a
// placeholder into its reply, and Unmask then puts the original value back
// before the reply goes to the client.
//
// The same value gets the same placeholder, and placeholders are numbered in
// the order of the first time each value occurs in the body. An agent sends
// the full conversation with each request, so the placeholders of a prefix
// do not change from one request to the next, and the provider prompt cache
// stays valid.
package redact

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The built-in kinds.
const (
	KindCreditCard = "credit_card"
	KindEmail      = "email"
	KindIPv4       = "ipv4"
	KindUSSSN      = "us_ssn"
)

type builtin struct {
	name string
	re   *regexp.Regexp
	// check, if not nil, reports whether the match at loc in s is a real
	// value.
	check func(s string, loc []int) bool
}

var builtins = map[string]builtin{
	KindCreditCard: {"CARD", regexp.MustCompile(`\b\d(?:[ -]?\d){12,18}\b`), func(s string, loc []int) bool { return luhn(s[loc[0]:loc[1]]) }},
	KindEmail:      {"EMAIL", regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`), nil},
	KindIPv4:       {"IP", regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\b`), notDotted},
	KindUSSSN:      {"SSN", regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`), nil},
}

// Kinds returns the names of the built-in kinds, in order.
func Kinds() []string {
	out := make([]string, 0, len(builtins))
	for k := range builtins {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// KnownKind reports whether k is a built-in kind.
func KnownKind(k string) bool {
	_, ok := builtins[k]
	return ok
}

// maxPatternLen is the longest regular expression of a pattern.
const maxPatternLen = 1024

// CheckPattern returns an error if expr cannot be the regular expression
// of a pattern: it must compile, and it must not match empty text.
func CheckPattern(expr string) error {
	if expr == "" {
		return errors.New("the regex is empty")
	}
	if len(expr) > maxPatternLen {
		return fmt.Errorf("the regex is longer than %d characters", maxPatternLen)
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return fmt.Errorf("compile: %w", err)
	}
	if re.MatchString("") {
		return errors.New("the regex matches empty text")
	}
	return nil
}

// Pattern is a detector with a regular expression.
type Pattern struct {
	// Name goes into the placeholder, for example "EMPLOYEE_ID".
	Name  string
	Regex string
}

type detector struct {
	name  string
	re    *regexp.Regexp
	check func(s string, loc []int) bool
}

// Redactor masks and restores the values of a set of detectors. It is safe
// for concurrent use.
type Redactor struct {
	detectors []detector
}

// New returns a Redactor for the built-in kinds and the patterns.
func New(kinds []string, patterns []Pattern) (*Redactor, error) {
	r := &Redactor{}
	for _, k := range kinds {
		b, ok := builtins[k]
		if !ok {
			return nil, fmt.Errorf("unknown kind %q", k)
		}
		r.detectors = append(r.detectors, detector(b))
	}
	for _, p := range patterns {
		if err := CheckPattern(p.Regex); err != nil {
			return nil, fmt.Errorf("pattern %s: %w", p.Name, err)
		}
		r.detectors = append(r.detectors, detector{name: p.Name, re: regexp.MustCompile(p.Regex)})
	}
	return r, nil
}

// Mapping holds the placeholders of one request and their original values.
type Mapping struct {
	byValue map[string]string // original value → placeholder
	values  map[string]string // placeholder → original value
	counts  map[string]int    // detector name → last number
	// maxLen is the length of the longest placeholder.
	maxLen int
}

// Len returns the number of placeholders.
func (m *Mapping) Len() int {
	if m == nil {
		return 0
	}
	return len(m.values)
}

func (m *Mapping) placeholder(name, value string) string {
	if p, ok := m.byValue[value]; ok {
		return p
	}
	m.counts[name]++
	p := "[" + name + "_" + strconv.Itoa(m.counts[name]) + "]"
	m.byValue[value] = p
	m.values[p] = value
	m.maxLen = max(m.maxLen, len(p))
	return p
}

// Mask returns body with each match in its JSON strings replaced by a
// placeholder. Only the strings with a match change; Mask writes them
// again with the standard JSON escapes. If nothing matched, Mask returns
// body and an empty mapping. Body must be valid JSON.
func (r *Redactor) Mask(body []byte) ([]byte, *Mapping, error) {
	m := &Mapping{byValue: make(map[string]string), values: make(map[string]string), counts: make(map[string]int)}
	out, err := rewriteStrings(body, func(s string) (string, bool) {
		return r.maskText(s, m)
	})
	if err != nil {
		return nil, nil, err
	}
	return out, m, nil
}

// maskText replaces the matches in s. The detectors run in order, and a
// later detector does not see the text that an earlier detector replaced.
func (r *Redactor) maskText(s string, m *Mapping) (string, bool) {
	changed := false
	for _, d := range r.detectors {
		locs := d.re.FindAllStringIndex(s, -1)
		if len(locs) == 0 {
			continue
		}
		var b strings.Builder
		pos := 0
		for _, loc := range locs {
			v := s[loc[0]:loc[1]]
			if (d.check != nil && !d.check(s, loc)) || isPlaceholderText(s, loc) {
				continue
			}
			b.WriteString(s[pos:loc[0]])
			b.WriteString(m.placeholder(d.name, v))
			pos = loc[1]
		}
		if pos == 0 {
			continue
		}
		b.WriteString(s[pos:])
		s, changed = b.String(), true
	}
	return s, changed
}

// isPlaceholderText reports whether the match at loc is inside a
// placeholder that an earlier detector wrote, for example the digits of
// "[EMAIL_12]".
func isPlaceholderText(s string, loc []int) bool {
	open := strings.LastIndexByte(s[:loc[0]], '[')
	if open < 0 || strings.IndexByte(s[open:loc[0]], ']') >= 0 {
		return false
	}
	end := strings.IndexByte(s[loc[1]:], ']')
	return end >= 0 && placeholderRE.MatchString(s[open:loc[1]+end+1])
}

var placeholderRE = regexp.MustCompile(`^\[[A-Z][A-Z0-9_]*_[0-9]+\]$`)

// Restore returns s with each complete placeholder of m replaced by its
// original value.
func (m *Mapping) Restore(s string) string {
	if m.Len() == 0 || !strings.Contains(s, "[") {
		return s
	}
	var b strings.Builder
	pos := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '[' {
			continue
		}
		end := strings.IndexByte(s[i:], ']')
		if end < 0 || end+1 > m.maxLen {
			continue
		}
		if v, ok := m.values[s[i:i+end+1]]; ok {
			b.WriteString(s[pos:i])
			b.WriteString(v)
			pos = i + end + 1
			i = pos - 1
		}
	}
	if pos == 0 {
		return s
	}
	b.WriteString(s[pos:])
	return b.String()
}

// split returns the part of s that is safe to restore now, and the end
// of s that can be the start of a placeholder that the next text completes.
func (m *Mapping) split(s string) (string, string) {
	i := strings.LastIndexByte(s, '[')
	if i < 0 || strings.IndexByte(s[i:], ']') >= 0 || len(s)-i >= m.maxLen {
		return s, ""
	}
	return s[:i], s[i:]
}

// UnmaskJSON returns body with the placeholders in its JSON strings
// replaced by the original values. If body is not valid JSON, UnmaskJSON
// returns it unchanged.
func (m *Mapping) UnmaskJSON(body []byte) []byte {
	if m.Len() == 0 || !bytes.Contains(body, []byte("[")) {
		return body
	}
	out, err := rewriteStrings(body, func(s string) (string, bool) {
		r := m.Restore(s)
		return r, r != s
	})
	if err != nil {
		return body
	}
	return out
}

// rewriteStrings calls f for the text of each JSON string in body, names
// included. If f returns true, the string is replaced with the quoted
// result. It returns body if no string changed.
func rewriteStrings(body []byte, f func(string) (string, bool)) ([]byte, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(body))
	var out []byte
	pos := 0
	for {
		switch dec.PeekKind() {
		case 0:
			if _, err := dec.ReadToken(); err != nil && !errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("read json: %w", err)
			}
			if out == nil {
				return body, nil
			}
			return append(out, body[pos:]...), nil
		case '"':
			v, err := dec.ReadValue()
			if err != nil {
				return nil, fmt.Errorf("read json: %w", err)
			}
			end := int(dec.InputOffset())
			start := end - len(v)
			text, err := jsontext.AppendUnquote(nil, v)
			if err != nil {
				return nil, fmt.Errorf("read json string: %w", err)
			}
			repl, changed := f(string(text))
			if !changed {
				continue
			}
			q, err := jsontext.AppendQuote(nil, repl)
			if err != nil {
				return nil, fmt.Errorf("quote json string: %w", err)
			}
			if out == nil {
				out = make([]byte, 0, len(body)+64)
			}
			out = append(append(out, body[pos:start]...), q...)
			pos = end
		default:
			if _, err := dec.ReadToken(); err != nil {
				return nil, fmt.Errorf("read json: %w", err)
			}
		}
	}
}

// notDotted reports whether the match at loc is not part of a longer
// dotted number, such as the version 1.2.3.4.5.
func notDotted(s string, loc []int) bool {
	dotDigit := func(i, j int) bool {
		return i >= 0 && j < len(s) && j >= 0 && s[i] == '.' && s[j] >= '0' && s[j] <= '9'
	}
	return !dotDigit(loc[0]-1, loc[0]-2) && !dotDigit(loc[1], loc[1]+1)
}

// luhn reports whether the digits of s pass the Luhn check of card
// numbers.
func luhn(s string) bool {
	sum, n := 0, 0
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		if c < '0' || c > '9' {
			continue
		}
		d := int(c - '0')
		if n%2 == 1 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		n++
	}
	return n >= 13 && n <= 19 && sum%10 == 0
}
