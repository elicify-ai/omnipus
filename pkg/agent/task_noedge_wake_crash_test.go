package agent

// Release crash found in the #1341 hands-on test (founder routing,
// 2026-10-18; fixed on the parent-wake branch because #1345 rewrites this
// code). A create_task task's lifecycle record has no steering edge
// (SteeredBy == nil, origin task, no parent); the starter's main session is
// only a captured MONITORING recipient. On release,
// steer_audience.go::SteerUpwardDeliverer.wakeStoredMessage read
// childRec.SteeredBy.ReportingTarget unguarded whenever that recipient had a
// lifecycle record and no live turn, so completeTaskWithResult panicked after
// the notice was stored and before finalizeTaskLifecycle: the task showed done,
// the run was recorded failed, the lifecycle record stayed running for ever,
// the starter was never woken, and goal termination, dependent unblocking and
// the rest of the completion tail were skipped (3 of 3 throwaway instances).
// stopped_notice.go::wakeParentForStoppedNotice carried the same unguarded
// read. Both tests convert a panic into a named failure so the RED run on the
// release tree reports the defect instead of killing the test binary.

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

const noEdgeWorkspace = "default"

// noEdgeTaskFixture is a native task loop with the REAL upward deliverer, an
// eligible idle starter main that already has a completed lifecycle record
// (the release crash precondition), and a create_task child record with no
// steering edge.
type noEdgeTaskFixture struct {
	al          *AgentLoop
	lifecycle   *session.LifecycleStore
	starterMain string
	taskSession string
	mu          sync.Mutex
	wakes       []AsyncNotifyEvent
}

func newNoEdgeTaskFixture(t *testing.T) *noEdgeTaskFixture {
	t.Helper()
	al := newNativeTaskCompletionTestLoop(t, &mockProvider{})
	lifecycle := al.GetSessionLifecycleStore()
	require.NotNil(t, lifecycle, "fixture: the native task loop wires the lifecycle store")
	require.NotNil(t, al.asyncNotifier, "fixture: the async notifier must be wired — the crash is on the notifier path")
	al.SetSteerAudienceDeps(NewSteerAudienceResolver(NewSteerRecordClassifier(lifecycle, al.GetSessionStore())),
		steer.NopBoundaryObserver{}, NewSteerUpwardDeliverer())
	al.SetAddressDeps(&fakeAddressDeps{eligible: map[string]bool{noEdgeWorkspace + "/native-agent": true}})

	store := al.GetSessionStore()
	main, err := store.GetOrCreateMainSession(noEdgeWorkspace, "native-agent")
	require.NoError(t, err)
	al.taskExecutor.SetMainSessionResolver(&u6FakeMainResolver{elig: map[[2]string]string{
		{noEdgeWorkspace, "native-agent"}: main.ID,
	}})
	// Idle starter: its last turn finished, no live turn is registered.
	require.NoError(t, lifecycle.Persist(&session.LifecycleRecord{
		SessionID: main.ID, State: session.LifecycleCompleted, Generation: 1,
		OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: noEdgeWorkspace, AgentID: "native-agent",
		OriginChannel: "webchat", OriginChatID: main.ID, Origin: &session.Origin{Kind: session.OriginKindChat},
	}))
	require.Nil(t, al.getActiveTurnState(main.ID), "fixture: the starter must be idle")

	taskMeta, err := store.NewSession(session.SessionTypeTask, "webchat", "native-agent")
	require.NoError(t, err)
	f := &noEdgeTaskFixture{al: al, lifecycle: lifecycle, starterMain: main.ID, taskSession: taskMeta.ID}
	al.asyncNotifier.registerObserver(func(e AsyncNotifyEvent) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.wakes = append(f.wakes, e)
	})
	return f
}

// persistNoEdgeTaskRecord writes the create_task child exactly as the
// launcher leaves it: origin task, running, NO steering edge.
func (f *noEdgeTaskFixture) persistNoEdgeTaskRecord(t *testing.T, taskID string) {
	t.Helper()
	require.NoError(t, f.lifecycle.Persist(&session.LifecycleRecord{
		SessionID: f.taskSession, State: session.LifecycleRunning, Generation: 1,
		OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: noEdgeWorkspace, AgentID: "native-agent",
		Origin: &session.Origin{Kind: session.OriginKindTask, TaskID: taskID},
	}))
	rec, err := f.lifecycle.Load(f.taskSession)
	require.NoError(t, err)
	require.Nil(t, rec.SteeredBy, "fixture: a create_task task has no steering edge")
}

func (f *noEdgeTaskFixture) starterWakes() []AsyncNotifyEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []AsyncNotifyEvent
	for _, e := range f.wakes {
		if e.TranscriptSessionID == f.starterMain {
			out = append(out, e)
		}
	}
	return out
}

func callWithoutPanic(t *testing.T, what string, fn func()) {
	t.Helper()
	var recovered any
	var stack []byte
	func() {
		defer func() {
			if r := recover(); r != nil {
				recovered, stack = r, debug.Stack()
			}
		}()
		fn()
	}()
	if recovered != nil {
		t.Fatalf("%s panicked: %v\n%s", what, recovered, stack)
	}
}

