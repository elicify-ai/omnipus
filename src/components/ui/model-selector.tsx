'use client'

import * as React from 'react'
import { CaretUpDown, Check, Keyboard, WarningCircle } from '@phosphor-icons/react'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { Input } from '@/components/ui/input'
import { isKnownModelSlugInList } from '@/lib/agents/model-validation'

export interface ModelGroup {
  providerName: string
  /** O3: explicit provider routing key (e.g. the provider's `id` field). Used to
   *  emit a `{model, provider}` pair via `onPairChange`. Optional — callers that
   *  do not set this get an empty string for the provider in `onPairChange`. */
  providerId?: string
  models: string[]
}

/** O3 two-field model: the value emitted by `onPairChange` when the user picks
 *  a model from a known provider group. */
export interface ModelPair {
  model: string
  /** Explicit provider routing key — matches the provider's `id` field on the
   *  backend. Empty string when the provider could not be resolved (e.g. the
   *  group has no `providerId` or the model was entered via free-text). */
  provider: string
}

interface ModelSelectorProps {
  models: string[]
  value: string
  onChange: (model: string) => void  // Named onChange (not onValueChange) since this supports free-text input, not just selection
  placeholder?: string
  disabled?: boolean
  /** When provided, renders models grouped by provider (shown when ≥2 providers configured). */
  providerGroups?: ModelGroup[]
  /**
   * Optional test ids for the combobox trigger and the pickable model
   * items. Defaults preserve the existing behavior (no data-testid on
   * the trigger or items) so the change is backward-compatible with
   * all current call sites.
   */
  triggerTestId?: string
  /** Prefix for each item's data-testid. The full id is `${itemTestIdPrefix}${model}`. */
  itemTestIdPrefix?: string
  /**
   * Optional callback fired when the user picks a free-text model slug
   * that is NOT in the supplied `models` / `providerGroups` list (i.e.
   * the "Use <query>" row at the bottom of the popover). Lets callers
   * surface a warning toast. Not called for exact-match picks.
   */
  onUnknownModel?: (model: string) => void
  /**
   * O3 two-field: called alongside `onChange` when the user picks a model from
   * a known provider group. Emits `{ model, provider }` where `provider` is the
   * group's `providerId` (or empty string when not set). NOT called for free-text
   * "Use <slug>" picks because those have no resolvable provider.
   */
  onPairChange?: (pair: ModelPair) => void
  /**
   * W6-C4 / G12: when `true`, the trigger button shows an inline "unresolved"
   * chip next to the current value if the value is NOT in the supplied
   * `models` / `providerGroups` list (case-insensitive, also matches the
   * bare slug stripped of its protocol prefix). Defaults to `true` so the
   * indicator is always on when this component is wired into a free-text
   * picker — pass `false` to opt out (e.g. for read-only displays).
   *
   * Ignored when `constrainToCatalog` is `true` — a constrained picker can
   * only ever hold a catalogue value, so the unresolved state cannot occur.
   */
  showUnresolvedIndicator?: boolean
  /**
   * UAT model-catalog fix: when `true`, the picker is a *constrained*
   * dropdown — selection is limited to the supplied `models` /
   * `providerGroups` catalogue. The free-text "Use <slug>" row is suppressed
   * and the "unresolved" indicator never renders (a constrained value is
   * always in the catalogue, so it cannot be unresolved). When the catalogue
   * is empty, the picker renders a disabled "no models" state instead of a
   * free-text input — UNLESS `allowFreeTextWhenEmpty` is also set (the
   * onboarding bootstrap path for an endpoint-less provider, where the user
   * must type the first slug because no catalogue exists yet).
   *
   * Defaults to `false` to preserve the historical free-text behaviour for
   * call sites that have not opted in.
   */
  constrainToCatalog?: boolean
  /**
   * Only meaningful with `constrainToCatalog`. When `true` and the catalogue
   * is empty, the picker falls back to a free-text <input> so the user can
   * seed the first model slug for an endpoint-less provider (onboarding).
   * No unresolved warning is shown in this bootstrap mode.
   */
  allowFreeTextWhenEmpty?: boolean
  /**
   * Optional message rendered inside the disabled "no models" state of a
   * constrained picker. Defaults to a generic "connect a provider" hint.
   */
  emptyCatalogHint?: string
  /**
   * Visual style of the trigger button.
   * - `'default'` (the default) — bordered form-field look: solid border, filled
   *   background, h-10, full-width. Preserves the existing appearance for all
   *   current call sites (backward-compatible).
   * - `'ghost'` — flat, compact, borderless: transparent background, h-7, smaller
   *   padding (px-2), muted text, hover-background only. Matches the ghost sibling
   *   controls in the chat header (New Chat / agent-picker buttons).
   */
  variant?: 'default' | 'ghost'
}

