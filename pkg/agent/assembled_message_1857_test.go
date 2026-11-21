// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// assembled_message_1857_test.go — #1857 frontend round, RED tests (agent half).
//
// Requirements covered (brief /workspace/logs/1857/fe-red-brief.md):
//
//	R1  D-A server layer: WireLLMError and the persisted record trust a
//	    correctly filled slotted catalogue sentence (flag true, byte-equal) and
//	    turn anything else into the generic sentence (flag true, one warning
//	    line naming the code and the missing slot names, never their values).
//	R5  D-B surfaces: task centre, parent tool result, completion message use
//	    the NEUTRAL form of the catalogue sentence; chat frame and persisted
//	    record use the chat form; a missing fact gives the generic sentence on
//	    both voices.
//	R6  C3: copy rules on the neutral forms of the new codes.
//
// Oracles: spec notifications-spec.md §13.2 / §13.8.3 / §13.8.4 / §11.4 and the
// chief-ruled design note /workspace/logs/1857/arch/design-note-frontend-round.md
// (D-A option 1, D-B B1). The expected CHAT sentence is the generated Go
// catalogue (generated.LLMErrorUserMessages / LLMErrorMessageVariants) filled
// with the test facts below. The expected NEUTRAL sentence is the contract's
// `neutral` / `neutral_variants` entry (contracts/components/schemas/LLMError.yaml,
// the source the generated NeutralMessages map is emitted from), read with a
// YAML parser, so the test compiles before the generated symbol exists. No
// sentence literal appears in this file (W1-W21 are under founder review).
//
// ASSUMPTION (declared per the brief): the neutral twin of a code whose chat
// form has no first-person token may be absent from the contract; such a code
// renders its chat sentence on both voices (design note D-B: "Codes whose
// single sentence has no first person ... need no twin").

package agent

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ─── shared oracle helpers ───────────────────────────────────────────────────

var (
	am1857Slot        = regexp.MustCompile(`\{([a-z_]+)\}`)
	am1857FirstPerson = regexp.MustCompile(`\bI\b`)
	am1857MeMy        = regexp.MustCompile(`(?i)\b(?:me|my)\b`)
)

// am1857Facts are the test facts every slotted sentence is filled with.
// The CLI display name for driver key "codex" is the closed map's value
// (spec §13.8.3 preamble: "Claude Code", "Codex", "OpenCode").
var am1857Facts = map[string]string{
	"cli":     "Claude Code",
	"model":   "test-model-1857",
	"n":       "7",
	"minutes": "3",
}

func am1857Fill(t *testing.T, template string, facts map[string]string) string {
	t.Helper()
	return am1857Slot.ReplaceAllStringFunc(template, func(slot string) string {
		name := slot[1 : len(slot)-1]
		value, ok := facts[name]
		if !ok {
			t.Fatalf("SETUP: slot {%s} has no test fact", name)
		}
		return value
	})
}

func am1857SlotNames(template string) []string {
	matches := am1857Slot.FindAllStringSubmatch(template, -1)
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, m[1])
	}
	return names
}

func am1857HasFirstPerson(text string) bool {
	return am1857FirstPerson.MatchString(text) || am1857MeMy.MatchString(text)
}

// am1857ChatSentence is a code's generated chat sentence (variant "" = the code's own).
func am1857ChatSentence(code, variant string) string {
	if variant == "" {
		return generated.LLMErrorUserMessages[code]
	}
	return generated.LLMErrorMessageVariants[code][variant]
}

type am1857Sentence struct {
	code    string
	variant string
	text    string
}

// am1857SlottedSentences lists every catalogue sentence (message and variants)
// of every code that carries a slot, straight from the generated catalogue.
func am1857SlottedSentences() []am1857Sentence {
	var out []am1857Sentence
	for _, code := range generated.LLMErrorCodes {
		if s := generated.LLMErrorUserMessages[code]; am1857Slot.MatchString(s) {
			out = append(out, am1857Sentence{code, "", s})
		}
		for variant, s := range generated.LLMErrorMessageVariants[code] {
			if am1857Slot.MatchString(s) {
				out = append(out, am1857Sentence{code, variant, s})
			}
		}
	}
	return out
}

type am1857ContractEntry struct {
	Message         string            `yaml:"message"`
	Attribution     string            `yaml:"attribution"`
	Neutral         string            `yaml:"neutral"`
	Variants        map[string]string `yaml:"variants"`
	NeutralVariants map[string]string `yaml:"neutral_variants"`
}

