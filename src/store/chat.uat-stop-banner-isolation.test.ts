import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ErrorFrame } from '@/lib/api/generated/asyncapi-types'
import { codeToDisplay } from '@/lib/llm-error'
import type { WsConnection } from '@/lib/ws'
import { getMessages, useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useWorkspacesStore } from './workspacesStore'

// Oracle: release-UAT dispatch D3, and validator/report-v.md V4b / D4:
// The turn_canceled message belongs only to the stopped chat and must never
// be shown in a brand-new chat, including a late ack.
// Evidence source: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/evidence/2026-10-06-release.
// REAL: all stores/actions, send/cancel, frame routing and foreground sync.
// FAKE: socket transport only. connectionError is the production AppShell
// banner's source, not a test-only selector. Also retain the old transcript
// so clearing all error/history state cannot manufacture a passing result.
// GREEN and the Proof-of-failability/mutation gate are deferred to CHECK.

const PARENT = 'session-uat-d3-banner-parent'
const WORKSPACE = 'uat-d3-banner-workspace'
const INITIAL_ID = 'uat-d3-banner-first-user'
const INITIAL_TEXT = 'Delegate one helper and report progress.'
const PARTIAL_TEXT = 'Checking helper progress.'
// Wording comes from the catalogue; the UAT oracle pins chat isolation.
const STOP_COPY = codeToDisplay.turn_canceled

const sender = { send: vi.fn<WsConnection['send']>() }

function resetStores() {
  act(() => {
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    localStorage.clear()
    sessionStorage.clear()
  })
}

beforeEach(() => {
  resetStores()
  sender.send.mockReset().mockReturnValue(true)
  act(() => {
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
    useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE })
    useSessionStore.getState().setActiveSession(PARENT, 'jim')
  })
})

afterEach(resetStores)

function sendAndStopParent() {
  act(() => {
    useChatStore.getState().sendMessage(INITIAL_TEXT, { clientMessageId: INITIAL_ID })
    useChatStore.getState().handleFrame({
      type: 'token', session_id: PARENT, agent_id: 'jim', content: PARTIAL_TEXT,
    })
  })
  expect(useChatStore.getState().isStreaming, 'fixture: a real turn exists before Stop').toBe(true)
  act(() => { useChatStore.getState().cancelStream() })
  expect(sender.send.mock.calls.map(([frame]) => frame), 'fixture: real send followed by parent-only Stop').toStrictEqual([
    {
      type: 'message', session_id: PARENT, content: INITIAL_TEXT,
      client_message_id: INITIAL_ID, agent_id: 'jim', metadata: { workspace_id: WORKSPACE },
    },
    { type: 'cancel', session_id: PARENT },
  ])
}

function acknowledgeParentStop() {
  const frame: ErrorFrame = {
    type: 'error', session_id: PARENT, message: STOP_COPY,
    payload: { llm_error: { code: 'turn_canceled', message: STOP_COPY, retryable: true } },
  }
  act(() => {
    useChatStore.getState().handleFrame(frame)
    useChatStore.getState().handleFrame({ type: 'done', session_id: PARENT })
  })
  const stoppedMessages = getMessages(useChatStore.getState().sessionsById[PARENT])
  expect(stoppedMessages.filter((message) => message.content === PARTIAL_TEXT).map((message) => message.status),
    'D3 instrument: Stop really interrupted the original partial answer').toStrictEqual(['interrupted'])
  expect(stoppedMessages.filter((message) => message.errorCode === 'turn_canceled')
    .map(({ content, errorCode }) => ({ content, errorCode })),
  'D3 instrument: the actual typed stop error reached the original chat').toStrictEqual([
    { content: STOP_COPY, errorCode: 'turn_canceled' },
  ])
}

function expectEmptyNewChatWithoutStopBanner() {
  expect(useSessionStore.getState().activeSessionId, 'D3: New chat must stay selected').toBeNull()
  expect(useChatStore.getState().messages, 'D3: new chat has no stopped parent/helper transcript').toStrictEqual([])
  expect(useChatStore.getState().isStreaming, 'D3: old stop ack must not start work in the new chat').toBe(false)
  expect(useConnectionStore.getState().isConnected, 'instrument: this is not a disconnect/reconnect clearing the error').toBe(true)
  expect(useConnectionStore.getState().connectionError,
    `D3: a brand-new chat must not display "${STOP_COPY}" in the AppShell banner`).toBeNull()
}

describe('release UAT D3 — stopped-turn banner is isolated from a new chat', () => {
  it('D3-B1: starting a new chat after Stop removes the old stop banner without deleting the stopped transcript', () => {
    sendAndStopParent()
    acknowledgeParentStop()
    const oldTranscript = structuredClone(getMessages(useChatStore.getState().sessionsById[PARENT]))

    act(() => { useSessionStore.getState().startNewSession() })

    expect(getMessages(useChatStore.getState().sessionsById[PARENT]),
      'D3: starting fresh must preserve all messages and stop evidence in the original chat').toStrictEqual(oldTranscript)
    expectEmptyNewChatWithoutStopBanner()
  })

  it('D3-B2: a delayed stop acknowledgement for the old chat cannot put its banner into an already-open new chat', () => {
    sendAndStopParent()
    act(() => { useSessionStore.getState().startNewSession() })
    expect(useChatStore.getState().messages, 'fixture: fresh view is empty before the delayed frame').toStrictEqual([])

    acknowledgeParentStop()

    expectEmptyNewChatWithoutStopBanner()
  })
})
