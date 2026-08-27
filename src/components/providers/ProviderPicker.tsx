// ProviderPicker.tsx — ADR-068 FR-021…FR-026, FR-037: the ONE provider picker.
//
// Onboarding step 3 and Settings -> Providers render this same component
// (FR-021). It is presentational: the catalog document, the operator's already
// configured providers and the fetch status all arrive as props, so the two
// mount points share the query policy in `providersCatalogQuery.ts` rather than
// each growing their own fetch.
//
// Everything shown is derived from the catalog through `provider-picker-model`
// (T068-19) — which companies are Popular tiles, the tile order, the letter
// grouping and the search predicate are all catalog data, never SPA constants
// (spec resolution #1).
//
// Layout is FR-022's order, top to bottom:
//
//   Popular tiles (role="group")
//   Recent (its own listbox, max 3, `updated_at` desc)
//   Search field
//   All providers (N)   <- collapsed until the query is non-empty or toggled
//   [virtualised letter-grouped list]
//   Custom endpoint     <- always last, always selectable (FR-037)
//
// Three implementation notes worth keeping:
//
//  1. cmdk is mounted with `shouldFilter={false}` (FR-026) — the filter is the
//     spec's own (`providerRowMatchesQuery`, literal substring over company /
//     name / plan / region / alias, FR-024). We deliberately do NOT use
//     `CommandItem` for the virtualised rows: cmdk's item registry assumes
//     every item is mounted, which is exactly what virtualisation breaks. The
//     rows are plain `role="option"` elements inside cmdk's listbox.
//
//  2. Keyboard navigation is index-based, not DOM-based (FR-026, MAJ-013). The
//     flat order comes from `pickerRowSequence`; moving to a row that the
//     virtualiser has not mounted calls `scrollToIndex` first and focuses the
//     row once it mounts. That is why an unmounted row is still reachable with
//     ArrowDown/Home/End. Every key we own is handled in a CAPTURE-phase
//     handler on the wrapper so cmdk's own bubble-phase navigation never also
//     fires on the same keystroke.
//
//  3. `aria-setsize` on a rendered option is the number of rows in the
//     virtualised set — i.e. one per company row currently in the filtered
//     list. The "All providers (N)" toggle shows a DIFFERENT number, the
//     catalog's non-Popular entry count (178 in the fixture), because that
//     counts catalog entries while a row counts companies. Deliberate: an
//     `aria-setsize` that disagreed with the `aria-posinset` range would be
//     invalid ARIA, and 4.1.2 is a tested constraint here.

import * as React from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { ArrowClockwise, CircleNotch, Plus, Prohibit, WarningCircle } from '@phosphor-icons/react'
import { Command, CommandInput, CommandList } from '@/components/ui/command'
import type { CatalogProvider, Provider, ProvidersCatalog } from '@/lib/api/generated/openapi-types'
import {
  buildPickerModel,
  pickerRowSequence,
  CUSTOM_ENDPOINT_LABEL,
  CUSTOM_ENDPOINT_ROW_ID,
  type PickerCompanyRow,
  type PickerModel,
  type PickerRowRef,
} from './provider-picker-model'
import { CustomEndpointPanel, type CustomEndpointDraft } from './CustomEndpointPanel'
import { ProviderDetailPanel, type ProviderDetailSelection } from './ProviderDetailPanel'

/** The picker's `performance.mark` name — SC-005 records first paint, never gates it. */
export const PROVIDER_PICKER_OPEN_MARK = 'provider-picker-open'

/** cmdk is always mounted with the spec-owned filter, never its built-in one (FR-026). */
export const PICKER_CMDK_SHOULD_FILTER = false

/** Default list viewport, in CSS px — the 480/40 fixture SC-005 is stated against. */
export const PICKER_VIEWPORT_HEIGHT = 480

/** Default row height, in CSS px. */
export const PICKER_ROW_HEIGHT = 40

