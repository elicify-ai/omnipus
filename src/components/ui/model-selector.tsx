'use client'

import * as React from 'react'
import { CaretUpDown, Check, CircleNotch, Keyboard, WarningCircle } from '@phosphor-icons/react'
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
  /**
   * Explicit tab-order position forwarded to the trigger element. Defaults
   * to 0 (not undefined) — the trigger renders a native `<button>`, and
   * `tabIndex={undefined}` omits the attribute entirely, making the button
   * unreachable via Tab on WebKit (Safari's default Tab policy visits only
   * form fields and elements with an explicit tabindex attribute). Every
   * call site historically relied on this being optional and omitted it,
   * which soft-locked keyboard users on Safari at any required Model field
   * (e.g. agent-creation wizard Step 1). Callers may still override.
   */
  tabIndex?: number
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
   * Distinguishes "the catalogue is empty" from "we don't know yet" /
   * "the fetch failed" — collapsing those into one state is the bug this
   * prop exists to fix (CI observed a healthy /providers endpoint whose
   * upstream model fetch failed 9x with `context canceled`; the picker
   * told the user no provider was connected, which was false — see
   * `Step1Identity.tsx`'s `catalogStatus` derivation for the full incident
   * note). Defaults to `'ready'` so every existing call site — which has
   * no loading/error concept of its own — renders exactly as before.
   */
  catalogStatus?: 'loading' | 'error' | 'ready'
  /** Message shown in the `catalogStatus === 'error'` state. Falls back to a generic message. */
  catalogErrorMessage?: string
  /** Retry action for the `catalogStatus === 'error'` state. Omit to render the error with no retry control. */
  onRetryCatalog?: () => void
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
  /**
   * Controlled open state. When provided alongside `onOpenChange`, the popover
   * is driven externally (e.g. by the /model slash command setting a store flag).
   * When omitted the popover manages its own open state via internal useState.
   */
  open?: boolean
  onOpenChange?: (open: boolean) => void
}

