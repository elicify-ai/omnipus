import type { LibraryIconProps } from './types'

/**
 * Workspace — the container ABOVE vault/folder/mount (library-b-c-design
 * §"Icon system — LOCKED"). Deliberately a rounded TILE, not a folder: a
 * workspace is not "a kind of folder", and giving it a folder base would
 * make gold-tile-workspace and gold-folder-vault blur together at a glance,
 * which is exactly the ambiguity colour + shape together are meant to kill.
 *
 * Solid fill + a bold 2×2 knockout grid — large and thick on purpose,
 * because the wireframe iteration proved a fine inner symbol vanishes at
 * 16px. One <path> with `fillRule="evenodd"`: the outer tile plus four
 * knockout squares as separate closed subpaths, so the squares render as
 * TRUE holes (the surface behind the icon shows through) rather than a
 * second shape painted a fixed background colour — the row's own
 * background (default / hover / selected) always shows through correctly.
 *
 * Colour is the caller's: render with `color: var(--color-accent)` (gold),
 * per the locked spec — this component only supplies `currentColor`.
 */
export function WorkspaceIcon({ size = 16, className }: LibraryIconProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      role="img"
      aria-label="Workspace"
      className={className}
    >
      <path
        fill="currentColor"
        fillRule="evenodd"
        d="M7,3 H17 A4,4 0 0 1 21,7 V17 A4,4 0 0 1 17,21 H7 A4,4 0 0 1 3,17 V7 A4,4 0 0 1 7,3 Z
           M7.2,6 H10 A1.2,1.2 0 0 1 11.2,7.2 V10 A1.2,1.2 0 0 1 10,11.2 H7.2 A1.2,1.2 0 0 1 6,10 V7.2 A1.2,1.2 0 0 1 7.2,6 Z
           M14,6 H16.8 A1.2,1.2 0 0 1 18,7.2 V10 A1.2,1.2 0 0 1 16.8,11.2 H14 A1.2,1.2 0 0 1 12.8,10 V7.2 A1.2,1.2 0 0 1 14,6 Z
           M7.2,12.8 H10 A1.2,1.2 0 0 1 11.2,14 V16.8 A1.2,1.2 0 0 1 10,18 H7.2 A1.2,1.2 0 0 1 6,16.8 V14 A1.2,1.2 0 0 1 7.2,12.8 Z
           M14,12.8 H16.8 A1.2,1.2 0 0 1 18,14 V16.8 A1.2,1.2 0 0 1 16.8,18 H14 A1.2,1.2 0 0 1 12.8,16.8 V14 A1.2,1.2 0 0 1 14,12.8 Z"
      />
    </svg>
  )
}
