# ADR 0019: Rebuild the spend from the usage file at start

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none

## Context

In single-node mode, the limit store is in memory. A restart clears it.
If a restart resets the spend of a month budget, the budget does not control cost.
The brief says that single-node mode needs no database.

## Decision

Tollgate writes one usage event for each request to a JSONL file (`usage.path`). Each event has the cost of the request in micro-USD.
At start, if `store.kind` is `memory` and `usage.sink` is `jsonl`, Tollgate reads the file before it accepts requests:

- For each tenant, it adds the cost of the events in the current month (UTC) to the month budget counter.
- For each session with an event in the last 24 hours, it adds the cost of all events of the session to the session budget counter.

Tollgate skips lines that are not events, for example a partial last line after a crash.
With the Redis store, Tollgate does not read the file, because Redis keeps the spend.

## Alternatives

1. Write the counters to a separate state file. This is a second format to keep correct, and it can disagree with the usage file.
2. Use an embedded database such as SQLite. This adds a dependency and a cgo or a large pure-Go package.

## Consequences

- A restart keeps the spend of the month and of recent sessions. `TestRestartKeepsMonthSpend` checks it with the real binary.
- Tollgate reads the full file at each start. A large file makes the start slower. Rotation of the usage file is not in this version.
- Events that are in the queue when the process crashes are lost. Their spend is then not in the rebuilt counters. A normal stop writes all queued events.
- Reservations of requests that are open at a crash are not in the file, which is correct: those requests did not finish.
