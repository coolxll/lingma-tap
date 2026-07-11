# Lingma Large Request Failure Analysis

Date: 2026-07-11

## Symptom

Claude Code sent a large Anthropic Messages request through the local gateway. The client only showed `API Error`, while Lingma Tap recorded upstream failures.

Observed gateway log shape:

- Model: `gm51model`
- Agent: `agent_chat`
- Reasoning: `true`
- Request size: about 278-287 KB
- Messages: 17
- Tools: 49
- Tool history: 12
- Tools schema size: about 153 KB
- Message payload size: about 126 KB

Recent failures included:

- Fast upstream SSE error: `418 Unknown sse issue`
- Delayed stream close: `lingma upstream connection closed before [DONE]`
- Earlier HTTP/2 transport symptom: `INTERNAL_ERROR`

After disabling Lingma HTTP/2, upstream requests used `HTTP/1.1`, but the same request still failed. This means HTTP/2 was a symptom/amplifier, not the root cause for this captured failure.

## Direct Replay

A diagnostic command was added:

```powershell
go run ./cmd/replay-upstream -id 463 -mode both -timeout 75s -max-events 8 -max-preview 700
```

It reads a captured `gateway_logs.request_body` from SQLite, signs it with local Lingma auth, and sends it directly to `lingma-api.tongyi.aliyun.com`, bypassing the local gateway.

Key replay results:

- Original request, `agent_chat + reasoning=true`: upstream returned HTTP `200` over `HTTP/1.1`, then SSE body `{"code":"418","message":"Unknown sse issue"}`, then closed around 62-65 seconds without `[DONE]`.
- Fallback request, `agent_common + reasoning=false`: upstream returned `406 Session blocked, Please clear context try again`.
- `agent_chat + reasoning=false` before repairing tool order exposed a stricter upstream error: an assistant `tool_calls` message was not immediately followed by matching `tool` messages.
- After repairing tool order, `agent_chat + reasoning=false` no longer produced that invalid-parameter error, but still returned `418 Unknown sse issue`.

## Root Causes

There are two separate issues.

1. Anthropic-to-OpenAI tool history ordering bug

   Claude Code can send a single Anthropic `user` message containing both `tool_result` blocks and normal text. The previous conversion emitted normal `user` text before `tool` results. That can produce this invalid OpenAI/Lingma sequence:

   ```text
   assistant tool_calls
   user text
   tool result
   ```

   Lingma's non-reasoning path rejects this shape because tool results must immediately answer the preceding assistant `tool_calls`.

   Fix: emit converted `tool` messages before normal `user` text for the same Anthropic user content block.

2. Upstream rejection of a specific tool turn

   The failure is not explained by context size alone. A 283 KB request with 49 tools and `reasoning=true` replayed successfully. Adding one more assistant `Read` tool call and its tool result made the request fail as `418 Unknown sse issue`.

   In the captured request, that last tool turn read `tag_policy.py`. The tool result contained simplified-Chinese/Japanese adult tag vocabulary. Replacing only the tool result was not enough. Replacing only the assistant tool-call arguments was not enough. Replacing both the assistant tool-call arguments and the matching tool result with safe placeholders made the full 17-message, 49-tool, `agent_chat + reasoning=true` request pass.

   Conclusion: closing reasoning is not required and is not sufficient. The reliable mitigation for this class is to preserve the tool-call structure but redact the rejected tool turn before retrying.

## Changes Made

- Added `cmd/replay-upstream` to replay captured SQLite gateway requests directly against Lingma upstream.
- Fixed Anthropic message conversion so `tool_result` messages stay directly after assistant `tool_calls`.
- Added a regression test for mixed `tool_result` plus text in the same Anthropic user message.
- Broadened large-thinking detection to include large requests with many tool schemas, not only long tool history.
- Changed the 418 retry path to keep `agent_chat + reasoning=true` when possible and redact the latest matching assistant tool-call arguments plus tool result before retrying.

## Current Mitigation Guidance

For generic large-request failures, reducing request size and complexity still helps:

- Send only tools relevant to the current task instead of the full Claude Code tool schema set.
- Summarize or truncate old tool results, especially large file/search outputs.
- Start a fresh session after several tool-heavy turns.
- Avoid reasoning mode for large tool-heavy requests where the upstream model supports a non-reasoning path, but do not rely on this as the primary fix. This captured request passed with reasoning enabled after the rejected tool turn was redacted.

For `418 Unknown sse issue` before any output, retrying once with the latest tool turn redacted is a useful low-impact probe and fixed the first captured request. It did not reliably fix later requests after additional tool turns were added, so it should not be treated as a general guarantee.

## Upstream E2E Exploration Matrix

`internal/bridge/upstream_replay_e2e_test.go` provides an opt-in test that reads a captured `gateway_logs` request and sends controlled variants to the real Lingma upstream. Normal tests and CI do not execute it.

Required environment variables:

- `LINGMA_E2E_CONFIRM=1` explicitly permits real upstream calls.
- `LINGMA_REPLAY_ID` selects the captured `gateway_logs` row.
- `LINGMA_REPLAY_VARIANTS` optionally selects comma-separated variants. The default is all variants.
- `LINGMA_REPLAY_TIMEOUT` optionally sets a per-variant timeout, for example `45s`.
- `LINGMA_REPLAY_DB` and `LINGMA_REPLAY_AUTH_DIR` optionally override local paths.

Example:

