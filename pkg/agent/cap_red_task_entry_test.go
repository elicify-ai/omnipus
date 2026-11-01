package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// Together with TaskMarkerPrecedesWorkerSpawn this tests S49d without a fake
// launcher or task worker. The real task producer runs; its caller is held at
// an unbuffered return handoff while the real worker is inside its model call.
// The source-order leg distinguishes marker-after-dispatchLaunchedTask-return,
// which a scheduler could otherwise hide before the worker is observed.
func TestCapRED_AdmissionView_TaskWorkerBeforeCallerReturns(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	provider, release := installParkedProvider(t, al)
	te := GetTaskExecutor(al)
	tk := capRedTask(t, al, "task-entry-boundary", task.StatusNext, "")
	parent := newTestSteeringSession(t, al, testHarnessWorkspaceMembershipID)
	child, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{SteeringSessionID: parent, TargetAgentID: testDefaultAgentID, Task: "hold task body", Label: tk.Title, Origin: steer.Origin{Kind: steer.OriginKindTask, TaskID: tk.ID}})
	if err != nil {
		t.Fatal(err)
	}
	ls := al.GetSessionLifecycleStore()
	rec, err := ls.Load(child.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	claim := executionClaim{SessionID: child.SessionID, Generation: child.Generation, RunID: freshRunID(), BootSeq: al.bootEpochFor()}
	if err := stampAdmissionExecution(ls, child.SessionID, child.Generation, claim.RunID, claim.BootSeq, rec.ExecutionID); err != nil {
		t.Fatal(err)
	}
	capRedAssert(t, "real task admission granted", capRedSteer(t, al.steerAdmission(), claim.SessionID, claim.Generation, claim.RunID, claim.BootSeq).granted, true)
	rec, err = commitSteeredExecutionState(ls, claim, session.LifecycleRunning, "")
	if err != nil {
		t.Fatal(err)
	}
	state := task.StatusInProgress
	if _, err := te.store.Update(tk.ID, task.Patch{Status: &state, SessionID: &child.SessionID}); err != nil {
		t.Fatal(err)
	}
	g := al.steerAdmission()
	g.entryMu.Lock()
	execution, err := al.attachSteeredExecution(claim)
	g.entryMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	returned := make(chan error)
	callerDone := make(chan struct{})
	go func() {
		defer close(callerDone)
		defer close(returned)
		returned <- te.dispatchLaunchedTask(rec, execution, te.finishTaskRun(execution, tk.ID, nil))
	}()
	defer func() {
		release()
		select {
		case err := <-returned:
			if err != nil {
				t.Errorf("real task dispatch: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("task producer did not reach caller return handoff")
		}
		<-callerDone
		te.wg.Wait()
	}()
	select {
	case <-provider.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("task body did not enter before caller returned")
	}
	v := capRedAdmissionView(t, al, claim, false)
	capRedAssert(t, "S49d task already running while caller has not returned", []any{v.state, v.present}, []any{"running", true})
	if text := capRedStatus(t, al, parent, child.SessionID); !strings.HasPrefix(text, "running,") {
		t.Errorf("S49d full status while worker inside body: %q", text)
	}
	capRedMarkerOrder(t, "task_executor.go", "dispatchLaunchedTask", "persistTaskSessionBinding", "runTaskFromInProgress")
}
