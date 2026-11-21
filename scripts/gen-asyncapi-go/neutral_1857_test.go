package main

// neutral_1857_test.go — #1857 frontend round, RED tests for design-note
// decision D-B (generator half): the contract's x-user-messages entry may
// carry a sibling `neutral` string and `neutral_variants` mapping, and the
// generators hard-fail on a malformed or missing neutral form.
//
// Oracle: /workspace/logs/1857/arch/design-note-frontend-round.md, D-B B1,
// "Generator change" and "Test hooks":
//
//	a first-person chat form without `neutral` must fail;
//	`neutral_variants` keys that differ from `variants` keys must fail;
//	a neutral form with an out-of-vocabulary slot must fail.
//
// First-person tokens named by the design note: \bI\b, I', I’, \bme\b, \bmy\b.
//
// The fixtures are written for this file (small synthetic catalogues), not
// copied from the shipped contract, so the tests do not depend on the wording
// under founder review. The positive controls prove the failures are caused by
// the named defect and not by an unrelated problem in the fixture.
//
// Only the Go generator is covered: extractUserMessageCatalogue is called
// directly. scripts/_gen-asyncapi-types.mjs has no exported entry point (it
// runs as a script), so its twin validation is not unit-testable here.

import (
	"strings"
	"testing"
)

// neutralFixtureDoc builds the minimal parsed AsyncAPI document the extractor
// reads: components.schemas.LLMError with a code enum, the attribution
// vocabulary and one x-user-messages entry per code.
func neutralFixtureDoc(entries map[string]map[string]any) map[string]any {
	codes := make([]any, 0, len(entries))
	messages := make(map[string]any, len(entries))
	for code, entry := range entries {
		codes = append(codes, code)
		messages[code] = entry
	}
	return map[string]any{
		"components": map[string]any{
			"schemas": map[string]any{
				"LLMError": map[string]any{
					"properties": map[string]any{
						"code": map[string]any{"enum": codes},
					},
					"x-user-message-attributions": []any{"config", "unknown"},
					"x-user-messages":             messages,
				},
			},
		},
	}
}

// entry builds one x-user-messages row.
func neutralEntry(message string, extra map[string]any) map[string]any {
	row := map[string]any{"message": message, "attribution": "config"}
	for k, v := range extra {
		row[k] = v
	}
	return row
}

