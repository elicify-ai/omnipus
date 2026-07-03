import { beforeEach, describe, expect, it } from 'vitest'

import type { WhatsAppPairingFrame } from '@/lib/api/generated/asyncapi-types'
import { useWhatsAppPairingStore } from '@/store/whatsappPairing'

function frame(over: Partial<WhatsAppPairingFrame>): WhatsAppPairingFrame {
  return {
    type: 'whatsapp_pairing',
    channel_id: 'whatsapp.sales',
    status: 'code',
    ...over,
  }
}

describe('whatsappPairing store (#283)', () => {
  beforeEach(() => {
    useWhatsAppPairingStore.setState({ byChannel: {} })
  })

  it('records a QR code frame keyed by channel_id', () => {
    useWhatsAppPairingStore.getState().apply(frame({ status: 'code', qr: 'QR-PAYLOAD-1' }))
    const s = useWhatsAppPairingStore.getState().byChannel['whatsapp.sales']
    expect(s).toEqual({ status: 'code', qr: 'QR-PAYLOAD-1', message: '' })
  })

  it('overwrites with the latest status (code -> linked) and clears qr', () => {
    const store = useWhatsAppPairingStore.getState()
    store.apply(frame({ status: 'code', qr: 'QR-1' }))
    store.apply(frame({ status: 'linked' }))
    expect(useWhatsAppPairingStore.getState().byChannel['whatsapp.sales']).toEqual({
      status: 'linked',
      qr: '',
      message: '',
    })
  })

  it('carries the message on error status', () => {
    useWhatsAppPairingStore.getState().apply(frame({ status: 'error', message: 'boom' }))
    expect(useWhatsAppPairingStore.getState().byChannel['whatsapp.sales'].message).toBe('boom')
  })

  it('keeps channels independent and clear() removes only one', () => {
    const store = useWhatsAppPairingStore.getState()
    store.apply(frame({ channel_id: 'whatsapp.sales', status: 'code', qr: 'A' }))
    store.apply(frame({ channel_id: 'other', status: 'waiting' }))
    store.clear('whatsapp.sales')
    const byChannel = useWhatsAppPairingStore.getState().byChannel
    expect(byChannel['whatsapp.sales']).toBeUndefined()
    expect(byChannel['other']).toBeDefined()
  })
})
