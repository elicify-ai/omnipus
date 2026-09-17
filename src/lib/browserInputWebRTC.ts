import { browserInputProtocol, encodeBrowserInput } from './browserInputCodec'
import { iceServersWithDefaults } from './browserWebRTC'
import type { BrowserInputFrame, BrowserInputOfferFrame, BrowserInputAnswerFrame, BrowserInputStateFrame, BrowserInputControlAckFrame } from '@/lib/api/generated/asyncapi-types'

type Input = Omit<BrowserInputFrame, 'type'>
export type BrowserInputOffer = Pick<BrowserInputOfferFrame, 'sdp' | 'offer_id' | 'input_epoch' | 'control_epoch'>
export type BrowserInputControl = Pick<BrowserInputFrame, 'input_epoch' | 'control_epoch'>
export type BrowserInputState = 'idle' | 'connecting' | 'ready' | 'paused' | 'failed'

interface Options { // not-wire-format: local dependency injection and lifecycle callbacks; never serialized to the gateway
  pcFactory?: (config: RTCConfiguration) => RTCPeerConnection
  sendOffer: (offer: BrowserInputOffer) => boolean
  onState: (state: BrowserInputState, reason?: string) => void
  onFailure?: () => boolean
  automaticRecoveryIdentity?: (ack?: BrowserInputControlAckFrame) => string | null
  onAutomaticRecovery?: () => void
}

/** Data-only peer owned by one socket attachment. Failed actions are never replayed. */
export class BrowserInputWebRTCSession {
  private pc: RTCPeerConnection | null = null
  private reliable: RTCDataChannel | null = null
  private hover: RTCDataChannel | null = null
  private iceServers: RTCIceServer[] = iceServersWithDefaults([])
  private epoch = 0
  private signaledEpoch = 0
  private retiredEpoch = 0
  private retirementControl: number | null = null
  private retirementRejected = false
  private control = 0
  private acknowledgedControl = 0
  private offeredControl = 0
  private reliableSequence = 0
  private hoverSequence = 0
  private barrier = 0
  private held = new Set<string>()
  private answered = false
  private applyingAnswer = false
  private pressurePaused = false
  private automaticRecoveryUsed = false
  private automaticIdentity: string | null = null
  private automaticControl: number | null = null
  private failedControl: number | null = null
  private failedReleaseControl: number | null = null
  private wanted = false
  private timer: ReturnType<typeof setTimeout> | null = null
  private cancelGather: (() => void) | null = null
  private currentState: BrowserInputState = 'idle'

  constructor(private readonly options: Options) {}
  get awaitingControl(): boolean { return this.control !== this.acknowledgedControl }
  get awaitingRetirement(): boolean { return this.retirementControl === this.control && this.awaitingControl }
  get needsAttachmentRetry(): boolean { return this.retirementRejected || (this.awaitingControl && !this.awaitingRetirement) }
  resetAttachment(): void {
    this.stop()
    this.epoch = this.signaledEpoch = this.retiredEpoch = this.control = this.acknowledgedControl = 0
    this.retirementControl = null
    this.retirementRejected = false
  }
  get state(): BrowserInputState { return this.currentState }
  setICEServers(servers: RTCIceServer[]): void { this.iceServers = iceServersWithDefaults(servers) }

  start(): void {
    if (this.pressurePaused) return
    this.wanted = true
    if (this.pc || this.retirementRejected || this.control !== this.acknowledgedControl) return
    this.cleanup()
    if (!Number.isSafeInteger(this.epoch + 1)) { this.fail('Input connection identity exhausted.'); return }
    this.epoch++
    this.automaticRecoveryUsed = false
    this.cancelAutomaticRecovery()
    this.offeredControl = this.control
    this.reliableSequence = this.hoverSequence = this.barrier = 0
    this.held.clear()
    this.answered = this.applyingAnswer = false
    this.change('connecting')
    this.armTimeout('Input connection timed out. Retry input.')
    void this.offer()
  }

  cancelAutomaticRecovery(): void {
    this.automaticIdentity = null
    this.automaticControl = null
  }

  resume(): void {
    if (!this.pressurePaused || this.awaitingControl) return
    this.pressurePaused = false
    this.cancelAutomaticRecovery()
    this.wanted = true
    this.updateReady()
  }

