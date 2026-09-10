import type { BrowserInputFrame, BrowserInputOfferFrame, BrowserInputAnswerFrame, BrowserInputStateFrame, BrowserInputControlAckFrame } from '@/lib/api/generated/asyncapi-types'

type Input = Omit<BrowserInputFrame, 'type'>
export type BrowserInputOffer = Pick<BrowserInputOfferFrame, 'sdp' | 'offer_id' | 'input_epoch' | 'control_epoch'>
export type BrowserInputControl = Pick<BrowserInputFrame, 'input_epoch' | 'control_epoch'>
export type BrowserInputState = 'idle' | 'connecting' | 'ready' | 'paused' | 'failed'

interface Options { // not-wire-format: local dependency injection and lifecycle callbacks; never serialized to the gateway
  pcFactory?: (config: RTCConfiguration) => RTCPeerConnection
  sendOffer: (offer: BrowserInputOffer) => boolean
  onState: (state: BrowserInputState, reason?: string) => void
}

/** Data-only peer owned by one socket attachment. Failed actions are never replayed. */
export class BrowserInputWebRTCSession {
  private pc: RTCPeerConnection | null = null
  private reliable: RTCDataChannel | null = null
  private hover: RTCDataChannel | null = null
  private iceServers: RTCIceServer[] = []
  private epoch = 0
  private control = 0
  private acknowledgedControl = 0
  private offeredControl = 0
  private reliableSequence = 0
  private hoverSequence = 0
  private barrier = 0
  private held = new Set<string>()
  private answered = false
  private applyingAnswer = false
  private wanted = false
  private timer: ReturnType<typeof setTimeout> | null = null
  private cancelGather: (() => void) | null = null
  private currentState: BrowserInputState = 'idle'

  constructor(private readonly options: Options) {}
  get awaitingControl(): boolean { return this.control !== this.acknowledgedControl }
  resetAttachment(): void { this.stop(); this.epoch = this.control = this.acknowledgedControl = 0 }
  get state(): BrowserInputState { return this.currentState }
  setICEServers(servers: RTCIceServer[]): void { this.iceServers = servers }

  start(): void {
    this.wanted = true
    if (this.pc || this.control !== this.acknowledgedControl) return
    this.cleanup()
    if (!Number.isSafeInteger(this.epoch + 1)) { this.fail('Input connection identity exhausted.'); return }
    this.epoch++
    this.offeredControl = this.control
    this.reliableSequence = this.hoverSequence = this.barrier = 0
    this.held.clear()
    this.answered = this.applyingAnswer = false
    this.change('connecting')
    this.armTimeout('Input connection timed out. Retry input.')
    void this.offer()
  }

  private async offer(): Promise<void> {
    let attemptedPeer: RTCPeerConnection | null = null
    const epoch = this.epoch
    try {
      const pc = (this.options.pcFactory ?? ((config) => new RTCPeerConnection(config)))({ iceServers: this.iceServers })
      attemptedPeer = pc
      this.pc = pc
      this.reliable = pc.createDataChannel('input-reliable', { ordered: true })
      this.hover = pc.createDataChannel('input-hover', { ordered: false, maxRetransmits: 0 })
      for (const channel of [this.reliable, this.hover]) {
        channel.onopen = () => { if (this.pc === pc) this.updateReady() }
        channel.onclose = () => { if (this.pc === pc) this.fail('Input connection closed. Retry input.') }
        channel.onerror = () => { if (this.pc === pc) this.fail('Input connection failed. Retry input.') }
      }
      pc.onconnectionstatechange = () => {
        if (this.pc === pc && ['failed', 'closed', 'disconnected'].includes(pc.connectionState)) this.fail('Input connection lost. Retry input.')
      }
      const offer = await pc.createOffer()
      if (this.pc !== pc) return
      await pc.setLocalDescription(offer)
      if (this.pc !== pc) return
      if (pc.iceGatheringState !== 'complete') await new Promise<void>((resolve) => {
        const done = () => { pc.removeEventListener('icegatheringstatechange', changed); this.cancelGather = null; resolve() }
        const changed = () => { if (pc.iceGatheringState === 'complete') done() }
        this.cancelGather = done
        pc.addEventListener('icegatheringstatechange', changed)
        changed()
      })
      if (this.pc !== pc) return
      if (!pc.localDescription?.sdp || !this.options.sendOffer({ sdp: pc.localDescription.sdp, offer_id: this.epoch, input_epoch: this.epoch, control_epoch: this.offeredControl })) this.fail('Input offer was not sent. Retry input.')
    } catch {
      if (this.epoch !== epoch || (attemptedPeer !== null && this.pc !== attemptedPeer)) return
      this.fail('Could not negotiate the input connection. Retry input.')
    }
  }

