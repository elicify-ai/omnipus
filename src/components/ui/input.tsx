import * as React from 'react'
import { cn } from '@/lib/utils'

// US-2: Input — dark background, Liquid Silver text, Forge Gold focus ring
const Input = React.forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  ({ className, type, ...props }, ref) => {
    return (
      <input
        type={type}
        className={cn(
          // 44px tap target on mobile (touch min); compact 36px on sm+ (pointer).
          'flex h-11 sm:h-9 w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-1 text-sm text-[var(--color-secondary)] transition-colors',
          'placeholder:text-[var(--color-muted)]',
          'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-[var(--color-accent)] focus-visible:border-[var(--color-accent)]',
          'disabled:cursor-not-allowed disabled:opacity-50',
          className
        )}
        ref={ref}
        {...props}
      />
    )
  }
)
Input.displayName = 'Input'

export { Input }
