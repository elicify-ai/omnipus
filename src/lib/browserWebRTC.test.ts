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
import { BrowserWebRTCSession } from './browserWebRTC'

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
      const machine = new BrowserWebRTCSession({ pcFactory, answerTimeoutMs: 20, retryDelayMs: 1000 })
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
      answerTimeoutMs: 20,
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
    // after the offer is sent).
    const machine = new BrowserWebRTCSession({ pcFactory, answerTimeoutMs: 15, retryDelayMs: 80 })
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
    const machine = new BrowserWebRTCSession({ pcFactory, answerTimeoutMs: 15, retryDelayMs: 1_000_000 })
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
  it('proceeds to send the offer with whatever candidates gathered if iceGatheringState never reaches complete within iceGatheringTimeoutMs', async () => {
    vi.useFakeTimers()
    try {
      const pc = makeFakePc()
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

      expect(sendOffer).toHaveBeenCalledWith('fake-offer-sdp')
      expect(machine.state).toBe('offering') // now waiting on the answer timeout instead
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
      answerTimeoutMs: 10,
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
