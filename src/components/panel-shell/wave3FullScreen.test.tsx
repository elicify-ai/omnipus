// PE1 RED: PANEL-EXPANSION-DECISION-20261005.md supersedes ONLY SP-38's
// Tasks/Team/Calendar same-tab exception. FR-008/009/013/018/019 and §8.3
// still require protected new tabs, current identity, visible failure and
// opener-owned restoration. Original registry and meaningful route cases stay.
// Real registry/shell/opener/presence/lifecycle/auth/routes/content; only the
// Window, channel, WebSocket, jsdom geometry and generated API PROCESS edges
// are controlled. Source→captured target→Back is unit integration, NOT native
// acceptance. GREEN, mutation and independent CHECK are deliberately deferred.
import { describe, it, expect, vi, beforeEach, afterEach, beforeAll } from 'vitest'
import { render, waitFor, screen, fireEvent, act, cleanup, within } from '@testing-library/react'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import type { PanelContext, PanelId, WorkspacePanelId } from './types'
import {
  apiEdges, signedInState, PanelChannelEdge, BrowserSocketEdge, popupEdge,
} from '../../../tests/fixtures/pe1-panel-process-edges'

vi.mock('@/lib/api', async (importOriginal) => {
  const { apiEdges } = await import('../../../tests/fixtures/pe1-panel-process-edges')
  return { ...(await importOriginal<typeof import('@/lib/api')>()), ...apiEdges }
})
vi.mock('@/lib/api/mail', async (importOriginal) => {
  const { mailApiEdges } = await import('../../../tests/fixtures/pe1-panel-process-edges')
  return { ...(await importOriginal<typeof import('@/lib/api/mail')>()), ...mailApiEdges }
})

import { panels, getPanelDefinition } from './registry'
import { SidePanelShell } from './SidePanelShell'
import { PanelTabPresenceBridge } from './PanelTabPresenceBridge'
import { usePanelShellStore } from './panelShellStore'
import { routeTree } from '@/routeTree.gen'
import {
  announcePanelTabPresence, getPanelTabHandleRegistry, panelIdentityKey, panelIdentityFromContext,
} from '@/lib/panelTabPresence'
import { announcePanelPopoutContext } from '@/lib/panelPopoutLifecycle'
import { consumeLoginReturn } from '@/routes/-loginReturn'
import { setLibraryEditorDirty, resolveDiscardConfirmDialog } from '@/components/library/preview/unsavedGuard'
import { setMailEditorDirty, resolveMailDiscardConfirmDialog } from '@/components/workspaces/mail/mailUnsavedGuard'

class RowResizeObserver {
  constructor(private readonly callback: ResizeObserverCallback) {}
  observe() {
    this.callback([{ contentRect: { width: 1280 } as DOMRectReadOnly } as ResizeObserverEntry], this as unknown as ResizeObserver)
  }
  unobserve() {}
  disconnect() {}
}

const clients: QueryClient[] = []
function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  clients.push(client)
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

const workspacePanels = [
  { id: 'tasks', title: 'Tasks', marker: 'tasks-heading' },
  { id: 'team', title: 'Team', marker: 'team-panel-fullscreen' },
  { id: 'calendar', title: 'Calendar', marker: 'calendar-toolbar' },
] as const satisfies readonly { id: WorkspacePanelId; title: string; marker: string }[]
const expansionCases = [
  ...workspacePanels.map(({ id, title }) => ({ id, title, context: { workspaceId: 'ws-1' } })),
  { id: 'library', title: 'Library', context: { workspaceId: 'ws-1', path: 'Current.txt' } },
  { id: 'mail', title: 'Mail', context: { workspaceId: 'ws-1', mailboxId: 'mia', folder: 'inbox', messageRef: 'uid:777:42' } },
  { id: 'browser', title: 'Browser', context: { sessionId: 'session-current', agentId: 'agent-current' } },
] satisfies { id: PanelId; title: string; context: PanelContext }[]

