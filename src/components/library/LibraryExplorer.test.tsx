// LibraryExplorer.test.tsx — the file-explorer core both Library entry
// points render (library-spec.md D-2/D-3). Covers: virtual-root workspace
// list, drilling into a workspace, the D-8 "Show Hidden" toggle revealing
// work/.library/, and the destructive-action (delete) confirm step.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useUiStore } from '@/store/ui'
import type { LibraryEntry, LibraryWorkspaceNode } from '@/lib/api'
import type { KnowledgeBaseInfo } from '@/lib/api/generated/openapi-types'
import { useKnowledgeIndexStore } from '@/store/knowledgeIndex'

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
    // ADR-067 stage 2. These were NOT mocked, so `...actual` handed the real
    // clients to KnowledgePanel and the reading view: the real fetch ran, the
    // query failed, and the panel rendered its red `knowledge-panel-error`
    // alert card in every single test in this file while they all reported
    // green. Exactly the shape docs/internal/false-green-patterns.md warns
    // about — a screen nobody asserted on, quietly broken.
    fetchKnowledgeBaseInfo: vi.fn(),
    fetchKnowledgeOutline: vi.fn(),
    fetchKnowledgeGraph: vi.fn(),
    // unified-search-and-grep-spec.md US-1/US-2: LibrarySearchBar (mounted by
    // LibraryExplorer for every workspace folder, not only KnowledgePanel's
    // vault-only surface any more) reaches these two directly. Same reasoning
    // as the ADR-067-stage-2 comment above — leaving either real would send
    // this whole file's tests to the real fetch the moment any test typed
    // into the bar.
    searchVault: vi.fn(),
    searchFiles: vi.fn(),
    // D-117 response half (2026-09-14 fix round): the Add-mount dialog asks
    // the folder listing for a typed path's verdict before submitting, and
    // the explorer POSTs the create — both must be mocked or this file's
    // renders hit the real fetch the moment a test opens that dialog.
    fetchHostFolders: vi.fn(),
    createWorkspaceMount: vi.fn(),
    deleteWorkspaceMount: vi.fn(),
    libraryDownloadUrl: vi.fn((wsId: string, path: string) => `/api/v1/library/${wsId}/download?path=${path}`),
    // HP-1 fix (defect-list-html-preview-2026-09-08.md): LibraryPreviewPane's
    // PREVIEW_TOKEN_MINTER now resolves to the real mintLibraryPreviewToken
    // when its mintPreviewToken prop is omitted (as this file's renders all
    // do). No test here currently opens an .html entry, so this mock is never
    // called today — it is here so one that does in the future doesn't fall
    // into the exact `...actual` trap the comment above already warns about.
    mintLibraryPreviewToken: vi.fn(),
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
  mkdirLibraryEntry,
  fetchKnowledgeBaseInfo,
  fetchKnowledgeOutline,
  fetchKnowledgeGraph,
  searchVault,
  searchFiles,
  fetchHostFolders,
  createWorkspaceMount,
  deleteWorkspaceMount,
  ApiError,
} from '@/lib/api'

const mockedFetchWorkspaces = vi.mocked(fetchLibraryWorkspaces)
const mockedFetchEntries = vi.mocked(fetchLibraryEntries)
const mockedFetchContent = vi.mocked(fetchLibraryContent)
const mockedDelete = vi.mocked(deleteLibraryEntry)
const mockedRename = vi.mocked(renameLibraryEntry)
const mockedMove = vi.mocked(moveLibraryEntry)
const mockedUpload = vi.mocked(uploadLibraryFiles)
const mockedMkdir = vi.mocked(mkdirLibraryEntry)
const mockedKnowledgeInfo = vi.mocked(fetchKnowledgeBaseInfo)
const mockedKnowledgeOutline = vi.mocked(fetchKnowledgeOutline)
const mockedKnowledgeGraph = vi.mocked(fetchKnowledgeGraph)
const mockedSearchVault = vi.mocked(searchVault)
const mockedSearchFiles = vi.mocked(searchFiles)
const mockedHostFolders = vi.mocked(fetchHostFolders)
const mockedCreateMount = vi.mocked(createWorkspaceMount)
const mockedDeleteMount = vi.mocked(deleteWorkspaceMount)

import { LibraryExplorer } from './LibraryExplorer'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderExplorer(initialWorkspaceId?: string, over: { layout?: 'stacked' | 'split' } = {}) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <LibraryExplorer
        initialWorkspaceId={initialWorkspaceId}
        onClose={vi.fn()}
        onPopOut={vi.fn()}
        {...over}
      />
    </QueryClientProvider>,
  )
}

function makeWorkspaceNode(over: Partial<LibraryWorkspaceNode> = {}): LibraryWorkspaceNode {
  return { id: 'ws-1', name: 'Website API', entry_count: 3, ...over }
}

/** Serve a directory listing per folder. Module scope so the knowledge-surface
 *  blocks at the end of this file can use it too — it was private to the
 *  deep-linking describe, and hoisting it verbatim is the whole change. */
function entriesByDir(map: Record<string, LibraryEntry[]>) {
  mockedFetchEntries.mockImplementation(async (_wsId: string, dir?: string, includeHidden?: boolean) => {
    const all = map[dir ?? ''] ?? []
    // Mirrors the server: hidden entries are filtered OUT of the listing
    // unless asked for, which is why a dot-prefixed deep-link target needs
    // its own wording rather than "not found".
    return includeHidden ? all : all.filter((e) => !e.name.startsWith('.'))
  })
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
  // Default: an ordinary folder and an ordinary markdown file. Both are the
  // common case, and both make the knowledge surfaces render nothing, so the
  // tests in this file that are about the FILE EXPLORER stay about the file
  // explorer. Tests that are about the knowledge surface override these.
  mockedKnowledgeInfo.mockResolvedValue(makeKnowledgeInfo({ is_knowledge_base: false, marker: 'none' }))
  mockedKnowledgeOutline.mockResolvedValue({
    path: 'report.md',
    is_knowledge_base: false,
    headings: [],
  })
  mockedKnowledgeGraph.mockResolvedValue({
    collection_id: 'kb_1',
    kind: 'backlinks',
    nodes: [],
    edges: [],
    skipped: [],
    truncated: false,
  })
  // Defaults for LibrarySearchBar's two search clients (unified-search-and-
  // grep-spec.md US-1/US-2) — empty-but-complete, matching the same "the
  // common case makes the surface render nothing" reasoning as the mocks
  // above. Tests that actually type into the bar override these.
  mockedSearchVault.mockResolvedValue({
    collection_id: 'kb_1',
    complete: true,
    notes: [],
    records: [],
    views: [],
  })
  mockedSearchFiles.mockResolvedValue({
    hits: [],
    truncated: false,
    limits_applied: {
      files: 50000,
      bytes: 268435456,
      matches: 1000,
      matches_per_file: 50,
      depth: 32,
      deadline_ms: 3000,
      output_bytes: 1048576,
    },
    stats: {
      files_visited: 0,
      bytes_scanned: 0,
      files_skipped_problems: 0,
      files_pruned_ignored: 0,
      files_skipped_per_file_cap: 0,
      hits_capped_per_file: 0,
    },
  })
  useKnowledgeIndexStore.setState({ byCollection: {} })
})

function makeKnowledgeInfo(over: Partial<KnowledgeBaseInfo> = {}): KnowledgeBaseInfo {
  return {
    workspace_id: 'ws-1',
    root_path: 'notes',
    is_knowledge_base: true,
    marker: 'omnipus_vault',
    collection_id: 'kb_1',
    ...over,
  }
}

function act_setToasts() {
  useUiStore.setState({ toasts: [] })
}

// Download / Rename / Move / Delete were removed from the preview pane
// (operator direction, 2026-08-04 — three header bars collapsed to one), so the
// entry row's own DotsThree menu is now the single path to all four. The flows
// themselves are unchanged; only the affordance that starts them moved.
async function openRowMenuAndClick(path: string, nameRegex: RegExp) {
  fireEvent.pointerDown(screen.getByTestId(`library-row-menu-${path}`), { ctrlKey: false, button: 0 })
  fireEvent.click(await screen.findByRole('menuitem', { name: nameRegex }))
}

// New Folder / Upload / Add mount / Manage mounts / New vault / New
// workspace collapsed into one "+" create menu (feature C2) — this is the
// toolbar-level sibling of openRowMenuAndClick above, same open-by-pointerdown
// mechanism (same DropdownMenu primitive).
async function openCreateMenuAndClick(nameRegex: RegExp) {
  fireEvent.pointerDown(screen.getByTestId('library-create-menu-trigger'), { ctrlKey: false, button: 0 })
  fireEvent.click(await screen.findByRole('menuitem', { name: nameRegex }))
}

/** Opens the create menu, reads whether the named item is disabled, then
 * closes the menu again so the caller's next interaction (a click elsewhere
 * on the page) isn't racing an open dropdown. */
