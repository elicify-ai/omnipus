// SF-01 / K5: committed 503 means saved, not confirmed continuation.
// Oracle: the SF-01 dispatch and chief's explicit clearing/racing-WS ruling.
// Real component, store, frame handling and API client; doubles only at fetch/socket.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { SecretRequestFrame, SecretRequestResolvedFrame, SessionSnapshotFrame, SessionStateFrame, SessionStateSecretRequest } from '@/lib/api/generated/asyncapi-types'
import type { ErrorResponse, SecretRequestState } from '@/lib/api/generated/openapi-types'
import { getSecretRequest } from '@/lib/api/secrets'
import { queryClient } from '@/lib/queryClient'
import { parseFrameSafe } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
import { useSecretRequestStore } from '@/store/secretRequests'
import { useSessionStore } from '@/store/session'
import { useToolApprovalStore } from '@/store/toolApproval'
import { SecretRequestThreadTail } from './SecretRequestCard'
import { deferred, installNetwork, jsonResponse, labelledControl, observableText, queryLabelledControl, requests, resetSharedState, socketSends } from '../../../tests/l1-ui-red-u23/support'

const SENTINEL = 'SF01_SECRET_VALUE_NOT_FOR_CHAT'
const WARNING = "Saved, but the agent's next step is not confirmed. Reopen this chat to retry. Your values will not be sent again."
const frame: SecretRequestFrame = {
  type: 'secret_request', request_id: 'sf01-request', session_id: 'sess-001',
  acting_agent_id: 'mia', acting_agent_name: 'Mia', purpose: 'Connect the reporting provider',
  recipient: { kind: 'provider_key', lines: ['Reporting provider', 'https://reports.example.test'] },
  fields: [{ key: 'api_key', label: 'Reporting API key' }],
}
const saved: SecretRequestState = {
  request_id: frame.request_id, state: 'submitted', reference_names: ['reporting_key.v1'], updated_at: '2026-10-17T09:00:00Z',
}
const resolutions = [
  { state: 'cancelled', text: 'Cancelled — nothing was stored.' },
  { state: 'stopped', text: 'Stopped — nothing was stored.' },
  { state: 'interrupted', text: 'Request unavailable — the gateway restarted before anything was stored. The agent can ask again.' },
  { state: 'refused', text: 'Refused — nothing was stored.' },
] as const
let observations: string[]
let unsubscribe: () => void

beforeEach(() => {
  resetSharedState()
  useSecretRequestStore.getState().reset()
  observations = [observableText(useSecretRequestStore.getState())]
  unsubscribe = useSecretRequestStore.subscribe(value => observations.push(observableText(value)))
  installNetwork(call => jsonResponse(saved, call.method === 'POST' ? 503 : 200))
})
afterEach(() => {
  unsubscribe()
  queryClient.clear()
  vi.unstubAllGlobals()
})

function mountAndSubmit() {
  act(() => useChatStore.getState().handleFrame(frame))
  render(<SecretRequestThreadTail />)
  const input = labelledControl('Reporting API key')
  fireEvent.change(input, { target: { value: SENTINEL } })
  fireEvent.click(screen.getByRole('button', { name: 'Save secret' }))
  expect(input).toHaveValue('') // FR-075: erased at handoff, before the reply.
  return input
}
function resolveFrame(state: SecretRequestResolvedFrame['state'] = 'submitted') {
  act(() => useChatStore.getState().handleFrame({ type: 'secret_request_resolved', request_id: frame.request_id, state }))
}
function expectWarning() {
  expect(screen.getByRole('alert').textContent).toBe(WARNING)
  expect(screen.getByTestId('secret-request-result')).toHaveTextContent('reporting_key.v1')
  expect(queryLabelledControl('Reporting API key')).not.toBeInTheDocument()
}
function expectNoEchoOrReplay() {
  expect(requests.filter(call => call.method === 'POST').map(call => ({ path: call.path, body: JSON.parse(call.body!) }))).toEqual([
    { path: '/api/v1/secret-requests/sf01-request/submit', body: { values: { api_key: SENTINEL } } },
  ])
  expect(useToolApprovalStore.getState().queue, 'secret cards never create an approval').toEqual([])
  expect(observations.length, 'store subscriptions actually observed transitions').toBeGreaterThan(1)
  for (const [name, sink] of Object.entries({
    observations, chat: useChatStore.getState(), approvals: useToolApprovalStore.getState(),
    queries: queryClient.getQueryCache().getAll(), mutations: queryClient.getMutationCache().getAll(),
    socketSends, otherRequests: requests.filter(call => call.method !== 'POST'),
    localStorage: { ...localStorage }, sessionStorage: { ...sessionStorage }, visibleText: document.body.textContent,
  })) expect(observableText(sink), `SF-01: secret must not echo into ${name}`).not.toContain(SENTINEL)
}

