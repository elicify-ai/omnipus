// knowledgeMarkdown.dashboards.test.tsx — ADR-083 Step 2: a `.base` embed
// that stands alone in its paragraph renders LIVE saved-view data, not a
// link (US-5, EMB-040 through EMB-046).
//
// This is a SEPARATE file from knowledgeMarkdown.test.tsx on purpose: that
// file globally stubs `@tanstack/react-query`'s useQuery to `{ data: null }`
// (its whole suite never needed real query behaviour before this work), and
// the components under test here — KbBaseEmbedMount, reached through
// KnowledgeBaseMarkdown — need REAL react-query state to prove anything.
//
// react-markdown, remark's real parser and every KB plugin stay REAL, same
// discipline as knowledgeMarkdown.test.tsx. Only the network boundary
// (`@/lib/api`) and Shiki/Mermaid/ChatImage are mocked.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { KnowledgeBaseMarkdown, type EmbedResolution } from './knowledgeMarkdown'
import type { KnowledgeBaseViews, ViewResult, LibraryEntry as GenLibraryEntry } from '@/lib/api/generated/openapi-types'

vi.mock('@/components/chat/mermaid-renderer', () => ({
  MermaidDiagram: ({ code }: { code: string }) => <div data-testid="mermaid-diagram">{code}</div>,
}))
vi.mock('react-shiki', () => ({
  ShikiHighlighter: ({ children }: { children?: React.ReactNode }) => <pre data-testid="shiki">{children}</pre>,
  createJavaScriptRegexEngine: () => ({}),
}))
vi.mock('@/components/chat/ChatImage', () => ({
  ChatImage: ({ src, alt }: { src: string; alt?: string }) => <img data-testid="chat-image" src={src} alt={alt} />,
}))
vi.mock('@/store/ui', () => ({ useUiStore: { getState: () => ({ addToast: vi.fn() }) } }))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchLibraryEntries: vi.fn(),
    fetchKnowledgeBaseViews: vi.fn(),
    fetchKnowledgeViewResult: vi.fn(),
  }
})

import { fetchLibraryEntries, fetchKnowledgeBaseViews, fetchKnowledgeViewResult } from '@/lib/api'

const WORKSPACE = 'ws-1'
const BASE_WORKSPACE_PATH = 'vault/dashboards/Tasks.base'

function entries(): GenLibraryEntry[] {
  return [
    {
      name: 'Tasks.base',
      path: BASE_WORKSPACE_PATH,
      is_dir: false,
      is_hidden: false,
      size: 10,
      modified_at: '2026-09-01T00:00:00Z',
      is_text_editable: true,
    },
  ]
}

function views(over: Partial<KnowledgeBaseViews> = {}): KnowledgeBaseViews {
  return {
    base_path: BASE_WORKSPACE_PATH,
    is_knowledge_base: true,
    collection_id: 'kb_1',
    collection_root: 'vault',
    source: 'dashboards/Tasks.base',
    views: [
      { name: 'tasks--needs-daniel', label: 'Needs Daniel' },
      { name: 'tasks--awaiting-founder', label: 'Awaiting founder' },
    ],
    unloadable_count: 0,
    ...over,
  }
}

function resultFor(view: string): ViewResult {
  if (view === 'tasks--needs-daniel') {
    return {
      view,
      label: 'Needs Daniel',
      parts: [{ part: 'table', source: { part: 'table' }, columns: ['file.name'] }],
      rows: [{ path: 'a.md', title: 'Needs-Daniel-Row', cells: [], joins: [] }],
      complete: true,
      problems: [],
    }
  }
  return {
    view,
    label: 'Awaiting founder',
    parts: [{ part: 'table', source: { part: 'table' }, columns: ['file.name'] }],
    rows: [{ path: 'b.md', title: 'Awaiting-Founder-Row', cells: [], joins: [] }],
    complete: true,
    problems: [],
  }
}

/** A resolveEmbedUrl that resolves ANY `.base` target this suite writes to
 *  the fixed fixture file above — the client-side resolver's own job
 *  (KnowledgeNoteView) is covered separately; this suite is about what
 *  happens ONCE an embed is told it resolved. */
function resolvedBaseResolver(): EmbedResolution {
  return {
    state: 'resolved',
    path: 'dashboards/Tasks.base',
    url: '/api/v1/library/ws-1/download?path=vault%2Fdashboards%2FTasks.base',
    workspaceId: WORKSPACE,
    workspacePath: BASE_WORKSPACE_PATH,
  }
}

