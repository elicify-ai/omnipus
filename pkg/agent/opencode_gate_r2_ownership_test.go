package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Ruling R2-b/R2-c/R2-f: known replacements suppress; an unreadable record
// alone must never strand the working spinner. This drives the existing
// settlement caller, not a proposed new ownership-helper signature.
func TestOpenCodeGateR2_HeldDoneOwnershipControls(t *testing.T) {
	for _, tc := range []struct {
		name       string
		durable    bool
		registered bool
		readFault  bool
		wantDone   int
		decision   string
	}{
		{name: "same_execution_readable_done_once", wantDone: 1},
		{name: "durable_replacement_not_registered_suppresses", durable: true},
		{name: "live_registered_replacement_suppresses", registered: true},
		{name: "read_fault_empty_registry_publishes", readFault: true, wantDone: 1, decision: "done_published"},
		{name: "durable_replacement_unreadable_not_registered_publishes", durable: true, readFault: true, wantDone: 1, decision: "done_published"},
		{name: "read_fault_registered_replacement_suppresses", registered: true, readFault: true, decision: "suppressed_registered_owner"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOpencodeR2Fixture(t)
			if tc.durable {
				f.replaceRecord(t)
			}
			var other *turnState
			if tc.registered {
				other = f.registerReplacement(t)
			} else {
				require.Nil(t, f.al.getActiveTurnState(f.record.SessionID), "instrument: no positive registered replacement evidence")
			}
			var lookupErr error
			if tc.readFault {
				lookupErr = f.faultRecord(t)
			}
			// Input to the settlement boundary is an already-held done. The
			// real deferral/admission path remains covered by the A4 regression.
			f.turn.externalLiveDoneDeferred.Store(true)
			require.NoError(t, f.al.settleDeferredExternalLiveDone(context.Background(), f.turn), "R2-b: lookup errors must not become helper failures")
			assert.False(t, f.turn.externalLiveDoneDeferred.Load(), "every branch consumes the held flag")
			assert.Equal(t, tc.wantDone, f.stream.finalizeCalled)
			require.NoError(t, f.al.settleDeferredExternalLiveDone(context.Background(), f.turn))
			assert.Equal(t, tc.wantDone, f.stream.finalizeCalled, "repeat settlement must neither duplicate nor resurrect a suppressed done")
			if tc.readFault {
				f.assertOwnershipLog(t, tc.decision, lookupErr)
			} else {
				assert.Empty(t, f.logs(t, opencodeR2OwnershipLog), "a readable ownership decision is not an ownership error")
			}
			assert.Empty(t, f.takeNotices(t), "an ownership lookup problem is not a user-facing run error")
			f.turn.mu.RLock()
			failed := f.turn.turnFailed
			f.turn.mu.RUnlock()
			assert.False(t, failed, "unknown ownership does not invalidate successful work")
			if other != nil {
				assert.Same(t, other, f.al.getActiveTurnState(f.record.SessionID))
				assert.True(t, other.IsAlive(), "the replacement must remain live and registered")
			}
		})
	}
}

// R2-d plus the analyzer's A4 announcer gap (CHECK F2): the saved row is
// independent of live ownership. The same-owner control proves publication
// works; the faulted-store control proves audience classification cannot mask
// a missing ownership suppression branch.
func TestOpenCodeGateR2_SaveNoticeOwnershipAndOutcome(t *testing.T) {
	for _, tc := range []struct {
		name       string
		durable    bool
		registered bool
		readFault  bool
		wantLive   int
	}{
		{name: "same_execution_live_and_saved_once", wantLive: 1},
		{name: "durable_replacement_saved_without_live", durable: true},
		{name: "registered_replacement_saved_without_live", registered: true},
		{name: "unknown_ownership_saved_without_live", readFault: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOpencodeR2Fixture(t)
			if tc.durable {
				f.replaceRecord(t)
			}
			var other *turnState
			if tc.registered {
				other = f.registerReplacement(t)
			}
			var lookupErr error
			if tc.readFault {
				lookupErr = f.faultRecord(t)
			}
			f.proveNoticeRoute(t)
			const privateCause = "fixture transcript write refused: /private/r2-only-save-target"
			saveErr := errors.New(privateCause)
			f.turn.setCallAccountingError(saveErr)
			require.ErrorIs(t, f.turn.callAccountingError(), saveErr)
			f.al.announceExternalSaveFailure(f.turn)
			f.al.announceExternalSaveFailure(f.turn)
			assert.True(t, f.turn.externalSaveFailureAnnounced.Load(), "the saved-only and suppressed branches also consume the once-only flag")
			notices := f.takeNotices(t)
			if assert.Len(t, notices, tc.wantLive) {
				for _, notice := range notices {
					assert.Equal(t, "session_save", notice.Stage)
					assert.Equal(t, "transcript_save_failed", notice.Code)
					assert.Equal(t, opencodeR2ChatWarning, notice.Message)
					assert.Empty(t, notice.Detail)
				}
			}
			entries, err := f.archive.ReadTranscript(f.record.SessionID)
			require.NoError(t, err)
			require.Len(t, entries, 1, "one classified row is saved even when no live frame is allowed")
			assert.Equal(t, "transcript_save_failed", entries[0].ErrorCode)
			assert.Equal(t, "error", entries[0].Status)
			assert.Equal(t, opencodeR2ChatWarning, entries[0].Content)
			assert.NotContains(t, entries[0].Content, privateCause, "raw storage causes never belong in history")
			rows := f.logs(t, opencodeR2SaveLog)
			if assert.Len(t, rows, 1, "one transcript failure log per affected turn") {
				assert.Equal(t, "error", rows[0]["level"])
				assert.Equal(t, f.turn.turnID, rows[0]["turn_id"])
				assert.Equal(t, privateCause, rows[0]["error"])
			}
			if tc.readFault {
				f.assertOwnershipLog(t, "save_notice_saved_only", lookupErr)
			} else {
				assert.Empty(t, f.logs(t, opencodeR2OwnershipLog))
			}
			f.turn.mu.RLock()
			failed := f.turn.turnFailed
			f.turn.mu.RUnlock()
			assert.False(t, failed, "R1-d: announcing incomplete history must not turn a real answer into an engine failure")
			if other != nil {
				assert.Same(t, other, f.al.getActiveTurnState(f.record.SessionID))
				assert.True(t, other.IsAlive())
			}
		})
	}
}
