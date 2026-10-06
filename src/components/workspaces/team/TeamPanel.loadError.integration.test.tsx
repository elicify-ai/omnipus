// PANEL-TEAM-LOAD-ERROR-RED-1758 — approved cold-cache Team recovery requirement.
// Oracle: the supplied ApiError reason stays visible with Retry, and a later
// valid API response restores the documented Team editor without a page reload.
// docs/tutorial.md step 7 names "Team & delegation"; docs/workspaces.md says
// every team agent appears as a node. Expected names below come from INPUT.
// REAL: registry, dock shell, authenticated fullscreen route, TeamPanel,
// WorkspaceTeamTab, context, stores, graph, controls, QueryClient/default retries.
// FAKED: API process edges only, plus missing jsdom layout primitives. No hook
// mocks, cache seeds, error-boundary throws, content stand-ins or retry overrides.
// GREEN, mutation probes and independent integrity CHECK are deferred to CHECK.

import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ApiError } from '@/lib/api-error'
import type { AppState, Workspace, WorkspaceDelegation } from '@/lib/api/generated/openapi-types'
import { queryClient as applicationQueryClient } from '@/lib/queryClient'
import { workspacesQueryKeys } from '@/lib/api'
import { makeAgent } from '@/test/factories'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { SidePanelShell } from '@/components/panel-shell/SidePanelShell'
import { panels } from '@/components/panel-shell/registry'
import { routeTree } from '@/routeTree.gen'

const api = vi.hoisted(() => ({
  fetchWorkspace: vi.fn<typeof import('@/lib/api').fetchWorkspace>(),
  fetchWorkspaceDelegation: vi.fn<typeof import('@/lib/api').fetchWorkspaceDelegation>(),
  fetchAgents: vi.fn<typeof import('@/lib/api').fetchAgents>(),
  fetchAppState: vi.fn<typeof import('@/lib/api').fetchAppState>(),
  // AgentProfile remains REAL; its closed editor still starts these API reads.
  fetchProviders: vi.fn<typeof import('@/lib/api').fetchProviders>(),
  fetchActivity: vi.fn<typeof import('@/lib/api').fetchActivity>(),
  fetchSkills: vi.fn<typeof import('@/lib/api').fetchSkills>(),
  fetchPerformanceSettings: vi.fn<typeof import('@/lib/api').fetchPerformanceSettings>(),
}))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  ...api,
}))

const WORKSPACE_ID = 'team-recovery-ws'
const MEMBER = makeAgent({ id: 'recovery-member', name: 'Recovery teammate' })
const WORKSPACE: Workspace = {
  id: WORKSPACE_ID, name: 'Recovery workspace', revision: '2'.repeat(64),
  status: 'active', pinned: false, pin_order: 0, task_count: 0,
  core_team: [MEMBER.id], created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z',
}
const DELEGATION: WorkspaceDelegation = {
  workspace_id: WORKSPACE_ID, revision: WORKSPACE.revision,
  team: [MEMBER.id], edges: [], default_depth: 3,
}
const SIGNED_IN: AppState = {
  onboarding_complete: true,
  identity: { mode: 'local', edition: 'core', signed_in: true, blocked_reason: 'none' },
}
// not-wire-format: case metadata for the approved HTTP/transport classes.
const failures = [
  { label: '403 forbidden', status: 403, reason: 'Access to this workspace was denied.', attempts: 1 },
  { label: '404 missing workspace', status: 404, reason: 'This workspace could not be found.', attempts: 1 },
  { label: '500 after automatic retries', status: 500, reason: 'The workspace server is unavailable.', attempts: 4 },
  { label: 'network after automatic retries', status: 0, reason: 'Network unavailable. Check your connection.', attempts: 4 },
] as const
// 1 initial request + 3 automatic retries for network/500; 403/404 never
// automatically retry (documented applicationQueryClient default policy).
// Real backoff is 1s + 2s + 4s. This bounded wait observes completion; it does
// NOT assert elapsed time, speed up the clock, or change retry configuration.
const RETRY_COMPLETION_WAIT_MS = 10_000
const clients: QueryClient[] = []
const dimensions = ['offsetWidth', 'offsetHeight', 'clientWidth', 'clientHeight'] as const
const originalDimensions = dimensions.map((key) => [
  key, Object.getOwnPropertyDescriptor(HTMLElement.prototype, key),
] as const)

function deferredWorkspace() {
  let resolve!: (workspace: Workspace) => void
  const promise = new Promise<Workspace>((accept) => { resolve = accept })
  return { promise, resolve }
}

beforeAll(async () => {
  // Warm the REAL lazy consumer, so the shell's chunk-loading Suspense fallback
  // cannot masquerade as workspace-request loading feedback in the pending test.
  await import('./TeamPanel')
})