async function createMenuItemDisabled(nameRegex: RegExp): Promise<boolean> {
  fireEvent.pointerDown(screen.getByTestId('library-create-menu-trigger'), { ctrlKey: false, button: 0 })
  const item = await screen.findByRole('menuitem', { name: nameRegex })
  const disabled = item.getAttribute('data-disabled') !== null
  fireEvent.keyDown(document, { key: 'Escape' })
  await waitFor(() => expect(screen.queryByRole('menuitem', { name: nameRegex })).not.toBeInTheDocument())
  return disabled
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

  // US-4 AS-2: "one bar, every Library location" is only true if the bar is
  // actually MOUNTED at the virtual root. A real-browser UAT found it absent
  // there entirely — the root rendered its workspace list INSTEAD of the bar,
  // so the promise held everywhere except the first screen a person sees.
  // LibrarySearchBar already knew how to render disabled for a null
  // workspaceId; nothing mounted it.
  it('renders the search bar DISABLED at the virtual root, wrapping the workspace list', async () => {
    mockedFetchWorkspaces.mockResolvedValue([
      makeWorkspaceNode({ id: 'ws-1', name: 'Website API', entry_count: 3 }),
    ])

    renderExplorer(undefined)

    await waitFor(() => {
      expect(screen.getByTestId('library-workspace-node-ws-1')).toBeInTheDocument()
    })

    const boxes = screen.getAllByRole('searchbox')
    expect(boxes).toHaveLength(1)
    expect(boxes[0]).toBeDisabled()
    // The workspace list must still render THROUGH the bar, not be replaced
    // by it — the bar renders `children` untouched while disabled.
    expect(screen.getByText('Website API')).toBeInTheDocument()
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
    await openRowMenuAndClick('report.md', /delete/i)

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
    await openRowMenuAndClick('draft.md', /delete/i)

    await waitFor(() => expect(screen.getByTestId('library-delete-confirm')).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))

    await waitFor(() => expect(screen.queryByTestId('library-delete-confirm')).not.toBeInTheDocument())
    expect(mockedDelete).not.toHaveBeenCalled()
    expect(screen.getByTestId('library-row-draft.md')).toBeInTheDocument()
  })

  // UAT D-121 (2026-09-13): the knowledge-base marker folder got the generic
  // folder wording, which said nothing about it BEING the knowledge base or
  // that the UI cannot make the folder one again afterwards.
  it('deleting ".omnipus-vault" names what it is and what is lost, unlike an ordinary folder', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([
      makeEntry({ name: '.omnipus-vault', path: 'UAT Vault/.omnipus-vault', is_dir: true }),
      makeEntry({ name: 'Assets', path: 'UAT Vault/Assets', is_dir: true }),
    ])

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-row-UAT Vault/.omnipus-vault')).toBeInTheDocument())
    await openRowMenuAndClick('UAT Vault/.omnipus-vault', /delete/i)
    await waitFor(() => expect(screen.getByTestId('library-delete-confirm')).toBeInTheDocument())
    expect(screen.getByRole('alertdialog')).toHaveTextContent(/is what makes "UAT Vault" a knowledge base/i)
    expect(screen.getByRole('alertdialog')).toHaveTextContent(/record types, id sequences, saved views and trash/i)
    expect(screen.getByRole('alertdialog')).toHaveTextContent(/cannot be made one again from here/i)
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))
    await waitFor(() => expect(screen.queryByTestId('library-delete-confirm')).not.toBeInTheDocument())

    // Positive control: an ordinary folder keeps the generic wording.
    await openRowMenuAndClick('UAT Vault/Assets', /delete/i)
    await waitFor(() => expect(screen.getByTestId('library-delete-confirm')).toBeInTheDocument())
    expect(screen.getByRole('alertdialog')).toHaveTextContent(/"Assets" and everything inside it will be permanently deleted/i)
    expect(screen.getByRole('alertdialog')).not.toHaveTextContent(/knowledge base/i)
  })

  // UAT re-test (2026-09-13), U-58, S2 silent failure. Deleting a knowledge
  // base's .omnipus-vault folder is refused server-side (reserved location),
  // but the dialog showed NOTHING at all — no error text, no toast. It just
  // sat there looking exactly like a click that did nothing, so a user has
  // no way to tell the delete failed. This reproduces the server's EXACT
  // repro shape (400 + `{"error":"knowledge: reserved location: ..."}`) and
  // asserts the reason is shown, the dialog stays open (not silently
  // reverted to look like success), and the user can still cancel out.
  it('a failed delete shows the server\'s reason, keeps the dialog open, and lets the user cancel or retry', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([
      makeEntry({ name: '.omnipus-vault', path: 'UAT Vault/.omnipus-vault', is_dir: true }),
    ])
    mockedDelete.mockRejectedValue(
      new ApiError(400, 'Bad request.', {
        body: JSON.stringify({
          error: 'knowledge: reserved location: UAT Vault/.omnipus-vault is the knowledge base marker folder',
        }),
      }),
    )

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-row-UAT Vault/.omnipus-vault')).toBeInTheDocument())
    await openRowMenuAndClick('UAT Vault/.omnipus-vault', /delete/i)
    await waitFor(() => expect(screen.getByTestId('library-delete-confirm')).toBeInTheDocument())

    fireEvent.click(screen.getByTestId('library-delete-confirm'))
    await waitFor(() => expect(mockedDelete).toHaveBeenCalled())

    // The server's own reason must be visible in the dialog itself — not
    // swallowed, not replaced by a generic message.
    await waitFor(() => {
      expect(screen.getByTestId('library-delete-dialog-error')).toHaveTextContent(
        'knowledge: reserved location: UAT Vault/.omnipus-vault is the knowledge base marker folder',
      )
    })
    // A failed destructive action must never look like it succeeded: the
    // confirm dialog stays open and the row is still there.
    expect(screen.getByTestId('library-delete-confirm')).toBeInTheDocument()
    expect(screen.getByTestId('library-row-UAT Vault/.omnipus-vault')).toBeInTheDocument()
    // The confirm button re-enabled (not stuck disabled/"Deleting…") so the
    // user can retry.
    expect(screen.getByTestId('library-delete-confirm')).not.toBeDisabled()
    expect(screen.getByTestId('library-delete-confirm')).not.toHaveTextContent('Deleting…')

    // And cancelling still works after a failure.
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))
    await waitFor(() => expect(screen.queryByTestId('library-delete-confirm')).not.toBeInTheDocument())
  })

  // UAT #701 / D-123 (2026-09-13): inside a knowledge base a note goes to the
  // trash and a rename rewrites links — the dialogs must say so, and a plain
  // folder (no knowledge base) must keep the permanent-delete wording.
  it('a note inside a knowledge base: delete offers "Move to trash", rename mentions links', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([makeEntry({ name: 'Tobias.md', path: 'vault/People/Tobias.md' })])
    mockedKnowledgeInfo.mockResolvedValue(makeKnowledgeInfo({ is_knowledge_base: true, marker: 'omnipus_vault' }))

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-row-vault/People/Tobias.md')).toBeInTheDocument())
    await waitFor(() => expect(mockedKnowledgeInfo).toHaveBeenCalled())
    await openRowMenuAndClick('vault/People/Tobias.md', /delete/i)
    await waitFor(() => expect(screen.getByTestId('library-delete-confirm')).toHaveTextContent('Move to trash'))
    expect(screen.getByRole('alertdialog')).toHaveTextContent(/moved to this knowledge base’s trash/i)
    expect(screen.getByRole('alertdialog')).not.toHaveTextContent(/permanently deleted/i)
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))

    await openRowMenuAndClick('vault/People/Tobias.md', /rename/i)
    await waitFor(() => expect(screen.getByTestId('library-rename-links-note')).toBeInTheDocument())
  })

  it('a note OUTSIDE any knowledge base keeps the permanent-delete wording and no links note', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([makeEntry({ name: 'loose.md', path: 'loose.md' })])
    mockedKnowledgeInfo.mockResolvedValue(makeKnowledgeInfo({ is_knowledge_base: false, marker: 'none' }))

    renderExplorer('ws-1')
    await waitFor(() => expect(screen.getByTestId('library-row-loose.md')).toBeInTheDocument())
    await openRowMenuAndClick('loose.md', /delete/i)
    await waitFor(() => expect(screen.getByTestId('library-delete-confirm')).toHaveTextContent('Delete'))
    expect(screen.getByRole('alertdialog')).toHaveTextContent(/permanently deleted/i)
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))
    await openRowMenuAndClick('loose.md', /rename/i)
    await waitFor(() => expect(screen.getByTestId('library-rename-dialog')).toBeInTheDocument())
    expect(screen.queryByTestId('library-rename-links-note')).not.toBeInTheDocument()
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
  //
  // confirmDiscardLibraryEdits() is async (it opens the catalogued
  // ConfirmDialog in place of window.confirm — see unsavedGuard.ts). This
  // test is also the mutation-proof required by the migration task: it
  // asserts navigation has NOT happened in the same tick as the click,
  // before the dialog's Promise ever resolves. Drop the `await` in
  // LibraryExplorer's handleSelectFile and the guard's `!expr` check
  // evaluates a Promise object — always truthy — so `if (!x) return` never
  // returns and the click navigates immediately; this test would then fail
  // on the "still on report.md, nothing awaited yet" assertion. Verified by
  // hand (2026-09-21): removing that one `await` failed this test with
  // draft.md open before the dialog even rendered; restoring it fixed it.
  it('opens the discard-unsaved-changes dialog before switching files, stays put on Cancel, and navigates on Discard', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([
      makeEntry({ name: 'report.md', path: 'report.md' }),
      makeEntry({ name: 'draft.md', path: 'draft.md' }),
    ])

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-row-report.md')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-row-report.md'))
    // Wait for the EDITOR itself to be mounted (the View/Edit toggle only
    // renders once useLibraryFileEditor is up), not just the preview
    // pane/title — those render immediately from `selectedEntry` alone,
    // before the content fetch (and so the editor's own mount effect,
    // which sets the guard's dirty flag false on mount) has resolved.
    // Setting the guard dirty before that effect has run would only have it
    // clobbered back to false the moment the effect finally fires.
    await screen.findByTestId('library-preview-mode-view')

    const { setLibraryEditorDirty } = await import('./preview/unsavedGuard')
    setLibraryEditorDirty(true)

    fireEvent.click(screen.getByTestId('library-row-draft.md'))

    // Synchronous assertion, no waitFor: the guard's Promise has not
    // resolved yet, so navigation must not have happened. This is the line
    // that only a real `await` in handleSelectFile keeps true.
    expect(screen.getByTestId('library-preview-title')).toHaveTextContent('report.md')

    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('Discard unsaved changes?')
    expect(dialog).toHaveTextContent('You have unsaved changes in the Library editor.')

    // Cancel: stays on report.md, draft.md's content never requested.
    fireEvent.click(within(dialog).getByText('Cancel'))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(screen.getByTestId('library-preview-title')).toHaveTextContent('report.md')
    expect(mockedFetchContent).not.toHaveBeenCalledWith('ws-1', 'draft.md')

    // Still dirty (Cancel does not clear it) — clicking draft.md again
    // re-opens the same dialog; this time answer Discard.
    fireEvent.click(screen.getByTestId('library-row-draft.md'))
    const secondDialog = await screen.findByRole('alertdialog')
    fireEvent.click(within(secondDialog).getByText('Discard'))

    await waitFor(() => expect(screen.getByTestId('library-preview-title')).toHaveTextContent('draft.md'))
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

  it('a failed rename shows a persistent, actionable error banner — dialog stays open, typed name preserved, button reverts (never silent)', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([makeEntry({ name: 'report.md', path: 'report.md' })])
    mockedRename.mockRejectedValue(new ApiError(400, 'Invalid path: contains directory traversal characters'))

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-row-report.md')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-row-report.md'))
    await openRowMenuAndClick('report.md', /rename/i)

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
    // UAT fix (Dana, re-verified v8): the banner AND a toast used to BOTH
    // fire for the same error — "two simultaneous displays... is noise".
    // The dialog's own banner (already visible, right next to the input) is
    // now the single channel; no toast on top of it.
    expect(useUiStore.getState().toasts.some((t) => t.variant === 'error')).toBe(false)
  })

  it('rejects a leading "/" in a Move destination instead of silently stripping it before sending', async () => {
    mockedFetchWorkspaces.mockResolvedValue([makeWorkspaceNode({ id: 'ws-1', name: 'Website API' })])
    mockedFetchEntries.mockResolvedValue([makeEntry({ name: 'report.md', path: 'report.md' })])

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-row-report.md')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-row-report.md'))
    await openRowMenuAndClick('report.md', /move/i)

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
    await openRowMenuAndClick('report.md', /move/i)

    await waitFor(() => expect(screen.getByTestId('library-transfer-dialog')).toBeInTheDocument())
    const pathInput = screen.getByTestId('library-transfer-path') as HTMLInputElement
    fireEvent.change(pathInput, { target: { value: 'archive/report.md' } })
    fireEvent.click(screen.getByTestId('library-transfer-confirm'))

    await waitFor(() => {
      expect(screen.getByTestId('library-transfer-error')).toHaveTextContent('Destination workspace not found')
    })
    expect(screen.getByTestId('library-transfer-dialog')).toBeInTheDocument()
    expect(pathInput.value).toBe('archive/report.md')
    // UAT fix (Dana, re-verified v8, exact repro): this used to ALSO fire a
    // toast for the same error at once — "two simultaneous displays... is
    // noise". The inline banner (right next to the destination input) is
    // now the single channel.
    expect(useUiStore.getState().toasts.some((t) => t.variant === 'error')).toBe(false)
  })

  // UAT fix (Dana, re-verified v8): the tester's EXACT repro — moving onto a
  // missing destination parent — got back
  //   {"error":"destination directory \"dana-missing-folder\" does not
  //   exist — create it first with POST /library/{workspace_id}/mkdir"}
  // from the real server, but the SPA showed the generic "The requested
  // resource was not found." (mapLibraryErr's 404 is a "known" status per
  // api-error.ts, so ApiError.fromResponse overrides userMessage with the
  // generic default — see libraryErrorMessage.ts). This reproduces that
  // EXACT shape (generic userMessage + the raw JSON body ApiError.fromResponse
  // actually retains) rather than a test-authored userMessage, so it proves
  // the fix recovers the real server text and reworks the raw API-path
  // guidance into UI terms without dropping the directory name.
  it('prefers the server\'s own `error` field over the generic 404 message, and rewords the raw mkdir API path into UI terms — without dropping the directory name', async () => {
    mockedFetchWorkspaces.mockResolvedValue([makeWorkspaceNode({ id: 'ws-1', name: 'Website API' })])
    mockedFetchEntries.mockResolvedValue([makeEntry({ name: 'report.md', path: 'report.md' })])
    mockedMove.mockRejectedValue(
      new ApiError(404, 'The requested resource was not found.', {
        body: JSON.stringify({
          error:
            'destination directory "dana-missing-folder" does not exist — create it first with POST /library/{workspace_id}/mkdir',
        }),
      }),
    )

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-row-report.md')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-row-report.md'))
    await openRowMenuAndClick('report.md', /move/i)

    await waitFor(() => expect(screen.getByTestId('library-transfer-dialog')).toBeInTheDocument())
    const pathInput = screen.getByTestId('library-transfer-path') as HTMLInputElement
    fireEvent.change(pathInput, { target: { value: 'dana-missing-folder/report.md' } })
    fireEvent.click(screen.getByTestId('library-transfer-confirm'))

    await waitFor(() => {
      const banner = screen.getByTestId('library-transfer-error')
      // The load-bearing part — the specific missing directory name —
      // must survive.
      expect(banner).toHaveTextContent('dana-missing-folder')
      // The raw API path must NOT leak to the end user...
      expect(banner.textContent).not.toContain('POST /library')
      expect(banner.textContent).not.toContain('{workspace_id}')
      // ...but the guidance itself must survive, reworded into UI terms.
      expect(banner).toHaveTextContent('create it first using New Folder')
      // And the generic fallback the tester saw twice must be gone.
      expect(banner.textContent).not.toBe('The requested resource was not found.')
    })
    // Single channel only (this dialog's banner) — no duplicate toast.
    expect(useUiStore.getState().toasts.some((t) => t.variant === 'error')).toBe(false)
  })

  it('a failed upload shows a persistent, dismissible error banner instead of the file just silently never appearing', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([])
    mockedUpload.mockRejectedValue(new ApiError(400, 'Upload rejected: filename contains invalid characters'))

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-upload-input')).toBeInTheDocument())
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

// UAT fix (Dana, live tester, re-verified v8) — "STILL BLOCKED for a user —
// only the backend gained a capability nobody can reach." POST
// /library/{workspace_id}/mkdir worked at the API layer with no reachable
// affordance anywhere in the explorer. These tests cover the new New Folder
// toolbar action end to end: it exists, creates in the CURRENT directory,
// validates client-side (rejecting ".."-prefixed/traversal names before
// ever submitting), and shows the same single-channel error-banner
// treatment Rename/Move now use on failure.
describe('LibraryExplorer — New Folder (mkdir UAT fix)', () => {
  it('offers New folder and Upload files from the create menu, scoped to a workspace', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([])

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-create-menu-trigger')).toBeInTheDocument())
    fireEvent.pointerDown(screen.getByTestId('library-create-menu-trigger'), { ctrlKey: false, button: 0 })
    expect(await screen.findByRole('menuitem', { name: 'New folder' })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: 'Upload files' })).toBeInTheDocument()
  })

  it('does NOT render the New Folder action at the virtual root (no workspace scoped yet)', async () => {
    mockedFetchWorkspaces.mockResolvedValue([makeWorkspaceNode({ id: 'ws-1', name: 'Website API' })])

    renderExplorer(undefined)

    await waitFor(() => expect(screen.getByTestId('library-workspace-node-ws-1')).toBeInTheDocument())
    expect(screen.queryByTestId('library-new-folder-button')).not.toBeInTheDocument()
  })

  it('creates a folder in the CURRENT directory and shows it in the listing after invalidation', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValueOnce([makeEntry({ name: 'report.md', path: 'report.md' })])
    mockedFetchEntries.mockResolvedValueOnce([
      makeEntry({ name: 'report.md', path: 'report.md' }),
      makeEntry({ name: 'drafts', path: 'drafts', is_dir: true }),
    ])
    mockedMkdir.mockResolvedValue(
      makeEntry({ name: 'drafts', path: 'drafts', is_dir: true, mime: undefined, is_text_editable: false }),
    )

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-create-menu-trigger')).toBeInTheDocument())
    await openCreateMenuAndClick(/New folder/)

    await waitFor(() => expect(screen.getByTestId('library-new-folder-dialog')).toBeInTheDocument())
    const input = screen.getByTestId('library-new-folder-input') as HTMLInputElement
    fireEvent.change(input, { target: { value: 'drafts' } })
    fireEvent.click(screen.getByTestId('library-new-folder-confirm'))

    await waitFor(() => expect(mockedMkdir).toHaveBeenCalledWith('ws-1', { path: 'drafts' }))
    await waitFor(() => expect(screen.queryByTestId('library-new-folder-dialog')).not.toBeInTheDocument())
    await waitFor(() => expect(screen.getByText('drafts')).toBeInTheDocument())
  })

  it('creates a nested folder using the CURRENT drilled-into directory as the parent path', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockImplementation((_wsId, path) =>
      Promise.resolve(
        // 'reports' is empty (no existing 'q1' sibling) so the collision
        // check doesn't get in the way of asserting the parent-path join.
        path === 'reports' ? [] : [makeEntry({ name: 'reports', path: 'reports', is_dir: true })],
      ),
    )
    mockedMkdir.mockResolvedValue(makeEntry({ name: 'q1', path: 'reports/q1', is_dir: true }))

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-row-reports')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-row-reports'))
    await waitFor(() => expect(mockedFetchEntries).toHaveBeenCalledWith('ws-1', 'reports', false))

    await openCreateMenuAndClick(/New folder/)
    await waitFor(() => expect(screen.getByTestId('library-new-folder-dialog')).toBeInTheDocument())
    fireEvent.change(screen.getByTestId('library-new-folder-input'), { target: { value: 'q1' } })
    fireEvent.click(screen.getByTestId('library-new-folder-confirm'))

    await waitFor(() => expect(mockedMkdir).toHaveBeenCalledWith('ws-1', { path: 'reports/q1' }))
  })

  it('rejects a ".."-prefixed name client-side, before ever submitting', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([])

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-create-menu-trigger')).toBeInTheDocument())
    await openCreateMenuAndClick(/New folder/)
    await waitFor(() => expect(screen.getByTestId('library-new-folder-dialog')).toBeInTheDocument())

    fireEvent.change(screen.getByTestId('library-new-folder-input'), { target: { value: '..dana-escape' } })

    expect(screen.getByTestId('library-new-folder-traversal')).toBeInTheDocument()
    expect(screen.getByTestId('library-new-folder-confirm')).toBeDisabled()
    fireEvent.click(screen.getByTestId('library-new-folder-confirm'))
    expect(mockedMkdir).not.toHaveBeenCalled()
  })

  it('rejects a general traversal ".." anywhere in the name client-side', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([])

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-create-menu-trigger')).toBeInTheDocument())
    await openCreateMenuAndClick(/New folder/)
    await waitFor(() => expect(screen.getByTestId('library-new-folder-dialog')).toBeInTheDocument())

    fireEvent.change(screen.getByTestId('library-new-folder-input'), { target: { value: 'foo/../bar' } })

    // Contains "/" too, but the traversal-specific message should still be
    // reachable once slash is removed — verify submit stays blocked either way.
    expect(screen.getByTestId('library-new-folder-confirm')).toBeDisabled()
    fireEvent.change(screen.getByTestId('library-new-folder-input'), { target: { value: 'dana..escape' } })
    expect(screen.getByTestId('library-new-folder-traversal')).toBeInTheDocument()
    expect(screen.getByTestId('library-new-folder-confirm')).toBeDisabled()
    fireEvent.click(screen.getByTestId('library-new-folder-confirm'))
    expect(mockedMkdir).not.toHaveBeenCalled()
  })

  it('rejects a name colliding with a sibling already visible in the current listing', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([makeEntry({ name: 'drafts', path: 'drafts', is_dir: true })])

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByText('drafts')).toBeInTheDocument())
    await openCreateMenuAndClick(/New folder/)
    await waitFor(() => expect(screen.getByTestId('library-new-folder-dialog')).toBeInTheDocument())

    fireEvent.change(screen.getByTestId('library-new-folder-input'), { target: { value: 'drafts' } })

    expect(screen.getByTestId('library-new-folder-collision')).toBeInTheDocument()
    expect(screen.getByTestId('library-new-folder-confirm')).toBeDisabled()
  })

  it('shows the same single-channel error-banner treatment Rename/Move now use on a server rejection — dialog stays open, no duplicate toast', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([])
    mockedMkdir.mockRejectedValue(new ApiError(409, 'an entry already exists at the destination path'))

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-create-menu-trigger')).toBeInTheDocument())
    await openCreateMenuAndClick(/New folder/)
    await waitFor(() => expect(screen.getByTestId('library-new-folder-dialog')).toBeInTheDocument())

    fireEvent.change(screen.getByTestId('library-new-folder-input'), { target: { value: 'new-dir' } })
    fireEvent.click(screen.getByTestId('library-new-folder-confirm'))

    await waitFor(() => {
      expect(screen.getByTestId('library-new-folder-error')).toHaveTextContent(
        'an entry already exists at the destination path',
      )
    })
    // Dialog stayed open with the input intact — not a silent revert.
    expect(screen.getByTestId('library-new-folder-dialog')).toBeInTheDocument()
    expect((screen.getByTestId('library-new-folder-input') as HTMLInputElement).value).toBe('new-dir')
    expect(useUiStore.getState().toasts.some((t) => t.variant === 'error')).toBe(false)
  })

  it('cancelling the dialog does not call mkdirLibraryEntry', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([])

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-create-menu-trigger')).toBeInTheDocument())
    await openCreateMenuAndClick(/New folder/)
    await waitFor(() => expect(screen.getByTestId('library-new-folder-dialog')).toBeInTheDocument())

    fireEvent.change(screen.getByTestId('library-new-folder-input'), { target: { value: 'drafts' } })
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    await waitFor(() => expect(screen.queryByTestId('library-new-folder-dialog')).not.toBeInTheDocument())
    expect(mockedMkdir).not.toHaveBeenCalled()
  })
})

