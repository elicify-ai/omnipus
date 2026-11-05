// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// plan_engine_expected_wait.go — expected plan waiting (tool-parity spec r17
// §7.2 TK-30, scenarios 18.19, 18.27–18.29, 18.32). A plan member whose only
// hold is its own live trigger occurrence is waiting as designed, not stalled:
// it contributes no stall reason, and a running plan every one of whose
// unfinished members is expected waiting is not idle-expired. The predicate is
// computed from persisted task state plus the scheduler's own facts; no status
// is added and no activity is invented.
//
// A member is expected waiting when:
//   - it is inbox, carries a once/recurring trigger, and its occurrence is
//     still ahead: strictly future (state 1) or armed/collected but not yet
//     delivered (state 2, OccurrencePending); or
//   - it is blocked, every unmet dependency in its transitive closure is
//     itself expected waiting, and its own trigger (if any) is not a missed
//     one.
//
// A once whose occurrence passed with no delivery and no live registration
// (state 4, missed) is not expected waiting: it stays a genuine stall until a
// person or the assignee re-schedules or moves it.

import (
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// expectedWaitProbe evaluates the predicate over one plan pass.
type expectedWaitProbe struct {
	pe      *PlanEngine
	now     time.Time
	byID    map[string]*task.Task
	memo    map[string]bool
	visit   map[string]bool
	pending func(taskID string) bool
}

func (pe *PlanEngine) newExpectedWaitProbe(tasks []task.Task, now time.Time) *expectedWaitProbe {
	p := &expectedWaitProbe{
		pe:    pe,
		now:   now,
		byID:  make(map[string]*task.Task, len(tasks)),
		memo:  map[string]bool{},
		visit: map[string]bool{},
	}
	for i := range tasks {
		p.byID[tasks[i].ID] = &tasks[i]
	}
	p.pending = func(string) bool { return false }
	if pe.agentLoop != nil {
		if s := pe.agentLoop.taskTriggerScheduler(); s != nil {
			p.pending = s.OccurrencePending
		}
	}
	return p
}

// lookup returns the task with id from this pass, loading a dependency that
// lies outside the plan from the store.
func (p *expectedWaitProbe) lookup(id string) *task.Task {
	if t, ok := p.byID[id]; ok {
		return t
	}
	if p.pe.taskStore == nil {
		return nil
	}
	t, err := p.pe.taskStore.Get(id)
	if err != nil {
		return nil
	}
	p.byID[id] = t
	return t
}

// ownOccurrenceAhead reports whether t's own trigger occurrence is still
// ahead: strictly future, or armed/collected and not yet delivered.
func (p *expectedWaitProbe) ownOccurrenceAhead(t *task.Task) bool {
	if !scheduledTaskTrigger(t) {
		return false
	}
	if task.TriggerAwaitingOccurrence(t, p.now) {
		return true
	}
	return !task.OnceDelivered(t) && p.pending(t.ID)
}

// expected reports whether t is expected waiting.
func (p *expectedWaitProbe) expected(t *task.Task) bool {
	if t == nil {
		return false
	}
	if v, ok := p.memo[t.ID]; ok {
		return v
	}
	if p.visit[t.ID] {
		return false // a dependency cycle is never a legitimate wait
	}
	p.visit[t.ID] = true
	v := p.evaluate(t)
	delete(p.visit, t.ID)
	p.memo[t.ID] = v
	return v
}

func (p *expectedWaitProbe) evaluate(t *task.Task) bool {
	switch t.Status {
	case task.StatusInbox:
		return p.ownOccurrenceAhead(t)
	case task.StatusBlocked:
	default:
		return false
	}
	if scheduledTaskTrigger(t) && t.Trigger.Type == task.TriggerOnce &&
		!p.ownOccurrenceAhead(t) && !task.OnceDelivered(t) {
		return false // its own once was missed: not a legitimate wait
	}
	unmet := 0
	for _, depID := range t.BlockedBy {
		dep := p.lookup(depID)
		if dep != nil && dep.Status == task.StatusDone {
			continue
		}
		unmet++
		if !p.expected(dep) {
			return false
		}
	}
	if unmet == 0 {
		return p.ownOccurrenceAhead(t)
	}
	return true
}

// scheduledTaskTrigger reports whether t carries a once or recurring trigger.
func scheduledTaskTrigger(t *task.Task) bool {
	return t != nil && t.Trigger != nil && t.Trigger.Type != task.TriggerManual
}

// allUnfinishedExpectedWaiting reports whether a plan with members tasks has
// at least one unfinished member and every unfinished member is expected
// waiting (the idle-expiry exemption, r17 TK-30 / 18.29).
func (pe *PlanEngine) allUnfinishedExpectedWaiting(tasks []task.Task, now time.Time) bool {
	probe := pe.newExpectedWaitProbe(tasks, now)
	unfinished := 0
	for i := range tasks {
		if task.IsTerminal(tasks[i].Status) {
			continue
		}
		unfinished++
		if !probe.expected(&tasks[i]) {
			return false
		}
	}
	return unfinished > 0
}

// planOnlyExpectedWaiting reports whether every unfinished member of planID
// is expected waiting. A member list that cannot be read is not evidence of
// waiting: the ordinary expiry rules apply.
func (pe *PlanEngine) planOnlyExpectedWaiting(planID string, now time.Time) bool {
	if pe.taskStore == nil {
		return false
	}
	members, err := pe.taskStore.List(task.Filter{PlanID: planID})
	if err != nil {
		logger.WarnCF("plan_engine", "idle sweep: list plan members failed",
			map[string]any{"plan_id": planID, "error": err.Error()})
		return false
	}
	return pe.allUnfinishedExpectedWaiting(members, now)
}
