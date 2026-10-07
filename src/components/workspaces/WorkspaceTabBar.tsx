import { useLocation, useNavigate } from '@tanstack/react-router'
import { motion } from 'framer-motion'
import {
  CaretDown,
  SquaresFour,
  CalendarBlank,
  UsersThree,
  Files,
  Tray,
  Buildings,
} from '@phosphor-icons/react'
import type { Icon } from '@phosphor-icons/react'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Button } from '@/components/ui/button'
import { Tooltip } from '@/components/ui/tooltip'
import { useWorkspaceHeaderMode } from './useWorkspaceHeaderMode'
import { useUiStore } from '@/store/ui'
import { leaveGateThen } from '@/components/panel-shell/leaveGate'
import type { WorkspacePanelId } from '@/components/panel-shell/types'
import { cn } from '@/lib/utils'

// The workspace container surface — the strip (US-5, SP-11). Wave 3
// (side-panel-shell-spec.md §10, SP-32..SP-42 amendments): EVERY entry is a
// REGISTERED side-panel toggle button with aria-pressed — mixed mode is
// ended (MAJ-012's links are gone).
//
// Entry kinds:
//   - workspace name → settings button (chrome, not a view; aria-current
//     when the settings route is the page)
//   - Tasks ('board' segment), Calendar, Library ('media' segment), Team,
//     Mail ('mail' segment) → REGISTERED panel toggles: aria-pressed,
//     opens/closes the panel scoped to this workspace via the leave gate —
//     NO navigation (US-5 AS-1/AS-2). Chat is NOT an entry (SP-40): chat is
//     the base route the shell sits over — the page underneath — and closing
//     whichever panel is open reveals it. Chat is never rendered as a strip
//     or dropdown entry; SP-40 removes ONLY Chat — Mail (registered in wave
//     2) keeps its strip/dropdown entry, exactly as on the release line.
//
// The routes behind the segments (workspaces.$workspaceId.board/calendar/
// media/team) are deep-link targets only: they redirect to
// chat?panel=<id> (§8.2), which owns the actual panel open.
//
// ADR-051 D1 — "Tasks" screen: Board/List/Graph collapse into ONE screen
// (WorkspaceTasksTab, rendered under the `board` route segment — kept
// unchanged to avoid breaking deep links) with an in-screen view switcher.
// The `list` and `graph` top-level tabs are retired; their route files now
// redirect to `board` (see workspaces.$workspaceId.list.tsx / .graph.tsx).
export const WORKSPACE_TABS = [
  { segment: 'board', label: 'Tasks', Icon: SquaresFour },
  { segment: 'calendar', label: 'Calendar', Icon: CalendarBlank },
  // Renamed Media -> Library (library-spec.md supersedes the old workspace
  // Media tab / UUID-blob manifest surface entirely). This strip entry is a
  // REGISTERED panel toggle (see PANEL_TOGGLE_SEGMENTS) — clicking it
  // opens/closes the Library side panel scoped to this workspace through
  // the shared panel-store/leave-gate path. The route itself
  // (routes/_app/workspaces.$workspaceId.media.tsx) remains a redirect stub
  // for BOOKMARKED /workspaces/{id}/media URLs: it opens the Library panel
  // and replaces the URL with chat?panel=library (§8.2), so an old link
  // never dead-ends on a page with no content of its own.
  { segment: 'media', label: 'Library', Icon: Files },
  // Mail (email-mail-view-spec.md US-3): the workspace Mail panel, registered
  // in wave 2 — its release-line strip position (between Library and Team)
  // is preserved. SP-40 removed Chat from the strip; Mail was never named by
  // SP-40 and stays, matching the release line's entry byte-for-byte
  // (segment 'mail', label 'Mail', Tray icon). Clicking toggles the docked
  // Mail panel scoped to this workspace; no mailbox context opens the
  // shell's "choose a mailbox" state (SP-23).
  { segment: 'mail', label: 'Mail', Icon: Tray },
  { segment: 'team', label: 'Team', Icon: UsersThree },
  // NOTE: workspace settings is deliberately NOT a tab — settings is chrome,
  // not a view. It's reached by clicking the workspace NAME in the top bar
  // (WorkspaceTabContainer) or the compact dropdown's settings entry,
  // Notion-style. The /settings route still exists.
] as const

/** Registered-panel strip entries: strip segment → panel id. Since wave 3
 * (SP-6) every strip segment maps to a registered panel — Tasks/Calendar/
 * Team joined Library and Mail (wave 2) keeps its mapping, ending MAJ-012's
 * mixed mode; their route files stay as the deep-link targets only (§10). */
const PANEL_TOGGLE_SEGMENTS: Partial<Record<TabSegment, WorkspacePanelId>> = {
  board: 'tasks',
  calendar: 'calendar',
  media: 'library',
  team: 'team',
  mail: 'mail',
}

/** Every real WORKSPACE_TABS segment — derived from the array itself (not a
 * hand-maintained union), so adding/renaming/removing a tab there can never
 * silently drift out of sync with this type. */