async function showUnconfirmed() {
  const input = mountAndSubmit()
  await waitFor(expectWarning)
  expect(input).toHaveValue('')
  return input
}

describe('SF-01 saved but continuation unconfirmed', () => {
  it.each(['before', 'after'] as const)('keeps the warning when submitted WS arrives %s the committed 503', async order => {
    const response = deferred<Response>()
    installNetwork(call => call.method === 'POST' ? response.promise : jsonResponse(saved))
    const input = mountAndSubmit()
    if (order === 'before') resolveFrame()
    await act(async () => response.resolve(jsonResponse(saved, 503)))
    await waitFor(expectWarning)
    if (order === 'after') resolveFrame()
    expectWarning()
    expect(input).toHaveValue('')
    expect(screen.queryByRole('button', { name: 'Dismiss' })).not.toBeInTheDocument()
    expectNoEchoOrReplay()
  })

  it('keeps the warning across GET submitted, snapshot, duplicate frame and navigation', async () => {
    await showUnconfirmed()
    const readback = await getSecretRequest(frame.request_id)
    act(() => useSecretRequestStore.getState().applyState(readback))
    expectWarning()
    // M13: a still-unconfirmed authoritative snapshot now says so explicitly.
    act(() => useSecretRequestStore.getState().reconcileSession(frame.session_id, [{ ...frame, ...saved, continuation_unconfirmed: true }]))
    expectWarning()
    act(() => useChatStore.getState().handleFrame(frame))
    expectWarning()
    act(() => useSessionStore.setState({ activeSessionId: 'another-chat' }))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    act(() => useSessionStore.setState({ activeSessionId: frame.session_id }))
    expectWarning()
    expectNoEchoOrReplay()
  })

  it('cannot be dismissed or evicted by a newer submitted card', async () => {
    await showUnconfirmed()
    act(() => useSecretRequestStore.getState().dismiss(frame.request_id))
    expectWarning()
    act(() => useSecretRequestStore.getState().reconcileSession(frame.session_id, [{
      ...frame, ...saved, request_id: 'newer-request', updated_at: '2026-10-17T10:00:00Z', reference_names: ['newer_key.v1'],
    }]))
    expect(screen.getByRole('alert').textContent).toBe(WARNING)
    expect(screen.getAllByTestId('secret-request-card')).toHaveLength(2)
    expect(document.body).toHaveTextContent('reporting_key.v1')
    expect(document.body).toHaveTextContent('newer_key.v1')
    expectNoEchoOrReplay()
  })

  it('clears only on explicit accepted submit success, not a not-pending receipt', async () => {
    await showUnconfirmed()
    act(() => useSecretRequestStore.getState().applyState(saved, 'not-pending'))
    expectWarning()
    act(() => useSecretRequestStore.getState().applyState(saved, 'accepted'))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByTestId('secret-request-result')).toHaveTextContent('Saved · stored as reporting_key.v1')
    expect(screen.getByRole('button', { name: 'Dismiss' })).toBeInTheDocument()
    expectNoEchoOrReplay()
  })

  describe.each(['WS', 'REST', 'snapshot'] as const)('authoritative resolution through %s', carrier => {
    it.each(resolutions)('resolves the warning on $state, without another submit', async ({ state, text }) => {
      await showUnconfirmed()
      const resolved = { ...saved, state, reference_names: undefined }
      if (carrier === 'WS') resolveFrame(state)
      else if (carrier === 'REST') act(() => useSecretRequestStore.getState().applyState(resolved))
      else act(() => useSecretRequestStore.getState().reconcileSession(frame.session_id, [{ ...frame, ...resolved }]))
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      expect(screen.getByTestId('secret-request-result').textContent).toBe(text)
      expect(screen.getByRole('button', { name: 'Dismiss' })).toBeInTheDocument()
      expectNoEchoOrReplay()
    })
  })

  it('does not warn after a successful accepted submit', async () => {
    installNetwork(() => jsonResponse(saved))
    mountAndSubmit()
    await waitFor(() => expect(screen.getByTestId('secret-request-result')).toHaveTextContent('Saved · stored as reporting_key.v1'))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expectNoEchoOrReplay()
  })

  it.each([
    { error: 'request store unavailable', code: 'unavailable' } satisfies ErrorResponse,
    { ...saved, request_id: 'different-request' },
    { ...saved, updated_at: 'not-a-date' },
  ])('does not claim a committed save from unavailable or invalid 503 body %j', async body => {
    installNetwork(call => call.method === 'POST' ? jsonResponse(body, 503) : jsonResponse({ ...saved, state: 'pending', reference_names: undefined }))
    const input = mountAndSubmit()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Save secret' })).toBeEnabled())
    expect(input).toHaveValue('')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.queryByTestId('secret-request-result')).not.toBeInTheDocument()
    expectNoEchoOrReplay()
  })
})

