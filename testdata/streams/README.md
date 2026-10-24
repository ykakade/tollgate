# Golden streams

This directory holds provider responses for the golden test in `internal/server/golden_test.go`.

The test sends each file through Tollgate. The client must get the same bytes, and the access log must show the usage in the `.usage.json` file of the same name. The test also sends each `.sse` file with CRLF line ends.

## Source of the files

The files in this directory were written by hand from the event formats in the Anthropic and OpenAI API documentation. They are not recorded from a real provider. The usage numbers in them are examples, not measurements.

| File | Content |
| --- | --- |
| `anthropic_text.sse` | Text, with the spaces in JSON that the documentation examples have |
| `anthropic_tool_use.sse` | Text and a `tool_use` block with `input_json_delta` events, with prompt cache tokens |
| `anthropic_thinking.sse` | A thinking block with a signature, then text. The last `message_delta` repeats the input usage. |
| `anthropic_error.sse` | An `error` event during the stream |
| `anthropic_message.json` | A response that is not a stream |
| `openai_text_usage.sse` | Text and the final usage chunk |
| `openai_tool_calls.sse` | Tool call chunks, with cached and reasoning tokens |
| `openai_completion.json` | A response that is not a stream |

## Record real responses

Prerequisites:

- An Anthropic API key, an OpenAI API key, or both.
- The name of a model of each provider. The Anthropic model must support extended thinking.

1. Set the variables:

   ```sh
   export ANTHROPIC_API_KEY=<your key>
   export RECORD_ANTHROPIC_MODEL=<model name>
   export OPENAI_API_KEY=<your key>
   export RECORD_OPENAI_MODEL=<model name>
   ```

2. Record the responses:

   ```sh
   make record
   ```

3. For each new file, read the usage in the file. Write it in the `.usage.json` file of the same name.
4. Change the "Source of the files" section of this page.
5. Run the golden test:

   ```sh
   go test ./internal/server -run TestGoldenStreams
   ```

Result: The golden test uses real provider responses. The recordings do not contain your keys, because keys are only in request headers.

`anthropic_error.sse` stays written by hand, because a provider error cannot be recorded on request.

If `make record` skips a provider, make sure that both variables of that provider are set.
