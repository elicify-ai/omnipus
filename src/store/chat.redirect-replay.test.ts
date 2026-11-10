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
    expect(a, 'assistant exists').toBeDefined()
    expect(getMessageStatusSuffix(a!), 'no (interrupted) after reload').toBeNull()
    expect(a!.status, 'finalised as an ordinary finished answer').toBe('done')
    expect(a!.isStreaming).toBe(false)
    expect(msgs.filter((m) => m.role === 'user').map((m) => m.content)).toEqual(['write a long answer', 'now just say the word mango'])
  })

  it('control: a turn followed by an ordinary user message keeps "(interrupted)"', () => {
    const msgs = replayCancelledTurn('u-second')
    const a = msgs.find((m) => m.role === 'assistant')
    expect(a, 'assistant exists').toBeDefined()
    expect(getMessageStatusSuffix(a!)).toBe('(interrupted)')
  })

  it('Stop, then a later unrelated user message, then a redirect: the earlier Stop marker stays', () => {
    feed({ type: 'replay_message', role: 'user', id: 'u-first', content: 'q1' })
    feed({ type: 'replay_message', role: 'assistant', id: 'a-1', content: 'stopped partial', turn_id: TURN, truncated: true, truncation_reason: 'cancelled' })
    feed({ type: 'replay_message', role: 'turn_canceled', content: '', turn_id: TURN })
    feed({ type: 'replay_message', role: 'user', id: 'u-2', content: 'q2' })
    feed({ type: 'replay_message', role: 'assistant', id: 'a-2', content: 'second answer', turn_id: 'turn-2' })
    feed({ type: 'replay_message', role: 'user', id: 'redirect-1', content: 'redirect idle chat' })
    const msgs = getMessages(useChatStore.getState().sessionsById[SID])
    const a1 = msgs.find((m) => m.id === 'a-1')
    expect(a1, 'a-1 exists').toBeDefined()
    expect(getMessageStatusSuffix(a1!), 'genuine Stop marker survives').toBe('(interrupted)')
  })

  it('turn_canceled stored AFTER the redirect instruction still leaves no marker', () => {
    feed({ type: 'replay_message', role: 'user', id: 'u-first', content: 'write a long answer' })
    feed({ type: 'replay_message', role: 'assistant', id: 'a-1', content: 'partial text so far', turn_id: TURN, truncated: true, truncation_reason: 'cancelled' })
    feed({ type: 'replay_message', role: 'user', id: 'redirect-9', content: 'now just say the word mango' })
    feed({ type: 'replay_message', role: 'turn_canceled', content: '', turn_id: TURN })
    const a = getMessages(useChatStore.getState().sessionsById[SID]).find((m) => m.id === 'a-1')
    expect(a, 'a-1 exists').toBeDefined()
    expect(getMessageStatusSuffix(a!)).toBeNull()
    expect(a!.status).toBe('done')
    expect(a!.isStreaming, 'never left streaming').toBeFalsy()
  })

  it('a user message with no reply between the Stop and the redirect stops the walk-back: marker stays', () => {
    feed({ type: 'replay_message', role: 'user', id: 'u-first', content: 'q1' })
    feed({ type: 'replay_message', role: 'assistant', id: 'a-1', content: 'stopped partial', turn_id: TURN, truncated: true, truncation_reason: 'cancelled' })
    feed({ type: 'replay_message', role: 'turn_canceled', content: '', turn_id: TURN })
    feed({ type: 'replay_message', role: 'user', id: 'u-2', content: 'q2 with no reply' })
    feed({ type: 'replay_message', role: 'user', id: 'redirect-2', content: 'redirect' })
    const a = getMessages(useChatStore.getState().sessionsById[SID]).find((m) => m.id === 'a-1')
    expect(a, 'a-1 exists').toBeDefined()
    expect(getMessageStatusSuffix(a!)).toBe('(interrupted)')
    expect(a!.status).toBe('interrupted')
  })

  it('an output-limit cutoff followed by a redirect keeps its own "(cut off at the output limit)" marker', () => {
    feed({ type: 'replay_message', role: 'user', id: 'u-first', content: 'q1' })
    feed({ type: 'replay_message', role: 'assistant', id: 'a-1', content: 'long answer cut', turn_id: TURN, truncated: true, truncation_reason: 'max_output_tokens' })
    feed({ type: 'replay_message', role: 'user', id: 'redirect-3', content: 'redirect' })
    const a = getMessages(useChatStore.getState().sessionsById[SID]).find((m) => m.id === 'a-1')
    expect(a, 'a-1 exists').toBeDefined()
    expect(getMessageStatusSuffix(a!)).toBe('(cut off at the output limit)')
  })

  // KNOWN LIMIT: see api.redirect-history.test.ts — plain Stop then a later
  // idle-chat redirect (no user message between) is stored like a redirected
  // turn, so the Stop marker is lost on reload. Pinned, not endorsed.
  it('KNOWN LIMIT: plain Stop then an idle-chat redirect loses the Stop marker on reload', () => {
    const msgs = replayCancelledTurn('redirect-idle')
    const a = msgs.find((m) => m.role === 'assistant')
    expect(a, 'assistant exists').toBeDefined()
    expect(getMessageStatusSuffix(a!)).toBeNull()
  })
})
