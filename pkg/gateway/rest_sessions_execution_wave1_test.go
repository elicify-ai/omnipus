package gateway

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Session.execution and Session.background_command_count.
// ARCH-DECISIONS Decision 4, C2 clarification (2026-10-08). execution preserves
// queued/running only while that same record's current-boot projection is working;
// every other lifecycle display omits execution. Reads never rewrite the record.
// background_command_count counts this session's own running background
// commands. It is not a roll-up of another session's processes.
//
// The shared process manager (pkg/tools/shell.go globalSessionManager) is
// initialised at package load and is never nil in this process, so the
// "omit when no manager is wired" branch cannot be driven here. The wired
// cases below are the ones this process can prove. A nil manager must omit
// the field rather than send 0; that branch needs a seam this package does
// not have.

func TestSessionList_Execution_QueuedAndRunningOnly(t *testing.T) {
	cases := []struct {
		name       string
		state      session.LifecycleState
		failed     string
		wantLife   string
		wantExec   string
		execAbsent bool
		priorBoot  bool
	}{
		{name: "queued emits execution and keeps working", state: session.LifecycleQueued, wantLife: "working", wantExec: "queued"},
		{name: "running emits execution and keeps working", state: session.LifecycleRunning, wantLife: "working", wantExec: "running"},
		{name: "prior boot queued root omits execution", state: session.LifecycleQueued, priorBoot: true, wantLife: "interrupted", execAbsent: true},
		{name: "prior boot running root omits execution", state: session.LifecycleRunning, priorBoot: true, wantLife: "interrupted", execAbsent: true},
		{name: "needs input omits execution", state: session.LifecycleNeedsInput, wantLife: "waiting_for_answer", execAbsent: true},
		{name: "completed omits execution", state: session.LifecycleCompleted, wantLife: "done", execAbsent: true},
		{name: "failed omits execution", state: session.LifecycleFailed, failed: "boom", wantLife: "failed", execAbsent: true},
		{name: "stopped omits execution", state: session.LifecycleStopped, wantLife: "stopped", execAbsent: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newI1R1BootFixture(t)
			api, ls := f.api, f.ls
			id := createTestSession(t, api)
			rec := &session.LifecycleRecord{
				SessionID:      id,
				State:          tc.state,
				Generation:     1,
				OwnerScopeKind: session.OwnerScopeHuman,
				FailedReason:   tc.failed,
			}
			if tc.state == session.LifecycleQueued || tc.state == session.LifecycleRunning {
				epoch := f.boot.Current()
				if tc.priorBoot {
					epoch = f.oldEpoch
				}
				rec.ExecutionID = &session.ExecutionIdentity{RunID: "wave1-execution-" + id, BootSeq: epoch}
			}
			if tc.state == session.LifecycleNeedsInput {
				rec.NeedsInput = &session.NeedsInput{CorrelationID: "corr-wave1"}
			}
			if tc.state == session.LifecycleStopped {
				// Persist requires a stop note for state stopped. The note is
				// not what this case is testing; execution must still be omitted.
				rec.StopNote = &session.StopNote{
					At:    time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
					By:    "human:tester-1",
					Cause: session.StopCauseStop,
					Seq:   1,
				}
			}
			require.NoError(t, ls.Persist(rec))
			before := f.journal(t, id)

			page, code := u18DoListSessions(t, api, "")
			require.Equal(t, 200, code)
			row := findSessionRow(page, id)
			require.NotNil(t, row)
			assert.Equal(t, tc.wantLife, row["lifecycle_state"], "lifecycle_state stays the authoritative current-boot display")
			assertExecution(t, row, tc.execAbsent, tc.wantExec)

			detail, code := doGetSession(t, api, id)
			require.Equal(t, 200, code)
			sessionObj, ok := detail["session"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, tc.wantLife, sessionObj["lifecycle_state"])
			assertExecution(t, sessionObj, tc.execAbsent, tc.wantExec)
			assert.Equal(t, before, f.journal(t, id), "list/detail projection must not rewrite lifecycle history")
		})
	}
}

