// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import "fmt"

// ClearDelegatedGoal supplies the registered delegate tool's goal-clear
// capability through its existing AgentLoop steering sink. The tool verifies
// steering authority before calling it. The bool reports whether a goal
// actually ended, so a goal-less target does not receive a false clear notice.
func (al *AgentLoop) ClearDelegatedGoal(sessionID string) (bool, error) {
	// Reuse /goal clear's retained transition and session-scoped cleanup, but
	// not clearGoalByUser's deferred-session completion tail: the helper must
	// remain able to receive the decision prompt and choose what to do next.
	reply, ended, ok := al.endActiveGoal(sessionID, al.ResolveSessionStore(sessionID), goalClearNoteUser)
	if !ok {
		return false, fmt.Errorf("%s", reply)
	}
	return ended != nil, nil
}
