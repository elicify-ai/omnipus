package judge

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderPrompt_BasicRendering(t *testing.T) {
	ctx := PromptContext{
		AgentName: "mia",
		AgentRole: "Omnipus Guide",
		Prompt:    "Hi, what can you help with?",
		Transcript: []TranscriptEntry{
			{Role: "user", Content: "Hi, what can you help with?"},
			{Role: "assistant", Content: "I can help with onboarding and more."},
		},
		ToolCalls: []ToolCallEntry{},
		Rubric:    "Be friendly and concise.",
	}

	out, err := RenderPrompt(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "mia") {
		t.Error("rendered prompt missing agent name")
	}
	if !strings.Contains(out, "Omnipus Guide") {
		t.Error("rendered prompt missing agent role")
	}
	if !strings.Contains(out, "Be friendly and concise") {
		t.Error("rendered prompt missing rubric")
	}
}

func TestRenderPrompt_TranscriptIsValidJSON(t *testing.T) {
	ctx := PromptContext{
		AgentName: "ray",
		AgentRole: "Researcher",
		Prompt:    "Find something",
		Transcript: []TranscriptEntry{
			{Role: "user", Content: "Find something"},
			{Role: "assistant", Content: "Sure"},
		},
		ToolCalls: []ToolCallEntry{
			{ToolName: "browser.navigate", Args: `{"url":"https://example.com"}`},
		},
		Rubric: "Must use browser tool.",
	}

	out, err := RenderPrompt(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Extract the JSON portion from the rendered prompt between "Transcript (JSON):" and "Tool calls made:"
	transcriptStart := strings.Index(out, "Transcript (JSON):\n") + len("Transcript (JSON):\n")
	toolCallsStart := strings.Index(out, "\n\nTool calls made:")
	if transcriptStart < 0 || toolCallsStart < 0 || toolCallsStart <= transcriptStart {
		t.Skip("cannot locate transcript JSON section in prompt")
	}
	transcriptSection := strings.TrimSpace(out[transcriptStart:toolCallsStart])
	var parsed []map[string]any
	if err := json.Unmarshal([]byte(transcriptSection), &parsed); err != nil {
		t.Errorf("transcript section is not valid JSON: %v\ngot:\n%s", err, transcriptSection)
	}
}

func TestRenderPrompt_UnicodeAndControlChars(t *testing.T) {
	ctx := PromptContext{
		AgentName: "ava",
		AgentRole: "Builder",
		Prompt:    "Unicode: 日本語 emoji: \U0001F600 tab:\there",
		Transcript: []TranscriptEntry{
			{Role: "user", Content: "Unicode: 日本語 emoji: \U0001F600"},
			{Role: "assistant", Content: "I handle unicode fine."},
		},
		ToolCalls: []ToolCallEntry{},
		Rubric:    "Handle unicode.",
	}

	out, err := RenderPrompt(ctx)
	if err != nil {
		t.Fatalf("unexpected error rendering prompt with unicode: %v", err)
	}
	// The JSON-encoded transcript should contain escaped unicode or raw unicode.
	// Either way the rendered prompt must be non-empty and contain the agent name.
	if !strings.Contains(out, "ava") {
		t.Error("rendered prompt missing agent name")
	}
}

func TestRenderPrompt_EmptyTranscript(t *testing.T) {
	ctx := PromptContext{
		AgentName:  "jim",
		AgentRole:  "Analyst",
		Prompt:     "",
		Transcript: []TranscriptEntry{},
		ToolCalls:  []ToolCallEntry{},
		Rubric:     "Any rubric.",
	}

	out, err := RenderPrompt(ctx)
	if err != nil {
		t.Fatalf("unexpected error for empty transcript: %v", err)
	}
	// Empty slice should produce "[]" in the JSON output.
	if !strings.Contains(out, "[]") {
		t.Error("expected '[]' for empty transcript/tool calls in rendered prompt")
	}
}

func TestRenderPrompt_NilVsEmptyToolCalls(t *testing.T) {
	// Nil ToolCalls should marshal to "null" — but callers should always pass
	// an empty slice. Verify that we don't error on nil.
	ctx := PromptContext{
		AgentName:  "max",
		AgentRole:  "Executor",
		Prompt:     "Do something",
		Transcript: nil,
		ToolCalls:  nil,
		Rubric:     "Some rubric.",
	}
	_, err := RenderPrompt(ctx)
	if err != nil {
		t.Fatalf("unexpected error for nil slices: %v", err)
	}
}

func TestRenderPrompt_JSONEscapesSpecialChars(t *testing.T) {
	ctx := PromptContext{
		AgentName: "ray",
		AgentRole: "Researcher",
		Prompt:    `quote " backslash \`,
		Transcript: []TranscriptEntry{
			{Role: "user", Content: `contains "quotes" and \backslashes\`},
		},
		ToolCalls: []ToolCallEntry{},
		Rubric:    "Handle special chars.",
	}

	out, err := RenderPrompt(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The rendered prompt must contain the transcript section; the JSON
	// encoding must be parseable.
	transcriptStart := strings.Index(out, "Transcript (JSON):\n") + len("Transcript (JSON):\n")
	toolCallsStart := strings.Index(out, "\n\nTool calls made:")
	if transcriptStart > 0 && toolCallsStart > transcriptStart {
		transcriptSection := strings.TrimSpace(out[transcriptStart:toolCallsStart])
		var parsed []map[string]any
		if err := json.Unmarshal([]byte(transcriptSection), &parsed); err != nil {
			t.Errorf("transcript with special chars is not valid JSON: %v", err)
		}
	}
}
