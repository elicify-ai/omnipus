// LibraryExplorer.newNote.test.tsx — UAT #699 / D-115 (2026-09-13): the
// Library can create a note. Two doors, both asserted: the create menu's
// "New note", and the empty knowledge base's "Write the first note" button,
// which used to be absent because no handler was ever wired to KnowledgePanel.
//
// Fails on the old code: `library-create-menu-new-note` did not exist,
// `knowledge-create-note` was never rendered (the panel printed
// `knowledge-create-note-unavailable` instead), and `createLibraryTextFile`
// did not exist to be called.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useUiStore } from '@/store/ui'
import { useKnowledgeIndexStore } from '@/store/knowledgeIndex'
import type { LibraryEntry } from '@/lib/api'
import type { KnowledgeBaseInfo } from '@/lib/api/generated/openapi-types'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchLibraryWorkspaces: vi.fn(),
    fetchLibraryEntries: vi.fn(),
    fetchLibraryContent: vi.fn(),
    createLibraryTextFile: vi.fn(),
    uploadLibraryFiles: vi.fn(),
    fetchKnowledgeBaseInfo: vi.fn(),
    fetchKnowledgeOutline: vi.fn(),
    fetchKnowledgeGraph: vi.fn(),
    searchVault: vi.fn(),
    searchFiles: vi.fn(),
    libraryDownloadUrl: vi.fn((wsId: string, path: string) => `/api/v1/library/${wsId}/download?path=${path}`),
    mintLibraryPreviewToken: vi.fn(),
  }
})

import {
  fetchLibraryWorkspaces,
  fetchLibraryEntries,
  fetchLibraryContent,
  createLibraryTextFile,
  uploadLibraryFiles,
  fetchKnowledgeBaseInfo,
  fetchKnowledgeOutline,
  ApiError,
} from '@/lib/api'
import { LibraryExplorer, uploadOutcomeMessage } from './LibraryExplorer'

const mockedFetchWorkspaces = vi.mocked(fetchLibraryWorkspaces)
const mockedFetchEntries = vi.mocked(fetchLibraryEntries)
const mockedFetchContent = vi.mocked(fetchLibraryContent)
const mockedCreate = vi.mocked(createLibraryTextFile)
const mockedKnowledgeInfo = vi.mocked(fetchKnowledgeBaseInfo)
const mockedOutline = vi.mocked(fetchKnowledgeOutline)

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function makeEntry(over: Partial<LibraryEntry> = {}): LibraryEntry {
  return {
    name: 'report.md',
    path: 'report.md',
    is_dir: false,
    is_hidden: false,
    size: 2048,
    modified_at: '2026-07-28T10:15:00Z',
    mime: 'text/markdown',
    is_text_editable: true,
    ...over,
  }
}

function makeKnowledgeInfo(over: Partial<KnowledgeBaseInfo> = {}): KnowledgeBaseInfo {
  return {
    workspace_id: 'ws-1',
    root_path: 'notes',
    is_knowledge_base: false,
    marker: 'none',
    ...over,
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  useUiStore.setState({ toasts: [] })
  useKnowledgeIndexStore.setState({ byCollection: {} })
  mockedFetchWorkspaces.mockResolvedValue([])
  mockedKnowledgeInfo.mockResolvedValue(makeKnowledgeInfo())
  mockedOutline.mockResolvedValue({ is_knowledge_base: false, headings: [] } as never)
  mockedFetchContent.mockResolvedValue({ content: '# Meeting\n\n', is_text: true, too_large: false } as never)
})

