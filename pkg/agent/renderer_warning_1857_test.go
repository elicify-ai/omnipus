// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// renderer_warning_1857_test.go — #1857 frontend round, fix rounds 1 and 2
// (CHECK F4).
//
// Design note /workspace/logs/1857/arch/design-note-frontend-round.md, D-A
// server layer: "`renderMessageSlots` gets the same log line on its existing
// fallback branch, which closes the rest of F7." The line names the code and
// the missing slot names, never their values (spec notifications-spec.md
// §13.2, F7).
//
// Scoping rule (fix round 2): the renderer and the wire/persist boundaries are
// independent sites that may each log. So
//   - each renderer warning is asserted in a capture around the renderer call
//     ALONE, before any boundary is invoked, so a boundary cannot supply the
//     renderer's evidence;
//   - boundary warnings are captured separately and checked on content only;
//   - nothing counts lines across a whole run: the design requires no
//     run-wide de-duplication.
// A direct renderer invocation is where the design specifies one line, so a
// per-call count of exactly one is kept there.
//
// Expected sentences come from the generated catalogue; no sentence literal.
// ASSUMPTION: the renderer-side line names the code (the design says "the same
// log line" as the boundary), so the renderer path is expected to know it.

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

// rw1857AssertSafeLine asserts one warning line names every missing slot and
// carries none of the forbidden values or sentences.
func rw1857AssertSafeLine(t *testing.T, where, line, code string, missing, forbidden []string) {
	t.Helper()
	// Strip the code so a slot name such as cli is not satisfied by the code text.
	rest := strings.ReplaceAll(line, code, "")
	for _, name := range missing {
		assert.Regexp(t, regexp.MustCompile(`\{`+name+`\}|\b`+name+`\b`), rest,
			"D-A renderer (F7) %s: the warning must name the missing slot %q", where, name)
	}
	for _, value := range forbidden {
		assert.NotContains(t, line, value,
			"D-A renderer (F7) %s: the warning must never carry a slot value or the sentence", where)
	}
}

// rw1857AssertRendererWarning asserts that the capture, taken around the
// renderer call alone, holds exactly one line naming the code, and that the
// line is safe.
func rw1857AssertRendererWarning(t *testing.T, where, log, code string, missing, forbidden []string) {
	t.Helper()
	lines := rw1857Lines(log, code)
	if !assert.Len(t, lines, 1,
		"D-A renderer (F7) %s: the renderer call alone must log exactly one warning naming %s on its missing-fact fallback, got %d", where, code, len(lines)) {
		return
	}
	rw1857AssertSafeLine(t, where, lines[0], code, missing, forbidden)
}

// rw1857AssertBoundaryLinesSafe checks boundary diagnostics on content only:
// whatever lines name the code must be safe. No count: whether a boundary logs
// for an already-generic input is not specified.
func rw1857AssertBoundaryLinesSafe(t *testing.T, where, log, code string, forbidden []string) {
	t.Helper()
	for _, line := range rw1857Lines(log, code) {
		rw1857AssertSafeLine(t, where, line, code, nil, forbidden)
	}
}

func TestRendererWarnsOnMissingFact1857(t *testing.T) {
	generic := generated.LLMErrorUserMessages["unknown"]

	t.Run("b3_no_wait_fact", func(t *testing.T) {
		const code = "agent_request_limit"
		template := generated.LLMErrorUserMessages[code]
		require.Contains(t, template, "{minutes}", "SETUP: agent_request_limit must carry the {minutes} slot")

		// (a) The renderer's warning, captured around the translate call alone,
		// before WireLLMError exists in this test.
		readRenderer := captureAgentLog(t)
		llm := TranslateTurnError(&agentRequestLimitError{policyRule: "agent_llm_calls_per_hour", retryAfterSeconds: 0})
		rendererLog := readRenderer()
		assert.Equal(t, generic, llm.Message, "SETUP: a missing wait fact gives the generic sentence")
		rw1857AssertRendererWarning(t, "B3 {minutes} renderer", rendererLog, code,
			[]string{"minutes"}, []string{template, generic})

		// (b) The boundary, in its own capture: evidence about the boundary
		// only, content rules only.
		readBoundary := captureAgentLog(t)
		wire := WireLLMError(ErrorPayload{Code: string(llm.Code), Message: llm.Message})
		boundaryLog := readBoundary()
		assert.Equal(t, generic, wire.Message, "D-A boundary: the generic sentence reaches the wire")
		if assert.NotNil(t, wire.ProviderMessage, "D-A boundary: the generic fallback is flagged") {
			assert.True(t, *wire.ProviderMessage)
		}
		rw1857AssertBoundaryLinesSafe(t, "B3 boundary", boundaryLog, code, []string{template, generic})
	})

	t.Run("cli_refusal_with_no_cli_name", func(t *testing.T) {
		const code = "external_cli_invalid_args"
		template := generated.LLMErrorUserMessages[code]
		require.Contains(t, template, "{cli}", "SETUP: %s must carry the {cli} slot", code)
		readLog := captureAgentLog(t)
		refusal := newExternalCLIRefusal(LLMErrorCode(code), "", "", errors.New("cause"))
		log := readLog()
		assert.Equal(t, generic, refusal.text, "SETUP: a missing CLI name gives the generic sentence")
		rw1857AssertRendererWarning(t, "CLI refusal {cli}", log, code,
			[]string{"cli"}, []string{template, generic})
	})

	t.Run("cli_model_slot_without_model", func(t *testing.T) {
		const code = "external_cli_model_unavailable"
		template := generated.LLMErrorUserMessages[code]
		require.Contains(t, template, "{model}", "SETUP: %s must carry the {model} slot", code)
		readLog := captureAgentLog(t)
		rendered := sanitizeRunnerErrorTyped(test1857C2Envelope, "").renderForRun("codex", "")
		log := readLog()
		assert.Equal(t, generic, rendered.AssistantText, "SETUP: a missing model gives the generic sentence")
		// The CLI name is a fact that WAS known: it must not be logged as a value.
		rw1857AssertRendererWarning(t, "CLI run {model}", log, code,
			[]string{"model"}, []string{CLIDisplayName("codex"), template, generic})
	})

	t.Run("whole_fatal_run_persists_the_generic_sentence_and_logs_only_safe_lines", func(t *testing.T) {
		const code = "external_cli_model_unavailable"
		template := generated.LLMErrorUserMessages[code]
		readLog := captureAgentLog(t)
		run := am1857DriveFatal(t, test1857C2Envelope, "")
		log := readLog()

		// No count across the run (the renderer and each boundary are
		// independent sites). The renderer's own line is proven in
		// cli_model_slot_without_model, around the renderer call alone; here
		// the run must show at least one diagnostic for the code, and every
		// such line must be safe.
		lines := rw1857Lines(log, code)
		assert.NotEmpty(t, lines, "D-A renderer (F7) whole run: a missing-fact run must log a diagnostic naming %s", code)
		// At least ONE diagnostic from the actual run names the code AND the
		// missing slot. Any number of further safe lines from the layered
		// steps is fine: this is an existence check, not a count.
		namesSlot := false
		for _, line := range lines {
			if regexp.MustCompile(`\{model\}|\bmodel\b`).MatchString(strings.ReplaceAll(line, code, "")) {
				namesSlot = true
				break
			}
		}
		assert.True(t, namesSlot,
			"D-A renderer (F7) whole run: at least one diagnostic from the real run must name %s and the missing slot %q; lines: %q", code, "model", lines)
		rw1857AssertBoundaryLinesSafe(t, "whole run", log, code,
			[]string{CLIDisplayName("codex"), template, generic})

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
