package agent

// Chief's 2026-11-02 CHECK follow-up F1–F3. The real dispatch, archive,
// classifier and boot recovery stay in the test. Filesystem damage is confined
// to a temporary session; no production hook, counter oracle or store fake.
import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A directory in place of a transcript file rejects writes even under root.
// Preserve and restore the real bytes by rename, never by deleting a fixture.
func uat6cBlockTranscript(t *testing.T, f *uat6cTranscriptFixture) (string, func()) {
	t.Helper()
	path := filepath.Join(f.store.BaseDir(), f.sid, "transcript.jsonl")
	backupDir := t.TempDir()
	backup := filepath.Join(backupDir, "saved-transcript")
	require.NoError(t, os.Rename(path, backup))
	require.NoError(t, os.Mkdir(path, 0o700))
	var once sync.Once
	restore := func() {
		once.Do(func() {
			require.NoError(t, os.Rename(path, filepath.Join(backupDir, "blocked-directory")))
			require.NoError(t, os.Rename(backup, path))
		})
	}
	t.Cleanup(restore)
	return path, restore
}

func Test6cUAT_F1_DispatchWriteFailurePublishesSessionSaveError(t *testing.T) {
	f := newUAT6cTranscriptFixture(t)
	f.append(t, uat6cAEntry(t))
	f.ts.routingSessionID = session.RoutingSessionID(f.sid)
	f.ts.opts.ChatID = "6c-save-failure-viewer"
	ex := f.execution()
	al := ex.rx.rr.rq.ri.rf.rt.al
	al.eventBus = NewEventBus()
	var visible []ErrorPayload
	al.SetEventSyncTap(func(event Event) {
		if event.Kind == EventKindError {
			payload, ok := event.Payload.(ErrorPayload)
			require.True(t, ok, "instrument: gateway error event has its real typed payload")
			visible = append(visible, payload)
		}
	})
	t.Cleanup(func() { al.SetEventSyncTap(nil) })
	path, restore := uat6cBlockTranscript(t, f)
	file, writeErr := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if file != nil {
		require.NoError(t, file.Close())
	}
	var pathErr *os.PathError
	require.ErrorAs(t, writeErr, &pathErr, "instrument: the actual archive append path rejects a write")
	require.Equal(t, path, pathErr.Path)

	accepted := ex.beginStartedTool(providers.ToolCall{ID: string(uat6cCall), Name: "bash"}, time.Now())
	assert.True(t, accepted, "F1: report the failed receipt without aborting an otherwise executable tool")
	// Amendment §4.2 / wu22NotSavedNotice: preserve the fixed user copy and
	// session_save classification. Filesystem detail is intentionally NOT UI copy.
	want := []ErrorPayload{{Stage: "session_save", Code: "unknown", Message: wu22NotSavedNotice,
		ChatID: "6c-save-failure-viewer", SessionID: f.sid}}
	assert.Equal(t, want, visible, "F1: a failed dispatch receipt must publish the visible session-save error, not merely WARN/count it")
	assert.True(t, f.ts.turnFailed, "F1: the session-save path marks the turn failed immediately")

	// Positive instrument control: the SAME real bus and broken store observe
	// cancel()'s established save-failure path. No synthetic error is published.
	visible = nil
	require.NotNil(t, ex.started, "instrument: real dispatch installed its outcome")
	ex.started.cancel()
	require.Equal(t, want, visible, "instrument: cancel's genuine save failure reaches this exact observer")
	restore()
	assert.Equal(t, []string{uat6cA}, uat6cIDs(f.entries(t)), "a refused write did not fabricate a durable tool receipt")
}

