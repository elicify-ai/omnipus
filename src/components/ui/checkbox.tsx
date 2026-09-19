import * as React from 'react'
import * as CheckboxPrimitive from '@radix-ui/react-checkbox'
import { Check } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

const Checkbox = React.forwardRef<
  React.ElementRef<typeof CheckboxPrimitive.Root>,
  React.ComponentPropsWithoutRef<typeof CheckboxPrimitive.Root>
>(({ className, ...props }, ref) => (
  <CheckboxPrimitive.Root
    ref={ref}
    data-ds-action=""
    // WebKit tabbability (repo convention): Radix renders the checkbox
    // button, so the explicit tabIndex stamp lives here; {...props} may
    // override.
    tabIndex={0}
    className={cn(
      'peer relative h-4 w-4 shrink-0 rounded-sm border border-[var(--color-border)] bg-[var(--color-surface-1)]',
      '',
      'disabled:cursor-not-allowed disabled:opacity-50',
      'data-[state=checked]:bg-[var(--color-accent)] data-[state=checked]:border-[var(--color-accent)] data-[state=checked]:text-[var(--color-primary)] data-[state=checked]:forced-colors:border-[HighlightText] data-[state=checked]:forced-colors:bg-[Highlight] data-[state=checked]:forced-colors:text-[HighlightText]',
      className
    )}
    {...props}
  >
    <CheckboxPrimitive.Indicator className="flex items-center justify-center text-current">
      <Check size={10} weight="bold" aria-hidden="true" />
    </CheckboxPrimitive.Indicator>
  </CheckboxPrimitive.Root>
))
Checkbox.displayName = CheckboxPrimitive.Root.displayName

export { Checkbox }
