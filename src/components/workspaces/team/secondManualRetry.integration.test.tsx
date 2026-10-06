// PANEL-TEAM-SECOND-RETRY-RED-0033: durable coverage of the SECOND failed
// manual Retry, not the old pack's held Retry followed by immediate success.
// Oracle: supplied HF2 requirement. Both failures must retain a usable Team,
// expose the latest safe reason plus explicit last-known context, and keep a
// keyboard Retry. Later success updates nodes in place; scope must not leak.
// REAL: registry, shell/route, graph/editor, HTTP decoding, QueryClient/cache,
// application retry/backoff, ApiError, and catalog controls. Only fetch and
// missing jsdom geometry are adapted (team-lead Q1=A, same accepted adapter).
// No cache seeds, hook mocks, state injection, fake clocks or retry overrides.
// GREEN and mutation/proof-of-failability: deferred to a different CHECK agent.

import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { ApiError } from '@/lib/api-error'
import { workspacesQueryKeys } from '@/lib/api'
import type { AppState, Workspace, WorkspaceDelegation } from '@/lib/api/generated/openapi-types'
import { queryClient as applicationQueryClient } from '@/lib/queryClient'
import { makeAgent } from '@/test/factories'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { SidePanelShell } from '@/components/panel-shell/SidePanelShell'
import { panels } from '@/components/panel-shell/registry'
import { routeTree } from '@/routeTree.gen'

const WORKSPACE_ID = 'a388185e-fbd5-4bb0-86d7-7243ec0422a1'
const OTHER_WORKSPACE_ID = 'b388185e-fbd5-4bb0-86d7-7243ec0422b2'
const MISSING_WORKSPACE_ID = 'c388185e-fbd5-4bb0-86d7-7243ec0422c3'
const INITIAL_MEMBER = makeAgent({ id: 'retry-initial-member', name: 'Retained teammate' })
const NEW_MEMBER = makeAgent({ id: 'retry-new-member', name: 'Recovered teammate' })
const OTHER_MEMBER = makeAgent({ id: 'retry-other-member', name: 'Other workspace teammate' })
const INITIAL_WORKSPACE: Workspace = {
  id: WORKSPACE_ID, name: 'Repeated Retry workspace', revision: '4'.repeat(64),
  status: 'active', pinned: false, pin_order: 0, task_count: 0,
  core_team: [INITIAL_MEMBER.id], created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z',
}
const UPDATED_WORKSPACE: Workspace = {
  ...INITIAL_WORKSPACE, revision: '5'.repeat(64), core_team: [INITIAL_MEMBER.id, NEW_MEMBER.id],
  updated_at: '2026-10-06T00:00:00Z',
}
const OTHER_WORKSPACE: Workspace = {
  ...INITIAL_WORKSPACE, id: OTHER_WORKSPACE_ID, name: 'Unrelated workspace', core_team: [OTHER_MEMBER.id],
}
const DELEGATION: WorkspaceDelegation = {
  workspace_id: WORKSPACE_ID, revision: INITIAL_WORKSPACE.revision, edges: [], default_depth: 3,
  // Contract's optional informational team is absent: the actual workspace
  // roster must reach the real editor, including the later NEW member.
}
const SIGNED_IN: AppState = {
  onboarding_complete: true,
  identity: { mode: 'local', edition: 'core', signed_in: true, blocked_reason: 'none' },
}
// Safe expected texts are accepted HF2 process-edge INPUT requirements, not
// copied output. The real HTTP layer constructs ApiError from these failures.
const NETWORK_REASON = 'Network unavailable. Check your connection.'
const SERVER_REASON = 'The server is unavailable. Please try again in a moment.'
const MISSING_REASON = 'The requested resource was not found.'
const PRIVATE_DIAGNOSTIC = 'TEST-ONLY private diagnostic /internal/team/retry-trace'
const LAST_KNOWN = /\blast[\s-]+known\b|\bcached\b|\bpreviously[\s-]+loaded\b|\b(?:may be|is)[\s-]+(?:out[\s-]+of[\s-]+date|outdated)\b/i
const WORKSPACE_PATH = `/api/v1/workspaces/${WORKSPACE_ID}`
// Protocol documented by queryClient::shouldRetryQuery: 1 attempt + 3 retries
// for network/ordinary 500. Thus cumulative workspace GETs are 1,5,9,13,14.
const FAILED_ATTEMPTS = 1 + 3

