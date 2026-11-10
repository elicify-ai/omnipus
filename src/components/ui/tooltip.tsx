import * as React from 'react'
import * as TooltipPrimitive from '@radix-ui/react-tooltip'
import { cn } from '@/lib/utils'

// Tooltip — shadcn/ui's Tooltip on @radix-ui/react-tooltip (W2-1 contract
// §2.9; R45, #1273, #828). Replaces the earlier hand-built wrapper and its
// `content`/`label`/`side`/`interactive` props; callers compose
// <Tooltip><TooltipTrigger asChild>{control}</TooltipTrigger>
// <TooltipContent>{text}</TooltipContent></Tooltip>.
//
// Behaviour comes from Radix: opens on mouse hover and keyboard focus, closes
// on pointer leave, blur, Escape (topmost dismissable layer only, so a
// tooltip inside a dialog closes alone) and trigger pointer-down; the bubble
// is hoverable (WCAG 1.4.13); focus never moves. Touch does not open a
// tooltip — founder answer Q2 = A (2026-11-06): Radix's behaviour is kept,
// and touch users get the text another way where they need it.

// One delay for the whole product (#1273): 0ms keeps the immediate open the
// hand-built tooltip had; 300ms lets the pointer travel between adjacent
// triggers without re-waiting.
function TooltipProvider({ delayDuration = 0, skipDelayDuration = 300, ...props }: React.ComponentProps<typeof TooltipPrimitive.Provider>) {
  return <TooltipPrimitive.Provider delayDuration={delayDuration} skipDelayDuration={skipDelayDuration} {...props} />
}

// Each Tooltip carries its own provider (the shadcn pattern), so no
// app-root provider is required.
function Tooltip(props: React.ComponentProps<typeof TooltipPrimitive.Root>) {
  return (
    <TooltipProvider>
      <TooltipPrimitive.Root {...props} />
    </TooltipProvider>
  )
}

function TooltipTrigger(props: React.ComponentProps<typeof TooltipPrimitive.Trigger>) {
  return <TooltipPrimitive.Trigger data-slot="tooltip-trigger" {...props} />
}

// sideOffset 4 = --space-1 (the old bubble gap); collisionPadding 8 =
// --space-2 (the old viewport margin). The width fits the text (the old
// fixed w-64 is gone, R45) up to the caption measure or the viewport.
function TooltipContent({ className, side = 'top', sideOffset = 4, collisionPadding = 8, children, ...props }: React.ComponentProps<typeof TooltipPrimitive.Content>) {
  return (
    <TooltipPrimitive.Portal>
      <TooltipPrimitive.Content
        data-slot="tooltip-content"
        side={side}
        sideOffset={sideOffset}
        collisionPadding={collisionPadding}
        className={cn(
          'z-50 w-fit max-w-[min(var(--type-caption-measure),calc(100vw-var(--space-4)))] rounded-[var(--radius-medium)] border border-[var(--color-border)] bg-[var(--color-surface-3)] px-[var(--space-2)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-secondary)] shadow-[var(--elevation-floating)] text-balance',
          'animate-in fade-in-0 zoom-in-95 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95 motion-reduce:animate-none',
          // Forced colours: an explicit system-colour repaint, as the old bubble
          // and Button/Progress/Slider/Switch do, so border, fill and text stay
          // distinguishable.
          'forced-colors:border-[CanvasText] forced-colors:bg-[Canvas] forced-colors:text-[CanvasText] forced-colors:[forced-color-adjust:none]',
          className,
        )}
        {...props}
      >
        {children}
      </TooltipPrimitive.Content>
    </TooltipPrimitive.Portal>
  )
}

export { TooltipProvider, Tooltip, TooltipTrigger, TooltipContent }
