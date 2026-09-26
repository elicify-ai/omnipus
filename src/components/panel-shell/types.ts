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
//   }
//
// Wave 0 (this demo) exercises the contract through Storybook stories only —
// no route wiring, no store integration with the real app (those are wave 1).
// Wave 1 refines `expandTarget`'s return to a TanStack Router location; the
// demo returns a URL string because the demo has no router.

import type { ComponentType } from 'react'

/** The registered panel ids (§8.1; §8.2: valid `?panel=` values are the REGISTERED ids). */
export type PanelId = 'library' | 'browser' | 'mail' | 'tasks' | 'team' | 'calendar'

/**
 * What a panel needs to render its content (§8.1: "context carries what the
 * panel needs"). Wave-0 panels use a subset; wave-1 wires the real ids.
 */
export interface PanelContext {
  /** Library / Tasks / Team / Calendar / Mail scope (undefined = virtual root → `app` bucket). */
  workspaceId?: string
  /** Browser scope (MAJ-201: the Browser's identity is a session, not a workspace). */
  sessionId?: string
  agentId?: string
  /** Mail's mailbox context (per the email spec, wave 2). */
  mailboxId?: string
}

/** Props every panel content component receives from the shell. */
export interface PanelContentProps {
  context: PanelContext
  /** Ask the shell to close this panel (runs the leave guard first). */
  close: () => void
  /** Ask the shell to expand this panel to its full-page route (SP-12). */
  expand: () => void
}

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
  content: ComponentType<PanelContentProps>
  /** The full-page route (URL) this panel expands to. */
  expandTarget: (context: PanelContext) => string
  /**
   * CRIT-001: the leave gate for panels with unsaved-edit risk. The shell
   * awaits it BEFORE touching store, URL or content — while the outgoing
   * panel is still mounted. False cancels the transition.
   */
  beforeLeave?: () => Promise<boolean>
}
