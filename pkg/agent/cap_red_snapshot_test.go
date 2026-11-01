package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func capRedLogs(t *testing.T) func() []map[string]any {
	t.Helper()
	path := filepath.Join(t.TempDir(), "application.jsonl")
	previous := logger.GetLevel()
	logger.SetLevel(logger.DEBUG)
	if err := logger.EnableFileLogging(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.DisableFileLogging(); logger.SetLevel(previous) })
	return func() []map[string]any {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var lines []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if line == "" {
				continue
			}
			var item map[string]any
			if err := json.Unmarshal([]byte(line), &item); err != nil {
				t.Fatalf("real structured log is invalid JSON: %v", err)
			}
			lines = append(lines, item)
		}
		return lines
	}
}
func capRedMessageLines(lines []map[string]any, fragment string) []map[string]any {
	out := make([]map[string]any, 0)
	for _, line := range lines {
		if text, _ := line["message"].(string); strings.Contains(text, fragment) {
			out = append(out, line)
		}
	}
	return out
}
func capRedSnapshotSource(t *testing.T, pe *PlanEngine) tools.JobCapSnapshotSource {
	t.Helper()
	source, ok := any(pe).(tools.JobCapSnapshotSource)
	if !ok {
		t.Fatal("CAP-B8 RED: real PlanEngine does not implement CapSnapshot; no observation is published")
	}
	return source
}

func TestCapRED_PlanEngine_PublishesOwnSummedObservation(t *testing.T) {
	h := newTestPlanEngine(t)
	h.pe.RegisterActiveCounter("goal", func() (int, error) { return 4, nil })
	h.pe.RegisterActiveCounter("loop", func() (int, error) { return 3, nil })
	yes, active, limit := h.pe.Admit("plan")
	capRedAssert(t, "PS10 summed real count", []any{yes, active, limit}, []any{true, 7, 16})
	source := capRedSnapshotSource(t, h.pe)
	a, m, reliable, at, tick := source.CapSnapshot()
	capRedAssert(t, "PS10 published decision", []any{a, m, reliable, at, tick}, []any{7, 16, true, h.clock.Now(), time.Time{}})
}

func TestCapRED_PlanEngine_FailedReadPublishesNothing(t *testing.T) {
	h := newTestPlanEngine(t)
	h.pe.RegisterActiveCounter("goal", func() (int, error) { return 3, nil })
	h.pe.Admit("plan")
	source := capRedSnapshotSource(t, h.pe)
	a, m, r, at, tick := source.CapSnapshot()
	h.clock.Set(h.clock.Now().Add(time.Second))
	h.pe.RegisterActiveCounter("goal", func() (int, error) { return 0, errors.New("injected count read failure") })
	yes, _, _ := h.pe.Admit("plan")
	capRedAssert(t, "PS11 failed count refuses", yes, false)
	aa, mm, rr, aat, tt := source.CapSnapshot()
	capRedAssert(t, "PS11 previous pair preserved", []any{aa, mm, rr, aat, tt}, []any{a, m, r, at, tick})
}

func TestCapRED_PlanEngine_OlderObservationCannotReplaceNewer(t *testing.T) {
	h := newTestPlanEngine(t)
	t0 := h.clock.Now()
	h.pe.RegisterActiveCounter("goal", func() (int, error) { return 7, nil })
	h.clock.Set(t0.Add(2 * time.Second))
	h.pe.Admit("plan")
	source := capRedSnapshotSource(t, h.pe)
	h.pe.RegisterActiveCounter("goal", func() (int, error) { return 1, nil })
	h.clock.Set(t0)
	h.pe.Admit("plan")
	a, m, r, at, _ := source.CapSnapshot()
	capRedAssert(t, "PS12 monotonic publication", []any{a, m, r, at}, []any{7, 16, true, t0.Add(2 * time.Second)})
}

