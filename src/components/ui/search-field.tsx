import * as React from 'react'
import { MagnifyingGlass, X } from '@phosphor-icons/react'
import { Input } from './input'
import { IconButton } from './icon-button'
import { cn } from '@/lib/utils'

// SearchField — the kit's list-filter search box (W2-1 contract §2.1, R33).
// Composes the existing kit Input (no second input primitive) and an
// IconButton for clear. Not a combobox: command.tsx's CommandInput stays
// the search for command palettes and pickers.
//
// Clear and Escape follow founder answer Q1 = A (2026-11-06): the clear
// button shows only while there is text; Escape clears the text when there
// is any, and on an empty field it is left alone so the surrounding pop-up
// closes as usual. The whole rule is gated by `clearable` and lives in
// `clearsOnEscape` below, so a different answer is a one-line change.
//
// One Escape does one thing (contract §2.1, team-lead decision D-K1).
// Radix dismissable layers (Dialog, Sheet, Popover, every DismissableLayer)
// listen for Escape on `document` in the CAPTURE phase, before this input's
// own handler runs, and dismiss unless the event is already
// `defaultPrevented`. A window capture-phase listener runs before them. It
// CLAIMS the Escape (preventDefault, so no layer dismisses; the input then
// clears) only when the text would clear AND the field is inside the topmost
// open layer, or no layer is open. When an open layer does not contain the
// field, the Escape is left to that layer: it closes and the field keeps its
// value.
export interface SearchFieldProps {
  value: string
  onValueChange: (value: string) => void
  /** Names what is searched, e.g. "Search connectors…" (R33). */
  placeholder: string
  /** Accessible name of the searchbox (R33). */
  'aria-label': string
  /** Shows the clear button with text and lets Escape clear. Default true. */
  clearable?: boolean
  disabled?: boolean
  autoFocus?: boolean
  id?: string
  /** Layout classes only (R41); the look lives in the component. */
  className?: string
  'data-testid'?: string
}

// Set by SearchToolbar: a SearchField inside a toolbar takes the toolbar's
// grow-with-minimum sizing itself, so the toolbar never has to read or merge
// a child's className.
const SearchToolbarContext = React.createContext(false)

// Why the DOM and not a registry: Radix keeps its layer stack in an internal
// context (DismissableLayerContext, not exported in 1.1.x), so the next best
// source is the structure Radix itself renders for an open layer: a popper
// wrapper (Popover, menus, Select, Tooltip, HoverCard) or an open
// dialog/alertdialog content (Dialog, AlertDialog, Sheet). Layers portal to
// the end of <body> in the order they open, and a nested layer comes after
// its parent, so the last match in document order is the topmost. A
// hand-kept registry would need every layer to opt in and would silently miss
// raw Radix layers and third-party ones.
const LAYER_SELECTOR = [
  '[data-radix-popper-content-wrapper]',
  '[role="dialog"][data-state="open"]',
  '[role="alertdialog"][data-state="open"]',
].join(', ')

// A popper wrapper stays mounted while its content plays the exit animation;
// its content then reads data-state="closed" and no longer counts as open.
function isOpenLayer(element: Element) {
  if (!element.hasAttribute('data-radix-popper-content-wrapper')) return true
  return element.firstElementChild?.getAttribute('data-state') !== 'closed'
}

function topmostOpenLayer(doc: Document): Element | null {
  const open = Array.from(doc.querySelectorAll(LAYER_SELECTOR)).filter(isOpenLayer)
  return open.length > 0 ? open[open.length - 1] : null
}

function clearsOnEscape(clearable: boolean, value: string) {
  return clearable && value !== ''
}

interface SearchFieldFrameProps {
  inToolbar: boolean
  disabled: boolean
  className?: string
  testId?: string
  children: React.ReactNode
}

// The field's wrapper. Inside a SearchToolbar it takes the toolbar's sizing:
// grow, with a 192px minimum (3 x --space-8) before the filters wrap (R33).
function SearchFieldFrame({ inToolbar, disabled, className, testId, children }: SearchFieldFrameProps) {
  return (
    <div
      data-slot="search-field"
      data-testid={testId}
      className={cn(
        'relative flex min-w-0 items-center',
        inToolbar && 'flex-[1_1_auto] min-w-[calc(var(--space-8)*3)]',
        disabled && 'opacity-50',
        className,
      )}
    >
      {children}
    </div>
  )
}

