// Oracle (specifications, not the implementation):
//   - R44 (founder 2026-10-07; ADR-20261007, "R44 measured-space modes"): the
//     strip steps down only when the full row no longer FITS the measured free
//     space; sidebar closed + a panel open at 1440 px keeps the full labelled
//     row.
//   - #1449 D13 (founder 2026-10-28; ADR-20261028 §A9 "Minimal chrome", on the
//     branch spec/1449-group-chat): the chat controls sit permanently at the
//     right end of the SAME header line, and "WorkspaceTabBar measures the
//     remaining slot (R44), so the added shrink-0 controls are absorbed by its
//     existing mode fallback" (§A9.1). The founder's rule for who yields
//     (2026-10-28, also the header of tests/e2e/chat-header-narrow.spec.ts):
//     "the workspace strip ALWAYS keeps the header line; the chat slot shrinks
//     and truncates first" — so the chat's TITLE is free room for the strip,
//     while the slot's other items (the "Add participant" icon, its padding and
//     gap) are not: D13 puts that control permanently at the right end of the
//     line.
//
// The widths below are independent synthetic fixtures taken from the failing
// CI geometry (run 40893972010, shard ui-3: F1 at 14 px and CR-1 at 20 px),
// never read off the mode-selection code. jsdom has no layout engine, so the
// geometry and ResizeObserver are the browser edges stubbed here; the real
// WorkspaceTabBar, the real hook and the real kit components stay under test.
// The same facts are measured in Chromium by tests/e2e/workspace-header-
// standalone.spec.ts (F1, CR-1).
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import type { CSSProperties } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useUiStore } from '@/store/ui'
import { WorkspaceTabBar } from './WorkspaceTabBar'

vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: '/workspaces/ws-1/chat' }),
  useNavigate: () => vi.fn(),
}))

// F1 (14 px root, 1440x900, sidebar closed, Tasks List open): the header is
// 792 wide; the sidebar launcher is 2.75rem = 38.5; the browser controls 139;
// the chat slot 152 = title 108 + gap 4 + icon 28 + right padding 12; the
// strip needs 466 (full row) or 273 (icons).
const F1 = { line: 792, launcher: 38.5, controls: 139, slot: 152, title: 108, full: 466, icons: 273 }
// CR-1 (20 px root, 1280 wide, sidebar pinned so no launcher): the header is
// 360 wide; controls 55; slot 90 = title 34 + gap 4 + icon 40 + padding 12;
// the strip needs 542 (full) or 276 (icons).
const CR1 = { line: 360, launcher: 0, controls: 55, slot: 90, title: 34, full: 542, icons: 276 }

type Geometry = typeof F1
const widths: Record<string, number> = {}
const resizeCallbacks = new Set<ResizeObserverCallback>()
let observed = new Set<Element>()
let resizeDisconnects = 0

function widthOf(element: HTMLElement): number | undefined {
  const measure = element.dataset.workspaceHeaderMeasure
  return widths[measure ? `measure-${measure}` : element.dataset.testid ?? '']
}

function setGeometry(g: Geometry, root: number) {
  Object.assign(widths, {
    line: g.line, launcher: g.launcher, controls: g.controls, slot: g.slot, title: g.title,
    'workspace-header-entries': root, 'measure-full': g.full, 'measure-icons': g.icons,
  })
}

function notify() {
  act(() => { for (const callback of resizeCallbacks) callback([], {} as ResizeObserver) })
}

