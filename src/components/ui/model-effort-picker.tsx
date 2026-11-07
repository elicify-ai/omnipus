// model-effort-picker.tsx — the shared model-and-effort picker
// (effort-levels-spec-r10.md, "Design-System Components", "Presentation by
// capability", "Accessibility and Keyboard"; FR-019, FR-052, FR-053, FR-054;
// founder P-Q1 A to P-Q5 A).
//
// Two exports, one domain file (it reads the served providers catalog through
// the SPA's catalog query, so it cannot be part of the reusable package):
//   - ModelEffortPicker: the effort radio group for one model — Not set (where
//     something sits above the slot), Provider default, then the model's own
//     levels / Off / On, exactly as the served `reasoning` object allows.
//   - EffortControl: the chip + popover around it, including the blocked
//     pick-a-level / "Let the provider decide" step of a pending model switch
//     (useEffortSwitch) and the mapping note with its Change link.
//
// Commit model: the commit is the change (no confirm step, P-Q4 A). A click,
// Enter or Space on an item commits it; arrow keys only move the selection
// (the catalogued RadioGroup moves focus and selection together, the commit
// waits for Enter/Space so a keyboard user can move without saving).

import * as React from 'react'
import { useEffect, useId, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { CaretDown, CircleNotch, Info } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { providersCatalogQueryOptions } from '@/lib/providersCatalogQuery'
import type { ProvidersCatalog } from '@/lib/api'
import { effortLabel, mapEffort, PROVIDER_DEFAULT } from '@/lib/effort/mapEffort'
import {
  effortItems,
  effortTargetOf,
  findServedRow,
  itemForStored,
  levelItems,
  presentationOf,
  type EffortItem,
  type ServedRow,
} from '@/lib/effort/servedReasoning'
import type { EffortPendingSwitch } from '@/lib/effort/useEffortSwitch'
import { Button } from './button'
import { FormError } from './FormError'
import { Popover, PopoverContent, PopoverTrigger } from './popover'
import { RadioGroup, RadioGroupItem } from './radio-group'
import { Tooltip } from './tooltip'

export interface EffortValue {
  provider: string
  model: string
  /** Stored effort: '' (Not set), 'provider_default', a level, 'on', 'off'. */
  effort: string
}

export interface EffortChangeMeta {
  /** Note shown with the change (mapping note), if any. */
  note?: string
}

export type EffortScope =
  | 'chat'
  | 'agent-primary'
  | 'agent-fallback'
  | 'default-model'
  | 'recap-model'
  | 'recap-fallback'
  | 'onboarding'
  | 'replacement'

export interface ModelEffortPickerProps {
  value: EffortValue
  onChange?: (effort: string, meta?: EffortChangeMeta) => void
  /**
   * The inherited value a Not set slot resolves to (chat: the agent's
   * setting; an agent primary riding the default model: the default model's).
   * The chat always offers Not set; elsewhere Not set is offered only when
   * this is given (FR-044, "Stored states").
   */
  inherit?: string
  scope: EffortScope
  /** Contextual accessible name, e.g. "Effort for fallback 2, MiniMax M3". */
  contextName?: string
  /** Recap slots default to Off and have no Not set row (FD-19). */
  defaultEffort?: 'off'
}

export const MUTED_EFFORT_TEXT = "Omnipus can't change effort for this model."

/** The effort popover's help line (wireframe effort popover). */
const EFFORT_HELP_TEXT = 'How much the model thinks before answering. More effort is slower and costs more.'

const NOT_SET = ''

/** The one catalog read every picker shares (ADR-068's catalog query). */
function useServedCatalog() {
  const query = useQuery(providersCatalogQueryOptions())
  return {
    catalog: query.data,
    status: query.status,
    retry: () => {
      void query.refetch()
    },
  }
}

function inheritPrefix(scope: EffortScope): string {
  return scope === 'agent-primary' ? "Default model's setting" : "Agent's setting"
}

function offersNotSet(props: Pick<ModelEffortPickerProps, 'scope' | 'inherit' | 'defaultEffort'>): boolean {
  if (props.defaultEffort === 'off') return false
  return props.scope === 'chat' || props.inherit !== undefined
}

/** What a stored value means on this slot: '' on a recap slot is Off; '' without a Not set row is Provider default. */
function effectiveStored(props: Pick<ModelEffortPickerProps, 'scope' | 'inherit' | 'defaultEffort'>, stored: string): string {
  if (stored !== NOT_SET) return stored
  if (props.defaultEffort === 'off') return 'off'
  return offersNotSet(props) ? NOT_SET : PROVIDER_DEFAULT
}

/** "Agent's setting: High", plus ", sent as Medium here" / ", nothing sent here" when the model differs (S-12, S-72, S-87). */
function inheritedText(
  scope: EffortScope,
  inherit: string | undefined,
  row: ServedRow,
): { text: string; differs: boolean; used: string | null } {
  const inherited = inherit && inherit !== '' ? inherit : PROVIDER_DEFAULT
  const base = `${inheritPrefix(scope)}: ${effortLabel(inherited)}`
  const mapping = mapEffort(inherited, effortTargetOf(row))
  if (mapping.blocked) return { text: `${base}, nothing sent here`, differs: true, used: null }
  const same =
    mapping.value === inherited || ((mapping.value === 'none' || mapping.value === 'off') && (inherited === 'off' || inherited === 'none'))
  if (same || mapping.value === NOT_SET || mapping.value === PROVIDER_DEFAULT) return { text: base, differs: false, used: inherited }
  return { text: `${base}, sent as ${effortLabel(mapping.value)} here`, differs: true, used: mapping.value }
}

/** The chip's visible word when nothing is sent (wireframe `chipLabel`). */
const NOTHING_SENT = 'Nothing sent'


interface EffortRadioListProps {
  items: EffortItem[]
  /** The checked item's value; undefined when none is checked (stale value, blocked step). */
  checked: string | undefined
  onCommit: (value: string) => void
  disabled?: boolean
  ariaLabel: string
  /** The blocked step's suggested level, marked "nearest" (S-10). */
  nearest?: string
}

const NO_SELECTION = '\u0000none'

/** Not set and Provider default are full-width rows; everything else is a level button. */
function isTopRow(value: string): boolean {
  return value === NOT_SET || value === PROVIDER_DEFAULT
}

/** Lit bars per value in the wireframes' five-bar glyph (preview `barsHtml`). */
const LIT_BARS: Record<string, number> = { off: 0, none: 0, minimal: 1, low: 1, medium: 2, high: 3, xhigh: 4, max: 5, on: 3 }
const BAR_HEIGHTS = [3, 5, 7, 9, 10]

/** The small signal-bar glyph of a level (decoration; the level's name carries the meaning). */
function EffortBars({ value }: { value: string }) {
  const lit = LIT_BARS[value]
  if (lit === undefined) return null
  return (
    <svg aria-hidden="true" data-effort-bars={lit} viewBox="0 0 14 10" width="14" height="10" fill="currentColor" className="shrink-0">
      {BAR_HEIGHTS.map((height, index) => (
        <rect key={height} x={index * 3} y={10 - height} width="2" height={height} rx="1" fillOpacity={index < lit ? 1 : 0.25} />
      ))}
    </svg>
  )
}

/**
 * A top row's content: radio dot, name and muted sub-text. Not set keeps its
 * text "Not set: Agent's setting: High" (the ": " is screen-reader only);
 * Provider default draws "provider decides" from an attribute so its text
 * and name stay "Provider default".
 */
function TopRowContent({ item, checked }: { item: EffortItem; checked: boolean }) {
  const split = item.value === NOT_SET ? item.label.indexOf(': ') : -1
  const name = split >= 0 ? item.label.slice(0, split) : item.label
  const sub = split >= 0 ? item.label.slice(split + 2) : null
  return (
    <>
      <span
        aria-hidden="true"
        data-effort-dot=""
        className={cn(
          'h-[var(--space-2-5)] w-[var(--space-2-5)] shrink-0 rounded-full border',
          checked ? 'border-[var(--color-accent)] bg-[var(--color-accent)]' : 'border-[var(--color-muted)]',
        )}
      />
      <span>{name}</span>
      {sub !== null ? (
        <>
          <span className="sr-only">:</span>{' '}
          <span data-effort-sub="" className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
            {sub}
          </span>
        </>
      ) : (
        <span
          aria-hidden="true"
          data-effort-sub="provider decides"
          className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] after:content-[attr(data-effort-sub)]"
        />
      )}
    </>
  )
}

