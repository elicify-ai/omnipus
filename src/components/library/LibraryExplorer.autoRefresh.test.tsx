// LibraryExplorer.autoRefresh.test.tsx — the Library's background self-refresh
// (founder decision 2026-10-30, "Q8b A"; scope narrowed by team-lead the same
// day to the FOLDER LISTING only — an open file is not live-updated).
//
// What is pinned here, against the real component with the HTTP boundary
// (src/lib/api clients) faked and timers faked:
//   R1  the shown listing re-checks every 10 s while the tab is visible AND
//       the window is focused; nothing fires at 9 s;
//   R1  no requests while the tab is hidden, the window unfocused, or the
//       panel unmounted — and the re-check resumes on return;
//   R2  only the listing on screen is re-fetched: the browsed folder's
//       entries with a workspace open (never other folders, never the
//       workspaces list), the workspaces list at the virtual root (never
//       entries);
//   R4  a failed tick leaves the view exactly as it was — rows stay, no
//       error banner, no toast — and ticks never stack onto an in-flight
//       re-fetch;
//   R6  an outside change to the shown folder appears without user action.
//
// Counting note: useLibraryCrossTabRefresh (pre-existing, untouched) ALSO
// invalidates the listings when the tab regains focus/visibility. In this
// file every resume-from-hidden/unfocused test settles that immediate
// invalidation refetch into the baseline BEFORE counting the timer's own
// contribution, so each assertion below measures one mechanism only.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
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
  fetchKnowledgeBaseInfo,
  fetchKnowledgeGraph,
  fetchKnowledgeOutline,
  fetchLibraryEntries,
  fetchLibraryWorkspaces,
  searchFiles,
  searchVault,
} from '@/lib/api'
import { LIBRARY_AUTO_REFRESH_INTERVAL_MS } from './useLibraryAutoRefresh'
import { LibraryExplorer } from './LibraryExplorer'

const mockedFetchWorkspaces = vi.mocked(fetchLibraryWorkspaces)
const mockedFetchEntries = vi.mocked(fetchLibraryEntries)

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

function makeWorkspaceNode(over: Partial<LibraryWorkspaceNode> = {}): LibraryWorkspaceNode {
  return { id: 'ws-1', name: 'Website API', entry_count: 1, ...over }
}

// The module the listing currently serves — mutated by tests to stage an
// "outside" change between ticks.
let folderNow: LibraryEntry[]

function serveFolderNow(_wsId: string, dir?: string, includeHidden?: boolean) {
  if (dir !== undefined && dir !== '') {
    // Subfolder listings are staged per test through mockResolvedValue; the
    // default is an empty folder.
    return Promise.resolve([])
  }
  void includeHidden
  return Promise.resolve(folderNow.filter((e) => !e.is_hidden))
}

let hasFocusSpy: ReturnType<typeof vi.spyOn>
let visibilitySpy: ReturnType<typeof vi.spyOn>

async function renderAndSettle(initialWorkspaceId?: string) {
  const view = render(
    <QueryClientProvider client={makeClient()}>
      <LibraryExplorer
        initialWorkspaceId={initialWorkspaceId}
        onClose={vi.fn()}
        onPopOut={vi.fn()}
      />
    </QueryClientProvider>,
  )
  await act(async () => {
    await vi.advanceTimersByTimeAsync(0)
    // TanStack schedules the observer's React notification through a
    // setTimeout(0) on the same fake clock; the trailing advance fires it so
    // the fetched data is guaranteed to be IN the DOM, not merely in the
    // cache, when the test asserts.
    await vi.advanceTimersByTimeAsync(50)
  })
  return view
}

async function tick(ms: number = LIBRARY_AUTO_REFRESH_INTERVAL_MS) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms)
    // See renderAndSettle: the re-fetch's own React notification is a
    // setTimeout(0) scheduled at the tick's boundary — advance past it.
    await vi.advanceTimersByTimeAsync(50)
  })
}

function entriesCalls(dir: string) {
  return mockedFetchEntries.mock.calls.filter((call) => call[1] === dir).length
}

function workspacesCalls() {
  return mockedFetchWorkspaces.mock.calls.length
}

function hideTab() {
  visibilitySpy.mockReturnValue('hidden')
  act(() => {
    fireEvent(document, new Event('visibilitychange'))
  })
}

function showTab() {
  visibilitySpy.mockReturnValue('visible')
  act(() => {
    fireEvent(document, new Event('visibilitychange'))
  })
}

function blurWindow() {
  hasFocusSpy.mockReturnValue(false)
  act(() => {
    fireEvent(window, new Event('blur'))
  })
}

function focusWindow() {
  hasFocusSpy.mockReturnValue(true)
  act(() => {
    fireEvent(window, new Event('focus'))
  })
}

