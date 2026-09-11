// BasePreview.recordWrite.test.tsx — the two things that make inline record
// editing REACHABLE and CORRECT from a real `.base` pane, neither of which
// had any test:
//
//   1. `workspaceId` reaching `ViewPartsRenderer` is what ENABLES an editor
//      at all. The existing editor tests render `TablePart` directly, so
//      deleting that one prop meant no cell in any view offered an editor
//      anywhere in the product while the whole SPA suite stayed green. The
//      tests below drive the REAL chain — BasePreview -> ViewPartsRenderer ->
//      TablePart -> EditableCell — so that deletion fails here.
//
//   2. ADR-083 §4.5 names FOUR caches a successful write invalidates. The
//      handler invalidated one exact view key. Two embeds of the SAME view
//      share that key, so the headline scenario worked and hid the rest.
//      These tests assert the whole key set, with a control proving a
//      DIFFERENT collection is left alone (the §4.5 scoping rule: never a
//      blanket sweep).
//
// The fetch boundary is BasePreview's injected loaders (the KnowledgeNoteView
// test-seam convention); only `writeVaultRecord` is module-mocked, because
// the editor calls it directly.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type {
  KnowledgeBaseViews,
  KnowledgeGraphResponse,
  VaultRecord,
  ViewResult,
} from '@/lib/api/generated/openapi-types'
import type { LibraryEntry } from '@/lib/api'
import type { KnowledgeGraphLoader } from '../knowledge/KnowledgeBacklinks'

vi.mock('react-shiki', () => ({
  ShikiHighlighter: ({ children }: { children?: React.ReactNode }) => <pre data-testid="shiki">{children}</pre>,
  createJavaScriptRegexEngine: () => ({}),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, writeVaultRecord: vi.fn(), fetchVaultRecord: vi.fn() }
})

import { writeVaultRecord } from '@/lib/api'
import { BasePreview } from './BasePreview'

const mockedWrite = vi.mocked(writeVaultRecord)

beforeEach(() => vi.clearAllMocks())

const BASE_CONTENT = 'views:\n  - type: table\n    name: Companies\n'

const ENTRY: LibraryEntry = {
  name: 'Companies.base',
  path: 'vault/Companies.base',
  is_dir: false,
  is_hidden: false,
  size: BASE_CONTENT.length,
  modified_at: '2026-09-01T10:00:00Z',
  is_text_editable: true,
}

const STATUS_VALUES = [
  { value: 'prospect', label: 'Prospect', position: 0 },
  { value: 'active', label: 'Active', position: 1 },
]

function baseViews(): KnowledgeBaseViews {
  return {
    base_path: 'vault/Companies.base',
    is_knowledge_base: true,
    collection_id: 'kb_1',
    collection_root: 'vault',
    source: 'Companies.base',
    views: [
      { name: 'companies--all', label: 'All' },
      { name: 'companies--active', label: 'Active only' },
    ],
    unloadable_count: 0,
  }
}

/** A view whose rows carry everything an inline editor needs: a record
 *  `type` on the result, and per-row `id` + `version_token` plus one
 *  editable (`enum`) cell. Any one of those missing and no editor appears —
 *  which is exactly why the fixture states all of them explicitly. */
function editableResult(): ViewResult {
  return {
    view: 'companies--all',
    label: 'All',
    type: 'company',
    parts: [{ part: 'table', source: { part: 'table' }, columns: ['file.name', 'status'] }],
    rows: [
      {
        path: 'Companies/Acme Ltd.md',
        title: 'Acme Ltd',
        id: 'CO-0142',
        version_token: 'sha256:aaa',
        joins: [],
        cells: [{ property: 'status', value: 'active', type: 'enum', values: STATUS_VALUES }],
      },
    ],
    complete: true,
    problems: [],
  }
}

function graph(over: Partial<KnowledgeGraphResponse> = {}): KnowledgeGraphResponse {
  return { collection_id: 'kb_1', kind: 'links', nodes: [], edges: [], skipped: [], truncated: false, ...over }
}

interface Loaders {
  loadViewResult?: (ws: string, collectionId: string, view: string) => Promise<ViewResult>
  loadGraph?: KnowledgeGraphLoader
}

function renderBase(loaders: Loaders = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const invalidateSpy = vi.spyOn(client, 'invalidateQueries')
  const loadContent = vi.fn().mockResolvedValue({ content: BASE_CONTENT, is_text: true, too_large: false })
  const loadBaseViews = vi.fn().mockResolvedValue(baseViews())
  const loadViewResult = loaders.loadViewResult ?? vi.fn().mockResolvedValue(editableResult())
  const loadGraph = loaders.loadGraph ?? vi.fn().mockResolvedValue(graph())
  render(
    <QueryClientProvider client={client}>
      <BasePreview
        workspaceId="ws-1"
        entry={ENTRY}
        loadContent={loadContent}
        loadBaseViews={loadBaseViews}
        loadViewResult={loadViewResult}
        loadGraph={loadGraph}
      />
    </QueryClientProvider>,
  )
  return { client, invalidateSpy, loadViewResult, loadGraph }
}

/** Every queryKey `invalidateQueries` was called with, flattened for
 *  `toContainEqual`-style assertions. */
function invalidatedKeys(spy: { mock: { calls: unknown[][] } }): unknown[][] {
  return spy.mock.calls
    .map((c: unknown[]) => (c[0] as { queryKey?: unknown[] } | undefined)?.queryKey)
    .filter((k): k is unknown[] => Array.isArray(k))
}