export function ModelSelector({ models, value, onChange, placeholder, disabled, providerGroups, triggerTestId, tabIndex = 0, itemTestIdPrefix, onUnknownModel, onPairChange, showUnresolvedIndicator = true, constrainToCatalog = false, allowFreeTextWhenEmpty = false, emptyCatalogHint, catalogStatus = 'ready', catalogErrorMessage, onRetryCatalog, variant = 'default', open: controlledOpen, onOpenChange: controlledOnOpenChange }: ModelSelectorProps) {
  const [internalOpen, setInternalOpen] = React.useState(false)
  const isControlled = controlledOpen !== undefined
  const open = isControlled ? controlledOpen : internalOpen
  const setOpen = React.useCallback((next: boolean) => {
    if (isControlled) {
      controlledOnOpenChange?.(next)
    } else {
      setInternalOpen(next)
    }
  }, [isControlled, controlledOnOpenChange])
  const [query, setQuery] = React.useState('')
  // Unique id for the sr-only description the popover's aria-describedby
  // points at. useId() guarantees uniqueness even if multiple
  // ModelSelectors are mounted on the same page.
  const descriptionId = React.useId()

  // Loading / error catalogue states pre-empt every other render path below.
  // A providers fetch that is still in flight, or has failed outright, must
  // never render as "no models available" — that copy reads as user error
  // ("you haven't connected a provider") when the real cause is transient:
  // CI observed exactly this, the gateway's upstream fetch to openrouter.ai
  // failing 9 times in a row with `context canceled` (zero successes) while
  // the /providers endpoint itself was healthy (a direct curl from the same
  // worker returned 200 in 0.46s) — and the picker still told the user no
  // provider was connected. `catalogStatus` defaults to `'ready'`, so a
  // caller that never sets it (every existing call site) is unaffected.
  // catalogStatus === 'loading' deliberately has NO early return: it renders
  // the SAME interactive combobox every other ready state does, and shows
  // "Loading models…" INSIDE the popover (see CommandList below).
  //
  // WHY (root-caused 2026-08-14, reproduced locally): this state used to
  // return a non-interactive <div role="status"> carrying triggerTestId —
  // an element that LOOKS like the trigger and answers to its test id, but
  // cannot be opened. The provider catalog is fetched live on every mount
  // (0.13s idle, measured 1.2-4.5s under a full e2e shard), so for that
  // whole window a click on "Model" hit the placeholder, did nothing, and
  // was LOST: when the catalog landed the real combobox replaced the div,
  // but nothing re-opened the popover. The user clicks, gets no feedback,
  // and must click again with no idea why. It failed the create-agent e2e
  // spec exactly this way — and passed in isolation, where the window is
  // ~0ms, which is why it read as flake for weeks.
  //
  // An earlier pass at this only changed the placeholder's TEXT (from a
  // false "Connect a provider in Settings" to "Loading models…"). That
  // fixed the lie and left the swallowed click untouched. Rendering one
  // trigger in every state is what actually fixes it.

  if (catalogStatus === 'error') {
    const isGhost = variant === 'ghost'
    return (
      <div
        data-testid={triggerTestId}
        role="alert"
        className={
          isGhost
            ? 'flex items-center gap-1.5 h-7 rounded-md px-1.5 text-xs'
            : 'flex w-full items-center gap-2 h-10 rounded-md border px-3 py-2 text-sm'
        }
        style={
          isGhost
            ? { color: 'var(--color-warning)' }
            : {
                borderColor: 'var(--color-warning)',
                backgroundColor: 'var(--color-surface-1)',
                color: 'var(--color-warning)',
              }
        }
      >
        <WarningCircle size={12} weight="fill" className="shrink-0" aria-hidden="true" />
        <span className="truncate text-xs flex-1">
          {catalogErrorMessage ?? 'Failed to load providers'}
        </span>
        {onRetryCatalog && (
          <button
            type="button"
            onClick={onRetryCatalog}
            data-testid={triggerTestId ? `${triggerTestId}-retry` : undefined}
            className="shrink-0 text-xs font-medium underline underline-offset-2 hover:opacity-80"
            style={{ color: 'var(--color-accent)' }}
          >
            Retry
          </button>
        )}
      </div>
    )
  }

  const catalogEmpty =
    models.length === 0 && (!providerGroups || providerGroups.every((g) => g.models.length === 0))

  // Constrained empty-catalogue state. The operator decision is that a
  // non-catalogue model must not be selectable, so when there is nothing to
  // pick we render a disabled, non-editable placeholder rather than a
  // free-text input that would flag every value as "unresolved". The
  // onboarding bootstrap path (`allowFreeTextWhenEmpty`) is the one
  // exception — there the user must type the first slug for an
  // endpoint-less provider.
  // catalogStatus !== 'loading' is load-bearing: while the catalog is in
  // flight `models` is legitimately empty, and without this guard the
  // loading state falls into this disabled "no models" placeholder — the
  // same non-interactive, click-swallowing element the loading branch was
  // just fixed to stop rendering. Empty-because-loading is not
  // empty-because-there-is-nothing.
  if (catalogEmpty && constrainToCatalog && !allowFreeTextWhenEmpty && catalogStatus !== 'loading') {
    const isGhost = variant === 'ghost'
    return (
      <div
        data-testid={triggerTestId}
        aria-disabled="true"
        className={
          isGhost
            ? 'flex items-center h-7 rounded-md px-1.5 text-xs cursor-not-allowed opacity-70'
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

  // Text input mode — no models available.
  // Excluded while loading for the same reason as the branch above: an empty
  // `models` during the fetch means "not here YET", and falling into free-text
  // entry mid-load would swap the control out from under a user who is
  // waiting for a list — a second way to lose their interaction.
  if (catalogEmpty && catalogStatus !== 'loading') {
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
          aria-busy={catalogStatus === 'loading' || undefined}
          aria-label={value ? `Model selector, currently ${value}${valueUnresolved ? ' (unresolved)' : ''}` : `Model selector, ${displayValue}`}
          aria-invalid={valueUnresolved || undefined}
          aria-describedby={valueUnresolved ? `${descriptionId}-unresolved` : undefined}
          disabled={disabled}
          tabIndex={tabIndex}
          data-testid={triggerTestId}
          data-unresolved={valueUnresolved || undefined}
          className={
            isGhost
              ? 'flex items-center gap-1 h-7 rounded-md px-1.5 text-xs transition-colors disabled:cursor-not-allowed disabled:opacity-50 hover:bg-[var(--color-surface-2)]'
              : 'flex w-full items-center justify-between h-10 rounded-md border px-3 py-2 text-sm transition-colors disabled:cursor-not-allowed disabled:opacity-50'
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
          {/* data-no-focus-ring: the sanctioned opt-out for a composite widget
              whose focus is shown by its parent surface (globals.css, "Central
              focus ring"). This input is auto-focused the instant the popover
              opens and is the only focusable thing in it, so the gold ring
              fires immediately on every open and boxes a field the bordered
              popover already frames — noise, not a focus cue. Same rationale
              as the chat textarea inside the composer card. Operator-requested
              2026-08-13. */}
          <CommandInput
            data-no-focus-ring
            placeholder="Search models..."
            value={query}
            onValueChange={setQuery}
          />
          <CommandList>
            {catalogStatus === 'loading' ? (
              <div
                className="flex items-center gap-2 px-3 py-6 text-sm"
                style={{ color: 'var(--color-muted)' }}
                role="status"
                aria-live="polite"
                data-testid={triggerTestId ? `${triggerTestId}-loading` : undefined}
              >
                <CircleNotch size={14} className="animate-spin shrink-0" aria-hidden="true" />
                <span>Loading models…</span>
              </div>
            ) : (
              <CommandEmpty>No models found.</CommandEmpty>
            )}
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
