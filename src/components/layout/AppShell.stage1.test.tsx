// Acceptance source: team-lead's 2026-10-14 stage-1 dispatch, items 2(a)–(f).
// SUPERSESSION (Train 3, amendment section 1 / bundle B3 + founder ruling
// 2026-10-18): approval pins are re-aimed from the app-wide modal to the
// inline card in the root chat; no case asserts the cross-workspace banner
// either way beyond the founder's "no Go-there notice" rule — see the note
// above showApproval.
// REAL production route tree, AppShell, sidebar/chat/profile/modal, stores,
// generated API clients and socket dispatcher. No feature-switch mocks.
// GREEN and the exact mutations listed per case are deferred to fresh CHECK.
//
// SUPERSESSION (founder decision 2026-10-18, Train 3 re-cut, recorded in
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/PLAN-omnipus-L1-v2-20261013.md
// line 143: "WU-15 rooms + room screens, rooms switched on"): every room pin
// below is re-aimed from the 2026-10-14 "hidden/unreachable under the off
// switch" pole to the matching switched-on pole at the same assertion
// strength (S1-a1, S1-a2, S1-a3, S1-a traffic ×4). The secrets and approval
// pins keep the 2026-10-14 stage-1 oracles unchanged.
import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { SecretRequestState, ToolApprovalActionRequest, ToolApprovalResponse } from '@/lib/api/generated/openapi-types'
import type { RoomRef, SecretRequestFrame, SessionStateFrame, SessionStateSecretRequest, ToolApprovalRequiredFrame } from '@/lib/api/generated/asyncapi-types'
import { queryClient } from '@/lib/queryClient'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { useSecretRequestStore } from '@/store/secretRequests'
import { useToolApprovalStore } from '@/store/toolApproval'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { json, MAIN_ID, NOW, ROOM_ID, snapshot, workspace, WS } from '../../../tests/l1-room-ui/harness'
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

// SUPERSESSION (Train 3, amendment section 1 / bundle B3): INLINE_TOOL_APPROVALS_ENABLED
// ships ON — the approval card shows INLINE in its root chat (ToolApprovalThreadTail
// above the composer) instead of the app-wide ToolApprovalModal, and a reconnect
// snapshot restores the full approvable card. Every modal-path S1 case below is
// re-aimed to the inline-card equivalent at the same strength. Whether the
// cross-workspace "N approvals waiting — Go there" banner ships is an OPEN founder
// decision: no case asserts its presence or absence; a clearly marked TODO marks
// where a banner assertion would go.
async function showApproval(socket: Parameters<typeof emitFromGateway>[0], frame = approval()) {
  expect(screen.queryAllByRole('dialog', { hidden: true })).toHaveLength(0)
  await emitFromGateway(socket, frame)
  await waitFor(() => expect(useToolApprovalStore.getState().queue.map((entry) => entry.approvalId)).toEqual([frame.approval_id]))
  // The card is the inline thread tail in the root chat — never the modal.
  const card = await screen.findByTestId('tool-approval-thread-tail')
  expect(card).toBeVisible()
  const panel = within(card).getByTestId('tool-approval-panel')
  expect(within(panel).getByRole('button', { name: 'Approve Once' })).toBeEnabled()
  expect(within(panel).getByRole('button', { name: 'Deny' })).toBeEnabled()
  expect(panel).toHaveTextContent(String(frame.args.path))
  expect(screen.queryByRole('dialog', { name: 'Tool Approval Required' })).toBeNull()
  return panel
}