func TestSessionList_Execution_OmittedWhenNoRecord(t *testing.T) {
	t.Run("no store wired", func(t *testing.T) {
		api, cleanup := newTestRestAPI(t)
		defer cleanup()
		require.Nil(t, api.agentLoop.GetSessionLifecycleStore())
		id := createTestSession(t, api)
		page, code := u18DoListSessions(t, api, "")
		require.Equal(t, 200, code)
		row := findSessionRow(page, id)
		require.NotNil(t, row)
		_, ok := row["execution"]
		assert.False(t, ok, "execution must be absent, not null, when no lifecycle store is wired; got %#v", row["execution"])
	})

	t.Run("store wired but this session has no record", func(t *testing.T) {
		api, cleanup := newTestRestAPI(t)
		defer cleanup()
		ls := session.NewLifecycleStore(t.TempDir())
		api.agentLoop.SetSessionMessagingStores(nil, ls)
		id := createTestSession(t, api)
		page, code := u18DoListSessions(t, api, "")
		require.Equal(t, 200, code)
		row := findSessionRow(page, id)
		require.NotNil(t, row)
		_, ok := row["execution"]
		assert.False(t, ok, "execution must be absent when this session has no lifecycle record; got %#v", row["execution"])
	})
}

func TestSessionList_BackgroundCommandCount_PerOwner(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	owner := createTestSession(t, api)
	other := createTestSession(t, api)
	idle := createTestSession(t, api)

	mgr := tools.GetSharedSessionManager()
	require.NotNil(t, mgr, "the process table is wired in this process")
	ids := []string{"wave1-bg-1", "wave1-bg-2", "wave1-fg", "wave1-done", "wave1-other"}
	t.Cleanup(func() {
		for _, id := range ids {
			mgr.Remove(id)
		}
	})
	mgr.Add(&tools.ProcessSession{ID: "wave1-bg-1", OwnerSessionID: owner, Background: true, Status: tools.StatusRunning, StartTime: time.Now().Unix()})
	mgr.Add(&tools.ProcessSession{ID: "wave1-bg-2", OwnerSessionID: owner, Background: true, Status: tools.StatusRunning, StartTime: time.Now().Unix()})
	mgr.Add(&tools.ProcessSession{ID: "wave1-fg", OwnerSessionID: owner, Background: false, Status: tools.StatusRunning, StartTime: time.Now().Unix()})
	mgr.Add(&tools.ProcessSession{ID: "wave1-done", OwnerSessionID: owner, Background: true, Status: tools.StatusDone, StartTime: time.Now().Unix()})
	mgr.Add(&tools.ProcessSession{ID: "wave1-other", OwnerSessionID: other, Background: true, Status: tools.StatusRunning, StartTime: time.Now().Unix()})

	page, code := u18DoListSessions(t, api, "flat=true")
	require.Equal(t, 200, code)

	ownerRow := findSessionRow(page, owner)
	otherRow := findSessionRow(page, other)
	idleRow := findSessionRow(page, idle)
	require.NotNil(t, ownerRow)
	require.NotNil(t, otherRow)
	require.NotNil(t, idleRow)

	assert.EqualValues(t, 2, ownerRow["background_command_count"], "two running background commands owned by this session; foreground and finished do not count, and the other session's command is not rolled in")
	assert.EqualValues(t, 1, otherRow["background_command_count"], "the other session counts only its own command")
	assert.EqualValues(t, 0, idleRow["background_command_count"], "a wired manager and a zero count sends 0, not an omitted field")

	detail, code := doGetSession(t, api, owner)
	require.Equal(t, 200, code)
	sessionObj, ok := detail["session"].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, 2, sessionObj["background_command_count"], "detail uses the same count as the list")
}

func assertExecution(t *testing.T, row map[string]any, absent bool, want string) {
	t.Helper()
	got, ok := row["execution"]
	if absent {
		assert.False(t, ok, "execution must be omitted, not null or empty; got %#v", got)
		return
	}
	assert.True(t, ok, "execution must be present")
	assert.Equal(t, want, got)
}
