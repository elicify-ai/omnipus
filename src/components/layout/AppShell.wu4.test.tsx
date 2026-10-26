// WU-4 screen dispatch; omnipus-system-agent K1/K3/K4/K5/K7, FR-074/079/251,
// §6.18. Real route tree, AppShell, ChatScreen, the inline approval card,
// stores and transport. Only the HTTP/socket/browser edges are doubled by the
// existing harness.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type {
  SecretRequestFrame, SecretRequestResolvedFrame, ServerFrame, SessionSnapshotFrame,
  SessionStateFrame, SessionStateSecretRequest,
} from '@/lib/api/generated/asyncapi-types'
import { parseFrameSafe } from '@/lib/ws'
import { queryClient } from '@/lib/queryClient'
import { useSecretRequestStore } from '@/store/secretRequests'
import { useToolApprovalStore } from '@/store/toolApproval'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { json, MAIN_ID, NOW, WS } from '../../../tests/l1-room-ui/harness'
import {
  approval, emitFromGateway, MAIN_PATH, mountStage1App, resetStage1App,
  settledMainChat, Stage1Network, stopStage1App,
} from '../../../tests/l1-room-ui/stage1-harness'

let net: Stage1Network
beforeEach(() => { resetStage1App(); net = new Stage1Network() })
afterEach(async () => {
  try {
    expect(net.unexpected, 'the real application must not hide a setup or transport failure').toEqual([])
  } finally {
    await stopStage1App()
  }
})

function secret(): SecretRequestFrame {
  return {
    type: 'secret_request', request_id: 'wu4-secret', session_id: 'setup-helper', root_session_id: MAIN_ID,
    acting_agent_id: 'mia', acting_agent_name: 'Setup helper', origin_label: 'asked by Omnipus',
    workspace_id: WS, purpose: 'Connect the reporting provider',
    recipient: { kind: 'provider_key', lines: ['Reporting provider', 'https://reports.example.test'] },
    fields: [{ key: 'api_key', label: 'Reporting API key' }],
  }
}

// Expected visible copy is specified by the card states in §6.3 / FR-074,
// not read from the implementation. K3 cannot carry stored reference names.
const terminalStates = [
  { state: 'submitted', message: 'Saved' },
  { state: 'cancelled', message: 'Cancelled — nothing was stored.' },
  { state: 'stopped', message: 'Stopped — nothing was stored.' },
  { state: 'interrupted', message: 'Request unavailable — the gateway restarted before anything was stored. The agent can ask again.' },
  { state: 'refused', message: 'Refused — nothing was stored.' },
] as const satisfies ReadonlyArray<{ state: SecretRequestResolvedFrame['state']; message: string }>

function secretSnapshot(state: SessionStateSecretRequest['state']): SessionStateSecretRequest {
  const { type: _type, ...payload } = secret()
  void _type
  return {
    ...payload, state, updated_at: NOW,
    ...(state === 'submitted' ? { reference_names: ['reporting_key.v1'] } : {}),
  }
}

async function emitSecretSnapshot(
  socket: Parameters<typeof emitFromGateway>[0],
  carrier: 'session_state' | 'session_snapshot',
  entry: SessionStateSecretRequest,
) {
  const frame: SessionStateFrame | SessionSnapshotFrame = carrier === 'session_state'
    ? { type: carrier, session_id: MAIN_ID, user_id: 'stage1-tester', emitted_at: NOW, pending_approvals: [], secret_requests: [entry] }
    : { type: carrier, session_id: MAIN_ID, seq: 1, secret_requests: [entry] }
  await emitFromGateway(socket, frame)
  await waitFor(() => expect(useSecretRequestStore.getState().byId[entry.request_id]?.request).toEqual(entry))
}

function secretRequests() {
  return net.requests.filter((request) => /^\/api\/v1\/(?:secrets|secret-requests)(?:\/|$)/.test(request.path))
}

// Bundle B3 re-aim (tests/train3-b3-logs/REAIM.md; commit d52b70bdc ships
// INLINE_TOOL_APPROVALS_ENABLED): the app-wide "Tool Approval Required" modal is
// retired by design, so the origin pins target the inline card in the approval's
// root chat (tool-approval-thread-tail → tool-approval-panel, origin via
// data-testid="tool-approval-origin") at the same strength, acting from the
// open main chat — the re-aimed AppShell.stage1.test.tsx pattern. The wire can
// no longer produce an incomplete reconnect stub (SessionStatePendingApproval
// requires tool_call_id/turn_id/args), so the stub fixture is gone with it.
function approvalOriginFrame(carrier: 'live' | 'session_state', origin: string | undefined): ServerFrame {
  const frame = approval({
    origin_label: origin, root_session_id: MAIN_ID,
    ...(origin === 'for group Sales' ? { room_ref: { room_id: 'sales-group', room_name: 'Sales', request_id: 'room-ask', role: 'request' } } : {}),
  })
  if (carrier === 'live') return frame
  const { type: _type, ...pending } = frame
  void _type
  return {
    type: 'session_state', session_id: MAIN_ID, user_id: 'stage1-tester', emitted_at: NOW,
    pending_approvals: [pending],
  }
}

