// provider_messages_catalogue_test.go — provider-messages spec RED tests:
// TDD row 6, Go half (C-3/C-4, OBS-002, MAJ-103). TS half:
// src/lib/llm-error.test.ts.
//
// Oracles from the SPEC ONLY (§6 + §7.1 item 5):
//   - Bijection (C-3/C-4): the codes carrying a provider_message variant
//     are exactly the spec §6 templated set — provider_auth_failed,
//     rate_limited, quota_billing, model_retired — both directions.
//   - Closed-slot token check (OBS-002): every template's slots come from
//     the closed set {provider}, {answered_model}, {unavailable_model}.
//
// CHARACTERIZATION PIN, green-today-by-design: Wave 1 (contracts) landed
// this contract data; these assertions guard it against drift. Expected
// values derive from §6, not from the implementation.

package generated

import (
	"regexp"
	"testing"
)

// pmTemplatedCodes is the spec §6 templated set — the codes whose
// x-user-messages catalogue entry carries a provider_message variant.
var pmTemplatedCodes = []string{"provider_auth_failed", "rate_limited", "quota_billing", "model_retired"}

// pmClosedSlots is OBS-002's closed slot vocabulary.
var pmClosedSlots = map[string]bool{"provider": true, "answered_model": true, "unavailable_model": true}

// pmSlotRe extracts {slot} tokens from a template string.
var pmSlotRe = regexp.MustCompile(`\{([a-z_]+)\}`)

// TestProviderMessageCatalogue_BijectionAndClosedSlots — row 6 Go half.
func TestProviderMessageCatalogue_BijectionAndClosedSlots(t *testing.T) {
	// (a) Bijection, both directions (C-3/C-4).
	if len(LLMErrorProviderMessages) != len(pmTemplatedCodes) {
		t.Fatalf("LLMErrorProviderMessages has %d entries, want exactly %d (spec 6 bijection, C-3/C-4)",
			len(LLMErrorProviderMessages), len(pmTemplatedCodes))
	}
	for _, code := range pmTemplatedCodes {
		if _, ok := LLMErrorProviderMessages[code]; !ok {
			t.Errorf("spec templated code %q has no provider_message variant (C-3/C-4)", code)
		}
	}
	for code := range LLMErrorProviderMessages {
		found := false
		for _, want := range pmTemplatedCodes {
			if code == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("catalogue carries %q — not a spec 6 templated code (C-3/C-4)", code)
		}
	}

	// (b) Closed-slot token check (OBS-002).
	for code, tmpl := range LLMErrorProviderMessages {
		for _, slot := range pmSlotRe.FindAllStringSubmatch(tmpl, -1) {
			if !pmClosedSlots[slot[1]] {
				t.Errorf("template for %q uses slot {%s} — outside OBS-002's closed set (OBS-002/spec 6)", code, slot[1])
			}
		}
	}
}
