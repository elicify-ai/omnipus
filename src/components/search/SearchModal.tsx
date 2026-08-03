// SearchModal — cross-workspace session search + management overlay.
// Rich appearance matching the old SessionPanel: collapsible workspace groups,
// collapsible agent sub-groups with avatar/icon, session metadata (dates,
// tokens), inline rename (edit icon), delete icon.
// Driven by useUiStore.searchModalOpen.

import { useEffect, useMemo, useState, useCallback, useRef } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import {
  MagnifyingGlass, Calendar, PencilSimple, Trash, Check, X,
  CaretRight, CaretDown, Folder, ArrowRight,
} from '@phosphor-icons/react'
import { fetchAgents, fetchSessions, fetchWorkspaces, renameSession, deleteSession, workspacesQueryKeys, getErrorMessage } from '@/lib/api'
import { formatTokens } from '@/lib/formatTokens'
import { formatRelative } from '@/lib/formatRelative'
import type { Session, Workspace, Agent, SessionTreeNode } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useSelectSession } from '@/components/chat/useSelectSession'
import { IconRenderer } from '@/components/shared/IconRenderer'
import { cn, initialOf } from '@/lib/utils'
import { SessionTree, SessionExpandToggle, flattenSessionTree, type SessionTreeFlatRow } from '@/components/sessions/SessionTree'

// ── ADR-057 US-19/FR-094 (W16g — operator decision 1, nested under parent) ──
//
// SearchModal fetches every session (roots AND delegated children,
// `flat: true` — FR-104) rather than the default roots-only page, because a
// text search must be able to find a match at ANY depth. The already-
// resident flat list is nested client-side (buildSearchNode below) rather
// than incrementally, unlike Sidebar's lazy per-node fetch
// (SessionTree.tsx's useSessionForest) — the whole point of search is that
// the match set isn't known ahead of expand-clicks.
function buildSearchNode(session: Session, childrenByParent: Map<string, Session[]>): SessionTreeNode {
  const kids = childrenByParent.get(session.id) ?? []
  return {
    session: { ...session, child_count: session.child_count ?? kids.length },
    children: kids.map((k) => buildSearchNode(k, childrenByParent)),
    childrenLoaded: true,
  }
}

// A session list this long stops being "the full list plus a scrollbar" and
// becomes the exact R-9/BDD-105 shape this story exists to fix (24-way
// delegation fan-out, "an order of magnitude longer" per the spec) — past
// this bound the group gets its own capped-height, virtualized viewport
// (FR-094) instead of mounting every row. Below it (every existing fixture
// in this file, and the overwhelming common case), rendering stays exactly
// as before — plain, fully mounted, zero behavior change.
const VIRTUALIZE_ROW_THRESHOLD = 20

/**
 * Renders one agent group's ROOT sessions as a tree — a root with delegated
 * children gets an expand toggle; children nest indented beneath it,
 * collapsed by default (or auto-revealed while a search finds a match
 * beneath them — see `effectiveExpandedIds` in SearchModal). Extracted as
 * its own component (rather than inlined in SearchModal's render loop) so
 * its `useRef`/`useMemo` calls have a stable, unconditional call site — one
 * instance is mounted per agent group in the `.map()` below, not a variable
 * number of hook calls inside a single component's render body.
 */