// Lower-priority UAT flag: uploading (or now, creating folders) into the
// reserved work/.library/ directory — the server-managed home for
// chat-uploaded attachments (library-spec.md D-1) — was silently ACCEPTED,
// mixing user files into that internal namespace. Decision: block both
// actions client-side while browsing inside .library or any of its
// subdirectories, rather than let it through silently. Existing entries
// inside .library remain fully browsable/renamable/downloadable/deletable —
// only ADDING new content via these two actions is restricted.
describe('LibraryExplorer — reserved .library directory guard', () => {
  it('disables Upload and New Folder while browsing inside work/.library/', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockImplementation((_wsId, _path, includeHidden) =>
      Promise.resolve(
        includeHidden
          ? [makeEntry({ name: '.library', path: '.library', is_dir: true, is_hidden: true, is_text_editable: false })]
          : [],
      ),
    )

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-create-menu-trigger')).toBeInTheDocument())
    expect(await createMenuItemDisabled(/Upload files/)).toBe(false)
    fireEvent.click(screen.getByTestId('library-show-hidden-toggle'))
    await waitFor(() => expect(screen.getByText('.library')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-row-.library'))

    await waitFor(() => expect(mockedFetchEntries).toHaveBeenCalledWith('ws-1', '.library', true))
    expect(await createMenuItemDisabled(/Upload files/)).toBe(true)
    expect(await createMenuItemDisabled(/New folder/)).toBe(true)
  })

  it('disables Upload and New Folder inside a SUBdirectory of .library too', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockImplementation((_wsId, path) =>
      Promise.resolve(
        path === '.library'
          ? [makeEntry({ name: 'attachments', path: '.library/attachments', is_dir: true, is_hidden: false })]
          : [makeEntry({ name: '.library', path: '.library', is_dir: true, is_hidden: true, is_text_editable: false })],
      ),
    )

    renderExplorer('ws-1')

    fireEvent.click(screen.getByTestId('library-show-hidden-toggle'))
    await waitFor(() => expect(screen.getByText('.library')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-row-.library'))
    await waitFor(() => expect(screen.getByText('attachments')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-row-.library/attachments'))

    // includeHidden stays true (state carried over from toggling Show
    // Hidden on to reach .library in the first place).
    await waitFor(() => expect(mockedFetchEntries).toHaveBeenCalledWith('ws-1', '.library/attachments', true))
    expect(await createMenuItemDisabled(/Upload files/)).toBe(true)
    expect(await createMenuItemDisabled(/New folder/)).toBe(true)
  })

  it('leaves Upload/New Folder enabled again once navigated back out of .library', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockImplementation((_wsId, path) =>
      Promise.resolve(
        path === ''
          ? [makeEntry({ name: '.library', path: '.library', is_dir: true, is_hidden: true, is_text_editable: false })]
          : [],
      ),
    )

    renderExplorer('ws-1')

    fireEvent.click(screen.getByTestId('library-show-hidden-toggle'))
    await waitFor(() => expect(screen.getByText('.library')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-row-.library'))
    await waitFor(() => expect(mockedFetchEntries).toHaveBeenCalledWith('ws-1', '.library', true))
    expect(await createMenuItemDisabled(/Upload files/)).toBe(true)

    // Back out via the workspace breadcrumb crumb.
    fireEvent.click(screen.getByTestId('library-crumb-workspace'))

    await waitFor(async () => expect(await createMenuItemDisabled(/Upload files/)).toBe(false))
    expect(await createMenuItemDisabled(/New folder/)).toBe(false)
  })
})

// Layout + inline media (operator direction, 2026-08-04).
describe('LibraryExplorer — list/preview split and inline media', () => {
  async function openPreviewOn(name: string, layout?: 'stacked' | 'split') {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([makeEntry({ name, path: name })])
    renderExplorer('ws-1', layout ? { layout } : undefined)
    await waitFor(() => expect(screen.getByTestId(`library-row-${name}`)).toBeInTheDocument())
    fireEvent.click(screen.getByTestId(`library-row-${name}`))
    return await screen.findByTestId('library-preview-pane-wrapper')
  }

  // The docked aside stacks; the fullscreen tab splits left/right. Asserted via
  // the flex share + which border edge divides the two, because jsdom does no
  // layout and cannot report the actual geometry.
  it('stacks the preview below the list by default, giving it the larger share', async () => {
    const wrapper = await openPreviewOn('notes.md')
    expect(wrapper.className).toContain('flex-[55]')
    expect(wrapper.className).toContain('border-t')
    expect(wrapper.className).not.toContain('border-l')
  })

  it('puts the preview to the RIGHT at 60% when layout="split"', async () => {
    const wrapper = await openPreviewOn('notes.md', 'split')
    expect(wrapper.className).toContain('flex-[60]')
    expect(wrapper.className).toContain('border-l')
    expect(wrapper.className).not.toContain('border-t')
  })

  // With nothing open the list must reclaim the whole box rather than sitting
  // at 45% beside empty space.
  it('gives the list the full box while no file is open', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([makeEntry({ name: 'notes.md', path: 'notes.md' })])
    renderExplorer('ws-1')
    await waitFor(() => expect(screen.getByTestId('library-row-notes.md')).toBeInTheDocument())

    expect(screen.queryByTestId('library-preview-pane-wrapper')).toBeNull()
    const list = screen.getByTestId('library-row-notes.md').closest('.overflow-y-auto') as HTMLElement
    expect(list.className).toContain('flex-1')
    expect(list.className).not.toContain('flex-[45]')
  })

  it('renders a real thumbnail inline for images and videos, and only for those', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([
      makeEntry({ name: 'shot.png', path: 'shot.png', mime: 'image/png' }),
      makeEntry({ name: 'clip.mp4', path: 'clip.mp4', mime: 'video/mp4' }),
      makeEntry({ name: 'notes.md', path: 'notes.md', mime: 'text/markdown' }),
    ])
    renderExplorer('ws-1')
    await waitFor(() => expect(screen.getByTestId('library-row-shot.png')).toBeInTheDocument())

    expect(screen.getByTestId('library-thumb-shot.png').tagName).toBe('IMG')
    expect(screen.getByTestId('library-thumb-clip.mp4').tagName).toBe('VIDEO')
    // A text file keeps its generic type glyph — no media fetch for it.
    expect(screen.queryByTestId('library-thumb-notes.md')).toBeNull()
  })

  // A directory of large videos must not become a directory of large downloads
  // just by being listed.
  it('lazy-loads image thumbnails and fetches only metadata for video', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([
      makeEntry({ name: 'shot.png', path: 'shot.png', mime: 'image/png' }),
      makeEntry({ name: 'clip.mp4', path: 'clip.mp4', mime: 'video/mp4' }),
    ])
    renderExplorer('ws-1')
    await waitFor(() => expect(screen.getByTestId('library-row-shot.png')).toBeInTheDocument())

    expect(screen.getByTestId('library-thumb-shot.png')).toHaveAttribute('loading', 'lazy')
    expect(screen.getByTestId('library-thumb-clip.mp4')).toHaveAttribute('preload', 'metadata')
  })

  // Unreadable media degrades to exactly what the row looked like before,
  // never an empty box.
  it('falls back to the type icon when the media will not load', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([makeEntry({ name: 'broken.png', path: 'broken.png', mime: 'image/png' })])
    renderExplorer('ws-1')
    const thumb = await screen.findByTestId('library-thumb-broken.png')

    fireEvent.error(thumb)

    await waitFor(() => expect(screen.queryByTestId('library-thumb-broken.png')).toBeNull())
    expect(screen.getByTestId('library-row-broken.png')).toBeInTheDocument()
  })
})

// ── Deep-linking (ADR-067 FR-012 / US-3) ──────────────────────────────────
// The explorer's half of it: what it does with an address it is HANDED, and
// what it reports back. Turning that address into a URL is the /library
// route's job and is tested in routes/_app/-library.test.tsx.
//
// The point of the addressed mode is that there is no second copy of "which
// workspace, which file" living in this component — so most of these tests
// assert an absence (nothing selected locally, no remount, no stale entry)
// rather than a presence, because a component keeping its own copy would pass
// the presence assertions just as well.
// Shared by both deep-linking describe blocks below — a function-size
// budget requirement (scripts/check-function-budget.sh; see
// scripts/budgets/functions.txt's grandfathered entry for this file).
function renderAddressedLibraryExplorer(address: { workspaceId?: string; path?: string }) {
  const client = makeClient()
  const onAddressChange = vi.fn()
  const tree = (a: { workspaceId?: string; path?: string }) => (
    <QueryClientProvider client={client}>
      <LibraryExplorer address={a} onAddressChange={onAddressChange} />
    </QueryClientProvider>
  )
  const utils = render(tree(address))
  return {
    ...utils,
    onAddressChange,
    /** Simulates the URL changing under the component — back button, a
     *  pasted link, or a link a later wave hands it. */
    navigateTo: (next: { workspaceId?: string; path?: string }) => utils.rerender(tree(next)),
  }
}

describe('LibraryExplorer — deep-linking (addressed mode)', () => {
  const renderAddressed = renderAddressedLibraryExplorer

  it('opens the addressed file selected, listing the folder that contains it (US-3 AS-3)', async () => {
    mockedFetchWorkspaces.mockResolvedValue([makeWorkspaceNode({ id: 'ws-1' })])
    entriesByDir({ notes: [makeEntry({ name: 'plan.md', path: 'notes/plan.md' })] })

    renderAddressed({ workspaceId: 'ws-1', path: 'notes/plan.md' })

    // The containing folder is on screen...
    await waitFor(() => expect(screen.getByTestId('library-row-notes/plan.md')).toBeInTheDocument())
    // ...and the file itself is open, not merely highlighted.
    expect(await screen.findByTestId('library-preview-pane')).toBeInTheDocument()
    expect(mockedFetchContent).toHaveBeenCalledWith('ws-1', 'notes/plan.md')
    expect(mockedFetchEntries).toHaveBeenCalledWith('ws-1', 'notes', false)
    // No "not found" while the address is being resolved against the WRONG
    // folder: the first listing this component fetches is the workspace root,
    // which of course does not contain notes/plan.md.
    expect(screen.queryByTestId('library-deeplink-unresolved')).toBeNull()
  })

  it('keeps the open file open across a Show-hidden toggle, which swaps the listing out from under it', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    entriesByDir({ '': [makeEntry({ name: 'report.md', path: 'report.md' })] })

    renderAddressed({ workspaceId: 'ws-1', path: 'report.md' })
    await screen.findByTestId('library-preview-pane')

    fireEvent.click(screen.getByTestId('library-show-hidden-toggle'))

    // Synchronously after the toggle the listing for the new query key has
    // not arrived. Resolving the entry from the listing alone would drop the
    // pane here — and take an unsaved edit with it.
    expect(screen.getByTestId('library-preview-pane')).toBeInTheDocument()
    await waitFor(() => expect(mockedFetchEntries).toHaveBeenCalledWith('ws-1', '', true))
    expect(screen.getByTestId('library-preview-pane')).toBeInTheDocument()
  })

  it('reports a clicked file to the caller and selects NOTHING itself — the address is the only copy', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    entriesByDir({ '': [makeEntry({ name: 'report.md', path: 'report.md' })] })

    const { onAddressChange } = renderAddressed({ workspaceId: 'ws-1' })

    await waitFor(() => expect(screen.getByTestId('library-row-report.md')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-row-report.md'))

    // The unsaved-edits guard is now async (confirmDiscardLibraryEdits()
    // always returns a Promise, even on its not-dirty fast path — see
    // unsavedGuard.ts), so goTo()/onAddressChange fire one microtask after
    // the click rather than synchronously within it.
    await waitFor(() =>
      expect(onAddressChange).toHaveBeenCalledWith({ workspaceId: 'ws-1', path: 'report.md' }),
    )
    // The caller has not yet handed a new address back, so nothing opened.
    // A component that also kept the selection locally would show the pane
    // here and would go on showing it even if the URL never changed.
    expect(screen.queryByTestId('library-preview-pane')).toBeNull()
  })

  it('follows an address change to a different file without remounting — the back-button path (US-3 AS-4)', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    entriesByDir({
      notes: [
        makeEntry({ name: 'first.md', path: 'notes/first.md' }),
        makeEntry({ name: 'second.md', path: 'notes/second.md' }),
      ],
    })

    const { navigateTo } = renderAddressed({ workspaceId: 'ws-1', path: 'notes/first.md' })
    await waitFor(() => expect(screen.getByTestId('library-preview-title')).toHaveTextContent('first.md'))

    navigateTo({ workspaceId: 'ws-1', path: 'notes/second.md' })
    await waitFor(() => expect(screen.getByTestId('library-preview-title')).toHaveTextContent('second.md'))

    // Back to the first file, which is what pressing back actually does. The
    // open pane is the oracle rather than a re-fetch: the content query is
    // cached by path, so a component that ignored the address change entirely
    // would also issue no new fetch.
    navigateTo({ workspaceId: 'ws-1', path: 'notes/first.md' })
    await waitFor(() => expect(screen.getByTestId('library-preview-title')).toHaveTextContent('first.md'))
  })

  it('drops the selection but stays in its folder when the address loses its path', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    entriesByDir({ 'a/b': [makeEntry({ name: 'deep.md', path: 'a/b/deep.md' })] })

    const { onAddressChange, navigateTo } = renderAddressed({ workspaceId: 'ws-1', path: 'a/b/deep.md' })
    const closeButton = await screen.findByTestId('library-preview-close')
    fireEvent.click(closeButton)

    // Async guard (see the comment on the sibling test above) — one
    // microtask after the click, not synchronously within it.
    await waitFor(() =>
      expect(onAddressChange).toHaveBeenCalledWith({ workspaceId: 'ws-1', path: undefined }),
    )

    navigateTo({ workspaceId: 'ws-1', path: undefined })
    await waitFor(() => expect(screen.queryByTestId('library-preview-pane')).toBeNull())
    // Still in a/b — closing a file must not throw you back to the workspace
    // root, which is what deriving the folder from the address alone would
    // do. `toHaveBeenCalledWith` (not `toHaveBeenLastCalledWith`): the mock
    // backs BOTH this folder's own listing query AND the separate
    // always-root `rootEntriesQuery` (used for the mounts list), so which of
    // the two fires last is incidental effect-scheduling order, not a stated
    // contract — C4's browsedDir-seeding change (LibraryExplorer.tsx) is
    // free to settle the a/b fetch in one render pass instead of two, which
    // reorders that incidental race without regressing the actual behaviour
    // this test exists to prove. The DOM assertion right below is the real,
    // order-independent oracle for "still showing a/b's listing".
    expect(mockedFetchEntries).toHaveBeenCalledWith('ws-1', 'a/b', false)
    expect(screen.getByTestId('library-row-a/b/deep.md')).toBeInTheDocument()
  })
})

