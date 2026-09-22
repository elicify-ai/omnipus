import * as React from 'react'
import * as SliderPrimitive from '@radix-ui/react-slider'
import { cn } from '@/lib/utils'

export interface SliderProps extends Omit<React.ComponentPropsWithoutRef<typeof SliderPrimitive.Root>, 'aria-label' | 'aria-labelledby' | 'aria-describedby'> {
  'aria-label'?: string
  'aria-labelledby'?: string
  'aria-describedby'?: string
  /** Required, unique accessible names when the slider has multiple thumbs. */
  thumbLabels?: readonly string[]
}

const Slider = React.forwardRef<
  React.ElementRef<typeof SliderPrimitive.Root>,
  SliderProps
>(({ className, value, defaultValue, 'aria-label': ariaLabel, 'aria-labelledby': ariaLabelledBy, 'aria-describedby': ariaDescribedBy, thumbLabels, ...props }, ref) => {
  const thumbCount = value?.length ?? defaultValue?.length ?? 1
  if (thumbCount > 1) {
    if (thumbLabels?.length !== thumbCount || thumbLabels.some((label) => !label.trim()) || new Set(thumbLabels).size !== thumbCount) {
      throw new RangeError('thumbLabels must provide one distinct non-empty name for each slider thumb')
    }
  }

  return (
    <SliderPrimitive.Root
      ref={ref}
      className={cn(
        'relative flex w-full touch-none select-none items-center',
        'data-[orientation=vertical]:h-full data-[orientation=vertical]:w-auto data-[orientation=vertical]:flex-col',
        className,
      )}
      value={value}
      defaultValue={defaultValue}
      {...props}
    >
      <SliderPrimitive.Track className="relative h-1.5 w-full grow overflow-hidden rounded-full bg-[var(--color-surface-2)] forced-colors:bg-[Canvas] forced-colors:[forced-color-adjust:none] data-[orientation=vertical]:h-full data-[orientation=vertical]:w-1.5">
        <SliderPrimitive.Range className="absolute h-full bg-[var(--color-accent)] forced-colors:bg-[Highlight] data-[orientation=vertical]:h-auto data-[orientation=vertical]:w-full" />
      </SliderPrimitive.Track>
      {Array.from({ length: thumbCount }, (_, index) => (
        <SliderPrimitive.Thumb
          key={index}
          data-ds-action=""
          aria-label={thumbCount > 1 ? thumbLabels?.[index] : (thumbLabels?.[0] ?? ariaLabel)}
          aria-labelledby={thumbCount === 1 ? ariaLabelledBy : undefined}
          aria-describedby={ariaDescribedBy}
          className="relative block h-4 w-4 rounded-full border border-[var(--color-accent)] bg-[var(--color-surface-1)] shadow transition-colors motion-reduce:transition-none data-[disabled]:pointer-events-none data-[disabled]:opacity-50"
        />
      ))}
    </SliderPrimitive.Root>
  )
})
Slider.displayName = SliderPrimitive.Root.displayName

export { Slider }
