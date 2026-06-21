import { useLocation, useNavigate, Link } from '@tanstack/react-router'
import { List, CaretLeft, Bell, UserCircle, SignOut, Sun, LockKey } from '@phosphor-icons/react'
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
} from '@/components/ui/dropdown-menu'
import { useSidebarStore } from '@/store/sidebar'
import { useUiStore } from '@/store/ui'
import { useAuthStore } from '@/store/auth'
import { useNotificationsStore } from '@/store/notifications'
import { SessionBar } from '@/components/chat/SessionBar'
import { cn } from '@/lib/utils'

/**
 * TopBar — application top bar rendered inside the main content column.
 *
 * Hosts the hamburger (sidebar toggle), the session bar (chat screen), the
 * sessions drawer button, the notification bell, and a profile dropdown menu
 * (Profile / Appearance / Sessions / Sign Out). The profile entry moved here
 * from the Sidebar so it is reachable without opening the sidebar overlay.
 */
export function TopBar() {
  const { toggle } = useSidebarStore()
  const location = useLocation()
  const navigate = useNavigate()
  const { openSessionPanel, toggleNotificationPanel } = useUiStore()
  const unreadCount = useNotificationsStore((s) => s.unreadCount)
  const username = useAuthStore((s) => s.username)
  const clearAuth = useAuthStore((s) => s.clearAuth)

  // Show SessionBar on the root chat route, named session routes, and the
  // workspace Chat tab (the workspace's front view).
  const isChatScreen =
    location.pathname === '/' ||
    location.pathname === '' ||
    location.pathname.startsWith('/sessions/') ||
    /^\/workspaces\/[^/]+\/chat$/.test(location.pathname)

  function handleLogout() {
    clearAuth()
    navigate({ to: '/login' })
  }

  return (
    <header className="flex items-center gap-3 px-4 py-3 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] flex-shrink-0">
      {/* Hamburger — always on left */}
      <button
        id="sidebar-hamburger"
        onClick={toggle}
        aria-label="Toggle navigation sidebar"
        className="flex items-center justify-center h-8 w-8 rounded-md text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors flex-shrink-0"
      >
        <List size={20} />
      </button>

      {/* Session bar — wired only on chat screen */}
      <div className="flex-1 min-w-0">
        {isChatScreen ? (
          <SessionBar />
        ) : (
          <div id="session-bar-slot" className="flex-1" />
        )}
      </div>

      {/* Sessions button — top-right on chat screen, opens session drawer */}
      {isChatScreen && (
        <button
          type="button"
          onClick={openSessionPanel}
          aria-label="Open sessions panel"
          className="flex items-center justify-center h-8 px-2 gap-1 rounded-md text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors flex-shrink-0 text-xs"
        >
          <span className="hidden sm:inline">Sessions</span>
          <CaretLeft size={14} className="rotate-180" />
        </button>
      )}

      {/* Notification center bell — #264. Unread badge renders 99+ past 99. */}
      <button
        type="button"
        onClick={toggleNotificationPanel}
        aria-label="Open notifications"
        data-testid="notification-bell"
        className="relative flex items-center justify-center h-8 w-8 rounded-md text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors flex-shrink-0"
      >
        <Bell size={20} />
        {unreadCount > 0 && (
          <span
            data-testid="notification-badge"
            className="absolute -top-0.5 -right-0.5 flex h-4 min-w-4 items-center justify-center rounded-full px-1 text-[10px] font-medium leading-none bg-[var(--color-error)] text-white"
          >
            {unreadCount > 99 ? '99+' : unreadCount}
          </span>
        )}
      </button>

      {/* Profile dropdown — avatar + username on the right */}
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            aria-label="Open profile menu"
            data-testid="profile-menu-trigger"
            className="flex items-center gap-1.5 h-8 px-1.5 rounded-md text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors flex-shrink-0"
          >
            <UserCircle size={20} weight="regular" />
            <span className="hidden sm:inline text-xs max-w-[120px] truncate">
              {username ?? 'Profile'}
            </span>
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
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
          <DropdownMenuItem
            onSelect={() => openSessionPanel()}
            className="flex items-center gap-2 cursor-pointer"
          >
            <LockKey size={14} />
            Sessions
          </DropdownMenuItem>
          <DropdownMenuSeparator className="bg-[var(--color-border)]" />
          <DropdownMenuItem
            onSelect={handleLogout}
            className="flex items-center gap-2 cursor-pointer text-[var(--color-muted)]"
          >
            <SignOut size={14} />
            Sign out
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </header>
  )
}
