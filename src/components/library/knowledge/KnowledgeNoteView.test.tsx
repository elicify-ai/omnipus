// KnowledgeNoteView.test.tsx — the wiring that makes the reading surface
// reachable (ADR-067 US-7, FR-012, FR-060, FR-062, FR-063, FR-065).
//
// What is asserted here is the CONTAINER's own decisions, each of which is a
// statement about honesty rather than about layout:
//
//   • the outline is asked for on ANY markdown file, and the linked-mentions
//     panel appears only inside a detected collection (FR-062's split);
//   • the collection root is IDENTIFIED, by matching the outline's own
//     collection_id up the ancestor chain — never guessed from "the first
//     ancestor that happens to be a knowledge base";
//   • when the root cannot be identified the panel is not rendered as an empty
//     list, because "no note links here" and "Omnipus could not work out where
//     this collection starts" are different facts;
//   • `resolveWikilink` is passed ONLY once the link graph has answered, so a
//     wikilink is never marked broken — or verified — on no evidence.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import { KnowledgeNoteView, findSkipForTarget, noteAncestorDirs } from './KnowledgeNoteView'
import type { KnowledgeGraphLoader } from './KnowledgeBacklinks'
import type { KnowledgeOutlineLoader } from './KnowledgeOutline'
import type {
  KnowledgeBaseInfo,
  KnowledgeGraphEdge,
  KnowledgeGraphResponse,
  KnowledgeGraphSkip,
  KnowledgeOutline,
} from '@/lib/api/generated/openapi-types'

vi.mock('@/components/chat/mermaid-renderer', () => ({
  MermaidDiagram: ({ code }: { code: string }) => <div data-testid="mermaid-diagram">{code}</div>,
}))
vi.mock('react-shiki', () => ({
  ShikiHighlighter: ({ children }: { children?: React.ReactNode }) => <pre data-testid="shiki">{children}</pre>,
  // markdown-shared.tsx passes Shiki its pure-JS regex engine (the SPA's CSP
  // refuses the WebAssembly default); the module is mocked here, so this only
  // has to exist.
  createJavaScriptRegexEngine: () => ({}),
}))
vi.mock('@/components/chat/ChatImage', () => ({
  ChatImage: ({ src, alt }: { src: string; alt?: string }) => <img data-testid="chat-image" src={src} alt={alt} />,
}))
vi.mock('@/store/ui', () => ({ useUiStore: { getState: () => ({ addToast: vi.fn() }) } }))

const COLLECTION = 'kb_3d1c9a7e5b2f4806'

function info(over: Partial<KnowledgeBaseInfo> = {}): KnowledgeBaseInfo {
  return {
    workspace_id: 'ws-1',
    root_path: 'notes/vault',
    is_knowledge_base: false,
    marker: 'none',
    ...over,
  }
}

function outline(over: Partial<KnowledgeOutline> = {}): KnowledgeOutline {
  return {
    path: 'notes/vault/architecture/sandboxing.md',
    is_knowledge_base: true,
    collection_id: COLLECTION,
    headings: [{ level: 1, text: 'Sandboxing', slug: 'sandboxing' }],
    ...over,
  }
}

function graph(over: Partial<KnowledgeGraphResponse> = {}): KnowledgeGraphResponse {
  return {
    collection_id: COLLECTION,
    kind: 'backlinks',
    nodes: [],
    edges: [],
    skipped: [],
    truncated: false,
    ...over,
  }
}

function embedEdge(over: Partial<KnowledgeGraphEdge> = {}): KnowledgeGraphEdge {
  return {
    heading_found: false,
    from_path: 'architecture/sandboxing.md',
    to_path: 'diagram.png',
    link_text: 'diagram.png',
    resolution: 'exact_path',
    ambiguous: false,
    embed: true,
    ...over,
  }
}

function skip(over: Partial<KnowledgeGraphSkip> = {}): KnowledgeGraphSkip {
  return { path: 'notes/private', reason: 'unreadable', ...over }
}

function bodyText(): string {
  return document.body.textContent ?? ''
}

/** Detection answers keyed by the folder asked about. Anything not listed is an
 *  ordinary folder, which is what the real endpoint says too. */
function detectionOf(map: Record<string, KnowledgeBaseInfo>) {
  return vi.fn(async (_ws: string, path: string) => map[path] ?? info({ root_path: path || '.' }))
}

