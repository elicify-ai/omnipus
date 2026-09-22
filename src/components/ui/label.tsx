import * as React from 'react'
import * as LabelPrimitive from '@radix-ui/react-label'
import { cn } from '@/lib/utils'

const Label = React.forwardRef<
  React.ElementRef<typeof LabelPrimitive.Root>,
  React.ComponentPropsWithoutRef<typeof LabelPrimitive.Root>
>(({ className, ...props }, ref) => (
  <LabelPrimitive.Root
    ref={ref}
    className={cn(
      // leading-compact, not shadcn's default leading-none: with a line height
      // of 1 the label's box hugs its glyphs, so the only space between the
      // text and the field below is the caller's gap token. Measured on the
      // login screen, a 4px -> 8px gap change moved the visible text-to-input
      // distance from ~7.6px to ~8px — leading-none had eaten the old half-
      // leading. The compact line height is the token paired with the
      // body-compact size this component already uses.
      'text-[length:var(--type-body-compact-size)] font-medium leading-[var(--font-line-height-compact)] text-[var(--color-secondary)] peer-disabled:cursor-not-allowed peer-disabled:opacity-70',
      className
    )}
    {...props}
  />
))
Label.displayName = LabelPrimitive.Root.displayName

export { Label }
