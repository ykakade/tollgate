package apierror

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ykakade/tollgate/internal/config"
)

func TestBody(t *testing.T) {
	tests := []struct {
		api    config.API
		status int
		want   string
	}{
		{config.APIAnthropic, 401, `{"type":"error","error":{"type":"authentication_error","message":"m"}}`},
		{config.APIAnthropic, 502, `{"type":"error","error":{"type":"api_error","message":"m"}}`},
		{config.APIOpenAI, 403, `{"error":{"message":"m","type":"invalid_request_error","param":null,"code":"model_not_allowed"}}`},
		{config.APIOpenAI, 503, `{"error":{"message":"m","type":"server_error","param":null,"code":"gateway_error"}}`},
	}
	for _, tt := range tests {
		if got := string(Body(tt.api, tt.status, "m")); got != tt.want {
			t.Errorf("Body(%s, %d) = %s, want %s", tt.api, tt.status, got, tt.want)
		}
	}
}

func TestBodyInvalidUTF8(t *testing.T) {
	got := string(Body(config.APIAnthropic, 400, "bad \xff"))
	want := `{"type":"error","error":{"type":"invalid_request_error","message":"Tollgate cannot show the error message."}}`
	if got != want {
		t.Errorf("Body() = %s, want %s", got, want)
	}
}

func TestWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, config.APIOpenAI, http.StatusServiceUnavailable, "m", 5)
	if rec.Code != 503 || rec.Header().Get("Retry-After") != "5" || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("got status %d, headers %v", rec.Code, rec.Header())
	}
}

func TestStreamEvent(t *testing.T) {
	if got, want := string(StreamEvent(config.APIAnthropic, "m")), "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"m\"}}\n\n"; got != want {
		t.Errorf("anthropic event = %q, want %q", got, want)
	}
	if got, want := string(StreamEvent(config.APIOpenAI, "m")), "data: {\"error\":{\"message\":\"m\",\"type\":\"server_error\",\"param\":null,\"code\":\"gateway_error\"}}\n\n"; got != want {
		t.Errorf("openai event = %q, want %q", got, want)
	}
}
