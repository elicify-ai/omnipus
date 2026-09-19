import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { deleteWorkspace, updateWorkspaceInstructions } from './workspaces'
import { ConfigurationSaveError } from './configuration'
import { ApiSchemaError } from './http'

const revision = 'c'.repeat(64)
const fetchMock = vi.fn()
const state = {
  revision,
  persistence_status: 'complete' as const,
  activation_status: 'active' as const,
  changed_fields: ['workspace'],
}

beforeEach(() => {
  fetchMock.mockReset()
  vi.stubGlobal('fetch', fetchMock)
  vi.spyOn(document, 'cookie', 'get').mockReturnValue('__Host-csrf=test')
})
afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('workspace configuration write outcomes', () => {
  it('returns the generated deletion state only after complete active deletion', async () => {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(state), { status: 200 }))
    await expect(deleteWorkspace('ws-1', revision)).resolves.toEqual(state)
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe(`/api/v1/workspaces/ws-1?revision=${revision}`)
    expect(init.method).toBe('DELETE')
  })

  it('preserves a partial workspace delete instead of treating HTTP 500 as a missing envelope', async () => {
    const partial = {
      ...state,
      persistence_status: 'partial',
      activation_status: 'not_attempted',
      error_stage: 'remove_directory',
      message: 'directory remains',
    }
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(partial), { status: 500 }))
    await expect(deleteWorkspace('ws-1', revision)).rejects.toMatchObject({
      name: 'ConfigurationSaveError',
      state: partial,
      message: 'Some changes were saved, but configuration is incomplete. Reload before making further changes.',
    })
  })

  it('sends the reviewed instructions revision and does not certify a 409 as saved', async () => {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ error: 'conflict' }), { status: 409 }))
    await expect(updateWorkspaceInstructions('ws-1', 'next', revision)).rejects.toMatchObject({ status: 409 })
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/v1/workspaces/ws-1/instructions')
    expect(JSON.parse(init.body)).toEqual({ content: 'next', revision })
  })

  it('returns instructions mutation state only when persistence is complete and active', async () => {
    const saved = { ...state, changed_fields: ['instructions'] }
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(saved), { status: 200 }))
    await expect(updateWorkspaceInstructions('ws-1', 'next', revision)).resolves.toEqual(saved)
  })

  it('does not certify success when delete HTTP 200 omits the envelope', async () => {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({}), { status: 200 }))
    await expect(deleteWorkspace('ws-1', revision)).rejects.toBeInstanceOf(ApiSchemaError)
  })

  it('surfaces a complete-but-inactive instructions save', async () => {
    const inactive = { ...state, activation_status: 'failed', changed_fields: ['instructions'] }
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(inactive), { status: 200 }))
    await expect(updateWorkspaceInstructions('ws-1', 'next', revision)).rejects.toBeInstanceOf(ConfigurationSaveError)
  })
})
