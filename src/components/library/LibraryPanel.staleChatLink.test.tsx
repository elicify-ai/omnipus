/**
 * UAT R2: the Library is already open on a folder. A file that was not in
 * that listing is created (it exists for the next read). Clicking its chat
 * link must open that file, not the "not found" landing from the listing
 * still on screen.
 *
 * Oracle: chat-links spec UI States "Docked Library on a deep address / file
 * missing" is only for a file that is not there. A file the next listing
 * returns is there. The not-found sentence is LibraryExplorer's own
 * ("was not found. Showing the folder that would contain it.") and must not
 * be shown for this file.
 *
 * Fetch is the only stand-in. LibraryPanel and LibraryExplorer are real.
 */
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createLinkRenderer } from '@/components/chat/markdown-shared'
import type { LibraryEntry, LibraryWorkspaceNode } from '@/lib/api'
import { useUiStore } from '@/store/ui'

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
const PLAN = 'plan body already listed'
const RETRO = 'retro body created after the listing'

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

const planEntry = entry('plan.md', 'notes/plan.md')
const retroEntry = entry('retro.md', 'notes/retro.md')
let notesListing: LibraryEntry[] = [planEntry]

function contentFor(path: string): string {
  if (path === 'notes/retro.md') return RETRO
  if (path === 'notes/plan.md') return PLAN
  return ''
}

beforeEach(() => {
  vi.clearAllMocks()
  notesListing = [planEntry]
  useUiStore.getState().closePanel()
  vi.mocked(fetchLibraryWorkspaces).mockResolvedValue([
    { id: 'W', name: 'Workspace', entry_count: 1 } satisfies LibraryWorkspaceNode,
  ])
  vi.mocked(fetchKnowledgeBaseInfo).mockResolvedValue({
    workspace_id: 'W',
    root_path: '',
    is_knowledge_base: false,
    marker: 'none',
  })
  vi.mocked(fetchLibraryEntries).mockImplementation(async (_ws: string, folder?: string) => {
    if (folder === 'notes') return notesListing
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

describe('a chat link opens a file created after the folder was listed (UAT R2)', () => {
  it('shows notes/retro.md instead of the stale not-found landing', async () => {
    useUiStore.getState().openPanel('library', { workspaceId: 'W', path: 'notes/plan.md' })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <LibraryPanel />
        <MarkdownLink href="omnipus://file/W/notes/retro.md">retro</MarkdownLink>
      </QueryClientProvider>,
    )
    expect(await screen.findByTestId('library-preview-title')).toHaveTextContent('plan.md')
    expect(await screen.findByText(PLAN)).toBeInTheDocument()
    expect(screen.queryByTestId('library-deeplink-unresolved')).toBeNull()

    // The file now exists for the next read. The listing already on screen
    // does not include it, which is the stale-cache case.
    notesListing = [planEntry, retroEntry]
    fireEvent.click(screen.getByTestId('markdown-link'))

    await waitFor(() => {
      expect(screen.queryByTestId('library-deeplink-unresolved')).toBeNull()
      expect(screen.getByTestId('library-preview-title')).toHaveTextContent('retro.md')
    })
    expect(screen.getByTestId('library-preview-pane')).toHaveTextContent(RETRO)
    expect(screen.getByTestId('library-preview-pane')).not.toHaveTextContent('was not found')
  })
})
