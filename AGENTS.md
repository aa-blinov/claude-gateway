# AGENTS.md — agent context for `claude-gateway`

A Go server that speaks the OpenAI API and answers with the Claude Code CLI.
One file holds the whole thing, and it is `main.go`. This file is what you
need to work on the project without reading all of it.

## Run / build / test

```bash
make test       # go test -v ./...
make build      # dist/claude-gateway
make build-all  # six platform binaries in dist/
go build -o claude-gateway .   # a binary in the working directory
```

Go 1.22 or newer. The tests need no network and no `claude` binary, and the
whole suite runs in well under a second.

## Where things live

| Path | What it holds |
| --- | --- |
| `main.go` | The whole server: flags, routes, validation, the call to the CLI, the translation back. |
| `main_test.go` | The tests for the pure helpers. Nothing else is tested. |
| `Makefile` | `test`, `build`, `build-all`, `clean`. |
| `go.mod` | The module. Go 1.22, and no requirements. |
| `PRODUCT.md` | The product truth. |

`src/`, `tests/` and `scripts/` are empty placeholders put there by the house
standard. The code is at the root of the repository, and it stays there.

## Traps

- **The request becomes a single prompt.** There is no conversation state.
  The `messages` array is flattened: system and developer messages go to
  `--system-prompt`, and the user and assistant turns are joined into one
  block on standard input, each prefixed with `USER:` or `ASSISTANT:`. The
  whole history is sent again with every turn, so a long conversation grows
  with every message in it.
- **The CLI's standard output is the contract.** The ordinary path parses all
  of it as one JSON object with `result`, `is_error` and `stop_reason`. If
  the CLI prints anything else there, the parse fails and the client gets a
  500 with the code `parse_error`.
- **Streaming is a second invocation with different flags.**
  `--output-format stream-json --verbose --include-partial-messages`, read
  line by line. Only a `content_block_delta` carrying a `text_delta` becomes
  a chunk, and a line that is not valid JSON is skipped in silence.
- **Once the stream has started, there is no error left to report.** The
  headers go out before the first chunk, so a CLI that fails halfway ends the
  stream with an ordinary `stop` chunk. The client cannot tell a failure from
  a finished answer.
- **Unsupported fields are refused, not ignored.** `validateChatRequest` and
  `validateResponsesRequest` name the offending fields in the 400. Many
  clients send `temperature` by default, and the answer is an error rather
  than a quiet drop.
- **The tool loop stays inside Claude Code.** The CLI runs bash, reads files
  and calls APIs on its own, so no `tool_calls` ever reach the client and
  there is no way to add them. `--no-tools` is the only switch, and it passes
  `--tools ""`.
- **The model name is not checked.** The `model` field goes to `--model` as
  it is. `knownModels` and `CLAUDE_GATEWAY_MODELS` decide only what
  `/v1/models` answers.
- **The concurrency limit is a semaphore, not a queue.** `sem` is a buffered
  channel of size `--max-concurrency`, and a request blocks on it. The
  default of 100 is 100 Claude Code processes, each with its own context and
  its own file access. A value below 1 stops the process at startup.
- **The timeout kills the child.** The context comes from `r.Context()`, so a
  client that disconnects takes the CLI down with it. A timeout answers with
  a 504.
- **The API key is never read.** The `Authorization` header is ignored on
  purpose, because clients send a placeholder. Do not add validation.
- **`--no-session-persistence` is always passed.** It is what keeps the CLI
  from keeping a session between requests. Removing it changes what the CLI
  writes to disk.

## Style / safety rails

- **No dependencies.** `go.mod` has no requirements, and the server is the
  standard library only. A release is one static binary per platform, and it
  is updated by replacing the file.
- **The gateway has no authentication.** It listens on `127.0.0.1` by default
  for that reason. Do not bind it to a routable address without putting a
  check of your own in front of it.
- **The CLI runs as the user who started the gateway.** Its bash and its file
  access are that user's, and so is its Claude Code session. Treat the port
  as a shell for whoever can reach it.
- **Every capability maps onto one flag of `claude --print`.** `--model`,
  `--system-prompt`, `--effort`, `--json-schema`, `--tools`,
  `--output-format`. A feature that cannot be written as one of those does
  not belong here.
- **Comments explain why, never what.**
- **No emojis**, in any file.

## Where the product truth lives

`PRODUCT.md` holds it. Read it before changing what the gateway answers, and
write the change there when the behaviour changes.
