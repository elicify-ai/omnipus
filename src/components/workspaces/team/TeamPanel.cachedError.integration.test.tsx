// PANEL-TEAM-WARM-ERROR-RED-2153 / frozen61f HF2 — approved SAME-workspace
// usability oracle: a failed background refresh keeps usable cached Team data,
// but must expose a persistent safe reason, last-known context and keyboard Retry.
// A later valid workspace response clears the warning and updates the real editor.
// Expectations come from the dispatch and the independent INPUT records below,
// not observed product output. No exact unapproved last-known sentence is required.
// REAL: registry, dock/fullscreen adapters, TeamPanel, editor/graph/context/stores,
// QueryClient/useQuery, application retry policy and backoff, catalog controls.
// FAKED: API process edges; missing jsdom geometry is a browser-platform adapter.
// No hook mocks, error-state injection, cache seeds, fake clocks or retry overrides.
// GREEN and implementation mutation/proof-of-failability checks: deferred to CHECK.

import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { ApiError } from '@/lib/api-error'
import type { AppState, Workspace, WorkspaceDelegation } from '@/lib/api/generated/openapi-types'
import { queryClient as applicationQueryClient } from '@/lib/queryClient'
import { workspacesQueryKeys } from '@/lib/api'
import { makeAgent } from '@/test/factories'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { SidePanelShell } from '@/components/panel-shell/SidePanelShell'
import { panels } from '@/components/panel-shell/registry'
import { QueryErrorState } from '@/components/ui/query-error-state'
import { routeTree } from '@/routeTree.gen'

const api = vi.hoisted(() => ({
  fetchWorkspace: vi.fn<typeof import('@/lib/api').fetchWorkspace>(),
  fetchWorkspaceDelegation: vi.fn<typeof import('@/lib/api').fetchWorkspaceDelegation>(),
  fetchAgents: vi.fn<typeof import('@/lib/api').fetchAgents>(),
  fetchAppState: vi.fn<typeof import('@/lib/api').fetchAppState>(),
  // The real, closed AgentProfile starts these process-edge reads on mount.
  fetchProviders: vi.fn<typeof import('@/lib/api').fetchProviders>(),
  fetchActivity: vi.fn<typeof import('@/lib/api').fetchActivity>(),
  fetchSkills: vi.fn<typeof import('@/lib/api').fetchSkills>(),
  fetchPerformanceSettings: vi.fn<typeof import('@/lib/api').fetchPerformanceSettings>(),
}))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  ...api,
}))

const WORKSPACE_ID = 'a15a2511-9360-4cd4-9fb0-a001d003a111'
const MISSING_WORKSPACE_ID = 'b25b2511-9360-4cd4-9fb0-b001d003b222'
const INITIAL_MEMBER = makeAgent({ id: 'cached-team-member', name: 'Cached teammate' })
const NEW_MEMBER = makeAgent({ id: 'updated-team-member', name: 'Newly loaded teammate' })
const INITIAL_WORKSPACE: Workspace = {
  id: WORKSPACE_ID, name: 'Cached Team workspace', revision: '2'.repeat(64),
  status: 'active', pinned: false, pin_order: 0, task_count: 0,
  core_team: [INITIAL_MEMBER.id], created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z',
}
const UPDATED_WORKSPACE: Workspace = {
  ...INITIAL_WORKSPACE, revision: '3'.repeat(64),
  core_team: [INITIAL_MEMBER.id, NEW_MEMBER.id], updated_at: '2026-10-02T00:00:00Z',
}
const DELEGATION: WorkspaceDelegation = {
  workspace_id: WORKSPACE_ID, revision: INITIAL_WORKSPACE.revision,
  edges: [], default_depth: 3,
  // The contract makes the informational team field optional. Leaving it
  // absent gives a valid edge-free response whose membership comes from the
  // workspace record, so a NEW record must actually reach the real editor.
}
const SIGNED_IN: AppState = {
  onboarding_complete: true,
  identity: { mode: 'local', edition: 'core', signed_in: true, blocked_reason: 'none' },
}
const PRIVATE_DIAGNOSTIC = 'TEST-ONLY private server diagnostic /internal/team/trace'
// not-wire-format: case metadata, not an HTTP payload or a copied response.
const failures = [
  { label: 'network', status: 0, reason: 'Network unavailable. Check your connection.' },
  { label: 'ordinary HTTP 500', status: 500, reason: 'The server is unavailable. Please try again in a moment.' },
] as const
// Exact safe reasons are process-edge INPUT. Last-known copy is deliberately
// semantic: each accepted alternative explicitly identifies retained/stale data.
const LAST_KNOWN_CONTEXT = /\blast[\s-]+known\b|\bcached\b|\bpreviously[\s-]+loaded\b|\b(?:may be|is)[\s-]+(?:out[\s-]+of[\s-]+date|outdated)\b/i
const WARNING_ORACLE_MESSAGE = 'HF2: cached Team refresh failure must expose its safe reason in exactly one accessible persistent warning'
const clients: QueryClient[] = []
const dimensions = ['offsetWidth', 'offsetHeight', 'clientWidth', 'clientHeight'] as const
const originalDimensions = dimensions.map((key) => [
  key, Object.getOwnPropertyDescriptor(HTMLElement.prototype, key),
] as const)