```powershell
$env:LINGMA_E2E_CONFIRM = '1'
$env:LINGMA_REPLAY_ID = '473'
$env:LINGMA_REPLAY_TIMEOUT = '45s'
$env:LINGMA_REPLAY_VARIANTS = 'original_http1,reasoning_off_keep_agent,latest_2_tool_turns_redacted'
go test -tags integration -run '^TestIntegration_ReplayCapturedRequestMatrix$' -count=1 -v ./internal/bridge
```

Each subtest logs one `LINGMA_E2E_RESULT` JSON object. Upstream rejection is recorded in that object rather than failing the Go subtest, so a complete matrix can finish and be compared.

### Matrix Result for gateway_logs id 473

| Variant | Result |
| --- | --- |
| Original HTTP/1.1 | 418 |
| Original HTTP/2 | 418 |
| Reasoning disabled, agent preserved | 418 |
| `agent_common` plus non-reasoning | 406 session blocked |
| Latest one complete tool turn redacted | 418 |
| Latest two complete tool turns redacted | Success |
| Latest three complete tool turns redacted | Success |
| Latest two tool-call arguments only redacted | 418 |
| Latest two tool results only redacted | 418 |
| All tool-call arguments redacted | 418 |
| All tool results truncated to 4 KiB | 418 |
| All tool results redacted | Success |
| All complete tool turns redacted | Success |
| All tool descriptions removed | 418 |
| All current tool definitions removed | 418 |

These results do not support a simple context-size, HTTP/2, reasoning, agent, or tool-schema explanation. They indicate that Lingma evaluates combinations across multiple historical tool turns. Complete-turn redaction of the latest two turns can pass even when changing only the argument side or only the result side does not. All-result redaction also passes, which indicates older tool results can remain relevant after later turns are added.

The matrix is intended for repeated observation. Results may vary with upstream deployment and account policy, so production behavior should not be changed solely from one captured request.

## SQLite History Survey

A scan of 242 `gateway_logs` rows with captured request bodies found:

- 143 successes
- 43 HTTP/2-style stream resets
- 33 early EOF / missing `[DONE]` failures
- 3 explicit `418 Unknown sse issue` rows
- 16 client cancellations
- 4 incomplete rows

Most failures were repeated retries of a small number of exact request fingerprints. For example, one body was retried 12 times with the same reset result, and another body was retried 10-12 times with early EOF. Raw failure counts therefore overstate the number of distinct failure shapes.

Request size is a risk factor but not a deterministic boundary:

- Requests below 128 KiB were almost always successful.
- Requests with a maximum tool result between 16 and 64 KiB failed about two thirds of the time in this history.
- Requests with a maximum tool result between 64 and 128 KiB also failed frequently.
- However, requests around 435 KiB and 135 messages succeeded, while smaller neighboring requests failed.

Several conversation boundaries show that an exact body can fail repeatedly and then a slightly extended body can succeed:

- `id=373-395`: the same 237 KiB body reset 12 times; `id=397`, with two additional messages, succeeded.
- `id=213-219`: the same 291 KiB body reset three times; `id=221`, with one additional message, succeeded.
- `id=445-468`: the `tag_policy.py` turn failed repeatedly; `id=471` succeeded after the bridge retry changed the latest tool turn.

The same tool-result hashes occur in both successful and failed requests. This argues against a single file, tool name, or fixed keyword being the complete explanation.

## Cross-Fingerprint E2E Results

Representative requests from different historical failure clusters were replayed with the E2E matrix.

### id 473: current Claude Code 418 cluster

- Original HTTP/1.1 and HTTP/2: 418 before first payload.
- Reasoning disabled: still 418.
- Tool definitions removed: still 418.
- Latest one tool turn redacted: still 418.
- Latest two or three complete tool turns redacted: success.
- All tool-call arguments redacted: still 418.
- All tool results redacted: success.
- Latest two arguments only or latest two results only: still 418.

### id 395: older gm51model stream-reset cluster

- Original now reproduces as 418 over both HTTP/1.1 and HTTP/2.
- Reasoning disabled while retaining `agent_chat`: success.
- Latest two complete tool turns redacted: success.
- All tool results redacted: success.
- Current tool definitions removed: success.

### id 429: later request in the same older conversation

- Original: 418.
- Reasoning disabled: `provider_error`, consistent with invalid historical tool ordering.
- Latest two complete tool turns redacted: success.
- All tool results redacted: success.
- Current tool definitions removed: success.

### id 319: 438 KiB / 112 tool-history request

- Original began emitting reasoning after about five seconds and continued for the full 60-second test timeout.
- It produced 1,588 reasoning events before the test canceled it.
- Other variants also streamed substantial output without reaching `[DONE]` during the timeout.
- This is a long-generation/client-timeout class, not a before-first-token 418 class.

### id 127: kmodel early-EOF cluster

- Original now returns `406 Session blocked` over both HTTP/1.1 and HTTP/2.
- Redacting the latest two complete tool turns did not help.
- Removing current tool definitions did not help.
- Redacting all historical tool results succeeded.

## Current Interpretation

The history contains at least three distinct failure classes:

1. Before-first-payload upstream rejection, commonly surfaced as 418 or 406. Historical tool results are the strongest shared factor, but the minimum successful transformation differs by request and model.
2. Invalid tool-history ordering, which may be hidden by reasoning mode and surface as `provider_error` after reasoning is disabled.
3. Long-running generation that emits many events but does not complete before the client or test timeout.

HTTP/2 changes how some failures are transported but does not explain the semantic rejection. Closing reasoning helps some fingerprints and fails others. Removing tool definitions also helps some fingerprints and fails others. The only transformation that succeeded across all replayed rejection clusters was redacting all historical tool results, but that is too destructive to adopt as an automatic production rule without a clearer product decision.
