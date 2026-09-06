import type { LibraryIconProps } from './types'

/**
 * Vault — a knowledge base (library-b-c-design §"Icon system — LOCKED").
 * Solid folder + an enlarged 4-point spark knockout filling the front panel
 * (the wireframe iteration proved a small spark reads as a smudge at 16px;
 * this one is sized to stay legible there).
 *
 * One <path>, `fillRule="evenodd"`: the folder outline plus the spark as a
 * second closed subpath, so the spark is a TRUE hole — whatever is behind
 * the icon (the row's default/hover/selected background) shows through,
 * rather than a spark hard-filled to a fixed dark colour that would look
 * wrong on any surface but one.
 *
 * Colour is the caller's: render with `color: var(--color-accent)` (gold),
 * per the locked spec.
 */
export function VaultIcon({ size = 16, className }: LibraryIconProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      role="img"
      aria-label="Vault"
      className={className}
    >
      <path
        fill="currentColor"
        fillRule="evenodd"
        d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z
           M12 8.2c.65 3.3 1.5 4.15 4.6 4.8-3.1 .65-3.95 1.5-4.6 4.8-.65-3.3-1.5-4.15-4.6-4.8 3.1-.65 3.95-1.5 4.6-4.8z"
      />
    </svg>
  )
}
