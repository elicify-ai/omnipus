// T33, B31/B51/D15/D-C1. Actual route tree/workspace pointer flow, adapter,
// store and socket. No route/component/store mocks; only HTTP + browser socket.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import { queryClient } from '@/lib/queryClient'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { runWorkspaceEntry, supersedeNavigationIntent } from '@/store/session/workspaceEntryFlow'
import { MAIN_ID, WS, json } from '../../tests/l1-room-ui/harness'
import { mountStage1App, resetStage1App, Stage1Network, stopStage1App } from '../../tests/l1-room-ui/stage1-harness'
import { expectValidDetail, session, unavailableDetail, STAMP, UNAVAILABLE } from '../../tests/chat-history-paging/fixtures'
import { historyTransport } from '../../tests/chat-history-paging/transport'

let transport: ReturnType<typeof historyTransport> | undefined
beforeEach(() => { resetStage1App(); supersedeNavigationIntent() })
afterEach(async () => {
  supersedeNavigationIntent()
  transport?.connection.disconnect(); transport = undefined
  await stopStage1App()
  queryClient.clear()
})

describe('T33 actual identity consumers with unavailable history — B31/B51', () => {
  it('a partial-list remembered pointer remains exact when detail is metadata-only', async () => {
    const network = new Stage1Network()
    // D15: missing from a partial list is unknown, not deleted. Detail still
    // supplies authorized identity although its chat index is unavailable.
    network.respond('GET', '/api/v1/sessions', () => json({ sessions: [], partial_errors: ['mia'] }))
    const detail = unavailableDetail(session(MAIN_ID))
    expectValidDetail(detail, [], 0)
    network.respond('GET', `/api/v1/sessions/${MAIN_ID}`, () => json(detail))
    act(() => useSessionStore.setState({ activeSessionId: null, sessionByWorkspace: {
      [WS]: { id: MAIN_ID, type: 'chat', title: 'Remember me', agentId: 'mia' },
    } }))
    transport = historyTransport()
    await act(async () => { await runWorkspaceEntry(WS) })
    expect(useSessionStore.getState().activeSessionId, 'D15 unavailable history cannot become pointer deletion/main fallback').toBe(MAIN_ID)
    expect(useSessionStore.getState().sessionByWorkspace[WS]?.id).toBe(MAIN_ID)
    expect(transport.socket.sent.filter((frame) => frame.type === 'attach_session' && !frame.ack_attention))
      .toStrictEqual([{ type: 'attach_session', session_id: MAIN_ID }])
    expect(network.requests.filter((read) => read.path === `/api/v1/sessions/${MAIN_ID}`)).toHaveLength(1)
    expect(network.unexpected).toStrictEqual([])
  })

  it('the actual session route attaches metadata-only identity and shows history error without a fake welcome', async () => {
    const network = new Stage1Network()
    const detail = unavailableDetail(session(MAIN_ID))
    expectValidDetail(detail, [], 0)
    network.respond('GET', `/api/v1/sessions/${MAIN_ID}`, () => json(detail))
    network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => json({ error: UNAVAILABLE }, 500))
    const mounted = await mountStage1App(`/sessions/${MAIN_ID}`)
    await waitFor(() => expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID))
    await waitFor(() => expect(mounted.socket.sent.filter((frame) => frame.type === 'attach_session' && !frame.ack_attention)).toHaveLength(1))
    await act(async () => {
      mounted.socket.receive({ type: 'session_state', session_id: MAIN_ID, user_id: 'history-reader', pending_approvals: [], emitted_at: STAMP,
        history_state: { status: 'unavailable', reason: 'index_missing', message: UNAVAILABLE } })
      mounted.socket.receive({ type: 'catch_up_complete', session_id: MAIN_ID, boot_id: 'history-boot', seq: 10, mode: 'live_only' })
    })
    expect(await screen.findByRole('alert')).toHaveTextContent(UNAVAILABLE)
    expect(screen.getByTestId('chat-input')).toBeEnabled()
    expect(screen.queryByText('Session not found', { exact: true })).not.toBeInTheDocument()
    expect(screen.queryByTestId('welcome-state')).not.toBeInTheDocument()
    expect(useChatStore.getState().sessionsById[MAIN_ID].replayCompletedForSession).toBeNull()
    expect(network.unexpected).toStrictEqual([])
  })
})
