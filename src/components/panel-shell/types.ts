// types.ts — the side-panel-shell contract shapes (side-panel-shell-spec.md
// §8.1). Shape-level contract, exactly as the spec's PanelDefinition pseudo-
// contract writes it:
//
//   PanelDefinition {
//     id:            'library' | 'browser' | 'mail' | 'tasks' | 'team' | 'calendar'
//     title:         string                          // shell header title
//     content:       React component (receives shell callbacks via props)
//     fullScreen:    context ↔ search codec         // shell-owned route state
//     beforeLeave?:  () => Promise<boolean>         // CRIT-001 transition gate
//     beforeLeaveRequired?: () => boolean            // optional synchronous fast path
//   }
//
import type { ReactNode } from 'react'

/** The registered panel ids (§8.1; §8.2: valid `?panel=` values are the REGISTERED ids). */
export type PanelId = 'library' | 'browser' | 'mail' | 'tasks' | 'team' | 'calendar'

export type PanelScope = 'workspace' | 'session' | 'app'
export type PanelSwitchFollows = 'always' | 'when-scoped' | 'never'

/** One policy table owns scope and workspace-switch behavior for every panel. */
export const PANEL_POLICIES = {
  library: { scope: 'workspace', switchFollows: 'when-scoped' },
  browser: { scope: 'session', switchFollows: 'never' },
  mail: { scope: 'workspace', switchFollows: 'always' },
  tasks: { scope: 'workspace', switchFollows: 'always' },
  team: { scope: 'workspace', switchFollows: 'always' },
  calendar: { scope: 'workspace', switchFollows: 'always' },
} as const satisfies Record<PanelId, { scope: PanelScope; switchFollows: PanelSwitchFollows }>

export type WorkspacePanelId = Exclude<PanelId, 'browser'>

export const WORKSPACE_SCOPED_PANELS = (Object.keys(PANEL_POLICIES) as PanelId[]).filter(
  (id): id is WorkspacePanelId => PANEL_POLICIES[id].scope === 'workspace',
)

export function isWorkspaceScopedPanel(id: PanelId): id is WorkspacePanelId {
  return PANEL_POLICIES[id].scope === 'workspace'
}

/**
 * The ids with a shell REGISTRATION in wave 1 (MAJ-012, §10). The chat route's
 * `validateSearch` accepts exactly these as `?panel=` values; every other
 * PanelId (`mail`, `tasks`, `team`, `calendar`) is unregistered yet and is
 * dropped exactly like an unknown id (US-7 AS-4, §12 dataset row 7). Static
 * values (not derived from `PanelId`) because the schema needs RUNTIME
 * membership; the `satisfies` keeps the list from drifting off the union.
 */
export const WAVE_1_PANEL_IDS = ['library', 'browser'] as const satisfies readonly PanelId[]

/**
 * What a panel needs to render its content (§8.1: "context carries what the
 * panel needs"). Wave-0 panels use a subset; wave-1 wires the real ids.
 */
export interface WorkspacePanelContext {
  /** Library / Tasks / Team / Calendar / Mail scope (undefined = virtual root → `app` bucket). */
  workspaceId?: string
  /** Mail's mailbox context (per the email spec, wave 2). */
  mailboxId?: string
  /** Library's selected work-tree item. */
  path?: string
  /** Library's browsed folder when no item is selected. */
  folder?: string
  sessionId?: never
  agentId?: never
}

export interface BrowserPanelContext {
  /** Browser scope (MAJ-201: the Browser's identity is a session, not a workspace). */
  sessionId: string
  agentId: string
  workspaceId?: never
  mailboxId?: never
  path?: never
  folder?: never
}

export type PanelContext = WorkspacePanelContext | BrowserPanelContext

/** Discriminated open specification: Browser context cannot be omitted. */
export type PanelOpenSpec =
  | { id: 'browser'; context: BrowserPanelContext }
  | { id: WorkspacePanelId; context: WorkspacePanelContext }

export type PanelOpenArgs =
  | [id: 'browser', context: BrowserPanelContext]
  | [id: WorkspacePanelId, context?: WorkspacePanelContext]

export type OpenPanel = (...args: PanelOpenArgs) => void

/** Props every panel content component receives from the shell. */
export interface PanelContentProps {
  context: PanelContext
  /** The shell uses one content component for its docked and full-screen presentations. */
  presentation: 'docked' | 'fullscreen'
  /** Ask the shell to close this panel (runs the leave guard first). */
  close: () => void
  /** Ask the shell to expand this panel through its shared full-screen route (SP-38). */
  expand: () => void
  /**
   * Report the panel's current addressable state. The shell owns popup,
   * presence, handoff, and re-dock behavior for every registered panel.
   */
  registerExpandContext: (getter: (() => PanelContext) | null) => void
  /**
   * Subscribe to settled divider widths. Browser uses the notification to
   * start its existing remote-viewport handover only after resize settles.
   */
  onWidthSettle: (listener: ((px: number) => void) | null) => void
}

/** Registry content receives the complete shell contract. The bivariant
 *  call signature also accepts probes/components that refine an optional
 *  prop without weakening the props the shell supplies at the call site. */
export type PanelContentComponent = {
  bivarianceHack(props: PanelContentProps): ReactNode
}['bivarianceHack']

/** What the store holds for the at-most-one open panel (§8.1, verbatim). */
export type ActivePanel = PanelOpenSpec

/**
 * One panel definition feeds the shell — the §8.1 shape, verbatim. Panels
 * register through a single registry; adding a panel MUST require no shell
 * change beyond a new PanelDefinition entry (SP-4's test).
 */
export interface PanelDefinition {
  id: PanelId
  /** Shell header title. */
  title: string
  /** The panel's content, rendered inside the shell below the header. */
  content: PanelContentComponent
  /** Converts panel state to/from the shared `#/panel/$panelId` route. */
  fullScreen: {
    toSearch: (context: PanelContext) => Record<string, string>
    fromSearch: (search: Record<string, unknown>) => PanelContext | null
  }
  /**
   * CRIT-001: the leave gate for panels with unsaved-edit risk. The shell
   * awaits it BEFORE touching store, URL or content — while the outgoing
   * panel is still mounted. False cancels the transition.
   */
  beforeLeave?: () => Promise<boolean>
  /**
   * Optional synchronous fast-path predicate for external transition
   * triggers. False means no leave decision is needed and the transition
   * may preserve its same-tick behavior; absent means the guard must run.
   */
  beforeLeaveRequired?: () => boolean
}
