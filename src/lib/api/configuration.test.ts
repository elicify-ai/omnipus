import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { updateAgent, fetchAgent, createAgent } from './agents'
import { createWorkspace, updateWorkspace, updateWorkspaceDelegation } from './workspaces'
import { updateAgentTools } from './tools'
import { installSkillBySlug } from './skills'
import { ApiSchemaError } from './http'
import { ApiError } from '../api-error'

const revision = 'a'.repeat(64)
const agent = {
  revision, id: 'mia', name: 'Mia', type: 'core', locked: true,
  needs_model: false, model: 'test-model', status: 'active', soul: 'fixed',
  timeout_seconds: 60, max_tool_iterations: 20, memory_enabled: true,
}
const state = { revision, persistence_status: 'complete', activation_status: 'active', changed_fields: ['model'] }
const fetchMock = vi.fn()
function response(body: unknown, status = 200) {
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
}
beforeEach(() => {
  fetchMock.mockReset()
  vi.stubGlobal('fetch', fetchMock)
  vi.spyOn(document, 'cookie', 'get').mockReturnValue('__Host-csrf=test')
})
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('ADR090 configuration save outcomes', () => {
  it('returns the resource only after a complete active save', async () => {
    response({ ...agent, ...state })
    await expect(updateAgent('mia', { revision, model: 'test-model' })).resolves.toEqual({ ...agent, ...state })
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/v1/agents/mia')
    expect(JSON.parse(init.body)).toEqual({ revision, model: 'test-model' })
  })
  it.each([
    ['complete', 'failed'], ['complete', 'not_attempted'],
    ['partial', 'failed'], ['none', 'not_attempted'],
    ['partial', 'active'], ['none', 'active'],
  ])('rejects %s persistence with %s activation without retrying', async (persistence, activation) => {
    const actualState = { ...state, persistence_status: persistence, activation_status: activation }
    response({ ...agent, ...actualState })
    await expect(updateAgent('mia', { revision, model: 'test-model' })).rejects.toMatchObject({
      name: 'ConfigurationSaveError', state: actualState,
    })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
  it('does not certify success when state fields are absent', async () => {
    response(agent)
    await expect(updateAgent('mia', { revision, model: 'test-model' })).rejects.toBeInstanceOf(ApiSchemaError)
  })
  it('preserves the actual state of a partial storage failure', async () => {
    const actualState = { ...state, persistence_status: 'partial', activation_status: 'not_attempted', error_stage: 'soul', message: 'Could not save instructions.' }
    response(actualState, 500)
    await expect(updateAgent('mia', { revision, model: 'test-model' })).rejects.toMatchObject({
      name: 'ConfigurationSaveError', state: actualState,
      message: 'Some changes were saved, but configuration is incomplete. Reload before making further changes.',
    })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
  it.each([
    { ...state, revision: 'invalid' },
    { ...state, changed_fields: null },
    { ...state, activation_status: 'unknown' },
  ])('rejects malformed mutation state %j', async (invalid) => {
    response({ ...agent, ...invalid })
    await expect(updateAgent('mia', { revision, model: 'test-model' })).rejects.toBeInstanceOf(ApiSchemaError)
  })
  it('keeps malformed storage failures as API errors', async () => {
    response({ error: 'storage unavailable' }, 500)
    await expect(updateAgent('mia', { revision, model: 'test-model' })).rejects.toBeInstanceOf(ApiError)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
  it('describes saved but inactive changes without claiming success', async () => {
    response({ ...agent, ...state, activation_status: 'failed' })
    await expect(updateAgent('mia', { revision, model: 'test-model' })).rejects.toMatchObject({
      name: 'ConfigurationSaveError',
      message: 'Changes were saved but are not active. Reload before making further changes.',
    })
  })
  it('keeps a conflict as an API error and never retries it', async () => {
    response({ error: 'revision conflict' }, 409)
    await expect(updateAgent('mia', { revision, model: 'test-model' })).rejects.toBeInstanceOf(ApiError)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
  it('continues to read resources without mutation outcome fields', async () => {
    response(agent)
    await expect(fetchAgent('mia')).resolves.toEqual(agent)
  })
})

const workspaceResource = {
  revision, id: 'ws', name: 'Team', status: 'active', pinned: false,
  pin_order: 0, task_count: 0, created_at: '2026-09-17T00:00:00Z', updated_at: '2026-09-17T00:00:00Z',
}
const mutationCases = [
  { name: 'agent creation', resource: agent, save: () => createAgent({ type: 'Main', name: 'New', soul: 'Instructions' }) },
  { name: 'workspace creation', resource: workspaceResource, save: () => createWorkspace({ name: 'Team' }) },
  { name: 'workspace update', resource: workspaceResource, save: () => updateWorkspace('ws', { revision, description: 'Updated' }) },
  { name: 'graph update', resource: { revision, workspace_id: 'ws', edges: [], default_depth: 3 }, save: () => updateWorkspaceDelegation('ws', { revision, edges: [] }) },
  { name: 'tool update', resource: { revision, override_names: [], config: { builtin: { policies: { bash: 'allow' } } }, tools: [] }, save: () => updateAgentTools('mia', { revision, override_names: [], config: { builtin: { policies: { bash: 'allow' } } } }) },
  { name: 'skill installation', resource: { revision, id: 'skill', name: 'skill', description: 'Test skill', version: '1', author: 'Elicify', verified: false, status: 'active' }, save: () => installSkillBySlug('skill') },
]

describe.each(mutationCases)('$name state handling', ({ resource, save }) => {
  it('returns an active complete resource', async () => {
    response({ ...resource, ...state })
    await expect(save()).resolves.toEqual({ ...resource, ...state })
  })
  it('cannot report a saved but inactive configuration as successful', async () => {
    response({ ...resource, ...state, activation_status: 'failed' })
    await expect(save()).rejects.toMatchObject({ name: 'ConfigurationSaveError' })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})
