'use client'

import * as React from 'react'
import { CaretUpDown, Check, CircleNotch, Keyboard, WarningCircle } from '@phosphor-icons/react'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { Input } from '@/components/ui/input'
import { useVirtualizer } from '@tanstack/react-virtual'
import { isKnownModelSlugInList } from '@/lib/agents/model-validation'
import {
  RECOMMENDED_CHIP_LABEL,
  orderModels,
  recommendedModelIds,
  shouldVirtualiseModelList,
} from '@/components/ui/model-ordering'
import type { CatalogModel, components } from '@/lib/api/generated/openapi-types'

/** The six provider statuses, straight off the wire contract (ADR-068 FR-038). */
export type ProviderStatus = components['schemas']['Provider']['status']

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

/**
 * ADR-068 FR-030 catalog mode. Where `providerGroups` carries bare slugs, this
 * carries the catalog rows themselves, which is what ordering by release date
 * and awarding a "Recommended for chat" chip need — neither is derivable from a
 * string. Supplying `catalogGroups` switches the list to catalog rendering;
 * omitting it leaves every existing call site on the string path untouched.
 */
export interface ModelCatalogGroup {
  /** Provider routing key — the configured provider's `id`. */
  providerId: string
  /** Display name, used as the vendor heading fallback for bare model ids. */
  providerName: string
  /** Connection status, so `filterProviders` can keep connected rows only. */
  status?: ProviderStatus
  models: CatalogModel[]
}

/**
 * ADR-068 FR-019: narrow which providers the selector offers. Both fields are
 * ANDed; an omitted or empty field is "no restriction on this axis".
 *
 * - Settings → Providers *Change* passes `{ statuses: ['connected', 'signed_in'] }`.
 * - A row's *Set as default model…* passes `{ providerIds: ['<that row>'] }`.
 *
 * `providerIds` also narrows the legacy string `providerGroups`; `statuses`
 * cannot (a string group carries no status) and is ignored there.
 */
export interface ModelSelectorProviderFilter {
  providerIds?: string[]
  statuses?: ProviderStatus[]
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
  /**
   * ADR-068 FR-029: the field's accessible name, verbatim. Onboarding passes
   * "Model for your first agent". With no value the trigger's accessible name
   * is exactly this string; once a model is chosen the value is appended after
   * it, so the label still leads the announcement.
   *
   * Omitted → the historical "Model selector, …" name, unchanged.
   */
  label?: string
  /** ADR-068 FR-030 catalog mode — see `ModelCatalogGroup`. */
  catalogGroups?: ModelCatalogGroup[]
  /** ADR-068 FR-019 — see `ModelSelectorProviderFilter`. */
  filterProviders?: ModelSelectorProviderFilter
}

/** One rendered line of catalog mode: a vendor heading or a pickable model. */
type CatalogRow =
  | { kind: 'heading'; key: string; label: string }
  | {
      kind: 'model'
      key: string
      model: CatalogModel
      providerId: string
      recommended: boolean
    }

/** Height used to lay out the virtualised list, in px. Rows are a fixed size. */
const CATALOG_ROW_HEIGHT = 32

