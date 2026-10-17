import { createElement, type ReactNode } from 'react'
import { act, cleanup, renderHook } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ConfigurationMutationState, Workspace } from '../../src/lib/api/generated/openapi-types'
import { updateWorkspace, fetchWorkspace, workspacesQueryKeys } from '../../src/lib/api/workspaces'
import { ConfigurationSaveError } from '../../src/lib/api/configuration'
import { classifyMutationFailure } from '../../src/lib/mutationOutcome'
import { ApiError } from '../../src/lib/api-error'
import { useTeamMembership } from '../../src/components/workspaces/team/useTeamMembership'

// Positive consumer composition control for NEW-SF-2. The server's real fault
// response is validated separately against this existing generated contract by
// TestRoomsFix4_SavedRoomSyncFailureUsesMutationEnvelope; no client code is mocked.
const before: Workspace = {
  revision: 'a'.repeat(64), id: 'sales', name: 'Sales', status: 'active',
  pinned: false, pin_order: 0, created_at: '2026-10-16T00:00:00Z', updated_at: '2026-10-16T00:00:00Z',
  task_count: 0, core_team: ['mia', 'jim'], persistence_status: 'complete', activation_status: 'active', changed_fields: [],
}
const saved: Workspace = { ...before, revision: 'b'.repeat(64), core_team: ['mia'], changed_fields: ['core_team'] }
const partial: ConfigurationMutationState = {
  revision: saved.revision, persistence_status: 'partial', activation_status: 'not_attempted',
  changed_fields: ['core_team'], error_stage: 'room_sync', message: 'Workspace saved; room synchronization failed. Read current state before retrying.',
}
function response(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}
beforeEach(() => { vi.spyOn(document, 'cookie', 'get').mockReturnValue('__Host-csrf=test') })
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('saved membership with failed room synchronization', () => {
  it('recognizes the generated partial state rather than calling it a refusal', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response(partial, 500)))
    let failure: unknown
    try { await updateWorkspace('sales', { revision: before.revision, core_team: ['mia'] }) } catch (error) { failure = error }
    expect(failure).toBeInstanceOf(ConfigurationSaveError)
    expect((failure as ConfigurationSaveError).state).toEqual(partial)
    expect(classifyMutationFailure(failure)).toBe('uncertain')
  })

  it('control: legacy ErrorResponse reproduces the refusal that the backend fix must remove', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response({ error: 'Workspace changes were saved but rooms failed' }, 500)))
    let failure: unknown
    try { await updateWorkspace('sales', { revision: before.revision, core_team: ['mia'] }) } catch (error) { failure = error }
    expect(failure).toBeInstanceOf(ApiError)
    expect(failure).not.toBeInstanceOf(ConfigurationSaveError)
    expect(classifyMutationFailure(failure)).toBe('refused')
  })

  it('Team membership invalidates saved state and reads the current revision before Retry', async () => {
    const calls: Array<{ method: string; url: string; body: unknown }> = [] // not-wire-format: local HTTP observation receipt
    let reads = 0
    let writes = 0
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      calls.push({ method, url: String(input), body: init?.body ? JSON.parse(String(init.body)) : null })
      if (method === 'GET') return response(reads++ === 0 ? before : saved)
      return writes++ === 0 ? response(partial, 500) : response(saved)
    }))
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const loaded = await client.fetchQuery({ queryKey: workspacesQueryKeys.detail('sales'), queryFn: () => fetchWorkspace('sales') })
    expect(loaded).toEqual(before)
    const wrapper = ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client, children })
    const { result, unmount } = renderHook(() => useTeamMembership(loaded), { wrapper })
    await act(async () => { await result.current.remove('jim', 'Jim') })
    expect(result.current.status).toEqual({ kind: 'uncertain', action: 'remove', agentId: 'jim', agentName: 'Jim' })
    expect(client.getQueryState(workspacesQueryKeys.detail('sales'))?.isInvalidated).toBe(true)
    await act(async () => { await result.current.retry() })
    expect(result.current.status).toEqual({ kind: 'idle' })
    expect(client.getQueryData(workspacesQueryKeys.detail('sales'))).toEqual(saved)
    expect(calls).toEqual([
      { method: 'GET', url: '/api/v1/workspaces/sales', body: null },
      { method: 'PUT', url: '/api/v1/workspaces/sales', body: { revision: before.revision, core_team: ['mia'] } },
      { method: 'GET', url: '/api/v1/workspaces/sales', body: null },
      { method: 'PUT', url: '/api/v1/workspaces/sales', body: { revision: saved.revision, core_team: ['mia'] } },
    ])
    unmount()
    client.clear()
  })
})
