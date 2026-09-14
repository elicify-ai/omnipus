import { browserInputProtocol, decodeBrowserInput } from './browserInputCodec'
import { describe, it, expect, vi } from 'vitest'
import { BrowserInputWebRTCSession } from './browserInputWebRTC'

function setup(onFailure?: () => boolean) {
  const channels: Record<string, { readyState: string; bufferedAmount: number; send: ReturnType<typeof vi.fn>; close: ReturnType<typeof vi.fn>; onopen?: () => void; onclose?: () => void }> = {}
  const pc = {
    iceGatheringState: 'complete', connectionState: 'new', localDescription: { sdp: 'offer-sdp' },
    createDataChannel: vi.fn((label: string) => channels[label] = { readyState: 'open', bufferedAmount: 0, send: vi.fn(), close: vi.fn() }),
    createOffer: vi.fn(async () => ({ type: 'offer', sdp: 'offer-sdp' })),
    setLocalDescription: vi.fn(async () => {}), setRemoteDescription: vi.fn(async () => {}),
    close: vi.fn(), onconnectionstatechange: null as (() => void) | null,
  }
  const offer = vi.fn(() => true)
  const changed = vi.fn()
  const machine = new BrowserInputWebRTCSession({ pcFactory: () => pc as unknown as RTCPeerConnection, sendOffer: offer, onState: changed, onFailure })
  async function connect() {
    machine.start()
    await vi.waitFor(() => expect(offer).toHaveBeenCalledTimes(1))
    machine.applyAnswer({ type: 'browser_input_answer', session_id: 'session', offer_id: 1, input_epoch: 1, control_epoch: 0, sdp: 'answer-sdp' })
    await vi.waitFor(() => expect(machine.state).toBe('ready'))
  }
  const input = { kind: 'mouse_move' as const, x: 12, y: 24, modifiers: 0, capture_id: 'a'.repeat(64), capture_generation: 1 }
  const sent = (label: string) => channels[label].send.mock.calls.map(([data]) => decodeBrowserInput(data as Uint8Array))
  return { machine, pc, channels, offer, changed, connect, input, sent }
}

