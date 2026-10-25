# ADR 0026: Export traces as OTLP JSON with an in-house exporter

- Status: Accepted
- Date: 2026-10-25
- Supersedes: none

## Context

The brief allows `go.opentelemetry.io/otel` and an OTLP exporter for traces.
The official OTLP/HTTP exporter, `otlptracehttp` v1.47.0, imports gRPC and the gRPC gateway, also when it sends HTTP only.
We measured `tollgate` builds with `CGO_ENABLED=0 go build -trimpath` on linux/amd64:

| Build | Size |
| --- | --- |
| M3, before telemetry | 15.6 MB |
| M4 with `otlptracehttp` | 36.8 MB |
| M4 with the in-house exporter | 21.0 MB |

ADR 0001 chose Go for a small binary, and ADR 0012 keeps the dependency tree small.
OTLP/HTTP has a JSON encoding that each OTLP receiver must accept, for example Jaeger and the OpenTelemetry Collector.

## Decision

Tollgate uses the OpenTelemetry API and SDK for spans, and its own `SpanExporter` in `internal/telemetry`.
The exporter posts batches from the SDK batch processor to `<otlp_endpoint>/v1/traces` as JSON.
It reads extra request headers from `OTEL_EXPORTER_OTLP_TRACES_HEADERS` or `OTEL_EXPORTER_OTLP_HEADERS`.
A failed export drops its spans. The exporter logs one warning when exports start to fail, and one line when they work again.
New traces are always sampled. A span with a client `traceparent` follows the sampled flag of that parent.
Tollgate does not add trace headers to provider requests.

## Alternatives

1. Use `otlptracehttp`. The binary is 15.8 MB larger, and gRPC is in the supply chain for a protocol that Tollgate does not use.
2. Use OTLP/gRPC. This adds gRPC on purpose.
3. Encode OTLP protobuf with `go.opentelemetry.io/proto/otlp`. Its collector package also imports gRPC.

## Consequences

- Tollgate has about 380 lines of exporter code to maintain. A unit test compares the bytes with a known request.
- The M4 test sent spans to Jaeger v2.22.0, and Jaeger showed each request as one trace.
- The exporter has no retry, compression or TLS client certificates. Send spans to a local OpenTelemetry Collector if you need these.
