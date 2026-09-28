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

async function putError(body: Parameters<typeof updatePerformanceSettings>[0] = { max_tool_iterations: 150 }): Promise<unknown> {
  try {
    await updatePerformanceSettings(body)
  } catch (err) {
    return err
  }
  throw new Error('expected updatePerformanceSettings to reject')
}

describe('updatePerformanceSettings — 500 mapping after the review gate', () => {
  const ALPHA = { agent_id: 'a1', agent_name: 'Alpha', old_value: 250, new_value: 200 }

  it('500 performance_reload_failed (stage reload) becomes PerformanceReloadFailedError: "Saved, but not applied yet: <server text>" carrying details.lowered_agents', async () => {
    fetchMock.mockResolvedValue(jsonResponse(500, {
      error: 'agent registry reload failed',
      code: 'performance_reload_failed',
      details: { stage: 'reload', changed_fields: ['max_tool_iterations'], lowered_agents: [ALPHA] },
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
    expect(e.stage).toBe('reload')
    expect(e.inMemoryUpdated).toBe(true)
    expect(e.changedFields).toEqual(['max_tool_iterations'])
    expect(e.loweredUnknown).toBe(false)
  })

  it('stage refresh says the values are in the settings file and apply after a restart or reload, with the server text', async () => {
    const text = 'performance settings saved but the reload failed; the new tool-iteration limit applies after the next reload or restart'
    fetchMock.mockResolvedValue(jsonResponse(500, {
      error: text,
      code: 'performance_reload_failed',
      details: { stage: 'refresh', changed_fields: ['max_tool_iterations'], lowered_agents: [] },
    }))
    const e = (await putError()) as PerformanceReloadFailedError
    expect(isPerformanceReloadFailed(e)).toBe(true)
    expect(e.stage).toBe('refresh')
    expect(e.inMemoryUpdated).toBe(false)
    expect(e.userMessage).toBe(`Saved, but not applied yet — saved to the settings file; takes effect after a restart or reload: ${text}`)
  })

  it('an EMPTY message still reads "saved, not applied" and names only the changed fields', async () => {
    fetchMock.mockResolvedValue(jsonResponse(500, {
      error: '',
      code: 'performance_reload_failed',
      details: { stage: 'refresh', changed_fields: ['goal_max_rounds'], lowered_agents: [] },
    }))
    const e = (await putError({ goal_max_rounds: 7 })) as PerformanceReloadFailedError
    expect(isPerformanceReloadFailed(e)).toBe(true)
    expect(e.userMessage).toBe('Saved, but not applied yet — saved to the settings file; takes effect after a restart or reload: the new goal tries will be used after a restart or reload')
    expect(e.userMessage).not.toMatch(/tool-call/)
  })

  it('a malformed body (no details, empty message) is still a committed save with an unknown stage, naming what the client sent', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    fetchMock.mockResolvedValue(jsonResponse(500, { error: '', code: 'performance_reload_failed' }))
    const e = (await putError()) as PerformanceReloadFailedError
    expect(isPerformanceReloadFailed(e)).toBe(true)
    expect(e.stage).toBeNull()
    expect(e.inMemoryUpdated).toBe(false)
    // #904 gate round 3 (#6): the body says nothing, so the changed settings
    // are named from the request itself.
    expect(e.changedFields).toEqual(['max_tool_iterations'])
    expect(e.userMessage).toBe('Saved, but not applied yet — saved to the settings file; takes effect after a restart or reload: the new tool-call limit will be used after a restart or reload')
  })

  it('a malformed body names every setting the request carried, and nothing it did not (confirmed_lowering is not a setting)', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    fetchMock.mockResolvedValue(jsonResponse(500, { error: '', code: 'performance_reload_failed', details: 'garbage' }))
    const e = (await putError({ max_parallel_agents: 4, tools_on_demand: false, goal_max_rounds: 9 })) as PerformanceReloadFailedError
    expect(e.changedFields).toEqual(['max_parallel_agents', 'tools_on_demand', 'goal_max_rounds'])
    expect(e.userMessage).toBe('Saved, but not applied yet — saved to the settings file; takes effect after a restart or reload: the new agents running at once, on-demand tool loading, goal tries will be used after a restart or reload')
    expect(e.userMessage).not.toMatch(/tool-call/)
  })

  it('an unknown stage value is not trusted: stage is null', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    fetchMock.mockResolvedValue(jsonResponse(500, {
      error: 'x',
      code: 'performance_reload_failed',
      details: { stage: 'applied', changed_fields: [], lowered_agents: [] },
    }))
    const e = (await putError()) as PerformanceReloadFailedError
    expect(e.stage).toBeNull()
  })

  it('a missing lowered list on a request that confirmed a lowering is flagged unknown, not silently empty', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    fetchMock.mockResolvedValue(jsonResponse(500, { error: 'reload failed', code: 'performance_reload_failed' }))
    const e = (await putError({ max_tool_iterations: 150, confirmed_lowering: [{ agent_id: 'a1', old_value: 250 }] })) as PerformanceReloadFailedError
    expect(e.loweredAgents).toEqual([])
    expect(e.loweredUnknown).toBe(true)
  })

  it('performance_reload_failed with no details on a request that confirmed no lowering: loweredAgents is empty and not unknown', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    fetchMock.mockResolvedValue(jsonResponse(500, { error: 'reload failed', code: 'performance_reload_failed' }))
    const err = (await putError()) as PerformanceReloadFailedError
    expect(isPerformanceReloadFailed(err)).toBe(true)
    expect(err.loweredAgents).toEqual([])
    expect(err.loweredUnknown).toBe(false)
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
    expect(err.loweredUnknown).toBe(true)
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('performance_reload_failed'), expect.anything())
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
