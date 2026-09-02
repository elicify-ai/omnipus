/**
 * browserWebRTC.test.ts — BrowserWebRTCSession state-machine tests
 * (ADR-047 "live-browser WebRTC", wave-plan.md W2-B).
 *
 * jsdom has no real WebRTC implementation (see BrowserLiveView.webrtcSink.test.tsx's
 * own note on this), so every test here injects a fake `RTCPeerConnection`
 * via the `pcFactory` constructor option rather than touching the real
 * global. Uses REAL timers with short, explicitly-configured
 * answerTimeoutMs/disconnectedGraceMs/retryDelayMs values (rather than
 * vi.useFakeTimers()) because `_beginOffer` chains several awaited
 * Promise-returning fake methods (createOffer/setLocalDescription) ahead of
 * each timer — interleaving fake timers with that microtask chain is a
 * known footgun; real timers with generous margins are simpler and no less
 * deterministic for delays this small.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { BrowserWebRTCSession, translateWebRTCFallbackReason, pcFactoryWithICEServers } from './browserWebRTC'

// Operator directive (JPEG-fallback removal) — WebRTC is the only live-video
// path now, so every onFallback reason must translate to an honest,
// user-facing message (BrowserLiveView.tsx's `displayError`), never silence.
describe('translateWebRTCFallbackReason', () => {
  it('maps the three capability-gate reasons to a specific, non-retry-inviting explanation', () => {
    expect(translateWebRTCFallbackReason('disabled')).toMatch(/turned off for this installation/i)
    expect(translateWebRTCFallbackReason('not_capable')).toMatch(/isn't supported on this server/i)
    expect(translateWebRTCFallbackReason('lite_build')).toMatch(/lite build/i)
  })

  it('maps "error" to a generic-but-honest message inviting a retry', () => {
    expect(translateWebRTCFallbackReason('error')).toMatch(/reported an error/i)
  })

  it('maps multi_agent_capture_denied to a named, actionable explanation', () => {
    expect(translateWebRTCFallbackReason('multi_agent_capture_denied')).toMatch(/already in use by another agent/i)
  })

  it('maps every other known runtime reason to a generic connection-failed message that names the raw reason', () => {
    for (const reason of [
      'ice-failed',
      'ice-disconnected-timeout',
      'answer-timeout',
      'offer-send-failed',
      'set-remote-description-failed',
      'pc-create-failed',
      'offer-setup-failed',
      'no-local-description',
      'stream-stopped',
      'unavailable',
    ]) {
      const message = translateWebRTCFallbackReason(reason)
      expect(message).toMatch(/connection failed/i)
      expect(message).toContain(reason)
    }
  })

  it('never returns an empty string, and never silently swallows an unrecognized future reason', () => {
    const message = translateWebRTCFallbackReason('some-future-reason-not-yet-enumerated')
    expect(message.length).toBeGreaterThan(0)
    expect(message).toContain('some-future-reason-not-yet-enumerated')
  })

  // UAT case 16. The enum can only ever name a CATEGORY of failure. When the
  // gateway also sends the cause (browser_webrtc_state.reason_detail), the
  // operator must see it — the generic sentence on its own re-hides exactly
  // what deleting the JPEG fallback was supposed to expose.
  describe('reason_detail (UAT case 16)', () => {
    const cause =
      'capture session: create encoder target: browser: timed out after 20s waiting for the browser to attach the tab (target may be unresponsive)'

    it('carries the gateway-reported cause verbatim, not just the generic sentence', () => {
      const message = translateWebRTCFallbackReason('error', cause)
      expect(message).toContain('timed out after 20s waiting for the browser to attach the tab')
      expect(message).toContain(cause)
    })

    it('keeps the plain-language category and its retry guidance in front of the cause', () => {
      const message = translateWebRTCFallbackReason('error', cause)
      expect(message).toMatch(/reported an error/i)
      expect(message).toMatch(/retry/i)
      expect(message.indexOf('reported an error')).toBeLessThan(message.indexOf(cause))
    })

    it('attaches a cause to a CLASSIFIED reason too — the class alone still does not say which wait expired', () => {
      const message = translateWebRTCFallbackReason(
        'ingest_timeout',
        'webrtc: viewer [v1/x]: no ingest video track after waiting 15s',
      )
      expect(message).toMatch(/video capture didn't start in time/i)
      expect(message).toContain('no ingest video track after waiting 15s')
    })

    it('is unchanged when the gateway sends no cause, or an empty/whitespace one', () => {
      const bare = translateWebRTCFallbackReason('error')
      expect(translateWebRTCFallbackReason('error', undefined)).toBe(bare)
      expect(translateWebRTCFallbackReason('error', '')).toBe(bare)
      expect(translateWebRTCFallbackReason('error', '   ')).toBe(bare)
    })

    it('appends a cause to an unrecognized future reason as well', () => {
      const message = translateWebRTCFallbackReason('some-future-reason', 'relay refused the allocation')
      expect(message).toContain('some-future-reason')
      expect(message).toContain('relay refused the allocation')
    })
  })
})

// jsdom has no `MediaStream` global at all — stub a minimal one (just
// enough for the ontrack-assembly branch's `new MediaStream([track])` /
// `.addTrack()` / `.getTracks()` calls) so tests can exercise that branch.
// Real browsers always provide the genuine constructor; this only stands in
// for the test environment.
class FakeMediaStream {
  private readonly tracks: MediaStreamTrack[]
  constructor(tracks: MediaStreamTrack[] = []) {
    this.tracks = [...tracks]
  }
  addTrack(track: MediaStreamTrack): void {
    this.tracks.push(track)
  }
  getTracks(): MediaStreamTrack[] {
    return this.tracks
  }
}

beforeEach(() => {
  vi.stubGlobal('MediaStream', FakeMediaStream)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

// ── Fake RTCPeerConnection / RTCDataChannel ─────────────────────────────────

interface FakeDataChannel {
  readyState: 'connecting' | 'open' | 'closing' | 'closed'
  onopen: (() => void) | null
  onclose: (() => void) | null
  send: ReturnType<typeof vi.fn>
  close: ReturnType<typeof vi.fn>
}

interface FakePc {
  addTransceiver: ReturnType<typeof vi.fn>
  createDataChannel: ReturnType<typeof vi.fn>
  createOffer: ReturnType<typeof vi.fn>
  setLocalDescription: ReturnType<typeof vi.fn>
  setRemoteDescription: ReturnType<typeof vi.fn>
  close: ReturnType<typeof vi.fn>
  localDescription: { sdp: string; type: string } | null
  iceGatheringState: string
  iceConnectionState: string
  ontrack: ((event: RTCTrackEvent) => void) | null
  oniceconnectionstatechange: (() => void) | null
  onicegatheringstatechange: (() => void) | null
  dataChannel: FakeDataChannel
}

function makeFakeDataChannel(): FakeDataChannel {
  return {
    readyState: 'connecting',
    onopen: null,
    onclose: null,
    send: vi.fn(),
    close: vi.fn(),
  }
}

function makeFakePc(): FakePc {
  const dc = makeFakeDataChannel()
  const pc: FakePc = {
    addTransceiver: vi.fn(),
    createDataChannel: vi.fn(() => dc),
    createOffer: vi.fn(async () => ({ type: 'offer', sdp: 'fake-offer-sdp' })),
    setLocalDescription: vi.fn(async (desc: { sdp: string; type: string }) => {
      pc.localDescription = desc
    }),
    setRemoteDescription: vi.fn(async () => undefined),
    close: vi.fn(),
    localDescription: null,
    iceGatheringState: 'new',
    iceConnectionState: 'new',
    ontrack: null,
    oniceconnectionstatechange: null,
    onicegatheringstatechange: null,
    dataChannel: dc,
  }
  return pc
}

function asRTCPeerConnection(pc: FakePc): RTCPeerConnection {
  return pc as unknown as RTCPeerConnection
}

/** Flushes the microtask queue (a real macrotask boundary), letting every
 * pending `await`-chained fake promise in `_beginOffer` settle. */
