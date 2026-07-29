// LibraryExplorer.test.tsx — the file-explorer core both Library entry
// points render (library-spec.md D-2/D-3). Covers: virtual-root workspace
// list, drilling into a workspace, the D-8 "Show Hidden" toggle revealing
// work/.library/, and the destructive-action (delete) confirm step.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useUiStore } from '@/store/ui'
import type { LibraryEntry, LibraryWorkspaceNode } from '@/lib/api'

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
    libraryDownloadUrl: vi.fn((wsId: string, path: string) => `/api/v1/library/${wsId}/download?path=${path}`),
  }
})

import {
  fetchLibraryWorkspaces,
  fetchLibraryEntries,
  fetchLibraryContent,
  deleteLibraryEntry,
  renameLibraryEntry,
  moveLibraryEntry,
  uploadLibraryFiles,
  ApiError,
} from '@/lib/api'

const mockedFetchWorkspaces = vi.mocked(fetchLibraryWorkspaces)
const mockedFetchEntries = vi.mocked(fetchLibraryEntries)
const mockedFetchContent = vi.mocked(fetchLibraryContent)
const mockedDelete = vi.mocked(deleteLibraryEntry)
const mockedRename = vi.mocked(renameLibraryEntry)
const mockedMove = vi.mocked(moveLibraryEntry)
const mockedUpload = vi.mocked(uploadLibraryFiles)

import { LibraryExplorer } from './LibraryExplorer'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderExplorer(initialWorkspaceId?: string) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <LibraryExplorer initialWorkspaceId={initialWorkspaceId} onClose={vi.fn()} onPopOut={vi.fn()} />
    </QueryClientProvider>,
  )
}

