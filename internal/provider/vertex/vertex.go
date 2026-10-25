// Package vertex is the adapter for Claude on Google Cloud Vertex AI (ADR
// 0024).
//
// Vertex AI takes the Anthropic Messages API body with two changes: the URL
// names the model, so the body has no model, and the body has an
// anthropic_version member. Its streams use the Anthropic event format, so
// the anthropic meters read the responses.
package vertex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/ykakade/tollgate/internal/provider"
)

// Version is the anthropic_version that the adapter puts in the body.
const Version = "vertex-2023-10-16"

// Scope is the OAuth scope of the access tokens.
const Scope = "https://www.googleapis.com/auth/cloud-platform"

// MessagesPath is the only client path that the adapter supports.
const MessagesPath = "/v1/messages"

// ErrUnsupportedPath is the error of Send for a path other than
// MessagesPath.
var ErrUnsupportedPath = errors.New("vertex ai does not support this path")

// BaseURL returns the URL of the Vertex AI endpoint for a location:
// "global", a multi-region ("us" or "eu"), or a region.
func BaseURL(region string) string {
	switch region {
	case "global":
		return "https://aiplatform.googleapis.com"
	case "us", "eu":
		return "https://aiplatform." + region + ".rep.googleapis.com"
	default:
		return "https://" + region + "-aiplatform.googleapis.com"
	}
}

// DefaultTokenSource returns a token source with the Google Application
// Default Credentials.
func DefaultTokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	creds, err := google.FindDefaultCredentials(ctx, Scope)
	if err != nil {
		return nil, fmt.Errorf("find google credentials: %w", err)
	}
	return oauth2.ReuseTokenSource(nil, creds.TokenSource), nil
}

// Adapter sends Messages API requests to Vertex AI. The body of each
// request must already be in the Vertex AI form: see
// preflight.Request.VertexBody.
type Adapter struct {
	base      *url.URL
	project   string
	region    string
	tokens    oauth2.TokenSource
	transport http.RoundTripper
}

// New returns an adapter for a project and a location. If baseURL is
// empty, the adapter uses BaseURL(region). If rt is nil, the adapter uses
// provider.NewTransport.
func New(baseURL, project, region string, tokens oauth2.TokenSource, rt http.RoundTripper) (*Adapter, error) {
	if baseURL == "" {
		baseURL = BaseURL(region)
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", err)
	}
	if rt == nil {
		rt = provider.NewTransport()
	}
	return &Adapter{base: u, project: project, region: region, tokens: tokens, transport: rt}, nil
}

// Send implements provider.Adapter. It sends a request that streams to
// streamRawPredict, and other requests to rawPredict.
func (a *Adapter) Send(ctx context.Context, r *provider.Request) (*http.Response, error) {
	if r.Path != MessagesPath {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedPath, r.Path)
	}
	action := "rawPredict"
	if r.Stream {
		action = "streamRawPredict"
	}
	segs := []string{"v1", "projects", a.project, "locations", a.region, "publishers", "anthropic", "models", r.Model + ":" + action}
	esc := make([]string, len(segs))
	for i, s := range segs {
		esc[i] = url.PathEscape(s)
	}
	u := *a.base
	prefix := strings.TrimSuffix(u.Path, "/")
	u.Path = prefix + "/" + strings.Join(segs, "/")
	u.RawPath = prefix + "/" + strings.Join(esc, "/")
	u.RawQuery = ""

	tok, err := a.tokens.Token()
	if err != nil {
		return nil, fmt.Errorf("get google access token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(r.Body))
	if err != nil {
		return nil, fmt.Errorf("make upstream request: %w", err)
	}
	req.Header = provider.ForwardHeader(r.Header)
	// The version goes in the body.
	req.Header.Del("Anthropic-Version")
	tok.SetAuthHeader(req)
	resp, err := a.transport.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("send upstream request: %w", err)
	}
	return resp, nil
}
