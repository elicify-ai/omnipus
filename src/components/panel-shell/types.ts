// types.ts — the side-panel-shell contract shapes (side-panel-shell-spec.md
// §8.1). Shape-level contract, exactly as the spec's PanelDefinition pseudo-
// contract writes it:
//
//   PanelDefinition {
//     id:            'library' | 'browser' | 'mail' | 'tasks' | 'team' | 'calendar'
//     title:         string                          // shell header title
//     content:       React component (receives close/expand callbacks via props)
//     expandTarget:  (context) => route location    // full-page route + params
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
 * The ids with a shell REGISTRATION at wave 2 (MAJ-012, §10: wave 1 shipped
 * library + browser; wave 2 "Mail adopts the shell" adds mail — FR-014). The
 * chat route's `validateSearch` accepts exactly these as `?panel=` values,
 * and usePanelDeepLink adopts/projects exactly these; every other PanelId
 * (`tasks`, `team`, `calendar`) is unregistered yet and is dropped exactly
 * like an unknown id (US-7 AS-4, §12 dataset row 7 — wave 3). Static values
 * (not derived from `PanelId`) because the schema needs RUNTIME membership;
 * the `satisfies` keeps the list from drifting off the union.
 */
export const WAVE_2_PANEL_IDS = ['library', 'browser', 'mail'] as const satisfies readonly PanelId[]

/**
 * What a panel needs to render its content (§8.1: "context carries what the
 * panel needs"). Wave-0 panels use a subset; wave-1 wires the real ids.
 */
export interface WorkspacePanelContext {
  /** Library / Tasks / Team / Calendar / Mail scope (undefined = virtual root → `app` bucket). */
  workspaceId?: string
  /**
   * Mail's mailbox context (§8.1: "mailbox context per the email spec";
   * SP-23). Three states, because the deep link's agent param is a DIRECTIVE:
   *   - string     — land on that mailbox (`?panel=mail&agent=…`)
   *   - null       — EXPLICIT choose-a-mailbox: the link named `panel=mail`
   *                  with no agent, so the picker faces the user and Mail
   *                  starts nothing costly (§8.2 Mail bullet, §12 row 8)
   *   - undefined  — no mailbox directive (tab-strip toggle, plain opens):
   *                  the panel keeps its own default-selection posture
   *                  (US-3: a single configured mailbox is opened live).
   * Folder/message focus never travels here — it rides the per-workspace
   * sessionStorage intent (src/components/workspaces/mail/mailPanelIntent.ts,
   * MC-31a) because a shareable chat-link must not pin a message ref (§8.2:
   * no foreign keys survive on the chat URL).
   */
  mailboxId?: string | null
  sessionId?: never
  agentId?: never
}

export interface BrowserPanelContext {
  /** Browser scope (MAJ-201: the Browser's identity is a session, not a workspace). */
  sessionId: string
  agentId: string
  workspaceId?: never
  mailboxId?: never
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
  /** Ask the shell to close this panel (runs the leave guard first). */
  close: () => void
  /** Ask the shell to expand this panel to its full-page route (SP-12). */
  expand: () => void
  /**
   * Register the panel-specific Expand implementation. Library uses this to
   * carry its current selection; Browser uses it for its ownership handoff.
   * Returning false means the popup was blocked and the shell must stay open.
   */
  registerExpand: (action: (() => boolean) | null) => void
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
  /** The full-page route (URL) this panel expands to. */
  expandTarget: (context: PanelContext) => string
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
