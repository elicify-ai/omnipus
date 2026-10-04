import { Link, useLocation, useNavigate } from '@tanstack/react-router'
import { motion } from 'framer-motion'
import {
  ChatCircle,
  SquaresFour,
  CalendarBlank,
  UsersThree,
  Files,
  Buildings,
  CaretDown,
  Tray,
} from '@phosphor-icons/react'
import type { Icon } from '@phosphor-icons/react'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Button } from '@/components/ui/button'
import { useUiStore } from '@/store/ui'
import { leaveGateThen } from '@/components/panel-shell/leaveGate'
import type { WorkspacePanelId } from '@/components/panel-shell/types'
import { cn } from '@/lib/utils'

// The workspace container surface — the MAJ-007 mixed-mode strip (US-5):
// entries are EITHER navigation links to their deep-linkable sub-route,
// OR, for a REGISTERED side panel, a toggle button with aria-pressed.
// Chat is the default landing tab and the panel-host route.
//
// Entry kinds (side-panel-shell-spec.md §10 / MAJ-007 / MAJ-012):
//   - workspace name → settings button (chrome, not a view; aria-current
//     when the settings route is the page)
//   - Chat → the panel-host page: a button that navigates to the chat route
//     and carries aria-current="page" while it IS the underlying page
//   - Library ('media', wave 1) and Mail ('mail', wave 2) → REGISTERED
//     panel toggles: aria-pressed, opens/closes the panel scoped to this
//     workspace via the leave gate — NO navigation (US-5 AS-1/AS-2)
//   - Tasks/Calendar/Team → unregistered panels (wave 3): ordinary
//     navigation Links, no aria-pressed (MAJ-012 mixed mode)
//
// ADR-051 D1 — "Tasks" screen: Board/List/Graph collapse into ONE screen
// (WorkspaceTasksTab, rendered under the `board` route segment — kept
// unchanged to avoid breaking deep links) with an in-screen view switcher.
// The `list` and `graph` top-level tabs are retired; their route files now
// redirect to `board` (see workspaces.$workspaceId.list.tsx / .graph.tsx).
export const WORKSPACE_TABS = [
  { segment: 'chat', label: 'Chat', Icon: ChatCircle },
  { segment: 'board', label: 'Tasks', Icon: SquaresFour },
  { segment: 'calendar', label: 'Calendar', Icon: CalendarBlank },
  // Renamed Media -> Library (library-spec.md supersedes the old workspace
  // Media tab / UUID-blob manifest surface entirely). Wave 1: this strip
  // entry is a REGISTERED panel toggle (see PANEL_TOGGLE_SEGMENTS) —
  // clicking it opens/closes the Library side panel scoped to this
  // workspace, the same store call the sidebar Library button
  // makes. The route itself (routes/_app/workspaces.$workspaceId.media.tsx)
  // remains a redirect stub for BOOKMARKED /workspaces/{id}/media URLs: it
  // opens the Library panel and replaces the URL with chat?panel=library
  // (§8.2), so an old link never dead-ends on a page with no content of its
  // own.
  { segment: 'media', label: 'Library', Icon: Files },
  // Mail (email-mail-view-spec.md US-3): the workspace Mail panel. Wave 2
  // (side-panel-shell-spec.md §10 + §15 item 1): a REGISTERED panel toggle
  // exactly like Library — aria-pressed, leave-gated open/close, no
  // navigation. The route itself (routes/_app/workspaces.$workspaceId.mail.tsx)
  // remains the expand target and the bookmarked-/draft-link stub: it
  // consumes the §17 params into the panel intent and retargets to
  // chat?panel=mail (§8.2).
  { segment: 'mail', label: 'Mail', Icon: Tray },
  { segment: 'team', label: 'Team', Icon: UsersThree },
  // NOTE: workspace settings is deliberately NOT a tab — settings is chrome,
  // not a view. It's reached by clicking the workspace NAME in the top bar
  // (WorkspaceTabContainer) or the compact dropdown's settings entry,
  // Notion-style. The /settings route still exists.
] as const

