# claude-gateway

An HTTP gateway that speaks the OpenAI API and answers with Claude Code. Every
request becomes a `claude --print` call on your own machine, using the
session you are already signed into, so the reply comes from an agent that can
read files and run bash. Point any OpenAI-compatible client at
`http://127.0.0.1:8000` and give it any API key.

[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)
[![Release](https://img.shields.io/github/v/release/aa-blinov/claude-gateway.svg)](https://github.com/aa-blinov/claude-gateway/releases/latest)
[![Go](https://img.shields.io/badge/go-1.22-00ADD8)](https://go.dev/)

---

## Quick start

Claude Code has to be installed and signed in (`claude auth login`), and the
`claude` binary has to be on `PATH`. Then, from a clone of this repository:

```bash
go build -o claude-gateway .
./claude-gateway --port 8000
```

Go 1.22 or newer. The binary has no dependencies of its own.

The [release](https://github.com/aa-blinov/claude-gateway/releases/latest) carries
a build for six platforms:

| Platform | File |
| --- | --- |
| Windows x64 | `claude-gateway-windows-amd64.exe` |
| Windows ARM64 | `claude-gateway-windows-arm64.exe` |
| Linux x64 | `claude-gateway-linux-amd64` |
| Linux ARM64 | `claude-gateway-linux-arm64` |
| macOS x64 | `claude-gateway-darwin-amd64` |
| macOS Apple Silicon | `claude-gateway-darwin-arm64` |

A request from the shell looks like this:

```bash
curl http://127.0.0.1:8000/v1/chat/completions \
  -H 'content-type: application/json' \
  -d '{"model": "claude-sonnet-4-6", "messages": [{"role": "user", "content": "Say hello"}]}'
```

Any API key is accepted, because the server never reads the `Authorization`
header.

## What it does

The Anthropic API needs an API key and has no agent loop of its own. Claude
Code has the session and the tools but speaks no HTTP. The gateway is the join
between the two.

- **Serve the OpenAI routes.** `GET /v1/models`, `POST /v1/chat/completions`,
  `POST /v1/responses` and `GET /healthz` answer in the OpenAI shapes, so a
  client that already speaks OpenAI needs no change.
- **Run the request as Claude Code.** The conversation is written to the
  standard input of `claude --print`, and Claude Code runs the agent loop
  itself: file access, bash and the rest of its tools.
- **Stream the answer.** With `"stream": true` the reply arrives as
  server-sent events, one `chat.completion.chunk` per piece of text.
- **Choose how much the model thinks.** `reasoning_effort`, or
  `reasoning.effort`, takes `low`, `medium`, `high` or `max`.
- **Hold the answer to a schema.** `response_format.type = "json_schema"` goes
  to Claude Code as `--json-schema`, and the model answers with JSON that
  follows it.
- **Turn the tools off.** `--no-tools` runs the CLI with an empty tool list,
  which leaves a plain model behind a plain HTTP API.
- **Refuse what it cannot serve.** A request field the gateway does not
  implement comes back as a 400 that names the field, rather than being
  dropped on the way to the CLI.

## How it works

```
request -> validate -> build the prompt -> claude --print -> parse -> OpenAI shape
```

`main.go` is the whole server: the flags, the four routes, the validation, the
call to the CLI and the translation back. It uses the standard library only.

| Route | What it does |
| --- | --- |
| `GET /v1/models` | The model list, three names unless `CLAUDE_GATEWAY_MODELS` says otherwise. |
| `POST /v1/chat/completions` | A chat completion, plain or streaming. |
| `POST /v1/responses` | The stateless part of the OpenAI Responses API. |
| `GET /healthz` | `{"status":"ok"}`. |

The `messages` array becomes one prompt. System and developer messages are
joined and handed over as `--system-prompt`; user and assistant turns are
written to standard input as `USER:` and `ASSISTANT:` blocks, separated by a
blank line. Nothing is kept between requests, and every call carries
`--no-session-persistence`, so the CLI starts from scratch each time.

An ordinary request runs the CLI with `--output-format json`, and the whole of
its standard output is parsed as one object with `result`, `is_error` and
`stop_reason`. A `max_tokens` stop reason becomes the `length` finish reason,
and anything else becomes `stop`.

A streaming request runs the CLI again, this time with `--output-format
stream-json --verbose --include-partial-messages`. The server reads the output
line by line and turns every `content_block_delta` carrying a `text_delta`
into one `chat.completion.chunk`, then writes `data: [DONE]`.

The CLI runs with the permissions of the user who started the gateway, so the
bash it runs and the files it reads are that user's.

## Configuration

Every flag has an environment variable behind it, and the flag wins when both
are given.

| Flag | Environment variable | Default | What it does |
| --- | --- | --- | --- |
| `--host` | `CLAUDE_GATEWAY_HOST` | `127.0.0.1` | The address to listen on. |
| `--port` | `CLAUDE_GATEWAY_PORT` | `8000` | The port to listen on. |
| `--claude-bin` | `CLAUDE_GATEWAY_BIN` | `claude` | The path of the Claude Code binary. |
| `--no-tools` | `CLAUDE_GATEWAY_NO_TOOLS` | `false` | Turns the Claude Code tools off. |
| `--max-concurrency` | `CLAUDE_GATEWAY_MAX_CONCURRENCY` | `100` | How many Claude processes may run at once. |
| `--timeout-seconds` | `CLAUDE_GATEWAY_TIMEOUT_SECONDS` | `600` | How long one request may take. |

`CLAUDE_GATEWAY_NO_TOOLS` counts as true when it is `1` or `true`, in any
case. A `--max-concurrency` or a `--timeout-seconds` below 1 stops the process
at startup.

One variable has no flag:

| Environment variable | Default | What it does |
| --- | --- | --- |
| `CLAUDE_GATEWAY_MODELS` | three built-in names | The list `GET /v1/models` returns, comma separated. |

The built-in names are `claude-opus-4-6`, `claude-sonnet-4-6` and
`claude-haiku-4-5-20251001`. They decide what `/v1/models` answers and nothing
else: the model in a request is passed to the CLI as it is.

## Development

```bash
make test       # go test -v ./...
make build      # dist/claude-gateway
make build-all  # the six platform binaries, in dist/
make clean      # remove dist/
```

Go 1.22 or newer, and nothing else: `go.mod` has no requirements, and the
server is built from the standard library.

| Path | What it holds |
| --- | --- |
| `main.go` | The server: flags, routes, validation, the call to the CLI. |
| `main_test.go` | The tests for the parsing helpers. |
| `Makefile` | The test, the build and the cross build. |
| `go.mod` | The module. Go 1.22, no dependencies. |

The suite covers the helpers that turn a request into a prompt. Nothing in it
starts the server or runs the CLI, so a change to a handler or to the CLI
arguments is not covered by `make test`.

## License

MIT, © Alexander Blinov. See [LICENSE](LICENSE).
