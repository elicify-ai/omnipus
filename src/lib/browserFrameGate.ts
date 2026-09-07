export type BrowserFrameGateState =
  | { status: 'locked'; reason: string }
  | { status: 'presenting'; generation: number; displayAt: number }
  | { status: 'ready'; generation: number }
  | { status: 'needs-fresh-viewer'; generation: number }

export interface BrowserPresentedFrame {
  rtpTimestamp?: number
  expectedDisplayTime?: number
}

/**
 * Proves a frame belongs to the current target/layout before enabling input.
 * Stream tokens must be actual peer-owned media stream identities. A fallback
 * authorization must come from a server-confirmed fresh peer for this boundary.
 * RTP serial order is only unambiguous within half the uint32 space; the server
 * must refresh attach markers rather than reuse a capture's hours-old marker.
 */
export class BrowserFrameGate {
  private generation: number | null = null
  private marker: number | null = null
  private stream: object | null = null
  private seenStreams = new WeakSet<object>()
  private freshViewer = false
  private latestFrame: BrowserPresentedFrame | null = null
  private displayAt: number | null = null

  expectGeneration(generation: number): void {
    if (!Number.isSafeInteger(generation) || generation <= 0) {
      throw new RangeError('generation must be a positive safe integer')
    }
    if (this.generation !== null && generation <= this.generation) return
    this.generation = generation
    this.marker = null
    this.displayAt = null
    this.freshViewer = false
  }

  acceptBoundary(boundary: { generation: number; rtpTimestamp: number }): void {
    if (!isRtpTimestamp(boundary.rtpTimestamp)) {
      throw new RangeError('rtpTimestamp must be a uint32')
    }
    this.expectGeneration(boundary.generation)
    if (boundary.generation !== this.generation || this.marker === boundary.rtpTimestamp) return
    this.marker = boundary.rtpTimestamp
    this.displayAt = null
    this.freshViewer = false
    this.considerFrame()
  }

  /** A lost feed cannot authorize input until recovery commits a new boundary. */
  suspend(): void {
    this.marker = null
    this.latestFrame = null
    this.displayAt = null
    this.freshViewer = false
  }

  bindStream(stream: object | null): void {
    if (stream === this.stream) return
    this.stream = stream
    if (stream) this.seenStreams.add(stream)
    this.latestFrame = null
    this.displayAt = null
    this.freshViewer = false
  }

  /** Call only after a matching server generation authorizes a new peer. */
  bindFreshViewer(stream: object, generation: number): boolean {
    if (generation !== this.generation || this.marker === null || this.seenStreams.has(stream)) return false
    this.bindStream(stream)
    this.freshViewer = true
    return true
  }

  observeFrame(stream: object, metadata: BrowserPresentedFrame, now: number): BrowserFrameGateState {
    if (stream === this.stream) {
      this.latestFrame = { ...metadata }
      this.considerFrame()
    }
    return this.read(now)
  }

  read(now: number): BrowserFrameGateState {
    if (!Number.isFinite(now) || now < 0) throw new RangeError('now must be finite and nonnegative')
    if (this.generation === null) return { status: 'locked', reason: 'generation-required' }
    if (this.stream === null) return { status: 'locked', reason: 'stream-required' }
    if (this.marker === null) return { status: 'locked', reason: 'boundary-required' }
    if (this.displayAt !== null) {
      return now >= this.displayAt
        ? { status: 'ready', generation: this.generation }
        : { status: 'presenting', generation: this.generation, displayAt: this.displayAt }
    }
    if (this.latestFrame === null) return { status: 'locked', reason: 'frame-required' }
    if (!validDisplayTime(this.latestFrame.expectedDisplayTime)) {
      return { status: 'locked', reason: 'presentation-time-unavailable' }
    }
    if (!isRtpTimestamp(this.latestFrame.rtpTimestamp) && !this.freshViewer) {
      return { status: 'needs-fresh-viewer', generation: this.generation }
    }
    return { status: 'locked', reason: 'frame-required' }
  }

  private considerFrame(): void {
    const frame = this.latestFrame
    if (this.displayAt !== null || this.marker === null || !frame || !validDisplayTime(frame.expectedDisplayTime)) return
    const matches = isRtpTimestamp(frame.rtpTimestamp)
      ? ((frame.rtpTimestamp - this.marker) >>> 0) < 0x80000000
      : this.freshViewer
    if (matches) this.displayAt = frame.expectedDisplayTime
  }
}

function isRtpTimestamp(value: number | undefined): value is number {
  return value !== undefined && Number.isInteger(value) && value >= 0 && value <= 0xffffffff
}

function validDisplayTime(value: number | undefined): value is number {
  // Zero/missing timestamps are not presentation proof; never substitute now.
  return value !== undefined && Number.isFinite(value) && value > 0
}
