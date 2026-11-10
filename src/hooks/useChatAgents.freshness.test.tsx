// PANEL-AGENT-PICKER-MECHANISM-RED-1929 / issue #1009.
// Oracle: the commission and the unchanged no-reload acceptance test at
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/panel-agent-picker-mechanism-red-20261006/tests/e2e/agent-picker-freshness.spec.ts.
// Unlike workspaceFreshness coverage, discovery is ALREADY POPULATED with
// the old list before REST creation, and no focus/WS signal refreshes it.
// Real boundary: AgentPicker's useChatAgents + useAgentsCrossTabRefresh
// consumption, singleton QueryClient, transport, validators and opening store
// action. Only HTTP is mocked. No query/refetch/invalidation method is mocked.
// UI limit: the actual Radix picker needs ResizeObserver, absent in jsdom;
// this network-only test verifies its real hook result, NOT picker DOM or
// browser reachability. The unchanged E2E must pass first attempt in CI.
// Production mutation sensitivity is deferred to a DIFFERENT qa-lead CHECK.

import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import type { Agent, AgentCreateRequest, Workspace, WorkspaceUpdateRequest } from '@/lib/api/generated/openapi-types'
import {
  Agent as AgentSchema,
  AgentCreateRequest as AgentCreateRequestSchema,
  Workspace as WorkspaceSchema,
  WorkspaceUpdateRequest as WorkspaceUpdateRequestSchema,
} from '@/lib/api/generated/schemas'
import { workspacesQueryKeys } from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { makeAgent } from '@/test/factories'
import { useAgentsCrossTabRefresh } from './useAgentsCrossTabRefresh'
import { useChatAgents } from './useChatAgents'

const ACTIVE_WORKSPACE = 'active-picker-workspace'
const OTHER_WORKSPACE = 'other-picker-workspace'
const workspaceKey = workspacesQueryKeys.list({ status: 'active' })
const previousWorkspaceId = useWorkspacesStore.getState().activeWorkspaceId
const previousPickerOpen = useUiStore.getState().agentSelectorOpen
const oldMember = makeAgent({ id: 'old-member', name: 'Existing member', type: 'Main', status: 'idle' })
const otherMember = makeAgent({ id: 'other-member', name: 'Other workspace member', type: 'Main', status: 'idle' })
const newMember = makeAgent({
  id: 'new-member', name: 'New REST member', type: 'Main', status: 'idle',
  soul: 'Created through the external REST process edge.',
})

let serverAgents: Agent[]
let serverWorkspaces: Workspace[]
let discoveryResponses: string[][]
let requests: string[]
let unexpectedRequests: string[]

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status, headers: { 'Content-Type': 'application/json' },
  })
}

const http = vi.fn<typeof fetch>(async (input, init) => {
  const address = typeof input === 'string' || input instanceof URL ? String(input) : input.url
  const url = new URL(address, window.location.origin)
  const method = init?.method ?? 'GET'
  const signature = `${method} ${url.pathname}${url.search}`
  requests.push(signature)

  if (url.origin === window.location.origin) {
    if (method === 'GET' && url.pathname === '/api/v1/agents' && url.search === '') {
      discoveryResponses.push(serverAgents.map((agent) => agent.id))
      return jsonResponse(serverAgents)
    }
    if (method === 'GET' && url.pathname === '/api/v1/workspaces' && url.search === '?status=active') {
      return jsonResponse(serverWorkspaces)
    }
    if (method === 'POST' && url.pathname === '/api/v1/agents' && url.search === '') {
      const body = AgentCreateRequestSchema.parse(JSON.parse(String(init?.body)))
      const created: Agent = { ...newMember, ...body }
      AgentSchema.parse(created)
      serverAgents = [...serverAgents, created]
      return jsonResponse(created, 201)
    }
    const workspace = serverWorkspaces.find((item) => url.pathname === `/api/v1/workspaces/${item.id}`)
    if (method === 'PUT' && workspace && url.search === '') {
      const body = WorkspaceUpdateRequestSchema.parse(JSON.parse(String(init?.body)))
      const updated: Workspace = {
        ...workspace, ...body, revision: '1'.repeat(64),
        persistence_status: 'complete', activation_status: 'active', changed_fields: ['core_team'],
      }
      serverWorkspaces = serverWorkspaces.map((item) => item.id === workspace.id ? updated : item)
      return jsonResponse(updated)
    }
  }

  unexpectedRequests.push(`${url.origin} ${signature}`)
  throw new Error(`Unexpected HTTP request: ${url.origin} ${signature}`)
})

