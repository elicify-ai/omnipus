package agent

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

func TestCapRED_TaskWaitingProjection_AllStatusPredicatesAndDelete(t *testing.T) {
	for _, status := range []task.Status{task.StatusInbox, task.StatusInProgress, task.StatusDone, task.StatusFailed, task.StatusBlocked, "delete"} {
		t.Run(string(status), func(t *testing.T) {
			high, ok := false, true
			capRedMemory(t, &high, &ok)
			al, _ := newSteerAL(t)
			te := GetTaskExecutor(al)
			te.autoSyncDispatchCapacity = false
			te.dispatchSema = newDispatchSemaphore(1)
			capRedHold(t, te.dispatchSema, 1)
			tk := capRedTask(t, al, "invalidated-task", task.StatusNext, "")
			if err := te.ExecuteTask(context.Background(), tk.ID, nil); err == nil {
				t.Fatal("configured-full task was not refused")
			}
			row := capRedFindRow(t, capRedRoster(t, al, testHarnessWorkspaceMembershipID), "task", tk.ID)
			capRedAssertWaiting(t, row["waiting"], "task_semaphore", "configured", 1, 1, false)
			switch status {
			case "delete":
				if _, err := GetTaskStore(al).Delete(tk.ID); err != nil {
					t.Fatal(err)
				}
			case task.StatusBlocked:
				dependency := capRedTask(t, al, "unmet-dependency", task.StatusNext, "")
				deps := []string{dependency.ID}
				if _, err := GetTaskStore(al).Update(tk.ID, task.Patch{BlockedBy: &deps}); err != nil {
					t.Fatal(err)
				}
			default:
				if _, err := GetTaskStore(al).Update(tk.ID, task.Patch{Status: &status}); err != nil {
					t.Fatal(err)
				}
			}
			for _, r := range capRedRoster(t, al, testHarnessWorkspaceMembershipID) {
				if r["kind"] == "task" && r["id"] == tk.ID {
					if status == "delete" {
						t.Error("deleted task still has row")
					}
					if _, yes := r["waiting"]; yes {
						t.Errorf("S53 stale wait survives %s: %#v", status, r)
					}
				}
			}
		})
	}
}

func TestCapRED_TaskWaitingProjection_GrantClearsWithoutTimer(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	installTestSessionLauncher(t, al)
	provider, release := installParkedProvider(t, al)
	defer release()
	te := GetTaskExecutor(al)
	te.autoSyncDispatchCapacity = false
	te.dispatchSema = newDispatchSemaphore(1)
	held := capRedHold(t, te.dispatchSema, 1)
	tk := capRedTask(t, al, "grant-clears", task.StatusNext, "")
	if err := te.ExecuteTask(context.Background(), tk.ID, nil); err == nil {
		t.Fatal("configured-full task not refused")
	}
	row := capRedFindRow(t, capRedRoster(t, al, testHarnessWorkspaceMembershipID), "task", tk.ID)
	capRedAssertWaiting(t, row["waiting"], "task_semaphore", "configured", 1, 1, false)
	held[0]()
	if err := te.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("real granted task did not reach model edge")
	}
	row = capRedFindRow(t, capRedRoster(t, al, testHarnessWorkspaceMembershipID), "task", tk.ID)
	if _, yes := row["waiting"]; yes {
		t.Errorf("S53 granted task retained old refusal: %#v", row)
	}
}

func TestCapRED_SteeredWaitingProjection_CancelAndGrant(t *testing.T) {
	for _, member := range []bool{false, true} {
		for _, cancel := range []bool{false, true} {
			t.Run(fmt.Sprintf("member_%t_cancel_%t", member, cancel), func(t *testing.T) {
				high, ok := false, true
				capRedMemory(t, &high, &ok)
				al, _ := newSteerAL(t)
				wireSteerCompletionDeps(t, al)
				provider, release := installParkedProvider(t, al)
				defer release()
				tk, _, holders := capRedQueuedMainMember(t, al)
				if !member {
					empty := ""
					if _, err := GetTaskStore(al).Update(tk.ID, task.Patch{PlanID: &empty}); err != nil {
						t.Fatal(err)
					}
				}
				repairInbox := capRedBlockInbox(t, al)
				high = true
				if err := GetTaskExecutor(al).ExecuteTask(context.Background(), tk.ID, nil); err != nil {
					t.Fatal(err)
				}
				repairInbox() // The roster is the independent fallback after actual Append failure.
				stored, err := GetTaskStore(al).Get(tk.ID)
				if err != nil {
					t.Fatal(err)
				}
				claim := capRedClaim(t, al, stored.SessionID)
				rows := capRedRoster(t, al, testHarnessWorkspaceMembershipID)
				kind, id, key := "task", tk.ID, "waiting"
				if member {
					kind, id, key = "plan", tk.PlanID, "waiting_runs"
				}
				row := capRedFindRow(t, rows, kind, id)
				if _, yes := row[key]; !yes {
					t.Errorf("S22d/e real queued in_progress run has no %s", key)
				}
				if cancel {
					c := NewSteerCanceller(al.GetSessionLifecycleStore(), al.SteerGenerationCancel)
					if _, err := c.StopTurns(context.Background(), stored.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "cap-test"}, true, al.SteerGenerationCancel); err != nil {
						t.Fatal(err)
					}
				} else {
					high = false
					al.drainSteerQueue(holders[0])
					select {
					case <-provider.entered:
					case <-time.After(3 * time.Second):
						t.Fatal("real promoted task body did not enter")
					}
				}
				rows = capRedRoster(t, al, testHarnessWorkspaceMembershipID)
				row = capRedFindRow(t, rows, kind, id)
				if _, yes := row[key]; yes {
					t.Errorf("S53 removed queue entry retained %s: %#v", key, row)
				}
				g := al.steerAdmission()
				g.mu.Lock()
				for _, e := range g.queue {
					if e.executionClaim() == claim {
						t.Error("execution still queued after grant/cancel")
					}
				}
				g.mu.Unlock()
			})
		}
	}
}

