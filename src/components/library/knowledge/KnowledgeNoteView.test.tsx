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
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import { KnowledgeNoteView, noteAncestorDirs } from './KnowledgeNoteView'
import type { KnowledgeGraphLoader } from './KnowledgeBacklinks'
import type { KnowledgeOutlineLoader } from './KnowledgeOutline'
import type {
  KnowledgeBaseInfo,
  KnowledgeGraphResponse,
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
                from_path: 'architecture/sandboxing.md',
                to_path: 'Ghost.md',
                link_text: 'Ghost',
                resolution: 'unresolved',
                ambiguous: false,
              },
              {
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