function renderView(opts: {
  loadOutline: KnowledgeOutlineLoader
  loadGraph?: KnowledgeGraphLoader
  loadInfo?: (ws: string, path: string) => Promise<KnowledgeBaseInfo>
  notePath?: string
  content?: string
  onOpenNote?: (p: string) => void
}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const loadGraph = opts.loadGraph ?? vi.fn().mockResolvedValue(graph())
  const loadInfo = opts.loadInfo ?? detectionOf({})
  const utils = render(
    <QueryClientProvider client={client}>
      <KnowledgeNoteView
        workspaceId="ws-1"
        notePath={opts.notePath ?? 'notes/vault/architecture/sandboxing.md'}
        content={opts.content ?? '# Sandboxing\n\nbody'}
        layout="wide"
        loadOutline={opts.loadOutline}
        loadGraph={loadGraph}
        loadInfo={loadInfo}
        {...(opts.onOpenNote ? { onOpenNote: opts.onOpenNote } : {})}
      />
    </QueryClientProvider>,
  )
  return { ...utils, loadGraph, loadInfo }
}

describe('noteAncestorDirs', () => {
  // Deepest first, because the nearest enclosing collection is the note's own —
  // a nested collection must never be attributed to its parent.
  it('lists the note’s folders from the deepest up to the work-tree root', () => {
    expect(noteAncestorDirs('a/b/c/n.md')).toEqual(['a/b/c', 'a/b', 'a', ''])
  })

  it('is just the root for a note at the top level', () => {
    expect(noteAncestorDirs('n.md')).toEqual([''])
  })

  it('ignores empty segments rather than producing duplicate folders', () => {
    expect(noteAncestorDirs('a//b/n.md')).toEqual(['a/b', 'a', ''])
  })
})

describe('findSkipForTarget (unit, ADR-083 EMB-021)', () => {
  it('matches on path equality (clause 1)', () => {
    expect(findSkipForTarget([skip({ path: 'notes/private/plan.md' })], 'notes/private/plan.md')?.reason).toBe(
      'unreadable',
    )
  })

  it('matches on basename equality, with and without a markdown extension (clause 2)', () => {
    // A bare wikilink target naming no folder at all — the skip's own path
    // still has one, so only the final segment can line up.
    expect(findSkipForTarget([skip({ path: 'notes/private/plan.md' })], 'plan')).toBeDefined()
    expect(findSkipForTarget([skip({ path: 'notes/private/plan' })], 'plan.md')).toBeDefined()
  })

  it('matches an unreadable DIRECTORY against every file beneath it (clause 3 — the dominant walk-level shape)', () => {
    // This is the shape EMB-021's own rationale names: `WalkContained`
    // records an unreadable directory under ITS OWN path, and every file
    // beneath it never enters the walk at all, so clauses 1 and 2 both miss.
    const found = findSkipForTarget([skip({ path: 'notes/private', reason: 'unreadable' })], 'notes/private/plan.md')
    expect(found?.reason).toBe('unreadable')
  })

  it('does NOT suppress a sibling directory whose name is a near-miss prefix (B5c/B5e near-misses)', () => {
    // `notes/priv` is a STRING prefix of `notes/private/plan.md` but not a
    // path-SEGMENT prefix — clause 3 must require the boundary.
    expect(findSkipForTarget([skip({ path: 'notes/priv' })], 'notes/private/plan.md')).toBeUndefined()
    // A skip naming the sibling directory must not suppress an unrelated one.
    expect(findSkipForTarget([skip({ path: 'notes/public' })], 'notes/private/plan.md')).toBeUndefined()
  })

  it('clause 2 is basename EQUALITY, not a basename prefix (guards a substring-match bug)', () => {
    // If clause 2 were implemented as a prefix/substring check instead of an
    // equality check, a skip named "plan.md" would wrongly suppress an
    // unrelated file whose name merely starts the same way.
    expect(findSkipForTarget([skip({ path: 'notes/plan.md' })], 'notes/plan-extended.md')).toBeUndefined()
  })

  it('reports no match when nothing in the skip list corresponds (the ordinary case)', () => {
    expect(findSkipForTarget([skip({ path: 'unrelated/dir' })], 'notes/plan.md')).toBeUndefined()
    expect(findSkipForTarget([], 'notes/plan.md')).toBeUndefined()
  })
})

describe('KnowledgeNoteView — an ordinary markdown file (FR-062)', () => {
  it('shows the outline and does NOT ask for a link graph', async () => {
    // An outline is parsed from the one file in hand and needs no index, so it
    // is offered everywhere. Search and backlinks need one, so they are not.
    const loadOutline = vi.fn().mockResolvedValue(
      outline({ is_knowledge_base: false, collection_id: undefined }),
    )
    const { loadGraph, loadInfo } = renderView({ loadOutline })

    expect(await screen.findByTestId('knowledge-outline-heading')).toHaveTextContent('Sandboxing')
    expect(screen.queryByTestId('knowledge-backlinks')).not.toBeInTheDocument()
    expect(loadGraph).not.toHaveBeenCalled()
    // No collection to find, so the ancestor walk never runs either.
    expect(loadInfo).not.toHaveBeenCalled()
  })
})

