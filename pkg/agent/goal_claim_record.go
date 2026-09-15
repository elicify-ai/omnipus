// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
)

// recordGoalClaim writes a worker's claim onto its goal record as
// Goal.LatestClaim: the claim's status, the worker's own one-line evidence, and
// when it was recorded.
//
// It is the ONE writer of that field for chat-owned and task-owned goals alike.
// GOAL-FR-013: "After activation there MUST be exactly one code path for the
// loop, the claim, the Judge, the budget accounting and the verdict." Both
// claim drivers call it for every claim kind — the chat after-turn hook
// (goal_loop.go::checkGoalLoopAfterTurn) and the task run loop
// (task_run_loop.go::recordRunClaim) — so a chat goal's record and a task
// goal's record carry the same claim for the same call (GOAL-FR-014, pinned by
// goal_parity_test.go::TestChatAndTaskGoalsBehaveIdentically).
//
// A claim is recorded when it is resolved, before any adjudication, so the
// record shows it even when the Judge then cannot run. A record that is no
// longer active is left untouched: a claim that lands after its goal ended is
// not that goal's claim.
func recordGoalClaim(goalID string, status generated.GoalLatestClaimStatus, evidence string) error {
	if strings.TrimSpace(goalID) == "" {
		return fmt.Errorf("goal: record %s claim: no goal id", status)
	}
	if _, err := resolveGoalRecordStore().Update(goalID, func(cur *goal.Goal) error {
		if !cur.IsActive() {
			return nil
		}
		return cur.RecordClaim(status, evidence, time.Now().UTC())
	}); err != nil {
		return fmt.Errorf("goal: record %s claim on %q: %w", status, goalID, err)
	}
	return nil
}
