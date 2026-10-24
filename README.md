# Tollgate

A lightweight AI gateway for coding agents.
One Go binary that controls cost, rate limits, and failover for the Anthropic and OpenAI APIs.

[![CI](https://github.com/ykakade/tollgate/actions/workflows/ci.yml/badge.svg)](https://github.com/ykakade/tollgate/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

> [!NOTE]
> Status: alpha. Tollgate does not send requests to providers yet. The configuration format can change before v1.0.

## What is in this repository

- `tollgate`: the gateway binary. At this time, it has only the `version` command.
- `fakeprovider`: a test server. It speaks the wire formats of the Anthropic Messages API and the OpenAI Chat Completions API. It always returns the same reply, with streaming and without streaming.

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

## Use the fake provider

1. Start the fake provider:

   ```sh
   ./bin/fakeprovider -listen 127.0.0.1:8090
   ```

2. In a different terminal, send a streaming request:

   ```sh
   curl http://127.0.0.1:8090/v1/messages \
     -H "content-type: application/json" \
     -d '{"model":"claude-test","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"Say hello."}]}'
   ```

Result: The fake provider sends an Anthropic event stream with the text `Hello from the Tollgate fake provider.`

The fake provider also accepts OpenAI requests on `/v1/chat/completions`.

If `fakeprovider` cannot start, make sure that no other program uses port 8090. You can also use a different `-listen` address.

## Design

- [Decision records](docs/adr/): the design decisions and the reasons for them
- [Glossary](docs/glossary.md): the technical words that the documents use

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md) before you open a pull request.

## Security

Do not report security problems in public issues. Use [private vulnerability reporting](https://github.com/ykakade/tollgate/security/advisories/new). See [SECURITY.md](SECURITY.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
