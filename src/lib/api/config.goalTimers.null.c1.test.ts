// Canonical PerformanceSettings/Update timer keys are integer/boolean, never
// nullable. Omission remains partial update, not a null-as-reset operation.
// Additive post-API baseline; independent CHECK owns actual mutant causality.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  PerformanceSettings as SettingsSchema,
  PerformanceSettingsUpdate as UpdateSchema,
} from '@/lib/api/generated/schemas'
import type { PerformanceSettings, PerformanceSettingsUpdate } from '@/lib/api/generated/openapi-types'
import { fetchPerformanceSettings, updatePerformanceSettings } from './config'
import { ApiSchemaError } from './http'

const timerKeys = ['goal_reminder_quiet_seconds', 'goal_stalled_checkin_enabled', 'goal_stalled_checkin_minutes'] as const
function validSettings(enabled: boolean): PerformanceSettings {
  return {
    max_parallel_agents: 4,
    effective_max_parallel_agents: 4,
    max_parallel_agents_configured: true,
    tools_on_demand: false,
    goal_max_rounds: 11,
    goal_reminder_quiet_seconds: 120,
    goal_stalled_checkin_enabled: enabled,
    goal_stalled_checkin_minutes: 45,
    max_tool_iterations: 1000,
  }
}
function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
}
let fetchMock: ReturnType<typeof vi.fn>
beforeEach(() => {
  fetchMock = vi.fn()
  vi.stubGlobal('fetch', fetchMock)
  vi.spyOn(document, 'cookie', 'get').mockReturnValue('csrf=c1-null-local-synthetic-cookie')
})
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('C1 explicit-null non-nullable runtime contracts', () => {
  it('known response validator and actual fetch instrument can accept and reject', async () => {
    const positive = validSettings(true)
    expect(SettingsSchema.parse(positive)).toEqual(positive)
    const negative = UpdateSchema.safeParse({ goal_max_rounds: 0 })
    expect(negative.success).toBe(false)
    if (!negative.success) expect(negative.error.issues.map((issue) => issue.path)).toEqual([['goal_max_rounds']])
    fetchMock.mockResolvedValue(jsonResponse(positive))
    expect(await fetchPerformanceSettings()).toEqual(positive)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/performance')
  })
  for (const enabled of [false, true]) {
    it(`nondefault values and explicit boolean ${enabled} survive real schemas and GET transport`, async () => {
      const response = validSettings(enabled)
      const update: PerformanceSettingsUpdate = {
        goal_max_rounds: 11,
        goal_reminder_quiet_seconds: 120,
        goal_stalled_checkin_enabled: enabled,
        goal_stalled_checkin_minutes: 45,
      }
      expect(UpdateSchema.parse(update)).toEqual(update)
      expect(SettingsSchema.parse(response)).toEqual(response)
      fetchMock.mockResolvedValue(jsonResponse(response))
      expect(await fetchPerformanceSettings()).toEqual(response)
    })
  }
  it('omitted optional UPDATE keys remain omitted, never substituted with null or defaults', () => {
    const input: PerformanceSettingsUpdate = { goal_max_rounds: 19 }
    const parsed = UpdateSchema.parse(input)
    expect(parsed).toEqual(input)
    for (const field of timerKeys) expect(parsed).not.toHaveProperty(field)
  })
  for (const field of timerKeys) {
    it(`generated UPDATE rejects present null for ${field} rather than accepting and dropping it`, () => {
      const positive = { goal_max_rounds: 19, goal_reminder_quiet_seconds: 120, goal_stalled_checkin_enabled: false, goal_stalled_checkin_minutes: 45 }
      expect(UpdateSchema.parse(positive)).toEqual(positive)
      const result = UpdateSchema.safeParse({ ...positive, [field]: null })
      expect(result.success).toBe(false)
      if (!result.success) expect(result.error.issues.map((issue) => issue.path)).toEqual([[field]])
    })
    it(`generated response rejects present null for ${field} rather than accepting and dropping it`, () => {
      const positive = validSettings(false)
      expect(SettingsSchema.parse(positive)).toEqual(positive)
      const result = SettingsSchema.safeParse({ ...positive, [field]: null })
      expect(result.success).toBe(false)
      if (!result.success) expect(result.error.issues.map((issue) => issue.path)).toEqual([[field]])
    })
    it(`actual GET transport refuses a null ${field} response with the exact invalid-field path`, async () => {
      const positive = validSettings(false)
      expect(SettingsSchema.parse(positive)).toEqual(positive)
      fetchMock.mockResolvedValue(jsonResponse({ ...positive, [field]: null }))
      let caught: unknown
      try { await fetchPerformanceSettings() } catch (error) { caught = error }
      expect(caught).toBeInstanceOf(ApiSchemaError)
      if (caught instanceof ApiSchemaError) expect(caught.zodIssues.map((issue) => issue.path)).toEqual([[field]])
      expect(fetchMock).toHaveBeenCalledTimes(1)
    })
  }
  it('real partial PUT serializes omission exactly and preserves false in its parsed response', async () => {
    const patch: PerformanceSettingsUpdate = { goal_max_rounds: 19 }
    const response = { ...validSettings(false), goal_max_rounds: 19 }
    fetchMock.mockResolvedValue(jsonResponse(response))
    expect(await updatePerformanceSettings(patch)).toEqual(response)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, options] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/v1/performance')
    expect(options.method).toBe('PUT')
    expect(JSON.parse(options.body)).toEqual(patch)
    for (const field of timerKeys) expect(JSON.parse(options.body)).not.toHaveProperty(field)
  })
})
