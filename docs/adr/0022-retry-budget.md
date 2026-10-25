# ADR 0022: Limit extra attempts with a local retry budget

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none; extends ADR 0007

## Context

Retries add load during an outage. A strict ratio prevents fallback when a gateway has very little traffic.
Retries and fallbacks both add upstream requests.

## Decision

Permit 10 extra attempts plus `retry.budget_ratio` times the recent request count.
The ratio defaults to 0.1. Both retries and fallbacks consume the budget.
Count requests and extra attempts in six 10-second buckets. Expire each bucket after one minute by its start time.
Keep this state in each process. A skipped target consumes no retry budget.

## Alternatives

1. Use only a strict ratio. A single request cannot fall back.
2. Share the retry budget in Redis. This adds limit-store operations for upstream attempts.

## Consequences

- A bucket expires 50 to 60 seconds after an individual event.
- A restart clears the budget. Multiple copies each have their own allowance.
- A ratio of 0 retains the floor of 10 attempts per window.