beforeEach(() => {
  for (const edge of Object.values(api)) edge.mockReset()
  api.fetchWorkspace.mockResolvedValue(WORKSPACE)
  api.fetchWorkspaceDelegation.mockResolvedValue(DELEGATION)
  api.fetchAgents.mockResolvedValue([MEMBER])
  api.fetchAppState.mockResolvedValue(SIGNED_IN)
  api.fetchProviders.mockResolvedValue([])
  api.fetchActivity.mockResolvedValue({ events: [] })
  api.fetchSkills.mockResolvedValue([])
  api.fetchPerformanceSettings.mockResolvedValue({ max_tool_iterations: 50 })
  useUiStore.setState({
    activePanel: null, panelWidth: null, guardPending: false, historyPushed: false,
    editAgentId: null, editAgentWorkspaceId: null, toasts: [],
  })
  // Deliberately different from the supplied workspace: requests must use the
  // panel/URL scope, never silently borrow the unrelated active workspace.
  useWorkspacesStore.setState({ activeWorkspaceId: 'unrelated-workspace' })
  window.history.replaceState({}, '', `/#/workspaces/${WORKSPACE_ID}/chat`)

  // jsdom has no layout engine. These are browser geometry adapters, not
  // product component/hook replacements; graph and panel logic stay real.
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

async function mountSurface(presentation: 'docked' | 'fullscreen') {
  const client = new QueryClient({ defaultOptions: applicationQueryClient.getDefaultOptions() })
  clients.push(client)
  expect(client.getQueryCache().getAll(), 'consumer must start with an actually cold cache').toEqual([])
  const provider = (content: React.ReactNode) => <QueryClientProvider client={client}>{content}</QueryClientProvider>
  let router: ReturnType<typeof createRouter> | undefined
  if (presentation === 'docked') {
    render(provider(<SidePanelShell
      panels={panels} username="team-recovery-test"
      chat={<textarea aria-label="Chat draft" defaultValue="Draft survives Team recovery" />}
    />))
    act(() => { useUiStore.getState().openPanel('team', { workspaceId: WORKSPACE_ID }) })
  } else {
    router = createRouter({
      routeTree,
      history: createMemoryHistory({ initialEntries: [`/panel/team?workspace=${WORKSPACE_ID}`] }),
    })
    render(provider(<RouterProvider router={router} />))
  }
  // A workspace API call is only made after the REAL lazy TeamPanel mounts;
  // this also excludes the shell's unrelated chunk-loading status as evidence.
  await waitFor(() => expect(api.fetchWorkspace).toHaveBeenCalledWith(WORKSPACE_ID))
  await act(async () => { await Promise.resolve() })
  return { client, router }
}

function assertShellRetained(presentation: 'docked' | 'fullscreen', router?: ReturnType<typeof createRouter>) {
  if (presentation === 'docked') {
    expect(screen.getByRole('complementary', { name: 'Team' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Close Team' })).toBeEnabled()
    expect(screen.getByRole('textbox', { name: 'Chat draft' })).toHaveValue('Draft survives Team recovery')
    expect(useUiStore.getState().activePanel).toEqual({ id: 'team', context: { workspaceId: WORKSPACE_ID } })
  } else {
    expect(screen.getByRole('button', { name: 'Back to chat' })).toBeEnabled()
    expect(router?.state.location.pathname).toBe('/panel/team')
    expect(router?.state.location.search).toEqual({ workspace: WORKSPACE_ID })
    expect(router?.state.matches.map((match) => match.routeId)).toContain('/_fullscreen/panel/$panelId')
    expect(api.fetchAppState).toHaveBeenCalledWith()
    expect(screen.queryByTestId('app-shell')).not.toBeInTheDocument()
  }
  expect(api.fetchWorkspace.mock.calls.every(([id]) => id === WORKSPACE_ID), 'all requests retain the supplied workspace').toBe(true)
}

async function assertMeaningfulTeam(client: QueryClient) {
  expect(await screen.findByRole('heading', { name: 'Team & delegation' })).toBeVisible()
  expect(await screen.findByText(MEMBER.name, { exact: true })).toBeVisible()
  expect(screen.getByRole('button', { name: 'Add agent' })).toBeEnabled()
  expect(api.fetchWorkspaceDelegation).toHaveBeenCalledExactlyOnceWith(WORKSPACE_ID)
  // fetchAgents takes no business input, but Query calls its queryFn with
  // framework metadata. Target the real result and rendered roster instead
  // of mistaking that incidental callback argument for an API requirement.
  expect(api.fetchAgents).toHaveBeenCalledTimes(1)
  expect(client.getQueryData(['agents'])).toEqual([MEMBER])
  // Select actual graph nodes, not nested Delegate/Remove action test IDs.
  const nodeIds = Array.from(
    screen.getByTestId('team-graph-canvas').querySelectorAll('.react-flow__node[data-id]'),
    (node) => node.getAttribute('data-id'),
  )
  expect(nodeIds).toEqual([MEMBER.id])
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
}

describe.each(['docked', 'fullscreen'] as const)('Team cold-cache recovery — %s', (presentation) => {
  it('shows loading while the real consumer waits for a cold workspace request, not a permanent blank body', async () => {
    const pending = deferredWorkspace()
    api.fetchWorkspace.mockReturnValue(pending.promise)
    const { client, router } = await mountSurface(presentation)
    expect(client.getQueryState(workspacesQueryKeys.detail(WORKSPACE_ID))?.fetchStatus).toBe('fetching')
    expect(client.getQueryData(workspacesQueryKeys.detail(WORKSPACE_ID))).toBeUndefined()
    assertShellRetained(presentation, router)
    expect(screen.queryByRole('heading', { name: 'Team & delegation' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
    const panelBody = presentation === 'docked'
      ? screen.getByRole('complementary', { name: 'Team' })
      : screen.getByTestId('fullscreen-panel')
    const waiting = within(panelBody).queryAllByRole('status').filter((status) =>
      /loading|waiting/i.test(status.textContent ?? ''),
    )
    expect(waiting, 'the actual panel body must show exactly one loading status while its workspace request is pending').toHaveLength(1)
    expect(waiting[0]).toBeVisible()
  })

  it.each(failures)('keeps $label visible with its reason and Retry; a later valid response restores the same workspace Team without reload', async ({ status, reason, attempts }) => {
    const failure = new ApiError(status, reason)
    api.fetchWorkspace.mockRejectedValue(failure)
    const { client, router } = await mountSurface(presentation)
    await waitFor(() => expect(client.getQueryState(workspacesQueryKeys.detail(WORKSPACE_ID))?.status).toBe('error'), {
      timeout: RETRY_COMPLETION_WAIT_MS,
    })
    // Reading real query state proves the intended ApiError path executed. It
    // never writes isError/data, catches a render throw, or manufactures cache.
    const queryError = client.getQueryState(workspacesQueryKeys.detail(WORKSPACE_ID))?.error
    expect(queryError).toBe(failure)
    expect(queryError).toBeInstanceOf(ApiError)
    expect((queryError as ApiError).status).toBe(status)
    expect(api.fetchWorkspace.mock.calls).toEqual(Array.from({ length: attempts }, () => [WORKSPACE_ID]))
    assertShellRetained(presentation, router)
    expect(screen.queryByRole('heading', { name: 'Team & delegation' })).not.toBeInTheDocument()
    const alert = screen.getByRole('alert')
    expect(alert, 'the failure reason belongs in a visible alert, not an empty dock or toast-only signal').toHaveTextContent(reason)
    expect(alert).toBeVisible()
    const retry = within(alert).getByRole('button', { name: 'Retry' })
    expect(retry).toBeEnabled()

    const later = deferredWorkspace()
    api.fetchWorkspace.mockReturnValue(later.promise)
    const addressBeforeRetry = window.location.href
    fireEvent.click(retry)
    await waitFor(() => expect(api.fetchWorkspace).toHaveBeenCalledTimes(attempts + 1))
    expect(api.fetchWorkspace).toHaveBeenLastCalledWith(WORKSPACE_ID)
    expect(client.getQueryState(workspacesQueryKeys.detail(WORKSPACE_ID))?.fetchStatus).toBe('fetching')
    expect(screen.getByRole('alert'), 'recovery must not clear the reason into a blank body while Retry waits').toHaveTextContent(reason)
    assertShellRetained(presentation, router)
    await act(async () => { later.resolve(WORKSPACE); await later.promise })
    await assertMeaningfulTeam(client)
    expect(client.getQueryState(workspacesQueryKeys.detail(WORKSPACE_ID))?.status).toBe('success')
    expect(window.location.href, 'Retry must recover in place, not navigate/reload').toBe(addressBeforeRetry)
    expect(api.fetchWorkspace.mock.calls).toEqual(Array.from({ length: attempts + 1 }, () => [WORKSPACE_ID]))
    assertShellRetained(presentation, router)
  })

  it('renders real Team members after a successful cold response, with no false error or Retry state', async () => {
    const { client, router } = await mountSurface(presentation)
    await assertMeaningfulTeam(client)
    expect(api.fetchWorkspace).toHaveBeenCalledExactlyOnceWith(WORKSPACE_ID)
    expect(client.getQueryState(workspacesQueryKeys.detail(WORKSPACE_ID))?.status).toBe('success')
    expect(client.getQueryState(workspacesQueryKeys.detail(WORKSPACE_ID))?.error).toBeNull()
    assertShellRetained(presentation, router)
  })
})
