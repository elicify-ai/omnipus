import type { ReactNode } from 'react'

export function SkeletonList() {
  return (
    <div className="space-y-2">
      {[1, 2, 3].map((i) => (
        <div
          key={i}
          className="h-16 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] animate-pulse"
        />
      ))}
    </div>
  )
}

export function EmptyState({ icon, message }: { icon: ReactNode; message: string }) {
  return (
    <div className="flex flex-col items-center justify-center py-16 gap-3 text-center">
      <div className="text-[var(--color-border)]">{icon}</div>
      <p className="text-sm text-[var(--color-muted)]">{message}</p>
    </div>
  )
}

export function ErrorState({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 py-8">
      <p className="text-sm text-[var(--color-error)]">{message}</p>
      {onRetry && (
        <button tabIndex={0}
          type="button"
          onClick={onRetry}
          className="rounded-md border border-[var(--color-border)] px-3 py-1.5 text-xs font-medium text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors focus:outline-none"
        >
          Retry
        </button>
      )}
    </div>
  )
}
