// Oracle: founder's Train 2 round-4 frontend brief, requirements 1–8.
// Real chat card, Connectors screen/configure panel, stores, HTTP client and
// catalog controls. Only fetch and WebSocket send are doubled at process edges.
// Mutations: drop each exact frame message; offer Retry during teardown; drop
// linked detail; genericize 500/409; mis-map 404; remove/over-broaden recovery.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { WhatsAppPairingFrame } from '@/lib/api/generated/asyncapi-types'
import type { ChannelEntry, ChannelRouting, ErrorResponse } from '@/lib/api/generated/openapi-types'
import type { WsConnection } from '@/lib/ws'
import { ConnectChannelTool } from '@/components/chat/tools/ConnectChannelTool'
import { ConnectorsScreen } from '@/components/screens/ConnectorsScreen'
import { ToastContainer } from '@/components/ui/toast-container'
import { useConnectionStore } from '@/store/connection'
import { useUiStore } from '@/store/ui'
import { useWhatsAppPairingStore } from '@/store/whatsappPairing'
import { ChannelConfigPanel } from './ChannelConfigPanel'

const CHANNEL = 'whatsapp.sales'
const REQUEST = 'original-connect-call-id'
const SESSION = 'requesting-chat-id'
// Expected strings are transcribed from the brief, never imported from production.
const INTERRUPTED = 'This pairing was interrupted because the WhatsApp connection was changed or restarted. Press Retry to get a new code.'
const LOCAL_DATA = 'WhatsApp could not open its local session data. Check that the Omnipus data directory is readable and writable, then press Retry.'
const NETWORK = 'WhatsApp could not be reached. Check the network connection, then press Retry.'
const START_FAILED = 'The WhatsApp pairing could not start. Details are in the server log. Press Retry to try again.'
const STOP_PENDING = 'Stopping this pairing has not finished yet. Omnipus keeps trying; a new pairing can start once it has stopped.'
const LINKED = 'This WhatsApp device is already linked.'
const CANCELLED = 'The WhatsApp connection is turned on, but its pairing was cancelled before it started. Press Retry to start a new one.'
const IN_PROGRESS = 'Another WhatsApp pairing of this connection is starting, running or stopping, so nothing was changed. Wait for it to finish, or stop it from the chat that started it.'
const DELETED = 'This WhatsApp connection was deleted. Add it again from Connectors.'
const CONFLICT = 'This chat can no longer retry this pairing. Open the WhatsApp connector to start a new pairing.'
const UNKNOWN_PREFIX = 'The channel was disabled before the enable request, but its current state could not be confirmed. '
const SURFACES = ['chat', 'Connectors'] as const
const clients: QueryClient[] = []

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })
}

function apply(status: WhatsAppPairingFrame['status'], message = '', identity: Pick<WhatsAppPairingFrame, 'request_id' | 'session_id'> = { request_id: REQUEST, session_id: SESSION }) {
  const frame: WhatsAppPairingFrame = {
    type: 'whatsapp_pairing', channel_id: CHANNEL, status, message,
    ...(status === 'code' ? { qr: 'fresh-round4-code' } : {}), ...identity,
  }
  act(() => useWhatsAppPairingStore.getState().apply(frame))
}

function client() {
  const cache = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
  })
  const routing: ChannelRouting = { default_agent_id: '', workspace_id: '', bound: false }
  cache.setQueryData(['channel-config', CHANNEL], {})
  cache.setQueryData(['channel-routing', CHANNEL], routing)
  cache.setQueryData(['agents'], [])
  cache.setQueryData(['workspaces', { status: 'active' }], [])
  cache.setQueryData(['workspaces', { status: 'all' }], [])
  cache.setQueryData(['agent-mailboxes'], [])
  clients.push(cache)
  return cache
}

function mount(surface: typeof SURFACES[number], enabled = true) {
  return render(surface === 'chat' ? (
    <ConnectChannelTool toolCallId={REQUEST} sessionId={SESSION} channelId={CHANNEL} live />
  ) : (
    <QueryClientProvider client={client()}>
      <ChannelConfigPanel channelId={CHANNEL} channelName="WhatsApp" enabled={enabled} open onOpenChange={vi.fn()} />
    </QueryClientProvider>
  ))
}

function failEnable(status: number, error: string, code?: string) {
  const body: ErrorResponse = { error, ...(code ? { code } : {}) }
  return vi.spyOn(globalThis, 'fetch')
    .mockResolvedValueOnce(json({ id: CHANNEL, enabled: false }))
    .mockResolvedValueOnce(json(body, status))
}

async function retry() {
  await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Retry' })))
}

function exactMessage(message: string) {
  expect(screen.getByText(message, { exact: true }).textContent).toBe(message)
}

