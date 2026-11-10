import { useState } from 'react'
import { CaretUpDown, Check } from '@phosphor-icons/react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Command, CommandList, CommandItem } from '@/components/ui/command'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import type { IntegrationProvider } from '@/lib/api'

// WebSearchGroup — #1055's card + Change design for Settings → Integrations,
// replacing ADR-096's one-row-per-provider radios (#1055 Approved design;
// #1056 additionally removes the retired local-search provider offering,
// which the backend no longer sends).
//
// Two exported pieces, composed by IntegrationsSection:
//   - WebSearchGroup       — the group surface: intro copy, the undecided /
//                            native-search / fallback-ignored notices (FR-012,
//                            FR-028, FR-031, R5, D10), and the Default search /
//                            Fallback cards, in the DefaultModelCard visual
//                            pattern (card + current choice + status +
//                            "Change" button opening a catalogued Popover +
//                            Command combobox — the same primitives
//                            DefaultModelCard's ModelSelector is built from).
//   - orderSearchProviders — ready-first, stable ordering for the service
//                            list IntegrationsSection renders below the cards.
//
// No RadioGroup anywhere in this file (#1055): role assignment happens only
// through a card's "Change" selector. Depth cap is read-only text on the
// Default card (#1055's depth-cap line) — never an editing control.
//
// Wire types come from src/lib/api/generated (Hard Constraint #8); this file
// never re-declares them.

/** Ready-first, stable ordering: usable providers first (original relative
 *  order preserved), then everything else (original relative order
 *  preserved) — the service list below the cards, "ready" before "needs
 *  setup". */
export function orderSearchProviders(
  providers: readonly IntegrationProvider[],
): IntegrationProvider[] {
  const ready = providers.filter((p) => p.usable === true)
  const needsSetup = providers.filter((p) => p.usable !== true)
  return [...ready, ...needsSetup]
}

/** Shared wording for a keyed provider whose saved key still doesn't reach
 *  search (`configured: true`, `usable: false`) — the service-row badge
 *  (IntegrationsSection.tsx::renderSearchRow) and the Default/Fallback
 *  selectors below must show this exact text, never "Needs configuration",
 *  so the two surfaces can't drift into two vocabularies for one status
 *  (#1055 heuristic H4). */
export const KEY_NOT_REACHING_SEARCH_LABEL = 'Key not reaching search'

/** Why a provider can't be picked from a role selector right now. */
function selectorDisabledReason(p: IntegrationProvider): string {
  if (p.requires_key && !p.configured) return 'Add a key first'
  if (p.requires_key && p.configured) return KEY_NOT_REACHING_SEARCH_LABEL
  return 'Needs configuration'
}

/** Why an already-chosen default/fallback stopped being usable. */
function unusableReason(p: IntegrationProvider): string {
  return p.requires_key && !p.configured ? 'key missing' : 'needs configuration'
}

export interface WebSearchGroupProps {
  /** The full search-provider list (`data.search`). */
  providers: readonly IntegrationProvider[]
  /** `default_search` — absent when the roles are not yet decided (migration deferred). */
  defaultSearch?: string
  /** `fallback_search` — null means an explicit "No fallback"; absent means undecided. */
  fallbackSearch?: string | null
  /** `fallback_ignored_reason` — `same_as_default` when R5 healed the interpretation. */
  fallbackIgnoredReason?: string
  /** `native_search_in_effect` (FR-031). */
  nativeSearchInEffect?: boolean
  /** True while a role/key save is in flight — disables both cards' Change buttons. */
  saving: boolean
  onSetDefault: (id: string) => void
  onSetFallback: (id: string) => void
  onSetNoFallback: () => void
  /** Opens the key-entry editor for the named provider's service row (the
   *  card's "Fix" action on an unusable default/fallback). */
  onFixProvider: (id: string) => void
}

