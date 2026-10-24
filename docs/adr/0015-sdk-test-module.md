# ADR 0015: Keep SDK tests in a separate Go module

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none

## Context

The brief requires tests that use the official Anthropic and OpenAI Go SDKs as clients.
These SDKs have large dependency trees. If they are in the main `go.mod`, the dependency list of Tollgate becomes much longer.

## Decision

Put the SDK tests in a separate module in `test/sdk/`. It has its own `go.mod`, with a `replace` directive to the main module.
The main `go.mod` contains only the dependencies of the Tollgate binaries.

## Alternatives

1. Put the SDK tests in the main module with a build tag. This is simpler, but the SDKs then appear in the main `go.mod` and `go.sum`.
2. Run the SDK tests from a script in a different language. This adds a second toolchain.

## Consequences

- `make test-integration` runs the SDK tests. `make lint`, `make vet`, and `make vuln` check both modules.
- Dependabot must update both `go.mod` files.
- The SDK tests can import `internal/` packages, because the module path is inside the main module path.
