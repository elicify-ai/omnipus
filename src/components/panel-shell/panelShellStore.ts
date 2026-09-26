// panelShellStore.ts — the shell's single store slice (§8.1): one
// `activePanel` state ("at most one open", SP-7) replacing today's two
// independent slices (`browserPanel` / `libraryPanel` in src/store/ui.ts).
// Wave 0 owns this demo-local slice; wave 1 moves the same shape into the
// real ui store — the shell never reads the two old slices directly.

import { create } from 'zustand'
import type { PanelContext, PanelId } from './types'

/**
 * Sentinel: "no width chosen yet — derive at open". Not a real px value.
 * Declared BEFORE the store: zustand runs the store initializer eagerly at
 * module evaluation, so the sentinel must already be initialized (a TDZ
 * ReferenceError otherwise — caught by the storybook interaction gate).
 */
export const PANEL_WIDTH_UNSET = -1

export interface ActivePanel {
  id: PanelId
  context: PanelContext
}

interface PanelShellStore {
  activePanel: ActivePanel | null
  /**
   * The stored (unsettled-geometry) panel width in px — what the width
   * memory holds for the active panel + scope, or the SP-17 default when
   * nothing is stored. The shell re-derives the APPLIED width at render by
   * clamping this against the current row geometry, so a window-driven
   * re-clamp is transient and never writes back (MAJ-009).
   */
  panelWidth: number
  /** True while a `beforeLeave` guard is deciding (dialog up, awaiting). */
  guardPending: boolean
  /**
   * SP-26 Back affordance: one history entry is on the stack for the open
   * panel (takeover). Shared by the shell hook and `usePanelShellHistory`
   * so close routing, pushes and pops agree on one truth.
   */
  historyPushed: boolean
  openPanel: (id: PanelId, context?: PanelContext) => void
  closePanel: () => void
  setPanelWidth: (px: number) => void
  setGuardPending: (pending: boolean) => void
  setHistoryPushed: (pushed: boolean) => void
}

export const usePanelShellStore = create<PanelShellStore>((set) => ({
  activePanel: null,
  panelWidth: PANEL_WIDTH_UNSET,
  guardPending: false,
  historyPushed: false,
  openPanel: (id, context = {}) =>
    set((state) => ({
      activePanel: { id, context },
      // Opening a DIFFERENT panel re-reads that panel's own width (its
      // stored value for its own scope, or the default). The shell applies
      // this via readPanelWidth at mount/open; the store just resets the
      // transient value so the shell's open-effect re-derives it.
      panelWidth: state.activePanel?.id === id ? state.panelWidth : PANEL_WIDTH_UNSET,
    })),
  closePanel: () =>
    set({ activePanel: null, panelWidth: PANEL_WIDTH_UNSET, guardPending: false, historyPushed: false }),
  setPanelWidth: (px) => set({ panelWidth: px }),
  setGuardPending: (pending) => set({ guardPending: pending }),
  setHistoryPushed: (pushed) => set({ historyPushed: pushed }),
}))
