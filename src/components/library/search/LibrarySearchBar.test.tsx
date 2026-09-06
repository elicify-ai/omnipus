// LibrarySearchBar.test.tsx — library-b-c-design-2026-09-07 §C1.
//
// The founder decision this pins down: a PERSISTENT bar, not a command
// palette — so the input is always mounted, and typing into it REPLACES the
// file list (`children`) with grouped results; clearing it restores the list
// exactly as the caller passed it in. Fixtures go through the generated zod
// schemas so nothing here is built on a payload the server could not send.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  VaultSearchResponse as VaultSearchResponseSchema,
  KnowledgeBaseInfo as KnowledgeBaseInfoSchema,
  ViewResult as ViewResultSchema,
} from '@/lib/api/generated/schemas'
import { LibrarySearchBar } from './LibrarySearchBar'
import type { VaultSearchResponse, KnowledgeBaseInfo, VaultSearchFn, LoadCollectionInfoFn } from './useVaultSearch'
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

function renderBar(opts: {
  res?: VaultSearchResponse | VaultSearchFn
  info?: KnowledgeBaseInfo | LoadCollectionInfoFn
  onOpenNote?: (p: string) => void
  loadViewResult?: LoadViewResultFn
} = {}) {
  const searchFn: VaultSearchFn =
    typeof opts.res === 'function' ? opts.res : vi.fn().mockResolvedValue(opts.res ?? response())
  const loadCollectionInfo: LoadCollectionInfoFn =
    typeof opts.info === 'function' ? opts.info : vi.fn().mockResolvedValue(opts.info ?? vaultInfo())
  const onOpenNote = opts.onOpenNote ?? vi.fn()
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const utils = render(
    <QueryClientProvider client={client}>
      <LibrarySearchBar
        workspaceId="ws-1"
        folderPath="vault"
        onOpenNote={onOpenNote}
        debounceMs={5}
        searchFn={searchFn}
        loadCollectionInfo={loadCollectionInfo}
        {...(opts.loadViewResult ? { loadViewResult: opts.loadViewResult } : {})}
      >
        <div data-testid="file-tree">The file tree</div>
      </LibrarySearchBar>
    </QueryClientProvider>,
  )
  return { ...utils, searchFn, loadCollectionInfo, onOpenNote }
}

function type(text: string) {
  fireEvent.change(screen.getByTestId('library-search-input'), { target: { value: text } })
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
  })

  it('restores the tree the instant the query is cleared', async () => {
    renderBar({ res: response({ notes: [{ path: 'a.md', title: 'Note A' }] }) })
    type('note')
    await waitFor(() => expect(screen.getByText('Note A')).toBeInTheDocument())

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

    await waitFor(() => expect(screen.getByText('Note A')).toBeInTheDocument())
    fireEvent.click(screen.getByText('Note A'))
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
    await waitFor(() => expect(screen.getByText('Note A')).toBeInTheDocument())
    expect(screen.getByText('Acme Co')).toBeInTheDocument()

    selectTab('library-search-filter-notes')

    await waitFor(() => expect(screen.queryByText('Acme Co')).toBeNull())
    expect(screen.getByText('Note A')).toBeInTheDocument()
  })

  it('says plainly when the selected kind has nothing, rather than an empty panel', async () => {
    renderBar({ res: response({ notes: [{ path: 'a.md', title: 'Note A' }] }) })
    type('a')
    await waitFor(() => expect(screen.getByText('Note A')).toBeInTheDocument())

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
    expect(screen.getByText('Note A')).toBeInTheDocument()
  })

  it('falls back to a generic sentence when the server sent no reason', async () => {
    renderBar({ res: response({ complete: false }) })
    type('note')

    const banner = await screen.findByTestId('library-search-not-ready')
    expect(banner.textContent ?? '').not.toBe('')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Disabled outside a vault
// ─────────────────────────────────────────────────────────────────────────────

describe('LibrarySearchBar — no vault in scope', () => {
  it('disables the input rather than accepting a query it cannot scope', async () => {
    renderBar({ info: vaultInfo({ is_knowledge_base: false, collection_id: undefined, marker: 'none' }) })
    await waitFor(() =>
      expect(screen.getByTestId('library-search-input')).toHaveAttribute(
        'placeholder',
        'Search available inside a vault',
      ),
    )
    expect(screen.getByTestId('library-search-input')).toBeDisabled()
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
