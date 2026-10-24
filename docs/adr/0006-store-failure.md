# ADR 0006: Rate limits fail open, hard budgets fail closed

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none

## Context

In cluster mode, Tollgate keeps limit state in Redis. Redis can fail.
If all requests fail when Redis fails, one failure stops all agents.
If all limits stop when Redis fails, spend has no limit.

## Decision

If the store is not available, rate limits fail open. Each copy then uses a local backup limit.
If the store is not available, requests that have a hard budget fail closed.
Count each store error in a metric.

## Alternatives

1. Fail open for all limits. This keeps traffic moving, but it permits unlimited spend.
2. Fail closed for all limits. This prevents overspend, but one Redis failure stops all traffic.

## Consequences

- Tenants with only soft budgets continue to work during a Redis outage.
- Tenants with hard budgets get an error until Redis is available again.
