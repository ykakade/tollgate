// Package anthropic is the adapter and usage meter for the Anthropic
// Messages API.
package anthropic

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"

	"github.com/ykakade/tollgate/internal/provider"
	"github.com/ykakade/tollgate/internal/sse"
)

// DefaultVersion is the anthropic-version header that the adapter adds if
// the client sends none.
const DefaultVersion = "2023-06-01"

// Limits of the meters. A response that is larger still goes to the client,
// but its usage is not read.
const (
	maxEventBytes = 4 << 20
	maxBodyBytes  = 8 << 20
)

// New returns an adapter for an Anthropic provider at baseURL. If apiKey is
// not empty, the adapter sends it in the x-api-key header.
func New(baseURL, apiKey string, rt http.RoundTripper) (*provider.HTTPAdapter, error) {
	return provider.NewHTTPAdapter(baseURL, func(h http.Header) {
		if apiKey != "" {
			h.Set("X-Api-Key", apiKey)
		}
		if h.Get("Anthropic-Version") == "" {
			h.Set("Anthropic-Version", DefaultVersion)
		}
	}, rt)
}

// usage holds the usage fields of the Messages API. A field is nil if the
// event did not have it.
type usage struct {
	InputTokens              *int64 `json:"input_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheCreation            *struct {
		Ephemeral1hInputTokens *int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

// merge copies the fields that next has into u. The message_delta event
// has cumulative counts, so a later value replaces an earlier value.
func (u *usage) merge(next *usage) {
	for _, f := range []struct{ dst, src **int64 }{
		{&u.InputTokens, &next.InputTokens},
		{&u.CacheCreationInputTokens, &next.CacheCreationInputTokens},
		{&u.CacheReadInputTokens, &next.CacheReadInputTokens},
		{&u.OutputTokens, &next.OutputTokens},
	} {
		if *f.src != nil {
			*f.dst = *f.src
		}
	}
	if next.CacheCreation != nil && next.CacheCreation.Ephemeral1hInputTokens != nil {
		u.CacheCreation = next.CacheCreation
	}
}

func (u *usage) value() provider.Usage {
	get := func(p *int64) int64 {
		if p == nil {
			return 0
		}
		return *p
	}
	var write1h int64
	if u.CacheCreation != nil {
		write1h = get(u.CacheCreation.Ephemeral1hInputTokens)
	}
	return provider.Usage{
		InputTokens:        get(u.InputTokens),
		OutputTokens:       get(u.OutputTokens),
		CacheReadTokens:    get(u.CacheReadInputTokens),
		CacheWriteTokens:   get(u.CacheCreationInputTokens),
		CacheWrite1hTokens: write1h,
	}
}

// StreamMeter reads the usage of a Messages API event stream. The
// message_start event has the input usage, and each message_delta event
// has the cumulative output usage.
type StreamMeter struct {
	parser *sse.Parser
	u      usage
	seen   bool
	model  string
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

func (m *StreamMeter) event(e sse.Event) {
	if m.err != nil {
		return
	}
	if !jsontext.Value(e.Data).IsValid() {
		m.err = fmt.Errorf("%w: the data of a %q event is not JSON", provider.ErrMalformed, e.Name)
		return
	}
	switch e.Name {
	case "message_start":
		var v struct {
			Message struct {
				Model string `json:"model"`
				Usage *usage `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(e.Data, &v) == nil {
			m.model = v.Message.Model
			if v.Message.Usage != nil {
				m.u.merge(v.Message.Usage)
				m.seen = true
			}
		}
	case "message_delta":
		var v struct {
			Usage *usage `json:"usage"`
		}
		if json.Unmarshal(e.Data, &v) == nil && v.Usage != nil {
			m.u.merge(v.Usage)
			m.seen = true
		}
	}
}

// Usage implements provider.Meter.
func (m *StreamMeter) Usage() (provider.Usage, bool) {
	return m.u.value(), m.seen
}

// Model returns the model that the message_start event names, or "".
func (m *StreamMeter) Model() string {
	return m.model
}

// AtBoundary reports whether the stream so far ends between two events.
func (m *StreamMeter) AtBoundary() bool {
	return m.parser.AtBoundary()
}

// NewBodyMeter returns a meter for a Messages API response that is not a
// stream.
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
