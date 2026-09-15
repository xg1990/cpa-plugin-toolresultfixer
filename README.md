# cpa-plugin-toolresultfixer

A [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) request-interceptor plugin that keeps `tool_use`/`tool_result` pairing valid before a request reaches an upstream provider — implemented entirely in Go, with no embedded JS engine.

## What it does

Anthropic's Messages API requires every `tool_use` block to be paired with a `tool_result` in the very next message, and some providers additionally require `tool_result` blocks to appear in the same order their `tool_use` blocks were issued. Truncated history, interrupted turns, or out-of-order concurrent tool execution can violate either rule and trigger a hard 400 from upstream.

On `InterceptRequestAfterAuth`, this plugin runs only when the selected upstream format is `antigravity` and the requested model is exactly `claude-sonnet-4-6`. Requests for Sonnet 5, other providers, and other models pass through unchanged. For matching requests it:

1. **Merges trailing system reminders after tool results.** A `system` or `developer` reminder immediately after a user message containing tool results is folded into that user content so the upstream sees one valid turn.
2. **Merges consecutive same-role messages.** Adjacent user messages are combined, while assistant messages are combined only when the earlier assistant message has no tool calls.
3. **Backfills orphaned `tool_use` calls.** For every `tool_use` whose id has no matching `tool_result` in the immediately following message, it appends a synthetic `tool_result` (`is_error: true`, with an explanatory message). If no user message immediately follows, it inserts one.
4. **Reorders out-of-order `tool_result` blocks.** Within a user message, `tool_result` blocks are sorted to match the dispatch order of the `tool_use` blocks in the preceding assistant message.

If none of these passes changes a matching request, or if the request is outside the Antigravity Sonnet 4.6 scope, the plugin returns an empty `RequestInterceptResponse.Body`, so the original request bytes pass through completely untouched.

## Why not JS

This plugin replaces [`cpa-plugin-jshandler`](https://github.com/router-for-me/cpa-plugin-jshandler) running a similar fixup script. The JS engine embedded there (goja) has a known bug handling unpaired/invalid UTF-16 surrogate pairs during `JSON.parse`/value export, which can silently corrupt request body content (replacing it with U+FFFD) even when the script itself made no changes. Doing the same JSON manipulation directly in Go removes that risk: Go strings are UTF-8 end to end, and `encoding/json` has no equivalent surrogate-pair defect. Numbers are decoded with `json.Number` so large integers survive the decode/re-encode round trip without float64 precision loss.

## Building

```
make build            # builds ./toolresultfixer.<so|dylib|dll> for the host OS/arch
make build GOOS=linux GOARCH=amd64
```

## Testing

```
go test ./...
```

Tests cover: no-op pass-through when already paired, backfilling a missing `tool_result` among several, wrapping a string `content` field before backfill, inserting a new user message when none/an assistant message follows, reordering out-of-order results while preserving other content blocks, refusing to reorder when there's nothing reliable to sort by, byte-for-byte pass-through and correct round-tripping of Unicode/emoji content, and preservation of large integer literals across a forced rewrite.
