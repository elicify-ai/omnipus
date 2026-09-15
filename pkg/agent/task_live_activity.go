package agent

import (
	"time"
)

// TaskLiveActivitySource is the gateway's read seam for a running task's
// LAST-ACTIVITY time (founder decision 2026-09-14): the live progress stamp
// of the task's own turn — which moves on every streamed reasoning and
// tool-call-argument delta — or of any turn that turn delegated to. Defined
// here as an interface (mirroring every other gateway<->agent seam, e.g.
// tools.DelegateProgressReader) so pkg/gateway can consume it without
// importing a concrete type it does not already have; wired at boot by
// setupAndStartServices via SetLiveTaskActivitySource.
type TaskLiveActivitySource interface {
	// TaskLiveLastActivity returns the most recent live progress timestamp
	// observed for taskID's in-flight run, or false when this task has no
	// live turn that recorded progress (not running, or a provider path that
	// reports none). A pure read of state the running turn already
	// maintains; nothing is written to answer it.
	TaskLiveLastActivity(taskID string) (time.Time, bool)
}

// TaskLiveLastActivity implements TaskLiveActivitySource. It scans the
// existing activeTurnStates registry for a live turn whose processOptions
// name this task (opts.RunningTaskID — set by processTaskDirect from the
// tools.WithRunningTaskID context the task executor already stamps on the
// run) and returns the latest progress stamp across that turn and every live
// descendant it spawned: the delegated children are typically the ones
// streaming while the parent waits, so excluding them would report silence
// for a task that is visibly working.
//
// Reads the same atomic the delegate-status poll and the goal keeper's work
// fingerprint read (atomicToolCallProgress.lastActivityUnixNano), so the
// value advances on reasoning deltas exactly as it does on tool-call
// argument deltas (UAT E-15c). Returns false when nothing was found — honest
// absence, never a fabricated stamp.
func (al *AgentLoop) TaskLiveLastActivity(taskID string) (time.Time, bool) {
	if al == nil || taskID == "" {
		return time.Time{}, false
	}

	// Pass 1: find this task's live root turn(s). Normally exactly one; the
	// scan tolerates a re-dispatch overlap window the same way every other
	// activeTurnStates reader does (IsAlive filters finished registrations).
	var roots []*turnState
	al.activeTurnStates.Range(func(_ any, value any) bool {
		ts, ok := value.(*turnState)
		if !ok || ts == nil || !ts.IsAlive() {
			return true
		}
		ts.mu.RLock()
		opts := ts.opts
		ts.mu.RUnlock()
		if opts.RunningTaskID == taskID {
			roots = append(roots, ts)
		}
		return true
	})
	if len(roots) == 0 {
		return time.Time{}, false
	}

	// Pass 2: expand the roots' live descendants (delegates) breadth-first
	// over childTurnIDs, read under each turn's own mu like every other
	// cross-goroutine reader of that field.
	ids := make([]string, 0, 8)
	seen := make(map[string]bool, 8)
	queue := append([]*turnState(nil), roots...)
	for len(queue) > 0 {
		ts := queue[0]
		queue = queue[1:]
		if ts == nil || seen[ts.turnID] {
			continue
		}
		seen[ts.turnID] = true
		ids = append(ids, ts.turnID)
		ts.mu.RLock()
		children := append([]string(nil), ts.childTurnIDs...)
		ts.mu.RUnlock()
		if len(children) > 0 {
			for _, child := range al.turnStatesByTurnID(children) {
				if child != nil && child.IsAlive() {
					queue = append(queue, child)
				}
			}
		}
	}

	var latest int64
	for _, ts := range al.turnStatesByTurnID(ids) {
		if ts == nil || !ts.IsAlive() {
			continue
		}
		if nanos := ts.toolCallProgress.lastActivityUnixNano.Load(); nanos > latest {
			latest = nanos
		}
	}
	if latest == 0 {
		return time.Time{}, false
	}
	return time.Unix(0, latest), true
}
