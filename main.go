package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ── defaults ──────────────────────────────────────────────────────────────────

const (
	defaultHost           = "127.0.0.1"
	defaultPort           = 8000
	defaultMaxConcurrency = 100
	defaultTimeoutSeconds = 600
)

var (
	supportedEfforts = map[string]bool{"low": true, "medium": true, "high": true, "max": true}
	textPartTypes    = map[string]bool{"text": true, "input_text": true, "output_text": true}
	knownModels      = []string{
		"claude-opus-4-6",
		"claude-sonnet-4-6",
		"claude-haiku-4-5-20251001",
	}
)

// ── server state ──────────────────────────────────────────────────────────────

type srv struct {
	claudeBin      string
	timeoutSeconds int
	noTools        bool
	sem            chan struct{}
}

// ── errors ────────────────────────────────────────────────────────────────────

type reqErr struct {
	msg        string
	statusCode int
	code       string
}

func (e *reqErr) Error() string { return e.msg }

func badRequest(msg string) *reqErr {
	return &reqErr{msg: msg, statusCode: 400, code: "invalid_request_error"}
}
func serverError(msg, code string) *reqErr { return &reqErr{msg: msg, statusCode: 500, code: code} }
func timeoutError(msg string) *reqErr      { return &reqErr{msg: msg, statusCode: 504, code: "timeout"} }

// ── JSON helpers ──────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, e *reqErr) {
	writeJSON(w, e.statusCode, map[string]any{
		"error": map[string]any{"message": e.msg, "type": e.code, "code": e.code},
	})
}

func randHex() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ── validation helpers ────────────────────────────────────────────────────────

func requireString(v any, field string) (string, error) {
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", badRequest(fmt.Sprintf("`%s` must be a non-empty string.", field))
	}
	return s, nil
}

func parseModel(raw any) (string, error) {
	return requireString(raw, "model")
}

func parseEffort(body map[string]any) (string, error) {
	direct := body["reasoning_effort"]
	nested := body["reasoning"]
	if direct != nil && nested != nil {
		return "", badRequest("Provide either `reasoning_effort` or `reasoning.effort`, not both.")
	}
	var raw any
	switch {
	case direct != nil:
		raw = direct
	case nested != nil:
		r, ok := nested.(map[string]any)
		if !ok || r["effort"] == nil {
			return "", badRequest("`reasoning` must be an object with an `effort` field.")
		}
		raw = r["effort"]
	default:
		return "", nil
	}
	effort, err := requireString(raw, "reasoning_effort")
	if err != nil {
		return "", err
	}
	if !supportedEfforts[effort] {
		return "", badRequest("`reasoning_effort` must be one of: high, low, max, medium.")
	}
	return effort, nil
}

