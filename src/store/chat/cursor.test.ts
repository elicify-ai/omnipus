// cursor.test.ts: BE-DESIGN.md §6.2 apply rule (invariant "C1: idempotent,
// gap-detecting cursor gate") and §6.2's session_snapshot history-wipe rule
// (invariant "C2: wipe preserves pending tail + out-of-band state").
//
// PROVISIONAL: these are hand-derived from the design table (§6.2), not from
// Lane A's real gateway output (Lane A's hub — pkg/gateway/ws_session_hub.go
// — had not landed on this branch's base at the time this was written; see
// SQUAD-REPORT-BEC.md). The design table itself is the oracle here, not any
// existing SPA implementation — this is oracle-independent by construction.

import { describe, expect, it } from 'vitest'
import { gateFrameBySeq, applySnapshotHistoryWipe, type SeqFrameLike } from './cursor'
import { emptySessionState } from './session'
import type { ChatMessage, SessionChatState, SessionCursor } from './types'

describe('gateFrameBySeq (§6.2 apply rule)', () => {
  it('C1a: a frame with no seq is always applied and never touches the cursor', () => {
    const cursor: SessionCursor = { bootId: 'boot-1', seq: 5 }
    const frame: SeqFrameLike = {}
    const decision = gateFrameBySeq(cursor, frame)
    expect(decision).toEqual({ kind: 'apply', cursor })
  })

  it('C1b: cursor null + first sequenced frame -> apply, cursor becomes {bootId, seq}', () => {
    const frame: SeqFrameLike = { seq: 1, boot_id: 'boot-1' }
    const decision = gateFrameBySeq(null, frame)
    expect(decision).toEqual({ kind: 'apply', cursor: { bootId: 'boot-1', seq: 1 } })
  })

  it('C1c: seq === cursor.seq + 1 -> apply, cursor advances by exactly one', () => {
    const cursor: SessionCursor = { bootId: 'boot-1', seq: 5 }
    const decision = gateFrameBySeq(cursor, { seq: 6, boot_id: 'boot-1' })
    expect(decision).toEqual({ kind: 'apply', cursor: { bootId: 'boot-1', seq: 6 } })
  })

  it('C1d: seq <= cursor.seq -> drop (already applied), cursor untouched', () => {
    const cursor: SessionCursor = { bootId: 'boot-1', seq: 5 }
    expect(gateFrameBySeq(cursor, { seq: 5, boot_id: 'boot-1' })).toEqual({ kind: 'drop' })
    expect(gateFrameBySeq(cursor, { seq: 3, boot_id: 'boot-1' })).toEqual({ kind: 'drop' })
  })

  it('C1e: seq > cursor.seq + 1 -> gap, cursor returned unchanged for the caller to re-attach with', () => {
    const cursor: SessionCursor = { bootId: 'boot-1', seq: 5 }
    const decision = gateFrameBySeq(cursor, { seq: 9, boot_id: 'boot-1' })
    expect(decision).toEqual({ kind: 'gap', cursor })
  })

  it('C1f: a frame with no boot_id inherits the existing cursor bootId', () => {
    const cursor: SessionCursor = { bootId: 'boot-1', seq: 5 }
    const decision = gateFrameBySeq(cursor, { seq: 6 })
    expect(decision).toEqual({ kind: 'apply', cursor: { bootId: 'boot-1', seq: 6 } })
  })
})

describe('applySnapshotHistoryWipe (§6.2 session_snapshot rule)', () => {
  function userMsg(id: string, deliveryStatus?: ChatMessage['deliveryStatus']): ChatMessage {
    return { id, role: 'user', content: id, timestamp: new Date().toISOString(), deliveryStatus }
  }
  function assistantMsg(id: string): ChatMessage {
    return { id, role: 'assistant', content: 'reply', timestamp: new Date().toISOString(), status: 'done' }
  }

  it('C2a: wipes ordinary history (assistant + acknowledged user messages)', () => {
    const bucket = {
      ...emptySessionState(),
      messagesById: { u1: userMsg('u1', 'received'), a1: assistantMsg('a1') },
      messageOrder: ['u1', 'a1'],
    }
    const wiped = applySnapshotHistoryWipe(bucket)
    expect(wiped.messageOrder).toEqual([])
    expect(wiped.messagesById).toEqual({})
  })

  it('C2b: preserves the pending tail — messages with deliveryStatus queued/sending', () => {
    const bucket = {
      ...emptySessionState(),
      messagesById: {
        u1: userMsg('u1', 'received'),
        a1: assistantMsg('a1'),
        u2: userMsg('u2', 'sending'),
        u3: userMsg('u3', 'queued'),
      },
      messageOrder: ['u1', 'a1', 'u2', 'u3'],
    }
    const wiped = applySnapshotHistoryWipe(bucket)
    expect(wiped.messageOrder).toEqual(['u2', 'u3'])
    expect(wiped.messagesById.u2).toEqual(bucket.messagesById.u2)
    expect(wiped.messagesById.u3).toEqual(bucket.messagesById.u3)
  })

  it('C2c: preserves out-of-band state (pendingAsk, goalStatus, activeTurnId, cancelStage)', () => {
    const bucket = {
      ...emptySessionState(),
      messagesById: { a1: assistantMsg('a1') },
      messageOrder: ['a1'],
      pendingAsk: { session_id: 's1' } as unknown as SessionChatState['pendingAsk'],
      activeTurnId: 'turn-1',
      activeTurnAgentId: 'agent-1',
      cancelStage: 'graceful' as const,
    }
    const wiped = applySnapshotHistoryWipe(bucket)
    expect(wiped.activeTurnId).toBe('turn-1')
    expect(wiped.activeTurnAgentId).toBe('agent-1')
    expect(wiped.cancelStage).toBe('graceful')
  })

  it('C2d: sets awaitingCatchUp true', () => {
    const wiped = applySnapshotHistoryWipe(emptySessionState())
    expect(wiped.awaitingCatchUp).toBe(true)
  })
})
