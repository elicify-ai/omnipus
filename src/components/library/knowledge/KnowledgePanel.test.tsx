// KnowledgePanel.test.tsx — the ORDERING of the first-run answers, and the
// panel's own contract with the Library surface (ADR-067 US-4, US-6, E-1, E-9,
// FR-020/FR-021, FR-024, FR-031, FR-080).
//
// ORACLE. Expected values come from the specification and the contract files,
// never from the component:
//
//   - "Given a mounted folder containing '.obsidian/' … it is treated as a
//      knowledge base"; ".omnipus-vault/" likewise; "neither" → an ordinary
//      folder                                          US-4 AS-1/AS-2/AS-3
//   - "Marker present but unreadable → Detection fails loudly; the folder is
//      not silently downgraded to 'ordinary'"                            E-9
//   - "Given a newly created and still-indexing knowledge base … the indexing
//      state is shown, not the empty-collection first run"         US-6 AS-5
//   - "Given indexing has finished … no incompleteness statement is shown"
//                                                                 US-6 AS-4
//   - "Collection with 0 notes → First-run offer to create a note"       E-1
//   - "'idle' … is the terminal success state and the client should stop
//      showing progress"          contracts/asyncapi.yaml, phase description
//   - "total_files is present if and only if total_known is true"      ditto
//   - KnowledgeBaseInfo "carries no index counts … a caller that wants to know
//      how far indexing has got subscribes, it does not poll"           FR-080
//   - display_name "Absent when the marker records none; the SPA then falls
//      back to the root folder's own name"                             FR-024
//
// MOCK BOUNDARY. Only the network is mocked — `loadInfo` is the injected
// fetch. KnowledgePanel, resolveKnowledgeFirstRunState and KnowledgeEmptyState
// are all real, so a break in any of them fails these tests.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import type { KnowledgeIndexProgressFrame } from '@/lib/api/generated/asyncapi-types'
import type { KnowledgeBaseInfo } from '@/lib/api/generated/openapi-types'
import { useKnowledgeIndexStore } from '@/store/knowledgeIndex'

import type { KnowledgeSearchFn } from './useKnowledgeSearch'
import {
  KnowledgePanel,
  knowledgeDisplayName,
  resolveKnowledgeFirstRunState,
} from './KnowledgePanel'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

const COLLECTION_ID = 'kb_3d1c9a7e5b2f4806'

function makeInfo(over: Partial<KnowledgeBaseInfo> = {}): KnowledgeBaseInfo {
  return {
    workspace_id: 'ws_7f3a',
    root_path: 'notes/vault',
    is_knowledge_base: true,
    marker: 'omnipus_vault',
    collection_id: COLLECTION_ID,
    display_name: 'Research vault',
    ...over,
  }
}

function makeProgress(
  over: Partial<KnowledgeIndexProgressFrame> = {},
): KnowledgeIndexProgressFrame {
  return {
    type: 'knowledge_index_progress',
    collection_id: COLLECTION_ID,
    workspace_id: 'ws_7f3a',
    phase: 'indexing',
    indexed_files: 12,
    total_known: true,
    total_files: 500,
    ...over,
  }
}

/** A search stub that answers with nothing. Module scope because more than one
 *  describe needs it — the panel mounts the real KnowledgeSearch whenever the
 *  folder is a collection, and an unstubbed search would reach the network. */
const noSearch: KnowledgeSearchFn = vi.fn().mockResolvedValue({
  collection_id: COLLECTION_ID,
  hits: [],
  incompleteness: { complete: true, total_known: true, statement: 'Search covered every note.' },
  limit_applied: 20,
  limit_clamped: false,
})

function renderPanel(props: Partial<React.ComponentProps<typeof KnowledgePanel>> = {}) {
  const loadInfo = props.loadInfo ?? vi.fn().mockResolvedValue(makeInfo())
  const utils = render(
    <QueryClientProvider client={makeClient()}>
      <KnowledgePanel workspaceId="ws_7f3a" path="notes/vault" {...props} loadInfo={loadInfo} />
    </QueryClientProvider>,
  )
  return { ...utils, loadInfo }
}

