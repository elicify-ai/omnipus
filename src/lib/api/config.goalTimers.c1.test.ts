// C1 DESIGN-v2: generated runtime validation must preserve values, not merely
// accept an object after silently stripping the three unknown timer keys.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  PerformanceSettings,
  PerformanceSettingsUpdate,
  PerformanceReloadFailedDetails,
  PerformancePendingApply,
} from '@/lib/api/generated/schemas'
import {
  fetchPerformanceSettings,
  updatePerformanceSettings,
  PerformanceReloadFailedError,
} from './config'

const known = {
  max_parallel_agents: 4,
  effective_max_parallel_agents: 4,
  max_parallel_agents_configured: true,
  tools_on_demand: true,
  goal_max_rounds: 7,
  max_tool_iterations: 1000,
}
const defaults = { goal_reminder_quiet_seconds: 60, goal_stalled_checkin_enabled: true, goal_stalled_checkin_minutes: 30 }
const retained = { goal_reminder_quiet_seconds: 120, goal_stalled_checkin_enabled: false, goal_stalled_checkin_minutes: 45 }
const keys = Object.keys(defaults)
let fetchMock: ReturnType<typeof vi.fn>
function response(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}
function tuple(value: unknown) {
  const raw = value as Record<string, unknown>
  return Object.fromEntries(keys.map((key) => [key, raw[key]]))
}

beforeEach(() => {
  fetchMock = vi.fn()
  vi.stubGlobal('fetch', fetchMock)
  vi.spyOn(document, 'cookie', 'get').mockReturnValue('csrf=c1-local-synthetic-cookie')
})
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('C1 performance timer runtime contract', () => {
  it('known generated validator and actual GET transport instrument', async () => {
    expect(PerformanceSettings.parse(known)).toEqual(known)
    expect(PerformanceSettings.safeParse({ ...known, goal_max_rounds: 0 }).success).toBe(false)
    expect(PerformanceSettingsUpdate.safeParse({ goal_max_rounds: 0 }).success).toBe(false)
    fetchMock.mockResolvedValue(response(known))
    const got = await fetchPerformanceSettings()
    expect(got).toEqual(known)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/performance')
  })
  it.each([defaults, retained])('generated response retains the exact three timer values %#', (fields) => {
    const parsed = PerformanceSettings.parse({ ...known, ...fields })
    expect(parsed.goal_max_rounds).toBe(7)
    expect(tuple(parsed)).toEqual(fields)
  })
  it.each([defaults, retained])('actual GET transport retains resolved values including false %#', async (fields) => {
    fetchMock.mockResolvedValue(response({ ...known, ...fields }))
    const got = await fetchPerformanceSettings()
    expect(got.goal_max_rounds).toBe(7)
    expect(tuple(got)).toEqual(fields)
  })
  for (const [key, values] of [
    ['goal_reminder_quiet_seconds', [30, 31, 3599, 3600]],
    ['goal_stalled_checkin_minutes', [5, 6, 1439, 1440]],
  ] as const) {
    it.each(values)(`generated request preserves inclusive ${key}=%i`, (value) => {
      const input = { [key]: value }
      expect(PerformanceSettingsUpdate.parse(input)).toEqual(input)
    })
  }
  it('generated request preserves explicit false and retained minutes', () => {
    expect(PerformanceSettingsUpdate.parse(retained)).toEqual(retained)
  })
  for (const [key, values] of [
    ['goal_reminder_quiet_seconds', [-1, 0, 29, 3601, 30.5, '60', false]],
    ['goal_stalled_checkin_minutes', [-1, 0, 4, 1441, 5.5, '30', false]],
    ['goal_stalled_checkin_enabled', [0, 'false']],
  ] as const) {
    it.each(values)(`generated request rejects malformed/outside ${key} case %#`, (value) => {
      const result = PerformanceSettingsUpdate.safeParse({ goal_max_rounds: 7, goal_stalled_checkin_enabled: false, [key]: value })
      expect(result.success).toBe(false)
      if (!result.success) expect(result.error.issues.some((issue) => issue.path[0] === key)).toBe(true)
    })
    it.each(values)(`generated response rejects malformed/outside ${key} case %#`, (value) => {
      const result = PerformanceSettings.safeParse({ ...known, ...defaults, [key]: value })
      expect(result.success).toBe(false)
      if (!result.success) expect(result.error.issues.some((issue) => issue.path[0] === key)).toBe(true)
    })
  }
  it('real PUT serializes false and retained minutes without adding absent siblings', async () => {
    const body = { goal_stalled_checkin_enabled: false, goal_stalled_checkin_minutes: 45 }
    fetchMock.mockResolvedValue(response({ ...known, ...retained }))
    const got = await updatePerformanceSettings(JSON.parse(JSON.stringify(body)), 'c1-synthetic-consent')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, options] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/v1/performance')
    expect(options.method).toBe('PUT')
    expect(JSON.parse(options.body)).toEqual(body)
    expect(new Headers(options.headers).get('X-Reauth-Token')).toBe('c1-synthetic-consent')
    expect(tuple(got)).toEqual(retained)
  })
  it.each(['refresh', 'reload'] as const)('generated reload and pending-apply enums retain all C1 changed_fields at %s', (stage) => {
    const fields = [...keys]
    const details = { stage, changed_fields: fields, lowered_agents: [] }
    expect(PerformanceReloadFailedDetails.parse(details)).toEqual(details)
    expect(PerformancePendingApply.parse({ stage, changed_fields: fields })).toEqual({ stage, changed_fields: fields })
  })
  it.each(['refresh', 'reload'] as const)('actual reload-failure decoder retains C1 fields/stage rather than salvaging empty defaults at %s', async (stage) => {
    fetchMock.mockResolvedValue(response({
      error: 'C1 synthetic saved but not applied', code: 'performance_reload_failed',
      details: { stage, changed_fields: keys, lowered_agents: [] },
    }, 500))
    let caught: unknown
    try { await updatePerformanceSettings(JSON.parse(JSON.stringify(retained))) } catch (err) { caught = err }
    expect(caught).toBeInstanceOf(PerformanceReloadFailedError)
    const error = caught as PerformanceReloadFailedError
    expect(error.status).toBe(500)
    expect(error.stage).toBe(stage)
    expect(error.changedFields).toEqual(keys)
    expect(error.loweredAgents).toEqual([])
    expect(error.inMemoryUpdated).toBe(stage === 'reload')
  })
})
