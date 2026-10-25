# Reliability

Tollgate tries the targets of a route in their configured order.
Put the same model on another host before a different model.
A route can have up to 10 targets. All targets must use the same API format.

## Retry rules

These rules apply before the first response body byte reaches the client.
Response headers from the provider do not start the client response.

| Result | Action |
| --- | --- |
| Status 429 | Try the next target. Skip this target until its `retry-after` ends. |
| Status 529 | Try the next target. |
| Other status 5xx | Retry the same target once, then try the next target. |
| Connect timeout, first-token timeout, or connection failure | Try the next target. |
| Other status 4xx | Send the response to the client unchanged. |

`retry.max_attempts` limits the total attempts, including the first. The default is 3.
Tollgate closes discarded error responses without a body read, so a stalled error body cannot delay the next target.
If no attempt remains, Tollgate sends the last upstream response unchanged, including its error status and `retry-after`.
If the last attempt fails without a response, Tollgate sends status 502 with the failed and skipped targets.

A valid `retry-after` can be a number of seconds or an HTTP date.
The cooldown has a maximum of 10 minutes. A missing or invalid value gives no cooldown.

After the first body byte, Tollgate never retries or falls back.
An upstream timeout, connection failure, or malformed SSE adds an error event and closes the stream.
The usage record keeps the usage received before the failure.
For malformed SSE, the client can receive the malformed bytes before the error event: the meter reads a copy after each write.

## Retry budget

The retry budget limits extra attempts across all routes in one Tollgate process.
It permits 10 extra attempts plus `retry.budget_ratio` times the number of requests in the recent minute.
The default ratio is `0.1`. A ratio of `0` permits only the 10 extra attempts.
The floor lets a gateway with little traffic recover from a failure.
Retries and fallbacks both consume this budget. A skipped target does not.

The window uses six 10-second buckets. A bucket expires between 50 and 60 seconds after an event.
Each process has its own budget. A restart clears it.

## Circuit breakers

Each provider, model, and region has one breaker in each Tollgate process.
A breaker opens when at least `breaker.min_requests` attempts have a failure ratio of at least `breaker.failure_ratio` in the recent minute.
The defaults are 20 attempts and a ratio of `0.5`. The window uses the same buckets as the retry budget.

- A successful 2xx attempt counts as a success.
- A 5xx, connection failure, upstream timeout, or malformed SSE counts as a failure.
- A 429, other 4xx, client disconnect, or client write failure does not count.
- A stream failure counts even after the first byte. It cannot cause a fallback.

An open breaker refuses attempts for `breaker.open_for`, which defaults to `30s`.
Then one request can test the target. Other requests skip it until the test ends.
A successful test closes the breaker. A failed test opens it again.
A neutral result permits another test.
Results from attempts that started before a state change cannot change the new state.

If all targets are blocked, Tollgate sends a fast 502 with the target names and a `retry-after` header.
It sends no request upstream.
Breakers and cooldowns stay in process memory, including with a Redis limit store ([ADR 0023](adr/0023-breakers.md)).

## Budgets and timeouts

Tollgate reserves the highest estimated cost among the route targets.
It settles once, at the price of the target that served the request.
If a stream is incomplete, the charge is at least that target's estimate. See [budgets](limits.md).
The usage event and the `x-tollgate-provider`, `x-tollgate-model`, and `x-tollgate-attempts` headers identify the final attempt.

