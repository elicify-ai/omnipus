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

export function ErrorState({ message }: { message: string }) {
  return (
    <div className="flex justify-center py-8">
      <p className="text-sm text-[var(--color-error)]">{message}</p>
    </div>
  )
}
