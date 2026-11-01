package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func capRedToolJSON(t *testing.T, result *tools.ToolResult) map[string]any {
	t.Helper()
	if result == nil || result.IsError {
		t.Fatalf("real tool failed instead of returning evidence: %+v", result)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result.ForLLM), &got); err != nil {
		t.Fatalf("decode real result %q: %v", result.ForLLM, err)
	}
	return got
}
func capRedTask(t *testing.T, al *AgentLoop, id string, status task.Status, planID string) *task.Task {
	t.Helper()
	// Use the loop's real stores, not a second lifecycle directory. The general
	// steering fixture does not wire these executor dependencies at gateway boot.
	te := GetTaskExecutor(al)
	te.SetLifecycleStore(al.GetSessionLifecycleStore())
	te.SetSessionLauncher(NewSteerLauncher(al))
	// The general steering fixture omits task completion permission. Seed the
	// shipped permission in this task fixture, as newPlanToolWiringTestLoop does,
	// so an admitted real worker can reach its model rather than fail readiness.
	inst, exists := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !exists {
		t.Fatal("task fixture agent missing")
	}
	var policy tools.ToolPolicyCfg
	if current := inst.LoadToolPolicy(); current != nil {
		policy = *current
	}
	policy.Policies = maps.Clone(policy.Policies)
	policy.GlobalPolicies = maps.Clone(policy.GlobalPolicies)
	if policy.Policies == nil {
		policy.Policies = make(map[string]config.ToolPolicy)
	}
	if policy.GlobalPolicies == nil {
		policy.GlobalPolicies = make(map[string]config.ToolPolicy)
	}
	policy.Policies["goal_claim"] = config.ToolPolicyAllow
	policy.GlobalPolicies["goal_claim"] = config.ToolPolicyAllow
	inst.StoreToolPolicy(&policy)
	tk := &task.Task{ID: id, Title: "CAP test task", Prompt: "Stay running until released", AgentID: testDefaultAgentID, WorkspaceID: testHarnessWorkspaceMembershipID, Status: status, PlanID: planID, RunIsolated: true}
	if err := GetTaskStore(al).Create(tk); err != nil {
		t.Fatal(err)
	}
	return tk
}
func capRedRunTool(t *testing.T, al *AgentLoop, taskID string) map[string]any {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP missing agent")
	}
	ctx := tools.WithAgentID(tools.WithWorkspaceID(context.Background(), testHarnessWorkspaceMembershipID), testDefaultAgentID)
	return capRedToolJSON(t, inst.Tools.Execute(ctx, "run_task", map[string]any{"task_id": taskID}))
}

func TestCapRED_RunTask_QueuedDisclosure(t *testing.T) {
	for _, tc := range []struct {
		id               string
		configured, held int
		high, ok         bool
		format           string
	}{
		{"S12_configured", 8, 8, false, true, "task started and queued: all %d delegated turn slots are busy (%d in use, checked just now); its turn has not started and it is number %d in line"},
		{"S12a_pressure", 8, 2, true, true, "task started and queued: this machine is low on memory, so new delegated turns are held at %d at a time (%d in use, checked just now); its turn has not started and it is number %d in line"},
		{"S12b_unverifiable", 8, 2, false, false, "task started and queued: this machine cannot report its available memory, so new delegated turns are held at %d at a time (%d in use, checked just now); its turn has not started and it is number %d in line"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			high, ok := false, true
			capRedMemory(t, &high, &ok)
			al, _, _ := newPlanToolWiringTestLoop(t)
			al.GetConfig().Performance.MaxParallelAgents = tc.configured
			capRedFillSteer(t, al.steerAdmission(), tc.held)
			high, ok = tc.high, tc.ok
			tk := &task.Task{ID: "cap-run", Title: "CAP task", Prompt: "do nothing", AgentID: "planner-agent", WorkspaceID: testHarnessWorkspaceMembershipID, Status: task.StatusNext, RunIsolated: true}
			if err := GetTaskStore(al).Create(tk); err != nil {
				t.Fatal(err)
			}
			inst, _ := al.GetRegistry().GetAgent("planner-agent")
			ctx := tools.WithAgentID(tools.WithWorkspaceID(context.Background(), testHarnessWorkspaceMembershipID), "planner-agent")
			got := capRedToolJSON(t, inst.Tools.Execute(ctx, "run_task", map[string]any{"task_id": tk.ID}))
			sid, _ := got["session_id"].(string)
			if sid == "" {
				t.Error("queued launch must have its real durable session id")
			}
			limit := tc.configured
			if tc.high || !tc.ok {
				limit = 2
			}
			want := map[string]any{"task_id": tk.ID, "status": "in_progress", "session_id": sid, "queue_position": float64(1), "concurrency_limit": float64(limit), "note": fmt.Sprintf(tc.format, limit, tc.held, 1)}
			capRedAssert(t, "complete queued JSON", got, want)
			stored, err := GetTaskStore(al).Get(tk.ID)
			if err != nil {
				t.Fatal(err)
			}
			capRedAssert(t, "queued launch stays durably in_progress", stored.Status, task.StatusInProgress)
		})
	}
}

