import type { SVGProps } from 'react'
import type { LibraryIconProps } from './types'

/**
 * Mounted folder — a real folder on the operator's machine, granted into a
 * workspace. Founder-ratified pivot (icon-consistency, 2026-09-07): a plain
 * dashed-outline folder (FolderSimpleDashed) read as placeholder/disabled,
 * and the dash pattern turned mushy at 16px. This renders the ordinary
 * FolderSimple silhouette instead — it must still read as "a folder that
 * holds files" — with a small corner arrow badge marking it as linked, the
 * macOS alias convention. The row's own "Mounted"/"Broad grant" chip and
 * host path carry the rest of the meaning; the badge only has to say "not a
 * plain folder", not the whole story (it is `aria-hidden`; the accessible
 * name below is what actually identifies the icon).
 *
 * A single shared component, deliberately: every surface that shows a mount
 * (LibraryEntryRow, LibraryMountsDialog, LibraryCreateMenu) renders the
 * identical composition rather than each inventing its own mark, which is
 * the whole reason the previous per-surface icon vocabularies were retired.
 *
 * Composed from Phosphor's own path data, not hand-drawn: the folder path is
 * FolderSimple's "regular"-weight glyph and the badge is ArrowUpRight's
 * "bold"-weight glyph, both copied verbatim from
 * @phosphor-icons/react/dist/defs and the badge scaled/translated into the
 * bottom-left corner via one <g transform>. One <svg> (not two nested icon
 * components) so it sizes via a single `size` prop exactly like every other
 * icon in this codebase, with no absolute-positioning wrapper to drift out
 * of alignment at small sizes.
 */
export function MountFolderIcon({
  size = 16,
  className,
  ...rest
}: LibraryIconProps & Omit<SVGProps<SVGSVGElement>, 'width' | 'height' | 'viewBox' | 'className'>) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 256 256"
      role="img"
      aria-label="Mounted folder"
      className={className}
      {...rest}
    >
      {/* FolderSimple, regular weight (@phosphor-icons/react dist/defs/FolderSimple.es.js) */}
      <path
        fill="currentColor"
        d="M216,72H130.67L102.93,51.2a16.12,16.12,0,0,0-9.6-3.2H40A16,16,0,0,0,24,64V200a16,16,0,0,0,16,16H216.89A15.13,15.13,0,0,0,232,200.89V88A16,16,0,0,0,216,72Zm0,128H40V64H93.33L123.2,86.4A8,8,0,0,0,128,88h88Z"
      />
      {/* ArrowUpRight, bold weight (@phosphor-icons/react dist/defs/ArrowUpRight.es.js),
          scaled ~0.62x and moved into the bottom-left corner — the "linked
          elsewhere" alias badge. */}
      <g transform="translate(-23.48,121.38) scale(0.62)" aria-hidden="true">
        <path
          fill="currentColor"
          d="M204,64V168a12,12,0,0,1-24,0V93L72.49,200.49a12,12,0,0,1-17-17L163,76H88a12,12,0,0,1,0-24H192A12,12,0,0,1,204,64Z"
        />
      </g>
    </svg>
  )
}
