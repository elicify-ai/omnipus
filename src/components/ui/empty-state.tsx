import type { ReactNode } from 'react'

import { Button } from './button'
import { clsx } from 'clsx'

type EmptyStateAction =
  | { actionLabel: string; onAction: () => void }
  | { actionLabel?: never; onAction?: never }

export type EmptyStateProps = {
  icon: ReactNode
  message: string
  className?: string
} & EmptyStateAction

export function EmptyState({ icon, message, actionLabel, onAction, className }: EmptyStateProps) {
  return (
    <div className={clsx("flex flex-col items-center justify-center py-16 gap-3 text-center", className)}>
      <div className="text-[var(--color-border)]" aria-hidden="true">{icon}</div>
      <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">{message}</p>
      {actionLabel && onAction ? <Button size="sm" variant="outline" onClick={onAction}>{actionLabel}</Button> : null}
    </div>
  )
}
