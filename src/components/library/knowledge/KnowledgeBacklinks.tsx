// KnowledgeBacklinks — the notes that point AT the open note (ADR-067 US-7
// AS-6, US-8 AS-2/AS-5, FR-063, FR-065, FR-054, FR-112; spec §6, §9).
//
// This is the half that makes a folder of markdown a knowledge base. It reads
// `GET /library/{workspace_id}/knowledge/graph?kind=backlinks`, whose response
// shape (KnowledgeGraphResponse) serves all five graph queries because they
// differ only in which subgraph is selected, not in what a link is.
//
// ── What this panel is not allowed to quietly drop ──────────────────────────
// The backend goes to real trouble to avoid a confidently incomplete answer,
// and every one of those signals arrives inside the same payload as the
// results. A reading surface that renders only `edges` and ignores the rest
// undoes all of it. So, in the panel body where the reader cannot miss them:
//
//   AMBIGUOUS (FR-041)   `edge.ambiguous` — more than one file matched the
//                        basename and the fixed tie-break ladder picked one.
//                        It resolved AND it is reported; "resolving it is not
//                        a licence to stay quiet about it". Every candidate is
//                        listed, in tie-break order.
//   UNRESOLVED (FR-065)  a node with `exists: false` is a link target that is
//                        not on disk. The schema is explicit: the client MUST
//                        mark it visibly and MUST NOT navigate on click. It is
//                        rendered as inert text with no href, so there is no
//                        link to middle-click either.
//   SKIPPED (FR-112)     `skipped[]` — symlinks the walk refused to follow,
//                        targets outside the collection root, unreadable
//                        files. "Reported rather than omitted: a file the
//                        system cannot address must be visible to the caller,
//                        never silently absent." An empty array is a positive
//                        statement that nothing was skipped, so the section
//                        simply does not appear.
//   TRUNCATED (FR-054)   `truncated` — a bound stopped the walk. Stated
//                        plainly, with whichever bound applied, "so a small
//                        graph is never mistaken for a clipped one".
//
// ── No percentages, ever ────────────────────────────────────────────────────
// While this query is in flight there is no total to divide by, so the waiting
// state is a sentence and not a bar. A progress bar needs a denominator, and
// inventing one is precisely the confidently-wrong answer the whole feature
// refuses. Same rule as KnowledgeOutline's loading state.
//
// ── FR-012 — a note is addressable by URL ───────────────────────────────────
// Every resolvable row is a real anchor whose href is the Library deep link
// for that note (`/#/library?workspace=…&path=…`, the address `library.tsx`
// already validates and turns back into a selection). That makes a backlink
// copyable, openable in a new tab, and reachable by the back button — not just
// a click handler. A plain left click is intercepted and handed to
// `onOpenNote` so the pane swaps in place; a modified click (new tab, new
// window) is left entirely alone, because overriding it would take away the
// one thing having a real URL bought.
//
// Paths in the graph are COLLECTION-relative while the Library address is
// WORKSPACE-relative, so the caller supplies `collectionRoot` (the collection's
// own workspace-relative path) and `collectionPathToWorkspacePath` joins them.
// Guessing that they are the same would produce a link that opens the wrong
// file — or nothing — for every collection that is not mounted at the
// workspace root.
//
// The loader is injected for the reason KnowledgeOutline.tsx records.

