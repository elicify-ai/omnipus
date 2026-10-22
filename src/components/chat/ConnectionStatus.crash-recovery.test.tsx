import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Session } from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { RestartInterruptedNotice } from './ConnectionStatus'
import { dismissRestartNotice } from './useRestartInterrupted'
import { sessionStatusLabel } from '@/components/sessions/sessionLabels'

// DESIGN Decision 1 / Q14 and issue 1419. Only REST is doubled; real query
// cache, stores, notice/dismissal hook and status label remain under test.
vi.mock('@/lib/api', async (original) => ({
  ...await original<typeof import('@/lib/api')>(),
  fetchSessions: vi.fn(async () => []),
}))
const SID = 'crash-recovery-notice'
const NOW = '2026-10-22T00:00:00Z'
function row(reason: string, lifecycle_state: Session['lifecycle_state'] = 'interrupted'): Session {
  return Object.assign({ id: SID, type: 'delegate' as const, agent_id: 'mia', title: 'Helper', status: 'active' as const,
    created_at: NOW, updated_at: NOW, channel: 'webchat', partitions: [],
    stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 }, lifecycle_state,
  }, { interruption_reason: reason })
}
function saved(reason: string, state: Session['lifecycle_state'] = 'interrupted') {
  act(() => { queryClient.setQueryData(['sessions'], [row(reason, state)]) })
}
function running(value: boolean) {
  act(() => { useChatStore.setState((s) => ({ isStreaming: value, sessionsById: { ...s.sessionsById,
    [SID]: { ...s.sessionsById[SID], activeTurnId: value ? 'new-turn' : null },
  } })) })
}
beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date(NOW))
  queryClient.clear()
  act(() => {
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useSessionStore.getState().setActiveSession(SID, 'mia')
    useChatStore.getState().resetSessionForReplay(SID)
    useChatStore.setState((s) => ({ isStreaming: false, isReplaying: false, sessionsById: {
      ...s.sessionsById, [SID]: { ...s.sessionsById[SID], isReplaying: false, activeTurnId: null },
    } }))
  })
})
afterEach(() => { cleanup(); queryClient.clear(); vi.useRealTimers() })

describe('crash recovery — cause-specific Interrupted notice', () => {
  it.each(['tool_limit', 'reconciliation_required'])('%s is Interrupted, never restart copy or Failed', (reason) => {
    saved(reason)
    render(<RestartInterruptedNotice />)
    const notice = screen.getByTestId('restart-interrupted-notice')
    expect(notice).toHaveTextContent('Interrupted')
    expect(notice).not.toHaveTextContent(/restart/i)
    expect(notice).not.toHaveTextContent(/failed|paused/i)
    expect(notice).toHaveTextContent(/continue|message/i)
    expect(document.querySelector('[role="progressbar"]')).toBeNull()
  })

  it('keeps the real restart explanation for the restart positive control', () => {
    saved('restart')
    render(<RestartInterruptedNotice />)
    expect(screen.getByTestId('restart-interrupted-notice')).toHaveTextContent(/restart/i)
  })

  it('re-arms a second ceiling in the same boot even if a fast refetch never showed Working', () => {
    saved('tool_limit')
    render(<RestartInterruptedNotice />)
    expect(screen.getAllByTestId('restart-interrupted-notice')).toHaveLength(1)
    act(() => { dismissRestartNotice(SID) })
    running(true)
    expect(screen.queryByTestId('restart-interrupted-notice')).toBeNull()
    running(false)
    saved('tool_limit')
    expect(screen.getAllByTestId('restart-interrupted-notice')).toHaveLength(1)
  })

  it('does not show an interruption notice during a real active turn', () => {
    saved('tool_limit')
    running(true)
    render(<RestartInterruptedNotice />)
    expect(screen.queryByTestId('restart-interrupted-notice')).toBeNull()
  })
})

describe('issue 1419 — coarse active never means a running helper', () => {
  it.each(['stopped', 'interrupted', 'done'] as const)('active metadata with %s uses the canonical ended label', (lifecycle_state) => {
    const label = sessionStatusLabel(row('tool_limit', lifecycle_state))
    expect(label.tone).toBe(lifecycle_state)
    expect(label.text).not.toMatch(/working|running/i)
  })
  it('active metadata without a lifecycle is Unavailable, not Working', () => {
    const item = row('restart')
    delete item.lifecycle_state
    expect(sessionStatusLabel(item)).toStrictEqual({ text: 'Unavailable', tone: 'unavailable' })
  })
  it('the positive running control still renders Working', () => {
    expect(sessionStatusLabel(row('restart', 'working'))).toStrictEqual({ text: 'Working', tone: 'working' })
  })
})
