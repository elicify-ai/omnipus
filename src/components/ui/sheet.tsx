import * as React from 'react'
import * as DialogPrimitive from '@radix-ui/react-dialog'
import { X } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

const Sheet = DialogPrimitive.Root
const SheetTrigger = DialogPrimitive.Trigger
const SheetClose = DialogPrimitive.Close
const SheetPortal = DialogPrimitive.Portal

const SheetOverlay = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Overlay>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Overlay>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Overlay
    ref={ref}
    className={cn(
      'fixed inset-0 z-50 bg-black/60 backdrop-blur-sm',
      'data-[state=open]:animate-in data-[state=closed]:animate-out',
      'data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0',
      className
    )}
    {...props}
  />
))
SheetOverlay.displayName = DialogPrimitive.Overlay.displayName

interface SheetContentProps
  extends React.ComponentPropsWithoutRef<typeof DialogPrimitive.Content> {
  side?: 'top' | 'bottom' | 'left' | 'right'
  overlay?: boolean
  /**
   * Tailwind width class (e.g. "sm:max-w-3xl", "sm:max-w-md") to override the
   * default side-width. Defaults to "sm:w-80" per side. Use this when a
   * slideout needs to be wider/narrower than the standard 20rem (320px).
   */
  widthClass?: string
  /**
   * UAT finding (browser live panel, ADR-040/041): defaults to `true` for
   * every existing Sheet consumer (unchanged). Set `false` when the content
   * already renders its OWN close affordance — e.g. BrowserLivePanel.tsx's
   * `<BrowserLiveView>` header has a custom `aria-label="Close live browser
   * panel"` button — so this component's own unconditional
   * `DialogPrimitive.Close` doesn't render a SECOND, unlabeled ("Close",
   * ~38px, absolute right-2 top-2) close control stacked on top of it. Radix
   * still closes on Escape / onOpenChange / the caller's own close button
   * either way — this prop only ever suppresses the REDUNDANT visual button.
   */
  showClose?: boolean
}

const sideVariants = {
  top: 'inset-x-0 top-0 border-b border-[var(--color-border)] data-[state=closed]:slide-out-to-top data-[state=open]:slide-in-from-top',
  bottom: 'inset-x-0 bottom-0 border-t border-[var(--color-border)] data-[state=closed]:slide-out-to-bottom data-[state=open]:slide-in-from-bottom',
  left: 'inset-y-0 left-0 h-full w-full border-r border-[var(--color-border)] data-[state=closed]:slide-out-to-left data-[state=open]:slide-in-from-left',
  right: 'inset-y-0 right-0 h-full w-full border-l border-[var(--color-border)] data-[state=closed]:slide-out-to-right data-[state=open]:slide-in-from-right',
}

/**
 * Per-side default width. The right side (slide-over) is capped at 32rem /
 * sm:max-w-2xl (672 px) on desktop per Wave 6 finding I2 — the Edit-agent
 * slide-over was rendering at 47% of a 1440 px viewport via the legacy
 * sm:max-w-3xl literal (768 px). 90vw on mobile keeps the slide-over inside
 * the viewport on phone widths. Callers may still override via the
 * `widthClass` prop (e.g. CreateAgentModal keeps its wider sm:max-w-3xl).
 */
const sideDefaultWidth = {
  top: '',
  bottom: '',
  left: 'w-[90vw] sm:max-w-md',
  right: 'w-[90vw] sm:max-w-2xl',
}

const SheetContent = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Content>,
  SheetContentProps
>(({ side = 'right', overlay = true, widthClass, showClose = true, className, children, ...props }, ref) => (
  <SheetPortal>
    {overlay && <SheetOverlay />}
    <DialogPrimitive.Content
      ref={ref}
      // WCAG 4.1.2: Radix Dialog.Content sets role="dialog" automatically;
      // pair it with aria-modal="true" so AT knows outside content is inert.
      aria-modal="true"
      className={cn(
        // surface-0 (Deep Space Black): panels match the app background so the
        // shell reads as one flat dark surface; cards/inputs (surface-2/3) still
        // lift above it for contrast.
        'fixed z-50 bg-[var(--color-surface-0)] p-6 shadow-xl transition ease-in-out',
        'data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:duration-300 data-[state=open]:duration-500',
        sideVariants[side],
        // Default per-side width (e.g. left/right = sm:w-80). The widthClass
        // prop OVERRIDES the default; pass a full Tailwind class like
        // "sm:max-w-3xl" or "w-full sm:max-w-2xl".
        widthClass ?? sideDefaultWidth[side],
        className
      )}
      {...props}
    >
      {children}
      {showClose && (
        <DialogPrimitive.Close className="absolute right-2 top-2 flex h-11 w-11 items-center justify-center rounded-sm opacity-70 ring-offset-[var(--color-primary)] transition-opacity hover:opacity-100">
          <X size={16} />
          <span className="sr-only">Close</span>
        </DialogPrimitive.Close>
      )}
    </DialogPrimitive.Content>
  </SheetPortal>
))
SheetContent.displayName = DialogPrimitive.Content.displayName

/**
 * SheetHeader — flat chrome header row shared with the workspace top bar.
 *
 * Defaults to a single 44px (`h-chrome-header`) horizontal row with no
 * bottom border so open panels align with the main shell instead of
 * sitting taller with stacked hairlines. For a multi-line header, pass
 * `h-auto min-h-0 flex-col items-start` (registered in the twMerge config
 * in lib/utils.ts so the override wins over the base token) and move
 * description content to a sibling below — never reintroduce `border-b`
 * on chrome rows.
 */
const SheetHeader = ({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) => (
  <div
    className={cn(
      'flex flex-row items-center gap-2 text-left h-chrome-header min-h-chrome-header px-4 shrink-0',
      className,
    )}
    {...props}
  />
)
SheetHeader.displayName = 'SheetHeader'

const SheetFooter = ({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) => (
  <div className={cn('flex flex-col-reverse sm:flex-row sm:justify-end sm:space-x-2', className)} {...props} />
)
SheetFooter.displayName = 'SheetFooter'

const SheetTitle = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Title>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Title>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Title
    ref={ref}
    className={cn(
      // text-sm / semibold keeps the title inside the 44px chrome row without
      // forcing the panel header taller than the workspace top bar. min-w-0 lets
      // the flex item shrink so truncate actually ellipses long titles.
      'font-headline text-sm font-semibold text-[var(--color-secondary)] truncate min-w-0',
      className,
    )}
    {...props}
  />
))
SheetTitle.displayName = DialogPrimitive.Title.displayName

const SheetDescription = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Description>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Description>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Description
    ref={ref}
    className={cn('text-sm text-[var(--color-muted)]', className)}
    {...props}
  />
))
SheetDescription.displayName = DialogPrimitive.Description.displayName

export {
  Sheet,
  SheetPortal,
  SheetOverlay,
  SheetTrigger,
  SheetClose,
  SheetContent,
  SheetHeader,
  SheetFooter,
  SheetTitle,
  SheetDescription,
}
