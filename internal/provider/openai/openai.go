// Package openai is the adapter and usage meter for the OpenAI Chat
// Completions API. Ollama also uses this format.
package openai

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"

	"github.com/ykakade/tollgate/internal/provider"
	"github.com/ykakade/tollgate/internal/sse"
)

// Limits of the meters. A response that is larger still goes to the client,
// but its usage is not read.
const (
	maxEventBytes = 4 << 20
	maxBodyBytes  = 8 << 20
)

// New returns an adapter for an OpenAI provider at baseURL. If apiKey is not
// empty, the adapter sends it as a bearer token.
func New(baseURL, apiKey string, rt http.RoundTripper) (*provider.HTTPAdapter, error) {
	return provider.NewHTTPAdapter(baseURL, func(h http.Header) {
		if apiKey != "" {
			h.Set("Authorization", "Bearer "+apiKey)
		}
	}, rt)
}

type usage struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// value returns the usage in Tollgate terms. OpenAI counts cached tokens as
// part of prompt_tokens, so InputTokens is prompt_tokens minus the cached
// tokens.
func (u *usage) value() provider.Usage {
	var cached, reasoning int64
	if u.PromptTokensDetails != nil {
		cached = u.PromptTokensDetails.CachedTokens
	}
	if u.CompletionTokensDetails != nil {
		reasoning = u.CompletionTokensDetails.ReasoningTokens
	}
	return provider.Usage{
		InputTokens:     max(u.PromptTokens-cached, 0),
		OutputTokens:    u.CompletionTokens,
		CacheReadTokens: cached,
		ReasoningTokens: reasoning,
	}
}

// StreamMeter reads the usage of a Chat Completions stream. With
// stream_options.include_usage, the last chunk before [DONE] has the usage.
type StreamMeter struct {
	parser *sse.Parser
	u      provider.Usage
	seen   bool
	err    error
}

// NewStreamMeter returns a new StreamMeter.
func NewStreamMeter() *StreamMeter {
	return &StreamMeter{parser: sse.NewParser(maxEventBytes)}
}

// Write implements provider.Meter. An event that is too large, or an
// event with data that is not JSON, is an error that wraps
// provider.ErrMalformed.
func (m *StreamMeter) Write(p []byte) (int, error) {
	if m.err == nil {
		if err := m.parser.Feed(p, m.event); err != nil && m.err == nil {
			m.err = fmt.Errorf("%w: %w", provider.ErrMalformed, err)
		}
	}
	if m.err != nil {
		return 0, m.err
	}
	return len(p), nil
}

var usageKey = []byte(`"usage"`)

var done = []byte("[DONE]")

func (m *StreamMeter) event(e sse.Event) {
	if m.err != nil {
		return
	}
	if !bytes.Equal(e.Data, done) && !jsontext.Value(e.Data).IsValid() {
		m.err = fmt.Errorf("%w: the data of an event is not JSON", provider.ErrMalformed)
		return
	}
	// Most chunks have no usage or "usage":null. Do not decode a chunk that
	// cannot have usage.
	if !bytes.Contains(e.Data, usageKey) {
		return
	}
	var v struct {
		Usage *usage `json:"usage"`
	}
	if json.Unmarshal(e.Data, &v) == nil && v.Usage != nil {
		m.u = v.Usage.value()
		m.seen = true
	}
}

// Usage implements provider.Meter.
func (m *StreamMeter) Usage() (provider.Usage, bool) {
	return m.u, m.seen
}

// AtBoundary reports whether the stream so far ends between two events.
func (m *StreamMeter) AtBoundary() bool {
	return m.parser.AtBoundary()
}

// NewBodyMeter returns a meter for a Chat Completions response that is not
// a stream.
func NewBodyMeter() *provider.BodyMeter {
	return provider.NewBodyMeter(maxBodyBytes, parseBody)
}

func parseBody(b []byte) (provider.Usage, bool) {
	var v struct {
		Usage *usage `json:"usage"`
	}
	if json.Unmarshal(b, &v) != nil || v.Usage == nil {
		return provider.Usage{}, false
	}
	return v.Usage.value(), true
}