describe('stage 1 — rooms switched on through the real application shell', () => {
  // SUPERSEDED pin, re-aimed per the founder's 2026-10-18 Train 3 decision
  // "rooms switched on" (see the file header): the 2026-10-14 stage-1 pin
  // asserted the Group chats section absent and the room collection unread.
  // The switched-on pin (FR-160, FR-161, FR-174; WorkspaceGroupChats.tsx)
  // asserts the section and its room row VISIBLE and the room-list read
  // issued exactly once.
  // CHECK mutant for S1-a1: featureSwitches.ts::ROOMS_ENABLED = false.
  it('[S1-a1; dispatch 2(a)] a populated expanded workspace shows its Group chats section and reads its room collection exactly once', async () => {
    const { router } = await mountStage1App(SETTINGS_PATH)
    const workspaces = screen.getByRole('group', { name: 'Workspaces' })
    expect(within(workspaces).getByRole('button', { name: 'Sales' })).toBeVisible()
    expect(within(workspaces).getByRole('button', { name: 'Mia' })).toBeVisible()
    expect(within(workspaces).getByRole('button', { name: 'Jim' })).toBeVisible()
    await settledReads()
    expect(router.state.location.pathname).toBe('/settings')
    const groupChats = screen.getByTestId(`group-chats-${WS}`)
    expect(groupChats).toBeVisible()
    expect(within(groupChats).getByText('Group chats', { exact: true })).toBeVisible()
    // The harness squad room (hidden: false) lists under the section per FR-174.
    expect(within(groupChats).getByTestId(`room-row-${ROOM_ID}`)).toBeVisible()
    expect(roomRequests().map((request) => `${request.method} ${request.path}`)).toEqual([
      `GET /api/v1/workspaces/${WS}/rooms`,
    ])
  })

  // SUPERSEDED pin, re-aimed per the founder's 2026-10-18 Train 3 decision
  // "rooms switched on" (see the file header): the 2026-10-14 stage-1 pin
  // asserted no Add participant entry point. The switched-on pin asserts the
  // entry point VISIBLE in the member main chat (AddParticipant.tsx), while
  // the chat itself still issues no attach_room and no room-detail read — the
  // sidebar's own single room-list read is the only room traffic.
  // CHECK mutant for S1-a2: featureSwitches.ts::ROOMS_ENABLED = false.
  it('[S1-a2; dispatch 2(a)] a member main chat shows the Add participant entry point without any room attach or room-detail read', async () => {
    const { router, socket } = await mountStage1App()
    await settledMainChat(socket)
    expect(screen.getByTestId('chat-input')).toBeVisible()
    expect(useSessionStore.getState().activeAgentId).toBe('mia')
    await settledReads()
    expect(router.state.location.pathname).toBe(MAIN_PATH)
    expect(screen.getByTestId('add-participant')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Add participant' })).toBeVisible()
    expect(roomRequests().map((request) => `${request.method} ${request.path}`)).toEqual([
      `GET /api/v1/workspaces/${WS}/rooms`,
    ])
    expect(socket.sent.filter((frame) => frame.type === 'attach_room')).toEqual([])
  })

  // SUPERSEDED pin, re-aimed per the founder's 2026-10-18 Train 3 decision
  // "rooms switched on" (see the file header): the 2026-10-14 stage-1 pin
  // asserted the direct room URL redirects to workspace chat. The switched-on
  // pin (FR-175, FR-199; K45 — the route "attaches the room only") asserts the
  // room screen mounts at the SAME URL, attaches ITS room, reads the room, and
  // renders from a real room_snapshot.
  // CHECK mutant for S1-a3: featureSwitches.ts::ROOMS_ENABLED = false.
  it('[S1-a3; dispatch 2(a)] a direct room URL mounts the room, attaches it and reads it without redirecting', async () => {
    const roomPath = `/workspaces/${WS}/rooms/${ROOM_ID}`
    const { router, socket } = await mountStage1App(roomPath)
    await settledReads()
    expect(router.state.location.pathname).toBe(roomPath)
    expect(roomRequests().map((request) => `${request.method} ${request.path}`)).toContain(`GET /api/v1/rooms/${ROOM_ID}`)
    expect(roomRequests().filter((request) => request.method !== 'GET')).toEqual([])
    await waitFor(() => {
      if (!socket.sent.some((frame) => frame.type === 'attach_room' && frame.room_id === ROOM_ID)) {
        throw new Error('RoomScreen has not attached its room on the hub connection')
      }
    })
    // The screen renders from a real snapshot, not from the REST read alone.
    await emitFromGateway(socket, snapshot())
    expect(await screen.findByTestId('room-header')).toBeVisible()
  })
})

describe('WU-5 — private secrets reachable through the shell-owned agent profile', () => {
  // Founder 2026-10-14 rule: the switch hid this UI only until its work unit
  // landed; WU-5 is in Train 2, so AGENT_PRIVATE_SECRETS_ENABLED is true and
  // the section renders for a conversation agent (architect F02, Train 2 gate).
  // CHECK mutant for S1-b: featureSwitches.ts::AGENT_PRIVATE_SECRETS_ENABLED = false.
  it('[S1-b; WU-5] a conversation agent Tools panel shows the Private secrets section and reads it through the authenticated endpoint', async () => {
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
    // WU-5 reachable: the section mounts and its list read went through the
    // real API client (the harness serves GET /api/v1/secrets deliberately so
    // this read is observable). Render-only: no secret write is issued.
    expect(within(panel).getByTestId('agent-private-secrets')).toHaveTextContent('No private secrets')
    expect(net.requests.filter((request) => request.method === 'GET' && /^\/api\/v1\/secrets(?:\/|$)/.test(request.path))).toHaveLength(1)
    expect(net.writes()).toEqual([])
  })
})

// Contract-derived actions, not legacy booleans: ToolApprovalActionRequest.yaml.
const settingsActions = [
  { label: 'Approve Once', action: 'allow_once' },
  { label: 'Deny', action: 'deny' },
] as const satisfies ReadonlyArray<{ label: string; action: ToolApprovalActionRequest['action'] }>

describe('stage 1 — the inline approval card is actionable in the root chat (bundle B3)', () => {
  // CHECK mutant for BOTH S1-c cases: remove ChatScreen's ToolApprovalThreadTail
  // mount (the inline card), or re-gate the ChatScreen.tsx inline mount on the switch.
  it.each(settingsActions)('[S1-c; dispatch 2(c)] $label on the card in the root chat posts the exact $action decision for the active chat approval', async ({ label, action }) => {
    const frame = approval({ approval_id: `settings-${action}`, args: { path: `${action}-requested.txt` } })
    const response: ToolApprovalResponse = { approval_id: frame.approval_id, action, status: 'ok' }
    net.respond('POST', `/api/v1/tool-approvals/${frame.approval_id}`, () => json(response))
    // Bundle B3: the card lives in the root chat, so the case now acts from
    // the open main chat (the approval's own chat) instead of Settings.
    const { router, socket } = await mountStage1App()
    await settledMainChat(socket)
    await settledReads()
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
    expect(frame.session_id).toBe(MAIN_ID)
    expect(screen.getByTestId('chat-input')).toBeVisible()
    const panel = await showApproval(socket, frame)
    await userEvent.setup().click(within(panel).getByRole('button', { name: label }))
    await waitFor(() => expect(net.writes()).toHaveLength(1))
    expect(net.writes().map(({ method, path, body, credentials }) => ({ method, path, body, credentials }))).toEqual([{
      method: 'POST', path: `/api/v1/tool-approvals/${frame.approval_id}`,
      body: { action }, credentials: 'include',
    }])
    await waitFor(() => expect(useToolApprovalStore.getState().queue).toEqual([]))
    expect(useToolApprovalStore.getState().resolvedIds).toContain(frame.approval_id)
    expect(router.state.location.pathname).toBe(MAIN_PATH)
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
  })
})

describe('stage 1 — every pending approval has exactly one actionable surface', () => {
  // CHECK mutant for S1-d: remove ChatScreen's ToolApprovalThreadTail mount.
  // Additional narrower mutant: filter the approval queue by activeSessionId.
  // Q4=A (team-lead, on K7 + U11 N2): a frame carrying root_session_id routes
  // to that root chat (toolApproval.ts::approvalChatId), and a main's
  // needs_attention is SERVER truth — the SPA refetches the roster on every
  // approval frame and reads it; the frame itself is never matched to mains.
  it('[S1-d; dispatch 2(d)] a root-routed helper approval appears on the card in the parent main chat, which stays open, without a Go there detour — and the server-flagged attention cue comes on', async () => {
    const { router, socket } = await mountStage1App()
    await settledMainChat(socket)
    // The helper frame routes to the MAIN (root_session_id), so its card
    // shows in the main's own chat.
    const frame = approval({ session_id: 'helper-child', agent_id: 'gp', root_session_id: MAIN_ID, args: { path: 'child-requested.txt' } })
    // Server truth: the pending helper approval flagged the main.
    net.mainNeedsAttention = true
    await showApproval(socket, frame)
    expect(router.state.location.pathname).toBe(MAIN_PATH)
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
    // Founder ruling 2026-10-18: "when it appears the attention mechanisms
    // must be triggered and the main agent entry in the sidebar must be
    // triggered" — the roster refetch reads the server's needs_attention and
    // the existing sidebar rendering responds: with the workspace expanded,
    // the main agent's row icon carries the warning attention halo (the
    // collapsed workspace shows the count cue instead).
    const salesGroup = screen.getByRole('group', { name: 'Sales' })
    const miaRow = within(salesGroup).getByRole('button', { name: 'Mia' })
    expect(
      miaRow.querySelector('[data-attention-halo="warning"]'),
      'the main agent entry must show the attention halo',
    ).not.toBeNull()
    expect(screen.queryByRole('button', { name: /go there/i, hidden: true })).toBeNull()
    expect(screen.queryByRole('link', { name: /go there/i, hidden: true })).toBeNull()
    expect(net.writes()).toEqual([])
  })

  // CHECK mutant for S1-e: re-gate the ChatScreen inline mount, or restore
  // AppShell's ToolApprovalModal mount.
  it('[S1-e; dispatch 2(e)] the open chat own approval appears exactly once, inline in the root chat — never the app-wide modal', async () => {
    const { router, socket } = await mountStage1App()
    await settledMainChat(socket)
    await showApproval(socket)
    // Exactly ONE card, rendered once, with one primary action — even counting
    // hidden nodes, so a second host cannot hide behind accessibility isolation.
    expect(screen.queryAllByTestId('tool-approval-thread-tail')).toHaveLength(1)
    expect(screen.queryAllByRole('button', { name: 'Approve Once', hidden: true })).toHaveLength(1)
    // The app-wide modal is gone with the inline switch (bundle B3).
    expect(screen.queryByRole('dialog', { name: 'Tool Approval Required' })).toBeNull()
    // Founder ruling 2026-10-18: no "N approvals waiting — Go there" notice —
    // attention mechanisms, not a navigation banner. (Own-chat approval: the
    // cross-workspace banner has nothing to report either way.)
    expect(screen.queryByTestId('cross-workspace-approval-banner')).toBeNull()
    expect(screen.queryByText(/approvals? waiting/i)).toBeNull()
    expect(router.state.location.pathname).toBe(MAIN_PATH)
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
  })

  // CHECK mutant for S1-f: broaden the card's chat filter to all workspaces
  // (it must stay scoped), or restore the Go-there banner.
  it('[S1-f; dispatch 2(f)] an approval from another workspace stays OUT of the open chat\'s card — queue holds it, workspace and session unchanged, and no Go-there banner appears', async () => {
    net.workspaces = [workspace(), workspace({ id: 'marketing', name: 'Marketing', core_team: ['ray'], member_configs: { ray: { main_session_id: 'main-ray-marketing' } } })]
    const { router, socket } = await mountStage1App()
    await settledMainChat(socket)
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe(WS)
    const frame = approval({ workspace_id: 'marketing', session_id: 'main-ray-marketing', agent_id: 'ray', args: { path: 'marketing-requested.txt' } })
    await emitFromGateway(socket, frame)
    await waitFor(() => expect(useToolApprovalStore.getState().queue.map((entry) => entry.approvalId)).toEqual([frame.approval_id]))
    // The card is chat-scoped (bundle B3): another chat's approval never
    // renders in the open chat.
    expect(screen.queryByTestId('tool-approval-panel')).toBeNull()
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe(WS)
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
    expect(router.state.location.pathname).toBe(MAIN_PATH)
    // Founder ruling 2026-10-18: NO CrossWorkspaceApprovalBanner / "N approvals
    // waiting in … — Go there" notice while an approval pends in another chat —
    // the card in its own chat plus the attention cues carry it instead.
    // RED-FOR-THE-FOLLOW-UP on 29f78ee56: the banner mount still renders for a
    // cross-workspace approval; the follow-up commit removes it.
    expect(screen.queryByTestId('cross-workspace-approval-banner')).toBeNull()
    expect(screen.queryByText(/approvals? waiting/i)).toBeNull()
    expect(screen.queryByText(/go there/i)).toBeNull()
    expect(net.writes()).toEqual([])
  })
})

describe('stage 1 — room traffic rendered in the real main-chat renderer', () => {
  // SUPERSEDED pins, re-aimed per the founder's 2026-10-18 Train 3 decision
  // "rooms switched on" (see the file header): the 2026-10-14 stage-1 pins
  // asserted a room_ref carrier renders nothing. The switched-on pins
  // (FR-162; RoomTrafficEntry.tsx) assert exactly ONE collapsed entry with its
  // disclosure toggle visible, which expands to the "Open group" link — while
  // the entry itself still issues no room REST read.
  // CHECK mutant for all four cases: ROOMS_ENABLED=false; a narrower probe
  // removes the gate at the corresponding RoomTrafficMessage mount.
  it.each([
    { layout: 'plain', kind: 'request' },
    { layout: 'virtual', kind: 'request' },
    { layout: 'virtual', kind: 'streaming answer' },
    { layout: 'virtual', kind: 'completed answer' },
  ] as const)(
    '[S1-a traffic; added dispatch] a $layout $kind carrying room_ref renders its room-traffic entry',
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
      // presence cannot pass because the fixture/parser dropped room_ref.
      await waitFor(() => {
        const messages = Object.values(useChatStore.getState().sessionsById[MAIN_ID]?.messagesById ?? {})
        expect(messages.find((message) => message.content === content)?.roomRef).toEqual(roomRef)
      })
      await settledReads()
      const entries = screen.getAllByTestId('room-traffic-entry')
      expect(entries).toHaveLength(1)
      expect(entries[0]).toBeVisible()
      const toggle = screen.getByTestId('room-traffic-toggle')
      expect(toggle).toBeVisible()
      await userEvent.setup().click(toggle)
      expect(await screen.findByRole('link', { name: 'Open group Launch' })).toBeVisible()
      expect(roomRequests().map((request) => `${request.method} ${request.path}`)).toEqual([
        `GET /api/v1/workspaces/${WS}/rooms`,
      ])
    },
  )
})