// ── The ordering rules, as a pure function ────────────────────────────────────

describe('resolveKnowledgeFirstRunState — precedence', () => {
  it('a detection error outranks is_knowledge_base=false — never the ordinary-folder answer (E-9)', () => {
    // The contract states is_knowledge_base "carries the last known answer"
    // when detection_error is present, so the two arriving together is the
    // normal shape, not a malformed one. Reading the boolean first is the bug.
    const state = resolveKnowledgeFirstRunState(
      makeInfo({
        is_knowledge_base: false,
        marker: 'none',
        collection_id: undefined,
        detection_error: { code: 'marker_unreadable', message: 'permission denied' },
      }),
    )
    expect(state.kind).toBe('detection_error')
  })

  it('a detection error outranks a live progress frame too', () => {
    const state = resolveKnowledgeFirstRunState(
      makeInfo({ detection_error: { code: 'root_unreadable', message: 'permission denied' } }),
      makeProgress({ phase: 'idle', indexed_files: 4200 }),
    )
    expect(state.kind).toBe('detection_error')
  })

  it('markers decide the verdict (US-4 AS-1, AS-2, AS-3)', () => {
    expect(
      resolveKnowledgeFirstRunState(makeInfo({ marker: 'obsidian', is_knowledge_base: true })).kind,
    ).not.toBe('not_a_knowledge_base')
    expect(
      resolveKnowledgeFirstRunState(makeInfo({ marker: 'omnipus_vault', is_knowledge_base: true })).kind,
    ).not.toBe('not_a_knowledge_base')
    expect(
      resolveKnowledgeFirstRunState(
        makeInfo({ marker: 'none', is_knowledge_base: false, collection_id: undefined }),
      ).kind,
    ).toBe('not_a_knowledge_base')
  })

  it('INDEXING outranks the empty-collection first run (US-6 AS-5)', () => {
    // A brand-new collection: nothing indexed yet, and the walk still running.
    // "This collection is empty" here would be a claim about a collection
    // nobody has finished looking at.
    const state = resolveKnowledgeFirstRunState(
      makeInfo(),
      makeProgress({ phase: 'enumerating', indexed_files: 0, total_known: false, total_files: undefined }),
    )
    expect(state.kind).toBe('indexing')
  })

  it('only the terminal idle phase may call a collection empty (E-1)', () => {
    const state = resolveKnowledgeFirstRunState(
      makeInfo(),
      makeProgress({ phase: 'idle', indexed_files: 0, total_known: true, total_files: 0 }),
    )
    expect(state.kind).toBe('empty_collection')
  })

  it('idle with files indexed is ready — the state that renders nothing', () => {
    const state = resolveKnowledgeFirstRunState(
      makeInfo(),
      makeProgress({ phase: 'idle', indexed_files: 4200, total_files: 4200 }),
    )
    expect(state.kind).toBe('ready')
  })

  it('a failed phase is surfaced, carrying the indexer\'s reason', () => {
    const state = resolveKnowledgeFirstRunState(
      makeInfo(),
      makeProgress({ phase: 'failed', indexed_files: 40, error: 'disk full' }),
    )
    expect(state).toMatchObject({ kind: 'index_failed', message: 'disk full', indexedFiles: 40 })
  })

  it('a failed phase with no reason still says something, never an empty message', () => {
    const state = resolveKnowledgeFirstRunState(
      makeInfo(),
      makeProgress({ phase: 'failed', indexed_files: 0, error: undefined }),
    )
    expect(state.kind).toBe('index_failed')
    expect(state.kind === 'index_failed' && state.message.length).toBeGreaterThan(0)
  })

  it('no progress frame → status UNKNOWN, never ready (absence of news is not news)', () => {
    const state = resolveKnowledgeFirstRunState(makeInfo(), undefined)
    expect(state.kind).toBe('index_status_unknown')
  })

  it("ignores a frame belonging to a DIFFERENT collection (FR-031)", () => {
    // Several collections are open in one app routinely. Letting another
    // collection's counts drive this pane produces a plausible wrong answer,
    // which is worse than no answer.
    const state = resolveKnowledgeFirstRunState(
      makeInfo(),
      makeProgress({ collection_id: 'kb_someone_else', phase: 'idle', indexed_files: 4200 }),
    )
    expect(state.kind).toBe('index_status_unknown')
  })

  it('carries indexing counts through unchanged, including the skip count (FR-112)', () => {
    const state = resolveKnowledgeFirstRunState(
      makeInfo(),
      makeProgress({ phase: 'indexing', indexed_files: 12, total_known: true, total_files: 500, skipped_files: 3 }),
    )
    expect(state).toMatchObject({
      kind: 'indexing',
      indexedFiles: 12,
      totalKnown: true,
      totalFiles: 500,
      skippedFiles: 3,
    })
  })

  it('does not invent a total when the indexer reports none', () => {
    const state = resolveKnowledgeFirstRunState(
      makeInfo(),
      makeProgress({ phase: 'enumerating', total_known: false, total_files: undefined }),
    )
    expect(state).toMatchObject({ kind: 'indexing', totalKnown: false })
    expect(state.kind === 'indexing' && state.totalFiles).toBeUndefined()
  })
})