function renderNote(content: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <KnowledgeBaseMarkdown content={content} notePath="dashboards/Cockpit.md" resolveEmbedUrl={resolvedBaseResolver} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  // No global clearMocks/restoreMocks is configured (vite.config.ts) — call
  // counts persist across tests in this file unless reset here, which the
  // view-list DEDUPLICATION test below depends on being exact.
  vi.clearAllMocks()
  vi.mocked(fetchLibraryEntries).mockResolvedValue(entries())
  vi.mocked(fetchKnowledgeBaseViews).mockResolvedValue(views())
  vi.mocked(fetchKnowledgeViewResult).mockImplementation((_ws, _cid, view) => Promise.resolve(resultFor(view)))
})

describe('a standalone `.base` embed renders live view data (US-5 AS-1)', () => {
  it('shows the NAMED view’s own rows, drawn by BasePreview — not a link', async () => {
    renderNote('![[Tasks.base#Needs Daniel]]')
    await waitFor(() => expect(screen.getByText('Needs-Daniel-Row')).toBeInTheDocument())
    expect(screen.getByTestId('base-preview')).toBeInTheDocument()
    // Never the link-fallback badge — this is the mounted renderer, not the
    // "embed shown as a link" treatment.
    expect(screen.queryByText('embed shown as a link')).not.toBeInTheDocument()
  })
})

describe('two embeds of one data file under different view names each show their own (US-5 AS-2)', () => {
  it('does not cross — the first embed never shows the second view’s row and vice versa', async () => {
    renderNote('![[Tasks.base#Needs Daniel]]\n\n![[Tasks.base#Awaiting founder]]')
    await waitFor(() => {
      expect(screen.getByText('Needs-Daniel-Row')).toBeInTheDocument()
      expect(screen.getByText('Awaiting-Founder-Row')).toBeInTheDocument()
    })
    const mounts = screen.getAllByTestId('base-preview')
    expect(mounts).toHaveLength(2)
  })
})

describe('no fragment named — first view shown, with a caption, no tabs (US-5 AS-4, EMB-043)', () => {
  it('renders the first view and states that the embed did not choose it', async () => {
    renderNote('![[Tasks.base]]')
    await waitFor(() => expect(screen.getByText('Needs-Daniel-Row')).toBeInTheDocument())
    expect(screen.getByTestId('base-preview-embed-caption').textContent).toMatch(/did not choose one/)
    expect(screen.queryByTestId('base-preview-tablist')).not.toBeInTheDocument()
  })
})

describe('duplicate view labels are refused, not guessed (US-5 AS-5, EMB-042)', () => {
  it('names both matching views and never mounts BasePreview', async () => {
    vi.mocked(fetchKnowledgeBaseViews).mockResolvedValue(
      views({
        views: [
          { name: 'crm--open-a', label: 'Open' },
          { name: 'crm--open-b', label: 'Open' },
        ],
      }),
    )
    renderNote('![[Tasks.base#Open]]')
    await waitFor(() => expect(screen.getByTestId('kb-base-embed-ambiguous-view')).toBeInTheDocument())
    const text = screen.getByTestId('kb-base-embed-ambiguous-view').textContent ?? ''
    expect(text).toContain('crm--open-a')
    expect(text).toContain('crm--open-b')
    expect(screen.queryByTestId('base-preview')).not.toBeInTheDocument()
  })
})

describe('a view label that does not exist lists what does (EMB-016)', () => {
  it('names the missing label and every label that DOES exist, without mounting BasePreview', async () => {
    renderNote('![[Tasks.base#Nonexistent]]')
    await waitFor(() => expect(screen.getByTestId('kb-base-embed-missing-view')).toBeInTheDocument())
    const text = screen.getByTestId('kb-base-embed-missing-view').textContent ?? ''
    expect(text).toContain('Nonexistent')
    expect(text).toContain('Needs Daniel')
    expect(text).toContain('Awaiting founder')
    expect(screen.queryByTestId('base-preview')).not.toBeInTheDocument()
  })
})

