# ADR 0027: Derive session IDs from the original prompt prefix

- Status: Accepted
- Date: 2026-10-25
- Supersedes: none

## Context

Agent clients do not always send a stable session ID. Redaction can make different prompts identical.

## Decision

Prefer x-tollgate-session. Otherwise hash the original system field and messages through the first user message with SHA-256. Use 128 hash bits with the auto- prefix. Derive before redaction. Permit operators to disable derivation.

## Alternatives

Require headers for all sessions. This leaves unmodified clients without session budgets. Hash the masked prefix. This can merge unrelated sessions.

## Consequences

JSON byte changes can change the derived ID. Explicit IDs remain the stable choice. Raw prompt text does not enter usage events.
