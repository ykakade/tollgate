package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// TracerName is the instrumentation scope of the Tollgate spans.
const TracerName = "github.com/ykakade/tollgate"

// tracesPath is the OTLP/HTTP path of traces.
const tracesPath = "/v1/traces"

// Attributes of the OpenTelemetry GenAI semantic conventions. The stable
// semconv package does not have them yet.
const (
	GenAIOperationName     = attribute.Key("gen_ai.operation.name")
	GenAIProviderName      = attribute.Key("gen_ai.provider.name")
	GenAIRequestModel      = attribute.Key("gen_ai.request.model")
	GenAIRequestMaxTokens  = attribute.Key("gen_ai.request.max_tokens")
	GenAIResponseModel     = attribute.Key("gen_ai.response.model")
	GenAIUsageInputTokens  = attribute.Key("gen_ai.usage.input_tokens")
	GenAIUsageOutputTokens = attribute.Key("gen_ai.usage.output_tokens")
)

// Attributes of Tollgate.
const (
	AttrRequestID        = attribute.Key("tollgate.request_id")
	AttrTenantID         = attribute.Key("tollgate.tenant_id")
	AttrSessionID        = attribute.Key("tollgate.session_id")
	AttrAttempt          = attribute.Key("tollgate.attempt")
	AttrAttempts         = attribute.Key("tollgate.attempts")
	AttrCache            = attribute.Key("tollgate.cache")
	AttrCostMicroUSD     = attribute.Key("tollgate.cost_micro_usd")
	AttrUsageEstimated   = attribute.Key("tollgate.usage_estimated")
	AttrCacheReadTokens  = attribute.Key("tollgate.usage.cache_read_tokens")
	AttrCacheWriteTokens = attribute.Key("tollgate.usage.cache_write_tokens")
	AttrTarget           = attribute.Key("tollgate.target")
	AttrDeniedBy         = attribute.Key("tollgate.denied_by")
)

// OperationChat is the gen_ai.operation.name of Messages and Chat
// Completions requests.
const OperationChat = "chat"

// ProviderName returns the gen_ai.provider.name of a Tollgate provider
// kind.
func ProviderName(kind string) string {
	switch kind {
	case "vertex":
		return "gcp.vertex_ai"
	default:
		// anthropic, openai and ollama.
		return kind
	}
}

// TracesURL returns the OTLP/HTTP traces URL of an endpoint. If the path of
// the endpoint does not end with /v1/traces, TracesURL adds it, as the
// OTEL_EXPORTER_OTLP_ENDPOINT variable does.
func TracesURL(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse otlp endpoint: %w", err)
	}
	if !strings.HasSuffix(u.Path, tracesPath) {
		u.Path = strings.TrimSuffix(u.Path, "/") + tracesPath
	}
	return u.String(), nil
}

// NewTracerProvider returns a tracer provider that sends spans in batches
// to the OTLP/HTTP receiver at endpoint. The service name is "tollgate";
// the OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES variables can change
// the resource, and OTEL_EXPORTER_OTLP_TRACES_HEADERS or
// OTEL_EXPORTER_OTLP_HEADERS can add request headers. A span with a
// sampled parent is sampled; a new trace is always sampled. Call Shutdown
// on the result to send the last spans.
func NewTracerProvider(ctx context.Context, endpoint, version string, logger *slog.Logger) (*sdktrace.TracerProvider, error) {
	traces, err := TracesURL(endpoint)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{}
	for _, name := range []string{"OTEL_EXPORTER_OTLP_TRACES_HEADERS", "OTEL_EXPORTER_OTLP_HEADERS"} {
		if v, ok := os.LookupEnv(name); ok {
			if headers, err = ParseHeaders(v); err != nil {
				return nil, fmt.Errorf("read %s: %w", name, err)
			}
			break
		}
	}
	exp := NewExporter(traces, headers, logger)
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName("tollgate"), semconv.ServiceVersion(version)),
		resource.WithTelemetrySDK(),
		resource.WithFromEnv(),
	)
	if err != nil {
		return nil, fmt.Errorf("make trace resource: %w", err)
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
	), nil
}
