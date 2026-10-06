import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { SubagentStartFrame, SubagentStateFrame } from '@/lib/api/generated/asyncapi-types'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import type { WsConnection } from '@/lib/ws'
import { useRunningActivity, type RunningActivity } from '@/hooks/useRunningActivity'
import { ActivityPanel } from './ActivityPanel'
import { ActivityBar } from './ActivityBar'

// U2-R1/R2: ADR-20260928 D2 calls Stop "non-terminal" and requires
// "same-generation RESUME". A stopped state needs no successful end frame.
// REAL: store, frame routing, hook, panel, bar, controls. Fake clock/socket,
// cached agents (no external service needed), router navigation only.
const PARENT = 'uat-activity-parent'
const sender = { send: vi.fn<WsConnection['send']>().mockReturnValue(true) }
const { navigate } = vi.hoisted(() => ({ navigate: vi.fn() }))
vi.mock('@tanstack/react-router', () => ({ useNavigate: () => navigate }))
let activity: RunningActivity

function Surface() {
  activity = useRunningActivity()
  return <ActivityPanel open onOpenChange={() => {}} running={activity.running} recentlyFinished={activity.recentlyFinished} />
}

function resetChat() {
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useChatStore.setState(useChatStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
  useConnectionStore.getState().setConnected(true)
  useSessionStore.getState().setActiveSession(PARENT, 'jim')
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] })
  vi.setSystemTime(new Date('2026-10-06T00:00:00Z'))
  queryClient.clear()
  queryClient.setQueryData(['agents'], [])
  act(resetChat)
  navigate.mockClear()
})
afterEach(() => {
  cleanup()
  queryClient.clear()
  vi.useRealTimers()
})

function start(child: string) {
  const frame: SubagentStartFrame = { type: 'subagent_start', session_id: PARENT, span_id: `span-${child}`, parent_call_id: `call-${child}`, child_session_id: child, task_label: `Work ${child}` }
  useChatStore.getState().handleFrame(frame)
}
function state(child: string, value: SubagentStateFrame['state']) {
  const frame: SubagentStateFrame = { type: 'subagent_state', session_id: PARENT, child_session_id: child, span_id: `span-${child}`, state: value, created_at: new Date().toISOString() }
  useChatStore.getState().handleFrame(frame)
}
function mountPanel() {
  render(<QueryClientProvider client={queryClient}><Surface /></QueryClientProvider>)
}
function childRows() {
  return activity.recentlyFinished.filter((item) => item.kind === 'agent').map((item) => ({ child: item.childSessionId, lifecycle: item.lifecycleState, duration: item.durationMs }))
}

