// Shared prop contract for the Library's locked custom icon set (C3,
// docs/internal/specs/library-b-c-design-2026-09-07.md §"Icon system —
// LOCKED"). Four container kinds — Workspace, Vault, Folder, Mount — on
// Phosphor's 24px grid, deliberately custom because Phosphor has no coherent
// family for this hierarchy (native Phosphor stays everywhere else).
//
// Every icon takes `size` + `className` only, exactly like the Phosphor
// icons they sit next to in LibraryEntryRow/LibraryExplorer — no `color`
// prop. Colour is deliberately left to the caller via `currentColor` (CSS
// `color`, e.g. `style={{ color: 'var(--color-accent)' }}` or a Tailwind
// `text-[var(--color-*)]` class), matching how every other icon in this
// codebase is tinted. Baking the locked palette into the components would
// make WorkspaceIcon/VaultIcon assume a background they don't control (a
// selected row, a hover state, dark vs. the rare lighter surface).
export interface LibraryIconProps {
  /** Pixel size (both width and height). Default 16 — the size this whole
   *  set is locked against (see the spec's "judge at 16px first" rule). */
  size?: number
  className?: string
}