func TestCapRED_PlanEngine_TickFreshCountPerDecision(t *testing.T) {
	h := newTestPlanEngine(t)
	t0 := h.clock.Now()
	observations := 0
	h.pe.RegisterActiveCounter("goal", func() (int, error) {
		observations++
		h.clock.Set(t0.Add(time.Duration(observations) * time.Second))
		return 14, nil
	})
	for i := 0; i < 3; i++ {
		p := mustCreatePlan(t, h.plans, &plan.Plan{ID: fmt.Sprintf("approved-%d", i), Title: "CAP approved", OwnerAgentID: "owner", WorkspaceID: "ws", State: plan.StateApproved})
		mustCreateTask(t, h.tasks, &task.Task{ID: fmt.Sprintf("member-%d", i), Title: "member", AgentID: "owner", WorkspaceID: "ws", PlanID: p.ID, Status: task.StatusNext, Criteria: []task.AcceptanceCriterion{planProseCriterion("done")}})
	}
	logs := capRedLogs(t)
	h.pe.Tick(context.Background())
	plans, err := h.plans.List(plan.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	states := map[plan.State]int{}
	for _, p := range plans {
		states[p.State]++
	}
	capRedAssert(t, "S48 only two plans admitted", states, map[plan.State]int{plan.StateRunning: 2, plan.StateApproved: 1})
	capRedAssert(t, "S48 own count per decision plus fresh pass end", observations, 4)
	source := capRedSnapshotSource(t, h.pe)
	a, m, r, at, tick := source.CapSnapshot()
	capRedAssert(t, "S48 final snapshot never 17", []any{a, m, r, at, tick}, []any{16, 16, true, t0.Add(4 * time.Second), t0.Add(4 * time.Second)})
	lines := capRedMessageLines(logs(), "approved plan is waiting for a planning-unit slot")
	capRedAssert(t, "M19 exactly once", len(lines), 1)
	if len(lines) == 1 {
		capRedAssert(t, "M19 text/level/count", []any{lines[0]["message"], lines[0]["level"], lines[0]["active"], lines[0]["cap"]}, []any{"approved plan is waiting for a planning-unit slot (16 of 16 in use)", "info", float64(16), float64(16)})
	}
}

func TestCapRED_PlanEngine_UnreadableWaitIsNotCapacity(t *testing.T) {
	h := newTestPlanEngine(t)
	p := mustCreatePlan(t, h.plans, &plan.Plan{ID: "unreadable", Title: "unreadable", OwnerAgentID: "owner", WorkspaceID: "ws", State: plan.StateApproved})
	h.pe.RegisterActiveCounter("goal", func() (int, error) { return 0, errors.New("injected counter failure") })
	logs := capRedLogs(t)
	h.pe.tryStartApprovedPlan(context.Background(), p.ID)
	lines := capRedMessageLines(logs(), "approved plan is waiting")
	capRedAssert(t, "M20 exactly once", len(lines), 1)
	if len(lines) == 1 {
		capRedAssert(t, "M20 exact text/level", []any{lines[0]["message"], lines[0]["level"]}, []any{"approved plan is waiting: the active count could not be read, so admission is refused (fail-closed)", "warn"})
	}
	if len(capRedMessageLines(logs(), "cap-waiting")) != 0 {
		t.Error("unreadable count collapsed into capacity-full diagnosis")
	}
}

func TestCapRED_PlanJudge_LaneMemoryGated(t *testing.T) {
	for _, tc := range []struct {
		name     string
		high, ok bool
		want     string
	}{
		{"pressure", true, true, "plan_engine: plan judge round is waiting — this machine is low on memory, so new judge rounds are held at 2 at a time (2 in flight)"},
		{"unverifiable", false, false, "plan_engine: plan judge round is waiting — this machine cannot report its available memory, so new judge rounds are held at 2 at a time (2 in flight)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			high, ok := false, true
			capRedMemory(t, &high, &ok)
			h := newTestPlanEngine(t)
			capRedHold(t, h.pe.judgeSema, 2)
			p := mustCreatePlan(t, h.plans, &plan.Plan{ID: "judge-cap", Title: "judge cap", OwnerAgentID: "owner", WorkspaceID: "ws", State: plan.StateRunning, DoD: []task.AcceptanceCriterion{planProseCriterion("done")}})
			logs := capRedLogs(t)
			high, ok = tc.high, tc.ok
			h.pe.beginPlanJudgeRound(p, nil)
			h.pe.judgeWG.Wait()
			capRedAssert(t, "S60 refused judge never called", h.judge.callCount(), 0)
			capRedAssert(t, "judge stored capacity remains four", h.pe.judgeSema.Cap(), 4)
			lines := capRedMessageLines(logs(), "plan judge round is waiting")
			capRedAssert(t, "one memory wait line", len(lines), 1)
			if len(lines) == 1 {
				capRedAssert(t, "exact M25 text/level/plan", []any{lines[0]["message"], lines[0]["level"], lines[0]["plan_id"]}, []any{tc.want, "info", p.ID})
			}
		})
	}
}