describe('knowledgeDisplayName (FR-024)', () => {
  it('prefers the name recorded in the marker', () => {
    expect(knowledgeDisplayName(makeInfo({ display_name: 'Research vault' }))).toBe('Research vault')
  })

  it("falls back to the root folder's own name when the marker records none", () => {
    expect(
      knowledgeDisplayName(makeInfo({ display_name: undefined, root_path: 'notes/vault' })),
    ).toBe('vault')
  })

  it('falls back again rather than render an empty heading at the work-tree root', () => {
    expect(knowledgeDisplayName(makeInfo({ display_name: undefined, root_path: '' }))).toBe(
      'This collection',
    )
  })

  it('treats a whitespace-only marker name as absent', () => {
    expect(knowledgeDisplayName(makeInfo({ display_name: '   ', root_path: 'notes/vault' }))).toBe(
      'vault',
    )
  })
})

// ── The panel ─────────────────────────────────────────────────────────────────

describe('KnowledgePanel — the virtual root has no folder to test', () => {
  it('renders nothing, and asks nothing, when no workspace is open', () => {
    const loadInfo = vi.fn()
    const { container } = render(
      <QueryClientProvider client={makeClient()}>
        <KnowledgePanel workspaceId={null} loadInfo={loadInfo} />
      </QueryClientProvider>,
    )
    expect(container).toBeEmptyDOMElement()
    expect(loadInfo).not.toHaveBeenCalled()
  })
})

describe('KnowledgePanel — asking the contract endpoint (FR-020, FR-021)', () => {
  it('asks about the workspace and the folder path currently on screen', async () => {
    const { loadInfo } = renderPanel({ workspaceId: 'ws_7f3a', path: 'notes/vault' })
    await waitFor(() => expect(loadInfo).toHaveBeenCalledWith('ws_7f3a', 'notes/vault'))
  })

  it("sends the work-tree root as '' rather than omitting it — the contract requires the parameter", async () => {
    const { loadInfo } = renderPanel({ workspaceId: 'ws_7f3a', path: undefined })
    await waitFor(() => expect(loadInfo).toHaveBeenCalledWith('ws_7f3a', ''))
  })

  it('asks exactly once and does NOT poll for progress (FR-080)', async () => {
    // KnowledgeBaseInfo deliberately carries no counts. A panel that re-asked
    // on a timer to watch a number move is the polling loop that decision
    // exists to prevent.
    vi.useFakeTimers()
    try {
      const loadInfo = vi.fn().mockResolvedValue(makeInfo())
      render(
        <QueryClientProvider client={makeClient()}>
          <KnowledgePanel workspaceId="ws_7f3a" path="notes/vault" loadInfo={loadInfo} />
        </QueryClientProvider>,
      )
      await vi.advanceTimersByTimeAsync(120_000)
      expect(loadInfo).toHaveBeenCalledTimes(1)
    } finally {
      vi.useRealTimers()
    }
  })

  it('says it is checking while the answer is outstanding — not a blank pane', () => {
    renderPanel({ loadInfo: vi.fn().mockReturnValue(new Promise(() => {})) })
    expect(screen.getByTestId('knowledge-panel-checking')).toBeInTheDocument()
  })
})

