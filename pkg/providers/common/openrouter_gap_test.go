// Omnipus — WP-F RED pack: OpenRouter's structured-reasoning gap, proven and
// bounded (spec Section 16 test 15, BDD "OpenRouter's structured-reasoning gap
// is real and bounded (not fixed in v1)", traces to D21 / #943).
//
// This is a PROVING test, not a fix: it demonstrates exactly what the v1
// carrier does with a SIGNED OpenRouter reasoning block, so the gap is
// documented and bounded rather than silently present. ADR-095 D6:
// "OpenRouter structured reasoning (LLMResponse.ReasoningDetails, which has no
// signature field) is out of v1 per spec D21/#943". The behavior pinned here is
// CURRENT behavior by design (characterization of the documented gap) — the
// spec explicitly rules it is NOT fixed in v1, and no v1 code path may pretend
// otherwise.
//
// The bound, stated precisely:
//   - Kept: display text (reasoning.text / reasoning.summary entries →
//     ReasoningDetails.Text; streamed spellings → Reasoning/ReasoningContent —
//     the stream half is WP-B's, already covered by
//     pkg/providers/openai_compat/streaming_reasoning_capture_test.go).
//   - Lost: the signed/encrypted material. protocoltypes.ReasoningDetail has
//     no field for a reasoning.encrypted entry's `data` (or any signature), so
//     encoding/json drops it at parse; and
//     common.SerializeMessages has no reasoning_details carrier at all, so
//     whatever structured reasoning arrived can never re-enter a request on
//     this route. A continuation that requires the signed block (the analogue
//     of Anthropic's signed thinking blocks) therefore cannot be served —
//     tracked as #943, out of v1.
package common

import (
	"encoding/json"
	"strings"
	"testing"
)

// openRouterGapSignatureMaterial is the fixture's signed/encrypted payload.
// Assertions lean on its absence/presence as a byte string.
const openRouterGapSignatureMaterial = "ENCRYPTED-SIGNATURE-MATERIAL-9f8e7d6c"

func TestParseResponse_OpenRouterSignedReasoningDetail_SignedMaterialHasNoCarrier(t *testing.T) {
	body := `{
		"choices":[{
			"message":{
				"content":"Answer.",
				"reasoning_details":[
					{"type":"reasoning.text","format":"text","index":0,"text":"visible summary"},
					{"type":"reasoning.encrypted","format":"reasoning.encrypted","index":1,"data":"` + openRouterGapSignatureMaterial + `"}
				]
			},
			"finish_reason":"stop"
		}],
		"usage":{"prompt_tokens":3,"completion_tokens":7,"total_tokens":10}
	}`

	resp, err := ParseResponse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}

	// Kept: the text-bearing entry survives with its text and type.
	if len(resp.ReasoningDetails) != 2 {
		t.Fatalf("ReasoningDetails len = %d, want 2 (the text entry is kept; the encrypted entry still identifies itself by type)", len(resp.ReasoningDetails))
	}
	if resp.ReasoningDetails[0].Type != "reasoning.text" || resp.ReasoningDetails[0].Text != "visible summary" {
		t.Errorf("ReasoningDetails[0] = %+v, want type %q with text %q — display text is kept", resp.ReasoningDetails[0], "reasoning.text", "visible summary")
	}

	// Lost: the encrypted entry's signed material. ReasoningDetail has no
	// field for it, so the entry decodes with no text and the material appears
	// nowhere in the parsed response.
	if got := resp.ReasoningDetails[1]; got.Text != "" || got.Type != "reasoning.encrypted" {
		t.Errorf("ReasoningDetails[1] = %+v, want type %q with NO carried payload — the detail type has no data field (ADR-095 D6)", got, "reasoning.encrypted")
	}
	marshalled, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshalling response: %v", err)
	}
	if strings.Contains(string(marshalled), openRouterGapSignatureMaterial) {
		t.Error("the signed material survives somewhere in the parsed response — the gap would not be bounded; #943's premise (no signature carrier) is false and this test's contract changes")
	}

	// The re-sendable reasoning carrier stays empty: nothing signed can ride it.
	if resp.ReasoningContent != "" {
		t.Errorf("ReasoningContent = %q, want empty — the encrypted entry is opaque ciphertext, never display text", resp.ReasoningContent)
	}
}

func TestSerializeMessages_OpenRouterReasoningOnlyHistory_ReSendsNoSignedMaterial(t *testing.T) {
	history := []Message{
		{Role: "user", Content: "question"},
		{Role: "assistant", Content: "Answer.", ReasoningContent: "visible summary"},
	}

	serialized := SerializeMessages(history)
	raw, err := json.Marshal(serialized)
	if err != nil {
		t.Fatalf("marshalling serialized messages: %v", err)
	}

	// The openai-compat re-send carries the display reasoning string…
	if !strings.Contains(string(raw), "visible summary") {
		t.Errorf("serialized request does not re-send the display reasoning — want reasoning_content carried (CF7 path)")
	}
	// …and has NO structured-reasoning carrier at all: whatever signed
	// material a provider returned can never re-enter a request on this
	// route. That absence is the bound recorded against #943.
	if strings.Contains(string(raw), "reasoning_details") {
		t.Errorf("serialized request carries a %q key — the v1 re-send has no structured carrier; if this appears, the route gained one and #943's bound moves", "reasoning_details")
	}
	if strings.Contains(string(raw), openRouterGapSignatureMaterial) {
		t.Error("serialized request carries the signed material — impossible by construction here, fail loudly if the fixture ever leaks")
	}
}
