// LibrarySearchBar.test.tsx — library-b-c-design-2026-09-07 §C1.
//
// The founder decision this pins down: a PERSISTENT bar, not a command
// palette — so the input is always mounted, and typing into it REPLACES the
// file list (`children`) with grouped results; clearing it restores the list
// exactly as the caller passed it in. Fixtures go through the generated zod
// schemas so nothing here is built on a payload the server could not send.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, within, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  VaultSearchResponse as VaultSearchResponseSchema,
  KnowledgeBaseInfo as KnowledgeBaseInfoSchema,
  ViewResult as ViewResultSchema,
  FileSearchResponse as FileSearchResponseSchema,
} from '@/lib/api/generated/schemas'
import { ApiError } from '@/lib/api-error'
import { LibrarySearchBar } from './LibrarySearchBar'
import type { VaultSearchResponse, KnowledgeBaseInfo, VaultSearchFn, LoadCollectionInfoFn } from './useVaultSearch'
import type { FileSearchResponse, FileSearchFn } from './useFileSearch'
import type { ViewResult } from '@/lib/api/generated/openapi-types'
import type { LoadViewResultFn } from './LibrarySearchBar'

function vaultInfo(over: Partial<KnowledgeBaseInfo> = {}): KnowledgeBaseInfo {
  const base: KnowledgeBaseInfo = {
    workspace_id: 'ws-1',
    root_path: 'vault',
    is_knowledge_base: true,
    marker: 'omnipus_vault',
    collection_id: 'kb_1',
    ...over,
  }
  return KnowledgeBaseInfoSchema.parse(base) as KnowledgeBaseInfo
}

function response(over: Partial<VaultSearchResponse> = {}): VaultSearchResponse {
  const base: VaultSearchResponse = {
    collection_id: 'kb_1',
    complete: true,
    notes: [],
    records: [],
    views: [],
    ...over,
  }
  return VaultSearchResponseSchema.parse(base) as VaultSearchResponse
}

function viewResult(over: Partial<ViewResult> = {}): ViewResult {
  const base: ViewResult = {
    view: 'open-deals',
    label: 'Open deals',
    parts: [],
    rows: [],
    complete: true,
    problems: [],
    ...over,
  }
  return ViewResultSchema.parse(base) as ViewResult
}

function filesResponse(over: Partial<FileSearchResponse> = {}): FileSearchResponse {
  const base: FileSearchResponse = {
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
    ...over,
  }
  return FileSearchResponseSchema.parse(base) as FileSearchResponse
}

/** A folder OUTSIDE any vault — the collection-detection lookup answers
 *  is_knowledge_base:false, which is what puts the bar in files mode. */
function plainFolderInfo(over: Partial<KnowledgeBaseInfo> = {}): KnowledgeBaseInfo {
  const base: KnowledgeBaseInfo = {
    workspace_id: 'ws-1',
    root_path: '01-Areas',
    is_knowledge_base: false,
    marker: 'none',
    ...over,
  }
  return KnowledgeBaseInfoSchema.parse(base) as KnowledgeBaseInfo
}

function renderBar(opts: {
  workspaceId?: string | null
  res?: VaultSearchResponse | VaultSearchFn
  info?: KnowledgeBaseInfo | LoadCollectionInfoFn
  filesRes?: FileSearchResponse | FileSearchFn
  onOpenNote?: (p: string) => void
  onOpenFolder?: (p: string) => void
  loadViewResult?: LoadViewResultFn
} = {}) {
  const searchFn: VaultSearchFn =
    typeof opts.res === 'function' ? opts.res : vi.fn().mockResolvedValue(opts.res ?? response())
  const loadCollectionInfo: LoadCollectionInfoFn =
    typeof opts.info === 'function' ? opts.info : vi.fn().mockResolvedValue(opts.info ?? vaultInfo())
  const searchFilesFn: FileSearchFn =
    typeof opts.filesRes === 'function' ? opts.filesRes : vi.fn().mockResolvedValue(opts.filesRes ?? filesResponse())
  const onOpenNote = opts.onOpenNote ?? vi.fn()
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const utils = render(
    <QueryClientProvider client={client}>
      <LibrarySearchBar
        workspaceId={opts.workspaceId === undefined ? 'ws-1' : opts.workspaceId}
        folderPath="vault"
        onOpenNote={onOpenNote}
        debounceMs={5}
        searchFn={searchFn}
        searchFilesFn={searchFilesFn}
        loadCollectionInfo={loadCollectionInfo}
        {...(opts.onOpenFolder ? { onOpenFolder: opts.onOpenFolder } : {})}
        {...(opts.loadViewResult ? { loadViewResult: opts.loadViewResult } : {})}
      >
        <div data-testid="file-tree">The file tree</div>
      </LibrarySearchBar>
    </QueryClientProvider>,
  )
  return { ...utils, searchFn, loadCollectionInfo, searchFilesFn, onOpenNote }
}

