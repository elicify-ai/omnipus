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
        'rounded-md bg-[var(--color-surface-2)] transition-opacity duration-150 motion-reduce:animate-none motion-reduce:transition-none',
        visible ? 'animate-pulse opacity-100' : 'opacity-0',
        className,
      )}
    />
  )
})
