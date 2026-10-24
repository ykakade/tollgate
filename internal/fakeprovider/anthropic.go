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
	Usage        anthropicUsage       `json:"usage"`
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
	Type  string               `json:"type"`
	Delta anthropicStopDelta   `json:"delta"`
	Usage anthropicOutputUsage `json:"usage"`
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
	body, err := readBody(w, r)
	if err != nil {
		status := bodyErrorStatus(err)
		s.logRequest(r, start, status, false, writeAnthropicError(w, status, "The request body is too large or cannot be read."))
		return
	}
	var req anthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.logRequest(r, start, http.StatusBadRequest, false, writeAnthropicError(w, http.StatusBadRequest, "The request body is not valid JSON."))
		return
	}
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
	usage := anthropicUsage{InputTokens: inputTokens(body), OutputTokens: len(chunks)}

	if req.Stream {
		err := s.streamAnthropic(r, startStream(w), id, req.Model, chunks, stopReason, usage)
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
	s.logRequest(r, start, http.StatusOK, false, writeJSON(w, http.StatusOK, msg))
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
func (s *Server) streamAnthropic(r *http.Request, ew *eventWriter, id, model string, chunks []string, stopReason string, usage anthropicUsage) error {
	startUsage := usage
	startUsage.OutputTokens = 1
	events := []sseEvent{
		{"message_start", anthropicMessageStart{
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
		{"content_block_start", anthropicBlockStart{
			Type:         "content_block_start",
			ContentBlock: anthropicTextBlock{Type: "text"},
		}},
		{"ping", anthropicTypeOnly{Type: "ping"}},
	}
	for _, c := range chunks {
		events = append(events, sseEvent{"content_block_delta", anthropicBlockDelta{
			Type:  "content_block_delta",
			Delta: anthropicTextDelta{Type: "text_delta", Text: c},
		}})
	}
	events = append(events,
		sseEvent{"content_block_stop", anthropicBlockStop{Type: "content_block_stop"}},
		sseEvent{"message_delta", anthropicMessageDelta{
			Type:  "message_delta",
			Delta: anthropicStopDelta{StopReason: stopReason},
			Usage: anthropicOutputUsage{OutputTokens: usage.OutputTokens},
		}},
		sseEvent{"message_stop", anthropicTypeOnly{Type: "message_stop"}},
	)
	return ew.writeAll(r.Context(), events)
}

// writeAnthropicError writes an error in the Messages API error format.
func writeAnthropicError(w http.ResponseWriter, status int, msg string) error {
	errType := "invalid_request_error"
	if status == http.StatusRequestEntityTooLarge {
		errType = "request_too_large"
	}
	return writeJSON(w, status, anthropicError{
		Type:  "error",
		Error: anthropicErrorDetail{Type: errType, Message: msg},
	})
}