const mockedKnowledgeInfo = vi.mocked(fetchKnowledgeBaseInfo)
const mockedKnowledgeOutline = vi.mocked(fetchKnowledgeOutline)
const mockedKnowledgeGraph = vi.mocked(fetchKnowledgeGraph)
const mockedSearchVault = vi.mocked(searchVault)
const mockedSearchFiles = vi.mocked(searchFiles)

beforeEach(() => {
  vi.clearAllMocks()
  // shouldAdvanceTime: real elapsed time auto-advances the fake clock (by
  // milliseconds), so testing-library's waitFor works alongside the explicit
  // advances below. A test's real duration is far below the 10 s interval,
  // so the auto-advance alone never fires a tick.
  vi.useFakeTimers({ shouldAdvanceTime: true })
  useUiStore.setState({ toasts: [] })
  folderNow = [makeEntry()]
  mockedFetchWorkspaces.mockResolvedValue([makeWorkspaceNode()])
  mockedFetchEntries.mockImplementation(serveFolderNow)
  // The knowledge surfaces answer per browsed folder; a bare vi.fn() would
  // hand the query `undefined` data and crash the tree mid-test (the same
  // false-green trap LibraryExplorer.test.tsx documents in its mock block).
  // Defaults: an ordinary folder — the knowledge surfaces render nothing.
  mockedKnowledgeInfo.mockResolvedValue({
    workspace_id: 'ws-1',
    root_path: '',
    is_knowledge_base: false,
    marker: 'none',
  })
  mockedKnowledgeOutline.mockResolvedValue({ path: '', is_knowledge_base: false, headings: [] })
  mockedKnowledgeGraph.mockResolvedValue({
    collection_id: 'kb_1',
    kind: 'backlinks',
    nodes: [],
    edges: [],
    skipped: [],
    truncated: false,
  })
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
      files: 0,
      bytes: 0,
      matches: 0,
      matches_per_file: 0,
      depth: 0,
      deadline_ms: 0,
      output_bytes: 0,
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
  hasFocusSpy = vi.spyOn(document, 'hasFocus').mockReturnValue(true)
  visibilitySpy = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('LibraryExplorer — background self-refresh of the shown listing (R1)', () => {
  it('re-checks the on-screen folder every 10 s while visible and focused — not at 9 s', async () => {
    await renderAndSettle('ws-1')
    expect(screen.getByTestId('library-row-report.md')).toBeInTheDocument()

    const before = entriesCalls('')
    await tick(9_000)
    expect(entriesCalls('')).toBe(before)
    await tick(1_000)
    expect(entriesCalls('')).toBe(before + 1)
    await tick(LIBRARY_AUTO_REFRESH_INTERVAL_MS)
    expect(entriesCalls('')).toBe(before + 2)
  })

  it('stops re-checking while the tab is hidden, resumes when it returns', async () => {
    await renderAndSettle('ws-1')
    const mounted = entriesCalls('')

    hideTab()
    await tick(3 * LIBRARY_AUTO_REFRESH_INTERVAL_MS)
    expect(entriesCalls('')).toBe(mounted)

    showTab()
    // The pre-existing focus-return invalidation refetches once immediately
    // (settled here, not asserted — the timer is measured below).
    await tick(0)
    const afterReturn = entriesCalls('')
    await tick(LIBRARY_AUTO_REFRESH_INTERVAL_MS)
    expect(entriesCalls('')).toBe(afterReturn + 1)
  })

  it('stops re-checking while the window is unfocused, resumes on focus', async () => {
    await renderAndSettle('ws-1')
    const mounted = entriesCalls('')

    blurWindow()
    await tick(3 * LIBRARY_AUTO_REFRESH_INTERVAL_MS)
    expect(entriesCalls('')).toBe(mounted)

    focusWindow()
    // The pre-existing focus-return invalidation refetches once immediately
    // (settled here, not asserted — the timer is measured below).
    await tick(0)
    const afterReturn = entriesCalls('')
    await tick(LIBRARY_AUTO_REFRESH_INTERVAL_MS)
    expect(entriesCalls('')).toBe(afterReturn + 1)
  })

  it('stops re-checking once the panel is unmounted (closed)', async () => {
    const view = await renderAndSettle('ws-1')
    view.unmount()

    const frozen = entriesCalls('')
    await tick(6 * LIBRARY_AUTO_REFRESH_INTERVAL_MS)
    expect(entriesCalls('')).toBe(frozen)
  })

  it('serves a listing fetched at mount when the panel never unmounts and stays visible', async () => {
    // Guard against the inverse reading of the tests above: with nothing
    // hidden, blurred, or unmounted, the ticks keep coming for the whole
    // window asserted here (30 s = three re-checks).
    await renderAndSettle('ws-1')
    const before = entriesCalls('')
    await tick(3 * LIBRARY_AUTO_REFRESH_INTERVAL_MS)
    expect(entriesCalls('')).toBe(before + 3)
  })
})

describe('LibraryExplorer — only the shown listing is re-fetched (R2)', () => {
  it('inside a workspace, ticks re-fetch only the browsed folder — not other folders, not the workspaces list', async () => {
    mockedFetchEntries.mockImplementation((_wsId, dir) =>
      dir === 'Q4'
        ? Promise.resolve([makeEntry({ name: 'plan.md', path: 'Q4/plan.md' })])
        : Promise.resolve([makeEntry({ name: 'Q4', path: 'Q4', is_dir: true, mime: undefined, is_text_editable: false })]),
    )
    await renderAndSettle('ws-1')
    fireEvent.click(screen.getByTestId('library-row-Q4'))
    // The click's navigation handler is async (the unsaved-edits guard is
    // awaited before the folder switches); settle through its fetch and the
    // observer notification rather than a fixed number of advances.
    await waitFor(() => expect(mockedFetchEntries).toHaveBeenCalledWith('ws-1', 'Q4', false))
    await waitFor(() => expect(screen.getByTestId('library-row-Q4/plan.md')).toBeInTheDocument())

    const rootBefore = entriesCalls('')
    const q4Before = entriesCalls('Q4')
    const wsBefore = workspacesCalls()

    await tick(2 * LIBRARY_AUTO_REFRESH_INTERVAL_MS)

    expect(entriesCalls('Q4')).toBe(q4Before + 2)
    expect(entriesCalls('')).toBe(rootBefore)
    expect(workspacesCalls()).toBe(wsBefore)
  })

  it('at the virtual root the workspaces list is the listing that ticks, and entries are never requested', async () => {
    await renderAndSettle(undefined)
    expect(screen.getByTestId('library-workspace-node-ws-1')).toBeInTheDocument()
    expect(mockedFetchEntries).not.toHaveBeenCalled()

    const before = workspacesCalls()
    await tick(2 * LIBRARY_AUTO_REFRESH_INTERVAL_MS)
    expect(workspacesCalls()).toBe(before + 2)
    expect(mockedFetchEntries).not.toHaveBeenCalled()
  })
})

describe('LibraryExplorer — an outside change to the shown folder appears without user action (R6)', () => {
  it('a file an agent just written shows up in the open folder within one re-check', async () => {
    await renderAndSettle('ws-1')
    expect(screen.getByTestId('library-row-report.md')).toBeInTheDocument()
    expect(screen.queryByTestId('library-row-agent-output.txt')).not.toBeInTheDocument()

    // The outside writer lands between ticks — no WS frame, no focus change.
    folderNow = [
      makeEntry(),
      makeEntry({ name: 'agent-output.txt', path: 'agent-output.txt', mime: 'text/plain' }),
    ]
    await tick(LIBRARY_AUTO_REFRESH_INTERVAL_MS)

    expect(screen.getByTestId('library-row-agent-output.txt')).toBeInTheDocument()
  })
})

describe('LibraryExplorer — a failed re-check is quiet and keeps the view (R4)', () => {
  it('a failed tick keeps the rows on screen, shows no error banner and raises no toast', async () => {
    await renderAndSettle('ws-1')
    expect(screen.getByTestId('library-row-report.md')).toBeInTheDocument()

    mockedFetchEntries.mockRejectedValueOnce(new Error('offline'))
    await tick(LIBRARY_AUTO_REFRESH_INTERVAL_MS)

    // The stale-but-present listing keeps rendering (D-98's data-presence
    // rule); the error state is for a folder that never loaded at all.
    expect(screen.getByTestId('library-row-report.md')).toBeInTheDocument()
    expect(screen.queryByTestId('library-entries-error')).not.toBeInTheDocument()
    expect(useUiStore.getState().toasts).toHaveLength(0)

    // The next tick recovers the view from the same rows.
    await tick(LIBRARY_AUTO_REFRESH_INTERVAL_MS)
    expect(screen.getByTestId('library-row-report.md')).toBeInTheDocument()
  })

  it('ticks never stack onto an in-flight re-fetch', async () => {
    await renderAndSettle('ws-1')
    const settled = entriesCalls('')

    let resolveFetch!: (value: LibraryEntry[]) => void
    mockedFetchEntries.mockImplementationOnce(
      () => new Promise<LibraryEntry[]>((resolve) => { resolveFetch = resolve }),
    )
    await tick(LIBRARY_AUTO_REFRESH_INTERVAL_MS)
    const inFlight = entriesCalls('')
    expect(inFlight).toBe(settled + 1)

    // Two more ticks land while the first re-fetch is still in flight —
    // both are skipped, not stacked.
    await tick(2 * LIBRARY_AUTO_REFRESH_INTERVAL_MS)
    expect(entriesCalls('')).toBe(inFlight)

    await act(async () => {
      resolveFetch(folderNow)
    })
    // The next tick after the re-fetch settles proceeds normally.
    await tick(LIBRARY_AUTO_REFRESH_INTERVAL_MS)
    expect(entriesCalls('')).toBe(inFlight + 1)
  })
})