function flush(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0))
}

function wait(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

/** Drives a pc through offer creation + ICE-gathering-complete, leaving the
 * machine in 'offering' with the offer already sent — the common setup step
 * most tests below build on. */
async function driveToOfferSent(pc: FakePc): Promise<void> {
  await flush()
  pc.iceGatheringState = 'complete'
  pc.onicegatheringstatechange?.()
  await flush()
}

async function driveToConnected(pc: FakePc): Promise<void> {
  await driveToOfferSent(pc)
  pc.iceConnectionState = 'connected'
  pc.oniceconnectionstatechange?.()
  await flush()
}

describe('BrowserWebRTCSession — happy path', () => {
  it('offers video+audio recvonly transceivers and an "input" data channel, waits for ICE gathering to complete before sending the offer, applies the answer, and transitions to connected on ICE connect', async () => {
    const pc = makeFakePc()
    const sendOffer = vi.fn()
    const onStream = vi.fn()
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc) })
    machine.onStream(onStream)

    machine.start(sendOffer)
    await flush()

    expect(pc.addTransceiver).toHaveBeenCalledWith('video', { direction: 'recvonly' })
    expect(pc.addTransceiver).toHaveBeenCalledWith('audio', { direction: 'recvonly' })
    expect(pc.createDataChannel).toHaveBeenCalledWith('input')
    expect(pc.setLocalDescription).toHaveBeenCalledWith({ type: 'offer', sdp: 'fake-offer-sdp' })
    // ICE gathering hasn't completed yet — the non-trickle offer must NOT
    // have been sent, and the state is 'offering'.
    expect(sendOffer).not.toHaveBeenCalled()
    expect(machine.state).toBe('offering')

    pc.iceGatheringState = 'complete'
    pc.onicegatheringstatechange?.()
    await flush()

    expect(sendOffer).toHaveBeenCalledWith('fake-offer-sdp')
    expect(machine.state).toBe('offering') // still waiting for the answer

    machine.applyAnswer('fake-answer-sdp')
    await flush()
    expect(pc.setRemoteDescription).toHaveBeenCalledWith({ type: 'answer', sdp: 'fake-answer-sdp' })

    const fakeStream = { addTrack: vi.fn() } as unknown as MediaStream
    pc.ontrack?.({ streams: [fakeStream], track: {} } as unknown as RTCTrackEvent)
    expect(onStream).toHaveBeenCalledWith(fakeStream)

    pc.iceConnectionState = 'connected'
    pc.oniceconnectionstatechange?.()
    expect(machine.state).toBe('connected')
  })

  it('assembles a MediaStream from separate tracks when the browser does not group them under one streams[0] (no shared MSID)', async () => {
    const pc = makeFakePc()
    const onStream = vi.fn()
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc) })
    machine.onStream(onStream)
    machine.start(vi.fn())
    await driveToOfferSent(pc)
    machine.applyAnswer('sdp')
    await flush()

    const videoTrack = {} as MediaStreamTrack
    pc.ontrack?.({ streams: [], track: videoTrack } as unknown as RTCTrackEvent)

    expect(onStream).toHaveBeenCalledTimes(1)
    const assembled = onStream.mock.calls[0][0] as MediaStream
    expect(assembled.getTracks()).toContain(videoTrack)

    const audioTrack = {} as MediaStreamTrack
    pc.ontrack?.({ streams: [], track: audioTrack } as unknown as RTCTrackEvent)
    // Same assembled stream instance, now carrying both tracks.
    expect(onStream.mock.calls[1][0]).toBe(assembled)
    expect(assembled.getTracks()).toEqual(expect.arrayContaining([videoTrack, audioTrack]))
  })

  it('notifies onInputChannelOpen/Close as the data channel readyState events fire', async () => {
    const pc = makeFakePc()
    const onOpen = vi.fn()
    const onClose = vi.fn()
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc) })
    machine.onInputChannelOpen(onOpen)
    machine.onInputChannelClose(onClose)
    machine.start(vi.fn())
    await flush()

    pc.dataChannel.onopen?.()
    expect(onOpen).toHaveBeenCalledTimes(1)
    expect(onClose).not.toHaveBeenCalled()

    pc.dataChannel.onclose?.()
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('start() is idempotent while already offering or connected — a second call never creates a second RTCPeerConnection', async () => {
    const pcFactory = vi.fn(() => asRTCPeerConnection(makeFakePc()))
    const machine = new BrowserWebRTCSession({ pcFactory })
    machine.start(vi.fn())
    await flush()
    machine.start(vi.fn())
    await flush()
    expect(pcFactory).toHaveBeenCalledTimes(1)
  })
})

describe('BrowserWebRTCSession — _beginOffer defensive "at most one PC" guard (HIGH, fix-wave B)', () => {
  it('cleans up any leftover peer connection before creating a fresh one, rather than leaking it', async () => {
    const pcs = [makeFakePc(), makeFakePc()]
    let call = 0
    const pcFactory = () => asRTCPeerConnection(pcs[call++])
    const machine = new BrowserWebRTCSession({ pcFactory })

    machine.start(vi.fn())
    await flush()
    expect(call).toBe(1)
    expect(pcs[0].close).not.toHaveBeenCalled()

    // No known caller in this codebase can currently reach `_beginOffer`
    // with `this.pc` already set (every real trigger — start()'s own state
    // guard, `_fallback`'s `_cleanupPeer` before scheduling a retry — clears
    // it first). This directly exercises the structural guard itself so the
    // invariant ("at most one live RTCPeerConnection at a time") is enforced
    // even against a future caller this fix wave didn't anticipate, per the
    // reviewer finding ("makes 'at most one PC' structural").
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    await (machine as any)._beginOffer()

    expect(pcs[0].close).toHaveBeenCalledTimes(1) // leftover pc cleaned up, not leaked
    expect(call).toBe(2) // a fresh pc was created for the new offer
  })
})

