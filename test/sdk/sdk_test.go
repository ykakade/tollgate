// Package sdk_test checks that the official Anthropic and OpenAI Go SDKs can
// parse what the fake provider sends. It is a separate module, so the SDKs
// do not become dependencies of Tollgate (see ADR 0015).
package sdk_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	aoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/openai/openai-go/v3"
	ooption "github.com/openai/openai-go/v3/option"

	"github.com/ykakade/tollgate/internal/fakeprovider"
)

// replyTokens is the number of output tokens in fakeprovider.Reply.
const replyTokens = 6

func newFakeProvider(t *testing.T) *httptest.Server {
	t.Helper()
	fp := fakeprovider.New(fakeprovider.Config{Logger: slog.New(slog.DiscardHandler)})
	srv := httptest.NewServer(fp.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func newAnthropic(t *testing.T) anthropic.Client {
	t.Helper()
	return anthropic.NewClient(
		aoption.WithBaseURL(newFakeProvider(t).URL),
		aoption.WithAPIKey("test-key"),
		aoption.WithMaxRetries(0),
	)
}

func newOpenAI(t *testing.T) openai.Client {
	t.Helper()
	return openai.NewClient(
		ooption.WithBaseURL(newFakeProvider(t).URL+"/v1"),
		ooption.WithAPIKey("test-key"),
		ooption.WithMaxRetries(0),
	)
}

func anthropicParams() anthropic.MessageNewParams {
	return anthropic.MessageNewParams{
		Model:     "claude-test",
		MaxTokens: 64,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("Say hello."))},
	}
}

func openaiParams() openai.ChatCompletionNewParams {
	return openai.ChatCompletionNewParams{
		Model:    "gpt-test",
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("Say hello.")},
	}
}

func checkAnthropicMessage(t *testing.T, msg *anthropic.Message) {
	t.Helper()
	if len(msg.Content) != 1 {
		t.Fatalf("got %d content blocks, want 1", len(msg.Content))
	}
	if got := msg.Content[0].Text; got != fakeprovider.Reply {
		t.Errorf("text = %q, want %q", got, fakeprovider.Reply)
	}
	if msg.Model != "claude-test" {
		t.Errorf("model = %q, want %q", msg.Model, "claude-test")
	}
	if msg.StopReason != anthropic.StopReasonEndTurn {
		t.Errorf("stop_reason = %q, want %q", msg.StopReason, anthropic.StopReasonEndTurn)
	}
	if msg.Usage.InputTokens <= 0 {
		t.Errorf("input_tokens = %d, want more than 0", msg.Usage.InputTokens)
	}
	if msg.Usage.OutputTokens != replyTokens {
		t.Errorf("output_tokens = %d, want %d", msg.Usage.OutputTokens, replyTokens)
	}
}

func TestAnthropicMessage(t *testing.T) {
	client := newAnthropic(t)
	msg, err := client.Messages.New(t.Context(), anthropicParams())
	if err != nil {
		t.Fatalf("Messages.New: %v", err)
	}
	checkAnthropicMessage(t, msg)
}

func TestAnthropicStream(t *testing.T) {
	client := newAnthropic(t)
	stream := client.Messages.NewStreaming(t.Context(), anthropicParams())
	var msg anthropic.Message
	for stream.Next() {
		if err := msg.Accumulate(stream.Current()); err != nil {
			t.Fatalf("Accumulate: %v", err)
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream: %v", err)
	}
	checkAnthropicMessage(t, &msg)
}

func TestAnthropicError(t *testing.T) {
	client := newAnthropic(t)
	params := anthropicParams()
	params.Messages = nil
	_, err := client.Messages.New(t.Context(), params)
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("Messages.New error = %v, want *anthropic.Error", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", apiErr.StatusCode, http.StatusBadRequest)
	}
}

func checkOpenAICompletion(t *testing.T, c *openai.ChatCompletion) {
	t.Helper()
	if len(c.Choices) != 1 {
		t.Fatalf("got %d choices, want 1", len(c.Choices))
	}
	if got := c.Choices[0].Message.Content; got != fakeprovider.Reply {
		t.Errorf("content = %q, want %q", got, fakeprovider.Reply)
	}
	if got := c.Choices[0].FinishReason; got != "stop" {
		t.Errorf("finish_reason = %q, want %q", got, "stop")
	}
	if c.Usage.PromptTokens <= 0 {
		t.Errorf("prompt_tokens = %d, want more than 0", c.Usage.PromptTokens)
	}
	if c.Usage.CompletionTokens != replyTokens {
		t.Errorf("completion_tokens = %d, want %d", c.Usage.CompletionTokens, replyTokens)
	}
	if c.Usage.TotalTokens != c.Usage.PromptTokens+c.Usage.CompletionTokens {
		t.Errorf("total_tokens = %d, want prompt_tokens + completion_tokens", c.Usage.TotalTokens)
	}
}

func TestOpenAICompletion(t *testing.T) {
	client := newOpenAI(t)
	c, err := client.Chat.Completions.New(t.Context(), openaiParams())
	if err != nil {
		t.Fatalf("Chat.Completions.New: %v", err)
	}
	checkOpenAICompletion(t, c)
}

func TestOpenAIStream(t *testing.T) {
	client := newOpenAI(t)
	params := openaiParams()
	params.StreamOptions = openai.ChatCompletionStreamOptionsParam{IncludeUsage: openai.Bool(true)}
	stream := client.Chat.Completions.NewStreaming(t.Context(), params)
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

func TestOpenAIError(t *testing.T) {
	client := newOpenAI(t)
	params := openaiParams()
	params.Messages = nil
	_, err := client.Chat.Completions.New(t.Context(), params)
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("Chat.Completions.New error = %v, want *openai.Error", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", apiErr.StatusCode, http.StatusBadRequest)
	}
}
