package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Analyzer round 2 minor #3, at the actual drainSaveFailures boundary:
// the original is clean and the second turn has its OWN latched failure.
// Joining and once-only announcement are independent assertions; a pre-existing
// working control must not be described as a new behavioral RED.
func TestOpenCodeGateR2_ContinuationOwnSaveFailureIsCollectedOnce(t *testing.T) {
	for _, kind := range []string{"no_carried_failure", "distinct_carried_failure", "identical_carried_failure"} {
		t.Run(kind, func(t *testing.T) {
			f := newOpencodeR2Fixture(t)
			f.proveNoticeRoute(t)
			continuation := f.newTurn(t, "r2-producing-run", 7)
			require.NotEqual(t, f.turn.turnID, continuation.turnID)
			ownErr := errors.New("continuation's own transcript write failed")
			continuation.setCallAccountingError(ownErr)
			require.NoError(t, f.turn.callAccountingError(), "instrument: no original-turn failure to accidentally carry instead")
			var carried error
			switch kind {
			case "distinct_carried_failure":
				carried = errors.New("earlier distinct transcript write failed")
			case "identical_carried_failure":
				carried = ownErr
			}
			turns := []*turnState{f.turn, continuation}
			joined := f.al.drainSaveFailures(carried, turns)
			require.ErrorIs(t, joined, ownErr, "the continuation's own error must survive the drain")
			if carried != nil {
				require.ErrorIs(t, joined, carried, "a distinct earlier error must also survive")
			}
			assert.Equal(t, 1, strings.Count(joined.Error(), ownErr.Error()), "an already-carried identical error is not joined again")
			// errorlint requires errors.Is for error comparison. The fixture
			// errors are unwrapped errors.New values, so errors.Is is
			// behaviorally identical to pointer identity here; the asserted
			// joined-message counts are unchanged.
			if carried != nil && !errors.Is(carried, ownErr) {
				assert.Equal(t, 1, strings.Count(joined.Error(), carried.Error()))
			}
			repeated := f.al.drainSaveFailures(joined, turns)
			require.ErrorIs(t, repeated, ownErr)
			assert.Equal(t, joined.Error(), repeated.Error(), "re-collection neither duplicates errors nor re-announces a turn")
			assert.False(t, f.turn.externalSaveFailureAnnounced.Load(), "the clean original must not acquire a save notice")
			assert.True(t, continuation.externalSaveFailureAnnounced.Load())
			notices := f.takeNotices(t)
			if assert.Len(t, notices, 1) {
				assert.Equal(t, "session_save", notices[0].Stage)
				assert.Equal(t, "transcript_save_failed", notices[0].Code)
				assert.Equal(t, opencodeR2ChatWarning, notices[0].Message)
			}
			entries, err := f.archive.ReadTranscript(f.record.SessionID)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			assert.Equal(t, "transcript_save_failed", entries[0].ErrorCode)
			assert.Equal(t, opencodeR2ChatWarning, entries[0].Content)
			rows := f.logs(t, opencodeR2SaveLog)
			if assert.Len(t, rows, 1) {
				assert.Equal(t, continuation.turnID, rows[0]["turn_id"], "diagnostic belongs to the affected continuation, never the clean original")
				assert.Equal(t, ownErr.Error(), rows[0]["error"])
			}
			assert.Empty(t, f.logs(t, opencodeR2OwnershipLog))
			continuation.mu.RLock()
			failed := continuation.turnFailed
			continuation.mu.RUnlock()
			assert.False(t, failed, "R1-d: collecting the continuation's save failure preserves its successful-work flag")
		})
	}
}

// R1-d must also hold in the REAL drain's deferred result tail, not just in
// its collection helper. A nil snapshot takes the existing early-return path
// but still runs that tail. No stream-finalize fault or model call is involved.
func TestOpenCodeGateR2_DrainSaveFailureDoesNotReplaceSuccessfulOutcome(t *testing.T) {
	for _, latchOnTurn := range []bool{false, true} {
		name := "carried_result_failure_only"
		if latchOnTurn {
			name = "turns_own_latched_failure"
		}
		t.Run(name, func(t *testing.T) {
			f := newOpencodeR2Fixture(t)
			saveErr := errors.New("durable history write failed after a real answer")
			initial := turnResult{status: TurnEndStatusCompleted, finalContent: "the successful answer", turnID: f.turn.turnID}
			if latchOnTurn {
				f.turn.setCallAccountingError(saveErr)
			} else {
				initial.saveFailure = saveErr
			}
			last, result, err := f.al.drainSteeredTurn(context.Background(), nil, f.turn, initial, nil)
			require.NoError(t, err, "save failure belongs in saveFailure, not the turn error")
			assert.Same(t, f.turn, last)
			require.ErrorIs(t, result.saveFailure, saveErr)
			assert.Equal(t, TurnEndStatusCompleted, result.status, "ruling R1-d: history failure does not overwrite a real-response status")
			assert.False(t, result.turnFailed, "ruling R1-d: history failure is not engine error fallback")
			assert.Equal(t, initial.finalContent, result.finalContent)
			assert.Equal(t, initial.turnID, result.turnID)
		})
	}
}
