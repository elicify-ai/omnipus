// Acceptance source: team-lead's 2026-10-14 stage-1 dispatch, items 2(a)–(f).
// REAL production route tree, AppShell, sidebar/chat/profile/modal, stores,
// generated API clients and socket dispatcher. No feature-switch mocks.
// GREEN and the exact mutations listed per case are deferred to fresh CHECK.
import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ToolApprovalActionRequest, ToolApprovalResponse } from '@/lib/api/generated/openapi-types'
import type { RoomRef, SecretRequestFrame, SessionStateFrame, SessionStateSecretRequest, ToolApprovalRequiredFrame } from '@/lib/api/generated/asyncapi-types'
import { queryClient } from '@/lib/queryClient'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { useSecretRequestStore } from '@/store/secretRequests'
import { useToolApprovalStore } from '@/store/toolApproval'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { json, MAIN_ID, NOW, ROOM_ID, workspace, WS } from '../../../tests/l1-room-ui/harness'
import {
  approval, emitFromGateway, exposeTranscriptViewport, MAIN_PATH, mountStage1App, resetStage1App,
  SETTINGS_PATH, settledMainChat, Stage1Network, stopStage1App,
} from '../../../tests/l1-room-ui/stage1-harness'

let net: Stage1Network
beforeEach(() => { resetStage1App(); net = new Stage1Network() })
afterEach(async () => {
  try {
    expect(net.unexpected, 'the real app must not fail its setup or hide an unexpected network error').toEqual([])
  } finally {
    vi.useRealTimers()
    await stopStage1App()
  }
})

async function settledReads() {
  // Query settlement, not an elapsed-time absence oracle. All issued reads are
  // recorded synchronously even if their responses have not yet settled.
  await waitFor(() => expect(queryClient.isFetching()).toBe(0))
  expect(net.requests.some((request) => request.method === 'GET' && request.path === '/api/v1/agents')).toBe(true)
  expect(net.requests.some((request) => request.method === 'GET' && request.path === '/api/v1/workspaces')).toBe(true)
}

function roomRequests() {
  return net.requests.filter((request) => /^\/api\/v1\/(?:rooms(?:\/|$)|workspaces\/[^/]+\/rooms(?:\/|$))/.test(request.path))
}

async function showApproval(socket: Parameters<typeof emitFromGateway>[0], frame = approval()) {
  expect(screen.queryAllByRole('dialog', { hidden: true })).toHaveLength(0)
  await emitFromGateway(socket, frame)
  await waitFor(() => expect(useToolApprovalStore.getState().queue.map((entry) => entry.approvalId)).toEqual([frame.approval_id]))
  const modal = await screen.findByRole('dialog', { name: 'Tool Approval Required' })
  expect(modal).toBeVisible()
  expect(within(modal).getByRole('button', { name: 'Approve Once' })).toBeEnabled()
  expect(within(modal).getByRole('button', { name: 'Deny' })).toBeEnabled()
  expect(modal).toHaveTextContent(String(frame.args.path))
  return modal
}