async function emitApprovalOrigin(
  socket: Parameters<typeof emitFromGateway>[0],
  carrier: 'live' | 'session_state',
  origin: string | undefined,
) {
  expect(screen.queryAllByRole('dialog', { hidden: true })).toEqual([])
  await emitFromGateway(socket, approvalOriginFrame(carrier, origin))
  await waitFor(() => expect(useToolApprovalStore.getState().queue.map((entry) => entry.approvalId)).toEqual(['stage1-approval']))
  // The card is the inline thread tail in the root chat — never the modal.
  const card = await screen.findByTestId('tool-approval-thread-tail')
  expect(card).toBeVisible()
  const panel = within(card).getByTestId('tool-approval-panel')
  // R6 (inline, not a dialog): appearance must not move focus — the retired
  // modal pinned focus ON Deny; the card pins the opposite, per the amendment.
  expect(within(panel).getByRole('button', { name: 'Deny' })).not.toHaveFocus()
  // The retired surface stays gone — counting hidden nodes, so a renamed
  // dialog cannot hide behind accessibility isolation.
  expect(screen.queryAllByRole('dialog', { hidden: true })).toEqual([])
  return panel
}

async function settledReads() {
  // Query settlement, not an elapsed-time absence oracle. All issued reads are
  // recorded synchronously even if their responses have not yet settled. The
  // agents read warms the ['agents'] cache the card resolves its acting-agent
  // name from (D-87), so the description oracle below is deterministic.
  await waitFor(() => expect(queryClient.isFetching()).toBe(0))
  expect(net.requests.some((request) => request.method === 'GET' && request.path === '/api/v1/agents')).toBe(true)
  expect(net.requests.some((request) => request.method === 'GET' && request.path === '/api/v1/workspaces')).toBe(true)
}

describe.each(['live', 'session_state'] as const)('WU-4 — release approval-card origin from %s', (carrier) => {
  it.each(['for group Sales', 'while answering Anna on Telegram', 'asked by Jim'])(
    'K7 / FR-251: displays the exact server origin "%s" without navigation or an implicit decision',
    async (origin) => {
      const { router, socket } = await mountStage1App()
      await settledMainChat(socket)
      await settledReads()
      const panel = await emitApprovalOrigin(socket, carrier, origin)
      // Exact origin at its dedicated element (K7), plus the exact request
      // sentence on the describedby element — the origin is a separate
      // labelled element on the card, so together these are the same oracle
      // the modal's combined description gave, now with the origin pinned
      // at its own node.
      expect(within(panel).getByTestId('tool-approval-origin').textContent).toBe(origin)
      expect(panel).toHaveAccessibleDescription('Mia is requesting permission to run a tool.')
      // Focus flip is the SPEC, not a weakening: the retired modal grabbed
      // focus (pinned toHaveFocus); the inline card must not (R6, pinned in
      // emitApprovalOrigin as not.toHaveFocus).
      expect(within(panel).getByRole('button', { name: 'Approve Once' })).toBeEnabled()
      // No navigation, no session/workspace side effect, no implicit decision.
      expect(router.state.location.pathname).toBe(MAIN_PATH)
      expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
      expect(useWorkspacesStore.getState().activeWorkspaceId).toBe(WS)
      expect(net.writes()).toEqual([])
    },
  )

  it('K7: omitted origin renders no empty label or guessed origin', async () => {
    const { router, socket } = await mountStage1App()
    await settledMainChat(socket)
    await settledReads()
    const panel = await emitApprovalOrigin(socket, carrier, undefined)
    expect(within(panel).getByRole('button', { name: 'Approve Once' })).toBeEnabled()
    expect(within(panel).queryByTestId('tool-approval-origin')).toBeNull()
    expect(panel).toHaveAccessibleDescription('Mia is requesting permission to run a tool.')
    expect(router.state.location.pathname).toBe(MAIN_PATH)
    expect(net.writes()).toEqual([])
  })

  it('K7: the wire schema rejects an empty origin (minLength 1), with a valid-frame control', () => {
    const valid = approvalOriginFrame(carrier, 'asked by Jim')
    expect(parseFrameSafe(valid)).toEqual(valid)
    expect(parseFrameSafe(approvalOriginFrame(carrier, ''))).toBeNull()
  })
})

