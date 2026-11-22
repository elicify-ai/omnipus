package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Round-3 analyzer item 3 / ruling R1-b: non-final warnings follow the real
// CLI failure reason rather than replacing it or prefixing the partial answer.
// The independent oracle is the exact suffix relationship in the ruling.
// completionDisposition supplies INPUT to this helper, never its expected output.
func TestOpenCodeGateR3_NonFinalSaveWarningPreservesFailureReasonAndFallback(t *testing.T) {
	const partial = "partial work from the single external run"
	// Founder-approved W7/start wording from LLMError.yaml, not runtime translation.
	cliErr := &externalCLIRunError{
		code:    CodeExternalCLIFailed,
		text:    "I couldn't start OpenCode: the program could not be launched. Check the CLI worker's program path in Settings.",
		neutral: "OpenCode could not be launched. Check the CLI worker's program path in Settings.",
	}
	saveErr := errors.New("fixture missing-history cause: /private/r3-warning-save")
	outcome, state, cliReason := completionDisposition(turnResult{
		status: TurnEndStatusError, finalContent: partial, saveFailure: saveErr,
	}, cliErr, partial)
	require.Equal(t, steer.OutcomeFailed, outcome, "a real CLI failure stays failed even when history also failed to save")
	require.Equal(t, session.LifecycleFailed, state)
	require.NotEmpty(t, cliReason)
	suffix := ". " + opencodeR2NeutralWarning
	for _, tc := range []struct {
		name     string
		reason   string
		saveErr  error
		fallback bool
	}{
		{name: "real_cli_failure_plus_save_failure", reason: cliReason, saveErr: saveErr},
		{name: "empty_reason_uses_nonempty_fallback", saveErr: saveErr, fallback: true},
		{name: "no_save_failure_preserves_original", reason: cliReason},
	} {
		t.Run(tc.name, func(t *testing.T) {
			answer, reason := withSaveFailureWarning(tc.saveErr, outcome, partial, tc.reason)
			assert.Equal(t, partial, answer, "a non-final warning belongs on the reason, not the partial answer")
			if tc.saveErr == nil {
				assert.Equal(t, tc.reason, reason)
				assert.NotContains(t, reason, opencodeR2NeutralWarning)
				return
			}
			if tc.fallback {
				// No independent source fixes the defensive fallback's wording.
				// Assert its meaning and exact warning placement, not invented copy.
				require.True(t, strings.HasSuffix(reason, suffix), "fallback ends with a separator and the verbatim approved warning")
				fallback := strings.TrimSuffix(reason, suffix)
				assert.NotEmpty(t, strings.TrimSpace(fallback), "the empty-input branch supplies a real failure reason, not only the save warning")
				assert.Equal(t, fallback+suffix, reason)
			} else {
				assert.Equal(t, tc.reason+suffix, reason, "real CLI failure reason is retained exactly, followed by '. ' and W17")
			}
			assert.Equal(t, 1, strings.Count(reason, opencodeR2NeutralWarning))
			assert.NotContains(t, reason, saveErr.Error(), "the raw storage cause is never part of the parent's notice")
		})
	}
}