/** The group-level surface: intro copy, notices, and the two role cards. */
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
  onFixProvider,
}: WebSearchGroupProps) {
  const rolesOnWire = defaultSearch !== undefined
  const duckduckgo = providers.find((p) => p.id === 'duckduckgo')
  const showDuckDuckGoTip = fallbackSearch === null && duckduckgo?.usable === true

  const handleFallbackSelect = (id: string | null) => {
    if (id === null) onSetNoFallback()
    else onSetFallback(id)
  }

  return (
    <div className="space-y-[var(--space-3)]" data-testid="websearch-group">
      <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
        Your agents search with the default service. If it fails, the fallback takes over.
      </p>

      {!rolesOnWire && (
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

      {rolesOnWire && (
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-[var(--space-3)]">
          <SearchRoleCard
            role="default"
            label="Default search"
            providers={providers}
            currentId={defaultSearch ?? null}
            saving={saving}
            onSelect={(id) => { if (id !== null) onSetDefault(id) }}
            onFix={onFixProvider}
          />
          <SearchRoleCard
            role="fallback"
            label="Fallback"
            providers={providers}
            currentId={fallbackSearch ?? null}
            excludeId={defaultSearch}
            allowNone
            duckDuckGoTip={showDuckDuckGoTip}
            saving={saving}
            onSelect={handleFallbackSelect}
            onFix={onFixProvider}
          />
        </div>
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

interface SearchRoleCardProps {
  role: 'default' | 'fallback'
  label: string
  /** The full search-provider list. */
  providers: readonly IntegrationProvider[]
  /** The role's current value: a provider id, or null for an explicit "None" (fallback only). */
  currentId: string | null
  /** A provider id to drop from the selector entirely (fallback: the current default). */
  excludeId?: string
  /** Whether the selector offers a "None" choice (fallback only). */
  allowNone?: boolean
  /** Whether to show the "DuckDuckGo works without a key" tip (fallback only, when None). */
  duckDuckGoTip?: boolean
  saving: boolean
  onSelect: (id: string | null) => void
  onFix: (id: string) => void
}

/** One role's card: current choice + status, "Change" opening a Popover +
 *  Command combobox (DefaultModelCard's visual pattern), and, when the
 *  stored choice has gone unusable, a visible error with a "Fix" action. */
function SearchRoleCard({
  role,
  label,
  providers,
  currentId,
  excludeId,
  allowNone,
  duckDuckGoTip,
  saving,
  onSelect,
  onFix,
}: SearchRoleCardProps) {
  const [sectionOpen, setSectionOpen] = useState(false)
  const [popoverOpen, setPopoverOpen] = useState(false)

  const current = currentId ? providers.find((p) => p.id === currentId) : undefined
  const unusable = current?.usable === false
  const selectable = providers.filter((p) => p.id !== excludeId)

  const handleSelect = (id: string | null) => {
    if (saving) return
    setPopoverOpen(false)
    setSectionOpen(false)
    if (id === currentId) return // already the stored value — no gated save
    onSelect(id)
  }

  return (
    <Card className="p-[var(--space-3)] space-y-[var(--space-2-5)]" data-testid={`${role}-search-card`}>
      <div className="flex items-start justify-between gap-[var(--space-2-5)]">
        <div className="min-w-0">
          <p className="text-[length:var(--type-utility-xs-size)] font-semibold uppercase tracking-wide text-[var(--color-muted)]">
            {label}
          </p>

          <p className="mt-[var(--space-1)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
            {current ? (
              <>
                <span>{current.display_name}</span>
                {role === 'fallback' && current.fallback_automatic === true && (
                  <span className="text-[var(--color-muted)]"> (automatic)</span>
                )}
                {!unusable && (
                  <Badge variant="muted" className="ml-[var(--space-2)]">Ready</Badge>
                )}
              </>
            ) : (
              <span className="text-[var(--color-muted)]">None</span>
            )}
          </p>

          {role === 'default' && current?.search_depth_cap && (
            <p className="mt-[var(--space-0-5)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
              Depth cap: {current.search_depth_cap}
            </p>
          )}

          {current && unusable && (
            <p
              className="mt-[var(--space-1)] text-[length:var(--type-body-compact-size)] text-[var(--color-text-error)]"
              role="alert"
            >
              {current.display_name}: {unusableReason(current)} —{' '}
              {role === 'default' ? 'searches will fail' : 'the fallback will not run'}
            </p>
          )}

          {!current && role === 'fallback' && duckDuckGoTip && (
            <p className="mt-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
              DuckDuckGo works without a key — try it as your fallback.
            </p>
          )}
        </div>

        <div className="flex items-center gap-[var(--space-2)] shrink-0">
          {current && unusable && (
            <Button size="sm" onClick={() => onFix(current.id)} disabled={saving}>
              Fix
            </Button>
          )}
          <Button
            size="sm"
            variant="outline"
            onClick={() => setSectionOpen((open) => !open)}
            disabled={saving}
          >
            Change
          </Button>
        </div>
      </div>

      {sectionOpen && (
        <Popover open={popoverOpen} onOpenChange={setPopoverOpen}>
          <PopoverTrigger asChild>
            <Button
              type="button"
              role="combobox"
              variant="outline"
              size="sm"
              aria-expanded={popoverOpen}
              aria-label={label}
              disabled={saving}
              className="w-full justify-between"
            >
              <span className="truncate">
                {current ? current.display_name : allowNone ? 'None' : 'Select…'}
              </span>
              <CaretUpDown size={14} className="shrink-0 opacity-50" />
            </Button>
          </PopoverTrigger>
          <PopoverContent align="start" className="w-[--radix-popover-trigger-width] p-0" aria-label={label}>
            <Command>
              <CommandList>
                {allowNone && (
                  <CommandItem value="none" disabled={saving} onSelect={() => handleSelect(null)}>
                    <Check
                      size={14}
                      className="mr-[var(--space-2)] shrink-0"
                      style={{ opacity: currentId === null ? 1 : 0 }}
                    />
                    None
                  </CommandItem>
                )}
                {selectable.map((p) => {
                  const disabled = p.usable !== true
                  return (
                    <CommandItem
                      key={p.id}
                      value={p.id}
                      disabled={disabled || saving}
                      onSelect={() => handleSelect(p.id)}
                    >
                      <Check
                        size={14}
                        className="mr-[var(--space-2)] shrink-0"
                        style={{ opacity: currentId === p.id ? 1 : 0 }}
                      />
                      <span className="min-w-0 flex-1 truncate">{p.display_name}</span>
                      {disabled && (
                        <span className="ml-[var(--space-2)] shrink-0 text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                          {selectorDisabledReason(p)}
                        </span>
                      )}
                    </CommandItem>
                  )
                })}
              </CommandList>
            </Command>
          </PopoverContent>
        </Popover>
      )}
    </Card>
  )
}
