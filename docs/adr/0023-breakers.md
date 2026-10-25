# ADR 0023: Keep circuit breakers in each process

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none; extends ADR 0007

## Context

Each provider, model, and region needs independent health state.
The brief proposes a Redis breaker key but also limits the request path to one Redis round trip.
The accepted M3 plan keeps breakers local.

## Decision

Keep one breaker per provider, model, and region in each process, with the retry budget's six-bucket window.
Open it at the configured failure ratio and minimum request count.
After `open_for`, permit one test attempt. Success closes it; failure opens it again.
Ignore results from attempts that started before the last state change.
Client failures and 4xx results are neutral. A 429 sets a separate cooldown from `retry-after`.
An upstream stream failure counts even after the response starts, but never causes a fallback.
If all targets are blocked, return 502 without an upstream request.

## Alternatives

1. Share breaker state in Redis. This needs extra round trips and a distributed lease for each test attempt.
2. Omit test attempts. Every request can reach the failed provider when the open time ends.

## Consequences

- Multiple copies can each test a target. Redis outages do not affect breaker decisions.
- A restart clears breaker and cooldown state.
- Warning logs record breaker state changes. Metrics remain part of M4.
