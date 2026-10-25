package redact

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"strconv"

	"github.com/ykakade/tollgate/internal/sse"
)

// Stream formats.
const (
	FormatAnthropic = "anthropic"
	FormatOpenAI    = "openai"
)

// maxStreamEventBytes is the largest event that a StreamUnmasker reads.
const maxStreamEventBytes = 4 << 20

// heldKey identifies one text field of a stream: a content block of an
// Anthropic stream, or a field of one choice of an OpenAI stream.
type heldKey struct {
	index int
	field string
}

// held is text that can be the start of a placeholder. The next delta of
// the same field completes it, or the end of the field flushes it.
type held struct {
	text string
	// deltaType is the Anthropic delta type of the field, for example
	// "text_delta".
	deltaType string
}

// StreamUnmasker restores the placeholders of a mapping in an event
// stream. It writes the events again to its writer, in the form
// "event: name\ndata: data\n\n". A placeholder that two text deltas split
// is restored in the second delta. Use Mapping.NewStreamUnmasker to make
// one.
//
// For OpenAI streams, the unmasker restores the content and
// reasoning_content text with this rule, and only complete placeholders in
// other strings, for example the arguments of a tool call.
type StreamUnmasker struct {
	m      *Mapping
	format string
	w      io.Writer
	parser *sse.Parser
	held   map[heldKey]*held
	buf    []byte
	err    error
}

// NewStreamUnmasker returns an unmasker that writes to w. The format is
// FormatAnthropic or FormatOpenAI.
func (m *Mapping) NewStreamUnmasker(w io.Writer, format string) *StreamUnmasker {
	return &StreamUnmasker{m: m, format: format, w: w, parser: sse.NewParser(maxStreamEventBytes), held: make(map[heldKey]*held)}
}

// Write reads the next bytes of the upstream stream and writes the
// complete events in them. It returns len(p) or the first error.
func (u *StreamUnmasker) Write(p []byte) (int, error) {
	if u.err != nil {
		return 0, u.err
	}
	u.buf = u.buf[:0]
	if err := u.parser.Feed(p, u.event); err != nil {
		u.err = fmt.Errorf("read stream: %w", err)
		return 0, u.err
	}
	if u.err != nil {
		return 0, u.err
	}
	if len(u.buf) > 0 {
		if _, err := u.w.Write(u.buf); err != nil {
			u.err = err
			return 0, err
		}
	}
	return len(p), nil
}

func (u *StreamUnmasker) emit(name string, data []byte) {
	if name != "" {
		u.buf = append(u.buf, "event: "...)
		u.buf = append(u.buf, name...)
		u.buf = append(u.buf, '\n')
	}
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		u.buf = append(u.buf, "data: "...)
		u.buf = append(u.buf, line...)
		u.buf = append(u.buf, '\n')
	}
	u.buf = append(u.buf, '\n')
}

func (u *StreamUnmasker) event(e sse.Event) {
	if u.format == FormatAnthropic {
		u.anthropic(e)
		return
	}
	u.openai(e)
}

// restore adds text to the held text of k, and returns the part that is
// safe to send now, with the placeholders restored.
func (u *StreamUnmasker) restore(k heldKey, deltaType, text string) string {
	h := u.held[k]
	if h == nil {
		h = &held{}
		u.held[k] = h
	}
	h.deltaType = deltaType
	ready, rest := u.m.split(h.text + text)
	h.text = rest
	return u.m.Restore(ready)
}

// flush returns the held text of k, restored, and removes it.
func (u *StreamUnmasker) flush(k heldKey) (string, string) {
	h := u.held[k]
	if h == nil || h.text == "" {
		return "", ""
	}
	delete(u.held, k)
	return u.m.Restore(h.text), h.deltaType
}

// Anthropic delta types and their text fields.
var anthropicFields = map[string]string{
	"text_delta":       "text",
	"thinking_delta":   "thinking",
	"input_json_delta": "partial_json",
}

func (u *StreamUnmasker) anthropic(e sse.Event) {
	switch e.Name {
	case "content_block_delta":
		var top map[string]jsontext.Value
		if json.Unmarshal(e.Data, &top) != nil {
			u.emit(e.Name, e.Data)
			return
		}
		var delta map[string]jsontext.Value
		var deltaType string
		if json.Unmarshal(top["delta"], &delta) != nil || json.Unmarshal(delta["type"], &deltaType) != nil {
			u.emit(e.Name, e.Data)
			return
		}
		field, ok := anthropicFields[deltaType]
		var text string
		if !ok || json.Unmarshal(delta[field], &text) != nil {
			u.emit(e.Name, u.m.UnmaskJSON(e.Data))
			return
		}
		k := heldKey{index: intValue(top["index"]), field: field}
		delta[field] = quote(u.restore(k, deltaType, text))
		top["delta"] = marshal(delta)
		u.emit(e.Name, marshal(top))
	case "content_block_stop":
		var top struct {
			Index int `json:"index"`
		}
		if json.Unmarshal(e.Data, &top) == nil {
			for _, field := range anthropicFields {
				k := heldKey{index: top.Index, field: field}
				if text, deltaType := u.flush(k); text != "" {
					data := marshal(map[string]any{
						"type":  "content_block_delta",
						"index": top.Index,
						"delta": map[string]any{"type": deltaType, field: text},
					})
					u.emit("content_block_delta", data)
				}
			}
		}
		u.emit(e.Name, e.Data)
	default:
		u.emit(e.Name, u.m.UnmaskJSON(e.Data))
	}
}

// OpenAI text fields of a choice delta that the unmasker holds.
var openaiFields = []string{"content", "reasoning_content"}

func (u *StreamUnmasker) openai(e sse.Event) {
	var top map[string]jsontext.Value
	if bytes.Equal(e.Data, []byte("[DONE]")) || json.Unmarshal(e.Data, &top) != nil {
		u.emit(e.Name, e.Data)
		return
	}
	var choices []map[string]jsontext.Value
	if json.Unmarshal(top["choices"], &choices) != nil || len(choices) == 0 {
		u.emit(e.Name, u.m.UnmaskJSON(e.Data))
		return
	}
	for _, c := range choices {
		index := intValue(c["index"])
		finished := len(c["finish_reason"]) > 0 && string(c["finish_reason"]) != "null"
		var delta map[string]jsontext.Value
		if json.Unmarshal(c["delta"], &delta) != nil || delta == nil {
			delta = make(map[string]jsontext.Value)
		}
		for _, field := range openaiFields {
			k := heldKey{index: index, field: field}
			var text string
			if v, ok := delta[field]; ok && json.Unmarshal(v, &text) == nil {
				delta[field] = quote(u.restore(k, "", text))
			}
			if finished {
				if rest, _ := u.flush(k); rest != "" {
					var prev string
					_ = json.Unmarshal(delta[field], &prev)
					delta[field] = quote(prev + rest)
				}
			}
		}
		c["delta"] = jsontext.Value(u.m.UnmaskJSON(marshal(delta)))
	}
	top["choices"] = marshal(choices)
	u.emit(e.Name, marshal(top))
}

func intValue(v jsontext.Value) int {
	n, _ := strconv.Atoi(string(v))
	return n
}

func quote(s string) jsontext.Value {
	q, _ := jsontext.AppendQuote(nil, s)
	return q
}

// marshal encodes v with sorted object names. The values of v are valid
// JSON, so it does not fail.
func marshal(v any) []byte {
	b, _ := json.Marshal(v, json.Deterministic(true))
	return b
}
