# ADR 0007: Fall back only before the first byte

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none

## Context

A provider can fail before the response starts or during the stream.
If Tollgate sends a request again after the client gets bytes, the client gets two responses in one stream.

## Decision

Tollgate sends a request to the next target only when no byte has gone to the client.
After the first byte, Tollgate sends an error event and closes the stream.
Retry or fall back on status 429, 5xx, and 529, and on a connect timeout or a first-token timeout.
Try the same model on a different host before a different model.
Retries can be at most 10% of requests (the retry budget). Keep one circuit breaker for each provider, model, and region.

## Alternatives

1. Keep the first tokens in a buffer and fall back during the buffer time. This adds latency to all requests.
2. Send a request again after the first byte. This gives the client damaged output.

## Consequences

- The first-token timeout controls how fast a fallback starts.
- A failure during a stream is visible to the client.
- The retry budget stops retry storms when a provider is overloaded.
