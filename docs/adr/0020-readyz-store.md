# ADR 0020: Keep /readyz ready when the limit store fails

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none

## Context

The project brief says that `/readyz` fails when the stores cannot be reached.
In cluster mode, all Tollgate copies use the same Redis server. If Redis fails and `/readyz` fails, the load balancer removes all copies, and all traffic stops.
ADR 0006 says that rate limits fail open when the store fails, so that one Redis failure does not stop all agents.

## Decision

`/readyz` does not fail when the limit store does not answer. It fails only during a drain (ADR 0013).
The body of `/readyz` shows the state of the store: `{"status":"ready","store":"ok"}` or `{"status":"ready","store":"unavailable"}`.

## Alternatives

1. Fail `/readyz` when Redis does not answer, as the brief says. This stops all traffic in a Redis outage, also for tenants with only soft budgets.

## Consequences

- During a Redis outage, the copies continue to serve requests with local rate limits. Requests of tenants with a hard budget get status 503.
- Monitoring must read the `store` field of `/readyz`, or the warning logs, to find a Redis outage. M4 adds the `store_errors_total` metric.
