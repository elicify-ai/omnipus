import type { ServerFrame } from '@/lib/api/generated/asyncapi-types'

type AttachRequest = { sessionId: string; generation: number; started: boolean } // not-wire-format: ordered socket request ownership
type PendingAttach = { frames: ServerFrame[]; outstanding: number; latestGeneration: number; bytes: number; progressive: boolean } // not-wire-format: private replay retention

type ReplayBufferOptions = { maxBytes?: number; maxFrames?: number; onLimit?: (sessionId: string) => void } // not-wire-format: local allocation policy

// Bound serialized UTF-16 retention and object count, not total browser heap.
export const MAX_REPLAY_BUFFER_BYTES = 8 * 1024 * 1024
const MAX_REPLAY_BUFFER_FRAMES = 4096
const supersededAttachFrames = new WeakSet<object>()
const progressiveAttachFrames = new WeakSet<object>()

/** Local delivery provenance, not a serialized field or a successful page claim. */
export function isProgressiveAttachFrame(frame: unknown): boolean {
  return typeof frame === 'object' && frame !== null && progressiveAttachFrames.has(frame)
}

/** Transport-local provenance: never serialized or added to generated frames. */
export function isSupersededAttachFrame(frame: unknown): boolean {
  return typeof frame === 'object' && frame !== null && supersededAttachFrames.has(frame)
}

function markSuperseded(frame: ServerFrame): void {
  if (frame.type === 'session_snapshot' || frame.type === 'session_state' || frame.type === 'catch_up_complete') supersededAttachFrames.add(frame)
}

function isAttachRefusal(frame: ServerFrame): boolean {
  return frame.type === 'error' && frame.seq === undefined && frame.client_message_id === undefined && frame.payload === undefined && frame.first_message_error === undefined
}

/** Each wire attach owns one ordered response generation. History stays outside
 * the view until completion, while older generations cannot acknowledge a newer
 * foreground open. Oversized history switches visibly to bounded progressive
 * delivery instead of retaining raw payloads without limit. */
export class AttachReplayBuffer {
  private readonly pending = new Map<string, PendingAttach>()
  private readonly requests: AttachRequest[] = []
  private endedOnError: AttachRequest | null = null
  private generation = 0
  private retainedBytes = 0
  private retainedFrames = 0

  constructor(private readonly options: ReplayBufferOptions = {}) {}

  get bufferedBytes(): number { return this.retainedBytes }

  begin(sessionId: string, queuedFrames: readonly ServerFrame[] = []): void {
    // A completed answer may already have left pending but still await a paint.
    // Its controls must not consume a later foreground open's acknowledgement.
    for (const frame of queuedFrames) {
      if ('session_id' in frame && frame.session_id === sessionId) markSuperseded(frame)
    }
    const generation = ++this.generation
    this.requests.push({ sessionId, generation, started: false })
    const replay = this.pending.get(sessionId)
    if (replay) {
      replay.outstanding++
      replay.latestGeneration = generation
      for (const frame of replay.frames) markSuperseded(frame)
    } else {
      this.pending.set(sessionId, { frames: [], outstanding: 1, latestGeneration: generation, bytes: 0, progressive: false })
    }
  }

  clear(): void {
    this.pending.clear()
    this.requests.length = 0
    this.endedOnError = null
    this.retainedBytes = 0
    this.retainedFrames = 0
  }

  private release(replay: PendingAttach): ServerFrame[] {
    const frames = replay.frames
    this.retainedBytes -= replay.bytes
    this.retainedFrames -= frames.length
    replay.frames = []
    replay.bytes = 0
    return frames
  }

  private retain(sessionId: string, replay: PendingAttach, frame: ServerFrame): ServerFrame[] {
    if (replay.progressive) { progressiveAttachFrames.add(frame); return [frame] }
    const bytes = JSON.stringify(frame).length * 2
    if (this.retainedBytes + bytes > (this.options.maxBytes ?? MAX_REPLAY_BUFFER_BYTES) || this.retainedFrames + 1 > (this.options.maxFrames ?? MAX_REPLAY_BUFFER_FRAMES)) {
      replay.progressive = true
      const ready = this.release(replay)
      for (const released of [...ready, frame]) progressiveAttachFrames.add(released)
      this.options.onLimit?.(sessionId)
      return [...ready, frame]
    }
    replay.frames.push(frame)
    replay.bytes += bytes
    this.retainedBytes += bytes
    this.retainedFrames++
    return []
  }

  private discardFailedHistory(replay: PendingAttach): void {
    const errors = this.release(replay).filter(frame => frame.type === 'error')
    replay.frames = errors
    replay.bytes = errors.reduce((bytes, frame) => bytes + JSON.stringify(frame).length * 2, 0)
    this.retainedBytes += replay.bytes
    this.retainedFrames += errors.length
  }

  accept(frame: ServerFrame): ServerFrame[] {
    const explicitSessionId = 'session_id' in frame ? frame.session_id
      : frame.type === 'ask_user_question' ? frame.card.session_id : undefined
    const request = this.requests[0]
    const refusal = isAttachRefusal(frame)
    const sessionId = explicitSessionId ?? (refusal && !request?.started ? request?.sessionId : undefined)
    const replay = sessionId ? this.pending.get(sessionId) : undefined
    const failed = frame.type === 'done' && frame.stats?.replay_error === true
    if (failed && sessionId && this.endedOnError?.sessionId === sessionId) {
      // A pre-snapshot read failure emits error + failure done; retire it once.
      this.endedOnError = null
      return replay ? this.retain(sessionId, replay, frame) : [frame]
    }
    if (!replay || !sessionId) return [frame]

    const ownsGeneration = request?.sessionId === sessionId
    if (ownsGeneration && (frame.type === 'session_snapshot' || frame.type === 'session_state')) {
      request.started = true
      this.endedOnError = null
    }
    // A held live error released after A1's boundary is not A2's refusal.
    // Only the serialized head can end; sequenced/message/provider errors cannot.
    const refused = ownsGeneration && !request.started && refusal
    const delivered = refused && !explicitSessionId ? { ...frame, session_id: sessionId } : frame
    if (ownsGeneration && request.generation !== replay.latestGeneration) markSuperseded(delivered)
    if (ownsGeneration && failed) this.discardFailedHistory(replay)
    const ready = this.retain(sessionId, replay, delivered)
    if (!ownsGeneration || (frame.type !== 'catch_up_complete' && !refused && !failed)) return ready

    this.requests.shift()
    if (refused) this.endedOnError = request
    if (--replay.outstanding > 0) return ready
    this.pending.delete(sessionId)
    return [...ready, ...this.release(replay)]
  }
}