type Presentation = 'docked' | 'fullscreen'
type TeamRouter = ReturnType<typeof createRouter>
type HttpPhase = 'loaded' | 'refresh-500' | 'first-manual-network' | 'second-manual-500' | 'late-success'
// not-wire-format: test HTTP-observation metadata, not a gateway payload.
type ObservedRequest = { method: string; path: string; phase: HttpPhase }
const http = vi.fn<typeof fetch>()
let phase: HttpPhase = 'loaded'
let lateResponse: ReturnType<typeof deferredResponse> | undefined
const requests: ObservedRequest[] = []
const unexpectedRequests: string[] = []
const clients: QueryClient[] = []
const subscriptions: Array<() => void> = []
const dimensions = ['offsetWidth', 'offsetHeight', 'clientWidth', 'clientHeight'] as const
const originalDimensions = dimensions.map((key) => [key, Object.getOwnPropertyDescriptor(HTMLElement.prototype, key)] as const)

function jsonResponse(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } })
}

function deferredResponse() {
  let resolve!: (response: Response) => void
  const promise = new Promise<Response>((accept) => { resolve = accept })
  return { promise, resolve }
}

async function serveHttp(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const path = new URL(input instanceof Request ? input.url : String(input), window.location.href).pathname
  const method = init?.method ?? (input instanceof Request ? input.method : 'GET')
  requests.push({ method, path, phase })
  if (method === 'GET' && path === WORKSPACE_PATH) {
    if (phase === 'loaded') return jsonResponse(INITIAL_WORKSPACE)
    if (phase === 'first-manual-network') throw new TypeError(PRIVATE_DIAGNOSTIC)
    if (phase === 'late-success' && lateResponse) return lateResponse.promise
    return jsonResponse({ error: PRIVATE_DIAGNOSTIC }, 500)
  }
  if (method === 'GET' && path === `/api/v1/workspaces/${MISSING_WORKSPACE_ID}`) {
    return jsonResponse({ error: PRIVATE_DIAGNOSTIC }, 404)
  }
  if (method === 'GET' && path === `/api/v1/workspaces/${OTHER_WORKSPACE_ID}`) return jsonResponse(OTHER_WORKSPACE)
  if (method === 'GET' && path === `${WORKSPACE_PATH}/delegation`) return jsonResponse(DELEGATION)
  if (method === 'GET' && path === '/api/v1/agents') return jsonResponse([INITIAL_MEMBER, NEW_MEMBER, OTHER_MEMBER])
  if (method === 'GET' && path === '/api/v1/state') return jsonResponse(SIGNED_IN)
  if (method === 'GET' && ['/api/v1/providers', '/api/v1/skills'].includes(path)) return jsonResponse([])
  if (method === 'GET' && path === '/api/v1/activity') return jsonResponse({ events: [] })
  if (method === 'GET' && path === '/api/v1/performance') return jsonResponse({ max_tool_iterations: 50 })
  unexpectedRequests.push(`${method} ${path}`)
  throw new Error(`Unspecified HTTP process edge: ${method} ${path}`)
}

beforeAll(async () => { await import('./TeamPanel') })

