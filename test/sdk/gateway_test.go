package sdk_test

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	aoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/openai/openai-go/v3"
	ooption "github.com/openai/openai-go/v3/option"

	"github.com/ykakade/tollgate/internal/auth"
	"github.com/ykakade/tollgate/internal/config"
	"github.com/ykakade/tollgate/internal/server"
)

// newGateway starts Tollgate in front of upstream and returns its URL and a
// client key. The routes use the model names of anthropicParams and
// openaiParams.
func newGateway(t *testing.T, upstream string) (string, string) {
	t.Helper()
	url, key, _ := newGatewayWith(t, upstream, "")
	return url, key
}

// sdkPrices is the price table of the gateways with a budget.
const sdkPrices = `
- {provider: anthropic, model: claude-test, effective_from: 2026-01-01, micro_usd_per_mtok: {input: 1000000, output: 2000000}}
- {provider: openai, model: gpt-test, effective_from: 2026-01-01, micro_usd_per_mtok: {input: 1000000, output: 2000000}}
`

// newGatewayWith is newGateway with more fields for the tenant, for
// example limits and budgets. It also returns a counter of the requests
// that reach Tollgate.
func newGatewayWith(t *testing.T, upstream, tenant string) (string, string, *atomic.Int64) {
	t.Helper()
	key, err := auth.NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	yaml := fmt.Sprintf(`version: 1
providers:
  anthropic: {kind: anthropic, base_url: %[1]s, api_key: env:KEY}
  openai: {kind: openai, base_url: %[1]s, api_key: env:KEY}
routes:
  - {model: claude-test, api: anthropic, targets: [{provider: anthropic}], timeouts: {idle: 300ms}}
  - {model: gpt-test, api: openai, targets: [{provider: openai}], timeouts: {idle: 300ms}}
pricing: {file: prices.yaml}
tenants:
  - id: t
    keys: [{id: k, hash: %[2]s}]
    models: [claude-test, gpt-test]
%[3]s
`, upstream, auth.Hash(key), tenant)
	cfg, err := config.Parse("sdk.yaml", []byte(yaml), config.Env{
		LookupEnv: func(string) (string, bool) { return "provider-key", true },
		ReadFile:  func(string) ([]byte, error) { return []byte(sdkPrices), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	adapters, err := server.NewAdapters(cfg.Providers, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(server.Options{Config: cfg, Adapters: adapters, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int64
	h := srv.Handler()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts.URL, key, &hits
}

func gatewayAnthropic(t *testing.T, upstream string) anthropic.Client {
	t.Helper()
	url, key := newGateway(t, upstream)
	return anthropic.NewClient(aoption.WithBaseURL(url), aoption.WithAPIKey(key), aoption.WithMaxRetries(0))
}

func gatewayOpenAI(t *testing.T, upstream string) openai.Client {
	t.Helper()
	url, key := newGateway(t, upstream)
	return openai.NewClient(ooption.WithBaseURL(url+"/v1"), ooption.WithAPIKey(key), ooption.WithMaxRetries(0))
}

func TestGatewayAnthropic(t *testing.T) {
	client := gatewayAnthropic(t, newFakeProvider(t).URL)
	msg, err := client.Messages.New(t.Context(), anthropicParams())
	if err != nil {
		t.Fatalf("Messages.New: %v", err)
	}
	checkAnthropicMessage(t, msg)

	stream := client.Messages.NewStreaming(t.Context(), anthropicParams())
	var acc anthropic.Message
	for stream.Next() {
		if err := acc.Accumulate(stream.Current()); err != nil {
			t.Fatalf("Accumulate: %v", err)
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream: %v", err)
	}
	checkAnthropicMessage(t, &acc)
}

func TestGatewayOpenAI(t *testing.T) {
	client := gatewayOpenAI(t, newFakeProvider(t).URL)
	c, err := client.Chat.Completions.New(t.Context(), openaiParams())
	if err != nil {
		t.Fatalf("Chat.Completions.New: %v", err)
	}
	checkOpenAICompletion(t, c)

	// The client does not ask for usage. Tollgate adds include_usage, so
	// the stream has one more chunk with no choices. The SDK must accept it.
	stream := client.Chat.Completions.NewStreaming(t.Context(), openaiParams())
	var acc openai.ChatCompletionAccumulator
	for stream.Next() {
		if !acc.AddChunk(stream.Current()) {
			t.Fatalf("AddChunk refused chunk %s", stream.Current().RawJSON())
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream: %v", err)
	}
	checkOpenAICompletion(t, &acc.ChatCompletion)
}

func TestGatewayErrorsParse(t *testing.T) {
	url, _ := newGateway(t, newFakeProvider(t).URL)
	a := anthropic.NewClient(aoption.WithBaseURL(url), aoption.WithAPIKey("tg_wrong"), aoption.WithMaxRetries(0))
	_, err := a.Messages.New(t.Context(), anthropicParams())
	if apiErr, ok := errors.AsType[*anthropic.Error](err); !ok || apiErr.StatusCode != http.StatusUnauthorized || apiErr.Type() != "authentication_error" {
		t.Errorf("anthropic error = %v, want a 401 authentication_error", err)
	}
	o := openai.NewClient(ooption.WithBaseURL(url+"/v1"), ooption.WithAPIKey("tg_wrong"), ooption.WithMaxRetries(0))
	_, err = o.Chat.Completions.New(t.Context(), openaiParams())
	if apiErr, ok := errors.AsType[*openai.Error](err); !ok || apiErr.StatusCode != http.StatusUnauthorized || apiErr.Code != "invalid_api_key" {
		t.Errorf("openai error = %v, want a 401 with code invalid_api_key", err)
	}
}

func TestGatewayRateLimitErrorsParse(t *testing.T) {
	url, key, _ := newGatewayWith(t, newFakeProvider(t).URL, "    limits: {requests_per_minute: 1}")
	a := anthropic.NewClient(aoption.WithBaseURL(url), aoption.WithAPIKey(key), aoption.WithMaxRetries(0))
	if _, err := a.Messages.New(t.Context(), anthropicParams()); err != nil {
		t.Fatalf("first request: %v", err)
	}
	_, err := a.Messages.New(t.Context(), anthropicParams())
	if apiErr, ok := errors.AsType[*anthropic.Error](err); !ok || apiErr.StatusCode != http.StatusTooManyRequests || apiErr.Type() != "rate_limit_error" || apiErr.Response.Header.Get("Retry-After") == "" {
		t.Errorf("anthropic error = %v, want a 429 rate_limit_error with retry-after", err)
	}
	o := openai.NewClient(ooption.WithBaseURL(url+"/v1"), ooption.WithAPIKey(key), ooption.WithMaxRetries(0))
	_, err = o.Chat.Completions.New(t.Context(), openaiParams())
	if apiErr, ok := errors.AsType[*openai.Error](err); !ok || apiErr.StatusCode != http.StatusTooManyRequests || apiErr.Code != "rate_limit_exceeded" || apiErr.Type != "rate_limit_error" {
		t.Errorf("openai error = %v, want a 429 with code rate_limit_exceeded", err)
	}
}

// TestGatewayBudgetErrorNotRetried checks that the SDKs read the 402
// error and do not send the request again, so that an agent stops at once.
func TestGatewayBudgetErrorNotRetried(t *testing.T) {
	url, key, hits := newGatewayWith(t, newFakeProvider(t).URL, "    budgets: [{window: month, limit_usd: 0.000001}]")
	// The SDKs retry 2 times by default.
	a := anthropic.NewClient(aoption.WithBaseURL(url), aoption.WithAPIKey(key))
	_, err := a.Messages.New(t.Context(), anthropicParams())
	if apiErr, ok := errors.AsType[*anthropic.Error](err); !ok || apiErr.StatusCode != http.StatusPaymentRequired || apiErr.Type() != "budget_exceeded" {
		t.Errorf("anthropic error = %v, want a 402 budget_exceeded", err)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("the Anthropic SDK sent %d requests, want 1", n)
	}
	o := openai.NewClient(ooption.WithBaseURL(url+"/v1"), ooption.WithAPIKey(key))
	_, err = o.Chat.Completions.New(t.Context(), openaiParams())
	if apiErr, ok := errors.AsType[*openai.Error](err); !ok || apiErr.StatusCode != http.StatusPaymentRequired || apiErr.Code != "budget_exceeded" {
		t.Errorf("openai error = %v, want a 402 with code budget_exceeded", err)
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("the OpenAI SDK sent %d requests, want 1", n-1)
	}
}

// stall is an upstream that sends the first part of a stream and then
// sends nothing more, so the idle timeout of Tollgate ends the stream.
func stall(first string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, first)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	return srv
}

func TestGatewayStreamErrorEvent(t *testing.T) {
	up := stall("event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":5,"output_tokens":1}}}` + "\n\n")
	t.Cleanup(up.Close)
	aclient := gatewayAnthropic(t, up.URL)
	stream := aclient.Messages.NewStreaming(t.Context(), anthropicParams())
	for stream.Next() {
	}
	if err := stream.Err(); err == nil || !strings.Contains(err.Error(), "idle timeout") {
		t.Errorf("anthropic stream error = %v, want the idle timeout error event", err)
	}

	up2 := stall(`data: {"id":"c","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}` + "\n\n")
	t.Cleanup(up2.Close)
	oclient := gatewayOpenAI(t, up2.URL)
	ostream := oclient.Chat.Completions.NewStreaming(t.Context(), openaiParams())
	for ostream.Next() {
	}
	if err := ostream.Err(); err == nil || !strings.Contains(err.Error(), "idle timeout") {
		t.Errorf("openai stream error = %v, want the idle timeout error event", err)
	}
}
