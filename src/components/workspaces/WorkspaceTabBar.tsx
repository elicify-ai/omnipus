import React from 'react'
import { Link, useLocation, useNavigate } from '@tanstack/react-router'
import { motion } from 'framer-motion'
import {
  ChatCircle,
  SquaresFour,
  ListBullets,
  Graph,
  CalendarBlank,
  UsersThree,
  CaretDown,
} from '@phosphor-icons/react'
import type { Icon } from '@phosphor-icons/react'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { cn } from '@/lib/utils'

// The 7-tab workspace container surface. Each tab is a deep-linkable sub-route
// under /workspaces/$workspaceId. Chat is the default landing tab.
export interface WorkspaceTab {
  segment: 'chat' | 'board' | 'list' | 'graph' | 'calendar' | 'team' | 'settings'
  label: string
  Icon: Icon
  /** Icon-only tab — the label is aria/tooltip only, never rendered as text. */
  iconOnly?: boolean
}

export const WORKSPACE_TABS: WorkspaceTab[] = [
  { segment: 'chat', label: 'Chat', Icon: ChatCircle },
  { segment: 'board', label: 'Board', Icon: SquaresFour },
  { segment: 'list', label: 'List', Icon: ListBullets },
  { segment: 'graph', label: 'Graph', Icon: Graph },
  { segment: 'calendar', label: 'Calendar', Icon: CalendarBlank },
  { segment: 'team', label: 'Team', Icon: UsersThree },
  // NOTE: workspace settings is deliberately NOT a tab — settings is chrome,
  // not a view. It's reached by clicking the workspace NAME in the top bar
  // (WorkspaceTabContainer), Notion-style. The /settings route still exists.
]

interface WorkspaceTabBarProps {
  workspaceId: string
}

/**
 * Workspace tab bar — Sovereign Deep, Outfit labels, gold active underline
 * that slides between tabs with a spring transition.
 *
 * Responsive strategy (container-query, relative to the @container top-bar):
 *   ≥ 72rem (1152px): full 7-tab strip (hidden @6xl:flex)
 *   < 72rem (1152px): single "Active ▾" view-switcher dropdown (flex @6xl:hidden)
 *
 * The full strip retains all workspace-tab-<segment> test ids so Playwright
 * tests at 1280px viewport (container ≥1152px) still find them.
 *
 * Sits inline inside the WorkspaceTabContainer top-bar row (Row 1). The parent
 * row owns the background (no border — flat shell alignment); this component
 * only renders the tab list.
 */
export function WorkspaceTabBar({ workspaceId }: WorkspaceTabBarProps) {
  const location = useLocation()
  const navigate = useNavigate()
  const activeSegment = resolveActiveSegment(location.pathname, workspaceId)
  const activeTab = WORKSPACE_TABS.find((t) => t.segment === activeSegment)

  return (
    <div className="flex-shrink-0 flex items-stretch">
      {/* ── Full 7-tab strip: shown when container ≥ 1152px (72rem) ─────── */}
      <div
        role="tablist"
        aria-label="Workspace views"
        className="hidden @6xl:flex items-stretch gap-1 overflow-x-auto min-w-0 flex-1"
        style={{ scrollbarWidth: 'none', msOverflowStyle: 'none' } as React.CSSProperties}
      >
        {WORKSPACE_TABS.map(({ segment, label, Icon, iconOnly }) => {
          const isActive = segment === activeSegment
          return (
            <Link
              key={segment}
              to={`/workspaces/$workspaceId/${segment}`}
              params={{ workspaceId }}
              role="tab"
              aria-selected={isActive}
              aria-label={label}
              title={iconOnly ? label : undefined}
              data-testid={`workspace-tab-${segment}`}
              className={cn(
                // h-chrome-header (the literal 44px token, NOT h-11) makes the tab
                // fill the workspace top bar's exact height so the active underline
                // lands flush on the bar's bottom edge. h-11 is rem-based and would
                // be 38.5px at the default 14px root font-size (globals.css clamps
                // root to 14px), leaving the underline ~5px high. NOT h-full either:
                // the parent header uses items-center, so height:100% resolves to
                // auto (no-op) and the underline would float mid-header.
                'group relative flex items-center gap-1.5 px-3 h-chrome-header min-h-chrome-header text-sm font-headline whitespace-nowrap outline-none transition-colors',
                'focus-visible:ring-2 focus-visible:ring-[var(--color-accent)]/50 rounded-t-sm',
                isActive
                  ? 'text-[var(--color-accent)]'
                  : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
              )}
            >
              <Icon size={16} weight={isActive ? 'fill' : 'regular'} />
              {!iconOnly && <span>{label}</span>}
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

      {/* ── View-switcher dropdown: shown when container < 1152px (72rem) ── */}
      <div className="flex @6xl:hidden items-center px-2">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              data-testid="workspace-view-switcher"
              aria-label={`Switch view, currently ${activeTab?.label ?? 'Chat'}`}
              className={cn(
                'flex items-center gap-1.5 px-3 h-11 text-sm font-headline whitespace-nowrap rounded-md',
                'text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors',
                'focus-visible:ring-2 focus-visible:ring-[var(--color-accent)]/50 outline-none',
                'pointer-coarse:min-h-[44px]',
              )}
            >
              {activeTab && <activeTab.Icon size={16} weight="fill" className="text-[var(--color-accent)]" />}
              <span className="text-[var(--color-accent)]">{activeTab?.label ?? 'Chat'}</span>
              <CaretDown size={13} className="opacity-60" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="w-44">
            {WORKSPACE_TABS.map(({ segment, label, Icon }) => {
              const isActive = segment === activeSegment
              return (
                <DropdownMenuItem
                  key={segment}
                  onClick={() => {
                    void navigate({
                      to: `/workspaces/$workspaceId/${segment}`,
                      params: { workspaceId },
                    })
                  }}
                  className={cn(
                    'flex items-center gap-2',
                    isActive && 'text-[var(--color-accent)]',
                  )}
                >
                  <Icon size={15} weight={isActive ? 'fill' : 'regular'} />
                  <span>{label}</span>
                  {isActive && (
                    <span className="ml-auto text-[10px] text-[var(--color-accent)]" aria-hidden="true">
                      ●
                    </span>
                  )}
                </DropdownMenuItem>
              )
            })}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </div>
  )
}

/**
 * Derive the active tab segment from a pathname. Returns 'chat' for the bare
 * container path (the index redirect target).
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
