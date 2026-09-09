// knowledgeMarkdown.transclusion.test.tsx — ADR-083 Step 3: one-level
// transclusion (US-7, EMB-055 through EMB-062, founder ruling N1).
//
// Separate file from knowledgeMarkdown.test.tsx for the same reason
// knowledgeMarkdown.dashboards.test.tsx is: that file globally stubs
// `@tanstack/react-query`'s useQuery to `{ data: null }`, which the
// components under test here need real behaviour from.
//
// N1's whole point, worth restating here because it is what these tests
// exist to catch: there is no cycle detector and no depth counter anywhere
// in this feature. What stops a second level is that a nested embed's
// resolver NEVER returns 'resolved' — see nestedTranscludedEmbedResolver in
// knowledgeMarkdown.tsx. A test that only checks "the page did not hang"
// would pass even with that resolver deleted (five levels of real nesting
// also finish inside any reasonable timeout) — every assertion below counts
// rendered copies instead, per the spec's own X8/test-112 guidance.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { KnowledgeBaseMarkdown, type EmbedResolution } from './knowledgeMarkdown'

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
  return { ...actual, fetchLibraryContent: vi.fn() }
})

import { fetchLibraryContent } from '@/lib/api'

const WORKSPACE = 'ws-1'
const OTHER_PATH = 'dashboards/Other.md'
const SELF_PATH = 'dashboards/Cockpit.md'

function textContent(content: string) {
  return { path: OTHER_PATH, content, size: content.length, is_text: true, too_large: false }
}

function resolverFor(targetPath: string, extraTargets: Record<string, string> = {}) {
  return (target: string): EmbedResolution => {
    const path = extraTargets[target] ?? targetPath
    return {
      state: 'resolved',
      path,
      url: `/api/v1/library/${WORKSPACE}/download?path=${encodeURIComponent(path)}`,
      workspaceId: WORKSPACE,
      workspacePath: path,
    }
  }
}

function renderNote(content: string, resolveEmbedUrl: ReturnType<typeof resolverFor>, notePath = SELF_PATH) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <KnowledgeBaseMarkdown content={content} notePath={notePath} resolveEmbedUrl={resolveEmbedUrl} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('a whole note transcluded (US-7 AS-1)', () => {
  it('shows the target note’s content in place, fetched through the ordinary content request', async () => {
    vi.mocked(fetchLibraryContent).mockResolvedValue(
      textContent('First paragraph of Other.\n\nSecond paragraph of Other.'),
    )
    renderNote('![[Other.md]]', resolverFor(OTHER_PATH))
    await waitFor(() => expect(screen.getByText('First paragraph of Other.')).toBeInTheDocument())
    expect(screen.getByText('Second paragraph of Other.')).toBeInTheDocument()
    expect(vi.mocked(fetchLibraryContent)).toHaveBeenCalledWith(WORKSPACE, OTHER_PATH)
  })
})

describe('a heading section transcluded (US-7 AS-2)', () => {
  it('shows only that section — not the content before or after it', async () => {
    vi.mocked(fetchLibraryContent).mockResolvedValue(
      textContent('# Title\n\nIntro text.\n\n## Results\n\nResults body.\n\n## Appendix\n\nAppendix body.\n'),
    )
    renderNote('![[Other.md#Results]]', resolverFor(OTHER_PATH))
    await waitFor(() => expect(screen.getByText('Results body.')).toBeInTheDocument())
    expect(screen.queryByText('Intro text.')).not.toBeInTheDocument()
    expect(screen.queryByText('Appendix body.')).not.toBeInTheDocument()
  })
})

describe('an anchored block transcluded (US-7 AS-3)', () => {
  it('shows only the anchored block', async () => {
    vi.mocked(fetchLibraryContent).mockResolvedValue(
      textContent('Para one.\n\nThe anchored line. ^blk1\n\nPara three.\n'),
    )
    renderNote('![[Other.md#^blk1]]', resolverFor(OTHER_PATH))
    await waitFor(() => expect(screen.getByText(/The anchored line\./)).toBeInTheDocument())
    expect(screen.queryByText('Para one.')).not.toBeInTheDocument()
    expect(screen.queryByText('Para three.')).not.toBeInTheDocument()
  })
})

describe('embeds inside a transcluded note render as links, never as content or as could-not-check (US-7 AS-4, EMB-060)', () => {
  it('renders the outer note’s text as content, and BOTH of the inner note’s own embeds — an image and a base view — as plain links with a reason', async () => {
    vi.mocked(fetchLibraryContent).mockResolvedValue(
      textContent('Other’s own paragraph.\n\n![[pic.png]]\n\n![[Data.base#View]]\n'),
    )
    renderNote('![[Other.md]]', resolverFor(OTHER_PATH))

    await waitFor(() => expect(screen.getByText('Other’s own paragraph.')).toBeInTheDocument())
    const box = screen.getByTestId('kb-transclusion')

    // No second level of ANY content — no nested transclusion box, no
    // mounted base preview, no picture.
    expect(within(box).queryByTestId('kb-transclusion')).not.toBeInTheDocument()
    expect(within(box).queryByTestId('base-preview')).not.toBeInTheDocument()
    expect(within(box).queryByRole('img')).not.toBeInTheDocument()

    // Both inner embeds are the LINK fallback, each carrying a reason —
    // never the could-not-be-checked (indeterminate) marker. The unresolved
    // treatment (UnresolvedLink) is a plain `data-kb-unresolved="true"` span
    // with the reason as its `title` — it carries no `data-kb-embed-*`
    // attributes of its own, so those are NOT what identifies it here.
    const links = within(box).getAllByTestId('markdown-link')
    const unresolvedLinks = links.filter((el) => el.getAttribute('data-kb-unresolved') === 'true')
    expect(unresolvedLinks).toHaveLength(2)
    for (const link of unresolvedLinks) {
      expect(link.getAttribute('title')).toMatch(/embeds inside a transcluded note are shown as links/)
    }
    // Never the could-not-be-checked marker's own dashed-warning treatment.
    expect(within(box).queryByText('could not be checked', { exact: false })).not.toBeInTheDocument()
  })
})

