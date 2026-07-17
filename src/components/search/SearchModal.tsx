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
import type { Session, Workspace, Agent } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useSelectSession } from '@/components/chat/useSelectSession'
import { IconRenderer } from '@/components/shared/IconRenderer'
import { cn, initialOf } from '@/lib/utils'

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

function WorkspaceHeader({ name, isCollapsed, onToggle, panelId, onSwitch }: {
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
}) {
  return (
    <div className="flex items-center w-full">
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
      </button>
      {onSwitch && (
        <button tabIndex={0}
          type="button"
          onClick={(e) => { e.stopPropagation(); onSwitch() }}
          data-testid="workspace-switch-arrow"
          aria-label={`Switch to workspace ${name}`}
          title={`Switch to workspace ${name}`}
          className="shrink-0 mr-2 flex items-center justify-center rounded p-1 text-[var(--color-accent)] opacity-80 hover:opacity-100 hover:underline transition-all"
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

  // Collapse state — default: all expanded (empty sets)
  const [collapsedWs, setCollapsedWs] = useState<Set<string>>(new Set())
  const [collapsedAgent, setCollapsedAgent] = useState<Set<string>>(new Set())

  const toggleWs = useCallback((key: string) => {
    setCollapsedWs((prev) => { const n = new Set(prev); n.has(key) ? n.delete(key) : n.add(key); return n })
  }, [])
  const toggleAgent = useCallback((key: string) => {
    setCollapsedAgent((prev) => { const n = new Set(prev); n.has(key) ? n.delete(key) : n.add(key); return n })
  }, [])

  useEffect(() => {
    if (!open) {
      setSearchText(''); setShowDateFilter(false); setFromDate(''); setToDate(''); setCollapsedWs(new Set()); setCollapsedAgent(new Set())
      editingSessionIdsRef.current.clear()
    }
  }, [open])

  const { data: sessions = [], isLoading: sLoading, isError: sessionsError } = useQuery({ queryKey: ['sessions'], queryFn: () => fetchSessions(), enabled: open })
  const { data: workspaces = [], isLoading: wLoading, isError: workspacesError } = useQuery({ queryKey: workspacesQueryKeys.list({ status: 'active' }), queryFn: () => fetchWorkspaces({ status: 'active' }), enabled: open })
  const { data: agents = [], isError: agentsError } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents, enabled: open })

  const selectSession = useSelectSession({ agents, workspaces, onClose: close })

  // Workspace-switch arrow (WorkspaceHeader) — mirrors Sidebar.tsx's
  // "New chat" workspace-row action EXACTLY (setActiveWorkspaceId ->
  // startNewSession -> navigate -> close): switching from search should land
  // the user on a fresh composer in the target workspace, not silently
  // re-attach whatever session happened to be active there before. Reads
  // both stores via getState() (a plain click handler, not something that
  // needs reactive re-renders) — the same pattern this file already uses for
  // deleteMut's pruneSessionDescriptor call above.
  const handleSwitchWorkspace = useCallback((ws: Workspace) => {
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

  const groups = useMemo<WsGroup[]>(() => {
    const wsMap = new Map(workspaces.map((w) => [w.id, w]))
    const q = debouncedSearch.trim().toLowerCase()
    const fromT = fromDate ? new Date(`${fromDate}T00:00:00`).getTime() : null
    const toT = toDate ? new Date(`${toDate}T23:59:59`).getTime() : null

    const filtered = sessions.filter((s) => {
      // Workspace pre-filter (from the sidebar "More…" button)
      if (wsFilter && s.workspace_id !== wsFilter) return false
      if (q) {
        const wsName = s.workspace_id ? (wsMap.get(s.workspace_id)?.name ?? '').toLowerCase() : ''
        if (!(s.title ?? '').toLowerCase().includes(q) && !wsName.includes(q)) return false
      }
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
      result.push({ workspace: wsId ? wsMap.get(wsId) ?? null : null, agentGroups: agGroups, totalCount: sess.length })
    }
    result.sort((a, b) => {
      if (a.workspace === null && b.workspace !== null) return 1
      if (a.workspace !== null && b.workspace === null) return -1
      return new Date(b.agentGroups[0]?.sessions[0]?.updated_at ?? '').getTime() - new Date(a.agentGroups[0]?.sessions[0]?.updated_at ?? '').getTime()
    })
    return result
  }, [sessions, workspaces, agents, debouncedSearch, fromDate, toDate, wsFilter])

  const total = groups.reduce((n, g) => n + g.totalCount, 0)
  const loading = sLoading || wLoading

  // Flat, in-render-order list of VISIBLE sessions (collapsed groups excluded)
  // — the arrow-key navigation space. Index 0 is what Enter selects by default.
  const flatSessions = useMemo<Session[]>(() => {
    const out: Session[] = []
    for (const g of groups) {
      const wsKey = g.workspace?.id ?? 'unfiled'
      if (collapsedWs.has(wsKey)) continue
      for (const ag of g.agentGroups) {
        if (collapsedAgent.has(`${wsKey}::${ag.agentId}`)) continue
        out.push(...ag.sessions)
      }
    }
    return out
  }, [groups, collapsedWs, collapsedAgent])

  // Keyboard highlight — follows ArrowUp/ArrowDown from the search input;
  // resets to the top whenever the result set changes.
  const [highlightIndex, setHighlightIndex] = useState(0)
  useEffect(() => { setHighlightIndex(0) }, [debouncedSearch, fromDate, toDate, wsFilter, flatSessions.length])
  const highlighted = flatSessions[highlightIndex]

  // Keep the highlighted row scrolled into view as the user arrows through.
  useEffect(() => {
    if (!highlighted) return
    document.getElementById(`search-result-${highlighted.id}`)?.scrollIntoView({ block: 'nearest' })
  }, [highlighted])

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
        // boundary: `border` + `shadow-2xl` here (shadow-2xl is already the
        // DialogContent base default; restated explicitly so this dialog
        // doesn't silently lose it if the shared base ever changes).
        overlayClassName="bg-transparent"
        className="max-w-2xl gap-0 overflow-hidden p-0 flex flex-col max-h-[85dvh] bg-[var(--color-surface-0)] border border-[var(--color-border)] rounded-2xl shadow-2xl">
        {/* Header with search input + date toggle */}
        <DialogHeader className="space-y-0 px-5 pt-5 pb-3 shrink-0 border-b border-[var(--color-border)]">
          <DialogTitle className="flex items-center gap-2 text-base mb-2">
            <MagnifyingGlass size={16} className="text-[var(--color-accent)]" />
            Search sessions
            {wsFilter && (
              <span className="ml-1 inline-flex items-center gap-1 rounded-full bg-[var(--color-surface-2)] px-2 py-0.5 text-[10px] font-normal text-[var(--color-secondary)]">
                {workspaces.find((w) => w.id === wsFilter)?.name ?? 'Filtered'}
                <button tabIndex={0} type="button" onClick={() => useUiStore.setState({ searchModalWorkspaceFilter: null })} className="text-[var(--color-muted)] hover:text-[var(--color-secondary)]" aria-label="Clear workspace filter">
                  <X size={10} />
                </button>
              </span>
            )}
          </DialogTitle>
          <DialogDescription className="sr-only">Search sessions across all workspaces, grouped by workspace and agent.</DialogDescription>

          <div className="relative">
            <MagnifyingGlass size={16} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--color-muted)]" />
            <Input autoFocus placeholder="Search by title or workspace..." value={searchText}
              onChange={(e) => setSearchText(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'ArrowDown') {
                  e.preventDefault()
                  setHighlightIndex((i) => Math.min(i + 1, flatSessions.length - 1))
                } else if (e.key === 'ArrowUp') {
                  e.preventDefault()
                  setHighlightIndex((i) => Math.max(i - 1, 0))
                } else if (e.key === 'Enter' && highlighted) {
                  e.preventDefault()
                  selectSession(highlighted)
                }
              }}
              className="pl-9" aria-label="Search sessions" />
            <button tabIndex={0} type="button" onClick={() => setShowDateFilter((v) => !v)}
              className={cn('absolute right-2 top-1/2 flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded transition-colors',
                showDateFilter ? 'bg-[var(--color-surface-2)] text-[var(--color-accent)]' : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)]')}
              title="Date range filter" aria-label="Toggle date range filter" aria-pressed={showDateFilter}>
              <Calendar size={15} />
            </button>
          </div>

          {showDateFilter && (
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
            <div className="px-3 py-10 text-center text-sm text-[var(--color-muted)]">Loading sessions...</div>
          ) : total === 0 ? (
            <div className="px-3 py-10 text-center text-sm text-[var(--color-muted)]">
              <p>No sessions found{wsFilter ? ' in this workspace' : ''}{debouncedSearch ? ` for "${debouncedSearch}"` : ''}.</p>
              {(wsFilter || debouncedSearch || fromDate || toDate) && (
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
            groups.map((group) => {
              const wsKey = group.workspace?.id ?? 'unfiled'
              const wsCollapsed = collapsedWs.has(wsKey)
              return (
                <div key={wsKey} className="mb-1">
                  <WorkspaceHeader
                    name={group.workspace?.name ?? 'Unfiled'}
                    isCollapsed={wsCollapsed}
                    onToggle={() => toggleWs(wsKey)}
                    panelId={`ws-panel-${wsKey}`}
                    // 'Unfiled' (group.workspace === null) has no real workspace
                    // to switch to — omit the arrow entirely for that group.
                    onSwitch={group.workspace ? () => handleSwitchWorkspace(group.workspace!) : undefined}
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
                                {ag.sessions.map((s) => (
                                  <SessionRow key={s.id} session={s} isActive={s.id === activeSessionId} isHighlighted={s.id === highlighted?.id} onSelect={() => selectSession(s)}
                                    onRename={(t) => renameMut.mutate({ id: s.id, title: t })}
                                    onDelete={() => deleteMut.mutate(s.id)}
                                    deleting={deleteMut.isPending && deleteMut.variables === s.id}
                                    onEditingChange={handleEditingChange} />
                                ))}
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
