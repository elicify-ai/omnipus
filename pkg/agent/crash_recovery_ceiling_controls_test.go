//go:build goolm && stdjson

package agent

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func crashRequireInterrupted(t *testing.T) {
	t.Helper()
	if !session.IsValidLifecycleState(session.LifecycleState("interrupted")) {
		t.Fatal("BLOCKED: durable Interrupted lifecycle not implemented — required by crash-recovery DESIGN Decision 1 / Q14")
	}
}

func TestCrashRecovery_StopAcceptedAtCeilingCommitWins(t *testing.T) {
	var selected string
	var count int
	var stopErr error
	h, rec := crashCeiling(t, func(h *crashHarness, rec *session.LifecycleRecord) {
		selected = rec.SessionID
		var once sync.Once
		completeStateWriteTestHook = func(id string) {
			if id != selected {
				return
			}
			once.Do(func() {
				count++
				_, stopErr = NewSteerCanceller(h.al.GetSessionLifecycleStore()).CancelSubtree(context.Background(), id, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "owner"})
			})
		}
		t.Cleanup(func() { completeStateWriteTestHook = nil })
	})
	require.NoError(t, stopErr)
	require.Equal(t, 1, count, "Stop is selected at the actual completion linearization boundary")
	got := h.record(t, rec.SessionID)
	assert.Equal(t, session.LifecycleStopped, got.State, "intentional Stop accepted first wins over the ceiling outcome")
	require.NotNil(t, got.StopNote)
	assert.Equal(t, session.StopCauseStop, got.StopNote.Cause)
	assert.Nil(t, got.FinalDelivery, "losing ceiling may not publish a final over the Stop")
}

func TestCrashRecovery_CeilingCommitFailureIsVisibleAndUnpublished(t *testing.T) {
	gate := make(chan struct{})
	p := &crashScript{gate: gate, entered: make(chan int, 8)}
	h := crashNew(t, p)
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})
	rec, owner := crashChild(t, h, p)
	path := filepath.Join(h.al.GetSessionLifecycleStore().Dir(), rec.SessionID+".jsonl")
	backup := path + ".fixture-backup"
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	var once sync.Once
	completeStateWriteTestHook = func(id string) {
		if id != rec.SessionID {
			return
		}
		once.Do(func() { require.NoError(t, os.Rename(path, backup)); require.NoError(t, os.Mkdir(path, 0700)) })
	}
	t.Cleanup(func() { completeStateWriteTestHook = nil })
	err = h.al.completeSteeredTurn(context.Background(), rec, turnResult{finalContent: toolLimitResponse, turnFailed: true}, nil)
	assert.Error(t, err, "failed persistence is returned, not reported as an ended/published ceiling")
	stored, readErr := os.ReadFile(backup)
	require.NoError(t, readErr)
	assert.Equal(t, before, stored, "failed commit does not change the saved producing generation")
	messages, _, _, inboxErr := h.al.GetMessageInboxStore().Drain(rec.SteeringSessionID(), rec.SessionID, "", 10)
	require.NoError(t, inboxErr)
	assert.Len(t, messages, 0, "no parent publication precedes the durable outcome")
	completeStateWriteTestHook = nil
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Rename(backup, path))
	close(gate)
	crashAwaitDone(t, owner.done)
}

func TestCrashRecovery_CeilingPublicationFailureRetainsRetryableOutbox(t *testing.T) {
	crashRequireInterrupted(t)
	var blocked string
	h, rec := crashCeiling(t, func(h *crashHarness, rec *session.LifecycleRecord) {
		blocked = filepath.Join(h.home, "session_messages", rec.SteeringSessionID()+".jsonl")
		require.NoError(t, os.MkdirAll(blocked, 0700))
	})
	ended := h.record(t, rec.SessionID)
	require.Equal(t, session.LifecycleState("interrupted"), ended.State)
	require.NotNil(t, ended.FinalDelivery)
	pending, err := h.al.GetSessionLifecycleStore().ListPendingFinalDeliveries()
	require.NoError(t, err)
	require.Len(t, pending, 1, "failed publication remains an owed durable delivery")
	require.NoError(t, os.Remove(blocked))
	r := &SteerBootRecovery{Lifecycle: h.al.GetSessionLifecycleStore(), Sessions: h.al.GetSessionStore(), Inbox: h.al.GetMessageInboxStore(), Deliverer: h.al.getUpwardDeliverer()}
	require.NoError(t, r.runFinalDeliveryPass(context.Background()))
	require.NoError(t, r.runFinalDeliveryPass(context.Background()))
	messages, _, _, err := h.al.GetMessageInboxStore().Drain(rec.SteeringSessionID(), rec.SessionID, "", 10)
	require.NoError(t, err)
	require.Len(t, messages, 1, "delivery retry deduplicates the one nonfatal parent outcome")
	e, err := messages[0].AsSessionMessageError()
	require.NoError(t, err)
	assert.False(t, e.Fatal)
	after := h.record(t, rec.SessionID)
	assert.Equal(t, ended.FinalDelivery, after.FinalDelivery, "publishing cannot rewrite the frozen outcome/outbox")
}

