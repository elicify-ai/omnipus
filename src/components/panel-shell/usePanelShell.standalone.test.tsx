// Oracle: founder E.1–E.3. Native Chrome --app probe reports standalone=true.
// This is a hook-level handoff/guard pack with a fixture codec/destination,
// not production route acceptance. The real registry/content/route round trip
// is covered by StandalonePanelRoundTrip.realRoute.test.tsx. Here the hook,
// store and router remain real; matchMedia/popup/navigation faults are edges.
import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react'
import { ToastContainer } from '@/components/ui/toast-container'
import { createMemoryHistory, createRootRoute, createRoute, createRouter, Outlet, RouterProvider } from '@tanstack/react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { discardPanelPopout } from '@/lib/panelPopoutLifecycle'
import { usePanelShellStore } from './panelShellStore'
import type { PanelContext, PanelDefinition } from './types'
import { usePanelShell } from './usePanelShell'

const appModes = ['standalone', 'minimal-ui', 'window-controls-overlay', 'fullscreen'] as const
const context = { workspaceId: 'ws-1', path: 'Notes/Current & next.md' }
const child = {
  closed: false, opener: window,
  location: { replace: vi.fn() }, close: vi.fn(), focus: vi.fn(),
} as unknown as Window

function displayMode(mode: string | null) {
  vi.stubGlobal('matchMedia', vi.fn((query: string) => ({
    matches: mode !== null && query === `(display-mode: ${mode})`, media: query,
    onchange: null, addListener: vi.fn(), removeListener: vi.fn(),
    addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn(),
  })))
}

function definition(id: 'library' | 'mail' = 'library', beforeLeave?: () => Promise<boolean>): PanelDefinition {
  return {
    id, title: id === 'library' ? 'Library' : 'Mail', content: () => null,
    fullScreen: {
      toSearch: (current: PanelContext) => ({ workspace: current.workspaceId ?? '', path: current.path ?? '' }),
      fromSearch: () => null,
    },
    ...(beforeLeave ? { beforeLeave } : {}),
  }
}

async function renderSource(panel = definition()) {
  let shell: ReturnType<typeof usePanelShell> | undefined
  function Source() {
    shell = usePanelShell([panel], 'dana')
    return <><div>Chat source</div><ToastContainer /></>
  }
  const root = createRootRoute({ component: Outlet })
  const chat = createRoute({ getParentRoute: () => root, path: '/workspaces/$workspaceId/chat', component: Source })
  const fullscreen = createRoute({
    getParentRoute: () => root, path: '/panel/$panelId',
    validateSearch: (search) => search as Record<string, unknown>,
    component: () => <div>Full-screen destination</div>,
  })
  const router = createRouter({
    routeTree: root.addChildren([chat, fullscreen]),
    history: createMemoryHistory({ initialEntries: ['/workspaces/ws-1/chat'] }),
  })
  render(<RouterProvider router={router} />)
  await waitFor(() => expect(shell).toBeDefined())
  return { router, expand: (getter?: () => PanelContext) => shell!.requestExpand(getter) }
}

