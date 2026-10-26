<p align="center">
  <img src="docs/assets/logo.svg" width="88" height="88" alt="Tollgate logo">
</p>
<h1 align="center">Tollgate</h1>
<p align="center"><strong>Cost controls and reliable model access for coding agents.</strong></p>
<p align="center">One Go binary. Native Anthropic and OpenAI APIs. An embedded dashboard.</p>

<p align="center">
  <a href="https://github.com/ykakade/tollgate/actions/workflows/ci.yml"><img src="https://github.com/ykakade/tollgate/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/ykakade/tollgate/releases/latest"><img src="https://img.shields.io/github/v/release/ykakade/tollgate" alt="Latest release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue" alt="Apache-2.0 license"></a>
</p>

<p align="center">
  <a href="docs/README.md"><strong>Documentation</strong></a> ·
  <a href="#quick-start">Quick start</a> ·
  <a href="docs/assets/tollgate.mp4">Watch the demo</a> ·
  <a href="docs/adr/README.md">Design decisions</a>
</p>

<a href="docs/assets/tollgate.mp4"><img src="docs/assets/tollgate.webp" width="100%" alt="Tollgate feature tour: request controls, provider failover, circuit breakers, budgets, agent sessions, traces, and the embedded dashboard."></a>

<p align="center"><a href="docs/assets/tollgate.mp4">Watch the full 81-second tour in 1080p</a> · <a href="docs/video/README.md">Video source and reproduction</a></p>

Tollgate sits between your agents and model providers. It checks keys, reserves budgets, applies rate limits, and routes requests to healthy targets. Each request records token usage and cost.

Use it when several agents share an API account, a task needs its own budget, or provider failures interrupt work.