describe('BrowserWebRTCSession — start()/retry-timer race (HIGH, fix-wave B)', () => {
  it('start() clears a pending automatic-retry timer, so a stale retry cannot fire _beginOffer on a healthy reconnected session and create a second RTCPeerConnection', async () => {
    vi.useFakeTimers()
    try {
      const pcs: FakePc[] = []
      const pcFactory = () => {
        const pc = makeFakePc()
        pcs.push(pc)
        return asRTCPeerConnection(pc)
      }
      const onFallback = vi.fn()
      // This is a cold-start (never-connected) negotiation, so
      // firstAnswerTimeoutMs — not answerTimeoutMs — governs it (fix-wave,
      // MED); set both to the same 20ms.
      const machine = new BrowserWebRTCSession({ pcFactory, answerTimeoutMs: 20, firstAnswerTimeoutMs: 20, retryDelayMs: 1000 })
      machine.onFallback(onFallback)

      // First attempt — never answered, falls back on the 20ms answer
      // timeout, arming a one-shot automatic retry for ~1000ms later.
      machine.start(vi.fn())
      await vi.advanceTimersByTimeAsync(0)
      pcs[0].iceGatheringState = 'complete'
      pcs[0].onicegatheringstatechange?.()
      await vi.advanceTimersByTimeAsync(20)

      expect(onFallback).toHaveBeenCalledTimes(1)
      expect(pcs).toHaveLength(1)
      expect(pcs[0].close).toHaveBeenCalledTimes(1)

      // Simulate the real sequence from the reviewer finding: well before the
      // stale retry timer fires, a fresh `available:true` signal arrives and
      // the caller (BrowserLiveView) reconnects by calling start() again.
      await vi.advanceTimersByTimeAsync(100)
      machine.start(vi.fn())
      expect(pcs).toHaveLength(2) // the healthy reconnect's own pc

      // Advance well past where the ORIGINAL stale retry timer (armed ~1000ms
      // after the first fallback, i.e. at the ~1000ms mark, and we're only
      // ~120ms past that fallback so far) would have fired had start() not
      // cleared it.
      await vi.advanceTimersByTimeAsync(1000)

      // Bug (pre-fix): the stale timer fired _beginOffer here, creating a
      // THIRD pc alongside the healthy one from the manual start() above and
      // leaking pc #2 (its close() never called).
      expect(pcs).toHaveLength(2)
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('BrowserWebRTCSession — input data-channel routing', () => {
  it('sendInput sends the given JSON string over the data channel and returns true when it is open', async () => {
    const pc = makeFakePc()
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc) })
    machine.start(vi.fn())
    await flush()
    pc.dataChannel.readyState = 'open'

    const sent = machine.sendInput('{"type":"browser_input","kind":"mouse_move"}')

    expect(sent).toBe(true)
    expect(pc.dataChannel.send).toHaveBeenCalledWith('{"type":"browser_input","kind":"mouse_move"}')
  })

  it('sendInput returns false and does not call send when the data channel is not open', async () => {
    const pc = makeFakePc()
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc) })
    machine.start(vi.fn())
    await flush()
    // readyState defaults to 'connecting' — never opened.

    const sent = machine.sendInput('{"type":"browser_input"}')

    expect(sent).toBe(false)
    expect(pc.dataChannel.send).not.toHaveBeenCalled()
  })

  it('sendInput returns false before start() has ever been called (no channel exists yet)', () => {
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(makeFakePc()) })
    expect(machine.sendInput('{}')).toBe(false)
  })

  it('sendInput returns false (never throws) when the channel is open but send() itself throws', async () => {
    const pc = makeFakePc()
    pc.dataChannel.send = vi.fn(() => {
      throw new Error('channel closed mid-send')
    })
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc) })
    machine.start(vi.fn())
    await flush()
    pc.dataChannel.readyState = 'open'

    expect(machine.sendInput('{}')).toBe(false)
  })
})

describe('BrowserWebRTCSession — fallback triggers', () => {
  it('falls back after answerTimeoutMs with no answer received, and closes the peer connection', async () => {
    const pc = makeFakePc()
    const onFallback = vi.fn()
    const machine = new BrowserWebRTCSession({
      pcFactory: () => asRTCPeerConnection(pc),
      // This is a first-ever (cold-start) negotiation, so firstAnswerTimeoutMs
      // — not answerTimeoutMs — is what actually governs it (fix-wave, MED);
      // set both to the same short value so this test still exercises "falls
      // back after the configured timeout" without caring which field wins.
      answerTimeoutMs: 20,
      firstAnswerTimeoutMs: 20,
      retryDelayMs: 100_000,
    })
    machine.onFallback(onFallback)
    machine.start(vi.fn())
    await driveToOfferSent(pc)

    await wait(60)

    expect(onFallback).toHaveBeenCalledWith('answer-timeout')
    expect(machine.state).toBe('fallback')
    expect(pc.close).toHaveBeenCalled()
  })

  it('does NOT fall back on the answer timeout once the answer has actually been applied', async () => {
    const pc = makeFakePc()
    const onFallback = vi.fn()
    const machine = new BrowserWebRTCSession({
      pcFactory: () => asRTCPeerConnection(pc),
      answerTimeoutMs: 20,
    })
    machine.onFallback(onFallback)
    machine.start(vi.fn())
    await driveToOfferSent(pc)
    machine.applyAnswer('sdp')
    await flush()

    await wait(60)

    expect(onFallback).not.toHaveBeenCalled()
  })

  it('falls back immediately when ICE connection state becomes "failed"', async () => {
    const pc = makeFakePc()
    const onFallback = vi.fn()
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc), retryDelayMs: 100_000 })
    machine.onFallback(onFallback)
    machine.start(vi.fn())
    await driveToOfferSent(pc)
    machine.applyAnswer('sdp')
    await flush()

    pc.iceConnectionState = 'failed'
    pc.oniceconnectionstatechange?.()

    expect(onFallback).toHaveBeenCalledWith('ice-failed')
    expect(machine.state).toBe('fallback')
    expect(pc.close).toHaveBeenCalled()
  })

  it('tolerates a brief ICE "disconnected" state and does not fall back if it recovers within the grace period', async () => {
    const pc = makeFakePc()
    const onFallback = vi.fn()
    const machine = new BrowserWebRTCSession({
      pcFactory: () => asRTCPeerConnection(pc),
      disconnectedGraceMs: 40,
    })
    machine.onFallback(onFallback)
    machine.start(vi.fn())
    await driveToConnected(pc)

    pc.iceConnectionState = 'disconnected'
    pc.oniceconnectionstatechange?.()
    await wait(15) // well within the 40ms grace
    pc.iceConnectionState = 'connected'
    pc.oniceconnectionstatechange?.()

    await wait(60) // past where the original grace timer would have fired

    expect(onFallback).not.toHaveBeenCalled()
    expect(machine.state).toBe('connected')
  })

  it('falls back when ICE stays "disconnected" past the grace period', async () => {
    const pc = makeFakePc()
    const onFallback = vi.fn()
    const machine = new BrowserWebRTCSession({
      pcFactory: () => asRTCPeerConnection(pc),
      disconnectedGraceMs: 20,
      retryDelayMs: 100_000,
    })
    machine.onFallback(onFallback)
    machine.start(vi.fn())
    await driveToConnected(pc)

    pc.iceConnectionState = 'disconnected'
    pc.oniceconnectionstatechange?.()

    await wait(50)

    expect(onFallback).toHaveBeenCalledWith('ice-disconnected-timeout')
    expect(machine.state).toBe('fallback')
  })
})

