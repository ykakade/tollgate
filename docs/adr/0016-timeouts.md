# ADR 0016: Define the four timeouts of an upstream request

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none

## Context

ADR 0007 and the brief name four timeouts for each route: connect, first token, idle, and total.
The brief does not say where each timeout starts and stops.
Agent responses can stream for minutes, so one server-wide write timeout cannot work.
Some agent requests do not stream. For these requests, the first body byte comes only after the provider made the full response.

## Decision

Tollgate measures each timeout as follows:

| Timeout | Starts | Stops |
| --- | --- | --- |
| `connect` | When Tollgate starts the upstream request | When a connection to the provider is ready, also a connection from the pool |
| `first_token` | When the connection is ready | When the first byte of the response body arrives |
| `idle` | After each body read | When the next body bytes arrive |
| `total` | When Tollgate starts the upstream request | When the response body ends |

The `first_token` timeout is the same for streaming and non-streaming requests.
The `idle` timeout is also the write deadline for each chunk that Tollgate sends to the client. A client that does not read for that time loses its stream.
When a timeout occurs, Tollgate closes the upstream connection.
Before the first byte, the client gets status 502 with the name of the timeout. After the first byte, the client gets an error event in the stream (ADR 0007).

## Alternatives

1. Stop the `first_token` timeout at the response headers. Anthropic sends the headers of a stream before the first token, so this does not find a provider that stops after the headers.
2. Use a different `first_token` rule for non-streaming requests. This makes the setting harder to understand, and the routes of coding agents use streams.
3. Use one server-wide write timeout. This cuts long streams.

## Consequences

- A route with long non-streaming responses must have a large `first_token` timeout.
- A slow client cannot keep an upstream connection open for longer than the `idle` timeout.
- The time to write the request body to the provider counts in the `first_token` timeout.
