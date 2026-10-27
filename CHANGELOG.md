# Changelog

All important changes to Tollgate are in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- A searchable VitePress documentation site, a quick start guide, and documentation build and deployment commands.
- A launch video at the top of the README, made with Remotion from real Tollgate output and dashboard screenshots. The source is in `docs/video`.
- A README banner, a light and dark "How it works" diagram, and a GitHub social preview image. `npm run readme` in `docs/video` renders them from Remotion stills.

### Changed

- A shorter README with feature comparisons, a first demo, and direct links to the guides.
- A new README that starts with the problem that Tollgate solves, with a quick start for Claude Code, real failover and budget output, and a FAQ. The feature tour moved to the "See it work" section.
- An 81-second feature tour, sharp 1080p exports, and light and dark dashboard screenshots with the current logo.
- A simplified T mark shared by the dashboard, favicon, documentation, and feature video.

## [0.1.0] - 2026-10-25

### Added

- Release archives for Linux on amd64 and arm64, with checksums, a keyless cosign signature and GitHub build provenance attestations.
- The demo GIF in the README, recorded with VHS from `docs/demo/demo.tape` by `make demo-gif`.

- Derived session IDs, a warning at the session budget threshold, approximate hard-budget stream cutoffs, and spend projections.
- Sticky routing with provider cache TTLs, target switch records, cache loss estimates, and the `tollgate report` command.
- A bounded, opt-in exact response cache per route, scoped by tenant and original request bytes.
- Tenant-level personal data redaction with built-in detectors, custom regular expressions and response placeholder restoration.
- A read-only admin API with a bounded usage index, and the embedded React dashboard with overview, sessions, providers and keys pages.
- Light and dark dashboard screenshots, a synthetic sticky routing replay with published results, build and demo targets, and dashboard build checks in CI.
- The agent features and admin API guides, and decision records ADR 0027 through ADR 0033.

- Prometheus metrics on the admin port at `server.admin_listen`, with an admin token for addresses that are not loopback addresses, and Go profiles behind `server.pprof`.
- OpenTelemetry traces with the GenAI attributes: one trace for each request, with a span for each upstream attempt, sent as OTLP JSON to `telemetry.otlp_endpoint`.
- The `x-tollgate-overhead-ms` response header.
- The demo stack in `deploy/compose` with Prometheus, Grafana, Jaeger and a load generator, the Grafana dashboard in `deploy/grafana`, and the `make demo`, `make demo-check` and `make demo-down` targets.
- The `-first-token-delay` and `-token-interval` flags of the fake provider.
- The observability guide, decision records ADR 0025 and ADR 0026, and a CI job that checks the demo stack for live data.

- Ordered fallback targets, retries before the first response byte, a retry budget, and circuit breakers for each provider, model, and region.
- The `retry` and `breaker` configuration fields, 429 cooldowns from `retry-after`, and reservation at the most expensive target's estimate.
- The `vertex` provider kind for Claude with Google Application Default Credentials, and the `ollama` kind for local models.
- Fake provider failure scenarios, the M3 acceptance suite, a bounded-buffer test for slow clients, and a reliability guide.
- Malformed SSE detection, partial usage on stream failures, and decision records ADR 0021 to ADR 0024.

- Rate limits for requests and tokens per minute for each tenant, with a sliding-window counter and the `ratelimit-*` and `retry-after` headers.
- Month and session budgets for each tenant, with hard and soft modes. A hard budget refuses requests with status 402 and the error type `budget_exceeded`.
- The `limiter.Store` interface with a memory store and a Redis store. One Lua script checks and reserves all limits of a request in one Redis round trip.
- Rate limits fail open to local limits when Redis is not available. Requests of tenants with a hard budget fail closed with status 503.
- The price table `pricing.yaml`, with the Anthropic models of `tollgate init`, and the cost of each request in micro-USD. The binary has a copy of the table.
- The JSONL usage file with one usage event for each request, and the rebuild of the month and session spend from the file at start.
- The `x-tollgate-session` request header for session budgets.
- The configuration fields `store`, `usage`, `pricing`, `reservation`, `tenants[].limits` and `tenants[].budgets`.
- The rate limits and budgets guide, with the tolerance of the rate limits and the worst-case overspend of a budget, and decision records ADR 0018, ADR 0019 and ADR 0020.
- Tests with three Tollgate copies that share one limit and one budget, a restart test with the real binary, and SDK tests for the 429 and 402 errors.
- The state of the limit store in the body of `/readyz`. `/readyz` does not fail when Redis does not answer (ADR 0020).
- The tokens that Anthropic wrote to its 1-hour prompt cache, in the usage of each response.
- The passthrough proxy for Anthropic `/v1/messages` and OpenAI `/v1/chat/completions`, with streaming and without streaming.
- Provider keys from environment variables or files, virtual keys from YAML, and model permissions for each tenant.
- The SSE parser and tee relay, with byte-exact golden tests and usage extraction for both API formats.
- Request IDs, JSON access logs with token usage, and `/healthz` and `/readyz` endpoints.
- Connect, first-token, idle and total timeouts, upstream cancellation on client disconnect, and graceful drain on `SIGTERM`.
- The `tollgate serve`, `tollgate init`, `tollgate keys create` and `tollgate config validate` commands.
- Configuration and operations guides, a Claude Code example, and decision records ADR 0016 and ADR 0017.
- SDK tests through the gateway, a `SIGTERM` subprocess test, fuzz tests, and a target to record provider responses.
- The `tollgate version` command. It shows the version, the commit, and the Go version of the binary.
- The `fakeprovider` test server. It speaks the Anthropic Messages API and the OpenAI Chat Completions API wire formats, with streaming and without streaming.
- Tests that use the official Anthropic and OpenAI Go SDKs to read the output of the fake provider.
- The decision records ADR 0001 to ADR 0015, and the glossary.
- The build, test, and lint targets in the `Makefile`, and the CI workflow.
- The contribution guide, the security policy, the code of conduct, and the issue and pull request templates.

[Unreleased]: https://github.com/ykakade/tollgate/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/ykakade/tollgate/releases/tag/v0.1.0