const SearchField = React.forwardRef<HTMLInputElement, SearchFieldProps>(function SearchField(
  { value, onValueChange, placeholder, 'aria-label': ariaLabel, clearable = true, disabled = false, autoFocus = false, id, className, 'data-testid': testId },
  forwardedRef,
) {
  const inputRef = React.useRef<HTMLInputElement | null>(null)
  const setRef = React.useCallback((node: HTMLInputElement | null) => {
    inputRef.current = node
    if (typeof forwardedRef === 'function') forwardedRef(node)
    else if (forwardedRef) forwardedRef.current = node
  }, [forwardedRef])

  const showClear = clearable && !disabled && value !== ''

  // Latest rule for the capture listener below, without re-subscribing.
  const clearsRef = React.useRef(false)
  React.useEffect(() => {
    clearsRef.current = clearsOnEscape(clearable, value)
  }, [clearable, value])
  // The Escape event this field claimed; the input's handler clears only for it.
  const claimedRef = React.useRef<Event | null>(null)
  React.useEffect(() => {
    const view = inputRef.current?.ownerDocument.defaultView
    if (!view) return
    function claimEscape(event: KeyboardEvent) {
      const input = inputRef.current
      if (event.key !== 'Escape' || !input || event.target !== input || !clearsRef.current) return
      const layer = topmostOpenLayer(input.ownerDocument)
      if (layer !== null && !layer.contains(input)) return
      claimedRef.current = event
      event.preventDefault()
    }
    view.addEventListener('keydown', claimEscape, { capture: true })
    return () => view.removeEventListener('keydown', claimEscape, { capture: true })
  }, [])
  const inToolbar = React.useContext(SearchToolbarContext)

  return (
    <SearchFieldFrame inToolbar={inToolbar} disabled={disabled} className={className} testId={testId}>
      <MagnifyingGlass
        aria-hidden="true"
        className="pointer-events-none absolute left-[var(--space-2-5)] size-[var(--icon-size-compact)] text-[var(--color-muted)]"
      />
      <Input
        ref={setRef}
        id={id}
        type="search"
        value={value}
        placeholder={placeholder}
        aria-label={ariaLabel}
        disabled={disabled}
        autoFocus={autoFocus}
        onChange={(event) => onValueChange(event.target.value)}
        onKeyDown={(event) => {
          if (event.key !== 'Escape' || !clearsOnEscape(clearable, value)) return
          // Not claimed: an open layer that does not contain the field gets this Escape.
          if (claimedRef.current !== event.nativeEvent) return
          claimedRef.current = null
          event.stopPropagation()
          event.preventDefault()
          onValueChange('')
        }}
        className={cn(
          // The wrapper carries the disabled dimming once, for icon and field together.
          'pl-[var(--space-5)] disabled:opacity-100 [&::-webkit-search-cancel-button]:appearance-none',
          showClear && 'pr-[var(--space-6)]',
        )}
      />
      {showClear ? (
        <IconButton
          aria-label="Clear search"
          size="sm"
          className="absolute right-0 pointer-coarse:size-[var(--target-touch-minimum)]"
          onClick={() => {
            onValueChange('')
            inputRef.current?.focus()
          }}
        >
          <X aria-hidden="true" className="size-[var(--icon-size-compact)]" />
        </IconButton>
      ) : null}
    </SearchFieldFrame>
  )
})
SearchField.displayName = 'SearchField'

export interface SearchToolbarProps {
  /** One SearchField first, then filter controls (ghost Select, FilterMenu, …). */
  children: React.ReactNode
  /** When given, the toolbar becomes a role="search" landmark with this name. */
  'aria-label'?: string
  className?: string
}

// The toolbar owns the row layout: the SearchField grows and keeps a 192px
// minimum (3 × --space-8), every other child stays at its content width
// (shrink-0), and the row wraps only when the filters no longer fit (R33).
function SearchToolbar({ children, 'aria-label': ariaLabel, className }: SearchToolbarProps) {
  const laidOut = React.Children.map(children, (child) => {
    if (!React.isValidElement(child)) return child
    // The SearchField sizes itself from SearchToolbarContext; it is not wrapped.
    if (child.type === SearchField) return child
    return <div className="flex shrink-0 items-center">{child}</div>
  })
  return (
    <div
      data-slot="search-toolbar"
      role={ariaLabel ? 'search' : undefined}
      aria-label={ariaLabel}
      className={cn('flex flex-wrap items-center gap-[var(--space-2)]', className)}
    >
      <SearchToolbarContext.Provider value={true}>{laidOut}</SearchToolbarContext.Provider>
    </div>
  )
}

export { SearchField, SearchToolbar }
