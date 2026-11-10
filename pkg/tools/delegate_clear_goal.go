// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// delegateGoalClearer is the goal-ending capability of the already-wired
// steering sink. It keeps the goal transition in pkg/agent without creating
// a tools/agent import cycle or widening steer-only test fakes.
type delegateGoalClearer interface {
	ClearDelegatedGoal(sessionID string) (bool, error)
}

func (t *DelegateTool) executeClearGoal(ctx context.Context, args map[string]any) *ToolResult {
	sessionID, err := requiredStringArg(args, "session_id")
	if err != nil {
		return ErrorResult(err.Error())
	}
	if t.lifecycle == nil {
		return ErrorResult("delegate: no lifecycle store configured")
	}
	rec, err := t.lifecycle.Load(sessionID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("delegate: clear_goal: %v", err)).WithError(err)
	}
	if err = t.verifyCallerOwnsSession(ctx, rec); err != nil {
		return ErrorResult(fmt.Sprintf("delegate: clear_goal: %v", err)).WithError(err)
	}
	if t.steering == nil {
		return ErrorResult("delegate: no steering sink configured")
	}
	clearer, ok := t.steering.(delegateGoalClearer)
	if !ok {
		return ErrorResult("delegate: no goal-clear capability configured")
	}
	// External CLI sessions have no native tool-boundary queue consumer.
	// Refuse before clearing rather than orphan their required decision prompt.
	if rec.Is3P {
		return ErrorResult(fmt.Sprintf(
			"delegate: clear_goal: not_steerable: external command-line session %s has no native steering-queue drain",
			sessionID,
		))
	}

	cleared, err := clearer.ClearDelegatedGoal(sessionID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("delegate: clear_goal: %v", err)).WithError(err)
	}
	if !cleared {
		return NewToolResult(fmt.Sprintf("Session %s has no active goal to clear; no decision prompt was queued.", sessionID))
	}

	// Clearing does not cancel the helper's live turn or touch its descendants.
	// The helper receives the decision at its own next tool boundary, including
	// when the clear lands while its current provider call is still running.
	correlationID, err := t.steering.EnqueueSteeringMessage(sessionID, rec.AgentID,
		providers.Message{Role: "user", Content: delegateClearGoalDecisionPrompt}, "")
	if err != nil {
		return ErrorResult(fmt.Sprintf(
			"delegate: clear_goal: goal for session %s was cleared, but the helper decision prompt could not be queued: %v",
			sessionID, err,
		)).WithError(err)
	}
	return NewToolResult(fmt.Sprintf(
		"Goal cleared for session %s; helper decision prompt queued (correlation_id=%s) for its next tool boundary. No descendant goals were cleared.",
		sessionID, correlationID,
	))
}
