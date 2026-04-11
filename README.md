# claude-gateway

OpenAI-compatible HTTP gateway over the [Claude Code CLI](https://github.com/anthropics/claude-code).

## Why

The Anthropic API already has an HTTP interface, but it requires an API key and doesn't include Claude Code's agentic capabilities (file access, bash, tools). Claude Code CLI, on the other hand, uses your existing OAuth session and runs as a full agent — but has no HTTP API.

`claude-gateway` bridges that gap: it exposes a standard OpenAI-compatible API and translates each request into a `claude --print` subprocess call, so any OpenAI-compatible client works with Claude Code out of the box.

## Requirements

- [Claude Code](https://github.com/anthropics/claude-code) installed and authenticated (`claude auth login`)
- The `claude` binary on `PATH`, or pass it explicitly via `--claude-bin`

## Installation

Download the binary for your platform from [Releases](https://github.com/aa-blinov/claude-gateway/releases/latest):

| Platform | File |
|----------|------|
| Windows x64 | `claude-gateway-windows-amd64.exe` |
| Windows ARM64 | `claude-gateway-windows-arm64.exe` |
| Linux x64 | `claude-gateway-linux-amd64` |
| Linux ARM64 | `claude-gateway-linux-arm64` |
| macOS x64 | `claude-gateway-darwin-amd64` |
| macOS Apple Silicon | `claude-gateway-darwin-arm64` |

Or build from source (requires Go 1.22+):

```bash
go build -o claude-gateway .
```

## Usage

```bash
./claude-gateway --port 8000
./claude-gateway --port 8000 --no-tools        # pure LLM mode, no file/bash access
./claude-gateway --claude-bin /path/to/claude --port 8000
```

Point any OpenAI-compatible client at `http://127.0.0.1:8000` with any API key (not validated).

### Options

| Flag | Env var | Default | Description |
|------|---------|---------|-------------|
| `--host` | `CLAUDE_GATEWAY_HOST` | `127.0.0.1` | Listen address |
| `--port` | `CLAUDE_GATEWAY_PORT` | `8000` | Listen port |
| `--claude-bin` | `CLAUDE_GATEWAY_BIN` | `claude` | Path to claude binary |
| `--no-tools` | `CLAUDE_GATEWAY_NO_TOOLS` | false | Disable all Claude Code tools (pure LLM mode) |
| `--max-concurrency` | `CLAUDE_GATEWAY_MAX_CONCURRENCY` | `100` | Max parallel claude processes |
| `--timeout-seconds` | `CLAUDE_GATEWAY_TIMEOUT_SECONDS` | `600` | Per-request timeout |

## API

### `GET /v1/models`

Returns the built-in Claude model list. Override with `CLAUDE_GATEWAY_MODELS=model-a,model-b`.

### `POST /v1/chat/completions`

Standard OpenAI chat completions with optional streaming:

```bash
# Non-streaming
curl http://127.0.0.1:8000/v1/chat/completions \
  -H 'content-type: application/json' \
  -d '{
    "model": "claude-sonnet-4-6",
    "messages": [{"role": "user", "content": "Say hello"}]
  }'

# Streaming
curl http://127.0.0.1:8000/v1/chat/completions \
  -H 'content-type: application/json' \
  -d '{
    "model": "claude-sonnet-4-6",
    "stream": true,
    "messages": [{"role": "user", "content": "Count to 5"}]
  }'
```

### `POST /v1/responses`

Stateless subset of the OpenAI Responses API:

```bash
curl http://127.0.0.1:8000/v1/responses \
  -H 'content-type: application/json' \
  -d '{
    "model": "claude-sonnet-4-6",
    "instructions": "Reply briefly.",
    "input": "What is 2+2?"
  }'
```

### `GET /healthz`

Health check — returns `{"status":"ok"}`.

## Reasoning effort

Pass `reasoning_effort` (or `reasoning.effort`) to control how much thinking Claude does:

```json
{ "model": "claude-sonnet-4-6", "reasoning_effort": "max", "messages": [...] }
```

Supported values: `low`, `medium`, `high`, `max`.

## Structured output

Pass `response_format.type = "json_schema"` to enforce a JSON Schema on the output:

```json
{
  "model": "claude-sonnet-4-6",
  "response_format": {
    "type": "json_schema",
    "json_schema": { "schema": { "type": "object", "properties": { "answer": { "type": "string" } } } }
  },
  "messages": [{ "role": "user", "content": "What is 2+2?" }]
}
```

## Why tools are not supported

The OpenAI tools API works as a two-phase loop: the model emits a `tool_calls` response, the **client** executes the tool, then sends the result back.

The Claude Code CLI handles tool execution internally — it runs bash commands, reads files, and calls APIs on its own, without exposing intermediate `tool_calls` to the caller. There is no way to intercept that loop and re-expose it as OpenAI-style function calling.

If you need client-controlled tool use, call the Anthropic API directly or use a proxy like [LiteLLM](https://github.com/BerriAI/litellm). The value of this gateway is using your existing Claude Code auth and its built-in agentic execution — controllable via `--no-tools` if you want pure LLM mode.

## Not supported

- `tools` / `tool_choice` in the request body — see above
- Sampling parameters (`temperature`, `top_p`, etc.)
- Multi-turn server-side state (`previous_response_id`)
- `n > 1`