beforeEach(() => {
  phase = 'loaded'
  lateResponse = undefined
  requests.splice(0)
  unexpectedRequests.splice(0)
  http.mockReset().mockImplementation(serveHttp)
  vi.stubGlobal('fetch', http)
  useUiStore.setState({
    activePanel: null, panelWidth: null, guardPending: false, historyPushed: false,
    editAgentId: null, editAgentWorkspaceId: null, toasts: [],
  })
  useWorkspacesStore.setState({ activeWorkspaceId: OTHER_WORKSPACE_ID })
  window.history.replaceState({}, '', `/#/workspaces/${WORKSPACE_ID}/chat`)
  // Q1=A: geometry adapter ONLY. jsdom lacks layout/DOMMatrix; no graph,
  // hook, query, component, route, or application outcome is substituted.
  vi.stubGlobal('ResizeObserver', class {
    constructor(private readonly callback: ResizeObserverCallback) {}
    observe(target: Element) {
      this.callback([{ target, contentRect: target.getBoundingClientRect() } as ResizeObserverEntry], this as unknown as ResizeObserver)
    }
    unobserve() {}
    disconnect() {}
  })
  vi.stubGlobal('DOMMatrixReadOnly', class { m22 = 1 })
  vi.stubGlobal('scrollTo', vi.fn())
  for (const key of dimensions) {
    Object.defineProperty(HTMLElement.prototype, key, { configurable: true, value: key.endsWith('Width') ? 800 : 600 })
  }
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockReturnValue({
    x: 0, y: 0, width: 800, height: 600, top: 0, left: 0, right: 800, bottom: 600, toJSON: () => ({}),
  } as DOMRect)
})

afterEach(() => {
  cleanup()
  for (const unsubscribe of subscriptions.splice(0)) unsubscribe()
  for (const client of clients.splice(0)) client.clear()
  useUiStore.getState().closePanel()
  useWorkspacesStore.setState({ activeWorkspaceId: null })
  for (const [key, descriptor] of originalDimensions) {
    if (descriptor) Object.defineProperty(HTMLElement.prototype, key, descriptor)
    else Reflect.deleteProperty(HTMLElement.prototype, key)
  }
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  expect(unexpectedRequests, 'all real companion HTTP reads must have explicitly specified valid fixtures').toEqual([])
})

function panelBody(presentation: Presentation) {
  return screen.getByTestId(`team-panel-${presentation}`)
}

