import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createMemoryHistory, createRouter, Outlet, RouterProvider } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useState } from 'react'
import type { PanelContentProps, PanelContext, PanelDefinition, PanelId } from '@/components/panel-shell/types'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'

const mocks = vi.hoisted(() => ({
  beforeLeave: vi.fn<() => Promise<boolean>>(),
  announceClosed: vi.fn(),
  stopPresence: vi.fn(),
  fetchWorkspaces: vi.fn(),
  delayChat: false,
  completeRestore: null as (() => void) | null,
}))

vi.mock('@/components/panel-shell/registry', () => {
  const content = (props: PanelContentProps) => (
    <div data-testid="panel-content">
      <textarea data-testid="panel-editor" defaultValue="original draft" />
      <input data-testid="panel-input" defaultValue="search draft" />
      <select data-testid="panel-select" defaultValue="edit"><option value="edit">Edit</option></select>
      <div data-testid="panel-rich-editor" contentEditable suppressContentEditableWarning>Rich draft</div>
      <div role="dialog" aria-label="Edit confirmation" data-state="closed">
        <span data-testid="dialog-content">Keep editing?</span>
      </div>
      <span data-testid="panel-context">{JSON.stringify(props.context)}</span>
    </div>
  )
  const definitions: Record<'library' | 'browser', PanelDefinition> = {
    library: {
      id: 'library', title: 'Library', content,
      fullScreen: {
        toSearch: (context: PanelContext) => ({
          ...(context.workspaceId ? { workspace: context.workspaceId } : {}),
          ...(context.path ? { path: context.path } : {}),
        }),
        fromSearch: (search: Record<string, unknown>) => ({
          ...(typeof search.workspace === 'string' ? { workspaceId: search.workspace } : {}),
          ...(typeof search.path === 'string' ? { path: search.path } : {}),
        }),
      },
      beforeLeave: mocks.beforeLeave,
    },
    browser: {
      id: 'browser', title: 'Browser', content,
      fullScreen: {
        toSearch: (context: PanelContext) => ({ session: context.sessionId!, agent: context.agentId! }),
        fromSearch: (search: Record<string, unknown>) =>
          typeof search.session === 'string' && typeof search.agent === 'string'
            ? { sessionId: search.session, agentId: search.agent }
            : null,
      },
    },
  }
  return {
    getPanelDefinition: (id: PanelId) => definitions[id as keyof typeof definitions],
  }
})

vi.mock('./-authenticatedBeforeLoad', () => ({ authenticatedBeforeLoad: vi.fn(async () => {}) }))
vi.mock('@/components/layout/AppShell', () => ({ AppShell: () => <Outlet /> }))
vi.mock('@/components/workspaces/WorkspaceTabContainer', () => ({ WorkspaceTabContainer: () => <Outlet /> }))
vi.mock('@/components/workspaces/WorkspaceChatTab', () => ({
  WorkspaceChatTab: () => {
    const [ready, setReady] = useState(!mocks.delayChat)
    mocks.completeRestore = () => setReady(true)
    return ready ? <textarea data-testid="chat-input" /> : <div data-testid="workspace-chat-restoring" />
  },
}))
vi.mock('@/lib/panelTabPresence', () => ({
  announcePanelTabPresence: vi.fn(() => ({ update: vi.fn(), stop: mocks.stopPresence })),
  panelIdentityFromContext: (panelId: PanelId, context: PanelContext) => ({ panelId, ...context }),
}))
vi.mock('@/lib/panelPopoutLifecycle', () => ({
  announcePanelPopoutClosed: mocks.announceClosed,
  announcePanelPopoutContext: vi.fn(),
}))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchWorkspaces: mocks.fetchWorkspaces,
}))

import { routeTree } from '@/routeTree.gen'

const originalClosed = Object.getOwnPropertyDescriptor(window, 'closed')

async function renderPanel(path: string) {
  const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: [path] }) })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  await screen.findByTestId('fullscreen-panel')
  return { router, client }
}

beforeEach(() => {
  mocks.delayChat = false
  mocks.completeRestore = null
  mocks.beforeLeave.mockReset().mockResolvedValue(true)
  mocks.announceClosed.mockReset()
  mocks.stopPresence.mockReset()
  mocks.fetchWorkspaces.mockReset().mockResolvedValue([
    { id: 'ws-home', name: 'Home', status: 'active', is_default: true },
  ])
  useUiStore.getState().closePanel()
  useWorkspacesStore.setState({ activeWorkspaceId: null })
  vi.spyOn(window, 'close').mockImplementation(() => {})
  Object.defineProperty(window, 'closed', { configurable: true, value: false })
})

afterEach(() => {
  cleanup()
  useUiStore.getState().closePanel()
  useWorkspacesStore.setState({ activeWorkspaceId: null })
  vi.restoreAllMocks()
  if (originalClosed) Object.defineProperty(window, 'closed', originalClosed)
  else Reflect.deleteProperty(window, 'closed')
})

