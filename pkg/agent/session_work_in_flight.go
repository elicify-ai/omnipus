package agent

import (
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// sessionWorkInFlight reports whether sessionID still has agent work that is
// running or waiting to run, for the idle-expiry sweeps (goal and loop) that
// must never end work a clock merely outlasted (issue #1306, R2-1).
//
// Three independent signals, any one of which makes the session busy:
//
//  1. A live turn routed under sessionID — the session's own root turn and,
//     for a chat root, every delegated turn in its tree (they inherit the
//     root's routing ID).
//  2. The session's OWN live turn, whatever routing ID it carries. A helper's
//     turn carries its root's routing ID (steer_reconstruct.go::
//     reconstructSteeredTurn), so signal 1 cannot see it when the goal lives
//     on the helper's own session.
//  3. The durable lifecycle tree rooted at sessionID: the session's own
//     record and every descendant reached through its steering edges that is
//     queued (admitted or waiting for a slot — it WILL run, and has no turn
//     yet), or running with evidence of execution (a live turn under its own
//     ID, an in-progress completion write, or a task run the executor holds).
//
// Only sessionID and its own descendants are consulted, never the root's
// whole tree, so a sibling's work never keeps the wrong goal alive. A
// lifecycle store that cannot be read makes the session count as busy: the
// sweep must not end work it cannot see into. A stopped record cuts the
// walk (the D6 completion-frontier rule); a needs_input record is waiting on
// a person, not working, and is walked through without counting as busy.
func (al *AgentLoop) sessionWorkInFlight(sessionID string) (bool, string) {
	if sessionID == "" {
		return false, ""
	}
	if al.sessionHasLiveTurn(sessionID) {
		return true, "a turn is running"
	}
	if ts := al.ownLiveTurn(sessionID); ts != nil {
		return true, "its own turn is running"
	}
	busy, why, err := al.lifecycleTreeWorkInFlight(sessionID)
	if err != nil {
		return true, "its work could not be read: " + err.Error()
	}
	return busy, why
}

// ownLiveTurn returns the live turn executing as sessionID itself — the same
// exact-session match a turn-only Stop uses (cancel_stop.go::
// activeTurnForCancel): registered under the session's own key, or carrying
// it as its transcript session or execution claim.
func (al *AgentLoop) ownLiveTurn(sessionID string) *turnState {
	if ts := al.getActiveTurnState(sessionID); ts != nil && ts.IsAlive() {
		return ts
	}
	var found *turnState
	al.activeTurnStates.Range(func(_, value any) bool {
		ts, ok := value.(*turnState)
		if ok && ts.IsAlive() && (ts.transcriptSessionID == sessionID || al.tsExecutionClaim(ts, "").SessionID == sessionID) {
			found = ts
			return false
		}
		return true
	})
	return found
}

// lifecycleTreeWorkInFlight walks sessionID's own lifecycle record and its
// steering descendants (see sessionWorkInFlight, signal 3).
func (al *AgentLoop) lifecycleTreeWorkInFlight(sessionID string) (bool, string, error) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		// SteerLauncher.Launch refuses a child without a lifecycle store, so
		// there is no queued or delegated work to find.
		return false, "", nil
	}
	own, err := lifecycle.Load(sessionID)
	switch {
	case errors.Is(err, session.ErrLifecycleNotFound):
		// A plain chat has no record of its own; its children still do.
	case err != nil:
		return false, "", fmt.Errorf("read lifecycle record of %q: %w", sessionID, err)
	case own != nil:
		if own.State == session.LifecycleStopped {
			return false, "", nil
		}
		if busy, why := al.lifecycleRecordWorking(own); busy {
			return true, "this session " + why, nil
		}
	}
	seen := map[string]bool{sessionID: true}
	queue := []string{sessionID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		children, err := lifecycle.List(session.LifecycleFilter{SteeringSessionID: current})
		if err != nil {
			return false, "", fmt.Errorf("list delegated sessions of %q: %w", current, err)
		}
		for i := range children {
			child := &children[i]
			if seen[child.SessionID] {
				continue
			}
			seen[child.SessionID] = true
			if child.State == session.LifecycleStopped {
				continue
			}
			if busy, why := al.lifecycleRecordWorking(child); busy {
				return true, "a delegated session " + why, nil
			}
			queue = append(queue, child.SessionID)
		}
	}
	return false, "", nil
}

// lifecycleRecordWorking reports whether one lifecycle record shows work in
// flight: queued, or running with evidence of execution. A running record
// with none of that evidence is idle (e.g. left by a lifecycle notice).
func (al *AgentLoop) lifecycleRecordWorking(rec *session.LifecycleRecord) (bool, string) {
	switch rec.State {
	case session.LifecycleQueued:
		return true, "is queued to run"
	case session.LifecycleRunning:
		if al.steeredCompletionWriteActive(rec.SessionID) {
			return true, "is completing"
		}
		if ts := al.getActiveTurnState(rec.SessionID); ts != nil && ts.IsAlive() {
			return true, "is running"
		}
		if al.taskRunInFlight(rec) {
			return true, "is running a task"
		}
	}
	return false, ""
}