describe('KnowledgePanel — a failed check is not an ordinary folder', () => {
  it('reports the failure and does NOT render the ordinary-folder answer', async () => {
    renderPanel({ loadInfo: vi.fn().mockRejectedValue(new Error('Network unavailable.')) })
    const block = await screen.findByTestId('knowledge-panel-error')
    expect(block).toHaveAttribute('role', 'alert')
    expect(screen.queryByTestId('knowledge-state-not-a-knowledge-base')).not.toBeInTheDocument()
    expect(block.textContent).toMatch(/the answer is unknown/i)
  })

  it("shows the failure's own reason", async () => {
    renderPanel({ loadInfo: vi.fn().mockRejectedValue(new Error('Network unavailable.')) })
    expect(await screen.findByTestId('knowledge-panel-error-reason')).toHaveTextContent(
      'Network unavailable.',
    )
  })

  it('offers a retry rather than leaving the reader stuck', async () => {
    renderPanel({ loadInfo: vi.fn().mockRejectedValue(new Error('boom')) })
    expect(await screen.findByTestId('knowledge-panel-retry')).toBeInTheDocument()
  })

  it('re-asks when that retry is used', async () => {
    const loadInfo = vi.fn().mockRejectedValue(new Error('boom'))
    renderPanel({ loadInfo })
    fireEvent.click(await screen.findByTestId('knowledge-panel-retry'))
    await waitFor(() => expect(loadInfo).toHaveBeenCalledTimes(2))
  })

  it('does not name itself "Retry" — the folder listing it sits above already owns that name', async () => {
    // This panel is mounted inside LibraryExplorer, directly above a listing
    // whose own failure state offers a button called "Retry". Two controls
    // with one accessible name on a single screen cannot be told apart by
    // anyone navigating by name.
    renderPanel({ loadInfo: vi.fn().mockRejectedValue(new Error('boom')) })
    const button = await screen.findByTestId('knowledge-panel-retry')
    expect(button).toHaveAccessibleName('Check again')
    expect(screen.queryByRole('button', { name: /retry/i })).not.toBeInTheDocument()
  })
})

