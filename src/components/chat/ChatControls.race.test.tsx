import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { getDiscardConfirmDialogOpen, resolveDiscardConfirmDialog, setLibraryEditorDirty } from '@/components/library/preview/unsavedGuard'

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn(), useLocation: () => ({ pathname: '/' }) }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, createSession: vi.fn() }
})

import { createSession } from '@/lib/api'
import { ChatControls } from './ChatControls'

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

beforeEach(() => {
  vi.clearAllMocks()
  useUiStore.getState().closePanel()
  useSessionStore.setState({ activeAgentId: 'mia', activeSessionId: null })
  useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' } as never)
  setLibraryEditorDirty(false)
})

afterEach(() => {
  setLibraryEditorDirty(false)
  if (getDiscardConfirmDialogOpen()) resolveDiscardConfirmDialog(false)
  useUiStore.getState().closePanel()
})

describe('ChatControls browser-session creation race', () => {
  it('marks Open browser as a tracked panel trigger and does not render Open library', () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <ChatControls />
      </QueryClientProvider>,
    )

    const browserTrigger = screen.getByRole('button', { name: 'Open browser' })
    expect(browserTrigger).toHaveAttribute('data-panel-trigger', 'browser')
    expect(screen.queryByRole('button', { name: 'Open library' })).toBeNull()
    client.clear()
  })

  it('does not replace a Library panel opened while createSession was pending', async () => {
    const creation = deferred<{ id: string; agent_id: string }>()
    vi.mocked(createSession).mockReturnValue(creation.promise as ReturnType<typeof createSession>)
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <ChatControls />
      </QueryClientProvider>,
    )

    const browserTrigger = screen.getByRole('button', { name: 'Open browser' })
    fireEvent.click(browserTrigger)
    await waitFor(() => expect(createSession).toHaveBeenCalledTimes(1))

    act(() => {
      useUiStore.getState().openPanel('library', { workspaceId: 'ws-1' })
      setLibraryEditorDirty(true)
    })
    await act(async () => {
      creation.resolve({ id: 'created-session', agent_id: 'mia' })
      await creation.promise
    })

    await waitFor(() => expect(useUiStore.getState().activePanel?.id).toBe('library'))
    expect(getDiscardConfirmDialogOpen()).toBe(false)
    client.clear()
  })
})
