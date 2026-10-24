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

## Top level

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `version` | number | none | The version of the file format. Set it to `1`. |
| `server` | mapping | | The data plane listener. |
| `store` | mapping | | The store of the rate limit and budget counters. |
| `usage` | mapping | | The usage events. |
| `pricing` | mapping | | The price table. |
| `reservation` | mapping | | The size of budget reservations. |
| `providers` | mapping | | The upstream services, by name. |
| `routes` | list | | One route for each model name that clients send. |
| `tenants` | list | | The users or teams, with their keys. |

## server

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `listen` | text | `:8080` | The address of the data plane, as `host:port`. |
| `max_body_bytes` | size | `16MiB` | The largest request body that Tollgate accepts. The maximum value is `1GiB`. A larger body gets status 413. |
| `drain_timeout` | duration | `10m` | After `SIGTERM`, the longest time that open requests can continue. See [operations](operations.md). |

## store

The store keeps the rate limit and budget counters. See [rate limits and budgets](limits.md).

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `kind` | text | `memory` | `memory` for one Tollgate copy. `redis` for two or more copies that share their limits. |
| `redis_url` | reference | none | The URL of the Redis server, as `env:NAME` or `file:/path`. Use the format `redis://[user:password@]host:port/db`, or `rediss://` for TLS. Necessary if `kind` is `redis`. |

The URL is a reference, because it can contain a password. If the URL has no query, Tollgate uses a connect timeout of 1 s, a read and write timeout of 500 ms, and 1 retry. To use other values, add the go-redis options to the query, for example `?read_timeout=1s`.

This version connects to one Redis server. It does not support Redis Cluster or Sentinel.

## usage

Tollgate writes one usage event for each request that has a valid key and a model. The event has the tokens, the cost and the timing of the request. See [operations](operations.md#usage-file) for the format.

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `sink` | text | `jsonl` | `jsonl` to write the events to a file. `none` to write no events. |
| `path` | path | `./data/usage.jsonl` | The usage file. A relative path starts at the directory of the configuration file. Tollgate makes the file and its directory if they do not exist. |
| `queue_size` | number | `10000` | The number of events that can wait for the file. If the queue is full, Tollgate drops the event. The request does not wait. |

With `store.kind: memory`, Tollgate reads the usage file at start to rebuild the spend of the budgets. If `sink` is `none`, a restart sets the spend to 0.

## pricing

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `file` | path | the table in the binary | The price table. A relative path starts at the directory of the configuration file. |

The binary has a copy of [`pricing.yaml`](../pricing.yaml). That table has the Anthropic models that `tollgate init` adds. To change prices or to add models, copy `pricing.yaml`, change the copy, and set `file` to its path. When you set `file`, Tollgate uses only that file.

Each entry gives the prices of one model from a date:

```yaml
- provider: anthropic          # a provider name or a provider kind
  model: claude-sonnet-5-5     # the model of the target
  effective_from: 2026-10-24   # the first day of these prices
  micro_usd_per_mtok:          # micro-USD for each million tokens
    input: 2000000             # 2 USD
    output: 10000000
    cache_read: 100000
    cache_write: 2500000       # writes to the 5-minute cache
    cache_write_1h: 4000000    # writes to the 1-hour cache; if 0, cache_write
  long_context:                # optional
    above_prompt_tokens: 100000
    micro_usd_per_mtok: {input: 500000, output: 2500000}
```

- Tollgate finds the entry of a target with the provider name first, then with the provider kind. Use the provider name for a provider with other prices, for example a local Ollama with prices of `0`.
- Tollgate uses the entry with the latest `effective_from` that is not after the request. To change a price, add an entry with a new date. Do not change an old entry.
- If a prompt has more tokens than `above_prompt_tokens`, Tollgate uses the `long_context` prices for the full request. The prompt counts the input, cache read and cache write tokens.
- If a tenant has a budget, each target of its models must have a price. Else the configuration is not valid.
- If a target has no price and the tenant has no budget, the cost of its requests is 0 in the usage events.

## reservation

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `output_tokens` | number | `4096` | The largest number of output tokens that a request reserves against a budget. A request with a lower `max_tokens` reserves its `max_tokens`. The maximum value is 10,000,000. |

A larger value makes the [worst-case overspend](limits.md#worst-case-overspend) smaller. Then a budget refuses requests earlier. See [ADR 0018](adr/0018-reservation-size.md).

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
| `limits` | mapping | no limits | The rate limits of the tenant. |
| `budgets` | list | none | The budgets of the tenant. |

### limits

All keys of the tenant share the limits. See [rate limits](limits.md#rate-limits).

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `requests_per_minute` | number | `0` | The largest number of requests in one minute. `0` means no limit. |
| `tokens_per_minute` | number | `0` | The largest number of tokens in one minute: input, output, cache read and cache write tokens. `0` means no limit. |

### budgets

Each budget has a different window. See [budgets](limits.md#budgets).

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `window` | text | none | `month` for the calendar month in UTC. `session` for the requests with the same `x-tollgate-session` header. |
| `limit_usd` | amount | none | The largest spend in USD, with at most 6 decimal places, for example `50` or `0.25`. |
| `mode` | text | `hard` | `hard` refuses requests with status 402 when the budget has no money left. `soft` only writes a warning to the log. |

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

store:
  kind: memory

usage:
  path: ./data/usage.jsonl

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

pricing:
  file: ./pricing.yaml

tenants:
  - id: personal
    keys:
      - id: laptop
        hash: sha256:2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae
    models: [claude-sonnet-5-5, local-coder]
    limits:
      requests_per_minute: 120
      tokens_per_minute: 2000000
    budgets:
      - window: month
        limit_usd: 50
      - window: session
        limit_usd: 5
        mode: soft
```

The file `./pricing.yaml` of this example must have a price for `local/qwen2.5-coder`, because the tenant has a budget.