// RETIRED with its subject (bundle B3): "an incomplete reconnect approval keeps
// its supplied origin and still offers Deny only". The wire can no longer
// produce an incomplete reconnect approval — SessionStatePendingApproval now
// REQUIRES tool_call_id/turn_id/args (commit d52b70bdc; the stub rendering
// branches were deleted in 8f740826e) — so its Deny-only/"Approval Details
// Unavailable" assertions are unreachable from real traffic. Its surviving
// current-wire equivalent — a session_state restore keeps the supplied origin
// and offers the full approvable card — is pinned by the session_state-carrier
// exact-origin cases above. Full disposition: tests/train3-b3-logs/REAIM.md.

it('K7: origin text is literal text, never executable markup', async () => {
  const { socket } = await mountStage1App()
  await settledMainChat(socket)
  const origin = 'for group <img src=x onerror="alert(1)">'
  const panel = await emitApprovalOrigin(socket, 'live', origin)
  const label = within(panel).getByTestId('tool-approval-origin')
  expect(label.textContent).toBe(origin)
  expect(label.children).toHaveLength(0)
  expect(net.writes()).toEqual([])
})

describe('WU-4 — mounted secret card error and routing boundaries', () => {
  it('K5: explicit rejection is announced, clears the value, and never claims Saved', async () => {
    const { socket } = await mountStage1App()
    await settledMainChat(socket)
    net.respond('POST', '/api/v1/secret-requests/wu4-secret/submit', () => json({ error: 'recipient changed', code: 'invalid_payload' }, 400))
    await emitFromGateway(socket, secret())
    const card = await screen.findByTestId('secret-request-card')
    const input = within(card).getByLabelText(/^Reporting API key(?: \*)?$/)
    const user = userEvent.setup()
    await user.type(input, 'wu4-refused-sentinel')
    await user.click(within(card).getByRole('button', { name: 'Save secret' }))
    await waitFor(() => expect(card).toHaveAttribute('data-state', 'rejected'))
    expect(within(card).getByRole('status')).toHaveTextContent('recipient changed')
    expect(input).toHaveValue('')
    expect(within(card).queryByText(/^Saved/)).toBeNull()
    expect(secretRequests().map(({ method, path, body }) => ({ method, path, body }))).toEqual([{
      method: 'POST', path: '/api/v1/secret-requests/wu4-secret/submit', body: { values: { api_key: 'wu4-refused-sentinel' } },
    }])
  })

  it('K5 / FR-079: a lost submit answer checks state once, shows uncertainty and never replays the secret', async () => {
    const { socket } = await mountStage1App()
    await settledMainChat(socket)
    net.respond('POST', '/api/v1/secret-requests/wu4-secret/submit', () => Promise.reject(new TypeError('connection lost')))
    let confirmState!: (response: Response) => void
    const lookup = new Promise<Response>((resolve) => { confirmState = resolve })
    net.respond('GET', '/api/v1/secret-requests/wu4-secret', () => lookup)
    await emitFromGateway(socket, secret())
    const card = await screen.findByTestId('secret-request-card')
    const input = within(card).getByLabelText(/^Reporting API key(?: \*)?$/)
    const user = userEvent.setup()
    await user.type(input, 'wu4-uncertain-sentinel')
    await user.click(within(card).getByRole('button', { name: 'Save secret' }))
    await waitFor(() => expect(card).toHaveAttribute('data-state', 'unknown'))
    expect(input).toHaveValue('')
    expect(input).toBeDisabled()
    expect(within(card).getByRole('status').textContent).toBe('Submission unknown — checking with the server. Your values will not be sent again.')
    expect(within(card).queryByText(/^Saved/)).toBeNull()
    const expectedRequests = [
      { method: 'POST', path: '/api/v1/secret-requests/wu4-secret/submit', body: { values: { api_key: 'wu4-uncertain-sentinel' } } },
      { method: 'GET', path: '/api/v1/secret-requests/wu4-secret', body: null },
    ]
    expect(secretRequests().map(({ method, path, body }) => ({ method, path, body }))).toEqual(expectedRequests)
    await act(async () => {
      confirmState(json({ request_id: 'wu4-secret', state: 'submitted', reference_names: ['reporting_key.v1'], updated_at: NOW }))
    })
    await waitFor(() => expect(card).toHaveAttribute('data-state', 'submitted'))
    expect(within(card).getByRole('status').textContent).toBe('Saved · stored as reporting_key.v1')
    expect(input).toHaveValue('')
    expect(secretRequests().map(({ method, path, body }) => ({ method, path, body }))).toEqual(expectedRequests)
  })

  it('K1: a wrong-root request stays hidden while a correctly rooted card remains reachable', async () => {
    const { socket } = await mountStage1App()
    await settledMainChat(socket)
    await emitFromGateway(socket, secret())
    const card = await screen.findByTestId('secret-request-card')
    expect(card).toBeVisible()
    await emitFromGateway(socket, {
      ...secret(), request_id: 'other-root-secret', session_id: 'other-helper', root_session_id: 'other-main', acting_agent_name: 'Other helper',
    })
    await waitFor(() => expect(useSecretRequestStore.getState().byId['other-root-secret']?.request.state).toBe('pending'))
    expect(screen.getAllByTestId('secret-request-card')).toEqual([card])
    expect(card).toHaveAttribute('aria-label', 'Secret requested by Setup helper')
    expect(screen.queryByText('Other helper needs a secret')).toBeNull()
    expect(secretRequests()).toEqual([])
  })
})

