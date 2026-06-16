import * as React from 'react'
import * as DialogPrimitive from '@radix-ui/react-dialog'
import { cn } from '@/lib/utils'
import { Button, type ButtonProps } from '@/components/ui/button'

/**
 * AlertDialog — a design-system confirmation dialog used to replace the native,
 * off-brand, embed-unsafe `window.confirm`.
 *
 * NOTE: `@radix-ui/react-alert-dialog` is intentionally NOT a dependency of this
 * project (no new runtime deps). This is built on the already-present
 * `@radix-ui/react-dialog` primitive and adds the alert-dialog semantics that
 * matter for a confirmation flow:
 *  - the surface is announced with `role="alertdialog"`,
 *  - it is modal and traps focus (Radix Dialog default),
 *  - it does NOT close on overlay click (a destructive confirm should be
 *    deliberate); only Cancel/Confirm/Escape dismiss it.
 *
 * Open state is controlled by the consumer via `useState`. Proceed ONLY on
 * Confirm — the existing action is wired to the Action button's onClick.
 */

const AlertDialog = DialogPrimitive.Root
const AlertDialogTrigger = DialogPrimitive.Trigger
const AlertDialogPortal = DialogPrimitive.Portal

const AlertDialogOverlay = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Overlay>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Overlay>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Overlay
    ref={ref}
    className={cn(
      'fixed inset-0 z-50 bg-[var(--color-primary)]/80 backdrop-blur-sm',
      'data-[state=open]:animate-in data-[state=closed]:animate-out',
      'data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0',
      className
    )}
    {...props}
  />
))
AlertDialogOverlay.displayName = 'AlertDialogOverlay'

const AlertDialogContent = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Content>
>(({ className, ...props }, ref) => (
  <AlertDialogPortal>
    <AlertDialogOverlay />
    <DialogPrimitive.Content
      ref={ref}
      role="alertdialog"
      // A confirmation must be deliberate: do not dismiss on outside click.
      onPointerDownOutside={(e) => e.preventDefault()}
      onInteractOutside={(e) => e.preventDefault()}
      className={cn(
        'fixed left-[50%] top-[50%] z-50 grid w-full max-w-md translate-x-[-50%] translate-y-[-50%] gap-4',
        'max-h-[90dvh] overflow-y-auto',
        'border border-[var(--color-border)] bg-[var(--color-surface-1)] p-6 shadow-2xl',
        'rounded-xl text-[var(--color-secondary)]',
        'data-[state=open]:animate-in data-[state=closed]:animate-out',
        'data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0',
        'data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95',
        'data-[state=closed]:slide-out-to-left-1/2 data-[state=closed]:slide-out-to-top-[48%]',
        'data-[state=open]:slide-in-from-left-1/2 data-[state=open]:slide-in-from-top-[48%]',
        className
      )}
      {...props}
    />
  </AlertDialogPortal>
))
AlertDialogContent.displayName = 'AlertDialogContent'

const AlertDialogHeader = ({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) => (
  <div className={cn('flex flex-col space-y-1.5 text-center sm:text-left', className)} {...props} />
)
AlertDialogHeader.displayName = 'AlertDialogHeader'

const AlertDialogFooter = ({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) => (
  <div
    className={cn('flex flex-col-reverse gap-2 sm:flex-row sm:justify-end sm:space-x-2 sm:gap-0', className)}
    {...props}
  />
)
AlertDialogFooter.displayName = 'AlertDialogFooter'

const AlertDialogTitle = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Title>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Title>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Title
    ref={ref}
    className={cn('font-headline text-lg font-bold leading-none tracking-tight', className)}
    {...props}
  />
))
AlertDialogTitle.displayName = 'AlertDialogTitle'

const AlertDialogDescription = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Description>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Description>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Description
    ref={ref}
    className={cn('text-sm text-[var(--color-muted)] whitespace-pre-line', className)}
    {...props}
  />
))
AlertDialogDescription.displayName = 'AlertDialogDescription'

// Cancel — dismisses the dialog without proceeding. Wrapped in DialogPrimitive.Close
// so it always closes regardless of controlled state wiring.
const AlertDialogCancel = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ variant = 'outline', ...props }, ref) => (
    <DialogPrimitive.Close asChild>
      <Button ref={ref} variant={variant} {...props} />
    </DialogPrimitive.Close>
  )
)
AlertDialogCancel.displayName = 'AlertDialogCancel'

// Action — the confirm button. Proceeds ONLY when clicked. `onClick` is REQUIRED:
// a confirm with no handler is a no-op (the dialog would never act on confirm),
// so we make that a compile error rather than a silent dead button.
type AlertDialogActionProps = ButtonProps & {
  onClick: NonNullable<ButtonProps['onClick']>
}
const AlertDialogAction = React.forwardRef<HTMLButtonElement, AlertDialogActionProps>(
  ({ variant = 'default', ...props }, ref) => <Button ref={ref} variant={variant} {...props} />
)
AlertDialogAction.displayName = 'AlertDialogAction'

export {
  AlertDialog,
  AlertDialogPortal,
  AlertDialogOverlay,
  AlertDialogTrigger,
  AlertDialogContent,
  AlertDialogHeader,
  AlertDialogFooter,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogCancel,
  AlertDialogAction,
}
