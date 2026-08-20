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
  mockSendInput: vi.fn((_input?: { kind: string; x: number; y: number }) => {
    void _input // present only to give the mock the real call-argument type it's asserted against below
    return true
  }),
  mockSendViewport: vi.fn(() => true),
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

/** Stand-in MediaStream — jsdom has no real WebRTC/MediaStream. Passed via
 * the `mediaStream` test/override seam (see BrowserLiveView.webrtcSink.test.tsx)
 * — the JPEG screencast sink is gone (ADR-047), WebRTC video is the only path. */
function fakeMediaStream(id = 'stream-1'): MediaStream {
  return { id } as unknown as MediaStream
}

/** Simulates the <video> sink decoding its first real frame at 1280x720
 * (16:9) — deliberately NOT the same aspect ratio as the container box the
 * tests below stub (2:1), so a correct fix MUST letterbox-correct rather
 * than treat the raw box as content-tight. Direct replacement for the old
 * JPEG-era screencast frame's declared width/height. Requires the component
 * to already be attached (rendered with `mediaStream`). */
function decodeFirstFrame() {
  const video = screen.getByTestId('browser-live-video') as HTMLVideoElement
  Object.defineProperty(video, 'videoWidth', { value: 1280, configurable: true })
  Object.defineProperty(video, 'videoHeight', { value: 720, configurable: true })
  fireEvent.loadedMetadata(video)
}

