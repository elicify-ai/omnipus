import { afterEach, beforeEach, vi } from 'vitest'
import { cleanup, act } from '@testing-library/react'
import type { BrowserLiveWsCallbacks } from '@/lib/browserLiveWs'

let callbacks = new Map<HTMLVideoElement, VideoFrameRequestCallback>()
let originalRequest: PropertyDescriptor | undefined
let originalCancel: PropertyDescriptor | undefined

/** Mock only the browser's frame callback boundary; the production gate stays real. */
export function installBrowserFrameCallbacks(): void {
  beforeEach(() => {
    callbacks = new Map()
    originalRequest = Object.getOwnPropertyDescriptor(HTMLVideoElement.prototype, 'requestVideoFrameCallback')
    originalCancel = Object.getOwnPropertyDescriptor(HTMLVideoElement.prototype, 'cancelVideoFrameCallback')
    Object.defineProperty(HTMLVideoElement.prototype, 'requestVideoFrameCallback', {
      configurable: true,
      value: function (this: HTMLVideoElement, callback: VideoFrameRequestCallback) { callbacks.set(this, callback); return 1 },
    })
    Object.defineProperty(HTMLVideoElement.prototype, 'cancelVideoFrameCallback', {
      configurable: true,
      value: function (this: HTMLVideoElement) { callbacks.delete(this) },
    })
  })
  afterEach(() => {
    cleanup()
    if (originalRequest) Object.defineProperty(HTMLVideoElement.prototype, 'requestVideoFrameCallback', originalRequest)
    else Reflect.deleteProperty(HTMLVideoElement.prototype, 'requestVideoFrameCallback')
    if (originalCancel) Object.defineProperty(HTMLVideoElement.prototype, 'cancelVideoFrameCallback', originalCancel)
    else Reflect.deleteProperty(HTMLVideoElement.prototype, 'cancelVideoFrameCallback')
  })
}

export function emitBrowserFrame(video: HTMLVideoElement, metadata: { rtpTimestamp?: number; expectedDisplayTime?: number }): void {
  const callback = callbacks.get(video)
  callbacks.delete(video)
  callback?.(performance.now(), {
    width: video.videoWidth, height: video.videoHeight, mediaTime: 0,
    presentationTime: performance.now(), presentedFrames: 1, ...metadata,
  } as VideoFrameCallbackMetadata)
}

/** Establish the same explicit boundary and compositor evidence as a live peer. */
export function confirmBrowserFrame(callbacks: BrowserLiveWsCallbacks | null, video: HTMLVideoElement): void {
  if (!callbacks) throw new Error('browser callbacks must be connected before presenting a frame')
  act(() => {
    callbacks.onVideoHealth({ type: 'browser_video_health', session_id: 's1', state: 'recovered', capture_id: 'capture-test', capture_generation: 1, rtp_timestamp: 100, css_width: 1280, css_height: 720 })
    if (vi.isFakeTimers() && performance.now() === 0) vi.advanceTimersByTime(1)
    emitBrowserFrame(video, { rtpTimestamp: 100, expectedDisplayTime: performance.now() })
  })
}