beforeEach(() => {
  displayMode(null)
  vi.spyOn(window, 'open').mockReturnValue(child)
  vi.mocked(child.location.replace).mockClear()
  usePanelShellStore.setState({ activePanel: null, guardPending: false, historyPushed: false, toasts: [] })
})
afterEach(() => {
  cleanup()
  discardPanelPopout({ panelId: 'library', workspaceId: 'ws-1' }, child)
  discardPanelPopout({ panelId: 'mail', workspaceId: 'ws-1' }, child)
  usePanelShellStore.getState().closePanel()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('E.1 installed-app Expand stays in this window', () => {
  it.each(appModes)('%s navigates to the existing full-screen route without opening a tab/window', async (mode) => {
    displayMode(mode)
    usePanelShellStore.getState().openPanel('library', context)
    const { router, expand } = await renderSource()
    await act(async () => { await expect(expand()).resolves.toBe('opened') })
    expect(window.open).not.toHaveBeenCalled()
    expect(router.state.location.pathname).toBe('/panel/library')
    expect(router.state.location.search).toEqual({ workspace: 'ws-1', path: 'Notes/Current & next.md' })
    expect(usePanelShellStore.getState().activePanel).toBeNull()
  })

  it('normal browser mode retains the blank-tab handoff and exact panel context', async () => {
    usePanelShellStore.getState().openPanel('library', context)
    const { router, expand } = await renderSource()
    await act(async () => { await expect(expand()).resolves.toBe('opened') })
    expect(window.open).toHaveBeenCalledExactlyOnceWith('about:blank', '_blank')
    expect(router.state.location.pathname).toBe('/workspaces/ws-1/chat')
    expect(child.location.replace).toHaveBeenCalledTimes(1)
    const url = new URL(vi.mocked(child.location.replace).mock.calls[0][0], window.location.href)
    expect(url.hash.split('?')[0]).toBe('#/panel/library')
    const search = new URLSearchParams(url.hash.split('?')[1])
    expect(search.get('workspace')).toBe('ws-1')
    expect(search.get('path')).toBe('Notes/Current & next.md')
    expect(search.get('popout')).toMatch(/^[a-zA-Z0-9_-]+$/)
    expect(usePanelShellStore.getState().activePanel).toBeNull()
  })
})

// Late-settle harness: the route change is committed and visible, but the
// router only reports navigate() as finished when the test calls settle().
async function startLateExpand() {
  displayMode('standalone')
  usePanelShellStore.getState().openPanel('library', context)
  const { router, expand } = await renderSource()
  let settle!: () => void
  const navigate = router.navigate.bind(router)
  vi.spyOn(router, 'navigate').mockImplementation(async (options) => {
    await navigate(options)
    await new Promise<void>((resolve) => { settle = resolve })
  })
  let expanded!: ReturnType<typeof expand>
  await act(async () => { expanded = expand() })
  await waitFor(() => expect(router.state.location.pathname).toBe('/panel/library'))
  return { router, expanded, settle: () => settle() }
}

describe('E.2 standalone Expand settles late', () => {
  it('a navigation that settles after Back re-docked the panel does not close that dock', async () => {
    displayMode('standalone')
    usePanelShellStore.getState().openPanel('library', context)
    const { router, expand } = await renderSource()
    // Force the timing: the route change is committed and visible, but the
    // router only reports the navigation as finished when we say so.
    let settle!: () => void
    const navigate = router.navigate.bind(router)
    vi.spyOn(router, 'navigate').mockImplementation(async (options) => {
      await navigate(options)
      await new Promise<void>((resolve) => { settle = resolve })
    })
    let expanded!: ReturnType<typeof expand>
    await act(async () => { expanded = expand() })
    await waitFor(() => expect(router.state.location.pathname).toBe('/panel/library'))
    // The user already pressed Back on the full-screen route: it re-docks the panel.
    await act(async () => { usePanelShellStore.getState().openPanel('library', context) })
    await act(async () => {
      settle()
      await expect(expanded).resolves.toBe('opened')
    })
    expect(usePanelShellStore.getState().activePanel).toEqual({ id: 'library', context })
  })
})

describe('E.2 variants: a newer panel intent made during the wait wins over the late settle', () => {
  it('closePanel during the wait ends with no active panel and the late settle adds no second close', async () => {
    const { expanded, settle } = await startLateExpand()
    await act(async () => { usePanelShellStore.getState().closePanel() })
    const revisionAfterUserClose = usePanelShellStore.getState().panelIntentRevision
    await act(async () => {
      settle()
      await expect(expanded).resolves.toBe('opened')
    })
    expect(usePanelShellStore.getState().activePanel).toBeNull()
    // A stale settle must not issue its own close: that would be a second
    // panel intent bumping the revision after the user's.
    expect(usePanelShellStore.getState().panelIntentRevision).toBe(revisionAfterUserClose)
  })

  it('opening mail during the wait keeps mail docked after the late settle', async () => {
    const { expanded, settle } = await startLateExpand()
    const mailContext = { workspaceId: 'ws-1', path: 'Inbox' }
    await act(async () => { usePanelShellStore.getState().openPanel('mail', mailContext) })
    await act(async () => {
      settle()
      await expect(expanded).resolves.toBe('opened')
    })
    expect(usePanelShellStore.getState().activePanel).toEqual({ id: 'mail', context: mailContext })
  })

  it('control: a prompt settle with no newer intent closes the dock', async () => {
    const { router, expanded, settle } = await startLateExpand()
    await act(async () => {
      settle()
      await expect(expanded).resolves.toBe('opened')
    })
    expect(router.state.location.pathname).toBe('/panel/library')
    expect(usePanelShellStore.getState().activePanel).toBeNull()
  })
})

describe('E.3 standalone Expand leaves only after the Library/Mail guard', () => {
  it.each(['library', 'mail'] as const)('%s cancellation preserves the dock and route', async (id) => {
    displayMode('standalone')
    const guard = vi.fn(async () => false)
    usePanelShellStore.getState().openPanel(id, context)
    const { router, expand } = await renderSource(definition(id, guard))
    await act(async () => { await expect(expand()).resolves.toBe('cancelled') })
    expect(guard).toHaveBeenCalledExactlyOnceWith()
    expect(window.open).not.toHaveBeenCalled()
    expect(router.state.location.pathname).toBe('/workspaces/ws-1/chat')
    expect(usePanelShellStore.getState().activePanel).toEqual({ id, context })
    expect(usePanelShellStore.getState().toasts).toEqual([])
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it.each(['library', 'mail'] as const)('%s remains mounted while its guard waits, then uses the current context', async (id) => {
    displayMode('standalone')
    let answer!: (allowed: boolean) => void
    const guard = vi.fn(() => new Promise<boolean>((resolve) => { answer = resolve }))
    usePanelShellStore.getState().openPanel(id, context)
    const { router, expand } = await renderSource(definition(id, guard))
    let pending!: ReturnType<typeof expand>
    act(() => { pending = expand(() => ({ workspaceId: 'ws-2', path: 'Updated.md' })) })
    expect(guard).toHaveBeenCalledExactlyOnceWith()
    expect(usePanelShellStore.getState().guardPending).toBe(true)
    expect(usePanelShellStore.getState().activePanel).toEqual({ id, context })
    expect(router.state.location.pathname).toBe('/workspaces/ws-1/chat')
    expect(window.open).not.toHaveBeenCalled()
    await act(async () => { answer(true); await expect(pending).resolves.toBe('opened') })
    expect(window.open).not.toHaveBeenCalled()
    expect(router.state.location.pathname).toBe(`/panel/${id}`)
    expect(router.state.location.search).toEqual({ workspace: 'ws-2', path: 'Updated.md' })
    expect(usePanelShellStore.getState().guardPending).toBe(false)
    expect(usePanelShellStore.getState().activePanel).toBeNull()
  })

  it.each([
    ['library', 'throw'], ['library', 'reject'], ['mail', 'throw'], ['mail', 'reject'],
  ] as const)('%s %s from beforeLeave is an error, not cancellation, and preserves all source state', async (id, failure) => {
    displayMode('standalone')
    const originalError = new Error('The leave check failed')
    const guard = vi.fn(() => {
      if (failure === 'throw') throw originalError
      return Promise.reject(originalError)
    })
    const log = vi.spyOn(console, 'error').mockImplementation(() => {})
    usePanelShellStore.getState().openPanel(id, context)
    const { router, expand } = await renderSource(definition(id, guard))
    await act(async () => { await expect(expand()).resolves.toBe('error') })
    expect(guard).toHaveBeenCalledExactlyOnceWith()
    expect(router.state.location.pathname).toBe('/workspaces/ws-1/chat')
    expect(router.state.location.search).toEqual({})
    expect(usePanelShellStore.getState().activePanel).toEqual({ id, context })
    expect(usePanelShellStore.getState().guardPending).toBe(false)
    expect(window.open).not.toHaveBeenCalled()
    expect(log).toHaveBeenCalledExactlyOnceWith('[side-panel] Expand leave guard failed', { panelId: id, error: originalError })
    expect(usePanelShellStore.getState().toasts.map(({ message, variant }) => ({ message, variant }))).toEqual([{
      message: `${id === 'library' ? 'Library' : 'Mail'} could not expand because its unsaved-change check failed. Try again.`, variant: 'error',
    }])
    expect(within(screen.getByRole('alert')).getByText(`${id === 'library' ? 'Library' : 'Mail'} could not expand because its unsaved-change check failed. Try again.`, { exact: true })).toBeVisible()
  })

  it('a rejected same-window navigation reports error without closing the dock or opening a popup', async () => {
    displayMode('standalone')
    const originalError = new Error('The destination could not load')
    const log = vi.spyOn(console, 'error').mockImplementation(() => {})
    usePanelShellStore.getState().openPanel('library', context)
    const { router, expand } = await renderSource()
    vi.spyOn(router, 'navigate').mockRejectedValueOnce(originalError)
    await act(async () => { await expect(expand()).resolves.toBe('error') })
    expect(router.state.location.pathname).toBe('/workspaces/ws-1/chat')
    expect(router.state.location.search).toEqual({})
    expect(usePanelShellStore.getState().activePanel).toEqual({ id: 'library', context })
    expect(usePanelShellStore.getState().guardPending).toBe(false)
    expect(window.open).not.toHaveBeenCalled()
    expect(log).toHaveBeenCalledExactlyOnceWith('[side-panel] Same-window Expand navigation failed', originalError)
    expect(usePanelShellStore.getState().toasts.map(({ message, variant }) => ({ message, variant }))).toEqual([{
      message: 'Library could not open full screen. The panel remains here.', variant: 'error',
    }])
    expect(within(screen.getByRole('alert')).getByText('Library could not open full screen. The panel remains here.', { exact: true })).toBeVisible()
  })

  it('does not navigate when another leave decision is already pending', async () => {
    displayMode('standalone')
    usePanelShellStore.getState().openPanel('library', context)
    usePanelShellStore.getState().setGuardPending(true)
    const { router, expand } = await renderSource()
    await act(async () => { await expect(expand()).resolves.toBe('cancelled') })
    expect(window.open).not.toHaveBeenCalled()
    expect(router.state.location.pathname).toBe('/workspaces/ws-1/chat')
    expect(usePanelShellStore.getState().activePanel).toEqual({ id: 'library', context })
  })
})
