// SetGoalToolUI — ADR-082 D9 (goal card anchored at its `set_goal` call, not
// at the thread tail; ui-independent-turns-spec.md FR-016..FR-019).
//
// The prior thread-tail component (now deleted) rendered the record card at
// the tail of the transcript, keyed purely off `goalPills` (the store's map of the LATEST
// `goal_status` frame per goal id). That meant: (a) the card only ever
// showed the CURRENT record, never each individual `set_goal` call's own
// record at the point it happened, and (b) nothing rendered until the
// engine's write-side `goal_status` emission reached the SPA — a round trip
// the call's own result already carries the answer to (`pkg/tools/
// set_goal.go`'s success payload now carries `goal_id` + the full
// definition/criteria/dod, ADR-082 D9/FR-016).
//
// This component renders the SAME `GoalEchoCard` directly from a `set_goal`
// tool call's own RESULT, at that call's own position in the message —
// live (via `makeAssistantToolUI`, registered as `SetGoalToolUI` below) and
// on replay (via the exported `SetGoalCardBlock`, called directly from
// `VirtualAssistantMessageRow`'s parts loop in ChatScreen.tsx). Each call
// renders its own card: a `mode: "register"` call shows the record as
// authored; a later `mode: "update"` (amend) call shows the amended record
// at ITS OWN position, while the earlier card is untouched (FR-019, spec
// S-16) — there is no shared mutable state between cards, only the
// independent props each call's own result provides.
//
// Live overlay (FR-018, spec S-17): once a `goal_status` frame lands for
// this call's `goal_id` (`useChatStore`'s `goalPills`), its state/round/cap/
// per-criterion `status` (pending/met/unmet) overlay onto the card WITHOUT
// replacing the definition/criteria/dod text the card already has — the
// result stays the fallback (and the ONLY source) for the record's own
// content, so the card renders correctly on first paint (before any frame
// has arrived) and again after a reload (EmitGoalStatusRehydrate re-sends
// one `goal_status` frame on session attach, landing here the same way).
// Per-criterion overlay matches by normalized TEXT (mirroring the backend's
// own `mergeCriterionKindFromOld` convention, pkg/tools/set_goal.go) since
// criterion ids are minted fresh by `task.NormalizeCriteria` on every write
// and are therefore not stable across an amend.

import { useMemo } from 'react'
import { makeAssistantToolUI } from '@assistant-ui/react'
import { useChatStore } from '@/store/chat'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'
import { GoalEchoCard } from '../GoalEchoCard'

/** The `criteria`/`dod` array element shape, taken from the wire frame type
 * itself (not re-declared) so this file can never drift from it. */
type GoalCriterion = NonNullable<GoalStatusFrame['criteria']>[number]

/** The shape of `set_goal`'s success result (pkg/tools/set_goal.go, ADR-082
 * D9/FR-016) — the model-facing `criteria_count`/`dod_count`/`mode`/
 * `assessment`/`diff` fields also exist on the payload but are not read by
 * this renderer. */
export interface SetGoalResult {
  goal_id: string
  definition: string
  criteria: GoalCriterion[]
  dod: GoalCriterion[]
}

/** Narrows an unknown tool-call result to `SetGoalResult`. Handles both
 * shapes a `result` prop can arrive in — a JSON string (the live WS
 * `tool_call_result` frame carries the tool's raw `ForLLM` text verbatim,
 * pkg/agent/events.go's `ToolExecEndPayload.Result`) or an already-parsed
 * object (the shape some render paths normalize to) — mirroring the
 * established parse pattern in this directory (see BrowserNavigate.tsx's
 * `parseResult`). Returns `null` for anything that doesn't validate: a
 * still-running call (no result yet), a failed call (whose `ForLLM` is a
 * plain error sentence, not this JSON shape — no card renders for a
 * rejected submission; the calling agent's own retry/response text is the
 * surface for that, per toolVisibility.ts's `set_goal` case), or a
 * genuinely malformed payload. */
export function parseSetGoalResult(result: unknown): SetGoalResult | null {
  if (result === null || result === undefined) return null
  let obj: unknown = result
  if (typeof result === 'string') {
    try {
      obj = JSON.parse(result)
    } catch {
      return null
    }
  }
  if (typeof obj !== 'object' || obj === null) return null
  const o = obj as Record<string, unknown>
  if (typeof o.goal_id !== 'string' || o.goal_id === '') return null
  if (typeof o.definition !== 'string') return null
  if (!Array.isArray(o.criteria)) return null
  return {
    goal_id: o.goal_id,
    definition: o.definition,
    criteria: o.criteria as GoalCriterion[],
    dod: Array.isArray(o.dod) ? (o.dod as GoalCriterion[]) : [],
  }
}

