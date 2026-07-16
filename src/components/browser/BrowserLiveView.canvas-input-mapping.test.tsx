// BrowserLiveView.canvas-input-mapping.test.tsx — Test 22 (live-browser-
// video-streaming spec, TDD plan). FR-008: the take-the-wheel input path
// (CDP Input.dispatch) and coordinate mapping MUST be unchanged now that the
// rendering surface is a `<canvas>` instead of the old `<img>`.
//
// mapClientToDevice (browserLiveCoords.ts) is a pure function of
// (clientX, clientY, containerRect, frameWidth, frameHeight, pageScale) —
// none of which reference the DOM element type doing the rendering. This
// suite proves that guarantee end-to-end: the canvas's `width`/`height`
// attributes are wired to the exact same values (frame.width/height) that
// used to drive the `<img>`'s intrinsic (naturalWidth/Height) CSS sizing,
// and a click at a given client coordinate produces EXACTLY the coordinate
// mapClientToDevice independently computes for the same inputs — i.e. the
// canvas swap changed nothing about where a click lands.
//
// Mocks BrowserLiveWsConnection the same way every sibling BrowserLiveView
// test file does (capturing the callbacks object passed to its constructor).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import type { BrowserLiveWsCallbacks } from '@/lib/browserLiveWs'
import { mapClientToDevice } from '@/lib/browserLiveCoords'
import { useChatStore } from '@/store/chat'

const { mockSendControl, mockSendInput, callbacksRef } = vi.hoisted(() => ({
  mockSendControl: vi.fn(() => true),
  mockSendInput: vi.fn(),
  callbacksRef: { current: null as BrowserLiveWsCallbacks | null },
}))

vi.mock('@/lib/browserLiveWs', () => ({
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
        isConnected: true,
      }
    },
  ),
}))

import { BrowserLiveView } from './BrowserLiveView'

const initialChatState = useChatStore.getState()

beforeEach(() => {
  vi.clearAllMocks()
  callbacksRef.current = null
})

afterEach(() => {
  vi.restoreAllMocks()
  useChatStore.setState(initialChatState, true)
})

/** Mounts, connects, and delivers a screencast frame at the given dimensions
 * (mirrors BrowserLiveView.mouseMoveThrottle.test.tsx's mountIdleWithFrame),
 * returning the frame container so callers can stub its layout rect. */
function mountWithFrame(width: number, height: number, pageScale?: number) {
  render(<BrowserLiveView sessionId="s1" agentId="a1" />)
  act(() => {
    callbacksRef.current?.onConnected?.()
    callbacksRef.current?.onScreencast?.({
      type: 'browser_screencast',
      session_id: 's1',
      seq: 1,
      data: 'AAAA',
      width,
      height,
      ...(pageScale !== undefined ? { page_scale: pageScale } : {}),
    })
  })
  return screen.getByTestId('browser-live-frame')
}

function stubRect(container: HTMLElement, rect: { left: number; top: number; width: number; height: number }) {
  vi.spyOn(container, 'getBoundingClientRect').mockReturnValue({
    ...rect,
    right: rect.left + rect.width,
    bottom: rect.top + rect.height,
    x: rect.left,
    y: rect.top,
    toJSON() {
      return {}
    },
  } as DOMRect)
}