describe('WU-4 — secret cards enabled independently of the WU-5 private list', () => {
  // WU-4 screen dispatch supersedes only the old hidden-card requirement.
  // CHECK mutant for BOTH cases: SECRET_CARDS_ENABLED=false, restore the
  // AGENT_PRIVATE_SECRETS_ENABLED guard, or remove the tail mount.
  it.each(['live', 'session_state'] as const)(
    '[WU-4 card] a %s secret request renders in its root chat and submits through the authenticated endpoint',
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
      const card = screen.getByTestId('secret-request-card')
      expect(card).toBeVisible()
      expect(screen.getByTestId('secret-request-thread-tail')).toContainElement(card)
      expect(card).toHaveAttribute('data-state', 'pending')
      expect(within(card).getByTestId('secret-request-asker')).toHaveTextContent('Mia needs a secret')
      expect(within(card).getByTestId('secret-request-purpose')).toHaveTextContent('Legacy secret request')
      expect(within(card).getByTestId('secret-request-recipient')).toHaveTextContent('STAGE1_TOKEN')
      const input = within(card).getByLabelText(/^Stage-one token(?: \*)?$/)
      expect(input).toHaveAccessibleName('Stage-one token')
      expect(input).toHaveAttribute('type', 'password')
      expect(net.requests.filter((request) => /^\/api\/v1\/(?:secrets|secret-requests)(?:\/|$)/.test(request.path))).toEqual([])

      const saved: SecretRequestState = {
        request_id: frame.request_id, state: 'submitted', reference_names: ['stage1_token.v1'], updated_at: NOW,
      }
      let acceptSubmit!: (response: Response) => void
      const response = new Promise<Response>((resolve) => { acceptSubmit = resolve })
      net.respond('POST', `/api/v1/secret-requests/${frame.request_id}/submit`, () => response)
      const user = userEvent.setup()
      // Synthetic value, never a real credential. The request is the only
      // permitted sink; the real request/store/card path is not mocked.
      const value = 'wu4-card-submit-sentinel'
      await user.type(input, value)
      const save = within(card).getByRole('button', { name: 'Save secret' })
      await user.click(save)
      expect(input).toHaveValue('')
      expect(save).toBeDisabled()
      expect(card).toHaveAttribute('data-state', 'submitting')
      expect(within(card).getByRole('status')).toHaveTextContent('Submitting…')
      expect(net.writes().map(({ method, path, body, credentials }) => ({ method, path, body, credentials }))).toEqual([{
        method: 'POST', path: `/api/v1/secret-requests/${frame.request_id}/submit`,
        body: { values: { value } }, credentials: 'include',
      }])
      expect(net.writes()[0]!.headers.get('X-CSRF-Token')).toBe('room-red-test')
      await user.click(save)
      expect(net.writes()).toHaveLength(1)
      await act(async () => { acceptSubmit(json(saved)) })
      await waitFor(() => expect(card).toHaveAttribute('data-state', 'submitted'))
      expect(within(card).getByRole('status')).toHaveTextContent(/^Saved · stored as stage1_token.v1$/)
      expect(within(card).queryByLabelText(/^Stage-one token/)).toBeNull()
      expect(input).toHaveValue('')
      expect(JSON.stringify(useSecretRequestStore.getState())).not.toContain(value)
      expect(net.requests.filter((request) => /^\/api\/v1\/secrets(?:\/|$)/.test(request.path))).toEqual([])
    },
  )
})

