// PANEL-PICKER-CACHED-ERROR-RED-2153 / frozen61f silent-failure-hunter HF3.
// Oracle: the binding brief and HF3, not the renderer's current output:
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/recovery-20261002/brief-panel-picker-cached-error-red.txt
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/recovery-20261002/review-dispatch-61f-20261006/gate-silent-failure-hunter-gpt.result.json
// REAL: AgentPicker DOM, Radix wrappers, both hooks, original singleton client,
// stores, transport and generated validators. Only HTTP and (if absent) the
// browser ResizeObserver edge are substituted. No query/refetch/invalidation
// mocks, cache pre-seeding, focus/visibility signal, WS frame or page reload.
// New copy is semantic, not an invented founder-approved exact sentence.
// GREEN, implementation mutation, independent CHECK and browser acceptance
// are deferred; jsdom DOM assertions are not feature-reachability certification.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import type {
  Agent, AgentCreateRequest, ErrorResponse, Workspace, WorkspaceUpdateRequest,
} from '@/lib/api/generated/openapi-types'
import {
  Agent as AgentSchema,
  AgentCreateRequest as AgentCreateRequestSchema,
  ErrorResponse as ErrorResponseSchema,
  Workspace as WorkspaceSchema,
  WorkspaceUpdateRequest as WorkspaceUpdateRequestSchema,
} from '@/lib/api/generated/schemas'
import { ApiError, workspacesQueryKeys } from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { makeAgent } from '@/test/factories'
import { AgentPicker } from './AgentPicker'

const ACTIVE_WORKSPACE = 'hf3-active-workspace'
const OTHER_WORKSPACE = 'hf3-other-workspace'
const workspaceKey = workspacesQueryKeys.list({ status: 'active' })
const RAW_DIAGNOSTIC = 'HF3_SYNTHETIC_PRIVATE_DIAGNOSTIC_DO_NOT_DISPLAY'
// The unmodified singleton retries ordinary failures after 1s + 2s + 4s.
// This observes the final error, not the first attempt still being retried.
const EXHAUSTED_RETRY_WAIT = { timeout: 10_000 }
const originalSession = useSessionStore.getState()
const originalWorkspace = useWorkspacesStore.getState().activeWorkspaceId
const originalPickerOpen = useUiStore.getState().agentSelectorOpen
const originalDefaults = queryClient.getDefaultOptions()
const oldMember = makeAgent({ id: 'hf3-old', name: 'Existing cached member', type: 'Main', status: 'idle' })
const otherMember = makeAgent({ id: 'hf3-other', name: 'Other workspace member', type: 'Main', status: 'idle' })
const newMember = makeAgent({
  id: 'hf3-new', name: 'New external member', type: 'Main', status: 'idle',
  soul: 'Created through an external REST operation.',
})
const fixtureAgents = [oldMember, otherMember, newMember]

// not-wire-format: fault selection belongs only to the finite HTTP test fake.
type DiscoveryFault = 'network' | 'http500'
let serverAgents: Agent[]
let serverWorkspaces: Workspace[]
let discoveryFault: DiscoveryFault | null
let teamFault: boolean
let requests: string[]
let discoveryResults: string[][]
let discoveryFailures: number[]
let unexpectedRequests: string[]
let browserSignals: string[]
let pendingRetryRequests: number
let heldRetry: ReturnType<typeof holdResponse> | null

