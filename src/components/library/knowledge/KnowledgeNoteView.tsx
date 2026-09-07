// KnowledgeNoteView — the wiring that makes the note-reading surface REACHABLE.
//
// ADR-067 US-7, FR-060..FR-065, FR-012, FR-062, FR-063.
//
// ── Why this file exists ─────────────────────────────────────────────────────
// KnowledgeReader, KnowledgeOutline, KnowledgeBacklinks and the KB markdown
// composition were all written, all tested, and imported by NOTHING outside
// their own test files. Every assertion about wikilinks, callouts, highlights,
// frontmatter suppression, the outline, linked mentions, unresolved-link
// inertness and `../../etc/passwd` containment was made against code that was
// not in the product: clicking a search hit opened the STAGE-1 markdown view,
// where `[[Wikilinks]]` are literal text and frontmatter is drawn as a rule and
// a heading. This component is the missing seam between the Library's preview
// pane and those four files.
//
// ── What it owns ─────────────────────────────────────────────────────────────
// Fetching, and the two path translations that fetching forces. It renders no
// markup of its own beyond the reader's own layout.
//
//   1. THE OUTLINE, for any markdown file (FR-062). One call answers three
//      questions at once — the headings, whether this file is inside a
//      knowledge base, and which collection — so it is what decides the shape
//      of the whole pane.
//   2. THE LINK GRAPH of this note (`kind=links`), knowledge bases only. It is
//      what lets a wikilink be marked resolved or unresolved HONESTLY: until it
//      arrives, `resolveWikilink` is deliberately NOT passed, so every wikilink
//      renders `unknown` — a visibly unverified link — rather than being
//      asserted to work or asserted to be broken on no evidence.
//   3. THE COLLECTION ROOT, which nothing on the wire reports.
//
// ── The collection root, and why it costs a walk ─────────────────────────────
// The graph endpoint speaks COLLECTION-relative paths; the Library address
// (FR-012) is WORKSPACE-relative. Converting between them needs the
// collection's own workspace-relative root, and no response carries it:
// KnowledgeOutline reports `collection_id` but not the root, and
// KnowledgeBaseInfo reports the root only for the folder you ask about.
//
// So this asks about the note's ancestor folders — in parallel, from the
// deepest up — and takes the one whose `collection_id` MATCHES the id the
// outline already reported. Matching on the id rather than on "the first
// ancestor that is a knowledge base" is what makes it an answer instead of a
// guess: nested collections and a collection reached through a symlink both
// come out right, and when nothing matches the linked-mentions panel is simply
// not offered rather than being pointed at a path invented here. Every request
// is a directory stat the gateway already serves cheaply, they share
// KnowledgePanel's own react-query cache key, and the count is bounded by the
// note's depth.
//
// This is a workaround for a missing field, and it should not outlive one: a
// `root_path` on KnowledgeOutline would replace the whole walk with nothing.
// That is a contract change, owned by the wave that owns contracts/.

import { useMemo } from 'react'
import { useQueries, useQuery } from '@tanstack/react-query'

import {
  fetchKnowledgeBaseInfo,
  fetchKnowledgeGraph,
  fetchKnowledgeOutline,
  libraryDownloadUrl,
} from '@/lib/api'
import type {
  KnowledgeBaseInfo,
  KnowledgeGraphResponse,
  KnowledgeOutline as KnowledgeOutlineResponse,
} from '@/lib/api/generated/openapi-types'

import { KnowledgeReader } from './KnowledgeReader'
import { KnowledgeOutline, type KnowledgeOutlineLoader } from './KnowledgeOutline'
import {
  KnowledgeBacklinks,
  collectionPathToWorkspacePath,
  libraryNoteHref,
  type KnowledgeGraphLoader,
} from './KnowledgeBacklinks'
import type { KbLinkResolution } from '../preview/knowledgeMarkdown'

/**
 * The note's ancestor folders, DEEPEST FIRST, ending with the work-tree root
 * (''). `notes/vault/a/n.md` → ['notes/vault/a', 'notes/vault', 'notes', ''].
 *
 * Deepest first matters: the nearest enclosing collection is the note's own
 * collection, and a nested collection must not be attributed to its parent.
 */