describe('dedicated input contract', () => {
  it('negotiates only the two specified data channels and applies an exact answer', async () => {
    const s = setup(); await s.connect()
    expect(s.pc.createDataChannel.mock.calls).toEqual([['input-reliable', { ordered: true, protocol: browserInputProtocol }], ['input-hover', { ordered: false, maxRetransmits: 0, protocol: browserInputProtocol }]])
    expect(s.offer.mock.calls).toEqual([[{ sdp: 'offer-sdp', offer_id: 1, input_epoch: 1, control_epoch: 0 }]])
    expect(s.pc.setRemoteDescription.mock.calls).toEqual([[{ type: 'answer', sdp: 'answer-sdp' }]])
    s.machine.stop()
  })
  it('sends hover separately, but keeps full drag and held-key movement reliable with exact barriers', async () => {
    const s = setup(); await s.connect()
    for (const input of [s.input, { ...s.input, kind: 'mouse_down' as const, button: 'left' as const }, s.input, { ...s.input, kind: 'mouse_up' as const, button: 'left' as const }, s.input]) expect(s.machine.sendInput(input)).toBe(true)
    expect(s.sent('input-hover').map(f => [f.hover_seq, f.gesture_barrier])).toEqual([[1, 0], [2, 2]])
    expect(s.sent('input-reliable').map(f => [f.kind, f.reliable_seq, f.gesture_barrier])).toEqual([['mouse_down', 1, 1], ['mouse_move', 2, 1], ['mouse_up', 3, 2]])
    s.machine.sendInput({ ...s.input, kind: 'key_down', key: 'ArrowLeft', code: 'ArrowLeft' })
    s.machine.sendInput(s.input)
    expect(s.sent('input-reliable').slice(-2).map(f => [f.kind, f.gesture_barrier])).toEqual([['key_down', 3], ['mouse_move', 3]])
    expect(s.sent('input-hover')).toHaveLength(2)
    s.machine.stop()
  })
  it('pauses synchronously for control, rejects stale/future acknowledgments and resumes only the matching epoch', async () => {
    const s = setup(); await s.connect()
    expect(s.machine.beginControl()).toBe(1)
    expect(s.machine.sendInput(s.input)).toBe(false)
    for (const [input_epoch, control_epoch] of [[0, 1], [1, 0], [1, 2]]) s.machine.applyControlAck({ type: 'browser_input_control_ack', session_id: 'session', input_epoch, control_epoch, ok: true })
    expect(s.machine.sendInput(s.input)).toBe(false)
    s.machine.applyControlAck({ type: 'browser_input_control_ack', session_id: 'session', input_epoch: 1, control_epoch: 1, ok: true })
    expect(s.machine.sendInput(s.input)).toBe(true)
    expect(s.sent('input-hover')[0].control_epoch).toBe(1)
    s.machine.stop()
  })
  it('rejects retired answers and keeps a failed control visibly paused', async () => {
    const s = setup(); s.machine.start()
    await vi.waitFor(() => expect(s.offer).toHaveBeenCalledTimes(1))
    s.machine.applyAnswer({ type: 'browser_input_answer', session_id: 'session', input_epoch: 0, offer_id: 1, control_epoch: 0, sdp: 'old' })
    expect(s.pc.setRemoteDescription).not.toHaveBeenCalled()
    s.machine.applyAnswer({ type: 'browser_input_answer', session_id: 'session', input_epoch: 1, offer_id: 1, control_epoch: 0, sdp: 'answer' })
    await vi.waitFor(() => expect(s.machine.state).toBe('ready'))
    s.machine.beginControl()
    s.machine.applyControlAck({ type: 'browser_input_control_ack', session_id: 'session', input_epoch: 1, control_epoch: 1, ok: false, reason: 'control rejected' })
    expect(s.machine.awaitingControl).toBe(true)
    expect(s.machine.state).toBe('failed')
    expect(s.machine.sendInput(s.input)).toBe(false)
    expect(s.pc.close).toHaveBeenCalledTimes(1)
  })
  it('fails reliable backpressure without replay and retires the peer', async () => {
    const s = setup(); await s.connect()
    s.channels['input-reliable'].bufferedAmount = 65536
    expect(s.machine.sendInput({ ...s.input, kind: 'text', text: '你好' })).toBe(false)
    expect(s.machine.state).toBe('failed')
    expect(s.sent('input-reliable')).toEqual([])
    expect(s.pc.close).toHaveBeenCalledTimes(1)
  })
})

it('ignores an old offer rejection after a fresh peer starts', async () => {
  const s = setup()
  let rejectOld!: (error: Error) => void
  s.pc.createOffer.mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectOld = reject }))
  s.machine.start()
  s.machine.stop()
  s.machine.start()
  await vi.waitFor(() => expect(s.offer).toHaveBeenCalledTimes(1))
  rejectOld(new Error('retired transport'))
  await Promise.resolve(); await Promise.resolve()
  expect(s.machine.state).toBe('connecting')
  expect(s.offer.mock.calls[0]).toEqual([{ sdp: 'offer-sdp', offer_id: 2, input_epoch: 2, control_epoch: 0 }])
  s.machine.stop()
})

it('times out missing control acknowledgment and exposes need for a fresh attachment', async () => {
  const s = setup(); await s.connect()
  vi.useFakeTimers()
  try {
    s.machine.beginControl()
    vi.advanceTimersByTime(30_000)
    expect(s.machine.state).toBe('failed')
    expect(s.machine.awaitingControl).toBe(true)
    expect(s.machine.sendInput(s.input)).toBe(false)
    s.machine.resetAttachment()
    expect(s.machine.awaitingControl).toBe(false)
    expect(s.machine.controlIdentity).toEqual({ input_epoch: 0, control_epoch: 0 })
  } finally { s.machine.stop(); vi.useRealTimers() }
})

