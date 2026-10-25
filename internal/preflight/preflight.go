// Package preflight reads the few request fields that Tollgate needs, and
// makes small edits to the request body.
//
// The original body goes to the provider (ADR 0002). Decode does not decode
// the full body. It reads the tokens of the top-level object and skips the
// values that it does not need, such as the messages. Rewrite changes only
// the bytes of the fields that it edits (ADR 0017).
package preflight

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
)

// Errors of Decode.
var (
	ErrNotJSON   = errors.New("body is not valid json")
	ErrNotObject = errors.New("body is not a json object")
	ErrNoModel   = errors.New("model field is missing or not a string")
	ErrStream    = errors.New("stream field is not a boolean")
)

// span is a range of bytes in the body, from start to end.
type span struct {
	start, end int
}

// Request holds the fields of a request body that Tollgate reads.
type Request struct {
	// Model is the model name that the client sent.
	Model string
	// Stream is true if the client asked for a stream.
	Stream bool
	// MaxTokens is the output token limit of the request: max_tokens, or
	// max_completion_tokens of the OpenAI API, which has priority. It is 0
	// if the body has no limit, or a limit that is not a positive integer.
	MaxTokens int64

	body        []byte
	objectStart int // the offset after the "{" of the top-level object
	model       span
	// modelMember is the bytes to remove to remove the model member: its
	// name, its value and one comma.
	modelMember   span
	streamOptions span // zero if the body has no stream_options
	members       int
	// anthropicVersion is true if the body has an anthropic_version member.
	anthropicVersion bool
	// system and messages are the values of the system and messages
	// members, or zero.
	system, messages span
}

// Decode reads the fields of body. It returns an error that wraps one of
// the Err values if the body is not valid or has no model.
func Decode(body []byte) (*Request, error) {
	r := &Request{body: body}
	dec := jsontext.NewDecoder(bytes.NewReader(body))
	tok, err := dec.ReadToken()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotJSON, err)
	}
	if tok.Kind() != '{' {
		return nil, ErrNotObject
	}
	r.objectStart = int(dec.InputOffset())
	hasModel := false
	var maxTokens, maxCompletion int64
	for dec.PeekKind() != '}' {
		// The offset is after the previous value and before its comma,
		// or after the "{" for the first member.
		memberStart := int(dec.InputOffset())
		tok, err := dec.ReadToken()
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNotJSON, err)
		}
		r.members++
		// A token is valid only until the next read, so copy the name.
		name := tok.String()
		if name == "anthropic_version" {
			r.anthropicVersion = true
		}
		switch name {
		case "max_tokens", "max_completion_tokens":
			v, err := dec.ReadValue()
			if err != nil {
				return nil, fmt.Errorf("%w: %w", ErrNotJSON, err)
			}
			n := positiveInt(v)
			if name == "max_tokens" {
				maxTokens = n
			} else {
				maxCompletion = n
			}
		case "model", "stream", "stream_options", "system", "messages":
			v, err := dec.ReadValue()
			if err != nil {
				return nil, fmt.Errorf("%w: %w", ErrNotJSON, err)
			}
			end := int(dec.InputOffset())
			sp := span{end - len(v), end}
			switch name {
			case "model":
				if v.Kind() != '"' {
					return nil, ErrNoModel
				}
				m, err := jsontext.AppendUnquote(nil, v)
				if err != nil {
					return nil, fmt.Errorf("%w: %w", ErrNotJSON, err)
				}
				r.Model, r.model, hasModel = string(m), sp, true
				r.modelMember = span{memberStart, end}
				if memberStart == r.objectStart {
					// The first member has no comma before it, so remove
					// the comma after it.
					r.modelMember.end = commaAfter(body, end)
				}
			case "stream":
				switch v.Kind() {
				case 't':
					r.Stream = true
				case 'f', 'n':
					r.Stream = false
				default:
					return nil, ErrStream
				}
			case "stream_options":
				r.streamOptions = sp
			case "system":
				r.system = sp
			case "messages":
				r.messages = sp
			}
		default:
			if err := dec.SkipValue(); err != nil {
				return nil, fmt.Errorf("%w: %w", ErrNotJSON, err)
			}
		}
	}
	if _, err := dec.ReadToken(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotJSON, err)
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: data after the object", ErrNotJSON)
	}
	if !hasModel || r.Model == "" {
		return nil, ErrNoModel
	}
	r.MaxTokens = maxTokens
	if maxCompletion > 0 {
		r.MaxTokens = maxCompletion
	}
	return r, nil
}

// PrefixHash returns a hash of the start of the prompt: the system member
// and the messages up to the first message with the role "user". Requests
// of one agent session share this prefix, and the provider prompt cache
// depends on it too (ADR 0027). PrefixHash returns "" if the body has no
// message with the role "user".
//
// The result is 32 lowercase hex digits.
func (r *Request) PrefixHash() string {
	if r.messages == (span{}) {
		return ""
	}
	h := sha256.New()
	h.Write([]byte("system\x00"))
	h.Write(r.body[r.system.start:r.system.end])
	h.Write([]byte("\x00messages\x00"))
	dec := jsontext.NewDecoder(bytes.NewReader(r.body[r.messages.start:r.messages.end]))
	if tok, err := dec.ReadToken(); err != nil || tok.Kind() != '[' {
		return ""
	}
	for dec.PeekKind() == '{' {
		v, err := dec.ReadValue()
		if err != nil {
			return ""
		}
		h.Write(v)
		h.Write([]byte{0})
		var m struct {
			Role string `json:"role"`
		}
		if json.Unmarshal(v, &m) == nil && m.Role == "user" {
			return hex.EncodeToString(h.Sum(nil)[:16])
		}
	}
	return ""
}

