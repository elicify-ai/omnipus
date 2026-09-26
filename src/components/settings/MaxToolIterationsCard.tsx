/**
 * MaxToolIterationsCard — Settings → Performance "Max tool calls per turn"
 * (docs/internal/specs/tool-iteration-limit-spec.md, User Story 1; founder
 * decisions D3, D8, D13, D17 in tool-iteration-limit-interview.md).
 *
 * Presentational only: PerformanceSection owns the value, the save flow (the
 * D11 lowering preview, the step-up gate, the D16 drift retry) and passes the
 * resulting messages in. This card renders:
 *   - the 1–1000 input inside a catalogued Field (bound errors, preview
 *     failures and lowering failures announced through Field's error region);
 *   - the D13/D17 warning when the value saved in config.json is missing or
 *     out of range, naming the saved value and the value actually in force;
 *   - the persistent F4 summary of the agents a save lowered, in its own
 *     role="status" region, until the admin next edits the field.
 */

import { Warning, Wrench } from '@phosphor-icons/react'
import { Card } from '@/components/ui/card'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import type { MaxToolIterationsSavedState } from '@/lib/api/generated/openapi-types'

export const MAX_TOOL_ITERATIONS_MIN = 1
export const MAX_TOOL_ITERATIONS_MAX = 1000

/** Parses a whole number within the 1–1000 bound; null for anything else. */
export function parseMaxToolIterations(raw: string): number | null {
  const trimmed = raw.trim()
  if (!/^\d+$/.test(trimmed)) return null
  const n = parseInt(trimmed, 10)
  return n >= MAX_TOOL_ITERATIONS_MIN && n <= MAX_TOOL_ITERATIONS_MAX ? n : null
}

export interface MaxToolIterationsCardProps {
  value: string
  onChange: (value: string) => void
  /** Bound violation, preview failure or save failure; null when none. */
  error: string | null
  /** The global limit in force, from GET /performance (undefined if absent). */
  inForce: number | undefined
  savedState: MaxToolIterationsSavedState | undefined
  savedRaw: number | undefined
  /** "Lowered N agents: …" after a save that lowered agents; null otherwise. */
  loweredSummary: string | null
}

// savedStateWarning words D13/D17's Settings warning. Text, not colour alone.
export function savedStateWarning(
  state: MaxToolIterationsSavedState | undefined,
  raw: number | undefined,
  inForce: number | undefined,
): string | null {
  if (state === undefined || state === 'ok') return null
  const using = inForce === undefined ? 'the default limit' : String(inForce)
  switch (state) {
    case 'missing':
      return `The limit is missing from config.json, so Omnipus is using ${using}. Saving a value here stores it.`
    case 'below_min':
      return `The limit saved in config.json (${raw ?? 'unknown'}) is below ${MAX_TOOL_ITERATIONS_MIN}, so Omnipus is using ${using} instead. The file has not been changed; saving a value here replaces it.`
    case 'above_max':
      return `The limit saved in config.json (${raw ?? 'unknown'}) is above ${MAX_TOOL_ITERATIONS_MAX}, so Omnipus is using ${using} instead. The file has not been changed; saving a value here replaces it.`
  }
}

export function MaxToolIterationsCard({
  value,
  onChange,
  error,
  inForce,
  savedState,
  savedRaw,
  loweredSummary,
}: MaxToolIterationsCardProps): React.ReactElement {
  const warning = savedStateWarning(savedState, savedRaw, inForce)

  return (
    <Card className="p-[var(--space-3)] space-y-[var(--space-2-5)]">
      <div className="flex items-center gap-[var(--space-2)]">
        <Wrench size={16} className="text-[var(--color-secondary)]" />
        <h3 className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">Tool calls per turn</h3>
      </div>

      {warning && (
        <div
          data-testid="performance-max-tool-iterations-saved-warning"
          className="flex items-start gap-[var(--space-2)] p-[var(--space-2)] rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 text-[length:var(--type-utility-xs-size)] text-[var(--color-warning)]"
        >
          <Warning size={14} className="mt-[var(--space-0-5)] shrink-0" />
          <span>{warning}</span>
        </div>
      )}

      <Field
        label="Max tool calls per turn"
        description={`How many tool steps any agent may take in one turn, from ${MAX_TOOL_ITERATIONS_MIN} to ${MAX_TOOL_ITERATIONS_MAX}. An agent can only lower this for itself, on its profile. Saving asks you to confirm the change.`}
        error={error}
      >
        <Input
          type="number"
          min={MAX_TOOL_ITERATIONS_MIN}
          max={MAX_TOOL_ITERATIONS_MAX}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className="w-24 h-7 text-[length:var(--type-body-compact-size)]"
          data-testid="performance-max-tool-iterations-input"
        />
      </Field>

      <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] leading-relaxed">
        Turns already running keep the limit they started with.
      </p>

      {loweredSummary && (
        <p
          role="status"
          data-testid="performance-max-tool-iterations-lowered-summary"
          className="text-[length:var(--type-utility-xs-size)] text-[var(--color-secondary)]"
        >
          {loweredSummary}
        </p>
      )}
    </Card>
  )
}
