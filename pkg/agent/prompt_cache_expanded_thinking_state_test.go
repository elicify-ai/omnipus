package agent

// CR-02 structural controls from architect-shape.md: signed messages are never
// merged/dropped; opaque replay consumes budget; collapsed text owns no signature.

import (
	"reflect"
	"testing"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

func TestExpandedThinking_NormalizerPreservesSignedOnlyMessages(t *testing.T) {
	signed := providers.Message{Role: "assistant", ThinkingBlocks: []string{`{"type":"thinking","thinking":"opaque","signature":"SIGNATURE"}`}}
	plain := providers.Message{Role: "assistant", Content: "next visible answer"}
	// Consecutive user messages force the full normalizer, not just its fast path.
	got := normalizeMessagesForProvider([]providers.Message{signed, plain, {Role: "user", Content: "one"}, {Role: "user", Content: "two"}})
	want := []providers.Message{signed, plain, {Role: "user", Content: "one\ntwo"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizer changed signed message structure: got%+v want%+v", got, want)
	}
	if needsNormalization([]providers.Message{signed, plain}) {
		t.Fatal("signed/plain adjacent assistants must not request a merge")
	}
}

func TestExpandedThinking_BudgetCountsOpaqueReplayAndCollapsedTextHasNoSignature(t *testing.T) {
	block := `{"type":"thinking","thinking":"π opaque thought","signature":"OPAQUE-SIGNATURE"}`
	msg := providers.Message{Role: "assistant", Content: "visible", ThinkingBlocks: []string{block}}
	// Existing estimator: UTF-8 runes,12chars/message overhead,2/5tokens/char.
	want := (utf8.RuneCountInString(msg.Content) + utf8.RuneCountInString(block) + 12) * 2 / 5
	if got := estimateMessageTokens(msg); got != want {
		t.Fatalf("signed replay budget=%d want%d", got, want)
	}
	chain := continuationChainMessages("collapsed answer")
	for _, entry := range chain {
		if len(entry.ThinkingBlocks) != 0 {
			t.Fatalf("collapsed continuation inherited a signature: %+v", entry)
		}
	}
	if sameContinuationChainMessage(msg, providers.Message{Role: "assistant", Content: "visible"}) {
		t.Fatal("a signed original must not be mistaken for a synthetic collapsed continuation")
	}
}