describe('KnowledgeNoteView — inside a collection', () => {
  it('finds the collection root by MATCHING the id, not by taking the nearest knowledge base', async () => {
    // A NESTED collection is the case that separates the two rules, and it is a
    // real one: a vault at `notes/vault` can perfectly well contain its own
    // marked sub-collection at `notes/vault/archive`, and the outline endpoint
    // reports whichever of the two the gateway's scope resolved the note to.
    // Here it reported the OUTER one.
    //
    // "The nearest ancestor that is a knowledge base" answers `notes/vault/
    // archive` — a plausible root, a wrong one, and one that makes every
    // backlink open the wrong file (`old.md` instead of `archive/old.md`).
    // Matching the id the outline actually named answers `notes/vault`.
    //
    // DIES ON: matching on `is_knowledge_base` instead of on `collection_id`.
    const loadOutline = vi.fn().mockResolvedValue(
      outline({ path: 'notes/vault/archive/old.md', collection_id: COLLECTION }),
    )
    const loadInfo = detectionOf({
      'notes/vault/archive': info({
        root_path: 'notes/vault/archive',
        is_knowledge_base: true,
        collection_id: 'kb_inner_collection',
      }),
      'notes/vault': info({ root_path: 'notes/vault', is_knowledge_base: true, collection_id: COLLECTION }),
    })
    const loadGraph = vi.fn().mockResolvedValue(graph())

    renderView({ loadOutline, loadInfo, loadGraph, notePath: 'notes/vault/archive/old.md' })

    await waitFor(() => expect(loadGraph).toHaveBeenCalled())
    expect(loadGraph.mock.calls[0][0]).toMatchObject({
      collectionId: COLLECTION,
      kind: 'backlinks',
      // COLLECTION-relative, i.e. the workspace path with the ROOT removed.
      path: 'archive/old.md',
    })
  })

  it('renders backlinks with a Library address built on the real root (FR-012)', async () => {
    const loadOutline = vi.fn().mockResolvedValue(outline())
    const loadInfo = detectionOf({
      'notes/vault': info({ root_path: 'notes/vault', is_knowledge_base: true, collection_id: COLLECTION }),
    })
    const loadGraph = vi.fn().mockResolvedValue(
      graph({
        nodes: [{ path: 'index.md', title: 'Index', exists: true }],
        edges: [
          {
            heading_found: false,
            from_path: 'index.md',
            to_path: 'architecture/sandboxing.md',
            resolution: 'exact_path',
            ambiguous: false,
          },
        ],
      }),
    )

    renderView({ loadOutline, loadInfo, loadGraph })

    const row = await screen.findByTestId('knowledge-backlink')
    expect(row.tagName).toBe('A')
    expect(row.getAttribute('href')).toBe('/#/library?workspace=ws-1&path=notes%2Fvault%2Findex.md')
  })

  it('says linked mentions are UNAVAILABLE when the root cannot be identified', async () => {
    // "No note links to this one" and "Omnipus could not work out where this
    // collection starts" are different facts. Rendering the second as the first
    // is a confident answer to a question nobody could answer.
    //
    // DIES ON: falling back to `collectionRoot = ''` when no ancestor matches.
    const loadOutline = vi.fn().mockResolvedValue(outline())
    const loadInfo = detectionOf({}) // nothing is a knowledge base
    const loadGraph = vi.fn().mockResolvedValue(graph())

    renderView({ loadOutline, loadInfo, loadGraph })

    const notice = await screen.findByTestId('knowledge-backlinks-unavailable')
    expect(notice.textContent ?? '').toMatch(/could not identify which folder this collection starts at/i)
    expect(screen.queryByTestId('knowledge-backlinks')).not.toBeInTheDocument()
    expect(loadGraph).not.toHaveBeenCalled()
  })
})