/** Registered-panel strip entries: strip segment → panel id. Wave 1:
 * Library ('media') only; wave 2 adds Mail (side-panel-shell-spec.md §10 +
 * §15 item 1 — "Mail joins in wave 2"); waves 2-3 leave Tasks/Calendar/
 * Team on the Link path until registered (MAJ-012 mixed mode), at which
 * point those segments move here too (their routes stay as the expand
 * targets). */
const PANEL_TOGGLE_SEGMENTS: Partial<Record<TabSegment, WorkspacePanelId>> = {
  media: 'library',
  mail: 'mail',
}

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

/** Single source for every segment's display label — the four WORKSPACE_TABS
 * labels plus 'settings', which deliberately has no WORKSPACE_TABS entry. All
 * three usages of this map are inside the ONE compact dropdown (the
 * view-switcher trigger button + its settings menu entry, both below @6xl) —
 * the full strip reads `label` directly off WORKSPACE_TABS and never
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
  /** Workspace display name — rendered as the FIRST strip entry (→ settings). */
  workspaceName: string
}

/**
 * Workspace tab bar — Sovereign Deep, Outfit labels, gold active underline
 * that slides between route entries with a spring transition.
 *
 * Responsive strategy (container-query, relative to the @container top-bar):
 *   ≥ 72rem (1152px): full strip — name → settings, Chat, the Library toggle,
 *     and the mixed-mode links (hidden @6xl:flex)
 *   < 72rem (1152px): single "Active ▾" view-switcher dropdown (flex
 *     @6xl:hidden) — registered panel entries use the same toggle model as
 *     the full strip; page entries navigate. It also carries settings,
 *     since narrow viewports have no other settings entry point here.
 *
 * The underline layoutId tracks the ROUTE entry only (the page you are on).
 * A pressed panel toggle shows its state via aria-pressed + accent colour,
 * not the underline — two elements sharing the layoutId at once (page entry
 * + pressed toggle) would make Framer's shared-layout animation fight
 * itself. The full strip retains all workspace-tab-<segment> test ids so
 * Playwright tests at 1280px viewport (container ≥1152px) still find them.
 *
 * Sits inline inside the WorkspaceTabContainer top-bar row (Row 1). The parent
 * row owns the background (no border — flat shell alignment); this component
 * only renders the entry strip.
 */
