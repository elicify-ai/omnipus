import { Link, useLocation } from '@tanstack/react-router'
import { motion } from 'framer-motion'
import {
  ChatCircle,
  SquaresFour,
  ListBullets,
  Graph,
  CalendarBlank,
  UsersThree,
  Gear,
} from '@phosphor-icons/react'
import type { Icon } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

// The 7-tab workspace container surface. Each tab is a deep-linkable sub-route
// under /workspaces/$workspaceId. Chat is the default landing tab.
export interface WorkspaceTab {
  /** The path segment appended to /workspaces/$workspaceId (e.g. 'chat'). */
  segment: 'chat' | 'board' | 'list' | 'graph' | 'calendar' | 'team' | 'settings'
  label: string
  Icon: Icon
}

export const WORKSPACE_TABS: WorkspaceTab[] = [
  { segment: 'chat', label: 'Chat', Icon: ChatCircle },
  { segment: 'board', label: 'Board', Icon: SquaresFour },
  { segment: 'list', label: 'List', Icon: ListBullets },
  { segment: 'graph', label: 'Graph', Icon: Graph },
  { segment: 'calendar', label: 'Calendar', Icon: CalendarBlank },
  { segment: 'team', label: 'Team', Icon: UsersThree },
  { segment: 'settings', label: 'Settings', Icon: Gear },
]

interface WorkspaceTabBarProps {
  workspaceId: string
}

/**
 * Sticky workspace tab bar — Sovereign Deep, Outfit labels, gold active
 * underline that slides between tabs with a spring transition (shared
 * layoutId so the underline animates rather than snapping).
 */
export function WorkspaceTabBar({ workspaceId }: WorkspaceTabBarProps) {
  const location = useLocation()
  // Resolve the active segment from the URL. The container's index route
  // redirects to /chat, so the bare /workspaces/$id path is transient.
  const activeSegment = resolveActiveSegment(location.pathname, workspaceId)

  return (
    <div
      role="tablist"
      aria-label="Workspace views"
      className="sticky top-0 z-10 flex items-stretch gap-1 px-3 border-b border-[var(--color-border)] bg-[var(--color-surface-1)]/95 backdrop-blur-sm overflow-x-auto flex-shrink-0"
    >
      {WORKSPACE_TABS.map(({ segment, label, Icon }) => {
        const isActive = segment === activeSegment
        return (
          <Link
            key={segment}
            to={`/workspaces/$workspaceId/${segment}`}
            params={{ workspaceId }}
            role="tab"
            aria-selected={isActive}
            aria-label={label}
            data-testid={`workspace-tab-${segment}`}
            className={cn(
              'group relative flex items-center gap-1.5 px-3 py-3 text-sm font-headline whitespace-nowrap outline-none transition-colors',
              'focus-visible:ring-2 focus-visible:ring-[var(--color-accent)]/50 rounded-t-sm',
              isActive
                ? 'text-[var(--color-accent)]'
                : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
            )}
          >
            <Icon size={16} weight={isActive ? 'fill' : 'regular'} />
            <span>{label}</span>
            {isActive && (
              <motion.div
                layoutId="workspace-tab-underline"
                className="absolute inset-x-1 -bottom-px h-0.5 rounded-full bg-[var(--color-accent)]"
                transition={{ type: 'spring', stiffness: 500, damping: 32 }}
              />
            )}
          </Link>
        )
      })}
    </div>
  )
}

/**
 * Derive the active tab segment from a pathname. Returns 'chat' for the bare
 * container path (the index redirect target) so the Chat tab reads active
 * during the transient pre-redirect render.
 */
export function resolveActiveSegment(
  pathname: string,
  workspaceId: string,
): WorkspaceTab['segment'] {
  const base = `/workspaces/${workspaceId}`
  if (!pathname.startsWith(base)) return 'chat'
  const rest = pathname.slice(base.length).replace(/^\//, '')
  const segment = rest.split('/')[0]
  const match = WORKSPACE_TABS.find((t) => t.segment === segment)
  return match?.segment ?? 'chat'
}