function QueryBoundary({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
}

function cachedTeams() {
  return queryClient.getQueryData<Workspace[]>(workspaceKey)?.map((workspace) => ({
    id: workspace.id, team: workspace.core_team,
  }))
}

function logBoundary(stage: string, choices: Agent[]) {
  console.info('PANEL-AGENT-PICKER-MECHANISM-EVIDENCE', JSON.stringify({
    stage, requests, discoveryResponses,
    cachedAgents: queryClient.getQueryData<Agent[]>(['agents'])?.map((agent) => agent.id),
    cachedTeams: cachedTeams(), choices: choices.map((agent) => agent.id),
  }))
}

async function mountPopulatedConsumer() {
  // This is the same hook pair the real AgentPicker consumes. The client is
  // never replaced or cleared between initial consumption and picker opening.
  const consumer = renderHook(() => {
    const choices = useChatAgents()
    useAgentsCrossTabRefresh()
    return choices
  }, { wrapper: QueryBoundary })

  await waitFor(() => {
    expect(consumer.result.current.agents.map((agent) => agent.id), 'Initial agents GET must be consumed by the real query')
      .toEqual(['old-member', 'other-member'])
    expect(queryClient.getQueryData<Agent[]>(['agents'])?.map((agent) => agent.id), 'The agents cache must already contain the complete old list')
      .toEqual(['old-member', 'other-member'])
    expect(queryClient.getQueryState(['agents'])?.status, 'The initial agents query must have succeeded').toBe('success')
    expect(queryClient.getQueryState(['agents'])?.fetchStatus, 'Initial discovery must finish before creation').toBe('idle')
    expect(queryClient.getQueryState(workspaceKey)?.fetchStatus, 'The initial membership refresh must finish before external writes').toBe('idle')
    expect(cachedTeams(), 'Both workspaces must have distinct, non-empty cached teams').toEqual([
      { id: ACTIVE_WORKSPACE, team: ['old-member'] },
      { id: OTHER_WORKSPACE, team: ['other-member'] },
    ])
    expect(consumer.result.current.chatAgents.map((agent) => agent.id), 'Positive scope control: retain the old active-workspace member and hide the other-workspace member')
      .toEqual(['old-member'])
  })
  expect(discoveryResponses, 'Exactly one actual agents GET must have populated the cache').toEqual([['old-member', 'other-member']])
  expect(useUiStore.getState().agentSelectorOpen, 'The picker must still be closed before external creation').toBe(false)
  logBoundary('old-list-fetched-consumed-and-cached', consumer.result.current.chatAgents)
  return consumer
}

async function createAndAssign(workspaceId: typeof ACTIVE_WORKSPACE | typeof OTHER_WORKSPACE) {
  // Raw process-edge writes deliberately bypass the UI creation mutation and
  // its callbacks. Neither write touches the query cache or emits a WS frame.
  const creation: AgentCreateRequest = { type: 'Main', name: 'New REST member', soul: 'Created through the external REST process edge.' }
  const created = await fetch('/api/v1/agents', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(creation),
  })
  expect(created.status, 'External REST creation must succeed after initial cache consumption').toBe(201)
  expect(AgentSchema.parse(await created.json()).id, 'REST creation must identify the new Main agent').toBe('new-member')

  // Membership is a SEPARATE REST process edge, matching the acceptance
  // fixture. The expected teams are fixture inputs, not observed hook output.
  const expectedTeam = workspaceId === ACTIVE_WORKSPACE ? ['old-member', 'new-member'] : ['other-member', 'new-member']
  const membership: WorkspaceUpdateRequest = { revision: '0'.repeat(64), core_team: expectedTeam }
  const assigned = await fetch(`/api/v1/workspaces/${workspaceId}`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(membership),
  })
  expect(assigned.status, 'The separate external workspace membership PUT must succeed').toBe(200)
  expect(WorkspaceSchema.parse(await assigned.json()).core_team, 'The PUT response must confirm the explicitly requested team')
    .toEqual(expectedTeam)
  expect(queryClient.getQueryData<Agent[]>(['agents'])?.map((agent) => agent.id), 'REST writes must leave the already populated agents cache outdated until the picker opens')
    .toEqual(['old-member', 'other-member'])
  expect(cachedTeams(), 'External membership writes must not pre-seed a fresh workspace cache').toEqual([
    { id: ACTIVE_WORKSPACE, team: ['old-member'] },
    { id: OTHER_WORKSPACE, team: ['other-member'] },
  ])
  expect(discoveryResponses, 'No focus, visibility or creation callback may refresh discovery before opening')
    .toEqual([['old-member', 'other-member']])
}

