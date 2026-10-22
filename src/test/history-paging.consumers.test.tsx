// T33, B31/B51/D15/D-C1. Actual route tree/workspace pointer flow, adapter,
// store and socket. No route/component/store mocks; only HTTP + browser socket.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import { queryClient } from '@/lib/queryClient'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { runWorkspaceEntry, supersedeNavigationIntent } from '@/store/session/workspaceEntryFlow'
import { MAIN_ID, WS, json } from '../../tests/l1-room-ui/harness'
import { mountStage1App, resetStage1App, Stage1Network, stopStage1App } from '../../tests/l1-room-ui/stage1-harness'
import { availableDetail, expectValidDetail, page, session, unavailableDetail, STAMP, UNAVAILABLE } from '../../tests/chat-history-paging/fixtures'
import { historyTransport } from '../../tests/chat-history-paging/transport'
import { WELCOME_HEADING } from '../../tests/chat-history-paging/application'

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
    const target = 'history-remembered-extra'
    expect(target, 'fixture control: fallback to the workspace main must be distinguishable').not.toBe(MAIN_ID)
    const detail = unavailableDetail(session(target))
    expectValidDetail(detail, [], 0)
    network.respond('GET', `/api/v1/sessions/${target}`, () => json(detail))
    act(() => useSessionStore.setState({ activeSessionId: null, sessionByWorkspace: {
      [WS]: { id: target, type: 'chat', title: 'Remember me', agentId: 'mia' },
    } }))
    expect(useSessionStore.getState().activeSessionId, 'fixture control: no pre-attached target').toBeNull()
    transport = historyTransport()
    await act(async () => { await runWorkspaceEntry(WS) })
    expect(useSessionStore.getState().activeSessionId, 'D15 unavailable history cannot become pointer deletion/main fallback').toBe(target)
    expect(useSessionStore.getState().sessionByWorkspace[WS]?.id).toBe(target)
    expect(transport.socket.sent.filter((frame) => frame.type === 'attach_session' && !frame.ack_attention))
      .toStrictEqual([{ type: 'attach_session', session_id: target }])
    expect(network.requests.filter((read) => read.path === `/api/v1/sessions/${target}`)).toHaveLength(1)
    expect(network.unexpected).toStrictEqual([])
  })

  it('the actual session route attaches metadata-only identity and shows history error without a fake welcome', async () => {
    const network = new Stage1Network()
    const target = 'history-deep-linked-extra'
    expect(useSessionStore.getState().activeSessionId, 'fixture control: the route target is not already attached').not.toBe(target)
    const detail = unavailableDetail(session(target))
    expectValidDetail(detail, [], 0)
    network.respond('GET', `/api/v1/sessions/${target}`, () => json(detail))
    network.respond('GET', `/api/v1/sessions/${target}/messages`, () => json({ error: UNAVAILABLE }, 500))
    const mounted = await mountStage1App(`/sessions/${target}`, { initialAttachResponse: 'caller' })
    await waitFor(() => expect(useSessionStore.getState().activeSessionId).toBe(target))
    await waitFor(() => expect(mounted.socket.sent.filter((frame) => frame.type === 'attach_session' && frame.session_id === target && !frame.ack_attention)).toHaveLength(1))
    await act(async () => {
      mounted.socket.receive({ type: 'session_state', session_id: target, user_id: 'history-reader', pending_approvals: [], emitted_at: STAMP,
        history_state: { status: 'unavailable', reason: 'index_missing', message: UNAVAILABLE } })
      mounted.socket.receive({ type: 'catch_up_complete', session_id: target, boot_id: 'history-boot', seq: 10, mode: 'live_only' })
    })
    expect(await screen.findByRole('alert')).toHaveTextContent(UNAVAILABLE)
    expect(screen.getByTestId('chat-input')).toBeEnabled()
    expect(screen.queryByText('Session not found', { exact: true })).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', WELCOME_HEADING)).not.toBeInTheDocument()
    expect(useChatStore.getState().sessionsById[target].replayCompletedForSession).toBeNull()
    expect(network.unexpected).toStrictEqual([])
  })

  it('live_only preserves an earlier legitimate history completion without claiming history is available', async () => {
    const network = new Stage1Network()
    const target = 'history-available-then-unavailable'
    const history = page([], { sessionId: target })
    const detail = availableDetail(history, session(target))
    expectValidDetail(detail, [], 0)
    network.respond('GET', `/api/v1/sessions/${target}`, () => json(detail))
    network.respond('GET', `/api/v1/sessions/${target}/messages`, () => json(history))
    // Here the default successful setup response is intentional: D7a does not
    // erase genuine earlier proof when a later attach completes only controls.
    const mounted = await mountStage1App(`/sessions/${target}`)
    expect(useChatStore.getState().sessionsById[target].replayCompletedForSession, 'positive control: an actual available snapshot completed first').toBe(target)
    const connection = useConnectionStore.getState().connection
    if (!connection) throw new Error('Fixture instrument: the real application WebSocket connection is missing')
    expect(connection.send({ type: 'attach_session', session_id: target, since_seq: 1, boot_id: 'stage1-fixture' }), 'the later live_only response must own an actual attach').toBe(true)
    await act(async () => {
      mounted.socket.receive({ type: 'session_state', session_id: target, user_id: 'history-reader', pending_approvals: [], emitted_at: STAMP,
        history_state: { status: 'unavailable', reason: 'index_missing', message: UNAVAILABLE } })
      mounted.socket.receive({ type: 'catch_up_complete', session_id: target, boot_id: 'history-boot', seq: 10, mode: 'live_only' })
    })
    await waitFor(() => expect(useChatStore.getState().sessionsById[target].cursor).toStrictEqual({ bootId: 'history-boot', seq: 10 }))
    expect(useChatStore.getState().sessionsById[target].replayCompletedForSession, 'D7a does not erase a prior legitimate history completion').toBe(target)
    expect(useChatStore.getState().sessionsById[target].history.historyLoaded, 'live_only is not current history readiness').toBe(false)
    expect(useChatStore.getState().sessionsById[target].isReplaying).toBe(false)
    expect(await screen.findByRole('alert')).toHaveTextContent(UNAVAILABLE)
    expect(screen.getByTestId('chat-input')).toBeEnabled()
    expect(screen.queryByRole('heading', WELCOME_HEADING)).not.toBeInTheDocument()
    expect(network.unexpected).toStrictEqual([])
  })
})
