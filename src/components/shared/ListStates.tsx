import { ErrorState as ErrorStatePrimitive } from '@/components/ui/error-state'
import { Skeleton } from '@/components/ui/skeleton'

export function SkeletonList() {
  return (
    <div className="space-y-[var(--space-2)]">
      {[1, 2, 3].map((i) => (
        <Skeleton
          key={i}
          className="h-16 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)]"
        />
      ))}
    </div>
  )
}

export function ErrorState({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return <ErrorStatePrimitive message={message} onRetry={onRetry} />
}