describe('a broken .base file says so, distinctly from "no view is named that" (EMB-028)', () => {
  // The trap: a test asserting only "some warning renders" would pass even
  // with the wrong sentence shown. Every assertion below checks the EXACT
  // testid/copy this state must produce, distinct from the sibling
  // `kb-base-embed-missing-view` state — and is paired with a control using
  // the identical empty-views fixture but unloadable_count back at zero, so
  // the pair proves the branch reads unloadable_count rather than merely
  // "views is empty".

  it('says the view could not be loaded when the server reports the one view in the file failed to parse', async () => {
    vi.mocked(fetchKnowledgeBaseViews).mockResolvedValue(views({ views: [], unloadable_count: 1 }))
    renderNote('![[Tasks.base#Needs Daniel]]')
    await waitFor(() => expect(screen.getByTestId('kb-base-embed-unloadable-views')).toBeInTheDocument())
    const text = screen.getByTestId('kb-base-embed-unloadable-views').textContent ?? ''
    expect(text).toMatch(/could not be loaded/i)
    expect(text).not.toContain('No view named')
    expect(screen.queryByTestId('kb-base-embed-missing-view')).not.toBeInTheDocument()
    expect(screen.queryByTestId('base-preview')).not.toBeInTheDocument()
  })

  it('pluralises correctly and names the count for more than one unloadable view', async () => {
    vi.mocked(fetchKnowledgeBaseViews).mockResolvedValue(views({ views: [], unloadable_count: 3 }))
    renderNote('![[Tasks.base#Needs Daniel]]')
    await waitFor(() => expect(screen.getByTestId('kb-base-embed-unloadable-views')).toBeInTheDocument())
    expect(screen.getByTestId('kb-base-embed-unloadable-views').textContent).toContain('All 3 views')
  })

  it('reports the unloadable state even when the embed named no view fragment at all', async () => {
    vi.mocked(fetchKnowledgeBaseViews).mockResolvedValue(views({ views: [], unloadable_count: 1 }))
    renderNote('![[Tasks.base]]')
    await waitFor(() => expect(screen.getByTestId('kb-base-embed-unloadable-views')).toBeInTheDocument())
    expect(screen.queryByTestId('kb-base-embed-missing-view')).not.toBeInTheDocument()
  })

  // The paired control: the SAME empty-views fixture, with unloadable_count
  // back at zero, must still produce the ORIGINAL "no view named" answer —
  // an honest "zero views were ever imported", not a load failure.
  it('keeps the ORIGINAL "no view named" answer when the views list is empty for an honest reason (zero imported, not failed)', async () => {
    vi.mocked(fetchKnowledgeBaseViews).mockResolvedValue(views({ views: [], unloadable_count: 0 }))
    renderNote('![[Tasks.base#Needs Daniel]]')
    await waitFor(() => expect(screen.getByTestId('kb-base-embed-missing-view')).toBeInTheDocument())
    expect(screen.queryByTestId('kb-base-embed-unloadable-views')).not.toBeInTheDocument()
    expect(screen.getByTestId('kb-base-embed-missing-view').textContent).toContain('No view named')
  })
})

// UAT U-32 (retest validation, S3 — "wrong error message"): a note embedding
// `![[Projects.base#Active Projects]]` said "No view named 'Active
// Projects'" even though the base-views oracle showed a view BY THAT NAME
// existed and had merely failed to load (a property rename, `priority`
// dropped from the `project` schema) — the same base-preview screen's own
// banner named the same broken view correctly. `matchBaseView` only ever
// sees the LOADED `views` list (EMB-028's own doc, above), so a fragment
// matching a view that exists ONLY in `unloadable` fell all the way through
// to the generic "does not exist" answer — true of the loaded views, false
// of the file as a whole.
//
// This distinguishes the two cases by the view's declared machine NAME —
// the one identifier `KnowledgeBaseUnloadableView` (contracts/components/
// schemas/KnowledgeBaseUnloadableView.yaml) actually carries for a rejected
// view (`pkg/records/view.go`'s `ViewRejection.Name`, always the `name:`
// key, never the `label:` one — confirmed by reading that struct and its
// two construction sites, neither of which captures `v.Def.Label`/
// `v.DisplayLabel()` even though it is already in hand at rejection time).
// A fragment that names a broken view by its LABEL only (the exact U-32
// repro) cannot be resolved from the wire data this endpoint sends today —
// that gap needs a `label` field added to the wire type and to
// `ViewRejection`, which is a contract + backend change outside this
// branch's scope; recorded, not silently worked around here.
describe('a view that failed to load is distinguished from one that was never declared (UAT U-32, S3)', () => {
  const unloadableActiveProjects = {
    name: 'tasks--active-projects',
    paths: ['.omnipus-vault/views/tasks--active-projects.yaml'],
    code: 'view_unknown_property',
    reason:
      'view "tasks--active-projects" names property "priority" in properties, which record type "project" does not declare; declared: budget, owner, start, status',
  }

  it('names the load-failure reason when the fragment matches a declared-but-unloadable view by its machine name', async () => {
    vi.mocked(fetchKnowledgeBaseViews).mockResolvedValue(
      views({ unloadable_count: 1, unloadable: [unloadableActiveProjects] }),
    )
    renderNote('![[Tasks.base#tasks--active-projects]]')
    // DIES ON the old code: falls through to `kb-base-embed-missing-view`
    // with "No view named" — the view exists, it just could not be loaded.
    await waitFor(() => expect(screen.getByTestId('kb-base-embed-view-load-failed')).toBeInTheDocument())
    const text = screen.getByTestId('kb-base-embed-view-load-failed').textContent ?? ''
    expect(text).toContain('tasks--active-projects')
    expect(text).toMatch(/priority/)
    expect(text).not.toContain('No view named')
    expect(screen.queryByTestId('kb-base-embed-missing-view')).not.toBeInTheDocument()
    expect(screen.queryByTestId('base-preview')).not.toBeInTheDocument()
  })

  it('matches case-insensitively, the same tolerance the loaded-view ladder already gives labels', async () => {
    vi.mocked(fetchKnowledgeBaseViews).mockResolvedValue(
      views({ unloadable_count: 1, unloadable: [unloadableActiveProjects] }),
    )
    renderNote('![[Tasks.base#TASKS--ACTIVE-PROJECTS]]')
    await waitFor(() => expect(screen.getByTestId('kb-base-embed-view-load-failed')).toBeInTheDocument())
  })

  it('a fragment matching NEITHER a loaded view NOR an unloadable one keeps the ORIGINAL "No view named" answer', async () => {
    vi.mocked(fetchKnowledgeBaseViews).mockResolvedValue(
      views({ unloadable_count: 1, unloadable: [unloadableActiveProjects] }),
    )
    renderNote('![[Tasks.base#Totally Different View]]')
    await waitFor(() => expect(screen.getByTestId('kb-base-embed-missing-view')).toBeInTheDocument())
    expect(screen.getByTestId('kb-base-embed-missing-view').textContent).toContain('No view named')
    expect(screen.queryByTestId('kb-base-embed-view-load-failed')).not.toBeInTheDocument()
  })
})

