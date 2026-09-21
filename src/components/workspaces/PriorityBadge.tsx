import { cn } from '@/lib/utils'

/**
 * Priority (P1-P5) pill colour — literal per-branch `className`, not a
 * dynamic string built from a data record.
 *
 * TaskCard.tsx used to keep a `PRIORITY_BADGE` record mapping priority ->
 * `className` (a raw Tailwind palette string), spread into JSX at three call
 * sites (TaskCard, ListView, CreateTaskSlideOver). The design-system static
 * scanners (`scripts/design-system-locks/{typography,spacing,ts-colors}.mjs`)
 * cannot resolve a class list assembled at runtime from data, so that pattern
 * produced "unsupported" findings that can never be baselined. A literal
 * `className` string per `switch` branch is what the scanners can read.
 *
 * Colours are the `--color-priority-N` / `--color-priority-N-background`
 * tokens (`design-system/tokens/colors.json`) — converted, byte-for-byte,
 * from this Tailwind v4 install's own `red-400`/`red-500`,
 * `orange-400`/`orange-500`, `yellow-400`/`yellow-500`, `blue-400`/`blue-500`
 * (`node_modules/tailwindcss/theme.css`), at the same 20% background alpha
 * (`bg-*-500/20`) the raw classes used — same rendered pixels, now tokens.
 * P5 reuses the existing `--color-muted` primitive for its foreground (it
 * was already `var(--color-muted)` before this change) plus one new 20%
 * background primitive, since no existing muted-grey primitive was tinted at
 * 20% (the closest, `grey-tint`, is 10%).
 */
export const PRIORITY_BADGE: Record<number, { label: string }> = {
  1: { label: 'P1' },
  2: { label: 'P2' },
  3: { label: 'P3' },
  4: { label: 'P4' },
  5: { label: 'P5' },
}

interface PriorityBadgeProps {
  priority: number
  /** Merged with (after) the token-driven colour classes via `cn` — size, margin, border, font stay owned by each call site. */
  className?: string
}

export function PriorityBadge({ priority, className }: PriorityBadgeProps) {
  switch (priority) {
    case 1:
      return (
        <span className={cn('bg-[var(--color-priority-1-background)] text-[var(--color-priority-1)]', className)}>
          {PRIORITY_BADGE[1].label}
        </span>
      )
    case 2:
      return (
        <span className={cn('bg-[var(--color-priority-2-background)] text-[var(--color-priority-2)]', className)}>
          {PRIORITY_BADGE[2].label}
        </span>
      )
    case 4:
      return (
        <span className={cn('bg-[var(--color-priority-4-background)] text-[var(--color-priority-4)]', className)}>
          {PRIORITY_BADGE[4].label}
        </span>
      )
    case 5:
      return (
        <span className={cn('bg-[var(--color-priority-5-background)] text-[var(--color-priority-5)]', className)}>
          {PRIORITY_BADGE[5].label}
        </span>
      )
    // Unknown/out-of-range priority (including the documented default, 3)
    // falls back to the P3 look — the exact behaviour of the old
    // `PRIORITY_BADGE[priority] ?? PRIORITY_BADGE[3]` lookup at every call site.
    case 3:
    default:
      return (
        <span className={cn('bg-[var(--color-priority-3-background)] text-[var(--color-priority-3)]', className)}>
          {PRIORITY_BADGE[3].label}
        </span>
      )
  }
}
