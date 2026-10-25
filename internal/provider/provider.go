// Package provider sends requests to upstream providers and reads the token
// usage of their responses.
//
// An Adapter sends one request to one provider. The anthropic and openai
// packages make adapters and usage meters for their wire formats.
package provider

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

// Usage is the token usage of one response.
type Usage struct {
	// InputTokens are the input tokens that the provider did not read from
	// or write to its prompt cache.
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	// CacheWrite1hTokens are the part of CacheWriteTokens that the
	// provider wrote to its 1-hour prompt cache. Only Anthropic reports
	// them.
	CacheWrite1hTokens int64
	// ReasoningTokens are the part of OutputTokens that the model used to
	// reason. Only some providers report them.
	ReasoningTokens int64
}

// ErrMalformed is the error of a Meter when the response is not valid in
// the wire format of the provider.
var ErrMalformed = errors.New("malformed response")

// Meter reads a copy of a response body and finds its usage. The relay
// calls Write with each chunk after it sends the chunk to the client.
type Meter interface {
	// Write reads the next chunk. It returns an error that wraps
	// ErrMalformed if the response is not valid, for example a stream
	// event that is not JSON. The relay then stops the response. A meter
	// that cannot find the usage for another reason returns no error.
	Write(p []byte) (int, error)
	// Usage returns the usage in the response. The bool is false if the
	// response had no usage that the meter could read.
	Usage() (Usage, bool)
}

// Request is one request to a provider.
type Request struct {
	// Path is the request path, for example "/v1/messages".
	Path string
	// RawQuery is the query of the client request, without "?".
	RawQuery string
	// Header holds the client headers. The adapter removes the headers that
	// must not go upstream and adds the provider key.
	Header http.Header
	Body   []byte
	// Model is the model of the target. Body already has it, if the
	// format of the provider puts the model in the body.
	Model string
	// Stream is true if the client asked for a stream.
	Stream bool
}

// Adapter sends requests to one provider.
type Adapter interface {
	// Send sends r with ctx and returns the response headers. The caller
	// reads and closes the body. Send does not follow redirects.
	Send(ctx context.Context, r *Request) (*http.Response, error)
}

// HTTPAdapter is an Adapter for a provider with an HTTP API. The provider
// packages make it with their own authentication.
type HTTPAdapter struct {
	base      *url.URL
	transport http.RoundTripper
	auth      func(http.Header)
}

// NewHTTPAdapter returns an adapter that sends requests to baseURL. The
// auth function adds the provider credentials to the headers of each
// request. If rt is nil, the adapter uses NewTransport.
func NewHTTPAdapter(baseURL string, auth func(http.Header), rt http.RoundTripper) (*HTTPAdapter, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", err)
	}
	if rt == nil {
		rt = NewTransport()
	}
	return &HTTPAdapter{base: u, transport: rt, auth: auth}, nil
}

// Send implements Adapter. It calls the transport directly, with no
// http.Client, so it never follows a redirect and never sends cookies.
func (a *HTTPAdapter) Send(ctx context.Context, r *Request) (*http.Response, error) {
	u := *a.base
	u.Path = strings.TrimSuffix(u.Path, "/") + r.Path
	u.RawPath = ""
	u.RawQuery = r.RawQuery
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(r.Body))
	if err != nil {
		return nil, fmt.Errorf("make upstream request: %w", err)
	}
	req.Header = ForwardHeader(r.Header)
	a.auth(req.Header)
	resp, err := a.transport.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("send upstream request: %w", err)
	}
	return resp, nil
}

// NewTransport returns the transport for one provider. It keeps a pool of
// connections and uses HTTP/2 when the provider supports it. It has no dial
// or header timeouts: the relay applies the timeouts of each route (ADR
// 0016). Compression is off, so the relay can read the bytes that it sends.
func NewTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           100,
		MaxIdleConnsPerHost:    100,
		IdleConnTimeout:        90 * time.Second,
		ExpectContinueTimeout:  1 * time.Second,
		DisableCompression:     true,
		MaxResponseHeaderBytes: 1 << 20,
	}
}

// hopHeaders are the hop-by-hop headers of RFC 9110. A proxy must not
// forward them.
var hopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Proxy-Connection",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// requestDrop are the client headers that do not go to the provider: the
// client key, headers that the transport sets, and cookies.
var requestDrop = []string{
	"Authorization",
	"X-Api-Key",
	"Host",
	"Content-Length",
	"Accept-Encoding",
	"Expect",
	"Cookie",
}

// ForwardHeader returns a copy of the client headers h without the headers
// that must not go to a provider.
func ForwardHeader(h http.Header) http.Header {
	out := h.Clone()
	removeHop(out, h)
	for _, k := range requestDrop {
		out.Del(k)
	}
	for k := range out {
		if strings.HasPrefix(k, "X-Tollgate-") {
			delete(out, k)
		}
	}
	return out
}

// CopyResponseHeader adds the provider response headers in src to dst,
// without the hop-by-hop headers and cookies.
func CopyResponseHeader(dst, src http.Header) {
	skip := make(map[string]bool)
	for _, k := range hopHeaders {
		skip[k] = true
	}
	for _, v := range src.Values("Connection") {
		for f := range strings.SplitSeq(v, ",") {
			skip[textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(f))] = true
		}
	}
	skip["Set-Cookie"] = true
	for k, vv := range src {
		if !skip[k] {
			dst[k] = append(dst[k], vv...)
		}
	}
}

// removeHop removes from dst the hop-by-hop headers and the headers that
// the Connection header of src names.
func removeHop(dst, src http.Header) {
	for _, v := range src.Values("Connection") {
		for f := range strings.SplitSeq(v, ",") {
			if f = strings.TrimSpace(f); f != "" {
				dst.Del(f)
			}
		}
	}
	for _, k := range hopHeaders {
		dst.Del(k)
	}
}

// BodyMeter is a Meter for a JSON response body. It keeps the body up to a
// limit and reads the usage when Usage is called. If the body is larger
// than the limit, Usage reports no usage.
type BodyMeter struct {
	buf   []byte
	limit int
	over  bool
	parse func([]byte) (Usage, bool)
}

// NewBodyMeter returns a BodyMeter that keeps up to limit bytes and reads
// the usage with parse.
func NewBodyMeter(limit int, parse func([]byte) (Usage, bool)) *BodyMeter {
	return &BodyMeter{limit: limit, parse: parse}
}

// Write implements Meter.
func (m *BodyMeter) Write(p []byte) (int, error) {
	if m.over {
		return len(p), nil
	}
	if len(m.buf)+len(p) > m.limit {
		m.over = true
		m.buf = nil
		return len(p), nil
	}
	m.buf = append(m.buf, p...)
	return len(p), nil
}

// Model returns the top-level "model" field of the body, or "". Both the
// Messages API and the Chat Completions API have this field.
func (m *BodyMeter) Model() string {
	if m.over || len(m.buf) == 0 {
		return ""
	}
	var v struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(m.buf, &v) != nil {
		return ""
	}
	return v.Model
}

// Usage implements Meter.
func (m *BodyMeter) Usage() (Usage, bool) {
	if m.over || len(m.buf) == 0 {
		return Usage{}, false
	}
	return m.parse(m.buf)
}
