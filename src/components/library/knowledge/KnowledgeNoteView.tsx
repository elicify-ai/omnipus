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
  KnowledgeGraphEdge,
  KnowledgeGraphNode,
  KnowledgeGraphResponse,
  KnowledgeGraphSkip,
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
import { WIKILINK_RE, type EmbedResolution, type KbLinkResolution } from '../preview/knowledgeMarkdown'
import { libraryEntryExt } from '../preview/libraryPreviewKind'

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

// ─────────────────────────────────────────────────────────────────────────────
// The embed resolver (ADR-083 EMB-011, EMB-012, EMB-013, EMB-014, EMB-016
// through EMB-024, EMB-029) — the honesty guarantee for `![[…]]` embeds.
//
// Extends the graph-backed `resolveEmbedUrl` memo below rather than adding a
// second resolver: `resolveWikilink`, the sibling memo just above it, answers
// the SAME question for plain `[[…]]` links and must not diverge from this
// one's verdicts, but it is out of this file's task scope today and is left
// exactly as it was.
// ─────────────────────────────────────────────────────────────────────────────

/** Collection-relative path, forward-slash separated, no leading/trailing
 *  slash and no `./` segments — the same normalisation the graph itself uses
 *  for `to_path`/`skipped[].path`, applied defensively on the client so a
 *  stray backslash or double slash never breaks a comparison. */
function normalizeCollectionPath(p: string): string {
  return p
    .replace(/\\/g, '/')
    .split('/')
    .filter((seg) => seg !== '' && seg !== '.')
    .join('/')
}

/**
 * `libraryDownloadUrl` returns a path relative to the SPA's own origin
 * (`BASE_URL` is `''` on a normal install — `src/lib/api.ts`), but the image
 * this embed resolves to is handed to CHAT'S inherited `img` slot
 * (`MarkdownImage`/`isSafeHref`, `src/components/chat/`), which calls
 * `new URL(href)` with NO base — a relative URL throws there and is judged
 * unsafe, degrading a working embed to a muted "[image: alt]" placeholder.
 * That gate belongs to chat, not to this file, so the fix here is on OUR
 * side of the boundary: hand it an absolute URL, the same
 * `BASE_URL || window.location.origin` fallback `src/lib/ws.ts` already uses
 * for the identical empty-`BASE_URL` case. */
function toAbsoluteEmbedUrl(relativeOrAbsolute: string): string {
  if (typeof window === 'undefined') return relativeOrAbsolute
  try {
    return new URL(relativeOrAbsolute, window.location.origin).toString()
  } catch {
    return relativeOrAbsolute
  }
}

function withoutMarkdownExt(basename: string): string {
  return basename.replace(/\.mdx?$/i, '')
}

function basenamesMatch(a: string, b: string): boolean {
  return a === b || withoutMarkdownExt(a) === withoutMarkdownExt(b)
}

const SKIP_REASON_TEXT: Record<KnowledgeGraphSkip['reason'], string> = {
  symlink: 'a symbolic link Omnipus did not follow',
  outside_root: 'a target outside the collection root',
  unreadable: 'a file or folder Omnipus could not read',
  not_addressable: 'a name Omnipus cannot address on this platform',
  node_limit: 'the neighbourhood node limit was reached before reaching it',
  hop_limit: 'the neighbourhood hop limit was reached before reaching it',
}

function describeSkip(skip: KnowledgeGraphSkip): string {
  const base = SKIP_REASON_TEXT[skip.reason] ?? skip.reason
  return skip.detail ? `${base} (${skip.detail})` : base
}

/**
 * EMB-021: before a missing edge or an unresolved edge is read as confident
 * absence, check whether the walk itself skipped this target — a directory
 * it could not list takes every file beneath it with it (its own §"EMB-021
 * rationale"), so a plain path/basename comparison against the skip's own
 * path misses the dominant real-world shape. Three clauses, in order, ANY of
 * which is a match; the first one found is returned (there is at most one in
 * practice — the skip list does not contain overlapping directory skips).
 *
 * Clause 3 (ancestor prefix) matches ONLY on a full path-segment boundary —
 * `notes/priv` must never suppress a target under `notes/private/` — and
 * MUST NOT be satisfied by a bare basename match, which is clause 2's job
 * and covers a different case (a bare wikilink naming no folder at all).
 */
