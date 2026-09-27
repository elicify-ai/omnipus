// panelWidth.ts — the SP-17 width geometry (as amended by SP-25) for the
// shared side-panel shell (side-panel-shell-spec.md §8.1).
//
// All measurements are taken on the shell's own flex row — MIN-001's basis:
// `row` is the width of the row the shell's [chat | separator | panel] split
// lives in, `sidebar` is the pinned sidebar's current width (0 when it is not
// in the row). Pure functions only: the component re-derives the applied
// width every render from (stored width, geometry) so a window-driven
// re-clamp is TRANSIENT by construction — the stored value is never touched
// by geometry changes (MAJ-009, US-3 AS-6: the panel returns to its stored
// width when the window comes back).
//
// Constants from the spec (§5, §10 SP-17):
//   - CHAT_FLOOR   360px — the chat column never drops below this ≥680px
//   - PANEL_MIN    320px — the panel never narrows below this
//   - TAKEOVER     680px — below this the panel takes over the full row
//                          (SP-25: overlay mode deleted; US-8)
//   - ceiling      min(70% of row, row − sidebar − 360px)
//   - default      clamp(0.45 × row, 320px, min(720px, ceiling))  (MAJ-203:
//                  an uncapped 45% broke the chat's 360px floor at 1024px
//                  with the sidebar pinned)

export const CHAT_FLOOR_PX = 360
export const PANEL_MIN_PX = 320
export const PANEL_TAKEOVER_PX = 680

/** The SP-17 maximum width: `min(70% of row, row − sidebar − 360px)`. */
export function panelWidthCeiling(rowWidth: number, sidebarWidth: number): number {
  return Math.min(rowWidth * 0.7, rowWidth - sidebarWidth - CHAT_FLOOR_PX)
}

/** The default width: `clamp(0.45 × row, 320px, min(720px, ceiling))`. */
export function panelDefaultWidth(rowWidth: number, sidebarWidth: number): number {
  const ceiling = panelWidthCeiling(rowWidth, sidebarWidth)
  return Math.max(PANEL_MIN_PX, Math.min(720, ceiling, Math.max(0.45 * rowWidth, PANEL_MIN_PX)))
}

/** Clamp any candidate width into [320, ceiling] for the given geometry. */
export function clampPanelWidth(width: number, rowWidth: number, sidebarWidth: number): number {
  const ceiling = panelWidthCeiling(rowWidth, sidebarWidth)
  // max() guard: at tiny row widths the ceiling can compute below 320 — the
  // floors are only guaranteed to fit at ≥680px (§5 boundary conditions), and
  // the takeover layout replaces the docked layout below 680 anyway.
  return Math.max(PANEL_MIN_PX, Math.min(width, ceiling))
}

/**
 * The row width below which the shell renders the SP-25 phone takeover
 * instead of the docked split. Measured on the shell's own row — not the
 * window — so the same code serves the real app (row = viewport minus
 * sidebar) and the Storybook demo (row = the story stage width).
 */
export function isPhoneTakeover(rowWidth: number): boolean {
  return rowWidth < PANEL_TAKEOVER_PX
}