describe('a note that shows itself renders once and stops (US-7 AS-5, EMB-061, N1)', () => {
  it('the self-embedded copy shows the note’s own text exactly once inside it, and its own inner self-embed is a link, not a second copy', async () => {
    const SELF_CONTENT = 'Distinctive-Self-Content.\n\n![[Cockpit.md]]'
    vi.mocked(fetchLibraryContent).mockResolvedValue(textContent(SELF_CONTENT))

    renderNote(SELF_CONTENT, resolverFor(SELF_PATH), SELF_PATH)

    await waitFor(() => expect(screen.getByTestId('kb-transclusion')).toBeInTheDocument())
    const box = screen.getByTestId('kb-transclusion')

    // Exactly once WITHIN the transcluded copy — not zero (the fetch failed
    // silently) and not two-or-more (a second level unrolled).
    expect(within(box).getAllByText('Distinctive-Self-Content.')).toHaveLength(1)

    // The copy's OWN self-embed is a link, not another mounted transclusion.
    expect(within(box).queryByTestId('kb-transclusion')).not.toBeInTheDocument()
    const innerLink = within(box)
      .getAllByTestId('markdown-link')
      .find((el) => el.getAttribute('data-kb-unresolved') === 'true')
    expect(innerLink).toBeDefined()
    expect(innerLink?.getAttribute('title')).toMatch(/embeds inside a transcluded note are shown as links/)
  })
})

describe('a transcluded empty note says so (US-7 AS-6, EMB-062)', () => {
  it('states the note is empty — not blank space, not a failure marker', async () => {
    vi.mocked(fetchLibraryContent).mockResolvedValue(textContent('   \n\n  '))
    renderNote('![[Other.md]]', resolverFor(OTHER_PATH))
    await waitFor(() => expect(screen.getByTestId('kb-transclusion-empty')).toBeInTheDocument())
    expect(screen.queryByTestId('kb-embed-mount-error')).not.toBeInTheDocument()
    expect(screen.queryByTestId('kb-transclusion-not-found')).not.toBeInTheDocument()
  })
})

describe('a block anchor that does not exist is stated, not silently blank (client-side slicing, EMB-059)', () => {
  it('renders a not-found notice naming the requested anchor', async () => {
    vi.mocked(fetchLibraryContent).mockResolvedValue(textContent('Para one.\n\nNo anchor here.\n'))
    renderNote('![[Other.md#^missing]]', resolverFor(OTHER_PATH))
    await waitFor(() => expect(screen.getByTestId('kb-transclusion-not-found')).toBeInTheDocument())
    expect(screen.getByTestId('kb-transclusion-not-found').textContent ?? '').toContain('missing')
  })
})

describe('an embed mixed inline with other text is NOT promoted to transcluded content', () => {
  it('keeps the existing link-fallback treatment when other words share its paragraph', async () => {
    vi.mocked(fetchLibraryContent).mockResolvedValue(textContent('Should never be fetched into view.'))
    renderNote('See ![[Other.md]] for the full write-up.', resolverFor(OTHER_PATH))
    await waitFor(() => expect(screen.getByTestId('markdown-link')).toBeInTheDocument())
    expect(screen.queryByTestId('kb-transclusion')).not.toBeInTheDocument()
    expect(vi.mocked(fetchLibraryContent)).not.toHaveBeenCalled()
  })
})

describe('a transclusion below the fold issues NO content request until it nears the viewport (EMB-065, wiring LazyEmbedMount)', () => {
  class NeverIntersectingObserver {
    constructor() {}
    observe() {}
    unobserve() {}
    disconnect() {}
    takeRecords() {
      return []
    }
  }

  it('reserves space and never calls fetchLibraryContent while unmounted', () => {
    vi.stubGlobal('IntersectionObserver', NeverIntersectingObserver)
    try {
      renderNote('![[Other.md]]', resolverFor(OTHER_PATH))
      const mount = screen.getByTestId('lazy-embed-mount')
      expect(mount.getAttribute('data-mounted')).toBe('false')
      expect(screen.queryByTestId('kb-transclusion')).not.toBeInTheDocument()
      expect(vi.mocked(fetchLibraryContent)).not.toHaveBeenCalled()
    } finally {
      vi.unstubAllGlobals()
    }
  })
})