// Persist the prior-boot execution and (for a child) its real parent edge, then
// reopen the stores and mint a different physical boot before classifying.
func uat6cRecoveryAfterCrash(t *testing.T, f *uat6cTranscriptFixture, parent string) (*SteerBootRecovery, *[]string) {
	t.Helper()
	oldBoot := session.NewBootEpochStore(f.home)
	oldEpoch, err := oldBoot.Mint()
	require.NoError(t, err)
	lifecycleDir := filepath.Join(f.home, "lifecycle")
	lifecycle := session.NewLifecycleStore(lifecycleDir)
	rec := &session.LifecycleRecord{
		SessionID: f.sid, Generation: 1, State: session.LifecycleRunning,
		Origin: &session.Origin{Kind: session.OriginKindChat}, OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID: "ws", AgentID: "mia",
		ExecutionID: &session.ExecutionIdentity{RunID: uat6cTurn, BootSeq: oldEpoch},
	}
	if parent != "" {
		require.NoError(t, lifecycle.Persist(&session.LifecycleRecord{
			SessionID: parent, Generation: 1, State: session.LifecycleRunning,
			Origin: &session.Origin{Kind: session.OriginKindChat}, OwnerScopeKind: session.OwnerScopeHuman,
			WorkspaceID: "ws", AgentID: "mia",
		}))
		rec.Origin = &session.Origin{Kind: session.OriginKindDelegate}
		rec.SteeredBy = &session.SteeredBy{SteeringSessionID: parent, RootSessionID: parent}
		rec.OwnerScopeKind, rec.OwnerScopeID, rec.ParentAgentID = session.OwnerScopeParentSession, parent, "mia"
	}
	require.NoError(t, lifecycle.Persist(rec))
	f.reopen(t)
	lifecycle = session.NewLifecycleStore(lifecycleDir)
	boot := session.NewBootEpochStore(f.home)
	currentEpoch, err := boot.Mint()
	require.NoError(t, err)
	require.Greater(t, currentEpoch, oldEpoch, "instrument: boot recovery must select a dead prior-boot execution")
	classifier := NewSteerRecordClassifier(lifecycle, f.store)
	class, err := classifier.Classify(context.Background(), f.sid)
	require.NoError(t, err)
	wantClass := steer.ClassOrdinaryRoot
	if parent != "" {
		wantClass = steer.ClassSteered
	}
	require.Equal(t, wantClass, class, "instrument: enter the requested real recovery arm")
	inbox := session.NewMessageInboxStore(filepath.Join(f.home, "inbox"))
	var notices []string
	r := &SteerBootRecovery{
		Lifecycle: lifecycle, Sessions: f.store, Inbox: inbox, BootEpoch: boot, Classifier: classifier,
		Deliverer:      &bootRecordingDeliverer{inbox: inbox, lifecycle: lifecycle},
		OperatorNotice: func(message string) { notices = append(notices, message) },
	}
	return r, &notices
}

func uat6cAssertRestartStop(t *testing.T, r *SteerBootRecovery, sid string) {
	t.Helper()
	// A fresh lifecycle reader verifies disk, not the recovery object's snapshot.
	rec, err := session.NewLifecycleStore(r.Lifecycle.Dir()).Load(sid)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleStopped, rec.State, "F2/F3: restart stop must land despite tool-recovery failure")
	assert.Equal(t, 1, rec.Generation)
	require.NotNil(t, rec.StopNote)
	assert.Equal(t, session.StopCauseRestart, rec.StopNote.Cause)
	assert.Equal(t, session.StopActorRestart, rec.StopNote.By)
	assert.Equal(t, r.BootEpoch.Current(), rec.StopNote.BootSeq)
}

// Keep the actual transcript a regular readable file, but make its day-mark
// read fail at os.ReadFile. A directory at transcript.jsonl would instead be
// rejected by structural partition validation before any I/O cause exists.
func uat6cBlockTranscriptDayMark(t *testing.T, f *uat6cTranscriptFixture) (string, func()) {
	t.Helper()
	path := filepath.Join(f.store.BaseDir(), f.sid, "transcript.day")
	backupDir := t.TempDir()
	backup := filepath.Join(backupDir, "saved-day-mark")
	require.NoError(t, os.Rename(path, backup))
	require.NoError(t, os.Mkdir(path, 0o700))
	var once sync.Once
	restore := func() {
		once.Do(func() {
			require.NoError(t, os.Rename(path, filepath.Join(backupDir, "blocked-directory")))
			require.NoError(t, os.Rename(backup, path))
		})
	}
	t.Cleanup(restore)
	return path, restore
}