/**
 * Rows kept mounted either side of the visible window. SC-005's bound is
 * `floor(viewportHeight / rowHeight) + 10`; at 480/40 that is 22, and
 * 13 visible + 2x4 overscan = 21 stays inside it with headroom.
 */
export const PICKER_OVERSCAN = 4

/**
 * FR-025: the catalog's `unsupported_reason` is never shown raw — "cloud-iam"
 * reads *needs request signing*. A reason we do not know still produces copy
 * rather than an empty disabled row.
 */
export const UNSUPPORTED_REASON_COPY: Record<string, string> = {
  'cloud-iam': 'needs request signing',
  'deployment-url': 'needs a per-deployment URL',
  withdrawn: 'no longer available',
}

export function unsupportedReasonCopy(reason: string | undefined): string {
  if (!reason) return 'not supported yet'
  return UNSUPPORTED_REASON_COPY[reason] ?? 'not supported yet'
}

/**
 * The picker's selection, as the spec's one TS discriminated union
 * (`tile | recent | row | custom` — "Conservative Type Design").
 */
export type PickerSelection =
  | { kind: 'tile'; company: PickerCompanyRow; provider: CatalogProvider }
  | { kind: 'recent'; provider: Provider }
  | { kind: 'row'; company: PickerCompanyRow; provider: CatalogProvider }
  | { kind: 'custom'; draft: CustomEndpointDraft }

export interface ProviderPickerProps {
  /** The catalog document. Undefined while loading or when the GET failed. */
  catalog?: ProvidersCatalog | null
  /** Already-configured providers — the Recent section (max 3, `updated_at` desc). */
  configured?: readonly Provider[]
  /** Catalog fetch state. `error` renders the Retry state; Custom stays selectable (FR-037). */
  status?: 'loading' | 'error' | 'ready'
  /** Retry handler for the error state. */
  onRetry?: () => void
  /** Fired for every selection kind. */
  onSelect: (selection: PickerSelection) => void
  /**
   * FR-027/FR-028 — the second-level panel (T068-21). Supplying this opts the
   * picker into opening `ProviderDetailPanel` when a company is chosen, and is
   * fired with the panel's resolved plan x region x auth-method selection.
   * `onSelect` still fires at the moment of choosing, unchanged; a caller that
   * only wants the first-level selection simply omits this and no panel mounts.
   */
  onProviderConfirm?: (selection: ProviderDetailSelection) => void
  /**
   * Locale driving the panel's region default (FR-027). Defaults to
   * `navigator.language` inside the panel.
   */
  locale?: string | null
  /** T068-33 seam, forwarded to the panel's *Sign in* button. */
  onSignIn?: (providerId: string) => void
  /** List viewport height in CSS px (the virtualiser's window). */
  viewportHeight?: number
  /** Row height in CSS px. */
  rowHeight?: number
  /**
   * Test seam: called with the flat virtual index every time the component asks
   * the virtualiser to scroll (FR-026's "scrollToIndex then focus"). Production
   * callers leave it unset.
   */
  onVirtualScrollToIndex?: (index: number) => void
  /** Focus the search field on mount. Default true. */
  autoFocus?: boolean
  'data-testid'?: string
}

type VirtualEntry =
  | { kind: 'header'; letter: string }
  | { kind: 'row'; row: PickerCompanyRow; position: number }

/** Stable DOM key for a row in the flat keyboard sequence. */
function refKey(ref: PickerRowRef): string {
  return `${ref.kind}:${ref.key}`
}

