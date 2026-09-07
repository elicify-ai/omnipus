// KnowledgeBacklinks.test.tsx — ADR-067 US-7 AS-6/AS-8/AS-9, US-8 AS-2/AS-5,
// FR-012, FR-041, FR-054, FR-063, FR-065, FR-112.
//
// Expected values are derived from the CONTRACT and the spec, never read back
// off the component:
//
//   • the ambiguity row exists because KnowledgeGraphEdge's schema says an
//     ambiguous basename "still RESOLVES by that rule and is ALSO reported as
//     ambiguous … resolving it is not a licence to stay quiet about it", with
//     `candidates` listing "every path that matched, in tie-break order";
//   • the inert unresolved row exists because KnowledgeGraphNode's schema says
//     of `exists: false` that "the client MUST mark such a node visibly and
//     MUST NOT navigate on click (FR-065)";
//   • the skipped section exists because KnowledgeGraphSkip is "reported rather
//     than omitted: a file the system cannot address must be visible to the
//     caller, never silently absent (FR-112)";
//   • the clipped-view statement exists because the response documents
//     `truncated` so "a caller can always tell a small graph from a clipped
//     one" (FR-054);
//   • the href is spelled out here from FR-012 plus `library.tsx`'s own search
//     schema (`workspace`, `path`) and `main.tsx`'s hash history — it is not
//     copied from the component's helper.
//
// The fetch boundary is the injected `loadGraph` client.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { KnowledgeGraphResponse } from '@/lib/api/generated/openapi-types'
import {
  KnowledgeBacklinks,
  collectionPathToWorkspacePath,
  libraryNoteHref,
} from './KnowledgeBacklinks'
import type { KnowledgeGraphLoader } from './KnowledgeBacklinks'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

type Edge = KnowledgeGraphResponse['edges'][number]
type Node = KnowledgeGraphResponse['nodes'][number]

function edge(over: Partial<Edge> = {}): Edge {
  return {
    from_path: 'index.md',
    to_path: 'architecture/sandboxing.md',
    resolution: 'exact_path',
    ambiguous: false,
    ...over,
  }
}

function node(path: string, over: Partial<Node> = {}): Node {
  return { path, exists: true, ...over }
}

function makeGraph(over: Partial<KnowledgeGraphResponse> = {}): KnowledgeGraphResponse {
  return {
    collection_id: 'kb_3d1c9a7e5b2f4806',
    kind: 'backlinks',
    source_path: 'architecture/sandboxing.md',
    nodes: [],
    edges: [],
    skipped: [],
    truncated: false,
    ...over,
  }
}

function renderBacklinks(opts: {
  loadGraph: KnowledgeGraphLoader
  onOpenNote?: (p: string) => void
  collectionRoot?: string
  collapsible?: boolean
}) {
  const onOpenNote = opts.onOpenNote ?? vi.fn()
  const utils = render(
    <QueryClientProvider client={makeClient()}>
      <KnowledgeBacklinks
        workspaceId="ws-1"
        collectionId="kb_3d1c9a7e5b2f4806"
        notePath="architecture/sandboxing.md"
        collectionRoot={opts.collectionRoot ?? 'notes/vault'}
        loadGraph={opts.loadGraph}
        onOpenNote={onOpenNote}
        collapsible={opts.collapsible}
      />
    </QueryClientProvider>,
  )
  return { ...utils, onOpenNote }
}

function rows() {
  return screen.queryAllByTestId('knowledge-backlink')
}

