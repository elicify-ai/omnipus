package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder #1217 option A: the prior process's prompt is gone. Recover its
// standing execution as restart Stop; do not reconstruct/re-ask that prompt.
func TestI1R1NeedsInputRestart(t *testing.T) {
	for _, current := range []bool{false, true} {
		name := "prior_boot_continues_once"
		if current {
			name = "current_boot_control"
		}
		t.Run(name, func(t *testing.T) {
			h := newD2bRoot(t)
			h.seedState(t, session.LifecycleRunning)
			oldEpoch := h.al.bootEpochFor()
			boot := session.NewBootEpochStore(filepath.Join(h.al.GetConfig().Agents.Defaults.Home, "boot_epoch"))
			writing, err := boot.Mint()
			require.NoError(t, err)
			epoch := oldEpoch
			if current {
				epoch = writing
			}
			ls := h.al.GetSessionLifecycleStore()
			require.NoError(t, ls.Mutate(h.id, func(rec *session.LifecycleRecord) error {
				rec.State = session.LifecycleNeedsInput
				rec.NeedsInput = &session.NeedsInput{CorrelationID: "lost-i1-prompt", Reconstructable: true, TTLDeadline: time.Now().Add(time.Hour)}
				rec.ExecutionID = &session.ExecutionIdentity{RunID: "i1-prompt-run", BootSeq: epoch}
				return nil
			}))
			before, journal := h.load(t), h.journal(t)
			h.al.SetBootEpochStore(boot)
			r := &SteerBootRecovery{Lifecycle: ls, Sessions: h.al.GetSessionStore(), Inbox: h.al.GetMessageInboxStore(), Classifier: NewSteerRecordClassifier(ls, h.al.GetSessionStore()), BootEpoch: boot}
			require.NoError(t, r.Run(context.Background()))
			rec := h.load(t)
			assert.Empty(t, h.provider.calls(), "boot must not re-ask or replay the lost prompt")
			if current {
				assert.Equal(t, before, rec)
				assert.Equal(t, journal, h.journal(t), "this-boot needs_input must be byte-identical")
				assert.Equal(t, session.LifecycleDisplayWaitingForAnswer, session.LifecycleRecordToDisplay(rec))
				return
			}
			require.Equal(t, session.LifecycleStopped, rec.State, "prior-boot lost prompt is interrupted, not indefinitely Waiting for answer")
			require.NotNil(t, rec.StopNote)
			assert.Equal(t, session.StopCauseRestart, rec.StopNote.Cause)
			assert.Nil(t, rec.NeedsInput, "the lost process's prompt must not be re-asked")
			assert.Equal(t, before.Generation, rec.Generation)
			assert.Equal(t, session.LifecycleDisplayInterrupted, session.LifecycleRecordToDisplay(rec))
			response, turnErr := h.humanTurn(t, "Continue once without the lost prompt.")
			require.NoError(t, turnErr)
			assert.Equal(t, d2bReply, response)
			calls := h.provider.calls()
			require.Len(t, calls, 1, "one new human instruction produces one new execution")
			require.NoError(t, calls[0].readErr)
			assert.Nil(t, calls[0].record.NeedsInput)
			assert.Equal(t, before.Generation, calls[0].record.Generation)
			require.NotNil(t, calls[0].record.ExecutionID)
			assert.NotEqual(t, before.ExecutionID.RunID, calls[0].record.ExecutionID.RunID)
			assert.Equal(t, writing, calls[0].record.ExecutionID.BootSeq)
		})
	}
}