function assertSourceRetained(presentation: Presentation, router?: TeamRouter, workspaceId = WORKSPACE_ID) {
  if (presentation === 'docked') {
    expect(screen.getByRole('complementary', { name: 'Team' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Close Team' })).toBeEnabled()
    expect(screen.getByRole('textbox', { name: 'Chat draft' })).toHaveValue('Draft survives two failed manual retries')
    expect(useUiStore.getState().activePanel).toEqual({ id: 'team', context: { workspaceId } })
  } else {
    expect(screen.getByRole('button', { name: 'Back to chat' })).toBeEnabled()
    expect(router?.state.location.pathname).toBe('/panel/team')
    expect(router?.state.location.search).toEqual({ workspace: workspaceId })
    expect(router?.state.matches.map((match) => match.routeId)).toContain('/_fullscreen/panel/$panelId')
    expect(screen.queryByTestId('app-shell')).not.toBeInTheDocument()
  }
}

async function assertMembers(ids: string[]) {
  expect(await screen.findByRole('heading', { name: 'Team & delegation' })).toBeVisible()
  expect(screen.getByRole('button', { name: 'Add agent' })).toBeEnabled()
  await waitFor(() => {
    const actualIds = Array.from(screen.getByTestId('team-graph-canvas').querySelectorAll('.react-flow__node[data-id]'), (node) => node.getAttribute('data-id'))
    expect(actualIds, 'real member nodes must exactly equal the requested workspace roster').toEqual(ids)
  })
  expect(screen.getByTestId(`team-node-${INITIAL_MEMBER.id}`)).toBeVisible()
  expect(within(screen.getByTestId(`team-node-${INITIAL_MEMBER.id}`)).getByText(INITIAL_MEMBER.name, { exact: true })).toBeVisible()
  if (ids.includes(NEW_MEMBER.id)) expect(screen.getByTestId(`team-node-${NEW_MEMBER.id}`)).toBeVisible()
  else expect(screen.queryByTestId(`team-node-${NEW_MEMBER.id}`)).not.toBeInTheDocument()
  expect(screen.queryByTestId(`team-node-${OTHER_MEMBER.id}`), 'unrelated active-workspace members must never enter this graph').not.toBeInTheDocument()
}

async function assertEditorUsable(user: ReturnType<typeof userEvent.setup>) {
  // Actually operate the retained editor, rather than trusting enabled markup.
  await user.click(screen.getByRole('button', { name: 'Add agent' }))
  const search = await screen.findByRole('textbox', { name: 'Search agents' })
  expect(search).toBeVisible()
  await user.type(search, NEW_MEMBER.name)
  expect(screen.getByTestId(`team-add-agent-option-${NEW_MEMBER.id}`)).toBeEnabled()
  expect(screen.queryByTestId(`team-add-agent-option-${INITIAL_MEMBER.id}`)).not.toBeInTheDocument()
  await user.keyboard('{Escape}')
  await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Search agents' })).not.toBeInTheDocument())
}

function assertNoWarning(body: HTMLElement) {
  expect(within(body).queryByRole('alert')).not.toBeInTheDocument()
  expect(within(body).queryByRole('button', { name: /^Retry$/ })).not.toBeInTheDocument()
  expect(body).not.toHaveTextContent(LAST_KNOWN)
  expect(body).not.toHaveTextContent(PRIVATE_DIAGNOSTIC)
}

function assertWarning(body: HTMLElement, reason: string, stage: string, oldReason?: string) {
  const announcements = [...within(body).queryAllByRole('alert'), ...within(body).queryAllByRole('status')]
    .filter((region) => (region.textContent ?? '').includes(reason))
  expect(announcements, `HF2 ${stage}: current safe reason must remain in one accessible persistent Team warning`).toHaveLength(1)
  const warning = announcements[0]
  expect(warning, `${stage}: notice must be visible, not only a toast or hidden text`).toBeVisible()
  expect(warning).toHaveTextContent(reason)
  expect(warning, `${stage}: identify the affected Team/workspace`).toHaveTextContent(/\b(?:team|workspace)\b/i)
  expect(warning, `${stage}: explicitly identify retained data as last known, not fresh`).toHaveTextContent(LAST_KNOWN)
  if (oldReason) expect(warning, `${stage}: replace the previous attempt's reason`).not.toHaveTextContent(oldReason)
  expect(body, `${stage}: private HTTP body/cause must never be rendered`).not.toHaveTextContent(PRIVATE_DIAGNOSTIC)
  const retry = within(warning).getByRole('button', { name: /^Retry$/ })
  expect(retry, `${stage}: keep a usable recovery action after failure`).toBeEnabled()
  return retry
}

async function reachRetryByKeyboard(retry: HTMLElement, user: ReturnType<typeof userEvent.setup>) {
  const targets = document.querySelectorAll('button, a[href], input, textarea, select, [tabindex]').length
  for (let step = 0; step <= targets && document.activeElement !== retry; step++) await user.tab()
  expect(retry, 'Retry must be reachable through ordinary sequential keyboard navigation').toHaveFocus()
}

function workspaceRequests() {
  return requests.filter((request) => request.path === WORKSPACE_PATH)
}

function assertWorkspaceTraffic(phases: HttpPhase[]) {
  expect(workspaceRequests(), 'every initial/refetch/retry HTTP request must target this exact workspace').toEqual(
    phases.map((requestPhase) => ({ method: 'GET', path: WORKSPACE_PATH, phase: requestPhase })),
  )
  expect(requests.filter((request) => request.path === `/api/v1/workspaces/${OTHER_WORKSPACE_ID}`)).toEqual([])
  expect(requests.filter((request) => request.method !== 'GET'), 'opening/closing the editor must not write cached data back').toEqual([])
}

function assertCompletedFailure(client: QueryClient, status: number, reason: string) {
  const state = client.getQueryState(workspacesQueryKeys.detail(WORKSPACE_ID))
  expect(state?.status, 'HTTP operation must have actually failed, not be an empty cache or injected hook state').toBe('error')
  expect(state?.fetchStatus, 'all real automatic retries must have finished').toBe('idle')
  expect(state?.fetchFailureCount).toBe(FAILED_ATTEMPTS)
  expect(state?.error).toBeInstanceOf(ApiError)
  expect(state?.error).toMatchObject({ status, userMessage: reason })
  expect(state?.data, 'actual cache must retain the exact successfully decoded initial workspace').toEqual(INITIAL_WORKSPACE)
  if (status === 0) expect((state?.error as ApiError).cause).toBeInstanceOf(TypeError)
  else expect((state?.error as ApiError).body).toContain(PRIVATE_DIAGNOSTIC)
}

function nextCompletedOperation(client: QueryClient) {
  const query = client.getQueryCache().find({ queryKey: workspacesQueryKeys.detail(WORKSPACE_ID), exact: true })
  expect(query, 'completion observer requires a real already-loaded query').toBeDefined()
  return new Promise<void>((resolve) => {
    // Public cache event observer only; never writes status or calls refetch.
    // Wait for the real backoff to finish without changing retry/wait timeouts.
    const unsubscribe = client.getQueryCache().subscribe((event) => {
      if (event.type === 'updated' && event.query === query && event.query.state.fetchStatus === 'idle'
        && (event.action.type === 'error' || event.action.type === 'success')) {
        unsubscribe()
        resolve()
      }
    })
    subscriptions.push(unsubscribe)
  })
}

async function mountLoadedSurface(presentation: Presentation) {
  const client = new QueryClient({ defaultOptions: applicationQueryClient.getDefaultOptions() })
  clients.push(client)
  expect(client.getQueryCache().getAll(), 'no preset or warmed test cache is allowed').toEqual([])
  const provider = (content: ReactNode) => <QueryClientProvider client={client}>{content}</QueryClientProvider>
  let router: TeamRouter | undefined
  if (presentation === 'docked') {
    render(provider(<SidePanelShell panels={panels} username="second-retry-test"
      chat={<textarea aria-label="Chat draft" defaultValue="Draft survives two failed manual retries" />}
    />))
    act(() => { useUiStore.getState().openPanel('team', { workspaceId: WORKSPACE_ID }) })
  } else {
    router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: [`/panel/team?workspace=${WORKSPACE_ID}`] }) })
    render(provider(<RouterProvider router={router} />))
  }
  await assertMembers([INITIAL_MEMBER.id])
  assertWorkspaceTraffic(['loaded'])
  expect(requests.filter((request) => request.path === `${WORKSPACE_PATH}/delegation`)).toEqual([
    { method: 'GET', path: `${WORKSPACE_PATH}/delegation`, phase: 'loaded' },
  ])
  const key = workspacesQueryKeys.detail(WORKSPACE_ID)
  expect(client.getQueryData(key)).toEqual(INITIAL_WORKSPACE)
  expect(client.getQueryState(key)?.status).toBe('success')
  expect(client.getQueryState(key)?.error).toBeNull()
  expect(client.getQueryData(workspacesQueryKeys.detail(OTHER_WORKSPACE_ID))).toBeUndefined()
  assertNoWarning(panelBody(presentation))
  assertSourceRetained(presentation, router)
  return { client, router }
}

