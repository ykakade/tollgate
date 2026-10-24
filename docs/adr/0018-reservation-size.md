# ADR 0018: Reserve a capped output, and document the overspend bound

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none

## Context

ADR 0005 says that a request reserves its estimated cost before it goes to the provider.
The output size of the response is not known before the response ends.
The size of the output part of the reservation sets how much a hard budget can go over its limit, and how early it refuses requests.

## Decision

The reservation of a request is the cost of two parts, with the prices of its target:

1. The input estimate of ADR 0003: the body size in bytes divided by 4.
2. The output part: the smaller of `max_tokens` (or `max_completion_tokens`) and `reservation.output_tokens`. The default of `reservation.output_tokens` is 4,096. If the request has no output limit, the output part is `reservation.output_tokens`.

A hard budget admits a request only if the spend, with all open reservations, plus the new reservation is not more than the limit.
After the response, the real cost replaces the reservation.
If the provider sends no usage, the request keeps the larger of its reservation and the cost of the input estimate.

The worst-case overspend of a hard budget is the sum, over the requests that are open at the same time, of:

- (`max_tokens` − reserved output tokens) × the output price, and
- (real input tokens − estimated input tokens) × the input price, if the estimate is too low. Cache writes cost more than input tokens, so a cache write adds its extra price too.

`docs/limits.md` gives the formula with an example. `TestWorstCaseOverspend` checks it with three Tollgate copies.

## Alternatives

1. Reserve the full `max_tokens`. The overspend is then only the input error, but a budget refuses requests while it still has much money. Agents often send a `max_tokens` of 32,000 or more and use a small part of it.
2. Reserve only the input. The overspend is then up to one full output for each open request.
3. Stop a stream when its spend reaches the limit. This needs a count of output tokens during the stream. M6 adds it for session budgets. Until then, a hard budget does not stop a stream that started.

## Consequences

- With the defaults, a budget refuses requests only near its limit.
- An operator can set `reservation.output_tokens` to the `max_tokens` of the clients to make the overspend almost 0.
- A request without an output limit has no overspend bound: its output can be as large as the model allows. The OpenAI API permits such requests. Anthropic requests always have `max_tokens`.