export function ModelSelector({ models, value, onChange, placeholder, disabled, providerGroups, triggerTestId, tabIndex = 0, itemTestIdPrefix, onUnknownModel, onPairChange, showUnresolvedIndicator = true, constrainToCatalog = false, allowFreeTextWhenEmpty = false, emptyCatalogHint, catalogStatus = 'ready', catalogErrorMessage, onRetryCatalog, variant = 'default', open: controlledOpen, onOpenChange: controlledOnOpenChange, label, catalogGroups, filterProviders }: ModelSelectorProps) {
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

  // Catalogue-unreachable escape hatch (dead-end fix). A CONSTRAINED picker
  // (constrainToCatalog) intentionally cannot hold a non-catalogue value —
  // that rule is sound when the catalogue loaded and simply doesn't contain
  // the model. It stops being sound the moment the catalogue itself cannot
  // be produced at all (the `catalogStatus === 'error'` and the
  // connected-but-empty branches below): the operator is then blocked from
  // creating or editing ANY agent, forever, through no typo of their own —
  // e.g. Ollama configured with no local server running, where the /providers
  // call succeeds but that row's live model list comes back empty with a
  // `warning`. Retry (already wired via `onRetryCatalog`) is the first line
  // of recovery and costs nothing since it doesn't touch the "no free text"
  // rule at all. `manualOverride` is the deliberate, explicit fallback for
  // when retry keeps failing (a genuinely down local server won't start
  // itself): it lets the operator type the exact slug they know is correct,
  // clearly marked "unresolved" everywhere that value is shown afterward —
  // the SAME unresolved-chip machinery every unconstrained picker in the
  // product already uses for a free-text value, not a new, silent
  // free-for-all. It is reachable ONLY from the two states where the
  // catalogue could not be produced; a picker whose catalogue loaded fine
  // still refuses free text exactly as before.
  const [manualOverride, setManualOverride] = React.useState(false)

  // ---------------------------------------------------------------------
  // ADR-068 FR-030 / FR-019 — catalog mode.
  //
  // Every hook below runs unconditionally and BEFORE this component's early
  // returns (error state, empty catalogue, text-input fallback). A hook placed
  // after them would be called on some renders and skipped on others, which
  // React forbids.
  // ---------------------------------------------------------------------
  const catalogMode = catalogGroups !== undefined
  // Call sites pass `filterProviders` as an inline object literal, so its
  // identity changes on every render. Memoise on its CONTENT instead.
  const filterKey = `${(filterProviders?.providerIds ?? []).join(',')}|${(filterProviders?.statuses ?? []).join(',')}`

  const visibleCatalogGroups = React.useMemo<ModelCatalogGroup[]>(() => {
    if (!catalogGroups) return []
    const ids = filterProviders?.providerIds
    const statuses = filterProviders?.statuses
    return catalogGroups.filter((group) => {
      if (ids && ids.length > 0 && !ids.includes(group.providerId)) return false
      if (statuses && statuses.length > 0 && !(group.status && statuses.includes(group.status))) return false
      return group.models.length > 0
    })
    // filterKey stands in for filterProviders (see above).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [catalogGroups, filterKey])

  // FR-019 also narrows the legacy string groups, by id only — a string group
  // carries no status, so a `statuses` filter cannot apply to it.
  const effectiveProviderGroups = React.useMemo<ModelGroup[] | undefined>(() => {
    if (!providerGroups) return providerGroups
    const ids = filterProviders?.providerIds
    if (!ids || ids.length === 0) return providerGroups
    return providerGroups.filter((g) => g.providerId !== undefined && ids.includes(g.providerId))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [providerGroups, filterKey])

  // FR-030 ordering + chips, computed once per catalog change. Recommended is
  // scoped PER PROVIDER (at most three each), so it is computed inside the
  // per-group loop rather than over the flattened list.
  const catalogRows = React.useMemo<CatalogRow[]>(() => {
    const rows: CatalogRow[] = []
    const multiProvider = visibleCatalogGroups.length > 1
    for (const group of visibleCatalogGroups) {
      const recommended = new Set(recommendedModelIds(group.models))
      for (const vendorGroup of orderModels(group.models, { fallbackVendor: group.providerName })) {
        const vendorLabel = vendorGroup.vendor || group.providerName
        rows.push({
          kind: 'heading',
          key: `heading:${group.providerId}:${vendorGroup.vendor}`,
          // With one provider the vendor alone is unambiguous; with several,
          // two providers can both expose an "anthropic" vendor group.
          label:
            multiProvider && vendorLabel !== group.providerName
              ? `${group.providerName} · ${vendorLabel}`
              : vendorLabel,
        })
        for (const model of vendorGroup.models) {
          rows.push({
            kind: 'model',
            key: `${group.providerId}:${model.id}`,
            model,
            providerId: group.providerId,
            recommended: recommended.has(model.id),
          })
        }
      }
    }
    return rows
  }, [visibleCatalogGroups])

  // The rows actually on screen: the query drops non-matching models, and any
  // heading whose whole group went with them.
  const visibleCatalogRows = React.useMemo<CatalogRow[]>(() => {
    const needle = query.trim().toLowerCase()
    if (needle === '') return catalogRows
    const kept: CatalogRow[] = []
    for (const row of catalogRows) {
      if (row.kind === 'heading') {
        kept.push(row)
        continue
      }
      if (
        row.model.id.toLowerCase().includes(needle) ||
        row.model.name.toLowerCase().includes(needle)
      ) {
        kept.push(row)
      }
    }
    // Drop headings left with no models under them.
    return kept.filter(
      (row, i) => row.kind === 'model' || (kept[i + 1] !== undefined && kept[i + 1].kind === 'model'),
    )
  }, [catalogRows, query])

  const catalogModelCount = catalogRows.reduce((n, r) => n + (r.kind === 'model' ? 1 : 0), 0)
  const visibleCatalogModelCount = visibleCatalogRows.reduce(
    (n, r) => n + (r.kind === 'model' ? 1 : 0),
    0,
  )
  // aria-posinset is 1-based over the models actually offered, so it has to be
  // assigned after filtering, not baked into catalogRows.
  const catalogPositions = React.useMemo(() => {
    const positions = new Map<string, number>()
    let pos = 0
    for (const row of visibleCatalogRows) {
      if (row.kind === 'model') positions.set(row.key, ++pos)
    }
    return positions
  }, [visibleCatalogRows])

  // FR-030: 100 renders whole, 101 does not.
  const virtualiseCatalog = catalogMode && shouldVirtualiseModelList(visibleCatalogModelCount)
  const catalogListRef = React.useRef<HTMLDivElement | null>(null)
  const catalogVirtualizer = useVirtualizer({
    count: virtualiseCatalog ? visibleCatalogRows.length : 0,
    getScrollElement: () => catalogListRef.current,
    estimateSize: () => CATALOG_ROW_HEIGHT,
    overscan: 10,
  })

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
            // Explicit tabIndex: src/lib/tabindex-convention.test.ts requires
            // every native interactive element in src/ to declare one — WebKit
            // does not make buttons tabbable by default, so an omitted tabIndex
            // silently costs keyboard users this control on Safari.
            tabIndex={tabIndex}
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

  // Catalog mode counts as "has models" through its own groups; a filter that
  // matches nothing (FR-019) legitimately lands here as an empty catalogue.
  const catalogEmpty =
    models.length === 0 &&
    (!effectiveProviderGroups || effectiveProviderGroups.every((g) => g.models.length === 0)) &&
    catalogModelCount === 0

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
  //
  // Retry: a provider can report `status: "connected"` (credentials
  // resolvable) while its live models fetch itself failed (e.g. Ollama
  // configured with no local server reachable) — the backend represents
  // that as `models: []` plus a `warning`, NOT as `catalogStatus === 'error'`
  // (Provider.yaml: "Empty array when the upstream fetch fails ..."). Before
  // this fix that state was a dead end: the hint explained WHY but gave the
  // operator no way to try again short of closing the wizard and reopening
  // it, and the "Missing: Model" gate on Next never lifted. `onRetryCatalog`
  // re-runs the SAME providers fetch the "error" state's Retry button uses
  // (`onRetryProviders` → `providersQuery.refetch()`), so a fix made out of
  // band (starting the local server, correcting the endpoint) becomes
  // visible without leaving this screen. This does not reopen the no-free-
  // text rule: retrying can only ever populate the catalogue with real
  // entries, never make an arbitrary slug selectable.
  if (catalogEmpty && constrainToCatalog && !allowFreeTextWhenEmpty && catalogStatus !== 'loading') {
    const isGhost = variant === 'ghost'
    return (
      <div
        data-testid={triggerTestId}
        aria-disabled="true"
        className={
          isGhost
            ? 'flex items-center gap-1.5 h-7 rounded-md px-1.5 text-xs cursor-not-allowed opacity-70'
            : 'flex w-full items-center gap-2 h-10 rounded-md border px-3 py-2 text-sm cursor-not-allowed opacity-70'
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
        <span className="truncate text-xs flex-1">
          {emptyCatalogHint ?? 'No models available — connect a provider first'}
        </span>
        {onRetryCatalog && (
          <button
            type="button"
            onClick={onRetryCatalog}
            // Explicit tabIndex, opted out of the parent's cursor-not-allowed
            // styling (this control itself IS actionable — only the picker
            // as a whole is not). See the `catalogStatus === 'error'` Retry
            // button above for the identical rationale on tabIndex.
            tabIndex={tabIndex}
            data-testid={triggerTestId ? `${triggerTestId}-retry` : undefined}
            className="shrink-0 cursor-pointer text-xs font-medium underline underline-offset-2 hover:opacity-80"
            style={{ color: 'var(--color-accent)' }}
          >
            Retry
          </button>
        )}
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
  const allModels: string[] = catalogMode
    ? visibleCatalogGroups.flatMap((g) => g.models.map((m) => m.id))
    : effectiveProviderGroups && effectiveProviderGroups.length > 0
      ? effectiveProviderGroups.flatMap((g) => g.models)
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
  const groupsWithModels = effectiveProviderGroups
    ? effectiveProviderGroups
        .filter((g) => g.models.length > 0)
        // O3: sort models within each group alphabetically for consistent discovery.
        .map((g) => ({ ...g, models: [...g.models].sort((a, b) => a.localeCompare(b)) }))
    : []
  const useGrouped = groupsWithModels.length >= 1

  // O3: resolve the provider routing key for a given model slug by searching
  // the providerGroups array. Returns the group's `providerId` or empty string.
  const resolveProviderId = (modelSlug: string): string => {
    if (catalogMode) {
      const group = visibleCatalogGroups.find((g) => g.models.some((m) => m.id === modelSlug))
      return group?.providerId ?? ''
    }
    if (!effectiveProviderGroups) return ''
    const group = effectiveProviderGroups.find((g) => g.models.includes(modelSlug))
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

  // FR-030 catalog rendering. Headings are plain rows rather than CommandGroup
  // headings so that the virtualised and whole lists are the SAME row sequence
  // — a virtualiser can only window a flat list, and having one code path means
  // the 101st model cannot render differently from the 100th.
  const renderCatalogHeading = (row: Extract<CatalogRow, { kind: 'heading' }>) => (
    <div
      key={row.key}
      data-testid="model-selector-vendor-heading"
      role="presentation"
      className="px-2 py-1.5 text-[10px] font-semibold uppercase tracking-wider"
      style={{ color: 'var(--color-muted)' }}
    >
      {row.label}
    </div>
  )

  const renderCatalogItem = (row: Extract<CatalogRow, { kind: 'model' }>) => {
    const chosen = value === row.model.id
    return (
      <CommandItem
        key={row.key}
        value={row.model.id}
        onSelect={() => handleSelect(row.model.id)}
        data-model={row.model.id}
        // FR-029: "chosen" is the operator's pick. cmdk's own aria-selected
        // tracks the keyboard cursor, which lands on the first row the moment
        // the list opens — it can never answer "did the user choose one?".
        data-chosen={chosen || undefined}
        data-recommended={row.recommended || undefined}
        // Virtualisation puts only a window of rows in the DOM; without these
        // a screen reader would announce "1 of 11" for a 359-model catalog.
        aria-setsize={visibleCatalogModelCount}
        aria-posinset={catalogPositions.get(row.key)}
        {...(itemTestIdPrefix ? { 'data-testid': `${itemTestIdPrefix}${row.model.id}` } : {})}
      >
        <Check
          size={14}
          className="mr-2 shrink-0"
          style={{ opacity: chosen ? 1 : 0, color: 'var(--color-accent)' }}
        />
        <span className="min-w-0 flex-1 truncate font-mono text-xs">{row.model.id}</span>
        {row.recommended && (
          <span
            className="ml-2 shrink-0 rounded border px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-wider"
            style={{
              backgroundColor: 'color-mix(in srgb, var(--color-accent) 15%, transparent)',
              color: 'var(--color-accent)',
              borderColor: 'color-mix(in srgb, var(--color-accent) 40%, transparent)',
            }}
          >
            {RECOMMENDED_CHIP_LABEL}
          </span>
        )}
      </CommandItem>
    )
  }

  const renderCatalogRow = (row: CatalogRow) =>
    row.kind === 'heading' ? renderCatalogHeading(row) : renderCatalogItem(row)

  const catalogList = virtualiseCatalog ? (
    // Above model-ordering's virtualisation threshold the list is windowed:
    // OpenRouter's 359 rows are ~360 DOM nodes of pure scroll cost otherwise.
    <div
      style={{ height: catalogVirtualizer.getTotalSize(), width: '100%', position: 'relative' }}
      data-testid="model-selector-virtual-list"
    >
      {catalogVirtualizer.getVirtualItems().map((virtualRow) => {
        const row = visibleCatalogRows[virtualRow.index]
        if (!row) return null
        return (
          <div
            key={virtualRow.key}
            style={{
              position: 'absolute',
              top: 0,
              left: 0,
              width: '100%',
              height: virtualRow.size,
              transform: `translateY(${virtualRow.start}px)`,
            }}
          >
            {renderCatalogRow(row)}
          </div>
        )
      })}
    </div>
  ) : (
    <div className="p-1">{visibleCatalogRows.map(renderCatalogRow)}</div>
  )

  const isGhost = variant === 'ghost'

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          role="combobox"
          aria-expanded={open}
          aria-busy={catalogStatus === 'loading' || undefined}
          aria-label={
            label
              // FR-029: with no value the accessible name is the caller's label
              // verbatim ("Model for your first agent"), nothing appended.
              ? value
                ? `${label}, currently ${value}${valueUnresolved ? ' (unresolved)' : ''}`
                : label
              : value
                ? `Model selector, currently ${value}${valueUnresolved ? ' (unresolved)' : ''}`
                : `Model selector, ${displayValue}`
          }
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
          <CommandList ref={catalogListRef} style={{ maxHeight: 300 }}>
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
            {catalogMode ? (
              catalogList
            ) : useGrouped ? (
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
