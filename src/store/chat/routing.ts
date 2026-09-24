// routing.ts: Plan/library invalidation, finished-turn tracking, and frame-routing helpers

import { queryClient } from '@/lib/queryClient'
import { libraryQueryKeys } from '@/lib/api'
import { queryKeyMentionsPlanId } from './frames'
import { finishedTurnIdsBySession } from './types'

// ── plan_status invalidation: scoped + coalesced (refetch-storm fix) ────────
//
// `plan_status` is a GLOBAL frame (no session_id — see SESSION_SCOPED_FRAME_TYPES
// below) broadcast to EVERY client on every plan MUTATION; a busy plan run
// (dispatch -> judge -> synthesize, repeated per task) can emit dozens of
// these in a few seconds. The naive handler used to fire two prefix-wide
// `invalidateQueries` calls PER FRAME (`['plans']`, `['tasks']`), which (a)
// forces every currently-mounted query under those roots to refetch
// IMMEDIATELY — react-query's default `refetchType: 'active'` ignores each
// query's own `staleTime` — hammering scan-heavy endpoints at 15-40 req/sec
// during a busy run, and (b) did this with zero coalescing, so a burst of N
// frames meant N full refetch waves back-to-back.
//
// Fix, two parts:
//   1. Coalesce: buffer plan_ids seen during a trailing-edge debounce window
//      and run exactly ONE invalidation pass per window, however many
//      plan_status frames (for the same or different plans) arrived inside
//      it. Module-level (not per-session — plan_status is GLOBAL), mirroring
//      the orphanTimers pattern just above.
//   2. Scope: the frame carries `plan_id`, so a precise, immediate-refetch
//      invalidation targets exactly the queries whose cache key mentions
//      that specific plan (`queryKeyMentionsPlanId`) — forward-compatible
//      with any plan-id-keyed cache added later, even though none of
//      today's live `plansQueryKeys`/`tasksQueryKeys` call sites key by
//      plan_id alone (they key by workspace_id, which this frame doesn't
//      carry). The broad `['plans']`/`['tasks']` prefixes are still
//      invalidated too (existing views that don't key by plan_id at all,
//      e.g. the workspace-wide task list, still need to know SOMETHING
//      changed) but with `refetchType: 'none'` — marks them stale WITHOUT
//      forcing a network round trip, so the existing 15s `refetchInterval`
//      polls (PlansFilterBand/WorkspaceTasksTab) pick up the fresh state on
//      their own cadence instead of every open tab hammering the backend in
//      lockstep on every broadcast.
const PLAN_STATUS_INVALIDATE_DEBOUNCE_MS = 1000

let planStatusInvalidateTimer: ReturnType<typeof setTimeout> | undefined

const pendingPlanStatusIds = new Set<string>()

function flushPlanStatusInvalidation(): void {
  const planIds = Array.from(pendingPlanStatusIds)
  pendingPlanStatusIds.clear()
  planStatusInvalidateTimer = undefined
  for (const planId of planIds) {
    queryClient.invalidateQueries({
      predicate: (query) =>
        (query.queryKey[0] === 'plans' || query.queryKey[0] === 'tasks') &&
        queryKeyMentionsPlanId(query.queryKey, planId),
    })
  }
  // Broad, unscoped fallback — stale-mark only, no forced refetch (see doc
  // comment above). Fires exactly once per debounce window regardless of how
  // many plan_status frames (or distinct plan ids) triggered it.
  queryClient.invalidateQueries({ queryKey: ['plans'], refetchType: 'none' })
  queryClient.invalidateQueries({ queryKey: ['tasks'], refetchType: 'none' })
}

export function schedulePlanStatusInvalidate(planId: string): void {
  pendingPlanStatusIds.add(planId)
  if (planStatusInvalidateTimer) return
  planStatusInvalidateTimer = setTimeout(flushPlanStatusInvalidation, PLAN_STATUS_INVALIDATE_DEBOUNCE_MS)
}

// F3 (SILENT-FAILURES-rate-limits-dd25339bf.md): `library_changed` fires on
// EVERY Library write — a bulk operation (e.g. trashing 54 files) or several
// tabs writing at once broadcasts a burst of these in quick succession. Each
// one used to trigger its OWN full invalidation pass (the listing prefix for
// its workspace, plus the shared workspaces list), so a burst of N frames
// cost N full reload passes — competing with the very same shared
// per-workspace knowledge rate limiter this fix round exists to stop
// tripping. Mirrors `schedulePlanStatusInvalidate` above exactly: same
// trailing-edge debounce shape, same "collect ids, flush once" pattern.
const LIBRARY_CHANGED_INVALIDATE_DEBOUNCE_MS = 1000

let libraryChangedInvalidateTimer: ReturnType<typeof setTimeout> | undefined

const pendingLibraryChangedWorkspaceIds = new Set<string>()

function flushLibraryChangedInvalidation(): void {
  const workspaceIds = Array.from(pendingLibraryChangedWorkspaceIds)
  pendingLibraryChangedWorkspaceIds.clear()
  libraryChangedInvalidateTimer = undefined
  for (const workspaceId of workspaceIds) {
    queryClient.invalidateQueries({ queryKey: ['library', workspaceId] })
  }
  // The workspaces list carries entry_count for every workspace — one shared
  // invalidation covers all of them, fired once per flush regardless of how
  // many distinct workspaces' frames arrived in this window.
  queryClient.invalidateQueries({ queryKey: libraryQueryKeys.workspaces() })
}

export function scheduleLibraryChangedInvalidate(workspaceId: string): void {
  pendingLibraryChangedWorkspaceIds.add(workspaceId)
  if (libraryChangedInvalidateTimer) return
  libraryChangedInvalidateTimer = setTimeout(flushLibraryChangedInvalidation, LIBRARY_CHANGED_INVALIDATE_DEBOUNCE_MS)
}

// ADR-082 review S2: turn ids whose OWN (non-replay-terminator) `done` has
// already been processed, keyed by session id. A `session_state.active_turn`
// announcement racing a reconnect can name a turn that this client already
// finalized on an earlier connection cycle (the announcement was snapshotted
// server-side before the done landed, or simply arrives late) — without this
// guard, re-applying that stale announcement would set isStreaming:true /
// activeTurnId again with no `done` ever coming to clear it a second time,
// wedging the Stop button and composer lock permanently. Bounded per-session
// FIFO: a session only ever has one turn "in flight" at a time from this
// client's perspective, so a handful of recently-finished ids per session is
// more than enough to catch any plausible race window; unbounded growth
// across a long-lived session is the failure mode this cap exists to avoid.
const FINISHED_TURN_IDS_CAP = 8

export function markTurnFinished(sessionId: string, turnId: string | null | undefined): void {
  if (!turnId) return
  const ids = finishedTurnIdsBySession[sessionId] ?? (finishedTurnIdsBySession[sessionId] = [])
  if (ids.includes(turnId)) return
  ids.push(turnId)
  if (ids.length > FINISHED_TURN_IDS_CAP) ids.shift()
}

export function isTurnFinished(sessionId: string, turnId: string): boolean {
  return finishedTurnIdsBySession[sessionId]?.includes(turnId) ?? false
}

// ADR-091 D10: `hasOpenSpanFast` and `bufferForSpan` (the O(1)
// spanByParentCallId check and the out-of-order tool_call_start/result
// buffer keyed by parent_call_id) are deleted along with the child-step-
// nesting mechanism they served — a child's own tool calls now always carry
// the child's own session_id (I-4) and never arrive in the parent's bucket,
// so there is nothing left to buffer or fast-check against.
