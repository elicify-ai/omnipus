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
// `VirtualAssistantMessageRow`'s parts loop in ChatScreen.tsx and from
// ToolCallBadge.tsx for a delegated worker's own `set_goal` step). Each call
// renders its own card: a `mode: "register"` call shows the record as
// authored; a later `mode: "update"` (amend) call shows the amended record
// at ITS OWN position, while the earlier card is untouched (FR-019, spec
// S-16) — there is no shared mutable state between cards, only the
// independent props each call's own result provides.
//
// Engine-anchored calls (review CR8): a goal record written by an engine
// path that never ran the tool (marker-path activation/restate, the keeper's
// D7 fallback compile) is anchored in the transcript by pkg/agent as a
// synthetic `set_goal` call carrying the byte-identical result payload
// (`tools.SetGoalResultPayload`), so this component renders those exactly
// like a tool-authored call — nothing here knows or cares which it was.
//
// Result SHAPES (review S9/S12): the same call's result reaches this file in
// three shapes, and `parseSetGoalResult` treats them identically —
//   - live path: the raw payload JSON as a STRING (the `tool_call_result`
//     frame carries the tool's `ForLLM` text verbatim);
//   - replay path: an ENVELOPE object `{ text: "<payload json>" }` — the
//     transcript persists every plain-text tool result under that key
//     (pkg/agent/loop.go's tcRecord construction), and replay.go forwards
//     the persisted map unchanged; the inner `text` may itself already be an
//     object on some normalising paths;
//   - an already-parsed payload object.
// Before S12 the envelope was not unwrapped, so a card that rendered live
// vanished after a reload (e2e goal-card-position.spec.ts, post-reload
// assertion) — the live ordering was right, the persisted shape was not
// recognised.
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
//
// No pill, no progress claim (review S3): with no matching pill the card
// shows the REGISTERED record only — no round counter, no cap, no state —
// because the result carries none of that; inventing "0 rounds · 0 loops"
// or an "active" state would be a false claim (after a reload a finished
// goal would read as active until its frame arrived). Progress renders only
// once a pill for this goal_id provides it.
//
// Visibility (review S4): this dedicated UI pre-empts GenericToolCall on
// both paths, so it must honour the same verbose-chat contract itself —
// with verbose chat ON it falls through to GenericToolCall (the raw call,
// params and result expandable, failure included); with it OFF a FAILED
// call renders a one-line quiet "Goal registration failed" trace (detail on
// hover/expand) instead of vanishing, and a still-running call renders
// nothing. `classifySetGoalCall` is the single decision table; ChatScreen's
// `wouldToolCallBeVisible` consults it too, so "is there visible content"
// and "what renders" can never disagree.

import { useMemo } from 'react'
import {
  makeAssistantToolUI,
  type MessagePartStatus,
  type ToolCallMessagePartStatus,
} from '@assistant-ui/react'
import { Target, Warning } from '@phosphor-icons/react'
import { useChatStore } from '@/store/chat'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { shouldRenderToolCall } from '@/lib/toolVisibility'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'
import { GoalEchoCard } from '../GoalEchoCard'
import { GenericToolCall } from './GenericToolCall'

/** The `criteria`/`dod` array element shape, taken from the wire frame type
 * itself (not re-declared) so this file can never drift from it. */
type GoalCriterion = NonNullable<GoalStatusFrame['criteria']>[number]

/** The shape of `set_goal`'s success result (pkg/tools/set_goal.go's
 * `SetGoalResultPayload`, ADR-082 D9/FR-016) — the model-facing
 * `criteria_count`/`dod_count`/`mode`/`assessment`/`diff` fields also exist
 * on the payload but are not read by this renderer. `goal_id` is `''` for a
 * result that carries none (a pre-D9 result, or a pre-ADR-053 goal meta —
 * review S9/F8): the record still renders, only the live overlay is
 * unavailable. */