// TestTaskNoEdgeCompletion_IdleStarterWokenAndCompletionTailRuns completes a
// goal-bearing create_task task (success, Judge met) whose record has no
// steering edge while the starter main is idle. The completion tail must run
// to the end: no panic, lifecycle terminal, run recorded done, the starter
// woken once for the outcome, the paired goal ended, and the dependent
// unblocked.
func TestTaskNoEdgeCompletion_IdleStarterWokenAndCompletionTailRuns(t *testing.T) {
	f := newNoEdgeTaskFixture(t)
	al := f.al
	store := GetTaskStore(al)

	tk := mustCreateInProgressTask(t, al, &task.Task{
		AgentID: "native-agent", WorkspaceID: noEdgeWorkspace,
		Title: "export the report", Prompt: "write the CSV export",
		Criteria:  []task.AcceptanceCriterion{proseCriterion("", dodTestCriterionText)},
		SessionID: f.taskSession,
	})
	seeded := seedTaskGoal(t, tk.ID, dodTestCriterionText, dodTestDoDText)
	activateSeededTaskGoal(t, seeded.GoalID, f.taskSession)
	f.persistNoEdgeTaskRecord(t, tk.ID)

	dependent := &task.Task{
		AgentID: "native-agent", WorkspaceID: noEdgeWorkspace, Title: "publish the report",
		Prompt: "publish it", Action: task.ActionLLM, Priority: 3,
		Status: task.StatusBlocked, BlockedBy: []string{tk.ID},
	}
	require.NoError(t, store.Create(dependent))

	run := al.taskExecutor.openRun(tk.ID, nil, task.RunKindManual, f.taskSession, f.starterMain)
	require.NotNil(t, run, "fixture: the task run must open with the starter as its captured recipient")

	var applied bool
	callWithoutPanic(t, "completeTaskWithResult for a task with no steering edge and an idle starter", func() {
		applied = al.taskExecutor.completeTaskWithResult(tk, f.taskSession, task.StatusInProgress, true,
			"The CSV export is written and verified.", run)
	})
	require.True(t, applied, "the completion write must land")

	final, err := store.Get(tk.ID)
	require.NoError(t, err)
	require.Equal(t, task.StatusDone, final.Status)

	rec, err := f.lifecycle.Load(f.taskSession)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleCompleted, rec.State, "finalizeTaskLifecycle must make the record terminal")

	runs, err := store.ListRuns(tk.ID)
	require.NoError(t, err)
	var closed *task.TaskRun
	for i := range runs {
		if runs[i].RunID == run.runID {
			closed = &runs[i]
		}
	}
	require.NotNil(t, closed, "the opened run must be in the run history")
	require.Equal(t, task.StatusDone, closed.Status, "the run is recorded with the task's own outcome")

	wakes := f.starterWakes()
	require.Len(t, wakes, 1, "an idle starter is woken exactly once for the finished task (PW-D4, PW-D5)")
	require.True(t, strings.HasPrefix(wakes[0].SourceKind, "message_parent:"), "wake kind = %q", wakes[0].SourceKind)
	require.Contains(t, wakes[0].Content, "The CSV export is written and verified.")

	goalRec := readTaskGoal(t, tk.ID)
	require.True(t, goalRec.IsTerminal(), "the paired goal must end with its task, state=%q", goalRec.State)

	unblocked, err := store.Get(dependent.ID)
	require.NoError(t, err)
	require.NotEqual(t, task.StatusBlocked, unblocked.Status, "a done task must unblock its dependent")
}

// TestStoppedNoticeNoEdgeTaskRecord_NoPanic is the stopped-notice twin. The
// release wake helper dereferenced rec.SteeredBy without a check; a restart
// or historical-timeout notice for a record with no steering edge must be
// refused or delivered, never crash, and a landed-stop delivery pass over an
// edgeless record names the gap visibly.
func TestStoppedNoticeNoEdgeTaskRecord_NoPanic(t *testing.T) {
	f := newNoEdgeTaskFixture(t)
	al := f.al
	f.persistNoEdgeTaskRecord(t, "noedge-stop-task")
	rec, err := f.lifecycle.Load(f.taskSession)
	require.NoError(t, err)

	for _, cause := range []session.StopCause{session.StopCauseRestart, session.StopCauseTimeout} {
		t.Run(string(cause), func(t *testing.T) {
			tr := session.StoppedTransition{
				SessionID: f.taskSession, ParentSessionID: f.starterMain, Generation: rec.Generation,
				StopSeq: 1, Cause: cause, At: time.Now().UTC(),
			}
			id := fmt.Sprintf("stopped-notice:%s:%s:%d:%d", f.starterMain, f.taskSession, rec.Generation, tr.StopSeq)
			callWithoutPanic(t, "wakeParentForStoppedNotice for a record with no steering edge", func() {
				_ = al.wakeParentForStoppedNotice(context.Background(), rec, f.starterMain, id, tr)
			})
		})
	}

	// The real delivery pass: an edgeless record's landed-stop history is
	// either empty (nothing owed) or names a parent it has no edge for, which
	// is a visible error — never a panic.
	callWithoutPanic(t, "deliverLandedStopNotices for a record with no steering edge", func() {
		_, _ = al.deliverLandedStopNotices(context.Background(), rec)
	})
}