describe('stage 1 — rooms hidden through the real application shell', () => {
  // CHECK mutant for S1-a1: featureSwitches.ts::ROOMS_ENABLED = true.
  it('[S1-a1; dispatch 2(a)] a populated expanded workspace has no Group chats and never reads its room collection', async () => {
    const { router } = await mountStage1App(SETTINGS_PATH)
    const workspaces = screen.getByRole('group', { name: 'Workspaces' })
    expect(within(workspaces).getByRole('button', { name: 'Sales' })).toBeVisible()
    expect(within(workspaces).getByRole('button', { name: 'Mia' })).toBeVisible()
    expect(within(workspaces).getByRole('button', { name: 'Jim' })).toBeVisible()
    await settledReads()
    expect(router.state.location.pathname).toBe('/settings')
    expect(screen.queryByText('Group chats', { exact: true })).toBeNull()
    expect(screen.queryByTestId(`group-chats-${WS}`)).toBeNull()
    expect(roomRequests()).toEqual([])
  })

  // CHECK mutant for S1-a2: featureSwitches.ts::ROOMS_ENABLED = true.
  it('[S1-a2; dispatch 2(a)] a member main chat has no Add participant entry point or room-list request', async () => {
    const { router, socket } = await mountStage1App()
    await settledMainChat(socket)
    expect(screen.getByTestId('chat-input')).toBeVisible()
    expect(useSessionStore.getState().activeAgentId).toBe('mia')
    await settledReads()
    expect(router.state.location.pathname).toBe(MAIN_PATH)
    expect(screen.queryByRole('button', { name: 'Add participant', hidden: true })).toBeNull()
    expect(roomRequests()).toEqual([])
  })

  // CHECK mutant for S1-a3: set ROOMS_ENABLED=true, or remove the disabled-room
  // beforeLoad redirect from the REAL workspace room Route.
  it('[S1-a3; dispatch 2(a)] a direct room URL redirects to workspace chat without any room REST request or attach_room frame', async () => {
    const { router, socket } = await mountStage1App(`/workspaces/${WS}/rooms/${ROOM_ID}`)
    await waitFor(() => expect(router.state.location.pathname).toBe(MAIN_PATH))
    await settledMainChat(socket)
    await settledReads()
    expect(roomRequests()).toEqual([])
    // Positive socket control: the actual app has attached the member main.
    expect(socket.sent.filter((frame) => frame.type === 'attach_session').map((frame) => frame.session_id)).toContain(MAIN_ID)
    expect(socket.sent.filter((frame) => frame.type === 'attach_room')).toEqual([])
    expect(screen.queryByTestId('room-header')).toBeNull()
  })
})

describe('stage 1 — private secrets hidden through the shell-owned agent profile', () => {
  // CHECK mutant for S1-b: featureSwitches.ts::AGENT_PRIVATE_SECRETS_ENABLED = true.
  it('[S1-b; dispatch 2(b)] a conversation agent Tools panel has no Private secrets section and never reads secrets', async () => {
    await mountStage1App(SETTINGS_PATH)
    // Open the shell's single profile through the real sidebar row menu.
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'More actions for Mia' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Edit' }))
    const profile = await screen.findByTestId('agent-profile-sheet')
    const basics = within(profile).getByRole('tabpanel', { name: 'Basics' })
    expect(within(basics).getByTestId('agent-name-input')).toHaveValue('Mia')
    const tools = within(profile).getByRole('tab', { name: 'Tools' })
    await user.click(tools)
    expect(tools).toHaveAttribute('aria-selected', 'true')
    const panel = within(profile).getByRole('tabpanel', { name: 'Tools' })
    await settledReads()
    expect(net.requests.filter((request) => request.method === 'GET' && request.path === '/api/v1/agents/mia/tools')).toHaveLength(1)
    expect(within(panel).queryByTestId('agent-tools-load-error')).toBeNull()
    expect(within(panel).getByTestId('mcp-assignment-section')).toHaveTextContent('Assigned connectors')
    expect(within(profile).queryByText('Private secrets', { exact: true })).toBeNull()
    expect(net.requests.filter((request) => request.method === 'GET' && /^\/api\/v1\/secrets(?:\/|$)/.test(request.path))).toEqual([])
    expect(net.writes()).toEqual([])
  })
})

// Contract-derived actions, not legacy booleans: ToolApprovalActionRequest.yaml.
const settingsActions = [
  { label: 'Approve Once', action: 'allow_once' },
  { label: 'Deny', action: 'deny' },
] as const satisfies ReadonlyArray<{ label: string; action: ToolApprovalActionRequest['action'] }>

