package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R1-a/R1-b: exercise the result-bearing durable completion caller with a REAL
// admitted execution, real outbox and parent inbox. The process is held at the
// external boundary; the unit input is a successful answer plus saveFailure.
// No test-side prefix or production cap helper can manufacture a false green.
func TestOpenCodeGateR2_SaveWarningPrecedesHandbackCap(t *testing.T) {
	const maxCharacters = 50000 // SessionMessageHandback.yaml::result_so_far.maxLength.
	prefix := opencodeR2NeutralWarning + "\n\n"
	prefixCharacters := utf8.RuneCountInString(prefix)
	for _, tc := range []struct {
		name       string
		characters int
		saveFailed bool
	}{
		{name: "clean_over_limit_control", characters: maxCharacters + 1},
		{name: "warning_plus_answer_max_minus_one", characters: maxCharacters - 1 - prefixCharacters, saveFailed: true},
		{name: "warning_plus_answer_at_max", characters: maxCharacters - prefixCharacters, saveFailed: true},
		{name: "warning_plus_answer_max_plus_one", characters: maxCharacters + 1 - prefixCharacters, saveFailed: true},
		{name: "answer_itself_exceeds_limit", characters: maxCharacters + 1, saveFailed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			al, driver, child := newOpencodeFixRun(t, true, nil)
			producing := al.getActiveTurnState(child.SessionID)
			require.NotNil(t, producing)
			snapshot, err := al.GetSessionLifecycleStore().Load(child.SessionID)
			require.NoError(t, err)
			claim := al.tsExecutionClaim(producing, "")
			require.True(t, claim.matches(snapshot), "instrument: completion carries the genuinely admitted producer's claim")
			require.True(t, al.steerAdmission().hasExecutionReservation(claim))
			answer := strings.Repeat("答", tc.characters) // Contract counts characters, not UTF-8 bytes.
			if tc.characters > maxCharacters {
				answer += "[BEYOND-HANDBACK-LIMIT]"
			}
			result := turnResult{status: TurnEndStatusCompleted, finalContent: answer, turnID: producing.turnID}
			want := answer
			if tc.saveFailed {
				result.saveFailure = errors.New("fixture save failure at /private/r2-completion-target")
				want = prefix + answer // Expected value ONLY; input answer stays unprefixed.
			}
			_, err = al.completeSteeredTurnDurably(context.Background(), snapshot, result, nil, claim)
			require.NoError(t, err, "the real durable completion boundary must be callable")
			driver.finishFirst()
			opencodeFixJoin(t, al)
			rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
			require.NoError(t, err)
			assert.Equal(t, session.LifecycleCompleted, rec.State, "R1-a: save failure preserves the otherwise successful lifecycle")
			if assert.NotNil(t, rec.FinalDelivery) {
				assert.Equal(t, "final_answer", rec.FinalDelivery.Outcome)
			}
			messages, _, more, err := al.GetMessageInboxStore().Drain(rec.SteeringSessionID(), child.SessionID, "", 10)
			require.NoError(t, err)
			require.False(t, more)
			require.Len(t, messages, 1, "one committed final, never a clean result followed by a warning")
			kind, err := messages[0].Discriminator()
			require.NoError(t, err)
			require.Equal(t, "handback", kind, "save failure must not discard the answer into a fatal error")
			final, err := messages[0].AsSessionMessageHandback()
			require.NoError(t, err)
			assert.Equal(t, gen.SessionMessageHandbackModeFinal, final.Mode)
			assert.LessOrEqual(t, utf8.RuneCountInString(final.ResultSoFar), maxCharacters)
			assert.NotContains(t, final.ResultSoFar, "/private/r2-completion-target")
			assert.NotContains(t, final.ResultSoFar, "[BEYOND-HANDBACK-LIMIT]", "answer content beyond the contract limit must not survive truncation")
			if utf8.RuneCountInString(want) <= maxCharacters {
				assert.Equal(t, want, final.ResultSoFar, "under/at the cap, neither warning nor answer is truncated")
			} else {
				wantPrefix := strings.Repeat("答", 100)
				if tc.saveFailed {
					wantPrefix = prefix + wantPrefix
				}
				assert.True(t, strings.HasPrefix(final.ResultSoFar, wantPrefix), "warning must survive first, followed by the answer's beginning; got %s", fmt.Sprintf("%.220s", final.ResultSoFar))
			}
			assert.Equal(t, boolInt(tc.saveFailed), strings.Count(final.ResultSoFar, opencodeR2NeutralWarning), "the neutral warning occurs exactly once iff history saving failed")
		})
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