func checkUnsupported(body map[string]any, fields []string) error {
	var bad []string
	for _, f := range fields {
		if body[f] != nil {
			bad = append(bad, f)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return badRequest(fmt.Sprintf("Unsupported request fields for this wrapper: %s.", strings.Join(bad, ", ")))
	}
	return nil
}

func validateChatRequest(body map[string]any) error {
	if v, _ := body["stream"].(bool); v {
		// streaming is supported — don't reject
	}
	if n := body["n"]; n != nil {
		if nf, ok := n.(float64); !ok || nf != 1 {
			return badRequest("Unsupported request fields for this wrapper: n.")
		}
	}
	return checkUnsupported(body, []string{
		"tools", "tool_choice", "parallel_tool_calls",
		"temperature", "top_p", "max_tokens", "max_completion_tokens",
		"presence_penalty", "frequency_penalty", "logprobs", "top_logprobs",
	})
}

func validateResponsesRequest(body map[string]any) error {
	return checkUnsupported(body, []string{
		"tools", "tool_choice", "parallel_tool_calls",
		"previous_response_id", "conversation", "truncation",
		"temperature", "top_p", "max_output_tokens", "max_tool_calls",
		"prompt", "include",
	})
}

// ── message building ──────────────────────────────────────────────────────────

func normalizeContent(role string, content any) (string, error) {
	switch v := content.(type) {
	case string:
		return v, nil
	case []any:
		var parts []string
		for i, item := range v {
			p, ok := item.(map[string]any)
			if !ok {
				return "", badRequest(fmt.Sprintf("`messages[].content[%d]` must be an object.", i))
			}
			pt, _ := p["type"].(string)
			if !textPartTypes[pt] {
				return "", badRequest(fmt.Sprintf("Unsupported content part type `%s` in role `%s`.", pt, role))
			}
			t, ok := p["text"].(string)
			if !ok {
				return "", badRequest(fmt.Sprintf("`messages[].content[%d].text` must be a string.", i))
			}
			parts = append(parts, t)
		}
		return strings.Join(parts, "\n"), nil
	default:
		return "", badRequest(fmt.Sprintf("`messages[].content` for role `%s` must be a string or list of text parts.", role))
	}
}

// buildConversation splits messages into system prompt + user/assistant transcript.
func buildConversation(messages []any) (systemPrompt string, transcript string, err error) {
	if len(messages) == 0 {
		return "", "", badRequest("`messages` must be a non-empty array.")
	}

	var systemParts, turns []string

	for i, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			return "", "", badRequest(fmt.Sprintf("`messages[%d]` must be an object.", i))
		}
		role, _ := msg["role"].(string)
		switch role {
		case "system", "developer", "user", "assistant":
		default:
			return "", "", badRequest(fmt.Sprintf("Unsupported role `%s` at `messages[%d]`.", role, i))
		}
		if msg["tool_calls"] != nil {
			return "", "", badRequest(fmt.Sprintf("`messages[%d].tool_calls` is not supported.", i))
		}
		content, err := normalizeContent(role, msg["content"])
		if err != nil {
			return "", "", err
		}
		if role == "system" || role == "developer" {
			systemParts = append(systemParts, content)
		} else {
			turns = append(turns, strings.ToUpper(role)+":\n"+content)
		}
	}

	if len(turns) == 0 {
		return "", "", badRequest("At least one `user` or `assistant` message is required.")
	}

	return strings.Join(systemParts, "\n\n"), strings.Join(turns, "\n\n"), nil
}

// ── schema extraction ─────────────────────────────────────────────────────────

func extractChatSchema(body map[string]any) (map[string]any, error) {
	rf := body["response_format"]
	if rf == nil {
		return nil, nil
	}
	rfMap, ok := rf.(map[string]any)
	if !ok {
		return nil, badRequest("`response_format` must be an object.")
	}
	rfType, _ := rfMap["type"].(string)
	if rfType == "" || rfType == "text" {
		return nil, nil
	}
	if rfType != "json_schema" {
		return nil, badRequest(`Only "response_format.type = \"json_schema\"" is supported.`)
	}
	jsc, ok := rfMap["json_schema"].(map[string]any)
	if !ok {
		return nil, badRequest("`response_format.json_schema` must be an object.")
	}
	schema, ok := jsc["schema"].(map[string]any)
	if !ok {
		return nil, badRequest("`response_format.json_schema.schema` must be an object.")
	}
	return schema, nil
}

func extractResponsesSchema(body map[string]any) (map[string]any, error) {
	tc, _ := body["text"].(map[string]any)
	if tc == nil {
		if body["text"] != nil {
			return nil, badRequest("`text` must be an object.")
		}
		return nil, nil
	}
	fc, _ := tc["format"].(map[string]any)
	if fc == nil {
		return nil, nil
	}
	fType, _ := fc["type"].(string)
	if fType == "" || fType == "text" {
		return nil, nil
	}
	if fType != "json_schema" {
		return nil, badRequest(`Only "text.format.type = \"json_schema\"" is supported.`)
	}
	jsc, ok := fc["json_schema"].(map[string]any)
	if !ok {
		return nil, badRequest("`text.format.json_schema` must be an object.")
	}
	schema, ok := jsc["schema"].(map[string]any)
	if !ok {
		return nil, badRequest("`text.format.json_schema.schema` must be an object.")
	}
	return schema, nil
}

