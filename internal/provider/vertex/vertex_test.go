package vertex

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/oauth2"

	"github.com/ykakade/tollgate/internal/provider"
)

func TestBaseURL(t *testing.T) {
	for region, want := range map[string]string{
		"global":   "https://aiplatform.googleapis.com",
		"us":       "https://aiplatform.us.rep.googleapis.com",
		"eu":       "https://aiplatform.eu.rep.googleapis.com",
		"us-east5": "https://us-east5-aiplatform.googleapis.com",
	} {
		if got := BaseURL(region); got != want {
			t.Errorf("BaseURL(%q) = %q, want %q", region, got, want)
		}
	}
}

func TestSend(t *testing.T) {
	type seen struct {
		path, auth, version, apiKey, beta, body string
	}
	got := make(chan seen, 1)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- seen{r.URL.EscapedPath(), r.Header.Get("Authorization"), r.Header.Get("Anthropic-Version"), r.Header.Get("X-Api-Key"), r.Header.Get("Anthropic-Beta"), string(b)}
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()
	a, err := New(up.URL+"/", "my-project", "us-east5", oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.test"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		stream bool
		model  string
		path   string
	}{
		{false, "claude-sonnet-5-5", "/v1/projects/my-project/locations/us-east5/publishers/anthropic/models/claude-sonnet-5-5:rawPredict"},
		{true, "claude-haiku-4-5@20251001", "/v1/projects/my-project/locations/us-east5/publishers/anthropic/models/claude-haiku-4-5@20251001:streamRawPredict"},
		{false, "a/b", "/v1/projects/my-project/locations/us-east5/publishers/anthropic/models/a%2Fb:rawPredict"},
	} {
		h := http.Header{"X-Api-Key": {"tg_client"}, "Anthropic-Version": {"2023-06-01"}, "Anthropic-Beta": {"x"}}
		resp, err := a.Send(t.Context(), &provider.Request{Path: MessagesPath, RawQuery: "beta=true", Header: h, Body: []byte(`{"anthropic_version":"vertex-2023-10-16"}`), Model: tt.model, Stream: tt.stream})
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		s := <-got
		want := seen{tt.path, "Bearer ya29.test", "", "", "x", `{"anthropic_version":"vertex-2023-10-16"}`}
		if s != want {
			t.Errorf("upstream saw %+v\nwant %+v", s, want)
		}
	}
}

func TestSendRefusesOtherPaths(t *testing.T) {
	a, err := New("http://127.0.0.1:1", "p", "global", oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "t"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a.Send(t.Context(), &provider.Request{Path: "/v1/messages/count_tokens", Model: "m"})
	if resp != nil {
		resp.Body.Close()
	}
	if !errors.Is(err, ErrUnsupportedPath) {
		t.Errorf("err = %v, want ErrUnsupportedPath", err)
	}
}

type failingTokens struct{}

func (failingTokens) Token() (*oauth2.Token, error) { return nil, errors.New("no credentials") }

func TestSendTokenError(t *testing.T) {
	a, err := New("http://127.0.0.1:1", "p", "global", failingTokens{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a.Send(t.Context(), &provider.Request{Path: MessagesPath, Model: "m"})
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil {
		t.Error("Send succeeded without a token")
	}
}
