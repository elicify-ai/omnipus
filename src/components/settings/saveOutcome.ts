// saveOutcome.ts — the four write outcomes of the Settings Save bar
// (Settings redesign #1519, FR-020 and the C-6 outcome table).
//
// Every write the Save bar sends ends in exactly one of four outcomes,
// classified in this order — a recognized "saved but not applied" signal beats
// the HTTP status:
//   1. saved, not in use — 500 with code `saved_not_refreshed` (keys from
//      `details.keys`), a 2xx with `requires_restart: true`, or a 2xx
//      PerformanceSettings carrying `pending_apply`;
//   2. saved and in use — any other 2xx;
//   3. not saved — a 4xx refusal (the draft stays, the server's message goes
//      under its field);
//   4. unknown — any other 5xx, a connection failure or a lost response. The
//      caller then runs the saved-state check (savedStateCheck below).
//
// The write itself is abstracted as a fetch-like response so a section can
// hand in either a raw `fetch` result or an adapted API-client call
// (toWriteResponse).

import { z } from 'zod'
import { isApiError } from '@/lib/api'
import type { PendingRestartEntry } from '@/lib/api'
import type { components } from '@/lib/api/generated/openapi-types'

type ErrorResponse = components['schemas']['ErrorResponse']
import { BASE_URL, buildHeaders } from '@/lib/api/http'
import { PendingRestartEntry as PendingRestartEntrySchema } from '@/lib/api/generated/schemas'

/**
 * A fresh, uncached read of GET /config/pending-restart for the saved-state
 * check — the one read that reflects config.json (C-6). The body is validated
 * with the generated schema; any failure (transport, non-2xx, bad body)
 * throws so the caller reports "may not have been saved".
 */
export async function readPendingRestartFresh(): Promise<PendingRestartEntry[]> {
  const res = await fetch(`${BASE_URL}/api/v1/config/pending-restart`, {
    credentials: 'include',
    headers: buildHeaders(),
    cache: 'no-store',
  })
  if (!res.ok) throw new Error(`the list of waiting changes could not be read (status ${res.status})`)
  return z.array(PendingRestartEntrySchema).parse(await res.json()) as PendingRestartEntry[]
}

/** The minimal response shape the Save bar reads. Matches `Response`. */
export interface WriteResponse { // not-wire-format: in-browser transport adapter over a fetch Response
  ok: boolean
  status: number
  json: () => Promise<unknown>
}

export type WriteOutcome = // not-wire-format: screen-side classification of a write result
  | { kind: 'saved'; body: unknown }
  | { kind: 'saved-not-in-use'; keys: string[]; body: unknown; requiresRestart: boolean }
  | { kind: 'not-saved'; message: string; field?: string; status: number; body: unknown }
  | { kind: 'unknown'; reason: string }

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

async function readJson(res: WriteResponse): Promise<unknown> {
  try {
    return await res.json()
  } catch {
    return undefined
  }
}

/** Classifies one write result per FR-020 (recognized signal before status). */
export async function classifyWrite(write: () => Promise<WriteResponse>): Promise<WriteOutcome> {
  let res: WriteResponse
  try {
    res = await write()
  } catch (err) {
    // A connection failure or a lost response: nothing is known.
    const reason = err instanceof Error && err.message ? err.message : 'no answer'
    return { kind: 'unknown', reason }
  }
  const body = await readJson(res)
  const err = isRecord(body) ? (body as Partial<ErrorResponse>) : undefined
  if (err?.code === 'saved_not_refreshed') {
    const details = isRecord(err.details) ? err.details : {}
    const keys = Array.isArray(details.keys) ? details.keys.filter((k: unknown): k is string => typeof k === 'string') : []
    return { kind: 'saved-not-in-use', keys, body, requiresRestart: true }
  }
  if (res.status >= 200 && res.status < 300) {
    if (isRecord(body) && (body.requires_restart === true || isRecord(body.pending_apply))) {
      return { kind: 'saved-not-in-use', keys: [], body, requiresRestart: body.requires_restart === true }
    }
    return { kind: 'saved', body }
  }
  if (res.status >= 400 && res.status < 500) {
    const message =
      typeof err?.error === 'string' && err.error.trim()
        ? err.error
        : `The server refused this change (status ${res.status}) and gave no reason.`
    return { kind: 'not-saved', message, field: typeof err?.field === 'string' ? err.field : undefined, status: res.status, body }
  }
  const reason = typeof err?.error === 'string' && err.error.trim() ? err.error : `status ${res.status}`
  return { kind: 'unknown', reason }
}

/**
 * Adapts an API-client call (which resolves with the parsed body or throws an
 * ApiError) to the fetch-like WriteResponse the Save bar classifies. A
 * transport failure (ApiError status 0) is re-thrown so it classifies as
 * unknown, exactly like a raw fetch rejection.
 */
export async function toWriteResponse(call: () => Promise<unknown>): Promise<WriteResponse> {
  try {
    const body = await call()
    return { ok: true, status: 200, json: async () => body }
  } catch (err) {
    if (isApiError(err) && err.status > 0) {
      let parsed: unknown
      try {
        parsed = err.body ? JSON.parse(err.body) : undefined
      } catch {
        parsed = undefined
      }
      const body = isRecord(parsed)
        ? parsed
        : { error: err.userMessage, code: err.code, field: err.field }
      return { ok: false, status: err.status, json: async () => body }
    }
    throw err
  }
}

/**
 * The saved-state comparison. Strings compare exactly: case and spaces can
 * change meaning (program paths, standard parameters), so they are kept.
 * Empty, null and missing are the same "not set". A number and the text of a
 * number field compare by number, so "100" typed matches 100 read back.
 */