export function findSkipForTarget(
  skipped: readonly KnowledgeGraphSkip[],
  targetPath: string,
): KnowledgeGraphSkip | undefined {
  const target = normalizeCollectionPath(targetPath)
  const targetBase = basenameOf(target)
  for (const skip of skipped) {
    const skipPath = normalizeCollectionPath(skip.path)
    if (skipPath === target) return skip // 1. path equality
    if (basenamesMatch(basenameOf(skipPath), targetBase)) return skip // 2. basename equality
    if (target.startsWith(`${skipPath}/`)) return skip // 3. ancestor prefix, segment-bounded
  }
  return undefined
}

/** EMB-011's match key: an embed edge and a plain-link edge to the identical
 *  target are different facts, so `embed` is checked first, then the written
 *  target (link_text, the resolved to_path, or its basename — the same
 *  fallback ladder `resolveWikilink` already used), then the heading
 *  fragment, then the block anchor. `heading` and `block` are mutually
 *  exclusive on the wire (CW-2, ADR-083 EMB-036) so comparing both is
 *  never redundant: a `[[Tasks.base#A]]` and a `[[Tasks.base#B]]` embed of
 *  one file now resolve independently, which they could not before this
 *  key existed. */
function edgeMatchesEmbedKey(
  edge: KnowledgeGraphEdge,
  target: string,
  heading: string | undefined,
  block: string | undefined,
): boolean {
  if (edge.embed !== true) return false
  const targetMatches =
    edge.link_text === target || edge.to_path === target || basenameOf(edge.to_path) === target
  if (!targetMatches) return false
  if ((edge.heading ?? undefined) !== (heading ?? undefined)) return false
  if ((edge.block ?? undefined) !== (block ?? undefined)) return false
  return true
}

function findMatchingEmbedEdges(
  graph: KnowledgeGraphResponse,
  target: string,
  heading: string | undefined,
  block: string | undefined,
): KnowledgeGraphEdge[] {
  return graph.edges.filter((e) => edgeMatchesEmbedKey(e, target, heading, block))
}

/** ADR-083 EMB-039: `heading_found` is meaningful ONLY for a markdown target
 *  carrying a heading fragment — it is false BY CONSTRUCTION for a `.base`
 *  target (where the fragment is a view label, not a heading) and for a
 *  block reference. Consulting it outside that gate renders the "no such
 *  heading" marker across every dashboard embed. */
function isMarkdownTarget(path: string): boolean {
  const ext = libraryEntryExt(path)
  return ext === 'md' || ext === 'markdown'
}

/**
 * The core of the embed resolver: given a graph answer already known to have
 * loaded (loading/graph_unavailable are handled by the caller, one level up,
 * because they are facts about the REQUEST, not about any one embed), decide
 * what this specific embed's evidence supports.
 *
 * `outlineForMissingHeading` answers EMB-016/EMB-038 for the one case that
 * needs a second fact: a markdown target whose named heading the graph says
 * it did NOT find. It is looked up only then — never speculatively — and its
 * absence (still loading, or the fetch failed) degrades to the reason with
 * no heading list rather than blocking the verdict.
 */
