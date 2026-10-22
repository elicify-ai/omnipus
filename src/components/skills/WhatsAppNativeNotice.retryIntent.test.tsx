// Oracle: chief's founder-approved round 6, CR-T2-R5-03 / T2-SF-17.
// Recovery belongs to the NEW Retry intent, never the previously displayed
// attempt. Real Configure panel/chat card, notice, stores and HTTP client;
// doubles only at fetch and WebSocket send. No application-only toast import.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { WhatsAppPairingFrame } from '@/lib/api/generated/asyncapi-types'
import type { ChannelEnabledResponse, ChannelRouting, ErrorResponse } from '@/lib/api/generated/openapi-types'
import type { WsConnection } from '@/lib/ws'
import { ConnectChannelTool } from '@/components/chat/tools/ConnectChannelTool'
import { useConnectionStore } from '@/store/connection'
import { useWhatsAppPairingStore } from '@/store/whatsappPairing'
import { ChannelConfigPanel } from './ChannelConfigPanel'

const CHANNEL = 'whatsapp.sales'
const REQUEST = 'ended-chat-connect-call'
const SESSION = 'ended-chat-session'
// Exact wording supplied by chief for backend round-6 item 6, not production imports.
const ORDINARY_UNCONFIRMED = 'The WhatsApp connection is enabled, but its restart could not be confirmed yet. The new code appears here when the pairing starts.'
const CHAT_UNCONFIRMED = 'The WhatsApp connection is enabled, but its restart could not be confirmed yet. The new code appears in this chat when the pairing starts.'
const UNKNOWN_PREFIX = 'The channel was disabled before the enable request, but its current state could not be confirmed. '
const DELETED = 'This WhatsApp connection was deleted. Add it again from Connectors.'
const IN_PROGRESS = 'Another WhatsApp pairing of this connection is starting, running or stopping, so nothing was changed. Wait for it to finish, or stop it from the chat that started it.'
const clients: QueryClient[] = []

function json(body: ChannelEnabledResponse | ErrorResponse, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })
}

function apply(frame: Omit<WhatsAppPairingFrame, 'type' | 'channel_id'>) {
  act(() => useWhatsAppPairingStore.getState().apply({
    type: 'whatsapp_pairing', channel_id: CHANNEL, ...frame,
  }))
}

function mountConfigure() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
  })
  const routing: ChannelRouting = { default_agent_id: '', workspace_id: '', bound: false }
  client.setQueryData(['channel-config', CHANNEL], {})
  client.setQueryData(['channel-routing', CHANNEL], routing)
  client.setQueryData(['agents'], [])
  client.setQueryData(['workspaces', { status: 'active' }], [])
  clients.push(client)
  return render(
    <QueryClientProvider client={client}>
      <ChannelConfigPanel channelId={CHANNEL} channelName="WhatsApp" enabled open onOpenChange={vi.fn()} />
    </QueryClientProvider>,
  )
}

function rejectEnable(error = ORDINARY_UNCONFIRMED, status = 500, code?: string) {
  return vi.spyOn(globalThis, 'fetch')
    .mockResolvedValueOnce(json({ id: CHANNEL, enabled: false }))
    .mockResolvedValueOnce(json({ error, ...(code ? { code } : {}) }, status))
}

async function retry() {
  await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Retry' })))
}

beforeEach(() => {
  document.cookie = 'csrf=retry-intent-test; path=/'
  useWhatsAppPairingStore.setState({ byChannel: {} })
  useConnectionStore.setState({ isConnected: true, connection: { send: vi.fn(() => true) } as unknown as WsConnection })
})

afterEach(() => {
  clients.splice(0).forEach((client) => client.clear())
  vi.restoreAllMocks()
  document.cookie = 'csrf=; max-age=0; path=/'
})

