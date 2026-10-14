// Acceptance source: team-lead's 2026-10-14 stage-1 dispatch, items 2(a)–(f).
// REAL production route tree, AppShell, sidebar/chat/profile/modal, stores,
// generated API clients and socket dispatcher. No feature-switch mocks.
// GREEN and the exact mutations listed per case are deferred to fresh CHECK.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ToolApprovalActionRequest, ToolApprovalResponse } from '@/lib/api/generated/openapi-types'
import { queryClient } from '@/lib/queryClient'
import { useSessionStore } from '@/store/session'
import { useToolApprovalStore } from '@/store/toolApproval'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { json, MAIN_ID, ROOM_ID, workspace, WS } from '../../../tests/l1-room-ui/harness'
import {
  approval, emitFromGateway, MAIN_PATH, mountStage1App, resetStage1App,
  SETTINGS_PATH, settledMainChat, Stage1Network, stopStage1App,
} from '../../../tests/l1-room-ui/stage1-harness'

let net: Stage1Network
beforeEach(() => { resetStage1App(); net = new Stage1Network() })
afterEach(async () => {
  try {
    expect(net.unexpected, 'the real app must not fail its setup or hide an unexpected network error').toEqual([])
  } finally {
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
