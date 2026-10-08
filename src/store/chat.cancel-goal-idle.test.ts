import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { WsConnection } from '@/lib/ws'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'
import { useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useUiStore } from './ui'

// G1 (UAT): in the idle gap between a helper's goal turns isStreaming is false,
// yet the goal keeper will resume the helper on its own. Stop must still reach
// the gateway (RequestCancel pauses the keeper). Without a goal, an idle Stop
// must send nothing (no cancel_prearm latch per Escape).
// REAL: chat/session/connection stores and cancelStream. FAKE: the socket.
const SID = 'sess-goal-idle'
const sender = { send: vi.fn<WsConnection['send']>() }

function goalFrame(state: GoalStatusFrame['state']): GoalStatusFrame {
  return {
    type: 'goal_status', session_id: SID, goal_id: 'g1', condition: 'finish the report',
    round: 1, max_rounds: 5, latest_reason: '', active_loops: 1, cap: 3, state,
  }
}

function reset() {
  act(() => {
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useUiStore.setState({ toasts: [] })
  })
}

beforeEach(() => {
  reset()
  sender.send.mockReset().mockReturnValue(true)
  act(() => {
    useSessionStore.getState().setActiveSession(SID, 'jim')
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
  })
})
afterEach(reset)

describe('cancelStream in the idle gap of a goal (G1)', () => {
  it.each(['active', 're-planning', 'judging', 'judge_unavailable'] as const)('sends cancel when not streaming and the goal is %s', (state) => {
    act(() => { useChatStore.getState().handleFrame(goalFrame(state)) })
    expect(useChatStore.getState().isStreaming).toBe(false)
    act(() => { useChatStore.getState().cancelStream() })
    expect(sender.send.mock.calls).toEqual([[{ type: 'cancel', session_id: SID }]])
  })

  it('sends nothing when not streaming and there is no goal (negative guard)', () => {
    act(() => { useChatStore.getState().cancelStream() })
    expect(sender.send).not.toHaveBeenCalled()
  })

  it.each(['done', 'failed', 'cleared', 'expired'] as const)('sends nothing when the goal is already %s', (state) => {
    act(() => { useChatStore.getState().handleFrame(goalFrame(state)) })
    act(() => { useChatStore.getState().cancelStream() })
    expect(sender.send).not.toHaveBeenCalled()
  })
})

// Round-1 review: an honest Stop. Nothing is claimed (no "(interrupted)" mark, no
// delivered=true) unless a cancel frame actually leaves, and a finished answer is
// never relabelled by a Stop that only pauses the goal keeper.
describe('cancelStream is honest about what it did (round-1 review)', () => {
  const SID_A = 'sess-goal-A'
  const SID_B = 'sess-other-B'
  function finishedAnswer() {
    const frames = [
      { type: 'session_snapshot', session_id: SID, seq: 3, boot_id: 'b1', reason: 'unknown_position' },
      { type: 'session_state', session_id: SID, user_id: 'u1', pending_approvals: [], emitted_at: '2026-10-07T06:01:00Z' },
      { type: 'replay_message', session_id: SID, id: 'u-1', role: 'user', content: 'Do the report' },
      { type: 'replay_message', session_id: SID, id: 'a-1', role: 'assistant', content: 'Finished answer', turn_id: 't-1' },
      { type: 'catch_up_complete', session_id: SID, seq: 3, boot_id: 'b1', mode: 'snapshot' },
    ] as Parameters<ReturnType<typeof useChatStore.getState>['handleFrame']>[0][]
    act(() => { for (const f of frames) useChatStore.getState().handleFrame(f) })
  }
  const snapshot = () => useChatStore.getState().messages.map((m) => [m.id, m.status, m.content])

  it('idle chat, no goal: nothing marked, nothing sent', () => {
    finishedAnswer()
    const before = snapshot()
    act(() => { useChatStore.getState().cancelStream() })
    expect(sender.send).not.toHaveBeenCalled()
    expect(snapshot()).toEqual(before)
  })

  it('idle gap of an active goal: the cancel is sent and the finished answer is left as it was', () => {
    finishedAnswer()
    act(() => { useChatStore.getState().handleFrame(goalFrame('active')) })
    const before = snapshot()
    let delivered: boolean | undefined
    act(() => { delivered = useChatStore.getState().cancelStream() })
    expect(delivered).toBe(true)
    expect(sender.send.mock.calls).toEqual([[{ type: 'cancel', session_id: SID }]])
    expect(snapshot()).toEqual(before)
  })

  it('a goal on session A does not make Stop on session B send anything (session scoping)', () => {
    act(() => {
      useChatStore.getState().handleFrame({ ...goalFrame('active'), session_id: SID_A })
      useSessionStore.getState().setActiveSession(SID_B, 'jim')
    })
    act(() => { useChatStore.getState().cancelStream(SID_B) })
    expect(sender.send).not.toHaveBeenCalled()
    act(() => { useChatStore.getState().cancelStream(SID_A) })
    expect(sender.send.mock.calls).toEqual([[{ type: 'cancel', session_id: SID_A }]])
  })
})
