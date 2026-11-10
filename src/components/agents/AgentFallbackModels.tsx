// AgentFallbackModels.tsx — the agent editor's fallback chain (extracted from
// AgentProfile.tsx to keep that file inside its size budget), with one effort
// chip per fallback (effort-levels-spec-r10.md FR-019, FR-029, S-54).
//
// The wire shape is FallbackModelPair[]: a fallback is always a (provider,
// model) pair (#1340), and carries its own effort. An entry saved without a
// provider is shown as incomplete and never re-sent.

import { ArrowDown, ArrowUp, CaretDown, Lock, Warning, X } from '@phosphor-icons/react'
import { IconButton } from '@/components/ui/icon-button'
import { ModelSelector } from '@/components/ui/model-selector'
import { EffortControl } from '@/components/ui/model-effort-picker'
import type { FallbackModel, Provider } from '@/lib/api'

/**
 * Editor's fallback entry — `FallbackModel` (the read shape) with `provider`
 * narrowed to a string. `''` marks an INCOMPLETE entry: a legacy fallback
 * saved without its provider (issue #1340). It is shown as incomplete and is
 * never sent; the editor itself only ever creates full pairs.
 */
export type FallbackEntry = FallbackModel & { provider: string }

/** A fallback is a (provider, model) pair — never a model alone (#1340, founder rule). */
export function isCompleteFallback(entry: FallbackEntry): boolean {
  return entry.provider.trim() !== '' && entry.model.trim() !== ''
}

interface ProviderGroup {
  providerName: string
  providerId: string
  models: string[]
}

interface AgentFallbackModelsProps {
  fallbackModels: FallbackEntry[]
  editable: boolean
  usableProviders: Provider[]
  availableModels: string[]
  providerGroups: ProviderGroup[]
  onAdd: (model: string, provider?: string) => void
  onRemove: (model: string) => void
  onMove: (model: string, direction: -1 | 1) => void
  onSetProvider: (model: string, provider: string) => void
  /** A fallback's effort was picked (saved at once by the editor). */
  onSetEffort: (model: string, effort: string) => void
}

