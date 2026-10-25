// Package fakeprovider implements a test server that speaks the wire formats
// of the Anthropic Messages API and the OpenAI Chat Completions API.
//
// The server always returns the same reply, in streaming and non-streaming
// form, with usage fields that match the reply. Tests and benchmarks use it in
// place of a real provider. A scenario makes the server fail on purpose; see
// ScenarioHeader. The server also takes Messages API requests in the form of
// Claude on Vertex AI.
package fakeprovider

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Reply is the text of every response.
const Reply = "Hello from the Tollgate fake provider."

// MaxBodyBytes is the largest request body the server accepts.
const MaxBodyBytes = 32 << 20

// bytesPerInputToken is the ratio the server uses to count input tokens.
// The fake provider has no tokenizer, so it counts one input token for each
// started group of 4 bytes in the request body.
const bytesPerInputToken = 4

// Config holds the settings of a Server.
type Config struct {
	// Logger receives one line per request. If nil, the server logs nothing.
	Logger *slog.Logger
	// Now returns the current time. If nil, the server uses time.Now.
	Now func() time.Time
	// Scenario is the scenario of all requests that do not send the
	// ScenarioHeader. Empty means a normal reply.
	Scenario string
}

// Server is a fake provider. Use New to make one.
type Server struct {
	logger *slog.Logger
	now    func() time.Time
	chunks []string
	seq    atomic.Uint64

	requests atomic.Uint64
	scenario atomic.Pointer[string]
}

// New returns a Server with the given configuration.
func New(cfg Config) *Server {
	s := &Server{
		logger: cfg.Logger,
		now:    cfg.Now,
		chunks: splitReply(Reply),
	}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	s.scenario.Store(&cfg.Scenario)
	return s
}

// Handler returns the HTTP handler of the server.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/messages", s.handleMessages)
	mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("POST /v1/projects/{project}/locations/{location}/publishers/anthropic/models/{action}", s.handleVertex)
	return mux
}

// splitReply splits text into chunks of one word each, with the trailing
// space kept on each word. The server sends one chunk per output token.
func splitReply(text string) []string {
	words := strings.SplitAfter(text, " ")
	chunks := words[:0]
	for _, w := range words {
		if w != "" {
			chunks = append(chunks, w)
		}
	}
	return chunks
}

// nextID returns a new request number. The first number is 1.
func (s *Server) nextID() uint64 {
	return s.seq.Add(1)
}

// reply returns the output chunks for a request with the given token limit.
// A limit of 0 or less means no limit. The bool result is true if the limit
// cut the reply.
func (s *Server) reply(limit int) ([]string, bool) {
	if limit > 0 && limit < len(s.chunks) {
		return s.chunks[:limit], true
	}
	return s.chunks, false
}

// readBody reads the request body up to MaxBodyBytes.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	return body, nil
}

// inputTokens returns the input token count for a request body.
func inputTokens(body []byte) int {
	return (len(body) + bytesPerInputToken - 1) / bytesPerInputToken
}

// bodyErrorStatus returns the HTTP status for an error from readBody.
func bodyErrorStatus(err error) int {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

// writeJSON writes v as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.MarshalWrite(w, v); err != nil {
		return fmt.Errorf("write json response: %w", err)
	}
	return nil
}

// eventWriter writes server-sent events and flushes each one to the client.
type eventWriter struct {
	w  io.Writer
	rc *http.ResponseController
	sc scenario
	// tokens is the number of token events written.
	tokens int
}

// startStream writes the headers of an event stream response.
func startStream(w http.ResponseWriter, sc scenario) *eventWriter {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	return &eventWriter{w: w, rc: http.NewResponseController(w), sc: sc}
}

// write sends one event. If name is empty, the event has no event field.
func (e *eventWriter) write(name string, data []byte) error {
	if name != "" {
		if _, err := fmt.Fprintf(e.w, "event: %s\n", name); err != nil {
			return fmt.Errorf("write event name: %w", err)
		}
	}
	if _, err := fmt.Fprintf(e.w, "data: %s\n\n", data); err != nil {
		return fmt.Errorf("write event data: %w", err)
	}
	if err := e.rc.Flush(); err != nil {
		return fmt.Errorf("flush event: %w", err)
	}
	return nil
}

// sseEvent is one server-sent event. The value v is sent as JSON in the data
// field. If name is empty, the event has no event field. A token event holds
// one output token.
type sseEvent struct {
	name  string
	v     any
	token bool
}

// writeAll sends events in order. It stops if ctx ends. It runs the
// scenarios that fail after a number of tokens.
func (e *eventWriter) writeAll(ctx context.Context, events []sseEvent) error {
	for _, ev := range events {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("stop stream: %w", err)
		}
		if ev.token {
			if e.tokens == e.sc.tokens {
				if err := e.fail(ctx, ev.name); err != nil {
					return err
				}
			}
			e.tokens++
		}
		if err := e.writeJSON(ev.name, ev.v); err != nil {
			return err
		}
	}
	return nil
}

// fail runs the scenario of the stream before the next token event.
func (e *eventWriter) fail(ctx context.Context, name string) error {
	switch e.sc.kind {
	case kindHangAfter:
		<-ctx.Done()
		return fmt.Errorf("hang: %w", ctx.Err())
	case kindResetAfter:
		return resetConnection(e.rc)
	case kindMalformedAfter:
		return e.write(name, []byte(`{"type":`))
	}
	return nil
}

// writeJSON sends one event with v encoded as JSON in the data field.
func (e *eventWriter) writeJSON(name string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode event %q: %w", name, err)
	}
	return e.write(name, data)
}

// logRequest writes one log line for a finished request.
func (s *Server) logRequest(r *http.Request, start time.Time, status int, stream bool, err error) {
	attrs := []any{
		"method", r.Method,
		"path", r.URL.Path,
		"status", status,
		"stream", stream,
		"duration_ms", time.Since(start).Milliseconds(),
	}
	if err != nil {
		s.logger.WarnContext(r.Context(), "request failed", append(attrs, "error", err)...)
		return
	}
	s.logger.InfoContext(r.Context(), "request done", attrs...)
}
