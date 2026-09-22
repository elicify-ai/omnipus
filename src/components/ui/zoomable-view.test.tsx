/**
 * zoomable-view.test.tsx — unit coverage for the D18 contract
 * (`docs/internal/design/components/zoomable-view.md`): the shared 25%–400%
 * range, the opening-size 12px label floor rule, the viewBox-based SVG
 * intrinsic-size fix (defect 1: "a wide diagram collapses to ~300px because
 * the size came from a percentage width"), and the media surface's
 * ctrl-wheel/`preventDefault` contract (D18: "the page's own zoom never
 * changes during a pinch inside the view").
 */

import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import {
  ZOOMABLE_VIEW_MAX_SCALE,
  ZOOMABLE_VIEW_MIN_SCALE,
  ZoomPill,
  ZoomableMediaSurface,
  clampZoomScale,
  computeFittedScale,
  computeOpeningScale,
  effectiveMinScale,
  resolveSvgIntrinsicSize,
  useZoomableMedia,
  useZoomableViewKeyboard,
} from './zoomable-view'
import { renderHook } from '@testing-library/react'

describe('clampZoomScale — the shared 25%–400% range clamp', () => {
  it('passes a value already inside the default range through unchanged', () => {
    expect(clampZoomScale(1)).toBe(1)
    expect(clampZoomScale(2.5)).toBe(2.5)
  })

  it('clamps below the 25% floor up to ZOOMABLE_VIEW_MIN_SCALE', () => {
    expect(clampZoomScale(0.1)).toBe(ZOOMABLE_VIEW_MIN_SCALE)
    expect(clampZoomScale(0)).toBe(ZOOMABLE_VIEW_MIN_SCALE)
    expect(clampZoomScale(-3)).toBe(ZOOMABLE_VIEW_MIN_SCALE)
  })

  it('clamps above the 400% ceiling down to ZOOMABLE_VIEW_MAX_SCALE', () => {
    expect(clampZoomScale(9)).toBe(ZOOMABLE_VIEW_MAX_SCALE)
  })

  it('treats non-finite input as the minimum, never NaN', () => {
    expect(clampZoomScale(NaN)).toBe(ZOOMABLE_VIEW_MIN_SCALE)
    expect(clampZoomScale(Infinity)).toBe(ZOOMABLE_VIEW_MIN_SCALE)
  })

  it('honours a caller-narrowed subrange (e.g. a graph with its own tighter bounds)', () => {
    expect(clampZoomScale(0.1, 0.5, 2)).toBe(0.5)
    expect(clampZoomScale(3, 0.5, 2)).toBe(2)
    expect(clampZoomScale(1, 0.5, 2)).toBe(1)
  })
})

describe('effectiveMinScale — the effective-floor rule (founder-approved clarification, 2026-09-21)', () => {
  it('floors to the FITTED scale, not 25%, when content needs less than 25% to fit', () => {
    // The live probe regression: portrait 1500x5000 content fits at ~0.17 in
    // a 1200x900-ish frame — well under the 25% floor. Fit must still open
    // and remain reachable, so the floor becomes the fit itself.
    expect(effectiveMinScale(0.17)).toBeCloseTo(0.17)
    expect(effectiveMinScale(0.2)).toBeCloseTo(0.2)
  })

  it('stays at 25% (the configured min) when the fitted scale is AT OR ABOVE it', () => {
    expect(effectiveMinScale(0.25)).toBe(ZOOMABLE_VIEW_MIN_SCALE)
    expect(effectiveMinScale(0.5)).toBe(ZOOMABLE_VIEW_MIN_SCALE)
    expect(effectiveMinScale(2)).toBe(ZOOMABLE_VIEW_MIN_SCALE)
  })

  it('honours a caller-narrowed `min` the same way as the default 25%', () => {
    expect(effectiveMinScale(0.1, 0.5)).toBeCloseTo(0.1)
    expect(effectiveMinScale(0.6, 0.5)).toBe(0.5)
  })

  it('falls back to `min` for a not-yet-known fit (non-finite, zero, or negative)', () => {
    expect(effectiveMinScale(NaN)).toBe(ZOOMABLE_VIEW_MIN_SCALE)
    expect(effectiveMinScale(Infinity)).toBe(ZOOMABLE_VIEW_MIN_SCALE)
    expect(effectiveMinScale(0)).toBe(ZOOMABLE_VIEW_MIN_SCALE)
    expect(effectiveMinScale(-1)).toBe(ZOOMABLE_VIEW_MIN_SCALE)
  })

  it('composes with clampZoomScale so a below-floor fit passes through unclamped', () => {
    const fit = 0.1684 // 842/5000, the live probe's binding axis
    const floor = effectiveMinScale(fit)
    expect(clampZoomScale(fit, floor, ZOOMABLE_VIEW_MAX_SCALE)).toBeCloseTo(fit)
  })
})

