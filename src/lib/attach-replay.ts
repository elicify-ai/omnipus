import type { ServerFrame } from '@/lib/api/generated/asyncapi-types'

type PendingAttach = { frames: ServerFrame[]; outstanding: number; started: boolean } // not-wire-format: per-socket replay bookkeeping, never serialized

/** A successful attach is a view transaction, not a live stream. The contract's
 * catch_up_complete closes it; a replay done is not a live turn's completion.
 * Keep this at the transport edge so replayed tool/status/card frames cannot
 * notify any of the app's stores before the whole response is ready. */
export class AttachReplayBuffer {
  private readonly pending = new Map<string, PendingAttach>()
  private readonly requests: string[] = []
  private readonly endedOnError = new Set<string>()

  begin(sessionId: string): void {
    this.requests.push(sessionId)
    const replay = this.pending.get(sessionId)
    // A → B → A can send two attaches for A on this socket. The gateway
    // handles them in order: keep the frames and count both completions.
    if (replay) replay.outstanding++
    else this.pending.set(sessionId, { frames: [], outstanding: 1, started: false })
  }

  clear(): void {
    this.pending.clear()
    this.requests.length = 0
    this.endedOnError.clear()
  }

  accept(frame: ServerFrame): ServerFrame[] {
    const explicitSessionId = 'session_id' in frame ? frame.session_id
      : frame.type === 'ask_user_question' ? frame.card.session_id : undefined
    // The gateway answers attaches serially in this socket's read loop. An
    // unscoped refusal before an answer starts belongs to the first request;
    // it must not discard or unbuffer the other sessions waiting behind it.
    const firstRequest = this.requests[0]
    const firstPending = firstRequest ? this.pending.get(firstRequest) : undefined
    const sessionId = explicitSessionId ?? (frame.type === 'error' && !firstPending?.started ? firstRequest : undefined)
    const replay = sessionId ? this.pending.get(sessionId) : undefined
    const failed = frame.type === 'done' && frame.stats?.replay_error === true
    if (failed && sessionId && this.endedOnError.delete(sessionId)) {
      // A read failure before session_snapshot sends error + failure done;
      // an early refusal sends only error. Both retire exactly ONE request.
      if (!replay) return [frame]
      replay.frames.push(frame)
      return []
    }
    if (!replay || !sessionId) return [frame]

    const refused = frame.type === 'error' && !replay.started
    if (refused) this.endedOnError.add(sessionId)
    if (frame.type === 'session_snapshot' || frame.type === 'session_state') {
      replay.started = true
      // The next serialized answer has begun; an earlier error-only response
      // can no longer be followed by its own failure done.
      this.endedOnError.clear()
    }
    if (failed) {
      // Never commit an incomplete snapshot or a cursor that claims success.
      // Its actual errors still run through the existing error/retry reducers.
      replay.frames = replay.frames.filter(item => item.type === 'error')
    }
    replay.frames.push(refused && !explicitSessionId ? { ...frame, session_id: sessionId } : frame)
    if (frame.type !== 'catch_up_complete' && !refused && !failed) return []

    replay.started = false
    const requestIndex = this.requests.indexOf(sessionId)
    if (requestIndex !== -1) this.requests.splice(requestIndex, 1)
    if (--replay.outstanding > 0) return []
    this.pending.delete(sessionId)
    return replay.frames
  }
}
