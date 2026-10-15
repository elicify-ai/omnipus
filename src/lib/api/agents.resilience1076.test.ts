// WU-10 fruit #1076 Expected: isolate malformed agent rows; keep healthy rows
// usable and identify the offending id/field without a version-mismatch story.
// Real fetchAgents + request + generated validator + query error reporting.
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { act, waitFor } from '@testing-library/react'
import { fetchAgents } from './agents'
import { ApiSchemaError } from './http'
import { Agent as AgentSchema } from './generated/schemas'
import type { Agent } from './generated/openapi-types'
import { queryClient } from '@/lib/queryClient'
import { useUiStore } from '@/store/ui'
import { JIM, MIA, agent } from '../../../tests/omnipus-l1-ui-red/fixtures'

let fetchMock = vi.fn<typeof fetch>()
beforeEach(() => {
  queryClient.clear()
  useUiStore.setState({ toasts: [] })
  fetchMock = vi.fn<typeof fetch>()
  vi.stubGlobal('fetch', fetchMock)
})
afterEach(() => {
  queryClient.clear()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})
function serve(body: unknown) {
  fetchMock.mockImplementation(async (url) => {
    expect(String(url), 'the request must exercise the real agents endpoint').toBe('/api/v1/agents')
    return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
  })
}
function healthyRows(rows: Agent[]) {
  // #1076 explicitly permits "skip or flag". A flagged/sanitized broken row
  // may remain; these assertions do not impose a new wire flag or forbid it.
  expect(rows.filter((row) => row.id !== 'broken')).toEqual([MIA, JIM])
  for (const row of rows) expect(AgentSchema.safeParse(row).success).toBe(true)
}

describe('#1076 per-entry roster resilience', () => {
  // Generated Agent.name min/max = 1/100. Independent contract boundaries:
  // min-1, min, min+1, max-1, max, max+1, not observed implementation output.
  it.each([1, 2, 99, 100])('contract positive control preserves a valid %i-character name', async (length) => {
    const boundary = agent('boundary', { name: 'n'.repeat(length) })
    expect(AgentSchema.safeParse(boundary).success).toBe(true)
    serve([MIA, boundary, JIM])
    await expect(fetchAgents()).resolves.toEqual([MIA, boundary, JIM])
  })
  it.each([0, 101])('#1076 name boundary %i rejects just that row, not healthy neighbors', async (length) => {
    const broken = agent('broken', { name: 'n'.repeat(length) })
    const rejected = AgentSchema.safeParse(broken)
    expect(rejected.success, 'instrument: generated validator sees the deliberate name defect').toBe(false)
    if (!rejected.success) expect(rejected.error.issues.map((issue) => issue.path)).toContainEqual(['name'])
    serve([MIA, broken, JIM])
    const rows = await fetchAgents()
    healthyRows(rows)
  })
  it.each([null, { id: 'broken', name: 'Missing required fields' }, { ...MIA, id: 'broken', type: 'unrecognized-kind' }])(
    '#1076 malformed row %j leaves the surrounding healthy roster usable', async (broken) => {
      expect(AgentSchema.safeParse(broken).success).toBe(false)
      serve([MIA, broken, JIM])
      healthyRows(await fetchAgents())
    },
  )
  it('#1076 repeated refresh with the same bad name still returns the same healthy agents', async () => {
    serve([MIA, agent('broken', { name: 'n'.repeat(101) }), JIM])
    healthyRows(await fetchAgents())
    healthyRows(await fetchAgents())
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })
  it('#1076 diagnostics name the bad agent and field, not a supposed server-version mismatch', async () => {
    const warnings = vi.spyOn(console, 'warn')
    const errors = vi.spyOn(console, 'error')
    // #1076 / CHECK F3: this id is used by no earlier boundary case, so the
    // page-life report dedup deliberately starts fresh without a helper mock.
    const invalidId = 'broken-warning-delivery'
    serve([MIA, agent(invalidId, { name: 'n'.repeat(101) }), JIM])
    await expect(queryClient.fetchQuery({ queryKey: ['agents', 'wu10-resilience'], queryFn: fetchAgents, retry: false })).resolves.toEqual(expect.arrayContaining([MIA, JIM]))
    // Logging is an explicit issue requirement; recording spies call through.
    await act(async () => { await Promise.resolve() })
    await waitFor(() => {
      const logged = JSON.stringify([...warnings.mock.calls, ...errors.mock.calls])
      expect(logged).toMatch(/broken/)
      expect(logged).toMatch(/name/)
      expect(logged).toMatch(/invalid|validation/i)
    })
    // Positive actual delivery, not a formatter/log or a vacuous empty negative.
    // The real rendered warning is also pinned through AppShell.warning1076.
    await waitFor(() => {
      const delivered = useUiStore.getState().toasts.filter((toast) =>
        toast.variant === 'warning' && toast.message.includes(invalidId))
      expect(delivered).toHaveLength(1)
      expect(delivered[0].message).toContain(invalidId)
      expect(delivered[0].message).toMatch(/\bname\b/)
      expect(delivered[0].message).toMatch(/invalid/i)
    })
    const messages = useUiStore.getState().toasts.map((toast) => toast.message).join('\n')
    expect(messages).not.toMatch(/different version|please refresh/i)
  })
  it.each([null, {}, 'not a list'])('contract negative control refuses malformed top-level response %j', async (body) => {
    serve(body)
    // Characterization of the existing HTTP error envelope only; #1076's
    // independent oracle here is that malformed top-level data is refused.
    await expect(fetchAgents()).rejects.toMatchObject({ name: 'ApiSchemaError', endpoint: 'GET /api/v1/agents' })
    await expect(fetchAgents()).rejects.toBeInstanceOf(ApiSchemaError)
  })
})