function holdResponse() {
  let resolve!: (response: Response) => void
  const response = new Promise<Response>((release) => { resolve = release })
  return { response, resolve }
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function failedHttpResponse() {
  const body: ErrorResponse = { error: RAW_DIAGNOSTIC }
  return jsonResponse(ErrorResponseSchema.parse(body), 500)
}

const http = vi.fn<typeof fetch>(async (input, init) => {
  const address = typeof input === 'string' || input instanceof URL ? String(input) : input.url
  const url = new URL(address, window.location.origin)
  const method = init?.method ?? 'GET'
  const signature = `${method} ${url.pathname}${url.search}`
  requests.push(signature)
  if (url.origin === window.location.origin) {
    if (method === 'GET' && url.pathname === '/api/v1/agents' && url.search === '') {
      if (heldRetry) {
        pendingRetryRequests++
        const response = await heldRetry.response
        discoveryResults.push(serverAgents.map((agent) => agent.id))
        return response
      }
      if (discoveryFault) {
        discoveryFailures.push(discoveryFault === 'network' ? 0 : 500)
        if (discoveryFault === 'network') throw new TypeError(RAW_DIAGNOSTIC)
        return failedHttpResponse()
      }
      discoveryResults.push(serverAgents.map((agent) => agent.id))
      return jsonResponse(serverAgents)
    }
    if (method === 'GET' && url.pathname === '/api/v1/workspaces' && url.search === '?status=active') {
      return teamFault ? failedHttpResponse() : jsonResponse(serverWorkspaces)
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
        ...workspace, ...body, revision: '1'.repeat(64), persistence_status: 'complete',
        activation_status: 'active', changed_fields: ['core_team'],
      }
      WorkspaceSchema.parse(updated)
      serverWorkspaces = serverWorkspaces.map((item) => item.id === workspace.id ? updated : item)
      return jsonResponse(updated)
    }
  }
  unexpectedRequests.push(`${url.origin} ${signature}`)
  throw new Error(`Unexpected HTTP process edge: ${url.origin} ${signature}`)
})

function cachedTeams() {
  return queryClient.getQueryData<Workspace[]>(workspaceKey)?.map((workspace) => ({
    id: workspace.id, team: workspace.core_team,
  }))
}

function pickerChoices() {
  // Read the real menu, not the trigger (which names the old agent even if
  // the menu is broken). Unknown/extra menu choices cannot silently pass.
  const menu = screen.getByRole('menu')
  return within(menu).getAllByRole('menuitem').filter((item) => !/\bretry\b/i.test(item.textContent ?? '')).map((item) => {
    const member = fixtureAgents.find((agent) => within(item).queryByText(agent.name, { exact: true }))
    expect(member, 'Every visible agent choice must be one of the finite HTTP fixture agents').not.toBeUndefined()
    return member?.id
  })
}

function discoveryNotices() {
  // Allow equivalent safe phrasing and standard accessible notice roles,
  // without prescribing an unapproved full sentence or a new testid.
  return ['note', 'status', 'alert'].flatMap((role) => screen.queryAllByRole(role)).filter((node) =>
    /\b(?:last[ -]+known|cached|previously loaded)\s+(?:agents|agent list)\b/i.test(node.textContent ?? ''),
  )
}

