# ADR 0028: Keep the last successful target for the provider cache lifetime

- Status: Accepted
- Date: 2026-10-25
- Supersedes: none

## Context

A return to the primary after fallback can lose a warm prompt cache on the backup.

## Decision

Read the sticky entry with admission and write the successful target with settlement. Scope the entry by tenant and session. Its TTL is `providers.<name>.cache_ttl`. Validate the stored target hash against the current route. Breakers, cooldowns and retry rules still apply. Record target switches and an upper bound for their cache loss.

## Alternatives

Always use the first target. This discards warm backup caches after recovery. Add a separate Redis read. This adds another request-path round trip.

## Consequences

Concurrent requests can race to select a target. Entries are advisory. Different routes validate the saved target before use. Cache loss includes new input and is not an exact counterfactual price.
