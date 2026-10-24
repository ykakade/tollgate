# ADR 0002: Pass request bodies through without changes

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none

## Context

Clients such as Claude Code and Cline use the native Anthropic and OpenAI formats.
These formats contain fields that agents need: prompt cache markers, thinking blocks, and tool-use details.
A translation layer between formats can lose these fields or change their meaning.

## Decision

Tollgate accepts `/v1/messages` and `/v1/chat/completions` in their native formats.
It sends the original request bytes to the provider. It does not decode and encode the body again.
Tollgate does not translate between formats by default.

## Alternatives

1. Translate all requests to one internal format, as LiteLLM does. This lets one route use providers with different formats, but it loses provider features.
2. Translate only for a fallback to a provider with a different format. This is out of scope until after v2.

## Consequences

- A fallback target must use the same API format as the route.
- New provider fields work at once, with no change to Tollgate.
- Tollgate decodes only the few fields that it needs, so the overhead stays low for large bodies.
