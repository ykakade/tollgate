package fakeprovider

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// anthropicRequest holds the fields of a Messages API request that the fake
// provider reads.
type anthropicRequest struct {
	Model     string           `json:"model"`
	MaxTokens int              `json:"max_tokens"`
	Stream    bool             `json:"stream"`
	Messages  []jsontext.Value `json:"messages"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	OutputTokens             int `json:"output_tokens"`
}

type anthropicTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicMessage struct {
	ID           string               `json:"id"`
	Type         string               `json:"type"`
	Role         string               `json:"role"`
	Model        string               `json:"model"`
	Content      []anthropicTextBlock `json:"content"`
	StopReason   *string              `json:"stop_reason"`
	StopSequence *string              `json:"stop_sequence"`
	Usage        *anthropicUsage      `json:"usage,omitzero"`
}

type anthropicMessageStart struct {
	Type    string           `json:"type"`
	Message anthropicMessage `json:"message"`
}

type anthropicBlockStart struct {
	Type         string             `json:"type"`
	Index        int                `json:"index"`
	ContentBlock anthropicTextBlock `json:"content_block"`
}

type anthropicTextDelta struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicBlockDelta struct {
	Type  string             `json:"type"`
	Index int                `json:"index"`
	Delta anthropicTextDelta `json:"delta"`
}

type anthropicBlockStop struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

type anthropicStopDelta struct {
	StopReason   string  `json:"stop_reason"`
	StopSequence *string `json:"stop_sequence"`
}

type anthropicOutputUsage struct {
	OutputTokens int `json:"output_tokens"`
}

type anthropicMessageDelta struct {
	Type  string                `json:"type"`
	Delta anthropicStopDelta    `json:"delta"`
	Usage *anthropicOutputUsage `json:"usage,omitzero"`
}

type anthropicTypeOnly struct {
	Type string `json:"type"`
}

type anthropicErrorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type anthropicError struct {
	Type  string               `json:"type"`
	Error anthropicErrorDetail `json:"error"`
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	body, sc, ok := s.prelude(w, r, start, writeAnthropicError)
	if !ok {
		return
	}
	var req anthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.logRequest(r, start, http.StatusBadRequest, false, writeAnthropicError(w, http.StatusBadRequest, "The request body is not valid JSON."))
		return
	}
	s.messages(w, r, start, body, sc, req)
}

// vertexRequest holds the fields of a Vertex AI request body that the
// fake provider checks.
type vertexRequest struct {
	anthropicRequest
	AnthropicVersion string         `json:"anthropic_version"`
	BodyModel        jsontext.Value `json:"model"`
}

// handleVertex takes a Messages API request in the form of Claude on
// Vertex AI: the URL names the model, and the body has anthropic_version
// and no model.
func (s *Server) handleVertex(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	model, method, _ := strings.Cut(r.PathValue("action"), ":")
	if method != "rawPredict" && method != "streamRawPredict" {
		s.logRequest(r, start, http.StatusNotFound, false, writeAnthropicError(w, http.StatusNotFound, "The method is not rawPredict or streamRawPredict."))
		return
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		s.logRequest(r, start, http.StatusUnauthorized, false, writeAnthropicError(w, http.StatusUnauthorized, "The request has no access token."))
		return
	}
	body, sc, ok := s.prelude(w, r, start, writeAnthropicError)
	if !ok {
		return
	}
	var req vertexRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.logRequest(r, start, http.StatusBadRequest, false, writeAnthropicError(w, http.StatusBadRequest, "The request body is not valid JSON."))
		return
	}
	var msg string
	switch {
	case req.BodyModel != nil:
		msg = "The body has a model field. Vertex AI takes the model from the URL."
	case req.AnthropicVersion != "vertex-2023-10-16":
		msg = "The anthropic_version field is missing or not vertex-2023-10-16."
	case req.Stream != (method == "streamRawPredict"):
		msg = "The stream field does not agree with the method."
	}
	if msg != "" {
		s.logRequest(r, start, http.StatusBadRequest, false, writeAnthropicError(w, http.StatusBadRequest, msg))
		return
	}
	req.Model = model
	s.messages(w, r, start, body, sc, req.anthropicRequest)
}

// messages sends the reply to a Messages API request.
func (s *Server) messages(w http.ResponseWriter, r *http.Request, start time.Time, body []byte, sc scenario, req anthropicRequest) {
	if msg := validateAnthropic(req); msg != "" {
		s.logRequest(r, start, http.StatusBadRequest, false, writeAnthropicError(w, http.StatusBadRequest, msg))
		return
	}

	chunks, cut := s.reply(req.MaxTokens)
	stopReason := "end_turn"
	if cut {
		stopReason = "max_tokens"
	}
	id := fmt.Sprintf("msg_fake_%06d", s.nextID())
	input, read, write := s.promptUsage(body)
	usage := &anthropicUsage{InputTokens: input, CacheReadInputTokens: read, CacheCreationInputTokens: write, OutputTokens: len(chunks)}
	if sc.kind == kindNoUsage {
		usage = nil
	}

	if req.Stream {
		err := s.streamAnthropic(r, s.startStream(w, sc), id, req.Model, chunks, stopReason, usage)
		s.logRequest(r, start, http.StatusOK, true, err)
		return
	}
	msg := anthropicMessage{
		ID:         id,
		Type:       "message",
		Role:       "assistant",
		Model:      req.Model,
		Content:    []anthropicTextBlock{{Type: "text", Text: strings.Join(chunks, "")}},
		StopReason: &stopReason,
		Usage:      usage,
	}
	if err := sleep(r.Context(), s.bodyDelay(len(chunks))); err != nil {
		s.logRequest(r, start, 0, false, err)
		return
	}
	s.logRequest(r, start, http.StatusOK, false, writeBody(w, r, sc, msg))
}

// validateAnthropic returns a message for the client if req is not valid,
// or an empty string if it is valid.
func validateAnthropic(req anthropicRequest) string {
	switch {
	case req.Model == "":
		return "The model field is missing. Add the model field and send the request again."
	case req.MaxTokens <= 0:
		return "The max_tokens field is missing or not positive. Set max_tokens to 1 or more."
	case len(req.Messages) == 0:
		return "The messages field is missing or empty. Add one message or more."
	}
	return ""
}

// streamAnthropic sends the reply as a Messages API event stream. The order
// of events follows the Anthropic streaming documentation.
func (s *Server) streamAnthropic(r *http.Request, ew *eventWriter, id, model string, chunks []string, stopReason string, usage *anthropicUsage) error {
	var startUsage *anthropicUsage
	var deltaUsage *anthropicOutputUsage
	if usage != nil {
		u := *usage
		u.OutputTokens = 1
		startUsage = &u
		deltaUsage = &anthropicOutputUsage{OutputTokens: usage.OutputTokens}
	}
	events := []sseEvent{
		{name: "message_start", v: anthropicMessageStart{
			Type: "message_start",
			Message: anthropicMessage{
				ID:      id,
				Type:    "message",
				Role:    "assistant",
				Model:   model,
				Content: []anthropicTextBlock{},
				Usage:   startUsage,
			},
		}},
		{name: "content_block_start", v: anthropicBlockStart{
			Type:         "content_block_start",
			ContentBlock: anthropicTextBlock{Type: "text"},
		}},
		{name: "ping", v: anthropicTypeOnly{Type: "ping"}},
	}
	for _, c := range chunks {
		events = append(events, sseEvent{name: "content_block_delta", token: true, v: anthropicBlockDelta{
			Type:  "content_block_delta",
			Delta: anthropicTextDelta{Type: "text_delta", Text: c},
		}})
	}
	events = append(events,
		sseEvent{name: "content_block_stop", v: anthropicBlockStop{Type: "content_block_stop"}},
		sseEvent{name: "message_delta", v: anthropicMessageDelta{
			Type:  "message_delta",
			Delta: anthropicStopDelta{StopReason: stopReason},
			Usage: deltaUsage,
		}},
		sseEvent{name: "message_stop", v: anthropicTypeOnly{Type: "message_stop"}},
	)
	return ew.writeAll(r.Context(), events)
}

// anthropicErrorTypes maps a status to its Messages API error type. Other
// 4xx statuses are invalid_request_error, and other 5xx are api_error.
var anthropicErrorTypes = map[int]string{
	http.StatusUnauthorized:          "authentication_error",
	http.StatusNotFound:              "not_found_error",
	http.StatusRequestEntityTooLarge: "request_too_large",
	http.StatusTooManyRequests:       "rate_limit_error",
	529:                              "overloaded_error",
}

// writeAnthropicError writes an error in the Messages API error format.
func writeAnthropicError(w http.ResponseWriter, status int, msg string) error {
	errType, ok := anthropicErrorTypes[status]
	switch {
	case ok:
	case status >= 500:
		errType = "api_error"
	default:
		errType = "invalid_request_error"
	}
	return writeJSON(w, status, anthropicError{
		Type:  "error",
		Error: anthropicErrorDetail{Type: errType, Message: msg},
	})
}
