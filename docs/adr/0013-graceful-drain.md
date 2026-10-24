# ADR 0013: Drain open streams before shutdown

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none

## Context

Agent responses can stream for minutes.
A rolling deploy sends `SIGTERM` to each copy. If the copy stops at once, it cuts all open streams.

## Decision

On `SIGTERM`, Tollgate does these steps:

1. It fails `/readyz`, so the load balancer stops sending new requests.
2. It refuses new requests with status 503 and `retry-after`.
3. It lets open streams finish, up to `drain_timeout`.
4. It cancels the streams that are still open, and then it stops.

## Alternatives

1. Stop at once. This cuts streams, and the tenant pays for tokens that the client does not get.
2. Wait with no limit. This can block a deploy for a long time.

## Consequences

- A deploy can take up to `drain_timeout` for each copy.
- The orchestrator must give a termination grace period that is longer than `drain_timeout`.