/**
 * The radio list (FD-16 P-Q1 A, "Design-System Components"): one radio group,
 * Not set and Provider default as full-width rows on top, then the model's own
 * levels (or Off / On) as a row of named buttons under a "Level" / "Reasoning"
 * label. Arrows move the selection across both rows; click, Enter or Space
 * commits. The items stay direct children of the group so its roving tab stop
 * sees them; a wrapping flex row puts the full-width rows and the label on
 * their own lines and shares one line among the level buttons.
 */
function EffortRadioList({ items, checked, onCommit, disabled, ariaLabel, nearest }: EffortRadioListProps) {
  const [selected, setSelected] = useState<string>(checked ?? NO_SELECTION)
  const nearestId = useId()
  useEffect(() => {
    setSelected(checked ?? NO_SELECTION)
  }, [checked])
  const levels = items.filter((item) => !isTopRow(item.value))
  const onlySwitch = levels.length > 0 && levels.every((item) => item.value === 'off' || item.value === 'on')
  const renderItem = (item: EffortItem) => {
    const top = isTopRow(item.value)
    const isNearest = !top && item.value === nearest
    return (
      <RadioGroupItem
        key={item.value === NOT_SET ? 'not-set' : item.value}
        value={item.value}
        onClick={(event) => {
          event.preventDefault()
          setSelected(item.value)
          onCommit(item.value)
        }}
        aria-describedby={isNearest ? nearestId : undefined}
        className={top ? 'basis-full justify-start gap-[var(--space-2)] whitespace-normal p-[var(--space-2)] text-left' : 'w-auto min-w-0 flex-1 basis-0 flex-col whitespace-normal justify-center gap-[var(--space-0-5)] px-[var(--space-1)] py-[var(--space-2)] text-center'}
      >
        {top ? <TopRowContent item={item} checked={selected === item.value} /> : <EffortBars value={item.value} />}
        {!top && item.label}
        {isNearest && (
          <span aria-hidden="true" className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
            nearest
          </span>
        )}
      </RadioGroupItem>
    )
  }
  return (
    <RadioGroup
      aria-label={ariaLabel}
      orientation="vertical"
      value={selected}
      onValueChange={setSelected}
      disabled={disabled}
      className="flex-row flex-wrap gap-[var(--space-1)]"
    >
      {items.filter((item) => isTopRow(item.value)).map(renderItem)}
      {levels.length > 0 && (
        <span aria-hidden="true" className="basis-full pt-[var(--space-1)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
          {onlySwitch ? 'Reasoning' : 'Level'}
        </span>
      )}
      {levels.map(renderItem)}
      {nearest !== undefined && (
        <span id={nearestId} className="sr-only">
          Nearest level
        </span>
      )}
    </RadioGroup>
  )
}

