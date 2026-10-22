import { ArrowsClockwise } from '@phosphor-icons/react'
import { useChatStore } from '@/store/chat'
import { formatTokens } from '@/lib/formatTokens'
import { cn } from '@/lib/utils'

/**
 * TokenCounter — read-only session token usage + streaming spinner.
 *
 * Reads recorded `sessionTokens` (undefined until known) / `isStreaming`
 * from the chat store; same testids (`session-token-counter`, `session-token-
 * value`). Status display, not a control.
 */
export function TokenCounter({ className }: { className?: string }) {
  const sessionTokens = useChatStore((s) => s.sessionTokens)
  const isStreaming = useChatStore((s) => s.isStreaming)

  return (
    <div
      className={cn('flex items-center gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] shrink-0', className)}
      data-testid="session-token-counter"
      aria-label={sessionTokens === undefined ? 'Session token usage unavailable' : `${sessionTokens} tokens used`}
      role="status"
    >
      <ArrowsClockwise
        size={11}
        className={cn(isStreaming ? 'animate-spin text-[var(--color-accent)]' : undefined)}
        aria-hidden="true"
      />
      <span
        className={cn('font-mono tabular-nums', isStreaming ? 'text-[var(--color-secondary)]' : undefined)}
        data-testid="session-token-value"
      >
        {sessionTokens === undefined ? 'Usage unavailable' : `${formatTokens(sessionTokens)} tokens`}
      </span>
    </div>
  )
}
