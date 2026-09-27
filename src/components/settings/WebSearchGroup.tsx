import { Card } from '@/components/ui/card'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import type { IntegrationProvider } from '@/lib/api'

// WebSearchGroup — the ADR-096 web-search role picker for Settings → Integrations.
//
// The operator chooses a default and a fallback; the group reports each
// provider's role and usability honestly (FR-012, FR-028, FR-031):
//   - a default radio stack and a separate fallback radio stack (FR-012),
//     with "No fallback" as a visible choice, not an unselected blank;
//   - the fallback option on the default's row disabled, with its reason
//     ("A provider cannot fall back to itself.") visible on the card;
//   - "Automatic fallback" labelling when the stored fallback is absent and
//     the R3 rule resolved DuckDuckGo (fallback_automatic on the wire);
//   - the R5 healing explanation when fallback_ignored_reason is present;
//   - the FR-031 notice when native model search is in effect — the group
//     names no provider as the one that answers;
//   - SearXNG offered as no new choice (ADR-096 D10 — descoped): it appears
//     in neither stack, and shows as the current default only as text.
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

export interface WebSearchGroupProps {
  /** The `search` rows of GET /api/v1/integrations/providers. */
  providers: IntegrationProvider[]
  /** `default_search` — absent when the roles are not yet decided (migration deferred). */
  defaultSearch?: string
  /** `fallback_search` — null means an explicit "No fallback"; absent means undecided. */
  fallbackSearch?: string | null
  /** `fallback_ignored_reason` — `same_as_default` when R5 healed the interpretation. */
  fallbackIgnoredReason?: string
  /** `native_search_in_effect` (FR-031). */
  nativeSearchInEffect?: boolean
  saving: boolean
  onSetDefault: (id: string) => void
  onSetFallback: (id: string) => void
  onSetNoFallback: () => void
}

export function WebSearchGroup({
  providers,
  defaultSearch,
  fallbackSearch,
  fallbackIgnoredReason,
  nativeSearchInEffect,
  saving,
  onSetDefault,
  onSetFallback,
  onSetNoFallback,
}: WebSearchGroupProps) {
  const choosable = providers.filter((p) => !NOT_CHOOSABLE.has(p.id))
  // A RadioGroup is controlled; '' matches no option, which the catalogued
  // component treats as "nothing checked" while keeping the group
  // keyboard-reachable (the first enabled item carries the Tab stop).
  const defaultValue = defaultSearch ?? ''
  // null ("No fallback") and undefined (undecided) must stay distinguishable
  // — null is a choice, absence is not. Collapsing either into the other is
  // the exact lie the spec calls out.
  const fallbackValue = fallbackSearch === null ? 'none' : (fallbackSearch ?? '')

  const handleDefaultSelect = (id: string) => {
    if (id === (defaultSearch ?? '')) return // already the default — no gated save
    onSetDefault(id)
  }
  const handleFallbackSelect = (value: string) => {
    if (value === 'none') {
      if (fallbackSearch !== null) onSetNoFallback()
      return
    }
    if (value === fallbackSearch) return // already the fallback
    onSetFallback(value)
  }

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

      <div className="grid gap-[var(--space-3)] md:grid-cols-2">
        <div className="space-y-[var(--space-2)]">
          <div className="text-[length:var(--type-utility-xs-size)] font-semibold uppercase tracking-wide text-[var(--color-muted)]">
            Default
          </div>
          {defaultSearch === 'searxng' && (
            <p
              className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]"
              data-testid="searxng-default-note"
            >
              Current default: SearXNG (self-hosted). Choose another provider to move the default — SearXNG cannot be
              newly chosen.
            </p>
          )}
          <RadioGroup
            data-testid="default-stack"
            aria-label="Web search default"
            value={defaultValue}
            onValueChange={handleDefaultSelect}
            disabled={saving}
            orientation="vertical"
            className="w-full"
          >
            {choosable.map((p) => (
              <RadioGroupItem key={p.id} value={p.id} data-testid={`default-option-${p.id}`} className="items-start">
                <span className="flex flex-col items-start gap-[var(--space-0-5)]">
                  <span>{p.display_name}</span>
                </span>
              </RadioGroupItem>
            ))}
          </RadioGroup>
        </div>

        <div className="space-y-[var(--space-2)]">
          <div className="text-[length:var(--type-utility-xs-size)] font-semibold uppercase tracking-wide text-[var(--color-muted)]">
            Fallback
          </div>
          <RadioGroup
            data-testid="fallback-stack"
            aria-label="Web search fallback"
            value={fallbackValue}
            onValueChange={handleFallbackSelect}
            disabled={saving}
            orientation="vertical"
            className="w-full"
          >
            {choosable.map((p) => {
              const isDefaultRow = p.id === defaultSearch
              const automatic = p.fallback_automatic === true
              return (
                <RadioGroupItem
                  key={p.id}
                  value={p.id}
                  data-testid={`fallback-option-${p.id}`}
                  disabled={isDefaultRow}
                  className="items-start"
                >
                  <span className="flex flex-col items-start gap-[var(--space-0-5)]">
                    <span>{p.display_name}</span>
                    {automatic && (
                      <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                        Automatic fallback
                      </span>
                    )}
                    {isDefaultRow && (
                      <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                        A provider cannot fall back to itself.
                      </span>
                    )}
                  </span>
                </RadioGroupItem>
              )
            })}
            <RadioGroupItem value="none" data-testid="fallback-option-none" className="items-start">
              <span className="flex flex-col items-start gap-[var(--space-0-5)]">
                <span>No fallback</span>
                <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                  The default gets the only try; a failure is a failure.
                </span>
              </span>
            </RadioGroupItem>
          </RadioGroup>
        </div>
      </div>
    </div>
  )
}