export function sameValue(a: unknown, b: unknown): boolean {
  const empty = (v: unknown) => v === undefined || v === null || v === ''
  if (empty(a) || empty(b)) return empty(a) && empty(b)
  if (typeof a === 'number' && typeof b === 'string') return numberText(b) === a
  if (typeof b === 'number' && typeof a === 'string') return numberText(a) === b
  const norm = (v: unknown): string => {
    if (typeof v === 'string') return v
    if (typeof v === 'number' || typeof v === 'boolean') return String(v)
    return canonicalJson(v)
  }
  return norm(a) === norm(b)
}

/** The number a number field's text stands for, or undefined. */
function numberText(text: string): number | undefined {
  const t = text.trim()
  if (t === '') return undefined
  const n = Number(t)
  return Number.isFinite(n) ? n : undefined
}

/** JSON with object keys sorted, so the same value read two ways compares equal. */
function canonicalJson(v: unknown): string {
  return JSON.stringify(v, (_k, val: unknown) =>
    val && typeof val === 'object' && !Array.isArray(val)
      ? Object.fromEntries(Object.entries(val as Record<string, unknown>).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)))
      : val,
  )
}

export type CheckedState = // not-wire-format: result of the saved-state check for one key
  | { kind: 'saved'; inUse: boolean }
  | { kind: 'not-saved' }
  | { kind: 'changed-elsewhere'; savedValue: unknown }
  /** The running read differs from the draft for a writer that does not
   *  record failed applies: nothing can be concluded (C-6). */
  | { kind: 'unknown' }

/** One value of a Save whose answer was lost, for the saved-state check. */
export interface SavedStateItem { // not-wire-format: screen-side input of the C-6 check
  /** Dotted config key, as GET /config/pending-restart names it. */
  key: string
  /** The value the Save sent. */
  draft: unknown
  /** The saved value before the Save. */
  before: unknown
  /**
   * False when the endpoint is not one of the C-6 writers that record a
   * failed apply in GET /config/pending-restart. Its running read then never
   * proves "not saved" (C-6); a read equal to the old value stays unknown.
   */
  recorded?: boolean
}

/**
 * The saved-state check of C-6 (FR-020 outcome 4). For a key the gateway
 * lists in GET /config/pending-restart, the saved value is the entry's
 * `persisted_value` (read from config.json); for a key it does not list, the
 * saved value equals the running value, read through `readBack`. Throws when
 * either read fails, so the caller can say "may not have been saved".
 */
export async function savedStateCheck(
  items: SavedStateItem[],
  readBack: ((key: string) => Promise<unknown>) | undefined,
): Promise<Record<string, CheckedState>> {
  const pending = await readPendingRestartFresh()
  const result: Record<string, CheckedState> = {}
  for (const item of items) {
    const entry = pending.find((e) => e.key === item.key)
    let saved: unknown
    if (entry) {
      saved = entry.persisted_value
    } else {
      if (!readBack) throw new Error('the saved value could not be read back')
      saved = await readBack(item.key)
    }
    if (sameValue(saved, item.draft)) result[item.key] = { kind: 'saved', inUse: !entry }
    else if (sameValue(saved, item.before)) {
      // C-6: only a recording writer's read can prove "not saved".
      result[item.key] = !entry && item.recorded === false ? { kind: 'unknown' } : { kind: 'not-saved' }
    }
    else result[item.key] = { kind: 'changed-elsewhere', savedValue: saved }
  }
  return result
}

/** The saved-state check's answer for one form part (one write). */
export type PartCheck = // not-wire-format: screen-side result of the C-6 check for a form part
  | { kind: 'saved'; inUse: boolean }
  | { kind: 'not-saved' }
  /** At least one value holds a third value; `saved` maps each such key to it. */
  | { kind: 'changed-elsewhere'; text: string; saved: Record<string, unknown> }
  | { kind: 'unknown' }

/** Shows a saved value in the "changed somewhere else" line. */
export function displaySavedValue(v: unknown): string {
  if (v === null || v === undefined || v === '') return 'empty'
  return typeof v === 'string' ? v : JSON.stringify(v)
}

/**
 * The C-6 saved-state check for a form part whose one write has an unknown
 * result (FR-020 outcome 4). Every value of the write is classified; the part
 * is saved only when every value holds its draft. A value that holds a third
 * value wins over the rest; then any value still holding its old value makes
 * the part not saved. Throws when either read fails.
 */
export async function checkFormPart(
  items: (SavedStateItem & { label: string })[],
  readBack: (key: string) => Promise<unknown>,
): Promise<PartCheck> {
  const states = await savedStateCheck(items, readBack)
  const elsewhere = items.filter((i) => states[i.key].kind === 'changed-elsewhere')
  if (elsewhere.length) {
    const saved: Record<string, unknown> = {}
    for (const i of elsewhere) saved[i.key] = (states[i.key] as { savedValue: unknown }).savedValue
    const text =
      elsewhere.length === 1
        ? `This setting was changed somewhere else while you were saving. Saved value: ${displaySavedValue(saved[elsewhere[0].key])}.`
        : `These settings were changed somewhere else while you were saving. Saved values: ${elsewhere
            .map((i) => `${i.label} ${displaySavedValue(saved[i.key])}`)
            .join(', ')}.`
    return { kind: 'changed-elsewhere', text, saved }
  }
  if (items.some((i) => states[i.key].kind === 'unknown')) return { kind: 'unknown' }
  if (items.some((i) => states[i.key].kind === 'not-saved')) return { kind: 'not-saved' }
  return { kind: 'saved', inUse: items.every((i) => (states[i.key] as { inUse?: boolean }).inUse !== false) }
}
