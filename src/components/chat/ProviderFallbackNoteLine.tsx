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

const MODEL_SETTINGS_HINT = "Pick a new model in the agent's settings."

export function ProviderFallbackNoteLine({
  message,
  pickNewModelHint,
}: {
  message: string
  pickNewModelHint?: boolean
}) {
  // Stored retired notes already end with the hint; live notes carry it as
  // a flag. Preserve the stored message in state and present both carriers
  // with one shared hint line, without rewriting saved copy or model facts.
  const noteText = pickNewModelHint && message.endsWith(MODEL_SETTINGS_HINT)
    ? message.slice(0, -MODEL_SETTINGS_HINT.length).trimEnd()
    : message
  return (
    <div
      data-testid="provider-fallback-note"
      className="flex min-w-0 flex-col gap-[var(--space-0-5)] px-[var(--space-3)] py-[var(--space-0-5)]"
    >
      <div className="flex min-w-0 items-center gap-[var(--space-1)]">
        <ArrowRight size={12} weight="bold" aria-hidden className="shrink-0" />
        <span className="min-w-0 truncate text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
          {noteText}
        </span>
      </div>
      {pickNewModelHint && (
        <span className="min-w-0 truncate pl-[var(--space-3)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
          {MODEL_SETTINGS_HINT}
        </span>
      )}
    </div>
  )
}
