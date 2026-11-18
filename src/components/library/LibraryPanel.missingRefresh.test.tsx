/**
 * R5-T1 / BS-1.5. A chat link names a file the folder does not contain.
 * The docked Library refreshes before it may say the file is missing.
 * While that refresh is still in flight there is no missing landing.
 * When the refresh finishes without the file, the existing not-there
 * landing is shown and the folder that would contain it stays usable.
 *
 * The spec does not fix a new sentence for that landing, so the test
 * uses the landing's existing alert (library-deeplink-unresolved).
 * Fetch is the only stand-in. LibraryPanel and LibraryExplorer are real.
 */
import { act } from 'react'
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
const PLAN = 'plan body listed'
const planEntry = entry('plan.md', 'notes/plan.md')
let notesListing: LibraryEntry[] = [planEntry]
let holdNotes: Promise<LibraryEntry[]> | null = null
let notesHoldEntered = 0

function entry(name: string, path: string, over: Partial<LibraryEntry> = {}): LibraryEntry {
  return {
    name,
    path,
    is_dir: false,
    is_hidden: false,
    size: 8,
    modified_at: '2026-10-30T00:00:00Z',
    is_text_editable: true,
    ...over,
  }
}

function contentFor(path: string): string {
  return path === 'notes/plan.md' ? PLAN : ''
}

beforeEach(() => {
  vi.clearAllMocks()
  notesListing = [planEntry]
  holdNotes = null
  notesHoldEntered = 0
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
    if (folder === 'notes') {
      if (holdNotes) {
        notesHoldEntered += 1
        return holdNotes
      }
      return notesListing
    }
    if (!folder) return [entry('notes', 'notes', { is_dir: true, is_text_editable: false, size: 0 })]
    return []
  })
  vi.mocked(fetchLibraryContent).mockImplementation(async (_ws: string, path: string) => {
    if (path === 'notes/gone.md') throw Object.assign(new Error('content missing'), { status: 404 })
    const content = contentFor(path)
    return { path, content, size: content.length, is_text: true, too_large: false }
  })
  vi.mocked(fetchLibraryContentVersioned).mockImplementation(async (_ws: string, path: string) => {
    const content = contentFor(path)
    return {
      version: `v-${path}`,
      data: { path, content, size: content.length, is_text: true, too_large: false },
    }
  })
})

describe('a missing chat-link target waits for the docked refresh (BS-1.5)', () => {
  it('shows no missing landing while the refresh is held, then the landing with the folder still usable', async () => {
    useUiStore.getState().openPanel('library', { workspaceId: 'W', path: 'notes/plan.md' })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <LibraryPanel />
        <MarkdownLink href="omnipus://file/W/notes/gone.md">gone</MarkdownLink>
      </QueryClientProvider>,
    )
    expect(await screen.findByTestId('library-preview-title')).toHaveTextContent('plan.md')
    expect(screen.queryByTestId('library-deeplink-unresolved')).toBeNull()

    let releaseNotes: (rows: LibraryEntry[]) => void = () => {}
    holdNotes = new Promise((resolve) => {
      releaseNotes = resolve
    })
    fireEvent.click(screen.getByTestId('markdown-link'))
    await waitFor(() => expect(notesHoldEntered).toBeGreaterThan(0))
    expect(screen.queryByTestId('library-deeplink-unresolved')).toBeNull()

    await act(async () => {
      releaseNotes(notesListing)
    })
    await waitFor(() => expect(screen.getByTestId('library-deeplink-unresolved')).toBeVisible())
    expect(screen.getByTestId('library-row-notes/plan.md')).toBeEnabled()
    expect(screen.queryByTestId('library-preview-title')).toBeNull()
  })
})
