package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func opencodeR4Stopped(t *testing.T, al *AgentLoop, sid string) *session.LifecycleRecord {
	t.Helper()
	require.NoError(t, al.GetSessionLifecycleStore().Mutate(sid, func(rec *session.LifecycleRecord) error {
		rec.State = session.LifecycleStopped
		rec.ExecutionID = nil
		rec.ExternalRunStarted = true
		rec.StopNote = &session.StopNote{At: time.Now().UTC(), By: "human:r4-person", Seq: 1, Cause: session.StopCauseStop}
		return nil
	}))
	rec, err := al.GetSessionLifecycleStore().Load(sid)
	require.NoError(t, err)
	return rec
}

// Stop a LIVE run through the real entry point; never turn a completed,
// immutable generation into a fabricated stopped record.
func opencodeR4StopRun(t *testing.T, al *AgentLoop, d *opencodeR4Driver, sid string) {
	t.Helper()
	_, err := al.StopSession(context.Background(), StopRequest{
		SessionID: sid, Channel: "webchat",
		By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "r4-person"},
	})
	require.NoError(t, err, "SETUP: real Stop must be accepted")
	d.finishFirst()
	opencodeFixJoin(t, al)
	rec, err := al.GetSessionLifecycleStore().Load(sid)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleStopped, rec.State, "SETUP: the actual stop must settle before input")
}

func opencodeR4AssertNoGoal(t *testing.T, al *AgentLoop, sid string, beforeCount int) {
	t.Helper()
	goals, skipped, err := resolveGoalRecordStore().List()
	require.NoError(t, err)
	require.Empty(t, skipped, "a corrupt goal must not disappear from the probe")
	assert.Len(t, goals, beforeCount, "R4-RED-6: pane /goal input must not create ANY new goal")
	for _, g := range goals {
		assert.NotEqual(t, sid, g.ActiveSessionID, "external helper must not acquire an active goal")
		assert.NotEqual(t, sid, g.OwnerID, "external helper must not own a goal")
	}
	rec, err := al.GetSessionLifecycleStore().Load(sid)
	require.NoError(t, err)
	assert.Empty(t, rec.GoalRef, "no goal may be bound to this external helper")
}

// R4-RED-6 is explicitly a probe. Preserve the no-goal oracle even if this
// already passes on 58349f98c; a baseline pass is not manufactured RED proof.
func TestOpenCodeR4_06_HelperPaneGoalCommandNeverCreatesGoal(t *testing.T) {
	for _, cli := range opencodeR4CLIs {
		for _, state := range []string{"live", "completed", "stopped"} {
			t.Run(cli.key+"/"+state, func(t *testing.T) {
				al, d, _, child := opencodeR4Running(t, cli.key)
				switch state {
				case "stopped":
					opencodeR4StopRun(t, al, d, child.SessionID)
				case "completed":
					d.finishFirst()
					opencodeFixJoin(t, al)
				}
				goals, skipped, err := resolveGoalRecordStore().List()
				require.NoError(t, err)
				require.Empty(t, skipped)
				opencodeR4AssertNoGoal(t, al, child.SessionID, len(goals))
				msg := opencodeR4SaveHuman(t, al, child.SessionID, "/goal write X")
				// This is the actual pane/worker front door. Calling a command
				// handler directly would bypass the interception under test.
				require.True(t, al.dispatchSessionWorker(child.SessionID, msg))
				opencodeR4AssertNoGoal(t, al, child.SessionID, len(goals))
				d.finishFirst()
				opencodeFixJoin(t, al)
				opencodeR4AssertNoGoal(t, al, child.SessionID, len(goals))
				rows := qaReceiptReopenTranscript(t, al.ResolveSessionStore(child.SessionID), child.SessionID)
				var saved []string
				for _, entry := range rows {
					if entry.Role == "user" && entry.Content == msg.Content {
						saved = append(saved, entry.ID)
					}
				}
				assert.Equal(t, []string{msg.TranscriptEntryID}, saved, "instrument: the actual pane input was durably submitted once")
			})
		}
	}
}

// R4-RED-7: do NOT fabricate a completed record/empty holder. A real external
// delegate runs, completes and disposes; only then send the human message.
func TestOpenCodeR4_07_CompletedReleasedHelperRefusesVisiblyAndDurably(t *testing.T) {
	for _, cli := range opencodeR4CLIs {
		t.Run(cli.key, func(t *testing.T) {
			al, d, processes, child := opencodeR4Running(t, cli.key)
			d.finishFirst()
			opencodeFixJoin(t, al)
			rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
			require.NoError(t, err)
			require.Equal(t, session.LifecycleCompleted, rec.State, "SETUP: real completion/dispose must have run")
			require.True(t, rec.ExternalRunStarted, "SETUP: the actual CLI run stamped the durable prior-start fact")
			holder := al.externalRunSessionIfPresent(child.SessionID)
			require.NotNil(t, holder)
			holder.mu.Lock()
			driver, running := holder.driver, holder.running
			holder.mu.Unlock()
			require.Nil(t, driver, "SETUP: disposeSteeredTurnResult must actually release the driver")
			require.False(t, running)
			parent := rec.SteeredBy.SteeringSessionID
			inboxBefore, err := al.GetMessageInboxStore().Entries(parent)
			require.NoError(t, err)
			require.NotEmpty(t, inboxBefore, "instrument: the real successful helper already reached the parent inbox")
			parentBefore := qaReceiptReopenTranscript(t, al.ResolveSessionStore(parent), parent)
			observed := opencodeR4Observe(t, al, child.SessionID)
			processesBefore, cancelsBefore := processes.Load(), d.cancels.Load()
			msg := opencodeR4SaveHuman(t, al, child.SessionID, "PING add one useful tip")
			require.True(t, al.deliverHumanHelperInput(msg))
			opencodeFixJoin(t, al)
			opencodeR4AssertRefusal(t, al, rec, msg, cli.display, observed)
			inboxAfter, err := al.GetMessageInboxStore().Entries(parent)
			require.NoError(t, err)
			assert.Equal(t, inboxBefore, inboxAfter, "D2: refusal must never notify or rewrite the parent inbox")
			assert.Equal(t, parentBefore, qaReceiptReopenTranscript(t, al.ResolveSessionStore(parent), parent), "D2: no parent transcript notice")
			assert.Equal(t, processesBefore, processes.Load(), "D2: no new driver may be constructed")
			assert.Equal(t, cancelsBefore, d.cancels.Load())
			d.mu.Lock()
			runs, resumes := len(d.runOpts), len(d.resumeOpts)
			d.mu.Unlock()
			assert.Equal(t, 1, runs)
			assert.Zero(t, resumes, "D2: a refusal must not resume an old or new process")
		})
	}
}

