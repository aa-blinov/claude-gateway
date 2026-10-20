# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

An HTTP server written in Go 1.22 and built from the standard library alone,
with no interface of its own. It runs on a machine where the Claude Code CLI
is installed and signed in, and it answers clients that speak the OpenAI API.

## Users

The person running it already has Claude Code and a session for it, and wants
that session to answer from tools that speak only OpenAI: a script, an editor
plugin, another application. The situation is a request that arrives over HTTP
and has to become a Claude Code run on the same machine.

The repository does not say who else uses it. It is public, it has one
release, it has no stars, and there is no telemetry, so nobody beyond the
owner is confirmed.

## Product Purpose

The gateway turns a Claude Code session into an HTTP service that speaks the
OpenAI API.

It exists because the two halves do not meet. The Anthropic API speaks HTTP
but wants an API key and has no agent loop, and Claude Code has the session
and the tools but speaks no HTTP. The gateway keeps the Claude Code side and
implements the HTTP side.

Success for the user: a client that already speaks OpenAI works against
Claude Code, with streaming, a reasoning effort and a JSON schema. Success for
the product: a release that somebody downloads and runs without a runtime to
install. The code cannot tell us whether either has happened. The download
counts on the release are the only sign there is, and they are small.

## Positioning

A neighbour can call the Anthropic API from a gateway, and many do. That
neighbour needs an API key and cannot run tools: the model answers and stops.

This one reuses the CLI the user is already signed into, and the CLI runs the
agent loop itself: bash, files, and the rest. The tool calls never cross the
HTTP boundary, so there is no `tool_calls` array for a client to execute and
no way to add one. What the client gets is what a finished run produced.

The second difference is the refusal. A field the gateway does not implement
comes back as a 400 that names it, instead of a request that quietly lost an
option on the way to the CLI.

## Operating Context

The gateway is a background process on a machine where Claude Code is
installed and authenticated. It listens on `127.0.0.1:8000` and waits.

A request arrives, and the gateway starts a `claude` process for it. That
process holds the conversation, uses the tools, and can take minutes: the
default timeout is 600 seconds, and the default limit is 100 of these at once.
The process runs with the file and shell permissions of the user who started
the gateway, and it uses that user's Claude Code session rather than a key
the gateway stores.

Nothing is kept afterwards. The CLI is started with `--no-session-persistence`
and the gateway holds no state of its own.

## Capabilities and Constraints

Confirmed by the code:

- Four routes: `GET /v1/models`, `POST /v1/chat/completions`,
  `POST /v1/responses` and `GET /healthz`.
- Chat completions with and without streaming, the streaming form being
  server-sent events of `chat.completion.chunk` objects.
- A stateless subset of the OpenAI Responses API, with `instructions`, `input`
  and `text.format`.
- Reasoning effort of `low`, `medium`, `high` or `max`, taken from
  `reasoning_effort` or `reasoning.effort`.
- JSON schema output, from `response_format.type = "json_schema"` or
  `text.format.type = "json_schema"`.
- System and developer messages handed to the CLI as `--system-prompt`.
- The Claude Code tools, on by default and off with `--no-tools`.
- A limit of 100 processes at once and a request timeout of 600 seconds, both
  configurable.
- Errors in the OpenAI envelope: 400 for a bad request, 500 when the CLI
  fails, 504 when it takes too long.
- No dependencies. One binary per platform, built from the standard library.

Deliberately not true, and worth knowing before a change:

- There is no client-side tool calling. The CLI executes the tools itself and
  the loop cannot be intercepted.
- Sampling parameters are refused. `temperature`, `top_p`, `max_tokens`,
  `max_completion_tokens`, `presence_penalty`, `frequency_penalty`, `logprobs`
  and `top_logprobs` each come back as a 400.
- `n` is accepted only as 1.
- There is no server-side conversation state. `previous_response_id` and
  `conversation` are refused.
- The model list is a constant, and the name in a request is never checked
  against it.
- There is no authentication. The API key in the `Authorization` header is
  never read.
- Only text is accepted. A content part that is not `text`, `input_text` or
  `output_text` is refused, so there is no image input.

## Brand Commitments

**Name:** claude-gateway. **Tone:** plain and technical. The README is short,
the error messages name the field that caused them, and nothing tries to
persuade.

The name says what it is: a gateway, over the Claude Code CLI, speaking the
OpenAI API. A rename has to carry all three parts.

What has to survive a redesign:

- The routes and the envelopes follow the OpenAI API, so a client needs no
  special mode.
- A refusal names the field and answers with a 400.
- The gateway stores nothing about a conversation.

## Evidence on Hand

- `main.go` holds the routes, the defaults, the validation and the flags.
- `main_test.go` covers seven helpers: the model, the effort, the chat
  validator, the conversation, the chat schema extractor, the responses input
  and the stop reason.
- `Makefile` holds the cross build for Windows, Linux and macOS, on amd64 and
  arm64.
- The v0.1.0 release, published on 2026-04-11, carries six binaries, and their
  download counts are between 4 and 12.
- The GitHub description: "OpenAI-compatible HTTP gateway over Claude Code CLI
  with real streaming".

Absent, and not to be invented: no user count, no benchmark, no testimonial,
no comparison with another gateway, and no measurement of what the subprocess
costs in latency. Before the adoption pull request the repository had no CI
and no licence file, and nothing in the code records who calls the service.

## Product Principles

1. **The CLI is the only backend.** Every capability maps onto one flag of
   `claude --print`. A feature that cannot be written as one of those flags
   does not go in.
2. **Refuse what cannot be served.** An unsupported field is a 400 that names
   it. A client that sends `temperature` learns that the parameter is not
   supported instead of watching it disappear.
3. **The session belongs to the user.** The gateway stores no key, opens no
   account and holds no conversation. The authentication is the `claude` login
   that is already there.
4. **No dependencies.** The standard library, one binary per platform, no
   runtime to install.
5. **The OpenAI shape is the contract.** The routes, the bodies and the error
   envelope follow the OpenAI API, and a client that speaks it works without
   changes.

## Accessibility & Inclusion

There is no interface, so there is no screen reader, no contrast and no focus
to think about. Three constraints still decide who can use the service:

- **Text only.** A content part that is not `text`, `input_text` or
  `output_text` is refused, so a client cannot send an image, a file or an
  audio clip.
- **No authentication.** The service is safe on loopback and unsafe anywhere
  else, which rules out a shared host without something in front of it.
- **No conversation state.** A client that wants a follow-up question has to
  send the history again.

No accessibility standard has been set for the API surface, and the repository
carries no accessibility statement. Whether the errors should say more, and in
which languages, is not decided.
