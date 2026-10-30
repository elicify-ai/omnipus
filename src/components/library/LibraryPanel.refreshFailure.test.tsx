/**
 * R5-SF-1. A chat link re-opens a file the Library is already showing.
 * The folder read succeeds. The file read fails after retries (HTTP 500),
 * or the file is gone (listing omits it and the file read is HTTP 404).
 *
 * Oracle: BS-1.3 says a re-click shows that file. A failed refresh must not
 * present the cached bytes as the current file: a visible alert with a Retry
 * control stays after the read has given up, and the earlier text may remain.
 * BS-1.5 says a missing file uses the Library's existing not-there landing,
 * not the cached text. BS-1.4 is why a dirty click is not the way into this
 * refresh: that click asks first. The draft case opens through the same store
 * update a clean click uses, while the editor is already dirty.
 *
 * The spec does not fix the alert's sentence, so this file does not either.
 * Fetch and the editor widget are the only stand-ins.
 */
import { act } from 'react'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createLinkRenderer } from '@/components/chat/markdown-shared'
import type { LibraryEntry, LibraryWorkspaceNode } from '@/lib/api'
import { ApiError } from '@/lib/api-error'
import { useUiStore } from '@/store/ui'

vi.mock('@uiw/react-codemirror', () => ({
  default: ({ value, onChange }: { value: string; onChange: (next: string) => void }) => (
    <textarea data-testid="library-editor-textarea" value={value} onChange={(event) => onChange(event.target.value)} />
  ),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchLibraryWorkspaces: vi.fn(),
    fetchLibraryEntries: vi.fn(),
    fetchLibraryContent: vi.fn(),
    fetchLibraryContentVersioned: vi.fn(),
    fetchKnowledgeBaseInfo: vi.fn(),
    fetchKnowledgeOutline: vi.fn(),
    fetchKnowledgeGraph: vi.fn(),
    searchVault: vi.fn(),
    searchFiles: vi.fn(),
  }
})

import {
  fetchKnowledgeBaseInfo,
  fetchKnowledgeGraph,
  fetchKnowledgeOutline,
  fetchLibraryContent,
  fetchLibraryContentVersioned,
  fetchLibraryEntries,
  fetchLibraryWorkspaces,
} from '@/lib/api'
import { LibraryPanel } from './LibraryPanel'

const MarkdownLink = createLinkRenderer(null, { chatLinks: true })
const BODY_A = 'cached-body-A'
const DRAFT = 'unsaved-draft-kept'
const PLAN = 'notes/plan.md'

function entry(name: string, path: string, over: Partial<LibraryEntry> = {}): LibraryEntry {
  return {
    name,
    path,
    is_dir: false,
    is_hidden: false,
    size: BODY_A.length,
    modified_at: '2026-10-30T00:00:00Z',
    is_text_editable: true,
    ...over,
  }
}

const planEntry = entry('plan.md', PLAN)
const otherEntry = entry('other.md', 'notes/other.md')
let notesListing: LibraryEntry[] = [planEntry, otherEntry]
let contentMode: 'ok' | 'down' | 'missing' = 'ok'
let contentFailures = 0

function contentBody(path: string): string {
  return path === PLAN ? BODY_A : 'other-body'
}

beforeEach(() => {
  vi.clearAllMocks()
  notesListing = [planEntry, otherEntry]
  contentMode = 'ok'
  contentFailures = 0
  useUiStore.getState().closePanel()
  vi.mocked(fetchLibraryWorkspaces).mockResolvedValue([
    { id: 'W', name: 'Workspace', entry_count: 2 } satisfies LibraryWorkspaceNode,
  ])
  vi.mocked(fetchKnowledgeBaseInfo).mockResolvedValue({
    workspace_id: 'W',
    root_path: '',
    is_knowledge_base: false,
    marker: 'none',
  })
  // An unresolved outline/graph query is itself an alert ("could not read
  // headings"). These tests are about the file-refresh alert, so the note
  // surfaces resolve empty.
  vi.mocked(fetchKnowledgeOutline).mockResolvedValue({
    path: PLAN,
    is_knowledge_base: false,
    headings: [],
  })
  vi.mocked(fetchKnowledgeGraph).mockResolvedValue({
    collection_id: 'kb_1',
    kind: 'backlinks',
    nodes: [],
    edges: [],
    skipped: [],
    truncated: false,
  })
  vi.mocked(fetchLibraryEntries).mockImplementation(async (_ws: string, folder?: string) => {
    if (folder === 'notes') return notesListing
    if (!folder) return [entry('notes', 'notes', { is_dir: true, is_text_editable: false, size: 0 })]
    return []
  })
  vi.mocked(fetchLibraryContent).mockImplementation(async (_ws: string, path: string) => {
    if (contentMode === 'down') {
      contentFailures += 1
      throw new ApiError(500, 'content refresh failed')
    }
    if (contentMode === 'missing') {
      contentFailures += 1
      throw new ApiError(404, 'content missing')
    }
    const content = contentBody(path)
    return { path, content, size: content.length, is_text: true, too_large: false }
  })
  vi.mocked(fetchLibraryContentVersioned).mockImplementation(async (_ws: string, path: string) => {
    const content = contentBody(path)
    return {
      version: `v-${path}`,
      data: { path, content, size: content.length, is_text: true, too_large: false },
    }
  })
})