describe('collectionPathToWorkspacePath', () => {
  // A graph path is collection-relative; the Library address is
  // workspace-relative. Getting this wrong opens the wrong file.
  const cases: Array<[string, string, string]> = [
    ['', 'architecture/sandboxing.md', 'architecture/sandboxing.md'],
    ['notes/vault', 'architecture/sandboxing.md', 'notes/vault/architecture/sandboxing.md'],
    ['/notes/vault/', 'architecture/sandboxing.md', 'notes/vault/architecture/sandboxing.md'],
    ['notes/vault', '/index.md', 'notes/vault/index.md'],
    ['notes/vault', 'index.md', 'notes/vault/index.md'],
  ]
  for (const [root, rel, expected] of cases) {
    it(`joins ${JSON.stringify(root)} + ${JSON.stringify(rel)}`, () => {
      expect(collectionPathToWorkspacePath(root, rel)).toBe(expected)
    })
  }
})

describe('libraryNoteHref (FR-012)', () => {
  it('addresses a note with the Library route the app already validates', () => {
    // Spelled out from `library.tsx`'s search schema and hash history, not
    // from the helper under test.
    expect(libraryNoteHref('ws-1', 'notes/vault/index.md')).toBe(
      '/#/library?workspace=ws-1&path=notes%2Fvault%2Findex.md',
    )
  })
})

