# ADR 0001: Use Go and one static binary

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none

## Context

Tollgate runs on the request path of every LLM call. It must use little memory and CPU, start fast, and be easy to install.
A gateway also holds provider keys, so each dependency is a risk.

## Decision

Write Tollgate in Go. Ship one static binary with no cgo.
Use the standard library first. Use an outside package only if it is on the allowed list in the brief, or if a new ADR approves it.

## Alternatives

1. Python, as LiteLLM uses. This gives a larger runtime, a larger image, and more dependencies.
2. Rust. This gives good performance, but slower development and fewer people who can contribute.
3. TypeScript on Node.js, as Portkey uses. This needs a runtime and has a large dependency tree.

## Consequences

- The release is one file for each platform. The container image can use a distroless static base.
- `context.Context` gives one model for timeouts and cancellation on all calls.
- The dependency tree stays small, so a review of each dependency is possible.