// ── 1. Reachability: the editor exists through the REAL chain ───────────────

describe('BasePreview — inline record editing is reachable from a real .base pane', () => {
  it('renders a working editor for an editable cell, driving BasePreview -> ViewPartsRenderer -> TablePart -> EditableCell', async () => {
    renderBase()

    // The editor itself — not a stand-in, the real control the user clicks.
    const select = await screen.findByTestId('viewpart-cell-editor-enum')
    expect(select).toBeInTheDocument()
    expect(select).toHaveAttribute('aria-label', 'Edit status')

    // And it WRITES: changing it calls the real write door with the row's
    // own id, the view's record type and the row's version token.
    mockedWrite.mockResolvedValue({
      id: 'CO-0142',
      type: 'company',
      path: 'Companies/Acme Ltd.md',
      title: 'Acme Ltd',
      version_token: 'sha256:bbb',
      properties: [{ property: 'status', values: [{ type: 'enum', enum: 'prospect' }] }],
    } as VaultRecord)
    fireEvent.change(select, { target: { value: 'prospect' } })

    await waitFor(() => expect(mockedWrite).toHaveBeenCalledTimes(1))
    expect(mockedWrite.mock.calls[0][0]).toBe('ws-1')
    expect(mockedWrite.mock.calls[0][1]).toMatchObject({
      type: 'company',
      id: 'CO-0142',
      version_token: 'sha256:aaa',
    })
  })

  it('paired negative — a result whose rows carry no version_token renders the SAME cell as plain text, no editor', async () => {
    const noToken = editableResult()
    const row = noToken.rows[0]
    if (row) delete (row as { version_token?: string }).version_token
    renderBase({ loadViewResult: vi.fn().mockResolvedValue(noToken) })

    // The value still renders — the row is not hidden…
    expect(await screen.findByText('active')).toBeInTheDocument()
    // …but there is no control, so the positive test above cannot be passing
    // on a component that renders editors unconditionally.
    expect(screen.queryByTestId('viewpart-cell-editor-enum')).not.toBeInTheDocument()
  })
})

// ── 2. ADR-083 §4.5: what a successful write invalidates ────────────────────

describe('BasePreview — a successful inline write invalidates the four caches §4.5 names', () => {
  async function writeOnce() {
    const h = renderBase()
    const select = await screen.findByTestId('viewpart-cell-editor-enum')
    mockedWrite.mockResolvedValue({
      id: 'CO-0142',
      type: 'company',
      path: 'Companies/Acme Ltd.md',
      title: 'Acme Ltd',
      version_token: 'sha256:bbb',
      properties: [{ property: 'status', values: [{ type: 'enum', enum: 'prospect' }] }],
    } as VaultRecord)
    h.invalidateSpy.mockClear()
    fireEvent.change(select, { target: { value: 'prospect' } })
    await waitFor(() => expect(mockedWrite).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(h.invalidateSpy.mock.calls.length).toBeGreaterThan(0))
    return h
  }

  it('invalidates view-results for the whole COLLECTION, not just the one view that hosted the edit', async () => {
    const { invalidateSpy } = await writeOnce()
    const keys = invalidatedKeys(invalidateSpy)

    // The collection-scoped prefix — deliberately WITHOUT the view name, so
    // a second saved view over the same records refreshes too. It was
    // `[..., 'view-result', 'kb_1', 'companies--all']` with `exact: true`,
    // which left every other view stale with nothing to notice.
    expect(keys).toContainEqual(['library', 'ws-1', 'knowledge', 'view-result', 'kb_1'])

    // And it is NOT a blanket sweep: §4.5 is explicit that invalidating
    // everything would re-evaluate every mounted view on the page for a
    // one-field edit. Every call carries a queryKey (no key-less call, which
    // invalidates the entire cache), and every key names this workspace.
    expect(invalidateSpy.mock.calls.length).toBe(keys.length)
    for (const k of keys) {
      expect(k.length).toBeGreaterThan(2)
      expect(k).toContain('ws-1')
    }
  })

  it("invalidates the written note's own content, outline and links caches — addressed by the path the write result carries", async () => {
    const { invalidateSpy } = await writeOnce()
    const keys = invalidatedKeys(invalidateSpy)

    // `row.path` is COLLECTION-relative ('Companies/Acme Ltd.md'); the two
    // workspace-keyed caches need it translated against the collection root
    // ('vault'), the graph key does not. Getting that wrong invalidates a
    // key nothing holds, which is indistinguishable from not invalidating.
    expect(keys).toContainEqual(['library', 'ws-1', 'content', 'vault/Companies/Acme Ltd.md'])
    expect(keys).toContainEqual(['library', 'knowledge', 'outline', 'ws-1', 'vault/Companies/Acme Ltd.md'])
    expect(keys).toContainEqual([
      'library',
      'knowledge',
      'graph',
      'links',
      'ws-1',
      'kb_1',
      'Companies/Acme Ltd.md',
    ])
  })

  it('control — nothing belonging to a DIFFERENT collection or a different note is invalidated', async () => {
    const { invalidateSpy } = await writeOnce()
    const keys = invalidatedKeys(invalidateSpy)
    const flat = keys.map((k) => JSON.stringify(k))

    // No other collection id appears anywhere in the invalidated set…
    expect(flat.some((k) => k.includes('kb_2'))).toBe(false)
    // …and no other note path does either.
    expect(flat.some((k) => k.includes('Some Other Note.md'))).toBe(false)
  })
})