describe('WU-4 — reachable secret card state transitions', () => {
  it.each(terminalStates)('K3: a live $state result updates the real card, wipes its field and never submits', async ({ state, message }) => {
    const { socket } = await mountStage1App()
    await settledMainChat(socket)
    await emitFromGateway(socket, secret())
    const card = await screen.findByTestId('secret-request-card')
    expect(card).toHaveAttribute('data-state', 'pending')
    expect(within(card).getByTestId('secret-request-asker')).toHaveTextContent('Setup helper needs a secret · asked by Omnipus')
    const input = within(card).getByLabelText(/^Reporting API key(?: \*)?$/)
    await userEvent.setup().type(input, 'wu4-disposal-sentinel')
    const resolved: SecretRequestResolvedFrame = {
      type: 'secret_request_resolved', request_id: secret().request_id, session_id: 'setup-helper', root_session_id: MAIN_ID, state,
    }
    await emitFromGateway(socket, resolved)
    await waitFor(() => expect(card).toHaveAttribute('data-state', state))
    expect(within(card).getByRole('status').textContent).toBe(message)
    expect(within(card).queryByLabelText(/^Reporting API key/)).toBeNull()
    expect(input).toHaveValue('')
    expect(secretRequests()).toEqual([])
  })

  it.each(['session_state', 'session_snapshot'] as const)('K4: a %s restores Saved references without fields or a value-bearing request', async (carrier) => {
    const { socket } = await mountStage1App()
    await settledMainChat(socket)
    await emitSecretSnapshot(socket, carrier, secretSnapshot('submitted'))
    const card = screen.getByTestId('secret-request-card')
    expect(card).toHaveAttribute('data-state', 'submitted')
    expect(within(card).getByRole('status').textContent).toBe('Saved · stored as reporting_key.v1')
    expect(within(card).queryByLabelText(/^Reporting API key/)).toBeNull()
    expect(secretRequests()).toEqual([])
  })

  it.each(['session_state', 'session_snapshot'] as const)('K4: a %s restores an unavailable request, never a fabricated Saved result', async (carrier) => {
    const { socket } = await mountStage1App()
    await settledMainChat(socket)
    await emitSecretSnapshot(socket, carrier, secretSnapshot('interrupted'))
    const card = screen.getByTestId('secret-request-card')
    expect(card).toHaveAttribute('data-state', 'interrupted')
    expect(within(card).getByRole('status').textContent).toBe('Request unavailable — the gateway restarted before anything was stored. The agent can ask again.')
    expect(within(card).queryByLabelText(/^Reporting API key/)).toBeNull()
    expect(within(card).queryByText(/^Saved/)).toBeNull()
    expect(secretRequests()).toEqual([])
  })

  it('K4/K5: a session_snapshot restores a usable pending card but blank input cannot submit', async () => {
    const { socket } = await mountStage1App()
    await settledMainChat(socket)
    await emitSecretSnapshot(socket, 'session_snapshot', secretSnapshot('pending'))
    const card = screen.getByTestId('secret-request-card')
    expect(card).toHaveAttribute('data-state', 'pending')
    const input = within(card).getByLabelText(/^Reporting API key(?: \*)?$/)
    expect(input).toHaveAttribute('type', 'password')
    expect(input).toHaveValue('')
    await userEvent.setup().click(within(card).getByRole('button', { name: 'Save secret' }))
    expect(within(card).getByRole('alert').textContent).toBe('Enter a value.')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(secretRequests()).toEqual([])
  })
})
