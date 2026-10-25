# Agent features

This page describes session budgets, provider prompt caches, the exact response cache, and personal data redaction.

## Session IDs

Send `x-tollgate-session` with a stable ID for each task. IDs have 1 to 128 visible ASCII characters, without spaces.
The tenant and the session ID identify the budget. Two tenants can use the same ID.

Without that header, Tollgate derives an ID from the original system field and the messages through the first user message.
The ID starts with `auto-` and contains 32 hexadecimal digits from SHA-256. Prompt text does not enter the usage file.
The hash uses the original JSON bytes, so changes to whitespace or the initial prompt can change the ID.
Requests without a user message have no derived session. Use an explicit ID when the client changes its initial prompt.

```yaml
sessions:
  derive: true
  sticky: true
  warn_ratio: 0.8
```

Set `derive: false` to require explicit session IDs. Session budgets do not apply to requests without a session ID.

## Budgets and projection

Add a session budget to the tenant:

```yaml
budgets:
  - window: session
    limit_usd: 5
    mode: hard
```

The spend expires after 24 hours without a request. A hard budget reserves estimated cost before each request.
An insufficient reservation returns status 402. A soft budget records warnings and permits the request.

When settled spend crosses `sessions.warn_ratio`, Tollgate writes a `session budget warning` log and increments a metric.
`x-tollgate-session-remaining-usd` shows the room after the request reservation, before the real cost is known.

For successful event streams, Tollgate also checks cumulative reported input usage and an output estimate from text bytes.
At the hard limit, it cancels the provider request and sends an error event in the caller's API format.
It does not retry that stream. The usage event has `budget_cutoff: true`.

This cutoff is approximate. Providers can produce output before cancellation, and text bytes are an estimate of tokens.
Concurrent streams each use the room at their admission. Non-stream responses have admission checks but no cutoff.
A cutoff keeps at least the reserved cost if usage is incomplete. See [limits](limits.md) for the overspend bound.

The admin API and dashboard show spend, mean request cost, requests left, and minutes left at the observed spend rate.
These projections use retained usage events. They are estimates, not the authoritative limit-store counters.
An index with incomplete history can show less spend than the enforced budget.

## Sticky routing

By default, the next request of a session prefers the target that last returned a successful response.
An open breaker, cooldown or upstream failure can still cause fallback. The fallback target becomes the preferred target.
This avoids an immediate return to a recovered primary with a cold prompt cache.

Each provider has a `cache_ttl`, default `5m`. Set it to the prompt cache lifetime used by that provider and your requests.
The sticky entry expires after that interval without a successful upstream response.
The entry shares the existing admission and settlement store calls. Redis needs no additional request-path round trip.

```yaml
providers:
  primary:
    kind: anthropic
    api_key: env:ANTHROPIC_API_KEY
    cache_ttl: 5m
```

The usage event records `target_switched` when the target differs from the previous session target.
`cache_loss_micro_usd` estimates an upper bound: input and cache-write cost minus their cost as cache reads.
It includes the new conversation turn, so it can exceed the actual loss. It is not an additional charge.
The first request has no prior target and therefore no measured switch loss.

## Cost reports

Prerequisite: a JSONL usage file from Tollgate.

1. Show costs and cache tokens per session:

   ```sh
   ./bin/tollgate report -file data/usage.jsonl -group-by session
   ```

2. Filter a tenant and time range:

   ```sh
   ./bin/tollgate report -file data/usage.jsonl -tenant personal -since 24h -group-by provider
   ```

3. Export the totals:

   ```sh
   ./bin/tollgate report -file data/usage.jsonl -format json
   ```

Result: the report shows request counts, fallbacks, target switches, token totals, cost and estimated cache loss.
If no events match, check the file path and filters. `-config` reads the usage path from the configuration instead.
Groups are `none`, `tenant`, `key`, `session`, `route`, `provider`, `hour` and `day`.
`-since` accepts RFC 3339 or a duration such as `24h` or `7d`.

## Exact response cache

The cache is off by default. Enable it on selected routes:

