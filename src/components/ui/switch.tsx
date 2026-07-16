import * as React from 'react'
import * as SwitchPrimitives from '@radix-ui/react-switch'
import { cn } from '@/lib/utils'

const Switch = React.forwardRef<
  React.ElementRef<typeof SwitchPrimitives.Root>,
  React.ComponentPropsWithoutRef<typeof SwitchPrimitives.Root>
>(({ className, ...props }, ref) => (
  <SwitchPrimitives.Root
    // WebKit tabbability (repo convention): Radix renders the switch button,
    // so the explicit tabIndex stamp lives here; {...props} may override.
    tabIndex={0}
    className={cn(
      'peer inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full border-2 border-transparent transition-colors disabled:cursor-not-allowed disabled:opacity-50',
      'data-[state=unchecked]:bg-[var(--color-surface-3)]',
      'data-[state=checked]:bg-[var(--color-accent)]',
      className
    )}
    {...props}
    ref={ref}
  >
    <SwitchPrimitives.Thumb
      className={cn(
        'pointer-events-none block h-4 w-4 rounded-full bg-[var(--color-secondary)] shadow-lg ring-0 transition-transform',
        'data-[state=unchecked]:translate-x-0 data-[state=checked]:translate-x-4'
      )}
    />
  </SwitchPrimitives.Root>
))
Switch.displayName = SwitchPrimitives.Root.displayName

export { Switch }
