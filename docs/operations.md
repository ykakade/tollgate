# Operations

This page tells you how to start and stop Tollgate, how to check its health, and how to read its logs.

## Start Tollgate

Prerequisites:

- A valid configuration file. See [configuration](configuration.md).
- The environment variables that the `api_key` fields name.

1. Check the configuration file:

   ```sh
   tollgate config validate -config tollgate.yaml
   ```

2. Start Tollgate:

   ```sh
   tollgate serve -config tollgate.yaml
   ```

Result: Tollgate writes the line `server started` with the listen address to standard error.

If Tollgate does not start, read the messages on standard error. Each message tells what failed and what to do.

## Health endpoints

| Endpoint | Status 200 | Status 503 |
| --- | --- | --- |
| `GET /healthz` | The process runs. | Not used. |
| `GET /readyz` | Tollgate accepts requests. | Tollgate drains and does not accept new requests. |

Use `/healthz` for a liveness probe and `/readyz` for a readiness probe.

## Stop Tollgate

When Tollgate gets `SIGTERM` or `SIGINT`, it drains ([ADR 0013](adr/0013-graceful-drain.md)):

1. `/readyz` gets status 503, so the load balancer stops sending new requests.
2. The listener stays open. New requests get status 503 with `retry-after: 5`.
3. Open requests and streams continue for up to `server.drain_timeout`.
4. When all requests are done, or when `drain_timeout` ends, Tollgate cancels the remaining requests and stops.

A second `SIGTERM` or `SIGINT` cancels the open requests at once.

Do not give the orchestrator a termination grace period that is shorter than `drain_timeout`. If the grace period is shorter, the orchestrator stops Tollgate before the streams finish.

If no request is open, Tollgate stops at once and closes the listener. To give the load balancer time to read `/readyz`, add a short delay before the signal, for example a Kubernetes `preStop` sleep.

## Logs

Tollgate writes one JSON line to standard error for each request. The message of the line is `request done`.

Tollgate never logs request bodies, response bodies, client keys or provider keys ([ADR 0010](adr/0010-logging.md)).

| Field | Description |
| --- | --- |
| `request_id` | The ID of the request. The client gets the same ID in the `x-request-id` header. |
| `method`, `path`, `status` | The request and the status that Tollgate sent. Status 499 means that the client disconnected before the response started. |
| `tenant_id`, `key_id` | The tenant and the key of the request. |
| `api`, `route_model`, `stream` | The API format, the model name that the client sent, and the stream setting. |
| `provider`, `provider_model`, `attempts` | The target that got the request, and the number of upstream requests. |
| `input_tokens`, `output_tokens` | The tokens that the provider reported. `input_tokens` does not include cache tokens. |
| `cache_read_tokens`, `cache_write_tokens` | The tokens that the provider read from or wrote to its prompt cache. |
| `reasoning_tokens` | The part of `output_tokens` that the model used to reason. Only OpenAI reports it. |
| `usage_estimated` | `true` if the provider reported no usage. Then `input_tokens` is an estimate from the body size ([ADR 0003](adr/0003-token-estimate.md)). |
| `ttft_ms` | The time from the start of the request to the first byte of the response body. |
| `duration_ms` | The time of the full request. |
| `overhead_us` | The time in Tollgate before the upstream request, without the time to read the request body. |
| `bytes_in`, `bytes_out` | The size of the request body and of the response body. |
| `upstream_request_id` | The request ID of the provider, for support requests to the provider. |
| `error` | Why the request failed or stopped early, if it did. |

## Errors after the first byte

If the provider stops or fails after Tollgate sent the first byte of a stream, Tollgate does not send the request again ([ADR 0007](adr/0007-fallback.md)). It adds an error event to the stream and closes it:

- Anthropic routes get `event: error` with an `error` object.
- OpenAI routes get a `data:` line with an `error` object.

The official Anthropic and OpenAI SDKs show these events as errors. The access log keeps the usage that the provider sent before the failure.
