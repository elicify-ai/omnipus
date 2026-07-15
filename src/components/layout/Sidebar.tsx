import { useEffect, useCallback, useRef, useState } from 'react'
import { Link, useLocation, useNavigate } from '@tanstack/react-router'
import { motion, AnimatePresence } from 'framer-motion'
import {
  Robot,
  PlugsConnected,
  PuzzlePiece,
  ChartBar,
  Gear,
  PushPin,
  PushPinSlash,
  SignOut,
  Plus,
  Folder,
  Tray,
  CaretDown,
  CaretRight,
  MagnifyingGlass,
  WarningCircle,
  ArrowClockwise,
  UserCircle,
} from '@phosphor-icons/react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useSidebarStore, SIDEBAR_PIN_BREAKPOINT } from '@/store/sidebar'
import { useAuthStore } from '@/store/auth'
import { useUiStore } from '@/store/ui'
import { useNotificationsStore } from '@/store/notifications'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { fetchWorkspaces, createWorkspace, workspacesQueryKeys, fetchSessions, fetchAgents } from '@/lib/api'
import { useSessionStore } from '@/store/session'
import { useSelectSession } from '@/components/chat/useSelectSession'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { cn } from '@/lib/utils'
import avatarUrl from '@/assets/logo/omnipus-avatar.svg?url'

// Library items — reusable assets that live outside any single workspace.
// (Per-workspace work — chat, tasks, calendar, team — lives inside the
// workspace tabs, not the sidebar.)
const LIBRARY_ITEMS = [
  { to: '/agents', label: 'Agents', Icon: Robot },
  { to: '/skills', label: 'Skills & Tools', Icon: PuzzlePiece },
  { to: '/connectors', label: 'Connectors', Icon: PlugsConnected },
] as const

const PROJECT_COLLAPSE_THRESHOLD = 5

