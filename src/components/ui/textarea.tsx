import * as React from 'react'
import { cn } from '@/lib/utils'

const Textarea = React.forwardRef<
  HTMLTextAreaElement,
  React.TextareaHTMLAttributes<HTMLTextAreaElement>
>(({ className, ...props }, ref) => {
  return (
    <textarea tabIndex={0}
      className={cn(
        'flex min-h-[80px] w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)] py-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)] placeholder:text-[var(--color-muted)]  disabled:cursor-not-allowed disabled:opacity-50 resize-none',
        className
      )}
      ref={ref}
      {...props}
    />
  )
})
Textarea.displayName = 'Textarea'

export { Textarea }
