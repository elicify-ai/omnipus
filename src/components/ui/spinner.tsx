import { CircleNotch } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

// Spinner — the kit's one loading glyph (W2-1 contract §2.7, R41 "loading →
// Spinner + Skeleton"). Same CircleNotch shape as Button's pending
// indicator, so a page-level spinner and a busy button read as one thing.
//
// Two modes:
// - default: a polite `role="status"` region named by `label`, for a region
//   whose loading state nothing else announces;
// - `decorative`: the glyph alone, `aria-hidden`, for use inside a control
//   that already announces its own busy state (no second announcement).
export interface SpinnerProps {
  /** `sm` = 16px (`--icon-size-medium`), `lg` = 24px (`--icon-size-prominent`). */
  size?: 'sm' | 'lg'
  /** Accessible name of the status region. Ignored when `decorative`. */
  label?: string
  /** Hide from assistive technology and drop the status role. */
  decorative?: boolean
  className?: string
}

const spinnerSizes = {
  sm: 'size-[var(--icon-size-medium)]',
  lg: 'size-[var(--icon-size-prominent)]',
} as const

export function Spinner({ size = 'sm', label = 'Loading', decorative = false, className }: SpinnerProps) {
  const glyph = (
    <CircleNotch
      aria-hidden="true"
      className={cn(spinnerSizes[size], 'shrink-0 animate-spin motion-reduce:animate-none')}
    />
  )
  if (decorative) {
    return <span aria-hidden="true" className={cn('inline-flex items-center justify-center', className)}>{glyph}</span>
  }
  return (
    <span role="status" aria-label={label} className={cn('inline-flex items-center justify-center', className)}>
      {glyph}
    </span>
  )
}
