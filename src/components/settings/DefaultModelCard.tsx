// DefaultModelCard.tsx — ADR-068 US-4 / FR-019.
//
// The FIRST card on Settings → Providers, and the ONLY place the global
// default model is changed (ADR-068 §4 as amended, X-37). It reads
// `provider · model` and `window · source` from GET /providers/default-model,
// and *Change* opens the shared ModelSelector filtered to providers that can
// actually serve a turn (`connected | signed_in`, FR-019).
//
// Two states this card must tell apart, because conflating them is how an
// operator ends up believing a number that is not true:
//
//  • A field the server did not send (window or its source — ADR-066's
//    ResolveWindow had nothing to say, or the row is an exempt subprocess CLI
//    reporting 0) renders as an em dash. Not "unknown", not a guess.
//  • `window_unknown: true` is the specific, actionable case (a local model
//    whose endpoint reported no context length): it renders the *No context
//    length* copy plus a link into Settings → Models → Model overrides with
//    the pair pre-filled, so the fix is one click away (X-08).

import * as React from 'react'
import { Button } from '@/components/ui/button'
import { ModelSelector } from '@/components/ui/model-selector'
import { buildProviderModelGroups, providerDisplayName } from '@/lib/providerModelGroups'
import { USABLE_PROVIDER_STATUSES } from '@/lib/providerStatus'
import {
  MODEL_OVERRIDE_LINK_TEXT,
  NO_CONTEXT_LENGTH_COPY,
  modelOverrideHref,
} from '@/lib/modelOverrideLink'
import { catalogEntryById } from '@/lib/catalogDisplay'
import type {
  DefaultModel,
  DefaultModelUpdateRequest,
  Provider,
  ProvidersCatalog,
} from '@/lib/api/generated/openapi-types'

/** What an absent window or window source renders as. */
export const ABSENT_FIELD = '—'

/** Thousands-separated, locale-independent so CI reads the same as a browser. */
const WINDOW_FORMAT = new Intl.NumberFormat('en-US')

export interface DefaultModelCardProps {
  /**
   * The current pair. `null` when the gateway has none yet — a fresh install
   * answers the GET with 404 until onboarding's explicit pick writes one.
   */
  defaultModel: DefaultModel | null
  /** Every configured row, for the display name and the *Change* selector. */
  providers: readonly Provider[]
  /** Registry-fed catalog document (ADR-068 FR-037). */
  catalog?: ProvidersCatalog | null
  /** Fired when the operator picks a new pair; the caller owns the PUT. */
  onChange: (pair: DefaultModelUpdateRequest) => void
  /** True while that PUT is in flight. */
  isSaving?: boolean
  /** GET state — `loading` renders the skeleton rather than a false "not set". */
  status?: 'loading' | 'ready'
  /**
   * Pre-selects one provider in the *Change* selector (FR-019's row action,
   * *Set as default model…*). Undefined = every usable provider is offered.
   */
  filterToProviderId?: string
  /** Externally controlled open state, so a row action can open this card's selector. */
  changing?: boolean
  onChangingChange?: (changing: boolean) => void
}

export function DefaultModelCard({
  defaultModel,
  providers,
  catalog,
  onChange,
  isSaving = false,
  status = 'ready',
  filterToProviderId,
  changing,
  onChangingChange,
}: DefaultModelCardProps) {
  const [internalChanging, setInternalChanging] = React.useState(false)
  const isControlled = changing !== undefined
  const open = isControlled ? changing : internalChanging
  const setOpen = (next: boolean) => {
    if (isControlled) onChangingChange?.(next)
    else setInternalChanging(next)
  }

  // Only providers that can serve a turn are offerable (FR-019).
  const usable = React.useMemo(
    () => providers.filter((p) => USABLE_PROVIDER_STATUSES.includes(p.status)),
    [providers],
  )
  const groups = React.useMemo(
    () => buildProviderModelGroups({ providers: usable, catalog }),
    [usable, catalog],
  )

  const backingRow = defaultModel
    ? providers.find((p) => p.id === defaultModel.provider)
    : undefined
  const providerLabel = defaultModel
    ? backingRow
      ? providerDisplayName(backingRow, catalogEntryById(catalog?.providers ?? [], backingRow.id))
      : defaultModel.provider
    : ABSENT_FIELD

  // 0 is the exempt-row encoding (a subprocess CLI has no window), not a
  // window of zero tokens — both it and an absent field read as the em dash.
  const window =
    defaultModel?.context_window && defaultModel.context_window > 0
      ? WINDOW_FORMAT.format(defaultModel.context_window)
      : ABSENT_FIELD
  const source = defaultModel?.window_source ?? ABSENT_FIELD
  const windowUnknown = defaultModel?.window_unknown === true

  return (
    <div
      className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] px-4 py-3"
      data-testid="default-model-card"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-xs font-semibold uppercase tracking-wide text-[var(--color-muted)]">
            Default model
          </p>

          {status === 'loading' ? (
            <div
              className="mt-1.5 h-4 w-40 animate-pulse rounded bg-[var(--color-surface-2)]"
              data-testid="default-model-loading"
            />
          ) : defaultModel ? (
            <p className="mt-1 text-sm text-[var(--color-secondary)]">
              <span data-testid="default-model-provider">{providerLabel}</span>
              {' · '}
              <span className="font-mono text-xs" data-testid="default-model-model">
                {defaultModel.model}
              </span>
            </p>
          ) : (
            <p className="mt-1 text-sm text-[var(--color-muted)]" data-testid="default-model-unset">
              No default model yet — pick one so new agents have somewhere to run.
            </p>
          )}

          {status !== 'loading' && defaultModel && (
            <p className="mt-0.5 text-xs text-[var(--color-muted)]">
              {windowUnknown ? (
                <>
                  <span data-testid="default-model-window">{NO_CONTEXT_LENGTH_COPY}</span>{' '}
                  <a
                    tabIndex={0}
                    href={modelOverrideHref(defaultModel.provider, defaultModel.model)}
                    data-testid="default-model-window-unknown-link"
                    className="underline underline-offset-2"
                    style={{ color: 'var(--color-accent)' }}
                  >
                    {MODEL_OVERRIDE_LINK_TEXT}
                  </a>
                </>
              ) : (
                <>
                  <span data-testid="default-model-window">{window}</span>
                  {' · '}
                  <span data-testid="default-model-source">{source}</span>
                </>
              )}
            </p>
          )}
        </div>

        <Button
          size="sm"
          variant="outline"
          className="h-7 shrink-0 px-3 text-xs"
          onClick={() => setOpen(!open)}
          disabled={isSaving}
          data-testid="default-model-change-btn"
        >
          Change
        </Button>
      </div>

      {open && (
        <div className="mt-3" data-testid="default-model-selector-wrap">
          <ModelSelector
            models={[]}
            value={defaultModel?.model ?? ''}
            constrainToCatalog
            catalogGroups={groups}
            filterProviders={{
              statuses: [...USABLE_PROVIDER_STATUSES],
              ...(filterToProviderId ? { providerIds: [filterToProviderId] } : {}),
            }}
            label="Default model"
            triggerTestId="default-model-select"
            itemTestIdPrefix="default-model-option-"
            emptyCatalogHint="No connected provider has models to offer"
            onChange={() => {
              /* the pair is what matters — see onPairChange */
            }}
            onPairChange={({ model, provider }) => {
              if (!provider || !model) return
              setOpen(false)
              onChange({ provider, model })
            }}
          />
        </div>
      )}
    </div>
  )
}
