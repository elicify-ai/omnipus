package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoalReminderR6ReceiptFailureReachesSession faults only the receipt archive.
// R1 and ADR-20260928 Vocabulary require a real admitted error to settle failed,
// not to preserve the prior completed identity. R6 still forbids provider work,
// a successful reminder notice, or a counted outcome when the receipt fails.
// Repair must admit a later reminder on this same session, worker and goal.
func TestGoalReminderR6ReceiptFailureReachesSession(t *testing.T) {
	h := grNewRecordedBrake(t)
	msg := h.reminder(t)
	grSetStreak(t, h, 2)
	require.True(t, h.al.goalIsIdleSettling(h.goalID), "SETUP: the published reminder is still outstanding")
	prior := h.load(t)
	require.Equal(t, session.LifecycleCompleted, prior.State)
	require.NotNil(t, prior.ExecutionID)
	before := h.root.count()
	r6GateRequireStorageHealth(t, h)
	paths := r6GateFaultReceipt(t, h)
	for _, id := range []string{"r6-gate-receipt-probe-a", "r6-gate-receipt-probe-b"} {
		written, err := r6GateProbeAppend(h, id)
		require.ErrorIs(t, err, os.ErrPermission, "SETUP: each fresh receipt append must fail with the intended permission error")
		require.False(t, written, "SETUP: a failed fresh append must not report a write")
	}
	r6GateRequireStorageHealth(t, h)
	require.Equal(t, prior, h.load(t), "SETUP: the file fault and health checks must not change the completed execution")

	sub := h.al.SubscribeEvents(64)
	require.NotEqual(t, uint64(0), sub.ID, "SETUP: subscribe to the real event bus")
	t.Cleanup(func() { h.al.UnsubscribeEvents(sub.ID) })
	require.True(t, h.al.dispatchSystemMessageToSessionWorker(msg, "webchat"), "the reminder must enter the real session worker")
	worker := h.worker(t)
	workerJoined := false
	t.Cleanup(func() {
		if !workerJoined {
			r6GateJoinWorker(t, h, worker)
		}
	})
	var unexpected []*grCall
	require.Eventually(t, func() bool {
		for {
			select {
			case call := <-h.root.requests:
				unexpected = append(unexpected, call)
				h.root.release(call)
			default:
				rec, err := h.al.GetSessionLifecycleStore().Load(h.id)
				return err == nil && rec.Generation > prior.Generation &&
					rec.State != session.LifecycleRunning &&
					!h.al.goalIsIdleSettling(h.goalID) && r6GateWorkerIdle(h, worker)
			}
		}
	}, grWatchdog, 20*time.Millisecond, "SETUP: the genuinely admitted worker input must settle before receipt assertions")

	t.Run("ErrorReachesTheSession", func(t *testing.T) {
		event, ok := findEvent(collectEventStream(sub.C), EventKindError)
		require.True(t, ok, "a receipt failure must be an error event for this session, not only a worker log")
		payload, isPayload := event.Payload.(ErrorPayload)
		require.True(t, isPayload, "the error event must carry ErrorPayload")
		assert.Equal(t, h.id, payload.SessionID)
		assert.NotEmpty(t, payload.Message)
	})

	assert.Len(t, unexpected, 0, "a failed receipt must not reach the provider")
	assert.Equal(t, before, h.root.count(), "the provider must not be called when the receipt cannot be saved")
	assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), "Goal reminder: continuing work toward the goal"), "a failed receipt must not leave the reminder notice")
	assert.Equal(t, 2, goalRecordForSession(t, h.id).ZeroOutputPushes, "a receipt failure is not an assessed reminder outcome")
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State))
	failed := r6GateRequireFailedReceiptIdentity(t, h, prior)
	r6GateRequireStorageHealth(t, h)

	r6GateRestoreReceipt(t, paths)
	written, err := r6GateProbeAppend(h, "r6-gate-receipt-probe-restored")
	require.NoError(t, err, "SETUP: the restored archive must accept a fresh append")
	require.True(t, written, "SETUP: repair must perform a write, not return an idempotent no-op")
	r6GateRequireStorageHealth(t, h)
	// Publication rearmed the ordinary quiet clock. Make this a later eligible
	// tick through the existing clock fixture, without touching pending/owners.
	h.age(t)
	restored := h.reminder(t)
	require.True(t, h.al.dispatchSystemMessageToSessionWorker(restored, "webchat"), "the repaired reminder must enter the same worker")
	require.Same(t, worker, h.worker(t), "repair must not replace the session worker")
	var call *grCall
	select {
	case call = <-h.root.requests:
	case <-time.After(grWatchdog):
		t.Fatal("SETUP: the repaired reminder did not reach the provider")
	}
	h.root.release(call)
	require.NoError(t, call.readErr)
	require.NotNil(t, call.record, "the repaired provider call must see its real durable identity")
	require.NotNil(t, call.owner, "the repaired provider call must have the ordinary execution owner")
	grAwaitOwner(t, call)
	laterCalls := r6GateJoinWorker(t, h, worker)
	workerJoined = true
	assert.Len(t, laterCalls, 0, "repair must not admit duplicate provider work")
	assert.Equal(t, before+1, h.root.count(), "one later reminder must reach the provider after repair")
	assert.Equal(t, h.id, call.sessionID, "the repair control must use the same session")
	assert.Equal(t, h.goalID, goalRecordForSession(t, h.id).GoalID, "the repair control must retain the same goal")
	completed := h.load(t)
	assert.Equal(t, session.LifecycleCompleted, completed.State, "the real repaired reminder must complete")
	assert.Equal(t, failed.Generation+1, completed.Generation, "repair must admit the round after the failed execution")
	assert.Equal(t, call.record.ExecutionID, completed.ExecutionID, "repair must settle its actual producing identity")
	assert.True(t, r6GateWorkerIdle(h, worker), "repair must leave no live turn or execution owner")
	t.Log("repair control: fresh archive append wrote; same worker reached provider; producing owner and worker joined")
}

