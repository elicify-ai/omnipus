package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func capRedRoster(t *testing.T, al *AgentLoop, workspace string) []map[string]any {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP missing agent")
	}
	ctx := tools.WithAgentID(tools.WithWorkspaceID(context.Background(), workspace), testDefaultAgentID)
	got := capRedToolJSON(t, inst.Tools.Execute(ctx, "list_jobs", map[string]any{"include_terminal": true, "limit": 200}))
	raw, ok := got["rows"].([]any)
	if !ok {
		t.Fatalf("real roster rows wrong shape: %#v", got)
	}
	rows := make([]map[string]any, 0, len(raw))
	for _, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("row wrong shape: %#v", v)
		}
		rows = append(rows, m)
	}
	return rows
}
func capRedFindRow(t *testing.T, rows []map[string]any, kind, id string) map[string]any {
	t.Helper()
	for _, row := range rows {
		if row["kind"] == kind && row["id"] == id {
			return row
		}
	}
	t.Fatalf("real collector missing %s/%s; rows=%#v", kind, id, rows)
	return nil
}
func capRedAssertWaiting(t *testing.T, v any, gate, cause string, limit, occupancy int, position bool) {
	t.Helper()
	wait, ok := v.(map[string]any)
	if !ok {
		t.Errorf("missing real waiting projection, got %#v", v)
		return
	}
	at, ok := wait["observed_at"].(string)
	if !ok {
		t.Errorf("waiting.observed_at absent: %#v", wait)
	} else if parsed, err := time.Parse(time.RFC3339Nano, at); err != nil || parsed.IsZero() {
		t.Errorf("waiting observation invalid: %q (%v)", at, err)
	}
	want := map[string]any{"gate": gate, "cause": cause, "limit": float64(limit), "in_flight": float64(occupancy), "observed_at": at}
	if position {
		want["queue_position"] = float64(1)
	}
	capRedAssert(t, "entire waiting projection (no title/assignee)", wait, want)
}

func TestCapRED_TaskWaitingProjection_RealCollector(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	_, release := installParkedProvider(t, al)
	defer release()
	te := GetTaskExecutor(al)
	te.autoSyncDispatchCapacity = false
	te.dispatchSema = newDispatchSemaphore(8)
	held := capRedHold(t, te.dispatchSema, 2)
	tk := capRedTask(t, al, "standalone-wait", task.StatusNext, "")
	high = true
	err := te.ExecuteTask(context.Background(), tk.ID, nil)
	if err == nil {
		t.Error("S22b task semaphore did not refuse the pressured third attempt")
	}
	row := capRedFindRow(t, capRedRoster(t, al, testHarnessWorkspaceMembershipID), "task", tk.ID)
	capRedAssertWaiting(t, row["waiting"], "task_semaphore", "memory_pressure", 2, 2, false)
	stored, e := GetTaskStore(al).Get(tk.ID)
	if e != nil {
		t.Fatal(e)
	}
	capRedAssert(t, "refusal preserves pre-dispatch next", stored.Status, task.StatusNext)
	// Gate-specific predicate: task-semaphore evidence cannot describe an
	// in_progress task, even if no retry has explicitly cleared the entry.
	status := task.StatusInProgress
	if _, e := GetTaskStore(al).Update(tk.ID, task.Patch{Status: &status}); e != nil {
		t.Fatal(e)
	}
	row = capRedFindRow(t, capRedRoster(t, al, testHarnessWorkspaceMembershipID), "task", tk.ID)
	if _, present := row["waiting"]; present {
		t.Errorf("S22b stale semaphore evidence survived in_progress: %#v", row)
	}
	held[0]()
	held[1]()
}

func TestCapRED_PlanWaitingMembers_BoundedRealCollector(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	ps := plan.New(t.TempDir())
	al.SetPlanStore(ps)
	p := mustCreatePlan(t, ps, &plan.Plan{ID: "bounded-plan", Title: "not a task row", OwnerAgentID: testDefaultAgentID, WorkspaceID: testHarnessWorkspaceMembershipID, State: plan.StateRunning})
	te := GetTaskExecutor(al)
	te.SetPlanStore(ps)
	te.autoSyncDispatchCapacity = false
	te.dispatchSema = newDispatchSemaphore(1)
	capRedHold(t, te.dispatchSema, 1)
	for i := 0; i < 4; i++ {
		tk := capRedTask(t, al, fmt.Sprintf("waiting-member-%d", i), task.StatusNext, p.ID)
		err := te.ExecuteTask(context.Background(), tk.ID, nil)
		if err == nil {
			t.Fatalf("SETUP configured-full member %d launched", i)
		}
	}
	rows := capRedRoster(t, al, testHarnessWorkspaceMembershipID)
	row := capRedFindRow(t, rows, "plan", p.ID)
	capRedAssert(t, "S22c total refused member count", row["waiting_member_count"], float64(4))
	list, okList := row["waiting_members"].([]any)
	if !okList {
		t.Errorf("S22c missing bounded waiting_members: %#v", row)
	} else {
		capRedAssert(t, "S22c exactly three sample entries", len(list), 3)
		seen := map[string]bool{}
		for _, entry := range list {
			m, ok := entry.(map[string]any)
			if !ok {
				t.Fatalf("bad member entry: %#v", entry)
			}
			id, _ := m["task_id"].(string)
			if id == "" || seen[id] {
				t.Errorf("member id missing/duplicated: %#v", list)
			}
			seen[id] = true
			copy := map[string]any{}
			for k, v := range m {
				if k != "task_id" {
					copy[k] = v
				}
			}
			capRedAssertWaiting(t, copy, "task_semaphore", "configured", 1, 1, false)
		}
	}
	for _, r := range rows {
		if r["kind"] == "task" {
			t.Errorf("plan member leaked into standalone task rows: %#v", r)
		}
	}
	// S38: owning the plan in one workspace grants no visibility in another.
	foreign := capRedRoster(t, al, "unrelated-workspace")
	capRedAssert(t, "foreign workspace cannot see member restrictions", len(foreign), 0)
	// S53: stopping the live plan drops both projections on the next read.
	failed := plan.StateFailed
	if _, err := ps.Update(p.ID, plan.Patch{State: &failed}); err != nil {
		t.Fatal(err)
	}
	row = capRedFindRow(t, capRedRoster(t, al, testHarnessWorkspaceMembershipID), "plan", p.ID)
	for _, key := range []string{"waiting_members", "waiting_member_count", "waiting_runs", "waiting_run_count"} {
		if _, present := row[key]; present {
			t.Errorf("S53 stopped plan retained %s", key)
		}
	}
}