function makeWorkspaceNode(over: Partial<LibraryWorkspaceNode> = {}): LibraryWorkspaceNode {
  return { id: 'ws-1', name: 'Website API', entry_count: 3, ...over }
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

beforeEach(() => {
  vi.clearAllMocks()
  act_setToasts()
  // Deterministic default so selecting the default markdown entry (see
  // makeEntry) never fires an unmocked network call — LibraryPreviewPane
  // fetches content for markdown/mermaid/text kinds as soon as a file is
  // selected. Individual tests override this when the content itself matters.
  mockedFetchContent.mockResolvedValue({
    path: 'report.md',
    content: '# Report\n',
    size: 9,
    is_text: true,
    too_large: false,
  })
})

function act_setToasts() {
  useUiStore.setState({ toasts: [] })
}

describe('LibraryExplorer — virtual root (sidebar entry point, D-3)', () => {
  it('lists every workspace as a top-level node when opened with no initial workspace', async () => {
    mockedFetchWorkspaces.mockResolvedValue([
      makeWorkspaceNode({ id: 'ws-1', name: 'Website API', entry_count: 3 }),
      makeWorkspaceNode({ id: 'ws-2', name: 'Marketing', entry_count: 0 }),
    ])

    renderExplorer(undefined)

    await waitFor(() => {
      expect(screen.getByTestId('library-workspace-node-ws-1')).toBeInTheDocument()
    })
    expect(screen.getByText('Website API')).toBeInTheDocument()
    expect(screen.getByText('Marketing')).toBeInTheDocument()
    expect(mockedFetchEntries).not.toHaveBeenCalled()
  })

  it('drills into a workspace on click, scoping subsequent listing calls to it (same component, D-3)', async () => {
    mockedFetchWorkspaces.mockResolvedValue([makeWorkspaceNode({ id: 'ws-1', name: 'Website API' })])
    mockedFetchEntries.mockResolvedValue([makeEntry({ name: 'notes', path: 'notes', is_dir: true })])

    renderExplorer(undefined)

    await waitFor(() => expect(screen.getByTestId('library-workspace-node-ws-1')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-workspace-node-ws-1'))

    await waitFor(() => {
      expect(mockedFetchEntries).toHaveBeenCalledWith('ws-1', '', false)
    })
    expect(screen.getByText('Website API')).toBeInTheDocument() // now the breadcrumb workspace crumb
  })
})

describe('LibraryExplorer — scoped entry point (chat/header bar, D-3)', () => {
  it('lands directly inside the given workspace when opened with an initialWorkspaceId', async () => {
    mockedFetchWorkspaces.mockResolvedValue([makeWorkspaceNode({ id: 'ws-9', name: 'Ops' })])
    mockedFetchEntries.mockResolvedValue([])

    renderExplorer('ws-9')

    await waitFor(() => {
      expect(mockedFetchEntries).toHaveBeenCalledWith('ws-9', '', false)
    })
    // No workspace-node buttons rendered — we start scoped, not at the root.
    expect(screen.queryByTestId('library-workspace-node-ws-9')).not.toBeInTheDocument()
  })
})

describe('LibraryExplorer — Show Hidden toggle (D-8)', () => {
  it('defaults to hiding dot-prefixed entries and include_hidden=false is sent', async () => {
    mockedFetchEntries.mockResolvedValue([makeEntry({ name: 'report.md', path: 'report.md' })])
    mockedFetchWorkspaces.mockResolvedValue([])

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByText('report.md')).toBeInTheDocument())
    expect(mockedFetchEntries).toHaveBeenCalledWith('ws-1', '', false)
    expect(screen.queryByText('.library')).not.toBeInTheDocument()
  })

  it('reveals work/.library/ once Show Hidden is toggled on — the whole point of the toggle', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockImplementation((_wsId, _path, includeHidden) =>
      Promise.resolve(
        includeHidden
          ? [
              makeEntry({ name: 'report.md', path: 'report.md' }),
              makeEntry({ name: '.library', path: '.library', is_dir: true, is_hidden: true, is_text_editable: false }),
            ]
          : [makeEntry({ name: 'report.md', path: 'report.md' })],
      ),
    )

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByText('report.md')).toBeInTheDocument())
    expect(screen.queryByText('.library')).not.toBeInTheDocument()

    fireEvent.click(screen.getByTestId('library-show-hidden-toggle'))

    await waitFor(() => {
      expect(mockedFetchEntries).toHaveBeenCalledWith('ws-1', '', true)
    })
    await waitFor(() => expect(screen.getByText('.library')).toBeInTheDocument())
    // Styled distinctly when shown (library-spec.md D-8: is_hidden entries
    // must be visually distinguishable even when the toggle reveals them).
    expect(screen.getByTestId('library-hidden-badge-.library')).toBeInTheDocument()
  })
})

describe('LibraryExplorer — destructive-action confirm (delete)', () => {
  it('selecting a file opens the preview pane; Delete requires an explicit confirm before deleteLibraryEntry fires', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValueOnce([makeEntry({ name: 'report.md', path: 'report.md' })])
    mockedFetchEntries.mockResolvedValueOnce([]) // after invalidation, refetch shows it gone
    mockedDelete.mockResolvedValue(undefined)

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-row-report.md')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-row-report.md'))

    await waitFor(() => expect(screen.getByTestId('library-preview-pane')).toBeInTheDocument())
    const pane = screen.getByTestId('library-preview-pane')
    fireEvent.click(within(pane).getByRole('button', { name: /delete/i }))

    // The delete has NOT happened yet — a confirm dialog must appear first.
    expect(mockedDelete).not.toHaveBeenCalled()
    await waitFor(() => expect(screen.getByTestId('library-delete-confirm')).toBeInTheDocument())

    fireEvent.click(screen.getByTestId('library-delete-confirm'))

    await waitFor(() => expect(mockedDelete).toHaveBeenCalledWith('ws-1', 'report.md'))
    await waitFor(() => expect(screen.queryByTestId('library-row-report.md')).not.toBeInTheDocument())
    // Preview pane closes once its entry is gone.
    expect(screen.queryByTestId('library-preview-pane')).not.toBeInTheDocument()
  })

  it('cancelling the confirm dialog does NOT call deleteLibraryEntry', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([makeEntry({ name: 'draft.md', path: 'draft.md' })])

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-row-draft.md')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-row-draft.md'))

    await waitFor(() => expect(screen.getByTestId('library-preview-pane')).toBeInTheDocument())
    fireEvent.click(within(screen.getByTestId('library-preview-pane')).getByRole('button', { name: /delete/i }))

    await waitFor(() => expect(screen.getByTestId('library-delete-confirm')).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))

    await waitFor(() => expect(screen.queryByTestId('library-delete-confirm')).not.toBeInTheDocument())
    expect(mockedDelete).not.toHaveBeenCalled()
    expect(screen.getByTestId('library-row-draft.md')).toBeInTheDocument()
  })
})