// ── responses input ───────────────────────────────────────────────────────────

func buildResponsesMessages(body map[string]any) ([]any, error) {
	var msgs []any
	if instr := body["instructions"]; instr != nil {
		s, err := requireString(instr, "instructions")
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, map[string]any{"role": "system", "content": s})
	}
	raw := body["input"]
	if raw == nil {
		return nil, badRequest("`input` is required for `/v1/responses`.")
	}
	if s, ok := raw.(string); ok {
		msgs = append(msgs, map[string]any{"role": "user", "content": s})
		return msgs, nil
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, badRequest("`input` must be a non-empty string or array.")
	}
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, badRequest(fmt.Sprintf("`input[%d]` must be an object.", i))
		}
		itemType, _ := m["type"].(string)
		if itemType == "" || itemType == "message" {
			if m["role"] == nil || m["content"] == nil {
				return nil, badRequest(fmt.Sprintf("`input[%d]` must have `role` and `content`.", i))
			}
			msgs = append(msgs, map[string]any{"role": m["role"], "content": m["content"]})
		} else if textPartTypes[itemType] {
			t, ok := m["text"].(string)
			if !ok {
				return nil, badRequest(fmt.Sprintf("`input[%d].text` must be a string.", i))
			}
			msgs = append(msgs, map[string]any{"role": "user", "content": t})
		} else {
			return nil, badRequest(fmt.Sprintf("Unsupported `input[%d].type` value `%s`.", i, itemType))
		}
	}
	return msgs, nil
}

// ── claude CLI invocation ─────────────────────────────────────────────────────

// claudeResult is the final JSON line from claude --output-format json
type claudeResult struct {
	Type    string `json:"type"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
	Stop    string `json:"stop_reason"`
}

func (s *srv) buildArgs(model, systemPrompt, effort string, schema map[string]any, streaming bool) ([]string, error) {
	args := []string{
		"--print",
		"--model", model,
		"--no-session-persistence",
	}
	if streaming {
		args = append(args, "--output-format", "stream-json", "--verbose", "--include-partial-messages")
	} else {
		args = append(args, "--output-format", "json")
	}
	if s.noTools {
		args = append(args, "--tools", "")
	}
	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}
	if effort != "" {
		args = append(args, "--effort", effort)
	}
	if schema != nil {
		b, err := json.Marshal(schema)
		if err != nil {
			return nil, serverError("failed to marshal json schema", "server_error")
		}
		args = append(args, "--json-schema", string(b))
	}
	return args, nil
}

// runClaude runs claude non-interactively and returns the full text response.
func (s *srv) runClaude(ctx context.Context, transcript, systemPrompt, model, effort string, schema map[string]any) (string, string, error) {
	args, err := s.buildArgs(model, systemPrompt, effort, schema, false)
	if err != nil {
		return "", "", err
	}

	cmd := exec.CommandContext(ctx, s.claudeBin, args...)
	cmd.Stdin = strings.NewReader(transcript)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", "", timeoutError("claude timed out.")
		}
		details := strings.TrimSpace(stderr.String())
		if details == "" {
			details = strings.TrimSpace(stdout.String())
		}
		if details == "" {
			details = "claude failed without output."
		}
		return "", "", serverError("claude failed: "+details, "claude_failed")
	}

	var result claudeResult
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &result); err != nil {
		return "", "", serverError("failed to parse claude output: "+err.Error(), "parse_error")
	}
	if result.IsError {
		return "", "", serverError("claude returned error: "+result.Result, "claude_error")
	}
	return result.Result, stopReason(result.Stop), nil
}

// streamClaude runs claude with stream-json output and writes SSE events to w.
func (s *srv) streamClaude(ctx context.Context, w http.ResponseWriter, completionID, model, transcript, systemPrompt, effort string, schema map[string]any) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, serverError("streaming not supported by this server", "server_error"))
		return
	}

	args, err := s.buildArgs(model, systemPrompt, effort, schema, true)
	if err != nil {
		writeError(w, err.(*reqErr))
		return
	}

	cmd := exec.CommandContext(ctx, s.claudeBin, args...)
	cmd.Stdin = strings.NewReader(transcript)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		writeError(w, serverError("failed to create pipe: "+err.Error(), "server_error"))
		return
	}

	if err := cmd.Start(); err != nil {
		writeError(w, serverError("failed to start claude: "+err.Error(), "server_error"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(200)

	sendChunk := func(text, finishReason string) {
		chunk := map[string]any{
			"id":      completionID,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []any{map[string]any{
				"index":         0,
				"delta":         map[string]any{"content": text},
				"finish_reason": finishReason,
			}},
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	scanner := bufio.NewScanner(stdoutPipe)
	for scanner.Scan() {
		var envelope struct {
			Type  string `json:"type"`
			Event *struct {
				Type  string `json:"type"`
				Delta *struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
			} `json:"event"`
			IsError bool   `json:"is_error"`
			Result  string `json:"result"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			continue
		}
		switch envelope.Type {
		case "stream_event":
			if envelope.Event != nil &&
				envelope.Event.Type == "content_block_delta" &&
				envelope.Event.Delta != nil &&
				envelope.Event.Delta.Type == "text_delta" &&
				envelope.Event.Delta.Text != "" {
				sendChunk(envelope.Event.Delta.Text, "")
			}
		case "result":
			if envelope.IsError {
				sendChunk("", "stop")
			}
		}
	}

	_ = cmd.Wait()
	sendChunk("", "stop")
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func stopReason(claudeStop string) string {
	switch claudeStop {
	case "max_tokens":
		return "length"
	default:
		return "stop"
	}
}