export function ProviderPicker({
  catalog,
  configured,
  status = 'ready',
  onRetry,
  onSelect,
  onProviderConfirm,
  locale,
  onSignIn,
  viewportHeight = PICKER_VIEWPORT_HEIGHT,
  rowHeight = PICKER_ROW_HEIGHT,
  onVirtualScrollToIndex,
  autoFocus = true,
  'data-testid': testId = 'provider-picker',
}: ProviderPickerProps) {
  const [query, setQuery] = React.useState('')
  const [expandedByOperator, setExpandedByOperator] = React.useState(false)
  const [customOpen, setCustomOpen] = React.useState(false)
  const [detailCompany, setDetailCompany] = React.useState<PickerCompanyRow | null>(null)
  const [activeIndex, setActiveIndex] = React.useState(-1)

  const inputRef = React.useRef<HTMLInputElement | null>(null)
  const scrollRef = React.useRef<HTMLDivElement | null>(null)
  const rowRefs = React.useRef(new Map<string, HTMLElement>())

  // SC-005: first paint is recorded, not gated.
  React.useEffect(() => {
    if (typeof performance !== 'undefined' && typeof performance.mark === 'function') {
      performance.mark(PROVIDER_PICKER_OPEN_MARK)
    }
  }, [])

  React.useEffect(() => {
    if (autoFocus) inputRef.current?.focus()
  }, [autoFocus])

  const model: PickerModel = React.useMemo(
    () => buildPickerModel({ catalog, configured, query, expandedByOperator }),
    [catalog, configured, query, expandedByOperator],
  )

  const sequence = React.useMemo(() => pickerRowSequence(model), [model])

  // The virtualised entries: one header per letter group, then its company rows.
  const entries = React.useMemo<VirtualEntry[]>(() => {
    if (!model.expanded) return []
    const out: VirtualEntry[] = []
    let position = 0
    for (const group of model.letterGroups) {
      out.push({ kind: 'header', letter: group.letter })
      for (const row of group.rows) {
        position += 1
        out.push({ kind: 'row', row, position })
      }
    }
    return out
  }, [model])

  const rowCount = React.useMemo(
    () => entries.reduce((n, entry) => (entry.kind === 'row' ? n + 1 : n), 0),
    [entries],
  )

  const virtualizer = useVirtualizer({
    count: entries.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => rowHeight,
    overscan: PICKER_OVERSCAN,
  })
  const virtualItems = virtualizer.getVirtualItems()

  /** Flat virtual index of a company row, or -1 when the list is collapsed. */
  const virtualIndexOfCompany = React.useCallback(
    (company: string) => entries.findIndex((e) => e.kind === 'row' && e.row.company === company),
    [entries],
  )

  const scrollToVirtualIndex = React.useCallback(
    (index: number) => {
      if (index < 0) return
      onVirtualScrollToIndex?.(index)
      virtualizer.scrollToIndex(index)
    },
    [onVirtualScrollToIndex, virtualizer],
  )

  // FR-026: move by index. A company row the virtualiser has not mounted is
  // scrolled to first; the focus effect below picks it up once it mounts.
  const moveTo = React.useCallback(
    (index: number) => {
      if (sequence.length === 0) return
      const clamped = Math.max(0, Math.min(index, sequence.length - 1))
      const ref = sequence[clamped]
      if (ref.kind === 'company') {
        scrollToVirtualIndex(virtualIndexOfCompany(ref.key))
      } else if (ref.kind === 'custom' && entries.length > 0) {
        // End lands on *Custom endpoint*, and the list behind it is scrolled to
        // its last row so the two agree about where "the end" is.
        scrollToVirtualIndex(entries.length - 1)
      }
      setActiveIndex(clamped)
    },
    [entries.length, scrollToVirtualIndex, sequence, virtualIndexOfCompany],
  )

  const activeRef: PickerRowRef | undefined =
    activeIndex >= 0 && activeIndex < sequence.length ? sequence[activeIndex] : undefined
  const activeKey = activeRef ? refKey(activeRef) : ''

  // Focus follows the active index. Re-runs when the virtual window changes so a
  // row that mounts after `scrollToIndex` still receives focus.
  const mountedSignature = virtualItems.map((item) => item.index).join(',')
  React.useLayoutEffect(() => {
    if (!activeKey) return
    rowRefs.current.get(activeKey)?.focus()
  }, [activeKey, mountedSignature])

  const registerRow = React.useCallback((key: string, el: HTMLElement | null) => {
    if (el) rowRefs.current.set(key, el)
    else rowRefs.current.delete(key)
  }, [])

  const select = React.useCallback(
    (ref: PickerRowRef) => {
      if (ref.kind === 'custom') {
        setCustomOpen(true)
        return
      }
      if (ref.kind === 'recent') {
        const recent = model.recent.find((r) => r.provider.id === ref.key)
        if (recent) onSelect({ kind: 'recent', provider: recent.provider })
        return
      }
      const pool = ref.kind === 'popular' ? model.popular : allRows(model)
      const row = pool.find((r) => r.company === ref.key)
      if (!row || row.disabled) return // FR-025: activating an unsupported row changes nothing.
      onSelect({
        kind: ref.kind === 'popular' ? 'tile' : 'row',
        company: row,
        provider: row.primary,
      })
      // FR-027/FR-028: the second-level panel opens in place, only for a caller
      // that asked for it (T068-21).
      if (onProviderConfirm) setDetailCompany(row)
    },
    [model, onProviderConfirm, onSelect],
  )

  const handleKeyDownCapture = React.useCallback(
    (event: React.KeyboardEvent<HTMLDivElement>) => {
      switch (event.key) {
        case 'ArrowDown':
          event.preventDefault()
          event.stopPropagation()
          moveTo(activeIndex + 1)
          return
        case 'ArrowUp':
          event.preventDefault()
          event.stopPropagation()
          if (activeIndex <= 0) {
            setActiveIndex(-1)
            inputRef.current?.focus()
            return
          }
          moveTo(activeIndex - 1)
          return
        case 'Home':
          event.preventDefault()
          event.stopPropagation()
          moveTo(0)
          return
        case 'End':
          event.preventDefault()
          event.stopPropagation()
          moveTo(sequence.length - 1)
          return
        case 'Enter': {
          if (!activeRef) return
          event.preventDefault()
          event.stopPropagation()
          select(activeRef)
          return
        }
        default:
      }
    },
    [activeIndex, activeRef, moveTo, select, sequence.length],
  )

  const listId = `${testId}-all-list`
  const showList = model.expanded && status === 'ready'

  return (
    <div data-testid={testId} className="flex flex-col gap-3">
      {/*
       * The capture-phase handler below owns Home/End/Arrow/Enter for the
       * picker's OWN rows (FR-026, MAJ-013 — see the file header). It must
       * never also see those keys when they originate inside a nested panel
       * (ProviderDetailPanel's plan/region buttons and its API-key input;
       * CustomEndpointPanel's form fields) — a text field there needs its own
       * Home/End for cursor movement, and Enter needs to submit ITS form, not
       * re-trigger `select(activeRef)` on whatever picker row happened to be
       * active underneath.
       *
       * Enumerating tag names (skip when `event.target` is an <input>, etc.)
       * is exactly the kind of check that silently stops working the next
       * time a nested panel adds a new interactive element — nobody updates a
       * remote condition like that when they add a field. Scoping the
       * listener to a DOM subtree that structurally EXCLUDES the nested
       * panels is durable instead: `ProviderDetailPanel` and
       * `CustomEndpointPanel` are deliberately rendered as siblings of this
       * inner wrapper (below), not descendants of it, so the capture phase
       * (root -> target) never reaches this handler for a key that started
       * inside either panel — no target inspection needed, and it stays
       * correct automatically as those panels grow.
       */}
      <div onKeyDownCapture={handleKeyDownCapture} className="contents">
      {/* ── Popular tiles (FR-022) ─────────────────────────────────────── */}
      <div
        role="group"
        aria-label="Popular providers"
        data-testid="picker-popular"
        className="grid grid-cols-4 gap-2"
      >
        {model.popular.map((row) => {
          const key = refKey({ kind: 'popular', key: row.company })
          return (
            <button
              key={row.company}
              type="button"
              ref={(el) => registerRow(key, el)}
              data-testid={`picker-popular-${row.primary.id}`}
              tabIndex={-1}
              aria-disabled={row.disabled || undefined}
              onClick={() => select({ kind: 'popular', key: row.company })}
              className="flex min-h-[44px] items-center justify-center rounded-md border px-3 py-2 text-sm"
              style={{ borderColor: 'var(--color-border)', color: 'var(--color-secondary)' }}
            >
              {row.company}
            </button>
          )
        })}
      </div>

      <Command
        shouldFilter={PICKER_CMDK_SHOULD_FILTER}
        label="Providers"
        data-testid="picker-command"
        className="gap-2"
      >
        {/* ── Recent (FR-022: between the Popular band and the search field) ── */}
        {model.recent.length > 0 && (
          // Recent is its own listbox, not a group: an element with role="option"
          // must have a listbox PARENT (ARIA aria-required-parent — an axe
          // "serious" violation otherwise) and these rows sit outside cmdk's own
          // list. The heading stays OUTSIDE that listbox for the mirror-image
          // rule, aria-required-children.
          <div data-testid="picker-recent" className="flex flex-col">
            <span
              id={`${testId}-recent-label`}
              className="px-2 py-1 text-xs uppercase"
              style={{ color: 'var(--color-muted)' }}
            >
              Recent
            </span>
            <div role="listbox" aria-labelledby={`${testId}-recent-label`} className="flex flex-col">
              {model.recent.map((recent) => {
                const key = refKey({ kind: 'recent', key: recent.provider.id })
                return (
                  <div
                    key={recent.provider.id}
                    role="option"
                    aria-selected={activeKey === key}
                    tabIndex={-1}
                    ref={(el) => registerRow(key, el)}
                    data-testid={`picker-recent-${recent.provider.id}`}
                    onClick={() => select({ kind: 'recent', key: recent.provider.id })}
                    className="flex min-h-[32px] cursor-pointer items-center rounded px-2 py-1 text-sm"
                    style={{ color: 'var(--color-secondary)' }}
                  >
                    {recent.label}
                  </div>
                )
              })}
            </div>
          </div>
        )}

        <CommandInput
          ref={inputRef}
          value={query}
          onValueChange={(next) => {
            setQuery(next)
            setActiveIndex(-1)
          }}
          placeholder="Search providers"
          data-testid="picker-search"
          aria-label="Search providers"
        />

        <button
          type="button"
          data-testid="picker-all-toggle"
          tabIndex={0}
          aria-expanded={model.expanded}
          aria-controls={listId}
          onClick={() => setExpandedByOperator((v) => !v)}
          className="flex min-h-[32px] items-center justify-between rounded px-2 text-sm"
          style={{ color: 'var(--color-secondary)' }}
        >
          All providers ({model.allProvidersCount})
        </button>

        {status === 'loading' && (
          <div data-testid="picker-catalog-loading" className="flex items-center gap-2 px-2 py-3 text-sm">
            <CircleNotch size={14} className="animate-spin" aria-hidden="true" />
            Loading providers…
          </div>
        )}

        {/* FR-037: the catalog GET failed. The error is explicit, retryable, and
            Custom endpoint below stays selectable so onboarding can proceed. */}
        {status === 'error' && (
          <div
            role="alert"
            aria-live="assertive"
            data-testid="picker-catalog-error"
            className="flex items-center gap-2 rounded border px-2 py-3 text-sm"
            style={{ borderColor: 'var(--color-border)', color: 'var(--color-secondary)' }}
          >
            <WarningCircle size={14} weight="fill" aria-hidden="true" />
            <span>Provider catalog unavailable. You can still add a custom endpoint.</span>
            <button
              type="button"
              data-testid="picker-catalog-retry"
              tabIndex={0}
              onClick={() => onRetry?.()}
              className="ml-auto flex min-h-[24px] items-center gap-1 rounded border px-2"
              style={{ borderColor: 'var(--color-border)' }}
            >
              <ArrowClockwise size={12} aria-hidden="true" />
              Retry
            </button>
          </div>
        )}

        <CommandList
          id={listId}
          ref={scrollRef}
          data-testid="picker-virtual-viewport"
          style={{ height: showList ? viewportHeight : 0, maxHeight: viewportHeight, overflowY: 'auto' }}
          hidden={!showList}
        >
          {showList && (
            <div style={{ height: virtualizer.getTotalSize(), position: 'relative', width: '100%' }}>
              {virtualItems.map((item) => {
                const entry = entries[item.index]
                if (!entry) return null
                const common: React.CSSProperties = {
                  position: 'absolute',
                  top: 0,
                  left: 0,
                  width: '100%',
                  height: item.size,
                  transform: `translateY(${item.start}px)`,
                }
                if (entry.kind === 'header') {
                  return (
                    <div
                      key={`h-${entry.letter}`}
                      role="presentation"
                      data-testid={`picker-letter-${entry.letter}`}
                      style={{ ...common, color: 'var(--color-muted)' }}
                      className="flex items-center px-2 text-xs uppercase"
                    >
                      {entry.letter}
                    </div>
                  )
                }
                const row = entry.row
                const key = refKey({ kind: 'company', key: row.company })
                const reason = row.disabled ? unsupportedReasonCopy(row.unsupportedReason) : undefined
                return (
                  <div
                    key={row.company}
                    role="option"
                    tabIndex={-1}
                    ref={(el) => registerRow(key, el)}
                    data-testid={`picker-row-${row.company}`}
                    aria-selected={activeKey === key}
                    aria-disabled={row.disabled || undefined}
                    aria-setsize={rowCount}
                    aria-posinset={entry.position}
                    onClick={() => select({ kind: 'company', key: row.company })}
                    style={{ ...common, color: 'var(--color-secondary)' }}
                    className="flex cursor-pointer items-center gap-2 px-2 text-sm"
                  >
                    {row.disabled && <Prohibit size={14} aria-hidden="true" />}
                    <span>{row.company}</span>
                    {reason && (
                      <span style={{ color: 'var(--color-muted)' }} className="text-xs">
                        {reason}
                      </span>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </CommandList>

        {model.expanded && !model.hasMatches && model.emptyMessage && (
          <div data-testid="picker-empty" className="px-2 py-3 text-sm" style={{ color: 'var(--color-muted)' }}>
            {model.emptyMessage}
          </div>
        )}

        {/* ── Custom endpoint — always the last row (FR-022, FR-024) ─────── */}
        {/* Its own listbox, same aria-required-parent reason as Recent above. */}
        <div role="listbox" aria-label="Custom endpoint">
          <div
            role="option"
            tabIndex={-1}
            ref={(el) => registerRow(refKey({ kind: 'custom', key: CUSTOM_ENDPOINT_ROW_ID }), el)}
            data-testid="picker-custom-endpoint"
            aria-selected={activeKey === `custom:${CUSTOM_ENDPOINT_ROW_ID}`}
            onClick={() => select({ kind: 'custom', key: CUSTOM_ENDPOINT_ROW_ID })}
            className="flex min-h-[32px] cursor-pointer items-center gap-2 rounded px-2 py-1 text-sm"
            style={{ color: 'var(--color-secondary)' }}
          >
            <Plus size={14} aria-hidden="true" />
            {CUSTOM_ENDPOINT_LABEL}
          </div>
        </div>
      </Command>
      </div>

      {detailCompany && onProviderConfirm && (
        <ProviderDetailPanel
          key={detailCompany.company}
          company={detailCompany}
          locale={locale}
          onSignIn={onSignIn}
          onConfirm={(selection) => {
            setDetailCompany(null)
            onProviderConfirm(selection)
          }}
          onCancel={() => setDetailCompany(null)}
        />
      )}

      {customOpen && (
        <CustomEndpointPanel
          onSubmit={(draft) => {
            setCustomOpen(false)
            onSelect({ kind: 'custom', draft })
          }}
          onCancel={() => setCustomOpen(false)}
        />
      )}
    </div>
  )
}

function allRows(model: PickerModel): PickerCompanyRow[] {
  return model.letterGroups.flatMap((group) => group.rows)
}