describe('stage 1 — release approval modal reachable on Settings', () => {
  // CHECK mutant for BOTH S1-c cases: remove AppShell's ToolApprovalModal mount.
  it.each(settingsActions)('[S1-c; dispatch 2(c)] $label on Settings posts the exact $action decision for the active chat approval', async ({ label, action }) => {
    const frame = approval({ approval_id: `settings-${action}`, args: { path: `${action}-requested.txt` } })
    const response: ToolApprovalResponse = { approval_id: frame.approval_id, action, status: 'ok' }
    net.respond('POST', `/api/v1/tool-approvals/${frame.approval_id}`, () => json(response))
    const { router, socket } = await mountStage1App(SETTINGS_PATH)
    await settledReads()
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
    expect(frame.session_id).toBe(MAIN_ID)
    expect(router.state.location.pathname).toBe('/settings')
    const modal = await showApproval(socket, frame)
    expect(screen.queryByTestId('chat-input')).toBeNull()
    await userEvent.setup().click(within(modal).getByRole('button', { name: label }))
    await waitFor(() => expect(net.writes()).toHaveLength(1))
    expect(net.writes().map(({ method, path, body, credentials }) => ({ method, path, body, credentials }))).toEqual([{
      method: 'POST', path: `/api/v1/tool-approvals/${frame.approval_id}`,
      body: { action }, credentials: 'include',
    }])
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Tool Approval Required' })).toBeNull())
    expect(useToolApprovalStore.getState().resolvedIds).toContain(frame.approval_id)
    expect(router.state.location.pathname).toBe('/settings')
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
  })
})

describe('stage 1 — every pending approval has exactly one actionable surface', () => {
  // CHECK mutant for S1-d: remove AppShell's ToolApprovalModal mount.
  // Additional narrower mutant: filter the modal queue by activeSessionId.
  it('[S1-d; dispatch 2(d)] a child approval without root_session_id appears while the parent main chat stays open, without a Go there detour', async () => {
    const { router, socket } = await mountStage1App()
    await settledMainChat(socket)
    const frame = approval({ session_id: 'helper-child', agent_id: 'gp', args: { path: 'child-requested.txt' } })
    expect(Object.hasOwn(frame, 'root_session_id')).toBe(false)
    await showApproval(socket, frame)
    expect(router.state.location.pathname).toBe(MAIN_PATH)
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
    expect(screen.queryByRole('button', { name: /go there/i, hidden: true })).toBeNull()
    expect(screen.queryByRole('link', { name: /go there/i, hidden: true })).toBeNull()
    expect(net.writes()).toEqual([])
  })

  // CHECK mutant for S1-e: featureSwitches.ts::INLINE_TOOL_APPROVALS_ENABLED = true.
  it('[S1-e; dispatch 2(e)] the open chat own approval appears once in the modal, never inline or in a banner', async () => {
    const { router, socket } = await mountStage1App()
    await settledMainChat(socket)
    await showApproval(socket)
    // hidden:true catches duplicate cards even if the modal's accessibility
    // isolation hides the chat subtree from ordinary role queries.
    expect(screen.getAllByRole('dialog', { hidden: true })).toHaveLength(1)
    expect(screen.getAllByRole('button', { name: 'Approve Once', hidden: true })).toHaveLength(1)
    expect(screen.queryByTestId('tool-approval-panel')).toBeNull()
    expect(screen.queryByTestId('tool-approval-thread-tail')).toBeNull()
    expect(screen.queryByTestId('cross-workspace-approval-banner')).toBeNull()
    expect(router.state.location.pathname).toBe(MAIN_PATH)
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
  })

  // CHECK mutant for S1-f: remove AppShell's ToolApprovalModal mount.
  // Additional narrower mutant: restore the release active-workspace queue filter.
  it('[S1-f; dispatch 2(f)] an approval from another workspace appears in the modal without switching the active workspace', async () => {
    net.workspaces = [workspace(), workspace({ id: 'marketing', name: 'Marketing', core_team: ['ray'], member_configs: { ray: { main_session_id: 'main-ray-marketing' } } })]
    const { router, socket } = await mountStage1App()
    await settledMainChat(socket)
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe(WS)
    const frame = approval({ workspace_id: 'marketing', session_id: 'main-ray-marketing', agent_id: 'ray', args: { path: 'marketing-requested.txt' } })
    await showApproval(socket, frame)
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe(WS)
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
    expect(router.state.location.pathname).toBe(MAIN_PATH)
    expect(screen.queryByTestId('cross-workspace-approval-banner')).toBeNull()
    expect(net.writes()).toEqual([])
  })
})

