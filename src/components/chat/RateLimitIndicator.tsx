import { useEffect, useState } from 'react'
import { Warning, Clock, X } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

export interface RateLimitIndicatorProps {
  scope: 'agent' | 'channel' | 'global'
  resource: string
  policyRule: string
  retryAfterSeconds: number
  tool?: string
  onDismiss: () => void
}

function formatSeconds(s: number): string {
  if (s <= 0) return '0s'
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  const rem = s % 60
  return rem > 0 ? `${m}m ${rem}s` : `${m}m`
}

export function RateLimitIndicator({
  resource,
  retryAfterSeconds,
  tool,
  onDismiss,
}: RateLimitIndicatorProps) {
  const [remaining, setRemaining] = useState(Math.max(0, retryAfterSeconds))

  // Reset the countdown when a new event arrives (prop change). The previous
  // implementation only ran setState on first mount, so a new event arriving
  // on the same mounted component would show a stale countdown — possibly
  // already at 0 even though the new event has 90 seconds remaining.
  useEffect(() => {
    setRemaining(Math.max(0, retryAfterSeconds))
  }, [retryAfterSeconds])

  // Tick the countdown every second. Cleanup runs when the component
  // unmounts OR when `remaining` transitions past zero and the effect
  // re-runs without scheduling a new interval.
  useEffect(() => {
    if (remaining <= 0) return
    const id = setInterval(() => {
      setRemaining((prev) => Math.max(0, prev - 1))
    }, 1000)
    return () => clearInterval(id)
  }, [remaining])

  const isToolCall = resource === 'tool_call' || !!tool
  const label = isToolCall
    ? `Tool call rate limited${tool ? ` (${tool})` : ''}`
    : 'LLM call rate limited'

  const canRetry = remaining <= 0

  return (
    <div
      className={cn(
        'flex items-start gap-2.5 px-3 py-2.5 rounded-lg border text-xs',
        canRetry
          ? 'border-emerald-500/30 bg-emerald-500/5 text-emerald-400'
          : 'border-amber-500/30 bg-amber-500/5 text-amber-400',
      )}
      role="status"
      aria-live="polite"
    >
      {canRetry ? (
        <Clock size={13} className="mt-0.5 shrink-0 text-emerald-400" />
      ) : (
        <Warning size={13} className="mt-0.5 shrink-0 text-amber-400" weight="fill" />
      )}

      <div className="flex-1 min-w-0">
        {canRetry ? (
          <span className="font-medium">Retry available — rate limit cleared</span>
        ) : (
          <>
            <span className="font-medium">{label}</span>
            <span className="mx-1.5 text-[var(--color-muted)]">—</span>
            <span>
              Retry in{' '}
              <span className="font-mono font-semibold">{formatSeconds(remaining)}</span>
            </span>
          </>
        )}
      </div>

      <button tabIndex={0}
        type="button"
        onClick={onDismiss}
        className="shrink-0 text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors mt-0.5"
        aria-label="Dismiss rate limit notice"
      >
        <X size={12} />
      </button>
    </div>
  )
}