describe('BrowserWebRTCSession — hasConnectedOnce + extended first-attempt answer timeout (MED, fix-wave — cold-start false positive)', () => {
  it('hasConnectedOnce is false until ICE first reaches "connected", then stays true even through a later fallback', async () => {
    const pc = makeFakePc()
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc) })
    expect(machine.hasConnectedOnce).toBe(false)

    machine.start(vi.fn())
    await driveToOfferSent(pc)
    expect(machine.hasConnectedOnce).toBe(false)

    machine.applyAnswer('sdp')
    await flush()
    pc.iceConnectionState = 'connected'
    pc.oniceconnectionstatechange?.()
    expect(machine.state).toBe('connected')
    expect(machine.hasConnectedOnce).toBe(true)

    // It's a lifetime flag, not a per-attempt one — BrowserLiveView.tsx
    // relies on this to distinguish a genuine degradation of a
    // previously-working stream from a cold start.
    pc.iceConnectionState = 'failed'
    pc.oniceconnectionstatechange?.()
    expect(machine.state).toBe('fallback')
    expect(machine.hasConnectedOnce).toBe(true)
  })

  it('uses the extended firstAnswerTimeoutMs default (30s) — not the regular 5s answerTimeoutMs default — for the very first negotiation attempt', async () => {
    vi.useFakeTimers()
    try {
      const pc = makeFakePc()
      const onFallback = vi.fn()
      const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc) })
      machine.onFallback(onFallback)
      machine.start(vi.fn())
      await vi.advanceTimersByTimeAsync(0)
      pc.iceGatheringState = 'complete'
      pc.onicegatheringstatechange?.()
      await vi.advanceTimersByTimeAsync(0)

      await vi.advanceTimersByTimeAsync(5000) // the regular default -- a cold start must survive this
      expect(onFallback).not.toHaveBeenCalled()

      await vi.advanceTimersByTimeAsync(25000) // now past the 30s cold-start allowance
      expect(onFallback).toHaveBeenCalledWith('answer-timeout')
    } finally {
      vi.useRealTimers()
    }
  })

  it('honors an explicit firstAnswerTimeoutMs override on the first attempt, ignoring answerTimeoutMs entirely', async () => {
    vi.useFakeTimers()
    try {
      const pc = makeFakePc()
      const onFallback = vi.fn()
      const machine = new BrowserWebRTCSession({
        pcFactory: () => asRTCPeerConnection(pc),
        firstAnswerTimeoutMs: 12345,
        answerTimeoutMs: 20, // must NOT govern the first attempt
      })
      machine.onFallback(onFallback)
      machine.start(vi.fn())
      await vi.advanceTimersByTimeAsync(0)
      pc.iceGatheringState = 'complete'
      pc.onicegatheringstatechange?.()
      await vi.advanceTimersByTimeAsync(0)

      await vi.advanceTimersByTimeAsync(12344)
      expect(onFallback).not.toHaveBeenCalled()
      await vi.advanceTimersByTimeAsync(1)
      expect(onFallback).toHaveBeenCalledWith('answer-timeout')
    } finally {
      vi.useRealTimers()
    }
  })

  it('reverts to the regular (short) answerTimeoutMs for a negotiation retry AFTER the session has already connected once', async () => {
    vi.useFakeTimers()
    try {
      const pcs: FakePc[] = []
      const pcFactory = () => {
        const p = makeFakePc()
        pcs.push(p)
        return asRTCPeerConnection(p)
      }
      const onFallback = vi.fn()
      const machine = new BrowserWebRTCSession({ pcFactory, answerTimeoutMs: 500, retryDelayMs: 10 })
      machine.onFallback(onFallback)

      machine.start(vi.fn())
      await vi.advanceTimersByTimeAsync(0)
      pcs[0].iceGatheringState = 'complete'
      pcs[0].onicegatheringstatechange?.()
      await vi.advanceTimersByTimeAsync(0)
      machine.applyAnswer('sdp')
      await vi.advanceTimersByTimeAsync(0)
      pcs[0].iceConnectionState = 'connected'
      pcs[0].oniceconnectionstatechange?.()
      expect(machine.hasConnectedOnce).toBe(true)

      // Force a fallback, which arms the one-shot automatic retry.
      pcs[0].iceConnectionState = 'failed'
      pcs[0].oniceconnectionstatechange?.()
      expect(onFallback).toHaveBeenCalledWith('ice-failed')

      await vi.advanceTimersByTimeAsync(10) // the retry fires, creating pcs[1]
      expect(pcs).toHaveLength(2)
      pcs[1].iceGatheringState = 'complete'
      pcs[1].onicegatheringstatechange?.()
      await vi.advanceTimersByTimeAsync(0)

      // The retry's own answer timeout must be the SHORT 500ms default, not
      // the 30s cold-start allowance -- hasConnectedOnce is already true.
      await vi.advanceTimersByTimeAsync(499)
      expect(onFallback).toHaveBeenCalledTimes(1) // still just the ice-failed one
      await vi.advanceTimersByTimeAsync(1)
      expect(onFallback).toHaveBeenCalledTimes(2)
      expect(onFallback).toHaveBeenLastCalledWith('answer-timeout')
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('BrowserWebRTCSession — single automatic retry', () => {
  it('retries exactly once after a fallback, then stops retrying until start() is called again', async () => {
    const pcs: FakePc[] = []
    const pcFactory = () => {
      const pc = makeFakePc()
      pcs.push(pc)
      return asRTCPeerConnection(pc)
    }
    const onFallback = vi.fn()
    // Well-separated windows so a real-timer check-in falls unambiguously
    // either "after the answer timeout but before the retry" or "after the
    // retry" — answerTimeoutMs=15, retryDelayMs=80 (retry fires at ~95ms
    // after the offer is sent). Both attempts here happen before the machine
    // has ever connected, so firstAnswerTimeoutMs (not answerTimeoutMs) is
    // what actually governs both — set to the same 15ms (fix-wave, MED).
    const machine = new BrowserWebRTCSession({ pcFactory, answerTimeoutMs: 15, firstAnswerTimeoutMs: 15, retryDelayMs: 80 })
    machine.onFallback(onFallback)

    machine.start(vi.fn())
    await driveToOfferSent(pcs[0])
    // Never answered — falls back on the answer timeout (~15ms), well
    // before the ~95ms retry point.
    await wait(45)
    expect(onFallback).toHaveBeenCalledTimes(1)
    expect(pcs).toHaveLength(1)

    // The single automatic retry fires ~80ms after THAT fallback — wait
    // well past it (from t=45ms to t=45+90=135ms, past the ~95ms mark).
    await wait(90)
    expect(pcs).toHaveLength(2)

    // That retry also gets no answer — falls back again on its own 15ms
    // answer timeout...
    await driveToOfferSent(pcs[1])
    await wait(45)
    expect(onFallback).toHaveBeenCalledTimes(2)

    // ...but no THIRD pc is ever automatically created, even well past
    // where another retry would have fired had the budget not been spent.
    await wait(120)
    expect(pcs).toHaveLength(2)
  })

  it('a fresh start() after an explicit stop() re-arms the one-shot retry budget', async () => {
    const pcs: FakePc[] = []
    const pcFactory = () => {
      const pc = makeFakePc()
      pcs.push(pc)
      return asRTCPeerConnection(pc)
    }
    const onFallback = vi.fn()
    // Both start() calls here are cold-start (never-connected) negotiations,
    // so firstAnswerTimeoutMs governs — set to match answerTimeoutMs (15ms)
    // (fix-wave, MED).
    const machine = new BrowserWebRTCSession({ pcFactory, answerTimeoutMs: 15, firstAnswerTimeoutMs: 15, retryDelayMs: 1_000_000 })
    machine.onFallback(onFallback)

    machine.start(vi.fn())
    await driveToOfferSent(pcs[0])
    await wait(40) // exhausts the (very long) retry budget by fallback #1
    expect(onFallback).toHaveBeenCalledTimes(1)
    expect(pcs).toHaveLength(1)

    machine.stop()
    machine.start(vi.fn()) // a fresh re-attach — resets retriedOnce
    expect(pcs).toHaveLength(2)
    await driveToOfferSent(pcs[1])
    await wait(40)
    // The re-armed budget lets this fall back too, on its own account.
    expect(onFallback).toHaveBeenCalledTimes(2)
  })
})

describe('BrowserWebRTCSession — applyState', () => {
  it('triggers a fallback with the frame\'s reason when available:false arrives while offering', async () => {
    const pc = makeFakePc()
    const onFallback = vi.fn()
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc) })
    machine.onFallback(onFallback)
    machine.start(vi.fn())
    await flush()

    machine.applyState({ available: false, reason: 'not_capable' })

    expect(onFallback).toHaveBeenCalledWith('not_capable')
    expect(machine.state).toBe('fallback')
  })

  it('falls back with a generic reason when available:false arrives with no reason field', async () => {
    const pc = makeFakePc()
    const onFallback = vi.fn()
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc) })
    machine.onFallback(onFallback)
    machine.start(vi.fn())
    await flush()

    machine.applyState({ available: false })

    expect(onFallback).toHaveBeenCalledWith('unavailable')
  })

  it('is a no-op when available:true — starting a session on an available signal is the caller\'s job, not applyState\'s', async () => {
    const pc = makeFakePc()
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc) })
    machine.start(vi.fn())
    await driveToConnected(pc)

    machine.applyState({ available: true })

    expect(machine.state).toBe('connected')
    expect(pc.close).not.toHaveBeenCalled()
  })

  it('is a no-op when the machine was never started (idle)', () => {
    const onFallback = vi.fn()
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(makeFakePc()) })
    machine.onFallback(onFallback)

    machine.applyState({ available: false, reason: 'disabled' })

    expect(onFallback).not.toHaveBeenCalled()
    expect(machine.state).toBe('idle')
  })
})