import { useMemo, useState } from "react";
import type { MouseEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { LinkBreak, WarningCircle } from "@phosphor-icons/react";
import { QueryErrorState } from "@/components/shared/QueryErrorState";
import {
  KnowledgeRailPanelHeader,
  type KnowledgeRailQualifier,
} from "./KnowledgeRailPanel";
import type {
  KnowledgeGraphResponse,
  KnowledgeGraphSkip,
} from "@/lib/api/generated/openapi-types";

/** Arguments for `GET /api/v1/library/{workspace_id}/knowledge/graph`. */
export interface KnowledgeGraphRequest {
  workspaceId: string;
  collectionId: string;
  kind: KnowledgeGraphResponse["kind"];
  /** Collection-relative path of the note the query is about. */
  path?: string;
  hops?: number;
  limit?: number;
}

/** The injected client for the graph endpoint. */
export type KnowledgeGraphLoader = (
  request: KnowledgeGraphRequest,
) => Promise<KnowledgeGraphResponse>;

export function knowledgeBacklinksQueryKey(
  workspaceId: string,
  collectionId: string,
  notePath: string,
) {
  return [
    "library",
    "knowledge",
    "graph",
    "backlinks",
    workspaceId,
    collectionId,
    notePath,
  ] as const;
}

/**
 * Joins a collection-relative path onto the collection's own workspace-relative
 * root, producing the `path` the Library address takes. An empty root means the
 * collection IS the workspace root, in which case the two are already the same.
 */
export function collectionPathToWorkspacePath(
  collectionRoot: string,
  collectionRelativePath: string,
): string {
  const root = collectionRoot.replace(/^\/+|\/+$/g, "");
  const rel = collectionRelativePath.replace(/^\/+/, "");
  return root === "" ? rel : `${root}/${rel}`;
}

/**
 * The Library deep link for one file (FR-012). Hash history — `main.tsx` builds
 * the router with `createHashHistory()` — so the address lives after the `#`,
 * exactly as `LibraryPanel`'s pop-out already writes it.
 */
export function libraryNoteHref(
  workspaceId: string,
  workspacePath: string,
): string {
  const params = new URLSearchParams({
    workspace: workspaceId,
    path: workspacePath,
  });
  return `/#/library?${params.toString()}`;
}

/** Plain-English rendering of every `KnowledgeGraphSkip.reason` (FR-112). */
const SKIP_REASON_TEXT: Record<KnowledgeGraphSkip["reason"], string> = {
  symlink: "Symbolic link — not followed",
  outside_root: "Outside the collection — not read",
  unreadable: "Could not be read",
  not_addressable: "Name cannot be represented on this system",
  node_limit: "Node limit reached",
  hop_limit: "Hop limit reached",
};

function basename(path: string): string {
  const parts = path.split("/");
  return parts[parts.length - 1] || path;
}

export interface KnowledgeBacklinksProps {
  workspaceId: string;
  /** The `KnowledgeBaseInfo.collection_id` this note belongs to. */
  collectionId: string;
  /** Collection-relative path of the open note. */
  notePath: string;
  /** The collection's own workspace-relative root path; '' when it is the workspace root. */
  collectionRoot: string;
  /** See KnowledgeOutline's header note — required, not defaulted. */
  loadGraph: KnowledgeGraphLoader;
  /** Open a backlink in place. Receives a WORKSPACE-relative path. */
  onOpenNote?: (workspacePath: string) => void;
  /** FR-064 / US-7 AS-9 — collapse to a toggle in a narrow docked rail. */
  collapsible?: boolean;
}

export function KnowledgeBacklinks({
  workspaceId,
  collectionId,
  notePath,
  collectionRoot,
  loadGraph,
  onOpenNote,
  collapsible = false,
}: KnowledgeBacklinksProps) {
  const [expanded, setExpanded] = useState(!collapsible);

  const query = useQuery({
    queryKey: knowledgeBacklinksQueryKey(workspaceId, collectionId, notePath),
    queryFn: () =>
      loadGraph({
        workspaceId,
        collectionId,
        kind: "backlinks",
        path: notePath,
      }),
  });

  const edges = useMemo(() => query.data?.edges ?? [], [query.data]);
  const skipped = useMemo(() => query.data?.skipped ?? [], [query.data]);
  const nodeByPath = useMemo(() => {
    const map = new Map<string, KnowledgeGraphResponse["nodes"][number]>();
    for (const node of query.data?.nodes ?? []) map.set(node.path, node);
    return map;
  }, [query.data]);

  // FR-054/FR-112 on the ALWAYS-VISIBLE row. The truncation notice and the
  // skipped-paths section below are inside the collapsed body; with
  // `collapsible` that body starts closed, and a docked reader would otherwise
  // see a bare "LINKED MENTIONS 200" — a confident count of a walk a bound
  // stopped. This file's own header requires the truncation be "stated plainly
  // … so a small graph is never mistaken for a clipped one"; a caveat behind a
  // disclosure is not stated at all.
  const qualifiers = useMemo<KnowledgeRailQualifier[]>(() => {
    const out: KnowledgeRailQualifier[] = [];
    if (query.data?.truncated) {
      const bounds = [
        query.data.node_limit_applied !== undefined
          ? `node limit ${query.data.node_limit_applied}`
          : "",
        query.data.hop_limit_applied !== undefined
          ? `hop limit ${query.data.hop_limit_applied}`
          : "",
      ].filter(Boolean);
      out.push({
        label: "truncated",
        detail:
          "A bound stopped the walk, so this count is of a clipped view rather than every " +
          `inbound link${bounds.length > 0 ? ` (${bounds.join(", ")})` : ""}.`,
      });
    }
    if (skipped.length > 0) {
      out.push({
        label: `${skipped.length} skipped`,
        detail:
          `${skipped.length} ${skipped.length === 1 ? "path was" : "paths were"} skipped while ` +
          "walking this collection and were not searched for links.",
      });
    }
    return out;
  }, [query.data, skipped]);

  return (
    <section
      data-testid="knowledge-backlinks"
      aria-label="Linked mentions"
      className="flex flex-col min-h-0"
    >
      <KnowledgeRailPanelHeader
        title="Linked mentions"
        count={query.data ? edges.length : undefined}
        collapsible={collapsible}
        expanded={expanded}
        onToggle={() => setExpanded((v) => !v)}
        testId="knowledge-backlinks-toggle"
        qualifiers={qualifiers}
      />

      {expanded && (
        <div className="flex flex-col min-h-0 overflow-y-auto">
          {query.isPending && (
            // Indeterminate. No bar, no ratio — there is no denominator here.
            <p
              data-testid="knowledge-backlinks-loading"
              className="px-3 py-2 text-xs text-[var(--color-muted)]"
            >
              Looking for notes that link here…
            </p>
          )}

          {query.isError && (
            <QueryErrorState
              layout="fill"
              testId="knowledge-backlinks-error"
              message="Could not read this note's linked mentions."
              onRetry={() => void query.refetch()}
            />
          )}

          {/* FR-054. Stated before the list, because it qualifies everything
              below it: what follows is a clipped view, not the whole graph. */}
          {query.data?.truncated && (
            <p
              data-testid="knowledge-backlinks-truncated"
              className="flex items-start gap-2 px-3 py-2 text-xs text-[var(--color-warning)]"
            >
              <WarningCircle
                size={14}
                className="mt-px shrink-0"
                aria-hidden="true"
              />
              <span>
                A bound stopped the walk, so this is a clipped view rather than
                every inbound link.
                {query.data.node_limit_applied !== undefined
                  ? ` Node limit: ${query.data.node_limit_applied}.`
                  : ""}
                {query.data.hop_limit_applied !== undefined
                  ? ` Hop limit: ${query.data.hop_limit_applied}.`
                  : ""}
              </span>
            </p>
          )}

          {query.isSuccess && edges.length === 0 && (
            <p
              data-testid="knowledge-backlinks-empty"
              className="px-3 py-2 text-xs text-[var(--color-muted)]"
            >
              No other note links to this one.
            </p>
          )}

          {edges.length > 0 && (
            <ul className="flex flex-col py-1">
              {edges.map((edge, i) => {
                const source = nodeByPath.get(edge.from_path);
                // A node the response never vouched for is treated exactly like
                // one it marked non-existent: shown, and not navigable. The
                // contract says `nodes` carries every node referenced by the
                // graph, so a gap is a broken answer — and the safe reading of
                // a broken answer is "do not send the reader to this path".
                const unresolved =
                  source?.exists !== true || edge.resolution === "unresolved";
                const workspacePath = collectionPathToWorkspacePath(
                  collectionRoot,
                  edge.from_path,
                );
                const label = source?.title?.trim() || basename(edge.from_path);

                const context = (
                  <>
                    <span className="block truncate text-[11px] text-[var(--color-muted)]">
                      {edge.from_path}
                    </span>
                    {edge.alias !== undefined && edge.alias !== "" && (
                      <span
                        data-testid="knowledge-backlink-alias"
                        className="block text-[11px] text-[var(--color-muted)]"
                      >
                        Refers to this note as “{edge.alias}”
                      </span>
                    )}
                    {edge.heading !== undefined && edge.heading !== "" && (
                      <span
                        data-testid="knowledge-backlink-heading"
                        className="block text-[11px] text-[var(--color-muted)]"
                      >
                        Links to the heading “{edge.heading}”
                      </span>
                    )}
                    {edge.embed === true && (
                      <span
                        data-testid="knowledge-backlink-embed"
                        className="block text-[11px] text-[var(--color-muted)]"
                      >
                        Embedded, not just linked
                      </span>
                    )}
                    {edge.ambiguous && (
                      <span
                        data-testid="knowledge-backlink-ambiguous"
                        className="mt-1 block text-[11px] text-[var(--color-warning)]"
                      >
                        Ambiguous link
                        {edge.link_text !== undefined && edge.link_text !== ""
                          ? `: “${edge.link_text}” matched more than one note`
                          : ": more than one note matched"}
                        . Resolved to {edge.to_path}
                        {edge.candidates !== undefined &&
                        edge.candidates.length > 0
                          ? `. Candidates, in tie-break order: ${edge.candidates.join(", ")}`
                          : ""}
                        .
                      </span>
                    )}
                    {unresolved && (
                      <span
                        data-testid="knowledge-backlink-unresolved"
                        className="mt-1 flex items-center gap-1 text-[11px] text-[var(--color-warning)]"
                      >
                        <LinkBreak size={12} aria-hidden="true" />
                        Unresolved — this note is not on disk, so it cannot be
                        opened
                      </span>
                    )}
                  </>
                );

                const rowClass = "block w-full px-3 py-1.5 text-left text-xs";

                return (
                  <li key={`${edge.from_path}-${edge.to_path}-${i}`}>
                    {unresolved ? (
                      // FR-065: no href at all, so there is nothing to click,
                      // middle-click, or copy as a link.
                      <span
                        data-testid="knowledge-backlink"
                        data-unresolved="true"
                        data-path={edge.from_path}
                        className={`${rowClass} text-[var(--color-muted)]`}
                      >
                        <span className="block truncate">{label}</span>
                        {context}
                      </span>
                    ) : (
                      <a
                        tabIndex={0}
                        data-testid="knowledge-backlink"
                        data-unresolved="false"
                        data-path={edge.from_path}
                        href={libraryNoteHref(workspaceId, workspacePath)}
                        onClick={(event: MouseEvent<HTMLAnchorElement>) => {
                          // Leave modified clicks to the browser — see header.
                          if (
                            event.metaKey ||
                            event.ctrlKey ||
                            event.shiftKey ||
                            event.altKey ||
                            event.button !== 0
                          ) {
                            return;
                          }
                          if (!onOpenNote) return;
                          event.preventDefault();
                          onOpenNote(workspacePath);
                        }}
                        className={`${rowClass} text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors`}
                      >
                        <span className="block truncate">{label}</span>
                        {context}
                      </a>
                    )}
                  </li>
                );
              })}
            </ul>
          )}

          {/* FR-112. Rendered whenever the walk reported anything, including
              alongside a full result list — a skipped file is not an error
              state, it is part of the answer. */}
          {skipped.length > 0 && (
            <div
              data-testid="knowledge-backlinks-skipped"
              className="border-t border-[var(--color-border)] px-3 py-2"
            >
              <p className="text-[11px] text-[var(--color-warning)]">
                {skipped.length === 1
                  ? "1 path was skipped"
                  : `${skipped.length} paths were skipped`}{" "}
                while walking this collection:
              </p>
              <ul className="mt-1 flex flex-col gap-1">
                {skipped.map((skip, i) => (
                  <li
                    key={`${skip.path}-${i}`}
                    data-testid="knowledge-backlinks-skip"
                    data-reason={skip.reason}
                    className="text-[11px] text-[var(--color-muted)]"
                  >
                    <span className="block truncate text-[var(--color-secondary)]">
                      {skip.path}
                    </span>
                    <span className="block">
                      {SKIP_REASON_TEXT[skip.reason]}
                      {skip.detail !== undefined && skip.detail !== ""
                        ? ` — ${skip.detail}`
                        : ""}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}
    </section>
  );
}
