# ADR 0032: Serve a read-only admin API from a bounded event index

- Status: Accepted
- Date: 2026-10-25
- Supersedes: none

## Context

The dashboard needs recent usage without database calls on the request path. The current release has JSONL and no history database.

## Decision

Keep the newest usage.index_events events in a synchronized ring. Populate it from JSONL at startup and emit new events directly. Aggregate usage and projections on the admin listener. Return only configured key IDs and policy fields, never key hashes or secrets. Protect API access with the separate admin bearer token when configured.

## Alternatives

Query the full JSONL file per API request. This grows without a bound. Add Postgres now. This expands deployment and dependency scope. Add key mutation without a persistent key store. This creates restart inconsistencies.

## Consequences

The index is local to one process and can have incomplete history. Projections are informational, independent of authoritative budget enforcement. Key creation, revocation and config reload endpoints remain unavailable; operators edit YAML and restart.
