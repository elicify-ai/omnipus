// goalSetupState.ts — the single "is a goal's record still being worked
// out" predicate, shared by the goal-aware thinking-indicator label and the
// goal-setup-failure quiet line (both in ChatScreen.tsx).
//
// Operator-reported UX fix, 2026-09-08: `/goal <text>` activates a goal
// INSTANTLY (ADR-088 D1, zero LLM calls) with an EMPTY compiled record —
// the working agent authors the record itself via its first `set_goal`
// call. In the reported repro that first `set_goal`/`ask_user_question`
// call failed, and the user watched a generic, content-free thinking
// indicator for 17 minutes with no sign anything had gone wrong. Both fixes
// need the SAME "are we still in that empty-record window" answer, so it
// lives here once rather than being reimplemented at each call site (and
// silently drifting).
//
// FR-039 (DEL-F39–40): the predicate is asked about a SPECIFIC goal — the
// producing run/turn/message's own `goal_id` — never the session's "latest
// goal" scalar. The old scalar reading (a handed GoalStatusFrame, i.e. the
// latest frame across ALL goals) was the deleted latest-goal-wins indicator
// branch: a live, record-empty goal G2 would wrongly relabel a message that
// belongs to G1 (or to no goal at all). Callers now pass the message's own
// `goalId`; an unknown association (no goalId) is NEUTRAL — false.
//
// WHY IT READS THE `goalPills` MAP RATHER THAN A FRAME IT IS HANDED (review
// finding 14): a routine `active` progress push carries NO `criteria` — the
// wire's own contract for "no record change on this push". Only `goalPills`
// goes through `mergeGoalPillFrame`, which carries a previously-authored
// record forward across criteria-less pushes. So reading `criteria` off any
// single frame answered "has this PUSH got a record", not "has this GOAL got
// a record": the very next progress push after the agent's `set_goal` flipped
// the predicate back to true and left it there permanently — the thinking
// indicator reverted to "Framing your goal" for the rest of the turn, and
// the goal-setup failure line re-armed and rewrote every historical failed
// tool call in the thread as "A step could not be completed during goal
// setup". `goalPills[goalId]` is the criteria-preserving copy, so that is
// what the record question has to be asked of — via the EXACT keyed join
// FR-039 requires.
//
// The map is passed in (not read via `getState()`) so every caller subscribes
// to it as a rendered value: a component that only read it via `getState()`
// would not re-render when a `goal_status` frame moved the record, leaving a
// stale indicator.

import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

/**
 * True while the goal identified by `goalId` is ACTIVE and its compiled
 * record has not been authored yet, judged from the per-goal-id `goalPills`
 * map. False for a null/undefined/empty `goalId` (an UNKNOWN association —
 * neutral, per FR-039), for a goal the map does not hold, for a terminal goal
 * (done/failed/cleared/...), or for a goal whose record has been authored.
 */
export function isGoalRecordEmpty(
  goalId: string | null | undefined,
  goalPills: Record<string, GoalStatusFrame> | undefined | null,
): boolean {
  if (!goalId) return false
  const merged = goalPills?.[goalId]
  if (!merged || merged.state !== 'active') return false
  const criteria = merged.criteria
  return !criteria || criteria.length === 0
}
