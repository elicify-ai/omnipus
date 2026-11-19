// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// neutral_settings_1857_test.go — #1857 frontend round, RED tests (gateway half).
//
// R5 (Settings surface, restAPIRunExecutorSmokeTest.buildResponse): the text a
// person reads in Settings -> worker -> "Send a test message" is the NEUTRAL
// form of the code's catalogue sentence rendered with the facts the handler
// knows; it contains no first-person token; a missing fact gives the generic
// sentence (design note D-B B1 and spec notifications-spec.md §13.8.4 rule 4).
//
// R1 (replay parity): a reloaded error frame carries the same flag and text as
// the live frame for every slotted sentence (design note D-A, test hooks).
//
// Oracles: the contract's `neutral` / `neutral_variants` entries
// (contracts/components/schemas/LLMError.yaml, the source of the generated
// neutral map) parsed with a YAML reader, and the generated chat catalogue
// filled with test facts. No sentence literal appears here (W1-W21 are under
// founder review). Where a chat form has no first-person token the neutral
// twin may be absent and the chat sentence serves both voices (D-B).

package gateway

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

var (
	gw1857Slot        = regexp.MustCompile(`\{([a-z_]+)\}`)
	gw1857FirstPerson = regexp.MustCompile(`\bI\b`)
	gw1857MeMy        = regexp.MustCompile(`(?i)\b(?:me|my)\b`)
)

func gw1857HasFirstPerson(text string) bool {
	return gw1857FirstPerson.MatchString(text) || gw1857MeMy.MatchString(text)
}

func gw1857Fill(t *testing.T, template string, facts map[string]string) string {
	t.Helper()
	return gw1857Slot.ReplaceAllStringFunc(template, func(slot string) string {
		name := slot[1 : len(slot)-1]
		value, ok := facts[name]
		if !ok {
			t.Fatalf("SETUP: slot {%s} has no test fact", name)
		}
		return value
	})
}

func gw1857Chat(code, variant string) string {
	if variant == "" {
		return generated.LLMErrorUserMessages[code]
	}
	return generated.LLMErrorMessageVariants[code][variant]
}