export function AgentFallbackModels({
  fallbackModels,
  editable,
  usableProviders,
  availableModels,
  providerGroups,
  onAdd,
  onRemove,
  onMove,
  onSetProvider,
  onSetEffort,
}: AgentFallbackModelsProps) {
  return (
    <section className="space-y-[var(--space-2-5)]">
      <p className="font-headline font-semibold text-[length:var(--type-body-size)] text-[var(--color-secondary)]">Fallback models</p>
      <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Tried in order if the primary model fails.</p>
      {!editable ? (
        <div
          data-testid="fallback-summary-locked-basics"
          className="space-y-[var(--space-2)] p-[var(--space-2-5)] rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)]"
        >
          <div className="flex items-center gap-[var(--space-2)] text-[var(--color-muted)]">
            <Lock size={12} weight="fill" aria-hidden="true" />
            <p className="text-[length:var(--type-caption-size)]">
              Locked: fallback models are inherited from the locked core config.
            </p>
          </div>
          {fallbackModels.length === 0 ? (
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">No fallback chain configured.</p>
          ) : (
            <ol className="space-y-[var(--space-1)]" data-testid="fallback-summary-locked-basics-list">
              {fallbackModels.map((entry, idx) => (
                <li
                  key={entry.model}
                  className="flex items-center gap-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-mono text-[var(--color-secondary)]"
                >
                  <span className="text-[var(--color-muted)] w-4 shrink-0 text-right">{idx + 1}.</span>
                  <span
                    data-testid={`fallback-summary-provider-${entry.model}`}
                    className="inline-flex items-center px-[var(--space-1)] rounded text-[length:var(--type-caption-size)] font-semibold"
                    style={{
                      backgroundColor: 'color-mix(in srgb, var(--color-accent) 15%, transparent)',
                      color: 'var(--color-accent)',
                      border: '1px solid color-mix(in srgb, var(--color-accent) 30%, transparent)',
                    }}
                  >
                    {entry.provider || '—'}
                  </span>
                  <span data-testid={`fallback-summary-model-${entry.model}`}>{entry.model}</span>
                </li>
              ))}
            </ol>
          )}
        </div>
      ) : (
        <div className="flex flex-wrap gap-[var(--space-1)] p-[var(--space-2)] rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] min-h-[36px]">
          {fallbackModels.map((entry, idx) => {
            // #1340: '' = an incomplete legacy entry (saved without its provider).
            const providerMissing = entry.provider === ''
            const providerLabel = providerMissing
              ? 'No provider'
              : (usableProviders.find((p) => p.id === entry.provider)?.display_name
                  ?? usableProviders.find((p) => p.id === entry.provider)?.name
                  ?? entry.provider)
            return (
              <span
                key={entry.model}
                data-testid={`fallback-chip-model-${entry.model}`}
                className="inline-flex items-center gap-[var(--space-1)] px-[var(--space-2)] py-[var(--space-0-5)] rounded text-[length:var(--type-caption-size)] font-mono bg-[var(--color-surface-2)] text-[var(--color-secondary)] border border-[var(--color-border)]"
              >
                <span
                  data-testid={`fallback-chip-provider-${entry.model}`}
                  className="inline-flex items-center px-[var(--space-1)] rounded text-[length:var(--type-caption-size)] font-semibold"
                  style={{
                    backgroundColor: 'color-mix(in srgb, var(--color-accent) 15%, transparent)',
                    color: 'var(--color-accent)',
                    border: '1px solid color-mix(in srgb, var(--color-accent) 30%, transparent)',
                  }}
                >
                  {providerLabel}
                </span>
                <span className="relative inline-block">
                  <select tabIndex={0}
                    data-testid={`fallback-provider-select-${entry.model}`}
                    aria-label={`Provider for fallback ${entry.model}`}
                    value={entry.provider}
                    disabled={!editable}
                    onChange={(e) => onSetProvider(entry.model, e.target.value)}
                    className="appearance-none bg-transparent text-[var(--color-muted)] hover:text-[var(--color-secondary)] pl-[var(--space-1)] pr-[var(--space-2-5)] py-0 text-[length:var(--type-caption-size)] focus-visible:border-[var(--color-accent)] rounded cursor-pointer"
                  >
                    {/* No empty choice (#1340): an incomplete entry shows a
                        placeholder it can only leave by choosing a provider. */}
                    {providerMissing && (
                      <option value="" disabled data-testid={`fallback-provider-option-placeholder-${entry.model}`}>Choose provider</option>
                    )}
                    {usableProviders.map((p) => (
                      <option
                        key={p.id}
                        value={p.id}
                        data-testid={`fallback-provider-option-${p.id}-${entry.model}`}
                      >
                        {p.display_name ?? p.name ?? p.id}
                      </option>
                    ))}
                  </select>
                  <CaretDown
                    size={9}
                    className="pointer-events-none absolute right-0.5 top-1/2 -translate-y-1/2 text-[var(--color-muted)]"
                    aria-hidden="true"
                  />
                </span>
                <span>{entry.model}</span>
                {!providerMissing && (
                  <EffortControl
                    scope="agent-fallback"
                    value={{ provider: entry.provider, model: entry.model, effort: entry.reasoning_effort ?? '' }}
                    contextName={`Effort for fallback ${idx + 1}, ${entry.model}`}
                    onChange={(effort) => onSetEffort(entry.model, effort)}
                  />
                )}
                <IconButton
                  variant="ghost"
                  size="sm"
                  data-testid={`fallback-chip-up-${entry.model}`}
                  aria-label={`Move fallback ${entry.model} up`}
                  disabled={!editable || idx === 0}
                  onClick={() => onMove(entry.model, -1)}
                  className="h-auto w-auto text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)] disabled:hover:text-[var(--color-muted)] pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]"
                >
                  <ArrowUp size={10} />
                </IconButton>
                <IconButton
                  variant="ghost"
                  size="sm"
                  data-testid={`fallback-chip-down-${entry.model}`}
                  aria-label={`Move fallback ${entry.model} down`}
                  disabled={!editable || idx === fallbackModels.length - 1}
                  onClick={() => onMove(entry.model, 1)}
                  className="h-auto w-auto text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)] disabled:hover:text-[var(--color-muted)] pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]"
                >
                  <ArrowDown size={10} />
                </IconButton>
                <IconButton
                  variant="ghost"
                  size="sm"
                  data-testid={`fallback-chip-remove-${entry.model}`}
                  aria-label={`Remove fallback ${entry.model}`}
                  disabled={!editable}
                  onClick={() => onRemove(entry.model)}
                  className="h-auto w-auto text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-error)] pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]"
                >
                  <X size={10} />
                </IconButton>
                {providerMissing && (
                  <span
                    data-testid={`fallback-chip-warning-${entry.model}`}
                    className="inline-flex items-center gap-[var(--space-0-5)] text-[var(--color-warning)]"
                  >
                    <Warning size={11} weight="fill" aria-hidden="true" />
                    Incomplete — choose a provider or remove it
                  </span>
                )}
              </span>
            )
          })}
          <div className="min-w-[220px] flex-1">
            {/* UAT model-catalog fix: fallbacks must be real catalogue
                models, so this picker is constrained too. */}
            <ModelSelector
              models={availableModels}
              value=""
              onChange={(v) => onAdd(v)}
              // H6: the grouped pick's exact provider overrides the
              // first-wins lookup — see addFallbackFromSelector.
              onPairChange={({ model: m, provider: p }) => onAdd(m, p)}
              placeholder="Add fallback…"
              label="Add fallback model"
              providerGroups={providerGroups}
              triggerTestId="fallback-add-trigger"
              itemTestIdPrefix="fallback-add-item-"
              constrainToCatalog
              emptyCatalogHint="Connect a provider to add fallbacks"
              disabled={!editable}
            />
          </div>
        </div>
      )}
      {editable && fallbackModels.some((entry) => !isCompleteFallback(entry)) && (
        <p data-testid="fallback-incomplete-notice" className="mt-[var(--space-1)] text-[length:var(--type-utility-xs-size)] text-[var(--color-warning)]">
          A fallback without a provider is not used, and fallback changes are not saved until every fallback has a provider.
        </p>
      )}
    </section>
  )
}
