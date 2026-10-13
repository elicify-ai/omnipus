// U2-C05–C07. FR-074/075/079/109; §6.3; BDD-6.5/6.7/6.11/12.9.
// State is value-free. All transitions use real store actions, not mocked helpers.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { queryClient } from '@/lib/queryClient'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import type { SecretRequestFrame, SecretRequestResolvedFrame, SessionStateSecretRequest } from '@/lib/api/generated/asyncapi-types'
import type { SecretRequestState } from '@/lib/api/generated/openapi-types'
import { labelledControl, queryLabelledControl, deferred, installNetwork, jsonResponse, loadSecretSurface, requests, resetSharedState } from '../../../tests/l1-ui-red-u23/support'


const frame: SecretRequestFrame = {
  type: 'secret_request', request_id: 'sec-state', session_id: 'sess-001',
  acting_agent_id: 'omnipus', acting_agent_name: 'Omnipus', purpose: 'Connect Slack',
  recipient: { kind: 'channel_credential', lines: ['Slack workspace Acme', 'bot and app tokens'] },
  fields: [{ key: 'bot_token', label: 'Bot token' }, { key: 'app_token', label: 'App token' }],
}
const references = ['slack_acme_bot_token.v1', 'slack_acme_app_token.v1']
const state = (status: SecretRequestState['state']): SecretRequestState => ({
  request_id: frame.request_id, state: status,
  ...(status === 'submitted' ? { reference_names: references } : {}), updated_at: '2026-10-14T09:00:00Z',
})
let surface: Awaited<ReturnType<typeof loadSecretSurface>>
function mount(request: SecretRequestFrame = frame, parentKeys: string[] = []) {
  act(() => surface.store.getState().upsertFromFrame(request))
  return render(<QueryClientProvider client={queryClient}>
    <div onKeyDown={event => parentKeys.push(event.key)}>
      <surface.Tail />
      <textarea aria-label="Chat composer" data-testid="chat-input" />
    </div>
  </QueryClientProvider>)
}
function fill() {
  fireEvent.change(labelledControl('Bot token'), { target: { value: 'bot-sentinel' } })
  fireEvent.change(labelledControl('App token'), { target: { value: 'app-sentinel' } })
}

beforeEach(async () => {
  resetSharedState()
  surface = await loadSecretSurface()
  surface.store.getState().reset()
  installNetwork(call => {
    if (call.path === '/api/v1/secret-requests/sec-state/submit') return jsonResponse(state('submitted'))
    if (call.path === '/api/v1/secret-requests/sec-state') return jsonResponse(state('submitted'))
    throw new Error(`Unexpected boundary ${call.method} ${call.path}`)
  })
})
afterEach(() => { queryClient.clear(); vi.unstubAllGlobals() })

describe('terminal disposal (FR-074/075; BDD-6.5)', () => {
  it.each(['submitted', 'cancelled', 'stopped', 'interrupted', 'refused'] as const)('FR-075: resolved %s clears even detached input handles and sends nothing', async terminal => {
    mount()
    const bot = labelledControl('Bot token')
    const app = labelledControl('App token')
    fill()
    const resolved: SecretRequestResolvedFrame = { type: 'secret_request_resolved', request_id: frame.request_id, state: terminal }
    act(() => surface.store.getState().markResolved(resolved))
    await waitFor(() => expect(queryLabelledControl('Bot token')).not.toBeInTheDocument())
    expect(bot).toHaveValue('')
    expect(app).toHaveValue('')
    expect(requests.filter(c => c.method === 'POST')).toEqual([])
    expect(JSON.stringify(surface.store.getState())).not.toContain('sentinel')
    expect(labelledControl('Chat composer')).toHaveFocus()
    // The visible state must have an announcement hook, not only a color.
    expect(document.querySelector('[role="status"], [role="alert"], [aria-live]')).not.toBeNull()
    if (terminal !== 'submitted') expect(screen.queryByText(/^Saved(?:\s|$)/)).not.toBeInTheDocument()
  })

  it('FR-075: unmount clears detached controls and remount does not restore or replay a draft', () => {
    const view = mount()
    const bot = labelledControl('Bot token')
    const app = labelledControl('App token')
    fill()
    view.unmount()
    expect(bot).toHaveValue('')
    expect(app).toHaveValue('')
    render(<QueryClientProvider client={queryClient}><surface.Tail /></QueryClientProvider>)
    expect(labelledControl('Bot token')).toHaveValue('')
    expect(labelledControl('App token')).toHaveValue('')
    expect(requests).toEqual([])
  })

  it('FR-075: navigation clears the old inputs and returning never re-sends them', () => {
    mount()
    const bot = labelledControl('Bot token')
    fill()
    act(() => useSessionStore.setState({ activeSessionId: 'other-chat' }))
    expect(queryLabelledControl('Bot token')).not.toBeInTheDocument()
    expect(bot).toHaveValue('')
    act(() => useSessionStore.setState({ activeSessionId: frame.session_id }))
    expect(labelledControl('Bot token')).toHaveValue('')
    expect(requests).toEqual([])
  })
})

