import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import type { BrowserLiveWsCallbacks } from '@/lib/browserLiveWs'
import type { SubmitAnnotationParams } from '@/lib/browserAnnotate'
import { confirmBrowserFrame, installBrowserFrameCallbacks } from './browserFrameTestUtils'

const state = vi.hoisted(() => ({ callbacks: null as BrowserLiveWsCallbacks | null, submit: vi.fn<(_params: SubmitAnnotationParams) => Promise<void>>() }))
vi.mock('@/lib/browserAnnotate', async importOriginal => ({
  ...await importOriginal<typeof import('@/lib/browserAnnotate')>(), submitAnnotation: state.submit,
}))
vi.mock('@/lib/browserLiveWs', async importOriginal => ({
  ...await importOriginal<typeof import('@/lib/browserLiveWs')>(),
  BrowserLiveWsConnection: vi.fn().mockImplementation(function (_session: string, _agent: string, callbacks: BrowserLiveWsCallbacks) {
    state.callbacks = callbacks
    return { connect: vi.fn(), close: vi.fn(), detach: vi.fn(), sendInput: () => true, sendControl: () => true, sendViewport: () => true, isConnected: true }
  }),
}))
import { BrowserLiveView } from './BrowserLiveView'

installBrowserFrameCallbacks()
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

it.each([false, true])('keeps crop pixels and original CSS proof across asynchronous crop (retire=%s)', async retire => {
  state.submit.mockReset().mockImplementation(() => new Promise(() => {}))
  vi.stubGlobal('PointerEvent', MouseEvent)
  const drawImage = vi.fn()
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({ drawImage } as unknown as CanvasRenderingContext2D)
  let finishCrop: BlobCallback | undefined
  vi.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation(callback => { finishCrop = callback })
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:annotation')
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
  render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={{ id: 'annotation-stream' } as MediaStream} canAnnotate />)
  const video = screen.getByTestId('browser-live-video') as HTMLVideoElement
  act(() => {
    state.callbacks?.onConnected?.()
    Object.defineProperties(video, { videoWidth: { value: 640, configurable: true }, videoHeight: { value: 360, configurable: true }, readyState: { value: 2, configurable: true } })
    fireEvent.loadedMetadata(video)
    confirmBrowserFrame(state.callbacks, video)
  })
  const container = screen.getByTestId('browser-live-frame')
  vi.spyOn(container, 'getBoundingClientRect').mockReturnValue({ left: 0, top: 0, width: 640, height: 360, right: 640, bottom: 360, x: 0, y: 0, toJSON: () => ({}) })
  container.setPointerCapture = vi.fn()
  container.releasePointerCapture = vi.fn()
  fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))
  fireEvent.pointerDown(container, { clientX: 300, clientY: 160, button: 0 })
  fireEvent.pointerMove(container, { clientX: 340, clientY: 200, button: 0 })
  fireEvent.pointerUp(container, { clientX: 340, clientY: 200, button: 0 })
  expect(drawImage).toHaveBeenCalledWith(video, 300, 160, 40, 40, 0, 0, 40, 40)
  expect(finishCrop).toBeTypeOf('function')
  if (retire) act(() => state.callbacks?.onVideoHealth({ type: 'browser_video_health', session_id: 's1', state: 'transitioning', capture_id: 'capture-test', capture_generation: 2, css_width: 1280, css_height: 720 }))
  await act(async () => finishCrop?.(new Blob(['png'], { type: 'image/png' })))
  fireEvent.change(screen.getByRole('textbox', { name: 'Annotation comment' }), { target: { value: 'Explain this' } })
  fireEvent.click(screen.getByRole('button', { name: /^send$/i }))
  await waitFor(() => expect(state.submit).toHaveBeenCalledTimes(1))
  const submission = state.submit.mock.calls[0][0]
  expect(submission.point).toEqual({ x: 640, y: 360 })
  expect(submission.isPointCurrent?.()).toBe(!retire)
  expect(submission.file).toBeInstanceOf(File)
})