export type TabSegment = (typeof WORKSPACE_TABS)[number]['segment']
/** Segment values resolveActiveSegment can report. 'chat' is deliberately
 * NOT a WORKSPACE_TABS entry (SP-40: chat is the base page underneath, never
 * a strip/dropdown entry) but remains a real segment — it is the panel-host
 * page and the redirect target of every panel deep link. 'settings' is
 * reached via the workspace-name button / compact dropdown's settings entry. */
export type WorkspaceSegment = TabSegment | 'chat' | 'settings'

export interface WorkspaceTab {
  segment: TabSegment
  label: string
  Icon: Icon
}

interface WorkspaceTabBarProps {
  workspaceId: string
  /** Workspace display name — rendered as the FIRST strip entry (→ settings). */
  workspaceName: string
}

/**
 * Workspace tab bar — Sovereign Deep, Outfit labels, gold active underline
 * on the workspace-name entry.
 *
 * Wave 3 (SP-40/SP-11): every strip entry is a panel toggle (aria-pressed +
 * accent colour when its panel is open — never the underline, which would
 * make Framer's shared-layout animation fight the pressed state). Chat is
 * NOT an entry: chat is the base page underneath; closing the open panel
 * reveals it. There is no page-semantic route entry left in the strip, so
 * the only aria-current carrier is the workspace-name → settings entry.
 *
 * R44 responsive strategy: measure the available flex slot and the natural
 * full/icons strip widths. Prefer name + icon/label toggles, then name +
 * icon-only toggles with kit tooltips. When neither fits, the workspace name
 * itself opens the kit menu (Settings + all five toggles). The only hamburger
 * is the sidebar launcher. Off-layout probes avoid mode-dependent oscillation.
 *
 * Strip and menu item test ids and panel accessible names remain unchanged.
 *
 * Sits inline inside the WorkspaceTabContainer top-bar row (Row 1). The parent
 * row owns the background (no border — flat shell alignment); this component
 * only renders the entry strip.
 */