describe('BrowserLiveView — canvas replaces <img>, same sizing surface', () => {
  it('the live rendering surface is a <canvas>, not an <img>', () => {
    mountWithFrame(1280, 720)
    expect(document.querySelector('img[alt="Live browser session"]')).not.toBeInTheDocument()
    expect(document.querySelector('canvas[aria-label="Live browser session"]')).toBeInTheDocument()
  })

  it("the canvas's intrinsic width/height attributes equal the frame's metadata dimensions — the exact role the old <img>'s naturalWidth/Height played for the h-auto/w-auto CSS sizing", () => {
    mountWithFrame(1280, 720)
    const canvas = document.querySelector('canvas') as HTMLCanvasElement
    expect(canvas.width).toBe(1280)
    expect(canvas.height).toBe(720)
  })

  it('re-sizes the canvas when a new screencast frame reports different dimensions (e.g. viewport resize)', () => {
    mountWithFrame(1280, 720)
    act(() => {
      callbacksRef.current?.onScreencast?.({
        type: 'browser_screencast',
        session_id: 's1',
        seq: 2,
        data: 'BBBB',
        width: 800,
        height: 600,
      })
    })
    const canvas = document.querySelector('canvas') as HTMLCanvasElement
    expect(canvas.width).toBe(800)
    expect(canvas.height).toBe(600)
  })
})

describe('BrowserLiveView — canvas→CSS coordinate mapping equals the prior <img> mapping (FR-008)', () => {
  it('a click at a 1:1 rect lands at exactly the client coordinate (scale 1, no page_scale)', () => {
    const container = mountWithFrame(1280, 720)
    stubRect(container, { left: 0, top: 0, width: 1280, height: 720 })

    fireEvent.pointerDown(container, { clientX: 200, clientY: 150 })

    const expected = mapClientToDevice(200, 150, { left: 0, top: 0, width: 1280, height: 720 }, 1280, 720, undefined)
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down', x: expected!.x, y: expected!.y }))
  })

  it('a click on a CSS-scaled-down container (rect smaller than frame dimensions) maps back up to device space, matching mapClientToDevice exactly', () => {
    // The container is rendered at HALF the frame's device-pixel size (a
    // realistic case: max-h-full/max-w-full shrank the canvas to fit the
    // panel) — same scaling scenario the old <img> handled identically.
    const container = mountWithFrame(1280, 720)
    stubRect(container, { left: 10, top: 20, width: 640, height: 360 })

    fireEvent.pointerDown(container, { clientX: 110, clientY: 70 })

    const expected = mapClientToDevice(110, 70, { left: 10, top: 20, width: 640, height: 360 }, 1280, 720, undefined)
    expect(expected).not.toBeNull()
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down', x: expected!.x, y: expected!.y }))
  })

  it('a page_scale != 1 (hi-DPI capture) divides the mapped coordinate down exactly like it did for the <img> path', () => {
    const container = mountWithFrame(1280, 720, 2)
    stubRect(container, { left: 0, top: 0, width: 1280, height: 720 })

    fireEvent.pointerDown(container, { clientX: 400, clientY: 300 })

    const expected = mapClientToDevice(400, 300, { left: 0, top: 0, width: 1280, height: 720 }, 1280, 720, 2)
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down', x: expected!.x, y: expected!.y }))
    // Sanity: page_scale halves the raw client coordinate.
    expect(expected).toEqual({ x: 200, y: 150 })
  })

  it('a wheel event maps through the identical formula (device coords + delta passthrough)', () => {
    const container = mountWithFrame(1280, 720)
    stubRect(container, { left: 0, top: 0, width: 1280, height: 720 })
    // The wheel handler only dispatches while the authoritative driveMode is
    // 'you-driving' (canDispatchInput) — implicit click-to-drive only flips
    // that once the server's 'controlling' ack round-trips back (see
    // computeDriveMode's own doc comment in BrowserLiveView.tsx); simulate
    // that ack directly rather than relying on the optimistic pendingTake
    // window, which intentionally does NOT authorize dispatch.
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    mockSendInput.mockClear()

    fireEvent.wheel(container, { clientX: 320, clientY: 180, deltaX: 5, deltaY: -5 })

    const expected = mapClientToDevice(320, 180, { left: 0, top: 0, width: 1280, height: 720 }, 1280, 720, undefined)
    expect(mockSendInput).toHaveBeenCalledWith(
      expect.objectContaining({ kind: 'wheel', x: expected!.x, y: expected!.y, delta_x: 5, delta_y: -5 }),
    )
  })
})