function requireDiscoveryNotice() {
  const notices = discoveryNotices()
  expect(notices,
    'HF3: populated cached agents with FAILED discovery and SUCCESSFUL team refresh need an accessible AGENTS-specific last-known reason, not an authoritative-looking old list')
    .toHaveLength(1)
  const notice = notices[0]
  expect(notice, 'The discovery reason must remain visible beside the usable cached picker').toBeVisible()
  expect(notice, 'The notice must explain that discovery could not refresh').toHaveTextContent(/\b(?:could not|couldn't|failed|unable|unavailable|cannot)\b/i)
  expect(notice, 'The failed operation must be discovery/loading/refreshing agents').toHaveTextContent(/\b(?:refresh|load|fetch|discovery)\b/i)
  expect(notice, 'A safe notice must never expose raw response text or transport details').not.toHaveTextContent(RAW_DIAGNOSTIC)
  return notice
}

function evidence(stage: string) {
  console.info('PANEL-PICKER-CACHED-ERROR-EVIDENCE', JSON.stringify({
    stage, requests, discoveryResults, discoveryFailures, pendingRetryRequests, browserSignals,
    cachedAgents: queryClient.getQueryData<Agent[]>(['agents'])?.map((agent) => agent.id),
    cachedTeams: cachedTeams(), visibleChoices: screen.queryByRole('menu') ? pickerChoices() : [],
    discoveryNoticeCount: discoveryNotices().length,
  }))
}

async function mountOldCache() {
  const user = userEvent.setup()
  render(<QueryClientProvider client={queryClient}><AgentPicker /></QueryClientProvider>)
  const trigger = screen.getByTestId('agent-picker-trigger')
  await waitFor(() => {
    expect(trigger, 'The real picker must consume and render the HTTP-fetched old member').toHaveAccessibleName('Select agent (current: Existing cached member)')
    expect(queryClient.getQueryData<Agent[]>(['agents'])?.map((agent) => agent.id)).toEqual(['hf3-old', 'hf3-other'])
    expect(queryClient.getQueryState(['agents'])?.status).toBe('success')
    expect(queryClient.getQueryState(['agents'])?.fetchStatus).toBe('idle')
    expect(queryClient.getQueryState(workspaceKey)?.fetchStatus).toBe('idle')
    expect(cachedTeams()).toEqual([
      { id: ACTIVE_WORKSPACE, team: ['hf3-old'] }, { id: OTHER_WORKSPACE, team: ['hf3-other'] },
    ])
  })
  expect(discoveryResults, 'Exactly one real agents GET must fill the original singleton cache').toEqual([['hf3-old', 'hf3-other']])
  expect(screen.queryByRole('menu'), 'The picker must be closed before external creation').not.toBeInTheDocument()
  expect(useUiStore.getState().agentSelectorOpen).toBe(false)
  return { user, trigger, originalCache: queryClient.getQueryData<Agent[]>(['agents']) }
}

async function createAndAssignExternally() {
  // Raw REST writes deliberately bypass UI mutations and their invalidation
  // callbacks. Neither request writes the client cache or emits a WS frame.
  const creation: AgentCreateRequest = { type: 'Main', name: newMember.name, soul: newMember.soul }
  const created = await fetch('/api/v1/agents', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(creation),
  })
  expect(created.status, 'The external REST agent creation must succeed').toBe(201)
  expect(AgentSchema.parse(await created.json()).id).toBe('hf3-new')
  const membership: WorkspaceUpdateRequest = { revision: '0'.repeat(64), core_team: ['hf3-old', 'hf3-new'] }
  const assigned = await fetch(`/api/v1/workspaces/${ACTIVE_WORKSPACE}`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(membership),
  })
  expect(assigned.status, 'The separate external membership PUT must succeed').toBe(200)
  expect(WorkspaceSchema.parse(await assigned.json()).core_team).toEqual(['hf3-old', 'hf3-new'])
  expect(queryClient.getQueryData<Agent[]>(['agents'])?.map((agent) => agent.id), 'The cache must still be populated with the OLD list').toEqual(['hf3-old', 'hf3-other'])
  expect(cachedTeams(), 'External PUT must not pre-seed fresh membership in the client').toEqual([
    { id: ACTIVE_WORKSPACE, team: ['hf3-old'] }, { id: OTHER_WORKSPACE, team: ['hf3-other'] },
  ])
  expect(discoveryResults, 'No discovery request may precede the actual picker-open action').toEqual([['hf3-old', 'hf3-other']])
  expect(browserSignals, 'No window focus or visibility recovery may hide the original defect').toEqual([])
}

async function openWithKeyboard(user: ReturnType<typeof userEvent.setup>, trigger: HTMLElement) {
  await user.tab()
  expect(trigger, 'The real picker trigger must be keyboard-reachable').toHaveFocus()
  await user.keyboard('{Enter}')
  expect(await screen.findByRole('menu'), 'The real Radix picker must mount, not a hook-only stand-in').toBeVisible()
  expect(useUiStore.getState().agentSelectorOpen).toBe(true)
}

async function failedDiscoveryWithSuccessfulTeam(fault: DiscoveryFault) {
  const consumer = await mountOldCache()
  await createAndAssignExternally()
  discoveryFault = fault
  await openWithKeyboard(consumer.user, consumer.trigger)
  await waitFor(() => {
    expect(queryClient.getQueryState(['agents'])?.status, 'The real agents query must reach a final error').toBe('error')
    expect(queryClient.getQueryState(['agents'])?.fetchStatus).toBe('idle')
    expect(queryClient.getQueryState(workspaceKey)?.status, 'The distinct team query must SUCCEED').toBe('success')
    expect(queryClient.getQueryState(workspaceKey)?.fetchStatus).toBe('idle')
    expect(cachedTeams(), 'Membership GET must consume the new member after the separate PUT').toEqual([
      { id: ACTIVE_WORKSPACE, team: ['hf3-old', 'hf3-new'] }, { id: OTHER_WORKSPACE, team: ['hf3-other'] },
    ])
  }, EXHAUSTED_RETRY_WAIT)
  const error = queryClient.getQueryState(['agents'])?.error
  expect(error, 'This must be an ordinary API failure, never a malformed-fixture/schema error').toBeInstanceOf(ApiError)
  if (!(error instanceof ApiError)) throw new Error('Expected a typed ordinary discovery failure')
  expect(error.status).toBe(fault === 'network' ? 0 : 500)
  expect(discoveryFailures, 'The original singleton retry policy must remain in effect').toEqual(Array(4).fill(fault === 'network' ? 0 : 500))
  expect(queryClient.getQueryData(['agents']), 'Failure must retain the SAME populated original cache').toBe(consumer.originalCache)
  expect(pickerChoices(), 'Positive old-agent/wrong-workspace controls must pass before the HF3 notice assertion').toEqual(['hf3-old'])
  expect(screen.queryByTestId('agent-picker-team-refresh-notice'), 'A successful team GET must not fabricate a Team failure').not.toBeInTheDocument()
  expect(browserSignals).toEqual([])
  evidence(`old-cache-and-team-success-controls-PASS:${fault}`)
  return consumer
}