```yaml
response_cache:
  max_bytes: 64MiB
  max_entry_bytes: 1MiB
routes:
  - model: claude-sonnet-5-5
    api: anthropic
    targets: [{provider: primary}]
    response_cache: {enabled: true, ttl: 10m}
```

The key hashes the tenant, API, route, version headers and exact original body.
Responses never cross tenant boundaries. Redacted requests with different original values have different cache keys.
Complete successful responses, including streams, stay in process memory under an LRU size limit.
Errors, interrupted streams and bodies above the entry limit are not stored.

A hit returns the cached bytes, with `x-tollgate-cache: hit`, a new request ID and no upstream attempt.
It costs zero provider tokens and refunds its reservation. Admission checks still apply before cache access.
A hit can therefore be refused by a hard budget or rate limit.
A miss has `x-tollgate-cache: miss`; routes without the cache have `bypass`.
A cached stream replays its bytes without the original token delays.

Cached responses can contain personal data after restoration. They stay in memory until eviction or process exit.
Use the cache only where reuse of the same response is acceptable. It has no semantic matching or cross-request tool validation.

## Personal data redaction

Enable detectors per tenant:

```yaml
redact:
  kinds: [email, credit_card, ipv4, us_ssn]
  patterns:
    - name: EMPLOYEE_ID
      regex: 'EMP-[0-9]{6}'
```

Tollgate replaces matching JSON string values with placeholders such as `[EMAIL_1]` before the provider request.
It restores recognized placeholders in successful JSON responses and supported text events of Anthropic and OpenAI streams.
The mapping stays in memory for that request. Only the count enters the usage event.
The same value receives the same placeholder within the request. Detectors run in configuration order.

This is pattern-based masking. It does not identify all personal data or inspect binary image content.
Models can alter placeholders, which prevents restoration. Response bodies above 8 MiB pass through with placeholders.
Custom regular expressions must compile and must not match empty text. Names use capital letters, digits and underscores.

## Replay results

The repository includes a reproducible synthetic agent session, not a recording from a live provider or a private coding session.
The replay sends 40 growing conversations through Tollgate and two fake providers with simulated 5-minute prompt caches.
The deterministic session uses seed `2026`, an approximately 16 KiB system prompt and a 2–8 KiB tool result per turn.
Requests are 20 s apart on a simulated clock. Output is fixed. Prices come from the committed `claude-sonnet-5-5` table.

![Cost of the sticky routing replay](assets/sticky-replay.svg)

| Primary outage | Sticky | Cache-read tokens | Cache-write tokens | Attempts | Cost (USD) | Cache loss upper bound (USD) |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| None | On | 1,115,782 | 57,349 | 40 | 0.259856 | 0 |
| None | Off | 1,115,782 | 57,349 | 40 | 0.259856 | 0 |
| 100 s | On | 1,101,462 | 71,669 | 41 | 0.294224 | 0.036238 |
| 100 s | Off | 1,095,148 | 77,983 | 45 | 0.309377 | 0.053885 |
| 6 min | On | 1,101,462 | 71,669 | 41 | 0.294224 | 0.036238 |
| 6 min | Off | 1,061,422 | 111,709 | 58 | 0.390320 | 0.135931 |

Sticky routing has no cost benefit without an outage in this replay. It costs about 25% less during the 6 min outage.
These are simulator results, not live-provider savings or gateway overhead benchmarks.
The fake cache matches JSON message prefixes; it does not reproduce every provider cache rule.

Prerequisite: Go from `go.mod`.

1. Reproduce and update the results and SVG:

   ```sh
   make replay
   ```

2. To replay a recorded session, prepare a local JSONL file with one complete Anthropic request body per line.
   Use the model `claude-sonnet-5-5`, streaming output, and a stable initial prompt.

3. Run the same scenarios on that file:

   ```sh
   TOLLGATE_REPLAY_FILE=/path/to/session.jsonl go test ./internal/server -run '^TestStickyReplay$' -count=1 -v
   ```

Result: the test prints cache tokens and cost with sticky routing on and off. An external file does not replace committed results.
If committed results change, inspect the pricing and cache simulation, then update this table with the measured output.
A recorded real-agent session remains necessary for the original M6 recorded-session acceptance criterion.