function failedRequests(requestPhase: HttpPhase): HttpPhase[] {
  return Array.from({ length: FAILED_ATTEMPTS }, () => requestPhase)
}

function recordCheckpoint(presentation: Presentation, client: QueryClient, stage: string) {
  const state = client.getQueryState<Workspace>(workspacesQueryKeys.detail(WORKSPACE_ID))
  // Sanitized evidence only; these observations never supply expected values.
  console.info('HF2_SECOND_RETRY_STATE', JSON.stringify({
    presentation, stage, workspaceRequests: workspaceRequests().length,
    queryStatus: state?.status, fetchStatus: state?.fetchStatus,
    errorStatus: state?.error instanceof ApiError ? state.error.status : null,
    safeReason: state?.error instanceof ApiError ? state.error.userMessage : null,
    dataWorkspaceId: state?.data?.id, dataMembers: state?.data?.core_team,
    nodeIds: Array.from(screen.getByTestId('team-graph-canvas').querySelectorAll('.react-flow__node[data-id]'), (node) => node.getAttribute('data-id')),
    accessibleWarnings: [...within(panelBody(presentation)).queryAllByRole('alert'), ...within(panelBody(presentation)).queryAllByRole('status')]
      .filter((region) => /unavailable/.test(region.textContent ?? '')).length,
  }))
}