describe('stage 1 — hidden room traffic in the real main-chat renderer', () => {
  // CHECK mutant for all four cases: ROOMS_ENABLED=true; a narrower probe
  // removes the gate at the corresponding RoomTrafficMessage mount.
  it.each([
    { layout: 'plain', kind: 'request' },
    { layout: 'virtual', kind: 'request' },
    { layout: 'virtual', kind: 'streaming answer' },
    { layout: 'virtual', kind: 'completed answer' },
  ] as const)(
    '[S1-a traffic; added dispatch] a $layout $kind carrying room_ref renders no room-traffic entry',
    async ({ layout, kind }) => {
      if (layout === 'plain') vi.stubGlobal('ResizeObserver', undefined)
      else exposeTranscriptViewport()
      const { socket } = await mountStage1App()
      await settledMainChat(socket)
      await emitFromGateway(socket, { type: 'catch_up_complete', session_id: MAIN_ID, seq: 1, mode: 'snapshot' })
      const ordinary = 'Ordinary main-chat control.'
      await emitFromGateway(socket, { type: 'user_message', session_id: MAIN_ID, id: 'ordinary-stage1', timestamp: NOW, content: ordinary })
      expect(await screen.findByText(ordinary, { exact: true })).toBeVisible()
      const roomRef: RoomRef = { room_id: ROOM_ID, room_name: 'Launch', request_id: 'legacy-room-delivery', role: kind === 'request' ? 'request' : 'answer' }
      const content = 'Stage-one room traffic must not become an entry.'
      if (kind === 'request') {
        await emitFromGateway(socket, { type: 'user_message', session_id: MAIN_ID, id: 'room-stage1', timestamp: NOW, content, agent_id: 'mia', room_ref: roomRef })
      } else {
        await emitFromGateway(socket, { type: 'token', session_id: MAIN_ID, message_id: 'room-answer-stage1', turn_id: 'room-turn-stage1', content, agent_id: 'mia', room_ref: roomRef })
        if (kind === 'completed answer') {
          await emitFromGateway(socket, { type: 'done', session_id: MAIN_ID, message_id: 'room-answer-stage1', turn_id: 'room-turn-stage1', room_ref: roomRef })
        }
      }
      // Positive transport/store control: the tested carrier really arrived;
      // absence cannot pass because the fixture/parser dropped room_ref.
      await waitFor(() => {
        const messages = Object.values(useChatStore.getState().sessionsById[MAIN_ID]?.messagesById ?? {})
        expect(messages.find((message) => message.content === content)?.roomRef).toEqual(roomRef)
      })
      await settledReads()
      expect(screen.queryByTestId('room-traffic-entry')).toBeNull()
      expect(screen.queryByTestId('room-traffic-toggle')).toBeNull()
      expect(screen.queryByRole('link', { name: /open group/i, hidden: true })).toBeNull()
      expect(roomRequests()).toEqual([])
    },
  )
})

describe('stage 1 — hidden secret cards for live and snapshot carriers', () => {
  // CHECK mutant for BOTH cases: AGENT_PRIVATE_SECRETS_ENABLED=true;
  // narrower probe: remove its SecretRequestThreadTail mount guard.
  it.each(['live', 'session_state'] as const)(
    '[S1-b card; added dispatch] a %s secret request renders no card or secret endpoint request',
    async (carrier) => {
      const { socket } = await mountStage1App()
      await settledMainChat(socket)
      const frame: SecretRequestFrame = {
        type: 'secret_request', request_id: `stage1-secret-${carrier}`,
        session_id: 'secret-helper', root_session_id: MAIN_ID,
        acting_agent_id: 'mia', acting_agent_name: 'Mia', workspace_id: WS,
        owner: { workspace_id: WS, agent_id: 'mia' }, purpose: 'Legacy secret request',
        recipient: { kind: 'agent_private', lines: ['Sales / Mia', 'STAGE1_TOKEN'] },
        fields: [{ key: 'value', label: 'Stage-one token' }],
      }
      const { type: _type, ...payload } = frame
      void _type
      if (carrier === 'live') {
        await emitFromGateway(socket, frame)
      } else {
        const snapshot: SessionStateSecretRequest = { ...payload, state: 'pending', updated_at: NOW }
        const state: SessionStateFrame = { type: 'session_state', session_id: MAIN_ID, user_id: 'stage1-tester', emitted_at: NOW, pending_approvals: [], secret_requests: [snapshot] }
        await emitFromGateway(socket, state)
      }
      await waitFor(() => expect(useSecretRequestStore.getState().byId[frame.request_id]?.request.state).toBe('pending'))
      const { updated_at: _updated, ...received } = useSecretRequestStore.getState().byId[frame.request_id]!.request
      void _updated
      expect(received).toEqual({ ...payload, state: 'pending' })
      expect(screen.queryByTestId('secret-request-card')).toBeNull()
      expect(screen.queryByTestId('secret-request-thread-tail')).toBeNull()
      expect(screen.queryByLabelText('Stage-one token')).toBeNull()
      expect(net.requests.filter((request) => /^\/api\/v1\/(?:secrets|secret-requests)(?:\/|$)/.test(request.path))).toEqual([])
    },
  )
})