describe('KnowledgeNoteView — wikilinks are resolved only on evidence (FR-065)', () => {
  const NOTE = 'see [[Ghost]] and [[Index]]'

  it('marks a wikilink unresolved when the graph says the target does not exist', async () => {
    const loadOutline = vi.fn().mockResolvedValue(outline())
    const loadInfo = detectionOf({
      'notes/vault': info({ root_path: 'notes/vault', is_knowledge_base: true, collection_id: COLLECTION }),
    })
    const loadGraph = vi.fn(async (req: { kind: string }) =>
      req.kind === 'links'
        ? graph({
            kind: 'links',
            nodes: [
              { path: 'Ghost.md', exists: false },
              { path: 'index.md', title: 'Index', exists: true },
            ],
            edges: [
              {
                heading_found: false,
                from_path: 'architecture/sandboxing.md',
                to_path: 'Ghost.md',
                link_text: 'Ghost',
                resolution: 'unresolved',
                ambiguous: false,
              },
              {
                heading_found: false,
                from_path: 'architecture/sandboxing.md',
                to_path: 'index.md',
                link_text: 'Index',
                resolution: 'unique_basename',
                ambiguous: false,
              },
            ],
          })
        : graph(),
    ) as unknown as KnowledgeGraphLoader

    renderView({ loadOutline, loadInfo, loadGraph, content: NOTE })

    await waitFor(() => {
      const links = screen.getAllByTestId('markdown-link')
      expect(links).toHaveLength(2)
      // The unresolved one is not an anchor and carries no address at all —
      // there is nothing to click, middle-click or copy.
      expect(links[0].getAttribute('data-kb-unresolved')).toBe('true')
      expect(links[0].tagName).not.toBe('A')
      // The resolved one is a real link.
      expect(links[1].tagName).toBe('A')
      expect(links[1].getAttribute('data-kb-state')).toBe('resolved')
    })
  })

  it('leaves every wikilink UNVERIFIED while the graph has not answered', async () => {
    // Absence of evidence is not evidence of absence — and it is not evidence
    // of presence either. The link is clickable and visibly unchecked.
    //
    // DIES ON: passing `resolveWikilink` before the links query resolves.
    const loadOutline = vi.fn().mockResolvedValue(outline())
    const loadInfo = detectionOf({
      'notes/vault': info({ root_path: 'notes/vault', is_knowledge_base: true, collection_id: COLLECTION }),
    })
    // A graph loader that never settles: the reading column must still render.
    const loadGraph = (() => new Promise<KnowledgeGraphResponse>(() => {})) as KnowledgeGraphLoader

    renderView({ loadOutline, loadInfo, loadGraph, content: NOTE })

    await waitFor(() => expect(screen.getAllByTestId('markdown-link')).toHaveLength(2))
    for (const link of screen.getAllByTestId('markdown-link')) {
      expect(link.getAttribute('data-kb-state')).toBe('unknown')
      expect(link.getAttribute('data-kb-unresolved')).toBeNull()
      expect(link.textContent ?? '').toMatch(/not verified/i)
    }
  })
})

