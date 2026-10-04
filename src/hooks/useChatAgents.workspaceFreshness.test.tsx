// Issue #1009 regression: refreshing agents is insufficient when a separate
// raw REST PUT adds the new agent to the active workspace's core_team.
// Oracle: the dispatch's picker-freshness requirements and section "2. Picker"
// of /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1090/receipts/ci-e2e-failures-1115.md.
// Both hooks, the production QueryClient, workspace store, API transport and
// generated response validators are real. Only HTTP is mocked. Focus and the
// agent-picker open flag are the production signals, not a manual query refetch.

import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, renderHook, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import type { Agent, Workspace, WorkspaceUpdateRequest } from '@/lib/api'
import { workspacesQueryKeys } from '@/lib/api'
import {
  Workspace as WorkspaceSchema,
  WorkspaceUpdateRequest as WorkspaceUpdateRequestSchema,
} from '@/lib/api/generated/schemas'
import { queryClient } from '@/lib/queryClient'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { makeAgent } from '@/test/factories'
import { useAgentsCrossTabRefresh } from './useAgentsCrossTabRefresh'
import { useChatAgents } from './useChatAgents'

const WORKSPACE_ID = 'picker-workspace'
const workspaceListKey = workspacesQueryKeys.list({ status: 'active' })
const initialActiveWorkspaceId = useWorkspacesStore.getState().activeWorkspaceId
const existingMember = makeAgent({ id: 'existing-member', type: 'Main', status: 'idle' })
const newMember = makeAgent({ id: 'new-member', type: 'Main', status: 'idle' })
const outsider = makeAgent({ id: 'outsider', type: 'Main', status: 'idle' })

let serverAgents: Agent[]
let serverWorkspace: Workspace

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

const http = vi.fn<typeof fetch>(async (input, init) => {
  const address = typeof input === 'string' || input instanceof URL ? String(input) : input.url
  const url = new URL(address, window.location.origin)
  const method = init?.method ?? 'GET'

  if (method === 'GET' && url.pathname === '/api/v1/agents') {
    return jsonResponse(serverAgents)
  }
  if (method === 'GET' && url.pathname === '/api/v1/workspaces' && url.search === '?status=active') {
    return jsonResponse([serverWorkspace])
  }
  if (method === 'PUT' && url.pathname === `/api/v1/workspaces/${WORKSPACE_ID}`) {
    const body = WorkspaceUpdateRequestSchema.parse(JSON.parse(String(init?.body)))
    serverWorkspace = {
      ...serverWorkspace,
      core_team: body.core_team,
      revision: '1'.repeat(64),
      persistence_status: 'complete',
      activation_status: 'active',
      changed_fields: ['core_team'],
    }
    return jsonResponse(serverWorkspace)
  }
  throw new Error(`Unexpected HTTP request: ${method} ${url.pathname}${url.search}`)
})

function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
}

function mountPickerHooks() {
  return renderHook(() => {
    const agents = useChatAgents()
    useAgentsCrossTabRefresh()
    return agents
  }, { wrapper: Wrapper })
}

beforeEach(() => {
  queryClient.clear()
  http.mockClear()
  serverAgents = [existingMember, outsider]
  // A non-empty team is deliberate: the hook's empty-team fallback must not
  // accidentally make this freshness regression pass by showing every agent.
  serverWorkspace = {
    id: WORKSPACE_ID,
    name: 'Picker workspace',
    revision: '0'.repeat(64),
    status: 'active',
    pinned: false,
    pin_order: 0,
    core_team: ['existing-member'],
    task_count: 0,
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-01T00:00:00Z',
  }
  useWorkspacesStore.getState().setActiveWorkspaceId(WORKSPACE_ID)
  vi.stubGlobal('fetch', http)
})

afterEach(() => {
  // Unmount before restoring HTTP/store state so observers cannot leak work
  // into the next case, including when the regression assertion fails.
  cleanup()
  queryClient.clear()
  useWorkspacesStore.getState().setActiveWorkspaceId(initialActiveWorkspaceId)
  useUiStore.getState().setAgentSelectorOpen(false)
  vi.unstubAllGlobals()
})

