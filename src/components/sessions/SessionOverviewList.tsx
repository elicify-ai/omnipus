// Renders one agent group's roots. Folds and missing-parent rows are expand
// targets, not sessions: Enter must not attach them. Real rows keep their id.

import { useMemo, useRef, type CSSProperties } from 'react'
import { DisclosureRow } from '@/components/ui/disclosure-row'
import type { Session, SessionTreeNode } from '@/lib/api'
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

function rowIndent(depth: number): CSSProperties | undefined {
  if (depth <= 0) return undefined
  return { '--search-modal-indent-depth': depth, paddingLeft: 'calc(var(--search-modal-indent-depth) * var(--space-3))' } as CSSProperties
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
    return (
      <div className="flex items-center" style={rowIndent(row.depth)}>
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
                expandLabel={`Expand ${session.title || 'Untitled session'} delegated sessions`}
                collapseLabel={`Collapse ${session.title || 'Untitled session'} delegated sessions`}
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
                onRename={(title) => onRename(session.id, title)}
                onDelete={() => onDelete(session.id)}
                deleting={isDeleting(session.id)}
                onEditingChange={onEditingChange}
              />
            </div>
          </>
        )}
      </div>
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
