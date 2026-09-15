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
import { useChatStore } from '@/store/chat'

/**
 * The `goalPills` map key for a frame — the same rule the store's
 * `goal_status` handler uses when it files the merged pill.
 */
function goalPillKey(frame: GoalStatusFrame): string {
  return frame.goal_id && frame.goal_id.length > 0 ? frame.goal_id : '_default'
}

/**
 * True while `goalStatus` describes an ACTIVE, non-terminal goal whose
 * compiled record has not been authored yet. False for a null/undefined
 * frame, a terminal frame (done/failed/cleared/...), or a goal whose record
 * has been authored.
 *
 * WHY THIS READS THE STORE RATHER THAN THE FRAME IT IS HANDED (review
 * finding 14): a routine `active` progress push carries NO `criteria` — the
 * wire's own contract for "no record change on this push" — and the store
 * files that frame into `goalStatus` VERBATIM. Only `goalPills` goes through
 * `mergeGoalPillFrame`, which carries a previously-authored record forward
 * across criteria-less pushes. So reading `goalStatus.criteria` answered
 * "has this PUSH got a record", not "has this GOAL got a record": the very
 * next progress push after the agent's `set_goal` flipped this predicate
 * back to true and left it there permanently — the thinking indicator
 * reverted to "Framing your goal" for the rest of the turn, and the
 * goal-setup failure line re-armed and rewrote every historical failed tool
 * call in the thread as "A step could not be completed during goal setup".
 *
 * `goalPills` is the criteria-preserving copy, so that is what the record
 * question has to be asked of. It is read via `getState()` rather than taken
 * as an argument because every call site is a render-body/conditional
 * position where a hook is illegal, and both fields are written by the SAME
 * `set()` in the store's `goal_status` handler — a caller subscribed to
 * `goalStatus` (all of them are) re-renders on exactly the frames that move
 * `goalPills`, so this read is never staler than the frame it is given.
 * This also restores the store's own stated invariant that `goalStatus`'
 * consumers never read `criteria` off it.
 */
export function isGoalRecordEmpty(goalStatus: GoalStatusFrame | null | undefined): boolean {
  if (!goalStatus || goalStatus.state !== 'active') return false
  const merged = useChatStore.getState().goalPills?.[goalPillKey(goalStatus)]
  // Fall back to the frame itself only when no pill has been filed yet (a
  // caller holding a frame the store never saw); the frame is then the only
  // record information in existence.
  const criteria = merged?.criteria ?? goalStatus.criteria
  return !criteria || criteria.length === 0
}
