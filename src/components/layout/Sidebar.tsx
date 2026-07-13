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
  Sun,
} from '@phosphor-icons/react'
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
} from '@/components/ui/dropdown-menu'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useSidebarStore, SIDEBAR_PIN_BREAKPOINT } from '@/store/sidebar'
import { useAuthStore } from '@/store/auth'
import { useUiStore } from '@/store/ui'
import { useNotificationsStore } from '@/store/notifications'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { fetchWorkspaces, workspacesQueryKeys } from '@/lib/api'
import { NewWorkspaceSlideOver } from '@/components/workspaces/NewWorkspaceSlideOver'
import { cn } from '@/lib/utils'
import avatarUrl from '@/assets/logo/omnipus-avatar.svg?url'

// Global libraries — reusable assets that live outside any single workspace.
// (Per-workspace work — chat, tasks, calendar, team — lives inside the
// workspace tabs, not the sidebar.)
const LIBRARY_ITEMS = [
  { to: '/agents', label: 'Agents', Icon: Robot },
  { to: '/connectors', label: 'Connectors', Icon: PlugsConnected },
  { to: '/skills', label: 'Skills & Tools', Icon: PuzzlePiece },
  { to: '/usage', label: 'Usage', Icon: ChartBar },
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
  const [newProjectOpen, setNewProjectOpen] = useState(false)
  const [projectsExpanded, setProjectsExpanded] = useState(false)
  const [archiveOpen, setArchiveOpen] = useState(false)
  const [searchQuery, setSearchQuery] = useState('')
  const [showSearch, setShowSearch] = useState(false)
  const searchInputRef = useRef<HTMLInputElement>(null)

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
      </div>

      {/* Workspaces (primary) + Global libraries */}
      <div className="flex-1 overflow-y-auto py-3">
        {/* Workspaces section */}
        <div
          className="mb-1"
          role="group"
          aria-label="Workspaces"
          tabIndex={-1}
          onKeyDown={(e: React.KeyboardEvent<HTMLDivElement>) => {
            if (e.key === '/' && !showSearch) {
              e.preventDefault()
              setShowSearch(true)
              setTimeout(() => searchInputRef.current?.focus(), 0)
            }
          }}
        >
          {/* Section header */}
          <div className="flex items-center justify-between px-4 py-1">
            <span className="text-[10px] font-semibold uppercase tracking-widest text-[var(--color-muted)]">
              Workspaces
            </span>
            <button
              type="button"
              onClick={() => setNewProjectOpen(true)}
              aria-label="New workspace"
              className="rounded p-0.5 text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors"
            >
              <Plus size={14} />
            </button>
          </div>

          {/* Inline search input (Fix 10) */}
          {showSearch && (
            <div className="px-3 pb-1">
              <div className="flex items-center gap-1.5 rounded bg-[var(--color-surface-2)] border border-[var(--color-border)] px-2 py-1">
                <MagnifyingGlass size={12} className="text-[var(--color-muted)] flex-shrink-0" />
                <input
                  ref={searchInputRef}
                  type="text"
                  value={searchQuery}
                  onChange={(e) => setSearchQuery(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Escape') {
                      setSearchQuery('')
                      setShowSearch(false)
                    }
                    e.stopPropagation()
                  }}
                  onBlur={() => {
                    setSearchQuery('')
                    setShowSearch(false)
                  }}
                  placeholder="Filter workspaces…"
                  aria-label="Filter workspaces"
                  className="flex-1 bg-transparent text-xs text-[var(--color-secondary)] outline-none placeholder:text-[var(--color-muted)]"
                />
              </div>
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
                onClick={() => setNewProjectOpen(true)}
                className="text-xs text-[var(--color-accent)] hover:underline"
              >
                New workspace
              </button>
            </div>
          )}

          {/* Workspace list — filtered by search query when showSearch is active */}
          {!projectsLoading && visibleProjects
            .filter((p) => !showSearch || p.name.toLowerCase().includes(searchQuery.toLowerCase()))
            .map((project) => {
            const isActive = activeWorkspaceId === project.id
            const isInbox = project.is_default === true
            return (
              <button
                key={project.id}
                type="button"
                onClick={() => {
                  setActiveWorkspaceId(project.id)
                  navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: project.id } })
                  if (!effectivelyPinned) close()
                }}
                className={cn(
                  'flex items-center gap-2 w-full px-4 py-2 mx-0 text-sm transition-colors text-left',
                  isActive
                    ? 'text-[var(--color-accent)] font-medium'
                    : 'text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]'
                )}
              >
                {/* Active indicator: gold pulsing dot; Inbox uses Tray icon */}
                {isActive ? (
                  <span
                    className="w-2 h-2 rounded-full bg-[var(--color-accent)] animate-pulse flex-shrink-0"
                    aria-hidden="true"
                  />
                ) : isInbox ? (
                  <Tray size={14} className="flex-shrink-0 text-[var(--color-accent)]" />
                ) : (
                  <Folder size={14} className="flex-shrink-0 text-[var(--color-muted)]" />
                )}
                <span className="flex-1 truncate">{project.name}</span>
                {project.task_count > 0 && (
                  <span className="text-[10px] text-[var(--color-muted)] flex-shrink-0">
                    {project.task_count}
                  </span>
                )}
              </button>
            )
          })}

          {/* Show more / less toggle */}
          {!projectsLoading && hasMore && !showSearch && (
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
                {project.task_count > 0 && (
                  <span className="text-[10px] text-[var(--color-muted)] flex-shrink-0">
                    {project.task_count}
                  </span>
                )}
              </button>
            )
          })}
        </div>

        {/* Global libraries section */}
        <div className="mt-4" role="group" aria-label="Global libraries">
          <div className="px-4 py-1">
            <span className="text-[10px] font-semibold uppercase tracking-widest text-[var(--color-muted)]">
              Library
            </span>
          </div>
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
                    : 'text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]',
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
      </div>

      {/* Bottom: Notifications + Settings + Profile + Pin toggle */}
      <div className="border-t border-[var(--color-border)] py-3">
        {/* Notifications row — Tray icon + unread badge */}
        <button
          type="button"
          onClick={toggleNotificationPanel}
          aria-label="Notifications"
          data-testid="sidebar-notifications"
          className="relative flex items-center gap-3 px-4 py-2.5 mx-2 rounded-lg text-sm text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors w-[calc(100%-16px)]"
        >
          <Tray size={18} />
          <span className="flex-1 text-left">Notifications</span>
          {unreadCount > 0 && (
            <span
              data-testid="sidebar-notification-badge"
              className="flex h-4 min-w-4 items-center justify-center rounded-full px-1 text-[10px] font-medium leading-none bg-[var(--color-error)] text-white"
            >
              {unreadCount > 99 ? '99+' : unreadCount}
            </span>
          )}
        </button>

        {/* Settings */}
        <Link
          to="/settings"
          aria-label="Settings"
          aria-current={location.pathname === '/settings' ? 'page' : undefined}
          onClick={() => { if (!effectivelyPinned) close() }}
          className={cn(
            'flex items-center gap-3 px-4 py-2.5 mx-2 rounded-lg text-sm transition-colors',
            location.pathname === '/settings'
              ? 'bg-[var(--color-surface-2)] text-[var(--color-accent)] font-medium'
              : 'text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]'
          )}
        >
          <Gear
            size={18}
            weight={location.pathname === '/settings' ? 'fill' : 'regular'}
          />
          Settings
        </Link>

        {/* Profile dropdown — opens upward */}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              aria-label="Open profile menu"
              data-testid="sidebar-profile-trigger"
              className="flex items-center gap-3 px-4 py-2.5 mx-2 rounded-lg text-sm text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors w-[calc(100%-16px)]"
            >
              <UserCircle size={18} weight="regular" />
              <span className="flex-1 text-left truncate">
                {username ?? 'Profile'}
              </span>
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
            <DropdownMenuItem asChild>
              <Link
                to="/profile"
                className={cn(
                  'flex items-center gap-2 cursor-pointer',
                  location.pathname === '/profile' && 'text-[var(--color-accent)]',
                )}
              >
                <UserCircle size={14} />
                Profile
              </Link>
            </DropdownMenuItem>
            <DropdownMenuItem asChild>
              <Link
                to="/profile"
                className="flex items-center gap-2 cursor-pointer"
              >
                <Sun size={14} />
                Appearance
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

        {/* Pin toggle — only shown when the viewport is wide enough to support it (≥1024px) */}
        {canPin && (
          <button
            onClick={togglePin}
            aria-label={isPinned ? 'Unpin sidebar' : 'Pin sidebar'}
            aria-pressed={isPinned}
            title={isPinned ? 'Unpin sidebar' : 'Pin sidebar'}
            className="flex items-center gap-3 px-4 py-2.5 mx-2 rounded-lg text-sm text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] transition-colors w-[calc(100%-16px)]"
          >
            {isPinned ? <PushPinSlash size={18} /> : <PushPin size={18} />}
            {isPinned ? 'Unpin sidebar' : 'Pin sidebar'}
          </button>
        )}
      </div>

      {/* New workspace slide-over — rendered inside nav to avoid stacking context issues */}
      <NewWorkspaceSlideOver open={newProjectOpen} onOpenChange={setNewProjectOpen} />
    </nav>
  )

  return (
    <>
      {/* FR-015: Pinned mode — permanent panel, only rendered when ≥1024px AND user chose to pin */}
      {effectivelyPinned && (
        <aside
          className="flex flex-col h-full flex-shrink-0 bg-[var(--color-surface-1)] border-r border-[var(--color-border)]"
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
            className="fixed left-0 top-0 z-40 flex h-full flex-col bg-[var(--color-surface-1)] border-r border-[var(--color-border)] shadow-2xl"
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