describe('KnowledgeNoteView — the embed resolver (ADR-083 EMB-011 through EMB-024)', () => {
  const KB_INFO = {
    'notes/vault': info({ root_path: 'notes/vault', is_knowledge_base: true, collection_id: COLLECTION }),
  }

  function renderEmbedNote(opts: { content: string; loadGraph: KnowledgeGraphLoader }) {
    const loadOutline = vi.fn().mockResolvedValue(outline())
    const loadInfo = detectionOf(KB_INFO)
    return renderView({ loadOutline, loadInfo, loadGraph: opts.loadGraph, content: opts.content })
  }

  it('mounts the image for a RESOLVED embed — proving the embed resolver, not the plain-wikilink resolver, drove it', async () => {
    // DIES ON: routing the embed through `resolveWikilink`'s match key (no
    // `embed` flag), which this fixture's edge would also satisfy.
    const loadGraph = vi.fn(async (req: { kind: string }) =>
      req.kind === 'links'
        ? graph({
            kind: 'links',
            nodes: [{ path: 'diagram.png', title: 'diagram.png', exists: true }],
            edges: [embedEdge({ to_path: 'diagram.png', link_text: 'diagram.png' })],
          })
        : graph(),
    ) as unknown as KnowledgeGraphLoader

    renderEmbedNote({ content: '![[diagram.png]]', loadGraph })

    const img = await screen.findByTestId('chat-image')
    expect(img.getAttribute('src') ?? '').toContain('diagram.png')
  })

  it('reserves space with NO marker while the graph is loading (EMB-015)', async () => {
    // `findByTestId` alone would be satisfied by the FIRST paint's transient
    // `indeterminate` default (no resolveEmbedUrl until collectionId
    // resolves) — `waitFor` re-asserts until the state genuinely SETTLES.
    const loadGraph = (() => new Promise<KnowledgeGraphResponse>(() => {})) as KnowledgeGraphLoader
    renderEmbedNote({ content: '![[report.pdf]]', loadGraph })

    await waitFor(() =>
      expect(screen.getByTestId('markdown-link').getAttribute('data-kb-embed-state')).toBe('loading'),
    )
    expect(bodyText()).not.toMatch(/embed shown as a link/i)
  })

  it('renders ONE page-level banner (with the real failure and a retry) and marks the embed graph_unavailable — never a per-embed marker (EMB-014)', async () => {
    const loadGraph = vi.fn(async (req: { kind: string }) => {
      if (req.kind === 'links') throw new Error('network down')
      return graph()
    }) as unknown as KnowledgeGraphLoader

    renderEmbedNote({ content: '![[report.pdf]]', loadGraph })

    const banner = await screen.findByTestId('knowledge-graph-unavailable')
    expect(banner.textContent ?? '').toMatch(/network down/i)
    const el = screen.getByTestId('markdown-link')
    expect(el.getAttribute('data-kb-embed-state')).toBe('graph_unavailable')
    expect(el.textContent ?? '').not.toMatch(/embed shown as a link/i)
    expect(el.textContent ?? '').not.toMatch(/could not be checked/i)
  })

  it('refetches the link graph when the banner’s Retry button is pressed', async () => {
    let attempt = 0
    const loadGraph = vi.fn(async (req: { kind: string }) => {
      if (req.kind !== 'links') return graph()
      attempt += 1
      if (attempt === 1) throw new Error('first attempt fails')
      return graph({
        kind: 'links',
        nodes: [{ path: 'report.pdf', title: 'report.pdf', exists: true }],
        edges: [embedEdge({ to_path: 'report.pdf', link_text: 'report.pdf' })],
      })
    }) as unknown as KnowledgeGraphLoader

    renderEmbedNote({ content: '![[report.pdf]]', loadGraph })

    await screen.findByTestId('knowledge-graph-unavailable')
    fireEvent.click(screen.getByRole('button', { name: /retry/i }))

    await waitFor(() => expect(screen.getByTestId('markdown-link').getAttribute('data-kb-state')).toBe('resolved'))
    expect(screen.queryByTestId('knowledge-graph-unavailable')).not.toBeInTheDocument()
  })

  it('shows the one page-level banner when the graph answers EMPTY for a note that plainly has links (EMB-014’s second clause)', async () => {
    const loadGraph = vi.fn(async (req: { kind: string }) =>
      req.kind === 'links' ? graph({ kind: 'links' }) : graph(),
    ) as unknown as KnowledgeGraphLoader

    renderEmbedNote({ content: '![[report.pdf]]', loadGraph })

    const banner = await screen.findByTestId('knowledge-graph-unavailable')
    expect(banner.textContent ?? '').toMatch(/returned no link information/i)
  })

  it('does NOT show the empty-answer banner for an ordinary note with no wikilink notation at all', async () => {
    // The empty shape is the ORDINARY case for a linkless note — showing a
    // warning here would be false-positive noise on the common case.
    const loadGraph = vi.fn().mockResolvedValue(graph()) as unknown as KnowledgeGraphLoader
    renderEmbedNote({ content: 'just prose, no links here', loadGraph })

    await waitFor(() => expect(loadGraph).toHaveBeenCalled())
    expect(screen.queryByTestId('knowledge-graph-unavailable')).not.toBeInTheDocument()
  })

  it('renders "could not be checked" (not the missing-file marker) when the skip list explains an unresolved edge (EMB-021)', async () => {
    const loadGraph = vi.fn(async (req: { kind: string }) =>
      req.kind === 'links'
        ? graph({
            kind: 'links',
            nodes: [{ path: 'private/plan.md', exists: false }],
            edges: [
              embedEdge({
                to_path: 'private/plan.md',
                link_text: 'private/plan',
                resolution: 'unresolved',
              }),
            ],
            // The skip names the DIRECTORY, not the file — the dominant
            // walk-level shape EMB-021 clause 3 exists for.
            skipped: [skip({ path: 'private', reason: 'unreadable' })],
          })
        : graph(),
    ) as unknown as KnowledgeGraphLoader

    renderEmbedNote({ content: '![[private/plan]]', loadGraph })

    // The FIRST paint's transient default is ALSO `indeterminate` (no
    // resolver until collectionId resolves), so a bare `findByTestId` would
    // pass here even with the skip-list cross-check deleted — `waitFor`
    // re-asserts the PAIRED negative until the mount has genuinely settled,
    // which the transient default cannot satisfy by construction (it never
    // sets `data-kb-embed-reason`).
    await waitFor(() => {
      const el = screen.getByTestId('markdown-link')
      expect(el.getAttribute('data-kb-embed-state')).toBe('indeterminate')
      expect(el.getAttribute('data-kb-unresolved')).toBeNull()
      expect(el.getAttribute('title') ?? '').toMatch(/could not read/i)
    })
  })

  it('still renders the ordinary missing-file marker when NO skip explains the absence (EMB-021’s pairing)', async () => {
    const loadGraph = vi.fn(async (req: { kind: string }) =>
      req.kind === 'links'
        ? graph({
            kind: 'links',
            nodes: [{ path: 'ghost.pdf', exists: false }],
            edges: [embedEdge({ to_path: 'ghost.pdf', link_text: 'ghost.pdf', resolution: 'unresolved' })],
          })
        : graph(),
    ) as unknown as KnowledgeGraphLoader

    renderEmbedNote({ content: '![[ghost.pdf]]', loadGraph })

    // `data-kb-unresolved="true"` is produced ONLY by `UnresolvedLink`, never
    // by the transient first-paint `indeterminate` default — so this settles
    // on the terminal state without racing it.
    await waitFor(() => {
      const el = screen.getByTestId('markdown-link')
      expect(el.getAttribute('data-kb-unresolved')).toBe('true')
      expect(el.getAttribute('data-kb-embed-state')).toBeNull()
      expect(el.textContent ?? '').toContain('ghost.pdf')
    })
  })

  it('does not suppress absence for a near-miss skip naming a SIBLING directory (EMB-021, B5e)', async () => {
    const loadGraph = vi.fn(async (req: { kind: string }) =>
      req.kind === 'links'
        ? graph({
            kind: 'links',
            nodes: [{ path: 'private/plan.md', exists: false }],
            edges: [
              embedEdge({ to_path: 'private/plan.md', link_text: 'private/plan', resolution: 'unresolved' }),
            ],
            skipped: [skip({ path: 'public', reason: 'unreadable' })],
          })
        : graph(),
    ) as unknown as KnowledgeGraphLoader

    renderEmbedNote({ content: '![[private/plan]]', loadGraph })

    await waitFor(() => {
      const el = screen.getByTestId('markdown-link')
      expect(el.getAttribute('data-kb-unresolved')).toBe('true')
      expect(el.textContent ?? '').toContain('private/plan')
    })
  })

  it('distinguishes a containment refusal from a missing file via unresolved_reason, and does not show the escaping path (EMB-017/023/024)', async () => {
    const loadGraph = vi.fn(async (req: { kind: string }) =>
      req.kind === 'links'
        ? graph({
            kind: 'links',
            nodes: [],
            edges: [
              embedEdge({
                to_path: '../../etc/passwd',
                link_text: '../../etc/passwd',
                resolution: 'unresolved',
                unresolved_reason: 'outside_root',
              }),
            ],
          })
        : graph(),
    ) as unknown as KnowledgeGraphLoader

    renderEmbedNote({ content: '![[../../etc/passwd]]', loadGraph })

    // `data-kb-embed-outside-root` is produced only by `ContainmentRefusedEmbed`
    // (the terminal state), never by the transient first-paint default.
    await waitFor(() => {
      const el = screen.getByTestId('markdown-link')
      expect(el.getAttribute('data-kb-embed-outside-root')).toBe('true')
      expect(el.getAttribute('data-kb-unresolved')).toBeNull()
      expect(el.getAttribute('title') ?? '').not.toContain('etc/passwd')
    })
  })

  it('treats a node/edge disagreement as indeterminate rather than believing either (EMB-022)', async () => {
    const loadGraph = vi.fn(async (req: { kind: string }) =>
      req.kind === 'links'
        ? graph({
            kind: 'links',
            // The edge resolved via exact_path, but the node list says the
            // target does not exist — the two disagree.
            nodes: [{ path: 'diagram.png', exists: false }],
            edges: [embedEdge({ to_path: 'diagram.png', link_text: 'diagram.png', resolution: 'exact_path' })],
          })
        : graph(),
    ) as unknown as KnowledgeGraphLoader

    renderEmbedNote({ content: '![[diagram.png]]', loadGraph })

    // DIES ON: dropping the guard and mounting the image anyway (the reason
    // text is checked too — the transient first-paint default is ALSO
    // `indeterminate`, but its reason is "no reason available", never a
    // sentence naming the disagreement).
    await waitFor(() => {
      const el = screen.getByTestId('markdown-link')
      expect(el.getAttribute('data-kb-embed-state')).toBe('indeterminate')
      expect(el.getAttribute('title') ?? '').toMatch(/disagreed with itself/i)
    })
    expect(document.querySelector('img')).toBeNull()
  })

  it('resolves two embeds of the same file under DIFFERENT headings INDEPENDENTLY — the live bug this work fixes', async () => {
    // Before this change, `resolveEmbedUrl`'s match key ignored the heading
    // fragment entirely, so both embeds below would have resolved off
    // whichever edge `.find()` happened to hit first.
    //
    // The two edges are made to disagree in outcome (A resolves, B does not)
    // — not just in `to_path` — so a match key that ignores `heading` is
    // CAUGHT: it would match BOTH embeds to the first edge in array order and
    // both would read as resolved, which a same-outcome fixture cannot show.
    const loadGraph = vi.fn(async (req: { kind: string }) =>
      req.kind === 'links'
        ? graph({
            kind: 'links',
            nodes: [{ path: 'Tasks.base', exists: true }],
            edges: [
              embedEdge({ to_path: 'Tasks.base', link_text: 'Tasks.base', heading: 'A', resolution: 'exact_path' }),
              embedEdge({ to_path: 'Tasks.base', link_text: 'Tasks.base', heading: 'B', resolution: 'unresolved' }),
            ],
          })
        : graph(),
    ) as unknown as KnowledgeGraphLoader

    renderEmbedNote({ content: '![[Tasks.base#A]] and ![[Tasks.base#B]]', loadGraph })

    // `data-kb-state="resolved"` / `data-kb-unresolved="true"` are each
    // produced only by their own terminal render, never by the shared
    // transient default (`indeterminate`) — this settles once both embeds
    // have independently reached their correct, DIFFERENT verdicts.
    await waitFor(() => {
      const links = screen.getAllByTestId('markdown-link')
      expect(links).toHaveLength(2)
      expect(links[0]?.getAttribute('data-kb-state')).toBe('resolved')
      expect(links[1]?.getAttribute('data-kb-unresolved')).toBe('true')
    })
  })

  it('reports an ambiguous resolution and names the alternatives instead of staying quiet (EMB-018)', async () => {
    const loadGraph = vi.fn(async (req: { kind: string }) =>
      req.kind === 'links'
        ? graph({
            kind: 'links',
            nodes: [{ path: 'archive/report.pdf', exists: true }],
            edges: [
              embedEdge({
                to_path: 'archive/report.pdf',
                link_text: 'report.pdf',
                resolution: 'unique_basename',
                ambiguous: true,
                candidates: ['archive/report.pdf', 'notes/report.pdf'],
              }),
            ],
          })
        : graph(),
    ) as unknown as KnowledgeGraphLoader

    renderEmbedNote({ content: '![[report.pdf]]', loadGraph })

    await waitFor(() => {
      const el = screen.getByTestId('markdown-link')
      expect(el.getAttribute('data-kb-embed-ambiguous')).toBe('')
      expect(el.textContent ?? '').toContain('notes/report.pdf')
    })
  })
})