> [!NOTE]
> **Alpha software.** The configuration format can change before v1.0. See [budget tolerances](docs/limits.md#reserve-and-settle) before deployment.

## Features

| Capability | What you get |
| --- | --- |
| **Cost controls** | Tenant, monthly, and session budgets. Hard budgets refuse requests with HTTP 402. Optional stream cutoffs reduce overspend. |
| **Provider resilience** | Ordered fallbacks, bounded retries, circuit breakers, and separate connect, first-token, idle, and total timeouts. |
| **Agent sessions** | Explicit or derived session IDs, spend projections, and sticky routing that helps preserve warm provider caches. |
| **Traffic controls** | Request and token limits per tenant. Multiple gateway copies share atomic counters through Redis. |
| **Privacy and caching** | Opt-in personal data redaction, response placeholder restoration, and an exact response cache scoped by tenant. |
| **Native APIs** | Anthropic `/v1/messages` and OpenAI `/v1/chat/completions`, with streaming. Anthropic, OpenAI, Vertex AI, and Ollama targets. |
| **Built-in visibility** | A React dashboard, JSONL cost records, Prometheus metrics, and OpenTelemetry traces for each upstream attempt. |

Streams pass through byte for byte unless redaction or a budget cutoff changes them. Fallbacks apply before the first response byte. See the [agent guide](docs/agent-features.md) and [reliability guide](docs/reliability.md) for exact behavior.

## How it works

```text
Claude Code / SDK / agent
          │  Tollgate key + native API request
          ▼
       Tollgate
       ├── Authenticate → inspect → reserve limits and budget
       ├── Check response cache → route → stream → settle cost
       └── Dashboard · usage records · metrics · traces
          │
          ▼
Anthropic / OpenAI / Vertex AI / Ollama
```

Use the memory store for one process, or Redis for shared limits and budgets. The dashboard ships inside the binary; Node.js is needed only to rebuild its assets.

## Quick start

**Goal:** run the dashboard with synthetic traffic, without a provider key.

**Prerequisites:** Git, GNU Make, Node.js with npm, and the Go version in [`go.mod`](go.mod).

1. Clone the repository:

   ```sh
   git clone https://github.com/ykakade/tollgate.git
   cd tollgate
   ```

2. Start the demo:

   ```sh
   make dashboard-demo
   ```

3. Open `http://127.0.0.1:9090`. Explore Overview, Sessions, Providers, and Keys.

**Result:** the dashboard shows spend, session budgets, cache use, fallbacks, and provider health. Stop the demo with `Ctrl-C`.

If a port is busy, stop the process that uses it. For a first API request, follow the [quick start guide](docs/quickstart.md#send-a-first-request).

### Install the gateway

Install with Go:

```sh
go install github.com/ykakade/tollgate/cmd/tollgate@latest
```

Or download a Linux binary for amd64 or arm64 from [Releases](https://github.com/ykakade/tollgate/releases/latest). Archives include checksums, a signature, and build provenance. The [installation guide](docs/quickstart.md#install-a-release-binary) covers verification and source builds.

### Connect Claude Code

Follow the [Claude Code example](examples/claude-code/README.md) to configure a provider and create a Tollgate key. Then point the client at the gateway:

```sh
export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
export ANTHROPIC_AUTH_TOKEN='YOUR_TOLLGATE_KEY'
claude
```

The client uses a Tollgate key. The gateway reads provider credentials from the environment or a file.

## Dashboard

Inspect cost and behavior without another service. Select a tenant and time range, then open a session to see its budget and request history.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/dashboard-overview-dark.png">
  <img alt="Tollgate overview: spend, cache use, request totals, hourly charts, and route costs" src="docs/assets/dashboard-overview-light.png">
</picture>

<details>
<summary><strong>Explore agent sessions and provider health</strong></summary>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/dashboard-session-dark.png">
  <img alt="Session detail with remaining budget, spend projection, and request timeline" src="docs/assets/dashboard-session-light.png">
</picture>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/dashboard-providers-dark.png">
  <img alt="Provider circuit breaker states and time to first token" src="docs/assets/dashboard-providers-light.png">
</picture>

</details>

All screenshots use synthetic demo data. Reproduce them with `make dashboard-demo`. See the [admin API and dashboard guide](docs/admin-api.md).

## Engineering

- **Streaming correctness:** golden tests check byte-exact provider streams. Client disconnects cancel upstream work; graceful shutdown drains active streams.
- **Shared-state checks:** cluster tests exercise limits and budget overspend with three gateway copies and real Redis.
- **Protocol checks:** integration tests use the official Anthropic and OpenAI SDKs. Fuzz targets cover SSE, request preflight, configuration, and redaction.
- **Documented tradeoffs:** [33 architecture decision records](docs/adr/README.md) explain the store, retries, budgets, sessions, cache, and dashboard.
- **Build checks:** CI runs the race detector, static analysis, dependency scans, and demo-stack checks. Releases include signatures and provenance.

The video shows **89 µs of gateway overhead for one local demo request**. This is an example, not a workload benchmark. See [observability](docs/observability.md) to measure your deployment.

## Documentation

Start at the [documentation home](docs/README.md), or go directly to a guide:

| Guide | Covers |
| --- | --- |
| [Quick start](docs/quickstart.md) | Installation, a first request, Claude Code, and demo commands |
| [Configuration](docs/configuration.md) | Providers, routes, tenants, keys, and every YAML field |
| [Limits and budgets](docs/limits.md) | Reservations, Redis behavior, and overspend bounds |
| [Reliability](docs/reliability.md) | Fallback rules, retries, circuit breakers, and timeouts |
| [Agent features](docs/agent-features.md) | Sessions, sticky routing, caching, redaction, and reports |
| [Admin API](docs/admin-api.md) | Dashboard access, endpoints, authentication, and data limits |
| [Observability](docs/observability.md) | Prometheus, OpenTelemetry, Grafana, and the Docker demo |
| [Operations](docs/operations.md) | Health checks, logs, shutdown, and deployment |

The searchable documentation site is in `docs/`. Run `make docs-serve` to read it locally, or `make docs` to build it.

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md) for the development workflow and required checks. Report bugs and propose features in [Issues](https://github.com/ykakade/tollgate/issues).

For security issues, use [private vulnerability reporting](https://github.com/ykakade/tollgate/security/advisories/new). See [SECURITY.md](SECURITY.md).

Licensed under [Apache-2.0](LICENSE).