describe('computeFittedScale — plain fit-to-frame, either axis binding', () => {
  it('binds on width for content wider than it is tall relative to the frame', () => {
    // 1000x100 content into a 500x500 frame: width is the binding axis (0.5), not height (5).
    expect(computeFittedScale({ width: 1000, height: 100 }, { width: 500, height: 500 })).toBeCloseTo(0.5)
  })

  it('binds on height for content taller than it is wide relative to the frame', () => {
    expect(computeFittedScale({ width: 100, height: 1000 }, { width: 500, height: 500 })).toBeCloseTo(0.5)
  })

  it('falls back to 1 for degenerate (zero or negative) sizes rather than dividing by zero', () => {
    expect(computeFittedScale({ width: 0, height: 100 }, { width: 500, height: 500 })).toBe(1)
    expect(computeFittedScale({ width: 100, height: 100 }, { width: 0, height: 500 })).toBe(1)
  })
})

describe('computeOpeningScale — D18 opening-size rule (12px label floor)', () => {
  it('opens fitted for SMALL content whose labels clear the 12px floor at the fitted scale', () => {
    // A small graph: fits at 2x (200 content into a 400 frame), and an 8px
    // authored label at 2x renders at 16px — comfortably above the floor.
    const scale = computeOpeningScale({
      content: { width: 200, height: 200 },
      frame: { width: 400, height: 400 },
      smallestLabelPx: 8,
    })
    expect(scale).toBeCloseTo(computeFittedScale({ width: 200, height: 200 }, { width: 400, height: 400 }))
    expect(scale).toBeCloseTo(2)
  })

  it('opens at the label floor, not fitted, for LARGE content where fitting would shrink labels below 12px', () => {
    // The live-measured task-graph regression (zoom-live-2026-09-19): a
    // large graph fits at 0.2x, and an authored 10px label would render at
    // 2px — the floor rule must override the fitted scale.
    const content = { width: 5000, height: 1000 }
    const frame = { width: 1000, height: 1000 }
    const fitted = computeFittedScale(content, frame)
    const scale = computeOpeningScale({ content, frame, smallestLabelPx: 10 })
    expect(fitted).toBeCloseTo(0.2)
    expect(scale).toBeGreaterThan(fitted)
    expect(scale).toBeCloseTo(1.2) // 12px floor / 10px authored label
  })

  it('opens fitted for WIDE content (width the binding axis) once labels clear the floor', () => {
    const content = { width: 2000, height: 300 }
    const frame = { width: 800, height: 800 }
    const scale = computeOpeningScale({ content, frame, smallestLabelPx: 30 })
    // fitted = 800/2000 = 0.4 (within the 25%-400% range); label at fitted = 30 * 0.4 = 12 — exactly the floor, so fitted wins.
    expect(scale).toBeCloseTo(0.4)
  })

  it('opens at the label floor for TALL content (height the binding axis) below the floor', () => {
    const content = { width: 300, height: 4000 }
    const frame = { width: 800, height: 800 }
    const scale = computeOpeningScale({ content, frame, smallestLabelPx: 10 })
    // fitted = 800/4000 = 0.2; label at fitted = 2px, below the floor -> use 12/10 = 1.2.
    expect(scale).toBeCloseTo(1.2)
  })

  it('clamps the resolved opening scale to the shared 25%–400% range', () => {
    const scale = computeOpeningScale({
      content: { width: 100000, height: 100 },
      frame: { width: 10, height: 10 },
      smallestLabelPx: 1,
    })
    expect(scale).toBeLessThanOrEqual(ZOOMABLE_VIEW_MAX_SCALE)
    expect(scale).toBeGreaterThanOrEqual(ZOOMABLE_VIEW_MIN_SCALE)
  })

  it('ignores the label floor entirely when no label size is given (e.g. a plain image)', () => {
    const scale = computeOpeningScale({
      content: { width: 800, height: 800 },
      frame: { width: 400, height: 400 },
      smallestLabelPx: 0,
    })
    expect(scale).toBeCloseTo(0.5)
  })

  it("Fit reproduces the opening frame identically — same inputs, same result", () => {
    const params = { content: { width: 3000, height: 900 }, frame: { width: 600, height: 600 }, smallestLabelPx: 9 }
    expect(computeOpeningScale(params)).toBe(computeOpeningScale(params))
  })
})

