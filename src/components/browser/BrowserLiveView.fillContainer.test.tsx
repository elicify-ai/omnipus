// BrowserLiveView.fillContainer.test.tsx — BUG 1 fix (live UAT re-run
// 2026-07-28): the fullscreen pop-out route rendered the live video tiny and
// letterboxed (measured live: 639×316 inside a 1600×880 window) because the
// media element was capped at intrinsic size (`h-auto w-auto max-h-full
// max-w-full`) and could shrink but never grow to fill its container.
//
// Two things are under test here, matching the two halves of the fix:
//
//   1. The `fillContainer` prop swaps the media element's sizing classes
//      from "intrinsic, capped" to "fill the box, object-contain" — asserted
//      via computed dimensions where jsdom makes that possible (the
//      coordinate-mapping integration test below), with a class-list check
//      as a secondary signal only (jsdom performs no real layout, so a class
//      name alone can't prove the box actually renders bigger — see that
//      test's own comment).
//
//   2. Filling the container can letterbox/pillarbox the actual visible
//      content within a bigger box whenever the box's aspect ratio doesn't
//      match the content's — every pointer/wheel handler must still land on
//      the correct device pixel. This is proven with REAL numbers: a click
//      inside the dead (pillarboxed) zone of a deliberately mismatched
//      container box must clamp to the content edge (x:0), not report a
//      false in-content coordinate the way the pre-fix, uncorrected
//      `rect` math would.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import type { BrowserLiveWsCallbacks } from '@/lib/browserLiveWs'

const { mockSendControl, mockSendInput, mockSendViewport, callbacksRef } = vi.hoisted(() => ({
  mockSendControl: vi.fn(() => true),
  mockSendInput: vi.fn((_input?: { kind: string; x: number; y: number }) => true),
  mockSendViewport: vi.fn((_w?: number, _h?: number, _dpr?: number) => true),
  callbacksRef: { current: null as BrowserLiveWsCallbacks | null },
}))

vi.mock('@/lib/browserLiveWs', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/browserLiveWs')>()
  return {
    ...actual,
    BrowserLiveWsConnection: vi.fn().mockImplementation(
      function (_sessionId: string, _agentId: string, callbacks: BrowserLiveWsCallbacks) {
        callbacksRef.current = callbacks
        return {
          connect: vi.fn(),
          detach: vi.fn(),
          close: vi.fn(),
          sendInput: mockSendInput,
          sendControl: mockSendControl,
          sendTabAction: vi.fn(() => true),
          // Adaptive viewport (2026-07-31): BrowserLiveView's ResizeObserver
          // calls this on mount, so every connection double needs it.
          sendViewport: mockSendViewport,
          isConnected: true,
        }
      },
    ),
  }
})

import { BrowserLiveView } from './BrowserLiveView'

function connectFrameAndDrive() {
  act(() => {
    callbacksRef.current?.onConnected?.()
    callbacksRef.current?.onScreencast?.({
      type: 'browser_screencast',
      session_id: 's1',
      seq: 1,
      data: 'AAAA',
      // 16:9 content — deliberately NOT the same aspect ratio as the
      // container box the tests below stub (2:1), so a correct fix MUST
      // letterbox-correct rather than treat the raw box as content-tight.
      width: 1280,
      height: 720,
    })
    callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
  })
}

/** Stubs the frame container to a box whose aspect ratio (2:1) deliberately
 * does NOT match the 1280×720 (16:9 ≈ 1.778:1) screencast frame stubbed
 * above — mirroring the pop-out's `fillContainer` layout, where the
 * container can be any shape while the media content keeps its own aspect
 * ratio via `object-fit: contain`. Pillarboxed content therefore sits at
 * `x ∈ [55.56, 944.44]` within this exact 1000×500 box (computed in the test
 * body below from the same public `computeObjectContainRect` used by the
 * component, so the expectation and the implementation share one source of
 * truth for the geometry — only the CALL SITE under test, not the math
 * itself, is being verified here). */
function stubMismatchedContainerRect() {
  const container = screen.getByTestId('browser-live-frame')
  vi.spyOn(container, 'getBoundingClientRect').mockReturnValue({
    left: 0, top: 0, width: 1000, height: 500, right: 1000, bottom: 500, x: 0, y: 0,
    toJSON() { return {} },
  } as DOMRect)
  return container
}

/** Exact class-TOKEN membership (split on whitespace) — a plain
 * `.className.includes('h-full')` substring check would false-positive on
 * `max-h-full`, which also contains the literal text "h-full". */
