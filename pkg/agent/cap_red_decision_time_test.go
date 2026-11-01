package agent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestCapRED_TaskStart_DecisionTimeSurvivesQueuedCommit(t *testing.T) {
	al, inst, _ := newPlanToolWiringTestLoop(t)
	te := GetTaskExecutor(al)
	method := reflect.ValueOf(te.StartTaskNowAs)
	if method.Type().Out(0).Kind() != reflect.Struct {
		t.Fatal("CAP-A5/S12h RED: StartTaskNowAs returns no TaskStartOutcome carrying the decision's ObservedAt")
	}
	// Observe the actual starter return at the tools/agent injection boundary.
	// This wrapper returns the REAL executor's result unchanged, never a canned
	// outcome. The tool's own precheck and the real launcher still execute.
	raw, exists := inst.Tools.Get("run_task")
	if !exists {
		t.Fatal("registered run_task missing")
	}
	setter := reflect.ValueOf(raw).MethodByName("SetStartTaskNow")
	if !setter.IsValid() {
		t.Fatal("real task starter injection boundary missing")
	}
	fnType := setter.Type().In(0)
	if fnType.NumIn() != 2 || fnType.NumOut() != 2 || fnType.Out(0) != method.Type().Out(0) {
		t.Fatal("run_task starter must carry the real TaskStartOutcome unchanged")
	}
	outcomes := make(chan []reflect.Value, 1)
	returnedAt := make(chan time.Time, 1)
	setter.Call([]reflect.Value{reflect.MakeFunc(fnType, func(args []reflect.Value) []reflect.Value {
		out := method.Call([]reflect.Value{args[0], args[1], reflect.Zero(method.Type().In(2))})
		outcomes <- out
		returnedAt <- time.Now()
		return out
	})})
	al.GetConfig().Performance.MaxParallelAgents = 8
	var pressured, armed atomic.Bool
	var owned atomic.Pointer[sync.Mutex]
	locked := make(chan string, 1)
	restore := config.SetMemoryProviderForTest(func() (bool, bool) {
		if armed.Load() {
			tk, err := te.store.Get("delayed-queued-commit")
			if err == nil && tk.SessionID != "" && armed.CompareAndSwap(true, false) {
				lock := al.GetSessionLifecycleStore().Lock(tk.SessionID)
				lock.Lock()
				owned.Store(lock)
				locked <- tk.SessionID
			}
		}
		return pressured.Load(), true
	}, func() (uint64, bool) { return 1 << 30, true })
	t.Cleanup(restore)
	capRedFillSteer(t, al.steerAdmission(), 8)
	tk := &task.Task{ID: "delayed-queued-commit", Title: "delayed", AgentID: "planner-agent", WorkspaceID: testHarnessWorkspaceMembershipID, Status: task.StatusNext, RunIsolated: true}
	if err := te.store.Create(tk); err != nil {
		t.Fatal(err)
	}
	ctx := tools.WithAgentID(tools.WithWorkspaceID(context.Background(), testHarnessWorkspaceMembershipID), "planner-agent")
	pressured.Store(true)
	armed.Store(true)
	done := make(chan *tools.ToolResult, 1)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		done <- inst.Tools.Execute(ctx, "run_task", map[string]any{"task_id": tk.ID})
	}()
	defer func() {
		armed.Store(false)
		if lock := owned.Swap(nil); lock != nil {
			lock.Unlock()
		}
		select {
		case <-joined:
		case <-time.After(3 * time.Second):
			t.Error("queued tool call did not join after filesystem barrier release")
		}
	}()
	var sid string
	select {
	case sid = <-locked:
	case result := <-done:
		t.Fatalf("queued commit boundary not held: %+v", result)
	case <-time.After(3 * time.Second):
		t.Fatal("missing real decision/queued-commit boundary")
	}
	at := capRedStoredQueueTime(t, al.steerAdmission(), sid)
	if at.IsZero() {
		t.Fatal("S12h actual queue has no decision time")
	}
	<-time.NewTimer(2100 * time.Millisecond).C // intentional clock aging
	owned.Swap(nil).Unlock()
	var result *tools.ToolResult
	select {
	case result = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("queued tool result did not finish after filesystem release")
	}
	out := <-outcomes
	if !out[1].IsNil() {
		t.Fatalf("real task starter: %v", out[1].Interface())
	}
	outcome := out[0]
	capRedAssert(t, "S12h outcome retains decision T0", outcome.FieldByName("ObservedAt").Interface(), at)
	capRedAssert(t, "S12h queued outcome identity", []any{outcome.FieldByName("State").String(), outcome.FieldByName("SessionID").String()}, []any{"queued", sid})
	got := capRedToolJSON(t, result)
	lo, hi := int((<-returnedAt).Sub(at)/time.Second), int(time.Since(at)/time.Second)
	if lo < 2 || hi >= 60 {
		t.Fatalf("fixture's short clock-aging interval invalid: %d..%d seconds", lo, hi)
	}
	matched := false
	for seconds := lo; seconds <= hi; seconds++ {
		note := fmt.Sprintf("task started and queued: this machine is low on memory, so new delegated turns are held at 2 at a time (8 in use, checked %d seconds ago); its turn has not started and it is number 1 in line", seconds)
		want := map[string]any{"task_id": tk.ID, "status": "in_progress", "session_id": sid, "queue_position": float64(1), "concurrency_limit": float64(2), "note": note}
		matched = matched || reflect.DeepEqual(got, want)
	}
	if !matched {
		t.Errorf("S12h fresh full tool result must age from T0 (%d..%d seconds), not delayed commit: %#v", lo, hi, got)
	}
	capRedAssert(t, "queue still retains original T0", capRedStoredQueueTime(t, al.steerAdmission(), sid), at)
}