function type(text: string) {
  fireEvent.change(screen.getByTestId('library-search-input'), { target: { value: text } })
}

/** KB-6d's client-side highlight can split a hit's text (title/snippet)
 *  across multiple elements — a `<span>` around the matched word, plain
 *  text around it — which defeats getByText's default single-text-node
 *  matching. This is react-testing-library's own documented workaround:
 *  match by the FULL, normalized textContent of the element whose own
 *  children do not individually contain it (so it matches the row's
 *  wrapping element, not the highlighted fragment alone). */
function getByFullText(text: string): HTMLElement {
  return screen.getByText((_, element) => {
    if (!element) return false
    const hasText = (el: Element) => el.textContent === text
    return hasText(element) && Array.from(element.children).every((child) => !hasText(child))
  })
}

/** Radix's TabsTrigger activates on `mousedown` (pointer path), not `click` —
 *  see @radix-ui/react-tabs's TabsTrigger, which wires onValueChange to
 *  onMouseDown/onKeyDown/onFocus and deliberately NOT onClick. */
function selectTab(testId: string) {
  fireEvent.mouseDown(screen.getByTestId(testId), { button: 0 })
}

// ─────────────────────────────────────────────────────────────────────────────
// Query → grouped render, and clearing restores the tree
// ─────────────────────────────────────────────────────────────────────────────

