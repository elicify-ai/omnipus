// Shared prop contract for the Library's icon set (icon-consistency,
// 2026-09-07): Knowledge Base / Workspace / Folder are plain Phosphor
// components now (Books / Buildings / FolderSimple) with no wrapper of
// their own. MountFolderIcon is the ONE deliberate exception — a mounted
// folder needs a small composed corner badge Phosphor has no primitive for
// — and this contract exists so that one exception still takes `size` +
// `className` exactly like every Phosphor icon it sits next to.
//
// No `color` prop: colour is deliberately left to the caller via
// `currentColor` (CSS `color`, e.g. `style={{ color: 'var(--color-accent)' }}`
// or a Tailwind `text-[var(--color-*)]` class), matching how every other
// icon in this codebase is tinted.
export interface LibraryIconProps {
  /** Pixel size (both width and height). Default 16. */
  size?: number
  className?: string
}
