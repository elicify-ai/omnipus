// R2-S3 (held tick): the sender's "Waiting" row is replaced when the server
// reports the held request delivered (follow-up message_status), and a held
// row never keeps Stop/placeholder semantics of a failure.
import { beforeEach, describe, expect, it } from 'vitest'
import { act } from 'react'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import type { ServerFrame } from '@/lib/ws'

const SID = 'held-tick-sid'
const feed = (...frames: Array<Record<string, unknown>>) =>
  act(() => { for (const fr of frames) useChatStore.getState().handleFrame({ session_id: SID, ...fr } as unknown as ServerFrame) })
const userRow = () => useChatStore.getState().messages.find((m) => m.role === 'user')!

let sent: Array<Record<string, unknown>> = []
beforeEach(() => {
  sent = []
  act(() => {
    useChatStore.getState().clearStreamingState()
    useChatStore.setState({ sessionsById: {}, messages: [], isStreaming: false } as never)
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'jim', activeAgentType: null })
    useConnectionStore.setState({
      connection: { send: (p: unknown) => { sent.push(p as Record<string, unknown>); return true }, close: () => {} } as never,
      isConnected: true,
    } as never)
  })
})

describe('held tick is replaced by the delivery follow-up', () => {
  it.each(['received', 'working'] as const)('held then %s replaces the waiting tick', (next) => {
    act(() => { useChatStore.getState().sendMessage('@Mia review this') })
    const cid = String(sent[0].client_message_id)
    feed({ type: 'message_status', client_message_id: cid, state: 'held' })
    expect(userRow().deliveryStatus).toBe('held')
    feed({ type: 'message_status', client_message_id: cid, state: next })
    expect(userRow().deliveryStatus).toBe(next)
    expect(userRow().status).toBe('done')
  })
})
