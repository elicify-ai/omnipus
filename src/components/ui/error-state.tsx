import { cn } from '@/lib/utils'

import { Button } from './button'

export interface ErrorStateProps {
  message: string
  onRetry?: () => void
  className?: string
}

export function ErrorState({ message, onRetry, className }: ErrorStateProps) {
  return (
    <div role="alert" className={cn('flex flex-col items-center justify-center gap-3 py-8', className)}>
      <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)] forced-colors:text-[CanvasText]">{message}</p>
      {onRetry ? (
        <Button variant="outline" size="sm" className="h-auto px-3 py-1.5 text-[length:var(--type-utility-xs-size)]" onClick={onRetry}>Retry</Button>
      ) : null}
    </div>
  )
}
