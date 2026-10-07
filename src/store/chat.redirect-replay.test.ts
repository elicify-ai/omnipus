import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { getMessageStatusSuffix } from '@/lib/truncation'
import { getMessages, useChatStore } from './chat'
import { useSessionStore } from './session'

// Founder ruling 2026-10-07: a redirected turn keeps its partial text with NO
// "(interrupted)" marker after a reload either; a plain Stop keeps it.
// Transcript order the gateway writes (pkg/agent/cancel.go turn_canceled
// entry, then pkg/agent/stop_redirect_root.go::continueOrdinaryAfterStop's
// user entry with id "redirect-<uuid>"): assistant partial (truncated:
// cancelled), turn_canceled(turn_id), user instruction.
const SID = 'session-s7-replay'
const TURN = 'turn-s7'

type Frame = Parameters<ReturnType<typeof useChatStore.getState>['handleFrame']>[0]
const feed = (f: Record<string, unknown>) =>
  act(() => { useChatStore.getState().handleFrame({ session_id: SID, ...f } as unknown as Frame) })

beforeEach(() => {
  act(() => {
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useSessionStore.getState().setActiveSession(SID, 'jim')
  })
})
afterEach(() => {
  act(() => {
    useChatStore.setState(useChatStore.getInitialState(), true)
    useSessionStore.setState(useSessionStore.getInitialState(), true)
  })
})

function replayCancelledTurn(followUpUserId: string) {
  feed({ type: 'replay_message', role: 'user', id: 'u-first', content: 'write a long answer' })
  feed({ type: 'replay_message', role: 'assistant', id: 'a-1', content: 'partial text so far', turn_id: TURN, truncated: true, truncation_reason: 'cancelled' })
  feed({ type: 'replay_message', role: 'turn_canceled', content: '', turn_id: TURN })
  feed({ type: 'replay_message', role: 'user', id: followUpUserId, content: 'now just say the word mango' })
  return getMessages(useChatStore.getState().sessionsById[SID])
}

describe('S7 reload (WS replay) — redirected vs plain-stopped turn', () => {
  it('a turn followed by a redirect-… instruction keeps its text and shows no marker', () => {
    const msgs = replayCancelledTurn('redirect-7f3a')
    const a = msgs.find((m) => m.role === 'assistant')
    expect(a?.content).toBe('partial text so far')
    expect(getMessageStatusSuffix(a!), 'no (interrupted) after reload').toBeNull()
    expect(msgs.filter((m) => m.role === 'user').map((m) => m.content)).toEqual(['write a long answer', 'now just say the word mango'])
  })

  it('control: a turn followed by an ordinary user message keeps "(interrupted)"', () => {
    const msgs = replayCancelledTurn('u-second')
    const a = msgs.find((m) => m.role === 'assistant')
    expect(getMessageStatusSuffix(a!)).toBe('(interrupted)')
  })
})
