import { useLayoutEffect, useRef, useState } from 'react'

export type WorkspaceHeaderMode = 'full' | 'icons' | 'narrow'

/**
 * DOM contract with the rest of the header line (no import between the modules):
 *   - the header row (WorkspaceTabContainer) carries `data-header-line` and the
 *     strip is its direct child;
 *   - the chat title (FeedKindLabel / TaskKindLabel) carries `data-header-yield`.
 *     It declares that the width of the item holding it (the chat slot) has an
 *     automatic minimum that EXCLUDES the title, so the title is room the strip
 *     may take back (#1449 D13, founder 2026-10-28: "the workspace strip ALWAYS
 *     keeps the header line; the chat slot shrinks and truncates first").
 */
const HEADER_LINE = '[data-header-line]'
const HEADER_YIELD = '[data-header-yield]'

/** Compare natural, off-layout strip widths with the space the strip can have.
 * No viewport/rem breakpoint is involved. */
function widestFittingMode(available: number, full: number, icons: number): WorkspaceHeaderMode {
  if (full <= available) return 'full'
  if (icons <= available) return 'icons'
  return 'narrow'
}

/** A computed length in px, or null for anything else ('auto', '%', 'normal', ''). */
function px(value: string): number | null {
  return /^-?\d+(\.\d+)?px$/.test(value.trim()) ? Number.parseFloat(value) : null
}

/** The width an item cannot go below because its CSS says so. 'auto' (the
 * automatic minimum) is 0 by the `data-header-yield` contract; a minimum the
 * browser reports in another unit (a percentage) cannot be honoured here, so
 * null = nothing is given back from that item. */
function minWidthFloor(value: string): number | null {
  const trimmed = value.trim()
  return trimmed === '' || trimmed === 'auto' ? 0 : px(trimmed)
}

/**
 * The width the strip can have on its header line, derived from the LINE and
 * the other items on it — never from the strip's own width. The strip may pin
 * itself at the width it claims (see below), and reading that width back would
 * echo the claim instead of the free space.
 *
 *   room = line content width
 *          - every other in-flow item (width + margins) - the gaps between items
 *          + the width each chat title can give back
 *
 * An item with `display: none`, or one positioned out of the flow, takes no
 * room and no gap. A title gives back min(its width, its item's width - the
 * item's CSS minimum): what it holds right now, and no more than the item can
 * lose. The result does not depend on how far the slot is already squeezed
 * (slot and title shrink together), so choosing a mode never changes the next
 * measurement.
 *
 * Returns null while the line has no layout (hidden/unmounted).
 */
function stripRoom(line: HTMLElement, strip: HTMLElement): number | null {
  const lineStyle = getComputedStyle(line)
  const content = line.getBoundingClientRect().width
    - (px(lineStyle.paddingLeft) ?? 0) - (px(lineStyle.paddingRight) ?? 0)
    - (px(lineStyle.borderLeftWidth) ?? 0) - (px(lineStyle.borderRightWidth) ?? 0)
  if (content <= 0) return null

  let flowItems = 0
  let taken = 0
  let givenBack = 0
  for (const item of line.children) {
    const style = getComputedStyle(item)
    if (style.display === 'none' || style.position === 'absolute' || style.position === 'fixed') continue
    flowItems += 1
    taken += (px(style.marginLeft) ?? 0) + (px(style.marginRight) ?? 0)
    if (item === strip) continue
    const width = item.getBoundingClientRect().width
    taken += width
    const floor = minWidthFloor(style.minWidth)
    if (floor === null) continue
    let titleWidth = 0
    for (const title of item.querySelectorAll(HEADER_YIELD)) titleWidth += title.getBoundingClientRect().width
    givenBack += Math.max(0, Math.min(titleWidth, width - floor))
  }
  taken += (px(lineStyle.columnGap) ?? 0) * Math.max(0, flowItems - 1)
  return content - taken + givenBack
}

interface HeaderFit {
  mode: WorkspaceHeaderMode
  /** Width the strip claims on the header line (px); 0 = no claim. */
  stripMinWidth: number
}

/**
 * Chooses how much of the workspace strip fits (R44, founder 2026-10-07): the
 * full labelled row, then icons only, then the name menu.
 *
 * On a header line the chat title is room the strip may take back, so the
 * choice is made against that room, and the strip CLAIMS the width of the mode
 * it chose (`stripMinWidth`, applied as its min-width): the line then takes the
 * shortfall from the chat title, which truncates, instead of from the strip.
 * Without a header line the strip is measured on its own flex slot, as before.
 */
export function useWorkspaceHeaderMode() {
  const availableRef = useRef<HTMLDivElement>(null)
  const fullRef = useRef<HTMLDivElement>(null)
  const iconsRef = useRef<HTMLDivElement>(null)
  const [fit, setFit] = useState<HeaderFit>({ mode: 'full', stripMinWidth: 0 })

  useLayoutEffect(() => {
    const available = availableRef.current
    const full = fullRef.current
    const icons = iconsRef.current
    if (!available || !full || !icons) return
    const parent = available.parentElement
    const line = parent?.matches(HEADER_LINE) ? parent : null

    const measure = () => {
      const fullWidth = full.getBoundingClientRect().width
      const iconsWidth = icons.getBoundingClientRect().width
      const ownWidth = available.getBoundingClientRect().width
      const room = line ? stripRoom(line, available) : ownWidth > 0 ? ownWidth : null
      // A hidden/unmounted row has no available size yet. Keep the previous
      // mode until all the real measurements exist, rather than guessing.
      if (room === null || fullWidth <= 0 || iconsWidth <= 0) return
      const mode = widestFittingMode(room, fullWidth, iconsWidth)
      const stripMinWidth = !line ? 0 : mode === 'full' ? fullWidth : mode === 'icons' ? iconsWidth : 0
      setFit((previous) => (
        previous.mode === mode && previous.stripMinWidth === stripMinWidth ? previous : { mode, stripMinWidth }
      ))
    }

    const resizeObserver = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(measure)
    let watched = new Set<Element>()
    const watch = () => {
      if (!resizeObserver) return
      const next = new Set<Element>([available, full, icons])
      if (line) {
        next.add(line)
        for (const item of line.children) next.add(item)
        for (const title of line.querySelectorAll(HEADER_YIELD)) next.add(title)
      }
      for (const element of watched) if (!next.has(element)) resizeObserver.unobserve(element)
      for (const element of next) if (!watched.has(element)) resizeObserver.observe(element)
      watched = next
    }
    // The line's items and the chat title come and go (the sidebar launcher,
    // the chat controls, a title that renders once the roster has loaded): keep
    // watching whatever is there now and measure again.
    let mutationObserver: MutationObserver | null = null
    if (line) {
      mutationObserver = new MutationObserver(() => { watch(); measure() })
      mutationObserver.observe(line, { childList: true, subtree: true })
    }

    // Set the initial mode before paint. Natural probes never change with the
    // selected mode, and the room is read from the line, not from the strip:
    // shrinking the strip cannot immediately make the full mode fit again.
    measure()
    watch()
    // Observing the probes also catches font/root-size and workspace-name
    // changes, including font loading when the row itself does not resize.
    window.addEventListener('resize', measure)
    return () => {
      resizeObserver?.disconnect()
      mutationObserver?.disconnect()
      window.removeEventListener('resize', measure)
    }
  }, [])

  return { mode: fit.mode, stripMinWidth: fit.stripMinWidth, availableRef, fullRef, iconsRef }
}