export function noteAncestorDirs(notePath: string): string[] {
  const parts = notePath.split('/').filter((p) => p !== '')
  parts.pop() // the file itself
  const out: string[] = []
  for (let i = parts.length; i >= 0; i--) out.push(parts.slice(0, i).join('/'))
  return out
}

/** Where the collection-root lookup has got to. `unavailable` is a real answer
 *  — the walk finished and nothing matched — and is rendered as "linked
 *  mentions are not available for this note", never as an empty list. */
export type CollectionRootStatus = 'idle' | 'pending' | 'ready' | 'unavailable'

export interface KnowledgeNoteViewProps {
  workspaceId: string
  /** Workspace-relative path of the open markdown file. */
  notePath: string
  /** Markdown source. The live editor draft when one is open, so the reading
   *  view reflects what is being typed. */
  content: string
  /** Open another note, workspace-relative. */
  onOpenNote?: (workspacePath: string) => void
  /** Force the reader's layout; `auto` measures. */
  layout?: 'auto' | 'wide' | 'docked'
  /** Test seams. Production uses the shared clients. */
  loadOutline?: KnowledgeOutlineLoader
  loadGraph?: KnowledgeGraphLoader
  loadInfo?: (workspaceId: string, path: string) => Promise<KnowledgeBaseInfo>
}

const defaultLoadOutline: KnowledgeOutlineLoader = ({ workspaceId, path }) =>
  fetchKnowledgeOutline(workspaceId, path)

const defaultLoadGraph: KnowledgeGraphLoader = ({ workspaceId, collectionId, kind, path, hops, limit }) =>
  fetchKnowledgeGraph(workspaceId, {
    collectionId,
    kind,
    ...(path === undefined ? {} : { path }),
    ...(hops === undefined ? {} : { hops }),
    ...(limit === undefined ? {} : { limit }),
  })