describe('BrowserWebRTCSession — applyState honors active:false after connection (MED, fix-wave B)', () => {
  it('falls back (default reason "stream-stopped") when available:true but active:false arrives after the machine has connected', async () => {
    const pc = makeFakePc()
    const onFallback = vi.fn()
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc) })
    machine.onFallback(onFallback)
    machine.start(vi.fn())
    await driveToConnected(pc)

    machine.applyState({ available: true, active: false })

    expect(onFallback).toHaveBeenCalledWith('stream-stopped')
    expect(machine.state).toBe('fallback')
    expect(pc.close).toHaveBeenCalled()
  })

  it('prefers the frame\'s own reason over the default when active:false arrives with one', async () => {
    const pc = makeFakePc()
    const onFallback = vi.fn()
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc) })
    machine.onFallback(onFallback)
    machine.start(vi.fn())
    await driveToConnected(pc)

    machine.applyState({ available: true, active: false, reason: 'error' })

    expect(onFallback).toHaveBeenCalledWith('error')
  })

  it('is a no-op for active:false while still offering — not yet connected', async () => {
    const pc = makeFakePc()
    const onFallback = vi.fn()
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc), retryDelayMs: 100_000 })
    machine.onFallback(onFallback)
    machine.start(vi.fn())
    await driveToOfferSent(pc)

    machine.applyState({ available: true, active: false })

    expect(onFallback).not.toHaveBeenCalled()
    expect(machine.state).toBe('offering')
  })

  it('stays connected for available:true with active:true, or with active omitted entirely', async () => {
    const pc = makeFakePc()
    const onFallback = vi.fn()
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc) })
    machine.onFallback(onFallback)
    machine.start(vi.fn())
    await driveToConnected(pc)

    machine.applyState({ available: true, active: true })
    machine.applyState({ available: true })

    expect(onFallback).not.toHaveBeenCalled()
    expect(machine.state).toBe('connected')
  })
})

