import { cn } from '@/lib/utils'

import { Button } from './button'

export interface ErrorStateProps {
  message: string
  onRetry?: () => void
  /** Suppress duplicate retries without removing focus from the action. */
  retryDisabled?: boolean
  className?: string
}

export function ErrorState({ message, onRetry, retryDisabled = false, className }: ErrorStateProps) {
  return (
    <div role="alert" className={cn('flex flex-col items-center justify-center gap-[var(--space-2-5)] py-[var(--space-5)]', className)}>
      <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)] forced-colors:text-[CanvasText]">{message}</p>
      {onRetry ? (
        <Button variant="outline" size="sm" className="h-auto px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-utility-xs-size)]" aria-disabled={retryDisabled || undefined} onClick={() => { if (!retryDisabled) onRetry() }}>Retry</Button>
      ) : null}
    </div>
  )
}