function visibleCardFor(frame: ToolApprovalRequiredFrame) {
  // Bundle B3: the queued head renders on the inline card in the root chat.
  const card = screen.getByTestId('tool-approval-thread-tail')
  expect(card).toBeVisible()
  const panel = within(card).getByTestId('tool-approval-panel')
  expect(panel).toHaveTextContent(String(frame.args.path))
  expect(screen.queryAllByRole('dialog', { hidden: true })).toEqual([])
  return panel
}

function replyToApproval(frame: ToolApprovalRequiredFrame, action: ToolApprovalActionRequest['action']) {
  const response: ToolApprovalResponse = { approval_id: frame.approval_id, action, status: 'ok' }
  net.respond('POST', `/api/v1/tool-approvals/${frame.approval_id}`, () => json(response))
}

describe('stage 1 — the inline card drains its chat\'s queued approvals (bundle B3)', () => {
  // CHECK mutant: remove ChatScreen's ToolApprovalThreadTail mount; narrower
  // probe: filter the approval queue by active session, losing the arrival
  // order. Bundle B3 + founder ruling 2026-10-18: the card is chat-scoped —
  // it drains THIS chat's approvals in arrival order; another chat's approval
  // never hijacks the card (its own chat + the attention cues carry it, and
  // there is no Go-there banner).
  it('[S1-q; added dispatch] approve/deny drains two same-chat queued requests in arrival order on the inline card, while another chat\'s approval waits its turn, without navigation', async () => {
    const { router, socket } = await mountStage1App()
    await settledMainChat(socket)
    const first = approval({ approval_id: 'queue-parent', args: { path: 'queue-parent.txt' } })
    const second = approval({ approval_id: 'queue-second', agent_id: 'gp', args: { path: 'queue-second.txt' } })
    const elsewhere = approval({ approval_id: 'queue-elsewhere', workspace_id: 'marketing', session_id: 'main-ray-marketing', agent_id: 'ray', args: { path: 'queue-elsewhere.txt' } })
    replyToApproval(first, 'allow_once'); replyToApproval(second, 'deny'); replyToApproval(elsewhere, 'allow_once')
    await showApproval(socket, first)
    await emitFromGateway(socket, second)
    await emitFromGateway(socket, elsewhere)
    await waitFor(() => expect(useToolApprovalStore.getState().queue.map((entry) => entry.approvalId)).toEqual([first.approval_id, second.approval_id, elsewhere.approval_id]))
    visibleCardFor(first)
    expect(visibleCardFor(first)).not.toHaveTextContent('queue-second.txt')
    expect(visibleCardFor(first)).not.toHaveTextContent('queue-elsewhere.txt')
    const user = userEvent.setup()
    await user.click(within(visibleCardFor(first)).getByRole('button', { name: 'Approve Once' }))
    await waitFor(() => expect(useToolApprovalStore.getState().queue.map((entry) => entry.approvalId)).toEqual([second.approval_id, elsewhere.approval_id]))
    // The next SAME-CHAT approval takes the card, in arrival order.
    expect(visibleCardFor(second)).toHaveTextContent('queue-second.txt')
    await user.click(within(visibleCardFor(second)).getByRole('button', { name: 'Deny' }))
    await waitFor(() => expect(useToolApprovalStore.getState().queue.map((entry) => entry.approvalId)).toEqual([elsewhere.approval_id]))
    // The other chat's approval is still pending — and it does NOT steal the
    // open chat's card (the card unmounts when this chat has nothing to show).
    expect(screen.queryByTestId('tool-approval-panel')).toBeNull()
    expect(useToolApprovalStore.getState().queue).toHaveLength(1)
    // Founder ruling 2026-10-18: no "N approvals waiting — Go there" notice.
    // RED-FOR-THE-FOLLOW-UP on 29f78ee56: the banner mount still renders.
    expect(screen.queryByTestId('cross-workspace-approval-banner')).toBeNull()
    expect(screen.queryByText(/approvals? waiting/i)).toBeNull()
    expect(net.writes().map(({ method, path, body }) => ({ method, path, body }))).toEqual([
      { method: 'POST', path: '/api/v1/tool-approvals/queue-parent', body: { action: 'allow_once' } },
      { method: 'POST', path: '/api/v1/tool-approvals/queue-second', body: { action: 'deny' } },
    ])
    expect(useToolApprovalStore.getState().resolvedIds).toEqual([first.approval_id, second.approval_id])
    expect(screen.queryByRole('dialog', { name: 'Tool Approval Required' })).toBeNull()
    expect(router.state.location.pathname).toBe(MAIN_PATH)
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe(WS)
  })
})

