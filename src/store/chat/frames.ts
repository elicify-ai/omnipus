// frames.ts: Frame-routing cache invalidation, finished-turn test reset, and goal-state classification

import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'
import { finishedTurnIdsBySession } from './types'

/**
 * True when `planId` appears anywhere in a cached query's key — either as a
 * top-level element (matches today's `plansQueryKeys.detail(workspaceId,
 * planId)` shape, `['plans', workspaceId, planId]`) or as a `plan_id`/
 * `planId` property on a top-level params-object element (matches
 * `tasksQueryKeys.list({ ...params, plan_id })`'s `['tasks', cleanedParams]`
 * shape — no live call site passes `plan_id` into that factory today, but
 * this keeps the scoped invalidation correct if/when one does). Pure/exported
 * so the scoping rule is unit-testable without populating the real
 * queryClient cache.
 */
export function queryKeyMentionsPlanId(queryKey: readonly unknown[], planId: string): boolean {
  return queryKey.some((segment) => {
    if (segment === planId) return true
    if (segment && typeof segment === 'object' && !Array.isArray(segment)) {
      const params = segment as Record<string, unknown>
      return params.plan_id === planId || params.planId === planId
    }
    return false
  })
}

/**
 * Test-only escape hatch: clears `finishedTurnIdsBySession`. This tracker is
 * deliberately module-scoped (it must survive a `resetStores()`-style
 * `sessionsById` wipe in production — that's the whole point of S2, so a
 * reconnect that rebuilds the bucket from scratch still remembers a turn id
 * it already finalized), which means it also survives across `it()` blocks
 * within one test FILE. Test suites that reuse the same session id + turn id
 * constant across multiple independent scenarios (as chat.reconnect.test.ts
 * and chat.session-state-routing.test.ts both do, deliberately, for
 * readability) must call this from their `resetStores()`/`beforeEach` or a
 * turn finalized in one `it()` block will be wrongly treated as
 * already-finished in a later, unrelated one.
 */
export function __resetFinishedTurnIdsForTests(): void {
  for (const sid of Object.keys(finishedTurnIdsBySession)) {
    delete finishedTurnIdsBySession[sid]
  }
}

// ── goalPills bound (regression fix, bc66345f follow-up) ──────────────────
//
// Authoritative terminal-state set per the wire contract
// (contracts/components/schemas/GoalStatusFrame.yaml `state` enum, now 14
// values after the joint ADR-084/ADR-085/ADR-086 delivery, C-39): `done`
// (success), `failed` (a genuine rounds-exhausted/idle-expired
// brake — pre-existing, now narrower now that `expired` has its own
// value, see below), `cleared` (a deliberate user-initiated stop — added
// post-ADR-053 so it does NOT collapse into `failed`), and `expired`
// (ADR-086 GOAL-FR-028: the 7-day idle-expiry sweep, D-A — the fourth
// distinguishable terminal ending, ADDED by this delivery). All other
// states (queued/active/waiting_on_user/judge_unavailable/re-planning/
// judging/judge_cas_loss/blocked/claim_overturned)
// are non-terminal: the goal can still receive another frame. `blocked`
// and `claim_overturned` deliberately do NOT join this set (plan OQ-17,
// C-17) even though both "park" the goal — the goal is still live and a
// returning operator seeing `claim_overturned` hidden by the terminal
// display timer would defeat JUDGE-FR-102's whole point.
//
// Exported (not just module-private) so `GoalPillTray.tsx` can key its own
// short-lived "keep a terminal pill visible briefly, then stop rendering
// it" display timer off the SAME authoritative set, rather than each site
// maintaining its own copy of the enum that could drift.
export const GOAL_TERMINAL_STATES: ReadonlySet<GoalStatusFrame['state']> = new Set([
  'done',
  'failed',
  'cleared',
  'expired',
])