func TestCapRED_RunTask_RepeatQueuedDoesNotDispatch(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _, _ := newPlanToolWiringTestLoop(t)
	al.GetConfig().Performance.MaxParallelAgents = 1
	capRedFillSteer(t, al.steerAdmission(), 1)
	tk := &task.Task{ID: "repeat-queued", Title: "repeat", AgentID: "planner-agent", WorkspaceID: testHarnessWorkspaceMembershipID, Status: task.StatusNext, RunIsolated: true}
	if err := GetTaskStore(al).Create(tk); err != nil {
		t.Fatal(err)
	}
	inst, _ := al.GetRegistry().GetAgent("planner-agent")
	ctx := tools.WithAgentID(context.Background(), "planner-agent")
	first := capRedToolJSON(t, inst.Tools.Execute(ctx, "run_task", map[string]any{"task_id": tk.ID}))
	sid := first["session_id"]
	// No release or new admission event occurs between the two real calls.
	before := al.steerAdmission().queueLen()
	gotResult := inst.Tools.Execute(ctx, "run_task", map[string]any{"task_id": tk.ID})
	if gotResult.IsError {
		t.Fatalf("S12d repeat redispatched instead of resolving live queue: %s", gotResult.ForLLM)
	}
	got := capRedToolJSON(t, gotResult)
	want := map[string]any{"task_id": tk.ID, "status": "in_progress", "session_id": sid, "queue_position": float64(1), "concurrency_limit": float64(1), "note": "task is queued: its turn has not started and it is number 1 in line. At its last check (just now) the lane allowed 1 delegated turns at a time (1 in use then): the lane was at its configured limit."}
	capRedAssert(t, "S12d complete dated repeat", got, want)
	capRedAssert(t, "repeat creates no queue entry", al.steerAdmission().queueLen(), before)
}

func TestCapRED_RunTask_ReservationWithoutSessionReportsStarting(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, inst, _ := newPlanToolWiringTestLoop(t)
	te := GetTaskExecutor(al)
	tk := &task.Task{ID: "only-reserved", Title: "reserved", AgentID: "planner-agent", WorkspaceID: testHarnessWorkspaceMembershipID, Status: task.StatusInProgress}
	if err := GetTaskStore(al).Create(tk); err != nil {
		t.Fatal(err)
	}
	if !te.reserveRunSlot(tk.ID) {
		t.Fatal("SETUP reservation failed")
	}
	defer te.releaseReservedRunSlot(tk.ID)
	result := inst.Tools.Execute(context.Background(), "run_task", map[string]any{"task_id": tk.ID})
	got := capRedToolJSON(t, result)
	capRedAssert(t, "S12g exact note", got["note"], "task launch is still being set up; its turn has not started yet — no action taken")
	if sid, ok := got["session_id"]; ok && sid != "" {
		t.Errorf("reservation without persisted session invented id %v", sid)
	}
	capRedAssert(t, "no admission created", al.steerAdmission().activeCount(), 0)
	capRedAssert(t, "no queue created", al.steerAdmission().queueLen(), 0)
}

