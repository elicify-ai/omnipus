import type { LibraryIconProps } from './types'

/**
 * Mount — a real folder on the operator's own machine, granted into a
 * workspace (library-b-c-design §"Icon system — LOCKED"). Solid folder + an
 * external "↗" knockout.
 *
 * DELIBERATE DEVIATION from the reference file's exact arrow geometry
 * (library-bc-design.html's `#mount` symbol draws the ↗ as two open,
 * stroked line segments — a right-angle corner plus a diagonal). Knockouts
 * in this set must be carved from ONE filled `<path>` via
 * `fillRule="evenodd"` so they read as true holes on any row background
 * (see the module doc on VaultIcon/WorkspaceIcon) — that requires a CLOSED
 * shape, not an open stroke, and a thin two-segment corner-plus-diagonal
 * closes into slivers that read as noise at 16px (the spec's own #1 rule:
 * "every icon is judged at 16px first"). This draws the same "external ↗"
 * concept as one bold, closed arrow polygon (a wide tip tapering through a
 * notch into a narrower shaft) instead — a shape proven to hold up at icon
 * sizes (the standard filled "arrow cursor" silhouette), at the cost of not
 * being pixel-identical to the reference stroke art. If the founder wants
 * literal fidelity to the two-stroke glyph instead, this is the one knowing
 * departure to revisit.
 *
 * Colour is the caller's: render with `color: var(--color-mount)`
 * (`#8ea3bd`, new token — see globals.css), per the locked spec.
 */
export function MountIcon({ size = 16, className }: LibraryIconProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      role="img"
      aria-label="Mount"
      className={className}
    >
      <path
        fill="currentColor"
        fillRule="evenodd"
        d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z
           M15,9.8 L14.8,12.6 L14.2,12 L10.6,16 L9.4,14.8 L12.9,10.9 L12.3,10.3 Z"
      />
    </svg>
  )
}