it('requires both channels open and exact current peer before enabling input', async () => {
  const s = setup(); s.machine.start()
  await vi.waitFor(() => expect(s.offer).toHaveBeenCalledTimes(1))
  s.channels['input-hover'].readyState = 'connecting'
  s.machine.applyAnswer({ type: 'browser_input_answer', session_id: 'session', input_epoch: 1, offer_id: 1, control_epoch: 0, sdp: 'answer' })
  await Promise.resolve(); await Promise.resolve()
  expect(s.machine.sendInput(s.input)).toBe(false)
  s.channels['input-hover'].readyState = 'open'; s.channels['input-hover'].onopen?.()
  await vi.waitFor(() => expect(s.machine.state).toBe('ready'))
  expect(s.machine.sendInput(s.input)).toBe(true)
  s.channels['input-reliable'].onclose?.()
  expect(s.machine.state).toBe('failed')
  expect(s.machine.sendInput(s.input)).toBe(false)
})

it('keeps hover pressure lossy while preserving reliable wheel and Unicode text exactly', async () => {
  const s = setup(); await s.connect()
  s.channels['input-hover'].bufferedAmount = 65536
  expect(s.machine.sendInput(s.input)).toBe(true)
  expect(s.sent('input-hover')).toEqual([])
  s.machine.sendInput({ ...s.input, kind: 'wheel', delta_x: -5, delta_y: 120 })
  s.machine.sendInput({ ...s.input, kind: 'text', text: '你好🙂' })
  expect(s.sent('input-reliable').map(f => [f.kind, f.delta_x, f.delta_y, f.text])).toEqual([['wheel', -5, 120, undefined], ['text', undefined, undefined, '你好🙂']])
  s.machine.stop()
})

it('resets control-scoped counters so an overtaken old packet cannot create a new sequence gap', async () => {
  const s = setup(); await s.connect()
  s.machine.sendInput(s.input)
  s.machine.sendInput({ ...s.input, kind: 'mouse_down', button: 'left' })
  s.machine.sendInput({ ...s.input, kind: 'mouse_move' }) // May be overtaken by the WS control before server acceptance.
  s.machine.beginControl()
  s.machine.applyControlAck({ type: 'browser_input_control_ack', session_id: 'session', input_epoch: 1, control_epoch: 1, ok: true })
  s.machine.sendInput(s.input)
  s.machine.sendInput({ ...s.input, kind: 'key_down', key: 'ArrowLeft', code: 'ArrowLeft' })
  expect(s.sent('input-hover').at(-1)).toMatchObject({ input_epoch: 1, control_epoch: 1, hover_seq: 1, gesture_barrier: 0 })
  expect(s.sent('input-reliable').at(-1)).toMatchObject({ input_epoch: 1, control_epoch: 1, reliable_seq: 1, gesture_barrier: 1 })
  s.machine.stop()
})

it('surfaces fatal state for the same peer even when a newer control overtook its notification', async () => {
  const s = setup(); await s.connect()
  s.machine.beginControl()
  s.machine.applyState({ type: 'browser_input_state', session_id: 'session', input_epoch: 0, offer_id: 1, control_epoch: 0, state: 'failed', reason: 'old peer' })
  expect(s.machine.state).toBe('paused')
  s.machine.applyState({ type: 'browser_input_state', session_id: 'session', input_epoch: 1, offer_id: 1, control_epoch: 0, state: 'failed', reason: 'peer transport closed' })
  expect(s.machine.state).toBe('failed')
  expect(s.pc.close).toHaveBeenCalledTimes(1)
})