func Test6cUAT_F2_BootToolInterruptionFailureIsVisibleAndStillStops(t *testing.T) {
	for _, steered := range []bool{false, true} {
		name := "ordinary"
		if steered {
			name = "steered"
		}
		t.Run(name, func(t *testing.T) {
			f := newUAT6cTranscriptFixture(t)
			var parent string
			if steered {
				parent = f.sid
				child, err := f.store.NewSession(session.SessionTypeDelegate, "delegate", "mia")
				require.NoError(t, err)
				require.NoError(t, f.store.SetMeta(child.ID, session.MetaPatch{ParentSessionID: &parent}))
				f.sid = child.ID
				f.newTurn()
			}
			f.append(t, uat6cAEntry(t))
			f.begin(t)
			recovery, notices := uat6cRecoveryAfterCrash(t, f, parent)
			path, restore := uat6cBlockTranscriptDayMark(t, f)
			_, readErr := os.ReadFile(path)
			var cause *os.PathError
			require.ErrorAs(t, readErr, &cause, "instrument: real interruption scan encounters an actual filesystem refusal")
			require.Equal(t, path, cause.Path)
			// End the filesystem fault at the operator-notice boundary, after
			// the real interruption has failed. Later helper-diagnostic writes
			// must stay healthy; they are not the operation this test faults.
			report := recovery.OperatorNotice
			recovery.OperatorNotice = func(message string) {
				report(message)
				restore()
			}

			err := recovery.Run(context.Background())
			require.Error(t, err, "F2: interruption failure must survive the recovery error join")
			assert.ErrorIs(t, err, cause.Err, "F2: the original filesystem cause survives every wrap/join")
			var reportedCause *os.PathError
			require.ErrorAs(t, err, &reportedCause)
			assert.Equal(t, path, reportedCause.Path)
			wantNoticeCount := 1
			if steered {
				wantNoticeCount = 3
			}
			require.Len(t, *notices, wantNoticeCount, "one interruption notice, plus only the known unwired steered-resume diagnostics")
			notice := (*notices)[0]
			assert.Contains(t, notice, "session "+f.sid+" running tool calls not interrupted at boot:", "F2: operator sees the affected session and failed operation")
			assert.Contains(t, notice, path, "F2: operator diagnostic retains the failed path")
			assert.Contains(t, notice, cause.Err.Error(), "F2: operator diagnostic retains the filesystem cause")
			assert.Contains(t, err.Error(), notice, "F2: the visible refusal is also returned, not log-only")
			if steered {
				// Harness characterization, as in _e: no parent wake/resume is wired.
				assert.Equal(t, []string{
					"helper " + f.sid + " resume did not start: stopped-child notice deliverer is not wired",
					"session " + f.sid + " stopped-child notice was not delivered: stopped-child notice deliverer is not wired",
				}, (*notices)[1:])
			}
			uat6cAssertRestartStop(t, recovery, f.sid)
			restore()
			assert.Equal(t, []session.ToolCall{{ID: uat6cCall, Tool: "bash", Status: "running", Parameters: map[string]any{"command": uat6cCmd}}},
				toolCallsFor(t, f.store, f.sid, uat6cCall), "failed interruption must not claim that the tool record settled")
		})
	}
}

func Test6cUAT_F3_SelectedCrashLeavesCompletedToolUntouched(t *testing.T) {
	f := newUAT6cTranscriptFixture(t)
	f.append(t, uat6cAEntry(t))
	finished := uat6cCompletedCall("success")
	finished.ID = "6c-already-finished"
	require.NoError(t, f.ts.appendToolCallTranscript(finished))
	f.begin(t)
	f.append(t, uat6cSteerEntry(t))
	before := f.entries(t)
	runningAddress, _ := uat6cOnlyToolSource(t, f.store, f.sid)
	raw, err := session.NewArchiveDayStore(f.store.BaseDir(), f.sid)
	require.NoError(t, err)
	var original []session.ArchiveRecord
	require.NoError(t, raw.ScanAll(func(_ session.ArchiveAddress, rec session.ArchiveRecord) bool {
		original = append(original, rec)
		return true
	}))
	require.Len(t, original, 4, "instrument: A, finished, running, steer are four real source records")
	recovery, notices := uat6cRecoveryAfterCrash(t, f, "")
	require.NoError(t, recovery.Run(context.Background()))
	require.Empty(t, *notices)
	uat6cAssertRestartStop(t, recovery, f.sid)
	interrupted := session.ToolCall{ID: uat6cCall, Tool: "bash", Status: "interrupted", Parameters: map[string]any{"command": uat6cCmd}}
	assert.Equal(t, []session.ToolCall{finished}, toolCallsFor(t, f.store, f.sid, finished.ID), "F3: preserve the completed call's exact status/result/parameters/duration")
	assert.Equal(t, []session.ToolCall{interrupted}, toolCallsFor(t, f.store, f.sid, uat6cCall), "F3: only the running call changes to interrupted")
	want := append([]session.TranscriptEntry(nil), before...)
	want[2].ToolCalls = []session.ToolCall{interrupted}
	assert.Equal(t, want, f.entries(t), "F3: preserve completed entry, source positions, timestamps and all unrelated rows")
	raw, err = session.NewArchiveDayStore(f.store.BaseDir(), f.sid)
	require.NoError(t, err)
	var after []session.ArchiveRecord
	require.NoError(t, raw.ScanAll(func(_ session.ArchiveAddress, rec session.ArchiveRecord) bool {
		after = append(after, rec)
		return true
	}))
	require.Len(t, after, len(original)+1, "F3: exactly one settlement, never a settlement for the completed call")
	assert.Equal(t, original, after[:len(original)], "F3: every physical source stays immutable")
	require.NotNil(t, after[len(original)].ToolCallEffect)
	assert.Equal(t, &session.ToolCallSettle{Target: runningAddress, IfStatus: "running", ToolCall: interrupted},
		after[len(original)].ToolCallEffect.Settle, "F3: the only effect targets the running call, guarded by running status")
}
