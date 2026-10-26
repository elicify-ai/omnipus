// Oracles: Train 2 F2–F5 and arch-retry-contract.md::Decision. REST alone
// admits a Retry; subscriptions (including reconnect) only observe. A refused
// enable never rolls back a successful disable. No local end-to-end test.
// Real notice, stores, API client, HTTP/ApiError and catalog components;
// doubles only at fetch and the WebSocket send edge.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ErrorResponse } from '@/lib/api/generated/openapi-types'
import type { WsConnection } from '@/lib/ws'
import { useConnectionStore } from '@/store/connection'
import { useWhatsAppPairingStore } from '@/store/whatsappPairing'
import { WhatsAppNativeNotice } from './WhatsAppNativeNotice'

const CHANNEL = 'whatsapp.sales'
const REQUEST = 'original-connect-call-id'
const SESSION = 'requesting-chat-id'
const CONFLICT = 'This chat can no longer retry this pairing. Open the WhatsApp connector to start a new pairing.'
const UNAVAILABLE = 'Pairing ownership service unavailable. Try again.'
const NETWORK = 'Network unavailable. Check your connection and try again.'
const DISABLED = 'The channel is now disabled. '

const sendSpy = vi.fn(() => true)

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })
}

function disabledResponse(): Response {
  return jsonResponse({ id: CHANNEL, enabled: false })
}

function seedTimeout(requestId = REQUEST): void {
  act(() => useWhatsAppPairingStore.getState().apply({
    type: 'whatsapp_pairing', channel_id: CHANNEL, status: 'timeout',
    request_id: requestId, session_id: SESSION, message: 'Pairing timed out.',
  }))
}

function mountChat() {
  seedTimeout()
  return render(<WhatsAppNativeNotice channelId={CHANNEL} requestId={REQUEST} sessionId={SESSION} />)
}

function requestSequence(calls: Parameters<typeof fetch>[]) {
  return calls.map(([url, init]) => ({ url, method: init?.method, body: init?.body }))
}