export function KnowledgeNoteView({
  workspaceId,
  notePath,
  content,
  onOpenNote,
  layout = 'auto',
  loadOutline = defaultLoadOutline,
  loadGraph = defaultLoadGraph,
  loadInfo = fetchKnowledgeBaseInfo,
}: KnowledgeNoteViewProps) {
  // ── 1. The outline: headings, and the knowledge-base verdict ──────────────
  const outlineQuery = useQuery<KnowledgeOutlineResponse>({
    queryKey: ['library', 'knowledge', 'outline', workspaceId, notePath],
    queryFn: () => loadOutline({ workspaceId, path: notePath }),
  })

  const collectionId = outlineQuery.data?.is_knowledge_base
    ? outlineQuery.data.collection_id
    : undefined

  // ── 2. The collection root, by matching id up the ancestor chain ──────────
  const ancestors = useMemo(() => noteAncestorDirs(notePath), [notePath])
  const ancestorQueries = useQueries({
    queries: ancestors.map((dir) => ({
      // The SAME key KnowledgePanel uses, so browsing to the folder and opening
      // a note in it cost one request between them, not two.
      queryKey: ['knowledge-base-info', workspaceId, dir],
      queryFn: () => loadInfo(workspaceId, dir),
      enabled: collectionId !== undefined,
      retry: false,
      refetchOnWindowFocus: false,
    })),
  })

  const { collectionRoot, rootStatus } = useMemo<{
    collectionRoot?: string
    rootStatus: CollectionRootStatus
  }>(() => {
    if (collectionId === undefined) return { rootStatus: 'idle' }
    for (let i = 0; i < ancestors.length; i++) {
      const q = ancestorQueries[i]
      if (q?.data?.collection_id === collectionId) {
        return { collectionRoot: ancestors[i], rootStatus: 'ready' }
      }
    }
    const settled = ancestorQueries.every((q) => q.isSuccess || q.isError)
    return { rootStatus: settled ? 'unavailable' : 'pending' }
  }, [collectionId, ancestors, ancestorQueries])

  const collectionNotePath =
    collectionRoot === undefined
      ? notePath
      : collectionRoot === ''
        ? notePath
        : notePath.slice(collectionRoot.length + 1)

  // ── 3. This note's outbound links, for honest wikilink resolution ─────────
  const linksQuery = useQuery<KnowledgeGraphResponse>({
    queryKey: ['library', 'knowledge', 'graph', 'links', workspaceId, collectionId, collectionNotePath],
    queryFn: () =>
      loadGraph({
        workspaceId,
        collectionId: collectionId as string,
        kind: 'links',
        path: collectionNotePath,
      }),
    enabled: collectionId !== undefined && rootStatus === 'ready',
    retry: false,
  })

  /** Collection-relative → the Library address for that file. Outside a
   *  knowledge base the two path spaces are already the same. */
  const toWorkspacePath = useMemo(
    () =>
      (p: string): string =>
        collectionRoot === undefined ? p : collectionPathToWorkspacePath(collectionRoot, p),
    [collectionRoot],
  )

  const linkHref = useMemo(
    () =>
      (p: string): string =>
        libraryNoteHref(workspaceId, toWorkspacePath(p)),
    [workspaceId, toWorkspacePath],
  )

  const navigate = useMemo(
    () =>
      onOpenNote
        ? (p: string) => onOpenNote(toWorkspacePath(p))
        : undefined,
    [onOpenNote, toWorkspacePath],
  )

  // Passed ONLY when the graph has actually answered. While it has not, the
  // prop is absent and every wikilink renders `unknown` — see
  // knowledgeMarkdown's KbLinkState: claiming a link is broken because the
  // evidence has not arrived is the same error as claiming it works.
  const resolveWikilink = useMemo(() => {
    const graph = linksQuery.data
    if (!graph) return undefined
    return (target: string): KbLinkResolution => {
      const edge = graph.edges.find(
        (e) => e.link_text === target || e.to_path === target || basenameOf(e.to_path) === target,
      )
      if (!edge) return { state: 'unknown' }
      if (edge.resolution === 'unresolved') return { state: 'unresolved' }
      const node = graph.nodes.find((n) => n.path === edge.to_path)
      if (node && node.exists === false) return { state: 'unresolved' }
      return { state: 'resolved', path: edge.to_path }
    }
  }, [linksQuery.data])

  const resolveEmbedUrl = useMemo(() => {
    const graph = linksQuery.data
    if (!graph) return undefined
    return (target: string): string | undefined => {
      const edge = graph.edges.find(
        (e) => e.embed === true && (e.link_text === target || basenameOf(e.to_path) === target),
      )
      if (!edge) return undefined
      const node = graph.nodes.find((n) => n.path === edge.to_path)
      if (node && node.exists === false) return undefined
      return libraryDownloadUrl(workspaceId, toWorkspacePath(edge.to_path))
    }
  }, [linksQuery.data, workspaceId, toWorkspacePath])

  return (
    <div data-testid="knowledge-note-view" className="w-full">
      <KnowledgeReader
        content={content}
        path={collectionNotePath}
        layout={layout}
        {...(navigate ? { onNavigate: navigate } : {})}
        {...(resolveWikilink ? { resolveWikilink } : {})}
        {...(resolveEmbedUrl ? { resolveEmbedUrl } : {})}
        linkHref={linkHref}
        renderRails={({ collapsible, scrollToHeading }) => (
          <>
            <KnowledgeOutline
              workspaceId={workspaceId}
              path={notePath}
              loadOutline={loadOutline}
              onNavigate={(heading) => scrollToHeading(heading)}
              collapsible={collapsible}
            />
            {collectionId !== undefined && rootStatus === 'ready' && collectionRoot !== undefined ? (
              <KnowledgeBacklinks
                workspaceId={workspaceId}
                collectionId={collectionId}
                notePath={collectionNotePath}
                collectionRoot={collectionRoot}
                loadGraph={loadGraph}
                {...(onOpenNote ? { onOpenNote } : {})}
                collapsible={collapsible}
              />
            ) : null}
            {collectionId !== undefined && rootStatus === 'unavailable' ? (
              // Said out loud rather than rendered as an empty panel. "No note
              // links here" and "Omnipus could not work out where this
              // collection starts" are different facts and must not share a
              // rendering.
              <p
                data-testid="knowledge-backlinks-unavailable"
                className="px-3 py-2 text-xs leading-snug text-[var(--color-warning)]"
              >
                Linked mentions are unavailable for this note: Omnipus could not identify which
                folder this collection starts at, so it cannot ask which notes link here.
              </p>
            ) : null}
          </>
        )}
      />
    </div>
  )
}

function basenameOf(path: string): string {
  const parts = path.split('/')
  return parts[parts.length - 1] || path
}
