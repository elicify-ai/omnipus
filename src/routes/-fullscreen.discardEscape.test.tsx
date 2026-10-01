import { act } from 'react'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRouter, Outlet, RouterProvider } from '@tanstack/react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  getDiscardConfirmDialogOpen,
  isLibraryEditorDirty,
  resolveDiscardConfirmDialog,
  setLibraryEditorDirty,
} from '@/components/library/preview/unsavedGuard'
import type { LibraryEntry, LibraryWorkspaceNode } from '@/lib/api'

// Only external network calls and route layout are replaced. LibraryExplorer,
// the discard guard, ConfirmDialog, Radix, and the full-screen Escape listener
// all run as production code in this test.
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchLibraryWorkspaces: vi.fn(),
  fetchLibraryEntries: vi.fn(),
  fetchLibraryContent: vi.fn(),
  fetchKnowledgeBaseInfo: vi.fn(),
}))
vi.mock('./-authenticatedBeforeLoad', () => ({ authenticatedBeforeLoad: vi.fn(async () => {}) }))
vi.mock('@/components/layout/AppShell', () => ({ AppShell: () => <Outlet /> }))
vi.mock('@/components/workspaces/WorkspaceTabContainer', () => ({ WorkspaceTabContainer: () => <Outlet /> }))

import {
  fetchLibraryWorkspaces,
  fetchLibraryEntries,
  fetchLibraryContent,
  fetchKnowledgeBaseInfo,
} from '@/lib/api'
import { routeTree } from '@/routeTree.gen'

const mockedFetchWorkspaces = vi.mocked(fetchLibraryWorkspaces)
const mockedFetchEntries = vi.mocked(fetchLibraryEntries)
const mockedFetchContent = vi.mocked(fetchLibraryContent)
const mockedKnowledgeInfo = vi.mocked(fetchKnowledgeBaseInfo)
const originalClosed = Object.getOwnPropertyDescriptor(window, 'closed')

beforeEach(() => {
  vi.clearAllMocks()
  setLibraryEditorDirty(false)
  const entries: LibraryEntry[] = [
    { name: 'report.md', path: 'report.md', is_dir: false, is_hidden: false, size: 9,
      modified_at: '2026-09-29T00:00:00Z', mime: 'text/markdown', is_text_editable: true },
    { name: 'draft.md', path: 'draft.md', is_dir: false, is_hidden: false, size: 8,
      modified_at: '2026-09-29T00:00:00Z', mime: 'text/markdown', is_text_editable: true },
  ]
  const workspaces: LibraryWorkspaceNode[] = [
    { id: 'ws-1', name: 'Workspace', entry_count: entries.length },
  ]
  mockedFetchWorkspaces.mockResolvedValue(workspaces)
  mockedFetchEntries.mockResolvedValue(entries)
  mockedFetchContent.mockImplementation(async (_workspaceId, path) => ({
    path, content: '# Report\n', size: 9, is_text: true, too_large: false,
  }))
  mockedKnowledgeInfo.mockResolvedValue({
    workspace_id: 'ws-1', root_path: 'notes', is_knowledge_base: false,
    marker: 'none', collection_id: 'kb_1',
  })
  vi.spyOn(window, 'close').mockImplementation(() => {})
  Object.defineProperty(window, 'closed', { configurable: true, value: false })
})

afterEach(() => {
  cleanup()
  if (getDiscardConfirmDialogOpen()) resolveDiscardConfirmDialog(false)
  setLibraryEditorDirty(false)
  vi.restoreAllMocks()
  if (originalClosed) Object.defineProperty(window, 'closed', originalClosed)
  else Reflect.deleteProperty(window, 'closed')
})

describe('full-screen Library discard confirmation', () => {
  it('keeps the edit and panel after each of three consecutive Escapes, cancelling both discard prompts', async () => {
    const user = userEvent.setup()
    const router = createRouter({
      routeTree,
      history: createMemoryHistory({ initialEntries: ['/panel/library?workspace=ws-1&popout=p1'] }),
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)

    await screen.findByTestId('fullscreen-panel')
    fireEvent.click(await screen.findByTestId('library-row-report.md'))
    // The editor's mount effect first clears the singleton dirty flag. Wait
    // until it is installed before setting the simulated unsaved draft; jsdom
    // cannot reliably synthesize a CodeMirror document edit (existing test seam).
    await screen.findByTestId('library-preview-mode-view')
    fireEvent.click(screen.getByTestId('library-preview-mode-edit'))
    await screen.findByTestId('library-code-editor')
    act(() => setLibraryEditorDirty(true))

    const back = screen.getByRole('button', { name: 'Back to chat' })
    await user.click(back)
    expect(await screen.findByRole('alertdialog')).toHaveTextContent('Discard unsaved changes?')
    for (let escapeNumber = 1; escapeNumber <= 3; escapeNumber += 1) {
      // 1 and 3 dismiss the real dialog; 2 comes from the restored Back
      // button and may request to leave again, but cannot silently discard.
      // The first/third native events also reach the route's window listener.
      await user.keyboard('{Escape}')
      if (escapeNumber === 2) {
        expect(await screen.findByRole('alertdialog')).toHaveTextContent('Discard unsaved changes?')
        expect(getDiscardConfirmDialogOpen()).toBe(true)
      } else {
        await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
        expect(getDiscardConfirmDialogOpen()).toBe(false)
      }
      expect(isLibraryEditorDirty()).toBe(true)
      expect(screen.getByTestId('library-preview-title')).toHaveTextContent('report.md')
      expect(screen.getByTestId('library-preview-mode-edit')).toHaveAttribute('aria-pressed', 'true')
      expect(screen.getByTestId('library-code-editor')).toBeInTheDocument()
      expect(router.state.location.pathname).toBe('/panel/library')
      expect(window.close).not.toHaveBeenCalled()
      expect(mockedFetchContent).not.toHaveBeenCalledWith('ws-1', 'draft.md')
    }

    // Positive control: only the explicit destructive action may proceed.
    await user.click(back)
    expect(await screen.findByRole('alertdialog')).toHaveTextContent('Discard unsaved changes?')
    await user.click(screen.getByRole('button', { name: 'Discard' }))
    await waitFor(() => expect(window.close).toHaveBeenCalledTimes(1))
    expect(isLibraryEditorDirty()).toBe(false)
    client.clear()
  })
})
