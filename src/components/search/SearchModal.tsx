// SearchModal — cross-workspace session search + management overlay.
// Opened from the sidebar search icon. Full depth: workspace → agent grouping,
// start/last-active dates, token count, inline rename (edit icon), delete.
// Driven by useUiStore.searchModalOpen.

import { useEffect, useMemo, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { MagnifyingGlass, Calendar, PencilSimple, Trash, Check, X } from '@phosphor-icons/react'
import { fetchAgents, fetchSessions, fetchWorkspaces, renameSession, deleteSession, workspacesQueryKeys } from '@/lib/api'
import type { Session, Workspace, Agent } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useSelectSession } from '@/components/chat/useSelectSession'
import { cn } from '@/lib/utils'

// ── helpers ──────────────────────────────────────────────────────────────────

function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const handle = setTimeout(() => setDebounced(value), delayMs)
    return () => clearTimeout(handle)
  }, [value, delayMs])
  return debounced
}

function formatRelative(dateStr: string): string {
  const date = new Date(dateStr)
  if (isNaN(date.getTime())) return ''
  const diffMs = Date.now() - date.getTime()
  const diffMins = Math.floor(diffMs / 60_000)
  if (diffMins < 1) return 'just now'
  if (diffMins < 60) return `${diffMins}m ago`
  const diffHours = Math.floor(diffMins / 60)
  if (diffHours < 24) return `${diffHours}h ago`
  const diffDays = Math.floor(diffHours / 24)
  if (diffDays < 7) return `${diffDays}d ago`
  return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

function formatTokens(n?: number): string {
  if (!n || n <= 0) return ''
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, '')}M`
  if (n >= 1000) return `${(n / 1000).toFixed(1).replace(/\.0$/, '')}k`
  return `${n}`
}

function agentName(agentId: string | undefined, agents: Agent[]): string {
  if (!agentId) return 'Unknown'
  return agents.find((a) => a.id === agentId)?.name ?? 'Unknown'
}

// ── types ────────────────────────────────────────────────────────────────────

interface AgentGroup {
  agentId: string
  agentName: string
  sessions: Session[]
}

interface WorkspaceGroup {
  workspace: Workspace | null
  agentGroups: AgentGroup[]
  totalCount: number
}

// ── component ────────────────────────────────────────────────────────────────

export function SearchModal() {
  const open = useUiStore((s) => s.searchModalOpen)
  const closeSearchModal = useUiStore((s) => s.closeSearchModal)
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)

  const [searchText, setSearchText] = useState('')
  const [showDateFilter, setShowDateFilter] = useState(false)
  const [fromDate, setFromDate] = useState('')
  const [toDate, setToDate] = useState('')

  const debouncedSearch = useDebouncedValue(searchText, 200)

  useEffect(() => {
    if (!open) {
      setSearchText('')
      setShowDateFilter(false)
      setFromDate('')
      setToDate('')
    }
  }, [open])

  const { data: sessions = [], isLoading: sessionsLoading } = useQuery({
    queryKey: ['sessions'],
    queryFn: () => fetchSessions(),
    enabled: open,
  })
  const { data: workspaces = [], isLoading: workspacesLoading } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchWorkspaces({ status: 'active' }),
    enabled: open,
  })
  const { data: agents = [] } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    enabled: open,
  })

  const selectSession = useSelectSession({
    agents,
    workspaces,
    onClose: closeSearchModal,
  })

  // Rename mutation
  const renameMut = useMutation({
    mutationFn: ({ id, title }: { id: string; title: string }) => renameSession(id, title),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['sessions'] }),
    onError: (err) => addToast({ message: err instanceof Error ? err.message : 'Rename failed', variant: 'error' }),
  })

  // Delete mutation
  const deleteMut = useMutation({
    mutationFn: (id: string) => deleteSession(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['sessions'] }),
    onError: (err) => addToast({ message: err instanceof Error ? err.message : 'Delete failed', variant: 'error' }),
  })

  // Client-side filter → group by workspace → sub-group by agent → sort recent-first.
  const groups = useMemo<WorkspaceGroup[]>(() => {
    const wsMap = new Map(workspaces.map((w) => [w.id, w]))
    const query = debouncedSearch.trim().toLowerCase()
    const fromTime = fromDate ? new Date(`${fromDate}T00:00:00`).getTime() : null
    const toTime = toDate ? new Date(`${toDate}T23:59:59`).getTime() : null

    const filtered = sessions.filter((s) => {
      if (query) {
        const wsName = s.workspace_id ? (wsMap.get(s.workspace_id)?.name ?? '').toLowerCase() : ''
        const title = (s.title ?? '').toLowerCase()
        if (!title.includes(query) && !wsName.includes(query)) return false
      }
      const updated = new Date(s.updated_at).getTime()
      if (fromTime !== null || toTime !== null) {
        if (isNaN(updated)) return false
        if (fromTime !== null && updated < fromTime) return false
        if (toTime !== null && updated > toTime) return false
      }
      return true
    })

    // Group by workspace
    const wsBuckets = new Map<string | null, Session[]>()
    for (const s of filtered) {
      const key = s.workspace_id && wsMap.has(s.workspace_id) ? s.workspace_id : null
      const arr = wsBuckets.get(key)
      if (arr) arr.push(s)
      else wsBuckets.set(key, [s])
    }

    // Within each workspace, sub-group by agent (first participating agent)
    const groupList: WorkspaceGroup[] = []
    for (const [wsId, sess] of wsBuckets) {
      const agentBuckets = new Map<string, Session[]>()
      for (const s of sess) {
        const aId = s.active_agent_id ?? s.agent_id ?? 'unknown'
        const arr = agentBuckets.get(aId)
        if (arr) arr.push(s)
        else agentBuckets.set(aId, [s])
      }
      const agentGroups: AgentGroup[] = []
      for (const [aId, aSess] of agentBuckets) {
        aSess.sort((a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime())
        agentGroups.push({ agentId: aId, agentName: agentName(aId, agents), sessions: aSess })
      }
      // Sort agent groups by their most-recent session
      agentGroups.sort((a, b) =>
        new Date(b.sessions[0]?.updated_at ?? '').getTime() - new Date(a.sessions[0]?.updated_at ?? '').getTime()
      )
      groupList.push({
        workspace: wsId ? (wsMap.get(wsId) ?? null) : null,
        agentGroups,
        totalCount: sess.length,
      })
    }

    // Sort workspace groups by their most-recent session
    groupList.sort((a, b) => {
      if (a.workspace === null && b.workspace !== null) return 1
      if (a.workspace !== null && b.workspace === null) return -1
      const aMax = a.agentGroups[0]?.sessions[0]?.updated_at ?? ''
      const bMax = b.agentGroups[0]?.sessions[0]?.updated_at ?? ''
      return new Date(bMax).getTime() - new Date(aMax).getTime()
    })
    return groupList
  }, [sessions, workspaces, agents, debouncedSearch, fromDate, toDate])

  const totalResults = groups.reduce((n, g) => n + g.totalCount, 0)
  const loading = sessionsLoading || workspacesLoading
  const firstResult = groups[0]?.agentGroups[0]?.sessions[0]

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) closeSearchModal() }}>
      <DialogContent className="max-w-2xl gap-0 overflow-hidden p-0 flex flex-col max-h-[85vh]">
        <DialogHeader className="space-y-0 px-5 pt-5 pb-3 shrink-0">
          <DialogTitle className="flex items-center gap-2 text-base">
            <MagnifyingGlass size={16} className="text-[var(--color-accent)]" />
            Search sessions
          </DialogTitle>
          <DialogDescription className="sr-only">
            Search across all workspaces and sessions. Grouped by workspace and agent.
          </DialogDescription>

          <div className="relative mt-2">
            <MagnifyingGlass
              size={16}
              className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--color-muted)]"
            />
            <Input
              autoFocus
              placeholder="Search by title or workspace..."
              value={searchText}
              onChange={(e) => setSearchText(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && firstResult) {
                  e.preventDefault()
                  selectSession(firstResult)
                }
              }}
              className="pl-9"
              aria-label="Search sessions"
            />
            <button
              type="button"
              onClick={() => setShowDateFilter((v) => !v)}
              className={cn(
                'absolute right-2 top-1/2 flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded transition-colors',
                showDateFilter
                  ? 'bg-[var(--color-surface-2)] text-[var(--color-accent)]'
                  : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
              )}
              title="Toggle date range filter"
              aria-label="Toggle date range filter"
              aria-pressed={showDateFilter}
            >
              <Calendar size={15} />
            </button>
          </div>

          {showDateFilter && (
            <div className="mt-2 flex items-center gap-2">
              <Input
                type="date"
                value={fromDate}
                onChange={(e) => setFromDate(e.target.value)}
                className="max-w-[180px]"
                aria-label="Filter from date"
              />
              <span className="text-xs text-[var(--color-muted)]">to</span>
              <Input
                type="date"
                value={toDate}
                onChange={(e) => setToDate(e.target.value)}
                className="max-w-[180px]"
                aria-label="Filter to date"
              />
              {(fromDate || toDate) && (
                <button
                  type="button"
                  onClick={() => { setFromDate(''); setToDate('') }}
                  className="text-xs text-[var(--color-muted)] underline-offset-2 hover:text-[var(--color-secondary)] hover:underline"
                >
                  Clear
                </button>
              )}
            </div>
          )}
        </DialogHeader>

        {/* Results — workspace → agent sub-groups with metadata + edit/delete */}
        <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-4">
          {loading ? (
            <div className="px-3 py-10 text-center text-sm text-[var(--color-muted)]">Loading sessions...</div>
          ) : totalResults === 0 ? (
            <div className="px-3 py-10 text-center text-sm text-[var(--color-muted)]">No sessions found</div>
          ) : (
            groups.map((group) => (
              <section key={group.workspace?.id ?? 'unfiled'} className="mb-2">
                {/* Workspace header */}
                <h3 className="px-3 py-1.5 text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">
                  {group.workspace?.name ?? 'Unfiled'}
                  <span className="ml-1.5 font-normal opacity-60">({group.totalCount})</span>
                </h3>

                {group.agentGroups.map((ag) => (
                  <div key={ag.agentId} className="mb-0.5">
                    {/* Agent sub-header */}
                    {group.agentGroups.length > 1 && (
                      <div className="flex items-center gap-1.5 px-5 py-0.5 text-[10px] text-[var(--color-muted)] opacity-70">
                        <span className="h-1 w-1 rounded-full bg-[var(--color-muted)]" />
                        {ag.agentName}
                      </div>
                    )}

                    {/* Session rows with metadata + edit/delete */}
                    {ag.sessions.map((session) => (
                      <SearchResultRow
                        key={session.id}
                        session={session}
                        onSelect={() => selectSession(session)}
                        onRename={(title) => renameMut.mutate({ id: session.id, title })}
                        onDelete={() => deleteMut.mutate(session.id)}
                        deleting={deleteMut.isPending && deleteMut.variables === session.id}
                      />
                    ))}
                  </div>
                ))}
              </section>
            ))
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}

// ── result row with metadata + edit + delete ─────────────────────────────────

interface SearchResultRowProps {
  session: Session
  onSelect: () => void
  onRename: (title: string) => void
  onDelete: () => void
  deleting: boolean
}

function SearchResultRow({ session, onSelect, onRename, onDelete, deleting }: SearchResultRowProps) {
  const [isEditing, setIsEditing] = useState(false)
  const [editValue, setEditValue] = useState(session.title || '')

  const commitRename = () => {
    const trimmed = editValue.trim()
    if (trimmed && trimmed !== session.title) {
      onRename(trimmed)
    }
    setIsEditing(false)
  }

  if (isEditing) {
    return (
      <div className="flex items-center gap-2 rounded-md px-3 py-2 bg-[var(--color-surface-2)]">
        <Input
          autoFocus
          value={editValue}
          onChange={(e) => setEditValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') { e.preventDefault(); commitRename() }
            if (e.key === 'Escape') { setIsEditing(false); setEditValue(session.title || '') }
          }}
          className="h-7 flex-1 text-sm"
        />
        <button
          type="button"
          onClick={commitRename}
          className="shrink-0 rounded p-1 text-[var(--color-success)] hover:bg-[var(--color-surface-3)]"
          aria-label="Confirm rename"
        >
          <Check size={14} weight="bold" />
        </button>
        <button
          type="button"
          onClick={() => { setIsEditing(false); setEditValue(session.title || '') }}
          className="shrink-0 rounded p-1 text-[var(--color-muted)] hover:bg-[var(--color-surface-3)]"
          aria-label="Cancel rename"
        >
          <X size={14} />
        </button>
      </div>
    )
  }

  return (
    <div className="group flex items-center gap-2 rounded-md px-3 py-2 transition-colors hover:bg-[var(--color-surface-2)]">
      <button
        type="button"
        onClick={onSelect}
        className="min-w-0 flex-1 text-left"
      >
        <div className="truncate text-sm font-medium text-[var(--color-secondary)]">
          {session.title || 'Untitled session'}
        </div>
        <div className="mt-0.5 flex items-center gap-1.5 text-[11px] text-[var(--color-muted)]">
          <span>Started {formatRelative(session.created_at)}</span>
          <span aria-hidden="true">·</span>
          <span>Active {formatRelative(session.updated_at)}</span>
          {session.total_tokens ? (
            <>
              <span aria-hidden="true">·</span>
              <span className="font-mono">{formatTokens(session.total_tokens)}</span>
            </>
          ) : null}
        </div>
      </button>

      {/* Edit icon — click to rename inline */}
      <button
        type="button"
        onClick={() => { setEditValue(session.title || ''); setIsEditing(true) }}
        className="shrink-0 rounded p-1 text-[var(--color-muted)] opacity-0 group-hover:opacity-100 hover:text-[var(--color-accent)] hover:bg-[var(--color-surface-3)] transition-all"
        aria-label={`Rename ${session.title || 'session'}`}
        title="Rename"
      >
        <PencilSimple size={13} />
      </button>

      {/* Delete icon */}
      <button
        type="button"
        onClick={onDelete}
        disabled={deleting || session.protected === true}
        className="shrink-0 rounded p-1 text-[var(--color-muted)] opacity-0 group-hover:opacity-100 hover:text-[var(--color-error)] hover:bg-[var(--color-surface-3)] transition-all disabled:opacity-30 disabled:cursor-not-allowed"
        aria-label={`Delete ${session.title || 'session'}`}
        title={session.protected === true ? 'Protected (heartbeat)' : 'Delete'}
      >
        <Trash size={13} />
      </button>
    </div>
  )
}