describe('BrowserWebRTCSession — applyState reacts promptly to a reported reason while offering (fix-wave C, "first-offer failure never surfaced" defect)', () => {
  it('falls back IMMEDIATELY — no waiting for the cold-start answer timeout — when a first-offer failure arrives as available:true + reason while still offering', async () => {
    const pc = makeFakePc()
    const onFallback = vi.fn()
    // Deliberately huge cold-start answer timeout AND retry delay: before
    // this fix, `applyState({available:true, reason:'error'})` while
    // `offering` was a complete no-op (neither the available:false branch
    // nor the active===false-while-connected branch matched — the gateway
    // never sends an explicit `active:false` on the wire, only `true` or
    // omitted, per `sendWebRTCState`'s `if active { f.Active = boolPtr(true) }`
    // encoding). Without the fix this assertion would fail immediately
    // (onFallback never called, state stuck at 'offering') rather than
    // merely being slow — proving the reaction is synchronous to the
    // server's report, not gated behind any timer.
    const machine = new BrowserWebRTCSession({
      pcFactory: () => asRTCPeerConnection(pc),
      firstAnswerTimeoutMs: 100_000,
      retryDelayMs: 100_000,
    })
    machine.onFallback(onFallback)
    machine.start(vi.fn())
    await driveToOfferSent(pc)
    expect(machine.state).toBe('offering')

    // Exactly the gateway's real HandleViewerOffer-failure frame shape
    // (available:true, active omitted, reason:"error" —
    // pkg/gateway/browser_webrtc.go:347's
    // `sendWebRTCState(wc, sessID, viewerID, true, false, false, "error")`).
    machine.applyState({ available: true, reason: 'error' })

    expect(onFallback).toHaveBeenCalledWith('error')
    expect(machine.state).toBe('fallback')
    expect(pc.close).toHaveBeenCalled()
  })

  it('is forward-compatible with an unrecognized/future reason value (e.g. a prospective "ingest_timeout") while offering — reacts the same way, passing the reason straight through rather than treating it as a capability gate', async () => {
    const pc = makeFakePc()
    const onFallback = vi.fn()
    const machine = new BrowserWebRTCSession({
      pcFactory: () => asRTCPeerConnection(pc),
      firstAnswerTimeoutMs: 100_000,
      retryDelayMs: 100_000,
    })
    machine.onFallback(onFallback)
    machine.start(vi.fn())
    await driveToOfferSent(pc)

    machine.applyState({ available: true, reason: 'ingest_timeout' })

    expect(onFallback).toHaveBeenCalledWith('ingest_timeout')
    expect(machine.state).toBe('fallback')
  })

  // UAT case 16 — the gateway's free-text cause must reach the panel, not
  // stop at the machine. Before this, applyState read only `reason` and the
  // cause the gateway had already computed was discarded one layer below the
  // component that renders the error.
  it('forwards the gateway-reported cause (reason_detail) to onFallback alongside the reason', async () => {
    const pc = makeFakePc()
    const onFallback = vi.fn()
    const machine = new BrowserWebRTCSession({
      pcFactory: () => asRTCPeerConnection(pc),
      firstAnswerTimeoutMs: 100_000,
      retryDelayMs: 100_000,
    })
    machine.onFallback(onFallback)
    machine.start(vi.fn())
    await driveToOfferSent(pc)

    const cause =
      'capture session: create encoder target: browser: timed out after 20s waiting for the browser to attach the tab (target may be unresponsive)'
    machine.applyState({ available: false, reason: 'error', reason_detail: cause })

    expect(onFallback).toHaveBeenCalledWith('error', cause)
  })

  it('still calls onFallback with the reason ALONE when the gateway sent no cause — the local fallbacks have none to give', async () => {
    const pc = makeFakePc()
    const onFallback = vi.fn()
    const machine = new BrowserWebRTCSession({
      pcFactory: () => asRTCPeerConnection(pc),
      firstAnswerTimeoutMs: 100_000,
      retryDelayMs: 100_000,
    })
    machine.onFallback(onFallback)
    machine.start(vi.fn())
    await driveToOfferSent(pc)

    machine.applyState({ available: false, reason: 'disabled' })

    expect(onFallback).toHaveBeenCalledWith('disabled')
  })

  it('also reacts promptly to a reported reason once already connected (unchanged/still-covered case)', async () => {
    const pc = makeFakePc()
    const onFallback = vi.fn()
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc), retryDelayMs: 100_000 })
    machine.onFallback(onFallback)
    machine.start(vi.fn())
    await driveToConnected(pc)

    machine.applyState({ available: true, reason: 'error' })

    expect(onFallback).toHaveBeenCalledWith('error')
    expect(machine.state).toBe('fallback')
  })

  it('still arms exactly one automatic retry (at the configured retryDelayMs) after an offering-stage reason fallback — the single-retry budget is unaffected by reacting sooner', async () => {
    const pcs: FakePc[] = []
    const pcFactory = () => {
      const pc = makeFakePc()
      pcs.push(pc)
      return asRTCPeerConnection(pc)
    }
    const machine = new BrowserWebRTCSession({ pcFactory, firstAnswerTimeoutMs: 100_000, retryDelayMs: 40 })
    machine.start(vi.fn())
    await driveToOfferSent(pcs[0])

    machine.applyState({ available: true, reason: 'error' })
    expect(pcs).toHaveLength(1) // no new PC yet — the retry timer just armed

    await wait(20)
    expect(pcs).toHaveLength(1) // not yet — well before the 40ms retry delay

    await wait(40) // past the 40ms retry delay (t=60ms total)
    expect(pcs).toHaveLength(2) // exactly one retry fired
  })

  it('the routine post-attach re-announcement (available:true, no reason, active omitted) stays a no-op while offering', async () => {
    const pc = makeFakePc()
    const onFallback = vi.fn()
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc) })
    machine.onFallback(onFallback)
    machine.start(vi.fn())
    await driveToOfferSent(pc)

    machine.applyState({ available: true })

    expect(onFallback).not.toHaveBeenCalled()
    expect(machine.state).toBe('offering')
  })
})

describe('BrowserWebRTCSession — sendOffer failure (MED, fix-wave B)', () => {
  it('falls back immediately with "offer-send-failed" when sendOffer returns false, without waiting for the answer timeout', async () => {
    const pc = makeFakePc()
    const onFallback = vi.fn()
    const machine = new BrowserWebRTCSession({
      pcFactory: () => asRTCPeerConnection(pc),
      // Deliberately huge — if the immediate-fallback path did NOT fire, the
      // fallback would never fire either, within this test's real-timer
      // window, so a false pass here can only mean the immediate path works.
      answerTimeoutMs: 100_000,
    })
    machine.onFallback(onFallback)
    const sendOffer = vi.fn(() => false)
    machine.start(sendOffer)
    await driveToOfferSent(pc)

    expect(sendOffer).toHaveBeenCalledWith('fake-offer-sdp')
    expect(onFallback).toHaveBeenCalledWith('offer-send-failed')
    expect(machine.state).toBe('fallback')
    expect(pc.close).toHaveBeenCalled()
  })

  it('does NOT treat a void/undefined sendOffer return as a failure — only an explicit false, so callers that do not report send success are unaffected', async () => {
    const pc = makeFakePc()
    const onFallback = vi.fn()
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc) })
    machine.onFallback(onFallback)
    machine.start(vi.fn()) // vi.fn() returns undefined, not a boolean
    await driveToOfferSent(pc)

    expect(onFallback).not.toHaveBeenCalled()
    expect(machine.state).toBe('offering') // still waiting on the answer, not fallen back
  })
})

