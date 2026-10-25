# Tollgate

A lightweight AI gateway for coding agents.
One Go binary that controls cost, rate limits, and failover for the Anthropic and OpenAI APIs.

[![CI](https://github.com/ykakade/tollgate/actions/workflows/ci.yml/badge.svg)](https://github.com/ykakade/tollgate/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

> [!NOTE]
> Status: alpha. The configuration format can change before v1.0.

## What Tollgate does

- **Recovers from provider failures.** Ordered targets, retries, a retry budget, and circuit breakers apply before the first byte. See [reliability](docs/reliability.md).
- **Supports cloud and local targets.** Use Claude on Vertex AI or local Ollama models with the same API format.

- **Controls cost.** Set a budget for each tenant, for each month and for each agent session. When a hard budget has no money left, Tollgate refuses the request with status 402. See [rate limits and budgets](docs/limits.md).
- **Limits traffic.** Set limits for requests and tokens per minute. The limits stay correct when you run more than one Tollgate copy with Redis.
- **Records the cost of each request.** Tollgate calculates the cost from a price table, and writes one usage event for each request to a JSONL file. A restart keeps the spend of the month.
- **Does not change requests.** Anthropic `/v1/messages` and OpenAI `/v1/chat/completions` requests go through in their native format. Streams go to the client byte for byte.
- **Uses its own keys.** Clients send a Tollgate key. Tollgate stores only the hash of the key, and sends the provider key from the environment.
- **Records the tokens of each request.** Tollgate reads the usage from each response, with cache tokens, and writes one JSON log line for each request.
- **Stops slow providers.** Each route has a connect, first-token, idle and total timeout.
- **Does not cut streams at shutdown.** On `SIGTERM`, Tollgate refuses new requests and lets open streams finish.
- **Stops upstream requests that nobody reads.** If the client disconnects, Tollgate cancels the request to the provider.
- **Shows what happens.** Prometheus metrics, OpenTelemetry traces with a span for each upstream attempt, and a Grafana dashboard. See [observability](docs/observability.md).

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
Then open the Tollgate dashboard at `http://127.0.0.1:3000`. Run `make demo-down` to stop the stack. See [the demo stack](docs/observability.md#demo-stack).

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
- [Reliability](docs/reliability.md): fallback rules, breakers, cloud targets, and M3 acceptance tests
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