describe('LibrarySearchBar — query replaces the tree, clearing restores it', () => {
  it('shows the file tree while the box is empty', () => {
    renderBar()
    expect(screen.getByTestId('file-tree')).toBeInTheDocument()
    expect(screen.queryByTestId('library-search-active')).toBeNull()
  })

  it('replaces the tree with grouped results once a query is typed', async () => {
    renderBar({
      res: response({
        notes: [{ path: 'a.md', title: 'Note A' }],
        records: [{ path: 'acme.md', title: 'Acme', record_type: 'company', cells: [{ property: 'status', value: 'open' }] }],
        views: [{ view: 'open-deals', label: 'Open deals', kind: 'table' }],
      }),
    })

    type('acme')

    await waitFor(() => expect(screen.getByTestId('library-search-results')).toBeInTheDocument())
    expect(screen.queryByTestId('file-tree')).toBeNull()

    expect(screen.getByText('Note A')).toBeInTheDocument()
    expect(screen.getByText('Acme')).toBeInTheDocument()
    expect(screen.getByText('company')).toBeInTheDocument()
    expect(screen.getByText('Open deals')).toBeInTheDocument()

    // Segmented filter carries per-kind counts.
    const filters = screen.getByTestId('library-search-filters')
    expect(within(filters).getByTestId('library-search-filter-all')).toHaveTextContent('All3')
    expect(within(filters).getByTestId('library-search-filter-notes')).toHaveTextContent('Notes1')
    expect(within(filters).getByTestId('library-search-filter-records')).toHaveTextContent('Records1')
    expect(within(filters).getByTestId('library-search-filter-views')).toHaveTextContent('Views1')

    // Ported from the retired KnowledgeSearch.test.tsx's "shows the results
    // and says nothing about PARTIAL-ness": a complete, unclamped answer
    // shows none of the honesty banners — never a "partial results" claim
    // over a complete answer (US-6 AS-4's guarantee, carried onto this bar).
    expect(screen.queryByTestId('library-search-not-ready')).toBeNull()
    expect(screen.queryByTestId('library-search-clamped')).toBeNull()
  })

  it('restores the tree the instant the query is cleared', async () => {
    renderBar({ res: response({ notes: [{ path: 'a.md', title: 'Note A' }] }) })
    type('note')
    await waitFor(() => expect(getByFullText('Note A')).toBeInTheDocument())

    type('')

    expect(screen.getByTestId('file-tree')).toBeInTheDocument()
    expect(screen.queryByTestId('library-search-active')).toBeNull()
  })

  it('opens a note hit via onOpenNote, translated to a workspace-relative path', async () => {
    const onOpenNote = vi.fn()
    renderBar({
      res: response({ notes: [{ path: 'sub/a.md', title: 'Note A' }] }),
      info: vaultInfo({ root_path: 'vault' }),
      onOpenNote,
    })
    type('note')

    await waitFor(() => expect(getByFullText('Note A')).toBeInTheDocument())
    fireEvent.click(getByFullText('Note A'))
    expect(onOpenNote).toHaveBeenCalledWith('vault/sub/a.md')
  })

  it('opens a record hit the same way, by its declaring note', async () => {
    const onOpenNote = vi.fn()
    renderBar({
      res: response({ records: [{ path: 'crm/acme.md', title: 'Acme', record_type: 'company', cells: [] }] }),
      info: vaultInfo({ root_path: 'vault' }),
      onOpenNote,
    })
    type('acme')

    await waitFor(() => expect(screen.getByText('Acme')).toBeInTheDocument())
    fireEvent.click(screen.getByText('Acme'))
    expect(onOpenNote).toHaveBeenCalledWith('vault/crm/acme.md')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Filter tabs switch what is shown
// ─────────────────────────────────────────────────────────────────────────────

describe('LibrarySearchBar — filter tabs', () => {
  it('shows only the selected kind once a filter tab is chosen', async () => {
    renderBar({
      res: response({
        notes: [{ path: 'a.md', title: 'Note A' }],
        records: [{ path: 'acme.md', title: 'Acme Co', cells: [] }],
      }),
    })
    type('a')
    await waitFor(() => expect(getByFullText('Note A')).toBeInTheDocument())
    expect(getByFullText('Acme Co')).toBeInTheDocument()

    selectTab('library-search-filter-notes')

    await waitFor(() => expect(screen.queryByText('Acme Co')).toBeNull())
    expect(getByFullText('Note A')).toBeInTheDocument()
  })

  it('says plainly when the selected kind has nothing, rather than an empty panel', async () => {
    renderBar({ res: response({ notes: [{ path: 'a.md', title: 'Note A' }] }) })
    type('a')
    await waitFor(() => expect(getByFullText('Note A')).toBeInTheDocument())

    selectTab('library-search-filter-records')

    expect(await screen.findByTestId('library-search-filter-empty')).toHaveTextContent(/no records match/i)
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Empty state
// ─────────────────────────────────────────────────────────────────────────────

describe('LibrarySearchBar — empty results', () => {
  it('says no results for the query when the collection genuinely has none', async () => {
    renderBar({ res: response() })
    type('nonexistent')

    const empty = await screen.findByTestId('library-search-empty')
    expect(empty).toHaveTextContent('nonexistent')
    expect(screen.queryByTestId('library-search-results')).toBeNull()
  })
})

describe('LibrarySearchBar — vault search failure (ported from the retired KnowledgeSearch box)', () => {
  it('surfaces a failed vault search as a visible error, never as an empty result list', async () => {
    const searchFn = vi.fn().mockRejectedValue(new Error('Search failed (HTTP 500).'))
    renderBar({ res: searchFn })
    type('landlock')

    const banner = await screen.findByTestId('library-search-error')
    expect(banner).toHaveTextContent('Search failed (HTTP 500).')
    // Critically: NOT reported as "no results", which would be a false
    // statement about the vault.
    expect(screen.queryByTestId('library-search-empty')).toBeNull()
    expect(screen.queryByTestId('library-search-results')).toBeNull()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Index-not-ready — complete: false
// ─────────────────────────────────────────────────────────────────────────────

describe('LibrarySearchBar — not-ready index', () => {
  it('states the server\'s own reason when the answer is not complete', async () => {
    renderBar({
      res: response({
        complete: false,
        complete_reason: 'the vault index has never finished indexing this vault',
        notes: [{ path: 'a.md', title: 'Note A' }],
      }),
    })
    type('note')

    const banner = await screen.findByTestId('library-search-not-ready')
    expect(banner).toHaveAttribute('role', 'status')
    expect(banner).toHaveTextContent('the vault index has never finished indexing this vault')
    // Results are still shown alongside the honesty banner.
    expect(getByFullText('Note A')).toBeInTheDocument()
  })

  it('falls back to a generic sentence when the server sent no reason', async () => {
    renderBar({ res: response({ complete: false }) })
    type('note')

    const banner = await screen.findByTestId('library-search-not-ready')
    expect(banner.textContent ?? '').not.toBe('')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Context switching (unified-search-and-grep-spec.md US-2/US-4) — a plain
// folder is now FILES-searchable rather than disabled; only the Library
// virtual root (and mid-resolution) stays disabled.
// ─────────────────────────────────────────────────────────────────────────────

describe('LibrarySearchBar — a plain folder gets Files search, not the old disabled state', () => {
  it('enables the input with a files-oriented placeholder outside a vault (US-2/US-4 AS-3)', async () => {
    // THE DEFECT THIS FIXES. Until unified-search-and-grep-spec.md, a folder
    // that was not a vault was indistinguishable from "nothing to search
    // here" — collectionId===undefined disabled the whole bar. A plain
    // folder/mount now gets the FILES kind instead.
    renderBar({ info: plainFolderInfo() })
    await waitFor(() =>
      expect(screen.getByTestId('library-search-input')).toHaveAttribute(
        'placeholder',
        'Search files and folders',
      ),
    )
    expect(screen.getByTestId('library-search-input')).not.toBeDisabled()
  })

  it('stays disabled only while collection detection is still resolving', () => {
    renderBar({ info: () => new Promise(() => {}) })
    expect(screen.getByTestId('library-search-input')).toHaveAttribute('placeholder', 'Checking this folder…')
    expect(screen.getByTestId('library-search-input')).toBeDisabled()
  })
})

describe('LibrarySearchBar — the Library virtual root (US-4 AS-2)', () => {
  it('renders in its disabled state with an explanatory placeholder, and never looks up a collection', async () => {
    const loadCollectionInfo = vi.fn()
    renderBar({ workspaceId: null, info: loadCollectionInfo })

    expect(screen.getByTestId('library-search-input')).toBeDisabled()
    expect(screen.getByTestId('library-search-input')).toHaveAttribute(
      'placeholder',
      'Open a workspace to search',
    )
    expect(screen.getByTestId('file-tree')).toBeInTheDocument()
    expect(loadCollectionInfo).not.toHaveBeenCalled()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// MV-10 — exactly one search input, in every mode
// ─────────────────────────────────────────────────────────────────────────────

describe('LibrarySearchBar — exactly one search input per view (MV-10)', () => {
  it('is the ONLY search input while in vault mode', () => {
    renderBar({ info: vaultInfo() })
    expect(screen.getAllByRole('searchbox')).toHaveLength(1)
  })

  it('is the ONLY search input while in files mode', () => {
    renderBar({ info: plainFolderInfo() })
    expect(screen.getAllByRole('searchbox')).toHaveLength(1)
  })

  it('is the ONLY search input at the disabled virtual root', () => {
    renderBar({ workspaceId: null })
    expect(screen.getAllByRole('searchbox')).toHaveLength(1)
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// A view hit opens its evaluated result
// ─────────────────────────────────────────────────────────────────────────────

describe('LibrarySearchBar — opening a view', () => {
  it('fetches and draws the view result in a dialog, addressed by name and collection alone', async () => {
    const loadViewResult = vi.fn().mockResolvedValue(viewResult())
    renderBar({
      res: response({ views: [{ view: 'open-deals', label: 'Open deals' }] }),
      loadViewResult,
    })
    type('deals')

    await waitFor(() => expect(screen.getByText('Open deals')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('vault-search-view-hit'))

    await waitFor(() => expect(loadViewResult).toHaveBeenCalledWith('ws-1', 'kb_1', 'open-deals', expect.anything()))
    expect(await screen.findByTestId('view-empty')).toBeInTheDocument()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Honesty port (unified-search-and-grep-spec.md US-1, MV-9) — every signal
// the retired KnowledgeSearch box carried, now on this bar.
// ─────────────────────────────────────────────────────────────────────────────

describe('LibrarySearchBar — attachments (US-1, ported attachment search)', () => {
  it('renders an Attachments group and tab with its own count', async () => {
    renderBar({
      res: response({
        notes: [{ path: 'a.md', title: 'Note A' }],
        attachments: [{ path: 'img/diagram-v3.png', name: 'diagram-v3.png' }],
      }),
    })
    type('diagram')

    await waitFor(() => expect(screen.getByText('diagram-v3.png')).toBeInTheDocument())
    const filters = screen.getByTestId('library-search-filters')
    expect(within(filters).getByTestId('library-search-filter-attachments')).toHaveTextContent('Attachments1')
  })

  it('never describes an attachment hit as a failure — it was never read, by design (FR-039a)', async () => {
    renderBar({ res: response({ attachments: [{ path: 'img/diagram-v3.png', name: 'diagram-v3.png' }] }) })
    type('diagram')

    const hit = await screen.findByTestId('vault-search-attachment-hit')
    expect(hit.textContent ?? '').not.toMatch(/could not be read|failed/i)
  })

  it('opens an attachment hit via onOpenNote, translated to a workspace-relative path', async () => {
    const onOpenNote = vi.fn()
    renderBar({
      res: response({ attachments: [{ path: 'img/diagram.png', name: 'diagram.png' }] }),
      info: vaultInfo({ root_path: 'vault' }),
      onOpenNote,
    })
    type('diagram')

    await waitFor(() => expect(screen.getByText('diagram.png')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('vault-search-attachment-hit'))
    expect(onOpenNote).toHaveBeenCalledWith('vault/img/diagram.png')
  })
})

describe('LibrarySearchBar — excerpt-unavailable note hits (US-1 AS-4)', () => {
  it('renders the note with an explicit marker instead of a bare title over nothing', async () => {
    renderBar({
      res: response({ notes: [{ path: 'notes/gone.md', title: 'Gone', excerpt_unavailable: true }] }),
    })
    type('gone')

    await waitFor(() => expect(screen.getByText('Gone')).toBeInTheDocument())
    expect(screen.getByTestId('vault-search-excerpt-unavailable')).toBeVisible()
  })

  it('shows the real snippet instead of the marker when one is present', async () => {
    renderBar({
      res: response({
        notes: [{ path: 'notes/a.md', title: 'A', snippet: 'kernel sandbox', excerpt_unavailable: true }],
      }),
    })
    type('a')

    await waitFor(() => expect(getByFullText('kernel sandbox')).toBeInTheDocument())
    expect(screen.queryByTestId('vault-search-excerpt-unavailable')).toBeNull()
  })
})

describe('LibrarySearchBar — server-authored statement and coverage (US-1 AS-1/AS-2, FR-036)', () => {
  it('shows the server statement, in the reading flow, ahead of a partial answer', async () => {
    const statement = 'Searched 4,120 of 12,880 notes — indexing is still running.'
    renderBar({
      res: response({
        complete: false,
        notes: [{ path: 'a.md', title: 'A' }],
        statement,
        notes_searched: 4120,
        notes_total_known: 12880,
      }),
    })
    type('a')

    const banner = await screen.findByTestId('library-search-not-ready')
    expect(within(banner).getByText(statement)).toBeVisible()
    expect(screen.getByTestId('library-search-coverage-ratio')).toHaveTextContent('4,120 of 12,880')
  })

  it('shows a bare "so far" count — never an invented denominator — when the total is unknown', async () => {
    renderBar({
      res: response({ complete: false, notes: [{ path: 'a.md', title: 'A' }], notes_searched: 4120 }),
    })
    type('a')

    const banner = await screen.findByTestId('library-search-not-ready')
    expect(screen.getByTestId('library-search-coverage-so-far')).toHaveTextContent('4,120 notes searched so far')
    expect(screen.queryByTestId('library-search-coverage-ratio')).toBeNull()
    // No invented ratio anywhere in the banner.
    expect(banner.textContent ?? '').not.toMatch(/[\d,]+\s*(?:of|\/)\s*[\d,]+/i)
  })

  it('shows the server statement beside a COMPLETE has-hits answer too, not only a bare count', async () => {
    const statement = 'Searched the whole of this vault; its index was complete at query time.'
    renderBar({
      res: response({ complete: true, notes: [{ path: 'a.md', title: 'A' }], statement }),
    })
    type('a')

    await waitFor(() => expect(screen.getByText('A')).toBeInTheDocument())
    expect(screen.getByTestId('library-search-complete-statement')).toHaveTextContent(statement)
    expect(screen.queryByTestId('library-search-not-ready')).toBeNull()
  })

  it('falls back to complete_reason when the server has not been upgraded to send `statement` yet', async () => {
    // Additive compatibility (MV-9): an older server sends complete_reason but
    // no statement — the not-ready banner still says something real.
    renderBar({
      res: response({ complete: false, complete_reason: 'the vault index has never finished indexing this vault' }),
    })
    type('a')

    const banner = await screen.findByTestId('library-search-not-ready')
    expect(banner).toHaveTextContent('the vault index has never finished indexing this vault')
  })

  it('shows the server statement on a COMPLETE but EMPTY answer too — never only the client\'s own sentence', async () => {
    // Ported from the retired KnowledgeSearch.test.tsx: an out-of-scope
    // collection_id (or any complete-but-refused answer) comes back as
    // hits: [], complete: true, with a server statement explaining why — the
    // server writes the sentence precisely so the client cannot phrase the
    // answer for it.
    const statement = 'No knowledge base with that identifier is available in this workspace.'
    renderBar({ res: response({ complete: true, statement }) })
    type('landlock')

    const empty = await screen.findByTestId('library-search-empty')
    expect(empty).toBeInTheDocument()
    expect(screen.getByTestId('library-search-complete-statement')).toHaveTextContent(statement)
  })
})

describe('LibrarySearchBar — clamp disclosure (FR-037)', () => {
  it('says the count was clamped, naming the refused number when the server echoed it', async () => {
    renderBar({
      res: response({ notes: [{ path: 'a.md', title: 'A' }], limit_clamped: true, limit_requested: 400 }),
    })
    type('a')

    const notice = await screen.findByTestId('library-search-clamped')
    expect(notice).toHaveTextContent(/clamped/i)
    expect(notice).toHaveTextContent('400')
  })

  it('still discloses the clamp when the server omitted the requested number', async () => {
    renderBar({ res: response({ notes: [{ path: 'a.md', title: 'A' }], limit_clamped: true }) })
    type('a')

    const notice = await screen.findByTestId('library-search-clamped')
    expect(notice).toHaveTextContent(/clamped/i)
    expect(notice.textContent ?? '').not.toMatch(/undefined|NaN/)
  })

  it('says nothing when nothing was clamped', async () => {
    renderBar({ res: response({ notes: [{ path: 'a.md', title: 'A' }] }) })
    type('a')

    await waitFor(() => expect(screen.getByText('A')).toBeInTheDocument())
    expect(screen.queryByTestId('library-search-clamped')).toBeNull()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// FILES kind (unified-search-and-grep-spec.md US-2/US-4)
// ─────────────────────────────────────────────────────────────────────────────

describe('LibrarySearchBar — files kind: name and content hits', () => {
  it('renders a name match with its path', async () => {
    renderBar({
      info: plainFolderInfo(),
      filesRes: filesResponse({ hits: [{ path: '01-Areas/Q3 report.md', match_kind: 'name' }] }),
    })
    type('report')

    const hit = await screen.findByTestId('file-search-name-hit')
    expect(hit).toHaveTextContent('01-Areas/Q3 report.md')
  })

  it('renders a content match with its line number and excerpt', async () => {
    renderBar({
      info: plainFolderInfo(),
      filesRes: filesResponse({
        hits: [
          {
            path: '01-Areas/notes.txt',
            match_kind: 'content',
            line: 42,
            excerpt: 'quarterly meeting notes',
          },
        ],
      }),
    })
    type('meeting')

    const hit = await screen.findByTestId('file-search-content-hit')
    expect(hit).toHaveTextContent('01-Areas/notes.txt')
    expect(hit).toHaveTextContent(':42')
    expect(hit).toHaveTextContent('quarterly meeting notes')
  })

  it('renders optional context lines around a content match', async () => {
    renderBar({
      info: plainFolderInfo(),
      filesRes: filesResponse({
        hits: [
          {
            path: 'a.txt',
            match_kind: 'content',
            line: 10,
            excerpt: 'the match line',
            context_before: ['line eight', 'line nine'],
            context_after: ['line eleven'],
          },
        ],
      }),
    })
    type('match')

    const hit = await screen.findByTestId('file-search-content-hit')
    expect(hit).toHaveTextContent('line eight')
    expect(hit).toHaveTextContent('line nine')
    expect(hit).toHaveTextContent('the match line')
    expect(hit).toHaveTextContent('line eleven')
  })

  it('opens a file hit via onOpenNote with its path unchanged (no collection translation)', async () => {
    const onOpenNote = vi.fn()
    renderBar({
      info: plainFolderInfo(),
      filesRes: filesResponse({ hits: [{ path: '01-Areas/report.md', match_kind: 'name' }] }),
      onOpenNote,
    })
    type('report')

    fireEvent.click(await screen.findByTestId('file-search-name-hit'))
    expect(onOpenNote).toHaveBeenCalledWith('01-Areas/report.md')
  })

  it('says no results for the query when nothing matched', async () => {
    renderBar({ info: plainFolderInfo(), filesRes: filesResponse() })
    type('nonexistent')

    const empty = await screen.findByTestId('library-search-empty')
    expect(empty).toHaveTextContent('nonexistent')
  })

  it('never sorts hits client-side — engine order is preserved', async () => {
    renderBar({
      info: plainFolderInfo(),
      filesRes: filesResponse({
        hits: [
          { path: 'zebra.md', match_kind: 'name' },
          { path: 'apple.md', match_kind: 'name' },
        ],
      }),
    })
    type('a')

    await screen.findAllByTestId('file-search-name-hit')
    const paths = screen.getAllByTestId('file-search-name-hit').map((el) => el.textContent)
    expect(paths[0]).toContain('zebra.md')
    expect(paths[1]).toContain('apple.md')
  })
})

describe('LibrarySearchBar — files kind: honest truncation (US-2 AS-3/AS-6, MV-3)', () => {
  it('states the stopped-early reason for a root_lost walk', async () => {
    renderBar({
      info: plainFolderInfo(),
      filesRes: filesResponse({
        truncated: true,
        truncated_reason: 'root_lost',
        stats: {
          files_visited: 12,
          bytes_scanned: 4096,
          files_skipped_problems: 0,
          files_pruned_ignored: 0,
          files_skipped_per_file_cap: 0,
          hits_capped_per_file: 0,
        },
      }),
    })
    type('report')

    const banner = await screen.findByTestId('library-search-truncated')
    expect(banner).toHaveAttribute('role', 'status')
    expect(banner.textContent ?? '').toMatch(/unreadable while searching/i)
    expect(banner.textContent ?? '').toMatch(/12 files? searched/i)
  })

  it('states a distinct reason for a deadline stop', async () => {
    renderBar({
      info: plainFolderInfo(),
      filesRes: filesResponse({ truncated: true, truncated_reason: 'deadline' }),
    })
    type('report')

    const banner = await screen.findByTestId('library-search-truncated')
    expect(banner.textContent ?? '').toMatch(/ran out of time/i)
  })

  it('shows no truncation banner for a complete answer', async () => {
    renderBar({
      info: plainFolderInfo(),
      filesRes: filesResponse({ hits: [{ path: 'a.md', match_kind: 'name' }], truncated: false }),
    })
    type('a')

    await waitFor(() => expect(screen.getByTestId('file-search-name-hit')).toBeInTheDocument())
    expect(screen.queryByTestId('library-search-truncated')).toBeNull()
  })
})

describe('LibrarySearchBar — files kind: literal metacharacters (US-2 AS-7, FR-016)', () => {
  it('never lets a regex metacharacter surface as a parse error — the bar always sends regex:false', async () => {
    const searchFilesFn = vi.fn().mockResolvedValue(filesResponse({ hits: [{ path: 'calc.py', match_kind: 'content', line: 3, excerpt: 'f(x)' }] }))
    renderBar({ info: plainFolderInfo(), filesRes: searchFilesFn })
    type('f(x)')

    await waitFor(() => expect(searchFilesFn).toHaveBeenCalled())
    const body = searchFilesFn.mock.calls[0]?.[1] as { query: string; regex: boolean }
    expect(body.query).toBe('f(x)')
    expect(body.regex).toBe(false)
    expect(screen.queryByTestId('library-search-error')).toBeNull()
  })
})

describe('LibrarySearchBar — files kind: at most one search in flight (MV-11)', () => {
  it('holds ≤1 in-flight search — a new keystroke cancels the previous request', async () => {
    const searchFilesFn = vi.fn().mockImplementation(() => new Promise(() => {}))
    renderBar({ info: plainFolderInfo(), filesRes: searchFilesFn })

    type('rep')
    await waitFor(() => expect(searchFilesFn).toHaveBeenCalledTimes(1))
    const firstSignal = searchFilesFn.mock.calls[0]?.[2] as AbortSignal

    type('report')
    await waitFor(() => expect(searchFilesFn).toHaveBeenCalledTimes(2))
    expect(firstSignal.aborted).toBe(true)
  })

  it('keeps previous results and retries once on a 429, without ever showing an error', async () => {
    const searchFilesFn = vi
      .fn()
      .mockResolvedValueOnce(filesResponse({ hits: [{ path: 'old.md', match_kind: 'name' }] }))
    renderBar({ info: plainFolderInfo(), filesRes: searchFilesFn })

    type('old')
    await waitFor(() => expect(screen.getByTestId('file-search-name-hit')).toHaveTextContent('old.md'))

    searchFilesFn.mockRejectedValueOnce(new ApiError(429, 'Too many requests'))
    searchFilesFn.mockResolvedValueOnce(filesResponse({ hits: [{ path: 'new.md', match_kind: 'name' }] }))

    type('new')
    await waitFor(() => expect(searchFilesFn).toHaveBeenCalledTimes(2))
    // The 429 landed but nothing on screen flashed an error — the stale
    // result is still what is rendered while the retry is pending.
    expect(screen.queryByTestId('library-search-error')).toBeNull()

    await act(async () => {
      await new Promise((r) => setTimeout(r, 550))
    })
    await waitFor(() => expect(searchFilesFn).toHaveBeenCalledTimes(3))
    await waitFor(() => expect(screen.getByTestId('file-search-name-hit')).toHaveTextContent('new.md'))
    expect(screen.queryByTestId('library-search-error')).toBeNull()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Finding F-L: a failed FILE search must surface as an error, exactly like a
// failed vault search already does — the discriminating test the audit found
// missing (a mutation to `isVaultMode ? vaultError : null` left every
// existing test green).
// ─────────────────────────────────────────────────────────────────────────────

describe('LibrarySearchBar — files kind search failure (finding F-L)', () => {
  it('surfaces a failed FILE search as a visible error, never as an empty result list', async () => {
    const searchFilesFn = vi.fn().mockRejectedValue(new Error('walk failed'))
    renderBar({ info: plainFolderInfo(), filesRes: searchFilesFn })

    type('report')
    const banner = await screen.findByTestId('library-search-error')
    expect(banner).toHaveTextContent('walk failed')
    // The failure must not ALSO render as a silent "No results" — that is
    // precisely the false-negative the missing wiring would produce.
    expect(screen.queryByTestId('library-search-empty')).toBeNull()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Finding R-3: a stale error from a PREVIOUS query must not survive into the
// next one's own in-flight or successful request.
// ─────────────────────────────────────────────────────────────────────────────

describe('LibrarySearchBar — files kind: a new query clears the previous one\'s error (finding R-3)', () => {
  it('drops query A\'s error once query B starts, and shows B\'s real results', async () => {
    const searchFilesFn = vi
      .fn()
      .mockRejectedValueOnce(new Error('query A failed'))
      .mockResolvedValueOnce(filesResponse({ hits: [{ path: 'b.md', match_kind: 'name' }] }))
    renderBar({ info: plainFolderInfo(), filesRes: searchFilesFn })

    type('a-query')
    await screen.findByTestId('library-search-error')

    type('b-query')
    await waitFor(() => expect(searchFilesFn).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.getByTestId('file-search-name-hit')).toHaveTextContent('b.md'))
    expect(screen.queryByTestId('library-search-error')).toBeNull()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Finding F-J: `truncated: true` with no `truncated_reason` must still
// render a banner — the schema does not enforce the reason's presence on the
// wire, so the client must not silently drop it.
// ─────────────────────────────────────────────────────────────────────────────

describe('LibrarySearchBar — files kind: truncated with no reason (finding F-J)', () => {
  it('renders a generic stopped-early banner rather than none at all', async () => {
    renderBar({
      info: plainFolderInfo(),
      filesRes: filesResponse({
        truncated: true,
        hits: [{ path: 'a.md', match_kind: 'name' }],
        stats: {
          files_visited: 5,
          bytes_scanned: 0,
          files_skipped_problems: 0,
          files_pruned_ignored: 0,
          files_skipped_per_file_cap: 0,
          hits_capped_per_file: 0,
        },
      }),
    })

    type('report')
    const banner = await screen.findByTestId('library-search-truncated')
    expect(banner).toHaveTextContent(/stopped early/i)
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Finding F-K: the walk-accounting stats were almost entirely discarded —
// the sharpest case is a file visible in the listing, pruned from search,
// with "No results" and nothing else on screen.
// ─────────────────────────────────────────────────────────────────────────────

describe('LibrarySearchBar — files kind: skip stats are surfaced (finding F-K)', () => {
  it('says WHY nothing matched when files were pruned, even on a zero-hit answer', async () => {
    renderBar({
      info: plainFolderInfo(),
      filesRes: filesResponse({
        hits: [],
        stats: {
          files_visited: 4,
          bytes_scanned: 0,
          files_skipped_problems: 0,
          files_pruned_ignored: 3,
          files_skipped_per_file_cap: 0,
          hits_capped_per_file: 0,
        },
      }),
    })

    type('report')
    await screen.findByTestId('library-search-empty')
    const stats = await screen.findByTestId('library-search-files-stats')
    expect(stats).toHaveTextContent(/3.*gitignore/i)
  })

  it('renders nothing extra when every stat is zero', async () => {
    renderBar({ info: plainFolderInfo(), filesRes: filesResponse({ hits: [] }) })
    type('report')
    await screen.findByTestId('library-search-empty')
    expect(screen.queryByTestId('library-search-files-stats')).toBeNull()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Finding F-I: a knowledge base whose detection FAILED must surface the
// failure, never silently fall through to a plain file walk.
// ─────────────────────────────────────────────────────────────────────────────

describe('LibrarySearchBar — knowledge base detection failure (finding F-I)', () => {
  it('surfaces detection_error as a visible error, and does not run a file search instead', async () => {
    const searchFilesFn = vi.fn().mockResolvedValue(filesResponse())
    renderBar({
      info: vaultInfo({
        is_knowledge_base: true,
        collection_id: undefined,
        detection_error: { code: 'root_unreadable', message: 'cannot read vault: permission denied' },
      }),
      filesRes: searchFilesFn,
    })

    type('report')
    const banner = await screen.findByTestId('library-search-error')
    expect(banner).toHaveTextContent('permission denied')
    expect(searchFilesFn).not.toHaveBeenCalled()
  })

  it('surfaces the collection-info request itself failing outright', async () => {
    const loadCollectionInfo = vi.fn().mockRejectedValue(new Error('network error'))
    renderBar({ info: loadCollectionInfo })

    type('report')
    const banner = await screen.findByTestId('library-search-error')
    expect(banner).toHaveTextContent('network error')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Findings R-1/R-2: a directory hit must open as a FOLDER (and clear the
// search), and must be inert — never fall back to opening it as a file —
// when the caller wired no onOpenFolder handler.
// ─────────────────────────────────────────────────────────────────────────────

describe('LibrarySearchBar — directory hits (findings R-1/R-2)', () => {
  it('clicking a directory hit clears the search and calls onOpenFolder, never onOpenNote', async () => {
    const onOpenFolder = vi.fn()
    const onOpenNote = vi.fn()
    renderBar({
      info: plainFolderInfo(),
      filesRes: filesResponse({ hits: [{ path: 'sub-dir', match_kind: 'name', is_dir: true }] }),
      onOpenFolder,
      onOpenNote,
    })

    type('sub')
    const row = await screen.findByTestId('file-search-name-hit')
    fireEvent.click(row)

    expect(onOpenFolder).toHaveBeenCalledWith('sub-dir')
    expect(onOpenNote).not.toHaveBeenCalled()
    // R-1: the query itself must be cleared as part of navigating — the
    // search input reverts to empty, which is what un-replaces `children`.
    await waitFor(() => expect(screen.getByTestId('library-search-input')).toHaveValue(''))
    await waitFor(() => expect(screen.getByTestId('file-tree')).toBeInTheDocument())
  })

  it('a directory hit is inert — not openFile — when no onOpenFolder handler is wired', async () => {
    const onOpenNote = vi.fn()
    renderBar({
      info: plainFolderInfo(),
      filesRes: filesResponse({ hits: [{ path: 'sub-dir', match_kind: 'name', is_dir: true }] }),
      onOpenNote,
    })

    type('sub')
    const row = await screen.findByTestId('file-search-name-hit')
    fireEvent.click(row)

    // R-2: no handler means the row does nothing — it must NOT degrade into
    // opening the directory path as though it were a note/file.
    expect(onOpenNote).not.toHaveBeenCalled()
  })
})
