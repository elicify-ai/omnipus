/**
 * #904 review-gate fixes — PUT /performance 500 mapping (team-lead brief item 2):
 * a registry reload failure AFTER a committed save (500 performance_reload_failed)
 * reads "Saved, but not applied yet", never the generic "server unavailable";
 * other #904-owned 500s (e.g. an agent read/list failure during a lowering)
 * keep the server's message; an unrelated 500 stays generic.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  updatePerformanceSettings,
  isPerformanceReloadFailed,
  PerformanceReloadFailedError,
  PERFORMANCE_RELOAD_FAILED_CODE,
} from './config'
import { ApiError } from '../api-error'

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
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
    await updatePerformanceSettings({ max_tool_iterations: 150 })
  } catch (err) {
    return err
  }
  throw new Error('expected updatePerformanceSettings to reject')
}

describe('updatePerformanceSettings — 500 mapping after the review gate', () => {
  const ALPHA = { agent_id: 'a1', agent_name: 'Alpha', old_value: 250, new_value: 200 }

  it('500 performance_reload_failed becomes PerformanceReloadFailedError: "Saved, but not applied yet: <server text>" carrying details.lowered_agents', async () => {
    fetchMock.mockResolvedValue(jsonResponse(500, {
      error: 'agent registry reload failed',
      code: 'performance_reload_failed',
      details: { lowered_agents: [ALPHA] },
    }))
    const err = await putError()
    expect(isPerformanceReloadFailed(err)).toBe(true)
    expect(err).toBeInstanceOf(PerformanceReloadFailedError)
    expect(err).toBeInstanceOf(ApiError)
    const e = err as PerformanceReloadFailedError
    expect(e.status).toBe(500)
    expect(e.code).toBe(PERFORMANCE_RELOAD_FAILED_CODE)
    expect(e.userMessage).toBe('Saved, but not applied yet: agent registry reload failed')
    expect(e.loweredAgents).toEqual([ALPHA])
  })

  it('performance_reload_failed with no details lowered nobody: loweredAgents is empty', async () => {
    fetchMock.mockResolvedValue(jsonResponse(500, { error: 'reload failed', code: 'performance_reload_failed' }))
    const err = (await putError()) as PerformanceReloadFailedError
    expect(isPerformanceReloadFailed(err)).toBe(true)
    expect(err.loweredAgents).toEqual([])
  })

  it('a malformed details.lowered_agents is reported, not guessed at', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    fetchMock.mockResolvedValue(jsonResponse(500, {
      error: 'reload failed',
      code: 'performance_reload_failed',
      details: { lowered_agents: [{ agent_id: 'a1', new_value: 5000 }] },
    }))
    const err = (await putError()) as PerformanceReloadFailedError
    expect(isPerformanceReloadFailed(err)).toBe(true)
    expect(err.loweredAgents).toEqual([])
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('details.lowered_agents'), expect.anything())
  })

  it('an agent read/list failure during a lowering (a max_tool_iterations_* 500) shows the server message', async () => {
    const text = 'limit not changed: could not read the agent list'
    fetchMock.mockResolvedValue(jsonResponse(500, { error: text, code: 'max_tool_iterations_agents_read_failed' }))
    const err = await putError()
    expect(isPerformanceReloadFailed(err)).toBe(false)
    expect((err as ApiError).userMessage).toBe(text)
  })

  it('a 500 without a #904 code keeps the generic message and is not a reload failure', async () => {
    fetchMock.mockResolvedValue(jsonResponse(500, { error: 'internal detail' }))
    const err = await putError()
    expect(isPerformanceReloadFailed(err)).toBe(false)
    expect((err as ApiError).userMessage).not.toBe('internal detail')
    expect((err as ApiError).userMessage).not.toMatch(/^Saved/)
  })

  it('performance_reload_failed on a non-500 status is not treated as a committed save', async () => {
    fetchMock.mockResolvedValue(jsonResponse(503, { error: 'agent registry reload failed', code: 'performance_reload_failed' }))
    const err = await putError()
    expect(isPerformanceReloadFailed(err)).toBe(false)
  })
})
