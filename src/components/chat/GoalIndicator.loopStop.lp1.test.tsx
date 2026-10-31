// LP1: parent-approved stop behavior; all old GoalIndicator cases remain unchanged.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act, cleanup, render, screen, within } from '@testing-library/react'
import { GoalIndicator } from './GoalIndicator'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { parseFrameSafe } from '@/lib/ws'
import type { GoalStatusFrame, LoopStatusFrame } from '@/lib/api/generated/asyncapi-types'

const SID_A = 'goal-status-test-sid-a'
const SID_B = 'goal-status-test-sid-b'

function lp1Goal(overrides: Partial<GoalStatusFrame> = {}): GoalStatusFrame {
  return {
    type: 'goal_status',
    session_id: 's1',
    condition: 'ship the release',
    round: 3,
    max_rounds: 20,
    latest_reason: 'tests still failing',
    active_loops: 1,
    cap: 16,
    state: 'active',
    ...overrides,
  }
}

function lp1Loop(overrides: Partial<LoopStatusFrame> = {}): LoopStatusFrame {
  return {
    type: 'loop_status',
    session_id: 's1',
    mode: 'interval',
    run: 2,
    max_runs: 10,
    next_delay: 900,
    state: 'active',
    ...overrides,
  }
}

function lp1RegisteredLoop(overrides: Partial<LoopStatusFrame> = {}): LoopStatusFrame {
  const input = lp1Loop(overrides)
  const parsed = parseFrameSafe(input)
  expect(parsed).toEqual(input)
  if (parsed === null || parsed.type !== 'loop_status') {
    throw new Error('BLOCKED: existing registered loop_status schema did not accept the arranged input')
  }
  return parsed
}

function LP1RealSessionFooter({ sessionId }: { sessionId: string }) {
  const loopStatus = useChatStore((state) => state.sessionsById[sessionId]?.loopStatus ?? null)
  return <GoalIndicator goalStatus={null} loopStatus={loopStatus} />
}

function lp1ResetStores() {
  act(() => {
    useChatStore.setState({ sessionsById: {}, goalStatus: null, loopStatus: null })
    useSessionStore.setState({ activeSessionId: null, activeAgentId: null, activeAgentType: null })
  })
}

beforeEach(lp1ResetStores)
afterEach(() => { cleanup(); lp1ResetStores() })

describe('LP1 stopped loop footer', () => {
  it('removes the real loop footer on active-to-stopped without reload', () => {
    const active = lp1RegisteredLoop({ state: 'active' })
    const stopped = lp1RegisteredLoop({ state: 'stopped' })
    const { container, rerender } = render(<GoalIndicator goalStatus={null} loopStatus={active} />)
    expect(screen.getByTestId('loop-status-line')).toBeVisible()
    expect(screen.getByTestId('loop-status-line')).toHaveTextContent(`run ${active.run}/${active.max_runs}`)
    rerender(<GoalIndicator goalStatus={null} loopStatus={stopped} />)
    expect(screen.queryByTestId('loop-status-line')).not.toBeInTheDocument()
    expect(container).toBeEmptyDOMElement()
    rerender(<GoalIndicator goalStatus={null} loopStatus={active} />)
    expect(screen.getByTestId('loop-status-line')).toBeVisible()
  })

  it('hides only the stopped session after the real registered frame handler stores it', () => {
    const activeA = lp1RegisteredLoop({ session_id: SID_A, state: 'active', seq: 1 })
    const activeB = lp1RegisteredLoop({ session_id: SID_B, state: 'active', seq: 1, run: 4 })
    const stoppedA = lp1RegisteredLoop({ session_id: SID_A, state: 'stopped', seq: 2 })
    act(() => {
      useSessionStore.setState({ activeSessionId: SID_A })
      useChatStore.getState().handleFrame(activeA)
      useChatStore.getState().handleFrame(activeB)
    })
    render(<><section data-testid="lp1-session-a"><LP1RealSessionFooter sessionId={SID_A} /></section><section data-testid="lp1-session-b"><LP1RealSessionFooter sessionId={SID_B} /></section></>)
    const a = within(screen.getByTestId('lp1-session-a'))
    const b = within(screen.getByTestId('lp1-session-b'))
    expect(a.getByTestId('loop-status-line')).toBeVisible()
    expect(b.getByTestId('loop-status-line')).toHaveTextContent(`run ${activeB.run}/${activeB.max_runs}`)
    act(() => { useChatStore.getState().handleFrame(stoppedA) })
    expect(useChatStore.getState().sessionsById[SID_A].loopStatus).toEqual(stoppedA)
    expect(useChatStore.getState().loopStatus).toEqual(stoppedA)
    expect(useChatStore.getState().sessionsById[SID_B].loopStatus).toEqual(activeB)
    expect(b.getByTestId('loop-status-line')).toBeVisible()
    expect(b.getByTestId('loop-status-line')).toHaveTextContent(`run ${activeB.run}/${activeB.max_runs}`)
    expect(a.queryByTestId('loop-status-line')).not.toBeInTheDocument()
    const laterA = lp1RegisteredLoop({ session_id: SID_A, state: 'active', seq: 3, run: 3 })
    act(() => { useChatStore.getState().handleFrame(laterA) })
    expect(a.getByTestId('loop-status-line')).toHaveTextContent(`run ${laterA.run}/${laterA.max_runs}`)
    expect(useChatStore.getState().sessionsById[SID_B].loopStatus).toEqual(activeB)
  })

  it('retains the goal branch and accepts subsequent active frames without touching another session', () => {
    const activeA = lp1RegisteredLoop({ session_id: SID_A, state: 'active', seq: 1 })
    const activeB = lp1RegisteredLoop({ session_id: SID_B, state: 'active', seq: 1, run: 4 })
    const stoppedA = lp1RegisteredLoop({ session_id: SID_A, state: 'stopped', seq: 2 })
    const laterA = lp1RegisteredLoop({ session_id: SID_A, state: 'active', seq: 3, run: 3 })
    act(() => {
      useSessionStore.setState({ activeSessionId: SID_A })
      for (const frame of [activeA, activeB, stoppedA]) useChatStore.getState().handleFrame(frame)
    })
    const goal = lp1Goal()
    const goalView = render(<GoalIndicator goalStatus={goal} loopStatus={stoppedA} />)
    expect(screen.getByTestId('goal-indicator-condition')).toHaveTextContent(goal.condition)
    expect(screen.getByTestId('goal-indicator-round')).toHaveTextContent(`round ${goal.round}/${goal.max_rounds}`)
    goalView.unmount()
    render(<LP1RealSessionFooter sessionId={SID_A} />)
    act(() => { useChatStore.getState().handleFrame(laterA) })
    expect(useChatStore.getState().sessionsById[SID_A].loopStatus).toEqual(laterA)
    expect(useChatStore.getState().loopStatus).toEqual(laterA)
    expect(useChatStore.getState().sessionsById[SID_B].loopStatus).toEqual(activeB)
    expect(screen.getByTestId('loop-status-line')).toHaveTextContent(`run ${laterA.run}/${laterA.max_runs}`)
  })
})
