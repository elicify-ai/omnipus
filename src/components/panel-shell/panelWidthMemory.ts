// panelWidthMemory.ts — SP-13/SP-20 width memory: the remembered panel width
// is keyed per USER × panel × workspace bucket, stored in localStorage
// (browser-local, US-3), one key per combination (MIN-204):
//
//   panel-width:<username>:<panelId>:<workspaceId|app>
//
// Write rules (MAJ-009 + MIN-205): the stored value is written ONLY on a
// settle event — drag release, keyboard settle (300ms after the last
// keypress), or reset. Reset DELETES the key: the default is re-derived at
// read time, never stored (a stored default in px would freeze it to one
// window size, US-3 AS-4). A window-driven re-clamp never writes: the
// applied width is re-derived from (stored, geometry) at render, so the
// stored value is untouched by window size (MAJ-009, US-3 AS-6).
//
// Scope table (§8.1's identity-key table, MAJ-201):
//   Library / Tasks / Team / Calendar → workspaceId ?? 'app'
//   Browser                           → 'app' ALWAYS (a session, not a
//                                        workspace — MAJ-201)
//   Mail                              → workspaceId ?? 'app' (mailbox does
//                                        not change the width bucket)

import { PANEL_POLICIES } from './types'
import type { BrowserPanelContext, PanelContext, PanelId } from './types'
import { PANEL_MIN_PX } from './panelWidth'

const PREFIX = 'panel-width'

/** localStorage key: `panel-width:<username>:<panelId>:<workspaceId|app>`. */
export function panelWidthKey(username: string, panelId: PanelId, scope: string): string {
  return `${PREFIX}:${username}:${panelId}:${scope || 'app'}`
}

/**
 * The width bucket for one panel in one context — the §8.1 identity-key
 * table. Browser is ALWAYS `app` (MAJ-201): its identity is a browser
 * session, not a workspace, so it has ONE width per user regardless of
 * which session or screen it was opened from.
 */
type LegacyBrowserWidthContext = Omit<BrowserPanelContext, 'workspaceId'> & {
  workspaceId?: string
}

type PanelWidthContext = PanelContext | LegacyBrowserWidthContext

export function panelWidthScope(panelId: PanelId, context: PanelWidthContext): string {
  if (PANEL_POLICIES[panelId].scope !== 'workspace') return 'app'
  return context.workspaceId ?? 'app'
}

/** Read the stored width for user × panel × scope, or null when none. */
export function readPanelWidth(
  username: string,
  panelId: PanelId,
  context: PanelContext,
): number | null {
  const key = panelWidthKey(username, panelId, panelWidthScope(panelId, context))
  let raw: string | null
  try {
    raw = window.localStorage.getItem(key)
  } catch {
    return null
  }
  if (raw === null) return null
  const parsed = Number(raw)
  if (!Number.isFinite(parsed) || parsed < PANEL_MIN_PX) return null
  return parsed
}

/** Write a settled width. Returns false when localStorage refused the write. */
export function writePanelWidth(
  username: string,
  panelId: PanelId,
  context: PanelContext,
  width: number,
): boolean {
  const key = panelWidthKey(username, panelId, panelWidthScope(panelId, context))
  try {
    window.localStorage.setItem(key, String(Math.round(width)))
    return true
  } catch {
    return false
  }
}

/**
 * Delete the stored width (a double-click reset DELETES; the default is
 * re-derived at read time — US-3 AS-4).
 */
export function deletePanelWidth(
  username: string,
  panelId: PanelId,
  context: PanelContext,
): void {
  const key = panelWidthKey(username, panelId, panelWidthScope(panelId, context))
  try {
    window.localStorage.removeItem(key)
  } catch {
    // Storage is optional; reset still succeeds for the in-memory store.
  }
}

/**
 * Optional maintenance seam (MIN-204): prune this user's `panel-width:<user>:*` entries
 * whose workspace no longer exists. Only the signed-in user's own entries
 * are ever touched — the key prefix guarantees it. Callers supply the
 * authoritative workspace list when they own a lifecycle event.
 */
export function prunePanelWidths(
  username: string,
  isKnownWorkspace: (workspaceId: string) => boolean,
): number {
  try {
    const prefix = `${PREFIX}:${username}:`
    const doomed: string[] = []
    for (let i = 0; i < window.localStorage.length; i++) {
      const key = window.localStorage.key(i)
      if (key === null || !key.startsWith(prefix)) continue
      const scope = key.slice(prefix.length).split(':')[1]
      if (scope && scope !== 'app' && !isKnownWorkspace(scope)) doomed.push(key)
    }
    for (const key of doomed) window.localStorage.removeItem(key)
    return doomed.length
  } catch {
    return 0
  }
}