export function WorkspaceTabBar({ workspaceId, workspaceName }: WorkspaceTabBarProps) {
  const navigate = useNavigate()
  const settingsActive =
    resolveActiveSegment(useLocation().pathname, workspaceId) === 'settings'
  const activePanelId = useUiStore((s) => s.activePanel?.id ?? null)
  const { mode, availableRef, fullRef, iconsRef } = useWorkspaceHeaderMode()

  /** Every toggle goes through the outgoing panel's leave gate; changing
   * header mode must never change the scoped open/close action. */
  const togglePanel = (panelId: WorkspacePanelId) => {
    const outgoingPanelId = useUiStore.getState().activePanel?.id ?? null
    leaveGateThen(outgoingPanelId, () => {
      const state = useUiStore.getState()
      if (state.activePanel?.id === panelId) state.closePanel()
      else state.openPanel(panelId, workspaceId ? { workspaceId } : {})
    })
  }
  const openSettings = () => navigate({
    to: '/workspaces/$workspaceId/settings', params: { workspaceId },
  })

  // Shared JSX builders keep the natural probes byte-for-byte styled like
  // the real strip. Probes are inert, off-layout and carry no trigger/test ids.
  const nameButton = (menu: boolean, measuring = false) => (
    <Button
      variant="ghost"
      onClick={menu || measuring ? undefined : openSettings}
      title={menu ? 'Open panels menu' : 'Workspace settings'}
      aria-label={menu ? 'Open panels menu' : `${workspaceName} — workspace settings`}
      aria-current={settingsActive ? 'page' : undefined}
      data-testid={measuring ? undefined : 'workspace-name-button'}
      tabIndex={measuring ? -1 : 0}
      className={cn(
        'relative h-chrome-header min-h-chrome-header min-w-0 max-w-[24ch] shrink-0 justify-start gap-[var(--space-1)] rounded-t-sm rounded-b-none px-[var(--space-2-5)] py-0 font-headline whitespace-nowrap outline-none hover:bg-transparent',
        menu && 'max-w-full shrink',
        settingsActive
          ? 'text-[var(--color-accent)]'
          : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
      )}
    >
      <Buildings size={16} weight={settingsActive ? 'fill' : 'regular'} className="shrink-0" aria-hidden="true" />
      <span className="truncate">{workspaceName}</span>
      {menu && <CaretDown size={16} className="shrink-0" aria-hidden="true" />}
      {settingsActive && !measuring && (
        <motion.div
          layoutId="workspace-tab-underline"
          className="absolute inset-x-1 -bottom-px h-0.5 rounded-full bg-[var(--color-accent)]"
          transition={{ type: 'spring', stiffness: 500, damping: 32 }}
        />
      )}
    </Button>
  )

  const stripEntries = (iconsOnly: boolean, measuring = false) => (
    <>
      {(measuring || mode !== 'narrow') && nameButton(false, measuring)}
      {WORKSPACE_TABS.map(({ segment, label, Icon }) => {
        const panelId = PANEL_TOGGLE_SEGMENTS[segment]
        if (!panelId) return null
        const pressed = activePanelId === panelId
        const button = (
          <Button
            key={segment}
            variant="ghost"
            onClick={measuring ? undefined : () => togglePanel(panelId)}
            title={iconsOnly ? undefined : label}
            aria-label={label}
            aria-pressed={pressed}
            tabIndex={measuring ? -1 : 0}
            data-panel-trigger={measuring ? undefined : panelId}
            data-testid={measuring ? undefined : `workspace-tab-${segment}`}
            className={cn(
              'group relative flex shrink-0 items-center gap-[var(--space-1)] px-[var(--space-2-5)] h-chrome-header min-h-chrome-header text-[length:var(--type-body-compact-size)] font-headline whitespace-nowrap outline-none transition-colors rounded-t-sm',
              pressed
                ? 'text-[var(--color-accent)]'
                : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
            )}
          >
            <Icon size={16} weight={pressed ? 'fill' : 'regular'} aria-hidden="true" />
            {!iconsOnly && <span>{label}</span>}
          </Button>
        )
        return iconsOnly && !measuring ? (
          <Tooltip key={segment} content={label} side="bottom" interactive>{button}</Tooltip>
        ) : button
      })}
    </>
  )

  return (
    <div
      ref={availableRef}
      data-testid="workspace-header-entries"
      data-mode={mode}
      className="relative flex min-w-0 flex-1 items-stretch"
    >
      {/* Clip only the invisible probes, never the real strip's tooltips.
          Both natural widths exist independently of the selected mode. */}
      <div aria-hidden="true" inert className="pointer-events-none absolute inset-0 overflow-hidden invisible">
        <div ref={fullRef} data-workspace-header-measure="full" className="flex w-max items-stretch gap-[var(--space-1)]">
          {stripEntries(false, true)}
        </div>
        <div ref={iconsRef} data-workspace-header-measure="icons" className="flex w-max items-stretch gap-[var(--space-1)]">
          {stripEntries(true, true)}
        </div>
      </div>

      {/* Panel toggles are buttons, not page tabs. Retain their ids/state
          while hidden so the compact menu and strip share the same model. */}
      <div
        data-testid="workspace-tab-strip"
        hidden={mode === 'narrow'}
        className={cn('w-max shrink-0 items-stretch gap-[var(--space-1)]', mode === 'narrow' ? 'hidden' : 'flex')}
      >
        {stripEntries(mode === 'icons')}
      </div>

      <div hidden={mode !== 'narrow'} className={cn('min-w-0 max-w-full items-stretch', mode === 'narrow' ? 'flex' : 'hidden')}>
        <DropdownMenu>
          {mode === 'narrow' && <DropdownMenuTrigger asChild>{nameButton(true)}</DropdownMenuTrigger>}
          <DropdownMenuContent align="start" className="w-44">
            <DropdownMenuItem
              data-testid="workspace-view-switcher-settings"
              aria-current={settingsActive ? 'page' : undefined}
              onClick={() => { void openSettings() }}
              className={cn('flex items-center gap-[var(--space-2)]', settingsActive && 'text-[var(--color-accent)]')}
            >
              <Buildings size={15} weight={settingsActive ? 'fill' : 'regular'} aria-hidden="true" />
              <span>Settings</span>
              {settingsActive && (
                <span className="ml-auto text-[length:var(--type-caption-size)] text-[var(--color-accent)]" aria-hidden="true">●</span>
              )}
            </DropdownMenuItem>
            {WORKSPACE_TABS.map(({ segment, label, Icon }) => {
              const panelId = PANEL_TOGGLE_SEGMENTS[segment]
              if (!panelId) return null
              const pressed = activePanelId === panelId
              return (
                <DropdownMenuItem
                  key={segment}
                  data-testid={`workspace-view-switcher-${segment}`}
                  data-panel-trigger={panelId}
                  aria-pressed={pressed}
                  onClick={() => togglePanel(panelId)}
                  className={cn('flex items-center gap-[var(--space-2)]', pressed && 'text-[var(--color-accent)]')}
                >
                  <Icon size={15} weight={pressed ? 'fill' : 'regular'} aria-hidden="true" />
                  <span>{label}</span>
                  {pressed && (
                    <span className="ml-auto text-[length:var(--type-caption-size)] text-[var(--color-accent)]" aria-hidden="true">●</span>
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
 * Derive the active segment from a pathname. 'chat' is NOT a WORKSPACE_TABS
 * entry (SP-40 — chat is the base page underneath, never a strip entry) but
 * remains the default: the bare container path, an unknown segment and the
 * chat route itself all resolve to it. The chat-only header controls
 * (WorkspaceTabContainer) and ChatControls gating key off this return value.
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
  // through to 'chat', wrongly rendering the chat-only header controls on the
  // settings page.
  if (segment === 'settings') return 'settings'
  const match = WORKSPACE_TABS.find((t) => t.segment === segment)
  return match?.segment ?? 'chat'
}
