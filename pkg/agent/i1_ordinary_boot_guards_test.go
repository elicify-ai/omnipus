package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracle: I1 option B selects only previous-boot queued/running ordinary
// executions; idle conversations, human Stops and replacement runs are protected.
func TestI1OrdinaryBootGuards(t *testing.T) {
	for _, name := range []string{"previous_running", "previous_queued", "idle_no_execution", "current_boot", "future_boot", "completed", "human_stopped", "human_fence", "accepted_without_fence", "stale_run", "stale_generation", "stop_accepted_after_snapshot", "unreadable_controls"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			us, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, us.Close()) })
			meta, err := us.NewSession(session.SessionTypeChat, "webchat", "mia")
			require.NoError(t, err)
			ls := session.NewLifecycleStore(filepath.Join(home, "lifecycle"))
			boot := session.NewBootEpochStore(home)
			oldEpoch, err := boot.Mint()
			require.NoError(t, err)
			boot = session.NewBootEpochStore(home) // A real next boot is a new store instance.
			writingEpoch, err := boot.Mint()
			require.NoError(t, err)
			rec := &session.LifecycleRecord{SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning,
				AgentID: "mia", WorkspaceID: "ws", OwnerScopeKind: session.OwnerScopeHuman, GoalRef: "i1-preserved-goal",
				Origin: &session.Origin{Kind: session.OriginKindChat}, ExecutionID: &session.ExecutionIdentity{RunID: "previous-run", BootSeq: oldEpoch}}
			switch name {
			case "previous_queued":
				rec.State = session.LifecycleQueued
			case "idle_no_execution":
				rec.ExecutionID = nil
			case "current_boot":
				rec.ExecutionID.BootSeq = writingEpoch
			case "future_boot":
				rec.ExecutionID.BootSeq = writingEpoch + 1
			case "completed":
				rec.State = session.LifecycleCompleted
			case "human_stopped":
				rec.State = session.LifecycleStopped
				rec.StopNote = &session.StopNote{At: time.Now().UTC(), By: session.StopActorHumanUser("owner"), Seq: 1, Cause: session.StopCauseStop}
			case "human_fence":
				rec.Stop = &session.Stop{At: time.Now().UTC(), Generation: rec.Generation, By: session.Principal{Kind: session.PrincipalKindHuman, ID: "owner"}}
			}
			require.NoError(t, ls.Persist(rec))
			selected, err := ls.Load(meta.ID)
			require.NoError(t, err)
			directWriter := name == "stale_run" || name == "stale_generation" || name == "stop_accepted_after_snapshot"
			if name == "stale_run" || name == "stale_generation" {
				require.NoError(t, ls.Mutate(meta.ID, func(cur *session.LifecycleRecord) error {
					if name == "stale_generation" {
						cur.Generation++
						cur.ResumedFrom = cur.SessionID
					} else {
						cur.ExecutionID = &session.ExecutionIdentity{RunID: "replacement-run", BootSeq: oldEpoch}
					}
					return nil
				}))
			}
			if name == "accepted_without_fence" || name == "stop_accepted_after_snapshot" {
				stampErr := errors.New("fixture: crash after accepted Stop, before fence")
				_, _, _, err := ls.AcceptStopControl(meta.ID, session.StopControlIntent{Cause: session.StopCauseStop, Actor: session.StopActorHumanUser("owner")}, func(*session.LifecycleRecord, session.ControlGrant, session.StopEffectTarget) error { return stampErr })
				require.ErrorIs(t, err, stampErr, "instrument: real ledger intent must survive failed fence write")
				intents, err := ls.UnfinishedStopIntents(meta.ID)
				require.NoError(t, err)
				require.Len(t, intents, 1)
			}
			path := filepath.Join(ls.Dir(), meta.ID+".jsonl")
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			var notices []string
			recovery := &SteerBootRecovery{Lifecycle: ls, Sessions: us, Inbox: session.NewMessageInboxStore(filepath.Join(home, "inbox")), Classifier: NewSteerRecordClassifier(ls, us), BootEpoch: boot, OperatorNotice: func(message string) { notices = append(notices, message) }}
			if name == "unreadable_controls" {
				controls := filepath.Join(ls.Dir(), "controls")
				require.NoError(t, os.MkdirAll(controls, 0700))
				require.NoError(t, os.WriteFile(filepath.Join(controls, meta.ID+".jsonl"), []byte("{\n"), 0600))
				err := recovery.Run(context.Background())
				var syntaxErr *json.SyntaxError
				require.ErrorAs(t, err, &syntaxErr, "a malformed JSON control ledger must reach the BootHook error channel")
				require.Len(t, notices, 1, "the refused recovery must also be operator-visible, not only a warning log")
				assert.Contains(t, notices[0], meta.ID, "operator notice must identify the refused conversation")
				after, readErr := os.ReadFile(path)
				require.NoError(t, readErr)
				assert.Equal(t, before, after, "a failed ledger read must refuse the restart write, not land a note without history")
				return
			}
			if directWriter {
				require.NoError(t, recovery.failInterrupted(selected))
			} else {
				require.NoError(t, recovery.Run(context.Background()))
			}
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			transitions, err := ls.ListStoppedTransitions(meta.ID)
			require.NoError(t, err)
			if name == "previous_running" || name == "previous_queued" {
				got, err := ls.Load(meta.ID)
				require.NoError(t, err)
				assert.Equal(t, session.LifecycleStopped, got.State, "only the previous-boot execution is recovered")
				assert.Equal(t, "i1-preserved-goal", got.GoalRef, "restart recovery must not clear the session-owned goal")
				if assert.NotNil(t, got.StopNote) {
					assert.Equal(t, session.StopCauseRestart, got.StopNote.Cause)
				}
				assert.Len(t, transitions, 1)
			} else {
				assert.Equal(t, before, after, "I1: protected record must remain byte-identical, not even append an unchanged snapshot")
				assert.Empty(t, transitions, "no fabricated restart stop on an idle/current/replaced/human-stopped record")
			}
		})
	}
}
