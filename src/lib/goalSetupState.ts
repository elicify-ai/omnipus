// goalSetupState.ts — the single "is a goal's record still being worked
// out" predicate, shared by the goal-aware thinking-indicator label and the
// goal-setup-failure quiet line (both in ChatScreen.tsx).
//
// Operator-reported UX fix, 2026-09-08: `/goal <text>` activates a goal
// INSTANTLY (ADR-081 D1, zero LLM calls) with an EMPTY compiled record —
// the working agent authors the record itself via its first `set_goal`
// call. In the reported repro that first `set_goal`/`ask_user_question`
// call failed, and the user watched a generic, content-free thinking
// indicator for 17 minutes with no sign anything had gone wrong. Both fixes
// need the SAME "are we still in that empty-record window" answer, so it
// lives here once rather than being reimplemented at each call site (and
// silently drifting).

import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

/**
 * True while `goalStatus` describes an ACTIVE, non-terminal goal whose
 * compiled record has not been authored yet (`criteria` absent or empty —
 * the wire's own contract for "no record change on this push", per
 * GoalStatusFrame.yaml's `criteria` field doc). False for a null/undefined
 * frame, a terminal frame (done/failed/cleared/...), or a frame that
 * already carries a record.
 */
export function isGoalRecordEmpty(goalStatus: GoalStatusFrame | null | undefined): boolean {
  return goalStatus?.state === 'active' && (!goalStatus.criteria || goalStatus.criteria.length === 0)
}
