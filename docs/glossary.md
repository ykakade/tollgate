# Glossary

This page lists the technical words of Tollgate and the meaning of each word.

All Tollgate documents use these words with these meanings only. If a document needs a new technical word, add it to this page in the same pull request.

## Technical names

| Word | Meaning |
| --- | --- |
| adapter | The code that sends requests to one kind of provider, for example Anthropic or Vertex AI. |
| admin port | The second listener of Tollgate. It serves metrics, the admin API, and the dashboard. By default, it listens on `127.0.0.1:9090`. |
| ADR | Architecture decision record. One file in `docs/adr/` that records one design decision. |
| budget | The maximum spend for a tenant in a window, in USD. |
| cache read tokens | Input tokens that the provider reads from its prompt cache. They cost less than other input tokens. |
| cache write tokens | Input tokens that the provider writes to its prompt cache. |
| circuit breaker | A switch for each provider, model, and region. When too many requests fail, the breaker opens, and Tollgate sends no requests to that target for a period. |
| data plane | The listener that receives client requests. By default, it listens on `:8080`. |
| drain | The shutdown steps: Tollgate refuses new requests and lets open streams finish. |
| exact cache | A cache that returns a stored response only for a request with the same bytes and the same tenant. |
| fake provider | The test server in `cmd/fakeprovider`. It speaks the Anthropic and OpenAI wire formats. |
| fallback | To send a request to the next target after a failure. |
| first byte | The first byte of the response that Tollgate sends to the client. After the first byte, Tollgate does not fall back. |
| first-token timeout | The longest time that Tollgate waits for the first part of a response from a provider. |
| hard budget | A budget that stops requests when the spend reaches the limit. |
| micro-USD | One millionth of a US dollar. Tollgate stores all amounts in micro-USD. |
| preflight | The step that reads the model and the stream setting of a request and estimates its tokens and cost. |
| provider | A service that runs LLMs, for example Anthropic, OpenAI, Vertex AI, or Ollama. |
| rate limit | The maximum number of requests or tokens for a tenant in one minute. |
| relay | The step that sends the provider response to the client and reads a copy of it. |
| reservation | An estimated cost that Tollgate adds to a budget before it sends a request. |
| retry budget | The maximum ratio of retries to requests. |
| route | The configuration for one model name that clients send. It gives the API format and the list of targets. |
| session | A group of requests from one agent task. |
| settle | To replace a reservation with the real cost after the response. |
| soft budget | A budget that only gives a warning when the spend reaches the limit. |
| SSE | Server-sent events. The stream format that Anthropic and OpenAI use. |
| STE | ASD-STE100 Simplified Technical English. The writing rules for Tollgate documents. |
| sticky routing | To send all requests of one session to the same target. |
| target | One provider and one model in the list of a route. |
| tenant | A user or a team that has its own keys, limits, and budgets. |
| usage event | The record of one request: tokens, cost, timing, and status. |
| virtual key | A key that Tollgate gives to a client. It starts with `tg_`. It is not a provider key. |
| window | The period of a budget, for example a month or a session. |

## Word choices

Use the word in the first column. Do not use the words in the second column.

| Use | Do not use |
| --- | --- |
| make | create, generate (except in names of API endpoints and commands) |
| start | launch, spin up, kick off, boot |
| stop | kill, shut off |
| send | fire, dispatch, push |
| show | display, print out |
| find | look up, locate |
| use | utilize, leverage |
| fail | break, blow up |
| request | call (as a noun) |