  applyAnswer(frame: BrowserInputAnswerFrame): void {
    const pc = this.pc
    if (!pc || this.answered || this.applyingAnswer || frame.input_epoch !== this.epoch || frame.offer_id !== this.epoch || frame.control_epoch !== this.offeredControl) return
    this.applyingAnswer = true
    void Promise.resolve().then(() => {
      if (this.pc !== pc) return
      return pc.setRemoteDescription({ type: 'answer', sdp: frame.sdp })
    }).then(() => {
      if (this.pc !== pc) return
      this.answered = true
      this.updateReady()
    }).catch(() => { if (this.pc === pc) this.fail('Input answer was rejected. Retry input.') })
  }

  applyState(frame: BrowserInputStateFrame): void {
    if (!this.pc || frame.input_epoch !== this.epoch || frame.offer_id !== this.epoch) return
    if (frame.state === 'failed' || frame.state === 'closed') this.fail(frame.reason || 'Input connection unavailable. Retry input.')
  }

  beginControl(): number | null {
    if (!Number.isSafeInteger(this.control + 1)) { this.fail('Input control identity exhausted.'); return null }
    this.control++
    this.reliableSequence = this.hoverSequence = this.barrier = 0
    if (this.pc && !this.answered) this.cleanup()
    this.held.clear() // Server retires and releases the preceding control epoch.
    this.change('paused')
    this.armTimeout('Browser control acknowledgment timed out. Retry input.')
    return this.control
  }

  get controlIdentity(): BrowserInputControl { return { input_epoch: this.epoch, control_epoch: this.control } }

  applyControlAck(frame: BrowserInputControlAckFrame): boolean {
    if (frame.input_epoch !== this.epoch || frame.control_epoch !== this.control || this.control === this.acknowledgedControl) return false
    if (!frame.ok) { this.fail(frame.reason || 'Browser control failed. Retry input.'); return true }
    this.acknowledgedControl = this.control
    this.clearTimer()
    if (!this.pc && this.wanted) this.start()
    else this.updateReady()
    return true
  }

  sendInput(input: Input): boolean {
    if (this.currentState !== 'ready' || !this.pc) return false
    const transition = ['mouse_down', 'mouse_up', 'key_down', 'key_up'].includes(input.kind)
    const hover = input.kind === 'mouse_move' && this.held.size === 0 && (input.modifiers ?? 0) === 0
    const channel = hover ? this.hover : this.reliable
    if (!channel || channel.readyState !== 'open') { this.fail('Input channel is not open. Retry input.'); return false }
    if (channel.bufferedAmount >= 64 * 1024) {
      if (hover) return true // Lossy positions may be discarded; never enqueue a stale backlog.
      this.fail('Input connection is congested. Retry input.'); return false
    }
    const sequence = (hover ? this.hoverSequence : this.reliableSequence) + 1
    const barrier = this.barrier + (transition ? 1 : 0)
    if (!Number.isSafeInteger(sequence) || !Number.isSafeInteger(barrier)) { this.fail('Input sequence exhausted. Retry input.'); return false }
    const frame: BrowserInputFrame = {
      ...input, type: 'browser_input', ...this.controlIdentity,
      gesture_barrier: barrier,
      ...(hover ? { hover_seq: sequence } : { reliable_seq: sequence }),
    }
    try { channel.send(JSON.stringify(frame)) } catch { this.fail('Input was not sent. Retry input.'); return false }
    this.barrier = barrier
    if (hover) this.hoverSequence = sequence
    else this.reliableSequence = sequence
    const key = input.kind.startsWith('mouse_') ? `button:${input.button}` : `key:${input.code || input.key}`
    if (input.kind === 'mouse_down' || input.kind === 'key_down') this.held.add(key)
    if (input.kind === 'mouse_up' || input.kind === 'key_up') this.held.delete(key)
    return true
  }

  fail(reason: string): void { this.cleanup(); this.change('failed', reason) }
  stop(): void { this.wanted = false; this.cleanup(); this.change('idle') }
  private updateReady(): void {
    if (!this.pc || !this.answered || this.reliable?.readyState !== 'open' || this.hover?.readyState !== 'open') return
    if (this.control !== this.acknowledgedControl) return
    this.clearTimer()
    this.change('ready')
  }
  private change(state: BrowserInputState, reason?: string): void { this.currentState = state; this.options.onState(state, reason) }
  private clearTimer(): void { if (this.timer !== null) clearTimeout(this.timer); this.timer = null }
  private armTimeout(reason: string): void { this.clearTimer(); this.timer = setTimeout(() => this.fail(reason), 30_000) }
  private cleanup(): void {
    const pc = this.pc
    this.pc = null
    this.cancelGather?.()
    this.clearTimer()
    this.reliable?.close(); this.hover?.close(); pc?.close()
    this.reliable = this.hover = null
    this.held.clear()
  }
}
