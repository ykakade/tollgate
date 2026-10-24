# ADR 0017: Edit request bodies in place, without a new encode

- Status: Accepted
- Date: 2026-10-24
- Supersedes: none

## Context

ADR 0002 says that Tollgate sends the original request bytes and does not decode and encode the body again.
Two features must change the body:

1. A route target can use a model name that is different from the name that the client sent.
2. ADR 0009 says that Tollgate adds `stream_options.include_usage` to OpenAI streams.

## Decision

Tollgate reads the tokens of the top-level JSON object and keeps the byte offsets of `model` and `stream_options`.
To make an edit, Tollgate replaces only the bytes of that value, or inserts a new member after the `{` of an object.
All other bytes stay the same.

The rules for `include_usage` are:

| `stream_options` in the request | Edit |
| --- | --- |
| Not present | Insert `"stream_options":{"include_usage":true}` |
| `null` | Replace it with `{"include_usage":true}` |
| An object without `include_usage` | Insert `"include_usage":true` |
| An object with `include_usage` that is not `true` | Replace the value with `true` |
| Another type | No edit. The provider refuses the request. |

Tollgate refuses a body with two members of the same name, because the provider can use a different member than Tollgate.

## Alternatives

1. Decode the full body into a map and encode it again. This changes the order and the format of the fields, and it uses much CPU time for large bodies.
2. Use a text search for `"model"`. This can find a field inside a message.

## Consequences

- The upstream body is the same as the client body, except for the edited values.
- A fuzz test checks that each edit gives valid JSON with the new values.