function visibleModalFor(frame: ToolApprovalRequiredFrame) {
  const modal = screen.getByRole('dialog', { name: 'Tool Approval Required' })
  expect(modal).toBeVisible()
  expect(modal).toHaveTextContent(String(frame.args.path))
  expect(screen.getAllByRole('dialog', { hidden: true })).toHaveLength(1)
  return modal
}

function replyToApproval(frame: ToolApprovalRequiredFrame, action: ToolApprovalActionRequest['action']) {
  const response: ToolApprovalResponse = { approval_id: frame.approval_id, action, status: 'ok' }
  net.respond('POST', `/api/v1/tool-approvals/${frame.approval_id}`, () => json(response))
}

describe('stage 1 — release modal drains every queued approval', () => {
  // CHECK mutant: remove AppShell's ToolApprovalModal mount; narrower probe:
  // filter the modal queue by active session, losing its child/other workspace.
  it('[S1-q; added dispatch] approve/deny/approve drains three real queued requests in arrival order without navigation', async () => {
    const { router, socket } = await mountStage1App(SETTINGS_PATH)
    const first = approval({ approval_id: 'queue-parent', args: { path: 'queue-parent.txt' } })
    const child = approval({ approval_id: 'queue-child', session_id: 'helper-child', agent_id: 'gp', args: { path: 'queue-child.txt' } })
    const other = approval({ approval_id: 'queue-other', workspace_id: 'marketing', session_id: 'main-ray-marketing', agent_id: 'ray', args: { path: 'queue-other.txt' } })
    replyToApproval(first, 'allow_once'); replyToApproval(child, 'deny'); replyToApproval(other, 'allow_once')
    await showApproval(socket, first)
    await emitFromGateway(socket, child)
    await emitFromGateway(socket, other)
    await waitFor(() => expect(useToolApprovalStore.getState().queue.map((entry) => entry.approvalId)).toEqual([first.approval_id, child.approval_id, other.approval_id]))
    visibleModalFor(first)
    expect(visibleModalFor(first)).not.toHaveTextContent('queue-child.txt')
    const user = userEvent.setup()
    await user.click(within(visibleModalFor(first)).getByRole('button', { name: 'Approve Once' }))
    await waitFor(() => expect(useToolApprovalStore.getState().queue.map((entry) => entry.approvalId)).toEqual([child.approval_id, other.approval_id]))
    await user.click(within(visibleModalFor(child)).getByRole('button', { name: 'Deny' }))
    await waitFor(() => expect(useToolApprovalStore.getState().queue.map((entry) => entry.approvalId)).toEqual([other.approval_id]))
    await user.click(within(visibleModalFor(other)).getByRole('button', { name: 'Approve Once' }))
    await waitFor(() => expect(useToolApprovalStore.getState().queue).toEqual([]))
    expect(net.writes().map(({ method, path, body }) => ({ method, path, body }))).toEqual([
      { method: 'POST', path: '/api/v1/tool-approvals/queue-parent', body: { action: 'allow_once' } },
      { method: 'POST', path: '/api/v1/tool-approvals/queue-child', body: { action: 'deny' } },
      { method: 'POST', path: '/api/v1/tool-approvals/queue-other', body: { action: 'allow_once' } },
    ])
    expect(useToolApprovalStore.getState().resolvedIds).toEqual([first.approval_id, child.approval_id, other.approval_id])
    expect(screen.queryByRole('dialog', { name: 'Tool Approval Required' })).toBeNull()
    expect(router.state.location.pathname).toBe('/settings')
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe(WS)
  })
})

