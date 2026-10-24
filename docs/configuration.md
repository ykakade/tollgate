# Configuration

This page describes each field of the Tollgate configuration file.

Tollgate reads one YAML file. The default path is `tollgate.yaml`. To use a different path, give the `-config` flag to `tollgate serve` or `tollgate config validate`.

To make a starter file, run this command:

```sh
tollgate init > tollgate.yaml
```

## Rules for all fields

- A field that Tollgate does not know is an error. This stops typos.
- Use one YAML document in the file. Tollgate refuses additional documents.
- Durations use Go syntax, for example `500ms`, `30s` or `10m`.
- Sizes are a number of bytes, or a number with `KiB`, `MiB` or `GiB`, for example `16MiB`.
- If you omit a field that has a default, or set it to `0`, Tollgate uses the default.
- `tollgate config validate` shows all errors at once, each with its line number.

Some fields of the project brief, such as `store`, `usage`, `limits` and `budgets`, are not in this version. If the file has them, Tollgate shows an error.

## Top level

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `version` | number | none | The version of the file format. Set it to `1`. |
| `server` | mapping | | The data plane listener. |
| `providers` | mapping | | The upstream services, by name. |
| `routes` | list | | One route for each model name that clients send. |
| `tenants` | list | | The users or teams, with their keys. |

## server

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `listen` | text | `:8080` | The address of the data plane, as `host:port`. |
| `max_body_bytes` | size | `16MiB` | The largest request body that Tollgate accepts. The maximum value is `1GiB`. A larger body gets status 413. |
| `drain_timeout` | duration | `10m` | After `SIGTERM`, the longest time that open requests can continue. See [operations](operations.md). |

## providers

Each key of `providers` is a provider name. Use letters, digits, `.`, `_` and `-`, with 64 characters or fewer. The name shows in the `x-tollgate-provider` header and in the logs.

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `kind` | text | none | The wire format of the provider: `anthropic` or `openai`. Use `openai` for Ollama. |
| `base_url` | URL | `https://api.anthropic.com` for `anthropic`, `https://api.openai.com` for `openai` | The URL of the provider. Do not add `/v1`. Tollgate adds the request path, for example `/v1/messages`. |
| `api_key` | reference | none | The provider key, as `env:NAME` or `file:/path`. If you omit it, Tollgate sends no key, for example to a local Ollama. |

Do not put a provider key in the configuration file. Tollgate refuses keys in plain text, because the file can go into version control.

- `env:NAME` reads the environment variable `NAME`. If the variable is not set or is empty, the file is not valid.
- `file:/path` reads the file and removes the spaces and line ends at the start and the end. A relative path starts at the directory of the configuration file.

## routes

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `model` | text | none | The model name that clients send in the `model` field. |
| `api` | text | none | The API format of the route: `anthropic` for `/v1/messages`, `openai` for `/v1/chat/completions`. |
| `targets` | list | none | The provider and the model that get the request. This version supports one target. |
| `timeouts` | mapping | | The four timeouts of the upstream request. |

Two routes can have the same `model` only if they have a different `api`.

### targets

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `provider` | text | none | The name of a provider. The `kind` of the provider must be the same as the `api` of the route. |
| `model` | text | the `model` of the route | The model name that Tollgate sends to the provider. If it is different, Tollgate changes only the `model` value in the request body. |

### timeouts

[ADR 0016](adr/0016-timeouts.md) gives the details of each timeout. No timeout can be longer than `total`.

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `connect` | duration | `5s` | The longest time to get a connection to the provider. |
| `first_token` | duration | `30s` | The longest time from a ready connection to the first byte of the response body. For a request that does not stream, this includes the full time of the response. |
| `idle` | duration | `60s` | The longest time between two parts of the response body. It is also the longest time to write one part to the client. |
| `total` | duration | `15m` | The longest time for the full request. |

## tenants

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `id` | text | none | The name of the tenant. Use letters, digits, `.`, `_` and `-`, with 64 characters or fewer. |
| `keys` | list | empty | The virtual keys of the tenant. |
| `models` | list of text | none | The route model names that the keys of the tenant can use. Each name must have a route. |

### keys

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `id` | text | none | The name of the key, for example the name of a computer. It shows in the logs. |
| `hash` | text | none | The SHA-256 hash of the key, from `tollgate keys create`. |

To add a key, do these steps:

1. Make a key:

   ```sh
   tollgate keys create -id laptop
   ```

2. Copy the key to a safe location. Tollgate shows the key one time only.
3. Add the two lines that the command shows to the `keys` list of the tenant.
4. Restart Tollgate.

Result: Clients can send requests with the new key.

If Tollgate refuses the key, make sure that the hash in the file is the hash that the command showed.

## Example

```yaml
version: 1

server:
  listen: ":8080"
  max_body_bytes: 16MiB
  drain_timeout: 10m

providers:
  anthropic:
    kind: anthropic
    api_key: env:ANTHROPIC_API_KEY
  local:
    kind: openai
    base_url: http://localhost:11434

routes:
  - model: claude-sonnet-5-5
    api: anthropic
    targets:
      - provider: anthropic
    timeouts:
      first_token: 60s
  - model: local-coder
    api: openai
    targets:
      - provider: local
        model: qwen2.5-coder

tenants:
  - id: personal
    keys:
      - id: laptop
        hash: sha256:2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae
    models: [claude-sonnet-5-5, local-coder]
```