type Presentation = 'docked' | 'fullscreen'
type TeamRouter = ReturnType<typeof createRouter>

function deferredWorkspace() {
  let resolve!: (workspace: Workspace) => void
  const promise = new Promise<Workspace>((accept) => { resolve = accept })
  return { promise, resolve }
}

beforeAll(async () => {
  // Do not mistake lazy-chunk loading for the workspace query's actual state.
  await import('./TeamPanel')
})

beforeEach(() => {
  for (const edge of Object.values(api)) edge.mockReset()
  api.fetchWorkspace.mockResolvedValue(INITIAL_WORKSPACE)
  api.fetchWorkspaceDelegation.mockResolvedValue(DELEGATION)
  api.fetchAgents.mockResolvedValue([INITIAL_MEMBER, NEW_MEMBER])
  api.fetchAppState.mockResolvedValue(SIGNED_IN)
  api.fetchProviders.mockResolvedValue([])
  api.fetchActivity.mockResolvedValue({ events: [] })
  api.fetchSkills.mockResolvedValue([])
  api.fetchPerformanceSettings.mockResolvedValue({ max_tool_iterations: 50 })
  useUiStore.setState({
    activePanel: null, panelWidth: null, guardPending: false, historyPushed: false,
    editAgentId: null, editAgentWorkspaceId: null, toasts: [],
  })
  // Panel/URL scope must win over this unrelated active-workspace selection.
  useWorkspacesStore.setState({ activeWorkspaceId: 'unrelated-active-workspace' })
  window.history.replaceState({}, '', `/#/workspaces/${WORKSPACE_ID}/chat`)
  // jsdom has no layout engine. These supply browser geometry, not product
  // hook/data/editor stand-ins, matching the accepted cold-cache integration.
  vi.stubGlobal('ResizeObserver', class {
    constructor(private readonly callback: ResizeObserverCallback) {}
    observe(target: Element) {
      this.callback([
        { target, contentRect: target.getBoundingClientRect() } as ResizeObserverEntry,
      ], this as unknown as ResizeObserver)
    }
    unobserve() {}
    disconnect() {}
  })
  vi.stubGlobal('DOMMatrixReadOnly', class { m22 = 1 })
  vi.stubGlobal('scrollTo', vi.fn())
  for (const key of dimensions) {
    Object.defineProperty(HTMLElement.prototype, key, {
      configurable: true, value: key.endsWith('Width') ? 800 : 600,
    })
  }
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockReturnValue({
    x: 0, y: 0, width: 800, height: 600, top: 0, left: 0, right: 800, bottom: 600,
    toJSON: () => ({}),
  } as DOMRect)
})

