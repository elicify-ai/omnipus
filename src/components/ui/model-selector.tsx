'use client'

import * as React from 'react'
import { CaretUpDown, Check, Keyboard, WarningCircle } from '@phosphor-icons/react'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { Input } from '@/components/ui/input'
import { isKnownModelSlugInList } from '@/lib/agents/model-validation'

export interface ModelGroup {
  providerName: string
  models: string[]
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
   * W6-C4 / G12: when `true`, the trigger button shows an inline "unresolved"
   * chip next to the current value if the value is NOT in the supplied
   * `models` / `providerGroups` list (case-insensitive, also matches the
   * bare slug stripped of its protocol prefix). Defaults to `true` so the
   * indicator is always on when this component is wired into a free-text
   * picker — pass `false` to opt out (e.g. for read-only displays).
   */
  showUnresolvedIndicator?: boolean
}

export function ModelSelector({ models, value, onChange, placeholder, disabled, providerGroups, triggerTestId, itemTestIdPrefix, onUnknownModel, showUnresolvedIndicator = true }: ModelSelectorProps) {
  const [open, setOpen] = React.useState(false)
  const [query, setQuery] = React.useState('')
  // Unique id for the sr-only description the popover's aria-describedby
  // points at. useId() guarantees uniqueness even if multiple
  // ModelSelectors are mounted on the same page.
  const descriptionId = React.useId()

  // Text input mode — no models available
  if (models.length === 0 && (!providerGroups || providerGroups.every((g) => g.models.length === 0))) {
    // W6-C4 / G12: in text-input mode the trigger is just an <input>; we
    // surface the unresolved state via a small inline note beneath the
    // input when a non-empty value isn't in the supplied flat list (which
    // is always empty here, so EVERY non-empty value is unresolved).
    const valueUnresolved = showUnresolvedIndicator && value.trim() !== ''
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
            Model not in any connected provider — calls will fail until you add a provider that supports this model.
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
  const valueUnresolved =
    showUnresolvedIndicator && value.trim() !== '' && !isKnownModelSlugInList(value, allModels)

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

  // Determine whether to render grouped or flat
  // Grouped: providerGroups supplied AND ≥2 groups with models
  const groupsWithModels = providerGroups ? providerGroups.filter((g) => g.models.length > 0) : []
  const useGrouped = groupsWithModels.length >= 2

  const handleSelect = (model: string) => {
    onChange(model)
    setOpen(false)
    setQuery('')
  }

  const handleUnknownSelect = (model: string) => {
    onUnknownModel?.(model)
    onChange(model)
    setOpen(false)
    setQuery('')
  }

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
          className="flex w-full items-center justify-between h-10 rounded-md border px-3 py-2 text-sm transition-colors focus-visible:outline-none focus-visible:ring-1 disabled:cursor-not-allowed disabled:opacity-50"
          style={{
            borderColor: open ? 'var(--color-accent)' : valueUnresolved ? 'var(--color-warning)' : 'var(--color-border)',
            backgroundColor: 'var(--color-surface-1)',
            color: value ? 'var(--color-secondary)' : 'var(--color-muted)',
          }}
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
            {queryRaw && !exactMatch && (
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
