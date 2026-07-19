/**
 * useOccurrences — TanStack Query hook for the workspace Calendar's
 * server-expanded recurring-task occurrences.
 *
 * Spec: docs/internal/specs/calendar-recurrence-redesign-spec.md
 *   - "Occurrence expansion endpoint" (wire shape, bucketing, caps, tz authority)
 *   - FR-008 / FR-008a (endpoint contract, `every_ms` projection)
 *   - Operations & Rollback → Observability commitments (the two client counters)
 *
 * Calls `GET /api/v1/tasks/occurrences?workspace_id&from_ms&to_ms&tz`, passing
 * FullCalendar's own `activeStart`/`activeEnd` (half-open, `activeEnd`
 * exclusive — passed straight through, no client-side adjustment) and the
 * browser's IANA zone (the day-boundary authority for bucketing; the SPA
 * never computes recurrence/cron occurrences itself — Explicit Non-Behaviors).
 *
 * Self-contained fetch (deliberately does NOT go through the shared
 * `request()` pipeline in `@/lib/api`): that pipeline validates a list
 * response as one all-or-nothing schema (`schema.safeParse(body)` on the
 * whole array), which would let a single malformed `TaskOccurrenceSet` hide
 * every other task's occurrences. Constraint #8 requires per-item
 * drop + counter behaviour here instead (mirrors the existing tolerant
 * per-item pattern in `fetchSkills`/`fetchCommands`, `src/lib/api.ts`), so
 * this module re-implements just enough of the request plumbing (base URL,
 * `credentials:'include'` cookie auth, `ApiError` mapping) to do that safely.
 * GET requests are never CSRF-gated (`STATE_CHANGING_METHODS` in api.ts only
 * covers POST/PUT/PATCH/DELETE), so no CSRF header handling is needed here.
 *
 * not-wire-format note: `TaskOccurrenceSet`/`DayBucket` (the actual wire
 * types) are imported directly from the generated contract artifacts
 * (Constraint #8) — never redeclared.
 */

import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import { ApiError, ApiSchemaError } from '@/lib/api'
import { maybeDevToast } from '@/lib/dev-toast'
import { logError } from '@/lib/telemetry'
import { TaskOccurrenceSet as TaskOccurrenceSetSchema } from '@/lib/api/generated/schemas'
import type { TaskOccurrenceSet } from '@/lib/api/generated/openapi-types'

const BASE_URL = import.meta.env.VITE_API_URL ?? ''
const ENDPOINT = '/tasks/occurrences'

// ── Query key ──────────────────────────────────────────────────────────────────
//
// Keyed on workspace + range + tz (per the task brief) so a browser-zone change
// mid-session (rare, but e.g. a laptop waking after travel) cannot serve
// stale-zone buckets — the endpoint spec explicitly calls this out.

export function occurrencesQueryKey(
  workspaceId: string,
  fromMs: number,
  toMs: number,
  tz: string,
): readonly [string, string, string, number, number, string] {
  return ['tasks', 'occurrences', workspaceId, fromMs, toMs, tz] as const
}

// ── Dedicated client counters (Operations & Rollback → Observability) ──────────
//
// TWO distinct counters, deliberately not conflated:
//   - schema-drop: an occurrence SET failed TaskOccurrenceSet validation and was
//     dropped (Constraint #8's "drop + counter" — a genuine contract-drift signal).
//   - truncated: a schema-VALID set came back with `truncated: true` (expansion
//     hit the server's 500-instant/10k-iteration cap). Not a validation failure —
//     "the zod-drop counter is the wrong mechanism" (spec, Observability commitments).
// Pattern mirrors the existing `_apiSchemaErrorCount`/`_droppedFrameCount`
// counters in src/lib/api.ts / src/lib/ws.ts: module-level counter + get/reset
// exports + rate-limited logError in production + window test-hook exposure.

let _droppedOccurrenceSetCount = 0
let _truncatedOccurrenceSetCount = 0

export function getDroppedOccurrenceSetCount(): number {
  return _droppedOccurrenceSetCount
}

export function resetDroppedOccurrenceSetCount(): void {
  _droppedOccurrenceSetCount = 0
}

export function getTruncatedOccurrenceSetCount(): number {
  return _truncatedOccurrenceSetCount
}

export function resetTruncatedOccurrenceSetCount(): void {
  _truncatedOccurrenceSetCount = 0
}

function _recordDroppedOccurrenceSets(count: number): void {
  if (count <= 0) return
  _droppedOccurrenceSetCount += count
  if (!import.meta.env.DEV && import.meta.env.MODE !== 'test') {
    logError({
      event: 'occurrenceSetSchemaError',
      droppedCount: count,
      totalDropped: _droppedOccurrenceSetCount,
    })
  }
}

function _recordTruncatedOccurrenceSets(count: number): void {
  if (count <= 0) return
  _truncatedOccurrenceSetCount += count
  if (!import.meta.env.DEV && import.meta.env.MODE !== 'test') {
    logError({
      event: 'occurrenceSetTruncated',
      truncatedCount: count,
      totalTruncated: _truncatedOccurrenceSetCount,
    })
  }
}

if (
  (import.meta.env.DEV || import.meta.env.MODE === 'test' ||
    (typeof navigator !== 'undefined' && navigator.webdriver)) &&
  typeof window !== 'undefined'
) {
  const w = window as unknown as { __omnipus_test_hooks?: Record<string, unknown> }
  w.__omnipus_test_hooks ??= {}
  w.__omnipus_test_hooks.getDroppedOccurrenceSetCount = getDroppedOccurrenceSetCount
  w.__omnipus_test_hooks.resetDroppedOccurrenceSetCount = resetDroppedOccurrenceSetCount
  w.__omnipus_test_hooks.getTruncatedOccurrenceSetCount = getTruncatedOccurrenceSetCount
  w.__omnipus_test_hooks.resetTruncatedOccurrenceSetCount = resetTruncatedOccurrenceSetCount
}

