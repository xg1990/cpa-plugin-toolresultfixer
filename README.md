# cpa-plugin-toolresultfixer

A [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) request-interceptor plugin that keeps `tool_use`/`tool_result` pairing valid before a request reaches an upstream provider — implemented entirely in Go, with no embedded JS engine.

## What it does

Anthropic's Messages API requires every `tool_use` block to be paired with a `tool_result` in the very next message, and some providers additionally require `tool_result` blocks to appear in the same order their `tool_use` blocks were issued. Truncated history, interrupted turns, or out-of-order concurrent tool execution can violate either rule and trigger a hard 400 from upstream.

On `InterceptRequestBeforeAuth`, this plugin:

1. **Backfills orphaned `tool_use` calls.** For every `tool_use` whose id has no matching `tool_result` in the immediately following message, it appends a synthetic `tool_result` (`is_error: true`, with an explanatory message). If no user message immediately follows, it inserts one. This preserves the assistant's own reasoning/text history instead of dropping it, and lets the model react to the failure on its next turn.
2. **Reorders out-of-order `tool_result` blocks.** Within a user message, `tool_result` blocks are sorted to match the dispatch order of the `tool_use` blocks in the preceding assistant message.

If neither pass changes anything, the plugin returns an empty `RequestInterceptResponse.Body`, which by the CLIProxyAPI plugin ABI means the original request bytes pass through completely untouched — no parse/re-encode round trip at all.

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
