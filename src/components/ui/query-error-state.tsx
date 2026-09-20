import { WarningCircle } from '@phosphor-icons/react'

import { cn } from '@/lib/utils'

import { Button } from './button'

export interface QueryErrorStateProps {
  message: string
  onRetry?: () => void
  layout?: 'absolute' | 'fill'
  testId?: string
  className?: string
}

export function QueryErrorState({
  message,
  onRetry,
  layout = 'absolute',
  testId,
  className,
}: QueryErrorStateProps) {
  return (
    <div
      role="alert"
      className={cn(
        layout === 'absolute'
          ? 'absolute inset-0 flex flex-col items-center justify-center gap-3 p-8 text-center'
          : 'flex flex-1 h-full flex-col items-center justify-center gap-3 p-8 text-center',
        className,
      )}
      data-testid={testId ?? 'query-error-state'}
    >
      <WarningCircle size={24} className="text-[var(--color-error)]" aria-hidden="true" />
      <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">{message}</p>
      {onRetry ? <Button variant="link" size="sm" className="h-auto p-0 text-[length:var(--type-utility-xs-size)]" onClick={onRetry}>Retry</Button> : null}
    </div>
  )
}