beforeEach(() => {
  sendSpy.mockClear()
  document.cookie = 'csrf=notice-retry-test; path=/'
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

describe('chat Retry — explicit REST ownership and visible failures', () => {
  it('disables then enables with the exact ORIGINAL connect and chat ids, without toggling subscriptions', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(disabledResponse())
      .mockResolvedValueOnce(jsonResponse({ id: CHANNEL, enabled: true }))
    mountChat()
    sendSpy.mockClear()

    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Retry' })))

    expect(requestSequence(fetchSpy.mock.calls)).toEqual([
      { url: '/api/v1/channels/whatsapp.sales/disable', method: 'PUT', body: undefined },
      {
        url: '/api/v1/channels/whatsapp.sales/enable', method: 'PUT',
        body: '{"pairing_retry":{"request_id":"original-connect-call-id","session_id":"requesting-chat-id"}}',
      },
    ])
    expect(sendSpy).not.toHaveBeenCalled()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('does not send enable until disable has succeeded', async () => {
    let finishDisable!: (response: Response) => void
    const pendingDisable = new Promise<Response>((resolve) => { finishDisable = resolve })
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
      .mockReturnValueOnce(pendingDisable)
      .mockResolvedValueOnce(jsonResponse({ id: CHANNEL, enabled: true }))
    mountChat()

    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Retry' })))
    expect(requestSequence(fetchSpy.mock.calls)).toEqual([
      { url: '/api/v1/channels/whatsapp.sales/disable', method: 'PUT', body: undefined },
    ])

    await act(async () => finishDisable(disabledResponse()))
    expect(fetchSpy).toHaveBeenCalledTimes(2)
    expect(fetchSpy.mock.calls[1][1]?.body).toBe('{"pairing_retry":{"request_id":"original-connect-call-id","session_id":"requesting-chat-id"}}')
  })

  it('awaits REST acceptance before showing even an early correlated QR frame', async () => {
    let acceptEnable!: (response: Response) => void
    const pendingEnable = new Promise<Response>((resolve) => { acceptEnable = resolve })
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(disabledResponse())
      .mockReturnValueOnce(pendingEnable)
    mountChat()

    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Retry' })))
    expect(fetchSpy).toHaveBeenCalledTimes(2)
    act(() => useWhatsAppPairingStore.getState().apply({
      type: 'whatsapp_pairing', channel_id: CHANNEL, status: 'code', qr: 'early-code',
      request_id: REQUEST, session_id: SESSION,
    }))
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
    expect(screen.getByText('Generating your QR code…')).toBeInTheDocument()

    await act(async () => acceptEnable(jsonResponse({ id: CHANNEL, enabled: true })))
    expect(screen.getByTestId('whatsapp-qr')).toBeInTheDocument()
  })

  it.each([
    CONFLICT,
    'This pairing has ended. Open the WhatsApp connector to start a new pairing.',
  ])('409 pairing_retry_conflict shows the exact server text and now-disabled state, with no bodyless fallback: %s', async (serverText) => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(disabledResponse())
      .mockResolvedValueOnce(jsonResponse({ code: 'pairing_retry_conflict', error: serverText }, 409))
    mountChat()
    sendSpy.mockClear()

    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Retry' })))

    expect(screen.getByRole('alert').textContent).toBe(DISABLED + serverText)
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
    expect(screen.queryByText('Generating your QR code…')).not.toBeInTheDocument()
    expect(fetchSpy).toHaveBeenCalledTimes(2)
    expect(sendSpy).not.toHaveBeenCalled()
  })

  // Round 3 F1/F3: a deleted instance is terminal at EITHER restart step.
  it.each(['disable', 'enable'] as const)('404 on %s shows the exact deletion message, removes Retry and makes no further calls', async (step) => {
    const deleted: ErrorResponse = { error: 'channel not found' }
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
      .mockImplementation(async () => jsonResponse({ id: CHANNEL, enabled: true }))
    if (step === 'disable') {
      fetchSpy.mockResolvedValueOnce(jsonResponse(deleted, 404))
    } else {
      fetchSpy.mockResolvedValueOnce(disabledResponse())
        .mockResolvedValueOnce(jsonResponse(deleted, 404))
    }
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    try {
      mountChat()
      sendSpy.mockClear()

      await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Retry' })))

      const message = 'This WhatsApp connection was deleted. Add it again from Connectors.'
      expect(screen.getByRole('alert').textContent).toBe(message)
      expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
      expect(screen.queryByText('Generating your QR code…')).not.toBeInTheDocument()
      // Even after the full Retry window, no timer may replace the terminal
      // notice, recreate the instance or send a second enable attempt.
      await act(async () => vi.advanceTimersByTimeAsync(90_000))
      expect(screen.getByRole('alert').textContent).toBe(message)
      expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
      expect(requestSequence(fetchSpy.mock.calls)).toEqual(step === 'disable' ? [
        { url: '/api/v1/channels/whatsapp.sales/disable', method: 'PUT', body: undefined },
      ] : [
        { url: '/api/v1/channels/whatsapp.sales/disable', method: 'PUT', body: undefined },
        {
          url: '/api/v1/channels/whatsapp.sales/enable', method: 'PUT',
          body: '{"pairing_retry":{"request_id":"original-connect-call-id","session_id":"requesting-chat-id"}}',
        },
      ])
      expect(sendSpy).not.toHaveBeenCalled()
    } finally {
      vi.useRealTimers()
    }
  })

  it('503 says the ownership service is unavailable, offers Retry and preserves the disabled-state message', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(disabledResponse())
      .mockResolvedValueOnce(jsonResponse({ error: 'session store unavailable' }, 503))
    mountChat()

    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Retry' })))

    expect(screen.getByRole('alert').textContent).toBe(DISABLED + UNAVAILABLE + 'Retry')
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    expect(screen.queryByText('Generating your QR code…')).not.toBeInTheDocument()
    expect(fetchSpy).toHaveBeenCalledTimes(2)
  })

  it('network failure during disable is actionable and never claims a successful disable or calls enable', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockRejectedValueOnce(new TypeError('Failed to fetch'))
    mountChat()

    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Retry' })))

    expect(screen.getByRole('alert').textContent).toBe(NETWORK + 'Retry')
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    expect(fetchSpy).toHaveBeenCalledTimes(1)
    expect(fetchSpy.mock.calls[0][0]).toBe('/api/v1/channels/whatsapp.sales/disable')
  })

  it('network failure during enable reports the confirmed prior disable, not a confirmed current state or rollback', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(disabledResponse())
      .mockRejectedValueOnce(new TypeError('Failed to fetch'))
    mountChat()

    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Retry' })))

    expect(screen.getByRole('alert').textContent).toBe(
      'The channel was disabled before the enable request, but its current state could not be confirmed. ' + NETWORK + 'Retry',
    )
    expect(fetchSpy).toHaveBeenCalledTimes(2)
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })

  it('a 500 after disable reports the known prior disable without inventing a rollback of an unknown partial enable', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(disabledResponse())
      .mockResolvedValueOnce(jsonResponse({ error: 'config reload failed' }, 500))
    mountChat()

    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Retry' })))

    expect(screen.getByRole('alert').textContent).toBe(
      'The channel was disabled before the enable request, but its current state could not be confirmed. config reload failedRetry',
    )
    expect(fetchSpy).toHaveBeenCalledTimes(2)
  })

  it('an enable refusal after disable remains visibly disabled and a deliberate Retry still sends the same ids', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(disabledResponse())
      .mockResolvedValueOnce(jsonResponse({ error: 'session store unavailable' }, 503))
      .mockResolvedValueOnce(disabledResponse())
      .mockResolvedValueOnce(jsonResponse({ id: CHANNEL, enabled: true }))
    mountChat()

    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Retry' })))
    expect(screen.getByRole('alert').textContent).toBe(DISABLED + UNAVAILABLE + 'Retry')
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Retry' })))

    expect(fetchSpy).toHaveBeenCalledTimes(4)
    expect(fetchSpy.mock.calls[1][1]?.body).toBe('{"pairing_retry":{"request_id":"original-connect-call-id","session_id":"requesting-chat-id"}}')
    expect(fetchSpy.mock.calls[3][1]?.body).toBe('{"pairing_retry":{"request_id":"original-connect-call-id","session_id":"requesting-chat-id"}}')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it.each([
    { requestId: REQUEST },
    { sessionId: SESSION },
    { requestId: REQUEST, sessionId: '' },
    { requestId: ' ', sessionId: SESSION },
  ])('missing chat identity never silently retries without a claim: %j', async (props) => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
    seedTimeout(props.requestId)
    render(<WhatsAppNativeNotice channelId={CHANNEL} {...props} />)

    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Retry' })))

    expect(screen.getByRole('alert').textContent).toBe(CONFLICT)
    expect(fetchSpy).not.toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
  })

  it('a passive reconnect sends only an observation subscription, never an enable request or ownership claim', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
      .mockImplementation(async () => jsonResponse({ id: CHANNEL, enabled: true }))
    mountChat()
    expect(sendSpy.mock.calls).toEqual([[{
      type: 'whatsapp_pairing_subscribe', channel_id: CHANNEL, active: true,
      request_id: REQUEST, session_id: SESSION,
    }]])
    const originalPairing = useWhatsAppPairingStore.getState().byChannel[CHANNEL]
    sendSpy.mockClear()

    act(() => useConnectionStore.setState({ isConnected: false }))
    expect(sendSpy).not.toHaveBeenCalled()
    act(() => useConnectionStore.setState({ isConnected: true }))

    await waitFor(() => expect(sendSpy.mock.calls).toEqual([[{
      type: 'whatsapp_pairing_subscribe', channel_id: CHANNEL, active: true,
      request_id: REQUEST, session_id: SESSION,
    }]]))
    expect(fetchSpy).not.toHaveBeenCalled()
    expect(useWhatsAppPairingStore.getState().byChannel[CHANNEL]).toBe(originalPairing)
    expect(screen.getByText('Pairing timed out.')).toBeInTheDocument()
  })
})
