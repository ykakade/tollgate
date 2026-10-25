# ADR 0030: Cache exact responses in bounded tenant-scoped memory

- Status: Accepted
- Date: 2026-10-25
- Supersedes: none

## Context

Some routes can reuse a complete response. Incorrect reuse across prompts or tenants is unacceptable.

## Decision

Keep complete successful responses in a process-local LRU cache, opt-in per route. Hash the tenant, API, route, version headers and exact original body. Bound entry size and total size. Use a route TTL. Check admission before cache access. Refund reservations on hits and record zero upstream cost.

## Alternatives

Semantic matching can reuse the wrong tool response. Redis response storage adds another request-path round trip. A shared cache introduces a larger data boundary.

## Consequences

A hit can still fail admission. Streams replay without original timing. Restored personal data can remain in cache memory. A restart clears the cache. No cross-process consistency is promised.
