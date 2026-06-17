'use client'

import * as React from 'react'
import { CaretUpDown, Check, Keyboard } from '@phosphor-icons/react'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { Input } from '@/components/ui/input'

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
   * Wave 2 / FIX-4: optional test ids for the combobox trigger and the
   * pickable model items. Defaults preserve the existing behavior
   * (no data-testid on the trigger or items) so the change is
   * backward-compatible with all current call sites.
   */
  triggerTestId?: string
  /** Prefix for each item's data-testid. The full id is `${itemTestIdPrefix}${model}`. */
  itemTestIdPrefix?: string
}

export function ModelSelector({ models, value, onChange, placeholder, disabled, providerGroups, triggerTestId, itemTestIdPrefix }: ModelSelectorProps) {
  const [open, setOpen] = React.useState(false)
  const [query, setQuery] = React.useState('')

  // Text input mode — no models available
  if (models.length === 0 && (!providerGroups || providerGroups.every((g) => g.models.length === 0))) {
    return (
      <Input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder ?? 'Enter model slug (e.g. MiniMax-M2.7)'}
        disabled={disabled}
        {...(triggerTestId ? { 'data-testid': triggerTestId } : {})}
        className="font-mono text-sm"
      />
    )
  }

  // Build the effective flat model list (used for exactMatch check and allModels filter)
  const allModels: string[] =
    providerGroups && providerGroups.length > 0
      ? providerGroups.flatMap((g) => g.models)
      : models

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

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          role="combobox"
          aria-expanded={open}
          disabled={disabled}
          data-testid={triggerTestId}
          className="flex w-full items-center justify-between h-10 rounded-md border px-3 py-2 text-sm transition-colors focus-visible:outline-none focus-visible:ring-1 disabled:cursor-not-allowed disabled:opacity-50"
          style={{
            borderColor: open ? 'var(--color-accent)' : 'var(--color-border)',
            backgroundColor: 'var(--color-surface-1)',
            color: value ? 'var(--color-secondary)' : 'var(--color-muted)',
          }}
        >
          <span className="truncate font-mono text-sm">{displayValue}</span>
          <CaretUpDown size={14} className="shrink-0 opacity-50" />
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-[--radix-popover-trigger-width] p-0" align="start">
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
                  onSelect={() => handleSelect(queryRaw)}
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
