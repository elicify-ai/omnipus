// Navigation hands-on regression, added to the #1428 + C1 RED pack.
// The takeover brief classifies this as PRE-EXISTING, not a #1266 regression.
//
// Oracle: chief's 1428-red-header brief (2026-10-23): after reattach without
// reload, a main chat keeps attachedSessionType 'main' and its header controls.
// Evidence: coordination/l1-uat/evidence/1266-bd037a35c/T/
// L1-reconnect-{before,online}.png and L1-reconnect-measurements.jsonl.
//
// Plan: attach a main chat, mount its real header control ONCE, disconnect
// for the observed 10 s, then invoke the real reconnect reattach helper.
// Assert the same session/owner, exact 'main' type, and one enabled control.
// REAL: stores, attach/reattach logic, MainChatAddParticipant and Button.
// DOUBLES: transport send and clock only. No source-text or mocked-UI oracle.
// Gaps: socket backoff, reload, other chat types and participant creation are
// outside this one regression. GREEN and mutation probes are deferred to CHECK
// (clear the type, replace it with 'chat', or suppress the header control).

import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import { MainChatAddParticipant } from '@/components/chat/AddParticipant'
import { reattachActiveSession } from '@/components/chat/OmnipusRuntimeProvider'
import type { ReattachSender } from '@/components/chat/OmnipusRuntimeProvider'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import { queryClient } from '@/lib/queryClient'
import type { WsConnection } from '@/lib/ws'

const SID = 'session_01JCEMAINHEADERRECONNECT0001'
const WORKSPACE = '01JCEMAINHEADERWORKSPACE00001'
const OWNER = 'jim'
const TITLE = 'Jim - Main Session'
const OFFLINE_MS = 10_000 // L1 reconnect evidence: about 10 s offline, no reload.
const CONTROL = { name: 'Add participant', exact: true }

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-10-23T13:57:47.880Z')) // L1 before-disconnect timestamp.
  queryClient.clear()
  localStorage.clear()
  act(() => {
    useChatStore.setState(useChatStore.getInitialState(), true)
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useUiStore.setState(useUiStore.getInitialState(), true)
    useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE })
  })
})

afterEach(() => {
  cleanup()
  queryClient.clear()
  localStorage.clear()
  vi.clearAllTimers()
  vi.useRealTimers()
})

it('keeps a main chat main and its Add participant header control after a 10 s disconnect and reattach without reload', () => {
  const firstTransport = { send: vi.fn().mockReturnValue(true) } satisfies ReattachSender
  act(() => {
    useConnectionStore.setState({
      connection: firstTransport as unknown as WsConnection,
      isConnected: true,
    })
    expect(useSessionStore.getState().attachToSession(SID, 'main', TITLE, OWNER)).toBe(true)
  })
  expect(firstTransport.send.mock.calls).toEqual([[{ type: 'attach_session', session_id: SID }]])
  expect(useSessionStore.getState().attachedSessionType).toBe('main')

  // This is the sole mount: reconnect must not rely on a route/page remount.
  render(<MainChatAddParticipant />)
  expect(screen.getByRole('button', CONTROL), 'instrument: the main-chat header initially renders the control').toBeVisible()
  expect(screen.getByRole('button', CONTROL)).toBeEnabled()

  act(() => {
    // The real WsLifecycle disconnect actions; no session/type reset or reload.
    useConnectionStore.getState().recordDisconnect(null)
    useChatStore.getState().clearStreamingState()
    vi.advanceTimersByTime(OFFLINE_MS)
  })
  expect(useConnectionStore.getState().isConnected).toBe(false)
  expect(useSessionStore.getState().attachedSessionType, 'disconnect alone must not change the chat kind').toBe('main')
  expect(screen.getByRole('button', CONTROL)).toBeVisible()

  const reconnectedTransport = { send: vi.fn().mockReturnValue(true) } satisfies ReattachSender
  act(() => {
    useConnectionStore.getState().setConnection(reconnectedTransport as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
    useConnectionStore.getState().setConnectionError(null)
    expect(reattachActiveSession(reconnectedTransport, useConnectionStore.getState().setConnectionError)).toBe(true)
  })
  expect(useConnectionStore.getState().lastDisconnectDurationMs, 'the fake clock reproduces the observed offline interval').toBe(OFFLINE_MS)
  expect(reconnectedTransport.send.mock.calls, 'a real reattach was sent for the SAME session').toEqual([
    [{ type: 'attach_session', session_id: SID }],
  ])
  expect(useConnectionStore.getState().connectionError).toBeNull()

  const state = useSessionStore.getState()
  expect({
    sessionId: state.activeSessionId,
    agentId: state.activeAgentId,
    attachedSessionType: state.attachedSessionType,
    controls: screen.queryAllByRole('button', CONTROL).map((control) => ({
      label: control.textContent?.trim(),
      disabled: control.hasAttribute('disabled'),
    })),
  }, 'reattach without reload must preserve the main-chat kind AND its accessible header control').toEqual({
    sessionId: SID,
    agentId: OWNER,
    attachedSessionType: 'main',
    controls: [{ label: 'Add participant', disabled: false }],
  })
})