func r6GateRequireFailedReceiptIdentity(t *testing.T, h *grHarness, prior *session.LifecycleRecord) *session.LifecycleRecord {
	t.Helper()
	failed := h.load(t)
	assert.Equal(t, session.LifecycleFailed, failed.State, "R1: an admitted receipt error settles failed, not the prior completed turn")
	assert.Equal(t, prior.Generation+1, failed.Generation, "the failed reminder owns its new ordinary generation")
	require.NotNil(t, failed.ExecutionID)
	assert.NotEqual(t, prior.ExecutionID.RunID, failed.ExecutionID.RunID, "the real failed admission must mint its own execution")
	assert.NotEmpty(t, failed.ExecutionID.RunID)
	assert.NotZero(t, failed.ExecutionID.BootSeq)
	assert.Contains(t, failed.FailedReason, "goal reminder: save chat event:", "the failure must name the actual receipt operation")
	assert.Contains(t, failed.FailedReason, "transcript.jsonl", "the error must be the archive fault, not metadata or lifecycle storage")
	var running *session.LifecycleRecord
	for _, line := range bytes.Split(h.journal(t), []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec session.LifecycleRecord
		require.NoError(t, json.Unmarshal(line, &rec))
		if rec.Generation == failed.Generation && rec.State == session.LifecycleRunning {
			running = &rec
		}
	}
	require.NotNil(t, running, "R1: this error followed a real durable running admission")
	assert.Equal(t, running.ExecutionID, failed.ExecutionID, "failure must settle exactly the admitted execution identity")
	claim := h.al.executionClaimFor(failed)
	assert.Nil(t, h.al.executionDispositionFor(claim), "the failed execution must release its owner")
	assert.Nil(t, h.al.ownLiveTurn(h.id), "the failed receipt must not leak an active turn")
	return failed
}

func r6GateRequireStorageHealth(t *testing.T, h *grHarness) {
	t.Helper()
	store := h.al.GetSessionStore()
	meta, err := store.GetMeta(h.id)
	require.NoError(t, err, "SETUP: metadata must remain readable")
	require.NoError(t, store.SetMeta(h.id, session.MetaPatch{Owner: &meta.Owner}), "SETUP: writing the unchanged owner must still work, including meta.json.lock")
	listed, err := store.ListSessions()
	require.NoError(t, err, "SETUP: the session index must remain readable")
	found := false
	for _, item := range listed {
		if item.ID == h.id {
			found = true
			require.Equal(t, meta.Owner, item.Owner)
		}
	}
	require.True(t, found, "SETUP: the same session must remain in the index")
	h.load(t)
	// Terminal records cannot be rewritten even unchanged. Check the journal's
	// append access without forging a transition; real admission and settlement
	// above prove actual lifecycle writes during the receipt-only fault.
	journal, err := os.OpenFile(filepath.Join(h.al.GetSessionLifecycleStore().Dir(), h.id+".jsonl"), os.O_WRONLY|os.O_APPEND, 0)
	require.NoError(t, err, "SETUP: lifecycle storage must retain append access")
	require.NoError(t, journal.Close())
}

type r6GateReceiptPath struct {
	path string
	mode os.FileMode
}

func r6GateFaultReceipt(t *testing.T, h *grHarness) []r6GateReceiptPath {
	t.Helper()
	dir := filepath.Join(h.al.GetSessionStore().BaseDir(), h.id)
	info, err := os.Stat(dir)
	require.NoError(t, err, "SETUP: the session directory must exist")
	require.True(t, info.IsDir())
	require.NotZero(t, info.Mode().Perm()&0o200, "SETUP: the session directory itself must stay writable")
	var paths []r6GateReceiptPath
	require.NoError(t, filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() != "transcript.jsonl" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if err := os.Chmod(path, 0o444); err != nil {
			return err
		}
		paths = append(paths, r6GateReceiptPath{path: path, mode: info.Mode().Perm()})
		return nil
	}))
	require.NotEmpty(t, paths, "SETUP: the receipt file must exist before it can be made unwritable")
	t.Cleanup(func() { r6GateRestoreReceipt(t, paths) })
	return paths
}

func r6GateRestoreReceipt(t *testing.T, paths []r6GateReceiptPath) {
	t.Helper()
	for _, path := range paths {
		require.NoError(t, os.Chmod(path.path, path.mode))
	}
}

func r6GateProbeAppend(h *grHarness, id string) (bool, error) {
	return h.al.GetSessionStore().AppendTranscriptOnce(h.id, session.TranscriptEntry{
		ID: id, Type: session.EntryTypeSystem, Role: "system",
		Content: "receipt probe " + id, Timestamp: time.Now().UTC(),
	})
}
