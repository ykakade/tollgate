# ADR 0021: Apply ordered retries before the first byte

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none; extends ADR 0007 and ADR 0018

## Context

M3 adds multiple targets to each route. Each API error needs a fixed retry rule.
An error response must remain useful to the official SDKs when no target remains.
A fallback can also have a different price.

## Decision

Try targets in configured order. Skip open breakers, busy test attempts, and targets with an active cooldown.
For 5xx except 529, retry the same target once, then advance. For 529 and 429, advance at once.
For connection failures and timeouts before the first byte, advance. Other 4xx responses pass through.
Never retry after the first body byte reaches the client. Upstream headers alone do not start the response.
`retry.max_attempts` includes the first attempt and defaults to 3.
Send the last upstream error response unchanged. If the last attempt has no response, send 502 with the targets and reasons.

Reserve the highest estimate among all targets. Settle once with the serving target's price.
An incomplete stream retains at least the serving target's estimate, as ADR 0018 requires.

## Alternatives

1. Retry every error on the same target. This delays recovery from overload and authentication errors.
2. Return a gateway error for every failed route. This hides upstream status and retry headers from SDKs.

## Consequences

- Configuration order decides the fallback order. Put the same model on another host before a different model.
- All attempts have separate timeouts. Maximum route time can exceed one attempt's total timeout.
- Pre-response failures release their reservations when the final request settles. Provider billing can still differ from observed usage.