describe('an embed mixed inline with other text is NOT promoted to a live view (block-promotion gate)', () => {
  // The critical regression test for this phase: delete the standalone check
  // in remarkKbPromoteBlockEmbeds and EVERY resolved `.base` embed — inline
  // or not — would mount BasePreview, including this one.
  it('keeps the existing link-fallback treatment when other words share its paragraph', async () => {
    renderNote('See ![[Tasks.base#Needs Daniel]] for details.')
    await waitFor(() => expect(screen.getByTestId('markdown-link')).toBeInTheDocument())
    expect(screen.queryByTestId('base-preview')).not.toBeInTheDocument()
    expect(screen.queryByText('Needs-Daniel-Row')).not.toBeInTheDocument()
  })
})

describe('N embeds of one data file share ONE view-list request (EMB-045)', () => {
  it('fetches the base-views answer exactly once for three embeds of the same file', async () => {
    renderNote(
      '![[Tasks.base#Needs Daniel]]\n\n![[Tasks.base#Awaiting founder]]\n\n![[Tasks.base#Needs Daniel]]',
    )
    await waitFor(() => {
      expect(screen.getAllByTestId('base-preview')).toHaveLength(3)
    })
    expect(vi.mocked(fetchKnowledgeBaseViews)).toHaveBeenCalledTimes(1)
  })
})

describe('a dashboard embed below the fold issues NO request at all until it nears the viewport (EMB-065, wiring LazyEmbedMount)', () => {
  // A stub IntersectionObserver whose callback never fires — LazyEmbedMount's
  // own `mounted` state (LazyEmbedMount.tsx) starts false whenever
  // IntersectionObserver exists at all, and only flips true on an
  // intersection event this stub never sends. This is the regression this
  // phase's own review caught: an earlier draft resolved the view (fetched
  // entries + base-views) BEFORE handing off to LazyEmbedMount, so a
  // forty-embed dashboard fired forty requests on first paint regardless of
  // scroll position. Wrapping the WHOLE resolving component — not just the
  // final BasePreview render — is what this test proves.
  class NeverIntersectingObserver {
    constructor() {}
    observe() {}
    unobserve() {}
    disconnect() {}
    takeRecords() {
      return []
    }
  }

  it('reserves space and never calls fetchLibraryEntries/fetchKnowledgeBaseViews while unmounted', () => {
    vi.stubGlobal('IntersectionObserver', NeverIntersectingObserver)
    try {
      renderNote('![[Tasks.base#Needs Daniel]]')
      const mount = screen.getByTestId('lazy-embed-mount')
      expect(mount.getAttribute('data-mounted')).toBe('false')
      expect(screen.queryByTestId('base-preview')).not.toBeInTheDocument()
      expect(vi.mocked(fetchLibraryEntries)).not.toHaveBeenCalled()
      expect(vi.mocked(fetchKnowledgeBaseViews)).not.toHaveBeenCalled()
    } finally {
      vi.unstubAllGlobals()
    }
  })
})
