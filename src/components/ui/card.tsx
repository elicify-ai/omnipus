import * as React from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

// US-2: Card — flat dark panel, Liquid Silver text, subtle border.
// Default matches the dominant hand-built card pattern found across the app
// (rounded-lg + border + surface-1 + no shadow): see the Card migration
// inventory (design-system/manifests/card.json owns the audit trail).
// `inset` covers the surface-2 nested-panel pattern (list rows, sub-panels
// inside a surface-1 parent). `floating` covers the small minority of
// popover/graph-node style cards that DO want elevation, using the
// `--elevation-floating` token rather than an invented shadow value.
const cardVariants = cva(
  'rounded-lg border border-[var(--color-border)] text-[var(--color-secondary)]',
  {
    variants: {
      variant: {
        default: 'bg-[var(--color-surface-1)]',
        inset: 'bg-[var(--color-surface-2)]',
        floating: 'rounded-xl bg-[var(--color-surface-1)] shadow-[var(--elevation-floating)]',
      },
    },
    defaultVariants: {
      variant: 'default',
    },
  }
)

export interface CardProps
  extends React.HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof cardVariants> {}

const Card = React.forwardRef<HTMLDivElement, CardProps>(
  ({ className, variant, ...props }, ref) => (
    <div ref={ref} className={cn(cardVariants({ variant }), className)} {...props} />
  )
)
Card.displayName = 'Card'

const CardHeader = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div ref={ref} className={cn('flex flex-col space-y-[var(--space-1)] p-[var(--space-3)]', className)} {...props} />
  )
)
CardHeader.displayName = 'CardHeader'

const CardTitle = React.forwardRef<HTMLParagraphElement, React.HTMLAttributes<HTMLHeadingElement>>(
  ({ className, ...props }, ref) => (
    <h3
      ref={ref}
      className={cn('font-headline text-[length:var(--type-body-compact-size)] font-semibold leading-none tracking-tight', className)}
      {...props}
    />
  )
)
CardTitle.displayName = 'CardTitle'

const CardDescription = React.forwardRef<HTMLParagraphElement, React.HTMLAttributes<HTMLParagraphElement>>(
  ({ className, ...props }, ref) => (
    <p ref={ref} className={cn('text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]', className)} {...props} />
  )
)
CardDescription.displayName = 'CardDescription'

const CardContent = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div ref={ref} className={cn('p-[var(--space-3)] pt-0', className)} {...props} />
  )
)
CardContent.displayName = 'CardContent'

const CardFooter = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div ref={ref} className={cn('flex items-center p-[var(--space-3)] pt-0', className)} {...props} />
  )
)
CardFooter.displayName = 'CardFooter'

export { Card, CardHeader, CardFooter, CardTitle, CardDescription, CardContent, cardVariants }