async function keyboardRetry(user: ReturnType<typeof userEvent.setup>) {
  const controls = [
    ...screen.queryAllByRole('button', { name: /\bretry\b/i }),
    ...screen.queryAllByRole('menuitem', { name: /\bretry\b/i }),
  ]
  expect(controls, 'HF3 discovery failure needs one accessible Retry action distinct from the Team notice').toHaveLength(1)
  const retry = controls[0]
  expect(retry).not.toHaveAttribute('aria-disabled', 'true')
  expect(retry).toBeEnabled()
  // Genuine key traversal, not retry.focus() (which would hide an unreachable
  // control inside Radix's menu). The finite fixture has one agent plus Retry.
  for (const keys of ['{Home}', '{End}', '{ArrowDown}', '{ArrowUp}', '{Tab}', '{Shift>}{Tab}{/Shift}']) {
    if (document.activeElement === retry) break
    await user.keyboard(keys)
  }
  expect(retry, 'HF3 Retry must be reachable through actual menu/Tab keyboard navigation').toHaveFocus()
  await user.keyboard('{Enter}')
}

function windowFocus() { browserSignals.push('window-focus') }
function visibilityChange() { browserSignals.push('visibilitychange') }

beforeEach(() => {
  // Isolation BEFORE mounting only; never replace or clear the populated
  // cache between initial consumption, external writes, opening and Retry.
  queryClient.clear()
  http.mockClear()
  serverAgents = [oldMember, otherMember]
  serverWorkspaces = [
    { id: ACTIVE_WORKSPACE, name: 'Active HF3 workspace', core_team: ['hf3-old'] },
    { id: OTHER_WORKSPACE, name: 'Other HF3 workspace', core_team: ['hf3-other'] },
  ].map((workspace) => ({
    ...workspace, revision: '0'.repeat(64), status: 'active', pinned: false, pin_order: 0,
    task_count: 0, created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z',
  }))
  discoveryFault = null
  teamFault = false
  requests = []
  discoveryResults = []
  discoveryFailures = []
  unexpectedRequests = []
  browserSignals = []
  pendingRetryRequests = 0
  heldRetry = null
  useWorkspacesStore.getState().setActiveWorkspaceId(ACTIVE_WORKSPACE)
  useUiStore.getState().setAgentSelectorOpen(false)
  useSessionStore.setState({ activeAgentId: oldMember.id, activeAgentType: 'Main', agentSelectionSource: 'auto', agentSelectionWorkspaceId: null })
  vi.stubGlobal('fetch', http)
  if (typeof globalThis.ResizeObserver === 'undefined') {
    // jsdom has no layout engine; this enables the real Radix portal without
    // fabricating agents, errors, choices, visibility or a recovery action.
    vi.stubGlobal('ResizeObserver', class {
      observe() {}
      unobserve() {}
      disconnect() {}
    })
  }
  window.addEventListener('focus', windowFocus)
  document.addEventListener('visibilitychange', visibilityChange)
})

afterEach(async () => {
  // Release a held process-edge response even if a persistence assertion
  // fails; this is teardown, not exception swallowing or changing the oracle.
  heldRetry?.resolve(jsonResponse(serverAgents))
  cleanup()
  await queryClient.cancelQueries()
  queryClient.clear()
  window.removeEventListener('focus', windowFocus)
  document.removeEventListener('visibilitychange', visibilityChange)
  useSessionStore.setState(originalSession, true)
  useWorkspacesStore.getState().setActiveWorkspaceId(originalWorkspace)
  useUiStore.getState().setAgentSelectorOpen(originalPickerOpen)
  vi.unstubAllGlobals()
  expect(unexpectedRequests, 'Only finite same-origin HTTP process edges are permitted').toEqual([])
  expect(queryClient.getDefaultOptions(), 'The singleton query defaults must never be changed to manufacture RED').toBe(originalDefaults)
})

