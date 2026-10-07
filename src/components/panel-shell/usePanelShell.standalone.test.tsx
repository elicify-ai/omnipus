// Oracle: founder E.1–E.3. Native Chrome --app probe reports standalone=true.
// The hook, store, full-screen context codec and router navigation are real;
// only matchMedia and the browser popup handle are controlled browser edges.
import { act, cleanup, render, waitFor } from '@testing-library/react'
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
    return <div>Chat source</div>
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
