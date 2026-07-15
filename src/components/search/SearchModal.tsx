// SearchModal — cross-workspace session search overlay (step 6 of the
// sidebar-merge plan). A Radix Dialog driven by `useUiStore.searchModalOpen`,
// mounted once at the AppShell root so both entry points — the sidebar search
// icon (Sidebar.tsx) and the /search slash command (useSlashMenu.ts) — drive
// the same single instance.
//
// Data: fetchSessions() + fetchWorkspaces({status:'active'}) + fetchAgents(),
// all lazily enabled on open. Filtering, grouping, and sorting are client-side
// (the session catalog is small enough for that and the workspace grouping is a
// pure-SPA concern). Session selection reuses the shared `useSelectSession` hook
// (same logic the sidebar accordion + SessionPanel use) so attach/navigate/close
// behaviour is identical everywhere a session can be picked.
//
// Design: "Sovereign Deep" dark-first, CSS-variable tokens, Phosphor icons.

import { useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { MagnifyingGlass, Calendar } from '@phosphor-icons/react'
import { fetchAgents, fetchSessions, fetchWorkspaces, workspacesQueryKeys } from '@/lib/api'
import type { Session, Workspace } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useSelectSession } from '@/components/chat/useSelectSession'
import { cn } from '@/lib/utils'

// ── helpers ──────────────────────────────────────────────────────────────────

/** Debounce a fast-changing value (the search box) so filtering doesn't run on
 *  every keystroke. 200ms per the plan. */
function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const handle = setTimeout(() => setDebounced(value), delayMs)
    return () => clearTimeout(handle)
  }, [value, delayMs])
  return debounced
}

/** Relative time — matches the formatter in SessionItem.tsx (not exported there,
 *  so inlined here to avoid touching Agent A's file). "just now" / "12m ago" /
 *  "3h ago" / "2d ago" / "Jul 14" for older. */
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

