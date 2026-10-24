# ADR 0004: Keep limit state behind one store interface

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none

## Context

Rate limits and budgets must stay correct when more than one Tollgate copy runs.
A single node must work with no other service.

## Decision

Define one `limiter.Store` interface. Use a memory store for a single node and a Redis store for a cluster.
For each request, one atomic store call checks all limits and reserves the estimated cost. In Redis, this call is one Lua script.
Use a sliding-window counter for rate limits.

## Alternatives

1. Token bucket. This is accurate, but it needs more state and a more complex Lua script.
2. GCRA. This is accurate and small, but it is harder to explain and to show in headers.
3. A separate call for each limit. This adds round trips and gives race conditions between calls.

## Consequences

- A request needs at most one Redis round trip before it goes to the provider.
- The two stores use the same key names, so one test suite covers both.
- The sliding-window counter is approximate at window edges. The docs give the tolerance.
