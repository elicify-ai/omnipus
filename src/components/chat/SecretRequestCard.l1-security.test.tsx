// U2-C01–C05. Oracles frozen from system-agent FR-061/062/071/074/075/083,
// §6.2/6.7/6.8; BDD-6.1/6.2/6.4/6.9/6.10/6.15. No implementer tests/bodies read.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { queryClient } from '@/lib/queryClient'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useAuthStore } from '@/store/auth'
import { useToolApprovalStore } from '@/store/toolApproval'
import { useConnectionStore } from '@/store/connection'
import { WsConnection } from '@/lib/ws'
import type { SecretRequestFrame } from '@/lib/api/generated/asyncapi-types'
import type { SecretRequestState } from '@/lib/api/generated/openapi-types'
import { containsSentinel, observableText, labelledControl, deferred, installNetwork, jsonResponse, loadSecretSurface, loadSecretApi, requests, resetSharedState, socketSends } from '../../../tests/l1-ui-red-u23/support'


const SENTINEL = 'L1_SECRET_SENTINEL_7d4f'
const reference = 'private_ws-sales_mia_WEATHER_TOKEN.v1'
const frame: SecretRequestFrame = {
  type: 'secret_request', request_id: 'sec-l1', session_id: 'sess-001',
  acting_agent_id: 'mia', acting_agent_name: 'Secret requester Mia', workspace_id: 'ws-sales',
  owner: { workspace_id: 'ws-sales', agent_id: 'mia' },
  purpose: 'Fetch the Sales weather report',
  recipient: { kind: 'agent_private', lines: ['Sales / Mia', 'WEATHER_TOKEN'] },
  fields: [{ key: 'token', label: 'Weather token' }],
}
const saved: SecretRequestState = {
  request_id: frame.request_id, state: 'submitted', reference_names: [reference], updated_at: '2026-10-14T09:00:00Z',
}
let surface: Awaited<ReturnType<typeof loadSecretSurface>>
let emissions: string[]
let disposers: Array<() => void>
let outputSpies: Array<ReturnType<typeof vi.spyOn>>
let connection: WsConnection

function mount(request: SecretRequestFrame = frame) {
  act(() => surface.store.getState().upsertFromFrame(request))
  return render(<QueryClientProvider client={queryClient}><surface.Tail /></QueryClientProvider>)
}
function assertNoLeak() {
  const consoleCalls = outputSpies.flatMap(spy => spy.mock.calls)
  const otherRequests = requests.filter(c => c.path !== '/api/v1/secret-requests/sec-l1/submit')
  for (const [name, sink] of Object.entries({ emissions, consoleCalls, otherRequests, socketSends, localStorage: { ...localStorage }, sessionStorage: { ...sessionStorage } })) {
    expect(containsSentinel(sink, SENTINEL), `FR-062/075: secret escaped to ${name}`).toBe(false)
  }
  expect(emissions.length, 'non-empty store/cache observations required').toBeGreaterThan(0)
}

