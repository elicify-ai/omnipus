/**
 * omnipusChat.test.ts — the Omnipus entry's dedicated attach path and the
 * workflow-start message (omnipus-system-agent FR-100, FR-045, FR-012, K8;
 * BDD-4.1, BDD-12.1; ADR-20261011 OSA-10 U1, OSA-11).
 *
 * Oracle: the spec, not the implementation —
 *   - opening Omnipus from Sales attaches Omnipus's main (the default
 *     workspace's member main) and leaves Sales active and Sales's remembered
 *     chat untouched;
 *   - a W1 button sends ONE message frame into that main carrying
 *     workflow_start {W1, Sales} and NO metadata.workspace_id (the server
 *     refuses re-binding the Omnipus chat to another workspace);
 *   - an unresolved main sends nothing;
 *   - offline, no workflow message is queued without its target data.
 * Transport boundary: connection.send only.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import type { Workspace } from '@/lib/api'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { useWorkspacesStore } from './workspacesStore'
import { useUiStore } from './ui'
import { openOmnipusChat } from './omnipusChat'

const OMNIPUS_MAIN = 'main-session-default+omnipus'
const SALES_CHAT = 'sales-remembered-chat'

const defaultWs = {
  id: 'ws-default',
  name: 'My Workspace',
  is_default: true,
  core_team: ['omnipus', 'mia'],
  member_configs: { omnipus: { main_session_id: OMNIPUS_MAIN }, mia: { main_session_id: 'main-default+mia' } },
} as unknown as Workspace
const salesWs = { id: 'ws-sales', name: 'Sales', core_team: ['mia'], member_configs: {} } as unknown as Workspace

type Frame = Record<string, unknown> & { type: string }

function setup(connected = true) {
  const send = vi.fn((frame: Frame) => { void frame; return true })
  act(() => {
    useConnectionStore.setState({ connection: connected ? ({ send } as never) : null, isConnected: connected })
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-sales' })
    useSessionStore.setState({
      activeSessionId: SALES_CHAT,
      activeAgentId: 'mia',
      sessionByWorkspace: { 'ws-sales': { id: SALES_CHAT, type: 'chat', title: null, agentId: 'mia' } },
      workspaceEntry: null,
      resolvingSessionForWorkspace: {},
    })
    useChatStore.setState({ sessionsById: {}, isStreaming: false })
    useUiStore.setState({ toasts: [] })
  })
  return send
}

function framesOf(send: ReturnType<typeof vi.fn>, type: string): Frame[] {
  return send.mock.calls.map((call) => call[0] as Frame).filter((frame) => frame.type === type)
}

describe('openOmnipusChat', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    localStorage.clear()
  })

  it('attaches Omnipus main without switching the workspace or touching Sales\'s remembered chat', () => {
    const send = setup()
    let outcome = ''
    act(() => { outcome = openOmnipusChat({ workspaces: [defaultWs, salesWs], sessions: [] }) })
    expect(outcome).toBe('opened')
    const attaches = framesOf(send, 'attach_session')
    expect(attaches).toHaveLength(1)
    expect(attaches[0].session_id).toBe(OMNIPUS_MAIN)
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('ws-sales')
    expect(useSessionStore.getState().activeSessionId).toBe(OMNIPUS_MAIN)
    expect(useSessionStore.getState().activeAgentId).toBe('omnipus')
    expect(useSessionStore.getState().sessionByWorkspace['ws-sales']?.id).toBe(SALES_CHAT)
    expect(framesOf(send, 'message')).toHaveLength(0)
  })

  it('a W1 button sends one message with workflow_start for Sales and no workspace_id', () => {
    const send = setup()
    act(() => {
      openOmnipusChat({
        workspaces: [defaultWs, salesWs],
        sessions: [],
        workflow: { workflow: 'W1', workspaceId: 'ws-sales', workspaceName: 'Sales' },
      })
    })
    const messages = framesOf(send, 'message')
    expect(messages).toHaveLength(1)
    expect(messages[0].session_id).toBe(OMNIPUS_MAIN)
    expect(messages[0].content).toBe('Create an agent in Sales')
    expect(messages[0].workflow_start).toEqual({ workflow: 'W1', target_workspace_id: 'ws-sales' })
    const metadata = messages[0].metadata as Record<string, unknown> | undefined
    expect(metadata?.workspace_id).toBeUndefined()
  })

  it('W18 names the team setup for the target workspace', () => {
    const send = setup()
    act(() => {
      openOmnipusChat({
        workspaces: [defaultWs, salesWs],
        sessions: [],
        workflow: { workflow: 'W18', workspaceId: 'ws-sales', workspaceName: 'Sales' },
      })
    })
    const [message] = framesOf(send, 'message')
    expect(message.content).toBe('Set up a team for Sales')
    expect(message.workflow_start).toEqual({ workflow: 'W18', target_workspace_id: 'ws-sales' })
  })

  it('sends nothing when Omnipus main does not resolve', () => {
    const send = setup()
    const noMain = { ...defaultWs, member_configs: { mia: { main_session_id: 'x' } } } as unknown as Workspace
    let outcome = ''
    act(() => {
      outcome = openOmnipusChat({
        workspaces: [noMain, salesWs],
        sessions: [],
        workflow: { workflow: 'W1', workspaceId: 'ws-sales', workspaceName: 'Sales' },
      })
    })
    expect(outcome).toBe('unavailable')
    expect(send).not.toHaveBeenCalled()
    expect(useSessionStore.getState().activeSessionId).toBe(SALES_CHAT)
  })

  it('offline: no workflow message is queued without its target data', () => {
    setup(false)
    act(() => {
      openOmnipusChat({
        workspaces: [defaultWs, salesWs],
        sessions: [],
        workflow: { workflow: 'W1', workspaceId: 'ws-sales', workspaceName: 'Sales' },
      })
    })
    expect(useChatStore.getState().outboundQueue ?? []).toHaveLength(0)
    expect(useUiStore.getState().toasts.some((toast) => /was not sent/.test(toast.message))).toBe(true)
  })

  it('an ordinary send from the Omnipus chat carries no workspace_id while Sales is active', () => {
    const send = setup()
    act(() => { openOmnipusChat({ workspaces: [defaultWs, salesWs], sessions: [] }) })
    act(() => { useChatStore.getState().sendMessage('hello') })
    const [message] = framesOf(send, 'message')
    const metadata = message.metadata as Record<string, unknown> | undefined
    expect(metadata?.workspace_id).toBeUndefined()
    expect(message.workflow_start).toBeUndefined()
  })
})
