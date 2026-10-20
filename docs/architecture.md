# Architecture

How claude-gateway is put together.

The whole server is one file, `main.go`, 759 lines, and the standard library
only. There is no `src/`, no package directory and no interface between the
parts. The structure is therefore a set of regions in one file, and the
boundaries between them are conventions rather than imports.

## The shape

A request arrives, is validated, is flattened into a single prompt, is handed
to `claude --print` as a child process, and the answer is translated back into
the OpenAI shape. The server keeps nothing between requests and starts a new
process for every one of them.

| Region of `main.go` | What it is responsible for |
| --- | --- |
| defaults | The four defaults, the four reasoning efforts, the three text part types and the three built-in model names. |
| `srv` | The state the handlers need: the path of the CLI, the timeout, the tools switch and the semaphore. |
| `reqErr` | The one error type. It carries a message, a status code and a code string, and the three constructors are `badRequest`, `serverError` and `timeoutError`. |
| `writeJSON`, `writeError` | The only two functions that write to an `http.ResponseWriter`. Everything else returns values. |
| `requireString`, `parseModel`, `parseEffort` | The field-level checks. They answer with a `*reqErr` and never touch the request. |
| `checkUnsupported`, `validateChatRequest`, `validateResponsesRequest` | The request-level checks. They name every field they refuse, sorted. |
| `normalizeContent`, `buildConversation` | The flattening of `messages` into a system prompt and a transcript. |
| `extractChatSchema`, `extractResponsesSchema` | The JSON schema, taken from `response_format` on one route and from `text.format` on the other. |
| `buildResponsesMessages` | The `input` field of the Responses API, turned into the same `messages` array the chat route uses. |
| `buildArgs` | The only place that knows the flags of `claude --print`. |
| `runClaude` | The plain path: one child process, the whole of its standard output parsed as one JSON object. |
| `streamClaude` | The streaming path: the same child process with different flags, read line by line. |
| `stopReason` | The translation of the CLI's stop reason into `stop` or `length`. |
| `listModels` | The model list, from the environment variable or from the built-in names. |
| `healthz`, `models`, `chatCompletions`, `responses` | The four handlers. |
| `envStr`, `envInt`, `envBool` | The environment lookup behind every flag. |
| `main` | The flags, the two startup checks, the `ServeMux` and the listener. |

The tests in `main_test.go` cover the pure helpers only: the model, the
effort, the chat validator, the conversation, the chat schema, the responses
input and the stop reason. Nothing starts the server and nothing runs the
CLI.

## The data flow

```
request -> validate -> build the prompt -> claude --print -> parse -> OpenAI shape
```

The two routes that take a body share the same middle, and they differ only
in the envelope at each end.

```
POST /v1/chat/completions          POST /v1/responses
messages[]                        instructions + input[]
        |                                  |
        +-----------> buildConversation <--+
                          |         |
              system prompt         transcript
                          |         |
                          +--> buildArgs <--+
                                   |
                            claude --print
                                   |
                     runClaude or streamClaude
                                   |
             chat.completion    or   chat.completion.chunk frames
```

The prompt the CLI receives has two parts. System and developer messages are
joined with a blank line and handed over as `--system-prompt`. User and
assistant turns are written to standard input as `USER:` and `ASSISTANT:`
blocks, also joined with a blank line. Nothing is kept between requests, and
every call carries `--no-session-persistence`, so the CLI starts from scratch
each time.

The plain path parses all of the standard output as one JSON object with
`result`, `is_error` and `stop_reason`. A `max_tokens` stop reason becomes
the `length` finish reason, and anything else becomes `stop`.

The streaming path runs the CLI again with `--output-format stream-json
--verbose --include-partial-messages`. The server reads the output line by
line and turns every `content_block_delta` carrying a `text_delta` into one
`chat.completion.chunk`, then writes `data: [DONE]`.

The CLI runs with the permissions of the user who started the gateway, so the
bash it runs and the files it reads are that user's.

## Decisions worth knowing about

### The whole request becomes one prompt

There is no conversation state. The `messages` array is flattened into a
system prompt and a transcript, and the whole history travels with every
turn. The cost is that a long conversation grows with every message in it,
and the benefit is that the CLI needs no session of its own.

### The output of the CLI is the contract

The plain path parses all of the standard output as one JSON object. If the
CLI prints anything else there, the parse fails and the client gets a 500
with the code `parse_error`. This is deliberate: a half-readable answer
would be worse than a refusal, because the client could not tell a truncated
answer from a complete one.

### Streaming is a second invocation

A streaming request does not reuse the plain path and does not write into
it. It runs the CLI with three extra flags and reads the pipe as it arrives.
The two paths share `buildArgs` and nothing else.

Once the stream has started there is no error left to report. The headers go
out before the first chunk, so a CLI that fails halfway ends the stream with
an ordinary `stop` chunk. A client cannot tell that failure from a finished
answer.

### The semaphore is a limit, not a queue

`sem` is a buffered channel of size `--max-concurrency`, and a request blocks
on it. The default of 100 is 100 Claude Code processes at once, each with its
own context and its own file access. A value below 1 stops the process at
startup.

### The timeout kills the child

The context comes from `r.Context()`, so a client that disconnects takes the
CLI down with it. A timeout answers with a 504.

### An unsupported field is refused

`validateChatRequest` and `validateResponsesRequest` name the offending
fields in the 400. Many clients send `temperature` by default, and the
answer is an error rather than a quiet drop.

## Boundaries

- **The standard library only.** `go.mod` has no requirements. A release is
  one static binary per platform, and it is updated by replacing the file.
- **The helpers do not know about HTTP.** Everything that writes a response
  goes through `writeJSON` or `writeError`, and everything that reads a
  request is a function that takes a `map[string]any`.
- **`buildArgs` is the only place that names a CLI flag.** `--model`,
  `--system-prompt`, `--effort`, `--json-schema`, `--tools`,
  `--output-format`. A capability that cannot be written as one of those
  flags does not belong in this repository.
- **The handlers do not hold state.** The state is `srv`, and it holds the
  configuration and the semaphore. Nothing else lives between requests.
- **`src/`, `tests/` and `scripts/` are empty placeholders** put there by the
  house standard. The code is at the root of the repository and it stays
  there.
