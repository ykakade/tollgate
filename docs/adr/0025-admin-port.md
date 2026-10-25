# ADR 0025: Serve metrics and profiles on a separate admin port

- Status: Accepted
- Date: 2026-10-25
- Supersedes: none

## Context

Prometheus reads metrics with an HTTP request. Profiles from `net/http/pprof` show memory and stack contents.
Clients with a Tollgate key must not read either of them.
The brief puts `/metrics` on an admin port, by default `127.0.0.1:9090`, and requires a separate token when that port is not on localhost.

## Decision

Tollgate opens a second listener at `server.admin_listen`. It serves `GET /metrics` from a registry that each server owns.
The profiles are under `/debug/pprof/` only when `server.pprof` is `true`. The default is `false`.
When `admin_listen` is not a loopback address, `server.admin_token` is necessary. Each request must send it as a bearer token.
Tollgate compares SHA-256 hashes of the tokens in constant time.
The admin port stays open during a drain, so `/metrics` shows the open streams. It stops after the data plane.

Metric labels have bounded values. The `route` label is a route of the configuration, and `none` for a request with no route.
The `provider`, `from` and `to` labels come from the configuration too. Tenants are not labels; usage events have that detail.
The inter-token latency is one value for each stream: the time from the first to the last body byte, divided by the output tokens minus 1.

## Alternatives

1. Serve `/metrics` on the data plane. Every client could read the metrics, and a client error could not be told apart from a scrape.
2. Use the global Prometheus registry. Tests and more than one server in a process would share counters.
3. Always serve the profiles. A CPU profile costs CPU, and a heap profile can show data from requests.

## Consequences

- A deployment opens two ports. A container that Prometheus scrapes from outside needs an admin token.
- The profiles need an explicit configuration change.
- The admin API and the dashboard of later milestones use the same port and the same token.
