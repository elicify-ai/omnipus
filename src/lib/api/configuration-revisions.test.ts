import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { deleteAgent } from './agents'
import { deleteWorkspace, updateWorkspaceDelegation } from './workspaces'
import { deleteSkill } from './skills'
import { updateAgentTools } from './tools'

const revision = 'b'.repeat(64)
const fetchMock = vi.fn()
beforeEach(() => {
  fetchMock.mockReset()
  vi.stubGlobal('fetch', fetchMock)
  vi.spyOn(document, 'cookie', 'get').mockReturnValue('__Host-csrf=test')
})
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

// ADR090 FR007: submit the revision the user reviewed, without a fresh read
// that would silently authorize deletion of someone else's newer changes.
describe('configuration revision transport', () => {
  it.each([
    ['agent', deleteAgent, '/api/v1/agents/a%2Fb', true],
    ['workspace', deleteWorkspace, '/api/v1/workspaces/a%2Fb', false],
    ['skill', deleteSkill, '/api/v1/skills/a%2Fb', false],
  ] as const)('sends the reviewed revision when deleting a %s', async (_kind, remove, path, returnsState) => {
    const body = returnsState
      ? JSON.stringify({ revision, persistence_status: 'complete', activation_status: 'active', changed_fields: ['agents.a/b'] })
      : null
    fetchMock.mockResolvedValueOnce(new Response(body, { status: returnsState ? 200 : 204 }))
    await remove('a/b', revision)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe(`${path}?revision=${revision}`)
    expect(init.method).toBe('DELETE')
  })
  it('sends an explicit empty graph with its reviewed revision', async () => {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ error: 'conflict' }), { status: 409 }))
    await expect(updateWorkspaceDelegation('ws', { revision, edges: [] })).rejects.toMatchObject({ status: 409 })
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/v1/workspaces/ws/delegation')
    expect(JSON.parse(init.body)).toEqual({ revision, edges: [] })
  })
  it('sends tool override intent separately from effective policy readback', async () => {
    const payload = { revision, override_names: [], config: { builtin: { policies: { bash: 'allow' as const } } } }
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ error: 'conflict' }), { status: 409 }))
    await expect(updateAgentTools('mia', payload, 'consent')).rejects.toMatchObject({ status: 409 })
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/v1/agents/mia/tools')
    expect(JSON.parse(init.body)).toEqual(payload)
    expect(init.headers['X-Reauth-Token']).toBe('consent')
  })
})