export interface SetGoalResult {
  goal_id: string
  definition: string
  criteria: GoalCriterion[]
  dod: GoalCriterion[]
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

/** Parses a JSON string, returning `undefined` (never throwing) when it is
 * not JSON. */
function parseJSONQuietly(text: string): unknown {
  try {
    return JSON.parse(text)
  } catch {
    return undefined
  }
}

/**
 * Normalises the three shapes a `set_goal` result arrives in (see the file
 * doc comment, "Result SHAPES") to the payload object, or `undefined` when
 * no object can be recovered. Exported for `wouldToolCallBeVisible`'s
 * sibling helpers and for tests; never throws.
 */
export function unwrapSetGoalResultEnvelope(result: unknown): unknown {
  if (result === null || result === undefined) return undefined
  let obj: unknown = result
  if (typeof obj === 'string') {
    obj = parseJSONQuietly(obj)
  }
  if (!isRecord(obj)) return undefined
  // Replay envelope: `{ text: "<json>" }` (or `{ text: {…} }`). Only unwrap
  // when the envelope carries nothing that looks like the payload itself —
  // a payload will never have a `text` key at its top level.
  if ('text' in obj && !('definition' in obj) && !('criteria' in obj)) {
    const inner = obj.text
    if (typeof inner === 'string') {
      const parsed = parseJSONQuietly(inner)
      return isRecord(parsed) ? parsed : undefined
    }
    return isRecord(inner) ? inner : undefined
  }
  return obj
}

/** Keeps only well-formed criterion items (an object with a non-empty
 * string `text`); everything else is dropped rather than thrown on (review
 * S9 — a malformed item used to throw inside render when a matching pill
 * existed, unmounting the whole thread). `status` is normalised to the
 * wire enum so the overlay and CriteriaBreakdown never see a stray value. */
function sanitizeCriteria(raw: unknown): GoalCriterion[] {
  if (!Array.isArray(raw)) return []
  const out: GoalCriterion[] = []
  for (const item of raw) {
    if (!isRecord(item)) continue
    if (typeof item.text !== 'string' || item.text.trim() === '') continue
    const status = item.status === 'met' || item.status === 'unmet' ? item.status : 'pending'
    out.push({ ...(item as unknown as GoalCriterion), status })
  }
  return out
}

/** Narrows an unknown tool-call result to `SetGoalResult`. Returns `null`
 * for anything that is not a record payload at all: a still-running call
 * (no result yet), a failed call (whose result is a plain error sentence,
 * not this JSON shape), or a genuinely malformed payload. A payload WITHOUT
 * `goal_id` still parses (goal_id `''`) — review S9: a pre-D9 result must
 * render a minimal card, not vanish. Never throws. */
export function parseSetGoalResult(result: unknown): SetGoalResult | null {
  const o = unwrapSetGoalResultEnvelope(result)
  if (!isRecord(o)) return null
  if (typeof o.definition !== 'string') return null
  if (!Array.isArray(o.criteria)) return null
  return {
    goal_id: typeof o.goal_id === 'string' ? o.goal_id : '',
    definition: o.definition,
    criteria: sanitizeCriteria(o.criteria),
    dod: sanitizeCriteria(o.dod),
  }
}

/** Overlays a live/rehydrated pool's per-item `status` onto `items`, matched
 * by normalized text (see this file's doc comment for why text, not id).
 * Items with no match in `pool` keep their original (freshly-written,
 * always `"pending"`) status unchanged. Pool items without a string `text`
 * are ignored (S9 — a frame is validated at the WS edge, but this must
 * never be the place a thread unmounts). */
function overlayCriterionStatus(items: GoalCriterion[], pool: GoalCriterion[] | undefined): GoalCriterion[] {
  if (!pool || pool.length === 0) return items
  const byText = new Map<string, GoalCriterion>()
  for (const p of pool) {
    if (typeof p?.text === 'string') byText.set(p.text.trim().toLowerCase(), p)
  }
  if (byText.size === 0) return items
  return items.map((item) => {
    const match = byText.get(item.text.trim().toLowerCase())
    return match ? { ...item, status: match.status } : item
  })
}

/** What `buildFrameFromSetGoalResult` hands the card: the frame to render
 * from, plus whether its progress fields (state/round/max_rounds/cap/
 * latest_reason/active_loops) came from a live pill. When `hasLiveProgress`
 * is false those fields are type-required placeholders only and MUST NOT be
 * rendered (review S3) — `SetGoalCardBlock` passes `showProgress={false}`
 * to the card in that case. */
export interface SetGoalCardView {
  frame: GoalStatusFrame
  hasLiveProgress: boolean
}

/**
 * Builds the `GoalStatusFrame`-shaped object `GoalEchoCard` renders from —
 * the call's own result is the fallback/only source for definition/
 * criteria/dod TEXT; a matching `goalPills` entry (by `goal_id`) overlays
 * progress fields (state/round/max_rounds/cap/active_loops/latest_reason)
 * and, per item, `status` (FR-018). A pill for a DIFFERENT goal_id, no pill
 * at all (first render, before any frame has landed; or after a reload
 * until rehydration), or a result with no goal_id contributes nothing: the
 * card still renders the record from the result alone, and reports
 * `hasLiveProgress: false` so no progress is claimed.
 */
export function buildFrameFromSetGoalResult(result: SetGoalResult, pill: GoalStatusFrame | undefined): SetGoalCardView {
  const matchingPill = pill && result.goal_id !== '' && pill.goal_id === result.goal_id ? pill : undefined
  return {
    hasLiveProgress: matchingPill !== undefined,
    frame: {
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
    },
  }
}

/** How a `set_goal` call presents in the thread — the ONE decision table
 * shared by `SetGoalCardBlock` (what renders) and ChatScreen's
 * `wouldToolCallBeVisible` (is there visible content), review S4:
 *   - `raw`    — verbose chat is on (or the classifier otherwise shows the
 *                raw call): render GenericToolCall, failure included.
 *   - `hidden` — still running, or completed with no result at all.
 *   - `failed` — a failed call, verbose off: the quiet one-line trace.
 *   - `card`   — a parseable record: GoalEchoCard.
 *   - `chip`   — a completed call whose result is present but is not a
 *                record (e.g. the tool's plain-text fallback line when its
 *                own payload marshal failed): a minimal chip with the text,
 *                so a successful registration always leaves a trace. */
export type SetGoalPresentation = 'raw' | 'hidden' | 'failed' | 'card' | 'chip'

export interface SetGoalCallInput {
  args?: unknown
  result: unknown
  isRunning: boolean
  isError: boolean
  verboseChatEnabled: boolean
}

/**
 * True when `result` (in any of the three shapes `unwrapSetGoalResultEnvelope`
 * normalizes) carries a truthy `unchanged` field — a `set_goal` call that
 * submitted an IDENTICAL duplicate of the already-registered record (a
 * separate wave adds this field to `tools.SetGoalResultPayload`; handled
 * defensively here regardless of exact landing order — CO-ORDINATION note
 * in this wave's brief). The operator's reported repro showed THREE
 * identical goal cards stacked in the thread from three such duplicate
 * calls; this is the field a caller checks to suppress the extra ones.
 * `unchanged` absent (older backend, or a genuinely new/changed record) is
 * simply `false` here — exactly today's behavior, no opt-in required.
 */
export function isUnchangedSetGoalResult(result: unknown): boolean {
  const o = unwrapSetGoalResultEnvelope(result)
  return isRecord(o) && o.unchanged === true
}

export function classifySetGoalCall(input: SetGoalCallInput): SetGoalPresentation {
  const params = isRecord(input.args) ? input.args : undefined
  if (shouldRenderToolCall('set_goal', params, input.verboseChatEnabled, input.isError)) {
    return 'raw'
  }
  if (input.isRunning) return 'hidden'
  if (input.isError) return 'failed'
  if (input.result === null || input.result === undefined) return 'hidden'
  // 2026-09-08 co-ordination fix: a duplicate call that changed nothing
  // renders NO card at all — not even the minimal 'chip' — same as a
  // still-running call. See isUnchangedSetGoalResult's doc comment.
  if (isUnchangedSetGoalResult(input.result)) return 'hidden'
  return parseSetGoalResult(input.result) ? 'card' : 'chip'
}

/** Maps the store's resolved ToolCall status to AssistantUI's per-part
 * status, for callers (ToolCallBadge, ChatScreen's replay branch) that hold
 * a store ToolCall rather than a live part. Mirrors ChatScreen's own
 * `replayPartStatus` for the terminal states and keeps `running` as
 * `running` (that helper deliberately folds it into `complete` for its own
 * replay-only purposes). */
export function partStatusFromToolCallStatus(
  status: 'running' | 'success' | 'error' | 'cancelled',
): MessagePartStatus {
  switch (status) {
    case 'running':
      return { type: 'running' }
    case 'cancelled':
      return { type: 'incomplete', reason: 'cancelled' }
    case 'error':
      return { type: 'incomplete', reason: 'error' }
    default:
      return { type: 'complete' }
  }
}

/** Renders the failure/chip detail text: the explicit error when the store
 * carries one, else the result verbatim. */
function detailText(result: unknown, error: string | undefined): string {
  if (error && error.trim() !== '') return error
  if (typeof result === 'string') return result
  if (result === null || result === undefined) return ''
  try {
    return JSON.stringify(result, null, 2)
  } catch {
    return String(result)
  }
}

/** Narrows the live tool-part status (which adds `requires-action` while a
 * human-in-the-loop tool awaits input) to the `MessagePartStatus`
 * GenericToolCall accepts — an awaiting-input call is, for rendering
 * purposes, still running. */
function toMessagePartStatus(status: ToolCallMessagePartStatus): MessagePartStatus {
  return status.type === 'requires-action' ? { type: 'running' } : status
}

export interface SetGoalCardBlockProps {
  /** The call's arguments (`params` on a store ToolCall) — consulted by the
   * visibility classifier and shown by GenericToolCall in verbose chat. */
  args?: unknown
  result: unknown
  /** Live tool-part status (the superset AssistantUI hands a tool UI) or a
   * plain `MessagePartStatus` from a store-backed caller. */
  status: ToolCallMessagePartStatus | MessagePartStatus
  /** Explicit running override for callers whose `status` mapping folds
   * `running` away (ChatScreen's `replayPartStatus`); defaults to
   * `status.type === 'running'`. */
  isRunning?: boolean
  /** The store's resolved failure outcome when the caller has one (issue
   * #617's rationale on GenericToolCallProps.isError); otherwise derived
   * from `status`/`error` exactly as GenericToolCall derives it. */
  isError?: boolean
  /** Failure text from the store (`ToolCall.error`). */
  error?: string
  durationMs?: number
  sessionId?: string
}

/**
 * Shared block for the live (`makeAssistantToolUI`), replay
 * (`VirtualAssistantMessageRow`'s parts loop) and delegated-step
 * (ToolCallBadge) paths — one presentational component driven by props,
 * registered for live dispatch by a thin factory below (the WebServeBlock/
 * BrowserToolReplayBlock precedent).
 */
export function SetGoalCardBlock({
  args,
  result,
  status,
  isRunning: isRunningProp,
  isError: isErrorProp,
  error,
  durationMs,
  sessionId = '',
}: SetGoalCardBlockProps) {
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  const partStatus = toMessagePartStatus(status as ToolCallMessagePartStatus)
  const isRunning = isRunningProp ?? partStatus.type === 'running'
  const isError =
    isErrorProp ?? ((partStatus.type === 'incomplete' && partStatus.reason !== 'cancelled') || !!error)
  const presentation = classifySetGoalCall({ args, result, isRunning, isError, verboseChatEnabled })
  const parsed = useMemo(
    () => (presentation === 'card' ? parseSetGoalResult(result) : null),
    [presentation, result],
  )
  const pill = useChatStore((s) => (parsed && parsed.goal_id !== '' ? s.goalPills?.[parsed.goal_id] : undefined))

  switch (presentation) {
    case 'hidden':
      return null
    case 'raw':
      return (
        <GenericToolCall
          toolName="set_goal"
          args={args}
          result={result}
          status={isRunning ? { type: 'running' } : partStatus}
          error={error}
          isError={isError}
          durationMs={durationMs}
          sessionId={sessionId}
        />
      )
    case 'failed': {
      const detail = detailText(result, error)
      return (
        <details data-testid="set-goal-failed" className="my-1 text-xs font-mono">
          <summary
            tabIndex={0}
            className="flex cursor-pointer list-none items-center gap-1.5 py-0.5 text-[var(--color-muted)]"
            title={detail || 'Goal registration failed'}
          >
            <Warning size={12} weight="fill" className="shrink-0 text-[var(--color-error)]" aria-hidden="true" />
            <span>Goal registration failed</span>
          </summary>
          {detail && (
            <pre
              data-testid="set-goal-failed-detail"
              className="ml-[3px] mt-1 max-h-48 overflow-auto whitespace-pre-wrap break-all border-l-2 border-[var(--color-border)] py-1 pl-3 text-[10px] text-[var(--color-secondary)]"
            >
              {detail}
            </pre>
          )}
        </details>
      )
    }
    case 'chip': {
      const detail = detailText(result, undefined)
      return (
        <div
          data-testid="set-goal-chip"
          className="my-1 flex items-center gap-1.5 text-xs text-[var(--color-muted)]"
          title={detail}
        >
          <Target size={12} weight="fill" className="shrink-0 text-[var(--color-accent)]" aria-hidden="true" />
          <span>Goal record registered</span>
          {detail && <span className="truncate font-mono text-[10px]">{detail}</span>}
        </div>
      )
    }
    case 'card': {
      if (!parsed) return null
      const view = buildFrameFromSetGoalResult(parsed, pill)
      return <GoalEchoCard frame={view.frame} showProgress={view.hasLiveProgress} />
    }
    default:
      return null
  }
}

/**
 * Live tool UI registration — mount `<SetGoalToolUI />` alongside the
 * other `makeAssistantToolUI` components (OmnipusRuntimeProvider.tsx).
 * `GenericToolCall` (the Fallback) never sees a `set_goal` part once this
 * is registered — AssistantUI dispatches by tool name to the most specific
 * registered component first — which is exactly why this UI re-implements
 * the verbose/failed contract itself (see the file doc comment).
 */
export const SetGoalToolUI = makeAssistantToolUI<Record<string, unknown>, unknown>({
  toolName: 'set_goal',
  render: ({ args, result, status, isError }) => (
    <SetGoalCardBlock args={args} result={result} status={status} isError={isError} />
  ),
})
