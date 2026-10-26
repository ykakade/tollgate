# Quick start

This page tells you how to install Tollgate, send a first request through it, and connect Claude Code.

## Install a release binary

Release binaries are available for Linux on amd64 and arm64. Each archive has `tollgate` and the `fakeprovider` test server.

Prerequisites: `curl`, `sha256sum` and `tar`.

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

Result: Tollgate shows its version, its commit, and the Go version.

If `sha256sum` reports a mismatch, download the archive again. Do not use an archive that fails the check.

The procedures below use `./bin/tollgate`. With a release archive, use `./tollgate` and `./fakeprovider`.

## Install with Go

On macOS or Windows, install with Go:

```sh
go install github.com/ykakade/tollgate/cmd/tollgate@latest
```

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

## Send a first request

Goal: send a request through Tollgate without a provider key.

Prerequisites: the binaries from one of the procedures above.

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

8. Open the dashboard at `http://127.0.0.1:9090`.

Result: Tollgate sends the event stream of the fake provider, with the text `Hello from the Tollgate fake provider.` Tollgate writes a log line with the tokens and the cost of the request. The dashboard shows the request.

If Tollgate does not start, run `./bin/tollgate config validate -config tollgate.yaml`. It shows each error with its line number.

## Connect Claude Code

Goal: send the requests of Claude Code through Tollgate.

Prerequisites: Tollgate, Claude Code and an Anthropic API key.

1. Copy [`examples/claude-code/tollgate.yaml`](../examples/claude-code/tollgate.yaml) to your working directory.
2. Make a key, and add the two lines that the command shows to the `keys` list:

   ```sh
   tollgate keys create -id laptop
   ```

3. Start Tollgate with your Anthropic API key:

   ```sh
   export ANTHROPIC_API_KEY='YOUR_ANTHROPIC_API_KEY'
   tollgate serve -config tollgate.yaml
   ```

4. In a different terminal, start Claude Code with the Tollgate address and the Tollgate key:

   ```sh
   export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
   export ANTHROPIC_AUTH_TOKEN='YOUR_TOLLGATE_KEY'
   claude
   ```

Result: Claude Code works as usual. Each request writes one log line and one usage event with its cost.

If Claude Code shows `The API key is not valid`, make sure that `ANTHROPIC_AUTH_TOKEN` is the key that starts with `tg_`. See the [Claude Code example](../examples/claude-code/README.md) for session budgets and fallbacks.

## Try the demo stack

If you have Docker Compose, run `make demo`. It starts Tollgate, fake providers, a load generator, Prometheus, Grafana and Jaeger.
Open Grafana at `http://127.0.0.1:3000`. Run `make demo-down` to stop the stack. See [the demo stack](observability.md#demo-stack).

To see the dashboard with synthetic data and no Docker, run `make dashboard-demo`.

## Commands

| Command | What it does |
| --- | --- |
| `tollgate serve` | Starts the gateway. |
| `tollgate init` | Shows a starter configuration file. |
| `tollgate keys create -id <name>` | Makes a key and shows its hash for the configuration file. |
| `tollgate config validate` | Checks the configuration file and shows all errors. |
| `tollgate report` | Shows cost and token totals from the usage file. |
| `tollgate version` | Shows the version. |

## Next steps

- [Configuration](configuration.md): each field of the configuration file.
- [Rate limits and budgets](limits.md): how Tollgate counts, and the tolerances.
- [Reliability](reliability.md): retries, fallbacks and circuit breakers.
