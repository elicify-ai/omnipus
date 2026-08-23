// KnowledgeOutline.test.tsx — ADR-067 US-7 AS-5 / AS-9, FR-062, E-17.
//
// Every expected value here is derived from the SPECIFICATION, not from what
// the component happens to render:
//
//   • the indent ladder comes from KnowledgeOutlineHeading's own schema note
//     ("a FLAT list … with nesting carried by level … where a tree would force
//     the server to invent an intermediate heading the author never wrote") —
//     so `[H1, H3]` must cost exactly one indent step and `[H3, H4]` must start
//     at the left edge;
//   • the "Untitled heading" row comes from `text` being documented as "may be
//     empty for a heading marker with no text";
//   • the malformed-frontmatter statement comes from E-17;
//   • the loading state carrying no ratio comes from US-6's rule that a total
//     you do not have is never rendered as one.
//
// The fetch boundary is the injected `loadOutline` client, which is where the
// real `src/lib/api.ts` wrapper will sit once it exists.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type {
  KnowledgeOutline as KnowledgeOutlineResponse,
  KnowledgeOutlineHeading,
} from '@/lib/api/generated/openapi-types'
import {
  KnowledgeOutline,
  KNOWLEDGE_OUTLINE_MAX_INDENT_DEPTH,
  outlineIndentDepths,
} from './KnowledgeOutline'
import type { KnowledgeOutlineLoader } from './KnowledgeOutline'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

/** A heading whose fields default to the schema's required minimum. */
function heading(
  level: number,
  text: string,
  slug = text.toLowerCase().replace(/\s+/g, '-'),
): KnowledgeOutlineHeading {
  return { level, text, slug }
}

function makeOutline(over: Partial<KnowledgeOutlineResponse> = {}): KnowledgeOutlineResponse {
  return {
    path: 'notes/vault/architecture/sandboxing.md',
    is_knowledge_base: true,
    collection_id: 'kb_3d1c9a7e5b2f4806',
    headings: [],
    ...over,
  }
}

function renderOutline(opts: {
  loadOutline: KnowledgeOutlineLoader
  onNavigate?: (h: KnowledgeOutlineHeading) => void
  collapsible?: boolean
  activeSlug?: string
}) {
  const onNavigate = opts.onNavigate ?? vi.fn()
  const utils = render(
    <QueryClientProvider client={makeClient()}>
      <KnowledgeOutline
        workspaceId="ws-1"
        path="notes/vault/architecture/sandboxing.md"
        loadOutline={opts.loadOutline}
        onNavigate={onNavigate}
        collapsible={opts.collapsible}
        activeSlug={opts.activeSlug}
      />
    </QueryClientProvider>,
  )
  return { ...utils, onNavigate }
}

function rows() {
  return screen.queryAllByTestId('knowledge-outline-heading')
}

describe('outlineIndentDepths', () => {
  // Derived from KnowledgeOutlineHeading's schema note, not from the renderer.
  const cases: Array<{ name: string; levels: number[]; expected: number[] }> = [
    { name: 'a plain H1/H2/H3 ladder nests one step per level', levels: [1, 2, 3], expected: [0, 1, 2] },
    { name: 'a sibling closes its deeper predecessors', levels: [1, 2, 3, 2], expected: [0, 1, 2, 1] },
    { name: 'returning to the top level returns to the left edge', levels: [1, 2, 1], expected: [0, 1, 0] },
    {
      name: 'a note whose headings start at H3 is not indented for ancestors it does not have',
      levels: [3, 4],
      expected: [0, 1],
    },
    {
      name: 'an H1 to H3 jump costs one step — no intermediate heading is invented',
      levels: [1, 3],
      expected: [0, 1],
    },
    { name: 'equal levels are siblings', levels: [2, 2, 2], expected: [0, 0, 0] },
    { name: 'no headings at all', levels: [], expected: [] },
  ]

  for (const c of cases) {
    it(c.name, () => {
      const hs = c.levels.map((l, i) => heading(l, `H${l} #${i}`, `s${i}`))
      expect(outlineIndentDepths(hs)).toEqual(c.expected)
    })
  }
})