// Control-ordering contract: pause immediately, use an identity the socket has
// actually seen, and recover without retiring the separate media connection.
it.each(['createOffer', 'setLocalDescription', 'gather'] as const)('acknowledges control during unsignaled %s and negotiates a fresh attempt', async (stage) => {
  const s = setup()
  let release!: () => void
  const pending = new Promise<void>((resolve) => { release = resolve })
  if (stage === 'createOffer') s.pc.createOffer.mockImplementationOnce(async () => { await pending; return { type: 'offer', sdp: 'retired' } })
  if (stage === 'setLocalDescription') s.pc.setLocalDescription.mockImplementationOnce(() => pending)
  if (stage === 'gather') {
    s.pc.iceGatheringState = 'gathering'
    Object.assign(s.pc, { addEventListener: vi.fn(), removeEventListener: vi.fn() })
  }
  s.machine.start()
  await Promise.resolve(); await Promise.resolve()
  expect(s.offer).not.toHaveBeenCalled()
  expect(s.machine.beginControl()).toBe(1)
  expect(s.machine.controlIdentity).toEqual({ input_epoch: 0, control_epoch: 1 })
  expect(s.machine.applyControlAck({ type: 'browser_input_control_ack', session_id: 'session', input_epoch: 1, control_epoch: 1, ok: true })).toBe(false)
  s.pc.iceGatheringState = 'complete'
  expect(s.machine.applyControlAck({ type: 'browser_input_control_ack', session_id: 'session', input_epoch: 0, control_epoch: 1, ok: true })).toBe(true)
  release()
  await vi.waitFor(() => expect(s.offer.mock.calls).toEqual([[{ sdp: 'offer-sdp', offer_id: 2, input_epoch: 2, control_epoch: 1 }]]))
  s.machine.applyAnswer({ type: 'browser_input_answer', session_id: 'session', offer_id: 1, input_epoch: 1, control_epoch: 0, sdp: 'retired' })
  expect(s.pc.setRemoteDescription).not.toHaveBeenCalled()
  s.machine.applyAnswer({ type: 'browser_input_answer', session_id: 'session', offer_id: 2, input_epoch: 2, control_epoch: 1, sdp: 'fresh' })
  await vi.waitFor(() => expect(s.machine.state).toBe('ready'))
  s.machine.stop()
})

it.each(['answer-first', 'ack-first'] as const)('preserves a signaled pending input peer when control arrives: %s', async (order) => {
  const s = setup(); s.machine.start()
  await vi.waitFor(() => expect(s.offer).toHaveBeenCalledTimes(1))
  s.machine.beginControl()
  expect(s.machine.controlIdentity).toEqual({ input_epoch: 1, control_epoch: 1 })
  expect(s.pc.close).not.toHaveBeenCalled()
  const answer = () => s.machine.applyAnswer({ type: 'browser_input_answer', session_id: 'session', offer_id: 1, input_epoch: 1, control_epoch: 0, sdp: 'answer' })
  const ack = () => expect(s.machine.applyControlAck({ type: 'browser_input_control_ack', session_id: 'session', input_epoch: 1, control_epoch: 1, ok: true })).toBe(true)
  if (order === 'answer-first') { answer(); await Promise.resolve(); await Promise.resolve(); expect(s.machine.sendInput(s.input)).toBe(false); ack() }
  else { ack(); expect(s.machine.sendInput(s.input)).toBe(false); answer() }
  await vi.waitFor(() => expect(s.machine.state).toBe('ready'))
  expect(s.offer).toHaveBeenCalledTimes(1)
  expect(s.machine.sendInput(s.input)).toBe(true)
  expect(s.sent('input-hover')[0]).toMatchObject({ input_epoch: 1, control_epoch: 1, hover_seq: 1 })
  s.machine.stop()
})