afterEach(() => {
  cleanup()
  for (const client of clients.splice(0)) client.clear()
  useUiStore.getState().closePanel()
  useWorkspacesStore.setState({ activeWorkspaceId: null })
  for (const [key, descriptor] of originalDimensions) {
    if (descriptor) Object.defineProperty(HTMLElement.prototype, key, descriptor)
    else Reflect.deleteProperty(HTMLElement.prototype, key)
  }
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function panelBody(presentation: Presentation) {
  return screen.getByTestId(`team-panel-${presentation}`)
}

function assertSourceRetained(presentation: Presentation, router?: TeamRouter, workspaceId = WORKSPACE_ID) {
  if (presentation === 'docked') {
    expect(screen.getByRole('complementary', { name: 'Team' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Close Team' })).toBeEnabled()
    expect(screen.getByRole('textbox', { name: 'Chat draft' })).toHaveValue('Draft survives cached Team refresh')
    expect(useUiStore.getState().activePanel).toEqual({ id: 'team', context: { workspaceId } })
  } else {
    expect(screen.getByRole('button', { name: 'Back to chat' })).toBeEnabled()
    expect(router?.state.location.pathname).toBe('/panel/team')
    expect(router?.state.location.search).toEqual({ workspace: workspaceId })
    expect(router?.state.matches.map((match) => match.routeId)).toContain('/_fullscreen/panel/$panelId')
    expect(screen.queryByTestId('app-shell')).not.toBeInTheDocument()
  }
}

async function assertTeamMembers(memberIds: string[]) {
  expect(await screen.findByRole('heading', { name: 'Team & delegation' })).toBeVisible()
  expect(screen.getByRole('button', { name: 'Add agent' })).toBeEnabled()
  await waitFor(() => {
    const nodeIds = Array.from(
      screen.getByTestId('team-graph-canvas').querySelectorAll('.react-flow__node[data-id]'),
      (node) => node.getAttribute('data-id'),
    )
    expect(nodeIds, 'real editor node membership must equal the specified workspace roster').toEqual(memberIds)
  })
  expect(screen.getByText(INITIAL_MEMBER.name, { exact: true })).toBeVisible()
  if (memberIds.includes(NEW_MEMBER.id)) expect(screen.getByText(NEW_MEMBER.name, { exact: true })).toBeVisible()
  else expect(screen.queryByText(NEW_MEMBER.name, { exact: true })).not.toBeInTheDocument()
}

function assertNoRefreshWarning(body: HTMLElement) {
  expect(within(body).queryByRole('alert')).not.toBeInTheDocument()
  expect(within(body).queryByRole('button', { name: /^Retry$/ })).not.toBeInTheDocument()
  expect(body).not.toHaveTextContent(LAST_KNOWN_CONTEXT)
}

function assertCachedRefreshWarning(body: HTMLElement, reason: string) {
  const announced = [
    ...within(body).queryAllByRole('alert'),
    ...within(body).queryAllByRole('status'),
  ].filter((region) => (region.textContent ?? '').includes(reason))
  expect(announced, WARNING_ORACLE_MESSAGE).toHaveLength(1)
  const warning = announced[0]
  expect(warning, 'refresh warning must remain visibly present, not toast-only or hidden').toBeVisible()
  expect(warning).toHaveTextContent(reason)
  expect(warning, 'warning must identify the affected Team/workspace operation').toHaveTextContent(/\b(?:team|workspace)\b/i)
  expect(warning, 'warning must identify displayed data as last known, not fresh').toHaveTextContent(LAST_KNOWN_CONTEXT)
  expect(body, 'raw diagnostic response bodies/causes must never appear as the user reason').not.toHaveTextContent(PRIVATE_DIAGNOSTIC)
  const retry = within(warning).getByRole('button', { name: /^Retry$/ })
  expect(retry, 'recovery must offer a working action, not only error markup').toBeEnabled()
  return retry
}

async function reachRetryByKeyboard(retry: HTMLElement, user: ReturnType<typeof userEvent.setup>) {
  // One pass through the actual possible focus targets, not an arbitrary
  // timeout, sleep or direct retry.focus() that would hide broken Tab reachability.
  const targetCount = document.querySelectorAll('button, a[href], input, textarea, select, [tabindex]').length
  for (let step = 0; step <= targetCount && document.activeElement !== retry; step++) await user.tab()
  expect(retry, 'Retry must be reachable with sequential keyboard Tab navigation').toHaveFocus()
}

async function mountLoadedSurface(presentation: Presentation) {
  const client = new QueryClient({ defaultOptions: applicationQueryClient.getDefaultOptions() })
  clients.push(client)
  expect(client.getQueryCache().getAll(), 'initial data must arrive from the API, not a seeded cache').toEqual([])
  const provider = (content: ReactNode) => <QueryClientProvider client={client}>{content}</QueryClientProvider>
  let router: TeamRouter | undefined
  if (presentation === 'docked') {
    render(provider(<SidePanelShell
      panels={panels} username="cached-team-test"
      chat={<textarea aria-label="Chat draft" defaultValue="Draft survives cached Team refresh" />}
    />))
    act(() => { useUiStore.getState().openPanel('team', { workspaceId: WORKSPACE_ID }) })
  } else {
    router = createRouter({
      routeTree,
      history: createMemoryHistory({ initialEntries: [`/panel/team?workspace=${WORKSPACE_ID}`] }),
    })
    render(provider(<RouterProvider router={router} />))
  }
  await assertTeamMembers([INITIAL_MEMBER.id])
  expect(api.fetchWorkspace).toHaveBeenCalledExactlyOnceWith(WORKSPACE_ID)
  expect(api.fetchWorkspaceDelegation).toHaveBeenCalledExactlyOnceWith(WORKSPACE_ID)
  expect(client.getQueryData(workspacesQueryKeys.detail(WORKSPACE_ID))).toEqual(INITIAL_WORKSPACE)
  expect(client.getQueryState(workspacesQueryKeys.detail(WORKSPACE_ID))?.status).toBe('success')
  assertNoRefreshWarning(panelBody(presentation))
  assertSourceRetained(presentation, router)
  return { client, router }
}

async function invalidateWorkspace(client: QueryClient) {
  // Public production query operation: keep the existing client/cache/observer.
  // Network/500 take the real application's 1s + 2s + 4s backoff; no timing
  // assertion, accelerated clock, wait widening or retry-policy override.
  await act(async () => {
    await client.invalidateQueries({ queryKey: workspacesQueryKeys.detail(WORKSPACE_ID), exact: true })
  })
}

describe.each(['docked', 'fullscreen'] as const)('Team cached-workspace recovery — %s', (presentation) => {
  it('initial valid workspace renders real members and usable controls with no false warning', async () => {
    const { client, router } = await mountLoadedSurface(presentation)
    expect(client.getQueryState(workspacesQueryKeys.detail(WORKSPACE_ID))?.error).toBeNull()
    expect(client.getQueryData(['agents'])).toEqual([INITIAL_MEMBER, NEW_MEMBER])
    assertSourceRetained(presentation, router)
  })

  it.each(failures)('HF2: $label refresh retains cached Team and exposes a safe persistent warning with keyboard Retry; new response recovers in place', async ({ status, reason }) => {
    const { client, router } = await mountLoadedSurface(presentation)
    const key = workspacesQueryKeys.detail(WORKSPACE_ID)
    const queryBefore = client.getQueryCache().find({ queryKey: key, exact: true })
    const bodyBefore = panelBody(presentation)
    const addressBefore = window.location.href
    const failure = new ApiError(status, reason, {
      body: PRIVATE_DIAGNOSTIC, cause: new TypeError(PRIVATE_DIAGNOSTIC),
    })
    api.fetchWorkspace.mockRejectedValue(failure)
    await invalidateWorkspace(client)
    const state = client.getQueryState(key)
    expect(state?.status, 'the actual background workspace request must have failed').toBe('error')
    expect(state?.fetchStatus).toBe('idle')
    expect(state?.error).toBe(failure)
    expect((state?.error as ApiError).status).toBe(status)
    expect(state?.data, 'TanStack must retain the exact previously successful workspace').toEqual(INITIAL_WORKSPACE)
    expect(client.getQueryCache().find({ queryKey: key, exact: true })).toBe(queryBefore)
    // 1 successful initial request + 1 failed refresh + 3 automatic retries.
    expect(api.fetchWorkspace.mock.calls).toEqual(Array.from({ length: 5 }, () => [WORKSPACE_ID]))
    await assertTeamMembers([INITIAL_MEMBER.id])
    assertSourceRetained(presentation, router)
    // The named pre-fix RED is HERE: the real query is in error and the real
    // cached editor works, but no accessible reason/Retry is rendered.
    const retry = await waitFor(() => assertCachedRefreshWarning(panelBody(presentation), reason))

    const later = deferredWorkspace()
    api.fetchWorkspace.mockReturnValue(later.promise)
    const user = userEvent.setup()
    await reachRetryByKeyboard(retry, user)
    await user.keyboard('{Enter}')
    await waitFor(() => expect(api.fetchWorkspace).toHaveBeenCalledTimes(6))
    expect(api.fetchWorkspace).toHaveBeenLastCalledWith(WORKSPACE_ID)
    expect(client.getQueryState(key)?.fetchStatus).toBe('fetching')
    expect(client.getQueryData(key)).toEqual(INITIAL_WORKSPACE)
    assertCachedRefreshWarning(panelBody(presentation), reason)
    await assertTeamMembers([INITIAL_MEMBER.id])
    assertSourceRetained(presentation, router)
    await act(async () => { later.resolve(UPDATED_WORKSPACE); await later.promise })
    await assertTeamMembers([INITIAL_MEMBER.id, NEW_MEMBER.id])
    await waitFor(() => assertNoRefreshWarning(panelBody(presentation)))
    expect(client.getQueryData(key)).toEqual(UPDATED_WORKSPACE)
    expect(client.getQueryState(key)?.status).toBe('success')
    expect(client.getQueryState(key)?.error).toBeNull()
    expect(client.getQueryCache().find({ queryKey: key, exact: true })).toBe(queryBefore)
    expect(panelBody(presentation), 'Retry must recover the existing mounted Team, not reload it').toBe(bodyBefore)
    expect(window.location.href).toBe(addressBefore)
    expect(api.fetchWorkspace.mock.calls).toEqual(Array.from({ length: 6 }, () => [WORKSPACE_ID]))
    assertSourceRetained(presentation, router)
  })

  it('independent successful refresh updates the real editor from a new workspace record on the same cache without reload', async () => {
    const { client, router } = await mountLoadedSurface(presentation)
    const key = workspacesQueryKeys.detail(WORKSPACE_ID)
    const queryBefore = client.getQueryCache().find({ queryKey: key, exact: true })
    const bodyBefore = panelBody(presentation)
    const addressBefore = window.location.href
    api.fetchWorkspace.mockResolvedValue(UPDATED_WORKSPACE)
    await invalidateWorkspace(client)
    await assertTeamMembers([INITIAL_MEMBER.id, NEW_MEMBER.id])
    expect(client.getQueryData(key)).toEqual(UPDATED_WORKSPACE)
    expect(client.getQueryState(key)?.status).toBe('success')
    expect(client.getQueryCache().find({ queryKey: key, exact: true })).toBe(queryBefore)
    expect(api.fetchWorkspace.mock.calls).toEqual([[WORKSPACE_ID], [WORKSPACE_ID]])
    expect(panelBody(presentation)).toBe(bodyBefore)
    expect(window.location.href).toBe(addressBefore)
    assertNoRefreshWarning(panelBody(presentation))
    assertSourceRetained(presentation, router)
  })

  it('switching from cached A to missing B never substitutes A members for B and preserves the cold-lookup reason and Retry', async () => {
    const { client, router } = await mountLoadedSurface(presentation)
    const reason = 'The requested workspace could not be found.'
    const failure = new ApiError(404, reason)
    api.fetchWorkspace.mockImplementation(async (id) => {
      if (id === MISSING_WORKSPACE_ID) throw failure
      if (id === WORKSPACE_ID) return INITIAL_WORKSPACE
      throw new Error('Unexpected workspace process-edge input')
    })
    if (presentation === 'docked') {
      act(() => { useUiStore.getState().openPanel('team', { workspaceId: MISSING_WORKSPACE_ID }) })
    } else {
      await act(async () => {
        await router?.navigate({ to: '/panel/$panelId', params: { panelId: 'team' }, search: { workspace: MISSING_WORKSPACE_ID } })
      })
    }
    await waitFor(() => expect(client.getQueryState(workspacesQueryKeys.detail(MISSING_WORKSPACE_ID))?.status).toBe('error'))
    expect(client.getQueryState(workspacesQueryKeys.detail(MISSING_WORKSPACE_ID))?.error).toBe(failure)
    expect(client.getQueryData(workspacesQueryKeys.detail(MISSING_WORKSPACE_ID))).toBeUndefined()
    expect(client.getQueryData(workspacesQueryKeys.detail(WORKSPACE_ID))).toEqual(INITIAL_WORKSPACE)
    expect(api.fetchWorkspace.mock.calls).toEqual([[WORKSPACE_ID], [MISSING_WORKSPACE_ID]])
    expect(screen.queryByText(INITIAL_MEMBER.name, { exact: true })).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Team & delegation' })).not.toBeInTheDocument()
    const alert = within(panelBody(presentation)).getByRole('alert')
    expect(alert).toBeVisible()
    expect(alert).toHaveTextContent(reason)
    expect(within(alert).getByRole('button', { name: /^Retry$/ })).toBeEnabled()
    expect(alert, 'a missing different workspace is not a last-known Team view').not.toHaveTextContent(LAST_KNOWN_CONTEXT)
    assertSourceRetained(presentation, router, MISSING_WORKSPACE_ID)
  })
})

// Assertion-instrument self-probes only. These detached catalog fixtures are
// never substituted for TeamPanel and cannot prove product GREEN. The SAME
// oracle used at the HF2 boundary must accept correct evidence and reject a
// deliberately suppressed warning and missing retained-data context.
function renderOracleFixture(lastKnown = true) {
  const reason = 'Network unavailable. Check your connection.'
  const context = lastKnown ? ' Showing the last known Team.' : ''
  const view = render(<QueryErrorState message={`Could not refresh Team. ${reason}${context}`} onRetry={() => {}} />)
  return { body: view.container, reason, view }
}

describe('HF2 warning oracle self-probes — not production GREEN', () => {
  it('accepts independently specified visible safe reason, last-known Team context and enabled catalog Retry', () => {
    const { body, reason } = renderOracleFixture()
    expect(assertCachedRefreshWarning(body, reason)).toBe(within(body).getByRole('button', { name: 'Retry' }))
  })

  it('rejects deliberate warning suppression instead of falsely accepting a retained editor with no error signal', () => {
    const { body, reason, view } = renderOracleFixture()
    assertCachedRefreshWarning(body, reason)
    // Suppress only this detached assertion fixture through React, so its
    // normal unmount cleanup remains valid. The product is never mutated.
    view.rerender(<></>)
    expect(() => assertCachedRefreshWarning(body, reason)).toThrowError(WARNING_ORACLE_MESSAGE)
  })

  it('rejects a reason and Retry that silently describe retained Team data as fresh', () => {
    const { body, reason } = renderOracleFixture(false)
    expect(() => assertCachedRefreshWarning(body, reason)).toThrowError('warning must identify displayed data as last known, not fresh')
  })
})
