# ADR 0003: Estimate input tokens from the body size

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none

## Context

Tollgate must estimate the cost of a request before it sends it, to check rate limits and budgets.
An exact count needs a tokenizer. Anthropic does not publish a local tokenizer, and a tokenizer for 150k tokens uses much CPU time on each request.

## Decision

Estimate input tokens from the number of body bytes, with one ratio for each provider.
After the response, replace the estimate with the usage that the provider reports.
If the provider reports no usage, keep the estimate and set `usage_estimated` to true.

## Alternatives

1. A local tokenizer such as tiktoken-go. This is exact only for some OpenAI models, and it adds CPU time and a dependency.
2. The Anthropic `count_tokens` endpoint. This adds one network call to each request.

## Consequences

- The estimate can be wrong. The settle step corrects the spend after each request.
- Rate limits on tokens are approximate before the response.
- If measurements show that the estimate is not sufficient, a new ADR can add a tokenizer.