// ── Fetch (tolerant per-item validation) ────────────────────────────────────────

export interface FetchOccurrencesParams { // not-wire-format: local fetch-fn args assembled into a query string; the wire response is the generated TaskOccurrenceSet
  workspaceId: string
  fromMs: number
  toMs: number
  tz: string
}

/**
 * Fetch and validate `GET /api/v1/tasks/occurrences`. Never throws on a
 * per-item schema mismatch — invalid entries are dropped and counted
 * (Constraint #8); only a transport failure, non-2xx response, non-JSON body,
 * or a top-level non-array shape throws.
 */
export async function fetchTaskOccurrences(
  params: FetchOccurrencesParams,
): Promise<TaskOccurrenceSet[]> {
  const search = new URLSearchParams({
    workspace_id: params.workspaceId,
    from_ms: String(params.fromMs),
    to_ms: String(params.toMs),
    tz: params.tz,
  })

  let res: Response
  try {
    res = await fetch(`${BASE_URL}/api/v1${ENDPOINT}?${search.toString()}`, {
      credentials: 'include',
    })
  } catch (cause) {
    throw new ApiError(0, 'Network unavailable. Check your connection.', { cause })
  }

  if (!res.ok) {
    throw await ApiError.fromResponse(res)
  }

  let body: unknown
  try {
    body = (await res.json()) as unknown
  } catch (cause) {
    const rawText = String(cause instanceof Error ? cause.message : cause)
    void maybeDevToast(`[occurrences] Non-JSON response: ${ENDPOINT}`, 'tasks:occurrences:non-json')
    throw new ApiSchemaError(
      `GET /api/v1${ENDPOINT}`,
      [{ path: [], message: 'Response is not valid JSON' }],
      rawText,
    )
  }

  if (!Array.isArray(body)) {
    void maybeDevToast(`[occurrences] Response is not an array: ${ENDPOINT}`, 'tasks:occurrences:shape')
    throw new ApiSchemaError(
      `GET /api/v1${ENDPOINT}`,
      [{ path: [], message: 'Response is not an array' }],
      body,
    )
  }

  // Tolerant per-item validation: one malformed TaskOccurrenceSet must not
  // hide every other task's occurrences (mirrors fetchSkills/fetchCommands
  // in src/lib/api.ts).
  const out: TaskOccurrenceSet[] = []
  let dropped = 0
  for (const item of body) {
    const parsed = TaskOccurrenceSetSchema.safeParse(item)
    if (parsed.success) out.push(parsed.data)
    else dropped++
  }
  if (dropped > 0) {
    _recordDroppedOccurrenceSets(dropped)
    void maybeDevToast(
      `[occurrences] dropped ${dropped} occurrence set(s) that failed schema validation`,
      'tasks:occurrences:schema',
    )
  }

  const truncatedCount = out.filter((s) => s.truncated).length
  _recordTruncatedOccurrenceSets(truncatedCount)

  return out
}

// ── Hook ─────────────────────────────────────────────────────────────────────

export interface UseOccurrencesParams { // not-wire-format: internal React hook params (workspace + Date range + tz); never serialized as a request body
  workspaceId: string
  /** FullCalendar's `view.activeStart` (or `datesSet` arg `.start`) — inclusive. */
  activeStart: Date
  /** FullCalendar's `view.activeEnd` (or `datesSet` arg `.end`) — EXCLUSIVE (half-open range). */
  activeEnd: Date
  /**
   * Override the viewer's IANA zone. Defaults to
   * `Intl.DateTimeFormat().resolvedOptions().timeZone` (the browser zone —
   * Timezone Semantics §1). Exposed mainly for deterministic tests; real
   * callers should omit it.
   */
  tz?: string
  /** Default `true`. Set `false` to suspend fetching (e.g. workspace not yet resolved). */
  enabled?: boolean
}

/**
 * Fetch server-expanded recurring-task occurrences for the given workspace +
 * visible calendar range. Read-only; no client-side recurrence computation
 * (Explicit Non-Behaviors) — this hook only fetches and validates, mapping
 * into chips happens in `eventMapping.mapToCalendarEvents`.
 *
 * On failure the caller sees `isError`/`error` (an `ApiError` or
 * `ApiSchemaError`) via the returned `UseQueryResult`, exactly like every
 * other query in this codebase — the "calendar still renders due/one-shot
 * chips + a non-blocking toast on occurrences failure" degrade behaviour
 * (Behavioral Contract, Error flows) is the caller's responsibility (it needs
 * the task list too, which this hook does not own).
 */
export function useOccurrences(
  params: UseOccurrencesParams,
): UseQueryResult<TaskOccurrenceSet[], ApiError | ApiSchemaError> {
  const { workspaceId, activeStart, activeEnd, enabled = true } = params
  const tz = params.tz ?? Intl.DateTimeFormat().resolvedOptions().timeZone
  const fromMs = activeStart.getTime()
  const toMs = activeEnd.getTime()

  return useQuery({
    queryKey: occurrencesQueryKey(workspaceId, fromMs, toMs, tz),
    queryFn: () => fetchTaskOccurrences({ workspaceId, fromMs, toMs, tz }),
    enabled: enabled && Boolean(workspaceId) && fromMs < toMs,
  })
}
