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

/** Normalises a value for the saved-state comparison. */
export function sameValue(a: unknown, b: unknown): boolean {
  const norm = (v: unknown): string => {
    if (v === undefined || v === null || v === '') return ''
    if (typeof v === 'number' || typeof v === 'boolean') return String(v)
    if (typeof v === 'string') return v.trim().toLowerCase()
    return JSON.stringify(v)
  }
  return norm(a) === norm(b)
}

export type CheckedState = // not-wire-format: result of the saved-state check for one key
  | { kind: 'saved'; inUse: boolean }
  | { kind: 'not-saved' }
  | { kind: 'changed-elsewhere'; savedValue: unknown }

/**
 * The saved-state check of C-6 (FR-020 outcome 4). For a key the gateway
 * lists in GET /config/pending-restart, the saved value is the entry's
 * `persisted_value` (read from config.json); for a key it does not list, the
 * saved value equals the running value, read through `readBack`. Throws when
 * either read fails, so the caller can say "may not have been saved".
 */
export async function savedStateCheck(
  items: { key: string; draft: unknown; before: unknown }[],
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
    else if (sameValue(saved, item.before)) result[item.key] = { kind: 'not-saved' }
    else result[item.key] = { kind: 'changed-elsewhere', savedValue: saved }
  }
  return result
}
