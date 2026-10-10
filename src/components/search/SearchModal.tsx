// Sessions view — cross-workspace session overview (Wave 1).
// All / Running uses the existing segmented control. FilterMenu and ViewSwitch
// belong to the tasks-panel squad and are not built here.
// Row identity stays the agent header's current mark until AgentIcon (FE-1) lands.

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
import { Button } from '@/components/ui/button'
import { ErrorState } from '@/components/ui/error-state'
import { IconButton } from '@/components/ui/icon-button'
import { SegmentedControl, SegmentedControlItem } from '@/components/ui/segmented-control'
import { MagnifyingGlass, Calendar, X } from '@phosphor-icons/react'
import { fetchAgents, fetchSessions, fetchWorkspaces, renameSession, deleteSession, workspacesQueryKeys, getErrorMessage, readSessionFetchCoverage } from '@/lib/api'
import type { Session, Workspace, Agent } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useSelectSession } from '@/components/chat/useSelectSession'
import { AgentHeader, WorkspaceHeader } from '@/components/sessions/SessionGroupHeaders'
import { SessionOverviewList, visibleOverviewIds } from '@/components/sessions/SessionOverviewList'
import { buildSessionOverview, sessionActivation } from '@/components/sessions/sessionOverview'

function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const handle = setTimeout(() => setDebounced(value), delayMs)
    return () => clearTimeout(handle)
  }, [value, delayMs])
  return debounced
}

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

type StatusFilter = 'all' | 'running'

function partialListMessage(errorCount: number): string {
  if (errorCount > 0) {
    const noun = errorCount === 1 ? 'source error' : 'source errors'
    return `Session list is incomplete (${errorCount} ${noun}). Missing parents are not marked unavailable.`
  }
  return 'Session list is incomplete. Not every page loaded, so missing parents are not marked unavailable.'
}