interface PickerBodyProps extends ModelEffortPickerProps {
  catalogStatus: 'pending' | 'error' | 'success'
  row: ServedRow
  onRetry: () => void
  onCommit: (value: string) => void
}

/**
 * Catalog loading or failed (S-07): the stored value as text, no choices; a
 * failure offers Retry. Copy as the wireframes word it: "Loading model
 * details. Saved effort: High." / "Couldn't load model details. Saved effort:
 * High. Retry".
 */
function CatalogStateLine({ status, saved, onRetry }: { status: 'pending' | 'error'; saved: string; onRetry: () => void }) {
  const value = <span>{effortLabel(saved)}</span>
  if (status === 'pending') {
    return (
      <span className="inline-flex items-center gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
        <CircleNotch data-effort-spinner="" size={12} aria-hidden="true" className="shrink-0 animate-spin motion-reduce:animate-none" />
        <span>Loading model details. Saved effort: {value}.</span>
      </span>
    )
  }
  return (
    <span className="inline-flex flex-wrap items-center gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)]">
      <FormError error="Couldn't load model details." className="mt-0" />
      <span className="text-[var(--color-muted)]">Saved effort: {value}.</span>
      <Button type="button" variant="link" size="sm" onClick={onRetry}>
        Retry
      </Button>
    </span>
  )
}

