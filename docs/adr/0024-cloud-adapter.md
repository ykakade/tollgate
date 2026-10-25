# ADR 0024: Use Vertex AI for the Claude cloud host

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none; extends ADR 0002 and ADR 0017

## Context

The brief requires one cloud host for Claude. Vertex AI sends Anthropic SSE, so it can reuse the existing meters.
Bedrock requires SigV4 and an AWS event-stream converter.

## Decision

Add the `vertex` provider kind with a Google Cloud project and region.
Use Application Default Credentials through the allowed `golang.org/x/oauth2/google` package.
Put the target model in the `rawPredict` or `streamRawPredict` URL.
Remove the top-level `model` member from the body, and insert `anthropic_version: vertex-2023-10-16` if absent.
Keep other bytes unchanged. Reuse the Anthropic relay and usage meters.
Skip Vertex targets for `/v1/messages/count_tokens`.
Require explicit Vertex prices for tenants with budgets. Do not infer cloud prices from direct Anthropic prices.

Add the `ollama` provider kind through the OpenAI adapter, with no key and a default URL of `http://localhost:11434`.
An Ollama target without a price entry costs 0.

## Alternatives

1. Implement Bedrock first. Its binary stream format needs more code and tests.
2. Use the Anthropic price table for Vertex. Prices can differ by host and region.

## Consequences

- Vertex startup requires Google credentials. Tests inject a static token and use the fake provider.
- No live cloud request is necessary for CI. Live authentication and billing remain unverified locally.
- Only Messages API calls use Vertex. Other Anthropic endpoints need a direct Anthropic target.

## Sources

- [Claude on Google Cloud](https://platform.claude.com/docs/en/build-with-claude/claude-models-on-google-cloud/google-managed)
- [Ollama OpenAI compatibility](https://docs.ollama.com/api/openai-compatibility)