func TestCapRED_SessionWorker_RefusalNamesCause(t *testing.T) {
	for _, tc := range []struct {
		id               string
		configured, held int
		high, ok         bool
		text             string
	}{
		{"S14_S54_over_lowered_limit", 8, 8, true, true, "I can't take on new work right now: this machine is low on memory, so new conversations are held at 2 at a time (8 in use). Please try again in a few seconds, or stop some running work to free memory."},
		{"S15_unverifiable", 8, 2, false, false, "I can't take on new work right now: this machine cannot report its available memory, so new conversations are held at 2 at a time (2 in use). Stop a running conversation to free a slot."},
		{"S16_configured", 3, 3, false, true, "I'm handling as many conversations as I can right now (3 of 3). Please try again in a few seconds."},
	} {
		t.Run(tc.id, func(t *testing.T) {
			high, ok := false, true
			capRedMemory(t, &high, &ok)
			a := newAdmissionController(tc.configured)
			for i := 0; i < tc.held; i++ {
				yes, rel := a.TryAdmit(fmt.Sprint(i))
				if !yes {
					t.Fatal("SETUP refused holder")
				}
				defer rel()
			}
			high, ok = tc.high, tc.ok
			b := bus.NewMessageBus()
			al := &AgentLoop{admission: a, bus: b}
			got := al.dispatchSessionWorker("refused", bus.InboundMessage{Channel: "webchat", ChatID: "cap-chat", SessionID: "cap-session", Content: "hello"})
			capRedAssert(t, "refusal returned to dispatcher", got, false)
			select {
			case msg := <-b.OutboundChan():
				capRedAssert(t, "exact person-facing refusal", []string{msg.Channel, msg.ChatID, msg.Content}, []string{"webchat", "cap-chat", tc.text})
			default:
				t.Fatal("refusal failed to reach caller")
			}
			capRedAssert(t, "refusal does not preempt held conversations", a.ActiveScopes(), tc.held)
		})
	}
}

func TestCapRED_ExecuteTask_RefusalNamesCause(t *testing.T) {
	// CAP-B5 governs the sentinel itself, not only the decided refusal wrapper.
	capRedAssert(t, "CAP-B5 sentinel exact text", ErrDispatchCapReached.Error(), "task dispatch is at its limit")
	for _, tc := range []struct {
		id        string
		cap, held int
		high, ok  bool
		want      string
	}{
		{"S29_S55_pressure", 8, 2, true, true, "task_executor: task dispatch is held at 2 by low memory (2 in flight) for task cap-refusal — retried on the next pass"},
		{"S30_configured", 2, 2, false, true, "task_executor: no task dispatch slot is free (2/2 in flight) for task cap-refusal — retried on the next pass"},
		{"S28a_unverifiable", 8, 2, false, false, "task_executor: task dispatch is held at 2 because this machine cannot report its available memory (2 in flight) for task cap-refusal — retried on the next pass"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			high, ok := false, true
			capRedMemory(t, &high, &ok)
			al, _ := newSteerAL(t)
			te := GetTaskExecutor(al)
			te.autoSyncDispatchCapacity = false
			te.dispatchSema = newDispatchSemaphore(tc.cap)
			capRedHold(t, te.dispatchSema, tc.held)
			tk := capRedTask(t, al, "cap-refusal", task.StatusNext, "")
			high, ok = tc.high, tc.ok
			err := te.ExecuteTask(context.Background(), tk.ID, nil)
			if err == nil {
				t.Error("expected a refused task dispatch, but it launched")
				return
			}
			capRedAssert(t, "typed sentinel remains reachable", errors.Is(err, ErrDispatchCapReached), true)
			capRedAssert(t, "exact decided refusal text", err.Error(), tc.want)
			if strings.Contains(err.Error(), "global dispatch cap") {
				t.Errorf("retired sentinel phrase leaked: %s", err)
			}
			stored, e := GetTaskStore(al).Get(tk.ID)
			if e != nil {
				t.Fatal(e)
			}
			capRedAssert(t, "semaphore refusal stays next", stored.Status, task.StatusNext)
		})
	}
}

