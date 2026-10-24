# ADR 0009: Relay streams as a tee

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none

## Context

Tollgate must send the stream to the client without changes, and it must also count the tokens in the stream.
OpenAI sends usage in a stream only if the request asks for it.
If the client disconnects, the provider continues to generate tokens, and the tenant pays for them.

## Decision

Send stream bytes to the client without changes, and parse a copy for usage and timing.
For OpenAI streams, add `stream_options.include_usage` to the request.
If the client disconnects, cancel the upstream request.

## Alternatives

1. Parse the stream and write new events. This can change the bytes and break clients.
2. Count tokens from the text. This is not exact and does not include cache tokens.

## Consequences

- The client gets the same bytes as from the provider.
- If the client did not ask for usage, the stream has one more chunk than the client expects. That chunk has no choices. A later ADR decides if Tollgate removes it.
- The parser must handle any input without a panic. Fuzz tests check this.
