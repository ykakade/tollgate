package fakeprovider

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// openaiRequest holds the fields of a Chat Completions request that the fake
// provider reads.
type openaiRequest struct {
	Model               string               `json:"model"`
	Stream              bool                 `json:"stream"`
	StreamOptions       *openaiStreamOptions `json:"stream_options"`
	MaxTokens           int                  `json:"max_tokens"`
	MaxCompletionTokens int                  `json:"max_completion_tokens"`
	Messages            []jsontext.Value     `json:"messages"`
}

type openaiStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// limit returns the output token limit of the request, or 0 for no limit.
// max_completion_tokens replaces the older max_tokens field, so it wins if
// the request sets both.
func (r openaiRequest) limit() int {
	if r.MaxCompletionTokens > 0 {
		return r.MaxCompletionTokens
	}
	return r.MaxTokens
}

type openaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openaiMessage struct {
	Role    string  `json:"role"`
	Content string  `json:"content"`
	Refusal *string `json:"refusal"`
}

type openaiChoice struct {
	Index        int           `json:"index"`
	Message      openaiMessage `json:"message"`
	Logprobs     *struct{}     `json:"logprobs"`
	FinishReason string        `json:"finish_reason"`
}

type openaiCompletion struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []openaiChoice `json:"choices"`
	Usage   *openaiUsage   `json:"usage,omitzero"`
}

type openaiDelta struct {
	Role    string  `json:"role,omitzero"`
	Content *string `json:"content,omitzero"`
}

type openaiChunkChoice struct {
	Index        int         `json:"index"`
	Delta        openaiDelta `json:"delta"`
	Logprobs     *struct{}   `json:"logprobs"`
	FinishReason *string     `json:"finish_reason"`
}

// openaiChunk is one event of a Chat Completions stream. Usage is a raw value
// because the API sends "usage": null on each chunk before the last one when
// the client asks for usage, and omits the field when it does not.
type openaiChunk struct {
	ID      string              `json:"id"`
	Object  string              `json:"object"`
	Created int64               `json:"created"`
	Model   string              `json:"model"`
	Choices []openaiChunkChoice `json:"choices"`
	Usage   jsontext.Value      `json:"usage,omitzero"`
}

type openaiErrorDetail struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    *string `json:"code"`
}

type openaiError struct {
	Error openaiErrorDetail `json:"error"`
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	body, sc, ok := s.prelude(w, r, start, writeOpenAIError)
	if !ok {
		return
	}
	var req openaiRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.logRequest(r, start, http.StatusBadRequest, false, writeOpenAIError(w, http.StatusBadRequest, "The request body is not valid JSON."))
		return
	}
	if msg := validateOpenAI(req); msg != "" {
		s.logRequest(r, start, http.StatusBadRequest, false, writeOpenAIError(w, http.StatusBadRequest, msg))
		return
	}

	chunks, cut := s.reply(req.limit())
	finishReason := "stop"
	if cut {
		finishReason = "length"
	}
	prompt := inputTokens(body)
	usage := &openaiUsage{
		PromptTokens:     prompt,
		CompletionTokens: len(chunks),
		TotalTokens:      prompt + len(chunks),
	}
	if sc.kind == kindNoUsage {
		usage = nil
	}
	id := fmt.Sprintf("chatcmpl-fake-%06d", s.nextID())
	created := s.now().Unix()

	if req.Stream {
		includeUsage := req.StreamOptions != nil && req.StreamOptions.IncludeUsage && usage != nil
		err := s.streamOpenAI(r, s.startStream(w, sc), openaiChunk{
			ID:      id,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   req.Model,
		}, chunks, finishReason, usage, includeUsage)
		s.logRequest(r, start, http.StatusOK, true, err)
		return
	}
	resp := openaiCompletion{
		ID:      id,
		Object:  "chat.completion",
		Created: created,
		Model:   req.Model,
		Choices: []openaiChoice{{
			Message:      openaiMessage{Role: "assistant", Content: strings.Join(chunks, "")},
			FinishReason: finishReason,
		}},
		Usage: usage,
	}
	if err := sleep(r.Context(), s.bodyDelay(len(chunks))); err != nil {
		s.logRequest(r, start, 0, false, err)
		return
	}
	s.logRequest(r, start, http.StatusOK, false, writeBody(w, r, sc, resp))
}

// validateOpenAI returns a message for the client if req is not valid, or an
// empty string if it is valid.
func validateOpenAI(req openaiRequest) string {
	switch {
	case req.Model == "":
		return "The model field is missing. Add the model field and send the request again."
	case len(req.Messages) == 0:
		return "The messages field is missing or empty. Add one message or more."
	}
	return ""
}

// streamOpenAI sends the reply as a Chat Completions event stream. base holds
// the fields that are the same in each chunk. If includeUsage is true, the
// last chunk before [DONE] has no choices and holds the usage.
func (s *Server) streamOpenAI(r *http.Request, ew *eventWriter, base openaiChunk, chunks []string, finishReason string, usage *openaiUsage, includeUsage bool) error {
	if includeUsage {
		base.Usage = jsontext.Value("null")
	}
	chunk := func(delta openaiDelta, finish *string) sseEvent {
		c := base
		c.Choices = []openaiChunkChoice{{Delta: delta, FinishReason: finish}}
		return sseEvent{v: c}
	}
	empty := ""
	events := []sseEvent{chunk(openaiDelta{Role: "assistant", Content: &empty}, nil)}
	for _, text := range chunks {
		ev := chunk(openaiDelta{Content: &text}, nil)
		ev.token = true
		events = append(events, ev)
	}
	events = append(events, chunk(openaiDelta{}, &finishReason))
	if includeUsage {
		usageJSON, err := json.Marshal(usage)
		if err != nil {
			return fmt.Errorf("encode usage: %w", err)
		}
		last := base
		last.Choices = []openaiChunkChoice{}
		last.Usage = usageJSON
		events = append(events, sseEvent{v: last})
	}
	if err := ew.writeAll(r.Context(), events); err != nil {
		return err
	}
	return ew.write("", []byte("[DONE]"))
}

// writeOpenAIError writes an error in the Chat Completions error format.
func writeOpenAIError(w http.ResponseWriter, status int, msg string) error {
	errType := "invalid_request_error"
	switch {
	case status == http.StatusTooManyRequests:
		errType = "rate_limit_exceeded"
	case status >= 500:
		errType = "server_error"
	}
	return writeJSON(w, status, openaiError{
		Error: openaiErrorDetail{Message: msg, Type: errType},
	})
}