beforeEach(() => {
  // Isolation only: clear BEFORE mounting, never to manufacture freshness.
  queryClient.clear()
  http.mockClear()
  requests = []
  unexpectedRequests = []
  discoveryResponses = []
  serverAgents = [oldMember, otherMember]
  serverWorkspaces = [
    { id: ACTIVE_WORKSPACE, name: 'Active picker workspace', core_team: ['old-member'] },
    { id: OTHER_WORKSPACE, name: 'Other picker workspace', core_team: ['other-member'] },
  ].map((workspace) => ({
    ...workspace, revision: '0'.repeat(64), status: 'active', pinned: false, pin_order: 0,
    task_count: 0, created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z',
  }))
  useWorkspacesStore.getState().setActiveWorkspaceId(ACTIVE_WORKSPACE)
  useUiStore.getState().setAgentSelectorOpen(false)
  vi.stubGlobal('fetch', http)
})

afterEach(async () => {
  cleanup()
  await queryClient.cancelQueries()
  queryClient.clear()
  useWorkspacesStore.getState().setActiveWorkspaceId(previousWorkspaceId)
  useUiStore.getState().setAgentSelectorOpen(previousPickerOpen)
  vi.unstubAllGlobals()
  expect(unexpectedRequests, 'Only the finite mocked HTTP process edges may be used').toEqual([])
})

describe('useChatAgents — populated discovery freshness on picker opening (issue #1009)', () => {
  it('shows the REST-created active-workspace member when opening a populated stale picker without reload or focus', async () => {
    const { result } = await mountPopulatedConsumer()
    await createAndAssign(ACTIVE_WORKSPACE)
    logBoundary('separate-rest-writes-complete-cache-still-old', result.current.chatAgents)

    act(() => { useUiStore.getState().setAgentSelectorOpen(true) })
    await waitFor(() => {
      expect(cachedTeams(), 'Picker opening must refresh active membership after the separate PUT').toEqual([
        { id: ACTIVE_WORKSPACE, team: ['old-member', 'new-member'] },
        { id: OTHER_WORKSPACE, team: ['other-member'] },
      ])
      expect(queryClient.getQueryState(workspaceKey)?.fetchStatus, 'The picker-open team refresh must settle').toBe('idle')
    })
    logBoundary('picker-open-team-fresh-before-discovery-assertion', result.current.chatAgents)

    await waitFor(() => {
      expect(result.current.chatAgents.map((agent) => agent.id),
        'STALE-AGENTS: opening a populated picker after REST creation and membership PUT must expose the new member, retain the old member, and exclude the other-workspace member without reload or focus')
        .toEqual(['old-member', 'new-member'])
    })
    expect(discoveryResponses, 'Opening must perform a second actual agents GET, not rely on a pre-seeded or replaced cache')
      .toEqual([['old-member', 'other-member'], ['old-member', 'other-member', 'new-member']])
    expect(queryClient.getQueryData<Agent[]>(['agents'])?.map((agent) => agent.id), 'The original cache must now contain the second HTTP discovery result')
      .toEqual(['old-member', 'other-member', 'new-member'])
    logBoundary('new-active-member-visible-in-real-hook-consumer', result.current.chatAgents)
  })

  it('refetches discovery but excludes the REST-created member assigned only to another workspace', async () => {
    const { result } = await mountPopulatedConsumer()
    await createAndAssign(OTHER_WORKSPACE)
    act(() => { useUiStore.getState().setAgentSelectorOpen(true) })
    await waitFor(() => {
      expect(cachedTeams(), 'The other workspace PUT must not change the active workspace team').toEqual([
        { id: ACTIVE_WORKSPACE, team: ['old-member'] },
        { id: OTHER_WORKSPACE, team: ['other-member', 'new-member'] },
      ])
      expect(queryClient.getQueryState(workspaceKey)?.fetchStatus, 'The picker-open team refresh must settle').toBe('idle')
    })
    logBoundary('picker-open-with-new-member-assigned-elsewhere', result.current.chatAgents)

    await waitFor(() => {
      expect(result.current.agents.map((agent) => agent.id),
        'STALE-AGENTS: picker opening must re-read populated discovery even when the created member belongs only to another workspace')
        .toEqual(['old-member', 'other-member', 'new-member'])
    })
    expect(discoveryResponses, 'Scope control must be checked against the actual refreshed discovery response')
      .toEqual([['old-member', 'other-member'], ['old-member', 'other-member', 'new-member']])
    expect(result.current.chatAgents.map((agent) => agent.id), 'Wrong-workspace negative control: show exactly the old active-workspace member, never either other-workspace member')
      .toEqual(['old-member'])
    logBoundary('fresh-discovery-preserves-active-workspace-scope', result.current.chatAgents)
  })
})
