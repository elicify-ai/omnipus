// Founder report (live instance, Jarvis chat): the goal pill stayed "active"
// after the goal was cleared, including after reopening the chat. Client-side
// regression pin with the real store frame path and the real tray: an active
// goal_status, then the server's terminal `cleared` frame for the SAME goal_id
// (pkg/agent/goal_loop.go::endActiveGoal emits it with the goal's own id),
// leaves no pill once the terminal confirmation has shown; and a fresh load of
// the chat, whose history replays the set_goal call but whose server sends no
// goal_status for a cleared goal (EmitGoalStatusRehydrate re-emits only an
// ACTIVE record), shows no pill at all. The server-side half (a clear whose
// record transition fails stays active) is the backend-lead's.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import type { GoalStatusFrame, ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useJudgeActivityStore } from '@/store/judgeActivity'
import { GoalPillTray } from './GoalPillTray'

const SID = 'goal-cleared-session'
const GOAL = 'goal-ship-release'

function goalStatus(state: GoalStatusFrame['state']): GoalStatusFrame {
  return {
    type: 'goal_status', session_id: SID, goal_id: GOAL, condition: 'Ship the release notes',
    round: 2, max_rounds: 20, latest_reason: state === 'cleared' ? 'cleared by user' : 'working', active_loops: 1, cap: 16, state,
  }
}
function feed(...frames: ServerFrame[]) {
  act(() => { for (const frame of frames) useChatStore.getState().handleFrame(frame) })
}
function freshPage() {
  // A reload: a brand-new store and session selection for the same chat.
  cleanup()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState({ ...useSessionStore.getInitialState(), activeSessionId: SID, activeAgentId: 'mia' }, true)
  useJudgeActivityStore.getState().reset()
}

beforeEach(() => {
  vi.useFakeTimers()
  freshPage()
})
afterEach(() => {
  cleanup()
  vi.useRealTimers()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
})

describe('goal pill after the goal is cleared', () => {
  it('active → cleared (same goal_id) shows the cleared confirmation, then no pill', () => {
    render(<GoalPillTray />)
    feed(goalStatus('active'))
    expect(screen.getByTestId('goal-pill-active')).toBeInTheDocument()

    feed(goalStatus('cleared'))
    expect(screen.queryByTestId('goal-pill-active'), 'the active pill is gone at once').toBeNull()
    expect(screen.getByTestId('goal-pill-cleared')).toBeInTheDocument()

    act(() => { vi.advanceTimersByTime(4_001) })
    expect(screen.queryByTestId('goal-pill-tray'), 'no goal pill once the confirmation has shown').toBeNull()
  })

  it('a fresh load after the clear shows no goal pill, even though history replays the set_goal call', () => {
    render(<GoalPillTray />)
    feed(goalStatus('active'), goalStatus('cleared'))
    act(() => { vi.advanceTimersByTime(4_001) })

    freshPage()
    render(<GoalPillTray />)
    // Replayed transcript of the turn that set the goal; no goal_status frame,
    // because the server re-emits goal status only for an active goal.
    feed(
      { type: 'replay_message', session_id: SID, id: 'goal-setter', role: 'assistant', content: 'Setting the goal.', turn_id: 'turn-goal' },
      { type: 'tool_call_start', session_id: SID, call_id: 'call-set-goal', tool: 'set_goal', params: { mode: 'register', condition: 'Ship the release notes' } },
      { type: 'tool_call_result', session_id: SID, call_id: 'call-set-goal', tool: 'set_goal', status: 'success', result: 'Goal registered' },
      { type: 'done', session_id: SID },
    )
    expect(useChatStore.getState().goalPills ?? {}, 'no goal pill is derived from replayed history').toStrictEqual({})
    expect(screen.queryByTestId('goal-pill-tray')).toBeNull()
  })
})
