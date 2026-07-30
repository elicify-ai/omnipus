import { Link, useLocation, useNavigate } from '@tanstack/react-router'
import { motion } from 'framer-motion'
import {
  ChatCircle,
  SquaresFour,
  ListBullets,
  Graph,
  CalendarBlank,
  UsersThree,
  Files,
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

// The workspace container surface: 7 view tabs (+ the workspace-name →
// settings item). Each tab is a deep-linkable sub-route under
// /workspaces/$workspaceId. Chat is the default landing tab.
export const WORKSPACE_TABS = [
  { segment: 'chat', label: 'Chat', Icon: ChatCircle },
  { segment: 'board', label: 'Board', Icon: SquaresFour },
  { segment: 'list', label: 'List', Icon: ListBullets },
  { segment: 'graph', label: 'Graph', Icon: Graph },
  { segment: 'calendar', label: 'Calendar', Icon: CalendarBlank },
  // Renamed Media -> Library (library-spec.md supersedes the old workspace
  // Media tab / UUID-blob manifest surface entirely). The segment/route
  // ('media') and its Link-based navigation are DELIBERATELY left in place
  // rather than special-cased into a plain button here: WORKSPACE_TABS feeds
  // both the full strip below and the compact view-switcher dropdown via one
  // uniform `.map`, and a route-vs-button special case would fork that
  // rendering in both places plus the SEGMENT_LABELS completeness map for a
  // single entry. Instead, the route itself
  // (routes/_app/workspaces.$workspaceId.media.tsx) is now a redirect stub:
  // clicking this tab (or hitting a bookmarked /workspaces/{id}/media URL
  // directly) opens the Library panel scoped to this workspace — the same
  // `useUiStore.getState().openLibraryPanel(workspaceId)` call
  // ChatControls.tsx's "Open library" button makes — then redirects back to
  // the workspace's Chat tab so the URL never dead-ends on a page with no
  // content of its own.
  { segment: 'media', label: 'Library', Icon: Files },
  { segment: 'team', label: 'Team', Icon: UsersThree },
  // NOTE: workspace settings is deliberately NOT a tab — settings is chrome,
  // not a view. It's reached by clicking the workspace NAME in the top bar
  // (WorkspaceTabContainer) or the compact dropdown's settings entry,
  // Notion-style. The /settings route still exists.
] as const

/** Every real WORKSPACE_TABS segment — derived from the array itself (not a
 * hand-maintained union), so adding/renaming/removing a tab there can never
 * silently drift out of sync with this type, including the SEGMENT_LABELS
 * completeness check below. */
export type TabSegment = (typeof WORKSPACE_TABS)[number]['segment']
export type WorkspaceSegment = TabSegment | 'settings'

export interface WorkspaceTab {
  segment: TabSegment
  label: string
  Icon: Icon
}

/** Single source for every segment's display label — the six WORKSPACE_TABS
 * labels plus 'settings', which deliberately has no WORKSPACE_TABS entry. All
 * three usages of this map are inside the ONE compact dropdown (the
 * view-switcher trigger button + its settings menu entry, both below @6xl) —
 * the full tab strip reads `label` directly off WORKSPACE_TABS and never
 * touches this map. Before this map existed, the compact dropdown re-derived
 * its own `activeTab?.label ?? (segment === 'settings' ? ... : 'Chat')`
 * fallback at each of those three call sites, and that duplication is what
 * previously let them drift ('Workspace settings' vs 'Settings') for the same
 * state. Built via `reduce` (not `Object.fromEntries`, whose lib type always
 * widens to a `{[k: string]: string}` index signature — TypeScript's
 * `Object.fromEntries` has no literal-key-preserving overload) so the
 * `tab.segment` key assignment below is checked against the declared
 * `Record<WorkspaceSegment, string>` on every iteration, derived straight
 * from `TabSegment` — a tab added to WORKSPACE_TABS without a label is a
 * compile error here, not a silent runtime gap. */
const SEGMENT_LABELS: Record<WorkspaceSegment, string> = WORKSPACE_TABS.reduce(
  (acc, tab) => {
    acc[tab.segment] = tab.label
    return acc
  },
  { settings: 'Settings' } as Record<WorkspaceSegment, string>,
)

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
 *   ≥ 72rem (1152px): full strip — 7 view tabs (+ the workspace-name →
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
          bounded (7 view tabs + the workspace-name → settings item, name
          truncated) so overflow can't occur. ─────── */}
      <div
        role="tablist"
        aria-label="Workspace views"
        className="hidden @6xl:flex items-stretch gap-1 min-w-0 flex-1"
      >
        {/* First tablist item: the workspace name → settings. Inside the
            tablist (not a stray sibling button) so it IS part of the menu
            component — same styling, same underline, same tab semantics. */}
        <button tabIndex={0}
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
              tabIndex={0}
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
            <button tabIndex={0}
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
              aria-current={settingsActive ? 'page' : undefined}
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
                  aria-current={isActive ? 'page' : undefined}
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