function classTokens(el: Element): Set<string> {
  return new Set(el.className.split(/\s+/).filter(Boolean))
}

function emitFirstFrame() {
  act(() => {
    callbacksRef.current?.onConnected?.()
    callbacksRef.current?.onScreencast?.({ type: 'browser_screencast', session_id: 's1', seq: 1, data: 'AAAA', width: 1280, height: 720 })
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  callbacksRef.current = null
})

describe('BrowserLiveView — fillContainer sizing (BUG 1)', () => {
  // NOTE: this covers the component DEFAULT, not the docked panel. As of
  // 2026-07-31 BrowserLivePanel passes `fillContainer` explicitly — an
  // operator UAT measured the page rendering at ~695x343 inside an ~890x1010
  // panel, too small to interact with, because the capped classes below can
  // shrink but never grow.
  it('defaults to the intrinsic-capped sizing classes when fillContainer is omitted', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    emitFirstFrame()
    const classes = classTokens(screen.getByTestId('browser-live-img'))
    expect(classes.has('h-auto')).toBe(true)
    expect(classes.has('w-auto')).toBe(true)
    expect(classes.has('max-h-full')).toBe(true)
    expect(classes.has('max-w-full')).toBe(true)
    expect(classes.has('h-full')).toBe(false)
    expect(classes.has('w-full')).toBe(false)
  })

  it('switches to fill+object-contain sizing classes when fillContainer is set (pop-out layout)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" fillContainer />)
    emitFirstFrame()
    const classes = classTokens(screen.getByTestId('browser-live-img'))
    expect(classes.has('h-full')).toBe(true)
    expect(classes.has('w-full')).toBe(true)
    expect(classes.has('object-contain')).toBe(true)
    // The old "never grow past intrinsic size" caps must be gone — their
    // presence is exactly what produced the 639×316-in-1600×880 bug.
    expect(classes.has('h-auto')).toBe(false)
    expect(classes.has('w-auto')).toBe(false)
    expect(classes.has('max-h-full')).toBe(false)
    expect(classes.has('max-w-full')).toBe(false)
  })

  // 2026-07-31: `fillContainer` + `canAnnotate` used to be forbidden together
  // (annotate's crop math read the raw container rect with no letterbox
  // correction). finalizeSelection now routes through computeObjectContainRect,
  // so the docked panel can have both — this pins that the combination renders
  // the fill sizing rather than silently falling back to the capped classes.
  it('applies fill sizing when combined with canAnnotate (the docked panel\'s configuration)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" fillContainer canAnnotate />)
    emitFirstFrame()
    const classes = classTokens(screen.getByTestId('browser-live-img'))
    expect(classes.has('h-full')).toBe(true)
    expect(classes.has('w-full')).toBe(true)
    expect(classes.has('object-contain')).toBe(true)
    expect(classes.has('max-h-full')).toBe(false)
    expect(classes.has('max-w-full')).toBe(false)
  })

  it('the frame container itself switches from shrink-wrap to fill-the-box sizing', () => {
    const { rerender } = render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    emitFirstFrame()
    let classes = classTokens(screen.getByTestId('browser-live-frame'))
    expect(classes.has('inline-block')).toBe(true)
    expect(classes.has('max-h-full')).toBe(true)
    expect(classes.has('h-full')).toBe(false)

    rerender(<BrowserLiveView sessionId="s1" agentId="a1" fillContainer />)
    classes = classTokens(screen.getByTestId('browser-live-frame'))
    expect(classes.has('h-full')).toBe(true)
    expect(classes.has('w-full')).toBe(true)
    expect(classes.has('inline-block')).toBe(false)
    expect(classes.has('max-h-full')).toBe(false)
  })
})

