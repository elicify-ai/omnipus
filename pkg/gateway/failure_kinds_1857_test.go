// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// failure_kinds_1857_test.go — #1857 RED tests, wording-independent half,
// gateway leg of T-56 (the Settings "Send a test message" drain).
//
// Spec: docs/internal/specs/notifications-spec.md @ 3748fd637a — §13.8.4
// (Settings surface row), §13.8.5 (B16-config/B17b Settings oracles; C11's
// retained any-error-fails rule, R2-MAJ-005), FR-092/FR-093, row T-56(a)/(b)/
// (g) Settings halves. Founder answers 2026-11-18: E1 B/E3 A (technical
// details and user surfaces carry only the safe facts), sec2 A (fail-closed
// copy), E4 A (exact copy out of scope).
//
// The fake CLI process/output boundary is the consent-routed event channel
// drainSmokeTestRun consumes (the same events a real driver's parser emits);
// everything from this channel to the serialized result is the real Settings
// drain path. Today's RED: the shared-translator paths end generic and the
// result is sanitized (the no-raw-text halves hold as the FR-093 status quo);
// the retained failed-test rule is pinned as a control so the WP-0 change
// cannot silently weaken it. The W1/W2 neutral copy halves are E4-pending.

package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// Fixture shapes — identical vocabulary to pkg/agent/failure_kinds_1857_test.go
// (the drivers' verbatim emission forms; the spec's §13.8.2 rows).
const (
	// test1857SettingsDupFlag is C1's duplicated-flag usage error (the Mac
	// team's first case; W1's Settings surface).
	test1857SettingsDupFlag = "codex exited: exit status 2\nstderr:\nerror: the argument '--model <model>' cannot be used multiple times"
	// test1857SettingsEnvelope is C2's nested provider.no-route envelope (the
	// Mac team's second case; W2's Settings surface).
	test1857SettingsEnvelope = `{"type":"error","error":{"type":"provider.no-route","message":"Model unavailable: no route found for the requested model"}}`
	// test1857SettingsDiagnostic is C11's nonfatal malformed-JSON diagnostic.
	test1857SettingsDiagnostic = "opencode: malformed JSON line on stdout"
)