describe('KnowledgePanel — the three first-run paths, end to end', () => {
  it('(a) an existing vault, still being counted: recognised, indeterminate, no invented total', async () => {
    renderPanel({
      loadInfo: vi.fn().mockResolvedValue(makeInfo()),
      progress: makeProgress({
        phase: 'enumerating',
        indexed_files: 340,
        total_known: false,
        total_files: undefined,
      }),
    })
    const block = await screen.findByTestId('knowledge-state-indexing')
    expect(block.textContent).toContain('Research vault')
    expect(screen.getByTestId('knowledge-index-progress-indeterminate')).toHaveTextContent('340')
    expect(screen.queryByTestId('knowledge-index-progress-ratio')).not.toBeInTheDocument()
    expect(block.textContent ?? '').not.toMatch(/\d[\d.,]*\s*%/)
    expect(screen.getByRole('progressbar')).not.toHaveAttribute('aria-valuenow')
  })

  it('(b) an ordinary folder: offers to create a collection', async () => {
    const onCreateCollection = vi.fn()
    renderPanel({
      loadInfo: vi
        .fn()
        .mockResolvedValue(
          makeInfo({ is_knowledge_base: false, marker: 'none', collection_id: undefined, display_name: undefined }),
        ),
      onCreateCollection,
    })
    expect(await screen.findByTestId('knowledge-state-not-a-knowledge-base')).toBeInTheDocument()
    expect(screen.getByTestId('knowledge-create-collection')).toBeInTheDocument()
  })

  it('(c) an empty collection: named as empty, with what to do next', async () => {
    renderPanel({
      loadInfo: vi.fn().mockResolvedValue(makeInfo()),
      progress: makeProgress({ phase: 'idle', indexed_files: 0, total_known: true, total_files: 0 }),
    })
    const block = await screen.findByTestId('knowledge-state-empty')
    expect(block.textContent).toContain('Research vault')
    expect(screen.queryByTestId('knowledge-state-indexing')).not.toBeInTheDocument()
  })

  it('(d) detection failed: said plainly, and never downgraded to ordinary (E-9)', async () => {
    renderPanel({
      loadInfo: vi.fn().mockResolvedValue(
        makeInfo({
          is_knowledge_base: false,
          marker: 'none',
          collection_id: undefined,
          detection_error: {
            code: 'marker_unreadable',
            message: 'cannot read notes/vault/.omnipus-vault: permission denied',
          },
        }),
      ),
    })
    expect(await screen.findByTestId('knowledge-state-detection-error')).toBeInTheDocument()
    expect(screen.queryByTestId('knowledge-state-not-a-knowledge-base')).not.toBeInTheDocument()
  })

  it('a finished index shows no banner at all (US-6 AS-4, AS-6)', async () => {
    renderPanel({
      loadInfo: vi.fn().mockResolvedValue(makeInfo()),
      progress: makeProgress({ phase: 'idle', indexed_files: 4200, total_files: 4200 }),
    })
    await waitFor(() => expect(screen.getByTestId('knowledge-panel')).toBeInTheDocument())
    expect(screen.queryByTestId('knowledge-state-indexing')).not.toBeInTheDocument()
    expect(screen.queryByTestId('knowledge-state-empty')).not.toBeInTheDocument()
    expect(screen.queryByTestId('knowledge-state-index-status-unknown')).not.toBeInTheDocument()
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument()
  })

  it('with no progress frame at all, says completeness is unknown', async () => {
    // Not "the current SPA gap" any more — the frame IS routed now (see the
    // live-store tests below). This is the honest state for a browser that
    // opened the Library between two lifecycle events and has genuinely heard
    // nothing: absence of news is not news.
    useKnowledgeIndexStore.setState({ byCollection: {} })
    renderPanel({ loadInfo: vi.fn().mockResolvedValue(makeInfo()), progress: undefined })
    expect(await screen.findByTestId('knowledge-state-index-status-unknown')).toBeInTheDocument()
  })
})

