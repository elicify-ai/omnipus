package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
)

// All literals below are independent oracles from cap-visibility-spec-r6 §5/§8.
const capRedM1 = "plan approved; at the last check (%s): %d of %d planning units in use (running plans, goals and loops). The engine checks again on its own schedule; list_jobs shows whether it has started"
const capRedM2 = "plan approved; its start depends on the engine's next capacity check, which this call could not read reliably. list_jobs shows whether it has started"
const capRedM16 = "Starting a task runs it in the background: the result says whether the run started or is waiting for admission, and when it is waiting, why and where it is in line. Starting the same task again while it is waiting, starting or running reports its current state and starts nothing new."
const capRedM17 = "If the work is a single lookup or one action you can do yourself, just do it — starting a child costs time."
const capRedM21 = "The capacity note in the result is an observation from the engine's last check, with its age — not a live diagnosis. list_jobs is the current-state check."

func TestCapRED_PlanExecute_ObservationDatasets(t *testing.T) {
	now := time.Date(2026, 10, 31, 20, 41, 0, 0, time.UTC)
	cases := []struct {
		id                      string
		active, max             int
		age                     time.Duration
		reliable, zero, unwired bool
		ageText                 string
	}{
		{"S1_PS1", 0, 16, 4 * time.Second, true, false, false, "4 seconds ago"},
		{"S2_PS2", 16, 16, 0, true, false, false, "just now"},
		{"PS3_over_capacity", 17, 16, 0, true, false, false, "just now"},
		{"S9_PS4", 3, 4, 0, true, false, false, "just now"},
		{"S3_historical", 0, 16, 70 * time.Second, true, false, false, "70 seconds ago"},
		{"S4_engine_stopped", 2, 16, 40 * time.Second, true, false, false, "40 seconds ago"},
		{"S7_PS5_unreliable", 3, 16, 0, false, false, false, ""},
		{"S6_PS6_stale", 0, 16, 91 * time.Second, true, false, false, ""},
		{"PS7_fresh", 0, 16, 89 * time.Second, true, false, false, "89 seconds ago"},
		{"freshness_exact_90_seconds", 0, 16, 90 * time.Second, true, false, false, "90 seconds ago"},
		{"S8_PS8_zero_time", 0, 16, 0, true, true, false, ""},
		{"S5_PS9_nil", 0, 16, 0, true, false, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			ps, ts := newPlanAndTaskStores(t)
			p := seedPlanWithMembers(t, ps, ts, "ws-1", 1, nil)
			tool := NewPlanExecuteTool(ps, ts)
			allowPlanExecution(tool)
			at := now.Add(-tc.age)
			if tc.zero {
				at = time.Time{}
			}
			var source JobCapSnapshotSource = capRedSnapshot{tc.active, tc.max, tc.reliable, at, now.Add(-100 * time.Second)}
			if tc.unwired {
				source = nil
			}
			capRedWireObservation(t, tool, source, now)
			got := capRedJSON(t, tool.Execute(context.Background(), map[string]any{"plan_id": p.ID}))
			want := map[string]any{"plan_id": p.ID, "state": "approved", "note": capRedM2}
			if tc.ageText != "" {
				want["note"] = fmt.Sprintf(capRedM1, tc.ageText, tc.active, tc.max)
				want["capacity"] = map[string]any{"units_active": float64(tc.active), "units_max": float64(tc.max), "observed_at": at.Format(time.RFC3339)}
			}
			capRedEqual(t, "complete approve result", got, want)
			stored, err := ps.Get(p.ID)
			if err != nil {
				t.Fatal(err)
			}
			capRedEqual(t, "durable state", stored.State, plan.StateApproved)
		})
	}
}

func TestCapRED_PlanExecute_AlreadyApproved_Observation(t *testing.T) {
	for _, wired := range []bool{true, false} {
		t.Run(fmt.Sprintf("S10_S11_wired_%t", wired), func(t *testing.T) {
			ps, ts := newPlanAndTaskStores(t)
			p := seedPlanWithMembers(t, ps, ts, "ws-1", 1, nil)
			state := plan.StateApproved
			before, err := ps.Update(p.ID, plan.Patch{State: &state})
			if err != nil {
				t.Fatal(err)
			}
			tool := NewPlanExecuteTool(ps, ts)
			now := time.Date(2026, 10, 31, 20, 41, 0, 0, time.UTC)
			var source JobCapSnapshotSource
			note := capRedM2
			if wired {
				source = capRedSnapshot{3, 16, true, now, now}
				note = fmt.Sprintf(capRedM1, "just now", 3, 16)
			}
			capRedWireObservation(t, tool, source, now)
			want := map[string]any{"plan_id": p.ID, "state": "approved", "note": note + " — no action taken"}
			if wired {
				want["capacity"] = map[string]any{"units_active": float64(3), "units_max": float64(16), "observed_at": now.Format(time.RFC3339)}
			}
			capRedEqual(t, "complete idempotent result", capRedJSON(t, tool.Execute(context.Background(), map[string]any{"plan_id": p.ID})), want)
			after, err := ps.Get(p.ID)
			if err != nil {
				t.Fatal(err)
			}
			capRedEqual(t, "no repeated approval write", after, before)
		})
	}
}

