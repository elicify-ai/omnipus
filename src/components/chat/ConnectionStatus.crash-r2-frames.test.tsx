import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Session } from '@/lib/api'
import type { ServerFrame, WsConnection } from '@/lib/ws'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { finishedTurnIdsBySession } from '@/store/chat/types'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { seedCanonicalSession } from '../../../tests/unit/fixtures/canonical-session'
import { RestartInterruptedNotice } from './ConnectionStatus'

// PRT T3 / DESIGN 1: only HTTP/socket edges are doubled. In particular this
// test never sets isStreaming, activeTurnId or endedInterruption by hand.
// The "stale" instrument is a real pre-turn saved read whose later refetches
// hang (the server stops answering) — never a fabricated newer contradiction.
let pending = false
let savedReadsExhausted = false
vi.mock('@/lib/api', async original => ({
  ...await original<typeof import('@/lib/api')>(),
  fetchSessions: vi.fn((): Promise<Session[]> => {
    if (pending || savedReadsExhausted) return new Promise(() => {})
    savedReadsExhausted = true
    return Promise.resolve([{
      id: 'r2-frame-chat', type: 'chat', agent_id: 'mia', title: 'Chat', status: 'active',
      created_at: '2026-10-26T00:00:00Z', updated_at: '2026-10-26T00:00:00Z', message_count: 0,
      lifecycle_state: 'working',
    }])
  }),
}))
const SID = 'r2-frame-chat'
let send: ReturnType<typeof vi.fn>
function feed(...frames: ServerFrame[]) {
  act(() => { for (const frame of frames) useChatStore.getState().handleFrame(frame) })
}
function start(turn: string, seq: number) {
  act(() => { useChatStore.getState().sendMessage('Explicit continuation: finish this work') })
  expect(send).toHaveBeenLastCalledWith(expect.objectContaining({ type: 'message', session_id: SID, content: 'Explicit continuation: finish this work' }))
  // ADR-082 D4: the turn is announced by the real session_state frame.
  feed({
    type: 'session_state', user_id: 'owner', pending_approvals: [], emitted_at: '2026-10-26T00:00:00Z',
    session_id: SID, active_turn: { turn_id: turn, agent_id: 'mia', started_at: '2026-10-26T00:00:00Z' },
  } as ServerFrame)
  feed({ type: 'token', session_id: SID, turn_id: turn, message_id: `message-${turn}`, content: `partial ${turn}`, seq })
  expect(useChatStore.getState().sessionsById[SID].isStreaming).toBe(true)
  expect(useChatStore.getState().sessionsById[SID].activeTurnId).toBe(turn)
}
beforeEach(() => {
  pending = false
  savedReadsExhausted = false
  queryClient.clear()
  // Module-scoped S2 registry survives across tests in this file/worker; a
  // stale finalized-turn record would make the real session_state
  // announcement a no-op (same reset rationale as frame-cases.catchup.test.ts).
  for (const key of Object.keys(finishedTurnIdsBySession)) delete finishedTurnIdsBySession[key]
  send = vi.fn(() => true)
  act(() => {
    useChatStore.setState(useChatStore.getInitialState(), true)
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useSessionStore.getState().setActiveSession(SID, 'mia')
    seedCanonicalSession(SID, 'mia')
    useConnectionStore.setState({ connection: { send, connect: vi.fn(), disconnect: vi.fn(), isConnected: true } as unknown as WsConnection, isConnected: true })
  })
  feed({ type: 'session_started', session_id: SID }, { type: 'catch_up_complete', session_id: SID, mode: 'incremental', boot_id: 'same-boot-r2', seq: 0 })
})
afterEach(() => { cleanup(); queryClient.clear(); vi.restoreAllMocks() })

describe.each(['stale_working', 'pending'] as const)('real typed done while REST is %s', (rest) => {
  it.each(['tool_limit', 'reconciliation_required'] as const)('%s immediately reaches the single notice; continuation, clean end and second interruption stay truthful', async reason => {
    pending = rest === 'pending'
    render(<RestartInterruptedNotice />)
    // Let the pre-turn saved read land before the turn starts, so the stale
    // instrument is a genuinely older observation, not one that lands after
    // the done (a later-landing read is newer by version, by design).
    await act(async () => { await Promise.resolve(); await Promise.resolve() })
    start('turn-1', 1)
    expect(screen.queryByTestId('restart-interrupted-notice')).toBeNull()
    // The done-triggered session-list refetch must hang: the saved read stays
    // at its pre-turn snapshot, so the frame itself is the only fresh signal.
    savedReadsExhausted = true
    feed({ type: 'done', session_id: SID, turn_id: 'turn-1', message_id: 'message-turn-1', stats: { tokens: 0, cost: 0, turn_failed: false, interruption_reason: reason }, seq: 2 })
    await waitFor(() => expect(screen.queryByTestId('restart-interrupted-notice')).not.toBeNull())
    const notice = screen.getByTestId('restart-interrupted-notice')
    expect(notice).toHaveTextContent('Interrupted')
    expect(notice).toHaveTextContent(reason === 'tool_limit' ? /tool.call limit/i : /file.*check/i)
    expect(notice).toHaveTextContent(/send a message to continue/i)
    expect(notice).not.toHaveTextContent(/restart|failed|paused/i)
    expect(useChatStore.getState().sessionsById[SID].endedInterruption).toMatchObject({ reason, turnId: 'turn-1' })
    expect(useChatStore.getState().sessionsById[SID].activeTurnId).toBeNull()
    expect(useChatStore.getState().isStreaming).toBe(false)

    start('turn-2', 3) // Actual user-send action and actual frames, not setter-produced state.
    expect(screen.queryByTestId('restart-interrupted-notice')).toBeNull()
    feed({ type: 'done', session_id: SID, turn_id: 'turn-2', message_id: 'message-turn-2', stats: { tokens: 0, cost: 0 }, seq: 4 })
    // A witnessed clean completion settles the interrupted report: while the
    // saved response stays hung/stale, no resurrected notice may appear.
    await act(async () => { await Promise.resolve() })
    expect(screen.queryByTestId('restart-interrupted-notice')).toBeNull()
    start('turn-3', 5)
    feed({ type: 'done', session_id: SID, turn_id: 'turn-3', message_id: 'message-turn-3', stats: { tokens: 0, cost: 0, interruption_reason: reason }, seq: 6 })
    await waitFor(() => expect(screen.getAllByTestId('restart-interrupted-notice')).toHaveLength(1))
    expect(screen.getByTestId('restart-interrupted-notice')).toHaveTextContent('Interrupted')
    expect(useChatStore.getState().sessionsById[SID].endedInterruption).toMatchObject({ reason, turnId: 'turn-3' })
  })
})
