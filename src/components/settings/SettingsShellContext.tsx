// SettingsShellContext — what the Settings shell offers every section's Save
// bar (Settings redesign #1519): the page-level live region (FR-018/FR-071),
// the unsaved-draft count that drives the in-app leave guard (FR-014), the
// "Restart now?" window after a Save that stored a restart-gated value
// (FR-033/FR-034), a refresh of the restart banner's list (FR-035) and the
// one password prompt per Save (FR-026). Outside the shell (a component test
// rendering one Save bar alone) every member is a harmless default, so a Save
// bar works without the shell — it then just does not announce or prompt.

import { createContext, useContext } from 'react'

export interface SettingsShellApi { // not-wire-format: React context contract inside the SPA
  /** Polite page-level announcement (one live region for the whole page). */
  announce: (text: string) => void
  /** Reports how many unsaved drafts one Save bar (owner) holds; the page sums them. */
  setDraftCount: (owner: string, count: number) => void
  /** Opens the "Restart now?" window naming the settings just saved. */
  offerRestart: (labels: string[]) => void
  /** Re-reads GET /config/pending-restart for the banner. */
  refreshPendingRestart: () => void
  /**
   * Asks once for the password (or, in platform mode, a confirmation) for
   * the listed settings. Resolves with the single-use consent token, or
   * null when the person cancels. Undefined outside the shell.
   */
  requestPassword?: (labels: string[]) => Promise<string | null>
  /** True while the Save bar shows (FR-017: Save changes is the only gold button). */
  saveBarVisible: boolean
  setSaveBarVisible: (owner: string, visible: boolean) => void
  /** True only for the default value: no Settings shell around this Save bar. */
  standalone?: boolean
}

const noop = () => {}

export const SettingsShellContext = createContext<SettingsShellApi>({
  announce: noop,
  setDraftCount: noop,
  offerRestart: noop,
  refreshPendingRestart: noop,
  requestPassword: undefined,
  saveBarVisible: false,
  setSaveBarVisible: noop,
  standalone: true,
})

export function useSettingsShell(): SettingsShellApi {
  return useContext(SettingsShellContext)
}
