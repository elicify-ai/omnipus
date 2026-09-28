// Omnipus — WP-F RED pack, stage B: the D6 signed-block carrier.
//
// Spec sources: ADR-095 D6 — ThinkingBlocks []ThinkingBlock on
// pkg/providers/protocoltypes/types.go::Message, with
// ThinkingBlock{Type, Thinking, Signature, Data} matching Anthropic's real
// block shapes ("thinking" = {type, thinking, signature};
// "redacted_thinking" = {type, data}); spec Section 2.2 row
// "protocoltypes.Message"; Section 1 C3. D8: the blocks serialize through the
// wholesale marshal — signatures and redacted data included — so every
// assertion here also pins that no json:"-" strip sneaks in (the
// ToolCall.ThoughtSignature precedent is explicitly NOT followed).
package protocoltypes

import (
	"encoding/json"
	"strings"
	"testing"
)

// The carrier must survive a wholesale JSON round-trip with every field
// byte-exact — this is the disk form the LLM-context file persists and the
// every-writer-carries-blocks rule leans on (ADR-095 D8).
func TestMessageThinkingBlocks_JSONRoundTripByteExact(t *testing.T) {
	blocks := []ThinkingBlock{
		{Type: "thinking", Thinking: "raw think → 回答の前に (multi-byte)", Signature: "sig-θ-signature=="},
		{Type: "redacted_thinking", Data: "opaque-encrypted-Ø-payload"},
	}
	in := Message{
		Role:           "assistant",
		Content:        "Answer.",
		ToolCallID:     "",
		ThinkingBlocks: blocks,
	}

	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// D8: signature and redacted data serialize — no strip step anywhere.
	for _, material := range []string{"sig-θ-signature==", "opaque-encrypted-Ø-payload", "raw think → 回答の前に (multi-byte)"} {
		if !strings.Contains(string(raw), material) {
			t.Errorf("marshalled Message JSON does not carry %q — thinking material must serialize (ADR-095 D8; ToolCall.ThoughtSignature's json:\"-\" precedent must NOT be copied)", material)
		}
	}

	var out Message
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.ThinkingBlocks) != len(blocks) {
		t.Fatalf("round-trip ThinkingBlocks len = %d, want %d", len(out.ThinkingBlocks), len(blocks))
	}
	for i := range blocks {
		if out.ThinkingBlocks[i] != blocks[i] {
			t.Errorf("round-trip ThinkingBlocks[%d] = %+v, want %+v (byte-exact)", i, out.ThinkingBlocks[i], blocks[i])
		}
	}
}

// The carrier's field shape is ADR-095 D6's verbatim list. This test is
// compile-anchored: it names every field with its declared role so a reshaped
// carrier (renamed field, changed role, dropped field) fails here first.
func TestThinkingBlock_CarrierShapeMatchesADR095D6(t *testing.T) {
	blocks := []ThinkingBlock{
		{Type: "thinking", Thinking: "t", Signature: "s"},
		{Type: "redacted_thinking", Data: "d"},
	}
	if blocks[0].Type != "thinking" || blocks[0].Thinking != "t" || blocks[0].Signature != "s" || blocks[0].Data != "" {
		t.Errorf("thinking block fields diverge from the D6 carrier: %+v", blocks[0])
	}
	if blocks[1].Type != "redacted_thinking" || blocks[1].Data != "d" || blocks[1].Thinking != "" || blocks[1].Signature != "" {
		t.Errorf("redacted_thinking block fields diverge from the D6 carrier: %+v", blocks[1])
	}
}