// M13 / T2-SF-02: the authoritative continuation flag is on K4's two
// session-state carriers, not on K3's one-shot resolved frame or K5 GET.
// Oracle: architect M13 ruling, Decision / SPA paragraph (2026-10-20).
const snapshotEntry: SessionStateSecretRequest = {
  ...saved,
  session_id: frame.session_id,
  acting_agent_id: frame.acting_agent_id,
  acting_agent_name: frame.acting_agent_name,
  purpose: frame.purpose,
  recipient: frame.recipient,
  fields: frame.fields,
}
const carriers = ['session_snapshot', 'session_state'] as const

function deliverProjection(carrier: typeof carriers[number], request: SessionStateSecretRequest) {
  const message: SessionSnapshotFrame | SessionStateFrame = carrier === 'session_snapshot'
    ? { type: carrier, session_id: frame.session_id, seq: 1, reason: 'unknown_position', secret_requests: [request] }
    : { type: carrier, session_id: frame.session_id, user_id: 'l1-red-user', pending_approvals: [], emitted_at: saved.updated_at, secret_requests: [request] }
  // Exercise the real generated validator as well as the dispatcher: a
  // schema that dropped the new field would fail this exact-shape assertion.
  const parsed = parseFrameSafe(JSON.stringify(message))
  expect(parsed).toEqual(message)
  if (!parsed || (parsed.type !== 'session_snapshot' && parsed.type !== 'session_state')) {
    throw new Error('M13 projection must validate as a server session-state frame')
  }
  act(() => useChatStore.getState().handleFrame(parsed))
}

function expectPlainSaved() {
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(screen.getByTestId('secret-request-result').textContent).toBe('Saved · stored as reporting_key.v1')
  expect(screen.getByRole('button', { name: 'Dismiss' })).toBeInTheDocument()
  expect(queryLabelledControl('Reporting API key')).not.toBeInTheDocument()
}

describe('M13 reload-safe continuation projection', () => {
  it.each(carriers)('restores the warning from %s after reload without any surviving tab-local state', async carrier => {
    await showUnconfirmed()
    cleanup()
    act(() => useSecretRequestStore.getState().reset())
    expect(useSecretRequestStore.getState().byId).toEqual({})

    deliverProjection(carrier, { ...snapshotEntry, continuation_unconfirmed: true })
    render(<SecretRequestThreadTail />)
    expectWarning()
    expect(screen.queryByRole('button', { name: 'Dismiss' })).not.toBeInTheDocument()
    expectNoEchoOrReplay()
  })

  describe.each(carriers)('%s authoritative confirmation', carrier => {
    it.each([undefined, false])('clears the warning when the flag is %s and renders plain Saved', async flag => {
      await showUnconfirmed()
      deliverProjection(carrier, { ...snapshotEntry, continuation_unconfirmed: flag })
      expectPlainSaved()
      expectNoEchoOrReplay()
    })
  })

  it('updates the mounted card as live session_state toggles the flag, without a submit or reload', () => {
    deliverProjection('session_state', snapshotEntry)
    render(<SecretRequestThreadTail />)
    expectPlainSaved()

    deliverProjection('session_state', { ...snapshotEntry, continuation_unconfirmed: true })
    expectWarning()
    deliverProjection('session_state', snapshotEntry)
    expectPlainSaved()
    // A later downstream revocation can make delivery unconfirmed again.
    deliverProjection('session_state', { ...snapshotEntry, continuation_unconfirmed: true })
    expectWarning()
    expect(screen.queryByRole('button', { name: 'Dismiss' })).not.toBeInTheDocument()
    expect(requests).toEqual([])
    expect(socketSends).toEqual([])
  })
})