describe('KnowledgeNoteView — transclusion heading honesty (ADR-083 EMB-035/EMB-038/EMB-039)', () => {
  const KB_INFO = {
    'notes/vault': info({ root_path: 'notes/vault', is_knowledge_base: true, collection_id: COLLECTION }),
  }

  function renderEmbedNote(opts: { content: string; loadGraph: KnowledgeGraphLoader; loadOutline?: KnowledgeOutlineLoader }) {
    const loadOutline = opts.loadOutline ?? vi.fn().mockResolvedValue(outline())
    const loadInfo = detectionOf(KB_INFO)
    return renderView({ loadOutline, loadInfo, loadGraph: opts.loadGraph, content: opts.content })
  }

  it('reports "no such heading" and lists the headings that DO exist, fetching the target’s outline exactly once (EMB-016/EMB-038)', async () => {
    const loadOutline = vi.fn(async ({ path }: { workspaceId: string; path: string }) => {
      if (path === 'notes/vault/target-note.md') {
        return outline({
          path: 'notes/vault/target-note.md',
          headings: [
            { level: 1, text: 'Intro', slug: 'intro' },
            { level: 1, text: 'Setup', slug: 'setup' },
          ],
        })
      }
      return outline()
    }) as unknown as KnowledgeOutlineLoader

    const loadGraph = vi.fn(async (req: { kind: string }) =>
      req.kind === 'links'
        ? graph({
            kind: 'links',
            nodes: [{ path: 'target-note.md', exists: true }],
            edges: [
              embedEdge({
                to_path: 'target-note.md',
                link_text: 'target-note.md',
                heading: 'Missing',
                heading_found: false,
                resolution: 'exact_path',
              }),
            ],
          })
        : graph(),
    ) as unknown as KnowledgeGraphLoader

    // Inline-mixed (other words share the paragraph) so this stays on the
    // already-covered CollectionLink/UnresolvedLink fallback path rather
    // than the rich transclusion mount — this describe block is about the
    // RESOLVER's verdict, not about what a standalone mount then fetches.
    renderEmbedNote({ content: '![[target-note.md#Missing]] and more text', loadGraph, loadOutline })

    await waitFor(() => {
      const el = screen.getByTestId('markdown-link')
      expect(el.getAttribute('data-kb-unresolved')).toBe('true')
      const detail = el.getAttribute('title') ?? ''
      expect(detail).toContain('no heading "Missing"')
      expect(detail).toContain('Intro')
      expect(detail).toContain('Setup')
    })

    const targetCalls = (loadOutline as unknown as { mock: { calls: [{ path: string }][] } }).mock.calls.filter(
      ([r]) => r.path === 'notes/vault/target-note.md',
    )
    expect(targetCalls).toHaveLength(1)
  })

  it('never consults heading_found for a `.base` target — the view label is untouched even when the server sets it false BY CONSTRUCTION (EMB-039)', async () => {
    const loadGraph = vi.fn(async (req: { kind: string }) =>
      req.kind === 'links'
        ? graph({
            kind: 'links',
            nodes: [{ path: 'Tasks.base', exists: true }],
            edges: [
              embedEdge({
                to_path: 'Tasks.base',
                link_text: 'Tasks.base',
                heading: 'Needs Daniel',
                heading_found: false,
                resolution: 'exact_path',
              }),
            ],
          })
        : graph(),
    ) as unknown as KnowledgeGraphLoader

    renderEmbedNote({ content: '![[Tasks.base#Needs Daniel]] and more text', loadGraph })

    await waitFor(() => {
      const el = screen.getByTestId('markdown-link')
      expect(el.getAttribute('data-kb-state')).toBe('resolved')
      expect(el.getAttribute('data-kb-unresolved')).not.toBe('true')
    })
  })

  it('does not gate on heading_found for a block reference — it is meaningless there by the wire’s own contract (EMB-039)', async () => {
    const loadGraph = vi.fn(async (req: { kind: string }) =>
      req.kind === 'links'
        ? graph({
            kind: 'links',
            nodes: [{ path: 'target-note.md', exists: true }],
            edges: [
              embedEdge({
                to_path: 'target-note.md',
                link_text: 'target-note.md',
                block: 'abc123',
                heading_found: false,
                resolution: 'exact_path',
              }),
            ],
          })
        : graph(),
    ) as unknown as KnowledgeGraphLoader

    renderEmbedNote({ content: '![[target-note.md#^abc123]] and more text', loadGraph })

    await waitFor(() => {
      const el = screen.getByTestId('markdown-link')
      expect(el.getAttribute('data-kb-state')).toBe('resolved')
    })
  })

  // WAS: "resolves normally when heading_found is simply absent — the field is
  // not yet required on the wire". That premise is now FALSE: heading_found is
  // `required` on KnowledgeGraphEdge, so an edge cannot omit it, and a payload
  // that does is rejected by the SPA's zod validation rather than reaching this
  // code with the field missing. The hazard the old pin guarded — absence being
  // read as "not found" — is therefore unreachable by construction.
  //
  // Kept, not deleted, with its premise corrected: the positive half still
  // needs a guard, because heading_found=true must resolve and NOT fall into
  // the refusal branch its sibling tests cover.
  it('resolves when heading_found is true — a found heading must never take the refusal branch', async () => {
    const loadGraph = vi.fn(async (req: { kind: string }) =>
      req.kind === 'links'
        ? graph({
            kind: 'links',
            nodes: [{ path: 'target-note.md', exists: true }],
            edges: [
              embedEdge({
                to_path: 'target-note.md',
                link_text: 'target-note.md',
                heading: 'Intro',
                heading_found: true,
                resolution: 'exact_path',
              }),
            ],
          })
        : graph(),
    ) as unknown as KnowledgeGraphLoader

    renderEmbedNote({ content: '![[target-note.md#Intro]] and more text', loadGraph })

    await waitFor(() => {
      const el = screen.getByTestId('markdown-link')
      expect(el.getAttribute('data-kb-state')).toBe('resolved')
    })
  })
})