func TestCapRED_SteeredTaskWait_VisibleWhileInProgress(t *testing.T) {
	for _, member := range []bool{false, true} {
		t.Run(fmt.Sprintf("S22d_S22e_member_%t", member), func(t *testing.T) {
			high, ok := false, true
			capRedMemory(t, &high, &ok)
			al, _ := newSteerAL(t)
			_, release := installParkedProvider(t, al)
			defer release()
			installTestSessionLauncher(t, al)
			al.GetConfig().Performance.MaxParallelAgents = 8
			capRedFillSteer(t, al.steerAdmission(), 8)
			main, err := al.GetSessionStore().GetOrCreateMainSession(testHarnessWorkspaceMembershipID, testDefaultAgentID)
			if err != nil {
				t.Fatal(err)
			}
			te := GetTaskExecutor(al)
			te.SetMainSessionResolver(&u6FakeMainResolver{elig: map[[2]string]string{{testHarnessWorkspaceMembershipID, testDefaultAgentID}: main.ID}})
			planID := ""
			if member {
				ps := plan.New(t.TempDir())
				al.SetPlanStore(ps)
				te.SetPlanStore(ps)
				p := mustCreatePlan(t, ps, &plan.Plan{ID: "steered-plan", Title: "main-owned plan", OwnerAgentID: testDefaultAgentID, WorkspaceID: testHarnessWorkspaceMembershipID, State: plan.StateRunning})
				planID = p.ID
			}
			tk := capRedTask(t, al, "main-wait", task.StatusNext, planID)
			isolated := false
			if _, err := GetTaskStore(al).Update(tk.ID, task.Patch{RunIsolated: &isolated}); err != nil {
				t.Fatal(err)
			}
			wireSteerCompletionDeps(t, al)
			repairInbox := capRedBlockInbox(t, al)
			logs := capRedLogs(t)
			high = true
			// S22d/e: force a real shared Deliver Append failure. The roster
			// must remain truthful even though the parent did not get a notice.
			if err := te.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
				t.Fatalf("real main-owned queued launch: %v", err)
			}
			repairInbox()
			capRedAssert(t, "S22d/e actual failed notice emits one warning", capRedWarnCount(logs()), 1)
			stored, err := GetTaskStore(al).Get(tk.ID)
			if err != nil {
				t.Fatal(err)
			}
			capRedAssert(t, "steered run never moved back to next", stored.Status, task.StatusInProgress)
			rows := capRedRoster(t, al, testHarnessWorkspaceMembershipID)
			if !member {
				row := capRedFindRow(t, rows, "task", tk.ID)
				capRedAssert(t, "durable in_progress normalizes to running", row["status"], "running")
				capRedAssertWaiting(t, row["waiting"], "steered", "memory_pressure", 2, 8, true)
			} else {
				row := capRedFindRow(t, rows, "plan", planID)
				capRedAssert(t, "one main-owned waiting run", row["waiting_run_count"], float64(1))
				list, yes := row["waiting_runs"].([]any)
				if !yes || len(list) != 1 {
					t.Errorf("missing one waiting_runs entry: %#v", row)
				} else {
					m := list[0].(map[string]any)
					capRedAssert(t, "member id", m["task_id"], tk.ID)
					copy := map[string]any{}
					for k, v := range m {
						if k != "task_id" {
							copy[k] = v
						}
					}
					capRedAssertWaiting(t, copy, "steered", "memory_pressure", 2, 8, true)
				}
			}
			for _, r := range rows {
				if r["kind"] == "subagent" && r["id"] == stored.SessionID {
					t.Errorf("task-origin run leaked into subagent row: %#v", r)
				}
				if member && r["kind"] == "task" && r["id"] == tk.ID {
					t.Errorf("member leaked into task row: %#v", r)
				}
			}
		})
	}
}