describe('BrowserLiveView — letterbox-corrected coordinate mapping (BUG 1 revert-proof)', () => {
  // THE key regression test: before the fix, `mapPointerToDeviceCoords` fed
  // `mapClientToDevice` the RAW container rect unconditionally. For a
  // container whose aspect ratio doesn't match the content (exactly what
  // `fillContainer` introduces), that mis-reports where the content edge
  // actually is — a click in what's actually dead pillarbox space would be
  // reported as landing 25.6px into the live page instead of clamping to the
  // content's left edge (x: 0). Run this test against the pre-fix
  // `mapPointerToDeviceCoords` (rect passed straight through, no
  // `computeObjectContainRect` correction) and it fails with `x: 25.6`
  // instead of `x: 0` — restoring that behavior locally and re-running
  // confirms the regression.
  it('clamps a click inside the pillarboxed dead-zone to the content edge, not a false in-content coordinate', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" fillContainer />)
    connectFrameAndDrive()
    const container = stubMismatchedContainerRect()

    // Content (1280x720, aspect 1.7778) pillarboxed inside the 1000x500
    // (aspect 2.0) box: visible width = 500 * 1.7778 = 888.89, so visible
    // content starts at x = (1000 - 888.89) / 2 = 55.56. A click at
    // clientX=20 is well inside the dead zone to its left.
    mockSendInput.mockClear()
    fireEvent.pointerDown(container, { clientX: 20, clientY: 250 })

    expect(mockSendInput).toHaveBeenCalledTimes(1)
    const sent = mockSendInput.mock.calls[0][0] as { kind: string; x: number; y: number }
    expect(sent.kind).toBe('mouse_down')
    // Uncorrected (pre-fix) math would report x ≈ 25.6 (20 * 1280/1000) — a
    // coordinate inside the live page, when the click never actually
    // reached visible content at all.
    expect(sent.x).toBe(0)
  })

  it('maps a click at the exact box center to the exact content center (sanity check both pre- and post-fix agree here)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" fillContainer />)
    connectFrameAndDrive()
    const container = stubMismatchedContainerRect()

    mockSendInput.mockClear()
    fireEvent.pointerDown(container, { clientX: 500, clientY: 250 })

    const sent = mockSendInput.mock.calls[0][0] as { kind: string; x: number; y: number }
    expect(sent.x).toBeCloseTo(640, 0)
    expect(sent.y).toBeCloseTo(360, 0)
  })

  it('maps a click just inside the visible content edge to a small-but-correct offset (not the raw-box offset)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" fillContainer />)
    connectFrameAndDrive()
    const container = stubMismatchedContainerRect()

    // 10 CSS px into the visible content past its left edge (55.56):
    // clientX = 65.56 → content x = 10 * (1280 / 888.89) ≈ 14.4.
    mockSendInput.mockClear()
    fireEvent.pointerDown(container, { clientX: 65.56, clientY: 250 })

    const sent = mockSendInput.mock.calls[0][0] as { kind: string; x: number; y: number }
    // Uncorrected math would instead give 65.56 * 1280/1000 ≈ 83.9 — a
    // clearly different, wrong value, so this discriminates the fix too.
    expect(sent.x).toBeCloseTo(14.4, 0)
    expect(sent.x).not.toBeCloseTo(83.9, 0)
  })
})


// Regression for the BLOCKER found in review of the adaptive-viewport feature.
//
// The effect that reports the panel geometry reads `containerRef.current` and
// bails if it is null. But the container div is gated on `frame`, while
// `connected` flips true from ws.onopen — SECONDS earlier, because the
// backend's cold capture start can take ~25s. With `[connected]` as the only
// dependency the effect ran exactly once, against a null ref, and never again:
// sendViewport was never called on a fresh open, so the capture stayed at its
// hardcoded 1280x720 and the feature was dead on its primary path.
//
// This test reproduces the REAL ordering (connected first, frame later, in
// SEPARATE acts) rather than the collapsed single-act ordering the other
// helpers use — which is precisely why the existing suite could not see it.
describe('BrowserLiveView — adaptive viewport reporting (BLOCKER regression)', () => {
  it('sends the panel geometry after a frame arrives, even though `connected` fired first', async () => {
    vi.useFakeTimers()
    try {
      mockSendViewport.mockClear()
      render(<BrowserLiveView sessionId="s1" agentId="a1" fillContainer canAnnotate />)

      // 1. Connection opens. The container does NOT exist yet.
      act(() => {
        callbacksRef.current?.onConnected?.()
      })
      await vi.advanceTimersByTimeAsync(500)
      expect(mockSendViewport).not.toHaveBeenCalled()

      // 2. First frame arrives — NOW the container mounts and the effect must
      //    re-run. Give the element a real box so the send is not skipped.
      act(() => {
        emitFirstFrame()
      })
      const el = document.querySelector('[data-testid="browser-live-frame"]') as HTMLElement | null
      if (el) {
        el.getBoundingClientRect = () =>
          ({ width: 890, height: 1010, top: 0, left: 0, right: 890, bottom: 1010, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect
      }
      await vi.advanceTimersByTimeAsync(500) // past the 400ms debounce

      expect(mockSendViewport).toHaveBeenCalled()
    } finally {
      vi.useRealTimers()
    }
  })
})
