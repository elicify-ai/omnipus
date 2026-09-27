import { Card } from '@/components/ui/card'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { cn } from '@/lib/utils'
import type { IntegrationProvider } from '@/lib/api'

// WebSearchGroup — the ADR-096 web-search role-picker pieces for
// Settings → Integrations, drawn the way the spec draws them: ONE row per
// provider, and the role radios live ON the provider's row (spec § Settings
// screen, "Default" / "Fallback" / "Same row" — the two radio groups exist
// semantically across rows, not as separate visual lists, which duplicated
// every provider name and broke the section's own tests).
//
// Three exported pieces, composed by IntegrationsSection:
//   - WebSearchGroup           — the group surface: help text + conditional
//                                notices (FR-012, FR-028, FR-031, R5, D10),
//                                rendered once above the rows;
//   - WebSearchRowRoles        — one row's default + fallback radios. The
//                                fallback radio on the default's row is
//                                disabled, with its reason visible on the row
//                                ("A provider cannot fall back to itself.");
//                                the R3 automatic fallback is labelled;
//   - WebSearchNoFallbackChoice — the visible "No fallback" choice — an
//                                option of the fallback group, rendered after
//                                the rows. Not an unselected blank.
//
// SearXNG is offered as no new choice (ADR-096 D10 — descoped): it appears
// with no radios at all, and shows as the current default only as text.
// Depth and site-filter controls are deliberately absent: the landed
// contract carries no depth or capability field, so any control here would
// offer a value nothing would read — exactly what US-5 forbids.
//
// Wire types come from src/lib/api/generated (Hard Constraint #8); this file
// never re-declares them.

// Provider ids the screen does not offer as a new default or fallback.
// SearXNG is the one descoped provider (ADR-096 D10): the migration may have
// recorded it as the resolved default, so the screen shows it as the current
// default as text, but offers no editor for it.
const NOT_CHOOSABLE: ReadonlySet<string> = new Set(['searxng'])

/** The row grid shared by the provider rows, the column-header row, and the
 *  "No fallback" choice line, so the radio columns align across all of them. */
export const WEB_SEARCH_ROW_GRID =
  'grid grid-cols-[minmax(0,1fr)_auto_auto_auto] items-center gap-x-[var(--space-2-5)]'

/** The radio circle's checked state — accent-ringed dot. Tokens only; the
 *  catalogued RadioGroupItem contributes the hit target and state machine. */
function RoleRadioDot({ checked }: { checked: boolean }) {
  return (
    <span
      aria-hidden="true"
      data-testid="role-radio-dot"
      className={cn(
        'block h-[var(--space-3)] w-[var(--space-3)] rounded-full border',
        checked
          ? 'border-[var(--color-accent)] bg-[var(--color-accent)]'
          : 'border-[var(--color-muted)] bg-transparent',
      )}
    />
  )
}

export interface WebSearchGroupProps {
  /** `default_search` — absent when the roles are not yet decided (migration deferred). */
  defaultSearch?: string
  /** `fallback_ignored_reason` — `same_as_default` when R5 healed the interpretation. */
  fallbackIgnoredReason?: string
  /** `native_search_in_effect` (FR-031). */
  nativeSearchInEffect?: boolean
}

/** The group-level surface: help text + notices. Rendered once, above the
 *  provider rows. */
export function WebSearchGroup({
  defaultSearch,
  fallbackIgnoredReason,
  nativeSearchInEffect,
}: WebSearchGroupProps) {
  return (
    <div className="space-y-[var(--space-3)]" data-testid="websearch-group">
      <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
        The order of this list does not choose who is tried first. You choose the default and the fallback;
        the default gets the first try, and the fallback one retry on a retryable failure.
      </p>

      {defaultSearch === undefined && (
        <Card className="p-[var(--space-3)]" data-testid="roles-undecided">
          <p className="text-[length:var(--type-body-compact-size)]">
            <span className="font-medium">Search roles are not decided yet.</span>{' '}
            <span className="text-[var(--color-muted)]">
              The provider-role migration is deferred on this install, so no default or fallback is recorded.
              These controls become effective once the migration completes.
            </span>
          </p>
        </Card>
      )}

      {nativeSearchInEffect === true && (
        <Card className="p-[var(--space-3)]" role="status" data-testid="native-search-notice">
          <p className="text-[length:var(--type-body-compact-size)]">
            <span className="font-medium">Native model search is in effect for the active model.</span>{' '}
            <span className="text-[var(--color-muted)]">
              The model does its own web searching, so these settings are not currently deciding who answers.
            </span>
          </p>
        </Card>
      )}

      {fallbackIgnoredReason && (
        <Card className="p-[var(--space-3)]" role="status" data-testid="fallback-ignored-notice">
          <p className="text-[length:var(--type-body-compact-size)]">
            <span className="font-medium">The stored fallback names the default provider, so it is ignored.</span>{' '}
            <span className="text-[var(--color-muted)]">
              A provider cannot fall back to itself. No fallback is in effect.
            </span>
          </p>
        </Card>
      )}
    </div>
  )
}

export interface WebSearchRowRolesProps {
  /** The row's provider. */
  provider: IntegrationProvider
  /** `default_search` — absent when undecided. */
  defaultSearch?: string
  /** `fallback_search` — null means an explicit "No fallback"; absent means undecided. */
  fallbackSearch?: string | null
  saving: boolean
  onSetDefault: (id: string) => void
  onSetFallback: (id: string) => void
}