describe('Configure Retry uses the new unowned intent', () => {
  it.each(['timeout', 'error', 'stopped'] as const)('a late unowned QR clears the unconfirmed 500 after a chat-owned %s', async (status) => {
    const fetchSpy = rejectEnable()
    apply({ status, message: 'The earlier chat pairing ended.', request_id: REQUEST, session_id: SESSION })
    mountConfigure()
    await retry()

    expect(fetchSpy.mock.calls.map(([url, init]) => ({ url, method: init?.method, body: init?.body }))).toEqual([
      { url: '/api/v1/channels/whatsapp.sales/disable', method: 'PUT', body: undefined },
      { url: '/api/v1/channels/whatsapp.sales/enable', method: 'PUT', body: undefined },
    ])
    expect(screen.getByRole('alert').textContent).toBe(UNKNOWN_PREFIX + ORDINARY_UNCONFIRMED + 'Retry')
    expect(screen.getByText(UNKNOWN_PREFIX + ORDINARY_UNCONFIRMED, { exact: true }).textContent).toBe(UNKNOWN_PREFIX + ORDINARY_UNCONFIRMED)
    expect(screen.queryByText(/appears in this chat/)).not.toBeInTheDocument()
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()

    // Deliberately no request_id or session_id: bodyless enable starts unowned.
    apply({ status: 'code', qr: 'new-unowned-configure-code' })
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByTestId('whatsapp-qr')).toBeVisible()
    expect(screen.queryByText('Generating your QR code…')).not.toBeInTheDocument()
    expect(fetchSpy).toHaveBeenCalledTimes(2)
  })

  it.each([
    { request_id: REQUEST, session_id: SESSION },
    { request_id: 'another-request', session_id: 'another-session' },
    { request_id: REQUEST },
    { session_id: SESSION },
  ])('an owned or partly identified frame cannot confirm the ordinary Retry: %j', async (identity) => {
    rejectEnable()
    apply({ status: 'timeout', request_id: REQUEST, session_id: SESSION })
    mountConfigure()
    await retry()
    const original = screen.getByRole('alert').textContent

    apply({ status: 'code', qr: 'not-the-new-unowned-attempt', ...identity })
    expect(screen.getByRole('alert').textContent).toBe(original)
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
    apply({ status: 'code', qr: 'new-unowned-attempt' })
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByTestId('whatsapp-qr')).toBeInTheDocument()
  })

  it.each([
    { status: 404, error: 'channel not found', code: undefined, expected: DELETED, retryable: false },
    { status: 409, error: IN_PROGRESS, code: 'pairing_in_progress', expected: IN_PROGRESS, retryable: true },
  ])('$status stays sticky after an unowned QR', async ({ status, error, code, expected, retryable }) => {
    rejectEnable(error, status, code)
    apply({ status: 'timeout', request_id: REQUEST, session_id: SESSION })
    mountConfigure()
    await retry()
    expect(screen.getByText(expected, { exact: true }).textContent).toBe(expected)

    apply({ status: 'code', qr: 'cannot-overwrite-refusal' })
    expect(screen.getByText(expected, { exact: true }).textContent).toBe(expected)
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' }) !== null).toBe(retryable)
  })
})

describe('Chat Retry still uses explicit request and session', () => {
  it.each([
    {},
    { request_id: 'another-request', session_id: SESSION },
    { request_id: REQUEST, session_id: 'another-session' },
  ])('rejects %j, then accepts only its own later QR', async (identity) => {
    const fetchSpy = rejectEnable(CHAT_UNCONFIRMED)
    // Even the displayed ended frame's session is not the new Retry's intent.
    apply({ status: 'timeout', request_id: REQUEST, session_id: 'older-frame-session' })
    render(<ConnectChannelTool toolCallId={REQUEST} sessionId={SESSION} channelId={CHANNEL} live />)
    await retry()
    expect(fetchSpy.mock.calls[1][1]?.body).toBe('{"pairing_retry":{"request_id":"ended-chat-connect-call","session_id":"ended-chat-session"}}')
    const original = screen.getByRole('alert').textContent
    expect(original).toBe(UNKNOWN_PREFIX + CHAT_UNCONFIRMED + 'Retry')

    apply({ status: 'code', qr: 'wrong-identity', ...identity })
    expect(screen.getByRole('alert').textContent).toBe(original)
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
    apply({ status: 'code', qr: 'new-explicit-chat-code', request_id: REQUEST, session_id: SESSION })
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByTestId('whatsapp-qr')).toBeInTheDocument()
  })
})
