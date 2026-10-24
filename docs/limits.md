# Rate limits and budgets

This page tells how Tollgate counts requests, tokens and spend, and how far the counts can go over a limit.

Each tenant has its own limits and budgets. All keys of a tenant share them. See [configuration](configuration.md#limits) for the fields.

## Rate limits

A tenant can have two rate limits:

- `requests_per_minute`: the number of requests in one minute.
- `tokens_per_minute`: the number of tokens in one minute. Tollgate counts the input, output, cache read and cache write tokens.

### How Tollgate counts

Tollgate uses a sliding-window counter with one-minute windows ([ADR 0004](adr/0004-limit-state.md)). It keeps one counter for the current minute and one for the previous minute. The estimate for the last 60 seconds is:

```text
estimate = previous minute × (1 − elapsed part of the current minute) + current minute
```

Tollgate admits a request if the estimate plus the cost of the request is not more than the limit. The cost is 1 for the request limit. For the token limit, the cost is the token estimate of the request: the input estimate ([ADR 0003](adr/0003-token-estimate.md)) and the reserved output tokens ([ADR 0018](adr/0018-reservation-size.md)). After the response, Tollgate replaces the estimate with the tokens that the provider reported.

The endpoint `/v1/messages/count_tokens` uses only the request limit.

### Tolerance

These limits are true for the count of admitted requests:

| Period | Most requests admitted |
| --- | --- |
| One calendar minute, for example 10:04:00 to 10:05:00 | The limit |
| Any 60-second period | 2 × the limit, in the worst case |

The worst case needs a burst at the end of one minute and steady traffic to the end of the next minute. For steady traffic, the count stays near the limit.

`TestThreeCopiesShareOneLimit` in `internal/server/cluster_test.go` checks the tolerance with three Tollgate copies that share one store, with a limit of 50 requests per minute:

| Load | Result in the test |
| --- | --- |
| 180 parallel requests in one minute, 60 to each copy | Exactly 50 admitted |
| 3 requests each second for 4 minutes, 1 to each copy | 49 admitted in each calendar minute. The largest count in a 60-second period is 50. |

The test fails if a calendar minute has more than the limit, if a full minute of overload has less than 90% of the limit, or if a 60-second period has more than 2 × the limit. CI runs it with a memory store and with Redis.

### Response headers

Each response of a tenant with a rate limit has these headers. If the tenant has two rate limits, the headers show the limit with the smallest part left.

| Header | Value |
| --- | --- |
| `ratelimit-limit` | The limit for one minute |
| `ratelimit-remaining` | The estimate of the count that the limit still allows |
| `ratelimit-reset` | The seconds to the end of the current minute |
| `retry-after` | Only on status 429: the estimate of the seconds until the limit admits the request |

If a request needs more tokens than `tokens_per_minute`, the limit can never admit it. Tollgate then sends status 429 with `retry-after: 60` and a message that tells you to make the request smaller.

### More than one Tollgate copy

With `store.kind: redis`, all copies use the same counters. One Lua script checks all limits and budgets of a request and changes them, in one Redis round trip. The script is atomic, so parallel requests to different copies cannot pass the limit together.

Each copy uses its own clock to find the current minute. Keep the clocks of all copies in sync with NTP. A clock difference of a few seconds moves the edge of the minute for that copy by the same time.

## Budgets

A budget is the largest spend of a tenant in a window, in USD. Tollgate calculates the cost of each request from the [price table](configuration.md#pricing), in micro-USD ([ADR 0011](adr/0011-money.md)).

| Window | Period | Key |
| --- | --- | --- |
| `month` | The calendar month in UTC | The tenant |
| `session` | All requests with the same `x-tollgate-session` header. The spend ends 24 hours after the last request of the session. | The tenant and the session ID |

A request without the `x-tollgate-session` header has no session budget. A session ID has 1 to 128 visible ASCII characters, with no spaces. For other values, Tollgate sends status 400.

| Mode | When the budget has no money left for a request |
| --- | --- |
| `hard` | Tollgate sends status 402 with the error type `budget_exceeded`. The Anthropic and OpenAI SDKs do not send the request again. |
| `soft` | Tollgate sends the request, and writes a `soft budget exceeded` warning to the log. |

### Reserve and settle

1. Before Tollgate sends a request, it reserves the estimated cost against each budget ([ADR 0005](adr/0005-budgets.md)). The estimate is the cost of the input estimate and of the smaller of `max_tokens` and `reservation.output_tokens` ([ADR 0018](adr/0018-reservation-size.md)).
2. A hard budget admits the request only if the spend, with all open reservations, plus the new reservation is not more than the limit.
3. After the response, Tollgate replaces the reservation with the real cost.

These cases change step 3:

- If the provider sends an error status, or no response, the request costs 0. Tollgate removes the reservation.
- If the provider sends a successful response with no usage, the cost is the larger of the reservation and the cost of the input estimate. The usage event has `usage_estimated: true`.
- If the response stops after its first byte, for example because the client disconnects or the provider fails, the usage is not complete. The provider can charge for output that Tollgate did not receive. The cost is then the larger of the reservation and the cost of the usage that Tollgate received.

### Worst-case overspend

A hard budget can go over its limit, because the real output of a request can be larger than its reserved output. The largest overspend is the sum, over the requests that are open at the same time, of:

```text
(max_tokens − reserved output tokens) × output price
+ (real input tokens − estimated input tokens) × input price, if the estimate is too low
```

Cache writes cost more than input tokens, so a cache write adds its extra price to the second line.

Example with the prices of `claude-sonnet-5-5` in `pricing.yaml` (10 USD for each million output tokens), the default `reservation.output_tokens` of 4,096, and clients that send `max_tokens: 32000`:

- One open request can go over its reservation by (32,000 − 4,096) × 10 / 1,000,000 = 0.28 USD.
- With 10 requests open at the same time, the budget can go over its limit by 2.80 USD, and the input error.

To make the overspend smaller, set `reservation.output_tokens` to the `max_tokens` of your clients. Then a budget refuses requests earlier.

A request without an output limit (OpenAI permits this) has no bound. Its output can be as large as the model allows.

`TestWorstCaseOverspend` in `internal/server/cluster_test.go` checks the bound with three Tollgate copies that share one budget. In the test, 120 parallel requests each reserve 223 micro-USD and use all of their `max_tokens`, at a cost of 2,023 micro-USD each. The budget of 10,000 micro-USD admits 44 requests. The spend is 89,012 micro-USD, and the bound is 89,200 micro-USD.

Tollgate does not stop a stream that started when its spend reaches the limit. M6 adds that for session budgets.

## When the limit store fails

With `store.kind: redis`, Redis can fail. Tollgate then works as follows ([ADR 0006](adr/0006-store-failure.md)):

| Tenant | Result |
| --- | --- |
| Has a hard budget | Status 503 with `retry-after: 5`. Tollgate does not send the request. |
| Has no hard budget | Tollgate sends the request. Each copy uses local rate limits with the same values. Soft budgets record no spend. |

After a Redis error, each copy uses only its local limits for 5 seconds. Then it tries Redis again. This prevents a wait for the Redis timeout on each request.

`/readyz` stays ready when Redis fails, and its body shows `"store":"unavailable"` ([ADR 0020](adr/0020-readyz-store.md)).

If the settle step fails, the reservation stays in the budget until the end of the period. The budget then counts more spend than the real spend, which is the safe direction.

## Restarts

With `store.kind: memory`, Tollgate reads the usage file at start, before it accepts requests. It adds the spend of the current month and of the sessions of the last 24 hours to the budgets ([ADR 0019](adr/0019-rebuild-spend.md)). The rate limit counters start at 0.

With `store.kind: redis`, Redis keeps the counters, and Tollgate does not read the usage file.