describe('resolveSvgIntrinsicSize — defect 1 regression (viewBox, never a percentage width)', () => {
  it('resolves intrinsic size from viewBox on raw markup, even when width/height are percentages', () => {
    const markup = '<svg width="100%" height="100%" viewBox="0 0 1600 300" xmlns="http://www.w3.org/2000/svg"></svg>'
    expect(resolveSvgIntrinsicSize(markup)).toEqual({ width: 1600, height: 300 })
  })

  it('resolves intrinsic size from viewBox on a live SVGSVGElement, even when width/height are percentages', () => {
    const el = document.createElementNS('http://www.w3.org/2000/svg', 'svg')
    el.setAttribute('width', '100%')
    el.setAttribute('height', '100%')
    el.setAttribute('viewBox', '0 0 1600 300')
    expect(resolveSvgIntrinsicSize(el)).toEqual({ width: 1600, height: 300 })
  })

  it('handles a comma-separated viewBox the same as a space-separated one', () => {
    const markup = '<svg viewBox="0,0,1200,400"></svg>'
    expect(resolveSvgIntrinsicSize(markup)).toEqual({ width: 1200, height: 400 })
  })

  it('falls back to explicit pixel width/height only when there is no viewBox at all', () => {
    const markup = '<svg width="640" height="480"></svg>'
    expect(resolveSvgIntrinsicSize(markup)).toEqual({ width: 640, height: 480 })
  })

  it('never treats a bare percentage width/height (no viewBox) as an intrinsic size', () => {
    const markup = '<svg width="100%" height="100%"></svg>'
    expect(resolveSvgIntrinsicSize(markup)).toBeNull()
  })

  it('returns null for a degenerate zero-area viewBox', () => {
    expect(resolveSvgIntrinsicSize('<svg viewBox="0 0 0 300"></svg>')).toBeNull()
    expect(resolveSvgIntrinsicSize('<svg></svg>')).toBeNull()
  })
})