// R4-RED-8 deliberately constructs the finishing-window state. It does not
// rely on winning a race or on a duration: the lifecycle is running, while
// the actual holder is idle and still retains its driver/cancel callback.
func TestOpenCodeR4_08_FinishingWindowRefusesWithoutQueueOrCancel(t *testing.T) {
	for _, cli := range opencodeR4CLIs {
		t.Run(cli.key, func(t *testing.T) {
			al, _ := opencodeR4Loop(t, cli.key)
			d, processes := opencodeR4ProcessEdge(t, al)
			child := launchDelegateTo(t, al, delegateExtCLIAgentID, "finishing-window shape")
			require.NoError(t, al.GetSessionLifecycleStore().Mutate(child.SessionID, func(rec *session.LifecycleRecord) error {
				rec.State = session.LifecycleRunning
				rec.ExternalRunStarted = true
				return nil
			}))
			holder := al.externalRunSession(child.SessionID)
			cancelCalls := &atomic.Int64{}
			holder.mu.Lock()
			holder.started, holder.running, holder.driver = true, false, d
			holder.cancelRun = func() { cancelCalls.Add(1) }
			holder.mu.Unlock()
			rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
			require.NoError(t, err)
			parent := rec.SteeredBy.SteeringSessionID
			inboxBefore, err := al.GetMessageInboxStore().Entries(parent)
			require.NoError(t, err)
			observed := opencodeR4Observe(t, al, child.SessionID)
			msg := opencodeR4SaveHuman(t, al, child.SessionID, "finishing-window human input")
			require.True(t, al.deliverHumanHelperInput(msg))
			opencodeFixJoin(t, al)
			opencodeR4AssertRefusal(t, al, rec, msg, cli.display, observed)
			assert.Zero(t, cancelCalls.Load(), "D2: the idle holder's cancellation callback must not fire")
			assert.Zero(t, d.cancels.Load())
			assert.Zero(t, processes.Load(), "D2: no driver construction in the finishing window")
			inboxAfter, err := al.GetMessageInboxStore().Entries(parent)
			require.NoError(t, err)
			assert.Equal(t, inboxBefore, inboxAfter)
		})
	}
}

// R4-RED-9 simulates loss of runtime state, not a full OS process restart:
// retain and reopen the real durable record, remove the driver registry entry.
func TestOpenCodeR4_09_RestartWithoutHolderRefusesAndStaysStopped(t *testing.T) {
	for _, cli := range opencodeR4CLIs {
		t.Run(cli.key, func(t *testing.T) {
			al, d, processes, child := opencodeR4Running(t, cli.key)
			opencodeR4StopRun(t, al, d, child.SessionID)
			al.ForgetExternalRunSession(child.SessionID)
			require.Nil(t, al.externalRunSessionIfPresent(child.SessionID), "SETUP: no holder survives the simulated restart")
			dir := al.GetSessionLifecycleStore().Dir()
			al.SetSessionMessagingStores(al.GetMessageInboxStore(), session.NewLifecycleStore(dir))
			rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
			require.NoError(t, err)
			require.Equal(t, session.LifecycleStopped, rec.State)
			require.True(t, rec.ExternalRunStarted, "instrument: prior-start survives a real store reopen")
			parent := rec.SteeredBy.SteeringSessionID
			inboxBefore, err := al.GetMessageInboxStore().Entries(parent)
			require.NoError(t, err)
			observed := opencodeR4Observe(t, al, child.SessionID)
			processesBefore := processes.Load()
			msg := opencodeR4SaveHuman(t, al, child.SessionID, "continue after the gateway restarted")
			require.True(t, al.deliverHumanHelperInput(msg))
			opencodeFixJoin(t, al)
			opencodeR4AssertRefusal(t, al, rec, msg, cli.display, observed)
			assert.Equal(t, processesBefore, processes.Load(), "D2: no fresh CLI fallback after restart")
			assert.Nil(t, al.externalRunSessionIfPresent(child.SessionID), "refusal must not manufacture a replacement holder")
			inboxAfter, err := al.GetMessageInboxStore().Entries(parent)
			require.NoError(t, err)
			assert.Equal(t, inboxBefore, inboxAfter)
		})
	}
}
