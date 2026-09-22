import * as React from 'react'
import * as ProgressPrimitive from '@radix-ui/react-progress'
import { cn } from '@/lib/utils'

export interface ProgressProps extends React.ComponentPropsWithoutRef<typeof ProgressPrimitive.Root> {
  label?: string
  'aria-valuenow'?: never
  'aria-valuemin'?: never
  'aria-valuemax'?: never
}

const Progress = React.forwardRef<
  React.ElementRef<typeof ProgressPrimitive.Root>,
  ProgressProps
>(({ className, value, max = 100, label, 'aria-label': ariaLabel, ...props }, ref) => {
  const validMax = Number.isFinite(max) && max > 0 ? max : 100
  const determinate = typeof value === 'number'
    && Number.isFinite(value)
    && value >= 0
    && value <= validMax
    && max === validMax
  const reportedValue = determinate ? value : null
  const completion = determinate ? (value / validMax) * 100 : null

  return (
    <ProgressPrimitive.Root
      ref={ref}
      className={cn(
        'relative h-2 w-full overflow-hidden rounded-full bg-[var(--color-surface-3)] forced-colors:border forced-colors:border-[CanvasText] forced-colors:bg-[Canvas] forced-colors:[forced-color-adjust:none]',
        className,
      )}
      value={reportedValue}
      max={validMax}
      aria-label={ariaLabel ?? label}
      {...props}
      aria-valuenow={reportedValue ?? undefined}
      aria-valuemin={0}
      aria-valuemax={validMax}
    >
      <ProgressPrimitive.Indicator
        className={cn(
          'h-full bg-[var(--color-accent)] transition-all duration-500 motion-reduce:transition-none forced-colors:bg-[Highlight]',
          determinate
            ? 'w-full'
            : 'mx-auto w-1/3 animate-pulse opacity-60 motion-reduce:animate-none',
        )}
        style={completion === null ? undefined : { transform: `translateX(-${100 - completion}%)` }}
      />
    </ProgressPrimitive.Root>
  )
})
Progress.displayName = ProgressPrimitive.Root.displayName

export { Progress }