// A function-size budget split from the describe block above — same
// "addressed mode" contract, same shared `renderAddressed` helper; no
// behavioural boundary between the two blocks.
describe('LibraryExplorer — deep-linking (addressed mode): missing path and edge cases', () => {
  const renderAddressed = renderAddressedLibraryExplorer

  it('opens the containing folder with a message naming the missing path, not an error state (US-3 AS-5)', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    entriesByDir({ notes: [makeEntry({ name: 'kept.md', path: 'notes/kept.md' })] })

    renderAddressed({ workspaceId: 'ws-1', path: 'notes/gone.md' })

    const banner = await screen.findByTestId('library-deeplink-unresolved')
    expect(banner).toHaveTextContent('notes/gone.md')
    expect(banner).toHaveTextContent(/not found/i)
    // The folder is browsable, not replaced by a failure screen or left blank.
    expect(screen.getByTestId('library-row-notes/kept.md')).toBeInTheDocument()
    expect(screen.queryByTestId('library-entries-error')).toBeNull()
    expect(screen.queryByTestId('library-preview-pane')).toBeNull()
  })

  it('withdraws the not-found message once you browse away from the folder it was about', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    entriesByDir({
      notes: [makeEntry({ name: 'other', path: 'notes/other', is_dir: true })],
      'notes/other': [makeEntry({ name: 'unrelated.md', path: 'notes/other/unrelated.md' })],
    })

    renderAddressed({ workspaceId: 'ws-1', path: 'notes/gone.md' })
    expect(await screen.findByTestId('library-deeplink-unresolved')).toHaveTextContent('notes/gone.md')

    // Opening a folder clears the selection, but until the caller hands a new
    // address back the address still names a file in a DIFFERENT folder from
    // the one now listed. The message reads "showing the folder that would
    // contain it" — leaving it up over an unrelated folder makes it a false
    // statement about what is on screen, and the operator has no way to tell.
    fireEvent.click(screen.getByTestId('library-row-notes/other'))

    await waitFor(() =>
      expect(screen.getByTestId('library-row-notes/other/unrelated.md')).toBeInTheDocument(),
    )
    expect(screen.queryByTestId('library-deeplink-unresolved')).toBeNull()
  })

  it('stays silent when navigation leaves the address unchanged, so the back button is not padded with duplicates', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    entriesByDir({
      '': [makeEntry({ name: 'notes', path: 'notes', is_dir: true })],
      notes: [makeEntry({ name: 'plan.md', path: 'notes/plan.md' })],
    })

    // Nothing selected: opening a folder does not change which file the URL
    // names, so there is nothing to report.
    const { onAddressChange } = renderAddressed({ workspaceId: 'ws-1' })
    await waitFor(() => expect(screen.getByTestId('library-row-notes')).toBeInTheDocument())

    fireEvent.click(screen.getByTestId('library-row-notes'))

    // The folder DID open — this is silence, not inaction.
    await waitFor(() => expect(screen.getByTestId('library-row-notes/plan.md')).toBeInTheDocument())
    expect(onAddressChange).not.toHaveBeenCalled()
  })

  it('says a hidden target is hidden rather than missing — it is in the folder, just filtered out', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    entriesByDir({ notes: [makeEntry({ name: '.secret.md', path: 'notes/.secret.md', is_hidden: true })] })

    renderAddressed({ workspaceId: 'ws-1', path: 'notes/.secret.md' })

    const banner = await screen.findByTestId('library-deeplink-unresolved')
    expect(banner).toHaveTextContent('notes/.secret.md')
    expect(banner).toHaveTextContent(/show hidden/i)
    expect(banner).not.toHaveTextContent(/not found/i)
  })

  it('says nothing at all while the folder listing is still in flight — "missing" is a claim, not a default', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    // Never settles: the listing that would prove the file absent has not
    // arrived, so no verdict is available yet.
    mockedFetchEntries.mockImplementation(() => new Promise<LibraryEntry[]>(() => {}))

    renderAddressed({ workspaceId: 'ws-1', path: 'notes/plan.md' })

    await waitFor(() => expect(screen.getByTestId('library-loading-skeleton')).toBeInTheDocument())
    expect(screen.queryByTestId('library-deeplink-unresolved')).toBeNull()
  })

  it('keeps its own retryable error state when the FOLDER fails to load — it cannot know the file is missing', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockRejectedValue(new Error('boom'))

    renderAddressed({ workspaceId: 'ws-1', path: 'notes/plan.md' })

    await waitFor(() => expect(screen.getByTestId('library-entries-error')).toBeInTheDocument())
    expect(screen.queryByTestId('library-deeplink-unresolved')).toBeNull()
  })

  it('still asks before discarding unsaved edits, and reports no address change when the operator stays', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    entriesByDir({
      '': [
        makeEntry({ name: 'report.md', path: 'report.md' }),
        makeEntry({ name: 'draft.md', path: 'draft.md' }),
      ],
    })

    const { onAddressChange } = renderAddressed({ workspaceId: 'ws-1', path: 'report.md' })
    // The editor itself (not just the pane) must be mounted before setting
    // the guard dirty — see the sibling test's comment above for why: its
    // own mount effect otherwise clobbers this back to false once the
    // content fetch resolves.
    await screen.findByTestId('library-preview-mode-view')

    const { setLibraryEditorDirty } = await import('./preview/unsavedGuard')
    setLibraryEditorDirty(true)

    fireEvent.click(screen.getByTestId('library-row-draft.md'))

    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('Discard unsaved changes?')
    fireEvent.click(within(dialog).getByText('Cancel'))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())

    // The URL must not move either — a blocked navigation that still rewrote
    // the address would leave the address pointing at a file the pane never
    // opened.
    expect(onAddressChange).not.toHaveBeenCalled()

    setLibraryEditorDirty(false)
  })

  it('falls back to local selection when given an address it has no way to report back — never a frozen pane', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    entriesByDir({ '': [makeEntry({ name: 'report.md', path: 'report.md' })] })

    render(
      <QueryClientProvider client={makeClient()}>
        {/* onAddressChange deliberately omitted. */}
        <LibraryExplorer address={{ workspaceId: 'ws-1' }} />
      </QueryClientProvider>,
    )

    await waitFor(() => expect(screen.getByTestId('library-row-report.md')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-row-report.md'))

    expect(await screen.findByTestId('library-preview-pane')).toBeInTheDocument()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// The knowledge surface, AS MOUNTED (ADR-067 US-4, US-6, US-7, FR-020, FR-062)
//
// This whole block exists because the feature's ONLY production wiring —
// LibraryExplorer → KnowledgePanel, and LibraryPreviewPane → the reading view —
// had no test at all. Two mutations were run against the previous suite to
// confirm it: replacing `path={browsedDir}` with `path=""` and blanking the
// onOpenNote handler passed 400/400, and removing the entire knowledge surface
// with `{false && (` passed 400/400 as well. Deleting the feature from the
// product left every library test green.
// ─────────────────────────────────────────────────────────────────────────────

describe('LibraryExplorer — the knowledge panel is mounted, and asked about the right folder', () => {
  it('asks about the folder currently being browsed, not the workspace root (FR-020)', async () => {
    // DIES ON: `path=""` in the KnowledgePanel mount, which would answer
    // "is this a knowledge base?" about a folder the reader is not looking at.
    mockedFetchWorkspaces.mockResolvedValue([])
    entriesByDir({
      '': [makeEntry({ name: 'notes', path: 'notes', is_dir: true })],
      notes: [makeEntry({ name: 'a.md', path: 'notes/a.md' })],
    })
    mockedKnowledgeInfo.mockResolvedValue(makeKnowledgeInfo({ root_path: 'notes' }))

    renderExplorer('ws-1')

    await waitFor(() => expect(mockedKnowledgeInfo).toHaveBeenCalledWith('ws-1', ''))
    fireEvent.click(await screen.findByTestId('library-row-notes'))
    await waitFor(() => expect(mockedKnowledgeInfo).toHaveBeenCalledWith('ws-1', 'notes'))
  })

  it('renders the collection surface for a knowledge base, with the ONE search box (MV-10)', async () => {
    // DIES ON: removing the KnowledgePanel mount, or gating it on something
    // other than "a workspace is open". Search itself is LibrarySearchBar's
    // (unified-search-and-grep-spec.md US-5) — this used to also assert a
    // `knowledge-search` testid, KnowledgePanel's own now-retired box.
    mockedFetchWorkspaces.mockResolvedValue([])
    entriesByDir({ '': [makeEntry({ name: 'a.md', path: 'a.md' })] })
    mockedKnowledgeInfo.mockResolvedValue(makeKnowledgeInfo({ root_path: '.' }))

    renderExplorer('ws-1')

    expect(await screen.findByTestId('knowledge-panel')).toBeInTheDocument()
    expect(await screen.findByTestId('library-search-bar')).toBeInTheDocument()
    expect(screen.getAllByRole('searchbox')).toHaveLength(1)
  })

  it('renders NO knowledge chrome at all for an ordinary folder (US-4 AS-3)', async () => {
    // Almost every folder is ordinary. A permanent "Not a knowledge base" card
    // above every listing is itself a knowledge-base feature switched on
    // everywhere, and it trains the reader to ignore the one spot a real
    // warning will appear.
    //
    // DIES ON: removing the `not_a_knowledge_base && !onCreateCollection` early
    // return from KnowledgePanel.
    mockedFetchWorkspaces.mockResolvedValue([])
    entriesByDir({ '': [makeEntry({ name: 'a.md', path: 'a.md' })] })
    mockedKnowledgeInfo.mockResolvedValue(
      makeKnowledgeInfo({ is_knowledge_base: false, marker: 'none', collection_id: undefined }),
    )

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-row-a.md')).toBeInTheDocument())
    await waitFor(() => expect(mockedKnowledgeInfo).toHaveBeenCalled())
    expect(screen.queryByTestId('knowledge-panel')).not.toBeInTheDocument()
    expect(screen.queryByTestId('knowledge-state-not-a-knowledge-base')).not.toBeInTheDocument()
  })

  it('does not render the panel at the virtual root — there is no folder to ask about', async () => {
    mockedFetchWorkspaces.mockResolvedValue([makeWorkspaceNode({ id: 'ws-1' })])
    renderExplorer(undefined)
    await waitFor(() => expect(screen.getByTestId('library-workspace-node-ws-1')).toBeInTheDocument())
    expect(mockedKnowledgeInfo).not.toHaveBeenCalled()
    expect(screen.queryByTestId('knowledge-panel')).not.toBeInTheDocument()
  })

  it('opens the note a search hit names, translated to a workspace-relative path', async () => {
    // unified-search-and-grep-spec.md US-1/US-5: search now lives entirely in
    // LibrarySearchBar (the ONE bar, MV-10) — KnowledgePanel no longer mounts
    // its own KnowledgeSearch box. This test used to type into THAT box; it
    // now types into the surviving bar and asserts the SAME translation
    // guarantee.
    //
    // DIES ON: blanking the onOpenNote handler at the LibrarySearchBar mount,
    // or dropping collectionPathToWorkspacePath — a hit at `a.md` inside a
    // collection mounted at `notes/` opens `notes/a.md`, not `a.md`.
    mockedFetchWorkspaces.mockResolvedValue([])
    entriesByDir({
      '': [makeEntry({ name: 'notes', path: 'notes', is_dir: true })],
      notes: [makeEntry({ name: 'a.md', path: 'notes/a.md' })],
    })
    mockedKnowledgeInfo.mockResolvedValue(makeKnowledgeInfo({ root_path: 'notes' }))
    mockedSearchVault.mockResolvedValue({
      collection_id: 'kb_1',
      complete: true,
      notes: [{ path: 'a.md', title: 'A note' }],
      records: [],
      views: [],
    })
    const onAddressChange = vi.fn()

    render(
      <QueryClientProvider client={makeClient()}>
        <LibraryExplorer address={{ workspaceId: 'ws-1' }} onAddressChange={onAddressChange} />
      </QueryClientProvider>,
    )

    fireEvent.click(await screen.findByTestId('library-row-notes'))
    await waitFor(() => expect(mockedKnowledgeInfo).toHaveBeenCalledWith('ws-1', 'notes'))

    // MV-10: exactly one search input in this view, even though a vault
    // folder now composes both KnowledgePanel (indexing status) and
    // LibrarySearchBar (search) — the shipped duplication US-1/US-5 exist to
    // fix.
    expect(screen.getAllByRole('searchbox')).toHaveLength(1)

    fireEvent.change(await screen.findByLabelText('Search this knowledge base'), { target: { value: 'landlock' } })
    const results = await screen.findByTestId('library-search-results')
    fireEvent.click(within(results).getByRole('button'))

    await waitFor(() =>
      expect(onAddressChange).toHaveBeenCalledWith({ workspaceId: 'ws-1', path: 'notes/a.md' }),
    )
  })
})

describe('LibraryExplorer — opening a markdown file reaches the STAGE 2 reading view (US-7)', () => {
  it('renders the reading column, not the plain stage-1 markdown view', async () => {
    // DIES ON: reverting LibraryMarkdownPreview's view slot to stage 1 — the
    // reading column disappears and `[[Wikilinks]]` go back to literal text,
    // which is what the product actually shipped while 138 tests asserted
    // otherwise about components nothing imported.
    mockedFetchWorkspaces.mockResolvedValue([])
    entriesByDir({ '': [makeEntry({ name: 'report.md', path: 'report.md' })] })
    mockedFetchContent.mockResolvedValue({
      path: 'report.md',
      content: 'see [[Other Note]] %%hidden aside%% visible',
      size: 40,
      is_text: true,
      too_large: false,
    })

    renderExplorer('ws-1')

    fireEvent.click(await screen.findByTestId('library-row-report.md'))

    const article = await screen.findByTestId('knowledge-reader-article')
    expect(article.textContent).not.toContain('hidden aside')
    expect(article.textContent).toContain('visible')
    expect(within(article).getByTestId('markdown-link').getAttribute('data-kb-target')).toBe(
      'Other Note',
    )
  })

  it('asks for the open file’s outline — an outline is offered for ANY markdown file (FR-062)', async () => {
    // DIES ON: gating the outline on is_knowledge_base at the mount site, or on
    // dropping the outline rail from the reading view.
    mockedFetchWorkspaces.mockResolvedValue([])
    entriesByDir({ '': [makeEntry({ name: 'report.md', path: 'report.md' })] })
    mockedKnowledgeInfo.mockResolvedValue(
      makeKnowledgeInfo({ is_knowledge_base: false, marker: 'none', collection_id: undefined }),
    )
    mockedKnowledgeOutline.mockResolvedValue({
      path: 'report.md',
      is_knowledge_base: false,
      headings: [{ level: 1, text: 'Report', slug: 'report' }],
    })

    renderExplorer('ws-1')
    fireEvent.click(await screen.findByTestId('library-row-report.md'))

    await waitFor(() =>
      expect(mockedKnowledgeOutline).toHaveBeenCalledWith('ws-1', 'report.md'),
    )
    expect(await screen.findByTestId('knowledge-outline-heading')).toHaveTextContent('Report')
    // Not a knowledge base: linked mentions genuinely do not apply, and an
    // empty panel would imply "nothing links here" rather than "the question
    // does not apply".
    expect(screen.queryByTestId('knowledge-backlinks')).not.toBeInTheDocument()
    expect(mockedKnowledgeGraph).not.toHaveBeenCalled()
  })
})

// D-117, response half (2026-09-14 fix round): the Add-mount dialog must
// render the SERVER's own answer — the 201 body's `warning` (broad grant the
// server allowed) and the 403's reason (grant the server refused) — inside
// the dialog, not in a toast that auto-dismisses. These tests go through the
// REAL explorer wiring (create menu → dialog → mutation → props) so a dialog
// that renders the right banners from props nobody passes cannot pass.
describe('LibraryExplorer — D-117 add-mount renders the server response in the dialog', () => {
  async function openAddMount() {
    await openCreateMenuAndClick(/Add a folder from your Mac/)
    // Typed path: the dialog looks the verdict up in the parent's listing
    // before submitting. An empty listing = "the server does not list it" =
    // submit straight away, which is the case whose verdict only the server
    // can give.
    mockedHostFolders.mockResolvedValue({ path: '/', entries: [] })
    fireEvent.change(await screen.findByTestId('library-add-mount-path'), {
      target: { value: '/scratch/repo' },
    })
  }

  it('a 201 carrying `warning` holds the dialog open with the broad banner until Done', async () => {
    mockedFetchWorkspaces.mockResolvedValue([makeWorkspaceNode()])
    entriesByDir({ '': [] })
    mockedCreateMount.mockResolvedValue({
      name: 'repo',
      host_path: '/scratch/repo',
      status: 'ok',
      warning: 'mounting "/scratch" is a broad grant: every file under it becomes agent-writable',
    })

    renderExplorer('ws-1')
    await openAddMount()
    fireEvent.click(screen.getByTestId('library-add-mount-confirm'))

    await waitFor(() => expect(mockedCreateMount).toHaveBeenCalledTimes(1))
    const banner = await screen.findByTestId('library-add-mount-dialog-broad')
    expect(banner).toHaveTextContent('broad grant')
    // The dialog is still open — the verdict stays on screen until acknowledged.
    expect(screen.getByTestId('library-add-mount-confirm')).toHaveTextContent('Done')

    fireEvent.click(screen.getByTestId('library-add-mount-confirm'))
    await waitFor(() =>
      expect(screen.queryByTestId('library-add-mount-dialog-broad')).not.toBeInTheDocument(),
    )
  })

  it('a 403 refusal renders its reason as the refused banner, not the generic error', async () => {
    mockedFetchWorkspaces.mockResolvedValue([makeWorkspaceNode()])
    entriesByDir({ '': [] })
    mockedCreateMount.mockRejectedValue(
      new ApiError(
        403,
        'mounting "/Users/op/.omnipus" is refused: it is this installation’s own data directory',
      ),
    )

    renderExplorer('ws-1')
    await openAddMount()
    fireEvent.click(screen.getByTestId('library-add-mount-confirm'))

    await waitFor(() => expect(mockedCreateMount).toHaveBeenCalledTimes(1))
    expect(await screen.findByTestId('library-add-mount-dialog-refused')).toHaveTextContent(
      'installation’s own data directory',
    )
    expect(screen.queryByTestId('library-add-mount-error')).not.toBeInTheDocument()
  })

  // UAT re-test (2026-09-13), U-21. The test above constructs its ApiError
  // by hand with the specific reason already sitting in `userMessage` — that
  // is NOT how a real 403 arrives. `ApiError.fromResponse` (src/lib/api-error.ts)
  // treats 403 as a "known" status and OVERRIDES userMessage with the generic
  // "You don't have permission to perform this action.", keeping the
  // server's real reason only on `.body` (the raw JSON text). Reading
  // `err.userMessage` directly (as the mount-refusal handler did) is exactly
  // the field-name mismatch that made U-21's dialog show the generic
  // fallback instead of the server's specific reason (e.g. naming /etc).
  // This reproduces the REAL shape `fromResponse` produces.
  it('a 403 refusal built the way the real server round-trip produces it still shows the SPECIFIC reason, not the generic fallback', async () => {
    mockedFetchWorkspaces.mockResolvedValue([makeWorkspaceNode()])
    entriesByDir({ '': [] })
    mockedCreateMount.mockRejectedValue(
      new ApiError(403, "You don't have permission to perform this action.", {
        body: JSON.stringify({ error: 'mounting "/etc" is refused: it is a system directory' }),
      }),
    )

    renderExplorer('ws-1')
    await openAddMount()
    fireEvent.click(screen.getByTestId('library-add-mount-confirm'))

    await waitFor(() => expect(mockedCreateMount).toHaveBeenCalledTimes(1))
    const banner = await screen.findByTestId('library-add-mount-dialog-refused')
    expect(banner).toHaveTextContent('mounting "/etc" is refused: it is a system directory')
    expect(banner.textContent).not.toBe("You don't have permission to perform this action.")
    expect(screen.queryByTestId('library-add-mount-error')).not.toBeInTheDocument()

    // D-127: the refusal is about the path that was SUBMITTED — editing the
    // field must retire it rather than leaving stale server text next to a
    // path the server never saw.
    fireEvent.change(screen.getByTestId('library-add-mount-path'), {
      target: { value: '/etc/changed' },
    })
    expect(screen.queryByTestId('library-add-mount-dialog-refused')).not.toBeInTheDocument()
  })
})

describe('LibraryExplorer — Unmount confirm error handling', () => {
  // The unmount confirm's onError used to CLOSE the dialog (setUnmountTarget
  // to null) and rely solely on a toast that auto-dismisses — a failed
  // unmount looked exactly like a successful one the moment the toast
  // scrolled away. This reproduces a server refusal and asserts the dialog
  // stays open with the reason visible, and the confirm button re-enables so
  // the user can retry or cancel.
  it('a failed unmount shows the server\'s reason, keeps the dialog open, and lets the user cancel or retry', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([
      makeEntry({
        name: 'api',
        path: 'api',
        is_dir: true,
        mount: { name: 'api', host_path: '/Users/dana/projects/api', broad: false },
      }),
    ])
    mockedDeleteMount.mockRejectedValue(
      new ApiError(404, 'Not found.', {
        body: JSON.stringify({ error: 'mount "api" was already removed' }),
      }),
    )

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-row-api')).toBeInTheDocument())
    await openRowMenuAndClick('api', /unmount/i)
    await waitFor(() => expect(screen.getByTestId('library-unmount-dialog')).toBeInTheDocument())

    fireEvent.click(screen.getByTestId('library-unmount-confirm'))
    await waitFor(() => expect(mockedDeleteMount).toHaveBeenCalledWith('ws-1', 'api'))

    await waitFor(() => {
      expect(screen.getByTestId('library-unmount-dialog-error')).toHaveTextContent(
        'mount "api" was already removed',
      )
    })
    // Never looks like success: the dialog is still open, the confirm
    // button re-enabled, and cancelling still works.
    expect(screen.getByTestId('library-unmount-dialog')).toBeInTheDocument()
    expect(screen.getByTestId('library-unmount-confirm')).not.toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))
    await waitFor(() => expect(screen.queryByTestId('library-unmount-dialog')).not.toBeInTheDocument())
  })
})