beforeEach(() => {
  document.cookie = 'csrf=round4-test; path=/'
  useWhatsAppPairingStore.setState({ byChannel: {} })
  useUiStore.setState({ toasts: [] })
  useConnectionStore.setState({ isConnected: true, connection: { send: vi.fn(() => true) } as unknown as WsConnection })
})

afterEach(() => {
  clients.splice(0).forEach((cache) => cache.clear())
  vi.restoreAllMocks()
  document.cookie = 'csrf=; max-age=0; path=/'
})

describe.each(SURFACES)('%s — exact server pairing states', (surface) => {
  // Round-6 chief-approved fixture correction: the NEW Connectors Retry is
  // unowned even when its displayed ended frame belonged to a chat. Chat
  // fixtures retain exactly the original REQUEST and SESSION bytes.
  const retryIdentity: Pick<WhatsAppPairingFrame, 'request_id' | 'session_id'> = surface === 'chat'
    ? { request_id: REQUEST, session_id: SESSION }
    : {}

  it.each([INTERRUPTED, LOCAL_DATA, NETWORK, START_FAILED])('shows the exact actionable server detail, with Retry: %s', (message) => {
    mount(surface)
    apply('error', message)
    exactMessage(message)
    // Chief ruling: ordinary error frames retain the existing generic heading.
    exactMessage('Pairing failed — select to retry.')
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
    expect(screen.queryByText('Generating your QR code…')).not.toBeInTheDocument()
  })

  it('withholds Retry while Stop is pending, then replaces the message on stopped', () => {
    mount(surface)
    apply('error', STOP_PENDING)
    exactMessage(STOP_PENDING)
    expect(screen.queryByText('Pairing failed — select to retry.')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
    expect(screen.queryByText('Generating your QR code…')).not.toBeInTheDocument()
    apply('stopped', 'Pairing stopped.')
    exactMessage('Pairing stopped.')
    expect(screen.queryByText(STOP_PENDING)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
  })

  it('shows the already-linked server text in linked state without Retry or QR wait', () => {
    mount(surface)
    apply('linked', LINKED)
    exactMessage(LINKED)
    expect(screen.queryByText('Linked successfully.')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
    expect(screen.queryByText('Generating your QR code…')).not.toBeInTheDocument()
  })

  it.each([
    { status: 'error', message: INTERRUPTED, retryable: true },
    { status: 'error', message: LOCAL_DATA, retryable: true },
    { status: 'error', message: NETWORK, retryable: true },
    { status: 'error', message: START_FAILED, retryable: true },
    { status: 'error', message: STOP_PENDING, retryable: false },
    { status: 'linked', message: LINKED, retryable: false },
  ] satisfies { status: WhatsAppPairingFrame['status']; message: string; retryable: boolean }[])('after an accepted Retry, $status replaces QR wait: $message', async ({ status, message, retryable }) => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(json({ id: CHANNEL, enabled: false }))
      .mockResolvedValueOnce(json({ id: CHANNEL, enabled: true }))
    apply('timeout')
    mount(surface)
    await retry()
    exactMessage('Generating your QR code…')
    apply(status, message, retryIdentity)
    exactMessage(message)
    expect(screen.queryByText('Generating your QR code…')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' }) !== null).toBe(retryable)
  })

  it('shows the enable-cancelled 500 verbatim, never says disabled, and offers Retry', async () => {
    const fetchSpy = failEnable(500, CANCELLED)
    apply('timeout')
    mount(surface)
    await retry()
    exactMessage(CANCELLED)
    expect(screen.queryByText(/The channel is now disabled|current state could not be confirmed/)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
    expect(fetchSpy).toHaveBeenCalledTimes(2)
  })

  it('keeps the server reason for a generic enable 500 alongside the unknown-state explanation', async () => {
    failEnable(500, 'config reload failed')
    apply('timeout')
    mount(surface)
    await retry()
    exactMessage(UNKNOWN_PREFIX + 'config reload failed')
    expect(screen.queryByText(/The server is unavailable/)).not.toBeInTheDocument()
  })

  it('shows pairing_in_progress verbatim as transient, not pairing_retry_conflict', async () => {
    failEnable(409, IN_PROGRESS, 'pairing_in_progress')
    apply('timeout')
    mount(surface)
    await retry()
    exactMessage(IN_PROGRESS)
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
    expect(screen.queryByText(CONFLICT)).not.toBeInTheDocument()
  })

  it.each([
    { status: 404, error: 'channel not found', code: undefined, expected: DELETED, retryable: false },
    { status: 409, error: CONFLICT, code: 'pairing_retry_conflict', expected: 'The channel is now disabled. ' + CONFLICT, retryable: false },
    { status: 409, error: IN_PROGRESS, code: 'pairing_in_progress', expected: IN_PROGRESS, retryable: true },
  ])('$status $code remains sticky after a matching later QR', async ({ status, error, code, expected, retryable }) => {
    failEnable(status, error, code)
    apply('timeout')
    mount(surface)
    await retry()
    exactMessage(expected)
    apply('code', '', retryIdentity)
    exactMessage(expected)
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' }) !== null).toBe(retryable)
  })

  it.each(['code', 'waiting', 'linked', 'error', 'stopped', 'timeout'] as const)('a later matching %s frame clears the recoverable 500', async (status) => {
    failEnable(500, 'config reload failed')
    apply('timeout')
    mount(surface)
    await retry()
    expect(screen.getByRole('alert')).toBeInTheDocument()
    apply(status, status === 'error' ? NETWORK : status === 'linked' ? LINKED : '', retryIdentity)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.queryByText(/current state could not be confirmed/)).not.toBeInTheDocument()
    if (status === 'code') expect(screen.getByTestId('whatsapp-qr')).toBeInTheDocument()
    if (status === 'waiting') exactMessage('Generating your QR code…')
    if (status === 'linked') exactMessage(LINKED)
    if (status === 'error') exactMessage(NETWORK)
    if (status === 'stopped') exactMessage('Pairing stopped.')
    if (status === 'timeout') exactMessage('QR expired — select to get a fresh one.')
  })

  it.each([
    { request_id: 'another-request', session_id: SESSION },
    { request_id: REQUEST, session_id: 'another-session' },
    { request_id: surface === 'chat' ? '' : REQUEST, session_id: '' },
  ])('another or missing request/session cannot clear the 500: %j', async (identity) => {
    failEnable(500, 'config reload failed')
    apply('timeout')
    mount(surface)
    await retry()
    const original = screen.getByRole('alert').textContent
    apply('code', '', identity)
    expect(screen.getByRole('alert').textContent).toBe(original)
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
  })

  it('an early QR before the failed response is not a later confirmation', async () => {
    let respond!: (response: Response) => void
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(json({ id: CHANNEL, enabled: false }))
      .mockReturnValueOnce(new Promise<Response>((resolve) => { respond = resolve }))
    apply('timeout')
    mount(surface)
    await retry()
    apply('code', '', retryIdentity)
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
    await act(async () => respond(json({ error: 'config reload failed' } satisfies ErrorResponse, 500)))
    expect(screen.getByRole('alert')).toBeInTheDocument()
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
    apply('code', '', retryIdentity)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByTestId('whatsapp-qr')).toBeInTheDocument()
  })
})

describe.each(['row Enable', 'Save & Enable'] as const)('Connectors — ordinary %s', (entry) => {
  it.each([
    { status: 500, error: CANCELLED, code: undefined, expected: CANCELLED },
    { status: 500, error: 'config reload failed', code: undefined, expected: 'config reload failed' },
    { status: 409, error: IN_PROGRESS, code: 'pairing_in_progress', expected: IN_PROGRESS },
    { status: 404, error: 'channel not found', code: undefined, expected: DELETED },
  ])('shows the exact $status server/deletion message', async ({ status, error, code, expected }) => {
    const channel: ChannelEntry = {
      id: CHANNEL, instance_id: CHANNEL, name: 'WhatsApp', transport: 'native',
      enabled: false, description: 'WhatsApp native', native_available: true,
    }
    const body: ErrorResponse = { error, ...(code ? { code } : {}) }
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockImplementation(async (url) => {
      if (String(url).endsWith('/enable')) return json(body, status)
      if (String(url).endsWith('/configure')) return json({})
      if (String(url).endsWith('/channels')) return json([channel])
      return json({})
    })
    const cache = client()
    cache.setQueryData(['channels'], [channel])
    render(
      <QueryClientProvider client={cache}>
        {entry === 'row Enable' ? <ConnectorsScreen /> : (
          <ChannelConfigPanel channelId={CHANNEL} channelName="WhatsApp" enabled={false} open onOpenChange={vi.fn()} />
        )}
        <ToastContainer />
      </QueryClientProvider>,
    )
    await act(async () => fireEvent.click(screen.getByRole('button', {
      name: entry === 'row Enable' ? `Enable ${CHANNEL}` : 'Save & Enable',
    })))
    // The modal sheet marks outside siblings aria-hidden, including the real
    // app-root toast. Check its rendered copy without claiming accessibility.
    const alert = await screen.findByRole('alert', { hidden: true })
    expect(within(alert).getByText(expected, { exact: true }).textContent).toBe(expected)
    expect(screen.queryByText(/The channel is now disabled|The server is unavailable/)).not.toBeInTheDocument()
    expect(fetchSpy.mock.calls.filter(([url]) => String(url).endsWith('/enable'))).toHaveLength(1)
    expect(fetchSpy.mock.calls.find(([url]) => String(url).endsWith('/enable'))?.[1]?.body).toBeUndefined()
    if (status === 404) expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
  })
})
