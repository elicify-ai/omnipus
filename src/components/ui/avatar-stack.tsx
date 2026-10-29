import * as React from 'react'
import { cn } from '@/lib/utils'
import { Avatar, AvatarFallback } from './avatar'

/** One avatar in the stack. `node` is the caller's own mark (an agent mark, an Avatar). */
export interface AvatarStackItem {
  key: string
  node: React.ReactNode
}

export interface AvatarStackProps extends React.HTMLAttributes<HTMLSpanElement> {
  items: readonly AvatarStackItem[]
  /** How many avatars are drawn before the overflow marker (default 3). */
  max?: number
  /**
   * The full count the overflow marker is computed from. Defaults to
   * `items.length`; pass it when only the first avatars are known.
   */
  total?: number
  /** Overlap and ring size: `sm` for compact chrome, `md` for list rows. */
  size?: 'sm' | 'md'
}

const overlap = {
  sm: '-space-x-[var(--space-1)]',
  md: '-space-x-[var(--space-2)]',
}

/**
 * A row of overlapping avatars with a "+N" overflow marker (#1449 D6: the
 * group chat's Participants button, its panel and the activity pills).
 * Built on `Avatar`: the marker is an Avatar fallback, each slot carries the
 * surface ring that separates the overlapped marks. Presentational only —
 * the caller owns the accessible name of whatever control contains it, so
 * the stack itself is hidden from assistive technology.
 */
const AvatarStack = React.forwardRef<HTMLSpanElement, AvatarStackProps>(
  ({ items, max = 3, total, size = 'sm', className, ...props }, ref) => {
    const shown = items.slice(0, Math.max(0, max))
    const overflow = Math.max(0, (total ?? items.length) - shown.length)
    return (
      <span
        ref={ref}
        aria-hidden="true"
        data-avatar-stack=""
        data-overflow={overflow > 0 ? overflow : undefined}
        className={cn('inline-flex shrink-0 items-center', overlap[size], className)}
        {...props}
      >
        {shown.map((item) => (
          <span key={item.key} data-avatar-stack-item="" className="inline-flex rounded-full ring-2 ring-[var(--color-surface-1)]">
            {item.node}
          </span>
        ))}
        {overflow > 0 && (
          <span data-avatar-stack-overflow="" className="inline-flex rounded-full ring-2 ring-[var(--color-surface-1)]">
            <Avatar size="sm">
              <AvatarFallback className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">+{overflow}</AvatarFallback>
            </Avatar>
          </span>
        )}
      </span>
    )
  },
)
AvatarStack.displayName = 'AvatarStack'

export { AvatarStack }
