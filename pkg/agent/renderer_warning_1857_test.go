// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// renderer_warning_1857_test.go — #1857 frontend round, fix round 1 (CHECK F4).
//
// Design note /workspace/logs/1857/arch/design-note-frontend-round.md, D-A
// server layer: "`renderMessageSlots` gets the same log line on its existing
// fallback branch, which closes the rest of F7." The line names the code and
// the missing slot names, never their values (spec notifications-spec.md
// §13.2, F7). The boundary test in assembled_message_1857_test.go only sees a
// finished generic sentence; these tests capture the log on the REAL
// missing-fact rendering paths:
//
//	B3 with no wait fact        TranslateTurnError(&agentRequestLimitError{})
//	CLI slot, pre-run refusal   newExternalCLIRefusal with no CLI name
//	CLI {model} slot, run       sanitizeRunnerErrorTyped(...).renderForRun
//	whole fatal run             runExternalCLISubTurn: live frame, persisted
//	                            record read back, task centre and parent text
//
// Expected sentences come from the generated catalogue; no sentence literal.
// ASSUMPTION: the warning is logged by the renderer path (or the translate
// function around it), so it is captured around those calls and not only at
// WireLLMError. The test asserts only on what the line names: the code and the
// missing slot names.

package agent

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// rw1857Lines returns the captured log lines that name the code.
func rw1857Lines(log, code string) []string {
	var out []string
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, code) {
			out = append(out, line)
		}
	}
	return out
}

// rw1857AssertOneWarning asserts exactly one line names the code and that it
// names every missing slot and none of the forbidden values.
func rw1857AssertOneWarning(t *testing.T, where, log, code string, missing, forbidden []string) {
	t.Helper()
	lines := rw1857Lines(log, code)
	if !assert.Len(t, lines, 1,
		"D-A renderer (F7) %s: exactly one warning line naming %s on the missing-fact fallback, got %d", where, code, len(lines)) {
		return
	}
	// Strip the code so a slot name such as cli is not satisfied by the code text.
	rest := strings.ReplaceAll(lines[0], code, "")
	for _, name := range missing {
		assert.Regexp(t, regexp.MustCompile(`\{`+name+`\}|\b`+name+`\b`), rest,
			"D-A renderer (F7) %s: the warning must name the missing slot %q", where, name)
	}
	for _, value := range forbidden {
		assert.NotContains(t, lines[0], value,
			"D-A renderer (F7) %s: the warning must never carry a slot value or the sentence", where)
	}
}

func TestRendererWarnsOnMissingFact1857(t *testing.T) {
	generic := generated.LLMErrorUserMessages["unknown"]

	t.Run("b3_no_wait_fact", func(t *testing.T) {
		template := generated.LLMErrorUserMessages["agent_request_limit"]
		require.Contains(t, template, "{minutes}", "SETUP: agent_request_limit must carry the {minutes} slot")
		readLog := captureAgentLog(t)
		llm := TranslateTurnError(&agentRequestLimitError{policyRule: "agent_llm_calls_per_hour", retryAfterSeconds: 0})
		assert.Equal(t, generic, llm.Message, "SETUP: a missing wait fact gives the generic sentence")
		// The wire boundary sees only the finished sentence and must not add a second line.
		_ = WireLLMError(ErrorPayload{Code: string(llm.Code), Message: llm.Message})
		rw1857AssertOneWarning(t, "B3 {minutes}", readLog(), "agent_request_limit",
			[]string{"minutes"}, []string{template, generic})
	})

	t.Run("cli_refusal_with_no_cli_name", func(t *testing.T) {
		const code = "external_cli_invalid_args"
		template := generated.LLMErrorUserMessages[code]
		require.Contains(t, template, "{cli}", "SETUP: %s must carry the {cli} slot", code)
		readLog := captureAgentLog(t)
		refusal := newExternalCLIRefusal(LLMErrorCode(code), "", "", errors.New("cause"))
		assert.Equal(t, generic, refusal.text, "SETUP: a missing CLI name gives the generic sentence")
		rw1857AssertOneWarning(t, "CLI refusal {cli}", readLog(), code,
			[]string{"cli"}, []string{template, generic})
	})

	t.Run("cli_model_slot_without_model", func(t *testing.T) {
		const code = "external_cli_model_unavailable"
		template := generated.LLMErrorUserMessages[code]
		require.Contains(t, template, "{model}", "SETUP: %s must carry the {model} slot", code)
		readLog := captureAgentLog(t)
		rendered := sanitizeRunnerErrorTyped(test1857C2Envelope, "").renderForRun("codex", "")
		assert.Equal(t, generic, rendered.AssistantText, "SETUP: a missing model gives the generic sentence")
		// The CLI name is a fact that WAS known: it must not be logged as a value.
		rw1857AssertOneWarning(t, "CLI run {model}", readLog(), code,
			[]string{"model"}, []string{CLIDisplayName("codex"), template, generic})
	})

	t.Run("whole_fatal_run_logs_once_and_persists_the_generic_sentence", func(t *testing.T) {
		const code = "external_cli_model_unavailable"
		readLog := captureAgentLog(t)
		run := am1857DriveFatal(t, test1857C2Envelope, "")
		log := readLog()

		// One warning for the whole run: the renderer logs at its fallback and
		// the later boundaries (live frame, persisted record) see a finished
		// generic sentence, so they add nothing.
		rw1857AssertOneWarning(t, "whole run", log, code, []string{"model"}, []string{CLIDisplayName("codex")})

		// Persisted-fallback boundary: the entry stored and read back from the
		// transcript carries the generic sentence, no slot, and the trust flag
		// so the reloaded frame shows it as sent.
		assert.Equal(t, generic, run.errEntry.Content,
			"D-A persisted fallback: the stored entry for a missing fact must hold the generic sentence")
		assert.NotRegexp(t, am1857Slot, run.errEntry.Content, "D-A persisted fallback: literal slot in the stored entry")
		assert.True(t, run.errEntry.ProviderMessage,
			"D-A persisted fallback: the stored generic sentence must carry the flag so a reload shows it as sent")
		assert.Equal(t, code, run.errEntry.ErrorCode, "D-A persisted fallback: the stored code stays the classified code")

		wire := WireLLMError(*run.payload)
		assert.Equal(t, generic, wire.Message, "D-A live frame: the missing-fact run shows the generic sentence")
		assert.Equal(t, run.errEntry.Content, wire.Message, "D-A: live frame and stored record say the same")
	})
}