func TestGeneratorNeutralForms1857(t *testing.T) {
	t.Run("control_clean_entry_with_neutral_and_matching_variants_passes", func(t *testing.T) {
		doc := neutralFixtureDoc(map[string]map[string]any{
			"alpha": neutralEntry("I could not start {cli}. Check the setting.", map[string]any{
				"neutral":          "{cli} could not be started. Check the setting.",
				"variants":         map[string]any{"v1": "I stopped {cli} at {n} turns."},
				"neutral_variants": map[string]any{"v1": "{cli} was stopped at {n} turns."},
			}),
		})
		if _, err := extractUserMessageCatalogue(doc, "LLMError"); err != nil {
			t.Fatalf("D-B control: a first-person chat form WITH a neutral twin and matching neutral_variants must pass, got: %v", err)
		}
	})

	t.Run("control_chat_form_without_first_person_needs_no_twin", func(t *testing.T) {
		doc := neutralFixtureDoc(map[string]map[string]any{
			"alpha": neutralEntry("{cli} is not signed in. Sign in to {cli}, then retry the task.", nil),
			// "Item" and "IP" contain the letter I but are not the pronoun.
			"beta": neutralEntry("The Item list for the IP address is empty.", nil),
		})
		if _, err := extractUserMessageCatalogue(doc, "LLMError"); err != nil {
			t.Fatalf("D-B control: a chat form with no first-person token needs no neutral twin, got: %v", err)
		}
	})

	firstPersonForms := map[string]string{
		"I_space":            "I could not start {cli}. Check the setting.",
		"I_straight_quote":   "I'll stop at {n} turns. Check the setting.",
		"I_curly_quote":      "I’ve reached the limit of {n} turns. Check the setting.",
		"me":                 "Tell me when {cli} is ready again.",
		"my":                 "Check my setting for {cli}.",
		"sentence_end_Me_My": "Open the page. My settings for {cli} are on it.",
	}
	for name, chat := range firstPersonForms {
		t.Run("first_person_chat_form_without_neutral_fails_"+name, func(t *testing.T) {
			doc := neutralFixtureDoc(map[string]map[string]any{"alpha": neutralEntry(chat, nil)})
			_, err := extractUserMessageCatalogue(doc, "LLMError")
			if err == nil {
				t.Fatalf("D-B generator: a first-person chat form without a `neutral` twin must fail the generator, got nil for %q", chat)
			}
			if !strings.Contains(err.Error(), "neutral") || !strings.Contains(err.Error(), "alpha") {
				t.Errorf("D-B generator: the error must name the code and the missing neutral form, got: %v", err)
			}
		})
	}

	t.Run("first_person_variant_without_neutral_variant_fails", func(t *testing.T) {
		doc := neutralFixtureDoc(map[string]map[string]any{
			"alpha": neutralEntry("{cli} stopped with an error.", map[string]any{
				"variants": map[string]any{"v1": "I could not read the output of {cli}."},
			}),
		})
		_, err := extractUserMessageCatalogue(doc, "LLMError")
		if err == nil {
			t.Fatal("D-B generator: a first-person variant without a neutral_variants twin must fail the generator, got nil")
		}
	})

	t.Run("neutral_variants_keys_differing_from_variants_keys_fail", func(t *testing.T) {
		cases := map[string]map[string]any{
			"missing_key": {"other": "{cli} was stopped."},
			"extra_key":   {"v1": "{cli} was stopped.", "v2": "{cli} was stopped again."},
			"empty_set":   {},
		}
		for name, neutralVariants := range cases {
			doc := neutralFixtureDoc(map[string]map[string]any{
				"alpha": neutralEntry("I could not start {cli}.", map[string]any{
					"neutral":          "{cli} could not be started.",
					"variants":         map[string]any{"v1": "I stopped {cli}."},
					"neutral_variants": neutralVariants,
				}),
			})
			if _, err := extractUserMessageCatalogue(doc, "LLMError"); err == nil {
				t.Errorf("D-B generator (%s): neutral_variants keys that differ from variants keys must fail the generator, got nil", name)
			}
		}
	})

	t.Run("neutral_with_out_of_vocabulary_slot_fails", func(t *testing.T) {
		for _, bad := range []string{"{provider} could not be started.", "{oops} stopped.", "{cli} could not start {host}."} {
			doc := neutralFixtureDoc(map[string]map[string]any{
				"alpha": neutralEntry("I could not start {cli}.", map[string]any{"neutral": bad}),
			})
			if _, err := extractUserMessageCatalogue(doc, "LLMError"); err == nil {
				t.Errorf("D-B generator: a neutral form with an out-of-vocabulary slot must fail the generator, got nil for %q", bad)
			}
		}
	})

	t.Run("neutral_variant_with_out_of_vocabulary_slot_fails", func(t *testing.T) {
		doc := neutralFixtureDoc(map[string]map[string]any{
			"alpha": neutralEntry("I could not start {cli}.", map[string]any{
				"neutral":          "{cli} could not be started.",
				"variants":         map[string]any{"v1": "I stopped {cli}."},
				"neutral_variants": map[string]any{"v1": "{provider} was stopped."},
			}),
		})
		if _, err := extractUserMessageCatalogue(doc, "LLMError"); err == nil {
			t.Fatal("D-B generator: a neutral_variants sentence with an out-of-vocabulary slot must fail the generator, got nil")
		}
	})

	t.Run("neutral_that_is_itself_first_person_fails", func(t *testing.T) {
		// Rule 4 of spec §13.8.4: a neutral twin is for surfaces outside a
		// chat; a twin that still says "I" defeats its purpose.
		doc := neutralFixtureDoc(map[string]map[string]any{
			"alpha": neutralEntry("I could not start {cli}.", map[string]any{"neutral": "I could not start {cli} either."}),
		})
		if _, err := extractUserMessageCatalogue(doc, "LLMError"); err == nil {
			t.Fatal("D-B generator: a neutral form containing a first-person token must fail the generator, got nil")
		}
	})
}