The four [timeouts](configuration.md#timeouts) apply separately to each attempt.
The route can therefore take up to `retry.max_attempts` times `timeouts.total`.
A slow client stops upstream reads through backpressure. The relay keeps one 32 KiB copy buffer and applies an idle write deadline.
The SSE meter has a separate, bounded event buffer of at most 4 MiB.

## Repeat a failure

Goal: Check fallback without a live provider account.

Prerequisites: The binaries from `make build`, and a configuration with two Anthropic providers on ports 8090 and 8091.
Add both providers to one route, with the provider on port 8090 first. Use the quick-start key procedure in the [README](../README.md#quick-start-with-the-fake-provider).

1. Start the first fake provider:

   ```sh
   ./bin/fakeprovider -listen 127.0.0.1:8090 -scenario status:529
   ```

2. In another terminal, start the second fake provider:

   ```sh
   ./bin/fakeprovider -listen 127.0.0.1:8091
   ```

3. Start Tollgate with that configuration.
4. Send the quick-start request. Read the response headers with `curl -i`.

Result: The response has the second provider's name and `x-tollgate-attempts: 2`.
If it fails, run `tollgate config validate` and read the warning logs.

The flag sets a scenario for all requests. The `x-fake-scenario` request header overrides it for one request.
Use the flag on an individual fake provider to test fallback; a request header also reaches later targets.

| Scenario | Behavior |
| --- | --- |
| `ok` | Normal response. |
| `status:500`, `status:529`, `status:429:7` | Error status; the last form adds `retry-after: 7`. |
| `hang-before-headers` | No response until the request is canceled. |
| `hang-after:2-tokens` | Send two tokens, then wait. |
| `reset-after:2-tokens` | Send two tokens, then close the HTTP/1 connection. |
| `malformed-after:2-tokens` | Send two tokens, then an event whose data is not JSON. |
| `no-usage` | Normal response without usage. |

## M3 acceptance evidence

`make check` runs these tests. CI adds the race detector and a real Redis store.
The fake provider tests and the official SDK tests also check each wire format.

| Failure scenario | Test |
| --- | --- |
| 429 with `retry-after` | [TestFailureScenarios](../internal/server/failure_test.go), `anthropic/429_retry_after` and `openai/429_retry_after` |
| 529 overloaded | Same suite, `529_overloaded` |
| 500 retry then fallback | Same suite, `500_retry_once` |
| Hang before headers | Same suite, `hang_before_headers` |
| Hang after the first byte | Same suite, `hang-after:2-tokens` |
| Connection reset during a stream | Same suite, `reset-after:2-tokens` |
| Client disconnect | Same suite, `client_disconnect`; [TestClientDisconnectCancelsUpstream](../internal/server/server_test.go) |
| Slow client | Same suite, `slow_client`; [TestSlowClientBoundsReads](../internal/relay/relay_test.go) checks that reads stop after one buffer |
| Malformed SSE | Same suite, `malformed-after:2-tokens` |
| No usage | Same suite, `no_usage` |
| Three copies, one shared limit | Same suite, `three_copies`; [TestThreeCopiesShareOneLimit](../internal/server/cluster_test.go) uses memory and Redis |
| Redis down | Same suite, `redis_down`; [TestStoreDown](../internal/server/limits_test.go) |
| SIGTERM with open streams | [TestServeSIGTERM](../cmd/tollgate/serve_test.go) sends the real signal to the binary |
| All breakers open | Same suite, `all_breakers_open` |

[Additional tests](../internal/server/failure_test.go) cover maximum attempts, the retry budget, the last error response, connection refusal, Vertex, Ollama, and cost settlement.
Live cloud credentials and real provider charges are not part of CI. Vertex tests use a static token and the fake provider.

## Cloud and local targets

Use `kind: vertex` for Claude on Google Cloud. Set `project` to the project ID and `region` to the model's location.
The URL names the target model. Tollgate removes `model` from the body and adds `anthropic_version` if absent.
The response uses the existing Anthropic format and usage meter. See [ADR 0024](adr/0024-cloud-adapter.md).

Goal: Add a Vertex fallback to an Anthropic route.

Prerequisites: A Google Cloud project with access to the model, the required predict permission, and Google Application Default Credentials.

1. On a development machine, run `gcloud auth application-default login`. On a host, use its service identity or `GOOGLE_APPLICATION_CREDENTIALS`.
2. Add a provider:

   ```yaml
   providers:
     vertex:
       kind: vertex
       project: your-project-id
       region: us-east5
   ```

3. Add a target after the direct Anthropic target. Set `provider: vertex` and `model` to the model ID from your Google Cloud project.
4. If the tenant has a budget, add an explicit Vertex price entry to your price table. Set `pricing.file` to that file.
5. Run `tollgate config validate`. Then start Tollgate.

Result: A pre-response failure can use Vertex. The response header names the target that served the request.
If startup fails, read the credential error. If Vertex refuses the request, check the model ID, location, and project access.
`/v1/messages/count_tokens` skips Vertex targets and uses the next direct Anthropic target. If none exists, it returns 404.

For a local model, use `kind: ollama` with an OpenAI route. It needs no provider key and defaults to `http://localhost:11434`.
An Ollama target without a price entry costs 0, including for tenants with budgets. An explicit entry overrides that price.
Tollgate adds `stream_options.include_usage` to OpenAI streams, including Ollama streams.

The protocol details follow the [Claude cloud documentation](https://platform.claude.com/docs/en/build-with-claude/claude-models-on-google-cloud/google-managed) and [Ollama compatibility documentation](https://docs.ollama.com/api/openai-compatibility).
