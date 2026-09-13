// Local opt-in observation only; these records never cross the signaling socket.
interface VideoDiagnosticSample {
  peer: number
  atMs: number
  durationMs: number
  video?: Record<string, number>
  playback?: { totalVideoFrames: number; droppedVideoFrames: number }
  rttSeconds?: number
  error?: string
}
declare global {
  interface Window {
    __omnipusVideoDiagnostics?: { clock: string; intervalMs: number; maximumLifetimeMs: number; nextPeer: number; samples: VideoDiagnosticSample[] }
  }
}
const videoFields = ['timestamp', 'framesReceived', 'framesDecoded', 'framesDropped', 'framesPerSecond', 'frameWidth', 'frameHeight', 'bytesReceived', 'packetsReceived', 'packetsLost', 'jitter', 'jitterBufferDelay', 'jitterBufferTargetDelay', 'jitterBufferMinimumDelay', 'jitterBufferEmittedCount', 'totalDecodeTime', 'totalProcessingDelay', 'totalInterFrameDelay', 'freezeCount', 'totalFreezesDuration', 'nackCount', 'pliCount'] as const

/** Read cumulative counters over a bounded window; differences between samples
 * give interval means. None of these clocks alone measures input-to-picture latency. */
export function startBrowserVideoDiagnostics(pc: RTCPeerConnection, stream: () => MediaStream | null): () => void {
  if (typeof window === 'undefined' || new URLSearchParams(window.location.search).get('browserVideoDiagnostics') !== '1') return () => {}
  const sink = window.__omnipusVideoDiagnostics ??= { clock: 'viewer performance.now milliseconds; RTC time fields seconds except timestamp milliseconds', intervalMs: 1000, maximumLifetimeMs: 120000, nextPeer: 0, samples: [] }
  const peer = ++sink.nextPeer
  let busy = false, stopped = false
  const stop = () => {
    stopped = true
    clearInterval(timer); clearTimeout(lifetime)
    window.removeEventListener('pagehide', stop)
  }
  const timer = setInterval(() => {
    if (busy || stopped) return
    busy = true
    const atMs = performance.now()
    const sample: VideoDiagnosticSample = { peer, atMs, durationMs: 0 }
    void Promise.resolve().then(() => pc.getStats()).then(report => {
      if (stopped) return
      report.forEach(row => {
        if (row.type === 'inbound-rtp' && row.kind === 'video') {
          sample.video = {}
          for (const key of videoFields) if (typeof row[key] === 'number' && Number.isFinite(row[key])) sample.video[key] = row[key]
        }
        if (row.type === 'transport' && row.selectedCandidatePairId) {
          const rtt = report.get(row.selectedCandidatePairId)?.currentRoundTripTime
          if (typeof rtt === 'number' && Number.isFinite(rtt) && rtt >= 0) sample.rttSeconds = rtt
        }
      })
      const current = stream()
      const video = current && Array.from(document.querySelectorAll('video')).find(element => element.srcObject === current)
      if (video && typeof video.getVideoPlaybackQuality === 'function') {
        const quality = video.getVideoPlaybackQuality()
        sample.playback = { totalVideoFrames: quality.totalVideoFrames, droppedVideoFrames: quality.droppedVideoFrames }
      }
    }).catch(() => { sample.error = 'stats-unavailable' }).finally(() => {
      busy = false
      if (stopped) return
      sample.durationMs = performance.now() - atMs
      sink.samples.push(sample)
      if (sink.samples.length > 120) sink.samples.splice(0, sink.samples.length - 120)
    })
  }, 1000)
  const lifetime = setTimeout(stop, 120000)
  window.addEventListener('pagehide', stop, { once: true })
  return stop
}