function renderOnPlan(retry: number) {
  useUiStore.getState().openPanel('library', { workspaceId: 'W', path: PLAN })
  const client = new QueryClient({ defaultOptions: { queries: { retry, retryDelay: 0 } } })
  render(
    <QueryClientProvider client={client}>
      <LibraryPanel />
      <MarkdownLink href="omnipus://file/W/notes/plan.md">plan</MarkdownLink>
    </QueryClientProvider>,
  )
}

async function shownCachedBody() {
  expect(await screen.findByTestId('library-preview-title')).toHaveTextContent('plan.md')
  expect(await screen.findByText(BODY_A)).toBeInTheDocument()
  expect(screen.queryByTestId('library-deeplink-unresolved')).toBeNull()
}

describe('a re-opened file whose fresh read fails (R5-SF-1)', () => {
  it('keeps the earlier text and a persistent Retry alert when the content refresh returns HTTP 500', async () => {
    renderOnPlan(2)
    await shownCachedBody()

    contentMode = 'down'
    fireEvent.click(screen.getByTestId('markdown-link'))
    await waitFor(() => expect(contentFailures).toBeGreaterThanOrEqual(3))

    const preview = screen.getByTestId('library-preview-pane')
    expect(preview).toHaveTextContent(BODY_A)
    const refreshNotice = screen.getByText('Could not refresh this file — showing the earlier version.')
    expect(refreshNotice.closest('[role="alert"]')).not.toBeNull()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeVisible()
    await act(async () => {
      await Promise.resolve()
    })
    expect(screen.getByText('Could not refresh this file — showing the earlier version.')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeVisible()
    expect(preview).toHaveTextContent(BODY_A)
  })

  it('keeps an unsaved draft when that same refresh fails', async () => {
    renderOnPlan(2)
    await shownCachedBody()
    fireEvent.click(screen.getByTestId('library-preview-mode-edit'))
    fireEvent.change(await screen.findByTestId('library-editor-textarea'), {
      target: { value: `${BODY_A}\n${DRAFT}` },
    })
    expect(screen.getByTestId('library-editor-textarea')).toHaveValue(`${BODY_A}\n${DRAFT}`)

    contentMode = 'down'
    useUiStore.getState().openPanel('library', { workspaceId: 'W', path: PLAN })
    await waitFor(() => expect(contentFailures).toBeGreaterThanOrEqual(3))

    expect(screen.getByTestId('library-editor-textarea')).toHaveValue(`${BODY_A}\n${DRAFT}`)
    const refreshNotice = screen.getByText('Could not refresh this file — showing the earlier version.')
    expect(refreshNotice.closest('[role="alert"]')).not.toBeNull()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeVisible()
  })

  it('says an already open file is missing when the fresh listing omits it and the read is HTTP 404', async () => {
    renderOnPlan(0)
    await shownCachedBody()

    notesListing = [otherEntry]
    contentMode = 'missing'
    fireEvent.click(screen.getByTestId('markdown-link'))

    await waitFor(() => {
      const missingNotice = screen.getByText('This file is no longer in the Library — showing the earlier version.')
      expect(missingNotice.closest('[role="alert"]')).not.toBeNull()
    })
    expect(screen.getByRole('button', { name: 'Retry' })).toBeVisible()
    expect(screen.getByTestId('library-preview-pane')).toHaveTextContent(BODY_A)
    expect(screen.getByTestId('library-row-notes/other.md')).toBeEnabled()
  })
})
