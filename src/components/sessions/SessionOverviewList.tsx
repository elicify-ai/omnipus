// Renders one agent group's roots. Folds and missing-parent rows are expand
// targets, not sessions: Enter must not attach them. Real rows keep their id.

import { useMemo, useRef, type ReactNode } from 'react'
import { DisclosureRow } from '@/components/ui/disclosure-row'
import type { Session, SessionTreeNode } from '@/lib/api'
import { sessionDisplayTitle } from '@/lib/nav/sessionDisplayTitle'
import { SessionExpandToggle, SessionTree, flattenSessionTree, type SessionTreeFlatRow } from './SessionTree'
import { FOLD_ID_PREFIX, MISSING_PARENT_ID_PREFIX, UNPLACED_ID_PREFIX, sessionActivation } from './sessionOverview'
import { SessionRow } from './SessionRow'

const VIRTUALIZE_ROW_THRESHOLD = 20

function buildSearchNode(session: Session, childrenByParent: Map<string, Session[]>): SessionTreeNode {
  const kids = childrenByParent.get(session.id) ?? []
  return {
    session: { ...session, child_count: kids.length },
    children: kids.map((kid) => buildSearchNode(kid, childrenByParent)),
    childrenLoaded: true,
  }
}

function IndentedRow({ depth, children }: { depth: number; children: ReactNode }) {
  if (depth <= 0) return <div className="flex items-center">{children}</div>
  if (depth === 1) return <div className="flex items-center pl-[var(--space-3)]">{children}</div>
  if (depth === 2) return <div className="flex items-center pl-[var(--space-5)]">{children}</div>
  if (depth === 3) return <div className="flex items-center pl-[var(--space-7)]">{children}</div>
  return <div className="flex items-center pl-[var(--space-8)]">{children}</div>
}

export function SessionOverviewList({
  sessions,
  childrenByParent,
  expandedIds,
  toggleExpand,
  activeSessionId,
  highlightedId,
  mode,
  selectSession,
  onRename,
  onDelete,
  isDeleting,
  onEditingChange,
  agentName,
}: {
  sessions: Session[]
  childrenByParent: Map<string, Session[]>
  expandedIds: ReadonlySet<string>
  toggleExpand: (sessionId: string) => void
  activeSessionId: string | null
  highlightedId: string | undefined
  mode: 'sessions' | 'workspaces'
  selectSession: (session: Session) => void
  onRename: (id: string, title: string) => void
  onDelete: (id: string) => void
  isDeleting: (id: string) => boolean
  onEditingChange: (sessionId: string, editing: boolean) => void
  /**
   * Display name of this agent group's agent (founder feedback 2026-10-11,
   * point 4): an untitled MAIN session reads "<Agent> - Main Session" instead
   * of "Untitled session", derived from this CURRENT name so a rename flows
   * through on the next render. Sessions here are grouped by their owner
   * agent, so a main always belongs to this group's agent. An unresolved
   * name falls back to the session's own agent id (R2-S4-01) — never a
   * fabricated placeholder.
   */
  agentName?: string
}) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const nodes = useMemo(
    () => sessions.map((session) => buildSearchNode(session, childrenByParent)),
    [sessions, childrenByParent],
  )
  const rows = useMemo(() => flattenSessionTree(nodes, expandedIds), [nodes, expandedIds])
  const shouldVirtualize = rows.length > VIRTUALIZE_ROW_THRESHOLD

  const renderRow = (row: SessionTreeFlatRow) => {
    const session = row.node.session
    const marker = sessionActivation(session.id) === 'expand'
    // Display title (founder feedback 2026-10-11, point 4; R2-S4-01): server
    // title when present; for an untitled main the "<Agent> - Main Session"
    // derivation from the CURRENT group agentName — recomputed every render,
    // never cached. The session's own agent_id is the 4th argument — the id
    // fallback the ruling accepts — so an unresolved name lands on
    // "jim - Main Session" instead of null leaking into the expand labels
    // ("Expand null delegated sessions") and the row. Non-main sessions keep
    // "Untitled session".
    const title = sessionDisplayTitle(session, agentName, 'Untitled session', session.agent_id) ?? 'Untitled session'
    return (
      <IndentedRow depth={row.depth}>
        {marker ? (
          <MarkerRow
            session={session}
            expanded={row.isExpanded}
            highlighted={mode === 'sessions' && session.id === highlightedId}
            onToggle={() => toggleExpand(session.id)}
          />
        ) : (
          <>
            {row.hasChildren ? (
              <SessionExpandToggle
                expanded={row.isExpanded}
                onToggle={() => toggleExpand(session.id)}
                expandLabel={`Expand ${title} delegated sessions`}
                collapseLabel={`Collapse ${title} delegated sessions`}
              />
            ) : (
              <span className="w-[var(--space-3)] shrink-0" aria-hidden="true" />
            )}
            <div className="min-w-0 flex-1">
              <SessionRow
                session={session}
                isActive={session.id === activeSessionId}
                isHighlighted={mode === 'sessions' && session.id === highlightedId}
                onSelect={() => selectSession(session)}
                onRename={(nextTitle) => onRename(session.id, nextTitle)}
                onDelete={() => onDelete(session.id)}
                deleting={isDeleting(session.id)}
                onEditingChange={onEditingChange}
                agentName={agentName}
              />
            </div>
          </>
        )}
      </IndentedRow>
    )
  }

  const content = (
    <SessionTree
      nodes={nodes}
      expandedIds={expandedIds}
      renderRow={renderRow}
      virtualize={shouldVirtualize ? { scrollElementRef: scrollRef, estimateRowHeight: 56 } : undefined}
    />
  )

  if (shouldVirtualize) {
    return (
      <div ref={scrollRef} className="max-h-[360px] overflow-y-auto" data-testid="search-session-list-virtual-scroll">
        {content}
      </div>
    )
  }
  return content
}

function MarkerRow({ session, expanded, highlighted, onToggle }: {
  session: Session
  expanded: boolean
  highlighted: boolean
  onToggle: () => void
}) {
  const testId = session.id.startsWith(FOLD_ID_PREFIX)
    ? 'session-fold'
    : session.id.startsWith(MISSING_PARENT_ID_PREFIX)
      ? 'session-missing-parent'
      : session.id.startsWith(UNPLACED_ID_PREFIX)
        ? 'session-unplaced'
        : 'session-marker'
  return (
    <div
      id={`search-result-${session.id}`}
      data-testid={testId}
      data-activation-id={session.id}
      className={highlighted ? 'min-w-0 flex-1 rounded-md bg-[var(--color-surface-2)]' : 'min-w-0 flex-1'}
    >
      <DisclosureRow
        expanded={expanded}
        expandable
        onExpandedChange={onToggle}
        aria-label={session.title}
      >
        <span>{session.title}</span>
      </DisclosureRow>
    </div>
  )
}

export function visibleOverviewIds(
  sessions: Session[],
  childrenByParent: Map<string, Session[]>,
  expandedIds: ReadonlySet<string>,
): string[] {
  const nodes = sessions.map((session) => buildSearchNode(session, childrenByParent))
  return flattenSessionTree(nodes, expandedIds).map((row) => row.node.session.id)
}