describe('submission uncertainty (FR-079; §6.3; BDD-6.7)', () => {
  it('FR-074: submitting is visible and disabled until the actual response arrives', async () => {
    const pending = deferred<Response>()
    installNetwork(() => pending.promise)
    mount()
    fill()
    const submit = screen.getByRole('button', { name: /submit|save/i })
    fireEvent.click(submit)
    expect(submit).toBeDisabled()
    expect(screen.getByText(/submitting|saving/i)).toBeInTheDocument()
    await act(async () => pending.resolve(jsonResponse(state('submitted'))))
    await waitFor(() => expect(document.body).toHaveTextContent(references[0]))
    expect(screen.getByText(/^Saved(?:\s|$)/)).toBeInTheDocument()
    expect(queryLabelledControl('Bot token')).not.toBeInTheDocument()
  })

  it('FR-074: explicit 400 rejection is visible, associated, value-free and not Saved', async () => {
    installNetwork(() => jsonResponse({ error: 'missing field', code: 'invalid_payload' }, 400))
    mount()
    fill()
    fireEvent.click(screen.getByRole('button', { name: /submit|save/i }))
    await waitFor(() => expect(surface.store.getState().byId[frame.request_id].localPhase).toBe('rejected'))
    expect(screen.queryByText(/^Saved(?:\s|$)/)).not.toBeInTheDocument()
    // Card-level server rejection may be polite; only field validation
    // requires the Field alert. The exact boundary error must be announced.
    const visibleRejection = screen.getByText(/missing field/)
    const live = visibleRejection.closest('[aria-live="polite"], [aria-live="assertive"], [role="status"], [role="alert"]')
    expect(live).toContainElement(visibleRejection)
    expect(live).not.toBeEmptyDOMElement()
    expect(live).toHaveTextContent(visibleRejection.textContent!, { normalizeWhitespace: false })
    expect(document.body.textContent).not.toContain('sentinel')
    expect(requests.filter(c => c.method === 'POST')).toHaveLength(1)
  })

  it('FR-079: a lost submit response reads value-free state and shows confirmed names without a second POST', async () => {
    const recovery = deferred<Response>()
    installNetwork(call => call.method === 'POST' ? Promise.reject(new TypeError('connection lost')) : recovery.promise)
    mount()
    fill()
    const bot = labelledControl('Bot token')
    fireEvent.click(screen.getByRole('button', { name: /submit|save/i }))
    await waitFor(() => expect(requests.map(c => c.method)).toEqual(['POST', 'GET']))
    expect(screen.getByText(/submission unknown.*checking/i)).toBeInTheDocument()
    expect(screen.queryByText(/^Saved(?:\s|$)/)).not.toBeInTheDocument()
    expect(bot).toHaveValue('')
    await act(async () => recovery.resolve(jsonResponse(state('submitted'))))
    await waitFor(() => expect(document.body).toHaveTextContent(references[0]))
    expect(document.body).toHaveTextContent(references[1])
    expect(requests.map(c => ({ path: c.path, method: c.method }))).toEqual([
      { path: '/api/v1/secret-requests/sec-state/submit', method: 'POST' },
      { path: '/api/v1/secret-requests/sec-state', method: 'GET' },
    ])
  })

  it.each(['pending', 'cancelled', 'stopped', 'interrupted', 'refused', 'missing'] as const)('FR-079: omission from snapshot plus %s lookup is never read as success', async lookup => {
    installNetwork(() => lookup === 'missing' ? jsonResponse({ error: 'not found' }, 404) : jsonResponse(state(lookup)))
    mount()
    fill()
    act(() => surface.store.getState().reconcileSession(frame.session_id, []))
    await waitFor(() => expect(requests.filter(c => c.method === 'GET')).toHaveLength(1))
    await waitFor(() => expect(surface.store.getState().byId[frame.request_id]?.localPhase).not.toBe('submitting'))
    expect(screen.queryByText(/^Saved(?:\s|$)/)).not.toBeInTheDocument()
    expect(requests.filter(c => c.method === 'POST')).toEqual([])
    expect(document.body.textContent).not.toContain('sentinel')
  })

  it('FR-079: terminal snapshot restores Saved with references without requesting any values', () => {
    const snapshot: SessionStateSecretRequest = {
      ...frame, state: 'submitted', reference_names: references, updated_at: state('submitted').updated_at,
    }
    act(() => surface.store.getState().reconcileSession(frame.session_id, [snapshot]))
    render(<QueryClientProvider client={queryClient}><surface.Tail /></QueryClientProvider>)
    expect(screen.getByText(/^Saved(?:\s|$)/)).toBeInTheDocument()
    expect(document.body).toHaveTextContent(references[0])
    expect(queryLabelledControl('Bot token')).not.toBeInTheDocument()
    expect(requests).toEqual([])
  })
})