func TestCapRED_PlanWaitingProjection_BlockStopDelete(t *testing.T) {
	for _, state := range []plan.State{"blocked", plan.StateFailed, "delete"} {
		t.Run(string(state), func(t *testing.T) {
			high, ok := false, true
			capRedMemory(t, &high, &ok)
			al, _ := newSteerAL(t)
			ps := plan.New(t.TempDir())
			al.SetPlanStore(ps)
			p := mustCreatePlan(t, ps, &plan.Plan{ID: "plan-invalidated", Title: "plan", OwnerAgentID: testDefaultAgentID, WorkspaceID: testHarnessWorkspaceMembershipID, State: plan.StateRunning})
			te := GetTaskExecutor(al)
			te.SetPlanStore(ps)
			te.autoSyncDispatchCapacity = false
			te.dispatchSema = newDispatchSemaphore(1)
			capRedHold(t, te.dispatchSema, 1)
			tk := capRedTask(t, al, "member-invalidated", task.StatusNext, p.ID)
			if err := te.ExecuteTask(context.Background(), tk.ID, nil); err == nil {
				t.Fatal("member not refused")
			}
			row := capRedFindRow(t, capRedRoster(t, al, testHarnessWorkspaceMembershipID), "plan", p.ID)
			capRedAssert(t, "live plan has own refused member", row["waiting_member_count"], float64(1))
			switch state {
			case "delete":
				stopped := plan.StateFailed
				if _, err := ps.Update(p.ID, plan.Patch{State: &stopped}); err != nil {
					t.Fatal(err)
				}
				if err := ps.Delete(p.ID); err != nil {
					t.Fatal(err)
				}
			case "blocked":
				reason := plan.PausedReasonOwnerDisabled
				if _, err := ps.Update(p.ID, plan.Patch{PausedReason: &reason}); err != nil {
					t.Fatal(err)
				}
			default:
				if _, err := ps.Update(p.ID, plan.Patch{State: &state}); err != nil {
					t.Fatal(err)
				}
			}
			for _, r := range capRedRoster(t, al, testHarnessWorkspaceMembershipID) {
				if r["kind"] == "plan" && r["id"] == p.ID {
					for _, key := range []string{"waiting_members", "waiting_member_count", "waiting_runs", "waiting_run_count"} {
						if _, yes := r[key]; yes {
							t.Errorf("S53 %s plan retains %s", state, key)
						}
					}
				}
			}
		})
	}
}

// Locate actual in-memory storage by an entry's real task id, not by an
// invented field name. No map/publisher is supplied by this adapter. All
// mutations below go through ExecuteTask and the actual persistent store.
func capRedWaitingMap(t *testing.T, te *TaskExecutor, id string) reflect.Value {
	t.Helper()
	var found reflect.Value
	var walk func(reflect.Value, int)
	walk = func(v reflect.Value, depth int) {
		if depth > 3 || !v.IsValid() || found.IsValid() {
			return
		}
		if v.Kind() == reflect.Pointer {
			if !v.IsNil() {
				walk(v.Elem(), depth+1)
			}
			return
		}
		if v.Kind() == reflect.Map && v.Type().Key().Kind() == reflect.String {
			key := reflect.ValueOf(id).Convert(v.Type().Key())
			if v.MapIndex(key).IsValid() {
				found = v
			}
			return
		}
		if v.Kind() == reflect.Struct {
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i), depth+1)
			}
		}
	}
	walk(reflect.ValueOf(te), 0)
	if !found.IsValid() {
		t.Fatal("CAP-A8/S53 RED: actual refusal was not retained in bounded per-task storage; no fake map substituted")
	}
	return found
}

func TestCapRED_TaskWaitingProjection_512EntryBoundAndReadPruning(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	te := GetTaskExecutor(al)
	te.autoSyncDispatchCapacity = false
	te.dispatchSema = newDispatchSemaphore(1)
	capRedHold(t, te.dispatchSema, 1)
	ids := make([]string, 0, 530)
	first := capRedTask(t, al, "bounded-000", task.StatusNext, "")
	ids = append(ids, first.ID)
	if err := te.ExecuteTask(context.Background(), first.ID, nil); err == nil {
		t.Fatal("first bounded-storage refusal missing")
	}
	storage := capRedWaitingMap(t, te, first.ID)
	capRedAssert(t, "one entry for first live task", storage.Len(), 1)
	for i := 1; i < 530; i++ {
		tk := capRedTask(t, al, fmt.Sprintf("bounded-%03d", i), task.StatusNext, "")
		ids = append(ids, tk.ID)
		for repeat := 0; repeat < 2; repeat++ {
			if err := te.ExecuteTask(context.Background(), tk.ID, nil); err == nil {
				t.Fatalf("refusal %d/%d missing", i, repeat)
			}
		}
		if storage.Len() > 512 {
			t.Fatalf("S53 waiting evidence grew to %d, hard maximum 512", storage.Len())
		}
	}
	for _, id := range ids {
		if _, err := GetTaskStore(al).Delete(id); err != nil {
			t.Fatal(err)
		}
	}
	capRedAssert(t, "deleted tasks have no roster rows", len(capRedRoster(t, al, testHarnessWorkspaceMembershipID)), 0)
	capRedAssert(t, "read prunes all evidence for deleted tasks without a timer", storage.Len(), 0)
}