describe('KnowledgePanel — reads the live progress frame from the store (FR-080)', () => {
  // The frame was contract-defined, generated, validated and BROADCAST, and
  // routed nowhere: no case in handleFrame and no store. Every knowledge base
  // in the product therefore reported "no indexing progress received" forever,
  // and `phase: "idle"` — the state that satisfies US-6 AS-4 and AS-6 — was
  // unreachable. These two tests are the panel end of that wire.

  beforeEach(() => {
    useKnowledgeIndexStore.setState({ byCollection: {} })
  })

  it('goes quiet once the indexer reports idle with a non-empty index (US-6 AS-4/AS-6)', async () => {
    // DIES ON: dropping the store subscription from KnowledgePanel — the panel
    // falls back to index_status_unknown and the banner never goes away.
    useKnowledgeIndexStore.getState().apply(
      makeProgress({ phase: 'idle', indexed_files: 4200, total_known: true, total_files: 4200 }),
    )
    renderPanel({ loadInfo: vi.fn().mockResolvedValue(makeInfo()), searchFn: noSearch })

    await waitFor(() => expect(screen.getByTestId('knowledge-search')).toBeInTheDocument())
    expect(screen.queryByTestId('knowledge-state-index-status-unknown')).not.toBeInTheDocument()
    expect(screen.queryByTestId('knowledge-state-empty')).not.toBeInTheDocument()
  })

  it('calls an idle, zero-file collection EMPTY — the road E-1 needs (E-1, US-6 AS-5)', async () => {
    // This is the second road to "this collection has no notes", and the only
    // one that exists against the real gateway: a COMPLETE search answer never
    // carries total_files, so the search box cannot tell an empty collection
    // from a query that matched nothing. The indexer's own terminal frame can.
    useKnowledgeIndexStore.getState().apply(
      makeProgress({ phase: 'idle', indexed_files: 0, total_known: true, total_files: 0 }),
    )
    renderPanel({ loadInfo: vi.fn().mockResolvedValue(makeInfo()), searchFn: noSearch })

    expect(await screen.findByTestId('knowledge-state-empty')).toBeInTheDocument()
  })

  it('ignores a frame belonging to a DIFFERENT collection', async () => {
    // A plausible wrong answer is the worst kind. Two collections are open in
    // one app all the time.
    useKnowledgeIndexStore.getState().apply(
      makeProgress({ collection_id: 'kb_somebody_else', phase: 'idle', indexed_files: 4200 }),
    )
    renderPanel({ loadInfo: vi.fn().mockResolvedValue(makeInfo()), searchFn: noSearch })

    expect(await screen.findByTestId('knowledge-state-index-status-unknown')).toBeInTheDocument()
  })
})

describe('KnowledgePanel — composition (US-4: features on for the right folders, off elsewhere)', () => {
  const child = <div data-testid="kb-surface-child">caller-supplied surface</div>

  it('renders the knowledge surface — search included — for a real collection', async () => {
    renderPanel({ loadInfo: vi.fn().mockResolvedValue(makeInfo()), children: child, searchFn: noSearch })
    expect(await screen.findByTestId('knowledge-panel-surface')).toBeInTheDocument()
    // Composed, not duplicated: KnowledgeSearch is the real component.
    expect(screen.getByTestId('knowledge-search')).toBeInTheDocument()
    expect(screen.getByTestId('kb-surface-child')).toBeInTheDocument()
  })

  it('renders it while indexing too — a partial index is still searchable (US-5 AS-5)', async () => {
    renderPanel({
      loadInfo: vi.fn().mockResolvedValue(makeInfo()),
      progress: makeProgress({ phase: 'indexing' }),
      children: child,
      searchFn: noSearch,
    })
    expect(await screen.findByTestId('knowledge-panel-surface')).toBeInTheDocument()
    expect(screen.getByTestId('knowledge-state-indexing')).toBeInTheDocument()
  })

  it('does NOT render it for an ordinary folder, and offers to create one when it can (US-4 AS-3)', async () => {
    renderPanel({
      loadInfo: vi
        .fn()
        .mockResolvedValue(makeInfo({ is_knowledge_base: false, marker: 'none', collection_id: undefined })),
      children: child,
      searchFn: noSearch,
      onCreateCollection: vi.fn(),
    })
    await screen.findByTestId('knowledge-state-not-a-knowledge-base')
    expect(screen.queryByTestId('knowledge-panel-surface')).not.toBeInTheDocument()
    expect(screen.queryByTestId('knowledge-search')).not.toBeInTheDocument()
    expect(screen.queryByTestId('kb-surface-child')).not.toBeInTheDocument()
  })

  it('renders NOTHING AT ALL for an ordinary folder when there is no collection to create', async () => {
    // US-4 AS-3 taken literally. Almost every folder in the Library is an
    // ordinary one, and this panel sits above the listing of whichever folder
    // is open — so a card explaining that an unavailable feature is switched
    // off would be permanent furniture on every screen in the product, and
    // would train the reader to ignore the one place a real warning appears.
    //
    // DIES ON: dropping the `not_a_knowledge_base && !onCreateCollection` early
    // return from KnowledgePanel, which leaves an empty padded wrapper behind
    // even once KnowledgeEmptyState renders null.
    const { container } = renderPanel({
      loadInfo: vi
        .fn()
        .mockResolvedValue(makeInfo({ is_knowledge_base: false, marker: 'none', collection_id: undefined })),
      children: child,
      searchFn: noSearch,
    })
    await waitFor(() =>
      expect(screen.queryByTestId('knowledge-panel-checking')).not.toBeInTheDocument(),
    )
    expect(screen.queryByTestId('knowledge-state-not-a-knowledge-base')).not.toBeInTheDocument()
    expect(screen.queryByTestId('knowledge-panel')).not.toBeInTheDocument()
    expect(container).toBeEmptyDOMElement()
  })

  it('does NOT render it when detection failed — an unknown verdict switches nothing on (E-9)', async () => {
    renderPanel({
      loadInfo: vi.fn().mockResolvedValue(
        makeInfo({ detection_error: { code: 'marker_unreadable', message: 'permission denied' } }),
      ),
      children: child,
      searchFn: noSearch,
    })
    await screen.findByTestId('knowledge-state-detection-error')
    expect(screen.queryByTestId('knowledge-panel-surface')).not.toBeInTheDocument()
    expect(screen.queryByTestId('kb-surface-child')).not.toBeInTheDocument()
  })
})