// US-5: Sidebar — overlay default, pin option, Framer Motion, Zustand
export function Sidebar() {
  const { isOpen, isPinned, close, toggle, togglePin } = useSidebarStore()
  const location = useLocation()
  const navigate = useNavigate()
  const { activeWorkspaceId, setActiveWorkspaceId } = useWorkspacesStore()

  const { toggleNotificationPanel } = useUiStore()
  const unreadCount = useNotificationsStore((s) => s.unreadCount)
  const username = useAuthStore((s) => s.username)

  const queryClient = useQueryClient()
  const [projectsExpanded, setProjectsExpanded] = useState(false)
  const [archiveOpen, setArchiveOpen] = useState(false)

  // Inline workspace creation — name-only, edited directly in the list.
  const [creatingWorkspace, setCreatingWorkspace] = useState(false)
  const [newWorkspaceName, setNewWorkspaceName] = useState('')
  const createWorkspaceMut = useMutation({
    mutationFn: (name: string) => createWorkspace({ name }),
    onSuccess: (ws) => {
      queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.list() })
      setCreatingWorkspace(false)
      setNewWorkspaceName('')
      // Land in the new workspace immediately.
      setActiveWorkspaceId(ws.id)
      navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: ws.id } })
    },
    onError: (err) => {
      useUiStore.getState().addToast({
        message: err instanceof Error ? err.message : 'Could not create workspace',
        variant: 'error',
      })
    },
  })
  const commitCreateWorkspace = () => {
    const name = newWorkspaceName.trim()
    if (!name || createWorkspaceMut.isPending) return
    createWorkspaceMut.mutate(name)
  }

  // Track whether the viewport is wide enough to allow pinning (≥1024px).
  const [canPin, setCanPin] = useState<boolean>(
    () => window.matchMedia(`(min-width: ${SIDEBAR_PIN_BREAKPOINT}px)`).matches
  )

  // The sidebar is effectively pinned only when the viewport allows it.
  // Declared early so all hooks below can reference it.
  const effectivelyPinned = isPinned && canPin
  const isVisible = effectivelyPinned || isOpen

  // Track when overlay sidebar was open so we can return focus to the hamburger on close
  const wasOverlayOpenRef = useRef(false)

  const handleLogout = useCallback(() => {
    useAuthStore.getState().clearAuth()
    navigate({ to: '/login' })
  }, [navigate])

  // Workspaces query — refetch every 30s
  const { data: projects = [], isLoading: projectsLoading, isError: projectsError } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchWorkspaces({ status: 'active' }),
    staleTime: 30_000,
    refetchInterval: 30_000,
  })

  // Archived workspaces query — only enabled when archive section is open
  const {
    data: archivedProjects = [],
    isError: archivedError,
    refetch: refetchArchived,
  } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'archived' }),
    queryFn: () => fetchWorkspaces({ status: 'archived' }),
    enabled: archiveOpen,
    staleTime: 30_000,
  })

  // Sessions + agents — used by the workspace accordion's expanded session list.
  const { data: allSessions = [], isError: sessionsError } = useQuery({
    queryKey: ['sessions'],
    queryFn: () => fetchSessions(),
    staleTime: 15_000,
  })
  const { data: agents = [] } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    staleTime: 30_000,
  })

  // Currently-active session — for the sidebar's simplified title-only rows.
  const activeSessionId = useSessionStore((s) => s.activeSessionId)

  // Reusable session-selection logic (attach WS + navigate + close sidebar).
  const selectSession = useSelectSession({
    agents,
    workspaces: projects,
    onClose: () => {
      if (!effectivelyPinned) close()
    },
  })

  // Workspace accordion expansion state.
  const [expandedWorkspaceIds, setExpandedWorkspaceIds] = useState<Set<string>>(new Set())
  const toggleWorkspaceExpansion = useCallback((workspaceId: string) => {
    setExpandedWorkspaceIds((prev) => {
      const next = new Set(prev)
      if (next.has(workspaceId)) {
        next.delete(workspaceId)
      } else {
        next.add(workspaceId)
      }
      return next
    })
  }, [])

  // Inbox workspace (is_default: true) always appears first; other workspaces: pinned then unpinned.
  const inboxProject = projects.find((p) => p.is_default)
  const nonInboxProjects = projects.filter((p) => !p.is_default)
  const pinnedProjects = nonInboxProjects.filter((p) => p.pinned)
  const unpinnedProjects = nonInboxProjects.filter((p) => !p.pinned)
  const hasMore = unpinnedProjects.length > PROJECT_COLLAPSE_THRESHOLD
  const visibleUnpinned = projectsExpanded
    ? unpinnedProjects
    : unpinnedProjects.slice(0, PROJECT_COLLAPSE_THRESHOLD)
  // Inbox always first, then pinned, then visible unpinned
  const visibleProjects = [
    ...(inboxProject ? [inboxProject] : []),
    ...pinnedProjects,
    ...visibleUnpinned,
  ]

  // US-5: Cmd+B / Ctrl+B keyboard shortcut + Escape to close
  const handleKeydown = useCallback(
    (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'b') {
        e.preventDefault()
        toggle()
      }
      // Escape closes the overlay sidebar; the pinned sidebar is never closed by Escape.
      // When viewport < 1024px pin is ignored, so treat isPinned as false there.
      if (e.key === 'Escape' && isOpen && !effectivelyPinned) {
        close()
      }
    },
    [toggle, close, isOpen, effectivelyPinned]
  )

  // Track when the viewport crosses the pin breakpoint.
  // We do NOT call unpin() here — the user's pin preference is preserved.
  // effectivelyPinned = isPinned && canPin already suppresses pinned behaviour
  // at narrow viewports, and the preference is automatically restored on widen.
  useEffect(() => {
    const mq = window.matchMedia(`(min-width: ${SIDEBAR_PIN_BREAKPOINT}px)`)
    function handleChange(e: MediaQueryListEvent) {
      setCanPin(e.matches)
    }
    mq.addEventListener('change', handleChange)
    return () => mq.removeEventListener('change', handleChange)
  }, [setCanPin])

  useEffect(() => {
    window.addEventListener('keydown', handleKeydown)
    return () => window.removeEventListener('keydown', handleKeydown)
  }, [handleKeydown])

  // Return focus to hamburger when overlay sidebar closes (Task 4)
  useEffect(() => {
    const overlayOpen = isOpen && !effectivelyPinned
    if (wasOverlayOpenRef.current && !overlayOpen) {
      const hamburger = document.getElementById('sidebar-hamburger') as HTMLButtonElement | null
      hamburger?.focus()
    }
    wasOverlayOpenRef.current = overlayOpen
  }, [isOpen, effectivelyPinned])

  // Sidebar content shared between pinned and overlay modes
  const sidebarContent = (
    <nav className="flex h-full flex-col" aria-label="Main navigation">
      {/* Brand mark — 44px chrome row, no border-b (flat shell alignment) */}
      <div className="flex items-center gap-2.5 px-4 h-chrome-header min-h-chrome-header shrink-0">
        <img
          src={avatarUrl}
          alt="Omnipus"
          className="h-6 w-6 flex-shrink-0"
        />
        <span className="font-headline text-sm font-bold text-[var(--color-secondary)]">
          Omnipus
        </span>
        {/* Search icon — opens the cross-workspace session search modal */}
        <button
          type="button"
          onClick={() => useUiStore.getState().openSearchModal()}
          aria-label="Search sessions"
          title="Search sessions"
          className="ml-auto shrink-0 rounded p-1.5 text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] transition-colors"
        >
          <MagnifyingGlass size={16} />
        </button>
        {/* Pin toggle — icon-only in the brand row (not a full-width bottom button) */}
        {canPin && (
          <button
            type="button"
            onClick={togglePin}
            aria-label={isPinned ? 'Unpin sidebar' : 'Pin sidebar'}
            aria-pressed={isPinned}
            title={isPinned ? 'Unpin sidebar' : 'Pin sidebar'}
            className="shrink-0 rounded p-1.5 text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] transition-colors"
          >
            {isPinned ? <PushPinSlash size={16} /> : <PushPin size={16} />}
          </button>
        )}
      </div>

      {/* Workspaces (primary, scrollable) */}
      {/* overscroll-contain: stop iOS scroll chaining — without it, hitting the
          end of the sidebar scroll drags the page itself up, leaving a gap at
          the bottom (the shell is fixed-position; the page should never move). */}
      <div className="flex-1 overflow-y-auto overscroll-contain py-3">
        {/* Workspaces section. The old hidden `/`-to-filter input is removed —
            undiscoverable (no visual hint, focus-dependent) and a subset of
            what the search modal does; workspace lookup lives there now. */}
        <div className="mb-1" role="group" aria-label="Workspaces">
          {/* Section header */}
          <div className="flex items-center justify-between px-4 py-1">
            <span className="text-[10px] font-semibold uppercase tracking-widest text-[var(--color-muted)]">
              Workspaces
            </span>
            <button
              type="button"
              onClick={() => setCreatingWorkspace(true)}
              aria-label="New workspace"
              className="rounded p-0.5 text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors"
            >
              <Plus size={14} />
            </button>
          </div>

          {/* Inline workspace creation — name is the only required field.
              + adds an editable row right in the list; Enter creates, Escape cancels. */}
          {creatingWorkspace && (
            <div className="flex items-center gap-2 px-4 py-1.5">
              <Folder size={14} className="flex-shrink-0 text-[var(--color-muted)]" />
              <input
                autoFocus
                type="text"
                value={newWorkspaceName}
                onChange={(e) => setNewWorkspaceName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault()
                    commitCreateWorkspace()
                  } else if (e.key === 'Escape') {
                    setCreatingWorkspace(false)
                    setNewWorkspaceName('')
                  }
                }}
                onBlur={() => {
                  // Commit on blur if a name was typed; otherwise cancel quietly.
                  if (newWorkspaceName.trim()) commitCreateWorkspace()
                  else { setCreatingWorkspace(false); setNewWorkspaceName('') }
                }}
                placeholder="Workspace name…"
                aria-label="New workspace name"
                disabled={createWorkspaceMut.isPending}
                className="flex-1 min-w-0 rounded border border-[var(--color-accent)] bg-[var(--color-surface-1)] px-2 py-1 text-sm text-[var(--color-secondary)] outline-none placeholder:text-[var(--color-muted)] disabled:opacity-50"
              />
            </div>
          )}

          {/* Error state (Fix 6) */}
          {projectsError && (
            <div className="px-4 py-1.5 flex items-center gap-1.5">
              <WarningCircle size={14} className="text-[var(--color-error)] flex-shrink-0" />
              <span className="text-xs text-[var(--color-error)] flex-1">Could not load workspaces</span>
              <button
                type="button"
                onClick={() => queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.list() })}
                aria-label="Retry loading workspaces"
                className="rounded p-0.5 text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors"
              >
                <ArrowClockwise size={12} />
              </button>
            </div>
          )}

          {/* Loading skeleton */}
          {projectsLoading && (
            <div className="flex flex-col gap-1 px-4 py-1">
              {[1, 2, 3].map((i) => (
                <div
                  key={i}
                  className="h-7 rounded bg-[var(--color-surface-2)] animate-pulse"
                  style={{ width: `${60 + i * 12}%` }}
                />
              ))}
            </div>
          )}

          {/* Empty state */}
          {!projectsLoading && !projectsError && projects.length === 0 && (
            <div className="px-4 py-1.5">
              <span className="text-xs text-[var(--color-muted)]">No workspaces yet — </span>
              <button
                type="button"
                onClick={() => setCreatingWorkspace(true)}
                className="text-xs text-[var(--color-accent)] hover:underline"
              >
                New workspace
              </button>
            </div>
          )}

          {/* Workspace list — accordion: click name navigates, click chevron expands sessions */}
          {!projectsLoading && visibleProjects
            .map((project) => {
            const isActive = activeWorkspaceId === project.id
            const isInbox = project.is_default === true
            const isExpanded = expandedWorkspaceIds.has(project.id)
            const workspaceSessions = allSessions
              .filter((s) => s.workspace_id === project.id)
              .sort((a, b) => {
                // Heartbeat sessions on top, then recent-first
                if (a.type === 'heartbeat' && b.type !== 'heartbeat') return -1
                if (b.type === 'heartbeat' && a.type !== 'heartbeat') return 1
                return new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()
              })
            const maxVisible = 9
            const visibleSessions = workspaceSessions.slice(0, maxVisible)
            return (
              <div key={project.id}>
                <div
                  className={cn(
                    'flex items-center gap-2 w-full px-4 py-2 mx-0 text-sm transition-colors text-left',
                    isActive
                      ? 'text-[var(--color-accent)] font-medium'
                      : 'text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]'
                  )}
                >
                  <button
                    type="button"
                    onClick={() => {
                      setActiveWorkspaceId(project.id)
                      navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: project.id } })
                      if (!effectivelyPinned) close()
                    }}
                    className="flex items-center gap-2 flex-1 min-w-0 text-left"
                  >
                    {isActive ? (
                      <span className="w-2 h-2 rounded-full bg-[var(--color-accent)] animate-pulse flex-shrink-0" aria-hidden="true" />
                    ) : isInbox ? (
                      <Tray size={14} className="flex-shrink-0 text-[var(--color-accent)]" />
                    ) : (
                      <Folder size={14} className="flex-shrink-0 text-[var(--color-muted)]" />
                    )}
                    <span className="flex-1 truncate">{project.name}</span>
                  </button>
                  <button
                    type="button"
                    onClick={(e) => { e.stopPropagation(); toggleWorkspaceExpansion(project.id) }}
                    aria-expanded={isExpanded}
                    aria-label={isExpanded ? `Collapse ${project.name} sessions` : `Expand ${project.name} sessions`}
                    className="shrink-0 rounded p-1.5 -m-1 text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] transition-colors"
                  >
                    {isExpanded ? <CaretDown size={12} /> : <CaretRight size={12} />}
                  </button>
                </div>
                {isExpanded && (
                  /* Hierarchy via a connector rail (border-l) instead of deep
                     pl-8 indentation — communicates "children of the workspace"
                     by connectedness while reclaiming ~14px of row width. */
                  <div className="pb-1 ml-5 border-l border-[var(--color-border)]">
                    <button
                      type="button"
                      onClick={() => {
                        setActiveWorkspaceId(project.id)
                        useSessionStore.getState().startNewSession()
                        navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: project.id } })
                        if (!effectivelyPinned) close()
                      }}
                      className="flex items-center gap-2 w-full pl-3 pr-4 py-1.5 text-[13px] text-[var(--color-muted)] hover:text-[var(--color-accent)] hover:bg-[var(--color-surface-2)] transition-colors"
                    >
                      <Plus size={12} /> New chat
                    </button>
                    {sessionsError ? (
                      <p className="pl-3 pr-4 py-1 text-[13px] text-[var(--color-error)]">Could not load sessions</p>
                    ) : workspaceSessions.length === 0 ? (
                      <p className="pl-3 pr-4 py-1 text-[13px] text-[var(--color-muted)] opacity-60">No sessions yet</p>
                    ) : (
                      // Pure navigation rows — manage lives behind "More…".
                      visibleSessions.map((s) => {
                        const sActive = s.id === activeSessionId
                        return (
                          <button
                            key={s.id}
                            type="button"
                            onClick={() => selectSession(s)}
                            className={cn(
                              'flex items-center gap-1.5 w-full pl-3 pr-4 py-1.5 text-[13px] transition-colors text-left',
                              sActive
                                ? 'text-[var(--color-accent)] font-medium'
                                : 'text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]'
                            )}
                          >
                            {sActive && <span className="w-1.5 h-1.5 rounded-full bg-[var(--color-accent)] flex-shrink-0" />}
                            <span className="flex-1 truncate">{s.title || 'Untitled'}</span>
                            {s.type === 'heartbeat' && (
                              <span className="text-[9px] uppercase tracking-wider text-[var(--color-muted)] flex-shrink-0">HB</span>
                            )}
                          </button>
                        )
                      })
                    )}
                    {/* Always the last entry — opens the session search pre-filtered to this workspace */}
                    {workspaceSessions.length > 0 && (
                      <button
                        type="button"
                        onClick={() => useUiStore.getState().openSearchModal(project.id)}
                        className="flex items-center gap-1 w-full pl-3 pr-4 py-1.5 text-[13px] text-[var(--color-muted)] hover:text-[var(--color-accent)] transition-colors"
                      >
                        More…
                      </button>
                    )}
                  </div>
                )}
              </div>
            )
          })}

          {/* Show more / less toggle */}
          {!projectsLoading && hasMore && (
            <button
              type="button"
              onClick={() => setProjectsExpanded((v) => !v)}
              className="flex items-center gap-2 w-full px-4 py-1.5 text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors"
            >
              {projectsExpanded
                ? 'Show fewer'
                : `${unpinnedProjects.length - PROJECT_COLLAPSE_THRESHOLD} more…`}
            </button>
          )}

          {/* Archive section (Fix 9) */}
          <button
            type="button"
            onClick={() => setArchiveOpen((v) => !v)}
            className="flex items-center gap-1.5 w-full px-4 py-1.5 text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors mt-1"
            aria-expanded={archiveOpen}
          >
            {archiveOpen ? <CaretDown size={10} /> : <CaretRight size={10} />}
            Archive
          </button>

          {archiveOpen && archivedError && (
            <div className="flex items-center justify-between gap-2 px-4 py-2 text-xs text-[var(--color-error)]">
              <span>Could not load archived workspaces</span>
              <button
                type="button"
                onClick={() => refetchArchived()}
                className="text-[var(--color-accent)] hover:underline flex-shrink-0"
              >
                Retry
              </button>
            </div>
          )}

          {archiveOpen && archivedProjects.map((project) => {
            return (
              <button
                key={project.id}
                type="button"
                onClick={() => {
                  setActiveWorkspaceId(project.id)
                  navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: project.id } })
                  if (!effectivelyPinned) close()
                }}
                className="flex items-center gap-2 w-full px-4 py-2 mx-0 text-sm transition-colors text-left opacity-70 text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]"
              >
                <Folder size={14} className="flex-shrink-0 text-[var(--color-muted)]" />
                <span className="flex-1 truncate">{project.name}</span>
              </button>
            )
          })}
        </div>

      </div>

      {/* Tiny divider between scrollable workspaces and fixed library items */}
      <div className="mx-4 my-1 border-t border-[var(--color-border)]" />

      {/* Library items (fixed bottom — not scrollable, only 3 items) */}
      <div className="shrink-0 py-2" role="group" aria-label="Library">
        {LIBRARY_ITEMS.map(({ to, label, Icon }) => {
          const isActive = location.pathname === to || location.pathname.startsWith(`${to}/`)
          return (
            <Link
              key={to}
              to={to}
              aria-label={label}
              aria-current={isActive ? 'page' : undefined}
              onClick={() => {
                if (!effectivelyPinned) close()
              }}
              className={cn(
                'flex items-center gap-3 px-4 py-2.5 mx-2 rounded-lg text-sm transition-colors',
                isActive
                  ? 'bg-[var(--color-surface-2)] text-[var(--color-accent)] font-medium'
                  : 'text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]',
              )}
            >
              <Icon
                size={18}
                weight={isActive ? 'fill' : 'regular'}
                className={isActive ? 'text-[var(--color-accent)]' : ''}
              />
              <span className="flex-1">{label}</span>
            </Link>
          )
        })}
      </div>

      {/* Username — stable at the very bottom, opens a popup menu */}
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            aria-label="Open user menu"
            data-testid="sidebar-profile-trigger"
            className="flex items-center gap-3 px-4 py-2.5 mx-2 rounded-lg text-sm text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] transition-colors w-[calc(100%-16px)]"
          >
            <UserCircle size={18} weight="regular" />
            <span className="flex-1 text-left truncate">{username ?? 'User'}</span>
            {unreadCount > 0 && (
              <span
                data-testid="sidebar-notification-badge"
                className="flex h-4 min-w-4 items-center justify-center rounded-full px-1 text-[10px] font-medium leading-none bg-[var(--color-error)] text-white"
              >
                {unreadCount > 99 ? '99+' : unreadCount}
              </span>
            )}
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          side="top"
          align="start"
          className="min-w-[180px] bg-[var(--color-surface-1)] border border-[var(--color-border)]"
        >
          <DropdownMenuLabel className="text-[10px] uppercase tracking-widest text-[var(--color-muted)]">
            {username ?? 'Account'}
          </DropdownMenuLabel>
          <DropdownMenuSeparator className="bg-[var(--color-border)]" />
          <DropdownMenuItem
            data-testid="sidebar-notifications"
            onSelect={() => { toggleNotificationPanel(); if (!effectivelyPinned) close() }}
            className="flex items-center gap-2 cursor-pointer"
          >
            <Tray size={14} />
            Notifications
            {unreadCount > 0 && (
              <span className="ml-auto flex h-4 min-w-4 items-center justify-center rounded-full px-1 text-[10px] font-medium leading-none bg-[var(--color-error)] text-white">
                {unreadCount > 99 ? '99+' : unreadCount}
              </span>
            )}
          </DropdownMenuItem>
          <DropdownMenuItem asChild>
            <Link to="/usage" className="flex items-center gap-2 cursor-pointer">
              <ChartBar size={14} />
              Usage
            </Link>
          </DropdownMenuItem>
          <DropdownMenuItem asChild>
            <Link to="/profile" className="flex items-center gap-2 cursor-pointer">
              <UserCircle size={14} />
              Profile
            </Link>
          </DropdownMenuItem>
          <DropdownMenuItem asChild>
            <Link to="/settings" className="flex items-center gap-2 cursor-pointer">
              <Gear size={14} />
              Settings
            </Link>
          </DropdownMenuItem>
          <DropdownMenuSeparator className="bg-[var(--color-border)]" />
          <DropdownMenuItem
            onSelect={handleLogout}
            aria-label="Sign out"
            className="flex items-center gap-2 cursor-pointer text-[var(--color-muted)]"
          >
            <SignOut size={14} />
            Sign out
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

    </nav>
  )

  return (
    <>
      {/* FR-015: Pinned mode — permanent panel, only rendered when ≥1024px AND user chose to pin */}
      {effectivelyPinned && (
        <aside
          className="flex flex-col h-full flex-shrink-0 bg-[var(--color-surface-0)] border-r border-[var(--color-border)]"
          style={{
            width: 'var(--spacing-sidebar)',
            paddingTop: 'env(safe-area-inset-top)',
            paddingBottom: 'env(safe-area-inset-bottom)',
            paddingLeft: 'env(safe-area-inset-left)',
          }}
          aria-label="Main navigation"
        >
          {sidebarContent}
        </aside>
      )}

      {/* US-5: Overlay mode — slides in from left, no backdrop dim */}
      <AnimatePresence>
        {isVisible && !effectivelyPinned && (
          <motion.aside
            initial={{ x: '-100%' }}
            animate={{ x: 0 }}
            exit={{ x: '-100%' }}
            transition={{ type: 'tween', duration: 0.22, ease: [0.4, 0, 0.2, 1] }}
            className="fixed left-0 top-0 z-40 flex h-full flex-col bg-[var(--color-surface-0)] border-r border-[var(--color-border)] shadow-2xl"
            style={{
              width: 'var(--spacing-sidebar)',
              paddingTop: 'env(safe-area-inset-top)',
              paddingBottom: 'env(safe-area-inset-bottom)',
              paddingLeft: 'env(safe-area-inset-left)',
            }}
            role="dialog"
            aria-modal="true"
            aria-label="Main navigation"
          >
            {sidebarContent}
          </motion.aside>
        )}
      </AnimatePresence>

      {/* US-5: Click-outside overlay dismiss — no background dimming */}
      <AnimatePresence>
        {isOpen && !effectivelyPinned && (
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.15 }}
            className="fixed inset-0 z-30"
            onClick={close}
            aria-hidden="true"
          />
        )}
      </AnimatePresence>
    </>
  )
}
