/**
 * UAT R12: the Library has unsaved edits. Open on the notice
 * "The agent wants to show you plan.md", then Discard, must open plan.md
 * and drop the edit. The same Discard from a chat link must do the same.
 *
 * Oracle: chat-links spec J1 and UI States "Open clicked" — the leave gate
 * runs, Discard continues, and the named file is what the Library shows.
 * The edit is discarded, so neither the unsaved text nor
 * "changed elsewhere while you were editing" remains.
 *
 * Fetch and the code editor widget are the only stand-ins. The panel, the
 * notice, the discard dialog and the editor hook are real.
 */
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { LibraryOpenNotice } from '@/components/chat/LibraryOpenNotice'
import { createLinkRenderer } from '@/components/chat/markdown-shared'
import {
  getDiscardConfirmDialogOpen,
  isLibraryEditorDirty,
  resolveDiscardConfirmDialog,
  setLibraryEditorDirty,
} from '@/components/library/preview/unsavedGuard'
import type { LibraryEntry, LibraryWorkspaceNode } from '@/lib/api'
import { resetLibraryOpenState, showLibraryOpenNotice } from '@/store/chat/libraryOpen'
import { useUiStore } from '@/store/ui'

vi.mock('@uiw/react-codemirror', () => ({
  default: ({ value, onChange }: { value: string; onChange: (v: string) => void }) => (
    <textarea
      data-testid="library-editor-textarea"
      value={value}
      onChange={(e) => onChange(e.target.value)}
    />
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
  fetchLibraryContent,
  fetchLibraryContentVersioned,
  fetchLibraryEntries,
  fetchLibraryWorkspaces,
} from '@/lib/api'
import { LibraryPanel } from './LibraryPanel'

const MarkdownLink = createLinkRenderer(null, { chatLinks: true })
const MEETINGS = 'meetings-saved-body'
const PLAN = 'plan-saved-body'
const UNSAVED = 'UNSAVED-UAT-EDIT'
const DIRTY_DRAFT = `${MEETINGS}\n${UNSAVED}`
const SESSION = 'sess-discard'
const REQUEST = 'a'.repeat(32)

function entry(name: string, path: string, over: Partial<LibraryEntry> = {}): LibraryEntry {
  return {
    name,
    path,
    is_dir: false,
    is_hidden: false,
    size: 12,
    modified_at: '2026-10-29T00:00:00Z',
    is_text_editable: true,
    ...over,
  }
}

function contentFor(path: string): string {
  if (path === 'notes/plan.md') return PLAN
  if (path === 'notes/meetings.md') return MEETINGS
  return ''
}

function visibleDraft(): string {
  const editor = screen.queryByTestId('library-editor-textarea')
  if (editor instanceof HTMLTextAreaElement) return editor.value
  return screen.getByTestId('library-preview-pane').textContent ?? ''
}

beforeEach(() => {
  vi.clearAllMocks()
  useUiStore.getState().closePanel()
  setLibraryEditorDirty(false)
  resetLibraryOpenState()
  vi.mocked(fetchLibraryWorkspaces).mockResolvedValue([
    { id: 'W', name: 'Workspace', entry_count: 2 } satisfies LibraryWorkspaceNode,
  ])
  vi.mocked(fetchKnowledgeBaseInfo).mockResolvedValue({
    workspace_id: 'W',
    root_path: '',
    is_knowledge_base: false,
    marker: 'none',
  })
  vi.mocked(fetchLibraryEntries).mockImplementation(async (_ws: string, folder?: string) => {
    if (folder === 'notes') {
      return [entry('meetings.md', 'notes/meetings.md'), entry('plan.md', 'notes/plan.md')]
    }
    if (!folder) return [entry('notes', 'notes', { is_dir: true, is_text_editable: false, size: 0 })]
    return []
  })
  vi.mocked(fetchLibraryContent).mockImplementation(async (_ws: string, path: string) => ({
    path,
    content: contentFor(path),
    size: contentFor(path).length,
    is_text: true,
    too_large: false,
  }))
  vi.mocked(fetchLibraryContentVersioned).mockImplementation(async (_ws: string, path: string) => ({
    version: `v-${path}`,
    data: {
      path,
      content: contentFor(path),
      size: contentFor(path).length,
      is_text: true,
      too_large: false,
    },
  }))
})

afterEach(() => {
  if (getDiscardConfirmDialogOpen()) resolveDiscardConfirmDialog(false)
  setLibraryEditorDirty(false)
  useUiStore.getState().closePanel()
  resetLibraryOpenState()
})

async function openBothFilesThenEditMeetings() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  useUiStore.getState().openPanel('library', { workspaceId: 'W', path: 'notes/plan.md' })
  render(
    <QueryClientProvider client={client}>
      <LibraryPanel />
      <LibraryOpenNotice sessionId={SESSION} />
      <MarkdownLink href="omnipus://file/W/notes/plan.md">plan</MarkdownLink>
    </QueryClientProvider>,
  )
  expect(await screen.findByTestId('library-preview-title')).toHaveTextContent('plan.md')
  expect(await screen.findByText(PLAN)).toBeInTheDocument()

  useUiStore.getState().openPanel('library', { workspaceId: 'W', path: 'notes/meetings.md' })
  await waitFor(() => expect(screen.getByTestId('library-preview-title')).toHaveTextContent('meetings.md'))
  await waitFor(() => expect(visibleDraft()).toContain(MEETINGS))
  await waitFor(() => expect(isLibraryEditorDirty()).toBe(false))

  fireEvent.click(screen.getByTestId('library-preview-mode-edit'))
  const textarea = await screen.findByTestId('library-editor-textarea')
  fireEvent.change(textarea, { target: { value: DIRTY_DRAFT } })
  await waitFor(() => expect(isLibraryEditorDirty()).toBe(true))
  expect(visibleDraft()).toContain(UNSAVED)
}

async function chooseDiscard() {
  expect(await screen.findByRole('alertdialog')).toHaveTextContent('Discard unsaved changes?')
  fireEvent.click(screen.getByRole('button', { name: 'Discard' }))
}

function expectPlanWithoutTheEdit() {
  expect(screen.getByTestId('library-preview-title')).toHaveTextContent('plan.md')
  expect(visibleDraft()).toContain(PLAN)
  expect(visibleDraft()).not.toContain(UNSAVED)
  expect(screen.queryByText(/changed elsewhere while you were editing/)).toBeNull()
  expect(isLibraryEditorDirty()).toBe(false)
}

describe('Discard then opens the file the agent asked to show (UAT R12)', () => {
  it('the notice Open path discards the edit and shows plan.md', async () => {
    await openBothFilesThenEditMeetings()
    showLibraryOpenNotice(SESSION, { workspaceId: 'W', path: 'notes/plan.md', requestId: REQUEST })
    expect(await screen.findByTestId('library-open-notice')).toHaveTextContent('The agent wants to show you')
    expect(screen.getByTestId('library-open-notice')).toHaveTextContent('plan.md')

    fireEvent.click(screen.getByRole('button', { name: 'Open plan.md in Library' }))
    await chooseDiscard()

    await waitFor(() => expectPlanWithoutTheEdit())
  })

  it('the chat-link path discards the edit and shows plan.md', async () => {
    await openBothFilesThenEditMeetings()
    fireEvent.click(screen.getByTestId('markdown-link'))
    await chooseDiscard()

    await waitFor(() => expectPlanWithoutTheEdit())
  })
})