beforeEach(() => {
  for (const key of Object.keys(widths)) delete widths[key]
  resizeCallbacks.clear()
  observed = new Set()
  resizeDisconnects = 0
  useUiStore.setState({ activePanel: null, guardPending: false, toasts: [] })
  const nativeRect = HTMLElement.prototype.getBoundingClientRect
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    const width = widthOf(this)
    return width === undefined ? nativeRect.call(this) : DOMRect.fromRect({ width, height: 44 })
  })
  vi.stubGlobal('ResizeObserver', class {
    constructor(private readonly callback: ResizeObserverCallback) { resizeCallbacks.add(callback) }
    observe(element: Element) { observed.add(element) }
    unobserve(element: Element) { observed.delete(element) }
    disconnect() { resizeCallbacks.delete(this.callback); resizeDisconnects++ }
  })
})
afterEach(() => {
  cleanup()
  useUiStore.getState().closePanel()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

interface LineOptions {
  /** The slot's explicit minimum (a coarse pointer's touch reservation). */
  slotMinWidth?: string
  /** False: the title exists but is not declared as room the chat gives back. */
  titleYields?: boolean
  /** False: the slot holds no title at the first render. */
  withTitle?: boolean
  /** The width (px) of a second declared title inside the SAME slot item; absent = one title. */
  secondTitle?: number
  lineStyle?: CSSProperties
  launcherStyle?: CSSProperties
  controlsStyle?: CSSProperties
}

/** The per-case inline styles are arbitrary geometry probes (px boundaries), not product
 * styling: they are set on the rendered nodes through the DOM, as a browser would hold them,
 * and the strip is told to measure again afterwards. */
function applyInlineStyle(element: HTMLElement, style: CSSProperties | undefined) {
  if (style) Object.assign(element.style, style)
}

function renderLine(g: Geometry, root: number, options: LineOptions = {}) {
  setGeometry(g, root)
  const { slotMinWidth, titleYields = true, withTitle = true, secondTitle, lineStyle, launcherStyle, controlsStyle } = options
  if (secondTitle !== undefined) widths.title2 = secondTitle
  const view = render(
    <header data-testid="line" data-header-line="">
      <div data-testid="launcher" />
      <WorkspaceTabBar workspaceId="ws-1" workspaceName="Workspace One" />
      <div data-testid="controls" />
      <div data-testid="slot" style={{ minWidth: slotMinWidth }}>
        {withTitle ? <span data-testid="title" {...(titleYields ? { 'data-header-yield': '' } : {})} /> : null}
        {secondTitle === undefined ? null : <span data-testid="title2" data-header-yield="" />}
      </div>
    </header>,
  )
  applyInlineStyle(view.getByTestId('line'), lineStyle)
  applyInlineStyle(view.getByTestId('launcher'), launcherStyle)
  applyInlineStyle(view.getByTestId('controls'), controlsStyle)
  if (lineStyle || launcherStyle || controlsStyle) notify()
  return view
}

const strip = () => screen.getByTestId('workspace-header-entries')

describe('the chat title is free room for the workspace strip (F1, R44 + D13)', () => {
  it('F1 geometry: the strip has 3.5 px too little on its own slot, yet keeps the full row because the title gives that back', () => {
    // Slot-only room: 792 - 38.5 - 139 - 152 = 462.5 < 466 (the CI failure).
    // Room with the title handed back: 462.5 + 108 = 570.5 >= 466 -> full.
    renderLine(F1, 462.5)
    expect(strip()).toHaveAttribute('data-mode', 'full')
    // The strip claims the width of its mode, so the line takes the shortfall
    // from the title instead of from the strip.
    expect(strip().style.minWidth).toBe('466px')
  })

  it('the same geometry steps down to icons when the title is NOT declared as room (negative control)', () => {
    renderLine(F1, 462.5, { titleYields: false })
    expect(strip()).toHaveAttribute('data-mode', 'icons')
    expect(strip().style.minWidth).toBe('273px')
  })

  it('CR-1 geometry: the Add-participant icon and padding never give way, so 249 px stays below the 276 px icons row', () => {
    // 360 - 55 - 90 = 215 on the strip's own slot; + title 34 = 249 < 276.
    renderLine(CR1, 215)
    expect(strip()).toHaveAttribute('data-mode', 'narrow')
    expect(strip().style.minWidth).toBe('')
  })

  it.each([
    // room = line - 55 - 90 + 34 = line - 111
    [386.99, 'narrow'], [387, 'icons'], [387.01, 'icons'],
    [652.99, 'icons'], [653, 'full'], [653.01, 'full'],
  ])('CR-1 fixture at a %s px header selects %s (icons needs room 276, full needs 542)', (line, mode) => {
    renderLine({ ...CR1, line: line as number }, (line as number) - 55 - 90)
    expect(strip()).toHaveAttribute('data-mode', mode as string)
  })

  it.each([
    // The CR-1 slot with its 34 px title split into 20 + 14 inside the same item: every declared
    // title is room the strip may take back, so room = line - 55 - 90 + (20 + 14) = line - 111,
    // the same boundary as the single 34 px title above (icons needs room 276). Counting only
    // the first title would leave line - 125 and put the boundary at 401 px.
    [386.99, 'narrow'], [387, 'icons'],
  ])('two declared titles in one slot item both give their width back: a %s px header selects %s', (line, mode) => {
    renderLine({ ...CR1, line: line as number, title: 20 }, (line as number) - 55 - 90, { secondTitle: 14 })
    expect(strip()).toHaveAttribute('data-mode', mode as string)
  })

  it('a slot with an explicit minimum (coarse-pointer reservation) gives back only the width above that minimum', () => {
    // Slot 124 wide with a 112 px floor: only 12 of the 40 px title may go.
    // room = line - 44 - 68 - 124 + 12 = line - 224; icons needs 300.
    const g = { line: 523.99, launcher: 44, controls: 68, slot: 124, title: 40, full: 537, icons: 300 }
    const { unmount } = renderLine(g, 523.99 - 44 - 68 - 124, { slotMinWidth: '112px' })
    expect(strip()).toHaveAttribute('data-mode', 'narrow')
    unmount()
    renderLine({ ...g, line: 524 }, 524 - 44 - 68 - 124, { slotMinWidth: '112px' })
    expect(strip()).toHaveAttribute('data-mode', 'icons')
  })

  it('a slot that reports less than its own minimum (sub-pixel rounding) gives nothing back, never a negative amount', () => {
    // Slot 100 wide against a 112 px floor: width - floor = -12. Held at 0 the room is
    // 520 - 44 - 68 - 100 = 308 >= 300 (icons); a negative give-back would leave 296 (narrow).
    const g = { line: 520, launcher: 44, controls: 68, slot: 100, title: 40, full: 537, icons: 300 }
    renderLine(g, 520 - 44 - 68 - 100, { slotMinWidth: '112px' })
    expect(strip()).toHaveAttribute('data-mode', 'icons')
  })

  it('a minimum the browser reports as a percentage gives nothing back (assuming room that cannot be delivered would overlap the strip)', () => {
    renderLine(F1, 462.5, { slotMinWidth: '10%' })
    expect(strip()).toHaveAttribute('data-mode', 'icons')
  })
})

describe('everything else on the line that takes room from the strip', () => {
  // F1: room = 792 - 38.5 - 139 - 152 + 108 = 570.5 against the 466 px full row, so 104.5 px of
  // slack. Taking 104 px more leaves 466.5 (full); taking 105 px leaves 465.5 (icons). Each
  // quantity is split over several items or sides, so dropping any ONE term lowers the total
  // below 105 px and the "105 px" case would stay at full.
  const solid = { borderStyle: 'solid' } as const

  it.each([
    { gap: 34, mode: 'full' }, // 3 gaps * 34 = 102 -> room 468.5
    { gap: 35, mode: 'icons' }, // 3 gaps * 35 = 105 -> room 465.5
  ])('a column gap of $gap px between the four items selects $mode', ({ gap, mode }) => {
    renderLine(F1, 462.5, { lineStyle: { columnGap: `${gap}px` } })
    expect(strip()).toHaveAttribute('data-mode', mode)
  })

  it.each([
    { own: 34, mode: 'full' }, // 30 + 40 + 34 = 104 -> room 466.5
    { own: 35, mode: 'icons' }, // 30 + 40 + 35 = 105 -> room 465.5
  ])('margins of 30 and 40 px on two items and $own px on the strip itself select $mode', ({ own, mode }) => {
    renderLine(F1, 462.5, { launcherStyle: { marginLeft: '30px' }, controlsStyle: { marginRight: '40px' } })
    expect(strip()).toHaveAttribute('data-mode', 'full') // 70 px taken: room 500.5
    strip().style.marginRight = `${own}px`
    notify()
    expect(strip()).toHaveAttribute('data-mode', mode)
  })

  it.each([
    { borderRight: 14, mode: 'full' }, // 40 + 30 + 20 + 14 = 104 -> room 466.5
    { borderRight: 15, mode: 'icons' }, // 40 + 30 + 20 + 15 = 105 -> room 465.5
  ])('line padding of 40 and 30 px and borders of 20 and $borderRight px select $mode', ({ borderRight, mode }) => {
    renderLine(F1, 462.5, {
      lineStyle: { ...solid, paddingLeft: '40px', paddingRight: '30px', borderLeftWidth: '20px', borderRightWidth: `${borderRight}px` },
    })
    expect(strip()).toHaveAttribute('data-mode', mode)
  })

  // 40 px gaps between the items that are in the flow. An item that is out of the flow takes
  // neither its width nor a gap:
  //   740 px, all four in the flow:      740 - 329.5 + 108 - 3 * 40 = 398.5 -> icons (control)
  //   740 px, launcher display: none:    740 - 291 + 108 - 2 * 40 = 477 -> full
  //   650 px, all four in the flow:      650 - 329.5 + 108 - 3 * 40 = 308.5 -> icons (control)
  //   650 px, controls absolute / fixed: 650 - 190.5 + 108 - 2 * 40 = 487.5 -> full
  it.each<{ name: string, line: number, options: LineOptions, mode: string }>([
    { name: 'every item in the flow (control)', line: 740, options: {}, mode: 'icons' },
    { name: 'the launcher has display: none', line: 740, options: { launcherStyle: { display: 'none' } }, mode: 'full' },
    { name: 'every item in the flow (control)', line: 650, options: {}, mode: 'icons' },
    { name: 'the controls are positioned absolute', line: 650, options: { controlsStyle: { position: 'absolute' } }, mode: 'full' },
    { name: 'the controls are positioned fixed', line: 650, options: { controlsStyle: { position: 'fixed' } }, mode: 'full' },
  ])('$name, header $line px, 40 px gaps -> $mode', ({ line, options, mode }) => {
    renderLine({ ...F1, line }, 462.5, { ...options, lineStyle: { columnGap: '40px' } })
    expect(strip()).toHaveAttribute('data-mode', mode)
  })
})

describe('the measurement does not echo its own claim', () => {
  it('a strip pinned at its claimed width steps down when the line has narrowed (room is read from the line, not from the strip)', () => {
    renderLine(F1, 462.5)
    expect(strip()).toHaveAttribute('data-mode', 'full')
    // Line 600: the claimed 466 pins the strip while the slot is squeezed to
    // its floor (gap 4 + icon 28 + padding 12 = 44, title 0).
    // room = 600 - 38.5 - 139 - 44 + 0 = 378.5 -> icons (273 <= 378.5 < 466).
    widths.line = 600; widths.slot = 44; widths.title = 0; widths['workspace-header-entries'] = 466
    notify()
    expect(strip()).toHaveAttribute('data-mode', 'icons')
    expect(strip().style.minWidth).toBe('273px')
    // Line 300: room = 300 - 38.5 - 139 - 44 = 78.5 < 273 -> the menu.
    widths.line = 300; widths['workspace-header-entries'] = 273
    notify()
    expect(strip()).toHaveAttribute('data-mode', 'narrow')
    expect(strip().style.minWidth).toBe('')
    // And back up: the room returns, the full row returns.
    widths.line = 792; widths.slot = 152; widths.title = 108; widths['workspace-header-entries'] = 462.5
    notify()
    expect(strip()).toHaveAttribute('data-mode', 'full')
  })

  it('the choice is the same however far the slot has already been squeezed', () => {
    // Header 700: unsqueezed slot (152, title 108) and the slot after the
    // 466 claim squeezed it (56.5, title 12.5) leave the same room, 478.5.
    renderLine({ ...F1, line: 700 }, 700 - 38.5 - 139 - 152)
    expect(strip()).toHaveAttribute('data-mode', 'full')
    widths.slot = 56.5; widths.title = 12.5; widths['workspace-header-entries'] = 466
    notify()
    expect(strip()).toHaveAttribute('data-mode', 'full')
    expect(strip().style.minWidth).toBe('466px')
  })
})

describe('what the hook watches on the header line', () => {
  it('observes the line, every item on it, the strip, both probes and the title', () => {
    renderLine(F1, 462.5)
    const ids = [...observed].map((element) => (element as HTMLElement).dataset.testid
      ?? `measure-${(element as HTMLElement).dataset.workspaceHeaderMeasure}`).sort()
    expect(ids).toEqual(['controls', 'launcher', 'line', 'measure-full', 'measure-icons', 'slot', 'title', 'workspace-header-entries'])
  })

  it('re-measures when a title that gives width back appears on the line later', async () => {
    const view = renderLine(F1, 462.5, { withTitle: false })
    expect(strip()).toHaveAttribute('data-mode', 'icons')
    const title = document.createElement('span')
    title.dataset.testid = 'title'
    title.setAttribute('data-header-yield', '')
    view.getByTestId('slot').appendChild(title)
    await waitFor(() => expect(strip()).toHaveAttribute('data-mode', 'full'))
    expect(observed.has(title)).toBe(true)
  })

  it('stops watching a title that has left the line', async () => {
    const view = renderLine(F1, 462.5)
    const title = view.getByTestId('title')
    expect(observed.has(title)).toBe(true)
    title.remove()
    await waitFor(() => expect(observed.has(title)).toBe(false))
  })

  it('keeps the previous mode and claim while the line has no layout (hidden)', () => {
    renderLine(F1, 462.5)
    expect(strip()).toHaveAttribute('data-mode', 'full')
    widths.line = 0
    notify()
    expect(strip()).toHaveAttribute('data-mode', 'full')
    expect(strip().style.minWidth).toBe('466px')
  })

  it('re-measures when the window is resized (a font or root-size change no watched element reports)', () => {
    renderLine(F1, 462.5)
    expect(strip()).toHaveAttribute('data-mode', 'full')
    // Line 600 with the slot squeezed to its floor (44, title 0): room = 600 - 38.5 - 139 - 44 = 378.5
    // -> icons (273 <= 378.5 < 466). Only the window event is fired here, not a ResizeObserver callback.
    widths.line = 600; widths.slot = 44; widths.title = 0
    act(() => { window.dispatchEvent(new Event('resize')) })
    expect(strip()).toHaveAttribute('data-mode', 'icons')
  })

  it('stops observing on unmount', () => {
    const mutationDisconnect = vi.spyOn(MutationObserver.prototype, 'disconnect')
    const added = vi.spyOn(window, 'addEventListener')
    const removed = vi.spyOn(window, 'removeEventListener')
    const view = renderLine(F1, 462.5)
    const resizeListeners = added.mock.calls.filter(([type]) => type === 'resize').map(([, listener]) => listener)
    expect(resizeListeners.length).toBeGreaterThan(0)
    view.unmount()
    expect(resizeDisconnects).toBe(1)
    expect(mutationDisconnect).toHaveBeenCalledTimes(1)
    // Every window 'resize' listener added while mounted is removed again (none is left behind).
    for (const listener of resizeListeners) expect(removed).toHaveBeenCalledWith('resize', listener)
  })
})

describe('without a header line the strip is measured on its own slot, as before', () => {
  it('sets no minimum width and ignores a title that is not on a declared line', () => {
    setGeometry(F1, 462.5)
    render(
      <header data-testid="line">
        <WorkspaceTabBar workspaceId="ws-1" workspaceName="Workspace One" />
        <div data-testid="slot"><span data-testid="title" data-header-yield="" /></div>
      </header>,
    )
    expect(strip()).toHaveAttribute('data-mode', 'icons')
    expect(strip().style.minWidth).toBe('')
  })
})