func TestCapRED_TaskWaitingProjection_DecisionTimeSurvivesPublicationDelay(t *testing.T) {
	al, _ := newSteerAL(t)
	te := GetTaskExecutor(al)
	te.autoSyncDispatchCapacity = false
	te.dispatchSema = newDispatchSemaphore(8)
	if reflect.TypeOf(te.dispatchSema.TryAcquire).NumOut() != 6 {
		t.Fatal("CAP-B2/S22f RED: real TryAcquire has no immutable observedAt return; no made-up publication time substituted")
	}
	var armed atomic.Bool
	blocked := make(chan struct{}, 1)
	restore := config.SetMemoryProviderForTest(func() (bool, bool) {
		if armed.CompareAndSwap(true, false) {
			te.mu.Lock()
			blocked <- struct{}{}
		}
		return true, true
	}, func() (uint64, bool) { return 1 << 30, true })
	t.Cleanup(restore)
	// Configured=1 controls use healthy or pressured memory identically; use
	// actual configured-eight gate with two existing holders for this case.
	restoreHealthy := config.SetMemoryProviderForTest(func() (bool, bool) { return false, true }, func() (uint64, bool) { return 1 << 30, true })
	capRedHold(t, te.dispatchSema, 2)
	restoreHealthy()
	tk := capRedTask(t, al, "delayed-refusal", task.StatusNext, "")
	start := time.Now()
	armed.Store(true)
	done := make(chan error, 1)
	joined := make(chan struct{})
	go func() { defer close(joined); done <- te.ExecuteTask(context.Background(), tk.ID, nil) }()
	select {
	case <-blocked:
	case err := <-done:
		t.Fatalf("real refusal did not reach memory decision: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("missing real task-memory decision")
	}
	// The storage guard is the executor's existing mu. If GREEN moves the
	// projection to another owner, adapt this I/O/publication barrier, never
	// keep a second storage owner for this test or change the T0 oracle.
	te.dispatchSema.mu.Lock()
	gateDone := time.Now()
	te.dispatchSema.mu.Unlock()
	unlocked := false
	defer func() {
		if !unlocked {
			te.mu.Unlock()
		}
		select {
		case <-joined:
		case <-time.After(3 * time.Second):
			t.Error("refusal publisher did not join after releasing publication barrier")
		}
	}()
	<-time.NewTimer(2100 * time.Millisecond).C
	te.mu.Unlock()
	unlocked = true
	var refusal error
	select {
	case refusal = <-done:
		if !errors.Is(refusal, ErrDispatchCapReached) {
			t.Fatalf("actual pressured task attempt must return its typed refusal: %v", refusal)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("refusal publication did not join")
	}
	row := capRedFindRow(t, capRedRoster(t, al, testHarnessWorkspaceMembershipID), "task", tk.ID)
	capRedAssertWaiting(t, row["waiting"], "task_semaphore", "memory_pressure", 2, 2, false)
	wait, yes := row["waiting"].(map[string]any)
	if !yes {
		return
	}
	text, _ := wait["observed_at"].(string)
	at, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		t.Fatal(err)
	}
	if at.Before(start) || at.After(gateDone) {
		t.Errorf("S22f observation was restamped outside real decision interval [%s,%s]: %s", start, gateDone, at)
	}
	// CAP-B2 requires the refusal to carry the SAME decision tuple. Compare
	// its actual observation to the published row, not to a later acquisition.
	carried := capRedRefusalTime(t, reflect.ValueOf(refusal), 0)
	if !carried.IsValid() {
		t.Error("CAP-B2 refusal omitted its immutable decision time")
	} else if !carried.Equal(reflect.ValueOf(at)) {
		t.Error("S22f row timestamp differs from the same attempt's actual refusal tuple")
	}
	// Separately exercise the real return contract: no post-decision counter
	// reconstruction is used to obtain numbers or its nonzero decision time.
	d := capRedAcquire(t, te.dispatchSema)
	capRedAssert(t, "real TryAcquire refusal evidence", []any{d.granted, d.occupancy, d.limit, d.cause, d.complete}, []any{false, 2, 2, "memory_pressure", true})
	if d.observed.IsZero() {
		t.Error("S22f real TryAcquire return has zero decision time")
	}
}

// Read-only adapter for the real refusal's internal tuple; no type or field
// name is imposed beyond its one mandated time.Time. No decision is invented.
func capRedRefusalTime(t *testing.T, v reflect.Value, depth int) reflect.Value {
	t.Helper()
	if !v.IsValid() || depth > 8 {
		return reflect.Value{}
	}
	if v.Type() == reflect.TypeFor[time.Time]() {
		return v
	}
	if v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if !v.IsNil() {
			return capRedRefusalTime(t, v.Elem(), depth+1)
		}
	} else if v.Kind() == reflect.Struct {
		for i := 0; i < v.NumField(); i++ {
			if found := capRedRefusalTime(t, v.Field(i), depth+1); found.IsValid() {
				return found
			}
		}
	}
	return reflect.Value{}
}