export function SearchModal() {
  const open = useUiStore((s) => s.searchModalOpen)
  const close = useUiStore((s) => s.closeSearchModal)
  const wsFilter = useUiStore((s) => s.searchModalWorkspaceFilter)
  const agentFilter = useUiStore((s) => s.searchModalAgentFilter)
  // TWO MODES, one panel (see ui.ts's doc comment on searchModalMode):
  // 'sessions' (/sessions + sidebar search + Past sessions) uses the session
  // overview and optional workspace/owner filters. 'workspaces' (/workspace)
  // lists ALL workspaces, starts groups collapsed, and switches on Enter.
  const mode = useUiStore((s) => s.searchModalMode)
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const navigate = useNavigate()
  const editingSessionIdsRef = useRef<Set<string>>(new Set())
  const handleEditingChange = useCallback((sessionId: string, editing: boolean) => {
    if (editing) editingSessionIdsRef.current.add(sessionId)
    else editingSessionIdsRef.current.delete(sessionId)
  }, [])
  const searchInputRef = useRef<HTMLInputElement>(null)

  const [searchText, setSearchText] = useState('')
  const [showDateFilter, setShowDateFilter] = useState(false)
  const [fromDate, setFromDate] = useState('')
  const [toDate, setToDate] = useState('')
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const debouncedSearch = useDebouncedValue(searchText, 200)

  const [collapsedWs, setCollapsedWs] = useState<Set<string>>(new Set())
  const [collapsedAgent, setCollapsedAgent] = useState<Set<string>>(new Set())
  const isWsCollapsed = useCallback(
    (key: string) => (mode === 'workspaces' ? !collapsedWs.has(key) : collapsedWs.has(key)),
    [mode, collapsedWs],
  )
  const toggleWs = useCallback((key: string) => {
    setCollapsedWs((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }, [])
  const toggleAgent = useCallback((key: string) => {
    setCollapsedAgent((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }, [])

  const [manualExpandedIds, setManualExpandedIds] = useState<Set<string>>(new Set())
  const [suppressedAutoIds, setSuppressedAutoIds] = useState<Set<string>>(new Set())
  const narrowingKey = `${statusFilter}|${debouncedSearch}|${fromDate}|${toDate}`
  useEffect(() => { setSuppressedAutoIds(new Set()) }, [narrowingKey])

  const prevModeRef = useRef(mode)
  const activationRef = useRef<string | null>(null)
  const prevFilterKey = useRef('')
  const prevVisible = useRef<string[]>([])
  useEffect(() => {
    const modeChanged = prevModeRef.current !== mode
    prevModeRef.current = mode
    if (!open || modeChanged) {
      setSearchText('')
      setShowDateFilter(false)
      setFromDate('')
      setToDate('')
      setStatusFilter('all')
      setCollapsedWs(new Set())
      setCollapsedAgent(new Set())
      setManualExpandedIds(new Set())
      setSuppressedAutoIds(new Set())
      editingSessionIdsRef.current.clear()
      activationRef.current = null
      prevFilterKey.current = ''
      prevVisible.current = []
      setActivationId(null)
      setAnnouncement('')
    }
  }, [open, mode])

  const { data: sessions = [], isLoading: sLoading, isError: sessionsError, refetch: retrySessions } = useQuery({
    queryKey: ['sessions', 'flat'],
    queryFn: () => fetchSessions(undefined, undefined, { flat: true }),
    enabled: open,
  })
  const { data: workspaces = [], isLoading: wLoading, isError: workspacesError, refetch: retryWorkspaces } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchWorkspaces({ status: 'active' }),
    enabled: open,
  })
  const { data: agentData, isError: agentsError, refetch: retryAgents } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    enabled: open,
  })
  const agents = agentData ?? []
  const agentNamesUnavailable = agentsError && agentData === undefined
  const failedRequests = sessionsError && workspacesError
    ? 'sessions and workspaces'
    : sessionsError ? 'sessions' : 'workspaces'
  const coverage = readSessionFetchCoverage(sessions)
  const requestActivityPanel = useUiStore((s) => s.requestActivityPanel)
  const selectSession = useSelectSession({
    agents,
    workspaces,
    onClose: close,
    onSelected: (session) => requestActivityPanel(session.id),
  })
  const wsMap = useMemo(() => new Map(workspaces.map((workspace) => [workspace.id, workspace])), [workspaces])
  const sessionById = useMemo(() => new Map(sessions.map((session) => [session.id, session])), [sessions])

  const overview = useMemo(() => buildSessionOverview(sessions, {
    view: mode === 'sessions' ? statusFilter : 'all',
    query: mode === 'sessions' ? debouncedSearch : '',
    fromDate: mode === 'sessions' ? fromDate : '',
    toDate: mode === 'sessions' ? toDate : '',
    incomplete: coverage.incomplete,
    workspaceName: (id) => (id ? wsMap.get(id)?.name ?? '' : ''),
    agentName: (id) => agents.find((agent) => agent.id === id)?.name ?? '',
  }), [sessions, mode, statusFilter, debouncedSearch, fromDate, toDate, coverage.incomplete, wsMap, agents])

  const effectiveExpandedIds = useMemo(() => {
    const next = new Set(manualExpandedIds)
    for (const id of overview.autoExpandIds) {
      if (!suppressedAutoIds.has(id)) next.add(id)
    }
    return next
  }, [manualExpandedIds, overview.autoExpandIds, suppressedAutoIds])

  const toggleExpand = useCallback((id: string) => {
    const isOpen = effectiveExpandedIds.has(id)
    if (isOpen) {
      setManualExpandedIds((prev) => { const next = new Set(prev); next.delete(id); return next })
      setSuppressedAutoIds((prev) => { const next = new Set(prev); next.add(id); return next })
      return
    }
    setManualExpandedIds((prev) => { const next = new Set(prev); next.add(id); return next })
    setSuppressedAutoIds((prev) => { const next = new Set(prev); next.delete(id); return next })
  }, [effectiveExpandedIds])

  // FR-004: a modal workspace switch uses the same exact remembered-chat /
  // validated Ava entry as the workspace name, not the + New chat action.
  // Switch the workspace before entry so a successful attach persists the
  // target descriptor under the right key; a failed restore retains the
  // committed chat and exposes Retry without erasing either saved pointer.
  const handleSwitchWorkspace = useCallback((ws: Workspace) => {
    if (ws.id === useWorkspacesStore.getState().activeWorkspaceId) {
      void navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: ws.id } })
      close()
      return
    }
    useWorkspacesStore.getState().setActiveWorkspaceId(ws.id)
    void useSessionStore.getState().enterWorkspaceChat(ws.id)
    void navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: ws.id } })
    close()
  }, [close, navigate])

  const renameMut = useMutation({
    mutationFn: ({ id, title }: { id: string; title: string }) => renameSession(id, title),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['sessions'] }),
    onError: (error) => addToast({ message: getErrorMessage(error, 'Rename failed'), variant: 'error' }),
  })
  const deleteMut = useMutation({
    mutationFn: (id: string) => deleteSession(id),
    onSuccess: (_data, deletedId) => {
      queryClient.invalidateQueries({ queryKey: ['sessions'] })
      useSessionStore.getState().pruneSessionDescriptor(deletedId)
    },
    onError: (error) => addToast({ message: getErrorMessage(error, 'Delete failed'), variant: 'error' }),
  })

  const sortSessions = (a: Session, b: Session) => {
    // The retired 'heartbeat' kind is gone from the generated Session enum
    // (Session.yaml; the computed main replaced it, FR-002/DEL-01), so this
    // sort is recency only.
    return new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()
  }
  const bucketByAgent = useCallback((sess: Session[]): AgentGroup[] => {
    const buckets = new Map<string, Session[]>()
    for (const session of sess) {
      const agentId = session.agent_id ?? 'unknown'
      const existing = buckets.get(agentId)
      if (existing) existing.push(session)
      else buckets.set(agentId, [session])
    }
    const groups: AgentGroup[] = []
    for (const [agentId, agentSessions] of buckets) {
      agentSessions.sort(sortSessions)
      groups.push({ agent: agents.find((agent) => agent.id === agentId), agentId, sessions: agentSessions })
    }
    groups.sort((a, b) => new Date(b.sessions[0]?.updated_at ?? '').getTime() - new Date(a.sessions[0]?.updated_at ?? '').getTime())
    return groups
  }, [agents])

  const groups = useMemo<WsGroup[]>(() => {
    const query = debouncedSearch.trim().toLowerCase()
    const roots = mode === 'sessions'
      ? overview.roots.filter((session) =>
          (!wsFilter || session.workspace_id === wsFilter) &&
          (!agentFilter || session.agent_id === agentFilter),
        )
      : overview.roots
    if (mode === 'workspaces') {
      const byWorkspace = new Map<string, Session[]>()
      for (const session of roots) {
        if (session.workspace_id && wsMap.has(session.workspace_id)) {
          const existing = byWorkspace.get(session.workspace_id)
          if (existing) existing.push(session)
          else byWorkspace.set(session.workspace_id, [session])
        }
      }
      const matched = query ? workspaces.filter((workspace) => workspace.name.toLowerCase().includes(query)) : workspaces
      return [...matched].sort((a, b) => a.name.localeCompare(b.name)).map((workspace) => {
        const sess = byWorkspace.get(workspace.id) ?? []
        return { workspace, agentGroups: bucketByAgent(sess), totalCount: sess.length }
      })
    }
    const buckets = new Map<string | null, Session[]>()
    for (const session of roots) {
      const key = session.workspace_id && wsMap.has(session.workspace_id) ? session.workspace_id : null
      const existing = buckets.get(key)
      if (existing) existing.push(session)
      else buckets.set(key, [session])
    }
    const result: WsGroup[] = []
    for (const [workspaceId, sess] of buckets) {
      result.push({
        workspace: workspaceId ? wsMap.get(workspaceId) ?? null : null,
        agentGroups: bucketByAgent(sess),
        totalCount: sess.length,
      })
    }
    result.sort((a, b) => {
      if (a.workspace === null && b.workspace !== null) return 1
      if (a.workspace !== null && b.workspace === null) return -1
      return new Date(b.agentGroups[0]?.sessions[0]?.updated_at ?? '').getTime() - new Date(a.agentGroups[0]?.sessions[0]?.updated_at ?? '').getTime()
    })
    return result
  }, [overview.roots, workspaces, debouncedSearch, wsFilter, agentFilter, mode, bucketByAgent, wsMap])

  const total = mode === 'workspaces' ? groups.length : groups.reduce((count, group) => count + group.totalCount, 0)
  const loading = sLoading || wLoading
  const flatIds = useMemo(() => {
    const ids: string[] = []
    for (const group of groups) {
      const wsKey = group.workspace?.id ?? 'unfiled'
      if (isWsCollapsed(wsKey)) continue
      for (const agentGroup of group.agentGroups) {
        if (collapsedAgent.has(`${wsKey}::${agentGroup.agentId}`)) continue
        ids.push(...visibleOverviewIds(agentGroup.sessions, overview.childrenByParent, effectiveExpandedIds))
      }
    }
    return ids
  }, [groups, isWsCollapsed, collapsedAgent, overview.childrenByParent, effectiveExpandedIds])

  const [highlightIndex, setHighlightIndex] = useState(0)
  const [activationId, setActivationId] = useState<string | null>(null)
  const [announcement, setAnnouncement] = useState('')
  const filterKey = `${mode}|${debouncedSearch}|${fromDate}|${toDate}|${wsFilter ?? ''}|${agentFilter ?? ''}|${statusFilter}`
  useEffect(() => { setHighlightIndex(0) }, [debouncedSearch, fromDate, toDate, wsFilter, agentFilter, mode, groups.length])
  useEffect(() => {
    if (mode !== 'sessions') return
    const visible = flatIds
    const filterChanged = prevFilterKey.current !== filterKey
    const previouslyVisible = prevVisible.current
    prevFilterKey.current = filterKey
    prevVisible.current = visible
    const current = activationRef.current
    if (filterChanged) {
      const next = visible[0] ?? null
      activationRef.current = next
      setActivationId(next)
      return
    }
    if (current && visible.includes(current)) return
    if (current && !visible.includes(current)) {
      activationRef.current = null
      setActivationId(null)
      setAnnouncement((text) => (text.endsWith(' ') ? 'Highlighted session is unavailable.' : 'Highlighted session is unavailable. '))
      searchInputRef.current?.focus()
      return
    }
    if (!current && visible[0] && previouslyVisible.length === 0) {
      activationRef.current = visible[0]
      setActivationId(visible[0])
    }
  }, [flatIds, filterKey, mode])

  const highlightedWorkspace = mode === 'workspaces' ? (groups[highlightIndex]?.workspace ?? null) : null
  useEffect(() => {
    if (mode === 'workspaces') {
      if (!highlightedWorkspace) return
      document.getElementById(`search-ws-${highlightedWorkspace.id}`)?.scrollIntoView({ block: 'nearest' })
      return
    }
    if (!activationId) return
    document.getElementById(`search-result-${activationId}`)?.scrollIntoView({ block: 'nearest' })
  }, [activationId, highlightedWorkspace, mode])

  function moveActivation(delta: number) {
    const index = activationRef.current ? flatIds.indexOf(activationRef.current) : -1
    const nextIndex = index < 0
      ? (delta > 0 ? 0 : flatIds.length - 1)
      : Math.min(Math.max(index + delta, 0), flatIds.length - 1)
    const next = flatIds[nextIndex] ?? null
    activationRef.current = next
    setActivationId(next)
  }

  function activateHighlighted() {
    const id = activationRef.current
    if (!id) return
    if (sessionActivation(id) === 'expand') {
      toggleExpand(id)
      return
    }
    const session = sessionById.get(id)
    if (session) selectSession(session)
  }

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next) close() }}>
      <DialogContent
        onEscapeKeyDown={(event) => { if (editingSessionIdsRef.current.size > 0) event.preventDefault() }}
        overlayClassName="bg-transparent"
        className="max-w-2xl gap-0 overflow-hidden p-0 flex flex-col max-h-[85dvh] bg-[var(--color-surface-1)] border border-[var(--color-muted)]/40 rounded-2xl shadow-2xl"
      >
        <DialogHeader className="space-y-0 px-[var(--space-3)] pt-[var(--space-3)] pb-[var(--space-2-5)] shrink-0 border-b border-[var(--color-border)]">
          <DialogTitle className="flex items-center gap-[var(--space-2)] text-base mb-[var(--space-2)]">
            <MagnifyingGlass size={16} className="text-[var(--color-accent)]" />
            {mode === 'workspaces' ? 'Switch workspace' : 'Sessions'}
            {mode === 'sessions' && wsFilter && (
              <span className="ml-[var(--space-1)] inline-flex items-center gap-[var(--space-1)] rounded-full bg-[var(--color-surface-2)] px-[var(--space-2)] py-[var(--space-0-5)] text-[length:var(--type-caption-size)] font-[var(--font-weight-regular)] text-[var(--color-secondary)]">
                {workspaces.find((workspace) => workspace.id === wsFilter)?.name ?? 'Filtered'}
                <IconButton onClick={() => useUiStore.setState({ searchModalWorkspaceFilter: null })} className="h-auto w-auto p-0 text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)]" aria-label="Clear workspace filter">
                  <X size={10} />
                </IconButton>
              </span>
            )}
            {mode === 'sessions' && agentFilter && (
              <span className="ml-[var(--space-1)] inline-flex items-center gap-[var(--space-1)] rounded-full bg-[var(--color-surface-2)] px-[var(--space-2)] py-[var(--space-0-5)] text-[length:var(--type-caption-size)] font-[var(--font-weight-regular)] text-[var(--color-secondary)]">
                {agents.find((agent) => agent.id === agentFilter)?.name ?? agentFilter}
                <IconButton onClick={() => useUiStore.getState().setSearchModalAgentFilter(null)} className="h-auto w-auto p-0 text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)]" aria-label="Clear agent filter">
                  <X size={10} />
                </IconButton>
              </span>
            )}
          </DialogTitle>
          <DialogDescription className="sr-only">
            {mode === 'workspaces'
              ? 'Switch workspace. Arrow keys move between workspaces, Enter switches to the highlighted one.'
              : 'Sessions across workspaces. All lists every session. Running lists only sessions that are executing.'}
          </DialogDescription>
          <div className="relative">
            <MagnifyingGlass size={16} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--color-muted)]" />
            <Input
              ref={searchInputRef}
              autoFocus
              placeholder={mode === 'workspaces' ? 'Filter workspaces by name...' : 'Search by title, workspace, or agent...'}
              value={searchText}
              onChange={(event) => setSearchText(event.target.value)}
              onKeyDown={(event) => {
                if (mode === 'workspaces') {
                  if (event.key === 'ArrowDown') { event.preventDefault(); setHighlightIndex((index) => Math.min(index + 1, groups.length - 1)) }
                  else if (event.key === 'ArrowUp') { event.preventDefault(); setHighlightIndex((index) => Math.max(index - 1, 0)) }
                  else if (event.key === 'Enter' && highlightedWorkspace) { event.preventDefault(); handleSwitchWorkspace(highlightedWorkspace) }
                  return
                }
                if (event.key === 'ArrowDown') { event.preventDefault(); moveActivation(1) }
                else if (event.key === 'ArrowUp') { event.preventDefault(); moveActivation(-1) }
                else if (event.key === 'Enter') { event.preventDefault(); activateHighlighted() }
              }}
              className="pl-[var(--space-5)]"
              aria-label={mode === 'workspaces' ? 'Filter workspaces' : 'Search sessions'}
            />
            {mode === 'sessions' && (
              <IconButton
                onClick={() => setShowDateFilter((value) => !value)}
                className={showDateFilter
                  ? 'absolute right-2 top-1/2 h-7 w-7 p-0 -translate-y-1/2 rounded bg-[var(--color-surface-2)] text-[var(--color-accent)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-accent)]'
                  : 'absolute right-2 top-1/2 h-7 w-7 p-0 -translate-y-1/2 rounded text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)]'}
                title="Date range filter"
                aria-label="Toggle date range filter"
                aria-pressed={showDateFilter}
              >
                <Calendar size={15} />
              </IconButton>
            )}
          </div>
          {mode === 'sessions' && (
            <SegmentedControl
              aria-label="Session filter"
              value={statusFilter}
              onValueChange={(value) => setStatusFilter(value === 'running' ? 'running' : 'all')}
              className="mt-[var(--space-2)]"
              data-testid="sessions-filter"
            >
              <SegmentedControlItem value="all">All</SegmentedControlItem>
              <SegmentedControlItem value="running">Running</SegmentedControlItem>
            </SegmentedControl>
          )}
          {mode === 'sessions' && showDateFilter && (
            <div className="mt-[var(--space-2)] flex items-center gap-[var(--space-2)]">
              <Input type="date" value={fromDate} onChange={(event) => setFromDate(event.target.value)} className="max-w-[180px]" aria-label="From date" />
              <span className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">to</span>
              <Input type="date" value={toDate} onChange={(event) => setToDate(event.target.value)} className="max-w-[180px]" aria-label="To date" />
              {(fromDate || toDate) && (
                <Button variant="ghost" onClick={() => { setFromDate(''); setToDate('') }} className="h-auto p-0 font-[var(--font-weight-regular)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)] hover:underline">
                  Clear
                </Button>
              )}
            </div>
          )}
        </DialogHeader>
        <p role="status" aria-live="polite" className="sr-only" data-testid="sessions-activation-live">{announcement}</p>
        <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain py-[var(--space-2)]">
          {mode === 'sessions' && agentNamesUnavailable && !loading && !sessionsError && !workspacesError && (
            <ErrorState
              message="Agent-name search is unavailable because agents could not be loaded. Title and workspace search still work."
              className="mx-[var(--space-3)] mb-[var(--space-2)] px-[var(--space-2)] py-[var(--space-2)]"
              onRetry={() => { void retryAgents() }}
            />
          )}
          {mode === 'sessions' && coverage.incomplete && !loading && !sessionsError && (
            <div role="status" data-testid="sessions-partial" className="mx-[var(--space-3)] mb-[var(--space-2)] rounded-md border border-[var(--color-border)] px-[var(--space-2)] py-[var(--space-2)] text-[length:var(--type-utility-xs-size)] text-[var(--color-secondary)]">
              <p>{partialListMessage(coverage.partialErrors.length)}</p>
              <Button variant="link" className="mt-[var(--space-1)] h-auto p-0 font-[var(--font-weight-regular)] text-[length:var(--type-utility-xs-size)]" onClick={() => { void queryClient.invalidateQueries({ queryKey: ['sessions', 'flat'] }) }}>
                Retry
              </Button>
            </div>
          )}
          {sessionsError || workspacesError ? (
            <ErrorState
              message={`Could not load ${failedRequests} — try again`}
              className="px-[var(--space-2-5)] py-[var(--space-6)] text-center"
              onRetry={() => {
                if (sessionsError) void retrySessions()
                if (workspacesError) void retryWorkspaces()
              }}
            />
          ) : loading ? (
            <div className="px-[var(--space-2-5)] py-[var(--space-6)] text-center text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
              {mode === 'workspaces' ? 'Loading workspaces...' : 'Loading sessions...'}
            </div>
          ) : total === 0 ? (
            <div className="px-[var(--space-2-5)] py-[var(--space-6)] text-center text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
              {mode === 'workspaces' ? (
                <p>No workspaces found{debouncedSearch ? ` for "${debouncedSearch}"` : ''}.</p>
              ) : (
                <p>{statusFilter === 'running' ? 'No running sessions' : 'No sessions found'}{wsFilter ? ' in this workspace' : ''}{agentFilter ? ' for this agent' : ''}{debouncedSearch ? ` for "${debouncedSearch}"` : ''}.</p>
              )}
              {mode === 'workspaces' ? (
                debouncedSearch && (
                  <Button variant="link" onClick={() => setSearchText('')} className="mt-[var(--space-2)] font-[var(--font-weight-regular)] text-[length:var(--type-utility-xs-size)]">
                    Clear filter
                  </Button>
                )
              ) : (wsFilter || agentFilter || debouncedSearch || fromDate || toDate || statusFilter !== 'all') && (
                <Button
                  variant="link"
                  onClick={() => { setSearchText(''); setFromDate(''); setToDate(''); setStatusFilter('all'); useUiStore.setState({ searchModalWorkspaceFilter: null, searchModalAgentFilter: null }) }}
                  className="mt-[var(--space-2)] font-[var(--font-weight-regular)] text-[length:var(--type-utility-xs-size)]"
                >
                  Clear all filters
                </Button>
              )}
            </div>
          ) : (
            groups.map((group, index) => {
              const wsKey = group.workspace?.id ?? 'unfiled'
              const wsCollapsed = isWsCollapsed(wsKey)
              const wsHighlighted = mode === 'workspaces' && index === highlightIndex
              return (
                <div key={wsKey} id={`search-ws-${wsKey}`} className="mb-[var(--space-1)]">
                  <WorkspaceHeader
                    name={group.workspace?.name ?? 'Unfiled'}
                    isCollapsed={wsCollapsed}
                    onToggle={() => toggleWs(wsKey)}
                    panelId={`ws-panel-${wsKey}`}
                    onSwitch={group.workspace ? () => handleSwitchWorkspace(group.workspace!) : undefined}
                    isHighlighted={wsHighlighted}
                  />
                  {!wsCollapsed && (
                    <div id={`ws-panel-${wsKey}`} role="region" className="space-y-[var(--space-0-5)] px-[var(--space-2)] pb-[var(--space-1)]">
                      {group.agentGroups.map((agentGroup) => {
                        const agentKey = `${wsKey}::${agentGroup.agentId}`
                        const agentCollapsed = collapsedAgent.has(agentKey)
                        const agentName = agentGroup.agent?.name ?? (agentsError || agentGroup.agentId === 'unknown' ? 'Unknown' : '[removed]')
                        return (
                          <div key={agentKey}>
                            <AgentHeader agent={agentGroup.agent} name={agentName} isCollapsed={agentCollapsed} onToggle={() => toggleAgent(agentKey)} panelId={`agent-panel-${agentKey}`} />
                            {!agentCollapsed && (
                              <div id={`agent-panel-${agentKey}`} role="region" className="space-y-[var(--space-0-5)] pl-[var(--space-2-5)]">
                                <SessionOverviewList
                                  sessions={agentGroup.sessions}
                                  childrenByParent={overview.childrenByParent}
                                  expandedIds={effectiveExpandedIds}
                                  toggleExpand={toggleExpand}
                                  activeSessionId={activeSessionId}
                                  highlightedId={activationId ?? undefined}
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
