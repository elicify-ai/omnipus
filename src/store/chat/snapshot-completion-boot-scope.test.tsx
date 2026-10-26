// Permanent r18 C/D counterexamples; original Round8/9 file stays unchanged.
// Oracles: paging D7 + user guide "If the gateway restarts during a reply":
// "A gateway restart ends an in-flight answer; losing only your browser
// connection does not." "Idle conversations and completed answers keep
// their previous state." No prior cursor is not a witnessed boot change.
// REAL frame validation, history owner/merge/sweep and connection-status UI.
// No implementation, store selector, page adapter or renderer is mocked.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import type { Session } from '@/lib/api'
import type { ServerFrame } from '@/lib/api/generated/asyncapi-types'
import type { SessionHistoryResponse } from '@/lib/api/generated/openapi-types'
import { queryClient } from '@/lib/queryClient'
import { AssistantMessageConnectionStatus } from '@/components/chat/ConnectionStatus'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { entry, expectValidFrame, expectValidPage, page, pos, replay, snapshot } from '../../../tests/chat-history-paging/fixtures'

const SID = 'r18-completed-boot-scope'
const USER = 'r18-user-source'
const ANSWER = 'r18-answer-source'
const TURN = 'r18-observed-turn'
const BOOT = 'r18-same-process'
const PREFIX = 'The tab watched this prefix. '
const COMPLETE = 'The tab watched this prefix. The gateway completed the answer while the tab was offline.'
const STAMP = '2026-10-25T10:00:00Z'
const savedDefaults = queryClient.getDefaultOptions()

function feed(frames: ServerFrame[]) {
  frames.forEach(expectValidFrame)
  act(() => frames.forEach(frame => useChatStore.getState().handleFrame(frame)))
}

function completedSnapshot(history: SessionHistoryResponse, cut: number): void {
  expectValidPage(history, history.entries.map((_, index) => pos(index + 1)), history.entries.length)
  feed([
    { ...snapshot(history), seq: cut, boot_id: BOOT },
    { type: 'session_state', session_id: SID, user_id: 'r18-reader', pending_approvals: [],
      emitted_at: STAMP, history_state: { status: 'available' } },
    ...history.entries.map(source => replay(source, SID)),
    { type: 'done', session_id: SID },
    { type: 'catch_up_complete', session_id: SID, seq: cut, boot_id: BOOT, mode: 'snapshot' },
  ])
}

function savedAnswer() {
  const history = page([
    entry(1, { id: USER, role: 'user', content: 'Finish this answer.', timestamp: STAMP }),
    entry(2, { id: ANSWER, role: 'assistant', content: COMPLETE, agent_id: 'mia', turn_id: TURN, timestamp: STAMP, status: 'ok' }),
  ], { sessionId: SID })
  expectValidPage(history, [pos(1), pos(2)], 2)
  return history
}

function witnessPrefix(numbered: boolean): void {
  feed([
    { type: 'user_message', session_id: SID, id: USER, content: 'Finish this answer.', timestamp: STAMP,
      ...(numbered ? { seq: 6 } : {}) },
    { type: 'token', session_id: SID, message_id: ANSWER, turn_id: TURN, agent_id: 'mia', content: PREFIX,
      ...(numbered ? { seq: 7 } : {}) },
  ])
  const current = useChatStore.getState().sessionsById[SID]
  expect(current.messagesById[ANSWER]).toMatchObject({ id: ANSWER, role: 'assistant', content: PREFIX, turnId: TURN })
  expect(current.messagesById[ANSWER].isStreaming, 'instrument: there really is an observed open prefix to settle').toBe(true)
}

function renderStatusConsumer() {
  render(<QueryClientProvider client={queryClient}>
    <div data-testid="r18-real-status-consumer"><AssistantMessageConnectionStatus messageId={ANSWER} agentName="Mia" /></div>
  </QueryClientProvider>)
  expect(screen.getByTestId('r18-real-status-consumer')).toBeInTheDocument()
}