describe.each(['docked', 'fullscreen'] as const)('Team second manual Retry — %s', (presentation) => {
  it('independent initial valid-load control: real current-workspace cache, member nodes and usable editor without a false warning', async () => {
    const { client, router } = await mountLoadedSurface(presentation)
    await assertEditorUsable(userEvent.setup())
    expect(client.getQueryData(['agents'])).toEqual([INITIAL_MEMBER, NEW_MEMBER, OTHER_MEMBER])
    expect(client.getQueryData(workspacesQueryKeys.detail(WORKSPACE_ID))).toEqual(INITIAL_WORKSPACE)
    assertNoWarning(panelBody(presentation))
    assertSourceRetained(presentation, router)
    assertWorkspaceTraffic(['loaded'])
    recordCheckpoint(presentation, client, 'independent initial valid-load control')
  })

  it('SECOND failed manual Retry: network then ordinary HTTP500 each keep latest safe last-known Team warning and keyboard recovery; later success updates nodes in place without workspace leakage', async () => {
    const { client, router } = await mountLoadedSurface(presentation)
    const user = userEvent.setup()
    await assertEditorUsable(user)
    const key = workspacesQueryKeys.detail(WORKSPACE_ID)
    const queryBefore = client.getQueryCache().find({ queryKey: key, exact: true })
    const bodyBefore = panelBody(presentation)
    const memberBefore = screen.getByTestId(`team-node-${INITIAL_MEMBER.id}`)
    const addressBefore = window.location.href

    phase = 'refresh-500'
    await act(async () => { await client.invalidateQueries({ queryKey: key, exact: true }) })
    assertCompletedFailure(client, 500, SERVER_REASON)
    const initialError = client.getQueryState(key)?.error
    const refreshTraffic: HttpPhase[] = ['loaded', ...failedRequests('refresh-500')]
    assertWorkspaceTraffic(refreshTraffic)
    expect(workspaceRequests()).toHaveLength(5)
    await assertMembers([INITIAL_MEMBER.id])
    await assertEditorUsable(user)
    assertSourceRetained(presentation, router)
    expect(screen.getByTestId(`team-node-${INITIAL_MEMBER.id}`)).toBe(memberBefore)
    expect(bodyBefore).not.toHaveTextContent(PRIVATE_DIAGNOSTIC)
    recordCheckpoint(presentation, client, 'completed initial refresh failure; retained editor operated')
    // Valid baseline RED is this named oracle, AFTER real load, completed 500
    // retries, retained exact cache and an actually operated editor. Pre-change
    // may stop here; that is NOT evidence of either failed manual Retry yet.
    const firstRetry = await waitFor(() => assertWarning(panelBody(presentation), SERVER_REASON, 'initial refresh failure'))

    phase = 'first-manual-network'
    const firstCompletion = nextCompletedOperation(client)
    await reachRetryByKeyboard(firstRetry, user)
    await user.keyboard('{Enter}')
    await waitFor(() => expect(workspaceRequests(), 'FIRST manual Retry must issue its own HTTP request').toHaveLength(6))
    expect(client.getQueryState(key)?.fetchStatus).toBe('fetching')
    expect(client.getQueryData(key)).toEqual(INITIAL_WORKSPACE)
    assertWarning(panelBody(presentation), SERVER_REASON, 'first manual Retry in flight')
    await act(async () => { await firstCompletion })
    assertCompletedFailure(client, 0, NETWORK_REASON)
    const firstManualError = client.getQueryState(key)?.error
    expect(firstManualError, 'the first manual failure must be new, not the original failed refresh').not.toBe(initialError)
    const firstTraffic = [...refreshTraffic, ...failedRequests('first-manual-network')]
    assertWorkspaceTraffic(firstTraffic)
    expect(workspaceRequests()).toHaveLength(9)
    await assertMembers([INITIAL_MEMBER.id])
    await assertEditorUsable(user)
    assertSourceRetained(presentation, router)
    expect(screen.getByTestId(`team-node-${INITIAL_MEMBER.id}`)).toBe(memberBefore)
    const secondRetry = await waitFor(() => assertWarning(panelBody(presentation), NETWORK_REASON, 'FIRST failed manual Retry', SERVER_REASON))
    recordCheckpoint(presentation, client, 'FIRST failed manual Retry')

    phase = 'second-manual-500'
    const secondCompletion = nextCompletedOperation(client)
    await reachRetryByKeyboard(secondRetry, user)
    await user.keyboard('{Enter}')
    // This source-bound assertion catches disconnecting ONLY the second Retry
    // on future GREEN: old tests would stop after one successful manual Retry.
    await waitFor(() => expect(workspaceRequests(), 'SECOND manual Retry must issue its own HTTP request, not reuse the first failure').toHaveLength(10))
    expect(client.getQueryState(key)?.fetchStatus).toBe('fetching')
    expect(client.getQueryData(key)).toEqual(INITIAL_WORKSPACE)
    assertWarning(panelBody(presentation), NETWORK_REASON, 'second manual Retry in flight')
    await act(async () => { await secondCompletion })
    assertCompletedFailure(client, 500, SERVER_REASON)
    expect(client.getQueryState(key)?.error, 'second manual Retry must complete with its own actual HTTP500 failure').not.toBe(firstManualError)
    const secondTraffic = [...firstTraffic, ...failedRequests('second-manual-500')]
    assertWorkspaceTraffic(secondTraffic)
    expect(workspaceRequests()).toHaveLength(13)
    await assertMembers([INITIAL_MEMBER.id])
    await assertEditorUsable(user)
    assertSourceRetained(presentation, router)
    expect(client.getQueryCache().find({ queryKey: key, exact: true })).toBe(queryBefore)
    expect(panelBody(presentation)).toBe(bodyBefore)
    expect(screen.getByTestId(`team-node-${INITIAL_MEMBER.id}`)).toBe(memberBefore)
    expect(window.location.href).toBe(addressBefore)
    const recoveryRetry = await waitFor(() => assertWarning(panelBody(presentation), SERVER_REASON, 'SECOND failed manual Retry', NETWORK_REASON))
    recordCheckpoint(presentation, client, 'SECOND failed manual Retry')

    lateResponse = deferredResponse()
    phase = 'late-success'
    const recoveryCompletion = nextCompletedOperation(client)
    await reachRetryByKeyboard(recoveryRetry, user)
    await user.keyboard('{Enter}')
    await waitFor(() => expect(workspaceRequests(), 'later recovery must still use the same correct-workspace HTTP query').toHaveLength(14))
    expect(client.getQueryState(key)?.fetchStatus).toBe('fetching')
    expect(client.getQueryData(key)).toEqual(INITIAL_WORKSPACE)
    assertWarning(panelBody(presentation), SERVER_REASON, 'later successful Retry still pending', NETWORK_REASON)
    await assertMembers([INITIAL_MEMBER.id])
    assertSourceRetained(presentation, router)
    await act(async () => { lateResponse?.resolve(jsonResponse(UPDATED_WORKSPACE)); await recoveryCompletion })
    await assertMembers([INITIAL_MEMBER.id, NEW_MEMBER.id])
    await waitFor(() => assertNoWarning(panelBody(presentation)))
    expect(client.getQueryData(key)).toEqual(UPDATED_WORKSPACE)
    expect(client.getQueryState(key)?.status).toBe('success')
    expect(client.getQueryState(key)?.fetchStatus).toBe('idle')
    expect(client.getQueryState(key)?.error).toBeNull()
    expect(client.getQueryCache().find({ queryKey: key, exact: true })).toBe(queryBefore)
    expect(panelBody(presentation)).toBe(bodyBefore)
    expect(screen.getByTestId(`team-node-${INITIAL_MEMBER.id}`), 'late success must update the mounted graph, not replace its existing member node').toBe(memberBefore)
    expect(window.location.href).toBe(addressBefore)
    assertWorkspaceTraffic([...secondTraffic, 'late-success'])
    assertSourceRetained(presentation, router)
    recordCheckpoint(presentation, client, 'later successful Retry; warning cleared')

    // No cache seeding: switch the real adapter to a different, genuinely
    // missing workspace. Neither A's last-known roster nor unrelated B leaks.
    if (presentation === 'docked') {
      act(() => { useUiStore.getState().openPanel('team', { workspaceId: MISSING_WORKSPACE_ID }) })
    } else {
      await act(async () => {
        await router?.navigate({ to: '/panel/$panelId', params: { panelId: 'team' }, search: { workspace: MISSING_WORKSPACE_ID } })
      })
    }
    const missingKey = workspacesQueryKeys.detail(MISSING_WORKSPACE_ID)
    await waitFor(() => expect(client.getQueryState(missingKey)?.status).toBe('error'))
    expect(client.getQueryState(missingKey)?.error).toBeInstanceOf(ApiError)
    expect(client.getQueryState(missingKey)?.error).toMatchObject({ status: 404, userMessage: MISSING_REASON })
    expect(client.getQueryData(missingKey)).toBeUndefined()
    expect(client.getQueryData(key)).toEqual(UPDATED_WORKSPACE)
    expect(requests.filter((request) => request.path === `/api/v1/workspaces/${MISSING_WORKSPACE_ID}`)).toEqual([
      { method: 'GET', path: `/api/v1/workspaces/${MISSING_WORKSPACE_ID}`, phase: 'late-success' },
    ])
    expect(screen.queryByTestId('team-graph-canvas')).not.toBeInTheDocument()
    for (const member of [INITIAL_MEMBER, NEW_MEMBER, OTHER_MEMBER]) {
      expect(screen.queryByText(member.name, { exact: true }), 'missing workspace must not inherit any previous workspace roster').not.toBeInTheDocument()
    }
    const alert = within(panelBody(presentation)).getByRole('alert')
    expect(alert).toBeVisible()
    expect(alert).toHaveTextContent(MISSING_REASON)
    expect(alert).not.toHaveTextContent(LAST_KNOWN)
    expect(panelBody(presentation)).not.toHaveTextContent(PRIVATE_DIAGNOSTIC)
    expect(within(alert).getByRole('button', { name: /^Retry$/ })).toBeEnabled()
    assertSourceRetained(presentation, router, MISSING_WORKSPACE_ID)
    assertWorkspaceTraffic([...secondTraffic, 'late-success'])
  })
})
