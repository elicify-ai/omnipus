import * as React from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

const badgeVariants = cva(
  'inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium transition-colors motion-reduce:transition-none',
  {
    variants: {
      variant: {
        default:
          'border-transparent bg-[var(--color-accent)] text-[var(--color-primary)] forced-colors:border-[CanvasText] forced-colors:bg-[ButtonFace] forced-colors:text-[ButtonText]',
        secondary:
          'border-transparent bg-[var(--color-surface-2)] text-[var(--color-secondary)]',
        outline:
          'border-[var(--color-border)] text-[var(--color-secondary)]',
        success:
          'border-transparent bg-[var(--color-success)]/20 text-[var(--color-success)]',
        error:
          'border-transparent bg-[var(--color-error)]/20 text-[color:var(--badge-error-foreground)]',
        destructive:
          'border-transparent bg-[var(--color-error)]/20 text-[color:var(--badge-error-foreground)]',
        warning:
          'border-transparent bg-[var(--color-warning)]/20 text-[var(--color-warning)]',
        muted:
          'border-[var(--color-border)] bg-[var(--color-surface-1)] text-[var(--color-muted)]',
      },
    },
    defaultVariants: {
      variant: 'default',
    },
  }
)

export interface BadgeProps
  extends React.HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, ...props }: BadgeProps) {
  return (
    <div className={cn(badgeVariants({ variant }), className)} {...props} />
  )
}

export { Badge, badgeVariants }
