# ADR 0005: Reserve the estimate, then settle the real cost

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none

## Context

The output size of a response is not known before the response ends.
If Tollgate reserves the cost of `max_tokens` for each request, budgets block requests too early.
If Tollgate reserves nothing, many parallel requests can go over the budget.

## Decision

Before a request, reserve the estimated cost against each budget.
After the response, replace the reservation with the real cost.
A hard budget stops a stream when the spend reaches the limit. A soft budget only gives a warning.
Document the worst-case overspend and test it.

## Alternatives

1. Reserve the cost of `max_tokens`. This is safe, but it blocks requests when the budget still has money.
2. Check the budget only after the response. This is simple, but parallel requests can overspend without a limit.

## Consequences

- The spend can go over a hard budget by a bounded amount. The docs give the bound.
- A crash between reserve and settle leaves a reservation. The reservation ends with its period.
