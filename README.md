<p align="center"><img src="docs/assets/logo.svg" width="64" height="64" alt="Tollgate logo"></p>

# Tollgate

A lightweight AI gateway for coding agents.
One Go binary that controls cost, rate limits, and failover for the Anthropic and OpenAI APIs.

[![CI](https://github.com/ykakade/tollgate/actions/workflows/ci.yml/badge.svg)](https://github.com/ykakade/tollgate/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/ykakade/tollgate)](https://github.com/ykakade/tollgate/releases/latest)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

> [!NOTE]
> Status: alpha. The configuration format can change before v1.0.

<a href="docs/assets/tollgate.mp4"><img src="docs/assets/tollgate.webp" width="100%" alt="Tollgate in 37 seconds. The primary provider fails with status 529 and Tollgate sends the request to the backup with 89 microseconds of overhead. A hard budget refuses a request with status 402. The dashboard shows spend and fallbacks."></a>

<p align="center"><a href="docs/assets/tollgate.mp4">Watch in 1080p (MP4)</a> · <a href="#a-real-run">See a real run</a> · <a href="docs/video/">How the video is made</a></p>

## What Tollgate does

- **Tracks agent sessions.** Session budgets, approximate stream cutoffs, sticky provider selection, cache cost reports, an opt-in response cache and personal data redaction. See [agent features](docs/agent-features.md).
- **Includes a dashboard.** Overview, sessions, providers and keys, in light and dark mode, served by the same binary. See [admin API and dashboard](docs/admin-api.md).
- **Recovers from provider failures.** Ordered targets, retries, a retry budget, and circuit breakers apply before the first byte. See [reliability](docs/reliability.md).
- **Supports cloud and local targets.** Use Claude on Vertex AI or local Ollama models with the same API format.

- **Controls cost.** Set a budget for each tenant, for each month and for each agent session. When a hard budget has no money left, Tollgate refuses the request with status 402. See [rate limits and budgets](docs/limits.md).
- **Limits traffic.** Set limits for requests and tokens per minute. The limits stay correct when you run more than one Tollgate copy with Redis.
- **Records the cost of each request.** Tollgate calculates the cost from a price table, and writes one usage event for each request to a JSONL file. A restart keeps the spend of the month.
- **Uses native API formats.** Anthropic `/v1/messages` and OpenAI `/v1/chat/completions` retain their provider format. Without opt-in redaction or a budget cutoff, streams pass through byte for byte.
- **Uses its own keys.** Clients send a Tollgate key. Tollgate stores only the hash of the key, and sends the provider key from the environment.
- **Records the tokens of each request.** Tollgate reads the usage from each response, with cache tokens, and writes one JSON log line for each request.
- **Stops slow providers.** Each route has a connect, first-token, idle and total timeout.
- **Does not cut streams at shutdown.** On `SIGTERM`, Tollgate refuses new requests and lets open streams finish.
- **Stops upstream requests that nobody reads.** If the client disconnects, Tollgate cancels the request to the provider.
- **Shows what happens.** Prometheus metrics, OpenTelemetry traces with a span for each upstream attempt, and a Grafana dashboard. See [observability](docs/observability.md).

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/dashboard-overview-dark.png">
  <img alt="Tollgate dashboard with synthetic demo data" src="docs/assets/dashboard-overview-light.png">
</picture>

The screenshot uses synthetic demo data. Run `make dashboard-demo` to reproduce the demo.

## A real run

Claude Code sends a request through Tollgate. The primary provider fails, Tollgate sends the request to the backup, and the dashboard shows the spend.

![Claude Code sends a request through Tollgate. The primary provider fails, Tollgate sends the request to the backup, and the dashboard shows the spend.](docs/assets/demo.gif)

The demo uses fake providers. Run `make demo-gif` to record it again. See [docs/demo](docs/demo/).

## Install

Release binaries are available for Linux on amd64 and arm64. Each archive has `tollgate` and the `fakeprovider` test server.

1. Download and extract the archive for your CPU:

   ```sh
   VERSION=0.1.0
   ARCH=amd64 # or arm64
   curl -fsSLO "https://github.com/ykakade/tollgate/releases/download/v${VERSION}/tollgate_${VERSION}_linux_${ARCH}.tar.gz"
   curl -fsSLO "https://github.com/ykakade/tollgate/releases/download/v${VERSION}/checksums.txt"
   sha256sum --ignore-missing -c checksums.txt
   tar -xzf "tollgate_${VERSION}_linux_${ARCH}.tar.gz" tollgate fakeprovider
   ```

2. Optional: verify the build provenance with the GitHub CLI:

   ```sh
   gh attestation verify "tollgate_${VERSION}_linux_${ARCH}.tar.gz" --repo ykakade/tollgate
   ```

3. Optional: verify the signature of the checksum file with [cosign](https://docs.sigstore.dev/cosign/system_config/installation/):

   ```sh
   curl -fsSLO "https://github.com/ykakade/tollgate/releases/download/v${VERSION}/checksums.txt.sigstore.json"
   cosign verify-blob checksums.txt \
     --bundle checksums.txt.sigstore.json \
     --certificate-identity "https://github.com/ykakade/tollgate/.github/workflows/release.yml@refs/tags/v${VERSION}" \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com
   ```

4. Show the version:

   ```sh
   ./tollgate version
   ```

Result: Tollgate shows its version, its commit, and the Go version. The procedures below use `./bin/tollgate`. With a release archive, use `./tollgate` and `./fakeprovider`.

On macOS or Windows, install with Go: `go install github.com/ykakade/tollgate/cmd/tollgate@latest`. You can also build from source.

## Build from source

Prerequisites:

- Go (the version in `go.mod`)
- GNU Make

1. Clone the repository:

   ```sh
   git clone https://github.com/ykakade/tollgate.git
   cd tollgate
   ```

2. Build the binaries:

   ```sh
   make build
   ```

3. Show the version:

   ```sh
   ./bin/tollgate version
   ```

Result: Tollgate shows its version, its commit, and the Go version.

If the build fails, make sure that your Go version is the same as or later than the version in `go.mod`.

## Quick start with the fake provider

This procedure uses the fake provider, so you need no provider key.

1. Start the fake provider:

   ```sh
   ./bin/fakeprovider -listen 127.0.0.1:8090
   ```

2. In a different terminal, make a configuration file:

   ```sh
   ./bin/tollgate init > tollgate.yaml
   ```

3. In `tollgate.yaml`, set the `base_url` of the `anthropic` provider to `http://127.0.0.1:8090`.
4. Make a key:

   ```sh
   ./bin/tollgate keys create -id laptop
   ```

5. Add the two lines that the command shows to the `keys` list of the tenant `personal` in `tollgate.yaml`. Remove `[]` after `keys:`.
6. Start Tollgate. The fake provider accepts any provider key:

   ```sh
   ANTHROPIC_API_KEY=fake ./bin/tollgate serve -config tollgate.yaml
   ```

7. In a third terminal, send a streaming request with your Tollgate key:

   ```sh
   curl http://127.0.0.1:8080/v1/messages \
     -H "x-api-key: <your Tollgate key>" \
     -H "anthropic-version: 2023-06-01" \
     -H "content-type: application/json" \
     -d '{"model":"claude-sonnet-5-5","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"Say hello."}]}'
   ```

Result: Tollgate sends the event stream of the fake provider, with the text `Hello from the Tollgate fake provider.` Tollgate writes a log line with the tokens of the request.

If Tollgate does not start, run `./bin/tollgate config validate -config tollgate.yaml`. It shows each error with its line number.

To use Tollgate with Claude Code and the Anthropic API, see [examples/claude-code](examples/claude-code/).

## Demo with metrics and traces

If you have Docker Compose, run `make demo`. It starts Tollgate, fake providers, a load generator, Prometheus, Grafana and Jaeger.
Then open Grafana at `http://127.0.0.1:3000`. Run `make demo-down` to stop the stack. See [the demo stack](docs/observability.md#demo-stack).

## Commands

| Command | What it does |
| --- | --- |
| `tollgate serve` | Starts the gateway. |
| `tollgate init` | Shows a starter configuration file. |
| `tollgate keys create -id <name>` | Makes a key and shows its hash for the configuration file. |
| `tollgate config validate` | Checks the configuration file and shows all errors. |
| `tollgate version` | Shows the version. |

## Documentation

- [Configuration](docs/configuration.md): each field of the configuration file
- [Rate limits and budgets](docs/limits.md): limits, budgets, and their tolerances
- [Reliability](docs/reliability.md): fallback rules, breakers, cloud targets, and M3 acceptance tests
- [Agent features](docs/agent-features.md): sessions, sticky routing, the response cache, and redaction
- [Admin API and dashboard](docs/admin-api.md): the endpoints of the admin port and the dashboard
- [Operations](docs/operations.md): start, stop, health endpoints and logs
- [Observability](docs/observability.md): metrics, traces, the admin port, the dashboard and the demo stack
- [M1 validation](docs/m1-validation.md): acceptance checks and validation limits
- [Decision records](docs/adr/): the design decisions and the reasons for them
- [Glossary](docs/glossary.md): the technical words that the documents use

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md) before you open a pull request.

## Security

Do not report security problems in public issues. Use [private vulnerability reporting](https://github.com/ykakade/tollgate/security/advisories/new). See [SECURITY.md](SECURITY.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
