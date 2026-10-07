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
  it.each(['active', 're-planning', 'judging'] as const)('sends cancel when not streaming and the goal is %s', (state) => {
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
