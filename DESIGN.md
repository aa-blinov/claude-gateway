---
name: claude-gateway
description: No interface of its own. The design here is the JSON it answers with and the wording of its refusals.
colors: {}
typography: {}
rounded: {}
spacing: {}
---

# Design system: claude-gateway

## Overview

This service has no interface. There is no stylesheet, no font, no palette and
no component library, because the whole server is one file, `main.go`, and it
answers in JSON. The token layer in the frontmatter is empty on purpose.

What the project does have is a wire contract, and the contract is where the
design decisions are. Four routes answer in the shapes of the OpenAI API, the
error envelope is the OpenAI error envelope, and a refusal names the field
that caused it.

The names are the design. `chat.completion`, `chat.completion.chunk`,
`response`, `invalid_request_error`, `parse_error`: a client that speaks
OpenAI already knows them, and a client that does not has the OpenAI
documentation to read. Renaming one of them breaks every client at once.

**Key characteristics:**

- The OpenAI shapes are the contract, the error envelope included.
- A refusal names the field. A field that is quietly dropped is a bug.
- The standard output of the CLI is parsed strictly, and anything else is a
  500 with the code `parse_error`.
- Nothing is kept between requests, so there is no session to design.

## Colors

There is no palette here, and no terminal palette either, because nothing is
printed for a person to read. What plays the role of colour is the status
vocabulary, and it has four members.

| Status | Code | What it means |
| --- | --- | --- |
| 200 | none | The answer is in the body. |
| 400 | `invalid_request_error` | The request named a field the gateway refuses, or left one out. |
| 500 | `server_error`, `claude_failed`, `claude_error`, `parse_error` | The CLI failed, or its output could not be read. |
| 504 | `timeout` | The CLI ran past `--timeout-seconds`. |

Every status carries a `code` string inside the error object, and the code is
the machine-readable half of the same signal: a client can branch on
`claude_failed` without reading the sentence.

## Typography

There is no typeface here either. What plays the role of type is the field
name, because the field name is what a client parses by.

Three rules govern the names:

- **They are the OpenAI names.** `messages`, `model`, `stream`,
  `response_format`, `reasoning_effort`, `previous_response_id`. The gateway
  does not translate them into a house vocabulary.
- **The error message names the field first.** "`messages[].content` for role
  `user` must be a string or list of text parts." The field is in backticks
  and it comes before the sentence that explains it.
- **The sentence says what is wrong.** "Unsupported request fields for this
  wrapper: temperature, top_p." The list is sorted, so the same request always
  produces the same message.

## Components

### The routes

| Route | Method | Body |
| --- | --- | --- |
| `/v1/models` | GET | A list of model objects. The names come from `CLAUDE_GATEWAY_MODELS` or from the three built-in ones. |
| `/v1/chat/completions` | POST | A `chat.completion`, or a stream of `chat.completion.chunk`. |
| `/v1/responses` | POST | A `response` object with one message in `output`. |
| `/healthz` | GET | `{"status":"ok"}`. |

### The error envelope

```json
{"error": {"message": "...", "type": "invalid_request_error", "code": "invalid_request_error"}}
```

`type` and `code` carry the same string, and they always do. The duplication
is deliberate: the OpenAI API has clients that read `type` and clients that
read `code`, and one envelope has to satisfy both.

### The stream frames

A streaming answer is a series of `data: ` frames, each one a
`chat.completion.chunk`, and the last line is `data: [DONE]`. The chunk
carries `id`, `object`, `created`, `model` and one `choices` entry with
`index`, `delta` and `finish_reason`.

Only a `content_block_delta` carrying a `text_delta` becomes a chunk. A line
of the CLI's output that is not valid JSON is skipped without a word.

### The completion id

A chat completion is `chatcmpl-` followed by 16 bytes of `crypto/rand` in
hex, and a response is `resp_` followed by the same. The id is not
persisted and carries no meaning beyond being unique inside one run.

## Do's and Don'ts

### Do:

- **Do** answer in the OpenAI shapes, down to the field order.
- **Do** name the offending field in every refusal.
- **Do** keep the `Authorization` header unread, because a client that sends
  a placeholder key has to be answered anyway.
- **Do** keep the model list and the model name apart. The list answers
  `/v1/models`; the name in a request goes to the CLI as it is.

### Don't:

- **Don't** accept a field the gateway does not implement. An unknown field
  is a 400, not a key that quietly disappears.
- **Don't** accept a conversation and store it. The whole history is sent
  with every request, and the gateway keeps nothing.
- **Don't** soften the error message. The field name and the reason are the
  whole value of the 400.
- **Don't** read the API key. There is no authentication, and the port is on
  loopback for that reason.