describe('useZoomableViewKeyboard — the +, −, 0, 1 shortcut contract', () => {
  it('binds +/=, -/_, 0 and 1 to zoom in, zoom out, fit and 100% respectively, and prevents default', () => {
    const handlers = { onZoomIn: vi.fn(), onZoomOut: vi.fn(), onFit: vi.fn(), onZoomTo100: vi.fn() }
    const { result } = renderHook(() => useZoomableViewKeyboard(handlers))
    const fire = (key: string) => {
      const preventDefault = vi.fn()
      result.current({ key, preventDefault } as unknown as React.KeyboardEvent)
      return preventDefault
    }
    expect(fire('+')).toHaveBeenCalled()
    expect(handlers.onZoomIn).toHaveBeenCalledTimes(1)
    expect(fire('=')).toHaveBeenCalled()
    expect(handlers.onZoomIn).toHaveBeenCalledTimes(2)
    fire('-')
    expect(handlers.onZoomOut).toHaveBeenCalledTimes(1)
    fire('0')
    expect(handlers.onFit).toHaveBeenCalledTimes(1)
    fire('1')
    expect(handlers.onZoomTo100).toHaveBeenCalledTimes(1)
  })

  it('ignores an unrelated key and does not call preventDefault', () => {
    const handlers = { onZoomIn: vi.fn(), onZoomOut: vi.fn(), onFit: vi.fn(), onZoomTo100: vi.fn() }
    const { result } = renderHook(() => useZoomableViewKeyboard(handlers))
    const preventDefault = vi.fn()
    result.current({ key: 'a', preventDefault } as unknown as React.KeyboardEvent)
    expect(preventDefault).not.toHaveBeenCalled()
    expect(Object.values(handlers).some((fn) => fn.mock.calls.length > 0)).toBe(false)
  })

  // LibraryPdfPreview attaches this handler to a container that ALSO holds
  // fillable PDF form fields — typing "10" into a date field must never
  // zoom.
  it('leaves a zoom shortcut typed into an editable field inside the view alone', async () => {
    const handlers = { onZoomIn: vi.fn(), onZoomOut: vi.fn(), onFit: vi.fn(), onZoomTo100: vi.fn() }
    function ZoomKeyboardHost() {
      const onKeyDown = useZoomableViewKeyboard(handlers)
      return (
        <div onKeyDown={onKeyDown}>
          <input aria-label="Date" />
        </div>
      )
    }
    const user = userEvent.setup()
    render(<ZoomKeyboardHost />)
    await user.type(screen.getByRole('textbox', { name: 'Date' }), '10')
    expect(screen.getByRole('textbox', { name: 'Date' })).toHaveValue('10')
    expect(handlers.onZoomIn).not.toHaveBeenCalled()
    expect(handlers.onZoomOut).not.toHaveBeenCalled()
    expect(handlers.onFit).not.toHaveBeenCalled()
    expect(handlers.onZoomTo100).not.toHaveBeenCalled()
  })

  it('still fires for the view frame itself, outside any editable target', () => {
    const handlers = { onZoomIn: vi.fn(), onZoomOut: vi.fn(), onFit: vi.fn(), onZoomTo100: vi.fn() }
    function ZoomKeyboardHost() {
      const onKeyDown = useZoomableViewKeyboard(handlers)
      return <div data-testid="frame" tabIndex={0} onKeyDown={onKeyDown} />
    }
    render(<ZoomKeyboardHost />)
    fireEvent.keyDown(screen.getByTestId('frame'), { key: '1' })
    expect(handlers.onZoomTo100).toHaveBeenCalledTimes(1)
  })

  it('lets the browser own a Ctrl/Cmd zoom combo instead of hijacking it', () => {
    const handlers = { onZoomIn: vi.fn(), onZoomOut: vi.fn(), onFit: vi.fn(), onZoomTo100: vi.fn() }
    const { result } = renderHook(() => useZoomableViewKeyboard(handlers))
    const preventDefault = vi.fn()
    result.current({ key: '=', ctrlKey: true, preventDefault } as unknown as React.KeyboardEvent)
    expect(preventDefault).not.toHaveBeenCalled()
    expect(handlers.onZoomIn).not.toHaveBeenCalled()
  })
})

describe('ZoomableMediaSurface — wheel zoom never leaks to the page', () => {
  it('calls preventDefault on a ctrl-wheel (trackpad pinch) event inside the view', () => {
    render(
      <ZoomableMediaSurface contentSize={{ width: 400, height: 300 }}>
        <img alt="" src="about:blank" />
      </ZoomableMediaSurface>,
    )
    const surface = screen.getByTestId('zoomable-media-surface')
    const result = fireEvent.wheel(surface, { deltaY: -100, ctrlKey: true })
    // testing-library's fireEvent returns false when the dispatched event's
    // preventDefault() was called — this is the D18 regression guard: a
    // trackpad pinch (ctrl-wheel) must never be left to resize the page.
    expect(result).toBe(false)
  })

  it('also calls preventDefault on a plain wheel event (the media viewer own-zoom, not a page scroll)', () => {
    render(
      <ZoomableMediaSurface contentSize={{ width: 400, height: 300 }}>
        <img alt="" src="about:blank" />
      </ZoomableMediaSurface>,
    )
    const surface = screen.getByTestId('zoomable-media-surface')
    const result = fireEvent.wheel(surface, { deltaY: -100 })
    expect(result).toBe(false)
  })

  it('does not zoom (and forwards no scale change) when disabled — the video-preview case (no zoom, D18 scope extension)', () => {
    const onScaleChange = vi.fn()
    render(
      <ZoomableMediaSurface contentSize={{ width: 400, height: 300 }} disabled onScaleChange={onScaleChange}>
        <img alt="" src="about:blank" />
      </ZoomableMediaSurface>,
    )
    const surface = screen.getByTestId('zoomable-media-surface')
    fireEvent.wheel(surface, { deltaY: -100 })
    expect(onScaleChange).not.toHaveBeenCalled()
  })
})

