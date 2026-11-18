// #1428 step 2. SV-12 browser half: generated Zod SendRefusedFrame.
// PIN of the contract already in this branch (C-1). Not a RED proof.
// Oracles: spec 4.2 / 4.3.2 and the generated schema, not a live server.
import { describe, expect, it } from 'vitest'
import { SendRefusedFrame } from '@/lib/api/generated/ws-schemas'

const chatOrigin = { kind: 'chat' as const, session_id: 'chat-a' }
const newOrigin = { kind: 'new_chat' as const, new_chat_key: 'k'.repeat(16) }

function refused(extra: Record<string, unknown> = {}) {
  return {
    type: 'send_refused',
    reason: 'wrong_chat',
    refused_frame: 'message',
    session_id: 'chat-b',
    origin: chatOrigin,
    client_message_id: 'm1',
    ...extra,
  }
}

describe('SV-12 generated SendRefusedFrame (PIN, contract already landed)', () => {
  it('parses a wrong_chat message refusal with a chat origin', () => {
    expect(SendRefusedFrame.parse(refused())).toEqual(refused())
  })

  it('parses redirect and cancel refusals, and a new_chat origin of 16 characters', () => {
    const redirect = refused({
      refused_frame: 'redirect',
      client_message_id: undefined,
      client_frame_id: 'r1',
      origin: newOrigin,
    })
    const cancel = refused({ refused_frame: 'cancel', client_frame_id: 'c1', client_message_id: undefined })
    expect(SendRefusedFrame.parse(redirect).refused_frame).toBe('redirect')
    expect(SendRefusedFrame.parse(cancel).reason).toBe('wrong_chat')
    expect(SendRefusedFrame.parse(refused({ reason: 'origin_malformed' })).reason).toBe('origin_malformed')
    expect(SendRefusedFrame.parse(refused({ reason: 'origin_unverified' })).reason).toBe('origin_unverified')
    expect(SendRefusedFrame.parse(refused({ reason: 'frame_malformed' })).reason).toBe('frame_malformed')
  })

  it('rejects an extra field, a null origin, an empty id, and a 15-character new_chat key', () => {
    expect(SendRefusedFrame.safeParse(refused({ unexpected: true })).success).toBe(false)
    expect(SendRefusedFrame.safeParse(refused({ origin: null })).success).toBe(false)
    expect(SendRefusedFrame.safeParse(refused({ client_message_id: '' })).success).toBe(false)
    expect(SendRefusedFrame.safeParse(refused({
      origin: { kind: 'new_chat', new_chat_key: 'k'.repeat(15) },
    })).success).toBe(false)
    expect(SendRefusedFrame.safeParse(refused({ reason: 'other' })).success).toBe(false)
    expect(SendRefusedFrame.safeParse({ ...refused(), type: 'error' }).success).toBe(false)
  })
})
