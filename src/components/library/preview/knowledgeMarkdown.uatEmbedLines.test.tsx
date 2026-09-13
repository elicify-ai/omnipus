// knowledgeMarkdown.uatEmbedLines.test.tsx — UAT 2026-09-13 D-12 (web half),
// D-131 and D-13 (web half): embeds written one per line with NO blank line
// between them are one markdown paragraph, and the old promotion rule
// ("exactly one embed in the paragraph") turned every such dashboard into a
// column of "embed shown as a link" badges with no reason given. Each of
// these tests fails on the pre-fix promotion pass.
//
// Same discipline as knowledgeMarkdown.dashboards.test.tsx: react-markdown,
// remark and every KB plugin are REAL; only the network boundary and the
// heavy renderers are mocked.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  KnowledgeBaseMarkdown,
  EMBED_INLINE_WITH_TEXT_REASON,
  resolvedEmbedLinkReason,
  type EmbedResolution,
} from './knowledgeMarkdown'
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
vi.mock('./KbPdfPageEmbedMount', () => ({
  KbPdfPageEmbedMount: (props: { page: number }) => (
    <div data-testid="kb-pdf-page-embed-mount-stub" data-page={props.page} />
  ),
}))

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
const PDF_WORKSPACE_PATH = 'vault/Assets/multipage.pdf'

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

function views(): KnowledgeBaseViews {
  return {
    base_path: BASE_WORKSPACE_PATH,
    is_knowledge_base: true,
    collection_id: 'kb_1',
    collection_root: 'vault',
    source: 'dashboards/Tasks.base',
    views: [
      { name: 'tasks--needs-daniel', label: 'Needs Daniel' },
      { name: 'tasks--blocked', label: 'Blocked' },
      { name: 'tasks--due-7d', label: 'Due ≤7d' },
    ],
    unloadable_count: 0,
  }
}

function resultFor(view: string): ViewResult {
  return {
    view,
    label: view,
    parts: [{ part: 'table', source: { part: 'table' }, columns: ['file.name'] }],
    rows: [{ path: `${view}.md`, title: `${view}-row`, cells: [], joins: [] }],
    complete: true,
    problems: [],
  }
}

/** Resolves any `.base` target to the fixture base file and any `.pdf`
 *  target to the fixture pdf — the client resolver's own matching is
 *  covered in KnowledgeNoteView.test.tsx. */
function resolver(target: string): EmbedResolution {
  if (target.endsWith('.pdf')) {
    return {
      state: 'resolved',
      path: 'Assets/multipage.pdf',
      url: '/api/v1/library/ws-1/download?path=vault%2FAssets%2Fmultipage.pdf',
      workspaceId: WORKSPACE,
      workspacePath: PDF_WORKSPACE_PATH,
    }
  }
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
      <KnowledgeBaseMarkdown content={content} notePath="dashboards/Cockpit.md" resolveEmbedUrl={resolver} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.mocked(fetchLibraryEntries).mockReset().mockResolvedValue(entries())
  vi.mocked(fetchKnowledgeBaseViews).mockReset().mockResolvedValue(views())
  vi.mocked(fetchKnowledgeViewResult)
    .mockReset()
    .mockImplementation(async (_ws: string, _col: string, view: string) => resultFor(view))
})

describe('UAT D-12 / D-131 — consecutive embed lines each mount (no blank line required)', () => {
  it('mounts BOTH base views written on adjacent lines — the Founder Cockpit "Needs you" shape', async () => {
    // DIES ON the old rule: two embeds in one paragraph → neither promoted →
    // two "embed shown as a link" badges and zero base-preview mounts.
    renderNote('## Needs you\n![[Tasks.base#Needs Daniel]]\n![[Tasks.base#Blocked]]\n')
    await waitFor(() => expect(screen.getAllByTestId('base-preview')).toHaveLength(2))
    expect(screen.queryByText('embed shown as a link')).not.toBeInTheDocument()
  })

  it('mounts every embed of a three-line run inside a callout blockquote — the collapsed-tier shape', async () => {
    renderNote(
      '> [!note]- Seeded tier\n> ### Tasks\n> ![[Tasks.base#Needs Daniel]]\n> ![[Tasks.base#Blocked]]\n> ![[Tasks.base#Due ≤7d]]\n',
    )
    await waitFor(() => expect(screen.getAllByTestId('base-preview')).toHaveLength(3))
  })

  it('keeps a whole-document pdf on its own line as a link, but still mounts the base view on the line after it', async () => {
    // A run of embed lines that MIXES kinds: the line with no inline
    // renderer stays a (badged) link, and does not drag the mountable
    // neighbour down with it.
    renderNote('![[Assets/multipage.pdf]]\n![[Tasks.base#Needs Daniel]]\n')
    await waitFor(() => expect(screen.getAllByTestId('base-preview')).toHaveLength(1))
    expect(screen.getByText('embed shown as a link')).toBeInTheDocument()
  })

  it('mounts a #page=N pdf embed written directly under a base embed', async () => {
    renderNote('![[Tasks.base#Needs Daniel]]\n![[Assets/multipage.pdf#page=2]]\n')
    await waitFor(() => expect(screen.getByTestId('kb-pdf-page-embed-mount-stub')).toBeInTheDocument())
    expect(screen.getByTestId('kb-pdf-page-embed-mount-stub').getAttribute('data-page')).toBe('2')
  })
})

describe('UAT D-12 (web half) — the badge says WHY an embed is a link', () => {
  it('names the shared line when a base embed sits inside a sentence', async () => {
    renderNote('See ![[Tasks.base#Needs Daniel]] for the list.')
    await waitFor(() => expect(screen.getByTestId('markdown-link')).toBeInTheDocument())
    expect(screen.queryByTestId('base-preview')).not.toBeInTheDocument()
    expect(screen.getByText('embed shown as a link')).toBeInTheDocument()
    expect(screen.getByTestId('kb-embed-link-reason').textContent).toContain(EMBED_INLINE_WITH_TEXT_REASON)
  })

  it('names the missing #page=N for a whole-document pdf on its own line', async () => {
    renderNote('![[Assets/multipage.pdf]]')
    await waitFor(() => expect(screen.getByTestId('markdown-link')).toBeInTheDocument())
    expect(screen.getByTestId('kb-embed-link-reason').textContent).toMatch(/add #page=N/)
  })

  it('UAT D-39 — names a malformed page token (#page=abc) instead of silently reading it as a heading', async () => {
    renderNote('![[Assets/multipage.pdf#page=abc]]')
    await waitFor(() => expect(screen.getByTestId('markdown-link')).toBeInTheDocument())
    expect(screen.queryByTestId('kb-pdf-page-embed-mount-stub')).not.toBeInTheDocument()
    expect(screen.getByTestId('kb-embed-link-reason').textContent).toMatch(/not a page number/)
  })

  it('resolvedEmbedLinkReason never answers with an empty reason for any kind', () => {
    for (const kind of ['image', 'base', 'markdown', 'audio', 'video', 'pdf', 'mermaid', 'html', 'text', 'other'] as const) {
      expect(resolvedEmbedLinkReason(kind, false, undefined).length).toBeGreaterThan(10)
      expect(resolvedEmbedLinkReason(kind, true, undefined).length).toBeGreaterThan(10)
    }
    expect(resolvedEmbedLinkReason(undefined, false, undefined).length).toBeGreaterThan(10)
  })
})