function AgentSessionList({
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
  const nodes = useMemo(() => sessions.map((s) => buildSearchNode(s, childrenByParent)), [sessions, childrenByParent])
  const rows = useMemo(() => flattenSessionTree(nodes, expandedIds), [nodes, expandedIds])
  const shouldVirtualize = rows.length > VIRTUALIZE_ROW_THRESHOLD

  const renderRow = (row: SessionTreeFlatRow) => {
    const s = row.node.session
    return (
      <div className="flex items-center" style={row.depth > 0 ? { paddingLeft: row.depth * 14 } : undefined}>
        {row.hasChildren ? (
          <SessionExpandToggle
            expanded={row.isExpanded}
            onToggle={() => toggleExpand(s.id)}
            expandLabel={`Expand ${s.title || 'Untitled session'} delegated sessions`}
            collapseLabel={`Collapse ${s.title || 'Untitled session'} delegated sessions`}
          />
        ) : (
          <span className="w-[18px] shrink-0" aria-hidden="true" />
        )}
        <div className="min-w-0 flex-1">
          <SessionRow
            session={s}
            isActive={s.id === activeSessionId}
            isHighlighted={mode === 'sessions' && s.id === highlightedId}
            onSelect={() => selectSession(s)}
            onRename={(t) => onRename(s.id, t)}
            onDelete={() => onDelete(s.id)}
            deleting={isDeleting(s.id)}
            onEditingChange={onEditingChange}
          />
        </div>
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

// ── helpers ──────────────────────────────────────────────────────────────────

function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const h = setTimeout(() => setDebounced(value), delayMs)
    return () => clearTimeout(h)
  }, [value, delayMs])
  return debounced
}

// ── types ────────────────────────────────────────────────────────────────────

interface AgentGroup {
  agent: Agent | undefined
  agentId: string
  sessions: Session[]
}
interface WsGroup {
  workspace: Workspace | null
  agentGroups: AgentGroup[]
  totalCount: number
}

// ── collapsible workspace header ─────────────────────────────────────────────

function WorkspaceHeader({ name, isCollapsed, onToggle, panelId, onSwitch, isHighlighted }: {
  name: string; isCollapsed: boolean; onToggle: () => void; panelId: string
  /**
   * Present only for a real workspace group — absent for the 'Unfiled'
   * pseudo-group (no workspace to switch to; the caller omits this prop
   * there, see SearchModal's render loop).
   *
   * The header row itself is a collapse-toggle <button>; this must be a
   * SIBLING <button>, never nested inside it (invalid HTML — a button can't
   * contain a button — and it would make "switch" and "collapse" the same
   * click target). Mirrors GenericToolCall.tsx's row split for its
   * independently-clickable "Watch live" launcher.
   */
  onSwitch?: () => void
  /**
   * Workspaces-mode only: true when this header is the keyboard-highlighted
   * row (ArrowUp/Down walks workspace headers in that mode — see
   * SearchModal's `highlightedWorkspace`). Reuses SessionRow's highlight
   * visual language (surface-2 wash + trailing "↵" hint) so the two modes
   * read as the same interaction pattern. Always false/omitted in sessions
   * mode, where the highlight instead follows SessionRow.
   */
  isHighlighted?: boolean
}) {
  return (
    <div className={cn('flex items-center w-full rounded-md', isHighlighted && 'bg-[var(--color-surface-2)]')}>
      <button tabIndex={0}
        type="button"
        onClick={onToggle}
        className="min-w-0 flex-1 flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-[var(--color-muted)] uppercase tracking-wider hover:text-[var(--color-secondary)] transition-colors"
        aria-expanded={!isCollapsed}
        aria-controls={panelId}
      >
        {isCollapsed ? <CaretRight size={10} className="shrink-0" /> : <CaretDown size={10} className="shrink-0" />}
        <Folder size={14} className="shrink-0" />
        <span className="flex-1 text-left truncate">{name}</span>
        {isHighlighted && (
          <span className="shrink-0 rounded border border-[var(--color-border)] px-1 text-[9px] font-normal normal-case tracking-normal text-[var(--color-muted)]" aria-hidden="true">↵</span>
        )}
      </button>
      {onSwitch && (
        <button tabIndex={0}
          type="button"
          onClick={onSwitch}
          data-testid="workspace-switch-arrow"
          aria-label={`Switch to workspace ${name}`}
          title={`Switch to workspace ${name}`}
          // p-1.5: 13px icon + 12px padding = 25px target (WCAG 2.5.8 needs
          // 24px, and the collapse toggle is flush-adjacent so the spacing
          // exception can't cover an undersized target). Matches the file's
          // rename/delete icon-button sizing.
          className="shrink-0 mr-2 flex items-center justify-center rounded p-1.5 text-[var(--color-accent)] opacity-80 hover:opacity-100 transition-all"
        >
          <ArrowRight size={13} />
        </button>
      )}
    </div>
  )
}

// ── collapsible agent header with avatar ──────────────────────────────────────

function AgentHeader({ agent, name, isCollapsed, onToggle, panelId }: { agent: Agent | undefined; name: string; isCollapsed: boolean; onToggle: () => void; panelId: string }) {
  return (
    <button tabIndex={0}
      type="button"
      onClick={onToggle}
      className="w-full flex items-center gap-1.5 px-2 py-1 text-[11px] font-medium text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors"
      aria-expanded={!isCollapsed}
      aria-controls={panelId}
    >
      {isCollapsed ? <CaretRight size={9} className="shrink-0" /> : <CaretDown size={9} className="shrink-0" />}
      <span
        className="w-4 h-4 rounded-full border border-[var(--color-primary)] flex items-center justify-center text-[7px] shrink-0"
        style={{ backgroundColor: agent?.color ?? 'var(--color-surface-3)' }}
        aria-hidden="true"
      >
        {agent?.icon ? (
          <IconRenderer icon={agent.icon} size={8} />
        ) : (
          <span className="text-[var(--color-secondary)] font-bold">{initialOf(name)}</span>
        )}
      </span>
      <span className="flex-1 text-left truncate">{name}</span>
    </button>
  )
}

// ── session row with metadata + edit/delete ──────────────────────────────────

function SessionRow({ session, isActive, isHighlighted, onSelect, onRename, onDelete, deleting, onEditingChange }: {
  session: Session; isActive: boolean; isHighlighted: boolean; onSelect: () => void; onRename: (t: string) => void; onDelete: () => void; deleting: boolean
  /** Notifies the parent modal whenever THIS row (identified by session.id)
   *  enters/leaves inline-rename mode — used to suppress the Dialog's own
   *  Escape-to-close while any row is mid-rename (see the DialogContent
   *  onEscapeKeyDown wiring below). Required (single private caller in
   *  SearchModal) and must be a stable identity — SearchModal passes a
   *  useCallback-memoized handler so this row's effect doesn't re-fire on
   *  every unrelated parent re-render. The effect below also reports `false`
   *  on unmount, so collapsing this row's group (or a refetch dropping it)
   *  while mid-rename can't leave the parent's aggregate wedged `true`. */
  onEditingChange: (sessionId: string, editing: boolean) => void
}) {
  const [editing, setEditing] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [val, setVal] = useState(session.title || '')
  const renameButtonRef = useRef<HTMLButtonElement>(null)
  const commit = () => {
    const t = val.trim()
    if (t && t !== session.title) onRename(t)
    setEditing(false)
  }
  useEffect(() => {
    onEditingChange(session.id, editing)
    // Unmount cleanup: report this row as no-longer-editing regardless of
    // its last-known state. Without this, closing the modal (or the group
    // collapsing / a refetch dropping the row) mid-rename leaves the
    // session's id stuck in the parent's editing set forever.
    return () => onEditingChange(session.id, false)
  }, [editing, onEditingChange, session.id])

  // Restore focus to the rename (pencil) button whenever a rename ends
  // (Escape-cancel, ✕-cancel, or successful commit) — the inline input
  // unmounts on all three paths and focus would otherwise drop to <body>.
  const wasEditingRef = useRef(false)
  useEffect(() => {
    if (wasEditingRef.current && !editing) {
      renameButtonRef.current?.focus()
    }
    wasEditingRef.current = editing
  }, [editing])

  if (editing) {
    return (
      <div className="flex items-center gap-2 rounded-md px-3 py-2 bg-[var(--color-surface-2)] mx-2">
        <Input autoFocus value={val} onChange={(e) => setVal(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') { e.preventDefault(); commit() }
            // Escape cancels the rename. Note this alone can't stop the Dialog
            // from also closing: Radix's DismissableLayer listens for Escape
            // on `document` in the CAPTURE phase, which fires before this
            // (bubble-phase) handler ever runs — preventDefault() here is too
            // late to affect that decision. The Dialog is kept open instead
            // via DialogContent's onEscapeKeyDown below, informed by
            // onEditingChange.
            if (e.key === 'Escape') { e.preventDefault(); setEditing(false); setVal(session.title || '') }
          }}
          className="h-7 flex-1 text-sm" />
        <button tabIndex={0} onClick={commit} className="shrink-0 rounded p-1.5 text-[var(--color-success)] hover:bg-[var(--color-surface-3)]" aria-label="Confirm rename"><Check size={14} weight="bold" /></button>
        <button tabIndex={0} onClick={() => { setEditing(false); setVal(session.title || '') }} className="shrink-0 rounded p-1.5 text-[var(--color-muted)] hover:bg-[var(--color-surface-3)]" aria-label="Cancel rename"><X size={14} /></button>
      </div>
    )
  }

  // Destructive action requires explicit confirmation — mistakes must be cheap.
  if (confirmDelete) {
    return (
      <div className="flex items-center gap-2 rounded-md px-3 py-2 bg-[var(--color-surface-2)] mx-2 text-sm">
        <span className="flex-1 truncate text-[var(--color-secondary)]">Delete "{session.title || 'Untitled session'}"?</span>
        <button tabIndex={0}
          type="button"
          onClick={() => { setConfirmDelete(false); onDelete() }}
          disabled={deleting}
          className="shrink-0 rounded px-2 py-1 text-xs text-[var(--color-error)] hover:bg-[var(--color-error)]/10 disabled:opacity-50"
        >
          Delete
        </button>
        <button tabIndex={0}
          type="button"
          onClick={() => setConfirmDelete(false)}
          className="shrink-0 rounded px-2 py-1 text-xs text-[var(--color-muted)] hover:bg-[var(--color-surface-3)]"
        >
          Cancel
        </button>
      </div>
    )
  }

  return (
    <div id={`search-result-${session.id}`} className={cn(
      'group flex items-center gap-2 rounded-md px-3 py-2 mx-1 transition-colors hover:bg-[var(--color-surface-2)]',
      // Keyboard highlight = what Enter selects; follows ArrowUp/Down.
      isHighlighted && 'bg-[var(--color-surface-2)]',
    )}>
      <button tabIndex={0} type="button" onClick={onSelect} className="min-w-0 flex-1 text-left">
        <div className={cn('truncate text-sm font-medium flex items-center gap-1.5', isActive ? 'text-[var(--color-accent)]' : 'text-[var(--color-secondary)]')}>
          <span className="truncate">{session.title || 'Untitled session'}</span>
          {isHighlighted && (
            <span className="shrink-0 rounded border border-[var(--color-border)] px-1 text-[9px] font-normal text-[var(--color-muted)]" aria-hidden="true">↵</span>
          )}
        </div>
        {/* flex-wrap + hiding the token count under 400px keeps the metadata legible on phones */}
        <div className="mt-0.5 flex flex-wrap items-center gap-x-1.5 gap-y-0 text-[11px] text-[var(--color-muted)]">
          {/* formatRelative returns '' for an unparseable date (documented
              contract in src/lib/formatRelative.ts) — fall back to an em
              dash here rather than rendering a dangling "Started ". */}
          <span>Started {formatRelative(session.created_at) || '—'}</span>
          <span aria-hidden>·</span>
          <span>Active {formatRelative(session.updated_at) || '—'}</span>
          {session.total_tokens ? (<span className="hidden min-[400px]:inline"><span aria-hidden>· </span><span className="font-mono">{formatTokens(session.total_tokens)}</span></span>) : null}
          {session.type === 'heartbeat' && (<><span aria-hidden>·</span><span className="uppercase tracking-wider text-[9px]">HB</span></>)}
        </div>
      </button>
      {/* Hover-revealed on pointer devices; always visible on touch ([@media(hover:none)]). */}
      <button tabIndex={0} ref={renameButtonRef} type="button" onClick={() => { setVal(session.title || ''); setEditing(true) }}
        className="shrink-0 rounded p-1.5 text-[var(--color-muted)] opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 focus-visible:opacity-100 [@media(hover:none)]:opacity-100 hover:text-[var(--color-accent)] hover:bg-[var(--color-surface-3)] transition-all"
        aria-label={`Rename ${session.title || 'Untitled session'}`} title="Rename"><PencilSimple size={13} /></button>
      <button tabIndex={0} type="button" onClick={() => setConfirmDelete(true)} disabled={deleting || session.protected === true}
        className="shrink-0 rounded p-1.5 text-[var(--color-muted)] opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 focus-visible:opacity-100 [@media(hover:none)]:opacity-100 hover:text-[var(--color-error)] hover:bg-[var(--color-surface-3)] transition-all disabled:opacity-30 disabled:cursor-not-allowed"
        aria-label={`Delete ${session.title || 'Untitled session'}`} title={session.protected ? 'Protected (heartbeat)' : 'Delete'}><Trash size={13} /></button>
    </div>
  )
}

// ── main component ───────────────────────────────────────────────────────────

export function SearchModal() {
  const open = useUiStore((s) => s.searchModalOpen)
  const close = useUiStore((s) => s.closeSearchModal)
  const wsFilter = useUiStore((s) => s.searchModalWorkspaceFilter)
  // TWO MODES, one panel (see ui.ts's doc comment on searchModalMode):
  // 'sessions' (default, /resume + sidebar search icon + "More…") is the
  // original session-search behavior, unchanged below. 'workspaces'
  // (/workspace only) switches the panel into workspace-switch mode — ALL
  // workspaces listed, groups collapsed by default, ArrowUp/Down walks
  // workspace headers, Enter switches, typing filters by workspace name.
  const mode = useUiStore((s) => s.searchModalMode)
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const navigate = useNavigate()
  // Tracks the set of session ids currently in inline-rename mode, so the
  // Dialog's own Escape-to-close can be suppressed in favour of just
  // cancelling the rename(s) in progress (see SessionRow's onEditingChange +
  // the DialogContent onEscapeKeyDown below). A Set (not a single boolean)
  // because more than one row can be mid-rename at once — one row cancelling
  // must not re-enable Escape-to-close while another is still editing.
  // handleEditingChange is a stable useCallback (not an inline arrow) so
  // SessionRow's effect only re-fires when ITS OWN editing state actually
  // changes, never on an unrelated parent re-render (e.g. a background
  // refetch delivering a new `sessions` array reference).
  const editingSessionIdsRef = useRef<Set<string>>(new Set())
  const handleEditingChange = useCallback((sessionId: string, editing: boolean) => {
    if (editing) editingSessionIdsRef.current.add(sessionId)
    else editingSessionIdsRef.current.delete(sessionId)
  }, [])

  const [searchText, setSearchText] = useState('')
  const [showDateFilter, setShowDateFilter] = useState(false)
  const [fromDate, setFromDate] = useState('')
  const [toDate, setToDate] = useState('')
  const debouncedSearch = useDebouncedValue(searchText, 200)

  // Collapse state. `collapsedWs` holds workspace-group keys the user has
  // manually TOGGLED away from the current mode's default — not "the set of
  // collapsed groups" directly — because the two modes have opposite
  // defaults: sessions mode starts every group EXPANDED (unchanged, original
  // behavior), workspaces mode starts every group COLLAPSED (user can still
  // expand one manually to peek at its sessions). `isWsCollapsed` below is
  // the single place that reconciles "is this key toggled" against "what's
  // the mode's default" into the actual collapsed/expanded answer — the
  // render loop and `flatSessions` both go through it rather than reading
  // `collapsedWs.has(...)` directly, so the two can never disagree.
  // `collapsedAgent` (the nested per-agent sub-groups) is unaffected by mode
  // — it always defaults expanded — so it keeps its original direct
  // semantics.
  const [collapsedWs, setCollapsedWs] = useState<Set<string>>(new Set())
  const [collapsedAgent, setCollapsedAgent] = useState<Set<string>>(new Set())

  const isWsCollapsed = useCallback(
    (key: string) => (mode === 'workspaces' ? !collapsedWs.has(key) : collapsedWs.has(key)),
    [mode, collapsedWs],
  )

  const toggleWs = useCallback((key: string) => {
    setCollapsedWs((prev) => { const n = new Set(prev); n.has(key) ? n.delete(key) : n.add(key); return n })
  }, [])
  const toggleAgent = useCallback((key: string) => {
    setCollapsedAgent((prev) => { const n = new Set(prev); n.has(key) ? n.delete(key) : n.add(key); return n })
  }, [])

  // Resets all transient panel state (search text, date filter, collapse
  // toggles, in-progress renames) whenever the panel closes — so the NEXT
  // open (whichever mode it's in) starts from that mode's clean default —
  // AND whenever the mode itself changes while the panel stays mounted
  // (open never goes false — e.g. a /workspace invocation landing on an
  // already-open sessions-mode panel). Without the mode branch, toggles
  // made in one mode would carry over and be misread against the other
  // mode's opposite default the next time `isWsCollapsed` evaluates them.
  const prevModeRef = useRef(mode)
  useEffect(() => {
    const modeChanged = prevModeRef.current !== mode
    prevModeRef.current = mode
    if (!open || modeChanged) {
      setSearchText(''); setShowDateFilter(false); setFromDate(''); setToDate(''); setCollapsedWs(new Set()); setCollapsedAgent(new Set())
      editingSessionIdsRef.current.clear()
    }
  }, [open, mode])

  // ADR-052 FR-036: verifier-role sessions stay excluded by default —
  // `includeVerifier` is intentionally omitted (see Sidebar.tsx for the
  // matching note and UsageScreen.tsx for the surface that DOES want them).
  //
  // ADR-057 US-19/FR-094/FR-104 (W16g, operator decision 1 — nested under
  // parent, not the `verifier` hidden-with-a-flag precedent): fetches
  // `flat: true` rather than the default roots-only page, because a text
  // search must be able to find a match in a delegated CHILD session, not
  // just a root — the default listing would silently exclude every
  // subordinate from the search space. The flat list is nested client-side
  // (buildSearchNode) and rendered through AgentSessionList below, which
  // keeps the DOM bounded via virtualization once a group's row count
  // crosses VIRTUALIZE_ROW_THRESHOLD (FR-094's "rendered node count bounded
  // by the viewport, not the result count"). Distinct queryKey from
  // Sidebar's `['sessions']` (roots-only) — the two surfaces intentionally
  // request different shapes and must not share a cache entry.
  const { data: sessions = [], isLoading: sLoading, isError: sessionsError } = useQuery({
    queryKey: ['sessions', 'flat'],
    queryFn: () => fetchSessions(undefined, undefined, { flat: true }),
    enabled: open,
  })
  const { data: workspaces = [], isLoading: wLoading, isError: workspacesError } = useQuery({ queryKey: workspacesQueryKeys.list({ status: 'active' }), queryFn: () => fetchWorkspaces({ status: 'active' }), enabled: open })
  const { data: agents = [], isError: agentsError } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents, enabled: open })

  const selectSession = useSelectSession({ agents, workspaces, onClose: close })

  // ── ADR-057 US-19/FR-091/FR-094 tree assembly ───────────────────────────
  const wsMap = useMemo(() => new Map(workspaces.map((w) => [w.id, w])), [workspaces])
  const sessionById = useMemo(() => new Map(sessions.map((s) => [s.id, s])), [sessions])
  // A session is a DISPLAY ROOT if it has no parent, or its declared parent
  // doesn't resolve in the fetched set — BDD-106: an orphan is shown as a
  // root, never silently dropped.
  const isDisplayRoot = useCallback((s: Session) => !s.parent_session_id || !sessionById.has(s.parent_session_id), [sessionById])
  const childrenByParent = useMemo(() => {
    const map = new Map<string, Session[]>()
    for (const s of sessions) {
      if (!s.parent_session_id || !sessionById.has(s.parent_session_id)) continue
      const arr = map.get(s.parent_session_id)
      arr ? arr.push(s) : map.set(s.parent_session_id, [s])
    }
    return map
  }, [sessions, sessionById])

  // Session-content search only applies in 'sessions' mode — 'workspaces'
  // mode's search box filters WORKSPACE NAMES (existing behavior below),
  // and must not incidentally auto-expand session trees in the peek view
  // just because a workspace-name substring happens to match a session
  // title too.
  const searchActive = mode === 'sessions' && debouncedSearch.trim().length > 0
  const matchesSessionQuery = useCallback(
    (s: Session): boolean => {
      const qq = debouncedSearch.trim().toLowerCase()
      if (!qq) return true
      const wsName = s.workspace_id ? (wsMap.get(s.workspace_id)?.name ?? '').toLowerCase() : ''
      return (s.title ?? '').toLowerCase().includes(qq) || wsName.includes(qq)
    },
    [wsMap, debouncedSearch],
  )

  // Single post-order walk from every display root: `selfMatchIds` are
  // sessions whose OWN title/workspace matched; `subtreeMatchIds` are
  // sessions where the match is anywhere in the subtree (self or any
  // descendant, any depth) — BDD-105's "the matching child appears nested
  // under its parent, with the parent shown for context even though it did
  // not match".
  const { selfMatchIds, subtreeMatchIds } = useMemo(() => {
    const self = new Set<string>()
    const subtree = new Set<string>()
    if (!searchActive) return { selfMatchIds: self, subtreeMatchIds: subtree }
    function visit(s: Session): boolean {
      const isSelf = matchesSessionQuery(s)
      if (isSelf) self.add(s.id)
      let any = isSelf
      for (const child of childrenByParent.get(s.id) ?? []) {
        if (visit(child)) any = true
      }
      if (any) subtree.add(s.id)
      return any
    }
    for (const s of sessions) {
      if (isDisplayRoot(s)) visit(s)
    }
    return { selfMatchIds: self, subtreeMatchIds: subtree }
  }, [searchActive, sessions, childrenByParent, isDisplayRoot, matchesSessionQuery])

  // Auto-reveal exactly the ancestors that need it to surface a match — a
  // node whose subtree matches but who did NOT itself match, and that
  // actually has children to show. A root that matched on its own title
  // does not get its whole fan-out dumped open just because it matched.
  const autoExpandIds = useMemo(() => {
    if (!searchActive) return new Set<string>()
    const out = new Set<string>()
    for (const id of subtreeMatchIds) {
      if (!selfMatchIds.has(id) && (childrenByParent.get(id)?.length ?? 0) > 0) out.add(id)
    }
    return out
  }, [searchActive, subtreeMatchIds, selfMatchIds, childrenByParent])

  // Manual expand/collapse (chevron clicks) — independent of search so a
  // user can browse a tree with no query typed at all, mirroring Sidebar's
  // affordance for the same underlying data shape.
  const [manualExpandedIds, setManualExpandedIds] = useState<Set<string>>(new Set())
  const toggleManualExpand = useCallback((sessionId: string) => {
    setManualExpandedIds((prev) => {
      const next = new Set(prev)
      if (next.has(sessionId)) next.delete(sessionId)
      else next.add(sessionId)
      return next
    })
  }, [])
  const effectiveExpandedIds = useMemo(
    () => new Set([...manualExpandedIds, ...autoExpandIds]),
    [manualExpandedIds, autoExpandIds],
  )

  // Workspace-switch arrow (WorkspaceHeader) — mirrors Sidebar.tsx's
  // "New chat" workspace-row action EXACTLY (setActiveWorkspaceId ->
  // startNewSession -> navigate -> close): switching from search should land
  // the user on a fresh composer in the target workspace, not silently
  // re-attach whatever session happened to be active there before. The
  // setActiveWorkspaceId-BEFORE-startNewSession order is load-bearing:
  // startNewSession clears sessionByWorkspace for the CURRENT workspace, so
  // the switch must happen first for the target to get the fresh slate.
  // Reads both stores via getState() (a plain click handler, not something
  // that needs reactive re-renders) — the same pattern deleteMut's
  // pruneSessionDescriptor call below uses.
  const handleSwitchWorkspace = useCallback((ws: Workspace) => {
    if (ws.id === useWorkspacesStore.getState().activeWorkspaceId) {
      // Already there: the arrow says "Switch", not "New chat" — don't
      // detach a possibly-live session; just land on the workspace chat.
      void navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: ws.id } })
      close()
      return
    }
    useWorkspacesStore.getState().setActiveWorkspaceId(ws.id)
    useSessionStore.getState().startNewSession()
    void navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: ws.id } })
    close()
  }, [close, navigate])

  const renameMut = useMutation({
    mutationFn: ({ id, title }: { id: string; title: string }) => renameSession(id, title),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['sessions'] }),
    onError: (e) => addToast({ message: getErrorMessage(e, 'Rename failed'), variant: 'error' }),
  })
  const deleteMut = useMutation({
    mutationFn: (id: string) => deleteSession(id),
    onSuccess: (_data, deletedId) => {
      queryClient.invalidateQueries({ queryKey: ['sessions'] })
      // Prunes any sessionByWorkspace entry pointing at the deleted session
      // (so a later enterWorkspaceChat doesn't zombie-reattach the dead id)
      // and clears activeSessionId if it matches — logic now lives in the
      // store so every delete path shares it, not just this one.
      useSessionStore.getState().pruneSessionDescriptor(deletedId)
    },
    onError: (e) => addToast({ message: getErrorMessage(e, 'Delete failed'), variant: 'error' }),
  })

  // Heartbeat sessions pinned at top, then recent-first
  const sortSessions = (a: Session, b: Session) => {
    if (a.type === 'heartbeat' && b.type !== 'heartbeat') return -1
    if (b.type === 'heartbeat' && a.type !== 'heartbeat') return 1
    return new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()
  }

  // Buckets a session list into AgentGroup[] — shared by both modes' branches
  // below (sessions mode buckets a workspace's filtered sessions; workspaces
  // mode buckets a workspace's UNFILTERED sessions, since the primary filter
  // target there is the workspace name, not session content).
  const bucketByAgent = useCallback((sess: Session[]): AgentGroup[] => {
    const aBuckets = new Map<string, Session[]>()
    for (const s of sess) {
      const aId = s.active_agent_id ?? s.agent_id ?? 'unknown'
      const arr = aBuckets.get(aId); arr ? arr.push(s) : aBuckets.set(aId, [s])
    }
    const agGroups: AgentGroup[] = []
    for (const [aId, aSess] of aBuckets) {
      aSess.sort(sortSessions)
      agGroups.push({ agent: agents.find((a) => a.id === aId), agentId: aId, sessions: aSess })
    }
    agGroups.sort((a, b) => new Date(b.sessions[0]?.updated_at ?? '').getTime() - new Date(a.sessions[0]?.updated_at ?? '').getTime())
    return agGroups
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agents])

  const groups = useMemo<WsGroup[]>(() => {
    const q = debouncedSearch.trim().toLowerCase()
    // ADR-057 US-19/FR-091: every surface below buckets ROOT sessions only —
    // delegated children never compete for a top-level slot in either mode.
    // They render nested under their real parent via AgentSessionList
    // (childrenByParent/effectiveExpandedIds), not as independent rows here.
    const rootSessions = sessions.filter(isDisplayRoot)

    if (mode === 'workspaces') {
      // Workspace-switch mode: source the FULL fetchWorkspaces list — every
      // active workspace, including ones with zero sessions — not just the
      // workspaces that happen to have a session (the sessions-mode bucket
      // approach below would silently drop an empty workspace). The primary
      // filter target is the workspace NAME; per-workspace sessions are
      // attached unfiltered for the (collapsed-by-default) peek view. No
      // Unfiled pseudo-group here — it isn't a real workspace and can't be
      // switched to (handleSwitchWorkspace requires a Workspace).
      const byWorkspace = new Map<string, Session[]>()
      for (const s of rootSessions) {
        if (s.workspace_id && wsMap.has(s.workspace_id)) {
          const arr = byWorkspace.get(s.workspace_id); arr ? arr.push(s) : byWorkspace.set(s.workspace_id, [s])
        }
      }
      const matched = q ? workspaces.filter((w) => w.name.toLowerCase().includes(q)) : workspaces
      const sorted = [...matched].sort((a, b) => a.name.localeCompare(b.name))
      return sorted.map((w) => {
        const sess = byWorkspace.get(w.id) ?? []
        return { workspace: w, agentGroups: bucketByAgent(sess), totalCount: sess.length }
      })
    }

    // Sessions mode (default): group the FILTERED root sessions by
    // workspace, then by agent within each workspace. A root passes the text
    // filter if it OR any descendant matches (subtreeMatchIds, BDD-105) —
    // the matching descendant itself is revealed via effectiveExpandedIds at
    // render time, not by being present in this bucket.
    const fromT = fromDate ? new Date(`${fromDate}T00:00:00`).getTime() : null
    const toT = toDate ? new Date(`${toDate}T23:59:59`).getTime() : null

    const filtered = rootSessions.filter((s) => {
      // Workspace pre-filter (from the sidebar "More…" button)
      if (wsFilter && s.workspace_id !== wsFilter) return false
      if (searchActive && !subtreeMatchIds.has(s.id)) return false
      const u = new Date(s.updated_at).getTime()
      if ((fromT !== null || toT !== null) && (isNaN(u) || (fromT !== null && u < fromT) || (toT !== null && u > toT))) return false
      return true
    })

    const wsBuckets = new Map<string | null, Session[]>()
    for (const s of filtered) {
      const k = s.workspace_id && wsMap.has(s.workspace_id) ? s.workspace_id : null
      const arr = wsBuckets.get(k); arr ? arr.push(s) : wsBuckets.set(k, [s])
    }

    const result: WsGroup[] = []
    for (const [wsId, sess] of wsBuckets) {
      result.push({ workspace: wsId ? wsMap.get(wsId) ?? null : null, agentGroups: bucketByAgent(sess), totalCount: sess.length })
    }
    result.sort((a, b) => {
      if (a.workspace === null && b.workspace !== null) return 1
      if (a.workspace !== null && b.workspace === null) return -1
      return new Date(b.agentGroups[0]?.sessions[0]?.updated_at ?? '').getTime() - new Date(a.agentGroups[0]?.sessions[0]?.updated_at ?? '').getTime()
    })
    return result
  }, [sessions, workspaces, debouncedSearch, fromDate, toDate, wsFilter, mode, bucketByAgent, isDisplayRoot, wsMap, searchActive, subtreeMatchIds])

  // Sessions mode: total is a SESSION count (drives "No sessions found").
  // Workspaces mode: total is a WORKSPACE count — `groups` already IS one
  // entry per matched workspace (zero-session ones included), so summing
  // `totalCount` (session counts) would wrongly read "no results" for an
  // all-empty-but-real workspace list.
  const total = mode === 'workspaces' ? groups.length : groups.reduce((n, g) => n + g.totalCount, 0)
  const loading = sLoading || wLoading

  // Flat, in-render-order list of VISIBLE sessions (collapsed groups excluded)
  // — the SESSIONS-mode arrow-key navigation space. Index 0 is what Enter
  // selects by default. Not used for navigation in workspaces mode (see
  // `groups` itself, used directly as that mode's flat nav list below) but
  // still computed unconditionally — harmless, and keeps this hook order
  // stable regardless of mode.
  const flatSessions = useMemo<Session[]>(() => {
    const out: Session[] = []
    for (const g of groups) {
      const wsKey = g.workspace?.id ?? 'unfiled'
      if (isWsCollapsed(wsKey)) continue
      for (const ag of g.agentGroups) {
        if (collapsedAgent.has(`${wsKey}::${ag.agentId}`)) continue
        // ADR-057 US-19: walks roots AND their currently-visible (expanded)
        // nested children, in the exact depth-first order AgentSessionList
        // renders them, so ArrowUp/Down/Enter can reach a nested delegate
        // session too — not just its root.
        const nodes = ag.sessions.map((s) => buildSearchNode(s, childrenByParent))
        const rows = flattenSessionTree(nodes, effectiveExpandedIds)
        out.push(...rows.map((r) => r.node.session))
      }
    }
    return out
  }, [groups, isWsCollapsed, collapsedAgent, childrenByParent, effectiveExpandedIds])

  // Keyboard highlight — TWO SEPARATE nav lists, one active per mode (kept
  // explicit rather than interleaved): sessions mode walks `flatSessions`
  // (unchanged, original behavior — collapsed groups excluded). Workspaces
  // mode walks `groups` directly — one entry per workspace header, ALWAYS
  // visible regardless of that workspace's own collapse state (collapsing a
  // group hides its sessions, never its own header row). Resets to the top
  // whenever the active list's shape changes.
  const [highlightIndex, setHighlightIndex] = useState(0)
  useEffect(() => { setHighlightIndex(0) }, [debouncedSearch, fromDate, toDate, wsFilter, flatSessions.length, mode, groups.length])
  const highlighted = mode === 'sessions' ? flatSessions[highlightIndex] : undefined
  const highlightedWorkspace = mode === 'workspaces' ? (groups[highlightIndex]?.workspace ?? null) : null

  // Keep the highlighted row scrolled into view as the user arrows through —
  // targets the session row's id in sessions mode, the workspace header's id
  // in workspaces mode (see the `id={search-ws-${wsKey}}` wrapper in the
  // render loop below).
  useEffect(() => {
    if (mode === 'workspaces') {
      if (!highlightedWorkspace) return
      document.getElementById(`search-ws-${highlightedWorkspace.id}`)?.scrollIntoView({ block: 'nearest' })
      return
    }
    if (!highlighted) return
    document.getElementById(`search-result-${highlighted.id}`)?.scrollIntoView({ block: 'nearest' })
  }, [highlighted, highlightedWorkspace, mode])

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) close() }}>
      {/* max-h uses dvh (dynamic viewport height), not vh: on iPad/iOS Safari,
          vh includes the collapsed-toolbar area, so an 85vh modal is taller
          than the visible viewport and its top clips off-screen when the
          result list grows (e.g. clearing the workspace filter). dvh tracks
          the actual visible height. */}
      <DialogContent
        onEscapeKeyDown={(e) => { if (editingSessionIdsRef.current.size > 0) e.preventDefault() }}
        // Operator direction (session-search enhancement): the rest of the
        // screen must stay 100% visible while search is open — no dim, no
        // blur. Opts THIS dialog out of the shared 80% Deep Space dim via
        // DialogContent's overlayClassName (dialog.tsx) rather than changing
        // the shared default (every other dialog in the app is unaffected).
        // The overlay element itself still renders (just transparent), so
        // outside-click-to-dismiss, Escape, and the focus trap all keep
        // working exactly as before. Since there's no dim to set the panel
        // apart from the page behind it, the panel supplies its own
        // boundary — and it must be STRONGER than the default border:
        // surface-0 equals the page background and --color-border is only
        // ~1.7:1 against it, invisible without the dim. bg-surface-1 lifts
        // the panel one surface step and the muted/40 border reads clearly
        // on the near-black page (1.4.11 non-text boundary). shadow-2xl is
        // restated so this dialog doesn't silently lose it if the shared
        // base ever changes.
        overlayClassName="bg-transparent"
        className="max-w-2xl gap-0 overflow-hidden p-0 flex flex-col max-h-[85dvh] bg-[var(--color-surface-1)] border border-[var(--color-muted)]/40 rounded-2xl shadow-2xl">
        {/* Header with search input + date toggle */}
        <DialogHeader className="space-y-0 px-5 pt-5 pb-3 shrink-0 border-b border-[var(--color-border)]">
          <DialogTitle className="flex items-center gap-2 text-base mb-2">
            <MagnifyingGlass size={16} className="text-[var(--color-accent)]" />
            {mode === 'workspaces' ? 'Switch workspace' : 'Search sessions'}
            {mode === 'sessions' && wsFilter && (
              <span className="ml-1 inline-flex items-center gap-1 rounded-full bg-[var(--color-surface-2)] px-2 py-0.5 text-[10px] font-normal text-[var(--color-secondary)]">
                {workspaces.find((w) => w.id === wsFilter)?.name ?? 'Filtered'}
                <button tabIndex={0} type="button" onClick={() => useUiStore.setState({ searchModalWorkspaceFilter: null })} className="text-[var(--color-muted)] hover:text-[var(--color-secondary)]" aria-label="Clear workspace filter">
                  <X size={10} />
                </button>
              </span>
            )}
          </DialogTitle>
          <DialogDescription className="sr-only">
            {mode === 'workspaces'
              ? 'Switch workspace. Arrow keys move between workspaces, Enter switches to the highlighted one.'
              : 'Search sessions across all workspaces, grouped by workspace and agent.'}
          </DialogDescription>

          <div className="relative">
            <MagnifyingGlass size={16} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--color-muted)]" />
            <Input autoFocus
              placeholder={mode === 'workspaces' ? 'Filter workspaces by name...' : 'Search by title or workspace...'}
              value={searchText}
              onChange={(e) => setSearchText(e.target.value)}
              onKeyDown={(e) => {
                const navLength = mode === 'workspaces' ? groups.length : flatSessions.length
                if (e.key === 'ArrowDown') {
                  e.preventDefault()
                  setHighlightIndex((i) => Math.min(i + 1, navLength - 1))
                } else if (e.key === 'ArrowUp') {
                  e.preventDefault()
                  setHighlightIndex((i) => Math.max(i - 1, 0))
                } else if (e.key === 'Enter') {
                  if (mode === 'workspaces') {
                    if (highlightedWorkspace) {
                      e.preventDefault()
                      handleSwitchWorkspace(highlightedWorkspace)
                    }
                  } else if (highlighted) {
                    e.preventDefault()
                    selectSession(highlighted)
                  }
                }
              }}
              className="pl-9" aria-label={mode === 'workspaces' ? 'Filter workspaces' : 'Search sessions'} />
            {mode === 'sessions' && (
              <button tabIndex={0} type="button" onClick={() => setShowDateFilter((v) => !v)}
                className={cn('absolute right-2 top-1/2 flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded transition-colors',
                  showDateFilter ? 'bg-[var(--color-surface-2)] text-[var(--color-accent)]' : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)]')}
                title="Date range filter" aria-label="Toggle date range filter" aria-pressed={showDateFilter}>
                <Calendar size={15} />
              </button>
            )}
          </div>

          {mode === 'sessions' && showDateFilter && (
            <div className="mt-2 flex items-center gap-2">
              <Input type="date" value={fromDate} onChange={(e) => setFromDate(e.target.value)} className="max-w-[180px]" aria-label="From date" />
              <span className="text-xs text-[var(--color-muted)]">to</span>
              <Input type="date" value={toDate} onChange={(e) => setToDate(e.target.value)} className="max-w-[180px]" aria-label="To date" />
              {(fromDate || toDate) && <button tabIndex={0} type="button" onClick={() => { setFromDate(''); setToDate('') }} className="text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:underline">Clear</button>}
            </div>
          )}
        </DialogHeader>

        {/* Results — collapsible workspace → agent → sessions */}
        <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain py-2">
          {sessionsError || workspacesError ? (
            // Do not fall through to the grouped list on a workspaces-fetch
            // failure — that would silently bucket everything under "Unfiled"
            // and present a transient outage as if no session had a workspace.
            <div className="px-3 py-10 text-center text-sm text-[var(--color-error)]">Could not load sessions — try again</div>
          ) : loading ? (
            <div className="px-3 py-10 text-center text-sm text-[var(--color-muted)]">
              {mode === 'workspaces' ? 'Loading workspaces...' : 'Loading sessions...'}
            </div>
          ) : total === 0 ? (
            <div className="px-3 py-10 text-center text-sm text-[var(--color-muted)]">
              {mode === 'workspaces' ? (
                <p>No workspaces found{debouncedSearch ? ` for "${debouncedSearch}"` : ''}.</p>
              ) : (
                <p>No sessions found{wsFilter ? ' in this workspace' : ''}{debouncedSearch ? ` for "${debouncedSearch}"` : ''}.</p>
              )}
              {mode === 'workspaces' ? (
                debouncedSearch && (
                  <button tabIndex={0}
                    type="button"
                    onClick={() => setSearchText('')}
                    className="mt-2 text-xs text-[var(--color-accent)] hover:underline"
                  >
                    Clear filter
                  </button>
                )
              ) : (wsFilter || debouncedSearch || fromDate || toDate) && (
                <button tabIndex={0}
                  type="button"
                  onClick={() => { setSearchText(''); setFromDate(''); setToDate(''); useUiStore.setState({ searchModalWorkspaceFilter: null }) }}
                  className="mt-2 text-xs text-[var(--color-accent)] hover:underline"
                >
                  Clear all filters
                </button>
              )}
            </div>
          ) : (
            groups.map((group, idx) => {
              const wsKey = group.workspace?.id ?? 'unfiled'
              const wsCollapsed = isWsCollapsed(wsKey)
              const wsHighlighted = mode === 'workspaces' && idx === highlightIndex
              return (
                <div key={wsKey} id={`search-ws-${wsKey}`} className="mb-1">
                  <WorkspaceHeader
                    name={group.workspace?.name ?? 'Unfiled'}
                    isCollapsed={wsCollapsed}
                    onToggle={() => toggleWs(wsKey)}
                    panelId={`ws-panel-${wsKey}`}
                    // 'Unfiled' (group.workspace === null) has no real workspace
                    // to switch to — omit the arrow entirely for that group.
                    // Never null in workspaces mode (Unfiled is excluded there),
                    // so an empty (zero-session) workspace still gets its arrow.
                    onSwitch={group.workspace ? () => handleSwitchWorkspace(group.workspace!) : undefined}
                    isHighlighted={wsHighlighted}
                  />
                  {!wsCollapsed && (
                    <div id={`ws-panel-${wsKey}`} role="region" className="space-y-0.5 px-2 pb-1">
                      {group.agentGroups.map((ag) => {
                        const agentKey = `${wsKey}::${ag.agentId}`
                        const agentCollapsed = collapsedAgent.has(agentKey)
                        // agentsError means we genuinely don't know whether this id still
                        // exists — never claim "[removed]" (an outage isn't a deletion).
                        const agentName = ag.agent?.name ?? (agentsError || ag.agentId === 'unknown' ? 'Unknown' : '[removed]')
                        return (
                          <div key={agentKey}>
                            {/* Always shown — even for a single-agent workspace
                                group (previously gated behind
                                agentGroups.length > 1). User direction: the
                                agent sub-header is useful context (avatar +
                                name) regardless of how many agents a
                                workspace has. */}
                            <AgentHeader agent={ag.agent} name={agentName} isCollapsed={agentCollapsed} onToggle={() => toggleAgent(agentKey)} panelId={`agent-panel-${agentKey}`} />
                            {!agentCollapsed && (
                              <div id={`agent-panel-${agentKey}`} role="region" className="space-y-0.5 pl-3">
                                {/* ADR-057 US-19/FR-093/FR-094: a root session
                                    with delegated children (child_count > 0)
                                    renders with an expand toggle; children
                                    nest indented beneath it, collapsed by
                                    default (or auto-revealed by an active
                                    search match beneath them). Virtualized
                                    once the group's row count crosses
                                    VIRTUALIZE_ROW_THRESHOLD. */}
                                <AgentSessionList
                                  sessions={ag.sessions}
                                  childrenByParent={childrenByParent}
                                  expandedIds={effectiveExpandedIds}
                                  toggleExpand={toggleManualExpand}
                                  activeSessionId={activeSessionId}
                                  highlightedId={highlighted?.id}
                                  mode={mode}
                                  selectSession={selectSession}
                                  onRename={(id, title) => renameMut.mutate({ id, title })}
                                  onDelete={(id) => deleteMut.mutate(id)}
                                  isDeleting={(id) => deleteMut.isPending && deleteMut.variables === id}
                                  onEditingChange={handleEditingChange}
                                />
                              </div>
                            )}
                          </div>
                        )
                      })}
                    </div>
                  )}
                </div>
              )
            })
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