func TestCrashRecovery_TreeStopTraversesInterruptedHelper(t *testing.T) {
	crashRequireInterrupted(t)
	var descendant *session.LifecycleRecord
	var childOwner *executionDisposition
	var release chan struct{}
	h, parent := crashCeiling(t, func(h *crashHarness, parent *session.LifecycleRecord) {
		release = make(chan struct{})
		p := &crashScript{gate: release, entered: make(chan int, 8)}
		h.al.GetRegistry().primaryChatAgentForTest().Provider = p
		t.Cleanup(func() {
			select {
			case <-release:
			default:
				close(release)
			}
		})
		launched, err := NewSteerLauncher(h.al).Launch(context.Background(), steer.LaunchRequest{SteeringSessionID: parent.SessionID, TargetAgentID: testDefaultAgentID, Task: "independent active descendant", Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "tree-stop-descendant"}})
		require.NoError(t, err)
		d, err := NewSteerLauncher(h.al).Dispatch(context.Background(), launched.SessionID, launched.Generation)
		require.NoError(t, err)
		require.Equal(t, steer.DispatchRunning, d.State)
		crashAwait(t, p.entered)
		descendant = h.record(t, launched.SessionID)
		childOwner = h.al.executionDispositionFor(h.al.executionClaimFor(descendant))
		require.NotNil(t, childOwner)
	})
	ended := h.record(t, parent.SessionID)
	require.Equal(t, session.LifecycleState("interrupted"), ended.State)
	result, err := h.al.StopSession(context.Background(), StopRequest{SessionID: parent.SessionID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "owner"}})
	require.NoError(t, err)
	require.Contains(t, result.Report.Reached, descendant.SessionID, "explicit tree Stop must cross the ended helper to reach active descendants")
	crashAwaitDone(t, childOwner.done)
	assert.Equal(t, session.LifecycleStopped, h.record(t, descendant.SessionID).State)
	assert.Equal(t, ended, h.record(t, parent.SessionID), "tree Stop must not rewrite the helper's frozen Interrupted outcome")
}

func TestCrashRecovery_ReportAndScheduleDoNotReviveInterruptedHelper(t *testing.T) {
	for _, source := range []string{"report", "schedule"} {
		t.Run(source, func(t *testing.T) {
			crashRequireInterrupted(t)
			h, child := crashCeiling(t)
			before := h.record(t, child.SessionID)
			p := &crashScript{}
			h.al.GetRegistry().primaryChatAgentForTest().Provider = p
			if source == "report" {
				id := appendWakeInboxEntry(t, h.al, child.SessionID, "ordinary retained descendant report")
				response, err := h.al.processSteeredSystemWake(context.Background(), wakeMessage(child.SessionID, id, child.Generation))
				require.NoError(t, err)
				assert.Equal(t, "", response, "ordinary report is retained, not a new explicit continuation")
			} else {
				_, err := h.al.ProcessScheduled(context.Background(), testDefaultAgentID, child.SessionID, "scheduled wake cannot accept uncertainty", "scheduled", "schedule")
				assert.Error(t, err, "schedule is not an authorized explicit helper continuation")
			}
			assert.Equal(t, 0, len(p.calls()))
			assert.Equal(t, before, h.record(t, child.SessionID), "nonhuman wake preserves the exact frozen generation")
		})
	}
}
