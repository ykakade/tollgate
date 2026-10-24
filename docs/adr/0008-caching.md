# ADR 0008: Use sticky routing and an exact cache, not a semantic cache

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none

## Context

Agent requests in one session share a long prefix. Providers keep a prompt cache for that prefix, which makes requests cheaper and faster.
If requests in one session go to different providers, the prompt cache is lost.
Agent requests rarely repeat exactly. A semantic cache can return a response for a different request, and an agent can then do the wrong action.

## Decision

Send all requests of one session to the same target while that target is healthy (sticky routing).
Provide an exact response cache. It is off by default, it is enabled for each route, and its key includes the tenant.
Do not provide a semantic cache.

## Alternatives

1. A semantic cache. A wrong hit can cause an agent to do the wrong action.
2. No cache and no sticky routing. Fallbacks and load spread then lose provider prompt caches.

## Consequences

- A fallback can lose the prompt cache. Cost reports show the cost of this loss.
- The exact cache helps only for repeated requests, such as tests and evaluations.