function PickerBody({ catalogStatus, row, onRetry, onCommit, ...props }: PickerBodyProps) {
  const stored = effectiveStored(props, props.value.effort)
  if (catalogStatus !== 'success') {
    return <CatalogStateLine status={catalogStatus} saved={stored} onRetry={onRetry} />
  }
  const presentation = presentationOf(row)
  if (presentation === 'none') return null
  if (presentation === 'muted') {
    return <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">{MUTED_EFFORT_TEXT}</p>
  }
  const notSetLabel = offersNotSet(props) ? `Not set: ${inheritedText(props.scope, props.inherit, row).text}` : null
  const items = effortItems(effortTargetOf(row), notSetLabel)
  return (
    <EffortRadioList
      items={items}
      checked={itemForStored(items, stored)?.value}
      onCommit={onCommit}
      ariaLabel={props.contextName ?? 'Effort'}
    />
  )
}

/** The effort radio group for one model (no chip, no popover). */
export function ModelEffortPicker(props: ModelEffortPickerProps) {
  const { catalog, status, retry } = useServedCatalog()
  const row = findServedRow(catalog, props.value.provider, props.value.model)
  return (
    <PickerBody
      {...props}
      catalogStatus={status}
      row={row}
      onRetry={retry}
      onCommit={(effort) => props.onChange?.(effort)}
    />
  )
}

export interface EffortControlProps extends ModelEffortPickerProps {
  /** A model switch waiting on the blocked step (useEffortSwitch). */
  pendingSwitch?: EffortPendingSwitch | null
  /** The blocked step was answered: a level, or Provider default. */
  onSwitchComplete?: (effort: string) => void
  /** The blocked step was cancelled (Escape, Cancel or a click outside). */
  onSwitchCancel?: () => void
  /** Where focus returns after a cancelled switch (the model button, FR-053). */
  cancelFocusTarget?: () => HTMLElement | null
  /** The mapping note of the last switch; shows with a Change link (P-Q4 A). */
  note?: string | null
  /** The value was set by `/effort` in this conversation (gold dot, S-41). */
  setByCommand?: boolean
  /** Polite announcement for the picker's one live region. */
  announcement?: string
  /** The chip element (focus target after a failed save, S-57). */
  chipRef?: React.Ref<HTMLButtonElement>
  /** The chip cannot be opened (another write is in flight, FR-054). */
  disabled?: boolean
  /** Explicit tab-order position (the chat composer's closed tab ring). */
  tabIndex?: number
  'data-testid'?: string
  className?: string
}

interface ChipDescription {
  /**
   * The chip's visible text: the level actually used (founder C1 to C3,
   * 2026-11-07, wireframe `chipLabel`). Where it comes from and what was
   * saved live in the accessible name (`spoken`) and the dashed outline.
   */
  text: string
  /** The value part of the accessible name. */
  spoken: string
  inherited: boolean
  amber: boolean
  /** A visible sentence under the chip explaining the amber state, if any. */
  hint?: string
  /** The value the chip's bar glyph draws, if any. */
  bars?: string
  /** A stored level this model no longer offers: the popover's warning sentence. */
  staleReason?: string
}

