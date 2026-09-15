// embedReservedHeights.rendered.test.tsx — the oracle `embedReservedHeights.test.ts`
// explicitly says it does NOT provide.
//
// That file closes EMB-066 for the four EXPORTED Step 6 constants and records
// its own limit honestly:
//
//   "Three more reserved heights are module-private inside
//    `knowledgeMarkdown.tsx` — BASE_EMBED (448), IMAGE_EMBED (240) and
//    TRANSCLUSION (72) — and nothing here or anywhere else would notice if
//    two of those collapsed onto one number. Widening this file would mean
//    exporting them purely for a test, which is a larger change than the gap
//    warrants."
//
// It is right that exporting a constant purely to assert it is the wrong
// trade. But the export was never the only way: what EMB-066 actually
// promises is OBSERVABLE — the height a wrapper reserves while its embed is
// still out of view. Asserting the rendered `style.minHeight` needs no
// production change at all, and is a STRICTLY BETTER oracle than importing
// the constant would be, because it also proves the constant is wired to the
// wrapper rather than merely declared. A constant that is distinct but unused
// passes an import-based check and fails this one.
//
// WHY THE NUMBERS MATTER. The reservation is what stops the page jumping
// under the reader as embeds mount during a scroll. Reserve too little and
// every embed shifts the text the moment it loads; reserve one shared value
// for all kinds and a 72px transclusion notice holds 448px of blank space.
// The three literals below are therefore derived from what each kind renders
// — a saved-view embed is a full panel, an image is a thumbnail band, a
// transclusion notice is a couple of lines — and are asserted as much for
// being DIFFERENT from one another as for their exact values.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { holdEmbedsOutOfView } from '@/test/intersectionObserver'
import { KnowledgeBaseMarkdown, type EmbedResolution } from './knowledgeMarkdown'

vi.mock('@/components/chat/mermaid-renderer', () => ({
  MermaidDiagram: () => <div data-testid="mermaid-diagram" />,
}))
vi.mock('react-shiki', () => ({
  ShikiHighlighter: ({ children }: { children?: React.ReactNode }) => <pre data-testid="shiki">{children}</pre>,
  createJavaScriptRegexEngine: () => ({}),
}))
vi.mock('@/components/chat/ChatImage', () => ({
  ChatImage: ({ src, alt }: { src: string; alt?: string }) => <img data-testid="chat-image" src={src} alt={alt} />,
}))
vi.mock('@/store/ui', () => ({ useUiStore: { getState: () => ({ addToast: vi.fn() }) } }))
// BasePreview drags in the whole saved-view machinery; this file is about the
// wrapper's reserved height, which is decided before any of it mounts.
vi.mock('./BasePreview', () => ({ BasePreview: () => null }))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchLibraryContent: vi.fn(), fetchLibraryEntries: vi.fn() }
})

const WORKSPACE = 'ws-1'
const NOTE_PATH = 'dashboards/Cockpit.md'

/** Resolves any embed target to a real path, so the mount under test is the
 *  `resolved` one rather than a notice. */
function resolveTo(path: string) {
  return (): EmbedResolution => ({
    state: 'resolved',
    path,
    url: `/api/v1/library/${WORKSPACE}/download?path=${encodeURIComponent(path)}`,
    workspaceId: WORKSPACE,
    workspacePath: path,
  })
}

function renderNote(content: string, path: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <KnowledgeBaseMarkdown content={content} notePath={NOTE_PATH} resolveEmbedUrl={resolveTo(path)} />
    </QueryClientProvider>,
  )
}

/** The three kinds, each with the note body that produces it. */
const KINDS = {
  base: { body: '![[Cockpit.base]]', path: 'dashboards/Cockpit.base' },
  image: { body: '![[diagram.png|400]]', path: 'media/diagram.png' },
  transclusion: { body: '![[Other.md]]', path: 'dashboards/Other.md' },
} as const

/** Renders each kind in turn and reads the height it ACTUALLY reserves. */
function measureAllKinds(): Record<keyof typeof KINDS, number> {
  const out = {} as Record<keyof typeof KINDS, number>
  for (const [kind, { body, path }] of Object.entries(KINDS) as [keyof typeof KINDS, (typeof KINDS)[keyof typeof KINDS]][]) {
    const { unmount } = renderNote(body, path)
    out[kind] = reservedHeightPx()
    unmount()
  }
  return out
}

/** The reserved height of the single lazy wrapper in the rendered note, in px. */
function reservedHeightPx(): number {
  const wrapper = screen.getByTestId('lazy-embed-mount')
  // Proving it is genuinely still gated is what makes the reservation
  // meaningful — a mounted wrapper releases its minHeight, so reading one off
  // a mounted embed would report '' and this helper would return NaN.
  expect(wrapper.getAttribute('data-mounted')).toBe('false')
  const raw = wrapper.style.minHeight
  expect(raw).toMatch(/^\d+px$/)
  return Number.parseInt(raw, 10)
}

beforeEach(() => {
  vi.clearAllMocks()
  holdEmbedsOutOfView()
})

describe('EMB-066 — the three module-private reserved heights, asserted as rendered behaviour', () => {
  it('a saved-view (.base) embed reserves a full panel', () => {
    renderNote('![[Cockpit.base]]', 'dashboards/Cockpit.base')
    expect(reservedHeightPx()).toBe(448)
  })

  it('a sized image embed reserves a thumbnail band — much less than a view panel', () => {
    // The `|400` matters: KbImageEmbedMount is the SIZED picture embed
    // (EMB-030). An unsized `![[diagram.png]]` renders through the ordinary
    // image path and never reaches this wrapper at all.
    renderNote('![[diagram.png|400]]', 'media/diagram.png')
    // And the reservation is deliberately NOT derived from that written
    // width — the source comment is explicit that a pixel width predicts no
    // aspect ratio, so reserving a height from it would invite a bigger
    // reflow than a fixed picture-shaped default.
    expect(reservedHeightPx()).toBe(240)
    expect(reservedHeightPx()).not.toBe(400)
  })

  it('a note transclusion reserves the least of the three', () => {
    renderNote('![[Other.md]]', 'dashboards/Other.md')
    expect(reservedHeightPx()).toBe(72)
  })

  it('and the three are DISTINCT — one shared constant would defeat the whole reservation', () => {
    // MEASURED, not restated. An earlier draft of this test compared three
    // hardcoded literals to each other, which is a test that cannot fail:
    // collapsing two of the real constants left it green. It now renders
    // each kind and reads the height the wrapper actually reserves, so it
    // fails on exactly the collapse embedReservedHeights.test.ts says
    // nothing would notice.
    const heights = measureAllKinds()
    const values = Object.values(heights)

    expect(new Set(values).size).toBe(values.length)

    // Ordered, not merely distinct: the ordering is the actual design claim —
    // a view panel is taller than an image band, which is taller than a
    // two-line notice. Three distinct but shuffled numbers would satisfy a
    // Set check and still reserve the wrong space for every kind.
    expect(heights.base).toBeGreaterThan(heights.image)
    expect(heights.image).toBeGreaterThan(heights.transclusion)
  })
})