describe('KnowledgeOutline', () => {
  it('asks the outline endpoint for the open file, by workspace-relative path', async () => {
    const loadOutline = vi.fn().mockResolvedValue(makeOutline())
    renderOutline({ loadOutline })
    await waitFor(() => expect(loadOutline).toHaveBeenCalled())
    expect(loadOutline).toHaveBeenCalledWith({
      workspaceId: 'ws-1',
      path: 'notes/vault/architecture/sandboxing.md',
    })
  })

  it('lists every heading in document order, each carrying its true level', async () => {
    const loadOutline = vi.fn().mockResolvedValue(
      makeOutline({
        headings: [heading(1, 'Sandboxing'), heading(2, 'Landlock'), heading(3, 'Rulesets')],
      }),
    )
    renderOutline({ loadOutline })

    await waitFor(() => expect(rows()).toHaveLength(3))
    expect(rows().map((r) => r.getAttribute('data-slug'))).toEqual([
      'sandboxing',
      'landlock',
      'rulesets',
    ])
    expect(rows().map((r) => r.getAttribute('data-level'))).toEqual(['1', '2', '3'])
    expect(screen.getByText('Landlock')).toBeInTheDocument()
  })

  it('moves the reader to a heading when it is clicked', async () => {
    const target = heading(2, 'Landlock')
    const loadOutline = vi.fn().mockResolvedValue(
      makeOutline({ headings: [heading(1, 'Sandboxing'), target] }),
    )
    const onNavigate = vi.fn()
    renderOutline({ loadOutline, onNavigate })

    await waitFor(() => expect(rows()).toHaveLength(2))
    fireEvent.click(rows()[1])
    expect(onNavigate).toHaveBeenCalledTimes(1)
    expect(onNavigate).toHaveBeenCalledWith(target)
  })

  it('stops indenting at the readable bound while every row still states its real level', async () => {
    // Six headings, one per level: the raw nesting ladder is 0,1,2,3,4,5 — the
    // deepest a markdown document can go, since there is no H7.
    const headings = [
      heading(1, 'One', 's1'),
      heading(2, 'Two', 's2'),
      heading(3, 'Three', 's3'),
      heading(4, 'Four', 's4'),
      heading(5, 'Five', 's5'),
      heading(6, 'Six', 's6'),
    ]
    const loadOutline = vi.fn().mockResolvedValue(makeOutline({ headings }))
    renderOutline({ loadOutline })

    await waitFor(() => expect(rows()).toHaveLength(6))

    // The raw nesting really is 0..5 — the clamp is a rendering decision, not a
    // claim that the document is shallower than it is.
    expect(outlineIndentDepths(headings)).toEqual([0, 1, 2, 3, 4, 5])

    const indents = rows().map((r) => Number(r.getAttribute('data-indent-depth')))

    // THE EXPECTED LADDER IS LITERAL, NOT READ BACK FROM THE COMPONENT'S OWN
    // CONSTANT. It used to be written as
    //   [0,1,2,3,4,MAX,MAX] with Math.max(...) === MAX
    // over a fixture whose raw depths were [0,1,2,3,4,5,5] — which passes for
    // ANY value of MAX at or above 4, including a value that makes Math.min a
    // no-op. Raising the constant from 4 to 5 turned the clamp off and the test
    // still passed 19/19. The regression it exists to catch was invisible to it.
    expect(indents).toEqual([0, 1, 2, 3, 4, 4])

    // And the property that is the whole point, stated directly: two headings
    // at DIFFERENT true depths (4 and 5) are rendered at the SAME indent. This
    // is what dies when the bound is loosened by even one step.
    expect(indents[5]).toBe(indents[4])
    expect(KNOWLEDGE_OUTLINE_MAX_INDENT_DEPTH).toBe(4)

    // Nothing is lost by the clamp: level 5 and level 6 are still told apart.
    expect(rows().map((r) => r.getAttribute('data-level'))).toEqual(['1', '2', '3', '4', '5', '6'])
  })

  it('says plainly that a note has no headings instead of rendering an empty box', async () => {
    const loadOutline = vi.fn().mockResolvedValue(makeOutline({ headings: [] }))
    renderOutline({ loadOutline })

    const empty = await screen.findByTestId('knowledge-outline-empty')
    expect(empty).toHaveTextContent('This note has no headings.')
    expect(rows()).toHaveLength(0)
  })

  it('renders a heading marker with no text as a labelled, clickable row', async () => {
    // KnowledgeOutlineHeading.text: "May be empty for a heading marker with no
    // text." An empty row would be unclickable and unreadable.
    const empty = heading(2, '   ', 'blank')
    const loadOutline = vi.fn().mockResolvedValue(makeOutline({ headings: [empty] }))
    const onNavigate = vi.fn()
    renderOutline({ loadOutline, onNavigate })

    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(rows()[0]).toHaveTextContent('Untitled heading')
    fireEvent.click(rows()[0])
    expect(onNavigate).toHaveBeenCalledWith(empty)
  })

  it('reports malformed frontmatter in the panel body, and still shows the outline (E-17)', async () => {
    const loadOutline = vi.fn().mockResolvedValue(
      makeOutline({ headings: [heading(1, 'Sandboxing')], frontmatter_malformed: true }),
    )
    renderOutline({ loadOutline })

    const notice = await screen.findByTestId('knowledge-outline-frontmatter-malformed')
    expect(notice).toBeVisible()
    expect(notice.textContent).toMatch(/not valid YAML/i)
    // Reported, not substituted for the answer: the outline is still there.
    expect(rows()).toHaveLength(1)
  })

  it('reports malformed frontmatter even when the note has no headings', async () => {
    const loadOutline = vi.fn().mockResolvedValue(
      makeOutline({ headings: [], frontmatter_malformed: true }),
    )
    renderOutline({ loadOutline })

    expect(await screen.findByTestId('knowledge-outline-frontmatter-malformed')).toBeVisible()
    expect(await screen.findByTestId('knowledge-outline-empty')).toBeVisible()
  })

  it('waits indeterminately — never a bar and never a ratio against a total it does not have', async () => {
    // US-6's rule, applied here: nothing in this panel knows how many headings
    // are coming, so nothing in it may imply it does.
    const loadOutline = vi.fn(() => new Promise<KnowledgeOutlineResponse>(() => {}))
    renderOutline({ loadOutline })

    const loading = await screen.findByTestId('knowledge-outline-loading')
    expect(loading).toBeVisible()
    const panel = screen.getByTestId('knowledge-outline')
    expect(panel.querySelector('[role="progressbar"]')).toBeNull()
    expect(panel.querySelector('progress')).toBeNull()
    expect(panel.textContent ?? '').not.toMatch(/%|\bof\s+\d/)
  })

  it('surfaces a failed read with a retry that asks again', async () => {
    const loadOutline = vi
      .fn()
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValueOnce(makeOutline({ headings: [heading(1, 'Sandboxing')] }))
    renderOutline({ loadOutline })

    expect(await screen.findByTestId('knowledge-outline-error')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: /retry/i }))
    await waitFor(() => expect(rows()).toHaveLength(1))
  })

  it('collapses to a toggle when docked, rendering no rows until it is opened (FR-064)', async () => {
    const loadOutline = vi.fn().mockResolvedValue(
      makeOutline({ headings: [heading(1, 'Sandboxing')] }),
    )
    renderOutline({ loadOutline, collapsible: true })

    const toggle = await screen.findByTestId('knowledge-outline-toggle')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(rows()).toHaveLength(0)

    fireEvent.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await waitFor(() => expect(rows()).toHaveLength(1))
  })

  it('keeps the malformed-frontmatter caveat VISIBLE while the panel is collapsed (E-17)', async () => {
    // The warning lives in the panel body, and with `collapsible` the body
    // starts closed — so the caveat rendered NOTHING while the header still
    // showed a heading count. A closed disclosure is not a weaker warning than
    // a tooltip; it is no warning at all. The count and the thing that
    // qualifies it must travel together.
    //
    // DIES ON: removing `qualifiers` from KnowledgeOutline's header.
    const loadOutline = vi.fn().mockResolvedValue(
      makeOutline({ headings: [heading(1, 'Sandboxing')], frontmatter_malformed: true }),
    )
    renderOutline({ loadOutline, collapsible: true })

    const toggle = await screen.findByTestId('knowledge-outline-toggle')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    // The body — and with it the full sentence — is genuinely not rendered.
    expect(screen.queryByTestId('knowledge-outline-frontmatter-malformed')).not.toBeInTheDocument()
    // …and the caveat is on screen anyway, next to the count it qualifies.
    const qualifier = await screen.findByTestId('knowledge-outline-toggle-qualifier')
    expect(qualifier.textContent).toMatch(/frontmatter unreadable/i)
    expect(qualifier.textContent).toMatch(/not valid YAML/i)
    expect(toggle).toContainElement(qualifier)
  })

  it('shows no qualifier when there is nothing to qualify', async () => {
    // The other half of the rule: a caveat that appears on every visit is
    // furniture, and furniture is ignored.
    const loadOutline = vi.fn().mockResolvedValue(makeOutline({ headings: [heading(1, 'Sandboxing')] }))
    renderOutline({ loadOutline, collapsible: true })
    await screen.findByTestId('knowledge-outline-toggle')
    await waitFor(() => expect(screen.getByTestId('knowledge-outline-toggle-count')).toBeInTheDocument())
    expect(screen.queryByTestId('knowledge-outline-toggle-qualifier')).not.toBeInTheDocument()
  })

  it('marks the heading the reader is currently at', async () => {
    const loadOutline = vi.fn().mockResolvedValue(
      makeOutline({ headings: [heading(1, 'Sandboxing'), heading(2, 'Landlock')] }),
    )
    renderOutline({ loadOutline, activeSlug: 'landlock' })

    await waitFor(() => expect(rows()).toHaveLength(2))
    expect(rows()[0]).not.toHaveAttribute('aria-current')
    expect(rows()[1]).toHaveAttribute('aria-current', 'true')
  })
})