/** The wireframes' stale-level sentences: the popover reason and the settings line. */
function staleSentences(stored: string, model: string, sent: string | null): { reason: string; line: string } {
  const phrase = `effort "${effortLabel(stored)}" is no longer offered by ${model}.`
  if (sent === null) {
    const tail = 'Turns go out with no effort setting until you pick one.'
    return { reason: `Your saved ${phrase} ${tail}`, line: `Saved ${phrase} ${tail}` }
  }
  return {
    reason: `Your saved ${phrase} Turns use ${sent}, the nearest level, until you pick one.`,
    line: `Saved ${phrase} Turns use ${sent} (nearest) until you pick one.`,
  }
}

/** Recap slots on a model that cannot turn reasoning off (Interaction states, Settings Memory). */
export const RECAP_OFF_NOT_SENDABLE_TEXT = "This model can't turn reasoning off; the recap sends no effort setting."

function describeChip(props: EffortControlProps, row: ServedRow, items: EffortItem[]): ChipDescription {
  const stored = effectiveStored(props, props.value.effort)
  if (stored === NOT_SET) {
    const inherited = inheritedText(props.scope, props.inherit, row)
    const text = inherited.used === null ? NOTHING_SENT : effortLabel(inherited.used)
    return {
      text,
      spoken: `Not set: ${inherited.text}`,
      inherited: true,
      amber: inherited.differs,
      bars: inherited.used ?? undefined,
    }
  }
  const label = effortLabel(stored)
  if (itemForStored(items, stored) === undefined) {
    if (props.defaultEffort === 'off' && (stored === 'off' || stored === 'none')) {
      // A recap slot keeps reading Off; the amber dot and one sentence say nothing is sent.
      return { text: 'Off', spoken: 'Off', inherited: false, amber: true, hint: RECAP_OFF_NOT_SENDABLE_TEXT }
    }
    // A stored value this model no longer offers keeps its text; say what is sent (FR-022).
    const mapping = mapEffort(stored, effortTargetOf(row))
    const nothingSent = mapping.blocked || mapping.value === PROVIDER_DEFAULT || mapping.value === NOT_SET
    const sent = nothingSent ? 'nothing sent here' : `sent as ${effortLabel(mapping.value)} here`
    const sentences = staleSentences(stored, props.value.model, nothingSent ? null : effortLabel(mapping.value))
    return {
      text: nothingSent ? NOTHING_SENT : effortLabel(mapping.value),
      spoken: `${label}, ${sent}`,
      inherited: false,
      amber: true,
      staleReason: sentences.reason,
      // Settings surfaces carry the sentence under the chip; the composer only in its popover.
      hint: props.scope === 'chat' ? undefined : sentences.line,
    }
  }
  return {
    text: label,
    spoken: props.setByCommand ? `${label}, set for this conversation` : label,
    inherited: false,
    amber: false,
    bars: stored,
  }
}

/** The popover's title "Effort · {model}", then the help line, or the stale-level warning instead of it. */
function PopoverHeader({ model, staleReason }: { model: string; staleReason?: string }) {
  return (
    <div>
      <p className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">
        Effort <span className="text-[var(--color-muted)]">· {model}</span>
      </p>
      {staleReason === undefined ? (
        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">{EFFORT_HELP_TEXT}</p>
      ) : (
        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-warning)]">{staleReason}</p>
      )}
    </div>
  )
}