describe('stage 1 — the inline card expiry cannot approve a timed-out call (bundle B3)', () => {
  // CHECK mutant: remove ChatScreen's ToolApprovalThreadTail mount; narrower
  // probe: force ToolApprovalPanel's hasExpired=false.
  // Oracle: added expiry requirement, ADR-092 D4 (no auto-submit; timeout is
  // denial) and asyncapi.yaml::ToolApprovalResolvedFrame (drop terminal entry).
  it('[S1-exp; added dispatch] at expiry the card actions disappear without a POST and denied_timeout promotes the next queued request', async () => {
    const { router, socket } = await mountStage1App()
    await settledMainChat(socket)
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
    expect(within(visibleCardFor(expiring)).getByRole('button', { name: 'Approve Once' })).toBeEnabled()
    act(() => { vi.advanceTimersByTime(1) })
    const expired = visibleCardFor(expiring)
    for (const name of ['Approve Once', 'Deny', 'Always Allow', 'Cancel']) {
      expect(within(expired).queryByRole('button', { name })).toBeNull()
    }
    expect(net.writes()).toEqual([])
    await emitFromGateway(socket, { type: 'tool_approval_resolved', approval_id: expiring.approval_id, state: 'denied_timeout', session_id: MAIN_ID })
    await waitFor(() => expect(useToolApprovalStore.getState().queue.map((entry) => entry.approvalId)).toEqual([next.approval_id]))
    expect(useToolApprovalStore.getState().resolvedIds).toContain(expiring.approval_id)
    expect(within(visibleCardFor(next)).getByRole('button', { name: 'Approve Once' })).toBeEnabled()
    expect(within(visibleCardFor(next)).getByRole('button', { name: 'Deny' })).toBeEnabled()
    expect(net.writes()).toEqual([])
    expect(router.state.location.pathname).toBe(MAIN_PATH)
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
    vi.useRealTimers()
  })

  // CHECK mutant: remove ChatScreen's ToolApprovalThreadTail mount, or force
  // hasExpired=false.
  it('[S1-exp zero; added dispatch] a zero-lifetime request is already non-actionable on arrival and never submits automatically', async () => {
    const { socket } = await mountStage1App()
    await settledMainChat(socket)
    const frame = approval({ approval_id: 'expired-on-arrival', expires_in_ms: 0, args: { path: 'expired-on-arrival.txt' } })
    await emitFromGateway(socket, frame)
    await waitFor(() => expect(useToolApprovalStore.getState().queue.map((entry) => entry.approvalId)).toEqual([frame.approval_id]))
    const card = await screen.findByTestId('tool-approval-thread-tail')
    expect(card).toBeVisible()
    const panel = within(card).getByTestId('tool-approval-panel')
    expect(panel).toHaveTextContent('expired-on-arrival.txt')
    for (const name of ['Approve Once', 'Deny', 'Always Allow', 'Cancel']) {
      expect(within(panel).queryByRole('button', { name })).toBeNull()
    }
    const dismiss = within(panel).getByRole('button', { name: 'Dismiss' })
    expect(dismiss).toBeEnabled()
    await userEvent.setup().click(dismiss)
    await waitFor(() => expect(useToolApprovalStore.getState().queue).toEqual([]))
    // The docked tail div is unconditional; the CARD inside it unmounts.
    expect(screen.queryByTestId('tool-approval-panel')).toBeNull()
    expect(screen.queryByRole('dialog', { name: 'Tool Approval Required' })).toBeNull()
    expect(net.writes()).toEqual([])
  })
})
