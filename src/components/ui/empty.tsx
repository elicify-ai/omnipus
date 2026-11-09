import * as React from 'react'
import { cn } from '@/lib/utils'

// Empty — the kit's "nothing here yet" surface (W2-1 contract §2.5, R41
// "Empty (shadcn)"). Same slot interface as shadcn/ui's Empty: plain
// elements with className passthrough and a data-slot equal to their
// kebab-case name. Static content, no role: a caller that needs the state
// announced (search "no results") puts role="status" on its own wrapper.
// Replaced EmptyState in W2-1: its callers moved here and EmptyState was removed.

function Empty({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      data-slot="empty"
      className={cn(
        'flex w-full flex-col items-center gap-[var(--space-2)] rounded-[var(--radius-large)] border border-dashed border-[var(--color-border)] p-[var(--space-4)] text-center',
        className,
      )}
      {...props}
    />
  )
}

function EmptyHeader({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div data-slot="empty-header" className={cn('flex flex-col items-center gap-[var(--space-2)]', className)} {...props} />
}

export interface EmptyMediaProps extends React.HTMLAttributes<HTMLDivElement> {
  /** `icon` draws the 40px surface box around a decorative glyph; `default` renders the media as given. */
  variant?: 'default' | 'icon'
}

function EmptyMedia({ variant = 'default', className, ...props }: EmptyMediaProps) {
  return (
    <div
      data-slot="empty-media"
      data-variant={variant}
      aria-hidden={variant === 'icon' ? 'true' : undefined}
      className={cn(
        'flex shrink-0 items-center justify-center',
        variant === 'icon' && 'size-[var(--space-6)] rounded-[var(--radius-large)] bg-[var(--color-surface-2)] text-[var(--color-muted)]',
        className,
      )}
      {...props}
    />
  )
}

// A div, never a heading: the caller's heading order must not be broken by
// a kit surface that cannot know where it sits in the page outline.
function EmptyTitle({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      data-slot="empty-title"
      className={cn('text-[length:var(--type-body-size)] font-semibold text-[var(--color-secondary)]', className)}
      {...props}
    />
  )
}

function EmptyDescription({ className, ...props }: React.HTMLAttributes<HTMLParagraphElement>) {
  return (
    <p
      data-slot="empty-description"
      className={cn('text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]', className)}
      {...props}
    />
  )
}

function EmptyContent({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      data-slot="empty-content"
      className={cn('flex flex-wrap items-center justify-center gap-[var(--space-2)]', className)}
      {...props}
    />
  )
}

export { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription, EmptyContent }