/** One search row's role radios — the default radio and the fallback radio
 *  the spec puts on every provider row. Returns a two-cell fragment so the
 *  row's grid can place each radio in its labelled column. A not-choosable
 *  provider (SearXNG) renders nothing: no radios, no cells.
 *
 *  Each radio sits in its own single-option catalogued RadioGroup — one DOM
 *  row cannot be a descendant of the two cross-row groups the roles imply,
 *  so the group semantics (one default across all rows, one fallback across
 *  all rows) are carried by the controlled values and the per-group labels,
 *  not by one shared group container. Clicking an already-selected radio
 *  fires nothing: a selection that changes nothing must not open a gated
 *  save. */
export function WebSearchRowRoles({
  provider,
  defaultSearch,
  fallbackSearch,
  saving,
  onSetDefault,
  onSetFallback,
}: WebSearchRowRolesProps) {
  if (NOT_CHOOSABLE.has(provider.id)) return null

  const isDefaultRow = provider.id === defaultSearch
  const isFallbackRow = fallbackSearch === provider.id
  const automatic = provider.fallback_automatic === true

  const handleDefaultSelect = (value: string) => {
    if (value === (defaultSearch ?? '')) return // already the default — no gated save
    onSetDefault(value)
  }
  const handleFallbackSelect = (value: string) => {
    if (value === (fallbackSearch ?? '')) return // already the fallback — no gated save
    onSetFallback(value)
  }

  return (
    <>
      <div className="flex items-center justify-center">
        <RadioGroup
          value={isDefaultRow ? provider.id : ''}
          onValueChange={handleDefaultSelect}
          aria-label={`Web search default — ${provider.display_name}`}
          disabled={saving}
          className="w-auto"
        >
          <RadioGroupItem
            value={provider.id}
            data-testid={`default-radio-${provider.id}`}
            aria-label={`Set ${provider.display_name} as the web search default`}
            className="h-[var(--space-4)] w-[var(--space-4)] justify-center rounded-full p-0"
          >
            <RoleRadioDot checked={isDefaultRow} />
          </RadioGroupItem>
        </RadioGroup>
      </div>
      <div className="flex flex-col items-center gap-[var(--space-0-5)]">
        <RadioGroup
          value={isFallbackRow ? provider.id : ''}
          onValueChange={handleFallbackSelect}
          aria-label={`Web search fallback — ${provider.display_name}`}
          disabled={saving}
          className="w-auto"
        >
          <RadioGroupItem
            value={provider.id}
            disabled={isDefaultRow}
            data-testid={`fallback-radio-${provider.id}`}
            aria-label={`Set ${provider.display_name} as the web search fallback`}
            className="h-[var(--space-4)] w-[var(--space-4)] justify-center rounded-full p-0"
          >
            <RoleRadioDot checked={isFallbackRow} />
          </RadioGroupItem>
        </RadioGroup>
        {isDefaultRow && (
          <span
            data-testid={`fallback-disabled-reason-${provider.id}`}
            className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] text-center"
          >
            A provider cannot fall back to itself.
          </span>
        )}
        {!isDefaultRow && automatic && (
          <span
            data-testid={`automatic-fallback-${provider.id}`}
            className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] text-center"
          >
            Automatic fallback
          </span>
        )}
      </div>
    </>
  )
}

export interface WebSearchNoFallbackChoiceProps {
  /** `fallback_search` — null means this choice is the checked one. */
  fallbackSearch?: string | null
  saving: boolean
  onSetNoFallback: () => void
}

/** The visible "No fallback" choice — the extra option of the fallback
 *  group, rendered after the provider rows. Spec § Settings screen,
 *  "Fallback": "A second radio, plus a visible 'No fallback' choice. Not an
 *  unselected blank." Clicking it when an explicit none is already stored
 *  fires nothing. */
export function WebSearchNoFallbackChoice({
  fallbackSearch,
  saving,
  onSetNoFallback,
}: WebSearchNoFallbackChoiceProps) {
  const isNone = fallbackSearch === null
  return (
    <div
      className={WEB_SEARCH_ROW_GRID}
      data-testid="no-fallback-choice"
    >
      <div className="min-w-0">
        <div className="text-[length:var(--type-body-compact-size)] font-medium text-[var(--color-secondary)]">
          No fallback
        </div>
        <div className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
          The default gets the only try; a failure is a failure.
        </div>
      </div>
      <span aria-hidden="true" />
      <div className="flex items-center justify-center">
        <RadioGroup
          value={isNone ? 'none' : ''}
          onValueChange={(value) => {
            if (value === 'none' && fallbackSearch !== null) onSetNoFallback()
          }}
          aria-label="Web search fallback — none"
          disabled={saving}
          className="w-auto"
        >
          <RadioGroupItem
            value="none"
            data-testid="fallback-radio-none"
            aria-label="No fallback — the default gets the only try"
            className="h-[var(--space-4)] w-[var(--space-4)] justify-center rounded-full p-0"
          >
            <RoleRadioDot checked={isNone} />
          </RadioGroupItem>
        </RadioGroup>
      </div>
      <span aria-hidden="true" />
    </div>
  )
}