beforeAll(async () => {
  // Cold transpile is not the behaviour under test. Warm REAL lazy chunks.
  await Promise.all([
    import('@/components/workspaces/WorkspaceTasksTab'),
    import('@/components/workspaces/team/TeamPanel'),
    import('@/components/screens/CalendarScreen'),
    import('@/components/library/LibraryPanel'),
    import('@/components/workspaces/mail/MailPanel'),
    import('@/components/browser/BrowserLivePanel'),
  ])
})
beforeEach(() => {
  vi.clearAllMocks()
  apiEdges.fetchAppState.mockReset().mockResolvedValue(signedInState)
  vi.stubGlobal('ResizeObserver', RowResizeObserver)
  vi.stubGlobal('BroadcastChannel', PanelChannelEdge)
  vi.stubGlobal('WebSocket', BrowserSocketEdge)
  vi.stubGlobal('DOMMatrixReadOnly', class { m22 = 1 })
  vi.stubGlobal('scrollTo', vi.fn())
  Object.defineProperty(HTMLElement.prototype, 'offsetWidth', { configurable: true, value: 800 })
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, value: 600 })
  sessionStorage.clear()
  usePanelShellStore.setState({ activePanel: null, panelWidth: null, guardPending: false, historyPushed: false, toasts: [] })
  window.history.replaceState({}, '', '/#/workspaces/ws-1/chat')
})
afterEach(() => {
  cleanup() // the real bridge stops its owner/monitor before process edges reset
  for (const client of clients.splice(0)) client.clear()
  resolveDiscardConfirmDialog(false)
  setLibraryEditorDirty(false)
  resolveMailDiscardConfirmDialog(false)
  setMailEditorDirty('compose', false)
  setMailEditorDirty('draft', false)
  usePanelShellStore.getState().closePanel()
  PanelChannelEdge.reset()
  BrowserSocketEdge.reset()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function renderSource() {
  return render(<>
    <PanelTabPresenceBridge />
    <SidePanelShell panels={panels} username="dana" chat={<textarea data-testid="chat-input" defaultValue="PE1 draft survives" />} />
  </>, { wrapper })
}

async function realContent(id: PanelId, owner: HTMLElement = document.body) {
  const view = within(owner)
  if (id === 'tasks') {
    // SP-32/33/36: the BODY has all three task views, the status Board and
    // Plans. Its dynamic heading is not the shell's independent "Tasks" title.
    expect(await view.findByTestId('tasks-heading')).toBeInTheDocument()
    expect(within(view.getByRole('radiogroup', { name: /^Task view$/ })).getAllByRole('radio').map((control) => control.textContent)).toEqual(['Board', 'List', 'Graph'])
    expect(view.getByTestId('tasks-view-board')).toHaveAttribute('aria-checked', 'true')
    expect((await view.findAllByRole('group', { name: / column$/ })).map((group) => group.getAttribute('aria-label'))).toEqual([
      'Inbox column', 'Next column', 'In Progress column', 'Blocked column', 'Done column', 'Failed column',
    ])
    expect(view.getByRole('heading', { name: /^Plans$/ })).toBeInTheDocument()
  } else if (id === 'team') {
    expect(await view.findByRole('heading', { name: /^Team & delegation$/ })).toBeInTheDocument()
  } else if (id === 'calendar') {
    expect(await view.findByTestId('calendar-toolbar')).toBeInTheDocument()
    expect(view.getByTestId('calendar-grid')).toBeInTheDocument()
  } else if (id === 'library') {
    expect(await view.findByTestId('library-row-Current.txt')).toHaveTextContent('Current.txt')
  } else if (id === 'mail') {
    expect(await view.findByText('PE1 Mail selection')).toBeInTheDocument()
    expect(view.getByRole('tab', { name: /^Inbox$/ })).toHaveAttribute('aria-selected', 'true')
  } else {
    // A jsdom process edge has no decoded video stream. Verify REAL viewer
    // controls and concrete session attachment, not a fabricated video/root.
    expect(await view.findByRole('textbox', { name: /^Address bar$/ })).toBeInTheDocument()
    await waitFor(() => expect(view.getByRole('button', { name: /^Refresh page$/ })).toBeEnabled())
    expect(view.getByRole('button', { name: /^Go back$/ })).toBeEnabled()
    expect(view.getByRole('button', { name: /^Stop loading$/ })).toBeEnabled()
    await waitFor(() => expect(BrowserSocketEdge.sockets.flatMap((socket) => socket.sent)).toContainEqual({
      type: 'browser_attach', input_mode: 'dedicated', session_id: 'session-current', agent_id: 'agent-current',
    }))
  }
  expect(view.queryByText('Something went wrong')).not.toBeInTheDocument()
  expect(view.queryByText(/can't open this panel/i)).not.toBeInTheDocument()
}

async function openDock(id: PanelId, context: PanelContext) {
  renderSource()
  act(() => {
    if (id === 'browser' && context.sessionId) usePanelShellStore.getState().openPanel(id, context)
    else usePanelShellStore.getState().openPanel(id as WorkspacePanelId, context as { workspaceId?: string })
  })
  const expectedTitle = expansionCases.find((entry) => entry.id === id)?.title
  if (!expectedTitle) throw new Error(`BLOCKED: missing independent ${id} title expectation — PE1`)
  expect(within(screen.getByTestId('side-panel-header')).getByText(expectedTitle, { exact: true })).toBeVisible()
  await realContent(id)
  if (id === 'library') {
    fireEvent.click(screen.getByTestId('library-row-Current.txt'))
    expect(await screen.findByText('PE1 Library selection')).toBeInTheDocument()
  }
  expect(usePanelShellStore.getState().activePanel).toEqual({ id, context })
  return { chat: screen.getByTestId('chat-input'), path: window.location.hash.split('?')[0] }
}

function generatedTarget(child: ReturnType<typeof popupEdge>, id: PanelId, context: PanelContext) {
  expect(child.location.replace, 'the REAL opener must generate the child destination').toHaveBeenCalledTimes(1)
  const href = child.location.replace.mock.calls[0]?.[0]
  expect(typeof href).toBe('string')
  if (!href) throw new Error('BLOCKED: Expand did not produce a target URL — PE1 / FR-008')
  const hash = new URL(href, window.location.href).hash
  expect(hash.split('?')[0]).toBe(`#/panel/${id}`)
  const search = new URLSearchParams(hash.split('?')[1])
  const popout = search.get('popout')
  expect(popout, 'FR-008: source-owned child identity must be carried').toMatch(/^[A-Za-z0-9-]+$/)
  search.delete('popout')
  const definition = getPanelDefinition(id)
  if (!definition) throw new Error(`BLOCKED: ${id} registration missing — PE1`)
  const decoded = definition.fullScreen.fromSearch(Object.fromEntries(search))
  expect(decoded, 'current context must survive the ACTUAL generated child URL').toEqual(context)
  expect(Object.fromEntries(search)).toEqual(definition.fullScreen.toSearch(context))
  return { href: hash.slice(1), popout: popout! }
}

function retainedHandle(id: PanelId, context: PanelContext, child: ReturnType<typeof popupEdge>) {
  const identity = panelIdentityFromContext(id, context)
  expect(identity).not.toBeNull()
  if (!identity) throw new Error('BLOCKED: invalid fixture identity — FR-009')
  expect(getPanelTabHandleRegistry().get(panelIdentityKey(identity))).toBe(child)
}

// RETAINED: original SP-6 registry inventory and real direct-link content.
describe.each(workspacePanels)('wave-3 registry — $title is a registered panel (SP-6)', ({ id, title }) => {
  it(`the production registry defines the ${id} panel with title "${title}"`, () => {
    const definition = getPanelDefinition(id)
    expect(definition).toBeDefined()
    expect(definition?.title).toBe(title)
    expect(definition?.content).toBeDefined()
  })
  it(`the ${id} panel participates in the same registry array as library/browser`, () => {
    expect(panels.some((panel) => panel.id === id)).toBe(true)
  })
})
describe.each(workspacePanels)('wave-3 full screen — $title retains the chrome-less Back route', ({ id }) => {
  it(`navigating to /panel/${id}?workspace=… renders the chrome-less panel shell with "Back to chat"`, async () => {
    const history = createMemoryHistory({ initialEntries: [`/panel/${id}?workspace=ws-1`] })
    const router = createRouter({ routeTree, history })
    const mounted = render(<RouterProvider router={router} />, { wrapper })
    await waitFor(() => expect(router.state.status).toBe('idle'), { timeout: 5_000 })
    expect(mounted.getByTestId('fullscreen-panel')).toBeInTheDocument()
    expect(mounted.getByRole('button', { name: /back to chat/i })).toBeInTheDocument()
    expect(mounted.queryByTestId('workspace-tab-strip')).not.toBeInTheDocument()
    expect(mounted.queryByText(/can't open this panel/i)).not.toBeInTheDocument()
    await realContent(id, mounted.container)
    expect(apiEdges.fetchAppState).toHaveBeenCalled()
  })
})

describe.each(expansionCases)('PE1 — actual $title Expand uses the protected NEW-tab path', ({ id, title, context }) => {
  it(`${id} source Expand opens NEW tab with current identity, severs opener and closes only the dock`, async () => {
    const child = popupEdge()
    const open = vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window)
    const baseline = await openDock(id, context)
    fireEvent.click(screen.getByRole('button', { name: `Expand ${title} panel` }))
    if (id === 'tasks' || id === 'team' || id === 'calendar' || id === 'browser') {
      // No awaited core mock: this assertion is in the actual gesture's tick.
      expect(open, `PE1 ${title}: actual source Expand must open a NEW tab synchronously`).toHaveBeenCalledExactlyOnceWith('about:blank', '_blank')
    }
    await waitFor(() => expect(child.location.replace).toHaveBeenCalledTimes(1))
    expect(open).toHaveBeenCalledExactlyOnceWith('about:blank', '_blank')
    generatedTarget(child, id, context)
    expect(child.opener).toBeNull()
    retainedHandle(id, context, child)
    expect(child.close).not.toHaveBeenCalled()
    expect(usePanelShellStore.getState().activePanel).toBeNull()
    expect(screen.queryByTestId('side-panel')).not.toBeInTheDocument()
    expect(screen.getByTestId('chat-input')).toBe(baseline.chat)
    expect(baseline.chat).toHaveValue('PE1 draft survives')
    expect(window.location.hash.split('?')[0]).toBe(baseline.path)
  })

  for (const failure of ['null', 'throw'] as const) {
    it(`${id} ${failure} window.open retains dock/context and shows the specific visible failure`, async () => {
      const error = new DOMException('opening denied', 'SecurityError')
      const logged = vi.spyOn(console, 'error') // call-through; not suppression
      const open = vi.spyOn(window, 'open').mockImplementation(() => {
        if (failure === 'throw') throw error
        return null
      })
      const baseline = await openDock(id, context)
      fireEvent.click(screen.getByRole('button', { name: `Expand ${title} panel` }))
      await waitFor(() => expect(screen.getByTestId('panel-expand-error')).toHaveTextContent(
        failure === 'null' ? 'Pop-up blocked — allow pop-ups to expand.' : 'Could not expand this panel. Try again.',
      ))
      expect(open).toHaveBeenCalledExactlyOnceWith('about:blank', '_blank')
      expect(usePanelShellStore.getState().activePanel).toEqual({ id, context })
      expect(screen.getByTestId('side-panel')).toBeInTheDocument()
      await realContent(id)
      expect(screen.getByTestId('chat-input')).toBe(baseline.chat)
      expect(baseline.chat).toHaveValue('PE1 draft survives')
      expect(window.location.hash.split('?')[0]).toBe(baseline.path)
      expect(screen.getByTestId('panel-expand-error')).toBeVisible()
      // Q1 B (squad ruling): application ToastContainer is not a public kit
      // export. Assert the REAL toast signal; native AppShell must separately
      // prove its visibility. This never substitutes for the visible inline error.
      expect(usePanelShellStore.getState().toasts.map((toast) => ({ message: toast.message, variant: toast.variant }))).toEqual([{
        message: failure === 'null'
          ? `${title} was blocked. Allow pop-ups and try again.`
          : `${title} could not open full screen. The panel remains here.`,
        variant: 'error',
      }])
      if (failure === 'throw') expect(logged).toHaveBeenCalledWith('[side-panel] Expand failed while opening a tab', error)
    })
  }
})

describe.each(workspacePanels)('PE1 — $title child, ownership and current scope', ({ id, title }) => {
  it(`${id} actual generated child renders authenticated meaningful content; Back restores its source`, async () => {
    const context = { workspaceId: 'ws-1' }
    const child = popupEdge()
    const open = vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window)
    const baseline = await openDock(id, context)
    fireEvent.click(screen.getByRole('button', { name: `Expand ${title} panel` }))
    expect(open, `PE1 ${title}: generated child requires a real source NEW-tab open`).toHaveBeenCalledExactlyOnceWith('about:blank', '_blank')
    const target = generatedTarget(child, id, context)
    const history = createMemoryHistory({ initialEntries: [target.href] })
    const router = createRouter({ routeTree, history })
    const mountedChild = render(<RouterProvider router={router} />, { wrapper })
    await realContent(id, mountedChild.container)
    expect(mountedChild.getByTestId('fullscreen-panel')).toBeInTheDocument()
    expect(mountedChild.queryByTestId('workspace-top-bar')).not.toBeInTheDocument()
    expect(apiEdges.fetchAppState).toHaveBeenCalled()
    expect(router.state.location.pathname).toBe(`/panel/${id}`)
    expect(router.state.location.search).toEqual({ workspace: 'ws-1', popout: target.popout })
    vi.stubGlobal('closed', false)
    const close = vi.spyOn(window, 'close').mockImplementation(() => {
      child.close()
      vi.stubGlobal('closed', child.closed)
    })
    fireEvent.click(mountedChild.getByRole('button', { name: 'Back to chat' }))
    await waitFor(() => expect(close).toHaveBeenCalledTimes(1))
    expect(child.closed).toBe(true)
    mountedChild.unmount() // unit Window boundary; real native close is a separate gate
    await waitFor(() => expect(usePanelShellStore.getState().activePanel).toEqual({ id, context }))
    await realContent(id)
    expect(screen.getByTestId('chat-input')).toBe(baseline.chat)
    expect(baseline.chat).toHaveValue('PE1 draft survives')
    expect(window.location.hash.split('?')[0]).toBe(baseline.path)
    expect(getPanelTabHandleRegistry().has(`${id}:ws-1`)).toBe(false)
  })

  it(`${id} signed-out generated child cannot bypass auth and preserves the exact return target`, async () => {
    const child = popupEdge()
    const open = vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window)
    await openDock(id, { workspaceId: 'ws-1' })
    fireEvent.click(screen.getByRole('button', { name: `Expand ${title} panel` }))
    expect(open, `PE1 ${title}: auth is checked on the generated NEW child, not a preset URL`).toHaveBeenCalledExactlyOnceWith('about:blank', '_blank')
    const target = generatedTarget(child, id, { workspaceId: 'ws-1' })
    apiEdges.fetchAppState.mockResolvedValue({
      ...signedInState, identity: { ...signedInState.identity, signed_in: false, blocked_reason: 'signed_out' },
    })
    const history = createMemoryHistory({ initialEntries: [target.href] })
    const router = createRouter({ routeTree, history })
    const mounted = render(<RouterProvider router={router} />, { wrapper })
    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(mounted.queryByTestId('fullscreen-panel')).not.toBeInTheDocument()
    expect(consumeLoginReturn()).toBe(target.href)
    expect(apiEdges.fetchAppState).toHaveBeenCalled()
  })

  it(`${id} handoff exception closes the failed child, restores the source and forgets its handle`, async () => {
    const child = popupEdge()
    const error = new DOMException('navigation denied', 'SecurityError')
    child.location.replace.mockImplementation(() => { throw error })
    const logged = vi.spyOn(console, 'error')
    const open = vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window)
    const baseline = await openDock(id, { workspaceId: 'ws-1' })
    fireEvent.click(screen.getByRole('button', { name: `Expand ${title} panel` }))
    expect(open, 'PE1: even a failed handoff starts through the protected new-tab opener').toHaveBeenCalledExactlyOnceWith('about:blank', '_blank')
    await waitFor(() => expect(screen.getByTestId('panel-expand-error')).toHaveTextContent('Could not expand this panel. Try again.'))
    expect(child.close).toHaveBeenCalledTimes(1)
    expect(getPanelTabHandleRegistry().has(`${id}:ws-1`)).toBe(false)
    expect(usePanelShellStore.getState().activePanel).toEqual({ id, context: { workspaceId: 'ws-1' } })
    expect(screen.getByTestId('chat-input')).toBe(baseline.chat)
    expect(logged).toHaveBeenCalledWith('[side-panel] Expand handoff failed', error)
    await realContent(id)
  })

  it(`${id} same-scope re-entry focuses the retained child without opening or re-navigating it`, async () => {
    const child = popupEdge()
    const open = vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window)
    await openDock(id, { workspaceId: 'ws-1' })
    fireEvent.click(screen.getByRole('button', { name: `Expand ${title} panel` }))
    expect(open, 'PE1: reuse must follow an actual first Expand').toHaveBeenCalledExactlyOnceWith('about:blank', '_blank')
    generatedTarget(child, id, { workspaceId: 'ws-1' })
    act(() => usePanelShellStore.getState().openPanel(id, { workspaceId: 'ws-1' }))
    expect(child.focus).toHaveBeenCalledTimes(1)
    expect(open).toHaveBeenCalledTimes(1)
    expect(child.location.replace).toHaveBeenCalledTimes(1)
    expect(usePanelShellStore.getState().activePanel).toBeNull()
    expect(screen.queryByTestId('side-panel')).not.toBeInTheDocument()
  })

  it(`${id} child scope A→B reuses only B, never yanks B back; A can open a different child`, async () => {
    const first = popupEdge()
    const second = popupEdge()
    const open = vi.spyOn(window, 'open').mockReturnValueOnce(first as unknown as Window).mockReturnValueOnce(second as unknown as Window)
    await openDock(id, { workspaceId: 'ws-1' })
    fireEvent.click(screen.getByRole('button', { name: `Expand ${title} panel` }))
    expect(open, 'PE1: current-scope test begins with a real Expand').toHaveBeenCalledExactlyOnceWith('about:blank', '_blank')
    const target = generatedTarget(first, id, { workspaceId: 'ws-1' })
    await act(async () => announcePanelPopoutContext(id, target.popout, { workspaceId: 'workspace-current' }))
    expect(getPanelTabHandleRegistry().has(`${id}:ws-1`)).toBe(false)
    retainedHandle(id, { workspaceId: 'workspace-current' }, first)
    act(() => usePanelShellStore.getState().openPanel(id, { workspaceId: 'workspace-current' }))
    expect(first.focus).toHaveBeenCalledTimes(1)
    expect(first.location.replace).toHaveBeenCalledTimes(1)
    expect(open).toHaveBeenCalledTimes(1)
    act(() => usePanelShellStore.getState().openPanel(id, { workspaceId: 'ws-1' }))
    await realContent(id)
    fireEvent.click(screen.getByRole('button', { name: `Expand ${title} panel` }))
    expect(open).toHaveBeenCalledTimes(2)
    const next = generatedTarget(second, id, { workspaceId: 'ws-1' })
    expect(next.popout).not.toBe(target.popout)
    retainedHandle(id, { workspaceId: 'ws-1' }, second)
    retainedHandle(id, { workspaceId: 'workspace-current' }, first)
    expect(first.location.replace).toHaveBeenCalledTimes(1)
  })

  it(`${id} manual-tab presence prevents duplication, emits the exact Switch affordance and clears on leave`, async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const focus = vi.spyOn(window, 'focus').mockImplementation(() => {})
    renderSource()
    const manual = announcePanelTabPresence({ panelId: id, workspaceId: 'ws-1' })
    await act(async () => {}) // browser channel delivery, not a core-presence mock
    act(() => usePanelShellStore.getState().openPanel(id, { workspaceId: 'ws-1' }))
    // Q1 B coverage transfer: exact production STORE affordance + its real
    // action here; VISIBLE AppShell toast/Switch is a mandatory native row,
    // currently UNVERIFIED, never a store-only visibility PASS.
    expect(usePanelShellStore.getState().toasts.map((toast) => ({ message: toast.message, variant: toast.variant, actionLabel: toast.action?.label }))).toEqual([{
      message: `${title} is already open in another tab — switch.`, variant: 'default', actionLabel: 'Switch',
    }])
    const switchAction = usePanelShellStore.getState().toasts[0]?.action
    expect(switchAction).toEqual({ label: 'Switch', onClick: expect.any(Function) })
    expect(screen.queryByTestId('side-panel')).not.toBeInTheDocument()
    expect(open).not.toHaveBeenCalled()
    act(() => switchAction!.onClick())
    await waitFor(() => expect(focus).toHaveBeenCalledTimes(1))
    await act(async () => manual.stop())
    act(() => usePanelShellStore.getState().openPanel(id, { workspaceId: 'ws-1' }))
    await realContent(id)
    expect(usePanelShellStore.getState().activePanel).toEqual({ id, context: { workspaceId: 'ws-1' } })
    expect(open).not.toHaveBeenCalled()
  })
})

// Positive guard controls: real registry beforeLeave + real discard dialog.
// Dirty setters arrange editor-state input; no guard result is stubbed.
for (const id of ['library', 'mail'] as const) {
  it(`PE1 legacy ${id} unsaved cancel prevents Expand and retains selection`, async () => {
    const entry = expansionCases.find((item) => item.id === id)!
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    await openDock(id, entry.context)
    act(() => {
      if (id === 'library') setLibraryEditorDirty(true)
      else setMailEditorDirty('compose', true)
    })
    fireEvent.click(screen.getByRole('button', { name: `Expand ${entry.title} panel` }))
    const dialog = await screen.findByRole('alertdialog', { name: 'Discard unsaved changes?' })
    expect(open).not.toHaveBeenCalled()
    expect(usePanelShellStore.getState().activePanel).toEqual({ id, context: entry.context })
    fireEvent.click(within(dialog).getByRole('button', { name: /^Cancel$/ }))
    await waitFor(() => expect(usePanelShellStore.getState().guardPending).toBe(false))
    expect(open).not.toHaveBeenCalled()
    expect(usePanelShellStore.getState().activePanel).toEqual({ id, context: entry.context })
    await realContent(id)
  })
}
