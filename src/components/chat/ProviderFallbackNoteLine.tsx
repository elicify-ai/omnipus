/**
 * ProviderFallbackNoteLine — provider-messages spec §6 (US-5, D12): the grey
 * event-line rendering the failover note — exact server-assembled text,
 * "Answered by the Fallback model ({X}) because {Y} was unavailable."
 * (never "backup"), plus the D12 hint ONLY for
 * `unavailable_code === 'model_retired'`.
 *
 * Grammar matches `DelegationEventLine.tsx` (muted caption line, Phosphor
 * mark, no card): this is the same family of system event line.
 */
import { ArrowRight } from '@phosphor-icons/react'

export function ProviderFallbackNoteLine({
  message,
  pickNewModelHint,
}: {
  message: string
  pickNewModelHint?: boolean
}) {
  return (
    <div
      data-testid="provider-fallback-note"
      className="flex min-w-0 flex-col gap-[var(--space-0-5)] px-[var(--space-3)] py-[var(--space-0-5)]"
    >
      <div className="flex min-w-0 items-center gap-[var(--space-1)]">
        <ArrowRight size={12} weight="bold" aria-hidden className="shrink-0" />
        <span className="min-w-0 truncate text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
          {message}
        </span>
      </div>
      {pickNewModelHint && (
        <span className="min-w-0 truncate pl-[var(--space-3)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
          Pick a new model in the agent's settings.
        </span>
      )}
    </div>
  )
}
