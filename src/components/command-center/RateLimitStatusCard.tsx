import { useQuery } from '@tanstack/react-query'
import { Gauge, ArrowClockwise, Warning } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { fetchRateLimits } from '@/lib/api'
import { cn } from '@/lib/utils'

function formatLimit(value: number | undefined, unit: string): string {
  return value && value > 0 ? `${value}/${unit}` : 'No limit'
}

export function RateLimitStatusCard() {
  const { data, isLoading, isError, refetch, isFetching } = useQuery({
    queryKey: ['rate-limits'],
    queryFn: fetchRateLimits,
    refetchInterval: 10_000,
  })

  function renderBody() {
    if (isError) {
      return (
        <div className="flex items-center gap-1.5 text-xs text-[var(--color-error)]">
          <Warning size={12} />
          <span>Could not load rate limit status.</span>
          <button
            type="button"
            onClick={() => void refetch()}
            className="underline hover:no-underline ml-1"
          >
            Retry
          </button>
        </div>
      )
    }

    if (isLoading) {
      return (
        <div className="space-y-2">
          <div className="h-2 w-full rounded-full bg-[var(--color-surface-2)] animate-pulse" />
          <div className="flex gap-4">
            <div className="h-3 w-24 rounded bg-[var(--color-surface-2)] animate-pulse" />
            <div className="h-3 w-24 rounded bg-[var(--color-surface-2)] animate-pulse" />
          </div>
        </div>
      )
    }

    // GET returns daily_cost_cap (no _usd suffix) per RateLimitsResponse schema.
    const hasCap = data?.daily_cost_cap && data.daily_cost_cap > 0
    const hasLlmLimit = data?.max_agent_llm_calls_per_hour && data.max_agent_llm_calls_per_hour > 0
    const hasToolLimit = data?.max_agent_tool_calls_per_minute && data.max_agent_tool_calls_per_minute > 0

    if (!hasCap && !hasLlmLimit && !hasToolLimit) {
      return (
        <p className="text-xs text-[var(--color-muted)]">
          No rate limits configured.
        </p>
      )
    }

    return (
      <div className="space-y-2.5">
        {hasCap && (
          <div className="flex items-center justify-between text-[10px] text-[var(--color-muted)]">
            <span>Daily cost cap</span>
            <span className="text-[var(--color-secondary)]">
              ${data!.daily_cost_cap!.toFixed(2)}
            </span>
          </div>
        )}

        {/* Per-agent limits */}
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-[10px] text-[var(--color-muted)]">
          <span>
            LLM calls:{' '}
            <span className="text-[var(--color-secondary)]">
              {formatLimit(data?.max_agent_llm_calls_per_hour, 'hour')}
            </span>
          </span>
          <span>
            Tool calls:{' '}
            <span className="text-[var(--color-secondary)]">
              {formatLimit(data?.max_agent_tool_calls_per_minute, 'minute')}
            </span>
          </span>
        </div>
      </div>
    )
  }

  return (
    <div className="border-b border-[var(--color-border)] px-4 py-3">
      {/* Header */}
      <div className="flex items-center justify-between mb-2">
        <div className="flex items-center gap-1.5">
          <Gauge size={13} className="text-[var(--color-muted)]" />
          <span className="text-xs font-semibold text-[var(--color-secondary)]">Rate Limits</span>
        </div>
        <Button
          variant="ghost"
          size="icon"
          className="h-6 w-6 text-[var(--color-muted)] hover:text-[var(--color-secondary)]"
          onClick={() => void refetch()}
          disabled={isFetching}
          aria-label="Refresh rate limit status"
          title="Refresh"
        >
          <ArrowClockwise size={12} className={cn(isFetching && 'animate-spin')} />
        </Button>
      </div>

      {renderBody()}
    </div>
  )
}
