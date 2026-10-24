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
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"slices"
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

	body          []byte
	objectStart   int // the offset after the "{" of the top-level object
	model         span
	streamOptions span // zero if the body has no stream_options
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
	for dec.PeekKind() != '}' {
		tok, err := dec.ReadToken()
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNotJSON, err)
		}
		// A token is valid only until the next read, so copy the name.
		name := tok.String()
		switch name {
		case "model", "stream", "stream_options":
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
	return r, nil
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
	slices.SortFunc(edits, func(a, b edit) int { return a.start - b.start })
	out := make([]byte, 0, len(r.body)+64)
	pos := 0
	for _, e := range edits {
		out = append(out, r.body[pos:e.start]...)
		out = append(out, e.text...)
		pos = e.end
	}
	return append(out, r.body[pos:]...), nil
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