// ── models ────────────────────────────────────────────────────────────────────

func listModels() []string {
	if env := os.Getenv("CLAUDE_GATEWAY_MODELS"); env != "" {
		var models []string
		for _, m := range strings.Split(env, ",") {
			if m = strings.TrimSpace(m); m != "" {
				models = append(models, m)
			}
		}
		return models
	}
	return knownModels
}

// ── handlers ──────────────────────────────────────────────────────────────────

func (s *srv) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *srv) models(w http.ResponseWriter, r *http.Request) {
	data := make([]any, 0)
	for _, m := range listModels() {
		data = append(data, map[string]any{
			"id": m, "object": "model", "created": 0, "owned_by": "anthropic",
		})
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": data})
}

func (s *srv) chatCompletions(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, badRequest("Request body must be valid JSON."))
		return
	}

	if err := validateChatRequest(body); err != nil {
		writeError(w, err.(*reqErr))
		return
	}

	requestedModel := body["model"]
	model, err := parseModel(requestedModel)
	if err != nil {
		writeError(w, err.(*reqErr))
		return
	}
	effort, err := parseEffort(body)
	if err != nil {
		writeError(w, err.(*reqErr))
		return
	}
	schema, err := extractChatSchema(body)
	if err != nil {
		writeError(w, err.(*reqErr))
		return
	}

	msgs, ok := body["messages"].([]any)
	if !ok {
		writeError(w, badRequest("`messages` must be a non-empty array."))
		return
	}
	systemPrompt, transcript, err := buildConversation(msgs)
	if err != nil {
		writeError(w, err.(*reqErr))
		return
	}

	stream, _ := body["stream"].(bool)
	completionID := "chatcmpl-" + randHex()

	s.sem <- struct{}{}
	defer func() { <-s.sem }()

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.timeoutSeconds)*time.Second)
	defer cancel()

	if stream {
		s.streamClaude(ctx, w, completionID, model, transcript, systemPrompt, effort, schema)
		return
	}

	text, finishReason, err := s.runClaude(ctx, transcript, systemPrompt, model, effort, schema)
	if err != nil {
		writeError(w, err.(*reqErr))
		return
	}

	writeJSON(w, 200, map[string]any{
		"id":      completionID,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   requestedModel,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": text},
			"finish_reason": finishReason,
		}},
	})
}