describe('LibraryExplorer — unsaved-edit navigation guard', () => {
  // Drives the guard directly via its own module (preview/unsavedGuard.ts)
  // rather than through a real CodeMirror keystroke: CodeMirror 6 mounts a
  // contenteditable div with its own internal DOM-mutation handling that
  // jsdom cannot reliably simulate through fireEvent, but the integration
  // point THIS test exists to cover — LibraryExplorer's navigation handlers
  // calling confirmDiscardLibraryEdits() before switching files — only
  // depends on the guard's boolean state, not on how it got set. The
  // editor's own dirty-tracking (draft !== last-saved) is covered separately
  // by useLibraryFileEditor.test.ts.
  it('warns before switching to a different file while an editor has unsaved edits, and stays put if the user cancels', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([
      makeEntry({ name: 'report.md', path: 'report.md' }),
      makeEntry({ name: 'draft.md', path: 'draft.md' }),
    ])

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-row-report.md')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-row-report.md'))
    await waitFor(() => expect(screen.getByTestId('library-preview-pane')).toBeInTheDocument())

    const { setLibraryEditorDirty } = await import('./preview/unsavedGuard')
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)
    setLibraryEditorDirty(true)

    fireEvent.click(screen.getByTestId('library-row-draft.md'))

    expect(confirmSpy).toHaveBeenCalled()
    // Still on report.md — draft.md's content was never requested.
    expect(mockedFetchContent).not.toHaveBeenCalledWith('ws-1', 'draft.md')

    confirmSpy.mockRestore()
    setLibraryEditorDirty(false)
  })
})

describe('LibraryExplorer — empty and error states', () => {
  it('shows an empty state when a workspace has no visible entries', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([])

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByText(/no files in this workspace yet/i)).toBeInTheDocument())
  })

  it('shows a retryable error state when listing entries fails', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockRejectedValue(new Error('boom'))

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-entries-error')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
  })
})