function connectFrameAndDrive() {
  act(() => {
    callbacksRef.current?.onConnected?.()
  })
  act(() => {
    decodeFirstFrame()
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

/** Connects the WS and decodes a first frame — requires the component to
 * already be attached (rendered with `mediaStream`), since the container
 * (and the `<video>` element `decodeFirstFrame` fires `loadedmetadata` on)
 * only mounts once attached. */
function emitFirstFrame() {
  act(() => {
    callbacksRef.current?.onConnected?.()
    decodeFirstFrame()
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
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    emitFirstFrame()
    const classes = classTokens(screen.getByTestId('browser-live-video'))
    expect(classes.has('h-auto')).toBe(true)
    expect(classes.has('w-auto')).toBe(true)
    expect(classes.has('max-h-full')).toBe(true)
    expect(classes.has('max-w-full')).toBe(true)
    expect(classes.has('h-full')).toBe(false)
    expect(classes.has('w-full')).toBe(false)
  })

  it('switches to fill+object-contain sizing classes when fillContainer is set (pop-out layout)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} fillContainer />)
    emitFirstFrame()
    const classes = classTokens(screen.getByTestId('browser-live-video'))
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
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} fillContainer canAnnotate />)
    emitFirstFrame()
    const classes = classTokens(screen.getByTestId('browser-live-video'))
    expect(classes.has('h-full')).toBe(true)
    expect(classes.has('w-full')).toBe(true)
    expect(classes.has('object-contain')).toBe(true)
    expect(classes.has('max-h-full')).toBe(false)
    expect(classes.has('max-w-full')).toBe(false)
  })

  it('the frame container itself switches from shrink-wrap to fill-the-box sizing', () => {
    const { rerender } = render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    emitFirstFrame()
    let classes = classTokens(screen.getByTestId('browser-live-frame'))
    expect(classes.has('inline-block')).toBe(true)
    expect(classes.has('max-h-full')).toBe(true)
    expect(classes.has('h-full')).toBe(false)

    rerender(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} fillContainer />)
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
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} fillContainer />)
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
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} fillContainer />)
    connectFrameAndDrive()
    const container = stubMismatchedContainerRect()

    mockSendInput.mockClear()
    fireEvent.pointerDown(container, { clientX: 500, clientY: 250 })

    const sent = mockSendInput.mock.calls[0][0] as { kind: string; x: number; y: number }
    expect(sent.x).toBeCloseTo(640, 0)
    expect(sent.y).toBeCloseTo(360, 0)
  })

  it('maps a click just inside the visible content edge to a small-but-correct offset (not the raw-box offset)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} fillContainer />)
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
// bails if it is null. But the container div is gated on `attached`
// (mediaStream !== null — the WebRTC-build replacement for the old JPEG-era
// `frame` gate), while `connected` flips true from ws.onopen — SECONDS
// earlier, because the backend's cold capture start / WebRTC negotiation can
// take ~25s. With `[connected]` as the only dependency the effect ran exactly
// once, against a null ref, and never again: sendViewport was never called
// on a fresh open, so the capture stayed at its hardcoded 1280x720 and the
// feature was dead on its primary path.
//
// This test reproduces the REAL ordering (connected first, the stream
// attaching later — via a `rerender` that adds the `mediaStream` prop,
// standing in for the internal WebRTC signaling resolving asynchronously —
// in SEPARATE acts) rather than the collapsed single-act ordering the other
// helpers use — which is precisely why the existing suite could not see it.
describe('BrowserLiveView — adaptive viewport reporting (BLOCKER regression)', () => {
  it('sends the panel geometry after the video attaches, even though `connected` fired first', async () => {
    vi.useFakeTimers()
    try {
      mockSendViewport.mockClear()
      const { rerender } = render(<BrowserLiveView sessionId="s1" agentId="a1" fillContainer canAnnotate />)

      // 1. Connection opens. No mediaStream yet — the container does NOT
      //    exist yet either.
      act(() => {
        callbacksRef.current?.onConnected?.()
      })
      await vi.advanceTimersByTimeAsync(500)
      expect(mockSendViewport).not.toHaveBeenCalled()

      // 2. The video attaches (internal WebRTC signaling resolving, stood in
      //    for here via a `mediaStream` prop rerender) — NOW the container
      //    mounts and the effect must re-run. Give the element a real box so
      //    the send is not skipped.
      act(() => {
        rerender(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} fillContainer canAnnotate />)
      })
      act(() => {
        decodeFirstFrame()
      })
      const el = document.querySelector('[data-testid="browser-live-frame"]') as HTMLElement | null
      if (el) {
        el.getBoundingClientRect = () =>
          ({ width: 890, height: 1010, top: 0, left: 0, right: 890, bottom: 1010, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect
      }
      // Past the 400ms debounce AND the 250ms settle check (2026-08-04): a
      // new size is only committed once it has held still, so the send lands
      // ~650ms after the resize rather than ~400ms. See VIEWPORT_SETTLE_MS.
      await vi.advanceTimersByTimeAsync(800)

      expect(mockSendViewport).toHaveBeenCalled()
    } finally {
      vi.useRealTimers()
    }
  })
})

// Regression coverage for the three input/resize fixes (operator video 0804).
describe('BrowserLiveView — transient-resize guard and input pacing', () => {
  // The operator's recording contained 11 unintended capture rebuilds. Focusing
  // the address bar opens Safari's AutoFill accessory bar, shrinking the
  // container ~50px (measured: focused 644px, blurred 694px, focused 644px).
  // Each transition pushed a viewport and rebuilt the capture — a stall the
  // user never asked for.
  it('does not push a viewport while focus sits in a panel input', async () => {
    vi.useFakeTimers()
    try {
      render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} fillContainer canAnnotate />)
      act(() => {
        callbacksRef.current?.onConnected?.()
        emitFirstFrame()
      })
      const el = document.querySelector('[data-testid="browser-live-frame"]') as HTMLElement | null
      if (el) {
        el.getBoundingClientRect = () =>
          ({ width: 890, height: 1010, top: 0, left: 0, right: 890, bottom: 1010, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect
      }
      await vi.advanceTimersByTimeAsync(800)

      // Focus the address bar, then shrink the container the way the AutoFill
      // bar does.
      const bar = screen.getByLabelText('Address bar')
      act(() => {
        bar.focus()
      })
      mockSendViewport.mockClear()
      if (el) {
        el.getBoundingClientRect = () =>
          ({ width: 890, height: 960, top: 0, left: 0, right: 890, bottom: 960, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect
      }
      act(() => {
        window.dispatchEvent(new Event('resize'))
      })
      await vi.advanceTimersByTimeAsync(1200)

      expect(mockSendViewport).not.toHaveBeenCalled()
    } finally {
      vi.useRealTimers()
    }
  })

  // The settle check exists so a size captured mid-drag or mid-animation is
  // never committed: each commit rebuilds the capture stream, and an
  // intermediate geometry costs a stall for a size that is already obsolete.
  // Scope note (second review pass): this proves the INTERMEDIATE size is never
  // committed, which is what it is named for. It is NOT differential coverage
  // for the lost-resize race — it dispatches a trailing resize event, and that
  // event alone would rescue even the old non-re-arming settle. The test that
  // actually pins that race is "commits the final size when the box stops
  // changing mid-settle", which deliberately dispatches no follow-up event.
  it('does not commit an intermediate size while the box is still changing', async () => {
    vi.useFakeTimers()
    try {
      render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} fillContainer canAnnotate />)
      act(() => {
        callbacksRef.current?.onConnected?.()
        emitFirstFrame()
      })
      const el = document.querySelector('[data-testid="browser-live-frame"]') as HTMLElement | null
      const box = (h: number) =>
        ({ width: 890, height: h, top: 0, left: 0, right: 890, bottom: h, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect
      if (el) el.getBoundingClientRect = () => box(1010)
      await vi.advanceTimersByTimeAsync(800)
      mockSendViewport.mockClear()

      // A drag in progress: the box keeps moving across the debounce window.
      if (el) el.getBoundingClientRect = () => box(900)
      act(() => {
        window.dispatchEvent(new Event('resize'))
      })
      await vi.advanceTimersByTimeAsync(500)
      if (el) el.getBoundingClientRect = () => box(850) // moved again mid-settle
      await vi.advanceTimersByTimeAsync(200)

      // Nothing committed yet — the size never held still.
      expect(mockSendViewport).not.toHaveBeenCalled()

      // Drag ends: the size now holds, so exactly one commit lands.
      act(() => {
        window.dispatchEvent(new Event('resize'))
      })
      await vi.advanceTimersByTimeAsync(1200)
      expect(mockSendViewport).toHaveBeenCalledTimes(1)
      expect(mockSendViewport).toHaveBeenLastCalledWith(890, 850, expect.any(Number))
    } finally {
      vi.useRealTimers()
    }
  })
})

// Regression coverage for the LOST-RESIZE race the settle check introduced
// (found in review, 2026-08-04).
//
// The settle check re-measures after a delay and bails when the size moved
// again. The first version then RETURNED, on the assumption that "a trailing
// schedule() will bring us back". That assumption is false whenever the size
// stops changing DURING the settle window: the last ResizeObserver event has
// already been consumed by the 400ms debounce, so nothing else is pending, and
// the panel stays the wrong size until some unrelated resize happens to occur.
//
// This test drives exactly that timeline — one resize event, then the box
// settles at a DIFFERENT size before the settle timer fires, and no further
// events. The settle must chase the new size on its own.
describe('BrowserLiveView — settle must not lose the final size', () => {
  it('commits the final size when the box stops changing mid-settle', async () => {
    vi.useFakeTimers()
    try {
      render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} fillContainer canAnnotate />)
      act(() => {
        callbacksRef.current?.onConnected?.()
        emitFirstFrame()
      })
      const el = document.querySelector('[data-testid="browser-live-frame"]') as HTMLElement | null
      const box = (h: number) =>
        ({ width: 890, height: h, top: 0, left: 0, right: 890, bottom: h, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect
      if (el) el.getBoundingClientRect = () => box(1010)
      await vi.advanceTimersByTimeAsync(900)
      mockSendViewport.mockClear()

      // ONE resize event. push() will measure 900 after the 400ms debounce.
      if (el) el.getBoundingClientRect = () => box(900)
      act(() => {
        window.dispatchEvent(new Event('resize'))
      })
      await vi.advanceTimersByTimeAsync(450) // debounce elapsed, settle armed at 900

      // The box reaches its FINAL size during the settle window — and no
      // further resize events follow.
      if (el) el.getBoundingClientRect = () => box(850)
      await vi.advanceTimersByTimeAsync(2000)

      expect(mockSendViewport).toHaveBeenCalledTimes(1)
      expect(mockSendViewport).toHaveBeenLastCalledWith(890, 850, expect.any(Number))
    } finally {
      vi.useRealTimers()
    }
  })
})

// Regression coverage for the focus guard slipping through the settle window
// (found in the second review pass, 2026-08-04).
//
// The guard was applied only when push() ran. A settle armed while nothing was
// focused would therefore commit a size measured AFTER the user clicked into
// the address bar — i.e. the AutoFill-shrunk geometry the guard exists to
// reject, arriving through a 250ms hole.
describe('BrowserLiveView — focus guard covers the settle window', () => {
  it('does not commit a size measured after focus entered a text field', async () => {
    vi.useFakeTimers()
    try {
      render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} fillContainer canAnnotate />)
      act(() => {
        callbacksRef.current?.onConnected?.()
        emitFirstFrame()
      })
      const el = document.querySelector('[data-testid="browser-live-frame"]') as HTMLElement | null
      const box = (h: number) =>
        ({ width: 890, height: h, top: 0, left: 0, right: 890, bottom: h, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect
      if (el) el.getBoundingClientRect = () => box(1010)
      await vi.advanceTimersByTimeAsync(900)
      mockSendViewport.mockClear()

      // A genuine resize begins with NOTHING focused, so push() passes the
      // guard and arms the settle.
      if (el) el.getBoundingClientRect = () => box(900)
      act(() => {
        window.dispatchEvent(new Event('resize'))
      })
      await vi.advanceTimersByTimeAsync(450) // debounce done, settle pending

      // Mid-settle the user clicks the address bar; the accessory bar shrinks
      // the container further.
      act(() => {
        screen.getByLabelText('Address bar').focus()
      })
      if (el) el.getBoundingClientRect = () => box(850)
      await vi.advanceTimersByTimeAsync(2000)

      expect(mockSendViewport).not.toHaveBeenCalled()
    } finally {
      vi.useRealTimers()
    }
  })
})

// Coverage for the four failure modes both review passes flagged as real but
// untested. Each is a "silent and permanent" class: the panel ends up pinned to
// the wrong geometry with no error, no retry, and no symptom until some
// unrelated resize happens to fire.
describe('BrowserLiveView — viewport settle: recovery paths', () => {
  const box = (w: number, h: number) =>
    ({ width: w, height: h, top: 0, left: 0, right: w, bottom: h, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect

  function mountSettled() {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} fillContainer canAnnotate />)
    act(() => {
      callbacksRef.current?.onConnected?.()
      emitFirstFrame()
    })
    return document.querySelector('[data-testid="browser-live-frame"]') as HTMLElement
  }

  // The converse of the focus guard, and the one BOTH reviewers reproduced
  // independently: a REAL resize that completes while a text field holds focus.
  // On desktop, focusing the address bar does not change the container size, so
  // blur produces no resize either — without an explicit blur catch-up the
  // resize is suppressed once and never retried. Resize the window while typing
  // a URL and the panel stayed pinned to the old geometry indefinitely.
  it('commits a real resize that happened while a text field had focus, once focus leaves', async () => {
    vi.useFakeTimers()
    try {
      const el = mountSettled()
      el.getBoundingClientRect = () => box(890, 1010)
      await vi.advanceTimersByTimeAsync(900)
      mockSendViewport.mockClear()

      // Focus first, THEN resize — the guard suppresses this push entirely.
      act(() => {
        screen.getByLabelText('Address bar').focus()
      })
      el.getBoundingClientRect = () => box(890, 1300)
      act(() => {
        window.dispatchEvent(new Event('resize'))
      })
      await vi.advanceTimersByTimeAsync(2000)
      expect(mockSendViewport).not.toHaveBeenCalled()

      // Blur with NO further resize event. The focusout catch-up is the only
      // thing that can rescue the 1300 height now.
      act(() => {
        ;(document.activeElement as HTMLElement | null)?.blur()
      })
      await vi.advanceTimersByTimeAsync(2000)

      expect(mockSendViewport).toHaveBeenCalledWith(890, 1300, expect.any(Number))
    } finally {
      vi.useRealTimers()
    }
  })

  // A container that momentarily measures 0x0 mid-chase (parent hidden for a
  // tick, display:none flash during an unrelated layout pass). Bailing here
  // recreated the lost-resize bug one branch over.
  it('survives a transient zero-size measurement mid-chase', async () => {
    vi.useFakeTimers()
    try {
      const el = mountSettled()
      el.getBoundingClientRect = () => box(890, 1010)
      await vi.advanceTimersByTimeAsync(900)
      mockSendViewport.mockClear()

      el.getBoundingClientRect = () => box(890, 1300)
      act(() => {
        window.dispatchEvent(new Event('resize'))
      })
      await vi.advanceTimersByTimeAsync(450) // debounce done, settle armed

      el.getBoundingClientRect = () => box(0, 0) // panel blinks out
      await vi.advanceTimersByTimeAsync(600)
      el.getBoundingClientRect = () => box(890, 1300) // and comes back

      // No further resize event is dispatched — recovery must come from the
      // chase re-arming, not from a new observation.
      await vi.advanceTimersByTimeAsync(2000)

      expect(mockSendViewport).toHaveBeenCalledWith(890, 1300, expect.any(Number))
    } finally {
      vi.useRealTimers()
    }
  })

  // DPR is re-read at commit time, not captured once in push(). Dragging a
  // window between displays of different pixel ratios fires a window resize but
  // need not change the container's CSS box, so committing push()'s stale DPR
  // would stamp the cache with a ratio the client no longer has.
  it('commits the DPR in effect at settle time, not the one sampled at push time', async () => {
    vi.useFakeTimers()
    const dprSpy = vi.spyOn(window, 'devicePixelRatio', 'get').mockReturnValue(1)
    try {
      const el = mountSettled()
      el.getBoundingClientRect = () => box(890, 1010)
      await vi.advanceTimersByTimeAsync(900)
      mockSendViewport.mockClear()

      el.getBoundingClientRect = () => box(890, 1300)
      act(() => {
        window.dispatchEvent(new Event('resize'))
      })
      await vi.advanceTimersByTimeAsync(450) // settle armed while DPR is 1
      dprSpy.mockReturnValue(2) // dragged onto a Retina display
      await vi.advanceTimersByTimeAsync(2000)

      expect(mockSendViewport).toHaveBeenCalledWith(890, 1300, 2)
    } finally {
      dprSpy.mockRestore()
      vi.useRealTimers()
    }
  })
})