func am1857Contract(t *testing.T) map[string]am1857ContractEntry {
	t.Helper()
	path := filepath.Join("..", "..", "contracts", "components", "schemas", "LLMError.yaml")
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "SETUP: reading the contract source of the catalogue")
	var doc struct {
		Messages map[string]am1857ContractEntry `yaml:"x-user-messages"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc), "SETUP: parsing the contract")
	require.NotEmpty(t, doc.Messages, "SETUP: contract x-user-messages must parse")
	return doc.Messages
}

// am1857NeutralSentence is the oracle for the neutral voice: the contract's
// neutral twin, or the chat sentence itself when it has no first person.
func am1857NeutralSentence(t *testing.T, code, variant string) string {
	t.Helper()
	entry := am1857Contract(t)[code]
	twin := entry.Neutral
	if variant != "" {
		twin = entry.NeutralVariants[variant]
	}
	if twin != "" {
		return twin
	}
	chat := am1857ChatSentence(code, variant)
	if !am1857HasFirstPerson(chat) {
		return chat
	}
	t.Fatalf("D-B B1 neutral twin: the contract has no neutral twin for %s (variant %q) although its chat form is first person: %q", code, variant, chat)
	return ""
}

// ─── R1: D-A server layer on WireLLMError ───────────────────────────────────

func TestAssembledSentenceWire1857(t *testing.T) {
	sentences := am1857SlottedSentences()
	require.GreaterOrEqual(t, len(sentences), 9, "SETUP: the generated catalogue must expose the slotted sentences")

	t.Run("filled_sentence_is_flagged_and_byte_equal", func(t *testing.T) {
		for _, s := range sentences {
			filled := am1857Fill(t, s.text, am1857Facts)
			wire := WireLLMError(ErrorPayload{Code: s.code, Message: filled})
			if assert.NotNil(t, wire.ProviderMessage,
				"D-A server layer: a correctly filled %s/%q sentence must carry the trust flag, got none (the SPA would show the generic sentence)", s.code, s.variant) {
				assert.True(t, *wire.ProviderMessage, "D-A server layer: flag must be true for %s/%q", s.code, s.variant)
			}
			assert.Equal(t, filled, wire.Message,
				"D-A server layer: the filled sentence for %s/%q must reach the wire byte for byte", s.code, s.variant)
		}
	})

	t.Run("literal_slot_becomes_generic_with_flag_and_one_warning", func(t *testing.T) {
		generic := generated.LLMErrorUserMessages["unknown"]
		for _, s := range sentences {
			// Partially filled where possible: {cli} is filled so a value could
			// leak into the log; every other slot stays literal.
			partial := strings.ReplaceAll(s.text, "{cli}", am1857Facts["cli"])
			for _, message := range []string{s.text, partial} {
				missing := am1857SlotNames(message)
				if len(missing) == 0 {
					continue
				}
				readLog := captureAgentLog(t)
				wire := WireLLMError(ErrorPayload{Code: s.code, Message: message})

				assert.Equal(t, generic, wire.Message,
					"D-A server layer: literal {cli} reached the wire — %s/%q with an unfilled slot must become the generic sentence", s.code, s.variant)
				if assert.NotNil(t, wire.ProviderMessage,
					"D-A server layer: the generic sentence is a full sentence and must carry the flag (%s/%q)", s.code, s.variant) {
					assert.True(t, *wire.ProviderMessage, "D-A server layer: flag true on the generic fallback (%s/%q)", s.code, s.variant)
				}

				var warnLines []string
				for _, line := range strings.Split(readLog(), "\n") {
					if strings.Contains(line, s.code) {
						warnLines = append(warnLines, line)
					}
				}
				if !assert.Len(t, warnLines, 1,
					"D-A server layer (F7): exactly one warning line naming %s must be logged for an unfilled slot, got %d", s.code, len(warnLines)) {
					continue
				}
				line := warnLines[0]
				for _, name := range missing {
					assert.Regexp(t, `\{`+name+`\}|\b`+name+`\b`, line,
						"D-A server layer (F7): the warning must name the missing slot %q", name)
				}
				assert.NotContains(t, line, am1857Facts["cli"],
					"D-A server layer (F7): the warning must never carry a slot VALUE")
				assert.NotContains(t, line, message,
					"D-A server layer (F7): the warning must never carry the message text")
			}
		}
	})

	t.Run("text_that_is_not_the_codes_sentence_becomes_generic", func(t *testing.T) {
		// A slotted code carrying text that is none of its sentences must not
		// ride the wire as assembled text (D-A: provably the catalogue sentence).
		const foreign = "completely unrelated provider-ish text 1857"
		for _, code := range []string{"external_cli_invalid_args", "agent_request_limit"} {
			wire := WireLLMError(ErrorPayload{Code: code, Message: foreign})
			assert.Equal(t, generated.LLMErrorUserMessages["unknown"], wire.Message,
				"D-A server layer: foreign text on slotted code %s must not reach the wire", code)
			if assert.NotNil(t, wire.ProviderMessage, "D-A server layer: the generic fallback is flagged (%s)", code) {
				assert.True(t, *wire.ProviderMessage)
			}
		}
	})

	t.Run("control_slot_free_code_is_unchanged", func(t *testing.T) {
		// Today's behaviour for a code whose sentence has no slots: untouched,
		// no flag without provider identity (design note D-A, first bullet).
		code := "rate_limited"
		wire := WireLLMError(ErrorPayload{Code: code, Message: generated.LLMErrorUserMessages[code]})
		assert.Equal(t, generated.LLMErrorUserMessages[code], wire.Message)
		assert.Nil(t, wire.ProviderMessage, "D-A control: a slot-free code without provider identity must not gain the flag")
	})
}

// ─── R1: the B3 {minutes} path ──────────────────────────────────────────────

func TestAgentRequestLimitMinutesWire1857(t *testing.T) {
	template := generated.LLMErrorUserMessages["agent_request_limit"]
	require.Contains(t, template, "{minutes}", "SETUP: agent_request_limit must carry the {minutes} slot")
	generic := generated.LLMErrorUserMessages["unknown"]

	cases := []struct {
		name    string
		retry   float64
		minutes string // oracle: the limiter's wait in seconds, rounded UP to whole minutes
	}{
		{"150s_is_3_minutes", 150, "3"},
		{"61s_is_2_minutes", 61, "2"},
		{"60s_is_1_minute", 60, "1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			llm := TranslateTurnError(&agentRequestLimitError{policyRule: "agent_llm_calls_per_hour", retryAfterSeconds: tc.retry})
			assert.Equal(t, "agent_request_limit", string(llm.Code), "SETUP: B3 must carry its own code")
			// The seam mirrors the emit sites in loop_run_turn.go
			// (ErrorPayload{Code: string(llm.Code), Message: llm.Message}).
			wire := WireLLMError(ErrorPayload{Code: string(llm.Code), Message: llm.Message})
			want := am1857Fill(t, template, map[string]string{"minutes": tc.minutes})
			assert.Equal(t, want, wire.Message, "D-A server layer (B3 {minutes}): the filled sentence must reach the wire")
			if assert.NotNil(t, wire.ProviderMessage,
				"D-A server layer (B3 {minutes}): the filled agent_request_limit sentence must carry the flag") {
				assert.True(t, *wire.ProviderMessage)
			}
		})
	}

	t.Run("no_wait_fact_is_generic_and_flagged", func(t *testing.T) {
		llm := TranslateTurnError(&agentRequestLimitError{policyRule: "agent_llm_calls_per_hour", retryAfterSeconds: 0})
		wire := WireLLMError(ErrorPayload{Code: string(llm.Code), Message: llm.Message})
		assert.Equal(t, generic, wire.Message, "D-A server layer (B3): a missing {minutes} fact gives the generic sentence")
		if assert.NotNil(t, wire.ProviderMessage, "D-A server layer (B3): the generic fallback is flagged") {
			assert.True(t, *wire.ProviderMessage)
		}
	})
}

// ─── fatal-run harness (mirrors TestCLIFailureAdaptersEndToEnd) ─────────────

type am1857Run struct {
	res      *tools.ToolResult
	err      error
	payload  *ErrorPayload
	errEntry *session.TranscriptEntry
}

func am1857DriveFatal(t *testing.T, raw, model string) am1857Run {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	al, ts := newExternalTestLoop(t, "codex", "")
	ts.agent.Model = model

	st, sessionID := new1857ExternalStore(t, home)
	ts.transcriptStore = st
	ts.transcriptSessionID = sessionID

	collector := &test1857ErrorEvents{}
	al.SetEventSyncTap(collector.tap)
	t.Cleanup(func() { al.SetEventSyncTap(nil) })

	fr, restore := withFakeDriver(t)
	defer restore()
	go func() {
		require.NoError(t, fr.InjectEvent(runner.RunEvent{
			Kind: runner.EventKindError,
			Err:  &runner.ErrorEvent{Message: raw, Fatal: true},
		}))
		fr.Cancel()
	}()

	res, err := runExternalCLISubTurn(context.Background(), al, ts, "task")
	require.Error(t, err, "SETUP: a fatal CLI error must fail the run")
	require.NoError(t, st.Close())
	entries, readErr := st.ReadTranscript(sessionID)
	require.NoError(t, readErr, "SETUP: reading back the persisted transcript")

	run := am1857Run{res: res, err: err}
	payloads := collector.errorPayloads()
	for i := range payloads {
		if payloads[i].Stage == "external_cli" {
			run.payload = &payloads[i]
			break
		}
	}
	require.NotNil(t, run.payload, "SETUP: the external_cli error event must be emitted")
	for i := range entries {
		if entries[i].ErrorCode != "" {
			run.errEntry = &entries[i]
			break
		}
	}
	require.NotNil(t, run.errEntry, "SETUP: the classified error entry must be persisted")
	return run
}

type am1857Case struct {
	name    string
	raw     string
	model   string
	code    string
	variant string
	facts   map[string]string
}

var am1857Cases = []am1857Case{
	{"invalid_args_cli_fact", test1857C1DuplicateFlag, "", "external_cli_invalid_args", "",
		map[string]string{"cli": "Codex"}},
	{"model_unavailable_cli_and_model", test1857C2Envelope, "test-model-1857", "external_cli_model_unavailable", "",
		map[string]string{"cli": "Codex", "model": "test-model-1857"}},
	{"turn_limit_n_from_the_cap", test1857C5TurnCap, "", "external_cli_turn_limit", "",
		map[string]string{"cli": "Codex", "n": "2"}},
	{"stream_read_variant", test1857C6Transport, "", "external_cli_failed", "stream_read",
		map[string]string{"cli": "Codex"}},
}

func am1857AssertNeutral(t *testing.T, surface, text string, want func(*testing.T) string) {
	t.Helper()
	assert.False(t, am1857HasFirstPerson(text),
		"D-B surface %s: first-person token reached a neutral surface: %q", surface, text)
	assert.NotRegexp(t, am1857Slot, text, "D-B surface %s: literal slot in %q", surface, text)
	assert.Contains(t, text, want(t),
		"D-B surface %s: text must contain the neutral catalogue entry rendered with the same facts", surface)
}

// ─── R1 persisted + R5 chat voice and the three agent-side neutral surfaces ─

func TestChatVoiceAndPersistedRecord1857(t *testing.T) {
	for _, tc := range am1857Cases {
		t.Run(tc.name, func(t *testing.T) {
			run := am1857DriveFatal(t, tc.raw, tc.model)
			chat := am1857Fill(t, am1857ChatSentence(tc.code, tc.variant), tc.facts)

			assert.Equal(t, tc.code, run.payload.Code, "SETUP: wire code")
			wire := WireLLMError(*run.payload)
			assert.Equal(t, chat, wire.Message,
				"D-B chat voice: the chat frame must carry the chat entry rendered with the facts")
			if assert.NotNil(t, wire.ProviderMessage,
				"D-A server layer: the live frame for %s must carry the trust flag", tc.code) {
				assert.True(t, *wire.ProviderMessage)
				assert.Equal(t, *wire.ProviderMessage, run.errEntry.ProviderMessage, "D-A: live/persisted flag parity")
			}

			assert.Equal(t, chat, run.errEntry.Content,
				"D-B chat voice: the persisted text must equal the chat entry rendered with the same facts")
			assert.True(t, run.errEntry.ProviderMessage,
				"D-A server layer (persisted record): the persisted entry must carry the same flag as the live frame, so a reload shows the same text")
			assert.Equal(t, wire.Message, run.errEntry.Content, "D-A: live/persisted text parity")
		})
	}
}

func TestNeutralVoiceSurfaces1857(t *testing.T) {
	for _, tc := range am1857Cases {
		t.Run(tc.name, func(t *testing.T) {
			run := am1857DriveFatal(t, tc.raw, tc.model)
			want := func(t *testing.T) string {
				t.Helper()
				return am1857Fill(t, am1857NeutralSentence(t, tc.code, tc.variant), tc.facts)
			}

			t.Run("task_centre_turnErrorUserText", func(t *testing.T) {
				got := turnErrorUserText(run.err)
				assert.False(t, am1857HasFirstPerson(got),
					"D-B task centre: first-person token reached task_failed body: %q", got)
				assert.Equal(t, want(t), got,
					"D-B task centre: text must equal the neutral entry rendered with the same facts")
			})

			t.Run("parent_tool_result_drainExternalRun", func(t *testing.T) {
				require.NotNil(t, run.res, "SETUP: tool result")
				assert.Contains(t, run.res.ForLLM, tc.code, "D-B parent tool result: typed code must stay")
				am1857AssertNeutral(t, "parent tool result", run.res.ForLLM, want)
			})

			t.Run("completion_message_completionMessage", func(t *testing.T) {
				outcome, _, reason := completionDisposition(turnResult{status: TurnEndStatusError}, run.err, "")
				require.Equal(t, steer.OutcomeFailed, outcome, "SETUP: outcome")
				al, _ := newGoalLoopTestLoop(t, &noCallProvider{t: t}, nil)
				rec := &session.LifecycleRecord{SessionID: "1857-neutral-session", AgentID: "ext-agent", Generation: 1}
				msg, err := al.completionMessage(rec, outcome, "", reason)
				require.NoError(t, err, "SETUP: completion message")
				errVariant, asErr := msg.AsSessionMessageError()
				require.NoError(t, asErr, "SETUP: error variant")
				am1857AssertNeutral(t, "completion message", errVariant.Text, want)
			})
		})
	}

	t.Run("missing_fact_gives_generic_on_both_voices", func(t *testing.T) {
		// model_unavailable without the model Omnipus passed: {model} has no
		// fact, so BOTH forms collapse to the generic sentence together.
		run := am1857DriveFatal(t, test1857C2Envelope, "")
		generic := generated.LLMErrorUserMessages["unknown"]

		assert.Equal(t, generic, turnErrorUserText(run.err), "D-B missing fact: task centre must show the generic sentence")
		assert.Contains(t, run.res.ForLLM, generic, "D-B missing fact: parent tool result must carry the generic sentence")
		assert.NotRegexp(t, am1857Slot, run.res.ForLLM, "D-B missing fact: literal slot in the tool result")

		assert.Equal(t, generic, WireLLMError(*run.payload).Message, "D-B missing fact: the chat frame must show the generic sentence")
		assert.Equal(t, generic, run.errEntry.Content, "D-B missing fact: the persisted chat text must be the generic sentence")

		_, _, reason := completionDisposition(turnResult{status: TurnEndStatusError}, run.err, "")
		assert.Contains(t, reason, generic, "D-B missing fact: completion reason must carry the generic sentence")
		assert.NotRegexp(t, am1857Slot, reason, "D-B missing fact: literal slot in the completion reason")
	})
}

// ─── R6 (Go half): copy rules on the neutral forms ──────────────────────────

func TestNeutralCopyRules1857(t *testing.T) {
	contract := am1857Contract(t)
	vocabulary := map[string]bool{"cli": true, "model": true, "n": true, "minutes": true}
	banned := []string{"switch models", "switch model.", "different model", "another model", "pick a model"}
	checked := 0

	for _, code := range generated.LLMErrorCodes {
		chatForms := map[string]string{"": generated.LLMErrorUserMessages[code]}
		for variant, text := range generated.LLMErrorMessageVariants[code] {
			chatForms[variant] = text
		}
		attribution := string(generated.LLMErrorUserAttributions[code])
		for variant, chat := range chatForms {
			if !am1857HasFirstPerson(chat) {
				continue // no twin needed: one sentence serves both voices
			}
			entry := contract[code]
			neutral := entry.Neutral
			if variant != "" {
				neutral = entry.NeutralVariants[variant]
			}
			if !assert.NotEmpty(t, neutral,
				"D-B B1 / C3: %s (variant %q) has a first-person chat form and needs a neutral twin in the contract", code, variant) {
				continue
			}
			checked++
			assert.False(t, am1857HasFirstPerson(neutral), "D-B: the neutral twin of %s/%q carries a first-person token: %q", code, variant, neutral)
			// Same facts as the chat form: identical slot sets, closed vocabulary.
			assert.ElementsMatch(t, am1857SlotNames(chat), am1857SlotNames(neutral),
				"D-B: %s/%q neutral twin must name the same facts as the chat form", code, variant)
			for _, name := range am1857SlotNames(neutral) {
				assert.True(t, vocabulary[name], "D-B: %s/%q neutral twin uses slot {%s} outside the closed vocabulary", code, variant, name)
			}
			lower := strings.ToLower(neutral)
			if attribution == "config" || attribution == "product" {
				for _, phrase := range banned {
					assert.NotContains(t, lower, phrase, "C3: %s (%s) neutral form shops for a model", code, attribution)
				}
			}
			if attribution == "config" {
				for _, phrase := range []string{"retry", "try again"} {
					assert.NotContains(t, lower, phrase, "C3: %s is a config fault; its neutral form must not say %q", code, phrase)
				}
			}
		}
	}
	assert.Positive(t, checked, "C3 / D-B: no neutral twin exists in the contract yet (first-person chat forms found none)")
}