// UAT fix (Dana, live tester) — Rename/Move/Upload failures were
// indistinguishable from nothing happening: the dialog quietly reverted to
// its idle state with zero error text, and a Move destination's leading "/"
// was silently stripped before being sent, so the server acted on a
// different path than the one the user typed. These tests prove each
// failure path now surfaces a message the user can act on (the server's own
// reason where available), keeps the dialog open with their input intact,
// and that the client never silently rewrites what was typed.
describe('LibraryExplorer — surfacing real failures (rename / move / upload)', () => {
  async function openPreviewAndClick(nameRegex: RegExp) {
    await waitFor(() => expect(screen.getByTestId('library-preview-pane')).toBeInTheDocument())
    fireEvent.click(within(screen.getByTestId('library-preview-pane')).getByRole('button', { name: nameRegex }))
  }

  it('a failed rename shows a persistent, actionable error banner — dialog stays open, typed name preserved, button reverts (never silent)', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([makeEntry({ name: 'report.md', path: 'report.md' })])
    mockedRename.mockRejectedValue(new ApiError(400, 'Invalid path: contains directory traversal characters'))

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-row-report.md')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-row-report.md'))
    await openPreviewAndClick(/rename/i)

    await waitFor(() => expect(screen.getByTestId('library-rename-dialog')).toBeInTheDocument())
    const input = screen.getByTestId('library-rename-input') as HTMLInputElement
    fireEvent.change(input, { target: { value: '..\\dana-pwned-backslash.txt' } })
    fireEvent.click(screen.getByTestId('library-rename-confirm'))

    await waitFor(() => {
      expect(screen.getByTestId('library-rename-error')).toHaveTextContent(
        'Invalid path: contains directory traversal characters',
      )
    })
    // Dialog stayed open with the edit intact — not a silent revert.
    expect(screen.getByTestId('library-rename-dialog')).toBeInTheDocument()
    expect(input.value).toBe('..\\dana-pwned-backslash.txt')
    expect(screen.getByTestId('library-rename-confirm')).toHaveTextContent('Rename')
    // Same two-channel pattern as the save flow: banner AND toast.
    expect(useUiStore.getState().toasts.some((t) => t.variant === 'error')).toBe(true)
  })

  it('rejects a leading "/" in a Move destination instead of silently stripping it before sending', async () => {
    mockedFetchWorkspaces.mockResolvedValue([makeWorkspaceNode({ id: 'ws-1', name: 'Website API' })])
    mockedFetchEntries.mockResolvedValue([makeEntry({ name: 'report.md', path: 'report.md' })])

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-row-report.md')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-row-report.md'))
    await openPreviewAndClick(/move/i)

    await waitFor(() => expect(screen.getByTestId('library-transfer-dialog')).toBeInTheDocument())
    const pathInput = screen.getByTestId('library-transfer-path') as HTMLInputElement
    fireEvent.change(pathInput, { target: { value: '/etc/dana-abs-test.txt' } })

    await waitFor(() => expect(screen.getByTestId('library-transfer-leading-slash')).toBeInTheDocument())
    expect(screen.getByTestId('library-transfer-confirm')).toBeDisabled()

    fireEvent.click(screen.getByTestId('library-transfer-confirm'))
    expect(mockedMove).not.toHaveBeenCalled()
    // The input still shows exactly what the user typed — nothing was
    // silently rewritten to a different path behind their back.
    expect(pathInput.value).toBe('/etc/dana-abs-test.txt')
  })

  it('a failed move shows a persistent, actionable error banner — dialog stays open with the destination preserved', async () => {
    mockedFetchWorkspaces.mockResolvedValue([makeWorkspaceNode({ id: 'ws-1', name: 'Website API' })])
    mockedFetchEntries.mockResolvedValue([makeEntry({ name: 'report.md', path: 'report.md' })])
    mockedMove.mockRejectedValue(new ApiError(404, 'Destination workspace not found'))

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-row-report.md')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-row-report.md'))
    await openPreviewAndClick(/move/i)

    await waitFor(() => expect(screen.getByTestId('library-transfer-dialog')).toBeInTheDocument())
    const pathInput = screen.getByTestId('library-transfer-path') as HTMLInputElement
    fireEvent.change(pathInput, { target: { value: 'archive/report.md' } })
    fireEvent.click(screen.getByTestId('library-transfer-confirm'))

    await waitFor(() => {
      expect(screen.getByTestId('library-transfer-error')).toHaveTextContent('Destination workspace not found')
    })
    expect(screen.getByTestId('library-transfer-dialog')).toBeInTheDocument()
    expect(pathInput.value).toBe('archive/report.md')
    expect(useUiStore.getState().toasts.some((t) => t.variant === 'error')).toBe(true)
  })

  it('a failed upload shows a persistent, dismissible error banner instead of the file just silently never appearing', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([])
    mockedUpload.mockRejectedValue(new ApiError(400, 'Upload rejected: filename contains invalid characters'))

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-upload-button')).toBeInTheDocument())
    const fileInput = screen.getByTestId('library-upload-input') as HTMLInputElement
    const file = new File(['data'], '..\\dana-upload-traversal.txt', { type: 'text/plain' })
    fireEvent.change(fileInput, { target: { files: [file] } })

    await waitFor(() => {
      expect(screen.getByTestId('library-upload-error')).toHaveTextContent(
        'Upload rejected: filename contains invalid characters',
      )
    })
    expect(useUiStore.getState().toasts.some((t) => t.variant === 'error')).toBe(true)

    fireEvent.click(screen.getByRole('button', { name: /dismiss error/i }))
    expect(screen.queryByTestId('library-upload-error')).not.toBeInTheDocument()
  })
})
