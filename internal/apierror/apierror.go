// Package apierror writes Tollgate errors in the format of the API that the
// client called, because client SDKs parse them.
package apierror

import (
	"encoding/json/v2"
	"net/http"
	"strconv"

	"github.com/ykakade/tollgate/internal/config"
)

// anthropicTypes maps a status to the error type of the Anthropic API.
var anthropicTypes = map[int]string{
	http.StatusBadRequest:            "invalid_request_error",
	http.StatusUnauthorized:          "authentication_error",
	http.StatusForbidden:             "permission_error",
	http.StatusNotFound:              "not_found_error",
	http.StatusMethodNotAllowed:      "invalid_request_error",
	http.StatusRequestEntityTooLarge: "request_too_large",
	http.StatusTooManyRequests:       "rate_limit_error",
	http.StatusPaymentRequired:       "budget_exceeded",
}

// openaiTypes maps a status to the error type and code of the OpenAI API.
var openaiTypes = map[int][2]string{
	http.StatusBadRequest:            {"invalid_request_error", "invalid_request"},
	http.StatusUnauthorized:          {"invalid_request_error", "invalid_api_key"},
	http.StatusForbidden:             {"invalid_request_error", "model_not_allowed"},
	http.StatusNotFound:              {"invalid_request_error", "not_found"},
	http.StatusMethodNotAllowed:      {"invalid_request_error", "method_not_allowed"},
	http.StatusRequestEntityTooLarge: {"invalid_request_error", "request_too_large"},
	http.StatusTooManyRequests:       {"rate_limit_error", "rate_limit_exceeded"},
	http.StatusPaymentRequired:       {"budget_exceeded", "budget_exceeded"},
}

type anthropicDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type anthropicBody struct {
	Type  string          `json:"type"`
	Error anthropicDetail `json:"error"`
}

type openaiDetail struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    string  `json:"code"`
}

type openaiBody struct {
	Error openaiDetail `json:"error"`
}

// Body returns the JSON error body for status and message in the format of
// api.
func Body(api config.API, status int, message string) []byte {
	var v any
	if api == config.APIAnthropic {
		t, ok := anthropicTypes[status]
		if !ok {
			t = "api_error"
		}
		v = anthropicBody{Type: "error", Error: anthropicDetail{Type: t, Message: message}}
	} else {
		tc, ok := openaiTypes[status]
		if !ok {
			tc = [2]string{"server_error", "gateway_error"}
		}
		v = openaiBody{Error: openaiDetail{Message: message, Type: tc[0], Code: tc[1]}}
	}
	b, err := json.Marshal(v)
	if err != nil {
		// The values are plain strings, so Marshal fails only if message
		// is not valid UTF-8. Send a fixed message in that case.
		return Body(api, status, "Tollgate cannot show the error message.")
	}
	return b
}

// Write writes an error response. If retryAfter is more than 0, it also
// sets the retry-after header, in seconds.
func Write(w http.ResponseWriter, api config.API, status int, message string, retryAfter int) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	if retryAfter > 0 {
		h.Set("Retry-After", strconv.Itoa(retryAfter))
	}
	w.WriteHeader(status)
	_, _ = w.Write(Body(api, status, message))
}

// StreamEvent returns an error event for a stream that started. Both SDKs
// end the stream with an error when they read it: the Anthropic SDK reads
// an "error" event, and the OpenAI SDK reads a data field with an "error"
// member.
func StreamEvent(api config.API, message string) []byte {
	body := Body(api, http.StatusBadGateway, message)
	if api == config.APIAnthropic {
		return append(append([]byte("event: error\ndata: "), body...), "\n\n"...)
	}
	return append(append([]byte("data: "), body...), "\n\n"...)
}