export function WorkspaceTabBar({ workspaceId, workspaceName }: WorkspaceTabBarProps) {
  const location = useLocation()
  const navigate = useNavigate()
  const activeSegment = resolveActiveSegment(location.pathname, workspaceId)
  const activeTab = WORKSPACE_TABS.find((t) => t.segment === activeSegment)
  const settingsActive = activeSegment === 'settings'
  const activePanelId = useUiStore((s) => s.activePanel?.id ?? null)

  /** US-5 AS-1/AS-2: toggle a registered panel scoped to this workspace —
   * through the CRIT-001 leave gate (a dirty outgoing Library asks before
   * it closes; clean runs synchronously). No navigation: the chat route
   * stays the underlying page. */
  const togglePanel = (panelId: WorkspacePanelId) => {
    const outgoingPanelId = useUiStore.getState().activePanel?.id ?? null
    leaveGateThen(outgoingPanelId, () => {
      const state = useUiStore.getState()
      if (state.activePanel?.id === panelId) {
        state.closePanel()
      } else {
        state.openPanel(panelId, workspaceId ? { workspaceId } : {})
      }
    })
  }

  const navigateToSegment = (segment: TabSegment) => {
    void navigate({ to: `/workspaces/$workspaceId/${segment}`, params: { workspaceId } })
  }

  const tabUnderline = (active: boolean) =>
    active ? (
      <motion.div
        layoutId="workspace-tab-underline"
        className="absolute inset-x-1 -bottom-px h-0.5 rounded-full bg-[var(--color-accent)]"
        transition={{ type: 'spring', stiffness: 500, damping: 32 }}
      />
    ) : null

  return (
    <div className="flex-shrink-0 flex items-stretch">
      {/* ── Full entry strip: shown when container ≥ 1152px (72rem).
          MAJ-007: this is NOT a role="tablist" — it is a mixed set of
          navigation links, a page entry and panel toggles, so the tablist
          tab semantics would be wrong. NO overflow-x-auto: a scrollable
          strip let mouse-wheel/touch gestures scroll it up/down (overflow
          containers clip + scroll BOTH axes) — chrome must never move. The
          strip's content is bounded (name + 5 entries, name truncated) so
          overflow can't occur. ─────── */}
      <div
        data-testid="workspace-tab-strip"
        className="hidden @6xl:flex items-stretch gap-[var(--space-1)] min-w-0 flex-1"
      >
        {/* First strip entry: the workspace name → settings. Inside the strip
            (not a stray sibling button) so it IS part of the menu component —
            same styling; it navigates, so it carries aria-current, never
            aria-pressed. */}
        <Button
          variant="ghost"
          onClick={() =>
            navigate({ to: '/workspaces/$workspaceId/settings', params: { workspaceId } })
          }
          title="Workspace settings"
          aria-label={`${workspaceName} — workspace settings`}
          aria-current={settingsActive ? 'page' : undefined}
          data-testid="workspace-name-button"
          className={cn(
            'relative h-chrome-header min-h-chrome-header max-w-[24ch] flex-shrink-0 justify-start gap-[var(--space-1)] rounded-t-sm rounded-b-none px-[var(--space-2-5)] py-0 font-headline whitespace-nowrap outline-none hover:bg-transparent',
            settingsActive
              ? 'text-[var(--color-accent)]'
              : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
          )}
        >
          <Buildings size={16} weight={settingsActive ? 'fill' : 'regular'} className="flex-shrink-0" />
          <span className="truncate">{workspaceName}</span>
          {tabUnderline(settingsActive)}
        </Button>

        {WORKSPACE_TABS.map(({ segment, label, Icon }) => {
          // Registered panel (wave 1: Library) → toggle button, no navigation.
          const panelId = PANEL_TOGGLE_SEGMENTS[segment]
          if (panelId) {
            const pressed = activePanelId === panelId
            return (
              <Button
                key={segment}
                variant="ghost"
                onClick={() => togglePanel(panelId)}
                title={label}
                aria-pressed={pressed}
                data-panel-trigger={panelId}
                data-testid={`workspace-tab-${segment}`}
                className={cn(
                  // h-chrome-header fills the exact 44px tokenized chrome row;
                  // h-11 is rem-based and is only 38.5px at the app root size.
                  'group relative flex items-center gap-[var(--space-1)] px-[var(--space-2-5)] h-chrome-header min-h-chrome-header text-[length:var(--type-body-compact-size)] font-headline whitespace-nowrap outline-none transition-colors rounded-t-sm',
                  pressed
                    ? 'text-[var(--color-accent)]'
                    : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
                )}
              >
                <Icon size={16} weight={pressed ? 'fill' : 'regular'} />
                <span>{label}</span>
              </Button>
            )
          }
          // Chat — the panel-host page. A button that navigates (same
          // treatment as the name entry) so the page-activeness attribute
          // lives on OUR element, not a routed anchor's prop whitelist;
          // aria-current="page" while chat IS the underlying page (US-5).
          if (segment === 'chat') {
            const isActive = segment === activeSegment
            return (
              <Button
                key={segment}
                variant="ghost"
                onClick={() => navigateToSegment(segment)}
                title={label}
                aria-current={isActive ? 'page' : undefined}
                data-testid={`workspace-tab-${segment}`}
                className={cn(
                  'group relative flex items-center gap-[var(--space-1)] px-[var(--space-2-5)] h-chrome-header min-h-chrome-header text-[length:var(--type-body-compact-size)] font-headline whitespace-nowrap outline-none transition-colors rounded-t-sm',
                  isActive
                    ? 'text-[var(--color-accent)]'
                    : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
                )}
              >
                <Icon size={16} weight={isActive ? 'fill' : 'regular'} />
                <span>{label}</span>
                {tabUnderline(isActive)}
              </Button>
            )
          }
          // Unregistered panels (Tasks/Calendar/Team, waves 2-3) — MAJ-012
          // mixed mode: ordinary navigation links to their full-page routes,
          // no aria-pressed. aria-current marks the one you are on.
          const isActive = segment === activeSegment
          return (
            <Link
              key={segment}
              to={`/workspaces/$workspaceId/${segment}`}
              params={{ workspaceId }}
              tabIndex={0}
              aria-current={isActive ? 'page' : undefined}
              aria-label={label}
              data-testid={`workspace-tab-${segment}`}
              className={cn(
                'group relative flex items-center gap-[var(--space-1)] px-[var(--space-2-5)] h-chrome-header min-h-chrome-header text-[length:var(--type-body-compact-size)] font-headline whitespace-nowrap outline-none transition-colors rounded-t-sm',
                isActive
                  ? 'text-[var(--color-accent)]'
                  : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
              )}
            >
              <Icon size={16} weight={isActive ? 'fill' : 'regular'} />
              <span>{label}</span>
              {tabUnderline(isActive)}
            </Link>
          )
        })}
      </div>

      {/* ── View-switcher dropdown: shown when container < 1152px (72rem) ── */}
      <div className="flex @6xl:hidden items-center px-[var(--space-2)]">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              data-testid="workspace-view-switcher"
              aria-label={`Switch view, currently ${SEGMENT_LABELS[activeSegment]}`}
              className={cn(
                'h-11 gap-[var(--space-1)] px-[var(--space-2-5)] font-headline whitespace-nowrap',
                'text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]',
                'outline-none',
                'pointer-coarse:min-h-[44px]',
              )}
            >
              {settingsActive ? (
                <Buildings size={16} weight="fill" className="text-[var(--color-accent)]" />
              ) : (
                activeTab && <activeTab.Icon size={16} weight="fill" className="text-[var(--color-accent)]" />
              )}
              <span className="text-[var(--color-accent)]">{SEGMENT_LABELS[activeSegment]}</span>
              <CaretDown size={13} className="opacity-60" />
            </Button>
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
                'flex items-center gap-[var(--space-2)]',
                settingsActive ? 'text-[var(--color-accent)]' : undefined,
              )}
            >
              <Buildings size={15} weight={settingsActive ? 'fill' : 'regular'} />
              <span>{SEGMENT_LABELS.settings}</span>
              {settingsActive && (
                <span className="ml-auto text-[length:var(--type-caption-size)] text-[var(--color-accent)]" aria-hidden="true">
                  ●
                </span>
              )}
            </DropdownMenuItem>
            {WORKSPACE_TABS.map(({ segment, label, Icon }) => {
              const isActive = segment === activeSegment
              const panelId = PANEL_TOGGLE_SEGMENTS[segment]
              if (panelId) {
                const pressed = activePanelId === panelId
                return (
                  <DropdownMenuItem
                    key={segment}
                    data-testid={`workspace-view-switcher-${segment}`}
                    data-panel-trigger={panelId}
                    aria-pressed={pressed}
                    onClick={() => togglePanel(panelId)}
                    className={cn(
                      'flex items-center gap-[var(--space-2)]',
                      pressed ? 'text-[var(--color-accent)]' : undefined,
                    )}
                  >
                    <Icon size={15} weight={pressed ? 'fill' : 'regular'} />
                    <span>{label}</span>
                    {pressed && (
                      <span
                        className="ml-auto text-[length:var(--type-caption-size)] text-[var(--color-accent)]"
                        aria-hidden="true"
                      >
                        ●
                      </span>
                    )}
                  </DropdownMenuItem>
                )
              }
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
                    'flex items-center gap-[var(--space-2)]',
                    isActive ? 'text-[var(--color-accent)]' : undefined,
                  )}
                >
                  <Icon size={15} weight={isActive ? 'fill' : 'regular'} />
                  <span>{label}</span>
                  {isActive && (
                    <span className="ml-auto text-[length:var(--type-caption-size)] text-[var(--color-accent)]" aria-hidden="true">
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