/** Absolute timestamp for the native tooltip — full date/time on hover. */
function formatAbsolute(dateStr: string): string {
  const date = new Date(dateStr)
  if (isNaN(date.getTime())) return ''
  return date.toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

/** Compact token counter: 850 → "850 tokens", 1200 → "1.2k tokens",
 *  15000 → "15k tokens", 2_400_000 → "2.4M tokens". */
function formatTokens(n?: number): string {
  if (!n || n <= 0) return ''
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, '')}M tokens`
  if (n >= 1000) return `${(n / 1000).toFixed(1).replace(/\.0$/, '')}k tokens`
  return `${n} tokens`
}

// ── types ────────────────────────────────────────────────────────────────────

interface WorkspaceGroup {
  /** null = the "Unfiled" bucket (no workspace / workspace not found). */
  workspace: Workspace | null
  sessions: Session[]
}

// ── component ────────────────────────────────────────────────────────────────

export function SearchModal() {
  const open = useUiStore((s) => s.searchModalOpen)
  const closeSearchModal = useUiStore((s) => s.closeSearchModal)

  const [searchText, setSearchText] = useState('')
  const [showDateFilter, setShowDateFilter] = useState(false)
  const [fromDate, setFromDate] = useState('')
  const [toDate, setToDate] = useState('')

  const debouncedSearch = useDebouncedValue(searchText, 200)

  // Reset all filters the moment the modal closes so the next open starts clean.
  useEffect(() => {
    if (!open) {
      setSearchText('')
      setShowDateFilter(false)
      setFromDate('')
      setToDate('')
    }
  }, [open])

  // Lazily fetch only while open — avoids loading the full session catalog on
  // every app boot. The ['sessions'] / workspaces / agents query keys are shared
  // with the rest of the app so a warm cache (e.g. SessionPanel just opened)
  // makes the modal instant.
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

  // Client-side filter → group by workspace → sort recent-first.
  const groups = useMemo<WorkspaceGroup[]>(() => {
    const wsMap = new Map(workspaces.map((w) => [w.id, w]))
    const query = debouncedSearch.trim().toLowerCase()

    const fromTime = fromDate ? new Date(`${fromDate}T00:00:00`).getTime() : null
    const toTime = toDate ? new Date(`${toDate}T23:59:59`).getTime() : null

    const filtered = sessions.filter((s) => {
      // Text match against session title OR its workspace's name.
      if (query) {
        const wsName = s.workspace_id ? (wsMap.get(s.workspace_id)?.name ?? '').toLowerCase() : ''
        const title = (s.title ?? '').toLowerCase()
        if (!title.includes(query) && !wsName.includes(query)) return false
      }
      // Date-range filter on updated_at (the "last interaction" timestamp).
      const updated = new Date(s.updated_at).getTime()
      if (fromTime !== null || toTime !== null) {
        if (isNaN(updated)) return false
        if (fromTime !== null && updated < fromTime) return false
        if (toTime !== null && updated > toTime) return false
      }
      return true
    })

    // Group by workspace; sessions whose workspace_id is absent or resolves to
    // no active workspace land in the null ("Unfiled") bucket.
    const buckets = new Map<string | null, Session[]>()
    for (const s of filtered) {
      const key = s.workspace_id && wsMap.has(s.workspace_id) ? s.workspace_id : null
      const arr = buckets.get(key)
      if (arr) arr.push(s)
      else buckets.set(key, [s])
    }

    // Sort each bucket's sessions by updated_at descending (recent first).
    for (const arr of buckets.values()) {
      arr.sort((a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime())
    }

    // Build the group list; order groups by their most-recent session's
    // updated_at, with the Unfiled bucket always pinned last.
    const groupList: WorkspaceGroup[] = []
    for (const [wsId, sess] of buckets) {
      groupList.push({ workspace: wsId ? (wsMap.get(wsId) ?? null) : null, sessions: sess })
    }
    groupList.sort((a, b) => {
      if (a.workspace === null && b.workspace !== null) return 1
      if (a.workspace !== null && b.workspace === null) return -1
      const aMax = a.sessions[0]?.updated_at ?? ''
      const bMax = b.sessions[0]?.updated_at ?? ''
      return new Date(bMax).getTime() - new Date(aMax).getTime()
    })
    return groupList
  }, [sessions, workspaces, debouncedSearch, fromDate, toDate])

  const totalResults = groups.reduce((n, g) => n + g.sessions.length, 0)
  const loading = sessionsLoading || workspacesLoading

  // Flatten for Enter-selects-first: the first session across ordered groups.
  const firstResult = groups[0]?.sessions[0]

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) closeSearchModal() }}>
      <DialogContent className="max-w-2xl gap-0 overflow-hidden p-0 flex flex-col">
        <DialogHeader className="space-y-0 px-5 pt-5 pb-3">
          <DialogTitle className="flex items-center gap-2 text-base">
            <MagnifyingGlass size={16} className="text-[var(--color-accent)]" />
            Search sessions
          </DialogTitle>
          <DialogDescription className="sr-only">
            Search across all workspaces and sessions by title, workspace name, or date range.
          </DialogDescription>

          {/* Search field with leading magnifier + trailing date-filter toggle */}
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
                // Enter selects the first matching session — matches the plan's
                // "Enter selects the first result" keyboard affordance.
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

          {/* Date range — collapsed by default so it doesn't clutter the chrome */}
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

        {/* Results — the only scroll region; groups render beneath */}
        <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-4">
          {loading ? (
            <div className="px-3 py-10 text-center text-sm text-[var(--color-muted)]">
              Loading sessions...
            </div>
          ) : totalResults === 0 ? (
            <div className="px-3 py-10 text-center text-sm text-[var(--color-muted)]">
              No sessions found
            </div>
          ) : (
            groups.map((group) => (
              <section key={group.workspace?.id ?? 'unfiled'} className="mb-1">
                <h3 className="px-3 py-1.5 text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">
                  {group.workspace?.name ?? 'Unfiled'}
                  <span className="ml-1.5 font-normal opacity-60">({group.sessions.length})</span>
                </h3>
                {group.sessions.map((session) => (
                  <SearchResultRow
                    key={session.id}
                    session={session}
                    onSelect={() => selectSession(session)}
                  />
                ))}
              </section>
            ))
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}

// ── result row ───────────────────────────────────────────────────────────────

interface SearchResultRowProps {
  session: Session
  onSelect: () => void
}

function SearchResultRow({ session, onSelect }: SearchResultRowProps) {
  return (
    <button
      type="button"
      onClick={onSelect}
      title={`Started ${formatAbsolute(session.created_at)} · Last active ${formatAbsolute(session.updated_at)}`}
      className="group flex w-full items-center gap-3 rounded-md px-3 py-2 text-left transition-colors hover:bg-[var(--color-surface-2)] focus-visible:bg-[var(--color-surface-2)] focus-visible:outline-none"
    >
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm font-medium text-[var(--color-secondary)]">
          {session.title || 'Untitled session'}
        </div>
        <div className="mt-0.5 flex items-center gap-1.5 text-[11px] text-[var(--color-muted)]">
          <span>Started {formatRelative(session.created_at)}</span>
          <span aria-hidden="true">·</span>
          <span>Active {formatRelative(session.updated_at)}</span>
        </div>
      </div>
      {session.total_tokens ? (
        <span className="shrink-0 rounded-full bg-[var(--color-surface-2)] px-2 py-0.5 font-mono text-[10px] text-[var(--color-muted)] group-hover:bg-[var(--color-surface-3)]">
          {formatTokens(session.total_tokens)}
        </span>
      ) : null}
    </button>
  )
}