func TestCapRED_PlanJudge_ReleaseOnPhaseUpdateFailure(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	h := newTestPlanEngine(t)
	// Missing durable plan makes the real phase update fail after acquiring.
	h.pe.beginPlanJudgeRound(&plan.Plan{ID: "not-persisted", State: plan.StateRunning}, nil)
	h.pe.judgeWG.Wait()
	capRedAssert(t, "S60 failed phase update returns owned release", h.pe.judgeSema.InFlight(), 0)
	capRedAssert(t, "no judge starts after failed phase update", h.judge.callCount(), 0)
}

func TestCapRED_PlanJudge_ReleaseOnRegistryFailure(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	h := newTestPlanEngine(t)
	p := mustCreatePlan(t, h.plans, &plan.Plan{ID: "registry-held", Title: "judge", State: plan.StateRunning, OwnerAgentID: "owner", WorkspaceID: "ws", DoD: []task.AcceptanceCriterion{planProseCriterion("done")}})
	if err := h.pe.registry().Register(verifierUnitForPlan(p.ID), "actual-existing-verifier"); err != nil {
		t.Fatal(err)
	}
	h.pe.beginPlanJudgeRound(p, nil)
	h.pe.judgeWG.Wait()
	capRedAssert(t, "S60 real CAS failure returns acquired release", h.pe.judgeSema.InFlight(), 0)
	capRedAssert(t, "CAS failure starts no judge", h.judge.callCount(), 0)
	id, exists := h.pe.registry().Lookup(verifierUnitForPlan(p.ID))
	capRedAssert(t, "existing verifier remains registered", []any{id, exists}, []any{"actual-existing-verifier", true})
}

func TestCapRED_PlanJudge_HealthyConfiguredFullKeepsDebug(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	h := newTestPlanEngine(t)
	capRedHold(t, h.pe.judgeSema, 4)
	p := mustCreatePlan(t, h.plans, &plan.Plan{ID: "configured-judge", Title: "judge", State: plan.StateRunning, OwnerAgentID: "owner", WorkspaceID: "ws", DoD: []task.AcceptanceCriterion{planProseCriterion("done")}})
	logs := capRedLogs(t)
	h.pe.beginPlanJudgeRound(p, nil)
	h.pe.judgeWG.Wait()
	lines := capRedMessageLines(logs(), "plan judge lane full; retrying next tick")
	capRedAssert(t, "S60 exactly one configured-full debug", len(lines), 1)
	if len(lines) == 1 {
		capRedAssert(t, "unchanged debug level and plan", []any{lines[0]["level"], lines[0]["plan_id"]}, []any{"debug", p.ID})
	}
	capRedAssert(t, "configured-full is not a memory notice", len(capRedMessageLines(logs(), "plan judge round is waiting")), 0)
	capRedAssert(t, "held rounds were not preempted", h.pe.judgeSema.InFlight(), 4)
	capRedAssert(t, "fifth round did not reach model", h.judge.callCount(), 0)
}