describe('shared full-screen exit', () => {
  it.each([
    ['library', '/panel/library?workspace=ws-a&popout=popout-a'],
    ['browser', '/panel/browser?session=session-a&agent=agent-a&popout=popout-b'],
  ])('shows a keyboard-reachable Back to chat control for %s', async (_id, path) => {
    const { client } = await renderPanel(path)
    const back = screen.getByRole('button', { name: 'Back to chat' })
    expect(back).toHaveTextContent('Back to chat')
    expect(back).toHaveAttribute('tabindex', '0')
    back.focus()
    expect(document.activeElement).toBe(back)
    expect(screen.getByTestId('panel-content')).toBeInTheDocument()
    client.clear()
  })

  it('cancels through beforeLeave without announcing or losing the edited page', async () => {
    mocks.beforeLeave.mockResolvedValue(false)
    const { router, client } = await renderPanel('/panel/library?workspace=ws-a&popout=popout-a')
    fireEvent.change(screen.getByTestId('panel-editor'), { target: { value: 'unsaved edit' } })
    fireEvent.click(screen.getByRole('button', { name: 'Back to chat' }))
    await waitFor(() => expect(mocks.beforeLeave).toHaveBeenCalledTimes(1))
    expect(screen.getByTestId('panel-editor')).toHaveValue('unsaved edit')
    expect(mocks.announceClosed).not.toHaveBeenCalled()
    expect(mocks.stopPresence).not.toHaveBeenCalled()
    expect(window.close).not.toHaveBeenCalled()
    expect(router.state.location.pathname).toBe('/panel/library')
    client.clear()
  })

  it('announces close only after beforeLeave accepts, before attempting to close the tab', async () => {
    const { client } = await renderPanel('/panel/library?workspace=ws-a&popout=popout-a')
    fireEvent.click(screen.getByRole('button', { name: 'Back to chat' }))
    await waitFor(() => expect(window.close).toHaveBeenCalledOnce())
    expect(mocks.beforeLeave).toHaveBeenCalledOnce()
    expect(mocks.announceClosed).toHaveBeenCalledExactlyOnceWith(
      'library', 'popout-a', { workspaceId: 'ws-a' },
    )
    expect(mocks.beforeLeave.mock.invocationCallOrder[0]).toBeLessThan(mocks.announceClosed.mock.invocationCallOrder[0])
    expect(mocks.announceClosed.mock.invocationCallOrder[0]).toBeLessThan(vi.mocked(window.close).mock.invocationCallOrder[0])
    client.clear()
  })

  it.each(['escape', 'back'])('releases presence before %s announces closure so the opener can re-dock', async (action) => {
    Object.defineProperty(window, 'closed', { configurable: true, value: true })
    const { client } = await renderPanel('/panel/library?workspace=ws-a&popout=popout-a')
    if (action === 'escape') {
      fireEvent.keyDown(screen.getByTestId('fullscreen-panel'), { key: 'Escape' })
    } else {
      fireEvent.click(screen.getByRole('button', { name: 'Back to chat' }))
    }
    await waitFor(() => expect(window.close).toHaveBeenCalledOnce())
    expect(mocks.stopPresence).toHaveBeenCalledOnce()
    expect(mocks.announceClosed).toHaveBeenCalledExactlyOnceWith(
      'library', 'popout-a', { workspaceId: 'ws-a' },
    )
    expect(mocks.beforeLeave.mock.invocationCallOrder[0]).toBeLessThan(mocks.stopPresence.mock.invocationCallOrder[0])
    expect(mocks.stopPresence.mock.invocationCallOrder[0]).toBeLessThan(mocks.announceClosed.mock.invocationCallOrder[0])
    expect(mocks.announceClosed.mock.invocationCallOrder[0]).toBeLessThan(vi.mocked(window.close).mock.invocationCallOrder[0])
    client.clear()
  })

  it('Escape closes Library through the same leave guard and announcement', async () => {
    const { client } = await renderPanel('/panel/library?workspace=ws-a&popout=popout-a')
    fireEvent.keyDown(screen.getByTestId('fullscreen-panel'), { key: 'Escape' })
    await waitFor(() => expect(mocks.announceClosed).toHaveBeenCalledExactlyOnceWith(
      'library', 'popout-a', { workspaceId: 'ws-a' },
    ))
    expect(mocks.beforeLeave).toHaveBeenCalledOnce()
    client.clear()
  })

  it.each([
    ['textarea', 'panel-editor'],
    ['input', 'panel-input'],
    ['select', 'panel-select'],
    ['contenteditable', 'panel-rich-editor'],
  ])('Escape inside %s leaves Library open; a plain Escape still closes', async (_label, target) => {
    const { router, client } = await renderPanel('/panel/library?workspace=ws-a&popout=popout-a')
    fireEvent.keyDown(screen.getByTestId(target), { key: 'Escape' })
    await Promise.resolve()
    expect(mocks.beforeLeave).not.toHaveBeenCalled()
    expect(mocks.announceClosed).not.toHaveBeenCalled()
    expect(router.state.location.pathname).toBe('/panel/library')
    fireEvent.keyDown(screen.getByTestId('fullscreen-panel'), { key: 'Escape' })
    await waitFor(() => expect(mocks.beforeLeave).toHaveBeenCalledOnce())
    client.clear()
  })

  it('Escape cannot close the panel while a dialog is open, even if the event targets outside it', async () => {
    const { router, client } = await renderPanel('/panel/library?workspace=ws-a&popout=popout-a')
    const dialog = screen.getByRole('dialog', { hidden: true })
    dialog.setAttribute('data-state', 'open')
    fireEvent.keyDown(screen.getByTestId('fullscreen-panel'), { key: 'Escape' })
    await Promise.resolve()
    expect(mocks.beforeLeave).not.toHaveBeenCalled()
    expect(mocks.announceClosed).not.toHaveBeenCalled()
    expect(router.state.location.pathname).toBe('/panel/library')
    dialog.setAttribute('data-state', 'closed')
    fireEvent.keyDown(screen.getByTestId('fullscreen-panel'), { key: 'Escape' })
    await waitFor(() => expect(mocks.beforeLeave).toHaveBeenCalledOnce())
    client.clear()
  })

  it('an already-consumed Escape leaves Library open; a plain Escape still closes', async () => {
    const { client } = await renderPanel('/panel/library?workspace=ws-a&popout=popout-a')
    const key = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })
    key.preventDefault()
    fireEvent(screen.getByTestId('fullscreen-panel'), key)
    await Promise.resolve()
    expect(mocks.beforeLeave).not.toHaveBeenCalled()
    fireEvent.keyDown(screen.getByTestId('fullscreen-panel'), { key: 'Escape' })
    await waitFor(() => expect(mocks.beforeLeave).toHaveBeenCalledOnce())
    client.clear()
  })

  it('Browser ignores Escape even on the root; its visible control still closes', async () => {
    const { router, client } = await renderPanel('/panel/browser?session=sess-a&agent=mia&popout=popout-b')
    fireEvent.keyDown(screen.getByTestId('fullscreen-panel'), { key: 'Escape' })
    await Promise.resolve()
    expect(mocks.announceClosed).not.toHaveBeenCalled()
    expect(router.state.location.pathname).toBe('/panel/browser')
    fireEvent.click(screen.getByRole('button', { name: 'Back to chat' }))
    await waitFor(() => expect(mocks.announceClosed).toHaveBeenCalledExactlyOnceWith(
      'browser', 'popout-b', { sessionId: 'sess-a', agentId: 'mia' },
    ))
    client.clear()
  })

  it('a refused close routes Library to workspace chat with the full selected path and focuses chat', async () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-other' })
    const { router, client } = await renderPanel('/panel/library?workspace=ws-a&path=Notes%2FCurrent.md&popout=p1')
    fireEvent.click(screen.getByRole('button', { name: 'Back to chat' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/workspaces/ws-a/chat'))
    expect(router.state.location.search).toMatchObject({ panel: 'library' })
    expect(useUiStore.getState().activePanel).toEqual({
      id: 'library', context: { workspaceId: 'ws-a', path: 'Notes/Current.md' },
    })
    await waitFor(() => expect(document.activeElement).toBe(screen.getByTestId('chat-input')))
    client.clear()
  })

  it('focuses chat after a delayed conversation restore, not only on the first navigation frame', async () => {
    mocks.delayChat = true
    const { router, client } = await renderPanel('/panel/library?workspace=ws-a&path=Notes%2FCurrent.md')
    fireEvent.click(screen.getByRole('button', { name: 'Back to chat' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/workspaces/ws-a/chat'))
    await screen.findByTestId('workspace-chat-restoring')
    await act(async () => {
      await new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())))
    })
    expect(mocks.completeRestore).toBeTypeOf('function')
    act(() => mocks.completeRestore?.())
    await waitFor(() => expect(document.activeElement).toBe(screen.getByTestId('chat-input')))
    client.clear()
  })

  it('a refused close routes Browser to default workspace chat without leaking its session into the URL', async () => {
    const { router, client } = await renderPanel('/panel/browser?session=sess-a&agent=mia')
    fireEvent.click(screen.getByRole('button', { name: 'Back to chat' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/workspaces/ws-home/chat'))
    expect(router.state.location.search).not.toHaveProperty('panel', 'browser')
    expect(router.state.location.search).not.toHaveProperty('session')
    expect(useUiStore.getState().activePanel).toEqual({
      id: 'browser', context: { sessionId: 'sess-a', agentId: 'mia' },
    })
    await waitFor(() => expect(document.activeElement).toBe(screen.getByTestId('chat-input')))
    client.clear()
  })
})