function expectCompletedWithoutRegeneration(): void {
  const state = useChatStore.getState().sessionsById[SID]
  expect(state.history.historyLoaded, 'the valid bounded history really committed').toBe(true)
  expect(state.history.readerPositions).toStrictEqual([pos(1), pos(2)])
  expect(state.cursor).toStrictEqual({ bootId: BOOT, seq: 20 })
  expect(state.messageOrder).toStrictEqual([USER, ANSWER])
  expect(state.messagesById[ANSWER]).toMatchObject({ id: ANSWER, role: 'assistant', content: COMPLETE, turnId: TURN, status: 'done' })
  expect(state.messagesById[ANSWER].isStreaming ?? false, 'a finished saved answer is not streaming').toBe(false)
  // Optional internal flags may be absent or false; neither may be true.
  expect(state.messagesById[ANSWER].confirmedUnfinished === true,
    'r18 C/D: a completed non-restart answer must never be confirmed unfinished').toBe(false)
  expect(state.isStreaming).toBe(false)
  expect(state.activeTurnId).toBeNull()
  expect(state.unansweredLastUserMessageId).toBeNull()
  expect(state.restartInterruptedBootId).toBeUndefined()
  expect(screen.queryByTestId('assistant-connection-status')).toBeNull()
  expect(screen.queryByRole('button', { name: /Generate again/i }), 'no false regeneration action on the real status consumer').toBeNull()
}

beforeEach(() => {
  queryClient.clear()
  queryClient.setDefaultOptions({ ...savedDefaults, queries: { ...savedDefaults.queries, retry: false, gcTime: Infinity } })
  act(() => {
    useSessionStore.setState({ ...useSessionStore.getInitialState(), activeSessionId: SID, activeAgentId: 'mia' }, true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState({ ...useConnectionStore.getInitialState(), isConnected: true }, true)
  })
  const listed: Session = { id: SID, agent_id: 'mia', title: 'Completed same-process reply', type: 'chat', lifecycle_state: 'done',
    created_at: STAMP, updated_at: STAMP, message_count: 2, workspace_id: 'sales' }
  // Fresh ordinary metadata prevents an unrelated roster read; no selector
  // or lifecycle classifier is replaced, and this is not a turn-state seed.
  queryClient.setQueryData(['sessions'], [listed])
})

afterEach(() => {
  cleanup()
  queryClient.clear()
  queryClient.setDefaultOptions(savedDefaults)
  act(() => {
    useChatStore.setState(useChatStore.getInitialState(), true)
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  })
})

describe('r18 snapshot completion distinguishes a real boot change', () => {
  it('same boot settles a witnessed prefix completed while disconnected, with no unfinished flag or Generate again', () => {
    completedSnapshot(page([], { sessionId: SID }), 5)
    expect(useChatStore.getState().sessionsById[SID].cursor).toStrictEqual({ bootId: BOOT, seq: 5 })
    witnessPrefix(true)
    expect(useChatStore.getState().sessionsById[SID].cursor).toStrictEqual({ bootId: BOOT, seq: 7 })
    renderStatusConsumer()
    act(() => useConnectionStore.getState().recordDisconnect(ANSWER))
    completedSnapshot(savedAnswer(), 20)
    act(() => useConnectionStore.getState().setConnected(true))
    expectCompletedWithoutRegeneration()
  })

  it('first completed snapshot with an unnumbered prefix and no prior cursor does not invent a boot change', () => {
    // Generated-contract/store reachable; no claim that this exact sequence
    // was captured from a live gateway. An ordinary cold case without the
    // open prefix would not discriminate the missing-prior-boot guard.
    witnessPrefix(false)
    expect(useChatStore.getState().sessionsById[SID].cursor, 'D boundary: no prior cursor really exists').toBeNull()
    renderStatusConsumer()
    completedSnapshot(savedAnswer(), 20)
    expectCompletedWithoutRegeneration()
  })
})