/** The effort chip and its popover (S-07, S-10 to S-14, S-54, S-57, S-73, S-74). */
export function EffortControl(props: EffortControlProps) {
  const {
    pendingSwitch = null,
    onSwitchComplete,
    onSwitchCancel,
    cancelFocusTarget,
    note,
    announcement = '',
    chipRef,
    disabled = false,
    tabIndex,
    className,
  } = props
  const { catalog, status, retry } = useServedCatalog()
  const [open, setOpen] = useState(false)
  const cancelledRef = useRef(false)
  const contentRef = useRef<HTMLDivElement>(null)
  const descriptionId = useId()
  const hintId = useId()
  const row = findServedRow(catalog, props.value.provider, props.value.model)
  const name = props.contextName ?? 'Effort'
  const popoverOpen = open || pendingSwitch !== null
  // The nothing-sendable step's spoken sentence (Accessibility table); the
  // step itself shows the wireframe's longer text.
  const spoken =
    pendingSwitch?.kind === 'nothing_sendable' ? `${pendingSwitch.model} can't turn reasoning off here. Let the provider decide, or cancel.` : announcement

  // The picker's one polite live region (FR-053). It carries the mapping note
  // with its Change link when a switch remapped the value, else the latest
  // announcement ("Switch cancelled", "{model} has no effort setting.").
  const liveRegion = (
    <span
      role="status"
      aria-live="polite"
      aria-atomic="true"
      className={cn(note ? 'inline-flex items-center gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]' : 'sr-only')}
    >
      {note ? (
        <>
          <span>{note}</span>
          <Button type="button" variant="link" size="sm" onClick={() => setOpen(true)}>
            Change
          </Button>
        </>
      ) : (
        spoken
      )}
    </span>
  )

  function closeWithCancel() {
    cancelledRef.current = true
    setOpen(false)
    onSwitchCancel?.()
  }

  function handleOpenChange(next: boolean) {
    if (next) {
      setOpen(true)
      return
    }
    if (pendingSwitch) {
      closeWithCancel()
      return
    }
    setOpen(false)
  }

  function commit(effort: string) {
    cancelledRef.current = false
    setOpen(false)
    if (pendingSwitch) {
      onSwitchComplete?.(effort)
      return
    }
    if (effort !== props.value.effort) props.onChange?.(effort)
  }

  if (status !== 'success' && pendingSwitch === null) {
    // Loading / failed: stored value as text, no choices; failure offers Retry (S-07).
    return (
      <span className={cn('inline-flex items-center gap-[var(--space-2)]', className)}>
        <CatalogStateLine status={status} saved={effectiveStored(props, props.value.effort)} onRetry={retry} />
        {liveRegion}
      </span>
    )
  }

  const presentation = presentationOf(row)
  if (presentation === 'none' && pendingSwitch === null) return liveRegion

  if (presentation === 'muted' && pendingSwitch === null) {
    if (props.scope !== 'chat') {
      // Settings surfaces: one muted line (FR-052).
      return (
        <span className={cn('inline-flex items-center gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]', className)}>
          <Info size={12} aria-hidden="true" />
          {MUTED_EFFORT_TEXT}
          {liveRegion}
        </span>
      )
    }
    return (
      <span className={cn('inline-flex items-center', className)}>
        <Tooltip content={MUTED_EFFORT_TEXT} interactive>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            aria-disabled="true"
            tabIndex={tabIndex}
            aria-label={name}
            aria-describedby={descriptionId}
            data-testid={props['data-testid']}
            className="gap-[var(--space-1)] text-[var(--color-muted)]"
          >
            <Info size={12} aria-hidden="true" />
            Effort
          </Button>
        </Tooltip>
        <span id={descriptionId} className="sr-only">
          {MUTED_EFFORT_TEXT}
        </span>
        {liveRegion}
      </span>
    )
  }

  const items = effortItems(effortTargetOf(row), offersNotSet(props) ? `Not set: ${inheritedText(props.scope, props.inherit, row).text}` : null)
  const chip = describeChip(props, row, items)
  // Settings surfaces show the chip as a field: an "Effort" key and a caret (wireframe shot-3).
  const field = props.scope !== 'chat'

  function focusInitial() {
    const content = contentRef.current
    if (!content) return
    let target: HTMLElement | null
    if (pendingSwitch?.kind === 'nothing_sendable') {
      target = content.querySelector<HTMLElement>('[data-effort-action="provider-default"]')
    } else if (pendingSwitch) {
      const radios = Array.from(content.querySelectorAll<HTMLElement>('[role="radio"]'))
      target = radios.find((radio) => radio.dataset.radioValue === pendingSwitch.nearest) ?? null
    } else {
      target = content.querySelector<HTMLElement>('[role="radio"][aria-checked="true"]')
    }
    target ??= content.querySelector<HTMLElement>('[role="radio"]:not([disabled])')
    target?.focus()
  }

  return (
    <span className={cn('inline-flex flex-wrap items-center gap-[var(--space-2)]', className)}>
      {/* Picking a value: modal, the rest of the page is inert until the
          commit or Escape. The blocked step of a pending switch is not modal:
          the slot's model button and chip stay in the page for assistive
          technology, and a click outside or Escape cancels the switch. */}
      <Popover open={popoverOpen} onOpenChange={handleOpenChange} modal={pendingSwitch === null}>
        <PopoverTrigger asChild>
          <Button
            ref={chipRef}
            type="button"
            variant="ghost"
            size="sm"
            disabled={disabled && pendingSwitch === null}
            tabIndex={tabIndex}
            aria-label={`${name}: ${chip.spoken}`}
            aria-describedby={chip.hint ? hintId : undefined}
            data-testid={props['data-testid']}
            data-effort-inherited={chip.inherited ? 'true' : undefined}
            className={cn(
              'gap-[var(--space-1)] border',
              chip.inherited ? 'border-dashed border-[var(--color-border)]' : 'border-solid border-[var(--color-border)]',
            )}
          >
            {field && (
              <span aria-hidden="true" className="text-[var(--color-muted)]">
                Effort
              </span>
            )}
            {chip.amber && (
              <span aria-hidden="true" data-effort-mark="attention" className="h-[var(--space-2)] w-[var(--space-2)] rounded-full bg-[var(--color-warning)]" />
            )}
            {props.setByCommand && !chip.amber && (
              <span aria-hidden="true" data-effort-mark="command" className="h-[var(--space-2)] w-[var(--space-2)] rounded-full bg-[var(--color-accent)]" />
            )}
            {!chip.amber && !props.setByCommand && chip.bars !== undefined && <EffortBars value={chip.bars} />}
            {chip.text}
            {field && <CaretDown size={12} aria-hidden="true" className="shrink-0" />}
          </Button>
        </PopoverTrigger>
        <PopoverContent
          ref={contentRef}
          align="start"
          className="w-72"
          onFocusOutside={(event) => {
            // The model list closing hands focus back to its own button; that
            // must not cancel the pending switch. Escape, Cancel or a click
            // outside still do.
            if (pendingSwitch) event.preventDefault()
          }}
          onOpenAutoFocus={(event) => {
            event.preventDefault()
            // After the model popover closes its own focus return runs on a
            // timer; focusing on the next tick keeps focus here (FR-053).
            setTimeout(focusInitial, 0)
          }}
          onCloseAutoFocus={(event) => {
            if (!cancelledRef.current) return
            cancelledRef.current = false
            const target = cancelFocusTarget?.()
            if (target) {
              event.preventDefault()
              target.focus()
            }
          }}
        >
          {pendingSwitch ? (
            <BlockedStep
              pending={pendingSwitch}
              previousModel={props.value.model}
              catalogRow={findServedRow(catalog, pendingSwitch.provider, pendingSwitch.model)}
              onCommit={commit}
              onCancel={closeWithCancel}
            />
          ) : (
            <div className="space-y-[var(--space-2)]">
              <PopoverHeader model={props.value.model} staleReason={chip.staleReason} />
              <PickerBody {...props} catalogStatus={status} row={row} onRetry={retry} onCommit={commit} />
            </div>
          )}
        </PopoverContent>
      </Popover>
      {chip.hint && (
        <span id={hintId} className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
          {chip.hint}
        </span>
      )}
      {liveRegion}
    </span>
  )
}

