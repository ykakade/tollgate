# ADR 0010: Log metadata only

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none

## Context

Prompts and responses can contain secrets and personal data.
Logs and traces often go to systems with wide access.

## Decision

By default, log only metadata: IDs, model names, token counts, cost, status, and timing.
Request bodies are logged only if a tenant enables it. Tollgate redacts them first.
Never put prompt or response text in traces.
Never log `Authorization`, `x-api-key`, or provider keys, also not in error messages.

## Alternatives

1. Log full bodies by default. This helps to find problems, but it puts secrets in logs.
2. No body logging at all. This is safe, but some teams need it to find problems.

## Consequences

- To find a problem in a request body, an operator must enable body logging for the tenant.
- Tests check that keys do not appear in logs.
