# Architecture decision records

This page lists the design decisions of Tollgate.

Each record has a number, a status, and a date. To change a decision, write a new record that supersedes the old record. Do not edit the decision of an accepted record.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](0001-language.md) | Use Go and one static binary | Accepted |
| [0002](0002-api-formats.md) | Pass request bodies through without changes | Accepted |
| [0003](0003-token-estimate.md) | Estimate input tokens from the body size | Accepted |
| [0004](0004-limit-state.md) | Keep limit state behind one store interface | Accepted |
| [0005](0005-budgets.md) | Reserve the estimate, then settle the real cost | Accepted |
| [0006](0006-store-failure.md) | Rate limits fail open, hard budgets fail closed | Accepted |
| [0007](0007-fallback.md) | Fall back only before the first byte | Accepted |
| [0008](0008-caching.md) | Use sticky routing and an exact cache, not a semantic cache | Accepted |
| [0009](0009-stream-relay.md) | Relay streams as a tee | Accepted |
| [0010](0010-logging.md) | Log metadata only | Accepted |
| [0011](0011-money.md) | Store money as integer micro-USD | Accepted |
| [0012](0012-secrets-and-supply-chain.md) | Protect keys and the build pipeline | Accepted |
| [0013](0013-graceful-drain.md) | Drain open streams before shutdown | Accepted |
| [0014](0014-json-v2.md) | Use encoding/json/v2 | Accepted |
| [0015](0015-sdk-test-module.md) | Keep SDK tests in a separate Go module | Accepted |
| [0016](0016-timeouts.md) | Define the four timeouts of an upstream request | Accepted |
| [0017](0017-body-edits.md) | Edit request bodies in place, without a new encode | Accepted |
| [0018](0018-reservation-size.md) | Reserve a capped output, and document the overspend bound | Accepted |
| [0019](0019-rebuild-spend.md) | Rebuild the spend from the usage file at start | Accepted |
| [0020](0020-readyz-store.md) | Keep /readyz ready when the limit store fails | Accepted |

## Template

Use this structure for a new record. Copy the next number.

```markdown
# ADR NNNN: Title in the imperative

- Status: Proposed | Accepted | Superseded by NNNN
- Date: YYYY-MM-DD
- Supersedes: none | NNNN

## Context

The problem and the facts that the decision depends on.

## Decision

What Tollgate does.

## Alternatives

1. Each alternative and the reason not to use it.

## Consequences

- What changes because of the decision.
```