describe('useChatAgents — workspace membership freshness (issue #1009)', () => {
  it('shows a ready Main agent already in the cached core_team', async () => {
    const { result } = mountPickerHooks()

    await waitFor(() => {
      expect(result.current.agents.map((agent) => agent.id), 'Both ready Main agents must load through real HTTP parsing')
        .toEqual(['existing-member', 'outsider'])
      expect(result.current.chatAgents.map((agent) => agent.id), 'The existing team member must be chat-selectable')
        .toEqual(['existing-member'])
    })
  })

  it('keeps a ready Main agent outside core_team hidden even though the agents list contains it', async () => {
    const { result } = mountPickerHooks()

    await waitFor(() => {
      expect(queryClient.getQueryData<Workspace[]>(workspaceListKey)?.map((workspace) => workspace.core_team), 'The real workspace query must load a non-empty team')
        .toEqual([['existing-member']])
      expect(result.current.agents.map((agent) => agent.id), 'The outsider must exist in the unfiltered agents response')
        .toEqual(['existing-member', 'outsider'])
      expect(result.current.chatAgents.map((agent) => agent.id), 'Team scoping must exclude the outsider, not hide a missing HTTP result')
        .toEqual(['existing-member'])
    })
  })

  it('includes a newly added workspace member on return focus after the agents list has already refreshed, without reload or manual refetch', async () => {
    const { result } = mountPickerHooks()

    await waitFor(() => {
      expect(result.current.agents.map((agent) => agent.id), 'Initial HTTP agents list must load before creation')
        .toEqual(['existing-member', 'outsider'])
      expect(queryClient.getQueryData<Workspace[]>(workspaceListKey)?.map((workspace) => workspace.core_team), 'Initial cached workspace must contain only the existing member')
        .toEqual([['existing-member']])
      expect(result.current.chatAgents.map((agent) => agent.id), 'Initial picker must show exactly the existing team member')
        .toEqual(['existing-member'])
    })

    // Match the failing trace's order: agent discovery succeeds BEFORE the
    // separate membership PUT. Drive the production focus listener, never
    // invalidateQueries/refetch/setQueryData from the test itself.
    serverAgents = [existingMember, newMember, outsider]
    act(() => { fireEvent(window, new Event('focus')) })
    await waitFor(() => {
      expect(result.current.agents.map((agent) => agent.id), 'Agent discovery must already contain the new ready Main agent')
        .toEqual(['existing-member', 'new-member', 'outsider'])
      expect(queryClient.getQueryState(['agents'])?.fetchStatus, 'Agents refresh must finish before the membership PUT').toBe('idle')
      expect(queryClient.getQueryData<Workspace[]>(workspaceListKey)?.map((workspace) => workspace.core_team), 'Membership has not changed yet')
        .toEqual([['existing-member']])
      expect(result.current.chatAgents.map((agent) => agent.id), 'A created agent is not a workspace member until the separate PUT')
        .toEqual(['existing-member'])
    })

    // A raw HTTP writer (another tab or the E2E fixture) bypasses the UI's
    // mutation callbacks. The successful PUT must not require a page reload
    // to become visible when the user returns to the picker.
    const membershipUpdate: WorkspaceUpdateRequest = {
      revision: serverWorkspace.revision,
      core_team: ['existing-member', 'new-member'],
    }
    const saved = await fetch(`/api/v1/workspaces/${WORKSPACE_ID}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(membershipUpdate),
    })
    expect(saved.status, 'The external membership PUT must succeed before checking picker freshness').toBe(200)
    expect(WorkspaceSchema.parse(await saved.json()).core_team, 'The successful PUT response must confirm the new membership')
      .toEqual(['existing-member', 'new-member'])

    act(() => { fireEvent(window, new Event('focus')) })

    await waitFor(() => {
      expect(result.current.chatAgents.map((agent) => agent.id),
        'Successful workspace membership PUT + return focus must refresh the cached core_team: include the new member, keep the outsider hidden, and require neither reload nor manual refetch')
        .toEqual(['existing-member', 'new-member'])
    })
  })

  it('includes a newly added workspace member when the picker opens after the agents list has already refreshed, without a second focus or reload', async () => {
    const { result } = mountPickerHooks()

    await waitFor(() => {
      expect(result.current.chatAgents.map((agent) => agent.id), 'Initial picker must show exactly the existing team member')
        .toEqual(['existing-member'])
    })

    // Same order as agent-picker-freshness.spec.ts: agent_created refreshes
    // the agents list (here, the focus listener) BEFORE the separate
    // membership PUT. The page stays focused, so nothing focuses it again.
    serverAgents = [existingMember, newMember, outsider]
    act(() => { fireEvent(window, new Event('focus')) })
    await waitFor(() => {
      expect(result.current.agents.map((agent) => agent.id), 'Agent discovery must already contain the new ready Main agent')
        .toEqual(['existing-member', 'new-member', 'outsider'])
      expect(queryClient.getQueryState(['agents'])?.fetchStatus, 'Agents refresh must finish before the membership PUT').toBe('idle')
      expect(queryClient.getQueryState(workspaceListKey)?.fetchStatus, 'The too-early team refresh must finish before the membership PUT').toBe('idle')
      expect(result.current.chatAgents.map((agent) => agent.id), 'A created agent is not a workspace member until the separate PUT')
        .toEqual(['existing-member'])
    })

    const membershipUpdate: WorkspaceUpdateRequest = {
      revision: serverWorkspace.revision,
      core_team: ['existing-member', 'new-member'],
    }
    const saved = await fetch(`/api/v1/workspaces/${WORKSPACE_ID}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(membershipUpdate),
    })
    expect(saved.status, 'The external membership PUT must succeed before the picker opens').toBe(200)
    expect(WorkspaceSchema.parse(await saved.json()).core_team, 'The successful PUT response must confirm the new membership')
      .toEqual(['existing-member', 'new-member'])
    expect(result.current.chatAgents.map((agent) => agent.id), 'The PUT alone must not be treated as already visible; the picker has not been opened')
      .toEqual(['existing-member'])

    act(() => { useUiStore.getState().setAgentSelectorOpen(true) })

    await waitFor(() => {
      expect(result.current.chatAgents.map((agent) => agent.id),
        'Opening the agent picker after the membership PUT must refresh the cached core_team: include the new member, keep the outsider hidden, and require neither a second focus nor a reload')
        .toEqual(['existing-member', 'new-member'])
    })
  })
})