interface BlockedStepProps {
  pending: EffortPendingSwitch
  /** The model the slot stays on until the step is answered. */
  previousModel: string
  catalogRow: ServedRow
  onCommit: (effort: string) => void
  onCancel: () => void
}

const BLOCKED_TITLE = 'text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]'
const BLOCKED_HELP = 'text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]'

/**
 * The pick-a-level step (S-10, S-11) or the "Let the provider decide" step
 * (S-73), laid out as the wireframe draws it: a title, where you stay, the
 * choices, then the provider-decides escape and Cancel.
 */
function BlockedStep({ pending, previousModel, catalogRow, onCommit, onCancel }: BlockedStepProps) {
  const stay = `You stay on ${previousModel} until you do.`
  if (pending.kind === 'nothing_sendable') {
    return (
      <div className="space-y-[var(--space-2)]">
        <p className={BLOCKED_TITLE}>{`${pending.model} can't turn reasoning off`}</p>
        <p className={BLOCKED_HELP}>{`This model has no levels to pick. You can let the provider decide, or stay where you are. ${stay}`}</p>
        <div className="flex gap-[var(--space-2)]">
          <Button type="button" size="sm" data-effort-action="provider-default" onClick={() => onCommit(PROVIDER_DEFAULT)}>
            Let the provider decide
          </Button>
          <Button type="button" variant="outline" size="sm" onClick={onCancel}>
            Cancel
          </Button>
        </div>
      </div>
    )
  }
  const items = levelItems(effortTargetOf(catalogRow))
  return (
    <div className="space-y-[var(--space-2)]">
      {/* One sentence for the spec's copy, shown as the wireframe's title
          line plus its first help line. */}
      <p className={cn(BLOCKED_HELP, 'first-line:text-[length:var(--type-body-compact-size)] first-line:font-semibold first-line:text-[var(--color-secondary)]')}>
        {`${pending.model} can't turn reasoning off. `}
        <br />
        {'Pick a level to finish switching.'}
      </p>
      <p className={BLOCKED_HELP}>{stay}</p>
      <EffortRadioList items={items} checked={undefined} onCommit={onCommit} ariaLabel={`Effort for ${pending.model}`} nearest={pending.nearest} />
      <div className="flex items-center justify-between gap-[var(--space-2)]">
        <Button type="button" variant="link" size="sm" onClick={() => onCommit(PROVIDER_DEFAULT)}>
          Let the provider decide instead
        </Button>
        <Button type="button" variant="outline" size="sm" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  )
}