// commaAfter returns the offset after the comma that follows offset i,
// with only white space before it. If there is no such comma, it returns i.
func commaAfter(body []byte, i int) int {
	for j := i; j < len(body); j++ {
		switch body[j] {
		case ' ', '\t', '\n', '\r':
		case ',':
			return j + 1
		default:
			return i
		}
	}
	return i
}

// positiveInt returns the value of a JSON number that is a positive
// integer, or 0.
func positiveInt(v jsontext.Value) int64 {
	if v.Kind() != '0' {
		return 0
	}
	n, err := strconv.ParseInt(string(v), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// edit replaces the bytes of a span with text. An insert has start == end.
type edit struct {
	span
	text string
}

// Rewrite returns the body with the model changed to model, if it is
// different. If includeUsage is true, it also makes sure that
// stream_options.include_usage is true (ADR 0009). It never changes the body
// that Decode got: if there is no edit, it returns that body; else it
// returns a new slice.
func (r *Request) Rewrite(model string, includeUsage bool) ([]byte, error) {
	var edits []edit
	if model != r.Model {
		q, err := jsontext.AppendQuote(nil, model)
		if err != nil {
			return nil, fmt.Errorf("quote model: %w", err)
		}
		edits = append(edits, edit{r.model, string(q)})
	}
	if includeUsage {
		e, ok, err := r.includeUsageEdit()
		if err != nil {
			return nil, err
		}
		if ok {
			edits = append(edits, e)
		}
	}
	if len(edits) == 0 {
		return r.body, nil
	}
	return r.apply(edits), nil
}

// apply returns a copy of the body with the edits. Edits must not overlap.
// At the same offset, an insert comes before a replacement.
func (r *Request) apply(edits []edit) []byte {
	slices.SortStableFunc(edits, func(a, b edit) int {
		if a.start != b.start {
			return a.start - b.start
		}
		return (a.end - a.start) - (b.end - b.start)
	})
	out := make([]byte, 0, len(r.body)+64)
	pos := 0
	for _, e := range edits {
		out = append(out, r.body[pos:e.start]...)
		out = append(out, e.text...)
		pos = e.end
	}
	return append(out, r.body[pos:]...)
}

// VertexBody returns the body for Claude on Vertex AI (ADR 0024): without
// the model member, because the URL names the model, and with an
// anthropic_version member with the value version, if the body has no
// anthropic_version member. Other bytes do not change.
func (r *Request) VertexBody(version string) ([]byte, error) {
	var edits []edit
	if !r.anthropicVersion {
		q, err := jsontext.AppendQuote(nil, version)
		if err != nil {
			return nil, fmt.Errorf("quote anthropic_version: %w", err)
		}
		text := `"anthropic_version":` + string(q)
		if r.members > 1 {
			text += ","
		}
		edits = append(edits, edit{span{r.objectStart, r.objectStart}, text})
	}
	edits = append(edits, edit{r.modelMember, ""})
	return r.apply(edits), nil
}

const includeUsageMember = `"include_usage":true`

// includeUsageEdit returns the edit that sets include_usage to true. The
// bool is false if no edit is necessary, or if stream_options has a type
// that the provider will refuse anyway.
func (r *Request) includeUsageEdit() (edit, bool, error) {
	so := r.streamOptions
	if so == (span{}) {
		return edit{span{r.objectStart, r.objectStart}, `"stream_options":{` + includeUsageMember + `},`}, true, nil
	}
	v := jsontext.Value(r.body[so.start:so.end])
	switch v.Kind() {
	case 'n':
		return edit{so, "{" + includeUsageMember + "}"}, true, nil
	case '{':
	default:
		return edit{}, false, nil
	}
	dec := jsontext.NewDecoder(bytes.NewReader(v))
	if _, err := dec.ReadToken(); err != nil {
		return edit{}, false, fmt.Errorf("read stream_options: %w", err)
	}
	inner := so.start + int(dec.InputOffset())
	empty := dec.PeekKind() == '}'
	for dec.PeekKind() != '}' {
		tok, err := dec.ReadToken()
		if err != nil {
			return edit{}, false, fmt.Errorf("read stream_options: %w", err)
		}
		name := tok.String()
		val, err := dec.ReadValue()
		if err != nil {
			return edit{}, false, fmt.Errorf("read stream_options: %w", err)
		}
		if name != "include_usage" {
			continue
		}
		if val.Kind() == 't' {
			return edit{}, false, nil
		}
		end := so.start + int(dec.InputOffset())
		return edit{span{end - len(val), end}, "true"}, true, nil
	}
	text := includeUsageMember
	if !empty {
		text += ","
	}
	return edit{span{inner, inner}, text}, true, nil
}

// bytesPerToken is the ratio that EstimateInputTokens uses. ADR 0003 asks
// for one ratio for each provider. Both providers use this value until
// measurements give better ratios.
const bytesPerToken = 4

// EstimateInputTokens returns an estimate of the input tokens of a body
// with n bytes. Tollgate uses it when the provider reports no usage.
func EstimateInputTokens(n int) int64 {
	return int64((n + bytesPerToken - 1) / bytesPerToken)
}
