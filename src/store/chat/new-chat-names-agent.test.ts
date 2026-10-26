// new-chat-names-agent.test.ts — K16 (omnipus-system-agent FR-122, §6.4):
// there is no default agent. A NEW chat names its agent on the minting WS
// frame; the gateway answers an agent-less frame with the error frame
// "an agent must be named". The SPA must never put that frame on the wire:
// the store refuses it locally, visibly, before any optimistic state.
//
// The real store runs (useChatStore.sendMessage / resendMessage) — only the
// WS connection is stubbed, exactly like disconnect-catchup-regressions.test.ts.

import { beforeEach, describe, expect, it } from 'vitest'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useUiStore } from '@/store/ui'
import { queryClient } from '@/lib/queryClient'
import { emptySessionState } from './session'

function sentFrames(): unknown[] {
  const sent: unknown[] = []
  const conn = {
    send: (frame: unknown) => {
      sent.push(frame)
      return true
    },
    close: () => {},
    isConnected: true,
  }
  useConnectionStore.setState({ connection: conn as never, isConnected: true } as never)
  return sent
}

function lastWarning(): { message: string; variant: string } | undefined {
  const toasts = useUiStore.getState().toasts
  return toasts[toasts.length - 1]
}

beforeEach(() => {
  queryClient.clear()
  useSessionStore.setState({ activeSessionId: null, activeAgentId: null } as never)
  useChatStore.setState({
    sessionsById: {},
    messages: [],
    messagesById: {},
    pendingFirstSend: null,
    pendingKickoff: null,
    pendingAutoApproveChoice: null,
  } as never)
  useUiStore.setState({ toasts: [] } as never)
})

describe('a NEW chat names its agent on the minting frame (K16)', () => {
  it('no agent selected → the frame never goes out; refusal is visible; no optimistic chat is minted', () => {
    const sent = sentFrames()

    useChatStore.getState().sendMessage('hello there')

    expect(sent).toHaveLength(0)
    // No '__pending' bucket, no pending first send: nothing was minted.
    expect(useChatStore.getState().sessionsById['__pending']).toBeUndefined()
    expect(useChatStore.getState().pendingFirstSend).toBeNull()
    const warning = lastWarning()
    expect(warning?.variant).toBe('warning')
    expect(warning?.message).toContain('no default agent')
  })

  it('agent selected → the minting frame carries agent_id', () => {
    const sent = sentFrames()
    useSessionStore.setState({ activeSessionId: null, activeAgentId: 'mia' } as never)

    useChatStore.getState().sendMessage('hello there')

    expect(sent).toHaveLength(1)
    const frame = sent[0] as { type: string; agent_id?: string; content?: string }
    expect(frame.type).toBe('message')
    expect(frame.agent_id).toBe('mia')
    expect(frame.content).toBe('hello there')
  })
})

describe('a __pending retry (a mint retry) names its agent too (K16)', () => {
  it('no agent selected → the retry is refused visibly and the bubble stays failed', () => {
    const sent = sentFrames()
    useSessionStore.setState({ activeSessionId: '__pending', activeAgentId: null } as never)
    useChatStore.setState({
      sessionsById: {
        __pending: {
          ...emptySessionState(),
          session_id: '__pending',
          messageOrder: ['u1'],
          messagesById: {
            u1: {
              id: 'u1',
              session_id: '__pending',
              role: 'user',
              content: 'hello there',
              timestamp: '2026-10-14T09:00:00Z',
              status: 'error',
              deliveryStatus: 'failed',
            },
          },
        },
      },
    } as never)

    useChatStore.getState().resendMessage('u1')

    expect(sent).toHaveLength(0)
    const bucket = useChatStore.getState().sessionsById['__pending']
    // This synthetic row has no retained first-send request or canonical owner;
    // foreground agent selection cannot authorize a generic Retry.
    expect(bucket?.messagesById['u1']?.deliveryStatus).toBe('failed')
    const warning = lastWarning()
    expect(warning?.variant).toBe('error')
    expect(warning?.message).toContain("conversation's agent is not loaded")
  })
})
