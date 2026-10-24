# ADR 0011: Store money as integer micro-USD

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none

## Context

Floating-point numbers cannot show most decimal amounts exactly. Sums of many small costs drift.
Budget checks must be exact and safe.

## Decision

Store amounts as `int64` micro-USD (1 USD = 1,000,000 micro-USD).
Store prices as `int64` micro-USD for each million tokens.
Round the cost of each request up to the next micro-USD.

## Alternatives

1. `float64` USD. This drifts and gives different results in different orders.
2. A decimal library. This is exact, but it adds a dependency and is slower.

## Consequences

- An `int64` holds more than 9 trillion USD in micro-USD, which is sufficient.
- Rounding up can add at most 1 micro-USD to each request. This keeps budget checks on the safe side.