describe('keyboard and root routing (FR-109/061; BDD-12.9/6.11)', () => {
  it('BDD-12.9 / §6.3: first field focused; only last-field Enter submits; neither Enter bubbles to composer', async () => {
    const parentKeys: string[] = []
    mount(frame, parentKeys)
    const bot = labelledControl('Bot token')
    const app = labelledControl('App token')
    expect(bot).toHaveFocus()
    fill()
    fireEvent.keyDown(bot, { key: 'Enter', code: 'Enter' })
    expect(requests).toEqual([])
    expect(parentKeys).toEqual([])
    app.focus()
    fireEvent.keyDown(app, { key: 'Enter', code: 'Enter' })
    await waitFor(() => expect(requests.filter(c => c.method === 'POST')).toHaveLength(1))
    expect(JSON.parse(requests[0].body!)).toEqual({ values: { bot_token: 'bot-sentinel', app_token: 'app-sentinel' } })
    expect(parentKeys).toEqual([])
    await waitFor(() => expect(labelledControl('Chat composer')).toHaveFocus())
  })

  it('FR-109: invalid last-field Enter announces errors associated with both labelled fields', () => {
    mount()
    fireEvent.keyDown(labelledControl('App token'), { key: 'Enter', code: 'Enter' })
    for (const label of ['Bot token', 'App token']) {
      const input = labelledControl(label)
      expect(input).toHaveAttribute('aria-invalid', 'true')
      const errorId = input.getAttribute('aria-describedby')
      expect(errorId).not.toBeNull()
      const error = document.getElementById(errorId!)
      expect(error).toHaveAttribute('role', 'alert')
      expect(error).not.toBeEmptyDOMElement()
      expect(input).toHaveAccessibleDescription(error!.textContent!)
    }
    expect(document.querySelector('[role="alert"], [aria-live="assertive"]')).not.toBeNull()
    expect(requests).toEqual([])
  })

  it('BDD-6.11 / K1: helper secret renders in root regardless of active workspace, not in acting chat', () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: 'omnipus-main' })
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-other' })
    })
    mount({ ...frame, session_id: 'helper-child', root_session_id: 'omnipus-main', acting_agent_name: 'Setup helper' })
    expect(screen.getByText(/^Setup helper(?:\s|$)/)).toBeInTheDocument()
    expect(labelledControl('Bot token')).toBeInTheDocument()
    act(() => useSessionStore.setState({ activeSessionId: 'helper-child' }))
    expect(queryLabelledControl('Bot token')).not.toBeInTheDocument()
    expect(requests).toEqual([])
  })
})
