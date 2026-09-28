// Omnipus — WP-F, squad-lead ruling 4 (D8.5 one-directional): when history
// already carries ThinkingBlocks from a prior round they round-trip back to
// the provider on a LATER turn regardless of whether THIS turn's
// reasoning_effort is set — round-trip fidelity of EXISTING blocks is
// unconditional; only the DECISION to REQUEST NEW thinking depends on the
// resolved effort (ADR-095 D6/D8/D8.5).
package anthropicprovider

import (
	"testing"
)

// The stage-B echo test pins the blocks-present + effort-high case. This is
// the complement: blocks present, NO effort — the assistant's thinking and
// redacted_thinking blocks must still be rebuilt on the wire byte-exact and
// in order, while the request carries NO thinking config (no thinking REQUEST
// without an effort; no guard omission-log either, nothing is omitted).
func TestChat_EchoesAssistantThinkingBlocks_EvenWhenEffortUnset(t *testing.T) {
	history := []Message{
		{Role: "user", Content: "What's the weather?"},
		{
			Role:    "assistant",
			Content: "Checking.",
			ThinkingBlocks: []ThinkingBlock{
				{Type: "thinking", Thinking: thinkingFixtureText, Signature: thinkingFixtureSignat},
				{Type: "redacted_thinking", Data: redactedFixtureData},
			},
		},
		{Role: "user", Content: "and now?"},
	}

	var captured string
	server := wireCaptureServer(t, &captured)
	defer server.Close()

	p := NewProviderWithBaseURL("test-token", server.URL)
	if _, err := p.Chat(t.Context(), history, nil, "claude-sonnet-4-6",
		map[string]any{"max_tokens": 1024}); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	body := capturedBodyMap(t, captured)

	// No effort → no thinking REQUEST (D9/D24)…
	if _, has := body["thinking"]; has {
		t.Errorf("effort unset: request carries %q = %v, want absence (D9/D24)", "thinking", body["thinking"])
	}
	if _, has := body["output_config"]; has {
		t.Errorf("effort unset: request carries %q = %v, want absence", "output_config", body["output_config"])
	}

	// …but the EXISTING blocks still round-trip byte-exact, in order,
	// thinking blocks preceding the text block (D6).
	msgs, ok := body["messages"].([]any)
	if !ok {
		t.Fatalf("messages is %T, want an array", body["messages"])
	}
	if len(msgs) != 3 {
		t.Fatalf("messages len = %d, want 3", len(msgs))
	}
	asst, ok := msgs[1].(map[string]any)
	if !ok || asst["role"] != "assistant" {
		t.Fatalf("messages[1] = %v, want the assistant message", msgs[1])
	}
	content, ok := asst["content"].([]any)
	if !ok {
		t.Fatalf("assistant content is %T, want an array", asst["content"])
	}
	if len(content) != 3 {
		t.Fatalf("assistant content has %d blocks, want 3: thinking, redacted_thinking, text (D6 echo is unconditional); got: %s", len(content), captured)
	}
	want0 := map[string]any{"type": "thinking", "thinking": thinkingFixtureText, "signature": thinkingFixtureSignat}
	if !reflectBlockEqual(t, content[0], want0) {
		t.Errorf("content[0] = %v, want %v", content[0], want0)
	}
	want1 := map[string]any{"type": "redacted_thinking", "data": redactedFixtureData}
	if !reflectBlockEqual(t, content[1], want1) {
		t.Errorf("content[1] = %v, want %v", content[1], want1)
	}
	want2 := map[string]any{"type": "text", "text": "Checking."}
	if !reflectBlockEqual(t, content[2], want2) {
		t.Errorf("content[2] = %v, want %v", content[2], want2)
	}
}
