package agent

import (
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/plan"
)

// concludeUnreadableJudgeRoundLocked ends a plan Judge round whose outcome
// cannot be applied because the plan record cannot be read (issue #1306 T3).
//
// Before this, such a round was logged as "Stop landed concurrently" and
// dropped: no handover, no wake, nothing counted. Now it concludes the same
// way an unavailable round does, but through routes that do not depend on
// reading the very record that failed:
//
//   - The round counts against the consecutive-unavailability bound (kept in
//     memory), so an unreadable record cannot buy unlimited silent rounds. At
//     the bound the park is DECIDED in memory; processPlan's existing H1
//     ladder re-attempts it, or fails the plan closed, once the record can be
//     read again.
//   - The revert/park writes are still attempted (a transient read fault may
//     not affect the write); their failures are logged by those helpers.
//   - The plan owner is woken with a plain explanation, using the round's
//     last good view of the plan (snapshot). The wake lands in the owner's
//     chat/session, which is stored outside the plan record.
//
// The round's verifier claim and Judge slot are released by
// runPlanJudgeRound's own defers, as on every other exit. Once the record is
// readable again, the plan is still at plan_phase=judging with no round in
// flight, so processPlan resumes the round (or the decided park).
//
// A real verdict that cannot be applied is discarded, not guessed onto the
// plan: the round is re-run after recovery. Caller holds planDecisionMu.
func (pe *PlanEngine) concludeUnreadableJudgeRoundLocked(
	planID string, snapshot *plan.Plan, result JudgeCriteriaResult, nothingToJudge bool, readErr error,
) {
	reason := "the plan record could not be read to apply the judge round's outcome: " + readErr.Error()
	if result.Unavailable && strings.TrimSpace(result.Reason) != "" {
		reason = result.Reason + "; then " + reason
	}
	streak := pe.bumpJudgeUnavailableStreak(planID)
	logger.ErrorCF("plan_engine",
		"plan judge round could not be concluded on the plan record (unreadable); counted as an unavailable round and reported to the plan owner",
		map[string]any{
			"plan_id": planID, "streak": streak, "bound": plan.MaxConsecutiveJudgeUnavailable,
			"verdict_discarded": !result.Unavailable && !nothingToJudge, "error": readErr.Error(),
		})
	if snapshot == nil {
		// Only reachable from a direct applyJudgeRoundOutcome call that has
		// no view of the plan at all; the ERROR above is its record.
		return
	}
	if streak >= plan.MaxConsecutiveJudgeUnavailable {
		pe.surfaceJudgeUnavailableStall(snapshot, streak, reason)
	} else {
		pe.revertAbandonedJudgeRoundLocked(snapshot, reason)
	}
	pe.wakeOwner(snapshot, buildUnreadableJudgeRoundWakeText(snapshot, streak, reason), "plan_judge_unavailable")
}

// buildUnreadableJudgeRoundWakeText is the owner-facing conclusion of a plan
// Judge round that could not be applied to an unreadable plan record.
func buildUnreadableJudgeRoundWakeText(p *plan.Plan, streak int, reason string) string {
	return fmt.Sprintf(
		"Plan %q (%s): the plan judge's round could not be concluded, so the Definition of Done has not "+
			"been scored. Reason: %s. No judge round was consumed (unavailable rounds in a row: %d of %d). "+
			"The members' work is unchanged. The engine retries the round once the plan record can be read "+
			"again; if it stays unreadable, check the plans directory for disk or permission errors.",
		p.Title, p.ID, judgeUnavailableReasonText(reason), streak, plan.MaxConsecutiveJudgeUnavailable)
}
