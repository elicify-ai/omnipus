import { afterEach, expect, it, vi } from 'vitest'
import { startBrowserVideoDiagnostics } from './browserVideoDiagnostics'

afterEach(() => { vi.useRealTimers(); window.history.replaceState({}, '', '/'); delete window.__omnipusVideoDiagnostics })

it('is opt-in and retains only numeric video-stage measurements, not network identities', async () => {
  vi.useFakeTimers()
  const stats = vi.fn(async () => new Map<string, Record<string, unknown>>([
    ['video', { type: 'inbound-rtp', kind: 'video', framesDecoded: 20, totalDecodeTime: .1, jitterBufferDelay: .3, jitterBufferEmittedCount: 20, totalProcessingDelay: .5, address: 'secret', ssrc: 42 }],
    ['audio', { type: 'inbound-rtp', kind: 'audio', framesDecoded: 999 }],
    ['transport', { type: 'transport', selectedCandidatePairId: 'pair' }],
    ['pair', { type: 'candidate-pair', currentRoundTripTime: .02, localCandidateId: 'secret', remoteCandidateId: 'secret' }],
  ]))
  const pc = { getStats: stats } as unknown as RTCPeerConnection
  const off = startBrowserVideoDiagnostics(pc, () => null)
  await vi.advanceTimersByTimeAsync(1000)
  expect(stats).not.toHaveBeenCalled(); expect(window.__omnipusVideoDiagnostics).toBeUndefined(); off()
  window.history.replaceState({}, '', '/?browserVideoDiagnostics=1')
  const stop = startBrowserVideoDiagnostics(pc, () => null)
  await vi.advanceTimersByTimeAsync(1000)
  expect(window.__omnipusVideoDiagnostics?.samples[0]).toEqual({ peer: 1, atMs: expect.any(Number), durationMs: expect.any(Number), video: { framesDecoded: 20, totalDecodeTime: .1, jitterBufferDelay: .3, jitterBufferEmittedCount: 20, totalProcessingDelay: .5 }, rttSeconds: .02 })
  stop(); await vi.advanceTimersByTimeAsync(1000); expect(stats).toHaveBeenCalledTimes(1)
})

it('never overlaps pending reads and discards completion after teardown', async () => {
  vi.useFakeTimers(); window.history.replaceState({}, '', '/?browserVideoDiagnostics=1')
  let complete!: (report: RTCStatsReport) => void
  const stats = vi.fn(() => new Promise<RTCStatsReport>(resolve => { complete = resolve }))
  const stop = startBrowserVideoDiagnostics({ getStats: stats } as unknown as RTCPeerConnection, () => null)
  await vi.advanceTimersByTimeAsync(5000); expect(stats).toHaveBeenCalledTimes(1)
  stop(); complete(new Map() as RTCStatsReport); await Promise.resolve(); await Promise.resolve()
  expect(window.__omnipusVideoDiagnostics?.samples).toEqual([])
})

it('caps collection lifetime and shared history, and records failure without the error message', async () => {
  vi.useFakeTimers(); window.history.replaceState({}, '', '/?browserVideoDiagnostics=1')
  const stats = vi.fn(async () => { throw new Error('secret address') })
  const pc = { getStats: stats } as unknown as RTCPeerConnection
  const stop = startBrowserVideoDiagnostics(pc, () => null)
  await vi.advanceTimersByTimeAsync(121000)
  expect(stats.mock.calls.length).toBeGreaterThanOrEqual(119); expect(stats.mock.calls.length).toBeLessThanOrEqual(120)
  const count = stats.mock.calls.length
  await vi.advanceTimersByTimeAsync(120000); expect(stats).toHaveBeenCalledTimes(count)
  expect(JSON.stringify(window.__omnipusVideoDiagnostics)).not.toContain('secret')
  const again = startBrowserVideoDiagnostics(pc, () => null)
  await vi.advanceTimersByTimeAsync(121000)
  expect(window.__omnipusVideoDiagnostics?.samples).toHaveLength(120)
  stop(); again()
})

it('reads presentation counters only from the video bound to this peer and stops on page exit', async () => {
  vi.useFakeTimers(); window.history.replaceState({}, '', '/?browserVideoDiagnostics=1')
  const stream = {} as MediaStream
  const matching = document.createElement('video'), unrelated = document.createElement('video')
  Object.defineProperty(matching, 'srcObject', { value: stream })
  Object.defineProperty(matching, 'getVideoPlaybackQuality', { value: () => ({ totalVideoFrames: 17, droppedVideoFrames: 2 }) })
  Object.defineProperty(unrelated, 'getVideoPlaybackQuality', { value: () => ({ totalVideoFrames: 999, droppedVideoFrames: 999 }) })
  document.body.append(unrelated, matching)
  const stats = vi.fn(async () => new Map() as RTCStatsReport)
  const stop = startBrowserVideoDiagnostics({ getStats: stats } as unknown as RTCPeerConnection, () => stream)
  try {
    await vi.advanceTimersByTimeAsync(1000)
    expect(window.__omnipusVideoDiagnostics?.samples[0].playback).toEqual({ totalVideoFrames: 17, droppedVideoFrames: 2 })
    window.dispatchEvent(new Event('pagehide'))
    await vi.advanceTimersByTimeAsync(2000)
    expect(stats).toHaveBeenCalledTimes(1)
  } finally { stop(); matching.remove(); unrelated.remove() }
})
