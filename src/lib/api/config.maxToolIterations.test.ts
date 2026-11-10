/**
 * #904 — PUT /performance error mapping and the lowering preview request
 * (docs/internal/specs/tool-iteration-limit-spec.md, Contract Changes: 409
 * MaxToolIterationsLoweringConflict; API and Data: the two lowering-failure
 * 500 codes). The 409 body is re-parsed with the generated Zod schema; a 409
 * that does not match stays a plain 409 ApiError (mirrors
 * src/lib/api/library.ts::LibraryVersionConflictError).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  updatePerformanceSettings,
  fetchMaxToolIterationsLoweringPreview,
  isMaxToolIterationsLoweringConflict,
  MaxToolIterationsLoweringConflictError,
} from './config'
import { ApiError, isApiError } from '../api-error'
import type { MaxToolIterationsLoweringConflict } from '@/lib/api/generated/openapi-types'

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

const CONFLICT: MaxToolIterationsLoweringConflict = {
  error: 'the agents to lower changed since the preview',
  code: 'max_tool_iterations_lowering_drift',
  preview: {
    value: 200,
    agents: [
      { agent_id: 'a', agent_name: 'Alpha', old_value: 250, new_value: 200 },
      { agent_id: 'b', agent_name: 'Beta', old_value: 220, new_value: 200 },
    ],
  },
}

let fetchMock: ReturnType<typeof vi.fn>

beforeEach(() => {
  fetchMock = vi.fn()
  vi.stubGlobal('fetch', fetchMock)
  vi.spyOn(document, 'cookie', 'get').mockReturnValue('__Host-csrf=test')
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

async function putError(): Promise<unknown> {
  try {
    await updatePerformanceSettings({ max_tool_iterations: 200, confirmed_lowering: [] })
  } catch (err) {
    return err
  }
  throw new Error('expected updatePerformanceSettings to reject')
}

describe('updatePerformanceSettings — #904 error mapping', () => {
  it('a 409 drift body becomes MaxToolIterationsLoweringConflictError carrying the fresh preview', async () => {
    fetchMock.mockResolvedValue(jsonResponse(409, CONFLICT))
    const err = await putError()
    expect(isMaxToolIterationsLoweringConflict(err)).toBe(true)
    expect(err).toBeInstanceOf(MaxToolIterationsLoweringConflictError)
    expect(err).toBeInstanceOf(ApiError)
    const conflict = err as MaxToolIterationsLoweringConflictError
    expect(conflict.status).toBe(409)
    expect(conflict.code).toBe('max_tool_iterations_lowering_drift')
    expect(conflict.preview).toEqual(CONFLICT.preview)
  })

  it('a 409 whose body does not match the conflict schema stays a plain 409 ApiError', async () => {
    fetchMock.mockResolvedValue(jsonResponse(409, { error: 'something else', code: 'other' }))
    const err = await putError()
    expect(isApiError(err)).toBe(true)
    expect((err as ApiError).status).toBe(409)
    expect(isMaxToolIterationsLoweringConflict(err)).toBe(false)
  })

  it('a 409 with a malformed preview (value out of 1–1000) is not treated as a drift', async () => {
    fetchMock.mockResolvedValue(jsonResponse(409, { ...CONFLICT, preview: { value: 0, agents: [] } }))
    const err = await putError()
    expect(isMaxToolIterationsLoweringConflict(err)).toBe(false)
    expect((err as ApiError).status).toBe(409)
  })

  it.each(['max_tool_iterations_lowering_failed', 'max_tool_iterations_rollback_incomplete'])(
    'a 500 with code %s surfaces the server message verbatim',
    async (code) => {
      const text = 'limit not changed; could not restore Alpha (now 200, was 250)'
      fetchMock.mockResolvedValue(jsonResponse(500, { error: text, code }))
      const err = await putError()
      expect((err as ApiError).status).toBe(500)
      expect((err as ApiError).code).toBe(code)
      expect((err as ApiError).userMessage).toBe(text)
    },
  )

  it('any other 500 keeps the generic message', async () => {
    fetchMock.mockResolvedValue(jsonResponse(500, { error: 'internal detail', code: 'boom' }))
    const err = await putError()
    expect((err as ApiError).userMessage).not.toBe('internal detail')
  })
})

describe('fetchMaxToolIterationsLoweringPreview', () => {
  it('GETs the preview endpoint with the value and returns the parsed preview', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, CONFLICT.preview))
    const preview = await fetchMaxToolIterationsLoweringPreview(200)
    expect(preview).toEqual(CONFLICT.preview)
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit | undefined]
    expect(url).toMatch(/\/api\/v1\/performance\/max-tool-iterations\/preview\?value=200$/)
    expect((init?.method ?? 'GET').toUpperCase()).toBe('GET')
  })
})
