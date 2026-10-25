# ADR 0029: Cancel streams when estimated cumulative cost exceeds their hard budget room

- Status: Accepted
- Date: 2026-10-25
- Supersedes: none

## Context

Admission reserves a capped output estimate. A long stream can spend more than the reservation.

## Decision

For successful event streams, compare running usage cost with the hard budget room at admission. Estimate output tokens from text bytes where the provider has no cumulative usage. Cancel upstream and send an API-format error event after a cutoff. Apply the smallest room across hard session and month budgets. Keep at least the reservation when usage is incomplete.

## Alternatives

Wait for final usage. This permits unlimited output after admission. Tokenize every delta locally. Provider tokenizers are not uniformly available and add cost.

## Consequences

This extends ADR 0018 with an approximate stream cutoff. Non-stream responses retain its admission-only behavior. Parallel streams and provider cancellation delay can still overspend. The cutoff never starts another attempt after response bytes.
