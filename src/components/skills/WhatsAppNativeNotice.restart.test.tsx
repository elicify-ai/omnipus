// Oracle: chief's Train 2 round 3 F5. A gateway restart sends an error
// frame with the exact restart message and NO request/session ids. Both live
// mount sites must render it and let the user explicitly Retry for a new QR.
// Real chat card, Connectors configure panel, notice, stores and API client;
// only fetch/WebSocket send are doubled. Unrelated read data is cached.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { WhatsAppPairingFrame } from '@/lib/api/generated/asyncapi-types'
import type { ChannelEnabledResponse, ChannelRouting } from '@/lib/api/generated/openapi-types'
import type { WsConnection } from '@/lib/ws'
import { ConnectChannelTool } from '@/components/chat/tools/ConnectChannelTool'
import { useConnectionStore } from '@/store/connection'
import { useWhatsAppPairingStore } from '@/store/whatsappPairing'
import { ChannelConfigPanel } from './ChannelConfigPanel'

const CHANNEL = 'whatsapp.sales'
const REQUEST = 'original-connect-call-id'
const SESSION = 'requesting-chat-id'
const RESTART_MESSAGE = 'A server restart interrupted this pairing. Press Retry to get a new code.'
const sendSpy = vi.fn(() => true)

function enabledResponse(enabled: boolean): Response {
  const body: ChannelEnabledResponse = { id: CHANNEL, enabled }
  return new Response(JSON.stringify(body), { headers: { 'content-type': 'application/json' } })
}

beforeEach(() => {
  sendSpy.mockClear()
  document.cookie = 'csrf=restart-notice-test; path=/'
  useWhatsAppPairingStore.setState({ byChannel: {} })
  useConnectionStore.setState({
    isConnected: true,
    connection: { send: sendSpy } as unknown as WsConnection,
  })
})

afterEach(() => {
  vi.restoreAllMocks()
  document.cookie = 'csrf=; max-age=0; path=/'
})

describe('restart-interrupted pairing — both real mount sites', () => {
  it.each(['chat', 'Connectors'] as const)('%s shows the exact uncorrelated restart error and Retry requests a fresh code', async (surface) => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
      .mockImplementation(async () => enabledResponse(true))
      .mockResolvedValueOnce(enabledResponse(false))
      .mockResolvedValueOnce(enabledResponse(true))
    // A previously owned QR must not hide the restart's uncorrelated error.
    useWhatsAppPairingStore.getState().apply({
      type: 'whatsapp_pairing', channel_id: CHANNEL, status: 'code', qr: 'pre-restart-code',
      request_id: REQUEST, session_id: SESSION,
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
    })
    const routing: ChannelRouting = { default_agent_id: '', workspace_id: '', bound: false }
    client.setQueryData(['channel-config', CHANNEL], {})
    client.setQueryData(['channel-routing', CHANNEL], routing)
    client.setQueryData(['agents'], [])
    client.setQueryData(['workspaces', { status: 'active' }], [])
    const view = render(surface === 'chat' ? (
      <ConnectChannelTool toolCallId={REQUEST} sessionId={SESSION} channelId={CHANNEL} live />
    ) : (
      <QueryClientProvider client={client}>
        <ChannelConfigPanel channelId={CHANNEL} channelName="WhatsApp" enabled open onOpenChange={vi.fn()} />
      </QueryClientProvider>
    ))
    try {
      expect(screen.getByTestId('whatsapp-qr')).toBeInTheDocument()
      act(() => useConnectionStore.setState({ isConnected: false }))
      act(() => useConnectionStore.setState({ isConnected: true }))
      // Deliberately omit request_id AND session_id, as the restart frame does.
      const restart: WhatsAppPairingFrame = {
        type: 'whatsapp_pairing', channel_id: CHANNEL, status: 'error', message: RESTART_MESSAGE,
      }
      act(() => useWhatsAppPairingStore.getState().apply(restart))

      expect(screen.getByText(RESTART_MESSAGE, { exact: true }).textContent).toBe(RESTART_MESSAGE)
      expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
      expect(screen.queryByText('Generating your QR code…')).not.toBeInTheDocument()
      expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
      expect(fetchSpy).not.toHaveBeenCalled()
      sendSpy.mockClear()

      await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Retry' })))

      expect(fetchSpy.mock.calls.map(([url, init]) => ({ url, method: init?.method, body: init?.body }))).toEqual([
        { url: '/api/v1/channels/whatsapp.sales/disable', method: 'PUT', body: undefined },
        {
          url: '/api/v1/channels/whatsapp.sales/enable', method: 'PUT',
          body: surface === 'chat'
            ? '{"pairing_retry":{"request_id":"original-connect-call-id","session_id":"requesting-chat-id"}}'
            : undefined,
        },
      ])
      expect(sendSpy).not.toHaveBeenCalled()
      expect(screen.getByText('Generating your QR code…')).toBeInTheDocument()
      act(() => useWhatsAppPairingStore.getState().apply({
        type: 'whatsapp_pairing', channel_id: CHANNEL, status: 'code', qr: 'fresh-after-retry',
        ...(surface === 'chat' ? { request_id: REQUEST, session_id: SESSION } : {}),
      }))
      expect(screen.getByTestId('whatsapp-qr')).toBeInTheDocument()
      expect(screen.queryByText(RESTART_MESSAGE)).not.toBeInTheDocument()
      expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
    } finally {
      view.unmount()
      client.clear()
    }
  })
})
