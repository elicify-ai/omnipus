import * as React from 'react'
import { Slot } from '@radix-ui/react-slot'
import { CheckCircle, CircleNotch, WarningCircle } from '@phosphor-icons/react'
import { cva, type VariantProps } from 'class-variance-authority'
import { useLoadingVisibility } from '@/design-system/use-loading-visibility'
import { cn } from '@/lib/utils'

const buttonVariants = cva(
  'relative inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md text-sm font-medium transition-colors motion-reduce:transition-none forced-colors:border forced-colors:border-[ButtonText] forced-colors:bg-[ButtonFace] forced-colors:text-[ButtonText] forced-colors:[forced-color-adjust:none] disabled:pointer-events-none disabled:opacity-50 aria-disabled:cursor-not-allowed aria-disabled:opacity-50',
  {
    variants: {
      variant: {
        // US-2: Forge Gold default button
        default:
          'bg-[var(--color-accent)] text-[var(--color-primary)] hover:bg-[var(--color-accent-hover)] font-semibold',
        // US-2: Ruby destructive button
        destructive:
          'bg-[var(--color-error)] text-[var(--color-primary)] hover:bg-[var(--color-destructive-action-hover)]',
        outline:
          'border border-[var(--color-border)] bg-transparent text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]',
        secondary:
          'bg-[var(--color-surface-2)] text-[var(--color-secondary)] hover:bg-[var(--color-surface-3)]',
        ghost:
          'text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]',
        link:
          'text-[var(--color-accent)] underline-offset-4 hover:underline p-0 h-auto',
      },
      size: {
        default: 'h-9 px-4 py-2',
        sm: 'h-8 rounded-md px-3 text-xs',
        lg: 'h-10 rounded-md px-8',
        icon: 'h-9 w-9',
      },
    },
    defaultVariants: {
      variant: 'default',
      size: 'default',
    },
  }
)

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean
  actionState?: 'idle' | 'pending' | 'success' | 'error'
}

function ActionAnnouncement({ description }: { description?: string }) {
  const [announcement, setAnnouncement] = React.useState('')
  React.useEffect(() => { setAnnouncement(description ?? '') }, [description])
  return <span className="sr-only" role="status" aria-live="polite" aria-atomic="true">{announcement}</span>
}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, actionState = 'idle', type, disabled, onClickCapture, onAuxClickCapture, children, 'aria-busy': ariaBusy, 'aria-disabled': ariaDisabled, 'aria-description': ariaDescription, ...props }, ref) => {
    const Comp = asChild ? Slot : 'button'
    const pending = actionState === 'pending'
    const loadingVisible = useLoadingVisibility(pending)
    const presentedState = loadingVisible ? 'pending' : actionState === 'pending' ? 'idle' : actionState
    const announcedState = pending && !loadingVisible ? 'idle' : actionState
    const feedbackDescription = announcedState === 'pending'
      ? 'Action in progress'
      : announcedState === 'success'
        ? 'Action succeeded'
        : announcedState === 'error' ? 'Action failed' : undefined
    const reservesFeedback = actionState !== 'idle' || loadingVisible
    const feedback = reservesFeedback ? (
      <span data-action-indicator="" className="inline-flex size-4 shrink-0 items-center justify-center" aria-hidden="true">
        {presentedState === 'pending' && <CircleNotch data-action-feedback="pending" className="size-4 animate-spin motion-reduce:animate-none" />}
        {presentedState === 'success' && <CheckCircle data-action-feedback="success" className="size-4 text-[var(--color-success)] forced-colors:text-[ButtonText]" weight="bold" />}
        {presentedState === 'error' && <WarningCircle data-action-feedback="error" className="size-4 text-[var(--color-error)] forced-colors:text-[ButtonText]" weight="bold" />}
      </span>
    ) : null
    const preventPendingActivation: React.MouseEventHandler<HTMLButtonElement> = (event) => {
      if (disabled || pending) {
        event.preventDefault()
        event.stopPropagation()
        return
      }
      onClickCapture?.(event)
    }
    const preventPendingAuxiliaryActivation: React.MouseEventHandler<HTMLButtonElement> = (event) => {
      if (disabled || pending) {
        event.preventDefault()
        event.stopPropagation()
        return
      }
      onAuxClickCapture?.(event)
    }
    const guardedChild = asChild && React.isValidElement(children)
      ? React.cloneElement(children as React.ReactElement<Record<string, unknown>>, {
          ...(pending ? { 'aria-busy': true } : {}),
          ...(disabled || pending ? { 'aria-disabled': true } : {}),
          ...(feedbackDescription ? { 'aria-description': feedbackDescription } : {}),
          onClickCapture: (event: React.MouseEvent<HTMLButtonElement>) => {
            if (disabled || pending) {
              event.preventDefault()
              event.stopPropagation()
              return
            }
            const childCapture = (children.props as { onClickCapture?: React.MouseEventHandler<HTMLButtonElement> }).onClickCapture
            childCapture?.(event)
            onClickCapture?.(event)
          },
          onAuxClickCapture: (event: React.MouseEvent<HTMLButtonElement>) => {
            if (disabled || pending) {
              event.preventDefault()
              event.stopPropagation()
              return
            }
            const childCapture = (children.props as { onAuxClickCapture?: React.MouseEventHandler<HTMLButtonElement> }).onAuxClickCapture
            childCapture?.(event)
            onAuxClickCapture?.(event)
          },
        }, <>{feedback}{(children.props as { children?: React.ReactNode }).children}</>)
      : children
    return (
      <>
      <Comp
        // Explicit tabindex: WebKit's default Tab policy skips native buttons
        // without one (repo convention — see tabindex-convention.test.ts).
        // Placed before {...props} so a caller-supplied tabIndex wins.
        tabIndex={0}
        className={cn(buttonVariants({ variant, size }), className)}
        ref={ref}
        {...props}
        {...(!asChild ? { type: type ?? 'button', disabled: disabled || pending } : { type })}
        aria-disabled={asChild && (disabled || pending) ? true : ariaDisabled}
        aria-busy={pending ? true : ariaBusy}
        aria-description={feedbackDescription ?? ariaDescription}
        data-action-state={actionState}
        data-ds-action=""
        onClickCapture={asChild ? undefined : preventPendingActivation}
        onAuxClickCapture={asChild ? undefined : preventPendingAuxiliaryActivation}
      >{asChild ? guardedChild : <>{feedback}{guardedChild}</>}</Comp>
      <ActionAnnouncement description={feedbackDescription} />
      </>
    )
  }
)
Button.displayName = 'Button'

export { Button, buttonVariants }
