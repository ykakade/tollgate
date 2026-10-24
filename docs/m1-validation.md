# M1 validation

This page records the local M1 acceptance checks on 2026-10-24.

## Acceptance checks

| Criterion | Evidence | Result |
| --- | --- | --- |
| Claude Code works through `ANTHROPIC_BASE_URL`. | Claude Code sent a request through Tollgate to the local fake provider. It printed `Hello from the Tollgate fake provider.` and exited with status 0. | Passed |
| Streams pass through byte for byte. | `TestGoldenStreams` checks the response bytes and token usage. It covers text, tools, thinking, provider errors, and LF and CRLF line ends. | Passed |
| A client disconnect cancels the upstream request. | `TestClientDisconnectCancelsUpstream` checks that the upstream context ends. It also checks the usage received before the disconnect. | Passed |
| `SIGTERM` lets open streams finish. | `TestServeSIGTERM` sends a real signal to a subprocess. It checks readiness, refusal of new requests, completion of the open stream, and clean exit. | Passed |

## Other checks

- `make check` passed: format, vet, module files, build, unit tests, SDK tests and lint.
- `make fuzz FUZZTIME=30s` passed for the SSE parser, request preflight and configuration loader.
- The recording test compiled with the `record` build tag. It made no requests to real providers.
- Regression tests cover a first-token timeout after provider headers, stale timeout callbacks, empty responses and additional YAML documents.
- The Claude Code smoke test checked token usage, key redaction and a clean drain in the gateway logs.

## Limits of this validation

The stream fixtures were written by hand. They are not recordings from real providers. See [golden streams](../testdata/streams/README.md).

The local host has no C compiler, so the race detector did not run. CI requires the race detector for unit and SDK tests.

Real provider calls, the vulnerability scan and the external link check were not run in this validation.