export interface QuietEffortLineProps {
  value: EffortValue
  /** The served catalog the surrounding step already holds (no second read). */
  catalog: ProvidersCatalog | null | undefined
  onChange: (effort: string) => void
  scope: Extract<EffortScope, 'onboarding' | 'replacement'>
  contextName?: string
  'data-testid'?: string
}

/**
 * The one quiet line of onboarding's model step and the remove-provider
 * replacement pick (US-11 AS3, AS4; FR-066, FR-067): "Effort: Provider
 * default. Change now". "Change now" opens the effort choices inline; a pick
 * closes them. A model without reasoning shows no line; one whose effort
 * Omnipus can't change shows the muted sentence.
 */
export function QuietEffortLine(props: QuietEffortLineProps) {
  const [choosing, setChoosing] = useState(false)
  const row = findServedRow(props.catalog ?? undefined, props.value.provider, props.value.model)
  if (props.value.model === '' || !props.catalog) return null
  const presentation = presentationOf(row)
  if (presentation === 'none') return null
  if (presentation === 'muted') {
    return (
      <p data-testid={props['data-testid']} className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
        {MUTED_EFFORT_TEXT}
      </p>
    )
  }
  const stored = props.value.effort === NOT_SET ? PROVIDER_DEFAULT : props.value.effort
  return (
    <div data-testid={props['data-testid']} className="space-y-[var(--space-1)]">
      <p className="inline-flex items-center gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
        <span>{`Effort: ${effortLabel(stored)}.`}</span>
        <Button type="button" variant="link" size="sm" aria-expanded={choosing} onClick={() => setChoosing((open) => !open)}>
          Change now
        </Button>
      </p>
      {choosing && (
        <PickerBody
          value={props.value}
          scope={props.scope}
          contextName={props.contextName ?? `Effort for ${props.value.model}`}
          catalogStatus="success"
          row={row}
          onRetry={() => undefined}
          onCommit={(effort) => {
            setChoosing(false)
            props.onChange(effort)
          }}
        />
      )}
    </div>
  )
}
