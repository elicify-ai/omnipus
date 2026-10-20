package session

// Supplemental CR-02 private-codec/projection controls; CHECK must audit.
// Oracle: signed thinking is lossless model-only data, never a chat wire field.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

func TestExpandedThinking_PrivateCodecKeepsOpaqueBlocksOffPublicWire(t *testing.T) {
	blocks := []string{
		"{ \"type\":\"thinking\", \"thinking\":\"unicode π <opaque>\", \"signature\":\"PRIVATE-SIGNED-THINKING\" }",
		`{"type":"redacted_thinking","data":"PRIVATE-REDACTED-BYTES"}`,
	}
	original := providers.Message{Role: "assistant", Content: "visible", ThinkingBlocks: append([]string(nil), blocks...)}
	payload, err := EncodeModelPayload(original)
	if err != nil {
		t.Fatal(err)
	}
	original.ThinkingBlocks[0] = "mutated input"
	if !reflect.DeepEqual(payload.ThinkingBlocks, blocks) {
		t.Fatalf("encode aliased/dropped signed blocks: %+v", payload.ThinkingBlocks)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var reopened ModelPayload
	if decodeErr := json.Unmarshal(raw, &reopened); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	replayed, err := DecodeModelPayload(reopened)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayed.ThinkingBlocks, blocks) {
		t.Fatalf("private disk roundtrip changed opaque bytes: got%q want%q", replayed.ThinkingBlocks, blocks)
	}
	for _, value := range []any{replayed, providers.LLMResponse{Content: "visible", ThinkingBlocks: blocks}} {
		public, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if strings.Contains(string(public), "PRIVATE-") || strings.Contains(string(public), "thinking_blocks") {
			t.Fatalf("private replay carrier leaked into public provider JSON: %s", public)
		}
	}
	replayed.ThinkingBlocks[0] = "mutated output"
	if !reflect.DeepEqual(reopened.ThinkingBlocks, blocks) {
		t.Fatalf("decode aliased stored signed blocks: %+v", reopened.ThinkingBlocks)
	}
}

func TestExpandedThinking_PrivatePayloadWithoutBlocksStaysAbsent(t *testing.T) {
	var plain ModelPayload
	if err := json.Unmarshal([]byte(`{"role":"assistant","content":"plain answer"}`), &plain); err != nil {
		t.Fatal(err)
	}
	msg, err := DecodeModelPayload(plain)
	if err != nil {
		t.Fatal(err)
	}
	if msg.ThinkingBlocks != nil {
		t.Fatalf("absent replay blocks fabricated: %+v", msg.ThinkingBlocks)
	}
}