func capRedLaunchChild(t *testing.T, al *AgentLoop, parent, label string) steer.LaunchResult {
	t.Helper()
	req := steer.LaunchRequest{TargetAgentID: testDefaultAgentID, Task: "hold", Label: label, SteeringSessionID: parent, Origin: steer.Origin{Kind: steer.OriginKindDelegate}}
	if parent == "" {
		req.WorkspaceID = testHarnessWorkspaceMembershipID
		req.Owner = "cap-test"
	}
	r, e := NewSteerLauncher(al).Launch(context.Background(), req)
	if e != nil {
		t.Fatalf("SETUP real child launch: %v", e)
	}
	return r
}
func capRedStatus(t *testing.T, al *AgentLoop, parent, sid string) string {
	t.Helper()
	ctx := tools.WithTranscriptSessionID(context.Background(), parent)
	result := capRedDelegateTool(t, al).Execute(ctx, map[string]any{"action": "status", "session_id": sid})
	if result == nil || result.IsError {
		t.Fatalf("real delegate status failed: %+v", result)
	}
	return result.ForLLM
}

func TestCapRED_DelegateStatus_RecordIsNotExecution(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	parent := newTestSteeringSession(t, al, testHarnessWorkspaceMembershipID)
	child := capRedLaunchChild(t, al, parent, "stamp-before-reservation")
	// S49c's discriminating read: legitimate nonterminal record, identity stamped,
	// no gate reservation. This asserts the full status surface, not delegate peek.
	err := al.GetSessionLifecycleStore().Mutate(child.SessionID, func(r *session.LifecycleRecord) error {
		r.State = session.LifecycleRunning
		r.ExecutionID = &session.ExecutionIdentity{RunID: "new-stamp", BootSeq: al.bootEpochFor()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := capRedStatus(t, al, parent, child.SessionID)
	wantPrefix := "running (recorded session state; no turn is executing now),"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("S49c no-view leading state got %q; want prefix %q", got, wantPrefix)
	}
	if strings.Contains(got, "Admission:") {
		t.Errorf("no-view status invented an admission: %q", got)
	}
	capRedAssert(t, "no reservation created by read", al.steerAdmission().activeCount(), 0)
}

func TestCapRED_AdmissionView_InitialEntryBoundary(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	_, release := installParkedProvider(t, al)
	defer release()
	parent := newTestSteeringSession(t, al, testHarnessWorkspaceMembershipID)
	child := capRedLaunchChild(t, al, parent, "starting-window")
	calls := 0
	previous := dispatchStateWriteTestHook
	dispatchStateWriteTestHook = func(sid string, _ int) {
		if sid != child.SessionID {
			return
		}
		calls++
		got := capRedStatus(t, al, parent, sid)
		capRedAssert(t, "S49 reservation before durable commit is starting", strings.SplitN(got, ",", 2)[0], "starting")
		if strings.Contains(got, "Admission:") {
			t.Errorf("starting view has a cap clause: %q", got)
		}
	}
	defer func() { dispatchStateWriteTestHook = previous }()
	dr, err := NewSteerLauncher(al).Dispatch(context.Background(), child.SessionID, child.Generation)
	if err != nil {
		t.Fatal(err)
	}
	capRedAssert(t, "exactly one pre-commit hook", calls, 1)
	capRedAssert(t, "successful handoff result", dr.State, steer.DispatchRunning)
	// A real body is parked at the model edge, so a running read cannot be an
	// after-completion session-state accident.
	got := capRedStatus(t, al, parent, child.SessionID)
	capRedAssert(t, "after producer boundary running", strings.SplitN(got, ",", 2)[0], "running")
}