describe('stage 1 — release modal expiry cannot approve a timed-out call', () => {
  // CHECK mutant: remove AppShell's modal mount; narrower probe: force
  // ToolApprovalModal.tsx::ToolApprovalCard hasExpired=false.
  // Oracle: added expiry requirement, ADR-092 D4 (no auto-submit; timeout is
  // denial) and asyncapi.yaml::ToolApprovalResolvedFrame (drop terminal entry).
  it('[S1-exp; added dispatch] at expiry actions disappear without a POST and denied_timeout promotes the next queued request', async () => {
    const { router, socket } = await mountStage1App(SETTINGS_PATH)
    await settledReads()
    // Clock edge only. Preserve real DOM/socket/query scheduling so fixture
    // delivery is not manufactured by mocked application helpers.
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] })
    vi.setSystemTime(new Date(NOW))
    const expiring = approval({ approval_id: 'expiry-first', expires_in_ms: 1000, args: { path: 'expiry-first.txt' } })
    const next = approval({ approval_id: 'expiry-next', args: { path: 'expiry-next.txt' } })
    await showApproval(socket, expiring)
    await emitFromGateway(socket, next)
    await waitFor(() => expect(useToolApprovalStore.getState().queue.map((entry) => entry.approvalId)).toEqual([expiring.approval_id, next.approval_id]))
    act(() => { vi.advanceTimersByTime(999) })
    expect(within(visibleModalFor(expiring)).getByRole('button', { name: 'Approve Once' })).toBeEnabled()
    act(() => { vi.advanceTimersByTime(1) })
    const expired = visibleModalFor(expiring)
    for (const name of ['Approve Once', 'Deny', 'Always Allow', 'Cancel']) {
      expect(within(expired).queryByRole('button', { name })).toBeNull()
    }
    expect(net.writes()).toEqual([])
    await emitFromGateway(socket, { type: 'tool_approval_resolved', approval_id: expiring.approval_id, state: 'denied_timeout', session_id: MAIN_ID })
    await waitFor(() => expect(useToolApprovalStore.getState().queue.map((entry) => entry.approvalId)).toEqual([next.approval_id]))
    expect(useToolApprovalStore.getState().resolvedIds).toContain(expiring.approval_id)
    expect(within(visibleModalFor(next)).getByRole('button', { name: 'Approve Once' })).toBeEnabled()
    expect(within(visibleModalFor(next)).getByRole('button', { name: 'Deny' })).toBeEnabled()
    expect(net.writes()).toEqual([])
    expect(router.state.location.pathname).toBe('/settings')
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
    vi.useRealTimers()
  })

  // CHECK mutant: remove AppShell's modal mount, or force hasExpired=false.
  it('[S1-exp zero; added dispatch] a zero-lifetime request is already non-actionable on arrival and never submits automatically', async () => {
    const { socket } = await mountStage1App(SETTINGS_PATH)
    const frame = approval({ approval_id: 'expired-on-arrival', expires_in_ms: 0, args: { path: 'expired-on-arrival.txt' } })
    await emitFromGateway(socket, frame)
    await waitFor(() => expect(useToolApprovalStore.getState().queue.map((entry) => entry.approvalId)).toEqual([frame.approval_id]))
    const modal = await screen.findByRole('dialog', { name: 'Tool Approval Required' })
    expect(modal).toBeVisible()
    expect(modal).toHaveTextContent('expired-on-arrival.txt')
    for (const name of ['Approve Once', 'Deny', 'Always Allow', 'Cancel']) {
      expect(within(modal).queryByRole('button', { name })).toBeNull()
    }
    const dismiss = within(modal).getByRole('button', { name: 'Dismiss' })
    expect(dismiss).toBeEnabled()
    await userEvent.setup().click(dismiss)
    await waitFor(() => expect(useToolApprovalStore.getState().queue).toEqual([]))
    expect(screen.queryByRole('dialog', { name: 'Tool Approval Required' })).toBeNull()
    expect(net.writes()).toEqual([])
  })
})