describe('U2 — Activity uses current child lifecycle, not an open delegation bracket', () => {
  it.each(['live', 'replay'] as const)('%s stopped states alone remove three helpers from Running now and keep all Open targets', async (mode) => {
    const children = ['child-1', 'child-2', 'child-3']
    act(() => { for (const child of children) { start(child); state(child, 'running') } })
    mountPanel()
    expect(activity.runningCount).toBe(3)
    expect(activity.runningChildren).toBe(3)
    expect(screen.getByText('3 running')).toBeInTheDocument()
    await act(async () => { await vi.advanceTimersByTimeAsync(2_000) })
    expect(activity.running.map((item) => item.durationMs)).toStrictEqual([2_000, 2_000, 2_000])
    act(() => {
      if (mode === 'replay') resetChat()
      for (const child of children) {
        if (mode === 'replay') { start(child); state(child, 'running') }
        state(child, 'stopped')
      }
      if (mode === 'replay') useChatStore.getState().handleFrame({ type: 'catch_up_complete', session_id: PARENT, seq: 0, boot_id: 'uat-replay-boot', mode: 'snapshot' })
    })
    expect(activity.runningChildren).toBe(0)
    expect(activity.runningCount).toBe(0)
    expect(activity.running).toStrictEqual([])
    const stoppedRows = childRows()
    expect([...stoppedRows].sort((a, b) => a.child!.localeCompare(b.child!))).toStrictEqual(children.map((child) => ({ child, lifecycle: 'stopped', duration: undefined })))
    expect(screen.getByText('0 running')).toBeInTheDocument()
    expect(screen.queryByTestId('activity-section-running')).not.toBeInTheDocument()
    expect(screen.getAllByText('stopped', { exact: true })).toHaveLength(3)
    expect(screen.getAllByTestId('activity-row-open')).toHaveLength(3)
    for (const child of children) {
      fireEvent.click(within(screen.getByText(`Work ${child}`).closest('[data-testid="activity-row"]') as HTMLElement).getByText('Open'))
      expect(navigate).toHaveBeenLastCalledWith({ to: '/sessions/$sessionId', params: { sessionId: child } })
    }
    await act(async () => { await vi.advanceTimersByTimeAsync(5_000) })
    expect(childRows()).toStrictEqual(stoppedRows)
  })

  it.each(['completed', 'failed'] as const)('same-span Resume reactivates one child; early %s stops its timer without stopping its sibling', (final) => {
    act(() => { for (const child of ['resumed', 'sibling']) { start(child); state(child, 'running') } })
    mountPanel()
    expect(activity.runningCount).toBe(2)
    act(() => state('resumed', 'stopped'))
    expect(activity.running.map((item) => item.key)).toStrictEqual(['span-sibling'])
    act(() => state('resumed', 'running'))
    expect(activity.runningCount).toBe(2)
    expect(activity.runningChildren).toBe(2)
    expect(activity.recentlyFinished).toStrictEqual([])
    act(() => state('resumed', final))
    expect(activity.running.map((item) => item.key)).toStrictEqual(['span-sibling'])
    expect(activity.recentlyFinished.map((item) => item.status)).toStrictEqual([final === 'completed' ? 'success' : 'error'])
    expect(activity.recentlyFinished.map((item) => item.durationMs)).toStrictEqual([undefined])
    act(() => useChatStore.getState().handleFrame({ type: 'subagent_end', session_id: PARENT, span_id: 'span-resumed', status: final === 'completed' ? 'success' : 'error', duration_ms: 100, final_result: 'Retained result' }))
    expect(activity.running.map((item) => item.key)).toStrictEqual(['span-sibling'])
    expect(activity.recentlyFinished.map((item) => ({ key: item.key, duration: item.durationMs }))).toStrictEqual([{ key: 'span-resumed', duration: 100 }])
  })

  it('keeps queued and waiting children visible without counting them as running', () => {
    act(() => { start('queued'); state('queued', 'queued'); start('waiting'); state('waiting', 'needs_input') })
    mountPanel()
    expect(activity.runningChildren).toBe(0)
    expect(activity.runningCount).toBe(0)
    expect(screen.getByText('0 running')).toBeInTheDocument()
    expect(screen.queryByTestId('activity-section-running')).not.toBeInTheDocument()
    expect(within(screen.getByTestId('activity-section-queued')).getByText('Work queued')).toBeInTheDocument()
    expect(within(screen.getByTestId('activity-section-waiting')).getByText('Work waiting')).toBeInTheDocument()
    expect(screen.getAllByTestId('activity-row-open')).toHaveLength(2)
  })

  it('keeps the Agents Activity control reachable for a stopped helper, without calling it failed', async () => {
    act(() => { start('inspectable'); state('inspectable', 'running'); state('inspectable', 'stopped') })
    render(<QueryClientProvider client={queryClient}><ActivityBar /></QueryClientProvider>)
    const bar = await screen.findByRole('button', { name: 'Agents — Activity' })
    fireEvent.click(bar)
    await waitFor(() => expect(screen.getByText('stopped', { exact: true })).toBeInTheDocument())
    expect(screen.getByTestId('activity-row-open')).toBeInTheDocument()
    expect(screen.queryByText('Running now')).not.toBeInTheDocument()
    expect(screen.queryByText(/failed/)).not.toBeInTheDocument()
  })
})