// TestCLIFailureAdaptersEndToEnd — gateway leg: the real Settings drain over a
// fake CLI output boundary. No flag/value text, no path, no raw envelope text
// in the serialized failed-test result; the retained failed-test rule holds on
// every error event including a nonfatal diagnostic.
func TestCLIFailureAdaptersEndToEnd(t *testing.T) {
	drain := func(t *testing.T, evs []runner.RunEvent) (responseText string, ok bool, errMsg string) {
		t.Helper()
		ch := make(chan runner.RunEvent, len(evs)+1)
		for _, ev := range evs {
			ch <- ev
		}
		close(ch)
		responseText, ok, errMsg = drainSmokeTestRun(context.Background(), "1857-settings-run", ch)
		return responseText, ok, errMsg
	}

	t.Run("a_duplicated_flag_failed_test_no_flag_value_text", func(t *testing.T) {
		// TODO(#1857 E4): exact copy after founder wording review — W1's
		// neutral Settings form; only the failed-test status and the
		// no-flag/value-text half are in scope now.
		_, ok, errMsg := drain(t, []runner.RunEvent{
			{Kind: runner.EventKindError, Err: &runner.ErrorEvent{Message: test1857SettingsDupFlag, Fatal: true}},
		})
		assert.False(t, ok, "T-56(a) Settings (§13.8.4, R2-MIN-001): the duplicated-arguments case must report a failed-test status")
		assert.NotContains(t, errMsg, "cannot be used multiple times",
			"T-56(a) Settings: raw stderr text reached the serialized Settings result")
		assert.NotContains(t, errMsg, "--model",
			"T-56(a) Settings (OQ-1857-3): the flag text itself must not reach the serialized Settings result")
	})

	t.Run("b_nested_envelope_failed_test_no_raw_text", func(t *testing.T) {
		// TODO(#1857 E4): exact copy after founder wording review — W2's
		// neutral Settings form; only the failed-test status and the
		// no-raw-text half are in scope now.
		_, ok, errMsg := drain(t, []runner.RunEvent{
			{Kind: runner.EventKindError, Err: &runner.ErrorEvent{Message: test1857SettingsEnvelope, Fatal: true}},
		})
		assert.False(t, ok, "T-56(b) Settings (§13.8.4): the model-unavailable envelope must report a failed-test status")
		assert.NotContains(t, errMsg, "provider.no-route",
			"T-56(b) Settings: raw stdout envelope text reached the serialized Settings result")
	})

	t.Run("g_nonfatal_diagnostic_keeps_failed_test_rule", func(t *testing.T) {
		// §13.8.5 C11 Settings disposition (R2-MAJ-005, pinned now): the
		// drain treats ANY error event, including a Fatal:false diagnostic,
		// as a failed test — that stricter behaviour is retained unchanged.
		_, ok, errMsg := drain(t, []runner.RunEvent{
			{Kind: runner.EventKindError, Err: &runner.ErrorEvent{Message: test1857SettingsDiagnostic, Fatal: false}},
		})
		assert.False(t, ok, "T-56(g) Settings (§13.8.5 C11, R2-MAJ-005): the retained any-error-fails rule must keep failing the test on a nonfatal diagnostic")
		assert.NotContains(t, errMsg, "malformed JSON",
			"T-56(g) Settings: the diagnostic's raw text must not reach the serialized Settings result")
	})

	t.Run("cli_relayed_limit_gets_the_cli_classifier_answer", func(t *testing.T) {
		// B18 (§13.8.2, R-2): the Settings paths go through "the same CLI-only
		// classifier as the run path" — a CLI-relayed rate-limit refusal must
		// surface the external_cli_upstream_refused answer (C9; W13's
		// already-neutral sentence is both voices, so the code's catalogue
		// copy IS the Settings sentence, {cli} substituted at render time),
		// never the provider rate_limited catalogue copy and never the
		// generic unknown fallback. Today the shared status-0 translator
		// answers exactly the provider copy — the RED half. The classifier's
		// catalogue entry is contract data WP-0 adds; it is referenced by
		// key, never spelled out here.
		_, ok, errMsg := drain(t, []runner.RunEvent{
			{Kind: runner.EventKindError, Err: &runner.ErrorEvent{Message: "API Error: 429 rate limit exceeded, please retry later", Fatal: true}},
		})
		assert.False(t, ok, "T-56/B18 Settings: the CLI-relayed refusal is a failed test")
		classifierCopy := generated.LLMErrorUserMessages["external_cli_upstream_refused"]
		require.NotEmpty(t, classifierCopy,
			"T-56/B18 (§13.8.2 B18/C9): the external_cli_upstream_refused catalogue entry must exist (contract data WP-0 adds) — absent today, the shared status-0 translator answers instead of the CLI-only classifier, and its provider rate_limited copy is what surfaces")
		for i, segment := range strings.Split(classifierCopy, "{cli}") {
			if segment == "" {
				continue
			}
			assert.Contains(t, errMsg, segment,
				"T-56/B18 (§13.8.2 B18/C9): the Settings result must carry the CLI-only classifier's external_cli_upstream_refused sentence (segment %d) — the same classifier as the run path must answer, got %q", i, errMsg)
		}
		assert.NotEqual(t, generated.LLMErrorUserMessages["unknown"], errMsg,
			"T-56/B18 (§13.8.2 B18): the generic unknown fallback must fail this subtest — a CLI-relayed refusal has a known safe reason (the classifier's own answer)")
		assert.NotEqual(t, generated.LLMErrorUserMessages["rate_limited"], errMsg,
			"T-56/B18 (§13.8.2 B18, R-2): the provider rate_limited catalogue copy must never surface for a CLI-relayed refusal — the wrong remedy points at Omnipus's provider settings")
		assert.NotContains(t, errMsg, "rate limit exceeded, please retry later",
			"T-56/B18 Settings: the raw relayed refusal text must not reach the serialized result")
	})

	t.Run("success_control_real_output_passes", func(t *testing.T) {
		// Differentiation control: the drain is not a fail-everything stub.
		responseText, ok, errMsg := drain(t, []runner.RunEvent{
			{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "settings test answer"}},
		})
		assert.True(t, ok, "T-56 Settings control: a run with real output must pass the test")
		assert.Empty(t, errMsg, "T-56 Settings control: a passing test carries no error message")
		assert.Equal(t, "settings test answer", responseText,
			"T-56 Settings control: the accumulated output is the response text")
	})

	t.Run("empty_output_failure_control", func(t *testing.T) {
		// Negative control: a closed channel with no output is a failed test.
		_, ok, errMsg := drain(t, nil)
		assert.False(t, ok, "T-56 Settings control: no textual output is not a passing smoke test")
		assert.NotEmpty(t, errMsg, "T-56 Settings control: the empty-output failure names its reason")
	})
}
