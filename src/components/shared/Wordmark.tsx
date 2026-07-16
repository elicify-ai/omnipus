import { cn } from '@/lib/utils'

/**
 * Brand wordmark — "omnipus.ai", all lowercase, with the ".ai" in Forge Gold.
 * The single source of truth for the textual brand across the UI (sidebar,
 * login, landing, about, chat welcome). Size via className (text-sm/xl/…).
 */
export function Wordmark({ className }: { className?: string }) {
  return (
    <span className={cn('font-headline font-bold lowercase text-[var(--color-secondary)]', className)}>
      omnipus<span className="text-[var(--color-accent)]">.ai</span>
    </span>
  )
}
