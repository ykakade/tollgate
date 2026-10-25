# Observability

Tollgate gives Prometheus metrics on its admin port and OpenTelemetry traces over OTLP/HTTP.
One request ID ties together the access log line, the trace, and the usage event of each request.

- Metrics tell you if Tollgate is healthy.
- Traces tell you why one request was slow.
- Usage events tell you what a request cost. See [operations](operations.md#usage-file).

## Metrics

Tollgate serves the metrics at `GET /metrics` on the [admin port](#admin-port), by default `127.0.0.1:9090`.
All Tollgate metrics have the prefix `tollgate_`. The Go runtime metrics (`go_*`) and process metrics (`process_*`) are also there.

| Metric | Type | Labels | Description |
| --- | --- | --- | --- |
| `tollgate_requests_total` | counter | `route`, `provider`, `status_class`, `cache` | Requests by the status that Tollgate sent: `2xx`, `4xx` or `5xx`. Status 499 (client disconnected) is `4xx`. `cache` is `bypass` in this version. |
| `tollgate_request_duration_seconds` | histogram | `route`, `provider` | Time from the start of a request to the end of its response. |
| `tollgate_time_to_first_token_seconds` | histogram | `route`, `provider` | Time from the start of a request to the first response body byte. It includes the time of failed attempts before a fallback. |
| `tollgate_inter_token_latency_seconds` | histogram | `route`, `provider` | One value for each stream: the time from the first to the last body byte, divided by the output tokens minus 1. |
| `tollgate_gateway_overhead_seconds` | histogram | `route` | Time in Tollgate before the first upstream request, without the time to read the request body. |
| `tollgate_tokens_total` | counter | `route`, `provider`, `kind` | Tokens by kind: `input`, `output`, `cache_read` or `cache_write`. |
| `tollgate_cost_micro_usd_total` | counter | `route`, `provider` | Cost in micro-USD, from the price table. |
| `tollgate_fallbacks_total` | counter | `route`, `from`, `to`, `reason` | Attempts that went to a different target after a failure. |
| `tollgate_retries_total` | counter | `provider` | All extra attempts, to the same target or to a different target, by the provider of the failed attempt. |
| `tollgate_retry_budget_exhausted_total` | counter | `provider` | Requests that got no extra attempt because the retry budget was empty. |
| `tollgate_breaker_state` | gauge | `provider`, `model`, `region` | The state of each circuit breaker: `0` closed, `1` half-open, `2` open. |
| `tollgate_limit_rejections_total` | counter | `kind` | Requests that a limit refused: `requests`, `tokens` or `budget`. |
| `tollgate_active_streams` | gauge | none | Event streams that Tollgate sends to clients now. |
| `tollgate_usage_events_dropped_total` | counter | `sink` | Usage events that Tollgate did not write, because the queue was full or the file failed. |
| `tollgate_store_errors_total` | counter | `store`, `op` | Failed calls to Redis, by operation: `admit`, `settle` or `ping`. |

The label values come from the configuration, so their number stays small:

- `route` is the `model` of a route. It is `none` for a request that has no route, for example a request with a key that is not valid.
- `provider` is the provider name of the last attempt, or `none` if no attempt started.
- `from` and `to` are targets as `provider/model`.
- `reason` is `rate_limited`, `overloaded`, `server_error`, `connect_timeout`, `first_token_timeout`, `total_timeout` or `connection_failed`.
- Tenants and sessions are not labels. Use the usage events for that detail.

The breaker state is the state at the time of the scrape. An open breaker whose open time ended shows `1` (half-open).
Each Tollgate copy has its own breakers, so add the `instance` label when you compare copies.

Use these PromQL queries as a start:

```promql
# Requests per second for each route
sum by (route) (rate(tollgate_requests_total[5m]))

# Time to first token, p95
histogram_quantile(0.95, sum by (le) (rate(tollgate_time_to_first_token_seconds_bucket[5m])))

# Spend in USD per hour
sum(rate(tollgate_cost_micro_usd_total[5m])) * 3600 / 1e6
```

## Traces

Tracing is off by default. To turn it on, set `telemetry.otlp_endpoint` to an OTLP/HTTP receiver, for example Jaeger or an OpenTelemetry Collector:

```yaml
telemetry:
  otlp_endpoint: http://localhost:4318
```

Tollgate writes the line `tracing started` at start. It sends spans in batches as OTLP JSON to `/v1/traces` ([ADR 0026](adr/0026-otlp-json-exporter.md)).
To add request headers, for example the key of a tracing service, set `OTEL_EXPORTER_OTLP_HEADERS`, for example `api-key=abc123`.
To change the service name or to add resource attributes, set `OTEL_SERVICE_NAME` or `OTEL_RESOURCE_ATTRIBUTES`.

Each request is one trace. If the client sends a `traceparent` header, the trace of Tollgate is part of the trace of the client.
Tollgate does not add trace headers to provider requests. A `traceparent` header from the client goes to the provider unchanged, as other client headers do.

| Span | Kind | Parent | Description |
| --- | --- | --- | --- |
| `POST /v1/messages`, `POST /v1/chat/completions` or `POST /v1/messages/count_tokens` | server | the client span, if any | The full request. |
| `auth` | internal | the server span | The key check. |
| `preflight` | internal | the server span | The body read, the model check, and the body for the first target. |
| `limit check` | internal | the server span | The rate limit and budget check, with the Redis call if the store is Redis. |
| `chat <model>` | client | the server span | One span for each upstream attempt. A fallback gives two or more of these spans. |

The server span has these attributes, if the request has them:

| Attribute | Value |
| --- | --- |
| `http.request.method`, `http.route`, `http.response.status_code` | The request and the status that Tollgate sent. |
| `gen_ai.operation.name` | `chat`. |
| `gen_ai.request.model` | The model that the client sent. |
| `gen_ai.response.model` | The model that the provider named in its response. |
| `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens` | The tokens of the response. |
| `tollgate.request_id` | The same ID as in the `x-request-id` header and the access log. |
| `tollgate.tenant_id`, `tollgate.session_id` | The tenant and the `x-tollgate-session` header. |
| `tollgate.attempts`, `tollgate.target` | The number of upstream attempts, and the target of the last attempt. |
| `tollgate.cache` | `bypass` in this version. |
| `tollgate.cost_micro_usd`, `tollgate.usage_estimated` | The cost, and `true` if the provider sent no usage. |
| `tollgate.usage.cache_read_tokens`, `tollgate.usage.cache_write_tokens` | The prompt cache tokens. |
| `tollgate.denied_by` | The limit that refused the request. |

Each attempt span has `gen_ai.operation.name`, `gen_ai.provider.name` (`anthropic`, `openai`, `gcp.vertex_ai` or `ollama`), `gen_ai.request.model` (the model of the target), `gen_ai.request.max_tokens`, `tollgate.attempt` (1 for the first attempt), `tollgate.target`, and `http.response.status_code`.
A failed attempt has the status `Error` and an `error.type`: the upstream status, or a reason such as `first_token_timeout`.
The server span has a `retry` event for each extra attempt, with the old target, the new target, and the reason.

The server span has the status `Error` for a 5xx status, and for a stream that stopped after its first byte.

Spans never contain prompts, responses, keys or headers ([ADR 0010](adr/0010-logging.md)).
If the receiver is not available, Tollgate drops the spans. It writes one warning, and one line when the receiver is available again. Requests do not wait for the receiver.

## Response header

Each response from a provider has the header `x-tollgate-overhead-ms`: the time in Tollgate before the first upstream request, in milliseconds with three decimals, for example `0.214`.
It is the same value as `overhead_us` in the access log and `tollgate_gateway_overhead_seconds`.

## Admin port

The admin port is a separate listener ([ADR 0025](adr/0025-admin-port.md)). Clients with a Tollgate key cannot use it.
Tollgate writes the line `admin port started` with its address at start.
During a drain, the admin port stays open until the data plane stops.

On a loopback address, the admin port needs no token. On other addresses, set `server.admin_token`:

```yaml
server:
  admin_listen: "0.0.0.0:9090"
  admin_token: env:TOLLGATE_ADMIN_TOKEN
```

Then each request must send the token as a bearer token:

```sh
curl -H "Authorization: Bearer $TOLLGATE_ADMIN_TOKEN" http://tollgate:9090/metrics
```

In Prometheus, use `authorization.credentials_file` in the scrape configuration. See [`deploy/compose/prometheus.yml`](../deploy/compose/prometheus.yml).

### Profiles

The Go profiles are off by default. To turn them on, set `server.pprof: true`. Then the admin port serves `/debug/pprof/`.

To record a CPU profile for 30 seconds and look at it, run this command on the Tollgate host:

```sh
go tool pprof -http=127.0.0.1:8081 'http://127.0.0.1:9090/debug/pprof/profile?seconds=30'
```

A heap profile can show data from requests. Turn the profiles on only when you need them, and keep the admin port on a loopback address or behind its token.

## Demo stack

The demo stack shows the metrics, the dashboard and the traces with live data. It needs no provider key.

Prerequisites:

- Docker with Docker Compose v2
- GNU Make
- The ports 3000, 8080, 9090, 9091 and 16686 on `127.0.0.1` are free.

1. Start the stack:

   ```sh
   make demo
   ```

2. Open Grafana at `http://127.0.0.1:3000`. The home page is the Tollgate dashboard.
3. Open Jaeger at `http://127.0.0.1:16686`, select the service `tollgate`, and select **Find Traces**.
4. Optional: check that the stack has live data:

   ```sh
   make demo-check
   ```

5. When you are done, stop the stack:

   ```sh
   make demo-down
   ```

Result: After about one minute, the dashboard shows requests, time to first token, spend, fallbacks, and the state of the circuit breakers.

The stack has these services:

| Service | What it does |
| --- | --- |
| `tollgate` | Tollgate with [`deploy/compose/tollgate.yaml`](../deploy/compose/tollgate.yaml). The data plane is on `127.0.0.1:8080` and the admin port on `127.0.0.1:9090`. |
| `fake-primary`, `fake-backup` | Fake providers with a first-token delay and a token rate, so the latency looks like a real model. |
| `fake-flaky` | A fake provider that always answers 529. It is the first target of `claude-haiku-5-5`, so that route shows fallbacks and an open breaker. |
| `loadgen` | Four loops that send streaming and non-streaming requests in both API formats. |
| `prometheus` | Reads `/metrics` every 5 seconds. Its UI is on `127.0.0.1:9091`. |
| `grafana` | The Tollgate dashboard, with anonymous view access. |
| `jaeger` | Receives the traces. |

The demo key and the admin token are in the repository, so they are public. Do not use the demo configuration outside your computer.

If `make demo` fails because a port is in use, stop the program that uses the port, or change the port in [`deploy/compose/compose.yaml`](../deploy/compose/compose.yaml).

## Use the dashboard in your Grafana

The dashboard is in [`deploy/grafana/tollgate.json`](../deploy/grafana/tollgate.json). It has a `datasource` variable, so it works with each Prometheus data source.

1. In Grafana, go to **Dashboards** > **New** > **Import**.
2. Upload `tollgate.json`.
3. Select your Prometheus data source in the **Data source** list at the top of the dashboard.

Result: The dashboard shows request rate, error rate, active streams, spend per hour, time to first token p50 and p95, gateway overhead p99, inter-token latency, spend by route, fallbacks, tokens, circuit breakers, and limit rejections.

## M4 acceptance evidence

| Criterion | Evidence |
| --- | --- |
| One request shows as one trace, with a span for each attempt | [TestOneTracePerRequest](../internal/server/telemetry_test.go) sends a request whose first target answers 529, in both API formats. It checks one trace ID, one server span, the `auth`, `preflight` and `limit check` spans, and one client span for each attempt. |
| Spans reach a real receiver | During development, Tollgate sent spans to Jaeger v2.22.0. Jaeger showed each of 292 requests as one trace with one server span. The fallback requests had two attempt spans. [TestEncodeSpans](../internal/telemetry/otlp_test.go) keeps the encoding the same. |
| The dashboard shows live data from `make demo` | The `Demo stack` job in [CI](../.github/workflows/ci.yml) runs `make demo-up` and `make demo-check`. During development, the same configuration ran with Prometheus v3.15.1 and Grafana v13.2.4, and each dashboard query returned data through Grafana. |
| The dashboard uses only real metrics | [TestDashboardUsesKnownMetrics](../internal/telemetry/dashboard_test.go). |
| The metrics have the values of the requests | [TestMetrics](../internal/server/telemetry_test.go), [TestLimitRejectionMetric](../internal/server/telemetry_test.go), and [the telemetry tests](../internal/telemetry/telemetry_test.go). |
| `/metrics` works during a drain | [TestServeSIGTERM](../cmd/tollgate/serve_test.go) reads `tollgate_active_streams 1` from the real binary after `SIGTERM`. |
| The admin token and the profiles | [the admin tests](../internal/admin/admin_test.go). |

The cost of tracing and of the metrics on the request path is not measured. The benchmarks of M5 will measure the overhead.
