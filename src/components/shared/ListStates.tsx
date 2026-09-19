import type { ReactNode } from 'react'
import { EmptyState as EmptyStatePrimitive } from '@/components/ui/empty-state'
import { ErrorState as ErrorStatePrimitive } from '@/components/ui/error-state'
import { Skeleton } from '@/components/ui/skeleton'

export function SkeletonList() {
  return (
    <div className="space-y-2">
      {[1, 2, 3].map((i) => (
        <Skeleton
          key={i}
          className="h-16 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)]"
        />
      ))}
    </div>
  )
}

export function EmptyState({ icon, message }: { icon: ReactNode; message: string }) {
  return <EmptyStatePrimitive icon={icon} message={message} />
}

export function ErrorState({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return <ErrorStatePrimitive message={message} onRetry={onRetry} />
}