describe('failed input retirement over the ordered control socket', () => {
  it('retires once even without local held keys, stays failed after ack, and waits for explicit Retry', async () => {
    const retired = vi.fn(() => { s.machine.beginControl(); return true })
    const s = setup(retired); await s.connect()
    s.machine.fail('local input closed')
    expect(retired).toHaveBeenCalledTimes(1)
    expect(s.machine.state).toBe('failed')
    expect(s.machine.awaitingRetirement).toBe(true)
    expect(s.machine.needsAttachmentRetry).toBe(false)
    s.machine.fail('duplicate closure')
    expect(retired).toHaveBeenCalledTimes(1)
    s.machine.applyControlAck({ type: 'browser_input_control_ack', session_id: 'session', input_epoch: 1, control_epoch: 1, ok: true })
    expect(s.machine.state).toBe('failed')
    expect(s.machine.awaitingRetirement).toBe(false)
    expect(s.offer).toHaveBeenCalledTimes(1)
    s.machine.start()
    await vi.waitFor(() => expect(s.offer).toHaveBeenCalledTimes(2))
    expect(s.offer.mock.calls.at(-1)).toEqual([{ sdp: 'offer-sdp', offer_id: 2, input_epoch: 2, control_epoch: 1 }])
    s.machine.stop()
  })
  it('queues explicit Retry behind only the matching retirement acknowledgment', async () => {
    const s = setup(() => { s.machine.beginControl(); return true }); await s.connect()
    s.machine.fail('local input closed'); s.machine.start()
    expect(s.machine.state).toBe('failed'); expect(s.offer).toHaveBeenCalledTimes(1)
    expect(s.machine.applyControlAck({ type: 'browser_input_control_ack', session_id: 'session', input_epoch: 0, control_epoch: 1, ok: true })).toBe(false)
    expect(s.offer).toHaveBeenCalledTimes(1)
    expect(s.machine.applyControlAck({ type: 'browser_input_control_ack', session_id: 'session', input_epoch: 1, control_epoch: 1, ok: true })).toBe(true)
    await vi.waitFor(() => expect(s.offer).toHaveBeenCalledTimes(2))
    s.machine.stop()
  })
  it.each(['send-failed', 'ack-failed', 'timeout'] as const)('requires a fresh attachment after retirement %s without repeated release', async (failure) => {
    const retired = vi.fn(() => { if (failure !== 'send-failed') s.machine.beginControl(); return failure !== 'send-failed' })
    const s = setup(retired); await s.connect(); vi.useFakeTimers()
    try {
      s.machine.fail('local input closed')
      if (failure === 'ack-failed') s.machine.applyControlAck({ type: 'browser_input_control_ack', session_id: 'session', input_epoch: 1, control_epoch: 1, ok: false })
      if (failure === 'timeout') vi.advanceTimersByTime(30000)
      expect(s.machine.awaitingRetirement).toBe(false)
      expect(s.machine.needsAttachmentRetry).toBe(true)
      expect(s.machine.state).toBe('failed')
      s.machine.start(); await Promise.resolve()
      expect(s.offer).toHaveBeenCalledTimes(1); expect(retired).toHaveBeenCalledTimes(1)
      s.machine.resetAttachment()
      expect(s.machine.needsAttachmentRetry).toBe(false)
      expect(s.machine.controlIdentity).toEqual({ input_epoch: 0, control_epoch: 0 })
    } finally { s.machine.stop(); vi.useRealTimers() }
  })
  it('does not retire an offer that never reached the server', async () => {
    const retired = vi.fn(() => true), s = setup(retired)
    s.pc.createOffer.mockImplementationOnce(() => new Promise(() => {}))
    s.machine.start(); s.machine.fail('local setup failed')
    expect(retired).not.toHaveBeenCalled()
    expect(s.machine.needsAttachmentRetry).toBe(false)
    s.machine.stop()
  })
})


it('ignores transport failure while ordered retirement awaits its own acknowledgment', async () => {
  const s = setup(() => { s.machine.beginControl(); return true }); await s.connect()
  s.machine.fail('input closed'); s.machine.start()
  const refusal = { type: 'browser_input_state' as const, session_id: 'session', input_epoch: 1, offer_id: 1, control_epoch: 1, state: 'failed' as const, reason: 'Input retirement refused' }
  for (const old of [{ input_epoch: 0 }, { offer_id: 0 }, { control_epoch: 0 }, { input_epoch: 2, offer_id: 2 }]) {
    s.machine.applyState({ ...refusal, ...old })
    expect(s.machine.awaitingRetirement).toBe(true)
    expect(s.machine.needsAttachmentRetry).toBe(false)
  }
  s.machine.applyState(refusal)
  expect(s.machine.awaitingRetirement).toBe(true)
  expect(s.machine.needsAttachmentRetry).toBe(false)
  expect(s.machine.state).toBe('failed')
  expect(s.offer).toHaveBeenCalledTimes(1)
  expect(s.machine.applyControlAck({ type: 'browser_input_control_ack', session_id: 'session', input_epoch: 1, control_epoch: 1, ok: true })).toBe(true)
  await vi.waitFor(() => expect(s.offer).toHaveBeenCalledTimes(2))
  s.machine.stop()
})

// Independent v1 wire oracle: OBI/version 1/text kind, presence mask,
// UTF-8 length + "a", followed by input/control/sequence/barrier doubles.
it('sends versioned binary bytes on the actual reliable transport', async () => {
  const s = setup(); await s.connect()
  expect(s.machine.sendInput({ kind: 'text', text: 'a' })).toBe(true)
  const packet = s.channels['input-reliable'].send.mock.calls[0][0]
  expect(packet).toBeInstanceOf(Uint8Array)
  expect(Array.from(packet as Uint8Array)).toEqual([
    79,66,73,1,7,0,132,11,0,1,0,97,
    0,0,0,0,0,0,240,63, 0,0,0,0,0,0,0,0,
    0,0,0,0,0,0,240,63, 0,0,0,0,0,0,0,0,
  ])
  s.machine.stop()
})


