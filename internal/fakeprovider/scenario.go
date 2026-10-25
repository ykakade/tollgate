package fakeprovider

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ScenarioHeader is the request header that chooses the scenario of one
// request. It has priority over the scenario of the server.
const ScenarioHeader = "X-Fake-Scenario"

// Scenarios make the server fail on purpose, so tests can repeat each
// failure. The values are:
//
//   - "" or "ok": a normal reply.
//   - "status:<code>": an error response with the status code, in the
//     format of the API. "status:<code>:<seconds>" also sets retry-after.
//   - "hang-before-headers": no response until the client goes away.
//   - "hang-after:<n>-tokens": send n output tokens, then stop until the
//     client goes away. Without a stream, send the headers, then stop.
//   - "reset-after:<n>-tokens": send n output tokens, then close the
//     connection. Without a stream, send part of the body, then close it.
//   - "malformed-after:<n>-tokens": send n output tokens, then an event
//     with data that is not JSON, then the rest of the stream.
//   - "no-usage": a normal reply with no usage fields.
type scenario struct {
	kind       string
	status     int
	retryAfter int
	tokens     int
}

// The kinds of scenarios.
const (
	kindOK             = ""
	kindStatus         = "status"
	kindHangBefore     = "hang-before-headers"
	kindHangAfter      = "hang-after"
	kindResetAfter     = "reset-after"
	kindMalformedAfter = "malformed-after"
	kindNoUsage        = "no-usage"
)

var errScenario = errors.New("scenario is not valid")

func parseScenario(text string) (scenario, error) {
	name, arg, _ := strings.Cut(strings.TrimSpace(text), ":")
	switch name {
	case "", "ok":
		return scenario{}, nil
	case kindHangBefore, kindNoUsage:
		if arg != "" {
			return scenario{}, fmt.Errorf("%w: %q takes no value", errScenario, name)
		}
		return scenario{kind: name}, nil
	case kindStatus:
		code, ra, hasRA := strings.Cut(arg, ":")
		sc := scenario{kind: kindStatus}
		var err error
		if sc.status, err = strconv.Atoi(code); err != nil || sc.status < 400 || sc.status > 599 {
			return scenario{}, fmt.Errorf("%w: status needs a code from 400 to 599", errScenario)
		}
		if hasRA {
			if sc.retryAfter, err = strconv.Atoi(ra); err != nil || sc.retryAfter < 0 {
				return scenario{}, fmt.Errorf("%w: retry-after must be a number of seconds", errScenario)
			}
		}
		return sc, nil
	case kindHangAfter, kindResetAfter, kindMalformedAfter:
		n, err := strconv.Atoi(strings.TrimSuffix(arg, "-tokens"))
		if err != nil || n < 0 || !strings.HasSuffix(arg, "-tokens") {
			return scenario{}, fmt.Errorf("%w: use %s:<n>-tokens", errScenario, name)
		}
		return scenario{kind: name, tokens: n}, nil
	}
	return scenario{}, fmt.Errorf("%w: %q is not known", errScenario, name)
}

// errorWriter writes an error response in the format of one API.
type errorWriter func(w http.ResponseWriter, status int, msg string) error

// prelude reads the body, counts the request, and runs the scenarios that
// end a request before its reply. It returns false if the request is done.
func (s *Server) prelude(w http.ResponseWriter, r *http.Request, start time.Time, writeErr errorWriter) ([]byte, scenario, bool) {
	s.requests.Add(1)
	body, err := readBody(w, r)
	if err != nil {
		status := bodyErrorStatus(err)
		s.logRequest(r, start, status, false, writeErr(w, status, "The request body is too large or cannot be read."))
		return nil, scenario{}, false
	}
	text := *s.scenario.Load()
	if h := r.Header.Get(ScenarioHeader); h != "" {
		text = h
	}
	sc, err := parseScenario(text)
	if err != nil {
		s.logRequest(r, start, http.StatusBadRequest, false, writeErr(w, http.StatusBadRequest, err.Error()))
		return nil, scenario{}, false
	}
	switch sc.kind {
	case kindStatus:
		if sc.retryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(sc.retryAfter))
		}
		s.logRequest(r, start, sc.status, false, writeErr(w, sc.status, fmt.Sprintf("The fake provider returns status %d on purpose.", sc.status)))
		return nil, sc, false
	case kindHangBefore:
		<-r.Context().Done()
		s.logRequest(r, start, 0, false, r.Context().Err())
		return nil, sc, false
	}
	return body, sc, true
}

// writeBody writes a response body that is not a stream, with the
// scenarios that change it.
func writeBody(w http.ResponseWriter, r *http.Request, sc scenario, v any) error {
	switch sc.kind {
	case kindHangAfter:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
		return fmt.Errorf("hang: %w", r.Context().Err())
	case kindResetAfter:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":`))
		_ = http.NewResponseController(w).Flush()
		return resetConnection(http.NewResponseController(w))
	}
	return writeJSON(w, http.StatusOK, v)
}

// Requests returns the number of requests that the server received.
func (s *Server) Requests() uint64 { return s.requests.Load() }

// SetScenario changes the scenario of the server for the next requests. It
// returns an error if the scenario is not valid.
func (s *Server) SetScenario(text string) error {
	if _, err := parseScenario(text); err != nil {
		return err
	}
	s.scenario.Store(&text)
	return nil
}

// resetConnection closes an HTTP/1 test connection without the final
// chunk, so the client sees an unexpected EOF instead of a complete body.
func resetConnection(rc *http.ResponseController) error {
	conn, _, err := rc.Hijack()
	if err != nil {
		return fmt.Errorf("hijack fake connection: %w", err)
	}
	_ = conn.Close()
	return errors.New("connection reset on purpose")
}
