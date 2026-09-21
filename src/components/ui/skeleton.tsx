import * as React from 'react'

import { useLoadingVisibility } from '@/design-system/use-loading-visibility'
import { cn } from '@/lib/utils'

export interface SkeletonProps extends React.HTMLAttributes<HTMLDivElement> {
  pending?: boolean
}

export const Skeleton = React.forwardRef<HTMLDivElement, SkeletonProps>(function Skeleton(
  { className, pending = true, ...props },
  ref,
) {
  const visible = useLoadingVisibility(pending)

  return (
    <div
      ref={ref}
      {...props}
      aria-hidden="true"
      data-visible={visible}
      className={cn(
        // skeleton-shimmer, not animate-pulse: the pulse throbs the whole
        // block's opacity 1 -> .5 -> 1 over 2s, which reads as blinking and
        // makes a page of placeholders look like it is breathing. The shimmer
        // sweeps a lighter band across a static surface, so the silhouette
        // holds still and the motion says "loading" rather than "flashing".
        // Defined in globals.css and library.css; motion-reduce flattens it.
        'rounded-md bg-[var(--color-surface-2)] transition-opacity duration-150 motion-reduce:animate-none motion-reduce:transition-none',
        visible ? 'skeleton-shimmer opacity-100' : 'opacity-0',
        className,
      )}
    />
  )
})
