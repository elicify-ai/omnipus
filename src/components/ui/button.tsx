import * as React from 'react'
import { Slot } from '@radix-ui/react-slot'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

const buttonVariants = cva(
  'inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md text-sm font-medium transition-colors disabled:pointer-events-none disabled:opacity-50',
  {
    variants: {
      variant: {
        // US-2: Forge Gold default button
        default:
          'bg-[var(--color-accent)] text-[var(--color-primary)] hover:bg-[var(--color-accent-hover)] font-semibold',
        // US-2: Ruby destructive button
        destructive:
          'bg-[var(--color-error)] text-[var(--color-secondary)] hover:bg-[var(--color-error-hover)]',
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
}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : 'button'
    return (
      <Comp
        className={cn(buttonVariants({ variant, size, className }))}
        ref={ref}
        {...props}
      />
    )
  }
)
Button.displayName = 'Button'

export { Button, buttonVariants }