func gw1857Neutral(t *testing.T, code, variant string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "components", "schemas", "LLMError.yaml"))
	require.NoError(t, err, "SETUP: reading the contract source of the catalogue")
	var doc struct {
		Messages map[string]struct {
			Neutral         string            `yaml:"neutral"`
			NeutralVariants map[string]string `yaml:"neutral_variants"`
		} `yaml:"x-user-messages"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc), "SETUP: parsing the contract")
	entry := doc.Messages[code]
	twin := entry.Neutral
	if variant != "" {
		twin = entry.NeutralVariants[variant]
	}
	if twin != "" {
		return twin
	}
	chat := gw1857Chat(code, variant)
	if !gw1857HasFirstPerson(chat) {
		return chat
	}
	t.Fatalf("D-B B1 neutral twin: the contract has no neutral twin for %s (variant %q) although its chat form is first person: %q", code, variant, chat)
	return ""
}

// gw1857Settings drives the real Settings path: the consent-routed event
// channel through drainSmokeTestRun, then buildResponse, which publishes the
// result the Settings screen shows.
func gw1857Settings(t *testing.T, raw, cli, model string) (ok bool, shown string) {
	t.Helper()
	ch := make(chan runner.RunEvent, 2)
	ch <- runner.RunEvent{Kind: runner.EventKindError, Err: &runner.ErrorEvent{Message: raw, Fatal: true}}
	close(ch)
	_, runOK, errMsg := drainSmokeTestRun(context.Background(), "1857-settings-neutral", ch)
	ra := &restAPIRunExecutorSmokeTest{
		cli:        cli,
		model:      model,
		ok:         runOK,
		errMsg:     errMsg,
		durationMs: func() int { return 1 },
		runID:      "1857-settings-neutral",
	}
	resp, _, _ := ra.buildResponse()
	require.NotNil(t, resp.Error, "SETUP: a failed test must carry its text")
	return resp.Ok, *resp.Error
}

func TestSettingsNeutralVoice1857(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		cli     string
		model   string
		code    string
		variant string
		facts   map[string]string
	}{
		{"duplicated_flag_cli_fact", test1857SettingsDupFlag, "codex", "", "external_cli_invalid_args", "",
			map[string]string{"cli": "Codex"}},
		{"model_unavailable_cli_and_model", test1857SettingsEnvelope, "codex", "test-model-1857", "external_cli_model_unavailable", "",
			map[string]string{"cli": "Codex", "model": "test-model-1857"}},
		{"turn_limit_n_from_the_cap", "turn cap exceeded: 3 turns (max 2)", "codex", "", "external_cli_turn_limit", "",
			map[string]string{"cli": "Codex", "n": "2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, shown := gw1857Settings(t, tc.raw, tc.cli, tc.model)
			assert.False(t, ok, "SETUP: the Settings test must report a failed test")
			assert.False(t, gw1857HasFirstPerson(shown),
				"D-B Settings surface: first-person token reached the Settings result: %q", shown)
			assert.NotRegexp(t, gw1857Slot, shown, "D-B Settings surface: literal slot in %q", shown)
			want := gw1857Fill(t, gw1857Neutral(t, tc.code, tc.variant), tc.facts)
			assert.Equal(t, want, shown,
				"D-B Settings surface: text must equal the neutral entry for %s rendered with the same facts", tc.code)
		})
	}

	t.Run("missing_fact_gives_generic_sentence", func(t *testing.T) {
		// model_unavailable with no model fact: the {model} slot has no value,
		// so the Settings result is the generic sentence, never a literal slot.
		ok, shown := gw1857Settings(t, test1857SettingsEnvelope, "codex", "")
		assert.False(t, ok)
		assert.Equal(t, generated.LLMErrorUserMessages["unknown"], shown,
			"D-B missing fact: the Settings result must be the generic sentence")
		assert.NotRegexp(t, gw1857Slot, shown)
	})
}

// ─── R1: live frame and reloaded frame agree ────────────────────────────────

func TestReplayMatchesLiveAssembledSentence1857(t *testing.T) {
	facts := map[string]string{"cli": "Claude Code", "model": "test-model-1857", "n": "7", "minutes": "3"}

	type sentence struct{ code, variant, text string }
	var all []sentence
	for _, code := range generated.LLMErrorCodes {
		if s := generated.LLMErrorUserMessages[code]; gw1857Slot.MatchString(s) {
			all = append(all, sentence{code, "", s})
		}
		for variant, s := range generated.LLMErrorMessageVariants[code] {
			if gw1857Slot.MatchString(s) {
				all = append(all, sentence{code, variant, s})
			}
		}
	}
	require.GreaterOrEqual(t, len(all), 9, "SETUP: slotted sentences in the generated catalogue")

	// The persisted record is what the live frame says (the agent half pins
	// that the writer stores exactly this; see pkg/agent
	// TestChatVoiceAndPersistedRecord1857). Replay must hand the SPA the same
	// flag and text.
	check := func(t *testing.T, code, message string) {
		t.Helper()
		live := agent.WireLLMError(agent.ErrorPayload{Code: code, Message: message})
		entry := session.TranscriptEntry{
			ID: "1857-" + code, Type: session.EntryTypeSystem, Status: "error",
			Content: live.Message, ErrorCode: live.Code,
		}
		if live.ProviderMessage != nil {
			entry.ProviderMessage = *live.ProviderMessage
		}
		frame := buildReplayErrorFrame("session_1857", entry)
		require.NotNil(t, frame.Payload, "SETUP: replay payload for %s", code)
		replay := frame.Payload.LlmError

		assert.NotRegexp(t, gw1857Slot, replay.Message, "D-A replay: literal slot on the reloaded frame for %s", code)
		if assert.NotNil(t, live.ProviderMessage,
			"D-A server layer: the live frame for %s must carry the flag so the reload can too", code) &&
			assert.NotNil(t, replay.ProviderMessage, "D-A replay: the reloaded frame for %s must carry the flag", code) {
			assert.Equal(t, *live.ProviderMessage, *replay.ProviderMessage, "D-A replay: flag parity for %s", code)
		}
		assert.Equal(t, live.Message, replay.Message, "D-A replay: text parity for %s", code)
	}

	t.Run("filled_sentences", func(t *testing.T) {
		for _, s := range all {
			check(t, s.code, gw1857Fill(t, s.text, facts))
		}
	})
	t.Run("literal_slot_sentences_become_generic_on_both", func(t *testing.T) {
		for _, s := range all {
			live := agent.WireLLMError(agent.ErrorPayload{Code: s.code, Message: s.text})
			assert.Equal(t, generated.LLMErrorUserMessages["unknown"], live.Message,
				"D-A server layer: literal {cli} reached the wire for %s/%q", s.code, s.variant)
			check(t, s.code, s.text)
		}
	})

	t.Run("control_unflagged_legacy_record_is_not_promoted", func(t *testing.T) {
		// A record written before this fix (slotted code, literal text, no
		// flag) must replay WITHOUT the trust flag, so the SPA guard (D-A SPA
		// layer) shows the generic sentence rather than the stored text.
		legacy := session.TranscriptEntry{
			ID: "1857-legacy", Type: session.EntryTypeSystem, Status: "error",
			Content: all[0].text, ErrorCode: all[0].code,
		}
		frame := buildReplayErrorFrame("session_1857", legacy)
		require.NotNil(t, frame.Payload)
		assert.Nil(t, frame.Payload.LlmError.ProviderMessage, "D-A replay: replay must never invent the flag")
	})
}
