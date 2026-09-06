import type { LibraryIconProps } from './types'

/**
 * Folder — an ordinary directory, no vault, no mount (library-b-c-design
 * §"Icon system — LOCKED"). Outline only, no knockout: the plainest of the
 * four kinds, so it must not compete visually with the filled Vault/Mount
 * icons it sits next to in the same list.
 *
 * Colour is the caller's: render with `color: var(--color-muted)`, per the
 * locked spec.
 */
export function FolderIcon({ size = 16, className }: LibraryIconProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      role="img"
      aria-label="Folder"
      className={className}
    >
      <path
        d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"
        fill="none"
        stroke="currentColor"
        strokeWidth={1.7}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}
