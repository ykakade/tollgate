# Claude Code example

This example sends the requests of Claude Code through Tollgate.

Prerequisites:

- Tollgate. See [Build from source](../../docs/quickstart.md#build-from-source).
- Claude Code.
- An Anthropic API key.

1. Copy `tollgate.yaml` from this directory to your working directory.
2. Make a key for Claude Code:

   ```sh
   tollgate keys create -id laptop
   ```

3. Add the two lines that the command shows to the `keys` list in `tollgate.yaml`.
4. Set your Anthropic API key, and start Tollgate:

   ```sh
   export ANTHROPIC_API_KEY=<your Anthropic API key>
   tollgate serve -config tollgate.yaml
   ```

5. In a different terminal, start Claude Code with the Tollgate address and the Tollgate key:

   ```sh
   export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
   export ANTHROPIC_AUTH_TOKEN=<your Tollgate key>
   export ANTHROPIC_CUSTOM_HEADERS="x-tollgate-session: my-task"
   claude
   ```

   The third line is optional. It gives all requests from this shell one session ID, so that the session budget applies.

Result: Claude Code works as usual. Tollgate writes one log line for each request, with the tokens and the cost of the request. The file `data/usage.jsonl` gets one line for each request.

If the month budget or the session budget has no money left, Claude Code shows an error with the type `budget_exceeded`. To continue, increase `limit_usd`, or start a new session with a new value of `x-tollgate-session`. Then restart Tollgate if you changed the file.

If Claude Code shows `The key cannot use the model`, read the model name in the message. Add a route for that model, and add the model to the `models` list of the tenant. Then restart Tollgate.

If Claude Code shows `The API key is not valid`, make sure that `ANTHROPIC_AUTH_TOKEN` is the key that starts with `tg_`, not your Anthropic API key.

## Add a fallback

Goal: Continue a request through a second provider if the first provider fails before the response starts.

Prerequisites: Access to a second Anthropic-compatible provider, or a Google Cloud project with Claude access and Application Default Credentials.

1. Add the second provider to `providers`. For Vertex, use `kind: vertex`, `project`, and `region`; omit `api_key`.
2. Add it after the first target in each required route. Set `model` to the second provider's model ID.
3. Add prices for every fallback target to your price table, because this example has hard budgets. Set `pricing.file` to that table.
4. Run `tollgate config validate -config tollgate.yaml`.
5. Restart Tollgate.

Result: Tollgate tries the ordered targets before the first response byte. The response header `x-tollgate-provider` names the serving provider.
If no target can serve the request, read the upstream error or the 502 target list. See [reliability](../../docs/reliability.md).