func (s *srv) responses(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, badRequest("Request body must be valid JSON."))
		return
	}
	if err := validateResponsesRequest(body); err != nil {
		writeError(w, err.(*reqErr))
		return
	}

	requestedModel := body["model"]
	model, err := parseModel(requestedModel)
	if err != nil {
		writeError(w, err.(*reqErr))
		return
	}
	effort, err := parseEffort(body)
	if err != nil {
		writeError(w, err.(*reqErr))
		return
	}
	schema, err := extractResponsesSchema(body)
	if err != nil {
		writeError(w, err.(*reqErr))
		return
	}

	msgs, err := buildResponsesMessages(body)
	if err != nil {
		writeError(w, err.(*reqErr))
		return
	}
	systemPrompt, transcript, err := buildConversation(msgs)
	if err != nil {
		writeError(w, err.(*reqErr))
		return
	}

	s.sem <- struct{}{}
	defer func() { <-s.sem }()

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.timeoutSeconds)*time.Second)
	defer cancel()

	text, _, err := s.runClaude(ctx, transcript, systemPrompt, model, effort, schema)
	if err != nil {
		writeError(w, err.(*reqErr))
		return
	}

	writeJSON(w, 200, map[string]any{
		"id":                 "resp_" + randHex(),
		"object":             "response",
		"created_at":         time.Now().Unix(),
		"status":             "completed",
		"error":              nil,
		"incomplete_details": nil,
		"instructions":       body["instructions"],
		"model":              requestedModel,
		"output": []any{map[string]any{
			"id":     "msg_" + randHex(),
			"type":   "message",
			"status": "completed",
			"role":   "assistant",
			"content": []any{map[string]any{
				"type":        "output_text",
				"text":        text,
				"annotations": []any{},
			}},
		}},
	})
}

// ── CLI & main ────────────────────────────────────────────────────────────────

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string) bool {
	v := strings.ToLower(os.Getenv(key))
	return v == "1" || v == "true"
}

func main() {
	host := flag.String("host", envStr("CLAUDE_GATEWAY_HOST", defaultHost), "Listen address")
	port := flag.Int("port", envInt("CLAUDE_GATEWAY_PORT", defaultPort), "Listen port")
	claudeBin := flag.String("claude-bin", envStr("CLAUDE_GATEWAY_BIN", "claude"), "Path to claude binary")
	maxConcurrency := flag.Int("max-concurrency",
		envInt("CLAUDE_GATEWAY_MAX_CONCURRENCY", defaultMaxConcurrency),
		fmt.Sprintf("Max concurrent claude processes (default %d)", defaultMaxConcurrency))
	timeoutSeconds := flag.Int("timeout-seconds",
		envInt("CLAUDE_GATEWAY_TIMEOUT_SECONDS", defaultTimeoutSeconds),
		fmt.Sprintf("Per-request timeout in seconds (default %d)", defaultTimeoutSeconds))
	noTools := flag.Bool("no-tools", envBool("CLAUDE_GATEWAY_NO_TOOLS"),
		"Disable all Claude Code tools (pure LLM mode)")
	flag.Parse()

	if *maxConcurrency < 1 {
		log.Fatal("--max-concurrency must be >= 1")
	}
	if *timeoutSeconds < 1 {
		log.Fatal("--timeout-seconds must be >= 1")
	}

	s := &srv{
		claudeBin:      *claudeBin,
		timeoutSeconds: *timeoutSeconds,
		noTools:        *noTools,
		sem:            make(chan struct{}, *maxConcurrency),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /v1/models", s.models)
	mux.HandleFunc("POST /v1/chat/completions", s.chatCompletions)
	mux.HandleFunc("POST /v1/responses", s.responses)

	addr := fmt.Sprintf("%s:%d", *host, *port)
	log.Printf("claude-gateway listening on http://%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
