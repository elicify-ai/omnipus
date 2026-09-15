// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_owner_deleted.go ends the active chat goals an agent was working when
// that agent is deleted (UAT E-3).
//
// The defect. Deleting an agent removed its entity record and reloaded the
// registry, and nothing else: a goal the agent was working stayed
// `state: active` on its record forever. The goal keeper kept treating it as
// live and re-posted it through the async notifier addressed to the deleted
// agent; processSystemMessage could not resolve that agent and silently handed
// the push to the DEFAULT agent, which then worked — and parked — a goal that
// was never its own. A different agent inheriting another agent's goal is the
// same identity violation ADR-032's "no inheritance from the parent" rule
// forbids for delegation.
//
// The rule applied here. ADR-086 D9: ending a goal is a status transition on a
// retained record, never an erasure — so the record is TERMINATED, not deleted.
// The operator's deletion of the agent is an explicit operator action that
// ends the goal, which is what `cleared` means in the terminal vocabulary
// (FR-028: met / exhausted / expired / cleared); the terminal reason names the
// deleted agent so the record says honestly why it ended. The goal is not
// re-homed onto any other agent.
//
// Scope: SESSION-owned goals only. A task-owned goal is owned by its task, not
// by the agent assigned to it (GOAL-FR-002, EC-4/FR-044 bind it to the task's
// life), and its terminal transition has exactly one shared writer —
// tools.TerminateTaskGoalRecord, called when the task itself reaches a
// terminal status. Ending it here would race that writer and leave the task
// and its goal disagreeing.
package agent

import (
	"errors"
	"fmt"
	"strings"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// goalAgentDeletedNotePrefix is the clearGoalStatus note shape for a goal ended
// because the agent working it was deleted. Matched by prefix (the agent label
// varies) and mapped to the `cleared` terminal state and pill — see
// clearGoalStatus's switch.
const goalAgentDeletedNotePrefix = "owning agent deleted: "

// goalRouteAgentIDInMemory returns the agent id recorded for sessionID's goal
// route at activation (recordGoalRouting), or "" when none is held in memory.
// Deliberately NOT routeFor: routeFor WARNs and persists a routing-lost reason
// onto the goal record when no route exists, which is a keeper-dispatch
// concern, not a side effect an identity lookup may have.
func goalRouteAgentIDInMemory(sessionID string) string {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.routing[sessionID].agentID
}

// goalSessionStoreFor resolves the session store that owns sessionID, falling
// back to the shared store (the same fallback goalIdleExpirySweep uses).
func (al *AgentLoop) goalSessionStoreFor(sessionID string) *session.UnifiedStore {
	if store := al.ResolveSessionStore(sessionID); store != nil {
		return store
	}
	return al.GetSessionStore()
}

// goalWorkingAgentID resolves the agent that works the goal bound to
// sessionID: the agent recorded on the goal's route at activation when this
// process still holds it, else the session's own active agent, else the
// session's agent (ADR-086 D6 folds goal_route_agent_id into the owner — for a
// session-owned goal, the session's agent). Every keeper dispatch and every
// ownership check must use this ONE resolution; an empty result is never a
// licence to fall through to the default agent.
func goalWorkingAgentID(sessionID string, store *session.UnifiedStore) (string, error) {
	if id := goalRouteAgentIDInMemory(sessionID); id != "" {
		return id, nil
	}
	if store == nil {
		return "", fmt.Errorf("no session store resolves session %q", sessionID)
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil {
		return "", fmt.Errorf("read session %q meta: %w", sessionID, err)
	}
	if meta == nil {
		return "", fmt.Errorf("session %q has no meta", sessionID)
	}
	if meta.ActiveAgentID != "" {
		return meta.ActiveAgentID, nil
	}
	if meta.AgentID != "" {
		return meta.AgentID, nil
	}
	return "", fmt.Errorf("session %q names no agent", sessionID)
}

// EndGoalsOfDeletedAgent terminates every ACTIVE session-owned goal that
// agentID was working, as a `cleared` status transition whose terminal reason
// names the deleted agent (see this file's doc comment). For each goal it
// performs the full clearGoalStatus ending — terminal record transition, any
// in-flight verifier cancelled, any parked question card cancelled without a
// resume, the keeper's in-memory park/streak/route state dropped, exactly one
// terminal goal-status frame — and then, through clearGoalWithOutcome, records
// the goal's outcome line in its session, whose text tells the user reading
// the chat that the goal ended and why.
//
// Called by the gateway's DELETE /api/v1/agents/{id} handler once the agent's
// entity record is gone. Returns how many goals were ended. A goal whose
// working agent cannot be resolved, or whose terminal transition the store
// refuses, is reported in the joined error and left active; the rest are still
// ended — one bad record must not keep every other orphaned goal alive.
func (al *AgentLoop) EndGoalsOfDeletedAgent(agentID, agentName string) (int, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return 0, errors.New("goal: end goals of a deleted agent: agent id is required")
	}
	active, err := resolveGoalRecordStore().ListActive()
	if err != nil {
		return 0, fmt.Errorf("goal: end goals of deleted agent %q: list active goals: %w", agentID, err)
	}
	label := agentID
	if name := strings.TrimSpace(agentName); name != "" && name != agentID {
		label = fmt.Sprintf("%s (%s)", name, agentID)
	}

	ended := 0
	var errs []error
	for i := range active {
		g := &active[i]
		if g.OwnerKind != generated.GoalOwnerKindSession {
			continue
		}
		sessionID := g.ActiveSessionID
		if sessionID == "" {
			errs = append(errs, fmt.Errorf("goal %q is active but bound to no session", g.GoalID))
			continue
		}
		store := al.goalSessionStoreFor(sessionID)
		working, werr := goalWorkingAgentID(sessionID, store)
		if werr != nil {
			errs = append(errs, fmt.Errorf("goal %q: resolve the agent working it: %w", g.GoalID, werr))
			continue
		}
		if working != agentID {
			continue
		}
		// The system note the user reads is the outcome entry's own content —
		// written once, only after the terminal transition landed.
		if _, ok := al.clearGoalWithOutcome(sessionID, store, goalAgentDeletedNotePrefix+label, goalOutcomeInput{
			ending:      generated.GoalOutcomeEndingOther,
			roundsUsed:  g.Round,
			maxRounds:   g.MaxRounds,
			judgeReason: g.LatestReason,
			content: fmt.Sprintf(
				"Goal %q ended: the agent working on it, %s, was deleted. The goal was not handed to another agent — "+
					"start a new goal with an existing agent to continue this work.",
				g.Prompt, label),
		}); !ok {
			errs = append(errs, fmt.Errorf("goal %q: the goal store refused the terminal transition; the goal is still active", g.GoalID))
			continue
		}
		ended++
		logger.InfoCF("agent", "goal: ended because the agent working it was deleted",
			map[string]any{"component": "goal", "session_id": sessionID, "goal_id": g.GoalID, "agent_id": agentID})
	}
	return ended, errors.Join(errs...)
}