describe('AgentPicker — HF3 cached-discovery-error', () => {
  it('CONTROL: actually fetches and consumes the old cached agent while excluding the other workspace in the real menu', async () => {
    const { user, trigger } = await mountOldCache()
    await openWithKeyboard(user, trigger)
    await waitFor(() => { expect(pickerChoices()).toEqual(['hf3-old']) })
    expect(discoveryNotices(), 'A healthy discovery must not fabricate a stale-agents error').toEqual([])
    evidence('initial-old-agent-and-wrong-workspace-controls-PASS')
  })

  it('CONTROL: failed discovery keeps the old menu usable while the separate team GET succeeds with the new member', async () => {
    await failedDiscoveryWithSuccessfulTeam('network')
  })

  it.each([
    { fault: 'network' as const }, { fault: 'http500' as const },
  ])('HF3 $fault: shows a persistent safe discovery reason and keyboard Retry that reveals the new active-workspace member', async ({ fault }) => {
    const { user, trigger } = await failedDiscoveryWithSuccessfulTeam(fault)
    requireDiscoveryNotice()
    const retryGate = holdResponse()
    heldRetry = retryGate
    discoveryFault = null
    await keyboardRetry(user)
    await waitFor(() => { expect(pendingRetryRequests, 'Keyboard Retry must make a real agents HTTP request').toBe(1) })
    requireDiscoveryNotice()
    expect(pickerChoices(), 'Keep cached choices AND the reason while discovery Retry is pending').toEqual(['hf3-old'])
    evidence(`notice-persists-during-real-retry:${fault}`)
    await act(async () => { retryGate.resolve(jsonResponse(serverAgents)) })
    await waitFor(() => {
      expect(queryClient.getQueryState(['agents'])?.status).toBe('success')
      expect(queryClient.getQueryState(['agents'])?.fetchStatus).toBe('idle')
      expect(queryClient.getQueryData<Agent[]>(['agents'])?.map((agent) => agent.id)).toEqual(['hf3-old', 'hf3-other', 'hf3-new'])
      expect(pickerChoices(), 'Retry, not another picker opening/reload, must expose the new active member and exclude the other workspace').toEqual(['hf3-old', 'hf3-new'])
      expect(discoveryNotices(), 'A genuinely successful discovery clears the last-known warning').toEqual([])
    })
    expect(screen.getByTestId('agent-picker-trigger'), 'Retry must preserve the mounted picker, not remount/reload it').toBe(trigger)
    expect(browserSignals, 'Neither window focus nor visibility recovery may substitute for Retry').toEqual([])
    evidence(`retry-discovers-new-active-member-without-reload:${fault}`)
  })

  it('CONTROL and oracle self-probe: a team-only refresh failure is visible but cannot satisfy the AGENTS discovery notice', async () => {
    const { user, trigger } = await mountOldCache()
    await createAndAssignExternally()
    teamFault = true
    await openWithKeyboard(user, trigger)
    await waitFor(() => {
      expect(queryClient.getQueryState(['agents'])?.status).toBe('success')
      expect(queryClient.getQueryState(['agents'])?.fetchStatus).toBe('idle')
      expect(queryClient.getQueryData<Agent[]>(['agents'])?.map((agent) => agent.id)).toEqual(['hf3-old', 'hf3-other', 'hf3-new'])
      expect(queryClient.getQueryState(workspaceKey)?.status).toBe('error')
      expect(queryClient.getQueryState(workspaceKey)?.fetchStatus).toBe('idle')
      expect(screen.getByRole('note')).toHaveTextContent('Team list could not be refreshed — showing the last known team.')
    }, EXHAUSTED_RETRY_WAIT)
    expect(pickerChoices(), 'Failed membership still scopes to its last known active-workspace team').toEqual(['hf3-old'])
    expect(discoveryNotices(), 'A Team error must NOT count as an agents-discovery failure notice').toEqual([])
    expect(() => requireDiscoveryNotice(), 'Test the instrument: the critical HF3 oracle must reject a visible Team-only notice').toThrowError(/HF3: populated cached agents/)
    evidence('team-only-positive-comparator-and-oracle-self-probe-PASS')
  })
})
