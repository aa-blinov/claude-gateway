package main

import (
	"strings"
	"testing"
)

// ── parseModel ────────────────────────────────────────────────────────────────

func TestParseModel(t *testing.T) {
	tests := []struct {
		input   any
		want    string
		wantErr bool
	}{
		{"claude-sonnet-4-6", "claude-sonnet-4-6", false},
		{"sonnet", "sonnet", false},
		{"", "", true},
		{nil, "", true},
		{42, "", true},
	}
	for _, tc := range tests {
		got, err := parseModel(tc.input)
		if (err != nil) != tc.wantErr {
			t.Errorf("parseModel(%v): wantErr=%v got %v", tc.input, tc.wantErr, err)
			continue
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("parseModel(%v): got %q want %q", tc.input, got, tc.want)
		}
	}
}

// ── parseEffort ───────────────────────────────────────────────────────────────

func TestParseEffort(t *testing.T) {
	tests := []struct {
		body    map[string]any
		want    string
		wantErr bool
	}{
		{map[string]any{}, "", false},
		{map[string]any{"reasoning_effort": "low"}, "low", false},
		{map[string]any{"reasoning_effort": "max"}, "max", false},
		{map[string]any{"reasoning": map[string]any{"effort": "high"}}, "high", false},
		{map[string]any{"reasoning_effort": "ultra"}, "", true},
		{map[string]any{"reasoning_effort": "low", "reasoning": map[string]any{"effort": "low"}}, "", true},
	}
	for _, tc := range tests {
		got, err := parseEffort(tc.body)
		if (err != nil) != tc.wantErr {
			t.Errorf("parseEffort(%v): wantErr=%v got %v", tc.body, tc.wantErr, err)
			continue
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("parseEffort(%v): got %q want %q", tc.body, got, tc.want)
		}
	}
}

// ── validateChatRequest ───────────────────────────────────────────────────────

func TestValidateChatRequest(t *testing.T) {
	tests := []struct {
		body    map[string]any
		wantErr bool
	}{
		{map[string]any{}, false},
		{map[string]any{"stream": true}, false},  // streaming is supported
		{map[string]any{"n": float64(1)}, false},
		{map[string]any{"n": float64(2)}, true},
		{map[string]any{"tools": []any{}}, true},
		{map[string]any{"temperature": 0.7}, true},
	}
	for _, tc := range tests {
		err := validateChatRequest(tc.body)
		if (err != nil) != tc.wantErr {
			t.Errorf("validateChatRequest(%v): wantErr=%v got %v", tc.body, tc.wantErr, err)
		}
	}
}

// ── buildConversation ─────────────────────────────────────────────────────────

func TestBuildConversation(t *testing.T) {
	// system + user
	msgs := []any{
		map[string]any{"role": "system", "content": "Be brief."},
		map[string]any{"role": "user", "content": "Hello"},
	}
	sys, transcript, err := buildConversation(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sys != "Be brief." {
		t.Errorf("system prompt: got %q", sys)
	}
	if !strings.Contains(transcript, "Hello") {
		t.Errorf("transcript missing user message")
	}
	if !strings.Contains(transcript, "USER:") {
		t.Errorf("transcript missing USER: label")
	}

	// multi-turn
	msgs2 := []any{
		map[string]any{"role": "user", "content": "Hi"},
		map[string]any{"role": "assistant", "content": "Hello!"},
		map[string]any{"role": "user", "content": "Bye"},
	}
	_, transcript2, err := buildConversation(msgs2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(transcript2, "ASSISTANT:") {
		t.Errorf("transcript missing ASSISTANT: label")
	}

	// only system → error
	_, _, err = buildConversation([]any{
		map[string]any{"role": "system", "content": "x"},
	})
	if err == nil {
		t.Error("expected error for system-only messages")
	}

	// empty → error
	_, _, err = buildConversation([]any{})
	if err == nil {
		t.Error("expected error for empty messages")
	}

	// bad role → error
	_, _, err = buildConversation([]any{
		map[string]any{"role": "function", "content": "x"},
	})
	if err == nil {
		t.Error("expected error for unsupported role")
	}

	// multiple system messages joined
	msgs3 := []any{
		map[string]any{"role": "system", "content": "Part 1."},
		map[string]any{"role": "developer", "content": "Part 2."},
		map[string]any{"role": "user", "content": "hi"},
	}
	sys3, _, err := buildConversation(msgs3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sys3, "Part 1.") || !strings.Contains(sys3, "Part 2.") {
		t.Errorf("system prompt not merged: %q", sys3)
	}
}

// ── extractChatSchema ─────────────────────────────────────────────────────────

func TestExtractChatSchema(t *testing.T) {
	s, err := extractChatSchema(map[string]any{})
	if err != nil || s != nil {
		t.Errorf("no rf: got %v, %v", s, err)
	}

	s, err = extractChatSchema(map[string]any{
		"response_format": map[string]any{"type": "text"},
	})
	if err != nil || s != nil {
		t.Errorf("type=text: got %v, %v", s, err)
	}

	s, err = extractChatSchema(map[string]any{
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"schema": map[string]any{"type": "object"},
			},
		},
	})
	if err != nil || s == nil {
		t.Errorf("valid json_schema: got %v, %v", s, err)
	}

	_, err = extractChatSchema(map[string]any{
		"response_format": map[string]any{"type": "json_object"},
	})
	if err == nil {
		t.Error("expected error for unsupported type")
	}
}

// ── buildResponsesMessages ────────────────────────────────────────────────────

func TestBuildResponsesMessages(t *testing.T) {
	msgs, err := buildResponsesMessages(map[string]any{"input": "hello"})
	if err != nil || len(msgs) != 1 {
		t.Fatalf("string input: got %v, %v", msgs, err)
	}

	msgs, err = buildResponsesMessages(map[string]any{
		"instructions": "Be brief.",
		"input":        "hello",
	})
	if err != nil || len(msgs) != 2 {
		t.Fatalf("with instructions: got %v, %v", msgs, err)
	}
	if msgs[0].(map[string]any)["role"] != "system" {
		t.Error("first message should be system")
	}

	_, err = buildResponsesMessages(map[string]any{})
	if err == nil {
		t.Error("expected error for missing input")
	}
}

// ── stopReason ────────────────────────────────────────────────────────────────

func TestStopReason(t *testing.T) {
	if stopReason("end_turn") != "stop" {
		t.Error("end_turn should map to stop")
	}
	if stopReason("max_tokens") != "length" {
		t.Error("max_tokens should map to length")
	}
	if stopReason("") != "stop" {
		t.Error("empty should map to stop")
	}
}