// The doc (docs/internal/design/components/zoomable-view.md) claims pinch is
// pointer-centred, same as wheel. These derive the expected translate
// directly from the SAME formula the wheel handler already uses
// (`next = anchor - (anchor - prevTranslate) * (nextScale / prevScale)`),
// never by reading the implementation.
describe('useZoomableMedia — pinch zoom is pointer-centred, like wheel', () => {
  function fakeFrame(rect: { left: number; top: number; width: number; height: number }) {
    return { getBoundingClientRect: () => rect } as unknown as HTMLDivElement
  }
  function pointerEvent(pointerId: number, clientX: number, clientY: number) {
    return { pointerId, clientX, clientY, target: {} } as unknown as React.PointerEvent<HTMLDivElement>
  }

  it('re-centres translate on the pinch midpoint across two incremental pinch-apart moves', () => {
    const { result } = renderHook(() => useZoomableMedia({ contentSize: { width: 400, height: 300 } }))
    act(() => { result.current.frameRef.current = fakeFrame({ left: 0, top: 0, width: 200, height: 200 }) })
    act(() => { result.current.controller.zoomTo100() }) // known baseline: scale=1, translate={0,0}

    // Two fingers land at (50,100) and (150,100) — distance 100, midpoint (100,100)
    // i.e. exactly the frame centre (anchor 0,0).
    act(() => { result.current.frameProps.onPointerDown(pointerEvent(1, 50, 100)) })
    act(() => { result.current.frameProps.onPointerDown(pointerEvent(2, 150, 100)) })

    // Move 1: finger 1 -> (0,100). Pair is now (0,100)/(150,100), distance 150,
    // ratio 1.5, midpoint (75,100) -> anchor (-25,0). prevScale=1, nextScale=1.5,
    // delta=1.5, prevTranslate={0,0}: next.x = -25 - (-25-0)*1.5 = 12.5.
    act(() => { result.current.frameProps.onPointerMove(pointerEvent(1, 0, 100)) })
    expect(result.current.scale).toBeCloseTo(1.5)
    expect(result.current.translate.x).toBeCloseTo(12.5)
    expect(result.current.translate.y).toBeCloseTo(0)

    // Move 2: finger 2 -> (200,100). Pair is now (0,100)/(200,100), distance 200,
    // ratio 2.0, midpoint (100,100) -> anchor (0,0). prevScale=1.5, nextScale=2,
    // delta=4/3, prevTranslate={12.5,0}: next.x = 0 - (0-12.5)*(4/3) = 50/3.
    act(() => { result.current.frameProps.onPointerMove(pointerEvent(2, 200, 100)) })
    expect(result.current.scale).toBeCloseTo(2)
    expect(result.current.translate.x).toBeCloseTo(50 / 3)
    expect(result.current.translate.y).toBeCloseTo(0)
  })

  it('shifts translate toward an off-centre pinch midpoint, not just the frame centre', () => {
    const { result } = renderHook(() => useZoomableMedia({ contentSize: { width: 400, height: 300 } }))
    act(() => { result.current.frameRef.current = fakeFrame({ left: 0, top: 0, width: 200, height: 200 }) })
    act(() => { result.current.controller.zoomTo100() })

    // Both fingers pinch near the frame's top-left, well off centre.
    act(() => { result.current.frameProps.onPointerDown(pointerEvent(1, 10, 10)) })
    act(() => { result.current.frameProps.onPointerDown(pointerEvent(2, 30, 10)) })
    // Move 1: finger 1 -> (0,10). Pair (0,10)/(30,10), distance 30, ratio 1.5,
    // midpoint (15,10) -> anchor (-85,-90). prevScale=1, nextScale=1.5, delta=1.5,
    // prevTranslate={0,0}: next = (42.5, 45).
    act(() => { result.current.frameProps.onPointerMove(pointerEvent(1, 0, 10)) })
    expect(result.current.translate.x).toBeCloseTo(42.5)
    expect(result.current.translate.y).toBeCloseTo(45)
    // Move 2: finger 2 -> (40,10). Pair (0,10)/(40,10), distance 40, ratio 2,
    // midpoint (20,10) -> anchor (-80,-90). prevScale=1.5, nextScale=2, delta=4/3,
    // prevTranslate={42.5,45}: next.x = -80 - (-80-42.5)*(4/3) = 250/3;
    // next.y = -90 - (-90-45)*(4/3) = 90 — off the frame centre in BOTH axes,
    // proving this isn't accidentally landing back on (0,0) by symmetry.
    act(() => { result.current.frameProps.onPointerMove(pointerEvent(2, 40, 10)) })
    expect(result.current.scale).toBeCloseTo(2)
    expect(result.current.translate.x).toBeCloseTo(250 / 3)
    expect(result.current.translate.y).toBeCloseTo(90)
  })
})