func TestCapRED_Descriptions_NoGenericCapWording(t *testing.T) {
	t.Run("S13_M16", func(t *testing.T) {
		got := NewTaskRunTool(nil).Description()
		if !strings.Contains(got, capRedM16) {
			t.Errorf("run_task description missing verbatim M16: %q", got)
		}
		for _, forbidden := range []string{"slot", "limit", "cap", "exhausted", "safe no-op returning the existing session"} {
			if strings.Contains(got, forbidden) {
				t.Errorf("run_task description contains retired generic claim %q: %q", forbidden, got)
			}
		}
	})
	t.Run("S44_M21", func(t *testing.T) {
		got := NewPlanExecuteTool(nil, nil).Description()
		capRedEqual(t, "unchanged preview", strings.SplitN(got, "\n", 2)[0], "Start autonomous execution of a draft plan once its member tasks are attached — no human approval required.")
		if !strings.Contains(got, capRedM21) {
			t.Errorf("execute_plan description missing verbatim M21: %q", got)
		}
		for _, forbidden := range []string{"concurrency cap", "cap is full", "promoting automatically when a slot frees", "waiting at 'approved'", "slot", "limit"} {
			if strings.Contains(got, forbidden) {
				t.Errorf("execute_plan contains generic cap claim %q", forbidden)
			}
		}
	})
	t.Run("S23_M17", func(t *testing.T) {
		// Byte-for-byte shared guidance, plus the independently specified final sentence.
		if !strings.HasSuffix(strings.TrimSpace(DelegateVsTaskVsPlanGuidance), capRedM17) {
			t.Errorf("guidance must end with verbatim M17; got %q", DelegateVsTaskVsPlanGuidance)
		}
		descriptions := map[string]string{"create_plan": NewPlanCreateTool(nil).Description(), "create_task": NewTaskCreateTool(nil).Description(), "delegate": NewDelegateTool("", 0, 0).Description()}
		for name, desc := range descriptions {
			capRedEqual(t, name+" shared guidance count", strings.Count(desc, DelegateVsTaskVsPlanGuidance), 1)
			if strings.Contains(desc, "limited number of concurrent slots") {
				t.Errorf("%s retained generic slot claim", name)
			}
		}
	})
}

func TestCapRED_ListJobs_CapPairDatasets(t *testing.T) {
	now := time.Date(2026, 10, 31, 20, 41, 0, 0, time.UTC)
	for _, tc := range []struct {
		id              string
		wired, reliable bool
		age             time.Duration
	}{
		{"S20_fresh", true, true, time.Second}, {"S21_PS14_stopped", true, true, 91 * time.Second},
		{"S22_unwired", false, true, 0}, {"S22a_unreliable", true, false, time.Second},
	} {
		t.Run(tc.id, func(t *testing.T) {
			tool := NewListJobsTool(nil, nil, nil)
			tool.SetNow(func() time.Time { return now })
			at := now.Add(-tc.age)
			if tc.wired {
				tool.SetCapSnapshotSource(func() JobCapSnapshotSource { return capRedSnapshot{2, 16, tc.reliable, at, at} })
			}
			got := capRedJSON(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))
			want := map[string]any{}
			if tc.wired {
				want["engine_running"] = tc.age <= 90*time.Second
				if tc.reliable {
					want["cap_active"] = float64(2)
					want["cap_max"] = float64(16)
					want["cap_observed_at"] = at.Format(time.RFC3339)
				}
			}
			actual := map[string]any{}
			for _, key := range []string{"cap_active", "cap_max", "cap_observed_at", "engine_running"} {
				if v, ok := got[key]; ok {
					actual[key] = v
				}
			}
			capRedEqual(t, "exact cap quartet including absence", actual, want)
		})
	}
}