describe('BrowserWebRTCSession — ICE gathering timeout (LOW, fix-wave B)', () => {
  it('proceeds to send the offer with a PARTIAL (non-empty) candidate set if iceGatheringState never reaches complete within iceGatheringTimeoutMs', async () => {
    vi.useFakeTimers()
    try {
      const pc = makeFakePc()
      // A non-empty (but incomplete) candidate set — the "proceed anyway"
      // case, distinct from an EMPTY set (which is refused outright — see
      // the sibling "refuses to ship" test below, SMALL-1 fix).
      pc.createOffer = vi.fn(async () => ({
        type: 'offer',
        sdp: 'fake-offer-sdp\na=candidate:1 1 UDP 2122260223 10.0.0.1 12345 typ host\n',
      }))
      const sendOffer = vi.fn(() => true)
      const machine = new BrowserWebRTCSession({
        pcFactory: () => asRTCPeerConnection(pc),
        iceGatheringTimeoutMs: 50,
      })
      machine.start(sendOffer)
      await vi.advanceTimersByTimeAsync(0) // let createOffer/setLocalDescription settle

      // iceGatheringState is left at its default 'new' — never reaches
      // 'complete' — so only the timeout can unblock this.
      expect(sendOffer).not.toHaveBeenCalled()

      await vi.advanceTimersByTimeAsync(50)

      expect(sendOffer).toHaveBeenCalled()
      expect(machine.state).toBe('offering') // now waiting on the answer timeout instead
    } finally {
      vi.useRealTimers()
    }
  })

  // SMALL-1 fix (external review, 2026-08-13): a prior revision (277cf7b7)
  // titled itself "refuse to ship an empty offer" but only ever logged a
  // console.warn and shipped the offer anyway — the commit message
  // overclaimed what the code did. This proves the genuine refusal.
  it('refuses to ship an offer with ZERO gathered candidates — falls back honestly instead of shipping it', async () => {
    vi.useFakeTimers()
    try {
      const pc = makeFakePc() // default fake offer sdp has no a=candidate: lines at all
      const sendOffer = vi.fn(() => true)
      const onFallback = vi.fn()
      const machine = new BrowserWebRTCSession({
        pcFactory: () => asRTCPeerConnection(pc),
        iceGatheringTimeoutMs: 50,
      })
      machine.onFallback(onFallback)
      machine.start(sendOffer)
      await vi.advanceTimersByTimeAsync(0)

      await vi.advanceTimersByTimeAsync(50)

      // The offer must never go out — an empty, non-trickle offer can only
      // ever fail 30s later with no indication why, so this reports a real,
      // visible reason immediately instead.
      expect(sendOffer).not.toHaveBeenCalled()
      expect(onFallback).toHaveBeenCalledWith('ice-gathering-empty')
      expect(machine.state).toBe('fallback')
      expect(pc.close).toHaveBeenCalled() // the dead PC is torn down, not left dangling
    } finally {
      vi.useRealTimers()
    }
  })

  it('does not fire onFallback if stop() lands before the ICE-gathering timeout', async () => {
    vi.useFakeTimers()
    try {
      const pc = makeFakePc()
      const sendOffer = vi.fn(() => true)
      const onFallback = vi.fn()
      const machine = new BrowserWebRTCSession({
        pcFactory: () => asRTCPeerConnection(pc),
        iceGatheringTimeoutMs: 50,
      })
      machine.onFallback(onFallback)
      machine.start(sendOffer)
      await vi.advanceTimersByTimeAsync(0)
      machine.stop()
      await vi.advanceTimersByTimeAsync(80)
      expect(onFallback).not.toHaveBeenCalled()
      expect(sendOffer).not.toHaveBeenCalled()
      expect(machine.state).toBe('idle')
    } finally {
      vi.useRealTimers()
    }
  })

  it('still resolves via the real ICE-gathering-complete event (not the timeout) when gathering finishes first', async () => {
    vi.useFakeTimers()
    try {
      const pc = makeFakePc()
      const sendOffer = vi.fn(() => true)
      const machine = new BrowserWebRTCSession({
        pcFactory: () => asRTCPeerConnection(pc),
        iceGatheringTimeoutMs: 3000,
      })
      machine.start(sendOffer)
      await vi.advanceTimersByTimeAsync(0)

      pc.iceGatheringState = 'complete'
      pc.onicegatheringstatechange?.()
      await vi.advanceTimersByTimeAsync(0)

      expect(sendOffer).toHaveBeenCalledWith('fake-offer-sdp')
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('BrowserWebRTCSession — applyAnswer defensive no-ops', () => {
  it('does not throw and stays idle when called before start()', () => {
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(makeFakePc()) })
    expect(() => machine.applyAnswer('sdp')).not.toThrow()
    expect(machine.state).toBe('idle')
  })
})

describe('BrowserWebRTCSession — stop()', () => {
  it('closes the peer connection and data channel, and cancels a pending automatic retry', async () => {
    const pc = makeFakePc()
    const onFallback = vi.fn()
    const machine = new BrowserWebRTCSession({
      pcFactory: () => asRTCPeerConnection(pc),
      // Cold-start (never-connected) negotiation — firstAnswerTimeoutMs
      // governs, so set it to match answerTimeoutMs (fix-wave, MED).
      answerTimeoutMs: 10,
      firstAnswerTimeoutMs: 10,
      retryDelayMs: 20,
    })
    machine.onFallback(onFallback)
    machine.start(vi.fn())
    await driveToOfferSent(pc)

    await wait(30) // triggers the answer-timeout fallback + schedules a retry
    expect(onFallback).toHaveBeenCalledTimes(1)
    expect(pc.close).toHaveBeenCalledTimes(1)

    machine.stop()

    // The retry that WOULD have fired ~20ms after the fallback must not —
    // stop() cancels it.
    await wait(60)
    expect(onFallback).toHaveBeenCalledTimes(1)
    expect(machine.state).toBe('idle')
  })

  it('closes an active (connected) peer connection and data channel', async () => {
    const pc = makeFakePc()
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc) })
    machine.start(vi.fn())
    await driveToConnected(pc)

    machine.stop()

    expect(pc.close).toHaveBeenCalledTimes(1)
    expect(pc.dataChannel.close).toHaveBeenCalledTimes(1)
    expect(machine.state).toBe('idle')
  })

  it('is safe to call when never started (idle → idle no-op)', () => {
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(makeFakePc()) })
    expect(() => machine.stop()).not.toThrow()
    expect(machine.state).toBe('idle')
  })
})

