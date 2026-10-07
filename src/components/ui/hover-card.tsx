import * as React from 'react'
import * as HoverCardPrimitive from '@radix-ui/react-hover-card'
import { cn } from '@/lib/utils'

// shadcn/ui Radix Hover Card composition, tokenised for Sovereign Deep.
const HoverCard = HoverCardPrimitive.Root

const HoverCardTrigger = React.forwardRef<
  React.ComponentRef<typeof HoverCardPrimitive.Trigger>,
  React.ComponentPropsWithoutRef<typeof HoverCardPrimitive.Trigger>
>(({ asChild, ...props }, ref) => (
  <HoverCardPrimitive.Trigger ref={ref} asChild={asChild} tabIndex={asChild ? undefined : 0} {...props} />
))
HoverCardTrigger.displayName = 'HoverCardTrigger'

const HoverCardContent = React.forwardRef<
  React.ComponentRef<typeof HoverCardPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof HoverCardPrimitive.Content>
>(({ className, align = 'start', sideOffset = 4, ...props }, ref) => (
  <HoverCardPrimitive.Portal>
    <HoverCardPrimitive.Content ref={ref} align={align} sideOffset={sideOffset}
      className={cn(
        'z-[100] w-max max-w-[min(28rem,calc(100vw-var(--space-4)))] max-h-[var(--radix-hover-card-content-available-height)] overflow-y-auto rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-[var(--space-3)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)] shadow-[var(--elevation-floating)] motion-reduce:animate-none forced-colors:border-[CanvasText] forced-colors:bg-[Canvas] forced-colors:text-[CanvasText] forced-colors:[forced-color-adjust:none]',
        className,
      )} {...props} />
  </HoverCardPrimitive.Portal>
))
HoverCardContent.displayName = 'HoverCardContent'

export { HoverCard, HoverCardTrigger, HoverCardContent }