describe('KnowledgePanel — opening a search hit lands on a Library address (FR-012)', () => {
  // Search hits are COLLECTION-relative (the contract's KnowledgeSearchHit.path
  // is a path inside the collection); the Library address is WORKSPACE-relative
  // (LibraryAddress.path). Handing a collection-relative path straight to the
  // Library opens the wrong file, or nothing — and it fails silently, because
  // both are plausible-looking relative paths.
  const hit = {
    path: 'topics/landlock.md',
    title: 'Landlock',
    score: 1,
    kind: 'note' as const,
    excerpt: 'kernel sandbox',
  }
  const searchFn: KnowledgeSearchFn = vi.fn().mockResolvedValue({
    collection_id: COLLECTION_ID,
    hits: [hit],
    incompleteness: { complete: true, total_known: true, statement: 'Search covered every note.' },
    limit_applied: 20,
    limit_clamped: false,
  })

  it("joins the hit onto the collection's own root before handing it to the caller", async () => {
    const onOpenNote = vi.fn()
    renderPanel({
      loadInfo: vi.fn().mockResolvedValue(makeInfo({ root_path: 'notes/vault' })),
      onOpenNote,
      searchFn,
    })
    const box = await screen.findByTestId('knowledge-search')
    fireEvent.change(within(box).getByRole('searchbox'), { target: { value: 'landlock' } })
    const link = await screen.findByText('Landlock')
    fireEvent.click(link)
    await waitFor(() =>
      expect(onOpenNote).toHaveBeenCalledWith('notes/vault/topics/landlock.md'),
    )
  })

  it('leaves the path alone when the collection IS the workspace root', async () => {
    const onOpenNote = vi.fn()
    renderPanel({
      loadInfo: vi.fn().mockResolvedValue(makeInfo({ root_path: '' })),
      path: '',
      onOpenNote,
      searchFn,
    })
    const box = await screen.findByTestId('knowledge-search')
    fireEvent.change(within(box).getByRole('searchbox'), { target: { value: 'landlock' } })
    const link = await screen.findByText('Landlock')
    fireEvent.click(link)
    await waitFor(() => expect(onOpenNote).toHaveBeenCalledWith('topics/landlock.md'))
  })
})
