// RED — #1449 D26, S-147, S-148.
// Oracle: docs/internal/specs/1449-group-chat-spec.md UI row "Library panel
// delete confirmation" and machine-verifiable deleteLibraryEntry rows.
// Real LibraryExplorer. The HTTP client is the process edge.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useUiStore } from '@/store/ui'
import { useKnowledgeIndexStore } from '@/store/knowledgeIndex'
import type { LibraryEntry } from '@/lib/api'
import type { KnowledgeBaseInfo } from '@/lib/api/generated/openapi-types'
import { ApiError } from '@/lib/api-error'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchLibraryWorkspaces: vi.fn(),
    fetchLibraryEntries: vi.fn(),
    fetchLibraryContent: vi.fn(),
    deleteLibraryEntry: vi.fn(),
    renameLibraryEntry: vi.fn(),
    moveLibraryEntry: vi.fn(),
    copyLibraryEntry: vi.fn(),
    uploadLibraryFiles: vi.fn(),
    mkdirLibraryEntry: vi.fn(),
    fetchKnowledgeBaseInfo: vi.fn(),
    fetchKnowledgeOutline: vi.fn(),
    fetchKnowledgeGraph: vi.fn(),
    searchVault: vi.fn(),
    searchFiles: vi.fn(),
    fetchHostFolders: vi.fn(),
    createWorkspaceMount: vi.fn(),
    deleteWorkspaceMount: vi.fn(),
    libraryDownloadUrl: vi.fn((wsId: string, path: string) => `/api/v1/library/${wsId}/download?path=${path}`),
    mintLibraryPreviewToken: vi.fn(),
  }
})

import {
  fetchLibraryWorkspaces, fetchLibraryEntries, fetchLibraryContent, deleteLibraryEntry,
  fetchKnowledgeBaseInfo, fetchKnowledgeOutline, fetchKnowledgeGraph, searchVault, searchFiles,
} from '@/lib/api'
import { LibraryExplorer } from './LibraryExplorer'

const mockedEntries = vi.mocked(fetchLibraryEntries)
const mockedDelete = vi.mocked(deleteLibraryEntry)

type EntryWithCopy = LibraryEntry & { chat_copy?: boolean }

function makeEntry(over: Partial<EntryWithCopy> = {}): LibraryEntry {
  const base: EntryWithCopy = {
    name: 'shot.png',
    path: 'shot.png',
    is_dir: false,
    is_hidden: false,
    size: 2048,
    modified_at: '2026-10-28T10:15:00Z',
    mime: 'image/png',
    is_text_editable: false,
    ...over,
  }
  return base
}

function knowledgeInfo(over: Partial<KnowledgeBaseInfo> = {}): KnowledgeBaseInfo {
  return {
    workspace_id: 'ws-1', root_path: '', is_knowledge_base: false, marker: 'none', collection_id: 'kb_1',
    ...over,
  }
}

function renderExplorer() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <LibraryExplorer initialWorkspaceId="ws-1" onClose={vi.fn()} onPopOut={vi.fn()} />
    </QueryClientProvider>,
  )
}

async function openDelete(name: string) {
  await waitFor(() => expect(screen.getByTestId(`library-row-${name}`)).toBeInTheDocument())
  fireEvent.click(screen.getByTestId(`library-row-${name}`))
  fireEvent.pointerDown(screen.getByTestId(`library-row-menu-${name}`), { button: 0 })
  fireEvent.click(await screen.findByRole('menuitem', { name: /delete/i }))
  await waitFor(() => expect(screen.getByTestId('library-delete-confirm')).toBeInTheDocument())
}

beforeEach(() => {
  vi.clearAllMocks()
  useUiStore.setState({ toasts: [] })
  vi.mocked(fetchLibraryWorkspaces).mockResolvedValue([])
  vi.mocked(fetchLibraryContent).mockResolvedValue({
    path: 'shot.png', content: '', size: 0, is_text: false, too_large: false,
  })
  vi.mocked(fetchKnowledgeBaseInfo).mockResolvedValue(knowledgeInfo())
  vi.mocked(fetchKnowledgeOutline).mockResolvedValue({ path: '', is_knowledge_base: false, headings: [] })
  vi.mocked(fetchKnowledgeGraph).mockResolvedValue({
    collection_id: 'kb_1', kind: 'backlinks', nodes: [], edges: [], skipped: [], truncated: false,
  })
  vi.mocked(searchVault).mockResolvedValue({ collection_id: 'kb_1', complete: true, notes: [], records: [], views: [] })
  vi.mocked(searchFiles).mockResolvedValue({
    hits: [], truncated: false,
    limits_applied: {
      files: 1, bytes: 1, matches: 1, matches_per_file: 1, depth: 1, deadline_ms: 1, output_bytes: 1,
    },
    stats: {
      files_visited: 0, bytes_scanned: 0, files_skipped_problems: 0, files_pruned_ignored: 0,
      files_skipped_per_file_cap: 0, hits_capped_per_file: 0,
    },
  })
  useKnowledgeIndexStore.setState({ byCollection: {} })
})

describe('Library delete confirmation for a chat file (D26, S-147, S-148)', () => {
  it('S-147 names the chat copy when the file has one', async () => {
    mockedEntries.mockResolvedValue([makeEntry({ chat_copy: true })])
    renderExplorer()
    await openDelete('shot.png')
    const dialog = screen.getByRole('alertdialog')
    expect(dialog).toHaveTextContent(/copy used in chats is deleted too/i)
    expect(dialog).toHaveTextContent(/unavailable/i)
    expect(mockedDelete).not.toHaveBeenCalled()
  })

  it('S-147 keeps today\'s dialog when the file has no chat copy', async () => {
    mockedEntries.mockResolvedValue([makeEntry({ chat_copy: false })])
    renderExplorer()
    await openDelete('shot.png')
    const dialog = screen.getByRole('alertdialog')
    expect(dialog).toHaveTextContent(/permanently deleted/i)
    expect(dialog).not.toHaveTextContent(/copy used in chats/i)
  })

  it('S-148 keeps the file listed and names it when deletion fails part-way', async () => {
    mockedEntries.mockResolvedValue([makeEntry({ chat_copy: true })])
    mockedDelete.mockRejectedValue(new ApiError(500, 'could not delete shot.png completely'))
    renderExplorer()
    await openDelete('shot.png')
    fireEvent.click(screen.getByTestId('library-delete-confirm'))
    expect(await screen.findByText(/could not delete shot\.png completely/i)).toBeInTheDocument()
    expect(screen.getByTestId('library-row-shot.png')).toBeInTheDocument()
    expect(screen.getByTestId('library-delete-confirm')).toBeEnabled()
  })
})