describe('KnowledgeBacklinks', () => {
  it('asks the graph endpoint for this note’s inbound links', async () => {
    const loadGraph = vi.fn().mockResolvedValue(makeGraph())
    renderBacklinks({ loadGraph })
    await waitFor(() => expect(loadGraph).toHaveBeenCalled())
    expect(loadGraph).toHaveBeenCalledWith({
      workspaceId: 'ws-1',
      collectionId: 'kb_3d1c9a7e5b2f4806',
      kind: 'backlinks',
      path: 'architecture/sandboxing.md',
    })
  })

  it('lists the notes that link to this one (US-7 AS-6)', async () => {
    const loadGraph = vi.fn().mockResolvedValue(
      makeGraph({
        nodes: [
          node('index.md', { title: 'Index' }),
          node('security/threat-model.md', { title: 'Threat model' }),
          node('archive/old-notes.md'),
        ],
        edges: [
          edge({ from_path: 'index.md' }),
          edge({ from_path: 'security/threat-model.md' }),
          edge({ from_path: 'archive/old-notes.md' }),
        ],
      }),
    )
    renderBacklinks({ loadGraph })

    await waitFor(() => expect(rows()).toHaveLength(3))
    expect(rows().map((r) => r.getAttribute('data-path'))).toEqual([
      'index.md',
      'security/threat-model.md',
      'archive/old-notes.md',
    ])
    // Titled nodes show their title; an untitled one falls back to its filename
    // rather than to a blank row.
    expect(screen.getByText('Index')).toBeInTheDocument()
    expect(screen.getByText('Threat model')).toBeInTheDocument()
    expect(screen.getByText('old-notes.md')).toBeInTheDocument()
  })

  it('gives every resolvable mention a real, addressable URL (FR-012)', async () => {
    const loadGraph = vi.fn().mockResolvedValue(
      makeGraph({
        nodes: [node('index.md', { title: 'Index' })],
        edges: [edge({ from_path: 'index.md' })],
      }),
    )
    renderBacklinks({ loadGraph, collectionRoot: 'notes/vault' })

    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(rows()[0].tagName).toBe('A')
    expect(rows()[0]).toHaveAttribute(
      'href',
      '/#/library?workspace=ws-1&path=notes%2Fvault%2Findex.md',
    )
  })

  it('opens a mention in place on a plain click, and leaves a modified click to the browser', async () => {
    const loadGraph = vi.fn().mockResolvedValue(
      makeGraph({ nodes: [node('index.md')], edges: [edge({ from_path: 'index.md' })] }),
    )
    const onOpenNote = vi.fn()
    renderBacklinks({ loadGraph, onOpenNote, collectionRoot: 'notes/vault' })
    await waitFor(() => expect(rows()).toHaveLength(1))

    // Record what the component did to the event, then stop jsdom actually
    // following the link.
    let prevented: boolean | undefined
    const spy = (e: Event) => {
      prevented = e.defaultPrevented
      e.preventDefault()
    }
    document.addEventListener('click', spy)
    try {
      fireEvent.click(rows()[0])
      expect(prevented).toBe(true)
      expect(onOpenNote).toHaveBeenCalledWith('notes/vault/index.md')

      onOpenNote.mockClear()
      prevented = undefined
      fireEvent.click(rows()[0], { metaKey: true })
      // A new-tab click is the whole point of having a URL — do not swallow it.
      expect(prevented).toBe(false)
      expect(onOpenNote).not.toHaveBeenCalled()
    } finally {
      document.removeEventListener('click', spy)
    }
  })

  it('shows an ambiguous link as ambiguous, with every candidate (FR-041)', async () => {
    const loadGraph = vi.fn().mockResolvedValue(
      makeGraph({
        nodes: [node('notes/setup.md', { title: 'Setup' })],
        edges: [
          edge({
            from_path: 'notes/setup.md',
            to_path: 'architecture/sandboxing.md',
            link_text: 'sandboxing',
            resolution: 'shortest_path',
            ambiguous: true,
            candidates: ['architecture/sandboxing.md', 'archive/sandboxing.md'],
          }),
        ],
      }),
    )
    renderBacklinks({ loadGraph })

    const notice = await screen.findByTestId('knowledge-backlink-ambiguous')
    expect(notice).toBeVisible()
    expect(notice.textContent).toMatch(/ambiguous/i)
    // The one it picked AND the one it did not — the reader is not told a
    // tie-break happened without being told what lost.
    expect(notice.textContent).toContain('architecture/sandboxing.md')
    expect(notice.textContent).toContain('archive/sandboxing.md')
    expect(notice.textContent).toContain('sandboxing')
  })

  it('marks an unresolved mention visibly and refuses to make it clickable (FR-065)', async () => {
    const loadGraph = vi.fn().mockResolvedValue(
      makeGraph({
        nodes: [node('ghost-note', { exists: false })],
        edges: [edge({ from_path: 'ghost-note', resolution: 'unresolved' })],
      }),
    )
    const onOpenNote = vi.fn()
    renderBacklinks({ loadGraph, onOpenNote })

    await waitFor(() => expect(rows()).toHaveLength(1))
    const row = rows()[0]
    expect(row).toHaveAttribute('data-unresolved', 'true')
    expect(screen.getByTestId('knowledge-backlink-unresolved')).toBeVisible()
    // Not merely styled differently: there is no link to follow at all.
    expect(row.tagName).not.toBe('A')
    expect(row).not.toHaveAttribute('href')
    fireEvent.click(row)
    expect(onOpenNote).not.toHaveBeenCalled()
  })

  it('treats a node the response never vouched for as unresolved rather than guessing', async () => {
    // `nodes` is documented as "every node referenced by this graph". A gap is
    // a broken answer, and the safe reading of a broken answer is not to send
    // the reader somewhere the server never confirmed.
    const loadGraph = vi.fn().mockResolvedValue(
      makeGraph({ nodes: [], edges: [edge({ from_path: 'index.md' })] }),
    )
    renderBacklinks({ loadGraph })

    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(rows()[0]).toHaveAttribute('data-unresolved', 'true')
    expect(rows()[0]).not.toHaveAttribute('href')
  })

  it('reports what the walk skipped, with the reason, alongside the results (FR-112)', async () => {
    const loadGraph = vi.fn().mockResolvedValue(
      makeGraph({
        nodes: [node('index.md')],
        edges: [edge({ from_path: 'index.md' })],
        skipped: [
          {
            path: 'notes/link-to-home',
            reason: 'symlink',
            detail: 'symbolic link to /home/dan/other-vault — not followed',
          },
          { path: 'notes/locked.md', reason: 'unreadable' },
        ],
      }),
    )
    renderBacklinks({ loadGraph })

    const section = await screen.findByTestId('knowledge-backlinks-skipped')
    expect(section).toBeVisible()
    const skips = screen.getAllByTestId('knowledge-backlinks-skip')
    expect(skips).toHaveLength(2)
    expect(skips.map((s) => s.getAttribute('data-reason'))).toEqual(['symlink', 'unreadable'])
    expect(skips[0].textContent).toContain('notes/link-to-home')
    expect(skips[0].textContent).toMatch(/not followed/i)
    expect(skips[1].textContent).toMatch(/could not be read/i)
    // Skipping is part of the answer, not an error state that replaces it.
    expect(rows()).toHaveLength(1)
  })

  it('says nothing was skipped by saying nothing — an empty array is a positive statement', async () => {
    const loadGraph = vi.fn().mockResolvedValue(
      makeGraph({ nodes: [node('index.md')], edges: [edge({ from_path: 'index.md' })], skipped: [] }),
    )
    renderBacklinks({ loadGraph })
    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(screen.queryByTestId('knowledge-backlinks-skipped')).toBeNull()
  })

  it('says the view is clipped when a bound stopped the walk (FR-054)', async () => {
    const loadGraph = vi.fn().mockResolvedValue(
      makeGraph({
        nodes: [node('index.md')],
        edges: [edge({ from_path: 'index.md' })],
        truncated: true,
        node_limit_applied: 200,
      }),
    )
    renderBacklinks({ loadGraph })

    const notice = await screen.findByTestId('knowledge-backlinks-truncated')
    expect(notice).toBeVisible()
    expect(notice.textContent).toMatch(/clipped/i)
    expect(notice.textContent).toContain('200')
    // A clipped view is still a view — the rows it did return are shown.
    expect(rows()).toHaveLength(1)
  })

  it('carries "truncated" onto the header while the panel is COLLAPSED (FR-054)', async () => {
    // The count sits on the header, which is always visible; the truncation
    // notice sat in the body, which with `collapsible` starts closed. A docked
    // reader therefore saw "LINKED MENTIONS 200" and nothing else — a confident
    // number for a walk a bound stopped, which is the one thing this panel's
    // own header forbids ("so a small graph is never mistaken for a clipped
    // one"). KnowledgeReader's rail toggle already did this correctly; the two
    // surfaces disagreed and this one lost.
    //
    // DIES ON: removing `qualifiers` from KnowledgeBacklinks' header.
    const loadGraph = vi.fn().mockResolvedValue(
      makeGraph({
        nodes: [node('index.md')],
        edges: [edge({ from_path: 'index.md' })],
        truncated: true,
        node_limit_applied: 200,
      }),
    )
    renderBacklinks({ loadGraph, collapsible: true })

    const toggle = await screen.findByTestId('knowledge-backlinks-toggle')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    // The count IS shown while collapsed …
    await waitFor(() =>
      expect(screen.getByTestId('knowledge-backlinks-toggle-count').textContent).toBe('1'),
    )
    // … the full notice is genuinely not rendered …
    expect(screen.queryByTestId('knowledge-backlinks-truncated')).not.toBeInTheDocument()
    // … and the caveat is on screen anyway.
    const qualifier = screen.getByTestId('knowledge-backlinks-toggle-qualifier')
    expect(qualifier.textContent).toMatch(/truncated/i)
    expect(qualifier.textContent).toMatch(/clipped view/i)
    expect(qualifier.textContent).toContain('200')
  })

  it('carries the skipped-path count onto the collapsed header too (FR-112)', async () => {
    const loadGraph = vi.fn().mockResolvedValue(
      makeGraph({
        nodes: [node('index.md')],
        edges: [edge({ from_path: 'index.md' })],
        skipped: [
          { path: 'notes/link-to-home', reason: 'symlink' },
          { path: 'notes/broken.md', reason: 'unreadable' },
        ],
      }),
    )
    renderBacklinks({ loadGraph, collapsible: true })

    const qualifiers = await screen.findAllByTestId('knowledge-backlinks-toggle-qualifier')
    expect(qualifiers.map((q) => q.getAttribute('data-qualifier'))).toEqual(['2 skipped'])
    expect(qualifiers[0].textContent).toMatch(/not searched for links/i)
  })

  it('shows no qualifier on a complete, unskipped answer', async () => {
    // A caveat on every visit is furniture; furniture gets ignored, and then
    // the real warning is invisible when it finally appears.
    const loadGraph = vi.fn().mockResolvedValue(
      makeGraph({ nodes: [node('index.md')], edges: [edge({ from_path: 'index.md' })] }),
    )
    renderBacklinks({ loadGraph, collapsible: true })
    await waitFor(() =>
      expect(screen.getByTestId('knowledge-backlinks-toggle-count').textContent).toBe('1'),
    )
    expect(screen.queryByTestId('knowledge-backlinks-toggle-qualifier')).not.toBeInTheDocument()
  })

  it('does not claim a clipped view when the walk finished', async () => {
    const loadGraph = vi.fn().mockResolvedValue(
      makeGraph({ nodes: [node('index.md')], edges: [edge({ from_path: 'index.md' })], truncated: false }),
    )
    renderBacklinks({ loadGraph })
    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(screen.queryByTestId('knowledge-backlinks-truncated')).toBeNull()
  })

  it('says plainly that nothing links here instead of rendering an empty box', async () => {
    const loadGraph = vi.fn().mockResolvedValue(makeGraph({ edges: [] }))
    renderBacklinks({ loadGraph })

    const empty = await screen.findByTestId('knowledge-backlinks-empty')
    expect(empty).toHaveTextContent('No other note links to this one.')
    expect(rows()).toHaveLength(0)
  })

  it('shows how the linking note referred to this one', async () => {
    const loadGraph = vi.fn().mockResolvedValue(
      makeGraph({
        nodes: [node('index.md', { title: 'Index' })],
        edges: [
          edge({
            from_path: 'index.md',
            alias: 'how sandboxing works',
            heading: 'Rulesets',
            embed: true,
          }),
        ],
      }),
    )
    renderBacklinks({ loadGraph })

    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(screen.getByTestId('knowledge-backlink-alias').textContent).toContain(
      'how sandboxing works',
    )
    expect(screen.getByTestId('knowledge-backlink-heading').textContent).toContain('Rulesets')
    expect(screen.getByTestId('knowledge-backlink-embed')).toBeVisible()
  })

  it('waits indeterminately — never a bar and never a ratio against a total it does not have', async () => {
    const loadGraph = vi.fn(() => new Promise<KnowledgeGraphResponse>(() => {}))
    renderBacklinks({ loadGraph })

    expect(await screen.findByTestId('knowledge-backlinks-loading')).toBeVisible()
    const panel = screen.getByTestId('knowledge-backlinks')
    expect(panel.querySelector('[role="progressbar"]')).toBeNull()
    expect(panel.querySelector('progress')).toBeNull()
    expect(panel.textContent ?? '').not.toMatch(/%|\bof\s+\d/)
  })

  it('surfaces a failed read with a retry that asks again', async () => {
    const loadGraph = vi
      .fn()
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValueOnce(
        makeGraph({ nodes: [node('index.md')], edges: [edge({ from_path: 'index.md' })] }),
      )
    renderBacklinks({ loadGraph })

    expect(await screen.findByTestId('knowledge-backlinks-error')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: /retry/i }))
    await waitFor(() => expect(rows()).toHaveLength(1))
  })

  it('collapses to a toggle when docked, rendering no rows until it is opened (FR-064)', async () => {
    const loadGraph = vi.fn().mockResolvedValue(
      makeGraph({ nodes: [node('index.md')], edges: [edge({ from_path: 'index.md' })] }),
    )
    renderBacklinks({ loadGraph, collapsible: true })

    const toggle = await screen.findByTestId('knowledge-backlinks-toggle')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(rows()).toHaveLength(0)

    fireEvent.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await waitFor(() => expect(rows()).toHaveLength(1))
  })
})