function resolveEmbedAgainstGraph(
  graph: KnowledgeGraphResponse,
  target: string,
  heading: string | undefined,
  block: string | undefined,
  toWorkspacePath: (p: string) => string,
  downloadUrl: (workspacePath: string) => string,
  workspaceId: string,
  outlineForMissingHeading: (collectionPath: string) => string[] | undefined,
): EmbedResolution {
  const matches = findMatchingEmbedEdges(graph, target, heading, block)

  const truncatedCaveat = graph.truncated
    ? 'this answer was truncated before the walk finished, so absence is not proven'
    : undefined

  if (matches.length === 0) {
    // EMB-013 / EMB-021: no matching edge is NOT the same fact as "this file
    // does not exist" — check the skip list and the truncation flag before
    // saying anything.
    const skip = findSkipForTarget(graph.skipped, target)
    if (skip) return { state: 'indeterminate', reason: describeSkip(skip) }
    if (truncatedCaveat) return { state: 'indeterminate', reason: truncatedCaveat }
    return { state: 'indeterminate', reason: 'no reason available' }
  }

  const edge = matches[0]
  const matchAmbiguous = matches.length > 1
  const matchAlternates = matches.slice(1).map((e) => e.to_path)

  if (edge.resolution === 'unresolved') {
    // EMB-021 again, against the RESOLVED edge's own target this time — a
    // walk-level skip still produces a matching unresolved edge, it does not
    // produce zero edges (measured; see the requirement's own rationale).
    const skip = findSkipForTarget(graph.skipped, edge.to_path)
    if (skip) return { state: 'indeterminate', reason: describeSkip(skip) }
    if (truncatedCaveat) return { state: 'indeterminate', reason: truncatedCaveat }
    // EMB-017 / EMB-023 / EMB-024: a containment refusal is a different fact
    // from an ordinary missing file, and CW-2's `unresolved_reason` is the
    // only thing that can tell them apart — absent (a handler not yet
    // updated, or truly `no_match`), this reads as an ordinary miss, which
    // is the conservative, non-regressing default.
    if (edge.unresolved_reason === 'outside_root') {
      return { state: 'unresolved', outsideRoot: true, reason: 'this target is outside the collection root' }
    }
    return { state: 'unresolved', reason: `no file in this collection matches "${target}"` }
  }

  // EMB-022: the edge and the node list disagreeing means neither is to be
  // believed. This guard already existed for the plain URL check this
  // function replaces; it now also catches the reverse disagreement (an
  // edge reporting unresolved is handled above, so what remains here is a
  // RESOLVED edge whose node says it does not exist).
  const node = graph.nodes.find((n): n is KnowledgeGraphNode => n.path === edge.to_path)
  if (node && node.exists === false) {
    return {
      state: 'indeterminate',
      reason: `the link graph disagreed with itself about "${edge.to_path}" — treated as unverified`,
    }
  }

  // ADR-083 EMB-035/EMB-038/EMB-039 (US-4, consumed here for transclusion,
  // US-7): a markdown target's named heading may not exist even though the
  // FILE does. Gated strictly on markdown + a heading fragment + no block —
  // heading_found is false BY CONSTRUCTION for a `.base` target (its
  // fragment is a view label) and for a block reference, and consulting it
  // there would mark every dashboard embed "no such heading".
  if (heading && !block && isMarkdownTarget(edge.to_path) && edge.heading_found === false) {
    const available = outlineForMissingHeading(edge.to_path)
    const reason =
      available && available.length > 0
        ? `no heading "${heading}" in this note — headings that do exist: ${available.join(', ')}`
        : `no heading "${heading}" in this note`
    return { state: 'unresolved', reason }
  }

  const backendCandidates = edge.candidates ?? []
  const backendAmbiguous = edge.ambiguous === true && backendCandidates.length > 0
  const ambiguous = matchAmbiguous || backendAmbiguous

  return {
    state: 'resolved',
    path: edge.to_path,
    url: downloadUrl(toWorkspacePath(edge.to_path)),
    workspaceId,
    workspacePath: toWorkspacePath(edge.to_path),
    ...(ambiguous
      ? { ambiguous: true, candidates: backendAmbiguous ? backendCandidates : matchAlternates }
      : {}),
  }
}

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

  // ADR-083 EMB-038: an outline fetch for a target note ONLY when its named
  // heading was reported not found, and exactly once per distinct target —
  // never in advance, and never for the dominant case (heading found, or no
  // heading asked for at all). Mirrors the ancestor-dirs `useQueries` pattern
  // above for the same reason: a bounded, per-item set of independent fetches
  // rather than one query re-keyed on a list.
  const missingHeadingTargets = useMemo(() => {
    const graph = linksQuery.data
    if (!graph) return [] as string[]
    const set = new Set<string>()
    for (const e of graph.edges) {
      if (e.embed !== true) continue
      if (e.resolution === 'unresolved') continue
      if (!e.heading || e.block) continue
      if (e.heading_found !== false) continue
      if (!isMarkdownTarget(e.to_path)) continue
      set.add(e.to_path)
    }
    return Array.from(set)
  }, [linksQuery.data])

  const missingHeadingOutlineQueries = useQueries({
    queries: missingHeadingTargets.map((collectionPath) => ({
      queryKey: ['library', 'knowledge', 'outline', workspaceId, 'embed-heading-check', collectionPath],
      queryFn: () => loadOutline({ workspaceId, path: toWorkspacePath(collectionPath) }),
      enabled: collectionId !== undefined,
      retry: false,
      refetchOnWindowFocus: false,
    })),
  })

  const outlineForMissingHeading = useMemo(() => {
    const map = new Map<string, string[]>()
    missingHeadingTargets.forEach((collectionPath, i) => {
      const headings = missingHeadingOutlineQueries[i]?.data?.headings
      if (headings) map.set(collectionPath, headings.map((h) => h.text))
    })
    return (collectionPath: string): string[] | undefined => map.get(collectionPath)
  }, [missingHeadingTargets, missingHeadingOutlineQueries])

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

  // Does this note contain wikilink/embed NOTATION at all? Needed only for
  // EMB-014's second clause: an empty graph answer (no edges, no skips) is
  // the ordinary, unremarkable shape for a note with no links — it must trip
  // the page-level "no link information" statement ONLY when the note
  // plainly has notation the graph should have had something to say about.
  // A fresh, non-global RegExp avoids `WIKILINK_RE`'s own shared `lastIndex`.
  const contentHasWikilinkNotation = useMemo(
    () => new RegExp(WIKILINK_RE.source, 'g').test(content),
    [content],
  )

  // EMB-014: exactly one page-level statement — never one could-not-be-checked
  // marker per embed — when the graph request failed, or when it succeeded
  // with nothing to say about a note that plainly has links to check. Gated
  // to `rootStatus === 'ready'`: the separate "linked mentions unavailable"
  // notice above already covers the "root could not be identified" case, and
  // this must not restate it as a second, differently-worded banner.
  const graphAnswerIssue = useMemo<{ message: string } | undefined>(() => {
    if (collectionId === undefined || rootStatus !== 'ready') return undefined
    if (linksQuery.isError) {
      const detail = linksQuery.error instanceof Error ? linksQuery.error.message : String(linksQuery.error)
      return { message: `Omnipus could not check this note's links and embeds: ${detail}` }
    }
    const graph = linksQuery.data
    if (
      linksQuery.isSuccess &&
      graph &&
      graph.edges.length === 0 &&
      graph.skipped.length === 0 &&
      contentHasWikilinkNotation
    ) {
      // EMB-014's own wording, verbatim — and deliberately not a diagnosis of
      // WHY the answer was empty (m8): the identical shape is also what a
      // perfectly valid, simply-not-yet-indexed note produces.
      return { message: 'the knowledge base returned no link information for this note' }
    }
    return undefined
  }, [
    collectionId,
    rootStatus,
    linksQuery.isError,
    linksQuery.error,
    linksQuery.isSuccess,
    linksQuery.data,
    contentHasWikilinkNotation,
  ])

  // The embed resolver (ADR-083 EMB-011 through EMB-024). Offered whenever
  // this note IS in a detected collection — unlike `resolveWikilink` above,
  // it is not withheld while the graph is in flight, because the reader
  // needs to tell `loading` apart from `graph_unavailable` from
  // `indeterminate`, and an absent function cannot report which.
  const resolveEmbedUrl = useMemo(() => {
    if (collectionId === undefined) return undefined
    return (target: string, heading?: string, block?: string): EmbedResolution => {
      if (rootStatus === 'unavailable') {
        return {
          state: 'graph_unavailable',
          reason: 'Omnipus could not identify which folder this collection starts at',
        }
      }
      if (rootStatus !== 'ready' || linksQuery.isPending) return { state: 'loading' }
      if (linksQuery.isError) {
        return {
          state: 'graph_unavailable',
          reason:
            linksQuery.error instanceof Error ? linksQuery.error.message : 'the link graph request failed',
        }
      }
      const graph = linksQuery.data
      if (!graph) return { state: 'loading' }
      if (graph.edges.length === 0 && graph.skipped.length === 0 && contentHasWikilinkNotation) {
        return {
          state: 'graph_unavailable',
          reason: 'the knowledge base returned no link information for this note',
        }
      }
      return resolveEmbedAgainstGraph(
        graph,
        target,
        heading,
        block,
        toWorkspacePath,
        (p) => toAbsoluteEmbedUrl(libraryDownloadUrl(workspaceId, p)),
        workspaceId,
        outlineForMissingHeading,
      )
    }
  }, [
    collectionId,
    rootStatus,
    linksQuery.isPending,
    linksQuery.isError,
    linksQuery.error,
    linksQuery.data,
    contentHasWikilinkNotation,
    toWorkspacePath,
    workspaceId,
    outlineForMissingHeading,
  ])

  return (
    <div data-testid="knowledge-note-view" className="w-full">
      {graphAnswerIssue ? (
        <div
          data-testid="knowledge-graph-unavailable"
          className="mb-3 flex items-center justify-between gap-3 rounded border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 px-3 py-2 text-xs leading-snug text-[var(--color-warning)]"
        >
          <span>{graphAnswerIssue.message}</span>
          <button
            type="button"
            onClick={() => void linksQuery.refetch()}
            className="shrink-0 rounded border border-current px-2 py-1 text-[10px] uppercase tracking-wide hover:opacity-80"
          >
            Retry
          </button>
        </div>
      ) : null}
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
