// RecapModelSlot.tsx — one recap model slot (the recap model or one recap
// fallback) with its effort chip (effort-levels-spec-r10.md FD-19, FR-047,
// FR-053, FR-054, S-60, S-93).
//
// Owns the slot's model-switch transaction (useEffortSwitch): a picked model
// maps the slot's effort against the served catalog, a blocked result opens
// the pick-a-level step in the chip, and a completed switch (or a chip pick)
// is handed to the section as ONE slot commit, which the section autosaves.
// Recap slots default to Off and never show Not set.

import { useCallback, type ReactNode, type Ref } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { EffortControl, type EffortValue } from '@/components/ui/model-effort-picker'
import { providersCatalogQueryOptions } from '@/lib/providersCatalogQuery'
import { findServedRow } from '@/lib/effort/servedReasoning'
import { useEffortSwitch, type EffortPair } from '@/lib/effort/useEffortSwitch'

export interface RecapSlotHandlers {
  onChange: (model: string) => void
  onPairChange: (pair: EffortPair) => void
}

interface RecapModelSlotProps {
  value: EffortValue
  scope: 'recap-model' | 'recap-fallback'
  /** Accessible name prefix of the chip, e.g. "Effort for recap fallback 1, {model}". */
  contextName: string
  /** Another Memory write is in flight: the chip cannot be opened (FR-054). */
  disabled: boolean
  /** The mapping note of this slot's last switch, if any. */
  note: string | null
  chipRef?: Ref<HTMLButtonElement>
  /** Where focus returns when the blocked step is cancelled: the slot's model button. */
  modelTrigger: () => HTMLElement | null
  /** One slot change: the model pair and its effort, saved together. */
  onCommit: (pair: EffortPair, effort: string, note: string | null) => void
  /** Renders the slot: the model selector wired to `handlers`, and the chip. */
  children: (parts: { handlers: RecapSlotHandlers; chip: ReactNode }) => ReactNode
}

export function RecapModelSlot({ value, scope, contextName, disabled, note, chipRef, modelTrigger, onCommit, children }: RecapModelSlotProps) {
  const catalogQuery = useQuery(providersCatalogQueryOptions())
  const catalog = catalogQuery.data
  const lookup = useCallback((pair: EffortPair) => findServedRow(catalog, pair.provider, pair.model), [catalog])
  const effortSwitch = useEffortSwitch({
    value,
    defaultEffort: 'off',
    lookup,
    onCommit,
    ready: catalogQuery.status === 'success',
  })

  // #1291 / FR-063: a slot saved without its provider (old data) cannot be
  // matched to one catalog row; the picker asks for the provider first and
  // offers effort only once the pair is stored.
  const providerCandidates =
    value.model !== '' && value.provider === '' && catalog
      ? catalog.providers.filter((p) => p.models.some((m) => m.id === value.model))
      : []
  const askProvider =
    effortSwitch.pending === null && providerCandidates.length > 0 ? (
      <span className="inline-flex flex-wrap items-center gap-[var(--space-2)]" data-testid={`${scope}-ask-provider`}>
        <span className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
          {`Choose the provider for ${value.model} before setting its effort.`}
        </span>
        {providerCandidates.map((candidate) => (
          <Button
            key={candidate.id}
            type="button"
            variant="outline"
            size="sm"
            disabled={disabled}
            onClick={() => onCommit({ provider: candidate.id, model: value.model }, value.effort, null)}
          >
            {`Use ${candidate.name}`}
          </Button>
        ))}
      </span>
    ) : null

  const chip = askProvider ?? (
    value.model === '' && effortSwitch.pending === null ? null : (
      <EffortControl
        scope={scope}
        value={value}
        defaultEffort="off"
        contextName={contextName}
        disabled={disabled}
        pendingSwitch={effortSwitch.pending}
        onSwitchComplete={effortSwitch.completePending}
        onSwitchCancel={effortSwitch.cancelPending}
        cancelFocusTarget={modelTrigger}
        note={note}
        announcement={effortSwitch.announcement}
        chipRef={chipRef}
        onChange={(effort) => onCommit({ provider: value.provider, model: value.model }, effort, null)}
      />
    )
  )

  return <>{children({ handlers: effortSwitch.selectorHandlers, chip })}</>
}