describe('Secret card — isolated values and bounded submit (FR-062/071/075)', () => {
  beforeEach(async () => {
    resetSharedState()
    surface = await loadSecretSurface()
    surface.store.getState().reset()
    emissions = []
    disposers = []
    for (const store of [surface.store, useChatStore, useSessionStore, useUiStore, useWorkspacesStore, useAuthStore, useToolApprovalStore, useConnectionStore]) {
      emissions.push(observableText(store.getState()))
      disposers.push(store.subscribe(state => emissions.push(observableText(state))))
    }
    const captureCache = () => emissions.push(observableText({
      queries: queryClient.getQueryCache().getAll().map(q => ({ key: q.queryKey, state: q.state })),
      mutations: queryClient.getMutationCache().getAll().map(m => ({ state: m.state })),
    }))
    disposers.push(queryClient.getQueryCache().subscribe(captureCache), queryClient.getMutationCache().subscribe(captureCache))
    captureCache()
    // Output edge only; spies call through. Never spy on validation/submit helpers.
    outputSpies = ['log', 'warn', 'error', 'info', 'debug'].map(method => vi.spyOn(console, method as 'log'))
    installNetwork(call => {
      if (call.path === '/api/v1/secret-requests/sec-l1/submit' || call.path === '/api/v1/secret-requests/sec-l1') return jsonResponse(saved)
      throw new Error(`Unexpected request ${call.method} ${call.path}`)
    })
    connection = new WsConnection({
      onConnected: () => useConnectionStore.getState().setConnected(true),
      onDisconnected: () => useConnectionStore.getState().setConnected(false),
      onError: message => useConnectionStore.getState().setConnectionError(message),
    })
    useConnectionStore.getState().setConnection(connection)
    connection.connect()
    await Promise.resolve() // the socket-boundary open event, no network or clock wait
    expect(connection.send({ type: 'ping' }), 'non-empty real socket observation control').toBe(true)
    expect(socketSends).toEqual([JSON.stringify({ type: 'ping' })])
  })
  afterEach(() => {
    connection?.disconnect()
    useConnectionStore.setState({ connection: null, isConnected: false })
    disposers?.forEach(dispose => dispose())
    outputSpies?.forEach(spy => spy.mockRestore())
    queryClient.clear()
    vi.unstubAllGlobals()
  })

  it('FR-061 / §6.8: shows actor, purpose, exact destination and private owner with hidden input', () => {
    mount()
    expect(screen.getByText(/^Secret requester Mia(?:\s|$)/)).toBeInTheDocument()
    expect(screen.getByText(frame.purpose)).toBeInTheDocument()
    expect(screen.getByText('Sales / Mia')).toBeInTheDocument()
    expect(screen.getByText('WEATHER_TOKEN')).toBeInTheDocument()
    expect(labelledControl('Weather token')).toHaveAttribute('type', 'password')
    // Promise wording A is deliberately narrower than "cannot ever be revealed".
    expect(screen.getByText(/a command you let the agent run can still reveal it/i)).toBeInTheDocument()
  })

  it('FR-061 / BDD-7.8: private owner is the server-bound target pair, not inferred from the acting agent', () => {
    mount({ ...frame, owner: { workspace_id: 'ws-target', agent_id: 'jim' }, recipient: { kind: 'agent_private', lines: ['Private API_KEY'] } })
    expect(screen.getByText(/^Secret requester Mia(?:\s|$)/)).toBeInTheDocument()
    expect(screen.getByText('Private API_KEY')).toBeInTheDocument()
    expect(document.body).toHaveTextContent('ws-target')
    expect(document.body).toHaveTextContent('jim')
    expect(requests).toEqual([])
  })

  it.each([
    { kind: 'provider_key' as const, destination: 'OpenAI · https://api.openai.com/v1' },
    { kind: 'mcp_env' as const, destination: 'Notion · npx notion-mcp · TOKEN' },
    { kind: 'channel_credential' as const, destination: 'Telegram connection tg-1 · bot_token' },
    { kind: 'mailbox_password' as const, destination: 'Sales / Mia · imap.a.example · account x' },
  ])('FR-061 / K2: $kind card shows the exact server destination, not an inferred recipient', ({ kind, destination }) => {
    mount({ ...frame, owner: undefined, recipient: { kind, lines: [destination] } })
    expect(screen.getByText(destination)).toBeInTheDocument()
    expect(screen.getByText(/^Secret requester Mia(?:\s|$)/)).toBeInTheDocument()
    expect(labelledControl('Weather token')).toHaveAttribute('type', 'password')
    expect(requests).toEqual([])
  })

  it('FR-062/075/083 / BDD-6.2/6.15: values travel once in authenticated REST, not state/cache/frames/logs or password routes', async () => {
    mount()
    fireEvent.change(labelledControl('Weather token'), { target: { value: SENTINEL } })
    assertNoLeak() // includes emissions during typing, not only the final state
    fireEvent.click(screen.getByRole('button', { name: /submit|save/i }))
    await waitFor(() => expect(document.body).toHaveTextContent(reference))
    expect(requests.map(c => ({ path: c.path, method: c.method, body: JSON.parse(c.body!), credentials: c.credentials }))).toEqual([
      { path: '/api/v1/secret-requests/sec-l1/submit', method: 'POST', body: { values: { token: SENTINEL } }, credentials: 'include' },
    ])
    expect(requests[0].headers.get('X-CSRF-Token')).toBe('l1-red-csrf')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.queryByLabelText(/password|re.?enter/i)).not.toBeInTheDocument()
    // Exercise a later real API read and a later real chat-store send. Empty
    // sinks are not secrecy evidence (BDD-6.2's non-empty-sink requirement).
    const api = await loadSecretApi()
    expect(await api.getSecretRequest(frame.request_id)).toEqual(saved)
    act(() => useChatStore.getState().sendMessage('Continue with the saved reference'))
    expect(socketSends.map(raw => JSON.parse(raw))).toContainEqual(expect.objectContaining({ type: 'message', content: 'Continue with the saved reference', session_id: frame.session_id }))
    console.info('value-free log control')
    expect(outputSpies.flatMap(spy => spy.mock.calls).length).toBeGreaterThan(0)
    expect(requests.filter(c => c.path !== '/api/v1/secret-requests/sec-l1/submit')).toHaveLength(1)
    expect(requests.filter(c => c.method === 'POST')).toHaveLength(1)
    assertNoLeak()
  })

  it('FR-074/075 / BDD-6.4/6.10: in-flight submit is disabled and rapid second click never sends twice', async () => {
    const pending = deferred<Response>()
    installNetwork(() => pending.promise)
    mount()
    const input = labelledControl('Weather token')
    fireEvent.change(input, { target: { value: SENTINEL } })
    const submit = screen.getByRole('button', { name: /submit|save/i })
    fireEvent.click(submit)
    // CHECK2 B-S2-submit-inflight / B-S2-refusal-inflight: FR-075 S2
    // clears at handoff, before any response or eventual disposal can help.
    expect(input).toHaveValue('')
    expect(JSON.parse(requests[0].body!)).toEqual({ values: { token: SENTINEL } })
    fireEvent.click(submit)
    await waitFor(() => expect(submit).toBeDisabled())
    expect(requests).toHaveLength(1)
    assertNoLeak() // mutation variables must not retain the secret either
    await act(async () => pending.resolve(jsonResponse(saved)))
    await waitFor(() => expect(document.body).toHaveTextContent(reference))
    expect(requests).toHaveLength(1)
  })

  it('FR-074/075: cancel sends cancel only, even when the field already contains a secret', async () => {
    const pending = deferred<Response>()
    installNetwork(() => pending.promise)
    mount()
    const input = labelledControl('Weather token')
    fireEvent.change(input, { target: { value: SENTINEL } })
    fireEvent.click(screen.getByRole('button', { name: /^cancel$/i }))
    // CHECK2 B-S2-submit-cancel: do not wait for terminal cleanup.
    expect(input).toHaveValue('')
    expect(requests.map(c => JSON.parse(c.body!))).toEqual([{ cancel: true }])
    assertNoLeak()
    await act(async () => pending.resolve(jsonResponse({ ...saved, state: 'cancelled', reference_names: undefined })))
    await waitFor(() => expect(surface.store.getState().byId['sec-l1'].request.state).toBe('cancelled'))
    expect(requests.map(c => JSON.parse(c.body!))).toEqual([{ cancel: true }])
    expect(input).toHaveValue('')
    assertNoLeak()
  })

  it('FR-075: confirmed REST submit clears the detached value control as well as shared state', async () => {
    const pending = deferred<Response>()
    installNetwork(() => pending.promise)
    mount()
    const input = labelledControl('Weather token')
    fireEvent.change(input, { target: { value: SENTINEL } })
    fireEvent.click(screen.getByRole('button', { name: /submit|save/i }))
    // CHECK2 B-S2-submit-success: observe before the redundant disposal wipe.
    expect(input).toHaveValue('')
    expect(requests).toHaveLength(1)
    expect(JSON.parse(requests[0].body!)).toEqual({ values: { token: SENTINEL } })
    assertNoLeak()
    await act(async () => pending.resolve(jsonResponse(saved)))
    await waitFor(() => expect(surface.store.getState().byId[frame.request_id].request.state).toBe('submitted'))
    expect(input).toHaveValue('')
    expect(requests.filter(c => c.method === 'POST')).toHaveLength(1)
    assertNoLeak()
  })

  // FR-071 byte boundaries, not guessed character limits. 3=min-1; 4=min;
  // 5=min+1; 16383=max-1; 16384=max; 16385=max+1.
  it.each([3, 4, 5, 16_383, 16_384, 16_385])('FR-071: ASCII value of %i UTF-8 bytes respects inclusive 4..16384', async bytes => {
    mount()
    const value = 'a'.repeat(bytes)
    const input = labelledControl('Weather token')
    fireEvent.change(input, { target: { value } })
    fireEvent.click(screen.getByRole('button', { name: /submit|save/i }))
    if (bytes < 4 || bytes > 16_384) {
      expect(requests).toEqual([])
      expect(input).toHaveAttribute('aria-invalid', 'true')
      const errorId = input.getAttribute('aria-describedby')
      expect(errorId).not.toBeNull()
      expect(document.getElementById(errorId!)?.textContent).toMatch(/4|16,?384/)
    } else {
      await waitFor(() => expect(requests).toHaveLength(1))
      expect(JSON.parse(requests[0].body!)).toEqual({ values: { token: value } })
    }
  })

  it.each([
    { value: '', reason: 'required' }, { value: 'abc', reason: 'short' }, { value: 'a\u0000bcd', reason: 'NUL' },
    { value: 'é'.repeat(8_193), reason: 'multibyte overflow' }, // 16,386 bytes, only 8,193 UTF-16 units
  ])('FR-071: rejects $reason without sending or echoing the value in the error', ({ value }) => {
    mount()
    const input = labelledControl('Weather token')
    fireEvent.change(input, { target: { value } })
    fireEvent.click(screen.getByRole('button', { name: /submit|save/i }))
    expect(requests).toEqual([])
    expect(input).toHaveAttribute('aria-invalid', 'true')
    const message = document.getElementById(input.getAttribute('aria-describedby')!)
    expect(message).toHaveAttribute('role', 'alert')
    expect(message).not.toBeEmptyDOMElement()
    expect(input).toHaveAccessibleDescription(message!.textContent!)
    if (value.length > 3) expect(message?.textContent).not.toContain(value)
  })

  it('FR-071: accepts four UTF-8 bytes even when they are only two characters', async () => {
    mount()
    fireEvent.change(labelledControl('Weather token'), { target: { value: 'éé' } })
    fireEvent.click(screen.getByRole('button', { name: /submit|save/i }))
    await waitFor(() => expect(requests).toHaveLength(1))
    expect(JSON.parse(requests[0].body!)).toEqual({ values: { token: 'éé' } })
  })

  it.each([0, 1, 2, 7, 8, 9])('FR-071 / K1: a card with %i fields is actionable only for 1..8', async count => {
    mount({ ...frame, fields: Array.from({ length: count }, (_, i) => ({ key: `field_${i}`, label: `Field ${i}` })) })
    if (count === 0 || count === 9) {
      const submit = screen.queryByRole('button', { name: /submit|save/i })
      if (submit) {
        expect(submit).toBeDisabled()
        fireEvent.click(submit)
      }
      expect(requests).toEqual([])
    } else {
      for (let i = 0; i < count; i++) fireEvent.change(labelledControl(`Field ${i}`), { target: { value: 'abcd' } })
      fireEvent.click(screen.getByRole('button', { name: /submit|save/i }))
      await waitFor(() => expect(requests).toHaveLength(1))
      expect(JSON.parse(requests[0].body!)).toEqual({ values: Object.fromEntries(Array.from({ length: count }, (_, i) => [`field_${i}`, 'abcd'])) })
    }
  })

  it.each([65_535, 65_536, 65_537])('FR-071: serialized submit body of %i bytes respects the inclusive 65536 ceiling', async bytes => {
    const fields = ['a', 'b', 'c', 'd'].map(key => ({ key, label: `Token ${key}` }))
    // ASCII envelope = JSON.stringify({values:{a:'',b:'',c:'',d:''}}), so
    // counting its bytes independently gives an exact boundary, not a magic margin.
    const envelopeBytes = new TextEncoder().encode(JSON.stringify({ values: { a: '', b: '', c: '', d: '' } })).length
    const finalSize = bytes - envelopeBytes - 3 * 16_384
    mount({ ...frame, fields })
    for (let i = 0; i < fields.length; i++) fireEvent.change(labelledControl(fields[i].label), { target: { value: 'x'.repeat(i < 3 ? 16_384 : finalSize) } })
    fireEvent.click(screen.getByRole('button', { name: /submit|save/i }))
    if (bytes > 65_536) {
      expect(requests).toEqual([])
      expect(screen.getByRole('alert')).not.toBeEmptyDOMElement()
    } else {
      await waitFor(() => expect(requests).toHaveLength(1))
      expect(new TextEncoder().encode(requests[0].body).length).toBe(bytes)
    }
  })

  it('FR-071 / BDD-6.9: a two-field card never submits a partial map', () => {
    mount({ ...frame, fields: [{ key: 'bot_token', label: 'Bot token' }, { key: 'app_token', label: 'App token' }] })
    fireEvent.change(labelledControl('Bot token'), { target: { value: SENTINEL } })
    fireEvent.click(screen.getByRole('button', { name: /submit|save/i }))
    expect(requests).toEqual([])
    expect(labelledControl('App token')).toHaveAttribute('aria-invalid', 'true')
    assertNoLeak()
  })
})

it('BDD-6.2 instrument control: the sentinel detector sees a deliberate store/cache/frame/log leak', () => {
  const cyclic: { value: string; self?: unknown } = { value: SENTINEL }
  cyclic.self = cyclic
  for (const sink of [{ state: { token: SENTINEL } }, { variables: { values: { token: SENTINEL } } }, [JSON.stringify({ type: 'message', text: SENTINEL })], ['logged', SENTINEL], new Error(SENTINEL), cyclic, new Map([['leak', SENTINEL]]), new Set([SENTINEL])]) {
    expect(containsSentinel(sink, SENTINEL)).toBe(true)
    expect(containsSentinel(sink, 'different sentinel')).toBe(false)
  }
})


it('FR-109 instrument control: required Field label is locatable and has its exact accessible name', () => {
  render(<Field label="Required sample" required><Input type="password" /></Field>)
  expect(labelledControl('Required sample')).toHaveAccessibleName('Required sample')
})