// Regression: the 2026-07-30 Batam/VPN UAT. The retry budget used to be a
// one-shot boolean (`retriedOnce`), so after a SINGLE failed re-offer the
// panel was stranded in the JPEG ("picture mode") sink for the rest of the
// session — which is exactly what the operator hit: the stream degraded and
// never came back. The budget is now a counter with exponential backoff that
// resets on every successful connect.
describe('BrowserWebRTCSession — retry budget (2026-07-30 UAT: permanent picture-mode stranding)', () => {
  // Drives the pc at `idx` to "offer sent", then lets its answer time out —
  // i.e. fails exactly one negotiation attempt.
  async function failAttempt(pcs: FakePc[], idx: number): Promise<void> {
    await vi.advanceTimersByTimeAsync(0)
    pcs[idx].iceGatheringState = 'complete'
    pcs[idx].onicegatheringstatechange?.()
    await vi.advanceTimersByTimeAsync(20)
  }

  it('retries repeatedly with exponential backoff rather than giving up after one re-offer', async () => {
    vi.useFakeTimers()
    try {
      const pcs: FakePc[] = []
      const pcFactory = () => {
        const pc = makeFakePc()
        pcs.push(pc)
        return asRTCPeerConnection(pc)
      }
      const machine = new BrowserWebRTCSession({
        pcFactory,
        answerTimeoutMs: 20,
        firstAnswerTimeoutMs: 20,
        retryDelayMs: 1000,
        maxRetries: 3,
      })
      machine.start(vi.fn())
      await failAttempt(pcs, 0)
      expect(pcs).toHaveLength(1)

      // Retry 1 at retryDelayMs * 2^0.
      await vi.advanceTimersByTimeAsync(1000)
      expect(pcs).toHaveLength(2)
      await failAttempt(pcs, 1)

      // Retry 2 at retryDelayMs * 2^1. PRE-FIX THIS NEVER HAPPENED — the
      // one-shot flag had already been consumed, so the session stayed in
      // 'fallback' forever.
      await vi.advanceTimersByTimeAsync(2000)
      expect(pcs).toHaveLength(3)
      await failAttempt(pcs, 2)

      // Retry 3 at retryDelayMs * 2^2 — the last one maxRetries allows.
      await vi.advanceTimersByTimeAsync(4000)
      expect(pcs).toHaveLength(4)
      await failAttempt(pcs, 3)

      // Budget exhausted: no further attempt, however long we wait.
      await vi.advanceTimersByTimeAsync(120_000)
      expect(pcs).toHaveLength(4)
    } finally {
      vi.useRealTimers()
    }
  })

  it('resets the retry budget after a successful connect, so a later blip gets a full set of attempts', async () => {
    vi.useFakeTimers()
    try {
      const pcs: FakePc[] = []
      const pcFactory = () => {
        const pc = makeFakePc()
        pcs.push(pc)
        return asRTCPeerConnection(pc)
      }
      const machine = new BrowserWebRTCSession({
        pcFactory,
        answerTimeoutMs: 20,
        firstAnswerTimeoutMs: 20,
        retryDelayMs: 1000,
        maxRetries: 1,
      })
      machine.start(vi.fn())

      // Attempt 1 FAILS, consuming the single retry the budget allows.
      await failAttempt(pcs, 0)
      await vi.advanceTimersByTimeAsync(1000)
      expect(pcs).toHaveLength(2)

      // Attempt 2 CONNECTS — this must clear the consumed count.
      await vi.advanceTimersByTimeAsync(0)
      pcs[1].iceGatheringState = 'complete'
      pcs[1].onicegatheringstatechange?.()
      machine.applyAnswer('sdp')
      await vi.advanceTimersByTimeAsync(0)
      pcs[1].iceConnectionState = 'connected'
      pcs[1].oniceconnectionstatechange?.()
      expect(machine.state).toBe('connected')

      // Now the working connection dies. Because the success above reset the
      // budget, this must buy a FRESH retry. Pre-fix — and with a
      // non-resetting counter — the budget was already spent by attempt 1 and
      // this blip would strand the panel in the JPEG sink permanently.
      pcs[1].iceConnectionState = 'failed'
      pcs[1].oniceconnectionstatechange?.()
      expect(machine.state).toBe('fallback')

      await vi.advanceTimersByTimeAsync(1000)
      expect(pcs).toHaveLength(3)
    } finally {
      vi.useRealTimers()
    }
  })
})

// ADR-062 tier 3. A hard-coded public STUN server can only help a client that
// is able to hole-punch; the client this tier exists for cannot. The gateway
// mints short-lived TURN credentials per viewer and sends them on the same
// frame that says "you may offer".
describe('gateway-supplied ICE servers', () => {
  it('keeps STUN and appends the gateway relay with its credentials', () => {
    const seen: RTCConfiguration[] = []
    const OriginalPC = globalThis.RTCPeerConnection
    // @ts-expect-error - jsdom has no RTCPeerConnection; record the config instead.
    globalThis.RTCPeerConnection = function (cfg: RTCConfiguration) {
      seen.push(cfg)
      return {} as RTCPeerConnection
    }
    try {
      pcFactoryWithICEServers([
        { urls: ['turn:203.0.113.9:30000?transport=udp'], username: '1750:viewer-1', credential: 'secret' },
      ])()
    } finally {
      globalThis.RTCPeerConnection = OriginalPC
    }

    expect(seen).toHaveLength(1)
    const servers = seen[0].iceServers ?? []
    expect(servers.length).toBe(2)
    expect(JSON.stringify(servers[0])).toContain('stun:')
    expect(servers[1]).toMatchObject({
      urls: ['turn:203.0.113.9:30000?transport=udp'],
      username: '1750:viewer-1',
      credential: 'secret',
    })
  })

  it('drops entries with no urls rather than handing Chrome an empty server', () => {
    const seen: RTCConfiguration[] = []
    const OriginalPC = globalThis.RTCPeerConnection
    // @ts-expect-error - see above.
    globalThis.RTCPeerConnection = function (cfg: RTCConfiguration) {
      seen.push(cfg)
      return {} as RTCPeerConnection
    }
    try {
      pcFactoryWithICEServers([{ urls: [] }])()
    } finally {
      globalThis.RTCPeerConnection = OriginalPC
    }
    expect((seen[0].iceServers ?? []).length).toBe(1)
  })

  it('never replaces a test-injected pcFactory — a gateway frame must not turn a fake PC into a real one', async () => {
    const pc = makeFakePc()
    const machine = new BrowserWebRTCSession({ pcFactory: () => asRTCPeerConnection(pc) })
    machine.setICEServers([{ urls: ['turn:203.0.113.9:30000'], username: 'u', credential: 'c' }])
    const sendOffer = vi.fn(() => true)
    machine.start(sendOffer)
    await vi.waitFor(() => expect(pc.createOffer).toHaveBeenCalled())
    machine.stop()
  })
})
