import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ServerFrame } from '@/lib/ws'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useRunningActivity, type RunningActivity } from '@/hooks/useRunningActivity'
import { ActivityPanel } from './ActivityPanel'

// DESIGN Q14 Activity/Open path. Real frame reducer, stores, aggregation and
// panel; fake time, cached external REST data and the navigation edge only.
const { navigate } = vi.hoisted(() => ({ navigate: vi.fn() }))
vi.mock('@tanstack/react-router', () => ({ useNavigate: () => navigate }))
const PARENT = 'crash-activity-parent'
const CHILD = 'crash-activity-helper'
const NOW = '2026-10-22T00:00:00Z'
let activity: RunningActivity
function Surface() {
  activity = useRunningActivity()
  return <ActivityPanel open onOpenChange={() => {}} running={activity.running} recentlyFinished={activity.recentlyFinished} />
}
beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] })
  vi.setSystemTime(new Date(NOW))
  queryClient.clear()
  queryClient.setQueryData(['agents'], [])
  queryClient.setQueryData(['agent-activity-runs', 'mia'], [])
  act(() => {
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useSessionStore.getState().setActiveSession(PARENT, 'mia')
    useChatStore.getState().handleFrame({ type: 'subagent_start', session_id: PARENT, span_id: 'span', parent_call_id: 'call', child_session_id: CHILD, agent_id: 'mia', task_label: 'Recoverable helper' })
    useChatStore.getState().handleFrame({ type: 'subagent_state', session_id: PARENT, child_session_id: CHILD, span_id: 'span', state: 'running', created_at: NOW })
  })
  navigate.mockClear()
})
afterEach(() => { cleanup(); queryClient.clear(); vi.useRealTimers() })

describe('crash-recovery Interrupted Activity/Open', () => {
  it.each(['tool_limit', 'reconciliation_required'])('%s ends running activity but retains the helper Open target', (reason) => {
    render(<QueryClientProvider client={queryClient}><Surface /></QueryClientProvider>)
    expect(activity.runningChildren).toBe(1)
    // Future contract payload enters at the real reducer boundary. Generated
    // schema acceptance/reason parity is tested independently in the wire file.
    const frame = { type: 'subagent_state', session_id: PARENT, child_session_id: CHILD, span_id: 'span', state: 'interrupted', interruption_reason: reason, created_at: NOW }
    act(() => { useChatStore.getState().handleFrame(frame as unknown as ServerFrame) })
    expect(activity.runningChildren).toBe(0)
    expect(activity.runningCount).toBe(0)
    expect(activity.running).toStrictEqual([])
    expect(screen.queryByTestId('activity-section-running')).toBeNull()
    const row = screen.getByTestId('activity-row')
    expect(row).toHaveTextContent(/\binterrupted\b/i)
    expect(row.querySelector('.animate-spin')).toBeNull()
    expect(screen.queryByText(/failed|restart/i)).toBeNull()
    const open = screen.getByTestId('activity-row-open')
    fireEvent.click(open)
    expect(navigate).toHaveBeenCalledWith({ to: '/sessions/$sessionId', params: { sessionId: CHILD } })
    const settled = activity.recentlyFinished.map((item) => ({ key: item.key, status: item.status, duration: item.durationMs }))
    act(() => { vi.advanceTimersByTime(5_000) })
    expect(activity.recentlyFinished.map((item) => ({ key: item.key, status: item.status, duration: item.durationMs }))).toStrictEqual(settled)
  })
})