  private async offer(): Promise<void> {
    let attemptedPeer: RTCPeerConnection | null = null
    const epoch = this.epoch
    try {
      const pc = (this.options.pcFactory ?? ((config) => new RTCPeerConnection(config)))({ iceServers: this.iceServers })
      attemptedPeer = pc
      this.pc = pc
      this.reliable = pc.createDataChannel('input-reliable', { ordered: true, protocol: browserInputProtocol })
      this.hover = pc.createDataChannel('input-hover', { ordered: false, maxRetransmits: 0, protocol: browserInputProtocol })
      for (const channel of [this.reliable, this.hover]) {
        channel.onopen = () => { if (this.pc === pc) this.updateReady() }
        channel.onclose = () => { if (this.pc === pc) this.fail('Input connection closed. Retry input.') }
        channel.onerror = () => { if (this.pc === pc) this.fail('Input connection failed. Retry input.') }
      }
      pc.onconnectionstatechange = () => {
        if (this.pc === pc && ['failed', 'closed', 'disconnected'].includes(pc.connectionState)) this.fail('Input connection lost. Retry input.')
      }
      const offer = await pc.createOffer()
      if (this.pc !== pc || this.epoch !== epoch) return
      await pc.setLocalDescription(offer)
      if (this.pc !== pc || this.epoch !== epoch) return
      if (pc.iceGatheringState !== 'complete') await new Promise<void>((resolve) => {
        const done = () => { pc.removeEventListener('icegatheringstatechange', changed); this.cancelGather = null; resolve() }
        const changed = () => { if (pc.iceGatheringState === 'complete') done() }
        this.cancelGather = done
        pc.addEventListener('icegatheringstatechange', changed)
        changed()
      })
      if (this.pc !== pc || this.epoch !== epoch) return
      if (!pc.localDescription?.sdp || !this.options.sendOffer({ sdp: pc.localDescription.sdp, offer_id: epoch, input_epoch: epoch, control_epoch: this.offeredControl })) {
        this.fail('Input offer was not sent. Retry input.')
        return
      }
      this.signaledEpoch = epoch
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
    if (frame.input_epoch !== this.epoch || frame.offer_id !== this.epoch) return
    if (frame.state !== 'failed' && frame.state !== 'closed') return
    // A native channel can close before its server cause arrives. Update only
    // presentation for that retired control; the release ACK remains authoritative.
    if (!this.pc) {
      if (this.currentState === 'failed' && (frame.control_epoch === this.failedControl || (frame.control_epoch === this.failedReleaseControl)) && frame.reason) this.change('failed', frame.reason)
      return
    }
    if (frame.control_epoch > this.control) return
    if (frame.reason === 'reliable input queue expired') {
      if (frame.control_epoch !== this.control || this.pressurePaused) return
      if (this.reliable?.readyState !== 'open' || this.hover?.readyState !== 'open' || ['closed', 'failed', 'disconnected'].includes(this.pc.connectionState)) {
        this.fail('Browser fell behind and the input connection closed. Retry input.')
        return
      }
      // Snapshot before paused-state callbacks clear local held bookkeeping.
      this.automaticIdentity = !this.automaticRecoveryUsed && this.held.size === 0 ? this.options.automaticRecoveryIdentity?.() ?? null : null
      this.automaticControl = this.automaticIdentity ? this.control + 1 : null
      this.pressurePaused = true
      this.wanted = false
      this.held.clear()
      this.change('paused', 'Browser fell behind. Input paused.')
      const previousControl = this.control
      if (!this.options.onFailure?.() || this.control <= previousControl || !this.awaitingControl) this.fail('Could not pause browser input safely. Retry input.')
      return
    }
    this.fail(frame.reason || 'Input connection unavailable. Retry input.')
  }

  beginControl(): number | null {
    if (!Number.isSafeInteger(this.control + 1)) { this.fail('Input control identity exhausted.'); return null }
    this.control++
    if (this.automaticControl !== this.control) this.cancelAutomaticRecovery()
    this.reliableSequence = this.hoverSequence = this.barrier = 0
    // A local attempt has no server identity until its offer is on the socket.
    // Keep signaled peers alive: the original answer still completes their SDP.
    if (this.pc && this.signaledEpoch !== this.epoch) this.cleanup()
    this.held.clear() // Server retires and releases the preceding control epoch.
    if (this.currentState !== 'failed') this.change('paused', this.pressurePaused ? 'Browser fell behind. Input paused.' : undefined)
    this.armTimeout('Browser control acknowledgment timed out. Retry input.')
    return this.control
  }

  get controlIdentity(): BrowserInputControl { return { input_epoch: this.signaledEpoch, control_epoch: this.control } }

  applyControlAck(frame: BrowserInputControlAckFrame): boolean {
    if (frame.input_epoch !== this.signaledEpoch || frame.control_epoch !== this.control || this.control === this.acknowledgedControl) return false
    if (!frame.ok) {
      if (this.retirementControl !== null) this.retirementRejected = true
      this.retirementControl = null
      this.fail(frame.reason || 'Browser control failed. Retry input.')
      return true
    }
    this.retirementControl = null
    this.retirementRejected = false
    this.acknowledgedControl = this.control
    this.clearTimer()
    if (!this.pc && this.wanted) this.start()
    else if (this.pc && this.pressurePaused) {
      const eligible = !this.automaticRecoveryUsed && this.automaticControl === this.control && this.automaticIdentity !== null
        && this.options.automaticRecoveryIdentity?.(frame) === this.automaticIdentity
        && this.pc.connectionState === 'connected' && this.reliable?.readyState === 'open' && this.hover?.readyState === 'open'
      this.cancelAutomaticRecovery()
      if (eligible) {
        this.automaticRecoveryUsed = true
        this.resume()
        this.options.onAutomaticRecovery?.()
      } else this.change('paused', 'Browser fell behind. Input paused.')
    }
    else if (this.pc) {
      this.armTimeout('Input connection timed out. Retry input.')
      this.updateReady()
    }
    return true
  }

  sendInput(input: Input): boolean {
    if (this.pressurePaused) this.cancelAutomaticRecovery()
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
    try { channel.send(encodeBrowserInput(frame)) } catch { this.fail('Input was not sent. Retry input.'); return false }
    this.barrier = barrier
    if (hover) this.hoverSequence = sequence
    else this.reliableSequence = sequence
    const key = input.kind.startsWith('mouse_') ? `button:${input.button}` : `key:${input.code || input.key}`
    if (input.kind === 'mouse_down' || input.kind === 'key_down') this.held.add(key)
    if (input.kind === 'mouse_up' || input.kind === 'key_up') this.held.delete(key)
    return true
  }

  fail(reason: string): void {
    if (this.currentState === 'failed' && !this.pc && this.awaitingRetirement) return
    this.cancelAutomaticRecovery()
    this.failedControl = this.control
    this.failedReleaseControl = null
    this.pressurePaused = false
    this.wanted = false
    this.cleanup()
    this.change('failed', reason)
    if (this.signaledEpoch > this.retiredEpoch && this.options.onFailure) {
      // Retire server ownership even when local held-key bookkeeping is empty.
      // Mark before calling out: a failed send must not recurse or send twice.
      this.retiredEpoch = this.signaledEpoch
      const previousControl = this.control
      const sent = this.options.onFailure()
      if (sent && this.control > previousControl && this.awaitingControl) this.failedReleaseControl = this.retirementControl = this.control
      else this.retirementRejected = true
    }
  }
  stop(): void { this.cancelAutomaticRecovery(); this.pressurePaused = false; this.failedControl = this.failedReleaseControl = null; this.wanted = false; this.cleanup(); this.change('idle') }
  private updateReady(): void {
    if (this.pressurePaused || !this.pc || !this.answered || this.reliable?.readyState !== 'open' || this.hover?.readyState !== 'open') return
    if (this.control !== this.acknowledgedControl) return
    this.clearTimer()
    this.change('ready')
  }
  private change(state: BrowserInputState, reason?: string): void { this.currentState = state; this.options.onState(state, reason) }
  private clearTimer(): void { if (this.timer !== null) clearTimeout(this.timer); this.timer = null }
  private armTimeout(reason: string): void {
    this.clearTimer()
    this.timer = setTimeout(() => {
      if (this.retirementControl !== null) this.retirementRejected = true
      this.retirementControl = null
      this.fail(reason)
    }, 30_000)
  }
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
