// maxToolIterationsLoweringConflict.test.ts — #904 RED, test plan row 13f
// (grill G3). The SPA must be able to read the D16 409 body's `preview`:
// the generic ApiError keeps a non-2xx body only as an opaque string, so the
// Performance API module adds MaxToolIterationsLoweringConflictError (extends
// ApiError, typed `.preview`) and the isMaxToolIterationsLoweringConflict
// guard, parsing the 409 against the GENERATED Zod schema — the same pattern
// as src/lib/api/library.ts::LibraryVersionConflictError.
//
// Spec: docs/internal/specs/tool-iteration-limit-spec.md — Integration
// Boundaries "409 consumption (grill G3)"; contract row
// MaxToolIterationsLoweringConflict {error, code, preview}.
//
// Assumed (not pinned by the spec, see RED report): the conversion happens
// on the existing PUT /performance function `updatePerformanceSettings`.
// Mock boundary: global fetch only.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  ApiError,
  isApiError,
  updatePerformanceSettings,
  MaxToolIterationsLoweringConflictError,
  isMaxToolIterationsLoweringConflict,
} from '@/lib/api'

const fetchMock = vi.fn()

function respond(status: number, body: unknown) {
  fetchMock.mockResolvedValueOnce(
    new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }),
  )
}

const PREVIEW = {
  value: 200,
  agents: [
    { agent_id: 'a1', agent_name: 'Alpha', old_value: 250, new_value: 200 },
    { agent_id: 'b1', agent_name: 'Beta', old_value: 220, new_value: 200 },
  ],
}
const DRIFT_BODY = {
  error: 'the agents affected by lowering the limit to 200 changed since the preview; review the updated list and confirm again',
  code: 'max_tool_iterations_lowering_drift',
  preview: PREVIEW,
}

beforeEach(() => {
  fetchMock.mockReset()
  vi.stubGlobal('fetch', fetchMock)
  vi.spyOn(document, 'cookie', 'get').mockReturnValue('__Host-csrf=test')
})
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

async function capture(p: Promise<unknown>): Promise<unknown> {
  try {
    await p
  } catch (err) {
    return err
  }
  throw new Error('expected the PUT to reject')
}

describe('MaxToolIterationsLoweringConflictError (D16 409, grill G3)', () => {
  it('a matching 409 becomes the typed error: guard true, still an ApiError(409), typed preview, code', async () => {
    respond(409, DRIFT_BODY)
    const err = await capture(updatePerformanceSettings({ max_tool_iterations: 200 } as never, 'tok'))
    expect(isMaxToolIterationsLoweringConflict(err)).toBe(true)
    expect(err).toBeInstanceOf(MaxToolIterationsLoweringConflictError)
    expect(err).toBeInstanceOf(ApiError)
    expect(isApiError(err)).toBe(true)
    const e = err as MaxToolIterationsLoweringConflictError
    expect(e.status).toBe(409)
    expect(e.code).toBe('max_tool_iterations_lowering_drift')
    expect(e.preview).toEqual(PREVIEW)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('a 409 whose body does not match the envelope stays a plain 409 ApiError (guard false)', async () => {
    respond(409, { error: 'some other conflict' })
    const err = await capture(updatePerformanceSettings({ max_tool_iterations: 200 } as never, 'tok'))
    expect(isMaxToolIterationsLoweringConflict(err)).toBe(false)
    expect(isApiError(err)).toBe(true)
    expect((err as ApiError).status).toBe(409)
  })

  it('a 409 with a malformed preview (agents missing) is NOT recognised as the typed conflict', async () => {
    respond(409, { ...DRIFT_BODY, preview: { value: 200 } })
    const err = await capture(updatePerformanceSettings({ max_tool_iterations: 200 } as never, 'tok'))
    expect(isMaxToolIterationsLoweringConflict(err)).toBe(false)
    expect((err as ApiError).status).toBe(409)
  })

  it('a non-409 error is never the typed conflict', async () => {
    respond(400, { error: 'max_tool_iterations must be between 1 and 1000' })
    const err = await capture(updatePerformanceSettings({ max_tool_iterations: 0 } as never, 'tok'))
    expect(isMaxToolIterationsLoweringConflict(err)).toBe(false)
    expect((err as ApiError).status).toBe(400)
  })

  it('the guard rejects non-errors', () => {
    expect(isMaxToolIterationsLoweringConflict(undefined)).toBe(false)
    expect(isMaxToolIterationsLoweringConflict(new Error('x'))).toBe(false)
    expect(isMaxToolIterationsLoweringConflict(new ApiError(409, 'x'))).toBe(false)
  })
})