describe('LibraryExplorer — New note (UAT #699 / D-115)', () => {
  it('create menu → New note → the file is created with the ABSENT version token, inside the browsed folder, and selected', async () => {
    mockedFetchEntries.mockResolvedValue([makeEntry({ name: 'existing.md', path: 'Q4/existing.md' })])
    // The REAL LibraryVersionedResult shape ({ data, version }) — the first
    // draft of this test mirrored a wrong `{ value }` shape from the code
    // under test and passed against it; typecheck caught it, this did not.
    mockedCreate.mockResolvedValue({
      data: makeEntry({ name: 'Meeting.md', path: 'Q4/Meeting.md', size: 12 }),
      version: 'v1:abc',
    })
    const onAddressChange = vi.fn()
    render(
      <QueryClientProvider client={makeClient()}>
        <LibraryExplorer address={{ workspaceId: 'ws-1', folder: 'Q4' }} onAddressChange={onAddressChange} />
      </QueryClientProvider>,
    )
    await waitFor(() => expect(screen.getByTestId('library-row-Q4/existing.md')).toBeInTheDocument())

    fireEvent.pointerDown(screen.getByTestId('library-create-menu-trigger'))
    fireEvent.click(screen.getByTestId('library-create-menu-trigger'))
    const item = await screen.findByTestId('library-create-menu-new-note')
    fireEvent.click(item)

    const input = await screen.findByTestId('library-new-note-input')
    fireEvent.change(input, { target: { value: 'Meeting' } })
    fireEvent.click(screen.getByTestId('library-new-note-confirm'))

    await waitFor(() => expect(mockedCreate).toHaveBeenCalledTimes(1))
    expect(mockedCreate).toHaveBeenCalledWith('ws-1', 'Q4/Meeting.md', '# Meeting\n\n')
    // The created note is SELECTED — the address names it, so the pane opens on it.
    await waitFor(() =>
      expect(onAddressChange).toHaveBeenCalledWith({ workspaceId: 'ws-1', path: 'Q4/Meeting.md' }),
    )
    await waitFor(() => expect(screen.queryByTestId('library-new-note-dialog')).not.toBeInTheDocument())
    expect(useUiStore.getState().toasts.some((t) => /Created Meeting\.md/.test(t.message))).toBe(true)
  })

  it('a 409 from the server keeps the dialog open with the reason', async () => {
    mockedFetchEntries.mockResolvedValue([])
    mockedCreate.mockRejectedValue(
      new ApiError(409, 'conflict', { body: JSON.stringify({ error: 'notes.md changed on disk since you opened it' }) }),
    )
    render(
      <QueryClientProvider client={makeClient()}>
        <LibraryExplorer initialWorkspaceId="ws-1" />
      </QueryClientProvider>,
    )
    await waitFor(() => expect(mockedFetchEntries).toHaveBeenCalled())
    fireEvent.pointerDown(screen.getByTestId('library-create-menu-trigger'))
    fireEvent.click(screen.getByTestId('library-create-menu-trigger'))
    fireEvent.click(await screen.findByTestId('library-create-menu-new-note'))
    fireEvent.change(await screen.findByTestId('library-new-note-input'), { target: { value: 'notes' } })
    fireEvent.click(screen.getByTestId('library-new-note-confirm'))
    await waitFor(() => expect(screen.getByTestId('library-new-note-error')).toHaveTextContent(/changed on disk/))
    expect(screen.getByTestId('library-new-note-dialog')).toBeInTheDocument()
  })

  it('an EMPTY knowledge base offers "Write the first note", which opens the same dialog', async () => {
    mockedFetchEntries.mockResolvedValue([])
    mockedKnowledgeInfo.mockResolvedValue(
      makeKnowledgeInfo({ is_knowledge_base: true, marker: 'omnipus_vault', collection_id: 'kb_1' }),
    )
    useKnowledgeIndexStore.setState({
      byCollection: {
        kb_1: {
          type: 'knowledge_index_progress',
          collection_id: 'kb_1',
          workspace_id: 'ws-1',
          phase: 'idle',
          indexed_files: 0,
          total_known: true,
          total_files: 0,
        },
      },
    })
    render(
      <QueryClientProvider client={makeClient()}>
        <LibraryExplorer initialWorkspaceId="ws-1" />
      </QueryClientProvider>,
    )
    const button = await screen.findByTestId('knowledge-create-note')
    expect(screen.queryByTestId('knowledge-create-note-unavailable')).not.toBeInTheDocument()
    fireEvent.click(button)
    expect(await screen.findByTestId('library-new-note-dialog')).toBeInTheDocument()
  })
})

// UAT D-124 (2026-09-13): the upload toast never said a file was renamed.
describe('LibraryExplorer — D-124 the upload toast names a renamed file', () => {
  it('uploadOutcomeMessage states each rename, and stays terse when nothing was renamed', () => {
    expect(uploadOutcomeMessage(['a.png'], ['a.png'])).toBe('Uploaded 1 file.')
    expect(uploadOutcomeMessage(['a.png', 'b.png'], ['a.png', 'b.png'])).toBe('Uploaded 2 files.')
    expect(uploadOutcomeMessage(['photo.png'], ['photo (2).png'])).toBe(
      'Uploaded 1 file. photo.png was saved as photo (2).png because that name was already taken.',
    )
    expect(uploadOutcomeMessage(['a.png', 'b.png'], ['a (1).png', 'b (1).png'])).toMatch(
      /a\.png was saved as a \(1\)\.png; b\.png was saved as b \(1\)\.png because those names were already taken\./,
    )
  })

  it('the toast after a duplicate upload names the new file name', async () => {
    mockedFetchEntries.mockResolvedValue([])
    vi.mocked(uploadLibraryFiles).mockResolvedValue({
      entries: [makeEntry({ name: 'photo (2).png', path: 'photo (2).png', mime: 'image/png', is_text_editable: false })],
    } as never)
    render(
      <QueryClientProvider client={makeClient()}>
        <LibraryExplorer initialWorkspaceId="ws-1" />
      </QueryClientProvider>,
    )
    await waitFor(() => expect(screen.getByTestId('library-upload-input')).toBeInTheDocument())
    const file = new File(['data'], 'photo.png', { type: 'image/png' })
    fireEvent.change(screen.getByTestId('library-upload-input'), { target: { files: [file] } })
    await waitFor(() =>
      expect(
        useUiStore
          .getState()
          .toasts.some((t) => t.message === 'Uploaded 1 file. photo.png was saved as photo (2).png because that name was already taken.'),
      ).toBe(true),
    )
  })
})