export function ModelSelector({ models, value, onChange, placeholder, disabled, providerGroups, triggerTestId, itemTestIdPrefix, onUnknownModel, onPairChange, showUnresolvedIndicator = true, constrainToCatalog = false, allowFreeTextWhenEmpty = false, emptyCatalogHint, variant = 'default' }: ModelSelectorProps) {
  const [open, setOpen] = React.useState(false)
  const [query, setQuery] = React.useState('')
  // Unique id for the sr-only description the popover's aria-describedby
  // points at. useId() guarantees uniqueness even if multiple
  // ModelSelectors are mounted on the same page.
  const descriptionId = React.useId()

  const catalogEmpty =
    models.length === 0 && (!providerGroups || providerGroups.every((g) => g.models.length === 0))

  // Constrained empty-catalogue state. The operator decision is that a
  // non-catalogue model must not be selectable, so when there is nothing to
  // pick we render a disabled, non-editable placeholder rather than a
  // free-text input that would flag every value as "unresolved". The
  // onboarding bootstrap path (`allowFreeTextWhenEmpty`) is the one
  // exception — there the user must type the first slug for an
  // endpoint-less provider.
  if (catalogEmpty && constrainToCatalog && !allowFreeTextWhenEmpty) {
    const isGhost = variant === 'ghost'
    return (
      <div
        data-testid={triggerTestId}
        aria-disabled="true"
        className={
          isGhost
            ? 'flex items-center h-8 rounded-md px-2 text-xs pointer-coarse:min-h-[44px] pointer-coarse:px-3 cursor-not-allowed opacity-70'
            : 'flex w-full items-center justify-between h-10 rounded-md border px-3 py-2 text-sm cursor-not-allowed opacity-70'
        }
        style={
          isGhost
            ? { color: 'var(--color-muted)' }
            : {
                borderColor: 'var(--color-border)',
                backgroundColor: 'var(--color-surface-1)',
                color: 'var(--color-muted)',
              }
        }
      >
        <span className="truncate text-xs">
          {emptyCatalogHint ?? 'No models available — connect a provider first'}
        </span>
      </div>
    )
  }

  // Text input mode — no models available
  if (catalogEmpty) {
    // W6-C4 / G12: in text-input mode the trigger is just an <input>; we
    // surface the unresolved state via a small inline note beneath the
    // input when a non-empty value isn't in the supplied flat list (which
    // is always empty here, so EVERY non-empty value is unresolved).
    // A constrained bootstrap picker (onboarding manual provider) never
    // shows the warning — the typed slug becomes the catalogue.
    const valueUnresolved = showUnresolvedIndicator && !constrainToCatalog && value.trim() !== ''
    return (
      <div className="space-y-1">
        <Input
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder ?? 'Enter model slug (e.g. MiniMax-M2.7)'}
          aria-label="Model slug"
          aria-invalid={valueUnresolved || undefined}
          aria-describedby={valueUnresolved ? `${descriptionId}-unresolved` : undefined}
          disabled={disabled}
          {...(triggerTestId ? { 'data-testid': triggerTestId } : {})}
          className="font-mono text-sm"
        />
        {valueUnresolved && (
          <p
            id={`${descriptionId}-unresolved`}
            data-testid={triggerTestId ? `${triggerTestId}-unresolved` : undefined}
            className="flex items-center gap-1 text-[10px] text-[var(--color-warning)]"
            role="status"
          >
            <WarningCircle size={11} weight="fill" aria-hidden="true" />
            Model not found in any connected provider — add a matching provider to use this model.
          </p>
        )}
      </div>
    )
  }

  // Build the effective flat model list (used for exactMatch check and allModels filter)
  const allModels: string[] =
    providerGroups && providerGroups.length > 0
      ? providerGroups.flatMap((g) => g.models)
      : models

  // W6-C4 / G12: when the current `value` doesn't appear in the flat model
  // list, render an "unresolved" chip on the trigger. The chip persists
  // across re-renders (no toast, no flicker) so the user always knows the
  // saved value can't be routed at chat time.
  // A constrained picker can only hold a catalogue value, so the unresolved
  // state cannot occur — never render the chip there.
  const valueUnresolved =
    showUnresolvedIndicator && !constrainToCatalog && value.trim() !== '' && !isKnownModelSlugInList(value, allModels)

  // Combobox mode — searchable dropdown
  const displayValue = value || placeholder || 'Select a model...'
  // queryRaw: exact user input (preserved case) — used for save and display
  // queryLower: lowercased copy — used only for case-insensitive filtering and exactMatch
  const queryRaw = query.trim()
  const queryLower = queryRaw.toLowerCase()

  // Filter models by model name (not provider name) — case-insensitive comparison only
  const filterModel = (model: string) =>
    queryLower === '' || model.toLowerCase().includes(queryLower)

  const exactMatch = allModels.some((m) => m.toLowerCase() === queryLower)

  // O3: provider-headed sections even with one provider — always group when
  // providerGroups is provided with at least one non-empty group. This lets
  // the provider name act as a stable visual heading. Previously grouped only
  // when ≥2 providers, which hid the heading for single-provider installs.
  const groupsWithModels = providerGroups
    ? providerGroups
        .filter((g) => g.models.length > 0)
        // O3: sort models within each group alphabetically for consistent discovery.
        .map((g) => ({ ...g, models: [...g.models].sort((a, b) => a.localeCompare(b)) }))
    : []
  const useGrouped = groupsWithModels.length >= 1

  // O3: resolve the provider routing key for a given model slug by searching
  // the providerGroups array. Returns the group's `providerId` or empty string.
  const resolveProviderId = (modelSlug: string): string => {
    if (!providerGroups) return ''
    const group = providerGroups.find((g) => g.models.includes(modelSlug))
    return group?.providerId ?? ''
  }

  const handleSelect = (model: string) => {
    onChange(model)
    onPairChange?.({ model, provider: resolveProviderId(model) })
    setOpen(false)
    setQuery('')
  }

  const handleUnknownSelect = (model: string) => {
    onUnknownModel?.(model)
    onChange(model)
    setOpen(false)
    setQuery('')
  }

  const isGhost = variant === 'ghost'

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          role="combobox"
          aria-expanded={open}
          aria-label={value ? `Model selector, currently ${value}${valueUnresolved ? ' (unresolved)' : ''}` : `Model selector, ${displayValue}`}
          aria-invalid={valueUnresolved || undefined}
          aria-describedby={valueUnresolved ? `${descriptionId}-unresolved` : undefined}
          disabled={disabled}
          data-testid={triggerTestId}
          data-unresolved={valueUnresolved || undefined}
          className={
            isGhost
              ? 'flex items-center gap-1 h-8 rounded-md px-2 text-xs pointer-coarse:min-h-[44px] pointer-coarse:px-3 transition-colors focus-visible:outline-none focus-visible:ring-1 disabled:cursor-not-allowed disabled:opacity-50 hover:bg-[var(--color-surface-2)]'
              : 'flex w-full items-center justify-between h-10 rounded-md border px-3 py-2 text-sm transition-colors focus-visible:outline-none focus-visible:ring-1 disabled:cursor-not-allowed disabled:opacity-50'
          }
          style={
            isGhost
              ? { color: 'var(--color-muted)' }
              : {
                  borderColor: open ? 'var(--color-accent)' : valueUnresolved ? 'var(--color-warning)' : 'var(--color-border)',
                  backgroundColor: 'var(--color-surface-1)',
                  color: value ? 'var(--color-secondary)' : 'var(--color-muted)',
                }
          }
        >
          <span className="flex items-center gap-2 min-w-0 flex-1">
            <span className="truncate font-mono text-sm">{displayValue}</span>
            {valueUnresolved && (
              <span
                id={`${descriptionId}-unresolved`}
                data-testid={triggerTestId ? `${triggerTestId}-unresolved` : undefined}
                className="inline-flex shrink-0 items-center gap-1 px-1.5 py-0.5 rounded text-[9px] font-semibold uppercase tracking-wider border"
                style={{
                  backgroundColor: 'color-mix(in srgb, var(--color-warning) 15%, transparent)',
                  color: 'var(--color-warning)',
                  borderColor: 'color-mix(in srgb, var(--color-warning) 40%, transparent)',
                }}
                role="status"
              >
                <WarningCircle size={10} weight="fill" aria-hidden="true" />
                Unresolved
              </span>
            )}
          </span>
          <CaretUpDown size={14} className="shrink-0 opacity-50" />
        </button>
      </PopoverTrigger>
      <PopoverContent
        className="w-[--radix-popover-trigger-width] p-0"
        align="start"
        // WAI-ARIA Combobox Pattern 1.2 (WCAG 1.3.1, 2.4.6, 4.1.2):
        // the trigger above carries `role="combobox"` + `aria-expanded`
        // + `aria-controls` (via PopoverTrigger), and the picker is a
        // generic container for the Command listbox below. We do NOT
        // set `role="dialog"` + `aria-modal="true"` because Radix
        // Popover is intentionally non-modal — there is no focus trap
        // or inert overlay, so claiming modal semantics would mislead
        // screen-reader users. The sr-only <p> below provides the
        // longer description via `aria-describedby` so the combobox
        // has an accessible name and description. The description id is
        // generated via useId() to keep it unique if multiple
        // ModelSelectors ever mount on the same page.
        aria-label="Select model"
        aria-describedby={descriptionId}
      >
        <p id={descriptionId} className="sr-only">
          Search or scroll to pick a model from the list. Press Enter on a suggestion to select it, or type a custom model slug and choose “Use your-slug” to save the exact value.
        </p>
        {/* shouldFilter=false: we handle filtering ourselves so search targets model name only */}
        <Command shouldFilter={false}>
          <CommandInput
            placeholder="Search models..."
            value={query}
            onValueChange={setQuery}
          />
          <CommandList>
            <CommandEmpty>No models found.</CommandEmpty>
            {useGrouped ? (
              // ≥2 providers: render one CommandGroup per provider with a heading
              groupsWithModels.map((group) => {
                const filteredModels = group.models.filter(filterModel)
                if (filteredModels.length === 0) return null
                return (
                  <CommandGroup
                    key={group.providerName}
                    heading={group.providerName}
                  >
                    {filteredModels.map((model) => (
                      <CommandItem
                        key={model}
                        value={model}
                        onSelect={() => handleSelect(model)}
                        {...(itemTestIdPrefix ? { 'data-testid': `${itemTestIdPrefix}${model}` } : {})}
                      >
                        <Check
                          size={14}
                          className="mr-2 shrink-0"
                          style={{ opacity: value === model ? 1 : 0, color: 'var(--color-accent)' }}
                        />
                        <span className="font-mono text-xs">{model}</span>
                      </CommandItem>
                    ))}
                  </CommandGroup>
                )
              })
            ) : (
              // Single provider or flat list
              <CommandGroup>
                {(providerGroups && groupsWithModels.length === 1
                  ? groupsWithModels[0].models
                  : models
                )
                  .filter(filterModel)
                  .map((model) => (
                    <CommandItem
                      key={model}
                      value={model}
                      onSelect={() => handleSelect(model)}
                      {...(itemTestIdPrefix ? { 'data-testid': `${itemTestIdPrefix}${model}` } : {})}
                    >
                      <Check
                        size={14}
                        className="mr-2 shrink-0"
                        style={{ opacity: value === model ? 1 : 0, color: 'var(--color-accent)' }}
                      />
                      <span className="font-mono text-xs">{model}</span>
                    </CommandItem>
                  ))}
              </CommandGroup>
            )}
            {queryRaw && !exactMatch && !constrainToCatalog && (
              <CommandGroup>
                <CommandItem
                  value={`custom:${queryLower}`}
                  onSelect={() => handleUnknownSelect(queryRaw)}
                >
                  <Keyboard size={14} className="mr-2 shrink-0" style={{ color: 'var(--color-muted)' }} />
                  <span className="text-xs">
                    Use "<span className="font-mono" style={{ color: 'var(--color-accent)' }}>{queryRaw}</span>"
                  </span>
                </CommandItem>
              </CommandGroup>
            )}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
