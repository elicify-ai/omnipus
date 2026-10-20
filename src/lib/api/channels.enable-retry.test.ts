// Oracles: Train 2 F1/F5 and arch-retry-contract.md::Decision — optional
// ChannelEnableRequest, exact original ids, ordinary bodyless enable unchanged.
// Only fetch is doubled; the client, HTTP transport and response schema are real.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ChannelEnableRequest } from '@/lib/api/generated/openapi-types'
import { enableChannel } from './channels'

beforeEach(() => {
  document.cookie = 'csrf=channel-enable-test; path=/'
})

afterEach(() => {
  vi.restoreAllMocks()
  document.cookie = 'csrf=; max-age=0; path=/'
})

function enabledResponse(): Response {
  return new Response(JSON.stringify({ id: 'whatsapp.sales', enabled: true }), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

describe('enableChannel — optional explicit chat Retry body', () => {
  it('sends the exact generated Retry body and returns the validated enabled response', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => enabledResponse())
    const body: ChannelEnableRequest = {
      pairing_retry: { request_id: 'original-connect-call-id', session_id: 'requesting-chat-id' },
    }

    const result = await enableChannel('whatsapp.sales', body)

    expect(fetchSpy).toHaveBeenCalledExactlyOnceWith('/api/v1/channels/whatsapp.sales/enable', {
      method: 'PUT',
      body: '{"pairing_retry":{"request_id":"original-connect-call-id","session_id":"requesting-chat-id"}}',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': 'channel-enable-test' },
    })
    expect(result).toEqual({ id: 'whatsapp.sales', enabled: true })
  })

  it('an enable without a body stays bodyless and still returns the enabled response', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => enabledResponse())

    const result = await enableChannel('whatsapp.sales')

    expect(fetchSpy).toHaveBeenCalledExactlyOnceWith('/api/v1/channels/whatsapp.sales/enable', {
      method: 'PUT',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': 'channel-enable-test' },
    })
    expect(result).toEqual({ id: 'whatsapp.sales', enabled: true })
  })

  it('an explicit empty body is sent unchanged and never gains a pairing claim', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => enabledResponse())

    const result = await enableChannel('whatsapp.sales', {})

    expect(fetchSpy).toHaveBeenCalledExactlyOnceWith('/api/v1/channels/whatsapp.sales/enable', {
      method: 'PUT',
      body: '{}',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': 'channel-enable-test' },
    })
    expect(result).toEqual({ id: 'whatsapp.sales', enabled: true })
  })
})
