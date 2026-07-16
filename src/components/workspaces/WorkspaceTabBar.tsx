import { Link, useLocation, useNavigate } from '@tanstack/react-router'
import { motion } from 'framer-motion'
import {
  ChatCircle,
  SquaresFour,
  ListBullets,
  Graph,
  CalendarBlank,
  UsersThree,
  Tray,
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

// The workspace container surface: 6 view tabs (+ the workspace-name →
// settings item). Each tab is a deep-linkable sub-route under
// /workspaces/$workspaceId. Chat is the default landing tab.
export type TabSegment = 'chat' | 'board' | 'list' | 'graph' | 'calendar' | 'team'
export type WorkspaceSegment = TabSegment | 'settings'

export interface WorkspaceTab {
  segment: TabSegment
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
  // NOTE: workspace settings is deliberately NOT a tab — settings is chrome,
  // not a view. It's reached by clicking the workspace NAME in the top bar
  // (WorkspaceTabContainer) or the compact dropdown's settings entry,
  // Notion-style. The /settings route still exists.
]

/** Single source for every segment's display label — the six WORKSPACE_TABS
 * labels plus 'settings', which deliberately has no WORKSPACE_TABS entry.
 * Both header renderings (full strip's compact switcher, and the compact
 * dropdown) read from this instead of each re-deriving their own
 * `activeTab?.label ?? (segment === 'settings' ? ... : 'Chat')` fallback —
 * that duplication is what previously let the two renderings drift
 * ('Workspace settings' vs 'Settings') for the same state. */
export const SEGMENT_LABELS: Record<WorkspaceSegment, string> = {
  ...(Object.fromEntries(WORKSPACE_TABS.map((t) => [t.segment, t.label])) as Record<
    TabSegment,
    string
  >),
  settings: 'Settings',
}

interface WorkspaceTabBarProps {
  workspaceId: string
  /** Workspace display name — rendered as the FIRST tablist item (→ settings). */
  workspaceName: string
}

/**
 * Workspace tab bar — Sovereign Deep, Outfit labels, gold active underline
 * that slides between tabs with a spring transition.
 *
 * Responsive strategy (container-query, relative to the @container top-bar):
 *   ≥ 72rem (1152px): full strip — 6 view tabs (+ the workspace-name →
 *     settings item) (hidden @6xl:flex)
 *   < 72rem (1152px): single "Active ▾" view-switcher dropdown (flex
 *     @6xl:hidden) — also carries a settings entry, since narrow viewports
 *     have no other settings entry point in this header
 *
 * The full strip retains all workspace-tab-<segment> test ids so Playwright
 * tests at 1280px viewport (container ≥1152px) still find them.
 *
 * Sits inline inside the WorkspaceTabContainer top-bar row (Row 1). The parent
 * row owns the background (no border — flat shell alignment); this component
 * only renders the tab list.
 */
export function WorkspaceTabBar({ workspaceId, workspaceName }: WorkspaceTabBarProps) {
  const location = useLocation()
  const navigate = useNavigate()
  const activeSegment = resolveActiveSegment(location.pathname, workspaceId)
  const activeTab = WORKSPACE_TABS.find((t) => t.segment === activeSegment)
  const settingsActive = activeSegment === 'settings'

  return (
    <div className="flex-shrink-0 flex items-stretch">
      {/* ── Full tab strip: shown when container ≥ 1152px (72rem).
          NO overflow-x-auto: a scrollable tablist let mouse-wheel/touch
          gestures scroll the menu itself up/down (overflow containers clip +
          scroll BOTH axes) — chrome must never move. The strip's content is
          bounded (6 view tabs + the workspace-name → settings item, name
          truncated) so overflow can't occur. ─────── */}
      <div
        role="tablist"
        aria-label="Workspace views"
        className="hidden @6xl:flex items-stretch gap-1 min-w-0 flex-1"
      >
        {/* First tablist item: the workspace name → settings. Inside the
            tablist (not a stray sibling button) so it IS part of the menu
            component — same styling, same underline, same tab semantics. */}
        <button
          type="button"
          role="tab"
          onClick={() =>
            navigate({ to: '/workspaces/$workspaceId/settings', params: { workspaceId } })
          }
          title="Workspace settings"
          aria-label={`${workspaceName} — workspace settings`}
          aria-selected={settingsActive}
          data-testid="workspace-name-button"
          className={cn(
            'relative flex items-center gap-1.5 px-3 h-chrome-header min-h-chrome-header max-w-[24ch] flex-shrink-0 text-sm font-headline whitespace-nowrap outline-none transition-colors',
            ' rounded-t-sm',
            settingsActive
              ? 'text-[var(--color-accent)]'
              : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
          )}
        >
          <Tray size={16} weight={settingsActive ? 'fill' : 'regular'} className="flex-shrink-0" />
          <span className="truncate">{workspaceName}</span>
          {settingsActive && (
            <motion.div
              layoutId="workspace-tab-underline"
              className="absolute inset-x-1 -bottom-px h-0.5 rounded-full bg-[var(--color-accent)]"
              transition={{ type: 'spring', stiffness: 500, damping: 32 }}
            />
          )}
        </button>

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
                // h-chrome-header (the literal 44px token, NOT h-11) makes the tab
                // fill the workspace top bar's exact height so the active underline
                // lands flush on the bar's bottom edge. h-11 is rem-based and would
                // be 38.5px at the default 14px root font-size (globals.css clamps
                // root to 14px), leaving the underline ~5px high. NOT h-full either:
                // the parent header uses items-center, so height:100% resolves to
                // auto (no-op) and the underline would float mid-header.
                'group relative flex items-center gap-1.5 px-3 h-chrome-header min-h-chrome-header text-sm font-headline whitespace-nowrap outline-none transition-colors',
                ' rounded-t-sm',
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

      {/* ── View-switcher dropdown: shown when container < 1152px (72rem) ── */}
      <div className="flex @6xl:hidden items-center px-2">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              data-testid="workspace-view-switcher"
              aria-label={`Switch view, currently ${SEGMENT_LABELS[activeSegment]}`}
              className={cn(
                'flex items-center gap-1.5 px-3 h-11 text-sm font-headline whitespace-nowrap rounded-md',
                'text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors',
                ' outline-none',
                'pointer-coarse:min-h-[44px]',
              )}
            >
              {settingsActive ? (
                <Tray size={16} weight="fill" className="text-[var(--color-accent)]" />
              ) : (
                activeTab && <activeTab.Icon size={16} weight="fill" className="text-[var(--color-accent)]" />
              )}
              <span className="text-[var(--color-accent)]">{SEGMENT_LABELS[activeSegment]}</span>
              <CaretDown size={13} className="opacity-60" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="w-44">
            {/* Settings entry — narrow viewports have no other settings entry
                point in this header (the full-strip name button is hidden
                below @6xl), so the compact dropdown must carry one too. */}
            <DropdownMenuItem
              key="settings"
              data-testid="workspace-view-switcher-settings"
              onClick={() => {
                void navigate({ to: '/workspaces/$workspaceId/settings', params: { workspaceId } })
              }}
              className={cn(
                'flex items-center gap-2',
                settingsActive && 'text-[var(--color-accent)]',
              )}
            >
              <Tray size={15} weight={settingsActive ? 'fill' : 'regular'} />
              <span>{SEGMENT_LABELS.settings}</span>
              {settingsActive && (
                <span className="ml-auto text-[10px] text-[var(--color-accent)]" aria-hidden="true">
                  ●
                </span>
              )}
            </DropdownMenuItem>
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
): WorkspaceSegment {
  const base = `/workspaces/${workspaceId}`
  if (!pathname.startsWith(base)) return 'chat'
  const rest = pathname.slice(base.length).replace(/^\//, '')
  const segment = rest.split('/')[0]
  // 'settings' is a real segment but deliberately NOT in WORKSPACE_TABS (it's
  // reached via the workspace-name button or the compact dropdown's settings
  // entry, not a tab). It must still resolve — otherwise /settings falls
  // through to 'chat', wrongly marking the Chat tab active and rendering the
  // chat-only header controls on the settings page.
  if (segment === 'settings') return 'settings'
  const match = WORKSPACE_TABS.find((t) => t.segment === segment)
  return match?.segment ?? 'chat'
}
