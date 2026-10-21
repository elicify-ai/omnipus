package agent

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// goalReminderScope is an observation of existing ownership records, not a new
// jobs registry. Independent task runs may live under another assignee's main.
type goalReminderScope struct {
	sessions    map[string]bool
	records     []session.LifecycleRecord
	tasks       []task.Task
	plans       []*plan.Plan
	scratchpads map[string]bool
}

func (al *AgentLoop) reminderScope(root string) (*goalReminderScope, error) {
	scope := &goalReminderScope{sessions: map[string]bool{root: true}, scratchpads: make(map[string]bool)}
	if lifecycle := al.GetSessionLifecycleStore(); lifecycle != nil {
		records, err := lifecycle.List(session.LifecycleFilter{})
		if err != nil {
			return nil, fmt.Errorf("goal reminder: read execution ownership: %w", err)
		}
		scope.records = records
	}
	if store := GetTaskStore(al); store != nil {
		tasks, unreadable, err := store.ListWithUnreadable(task.Filter{})
		if err != nil {
			return nil, fmt.Errorf("goal reminder: read tasks: %w", err)
		}
		if len(unreadable) != 0 {
			return nil, fmt.Errorf("goal reminder: %d task ownership records could not be read", len(unreadable))
		}
		for _, item := range tasks {
			if item.Scratchpad {
				scope.scratchpads[item.ID] = true
				continue
			}
			scope.tasks = append(scope.tasks, item)
		}
	}
	plans, err := al.reminderPlans()
	if err != nil {
		return nil, err
	}
	scope.plans = plans
	for changed := true; changed; {
		changed = false
		add := func(id string) {
			if id != "" && !scope.sessions[id] {
				scope.sessions[id], changed = true, true
			}
		}
		for _, record := range scope.records {
			if record.Origin != nil && record.Origin.Kind == session.OriginKindVerifier {
				continue // Judge activity is not the working agent making progress.
			}
			isTaskRun := record.Origin != nil && record.Origin.Kind == session.OriginKindTask
			// A task's steering parent is its assignee main, not necessarily
			// the chat that started the run. Never borrow that main's siblings.
			if !isTaskRun && record.SteeredBy != nil && scope.sessions[record.SteeredBy.SteeringSessionID] {
				add(record.SessionID)
			}
			if isTaskRun && record.Origin.TaskID != "" && !scope.scratchpads[record.Origin.TaskID] && record.InitiatedBy != nil && scope.sessions[record.InitiatedBy.SessionID] {
				// Execution provenance and retained run history remain valid
				// after deletion of the current task row.
				add(record.SessionID)
			}
		}
		for _, item := range scope.plans {
			if item.InitiatedBy != nil && scope.sessions[item.InitiatedBy.SessionID] {
				add(item.OwnerSessionID)
				for _, member := range scope.tasks {
					if member.PlanID == item.ID {
						add(member.SessionID)
					}
				}
			}
		}
	}
	return scope, nil
}

func (al *AgentLoop) reminderPlans() ([]*plan.Plan, error) {
	store := al.GetPlanStore()
	if store == nil {
		return nil, nil
	}
	entries, err := os.ReadDir(store.Dir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("goal reminder: list plans: %w", err)
	}
	var result []*plan.Plan
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		item, err := store.Get(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, fmt.Errorf("goal reminder: read plan ownership: %w", err)
		}
		if item == nil {
			return nil, errors.New("goal reminder: plan ownership is unavailable")
		}
		result = append(result, item)
	}
	return result, nil
}

func reminderEvent(work *goalReminderWork, key string, at, since time.Time) bool {
	if !since.IsZero() && at.Before(since.Truncate(time.Second)) {
		return false
	}
	if since.IsZero() && at.Before(time.Now().Truncate(time.Second)) {
		return false // old baseline IDs cannot be new in a future window
	}
	if _, seen := work.events[key]; seen {
		return false
	}
	work.events[key] = at
	if since.IsZero() || at.IsZero() || at.Before(since.Truncate(time.Second)) {
		return false
	}
	work.observedProgress = true
	return true
}

func parseReminderTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("goal reminder: invalid activity time: %w", err)
	}
	return at, nil
}

func (al *AgentLoop) reminderBackgroundProcesses(scope *goalReminderScope, work *goalReminderWork, since time.Time) (bool, bool) {
	manager := tools.GetSharedSessionManager()
	for _, info := range manager.List() {
		process, err := manager.Get(info.ID)
		if err == nil && process.Background && scope.sessions[process.OwnerSessionID] {
			work.processes[process.ID] = process
		}
	}
	busy, progress := false, false
	for id, process := range work.processes {
		if !scope.sessions[process.OwnerSessionID] {
			delete(work.processes, id)
			continue
		}
		running := !process.IsDone()
		busy = busy || running
		at := process.LastActivityAt()
		if !since.IsZero() && at.After(since) {
			progress, work.observedProgress = true, true
		}
		if !running {
			// The progress latch retains the observation, not another 1MB buffer.
			delete(work.processes, id)
		}
	}
	return busy, progress
}

func (al *AgentLoop) reminderOwnedJobs(root string, scope *goalReminderScope, work *goalReminderWork, since time.Time) (bool, bool, error) {
	busy, progress := false, false
	for id := range scope.sessions {
		if id != root {
			if active, _ := al.sessionWorkInFlight(id); active {
				busy = true
			}
		}
	}
	taskProgress, err := al.reminderTaskHistory(scope, work, since)
	if err != nil {
		return false, false, err
	}
	progress = taskProgress
	for _, item := range scope.tasks {
		owned := item.SessionID != "" && scope.sessions[item.SessionID]
		queued := item.SessionID == "" && item.Initiator != nil && scope.sessions[item.Initiator.SessionID]
		if !owned && !queued {
			continue
		}
		if al.taskExecutor != nil && al.taskExecutor.holdsRun(item.ID) {
			busy = true
		}
		if queued && item.Status == task.StatusInProgress {
			busy = true
		}
		if owned && item.CompletedAt != "" {
			at, err := parseReminderTime(item.CompletedAt)
			if err != nil {
				return false, false, err
			}
			progress = reminderEvent(work, "task-final:"+item.ID+":"+item.SessionID+":"+item.CompletedAt, at, since) || progress
		}
	}
	for _, item := range scope.plans {
		if item.InitiatedBy == nil || !scope.sessions[item.InitiatedBy.SessionID] {
			continue
		}
		if engine := GetPlanEngine(al); engine != nil {
			if active, _ := engine.planWorkInFlight(item.ID); active {
				busy = true
			}
		} else if item.ActiveLoop {
			return false, false, errors.New("goal reminder: owned plan has no work registry")
		}
		if item.CompletedAt != "" {
			at, err := parseReminderTime(item.CompletedAt)
			if err != nil {
				return false, false, err
			}
			progress = reminderEvent(work, "plan:"+item.ID+":"+item.CompletedAt, at, since) || progress
		}
	}
	return busy, progress, nil
}
