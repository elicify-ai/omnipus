/**
 * FR-007 / BS-1.17 / TDD row 5: a chat link does not stop at the panel
 * address. The real docked explorer selects that file and shows it.
 * Fetch is the only stand-in; LibraryExplorer and LibraryPanel are real.
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
  fetchLibraryEntries,
  fetchLibraryWorkspaces,
} from '@/lib/api'
import { LibraryPanel } from './LibraryPanel'

const MarkdownLink = createLinkRenderer(null, { chatLinks: true })
const PLAN = 'plan body unique'

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

beforeEach(() => {
  vi.clearAllMocks()
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
  vi.mocked(fetchLibraryEntries).mockImplementation(async (_ws: string, folder?: string) => {
    if (folder === 'notes') {
      return [
        entry('plan.md', 'notes/plan.md'),
        entry('other.md', 'notes/other.md'),
      ]
    }
    if (!folder) return [entry('notes', 'notes', { is_dir: true, is_text_editable: false, size: 0 })]
    return []
  })
  vi.mocked(fetchLibraryContent).mockImplementation(async (_ws: string, path: string) => ({
    path,
    content: path === 'notes/plan.md' ? PLAN : 'other body',
    size: 16,
    is_text: true,
    too_large: false,
  }))
})

describe('a chat link opens the named file in the docked preview (FR-007)', () => {
  it('shows that file, then re-addresses when a second path is supplied', async () => {
    render(<MarkdownLink href="omnipus://file/W/notes/plan.md">plan</MarkdownLink>)
    fireEvent.click(screen.getByTestId('markdown-link'))
    expect(useUiStore.getState().activePanel).toEqual({
      id: 'library',
      context: { workspaceId: 'W', path: 'notes/plan.md' },
    })

    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <LibraryPanel />
      </QueryClientProvider>,
    )
    expect(await screen.findByTestId('library-preview-title')).toHaveTextContent('plan.md')
    expect(await screen.findByText(PLAN)).toBeInTheDocument()

    useUiStore.getState().openPanel('library', { workspaceId: 'W', path: 'notes/other.md' })
    await waitFor(() => expect(screen.getByTestId('library-preview-title')).toHaveTextContent('other.md'))
    expect(await screen.findByText('other body')).toBeInTheDocument()
  })
})