/** Overlays a live/rehydrated pool's per-item `status` onto `items`, matched
 * by normalized text (see this file's doc comment for why text, not id).
 * Items with no match in `pool` keep their original (freshly-written,
 * always `"pending"`) status unchanged. */
function overlayCriterionStatus(items: GoalCriterion[], pool: GoalCriterion[] | undefined): GoalCriterion[] {
  if (!pool || pool.length === 0) return items
  const byText = new Map(pool.map((p) => [p.text.trim().toLowerCase(), p]))
  return items.map((item) => {
    const match = byText.get(item.text.trim().toLowerCase())
    return match ? { ...item, status: match.status } : item
  })
}

/**
 * Builds the `GoalStatusFrame`-shaped object `GoalEchoCard` renders from —
 * the call's own result is the fallback/only source for definition/
 * criteria/dod TEXT; a matching `goalPills` entry (by `goal_id`) overlays
 * progress fields (state/round/max_rounds/cap/active_loops/latest_reason)
 * and, per item, `status` (FR-018). A pill for a DIFFERENT goal_id (or no
 * pill at all — first render, before any frame has landed) contributes
 * nothing; the card still renders correctly from the result alone.
 */
export function buildFrameFromSetGoalResult(result: SetGoalResult, pill: GoalStatusFrame | undefined): GoalStatusFrame {
  const matchingPill = pill && pill.goal_id === result.goal_id ? pill : undefined
  return {
    type: 'goal_status',
    session_id: matchingPill?.session_id ?? '',
    goal_id: result.goal_id,
    condition: result.definition,
    definition: result.definition,
    round: matchingPill?.round ?? 0,
    max_rounds: matchingPill?.max_rounds ?? 0,
    latest_reason: matchingPill?.latest_reason ?? '',
    active_loops: matchingPill?.active_loops ?? 0,
    cap: matchingPill?.cap ?? 0,
    state: matchingPill?.state ?? 'active',
    criteria: overlayCriterionStatus(result.criteria, matchingPill?.criteria),
    dod: overlayCriterionStatus(result.dod, matchingPill?.dod),
  }
}

export interface SetGoalCardBlockProps {
  result: unknown
  /** Still executing (no result yet) — renders nothing rather than a
   * placeholder; the record card has nothing to show until the write
   * actually lands (there is no useful partial state to preview). */
  isRunning: boolean
}

/**
 * Shared block for both the live (`makeAssistantToolUI`) and replay
 * (`VirtualAssistantMessageRow`'s parts loop) paths — mirrors the
 * WebServeBlock/BrowserToolReplayBlock precedent of one presentational
 * component driven by props, registered for live dispatch by a thin
 * factory below.
 */
export function SetGoalCardBlock({ result, isRunning }: SetGoalCardBlockProps) {
  const parsed = useMemo(() => parseSetGoalResult(result), [result])
  const pill = useChatStore((s) => (parsed ? s.goalPills?.[parsed.goal_id] : undefined))

  if (isRunning || !parsed) {
    // No result yet, or a failed/malformed result — nothing to render. The
    // raw tool-call chip stays hidden regardless (toolVisibility.ts's
    // `set_goal` case), so a still-running or rejected call is silent here,
    // exactly as spec'd (no error-forces-visible exception for this tool).
    return null
  }

  const frame = buildFrameFromSetGoalResult(parsed, pill)
  return <GoalEchoCard frame={frame} />
}

/**
 * Live tool UI registration — mount `<SetGoalToolUI />` alongside the
 * other `makeAssistantToolUI` components (OmnipusRuntimeProvider.tsx).
 * `GenericToolCall` (the Fallback) never sees a `set_goal` part once this
 * is registered — AssistantUI dispatches by tool name to the most specific
 * registered component first.
 */
export const SetGoalToolUI = makeAssistantToolUI<Record<string, unknown>, unknown>({
  toolName: 'set_goal',
  render: ({ result, status }) => (
    <SetGoalCardBlock result={result} isRunning={status.type === 'running'} />
  ),
})