describe('ZoomPill — range-clamped button disabling', () => {
  it('disables Zoom out at the minimum and Zoom in at the maximum of the given range', () => {
    render(
      <ZoomPill
        zoom={ZOOMABLE_VIEW_MIN_SCALE}
        onZoomIn={vi.fn()}
        onZoomOut={vi.fn()}
        onFit={vi.fn()}
        onZoomTo100={vi.fn()}
      />,
    )
    expect(screen.getByRole('button', { name: 'Zoom out' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Zoom in' })).not.toBeDisabled()
  })

  it('disables Zoom out exactly at the EFFECTIVE floor for oversized content, not at 25%', () => {
    // Portrait content whose fit is ~17% — below the 25% configured min.
    // The caller (useZoomableMedia / useZoomableCanvasPill) is responsible
    // for passing `effectiveMinScale(fit)` as `min`, not the raw 25%.
    const fit = 0.17
    const floor = effectiveMinScale(fit)
    const { rerender } = render(
      <ZoomPill zoom={fit} min={floor} onZoomIn={vi.fn()} onZoomOut={vi.fn()} onFit={vi.fn()} onZoomTo100={vi.fn()} />,
    )
    // At the effective floor (the fitted scale itself), Zoom out is disabled.
    expect(screen.getByRole('button', { name: 'Zoom out' })).toBeDisabled()
    // Above the floor but still below the stock 25% min, Zoom out stays enabled.
    rerender(
      <ZoomPill zoom={0.2} min={floor} onZoomIn={vi.fn()} onZoomOut={vi.fn()} onFit={vi.fn()} onZoomTo100={vi.fn()} />,
    )
    expect(screen.getByRole('button', { name: 'Zoom out' })).not.toBeDisabled()
    // The percentage reflects the true 17%/20% zoom, never clamped up to 25%.
    expect(screen.getByTestId('zoomable-view-percent')).toHaveTextContent('20%')
  })

  it('still disables Zoom out at 25% when the fitted scale is above it (the ordinary case)', () => {
    const fit = 0.6
    const floor = effectiveMinScale(fit)
    expect(floor).toBe(ZOOMABLE_VIEW_MIN_SCALE)
    render(
      <ZoomPill
        zoom={ZOOMABLE_VIEW_MIN_SCALE}
        min={floor}
        onZoomIn={vi.fn()}
        onZoomOut={vi.fn()}
        onFit={vi.fn()}
        onZoomTo100={vi.fn()}
      />,
    )
    expect(screen.getByRole('button', { name: 'Zoom out' })).toBeDisabled()
  })

  it('omits "Zoom to selection" from the OPEN menu when the handler prop is not supplied (the media-viewer face)', async () => {
    const user = userEvent.setup()
    render(<ZoomPill zoom={1} onZoomIn={vi.fn()} onZoomOut={vi.fn()} onFit={vi.fn()} onZoomTo100={vi.fn()} />)
    await user.click(screen.getByTestId('zoomable-view-percent'))
    expect(screen.getByRole('menuitem', { name: /Fit/ })).toBeVisible()
    expect(screen.queryByTestId('zoomable-view-menu-selection')).not.toBeInTheDocument()
  })

  it('includes "Zoom to selection" in the OPEN menu when the handler prop IS supplied (the graph face)', async () => {
    const user = userEvent.setup()
    const onZoomToSelection = vi.fn()
    render(
      <ZoomPill
        zoom={1}
        onZoomIn={vi.fn()}
        onZoomOut={vi.fn()}
        onFit={vi.fn()}
        onZoomTo100={vi.fn()}
        onZoomToSelection={onZoomToSelection}
      />,
    )
    await user.click(screen.getByTestId('zoomable-view-percent'))
    await user.click(screen.getByRole('menuitem', { name: 'Zoom to selection' }))
    expect(onZoomToSelection).toHaveBeenCalledTimes(1)
  })
})