describe('recoverable queue pressure', () => {
  it('preserves the healthy peer, drains control, and requires an explicit fresh resume', async () => {
    const release = vi.fn(() => { s.machine.beginControl(); return true })
    const s = setup(release); await s.connect()
    s.machine.sendInput({ ...s.input, kind: 'key_down', key: 'ArrowLeft', code: 'ArrowLeft' })
    s.machine.applyState({ type: 'browser_input_state', session_id: 'session', input_epoch: 1, offer_id: 1, control_epoch: 0, state: 'failed', reason: 'reliable input queue expired' })
    expect(s.machine.state).toBe('paused')
    expect(s.changed).toHaveBeenLastCalledWith('paused', 'Browser fell behind. Input paused.')
    expect(s.pc.close).not.toHaveBeenCalled()
    expect(release).toHaveBeenCalledTimes(1)
    expect(s.machine.sendInput(s.input)).toBe(false)
    s.machine.resume() // Resume is unavailable while release has not been acknowledged.
    s.machine.applyControlAck({ type: 'browser_input_control_ack', session_id: 'session', input_epoch: 1, control_epoch: 1, ok: true })
    expect(s.machine.state).toBe('paused')
    expect(s.machine.sendInput(s.input)).toBe(false)
    s.machine.start()
    expect(s.machine.state).toBe('paused')
    s.machine.resume()
    expect(s.machine.state).toBe('ready')
    expect(s.machine.sendInput({ ...s.input, kind: 'mouse_down', button: 'left' })).toBe(true)
    expect(s.sent('input-reliable').map(f => [f.kind, f.control_epoch, f.reliable_seq])).toEqual([['key_down', 0, 1], ['mouse_down', 1, 1]])
    s.machine.applyState({ type: 'browser_input_state', session_id: 'session', input_epoch: 1, offer_id: 1, control_epoch: 0, state: 'failed', reason: 'reliable input queue expired' })
    expect(s.machine.state).toBe('ready')
    expect(release).toHaveBeenCalledTimes(1)
    s.machine.stop()
  })
  it.each([0, 1])('retains the backend cause at control %i after local closure without treating it as a control refusal', async (control) => {
    const s = setup(() => { s.machine.beginControl(); return true }); await s.connect()
    s.channels['input-reliable'].onclose?.()
    expect(s.machine.state).toBe('failed')
    const server = { type: 'browser_input_state' as const, session_id: 'session', input_epoch: 1, offer_id: 1, control_epoch: control, state: 'failed' as const, reason: 'Input channels did not become ready. Retry input.' }
    s.machine.applyState({ ...server, input_epoch: 0, offer_id: 0 })
    expect(s.changed).toHaveBeenLastCalledWith('failed', 'Input connection closed. Retry input.')
    s.machine.applyState(server)
    expect(s.changed).toHaveBeenLastCalledWith('failed', server.reason)
    expect(s.machine.awaitingRetirement).toBe(true)
    s.machine.applyControlAck({ type: 'browser_input_control_ack', session_id: 'session', input_epoch: 1, control_epoch: 1, ok: true })
    expect(s.machine.needsAttachmentRetry).toBe(false)
    expect(s.machine.state).toBe('failed')
    expect(s.pc.close).toHaveBeenCalledTimes(1)
    s.machine.stop()
  })
})


it('uses full Retry when queue expiry arrives after the native channel has already closed', async () => {
  const s=setup(()=>{s.machine.beginControl();return true});await s.connect()
  s.channels['input-reliable'].readyState='closed'
  s.machine.applyState({type:'browser_input_state',session_id:'session',input_epoch:1,offer_id:1,control_epoch:0,state:'failed',reason:'reliable input queue expired'})
  expect(s.machine.state).toBe('failed')
  expect(s.pc.close).toHaveBeenCalledTimes(1)
  expect(s.machine.sendInput(s.input)).toBe(false)
  s.machine.stop()
})
