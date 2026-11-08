import * as React from 'react'
import { CheckCircle, Info, Warning, WarningCircle } from '@phosphor-icons/react'
import { cva } from 'class-variance-authority'
import { cn } from '@/lib/utils'

// Callout — the kit's inline notice (W2-1 contract §2.6, R41 "error →
// Callout/Alert"). Error and warning interrupt (role="alert"); info and
// success are polite (role="status"). Text on the error variant stays the
// secondary/muted tokens on a tinted surface — never text-white (skill rule 8).

const calloutVariants = cva(
  'flex w-full items-start gap-[var(--space-2-5)] rounded-[var(--radius-large)] border bg-[var(--color-surface-1)] px-[var(--space-2-5)] py-[var(--space-2)] text-[length:var(--type-body-compact-size)] forced-colors:border-[CanvasText]',
  {
    variants: {
      variant: {
        info: 'border-[var(--color-border)]',
        success: 'border-[var(--color-border)]',
        warning: 'border-[color-mix(in_srgb,var(--color-warning)_45%,var(--color-border))]',
        error: 'border-[color-mix(in_srgb,var(--color-error)_50%,var(--color-border))] bg-[color-mix(in_srgb,var(--color-error)_8%,var(--color-surface-1))]',
      },
    },
    defaultVariants: { variant: 'info' },
  },
)

type CalloutVariant = 'info' | 'warning' | 'error' | 'success'

const defaultIcons: Record<CalloutVariant, React.ComponentType<{ className?: string; 'aria-hidden'?: 'true' }>> = {
  info: Info,
  warning: Warning,
  error: WarningCircle,
  success: CheckCircle,
}

// Literal class strings per variant so Tailwind's source scan sees each one.
const iconColors: Record<CalloutVariant, string> = {
  info: 'text-[var(--color-info)]',
  warning: 'text-[var(--color-warning)]',
  error: 'text-[var(--color-error)]',
  success: 'text-[var(--color-success)]',
}

export interface CalloutProps extends React.HTMLAttributes<HTMLDivElement> {
  variant?: CalloutVariant
  /** Replaces the variant's default icon; `false` hides it. */
  icon?: React.ReactNode | false
  'data-testid'?: string
}

function Callout({ variant = 'info', icon, className, children, ...props }: CalloutProps) {
  const DefaultIcon = defaultIcons[variant]
  // Layout is [icon][text block][actions]: CalloutActions children sit in the
  // root row (ml-auto, self-center); everything else stacks in the text block.
  const childList = React.Children.toArray(children)
  const actions = childList.filter((child) => React.isValidElement(child) && child.type === CalloutActions)
  const text = childList.filter((child) => !(React.isValidElement(child) && child.type === CalloutActions))
  const renderedIcon = icon === false ? null : (
    <span aria-hidden="true" className={cn('flex shrink-0 items-center', iconColors[variant])}>
      {icon ?? <DefaultIcon aria-hidden="true" className="size-[var(--icon-size-medium)]" />}
    </span>
  )
  return (
    <div
      data-slot="callout"
      data-variant={variant}
      role={variant === 'error' || variant === 'warning' ? 'alert' : 'status'}
      className={cn(calloutVariants({ variant }), className)}
      {...props}
    >
      {renderedIcon}
      {text.length > 0 ? <div className="flex min-w-0 flex-1 flex-col gap-[var(--space-0-5)]">{text}</div> : null}
      {actions}
    </div>
  )
}

function CalloutTitle({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div data-slot="callout-title" className={cn('font-semibold text-[var(--color-secondary)]', className)} {...props} />
}

function CalloutDescription({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div data-slot="callout-description" className={cn('text-[var(--color-muted)]', className)} {...props} />
}

function CalloutActions({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div data-slot="callout-actions" className={cn('ml-auto flex self-center gap-[var(--space-1)]', className)} {...props} />
}

export { Callout, CalloutTitle, CalloutDescription, CalloutActions, calloutVariants }
